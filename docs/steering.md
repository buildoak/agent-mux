# Steering

agent-mux steering is unified under `internal/steer`. That package owns both delivery channels:

- `Delivery.InboxDir`: the dispatch artifact directory, where durable inbox messages live in `inbox.md`
- `Delivery.FIFOPath`: the optional `stdin.pipe` named pipe used for live Codex soft steering on FIFO-capable platforms

`--signal` and inbox fallback use the durable inbox path. `steer nudge` and `steer redirect` try the FIFO first when a live Codex run is ready; otherwise they fall back to the same inbox/resume path.

## Signal System

### --signal Flag

```bash
agent-mux --signal <dispatch_id> "Focus on the parser module only"
```

Writes a message to the running dispatch's inbox and returns a JSON ack. The ack confirms the inbox write succeeded; it does not confirm the worker has consumed the message yet.

### Inbox Mechanics

`CreateInbox` creates `inbox.md` at dispatch start.

`WriteInbox` opens `inbox.md` with `O_WRONLY|O_APPEND|O_CREATE`, takes `LOCK_EX` with `syscall.Flock`, and appends one NDJSON-encoded `InboxMessage`. Each record carries the message text plus an RFC3339Nano UTC timestamp.

`HasMessages` is a fast size check on `inbox.md`. The engine polls it in three places:

1. after every harness stdout line in `scanHarnessOutput`
2. on the 250ms inbox ticker
3. on the 5-second watchdog ticker

That dual path keeps steering responsive even when a harness is temporarily quiet.

`ReadInbox` opens `inbox.md` with `O_RDWR`, takes `LOCK_EX`, reads all content, truncates the file to zero, and parses the queued messages. It accepts the current NDJSON format and still reads the legacy `\n---\n` block format for backward compatibility.

### Resume Flow

When queued inbox messages are ready and the adapter supports resume, `LoopEngine`:

1. emits `coordinator_inject` for the queued message
2. gracefully stops the current harness process
3. re-invokes the harness through the adapter's native resume protocol (`ResumeArgs`)

The harness owns conversation state. agent-mux only manages delivery and process lifecycle.

For inbox fallback from `steer nudge` and `steer redirect`, the CLI writes `[NUDGE]` or `[REDIRECT]` prefixes into the inbox. The engine reformats those into the same coordinator phrasing used for live FIFO injection before resuming.

## Steer Commands

`agent-mux steer <dispatch_id> <action> [args]` provides structured control actions.

Live observation is separate: use `agent-mux status [--json] <dispatch_id>` for one-off liveness checks and `agent-mux wait <dispatch_id>` for completion.

### steer abort

```bash
agent-mux steer 01JQXYZ abort
```

Kills the worker process. It tries `SIGTERM` to `host.pid` first. If there is no live host PID, it falls back to writing `control.json` with `abort: true` for the watchdog to pick up.

### steer nudge

```bash
agent-mux steer 01JQXYZ nudge
agent-mux steer 01JQXYZ nudge "Please summarize what you have so far"
```

Sends a wrap-up message. Default message: "Please wrap up your current work and provide a final summary."

On a live Codex run with a ready FIFO bridge, agent-mux writes a soft-steer envelope to `stdin.pipe`. Otherwise it falls back to inbox delivery with a `[NUDGE]` prefix.

### steer redirect

```bash
agent-mux steer 01JQXYZ redirect "focus on the tests, skip the refactor"
```

Redirects the worker with new instructions. On a live Codex run with a ready FIFO bridge, agent-mux writes a soft-steer envelope to `stdin.pipe`. Otherwise it falls back to inbox delivery with a `[REDIRECT]` prefix. The instructions argument is required.

### Argument Order

Both orderings are accepted:

```bash
agent-mux steer 01JQXYZ nudge "message"   # canonical: <id> <action>
agent-mux steer nudge 01JQXYZ "message"   # reversed: <action> <id> — auto-detected and swapped
```

## FIFO Soft Steering For Codex

> **Note (codex-cli 0.121+):** Soft-steer for codex dispatches is currently
> unavailable. codex-cli 0.121 (upstream PR
> [openai/codex#15917](https://github.com/openai/codex/pull/15917)) drains
> stdin to EOF before processing the prompt, making the stdin-based
> steering channel unusable. agent-mux now skips the `stdin.pipe` FIFO
> bridge for codex dispatches, reports `stdin_pipe_ready: false` in
> `status.json`, and `agent-mux steer <id> nudge|redirect` automatically
> falls back to inbox + session-resume. Only `steer abort` still works
> against codex via its normal SIGTERM / `control.json` paths. A future
> version of agent-mux will wire `<artifact_dir>/inbox.md` as a proper
> out-of-band steering channel for live codex sessions; until then, use
> inbox-driven resume.

On FIFO-capable platforms, `LoopEngine.Dispatch` creates `stdin.pipe` inside the artifact directory and keeps a read end plus keepalive writer open for the run lifetime **for non-codex engines**. `agent-mux steer <id> nudge|redirect` resolves the artifact directory from the durable dispatch metadata and uses FIFO injection only when all of these are true:

- dispatch metadata says the engine is `codex` *(currently disabled — see note above)*
- the platform supports FIFOs
- `status.json` reports `state: "running"`
- `status.json` reports `stdin_pipe_ready: true`
- `host.pid` exists and is still alive

If any check fails, or FIFO open/write returns readiness errors such as missing path, no reader, or broken pipe, the CLI falls back to inbox delivery.

The FIFO payload is one JSON envelope with `action` and `message`. The loop's soft-stdin bridge decodes it, emits `coordinator_inject`, and writes formatted text directly into the live Codex stdin pipe. If a tool or command is active, delivery is deferred until it completes; once `max_steer_wait_seconds` is exceeded, the loop force-proceeds instead of deferring forever.

## Live Status

```bash
agent-mux status --json 01JQXYZ
```

Reads live status from `status.json` in the artifact directory. It can detect orphaned processes where the host PID is dead but the state is still recorded as `running`.

For soft steering, the important field is:

- `stdin_pipe_ready`: true when the dispatch-local `stdin.pipe` FIFO bridge is live and safe for `steer nudge` / `steer redirect`

## Output Format

All steer commands output a JSON ack:

```json
{
  "action": "redirect",
  "dispatch_id": "01JQXYZ...",
  "mechanism": "stdin_fifo",
  "delivered": true
}
```

`mechanism` reports how the steer action was delivered:

- `stdin_fifo`: live Codex soft steering through `stdin.pipe`
- `inbox`: inbox fallback for non-Codex, unsupported platforms, not-ready runs, or failed FIFO delivery
- `sigterm`: `steer abort` delivered through `SIGTERM`
- `control_file`: `steer abort` fallback through `control.json`

Errors follow the standard envelope: `{"kind":"error","error":{...}}`.

## Cross-References

- [Architecture](./architecture.md) for package boundaries and concurrency
- [Recovery](./recovery.md) for durable dispatch metadata and `--recover`
- [Async](./async.md) for background dispatch and status.json
- [Dispatch](./dispatch.md) for the `DispatchResult` contract
- [Engines](./engines.md) for per-adapter resume support
