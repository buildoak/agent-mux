---
date: 2026-03-29
status: draft
engine: claude
---

# Streaming Protocol v2: Silent Defaults, Async Dispatch, Mid-Flight Steering

## Problem

When an LLM coordinator dispatches via `ax run`, stdout+stderr merge into its context. 96.4% of output lines are streaming telemetry (heartbeats, tool_start/end, command_run) -- ~9,250 wasted tokens across 5 dispatches. The coordinator is blind during foreground dispatch and cannot act on mid-flight events.

## Architecture: Three Tiers

### Tier 1 -- Silent Result (build now)

**Change:** stderr silent by default. Only emit bookend events and failures.

Events emitted to stderr:
- `dispatch_start` (one line)
- `dispatch_end` (one line)
- `error`, `frozen_warning`, `frozen_killed`, `timeout_warning` (failure path)

Events suppressed from stderr but still written to `events.jsonl`:
- `heartbeat`, `tool_start`, `tool_end`, `file_write`, `file_read`, `command_run`, `progress`, `turn_complete`, `info`, `warning`, `coordinator_inject`

CLI surface:
- `--stream` restores current behavior (all events to stderr). For terminal humans.
- `--verbose` unchanged (raw harness lines, implies `--stream`).
- Default: silent. No flag needed.

The frozen watchdog is in-process -- it reads from the signal channel, not stderr. Silence on stderr does not affect liveness detection.

### Tier 2 -- Async Dispatch with Pull-Based Status (build next)

**Change:** decouple dispatch from result collection. Coordinator controls when output enters its context.

CLI surface:
- `ax run --async ...` returns immediately with:
  ```json
  {"schema_version":1,"kind":"async_started","dispatch_id":"01KN...","salt":"coral-hawk-three","artifact_dir":"/tmp/agent-mux-501/01KN..."}
  ```
- `ax status <id> --json` returns one-line activity summary:
  ```json
  {"dispatch_id":"01KN...","state":"running","elapsed_s":47,"last_activity":"tool: Bash","tools_used":3,"files_changed":1}
  ```
  State values: `running`, `completed`, `failed`, `timed_out`.
- `ax result <id>` returns the dispatch result JSON (blocks if still running, or returns immediately if done). `--no-wait` returns error if not done yet.

Implementation:
- `--async` forks the dispatch loop into a background goroutine. The CLI process stays alive as the host -- it owns the child process, watchdog, and event log. Stdout emits the async_started ack and the process detaches from the calling shell's stdout/stderr.
- `status.json` written atomically to artifact dir on each heartbeat tick. Same data as `ax status --json`. Any observer can poll this file -- no CLI required.
- `ax status` reads `status.json` from the artifact dir resolved via dispatch ID. If the file is missing, falls back to the store record (dispatch already finished).
- `ax result` reads from the store. If dispatch is still running (no store record, `status.json` shows `running`), blocks with a 1s poll loop on `status.json` until state changes. `--no-wait` skips the poll.
- Process lifecycle: the `ax run --async` process must stay alive to host the child. It writes its own PID to `host.pid` in the artifact dir. `ax steer <id> abort` sends SIGTERM to the host PID.

### Tier 3 -- Structured Progress Milestones (park)

Workers emit logical phase markers. Agent-mux infers phases from event patterns or parses explicit markers. `ax status` returns current phase. Parked until Tier 2 is in production and we have data on what phases matter.

## Mid-Flight Steering Protocol

### Steering Actions

| Action | Semantics | Mechanism |
|--------|-----------|-----------|
| `abort` | Kill worker, return partial result | SIGTERM to host process (async) or context cancel (foreground) |
| `nudge` | "Wrap up now" -- soft pressure | Inbox write: wrap-up message. Worker sees it on next inbox poll (250ms). |
| `redirect` | "Focus on X, skip Y" | Inbox write: free-form instruction. Triggers resume cycle if adapter supports it. |
| `extend` | Postpone watchdog kill | Write `control.json` with `{"extend_kill_seconds": N}`. Watchdog reads it. |
| `status` | What are you doing? | Read `status.json`. No process interaction needed. |

### The Inbox is the Steering Channel

The inbox mechanism (`inbox.md` in artifact dir) already exists and works. It is polled every 250ms by the dispatch loop. Messages trigger the resume cycle: stop current run, restart harness with `--resume <session_id> --continue <message>`. This is the right steering channel for `nudge` and `redirect` because:

1. It is file-based -- any process can write to it, cross-session.
2. It is already integrated with the watchdog loop.
3. It handles concurrent writes via flock.
4. It triggers the resume cycle which is the only way to inject mid-flight instructions into Claude and Gemini (they don't accept stdin).

### Control File: `control.json`

New file in artifact dir. Atomically overwritten. Read by the watchdog on each tick (5s).

```json
{
  "extend_kill_seconds": 360,
  "updated_at": "2026-03-29T14:00:00Z"
}
```

The watchdog reads `control.json` on each tick. If `extend_kill_seconds` is set and `updated_at` is within the last 60s, it overrides `silence_kill_seconds` for this dispatch. This prevents the "I told it to take longer" problem -- manual extension beats automatic kill.

Only `extend` uses `control.json`. All other steering goes through the inbox. One file for reads (status.json), one for writes (control.json), one for messages (inbox.md). Clean separation.

### CLI Surface

```
ax steer <dispatch_id> abort
ax steer <dispatch_id> nudge ["optional message"]
ax steer <dispatch_id> redirect "focus on the tests, skip the refactor"
ax steer <dispatch_id> extend 300
ax steer <dispatch_id> status
```

`ax steer <id> status` is sugar for `ax status <id> --json`. The rest resolve the artifact dir and write to inbox or control.json.

For foreground dispatches: `ax steer` works cross-session. The coordinator is blocked on stdout, but another terminal/session can steer. This is the key insight -- steering is always cross-process.

### Per-Engine Feasibility

**Codex:** Best support. Stdin pipe is live (`StdinNudge` returns `"\n"`). The resume cycle works (`exec resume <thread_id> <message>`). Nudge can use either stdin (fast, no restart) or inbox (triggers resume with richer context). Recommendation: stdin for nudge, inbox for redirect.

**Claude Code:** No stdin nudge (`StdinNudge` returns nil). Resume works (`--resume <session_id> --continue <message>`). All steering goes through inbox -> resume cycle. This means every steer action except abort/extend/status costs a harness restart. Acceptable -- Claude sessions resume fast.

**Gemini:** Same as Claude. No stdin, resume works (`--resume <session_id> -p <message>`). All steering via inbox -> resume.

### Steering vs Watchdog Interaction

The watchdog currently has two stages:
1. Warn at `silence_warn_seconds` (90s default): emit `frozen_warning`, send stdin nudge (Codex only)
2. Kill at `silence_kill_seconds` (180s default): emit `frozen_killed`, terminate process

Steering overrides:
- `ax steer <id> extend N` sets `control.json`. Watchdog reads it and uses `max(silence_kill_seconds, extend_kill_seconds)` as the effective kill threshold.
- `ax steer <id> nudge` writes to inbox. If the worker is frozen, the inbox poll still fires (it runs on the 250ms ticker, independent of harness output). The resume cycle restarts the harness, which resets `lastActivity` and `frozenWarned`. The watchdog resets.
- `ax steer <id> abort` bypasses the watchdog entirely -- sends SIGTERM to host PID.

The invariant: manual steering always takes precedence over automatic watchdog actions. If a human or coordinator says "keep going," the watchdog respects it.

## What to Build, In Order

**Phase 1 (Tier 1 -- days):**
1. Add `--stream` flag to main.go. Default stderr to bookend+failure events only.
2. Suppress `heartbeat`, `tool_*`, `file_*`, `command_run`, `progress` from stderr unless `--stream`.
3. `--verbose` implies `--stream`.

**Phase 2 (Tier 2 -- week):**
1. `--async` flag: fork dispatch to background goroutine, emit async_started, detach stdio.
2. `status.json` written by heartbeat ticker in loop.go.
3. `ax status <id>` reads status.json or store record.
4. `ax result <id> --no-wait` flag.
5. `host.pid` written at async start.

**Phase 3 (Steering -- after Tier 2 stabilizes):**
1. `ax steer` subcommand with abort, nudge, redirect, extend, status.
2. `control.json` read in watchdog tick.
3. Wire `ax steer nudge/redirect` to inbox.WriteInbox.
4. Wire `ax steer abort` to SIGTERM on host.pid.

## Edge Cases and Failure Modes

**Stale `status.json`:** Host process dies without cleanup. status.json shows `running` but process is dead. Mitigation: `ax status` checks `host.pid` -- if PID is dead, report `orphaned` and fall back to store record.

**Inbox write after process exit:** Message written to inbox but harness already exited. The dispatch loop drains the inbox on exit. If the message arrives after drain, it's orphaned. Acceptable -- the coordinator gets the result and knows the dispatch ended.

**Concurrent steerers:** Two coordinators steer the same dispatch. Inbox handles this via flock -- messages are serialized. control.json uses atomic rename -- last writer wins. Both are safe.

**Async dispatch and machine restart:** Host process dies. status.json is stale. `ax status` detects dead PID, reports orphaned. `ax result` returns whatever was persisted. Partial work is in the artifact dir. This is the same failure mode as a foreground dispatch killed by SIGTERM -- no new risk.

**Resume cycle failure during steer:** Inbox message triggers resume, but harness fails to restart. The existing `resume_start_failed` error path handles this -- dispatch enters `failed` state, result is built from whatever was collected. The steerer sees `failed` on next `ax status`.

## Relation to Existing Backlog

- **F-9 (`--quiet`):** Superseded by Tier 1. `--quiet` becomes the default; `--stream` is the opt-in. F-9 can be closed when Tier 1 ships.
- **F-10 (pipeline gates):** Tier 2 async + steering enables a coordinator to gate pipelines manually. F-10's deterministic shell gates remain valuable for automated pipelines but are orthogonal.
- **F-3 (pipeline branching):** Unrelated. Steering is per-dispatch, not per-pipeline.
- **F-5 (daemon/JSON-RPC):** Tier 2 async is a lighter alternative. If async dispatch + file-based steering proves sufficient, F-5 stays parked permanently.
