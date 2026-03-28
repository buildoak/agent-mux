# Recovery and Signal Guide

## Contents

- Artifact directory layout
- Recovery workflow
- Signal workflow
- Control records
- Inbox mechanics
- Supervisor and liveness

---

## Artifact Directory Layout

Every dispatch gets an artifact directory. Default location:

```
/tmp/agent-mux-<uid>/<dispatch_id>/
```

If `$XDG_RUNTIME_DIR` is set, uses `$XDG_RUNTIME_DIR/agent-mux/` instead.
The per-UID suffix ensures artifact isolation between users.

Contents:

| File | Purpose |
|------|---------|
| `_dispatch_meta.json` | Dispatch metadata (ID, salt, engine, model, status, timestamps) |
| `events.jsonl` | NDJSON event log (mirrored from stderr) |
| `inbox.md` | Coordinator mailbox for signal injection |
| `full_output.md` | Full response when truncation occurred |
| (worker files) | Any files the worker created as artifacts |

### Pipeline Artifacts

Pipeline dispatches create a nested structure:

```
/tmp/agent-mux-<uid>/<dispatch_id>/pipeline/
  step-0/worker-0/
    _dispatch_meta.json
    events.jsonl
    output.md
  step-1/worker-0/
    ...
```

---

## Recovery Workflow

Recovery continues a timed-out or interrupted dispatch.

### How It Works

1. Caller provides `continues_dispatch_id` (JSON) or `--recover` (CLI)
2. v2 resolves the artifact directory via control record, then legacy fallback
3. v2 reads `_dispatch_meta.json` and scans for artifact files
4. v2 builds a continuation prompt that includes:
   - Original dispatch ID, engine, model
   - Previous status
   - List of existing artifacts
   - Caller's new prompt (as additional instruction)

### JSON Invocation

```json
{
  "engine": "codex",
  "continues_dispatch_id": "01KM...",
  "prompt": "Continue by finishing the remaining test cases",
  "cwd": "/repo"
}
```

### When to Use Recovery

- A prior dispatch timed out after writing useful artifacts
- A dispatch was interrupted (SIGINT/SIGTERM) mid-work
- You want to continue from where the worker left off

### Recovery Prompt Structure

v2 auto-generates:

```
You are continuing a previous dispatch (ID: 01KM...).
Engine: codex, Model: gpt-5.4
Previous status: timed_out.
Artifacts from previous run:
- /tmp/agent-mux-501/01KM.../src/parser.go
- /tmp/agent-mux-501/01KM.../notes.md

(Original prompt hash: sha256:... — re-read artifacts for context.)

Please continue from where the previous run left off.

[Your additional prompt here]
```

### Tips

- Your recovery prompt should be the delta, not a full re-brief
- Good: "Finish the remaining tests and summarize what's left"
- Bad: "Here's the entire project context again from scratch"

---

## Signal Workflow

Signals send steering messages to a running dispatch.

### How It Works

1. Caller provides dispatch ID and a message
2. v2 resolves the artifact directory via control record
3. v2 appends the message to `inbox.md` (atomic append with flock)
4. v2 returns a JSON acknowledgement
5. The running dispatch checks inbox at event boundaries
6. If the harness has a resumable session/thread ID, v2 gracefully stops the
   current process and resumes with the inbox message injected

### CLI Invocation

```bash
agent-mux --signal=01KM... "Focus on auth paths; skip tests"
```

### Signal Acknowledgement

```json
{
  "status": "ok",
  "dispatch_id": "01KM...",
  "artifact_dir": "/tmp/agent-mux-501/01KM...",
  "message": "Signal delivered to inbox"
}
```

### Important Caveats

- **Ack != delivery.** The ack confirms the inbox write succeeded. Actual
  injection happens later, at an event boundary, when the harness has emitted
  a resumable session/thread ID.
- **Not instant.** If the harness hasn't emitted a session ID yet, the inbox
  message waits until one is available.
- **Keep signals short.** They become a resumed turn inside the harness.
  Crisp steering commands, not multi-paragraph redesigns.

---

## Control Records

Control records map dispatch IDs to artifact directories.

Location: `/tmp/agent-mux-<uid>/control/<dispatch_id>.json`
(or `$XDG_RUNTIME_DIR/agent-mux/control/` if XDG is set)

```json
{
  "dispatch_id": "01KM...",
  "artifact_dir": "/tmp/agent-mux-501/01KM...",
  "dispatch_salt": "mint-ant-five",
  "trace_token": "AGENT_MUX_GO_01KM..."
}
```

Resolution order for `--recover` and `--signal`:

1. Control record at `/tmp/agent-mux-<uid>/control/<id>.json`
2. Legacy default directory at `/tmp/agent-mux-<uid>/<id>/`
3. Error if neither found

---

## Inbox Mechanics

The inbox is a file-based message queue at `<artifact_dir>/inbox.md`.

### Write Protocol

- Messages are appended with `\n---\n` delimiter
- File lock via `flock(LOCK_EX)` for atomicity
- Messages <= 4096 bytes are atomic on POSIX (PIPE_BUF guarantee)

### Read Protocol

- Reader acquires exclusive lock
- Reads all content and splits by delimiter
- Truncates file to zero (consume-and-clear)
- Returns slice of messages

### Check Protocol

- `HasMessages()` is a fast-path stat check (file size > 0)
- No locking — used for non-blocking polling at event boundaries

### Lifecycle

1. `CreateInbox()` at dispatch start (idempotent)
2. `WriteInbox()` from `--signal` callers
3. LoopEngine checks `HasMessages()` at each event boundary
4. On inbox content: `ReadInbox()` returns messages, clears file
5. Messages are injected via harness-native resume protocol

---

## Supervisor and Liveness

### Process Supervision

The supervisor manages the harness process lifecycle:

- Creates process groups (`Setpgid: true`) so grandchildren die with parent
- `GracefulStop()`: SIGTERM first, then SIGKILL after grace period
- Process group kill (`Kill(-pgid, ...)`) ensures all descendants terminate

### Liveness Watchdog

Monitors harness event stream for silence:

| Threshold | Action |
|-----------|--------|
| `silence_warn_seconds` (default 90s) | Emit `frozen_warning` event |
| `silence_kill_seconds` (default 180s) | Kill harness, return `frozen_tool_call` error |

The liveness watchdog runs as a goroutine that tracks time since last event.
Legitimate long operations (Rust builds, large installs) can trigger false
warnings — the kill threshold should be set high enough to accommodate these.

### Heartbeat

The heartbeat emitter runs on a configurable interval (default 15s) and emits
`heartbeat` events to the stderr NDJSON stream. Each heartbeat includes
elapsed time, interval, and last known activity string.

### Timeout Flow

1. Dispatch starts with `timeout_sec` (from role, effort, or explicit override)
2. At `timeout_sec`: soft timeout fires, emits `timeout_warning` event
3. Grace period begins (`grace_sec`, default 60s)
4. If harness exits cleanly during grace: status = `completed`
5. If grace expires: SIGTERM -> wait `grace_sec` -> SIGKILL
6. Status = `timed_out`, `partial = true`, `recoverable = true`

The design principle: kill the process, preserve the artifacts. Written files
persist at the artifact path regardless of timeout.
