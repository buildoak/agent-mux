# Recovery Guide

Recovery flow, runtime layout, inbox mechanics, and watchdog behavior.

---

## Runtime Layout

Every dispatch has:

- a runtime artifact directory
- a durable store entry under `~/.agent-mux/dispatches/<id>/`

### Artifact directory

| File | Purpose |
|------|---------|
| `_dispatch_ref.json` | Thin pointer to the durable store |
| `events.jsonl` | Full NDJSON event log |
| `status.json` | Live status snapshot |
| `host.pid` | PID of the async host process |
| `control.json` | Abort requests |
| `inbox.md` | NDJSON coordinator inbox |
| `stdin.pipe` | Unix FIFO only when a soft-stdin bridge is created; current Codex and agy runs skip it |
| worker files | Any artifacts written by the worker |

`_dispatch_ref.json` replaces `_dispatch_meta.json` as the runtime control
record in the artifact dir.

### Durable store

The only durable persistence location is:

`~/.agent-mux/dispatches/<dispatch_id>/`

Files:

| File | Purpose |
|------|---------|
| `meta.json` | persistent dispatch metadata |
| `result.json` | persistent dispatch result |

Lifecycle commands (`list`, `status`, `result`, `inspect`, `wait`) use this
store as their source of truth.

---

## Recovery Workflow

Recovery lives in `internal/dispatch/recovery.go`.

Use `--recover <id>` or `"recover": "<id>"` in stdin JSON.

### Flow

1. resolve the artifact directory via `ResolveArtifactDir`, which checks durable metadata first:
   - persistent meta's `artifact_dir` (first priority)
   - current secure artifact root
   - legacy `/tmp/agent-mux/<id>` (fallback)
2. read dispatch metadata from the artifact dir; `ReadDispatchMeta` uses
   `_dispatch_ref.json` first and falls back to legacy `_dispatch_meta.json`
3. if artifact meta fails, recovery falls back to durable `meta.json`
4. scan the artifact directory for worker-written files
5. build a continuation prompt with dispatch ID, engine, model, prior status, artifact list, and prompt hash
6. run a new dispatch with that continuation prompt prepended

The added recovery prompt already says "continue from where the previous run
left off." Your prompt should only state what remains.

### When recovery is appropriate

- the prior run timed out after writing useful artifacts
- the dispatch was interrupted mid-work
- you want a continuation, not a restart

---

## Dispatch ID Resolution

Lifecycle commands accept a full dispatch ID or a unique prefix.

Resolution is driven from `~/.agent-mux/dispatches/`:

1. search dispatch directories by prefix
2. error if more than one dispatch matches
3. use the matching dispatch's durable metadata to find the artifact dir

---

## Inbox Mechanics

`inbox.md` is a file-backed coordinator mailbox managed by `internal/steer`.

### Write path

- open `inbox.md` with append/create
- take `flock(LOCK_EX)`
- append one NDJSON message with timestamp

### Read path

- open `inbox.md` read-write
- take `flock(LOCK_EX)`
- read all messages
- truncate the file to zero
- return the parsed message list

### Fast path

`HasMessages()` uses a stat check on file size without locking.

### Where the loop checks inbox messages

The loop checks for pending inbox messages:

1. after harness output is scanned
2. on the `250ms` inbox ticker
3. on the `5s` watchdog ticker

That is why steer and `--signal` are not tied to a single polling path.

---

## Liveness

The global dispatch timeout (`timeout_sec` + `grace_sec`) is the hard backstop.
There is no automatic silence-based kill. Use `agent-mux steer <id> abort` for manual
kill when a worker appears stuck.

### Heartbeats

Heartbeat interval default: `15s`.

### Soft timeout flow

1. at `timeout_sec`, emit `timeout_warning`
2. write a wrap-up message to the inbox telling the worker to write final artifacts to `$AGENT_MUX_ARTIFACT_DIR`
3. start the grace timer
4. if grace expires, stop the worker and return `timed_out`

### Worker Diagnostics

For silent-worker triage, see `worker-diagnostics.md`.

---

## Steering and Recovery Interaction

Soft steering is unified under `internal/steer`:

- **Codex**: nudge/redirect currently falls back to inbox + resume because the
  loop disables child-stdin soft steering
- **Claude/Gemini**: inbox delivery triggers session resume/restart via
  `ResumeArgs()` — the loop restarts the harness with the pending inbox
  messages as the resume prompt

If a steer message arrives while a tool is still active, agent-mux defers
the resume/restart until the tool finishes. If a tool remains active past
`engine_opts.max_steer_wait_seconds` (default 120s), the steer is
force-delivered.

---

## Failure Decision Tree

```text
status?
 timed_out + files_changed non-empty
   -> --recover=<dispatch_id> with a continuation prompt
 timed_out + files_changed empty
   -> prompt too broad; tighten scope and retry once
 failed + error.retryable
   -> fix the cause and retry once
 failed + not retryable
   -> structural failure; escalate
 second failure on the same step
   -> stop and reframe the work
```
