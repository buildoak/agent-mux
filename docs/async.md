# Async Dispatch

agent-mux supports fire-and-forget dispatch via `--async`. The CLI returns immediately with a dispatch ID; the worker runs in the background. Results are collected later with `wait` or `result`.

This is the escape hatch for callers that cannot block on a long-running dispatch. The tradeoff is explicit: you give up real-time event visibility in exchange for immediate control return.

## --async Flag

When `--async` is present, agent-mux:

1. Starts the dispatch in a background goroutine
2. Redirects the worker's stdout and stderr to `/dev/null`
3. Returns an `async_started` JSON ack to stdout immediately

The calling process exits after printing the ack. The worker continues in the same OS process until completion or timeout.

## async_started Response

```json
{
  "schema_version": 1,
  "kind": "async_started",
  "dispatch_id": "01JQXYZ...",
  "artifact_dir": "/path/to/artifact/dir/01JQXYZ..."
}
```

The `dispatch_id` is the handle for all subsequent operations: `wait`, `result`, `status`, `inspect`, `steer`.

The `artifact_dir` is guaranteed to exist and contain `host.pid` and `status.json` before the ack is emitted. Consumers can begin polling immediately after receiving the ack.

## Collecting Results

### agent-mux wait

Blocks until an async dispatch reaches a terminal state. Reads `status.json` from the artifact directory on a polling loop.

```bash
agent-mux wait 01JQXYZ --poll 5s
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--poll` | `60s` | Check interval |
| `--config` | standard | Config resolution for poll_interval fallback |
| `--cwd` | current dir | Working directory for config lookup |

Poll interval precedence: CLI `--poll` flag > `[async].poll_interval` in config.toml > hardcoded default (60s).

Prints the final `DispatchResult` JSON to stdout on completion.

### agent-mux result

Retrieves the dispatch response. If the dispatch is still running, blocks until completion by default.

```bash
agent-mux result 01JQXYZ          # blocks until done, prints response
agent-mux result 01JQXYZ --no-wait  # returns error if still running
agent-mux result --artifacts 01JQXYZ  # lists artifact files instead
```

Falls back to `full_output.md` in the artifact directory for truncated or legacy dispatches.

## status.json

The artifact directory contains a `status.json` file updated throughout the run. This is the primary observability path for programmatic callers that do not want to parse the event stream.

Fields:

| Field | Type | Description |
| --- | --- | --- |
| `state` | string | `running`, `completed`, `failed`, `timed_out`, `orphaned` |
| `elapsed_s` | int | Seconds since dispatch start |
| `last_activity` | string | Description of the most recent activity |
| `tools_used` | int | Number of tool calls observed |
| `files_changed` | int | Number of file writes observed |
| `dispatch_id` | string | Dispatch identifier |
| `session_id` | string | Harness session ID when established |

`orphaned` state is set by `agent-mux status` when the host PID recorded in `host.pid` is no longer alive but the dispatch was never marked terminal.

Completion is defined by `result.json` appearing under `~/.agent-mux/dispatches/<dispatch_id>/`, not by `status.json` alone. `agent-mux wait` polls for `result.json` on each interval tick.

## Streaming Protocol v2

### Default: Silent Mode

Only bookend events (`dispatch_start`, `dispatch_end`) and failure/error events are emitted to stderr. This keeps the CLI quiet for programmatic callers.

All events are still written to `events.jsonl` in the artifact directory for post-hoc inspection via `agent-mux inspect`.

### --stream / -S

Passing `--stream` restores full event streaming to stderr: heartbeats, tool_start, tool_end, file_write, progress, and all other event types. Useful for human debugging and real-time monitoring.

### Event Types

All 13+ event types carry a common envelope:

```json
{
  "schema_version": 1,
  "type": "<event_type>",
  "dispatch_id": "<ulid>",
  "ts": "2026-03-28T10:00:00Z"
}
```

| Type | Key Fields |
| --- | --- |
| `dispatch_start` | `engine`, `model`, `effort`, `timeout_sec`, `grace_sec`, `cwd`, `skills` |
| `dispatch_end` | `status`, `duration_ms` |
| `heartbeat` | `elapsed_s`, `interval_s`, `last_activity` |
| `tool_start` | `tool`, `args` |
| `tool_end` | `tool`, `duration_ms` |
| `file_write` | `path` |
| `file_read` | `path` |
| `command_run` | `command` |
| `progress` | `message` |
| `timeout_warning` | `message` |
| `frozen_warning` | `silence_seconds`, `message` |
| `long_command_detected` | `command`, `timeout_seconds`, `message` |
| `info` | `error_code` (as info code), `message` |
| `error` | `error_code`, `message` |
| `coordinator_inject` | `message` |

The `info` type carries diagnostic codes like `stdin_nudge`. Heartbeats are emitted every 15 seconds (configurable) and carry `last_activity` for liveness context.

## Cross-References

- [Architecture](./architecture.md) for the event emitter and supervision model
- [Dispatch](./dispatch.md) for the DispatchResult contract
- [Steering](./steering.md) for mid-flight control over async dispatches
- [Lifecycle](./lifecycle.md) for `list`, `status`, `inspect` commands
- [Recovery](./recovery.md) for artifact directory layout
