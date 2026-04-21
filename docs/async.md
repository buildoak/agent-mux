# Async Dispatch

`agent-mux --async` switches dispatch into an acknowledgement-first flow for long-running work.

## What `--async` Does

Before the async acknowledgement is written, agent-mux:

1. Ensures the artifact directory exists.
2. Registers the dispatch in the durable store at `~/.agent-mux/dispatches/<dispatch_id>/meta.json`.
3. Writes `<artifact_dir>/host.pid`.
4. Writes an initial `<artifact_dir>/status.json`.
5. Emits an `async_started` JSON object to stdout.

After that ack, stdout and stderr are detached to `/dev/null`, and the dispatch continues in the current process.

`--async` does not fork or daemonize a separate worker. If the caller needs shell control back immediately, it must background or supervise `agent-mux` itself.

## Lifecycle

`--async` means **detached**: the caller has declared that it will not remain alive for the duration of the dispatch.

Concretely, the parent-death reaper (kqueue `EVFILT_PROC+NOTE_EXIT` on darwin, `PR_SET_PDEATHSIG` on linux) is **not armed** on the async path. Under `agent-mux --async`, the caller's exit does NOT affect the worker. The worker survives until one of:

- its own completion (a `result.json` appears in the durable store),
- its soft/hard timeout (`--timeout` / `--grace`), or
- an explicit abort via `agent-mux steer abort <dispatch_id>`.

This matters for short-lived callers (schedulers, `tickets tick`, cron jobs, one-shot shell wrappers) that fire `--async` and exit seconds later. In the interactive case (`agent-mux ... &` in a long-lived shell), Ctrl+C on the shell still kills the worker because the signal propagates through the foreground process group, independent of the reaper.

Non-async dispatches (the default, synchronous path) continue to arm the reaper as an orphan guard: if the caller dies unexpectedly mid-dispatch, the worker's process group is SIGKILL'd.

## `async_started` Ack

```json
{
  "schema_version": 1,
  "kind": "async_started",
  "dispatch_id": "01K...",
  "artifact_dir": "/path/to/artifacts/01K.../"
}
```

At ack time, `host.pid` and `status.json` are already on disk. The artifact-local `_dispatch_ref.json` pointer is written later during normal dispatch setup; the authoritative metadata already lives in `~/.agent-mux/dispatches/<dispatch_id>/meta.json`.

## Runtime Files vs Durable Store

Runtime state lives in the artifact directory:

- `_dispatch_ref.json`
- `status.json`
- `host.pid` for async dispatches
- `events.jsonl`
- `inbox.md` and `control.json` when steering is used
- worker-created artifact files

Durable state lives only in:

```text
~/.agent-mux/dispatches/<dispatch_id>/
  meta.json
  result.json
```

`wait` and `result` treat `result.json` as the completion signal. `status.json` is live telemetry, not the durable completion record.

## `status.json`

Source of truth: `dispatch.LiveStatus` in `internal/dispatch/status.go`.

| Field | Type | Notes |
| --- | --- | --- |
| `state` | string | `running`, `completed`, `failed`, `timed_out`; `status` may also synthesize `orphaned` |
| `elapsed_s` | int | Seconds since dispatch start |
| `last_activity` | string | Most recent activity label |
| `tools_used` | int | Tool-call count |
| `files_changed` | int | File-write count |
| `stdin_pipe_ready` | bool | Present when Codex soft steering is available |
| `ts` | string | RFC3339 timestamp of the write |
| `dispatch_id` | string | Dispatch identifier when known |
| `session_id` | string | Harness session ID when known |

`agent-mux status` marks a live dispatch as `orphaned` when `host.pid` exists but the process is no longer alive.

## Collecting Results

Use the lifecycle commands after the ack:

- `agent-mux status <dispatch_id>` for current live or durable status
- `agent-mux result <dispatch_id>` for the stored response
- `agent-mux inspect <dispatch_id>` for response, artifacts, and metadata together
- `agent-mux wait <dispatch_id>` to block on `~/.agent-mux/dispatches/<dispatch_id>/result.json`

`agent-mux result` blocks by default if the dispatch is still running. `agent-mux wait` polls the persistent `result.json` path and emits progress lines to stderr between polls.

## Event Streaming

All structured events are appended to `<artifact_dir>/events.jsonl`.

stderr behavior depends on flags:

- default: only the silent-mode subset is written to stderr
- `--stream` or `-S`: all structured events are written to stderr
- `--verbose` or `-v`: all structured events plus raw harness lines are written to stderr

The shared event envelope is:

```json
{
  "schema_version": 1,
  "type": "<event_type>",
  "dispatch_id": "01K...",
  "ts": "2026-04-03T10:00:00Z"
}
```

Common event types include `dispatch_start`, `dispatch_end`, `heartbeat`, `tool_start`, `tool_end`, `file_write`, `file_read`, `command_run`, `progress`, `timeout_warning`, `info`, `warning`, `error`, and `coordinator_inject`.

## Cross-References

- [dispatch.md](./dispatch.md) for `DispatchSpec` and `DispatchResult`
- [lifecycle.md](./lifecycle.md) for `list`, `status`, `result`, `inspect`, and `wait`
- [recovery.md](./recovery.md) for `_dispatch_ref.json`, `meta.json`, and `result.json`
