# Async Dispatch and Mid-Flight Steering

Async launch, live status, result collection, and steering for running dispatches.

---

## Async Dispatch

### --async ack

`--async` writes runtime control files, emits an ack on `stdout`, detaches
stdio, then runs the dispatch synchronously in the current process. It does
NOT daemonize. The caller is expected to background the process (shell `&`,
`run_in_background`, etc.) if true background execution is needed.

### Lifecycle (detached semantics)

`--async` means **detached**: the parent-death reaper is NOT armed on the
async path. The caller's exit does not affect the worker. This fixed the
short-lived-scheduler regression where callers (e.g. `tickets tick`) would
dispatch async and exit within ~30s, causing the worker's process group to
receive SIGKILL and die at ~22ms with `killed_by_user`.

Under `--async`, the worker survives until one of:

- its own completion (durable `result.json` appears),
- its soft/hard timeout (`--timeout` / `--grace`), or
- an explicit abort via `agent-mux steer abort <dispatch_id>`.

Non-async (synchronous) dispatches still arm the reaper as an orphan guard.
Interactive shell use (`agent-mux ... &`) is unaffected: Ctrl+C still kills
the worker via the foreground process group, independent of the reaper.
See agent-mux `docs/async.md` "Lifecycle" for the full rationale.

```json
{
  "schema_version": 1,
  "kind": "async_started",
  "dispatch_id": "01K...",
  "artifact_dir": "/tmp/agent-mux-501/01K.../"
}
```

Before the ack is emitted:

- `~/.agent-mux/dispatches/<id>/meta.json` has been written (via `RegisterDispatchSpec`)
- `<artifact_dir>/host.pid` exists and is fsynced
- `<artifact_dir>/status.json` exists (state `running`, last_activity `initializing`)

NOT guaranteed before ack:

- `<artifact_dir>/_dispatch_ref.json` — written later during engine startup
  in `internal/engine/loop.go`. Do not read it immediately after the ack.

`_dispatch_ref.json` is a thin pointer to the durable store, but it is not
ack-gated. Consumers that run immediately after ack should use the ack fields or
durable `~/.agent-mux/dispatches/<id>/meta.json`.

### Fan-out and startup latency

`--async` writes the ack before engine startup, but the `agent-mux` process
keeps running until dispatch completion unless the caller backgrounds it. The
ack is emitted after artifact dir creation, durable meta registration, fsynced
`host.pid`, and initial `status.json`; it should not wait for harness init.
Sequential fan-out without shell backgrounding still serializes those ack waits.

Use shell `&` around each `agent-mux --async` invocation to parallelize startup,
then `agent-mux wait` to synchronize on completion:

```bash
# Fan-out: shell & backgrounds each agent-mux process
for svc in auth billing orders; do
  { agent-mux --async -P=scout -C="/repo/$svc" "Audit $svc" 2>/dev/null | jq -r .dispatch_id > "/tmp/$svc.id"; } &
done
wait  # all acks received, all workers running concurrently
# Synchronize:
for svc in auth billing orders; do
  agent-mux wait "$(cat /tmp/$svc.id)"
done
```

This is the recommended pattern for any batch fan-out because `--async` does not
daemonize by itself.

### Collecting results

`wait` is the completion primitive:

```bash
agent-mux wait 01K... 2>/dev/null
agent-mux wait --poll 30s 01K... 2>/dev/null
agent-mux wait --json 01K... 2>/dev/null
```

- `wait` blocks until `result.json` exists
- stderr gets periodic status lines while waiting
- `wait --json` matches `result --json` only after `result.json` exists
- before `result.json`, timeout and dead-host paths emit structured errors
- only an explicit live `orphaned` state emits raw `LiveStatus` and exits 1

`result` reads the durable result:

```bash
agent-mux result 01K... 2>/dev/null
agent-mux result --json 01K... 2>/dev/null
agent-mux result --no-wait 01K... 2>/dev/null
agent-mux result --artifacts 01K... 2>/dev/null
```

- plain `result` prints the stored response text
- `--json` prints a compact lifecycle JSON object
- `--artifacts` lists non-internal files in the artifact dir
- `--no-wait` errors if the dispatch is still running
- if no persisted `result.json` exists, `result` falls back to reading
  `full_output.md` from the artifact directory (legacy compatibility)

Current source of truth: `~/.agent-mux/dispatches/<id>/result.json`.

### Poll interval precedence

```
CLI --poll
  > config.DefaultAsyncPollInterval (60s)
```

No env or TOML config is read for the current poll default.

### status.json

The initial `<artifact_dir>/status.json` is written before async ack. Running
updates happen on the 5-second watchdog tick and when session start is
persisted.

| Field | Type | Meaning |
|-------|------|---------|
| `state` | string | `running`, `completed`, `failed`, `timed_out` |
| `elapsed_s` | int | Seconds since start |
| `last_activity` | string | Most recent activity summary |
| `tools_used` | int | Tool-call count seen so far |
| `files_changed` | int | File-write count seen so far |
| `stdin_pipe_ready` | bool | true only when a soft-stdin bridge is active; current Codex and agy runs keep it false |
| `ts` | string | RFC3339 timestamp |
| `dispatch_id` | string | Dispatch ID |
| `session_id` | string | Harness session ID (available once engine emits init event) |

Note: `state` values are `running`, `completed`, `failed`, `timed_out`. The
initial write sets `state: "running"` with `last_activity: "initializing"` —
there is no `"initializing"` state value.

`agent-mux status` may synthesize `orphaned` if `host.pid` exists but the
process is no longer alive.

`session_id` is captured when the engine emits its init event. Durable
`meta.json` gets `session_id` via `UpdatePersistentMetaSessionID`; `status.json`
is also refreshed when the session is observed, then again on watchdog ticks.

---

## Steering

Steering is unified under `internal/steer`. `steer.Delivery` owns both
soft-delivery channels:

- inbox file in the artifact dir
- FIFO named pipe at `<artifact_dir>/stdin.pipe` on Unix

`agent-mux steer <dispatch_id> <action> [args]` accepts a full dispatch ID or
a unique prefix. Both argument orderings work: `steer <id> <action>` and
`steer <action> <id>`.

### Steering delivery by engine

| Engine | Primary mechanism | Behavior |
|--------|-------------------|----------|
| Codex | Inbox + `codex exec resume` | FIFO disabled because child stdin is an EOF reader; `stdin_pipe_ready` is false, so CLI routes to inbox |
| Claude | Inbox + resume/restart through `ResumeArgs()` | Loop restarts harness when inbox messages are pending |
| Gemini | Inbox + resume/restart through `ResumeArgs()` | Same resume/restart pattern as Claude |

> **Note (codex-cli 0.121+):** Codex FIFO delivery is disabled. `steer
> nudge|redirect` falls back to inbox + `codex exec resume`, which can only be
> delivered after the current turn reaches a safe boundary or the run exits.

For Claude and Gemini, steering is NOT passive inbox delivery — it actively
resumes/restarts the session with the steering message. If a tool is currently
executing, the restart is deferred until the tool completes (or until
`engine_opts.max_steer_wait_seconds` is exceeded, whichever comes first).

### steer abort

Try SIGTERM against `host.pid`. If there is no live host PID, fall back to
`control.json`.

```bash
agent-mux steer 01K... abort
```

Possible JSON responses:

```json
{"action":"abort","dispatch_id":"01K...","mechanism":"sigterm","pid":12345,"delivered":true}
```

```json
{"action":"abort","dispatch_id":"01K...","mechanism":"control_file","delivered":true}
```

### steer nudge

Send a wrap-up message. Default message:

`Please wrap up your current work and provide a final summary.`

```bash
agent-mux steer 01K... nudge
agent-mux steer 01K... nudge "Summarize what you have so far"
```

Delivery order:

1. `stdin_fifo` only when `status.stdin_pipe_ready=true` and host PID is live
2. inbox fallback for everything else

Current Codex runs keep `stdin_pipe_ready=false`, so nudge uses inbox.

Inbox fallback writes `[NUDGE] <message>`.

### steer redirect

Send a new instruction set. Argument is required.

```bash
agent-mux steer 01K... redirect "Focus on tests; skip the refactor"
```

Delivery order is the same as `nudge`.

Inbox fallback writes `[REDIRECT] <message>`.

Typical JSON response:

```json
{"action":"redirect","dispatch_id":"01K...","mechanism":"inbox","delivered":true}
```

### Steering mechanisms

| Mechanism | When used |
|-----------|-----------|
| `stdin_fifo` | Live runs with `stdin_pipe_ready=true`; current Codex does not enable it |
| `inbox` | Fallback path for `nudge` and `redirect`; triggers resume/restart for Codex, Claude, and Gemini |
| `sigterm` | `abort` when host PID is alive |
| `control_file` | `abort` fallback |

---

## Signal Flag

`--signal` is a convenience write to the inbox:

```bash
agent-mux --signal 01K... "Focus on auth paths only" 2>/dev/null
```

Ack shape:

```json
{
  "status": "ok",
  "dispatch_id": "01K...",
  "artifact_dir": "/tmp/agent-mux-501/01K.../",
  "message": "Signal delivered to inbox"
}
```

Ack means the message was written to the inbox, not that the worker has already
consumed it.

---

## Streaming

Default stderr mode is silent except for bookend and failure-family events. All
structured events are always appended to `<artifact_dir>/events.jsonl`.

- `--stream` / `-S`: emit the full NDJSON event stream to stderr
- `--verbose` / `-v`: include raw harness lines as well
