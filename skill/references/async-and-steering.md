# Async Dispatch and Mid-Flight Steering

Fire-and-forget dispatch, result collection, and live control over running workers.

---

## Async Dispatch

### --async flag

When `--async` is present, agent-mux starts the dispatch in a background
goroutine and returns immediately with an ack:

```json
{
  "schema_version": 1,
  "kind": "async_started",
  "dispatch_id": "01JQXYZ...",
  "salt": "coral-fox-nine",
  "artifact_dir": "/tmp/agent-mux/01JQXYZ..."
}
```

The calling process exits after printing the ack. The worker continues in the
same OS process. The `dispatch_id` is the handle for all subsequent operations.

### Collecting results

**wait** — block until terminal state:
```bash
agent-mux wait 01JQXYZ              # default poll 60s
agent-mux wait 01JQXYZ --poll 5s    # faster polling
```

Prints the final `DispatchResult` JSON to stdout on completion.

**result** — retrieve the response:
```bash
agent-mux result 01JQXYZ            # blocks until done
agent-mux result 01JQXYZ --no-wait  # error if still running
agent-mux result --artifacts 01JQXYZ  # list artifact files
```

Falls back to `full_output.md` for truncated or legacy dispatches.

### Poll interval precedence

```
CLI --poll flag > config.toml [async].poll_interval > 60s hardcoded default
```

### status.json

The artifact directory contains `status.json`, updated on each event boundary:

| Field | Type | Description |
|-------|------|-------------|
| `state` | string | `running`, `completed`, `failed` |
| `elapsed_s` | int | Seconds since dispatch start |
| `last_activity` | string | Most recent activity description |
| `tool_count` | int | Tool calls observed |
| `files_changed_count` | int | File writes observed |

---

## Steering Commands

`agent-mux steer <dispatch_id> <action> [args]` provides structured control
over running dispatches.

### steer status

Read live status from `status.json`. Detects orphaned processes.

```bash
agent-mux steer 01JQXYZ status
```

### steer abort

Kill the worker process. Sends SIGTERM to host PID (async) or writes
`control.json` with `abort: true` (foreground).

```bash
agent-mux steer 01JQXYZ abort
```

### steer nudge

Send a wrap-up message. Live Codex workers on Unix may receive this through the
dispatch-local `stdin.pipe` FIFO; other cases fall back to inbox delivery.
Default: "Please wrap up your current work and provide a final summary."

```bash
agent-mux steer 01JQXYZ nudge
agent-mux steer 01JQXYZ nudge "Summarize what you have so far"
```

### steer redirect

Redirect the worker with new instructions. Argument is required.

```bash
agent-mux steer 01JQXYZ redirect "Focus on the tests, skip the refactor"
```

The steer ack JSON includes a `mechanism` field. For Codex soft steering this
is `stdin_fifo`; inbox fallback reports `inbox`.

### steer extend

Extend the watchdog kill threshold. Writes `control.json` with
`extend_kill_seconds`. Useful for legitimate long-running operations.

```bash
agent-mux steer 01JQXYZ extend 300
```

### Output format

All steer commands return JSON:

```json
{
  "action": "redirect",
  "dispatch_id": "01JQXYZ...",
  "delivered": true
}
```

Errors follow standard envelope: `{"kind":"error","error":{...}}`.

---

## Signal System (Legacy)

`--signal` delivers a message to a running dispatch's inbox:

```bash
agent-mux --signal 01JQXYZ "Focus on auth paths; skip tests"
```

### How it works

1. Message appended to `inbox.md` (atomic with flock)
2. JSON ack returned confirming write
3. LoopEngine checks inbox at event boundaries
4. If harness has a resumable session ID: graceful stop, then resume with
   message injected into the conversation

### Important caveats

- **Ack != delivery.** Ack confirms inbox write, not worker consumption.
- **Not instant.** Waits for an event boundary and a resumable session ID.
- **Keep signals short.** They become a resumed turn. Crisp commands, not
  multi-paragraph redesigns.

---

## Streaming Protocol v2

### Default: silent mode

Only bookend events (`dispatch_start`, `dispatch_end`) and failure events
are emitted to stderr. All events still written to `events.jsonl`.

### --stream / -S

Restores full event streaming to stderr: heartbeats, tool activity, progress.
Useful for human debugging and real-time monitoring.

### Event types

All events carry: `schema_version`, `type`, `dispatch_id`, `salt`,
`trace_token`, `ts`.

| Type | Key fields |
|------|------------|
| `dispatch_start` | `engine`, `model`, `effort`, `timeout_sec` |
| `dispatch_end` | `status`, `duration_ms` |
| `heartbeat` | `elapsed_s`, `interval_s`, `last_activity` |
| `tool_start` / `tool_end` | `tool`, `args` / `duration_ms` |
| `file_write` / `file_read` | `path` |
| `command_run` | `command` |
| `progress` | `message` |
| `timeout_warning` | `message` |
| `frozen_warning` | `silence_seconds`, `message` |
| `coordinator_inject` | `message` |
| `error` | `error_code`, `message` |

Heartbeats emit every 15s (configurable). `--verbose` adds raw harness lines
but breaks pure NDJSON parsing.
