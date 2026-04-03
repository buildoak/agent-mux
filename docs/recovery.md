# Recovery

agent-mux preserves work across timeout, process death, and cancellation. Recovery is the system that makes interrupted dispatches resumable and partial results observable.

The core invariant is artifact-first: the artifact directory exists before the harness starts, metadata is written throughout the run, and result assembly scans artifacts regardless of how the process terminated.

## Artifact Directory Layout

Every dispatch gets an artifact directory. The default root is resolved by `sanitize.SecureArtifactRoot()` (commonly a subdirectory under the user home or `/tmp/agent-mux/`). Override with `--artifact-dir`.

```
<artifact_dir>/
  status.json            # live dispatch state (state, elapsed_s, last_activity, tool counts)
  host.pid               # PID of the dispatching process (async only)
  events.jsonl           # one JSON object per harness event
  full_output.md         # full streamed output text (fallback for legacy dispatches)
  inbox.md               # pending signal messages
  <worker artifacts>     # files written by the harness
```

`events.jsonl` is appended throughout the run. `full_output.md` accumulates the worker's streamed text when a result cannot be stored in the durable store. `inbox.md` holds pending steering messages.

## Durable Persistence

All dispatches write durable records under `~/.agent-mux/dispatches/<dispatch_id>/`:

```
~/.agent-mux/dispatches/<dispatch_id>/
  meta.json     # dispatch metadata (engine, model, role, cwd, artifact_dir, started_at)
  result.json   # full DispatchResult once the run completes
```

`meta.json` is written at dispatch start and updated with the session ID once the harness establishes one. `result.json` is written atomically (tmp file + fsync + rename) when the dispatch reaches a terminal state. Completion is defined by the presence of `result.json` — `wait` polls for this file.

`agent-mux list` reads all `~/.agent-mux/dispatches/` subdirectories, building records from both `meta.json` (for in-flight dispatches) and `result.json` (for completed ones).

## Recovery Flow

`--recover <dispatch_id>` continues a previous dispatch using the `recover` key:

1. **ResolveArtifactDir** — reads `meta.json` from `~/.agent-mux/dispatches/<dispatch_id>/` to find the artifact directory. Falls back to the default artifact path if no durable record exists, then the legacy `/tmp/agent-mux/<dispatch_id>/` path.
2. **RecoverDispatch** — reads `dispatch_meta.json` from the artifact directory (or synthesizes from `meta.json`) and scans artifacts to reconstruct dispatch state.
3. **BuildRecoveryPrompt** — constructs a continuation prompt containing: dispatch ID, engine, model, previous terminal status, artifact file paths, and original prompt hash.
4. **Re-dispatch** — the recovery prompt replaces `spec.Prompt` and dispatch runs through the normal path.

The recovery prompt gives the new worker enough context to pick up where the previous one stopped without re-reading the entire original prompt.

## Timeout System

### Effort-to-Timeout Mapping

| Effort | Default Timeout |
| --- | --- |
| `low` | 120s (2 min) |
| `medium` | 600s (10 min) |
| `high` | 1800s (30 min) — also the fallback for unknown strings |
| `xhigh` | 2700s (45 min) |

Priority chain (highest wins): step-level `timeout` > role-level `timeout` > `TimeoutForEffort(effort)`.

### Two-Phase Timeout

**Phase 1 — Soft timeout:**

1. Emit `timeout_warning` event
2. Write wrap-up message to inbox: "Soft timeout reached. Wrap up your current work..."
3. Start grace timer (default 60s from `[timeout].grace`)

**Phase 2 — Hard timeout:**

1. Set terminal state to `timed_out`
2. `GracefulStop(spec.GraceSec)` — SIGTERM to process group, then SIGKILL after `grace_sec` seconds (minimum 10s). `grace_sec` is sourced from the dispatch spec, role, or `[timeout].grace` config key.
3. Drain remaining events from scanner

If the harness exits cleanly during the grace period (it read the inbox and wrapped up on its own), the result routes to the normal success path — `completed`, not `timed_out`.

## Partial Result Preservation

On any terminal state, the result captures:

- `Response`: `lastResponse` text, or `lastProgressText` if response is empty. This applies to all terminal states including `failed` — accumulated response is never discarded on the error path.
- `Artifacts`: output of `ScanArtifacts()` walking the artifact directory
- `Partial: true` when the worker was interrupted
- `Recoverable: true` when the dispatch can be continued with `--recover`

Store records are written atomically (tmp file + fsync + rename) before the terminal status event is emitted. If the store write fails, a warning is logged and the response is persisted to `full_output.md` in the artifact directory as a fallback.

## Liveness Watchdog

The watchdog ticker fires every 5 seconds. `lastActivity` is reset on every parsed harness event.

| Threshold | Default | Action |
| --- | --- | --- |
| `silence_warn_seconds` | 90s | Emit `frozen_warning` event. Send stdin nudge if adapter supports it. Set `frozenWarned` flag. |
| `silence_kill_seconds` | 180s | Emit `error` with code `frozen_tool_call`. `GracefulStop(5)`. Result: `failed`. |
| `long_command_silence_seconds` | 540s (9 min) | Extended kill threshold for known long-running commands. |

### Long-Command Protection

Build tools like `cargo`, `make`, `nvcc`, `go build`, `go test`, `cmake`, `npm install`, `npm run build`, `pip install`, `docker build`, `rustc`, `gcc`, `g++`, and `clang` can run for minutes without producing harness events.

The watchdog tracks the active command via `EventToolStart`/`EventCommandRun` (set) and `EventToolEnd` (clear). When the active command matches a known prefix, the effective kill threshold becomes `long_command_silence_seconds` (default 540s). A `long_command_detected` event is emitted once per command for observability. When `EventToolEnd` fires, the normal `silence_kill_seconds` threshold resumes immediately.

Custom prefixes can be added via `engine_opts["long_command_prefixes"]` (comma-separated string).

### Stdin Nudge

At the warn threshold, the LoopEngine calls `adapter.StdinNudge()`. If the adapter returns non-nil bytes (Codex returns `"\n"`), those bytes are written to the process's stdin pipe. This gives the harness a chance to recover from a frozen state before the hard kill at `silence_kill_seconds`. An `info` event with code `stdin_nudge` is emitted on successful write. Claude and Gemini return `nil` (no stdin nudge).

All liveness thresholds are also readable from `spec.EngineOpts` for per-dispatch tuning.

## Supervisor

`supervisor.Process` wraps `*exec.Cmd` with process-group awareness:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
```

The child process gets its own process group. `signalGroup` sends signals to `-pgid`, killing the entire tree atomically. `ESRCH` and "already exited" errors are silently swallowed.

### Graceful Shutdown Sequence

`GracefulStop(graceSec)`:

1. `SIGTERM` to process group
2. Wait up to `graceSec` seconds
3. `SIGKILL` to process group if still alive
4. Block on `Wait()` to reap zombie

Two goroutines per run: scanner (stdout line reader) and waiter (`proc.Wait()` result).

## Cross-References

- [Architecture](./architecture.md) for the concurrency model and package map
- [Dispatch](./dispatch.md) for the DispatchResult contract
- [Engines](./engines.md) for per-adapter resume support and StdinNudge behavior
- [Steering](./steering.md) for inbox mechanics and mid-flight control
- [Async](./async.md) for status.json and background dispatch
