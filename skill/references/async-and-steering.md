# Async Dispatch and Mid-Flight Steering

Async launch, live status, result collection, and steering for running dispatches.

---

## Async Dispatch

### --async ack

`--async` writes runtime control files, emits an ack on `stdout`, detaches
stdio, then runs the dispatch synchronously in the current process. It does
NOT daemonize. The caller is expected to background the process (shell `&`,
`run_in_background`, etc.) if true background execution is needed.

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

`_dispatch_ref.json` is a thin pointer to the durable store:

```json
{
  "dispatch_id": "01K...",
  "store_dir": "/Users/alice/.agent-mux/dispatches/01K..."
}
```

The durable store is always `~/.agent-mux/dispatches/<id>/`.

### Fan-out and startup latency

`--async` returns control after the ack, but the ack itself is not
instantaneous — the engine must initialize before emitting it. Measured
startup latency: Codex ~15-20s, Claude and Gemini vary. Sequential fan-out
(dispatching N workers one after another in a loop) pays this cost serially:
5 Codex workers means ~80-100s of blocked waiting before any worker is
running.

Use shell `&` to parallelize the startup cost across all workers, then
`agent-mux wait` to synchronize on completion:

```bash
# Fan-out: shell & parallelizes engine startup
for svc in auth billing orders; do
  { agent-mux --async -P=scout -C="/repo/$svc" "Audit $svc" 2>/dev/null | jq -r .dispatch_id > "/tmp/$svc.id"; } &
done
wait  # all acks received, all workers running concurrently
# Synchronize:
for svc in auth billing orders; do
  agent-mux wait "$(cat /tmp/$svc.id)"
done
```

This is the recommended pattern for any batch fan-out. Workers DO run
concurrently once started — the only serial cost is startup.

### Collecting results

`wait` is the completion primitive:

```bash
agent-mux wait 01K... 2>/dev/null
agent-mux wait --poll 30s 01K... 2>/dev/null
agent-mux wait --json 01K... 2>/dev/null
```

- `wait` blocks until `result.json` exists
- stderr gets periodic status lines while waiting
- `wait --json` normally prints the same compact lifecycle JSON shape as
  `result --json`
- **Exception:** if the dispatch is orphaned (dead `host.pid`, no result),
  `wait --json` emits raw `LiveStatus` JSON and exits nonzero

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
  > 60s hardcoded default
```

### status.json

`<artifact_dir>/status.json` is updated on the 5-second watchdog tick, not on
every event. For near-immediate session ID visibility, read `meta.json` instead.

| Field | Type | Meaning |
|-------|------|---------|
| `state` | string | `running`, `completed`, `failed`, `timed_out` |
| `elapsed_s` | int | Seconds since start |
| `last_activity` | string | Most recent activity summary |
| `tools_used` | int | Tool-call count seen so far |
| `files_changed` | int | File-write count seen so far |
| `stdin_pipe_ready` | bool | Codex stdin FIFO bridge is ready |
| `ts` | string | RFC3339 timestamp |
| `dispatch_id` | string | Dispatch ID |
| `session_id` | string | Harness session ID (available once engine emits init event) |

Note: `state` values are `running`, `completed`, `failed`, `timed_out`. The
initial write sets `state: "running"` with `last_activity: "initializing"` —
there is no `"initializing"` state value.

`agent-mux status` may synthesize `orphaned` if `host.pid` exists but the
process is no longer alive.

`session_id` is captured early — as soon as the engine emits its init event.
However, it appears in `status.json` only on the next 5s watchdog tick.
`meta.json` updates immediately when the session ID is first seen. For async
dispatches, prefer reading `meta.json` for early session ID access.

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
| Codex | Inbox + session resume (via `codex exec resume`) | Soft-steer via FIFO is currently **disabled** for codex ≥0.121 — see note below. `stdin_pipe_ready` is always `false` for codex, so CLI routes to inbox automatically. |
| Claude | Inbox + session resume | Loop restarts harness with `ResumeArgs()` when inbox messages are pending |
| Gemini | Inbox + session resume | Same resume/restart pattern as Claude |

> **Note (codex-cli 0.121+):** Soft-steer for codex dispatches is currently
> unavailable. codex-cli 0.121+ drains stdin to EOF before processing the
> prompt (upstream PR openai/codex#15917), making the stdin-based steering
> channel unusable. A future version of agent-mux will wire
> `/tmp/agent-mux-501/{dispatch_id}/inbox.md` as a proper out-of-band
> steering channel for codex; until then, only `steer abort` works against
> running codex dispatches with immediate effect. `steer nudge|redirect`
> still works via inbox + `codex exec resume` (session-restart) but requires
> the current turn to end first.

For Claude and Gemini, steering is NOT passive inbox delivery — it actively
resumes/restarts the session with the steering message. If a tool is currently
executing, the restart is deferred until the tool completes (or until
`max_steer_wait_seconds` is exceeded, whichever comes first).

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

1. Codex stdin FIFO when the worker is running and `stdin_pipe_ready=true`
2. inbox fallback for everything else (triggers resume for Claude/Gemini)

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
| `stdin_fifo` | Codex only, running state, stdin bridge ready, host PID alive |
| `inbox` | Fallback path for `nudge` and `redirect`; triggers resume for Claude/Gemini |
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
