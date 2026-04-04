# Output Contract

JSON schemas for dispatch results, preview, async ack, lifecycle commands, and
structured errors.

---

## DispatchResult JSON

Normal dispatch writes one `DispatchResult` object to `stdout`.

```json
{
  "schema_version": 1,
  "status": "completed",
  "dispatch_id": "01KM...",
  "response": "Worker response text",
  "response_truncated": false,
  "full_output": null,
  "handoff_summary": "Short summary for handoff",
  "artifacts": ["/tmp/agent-mux-501/01KM.../notes.md"],
  "activity": {
    "files_changed": ["src/parser.go"],
    "files_read": ["src/types.go"],
    "commands_run": ["go test ./..."],
    "tool_calls": ["Edit", "Bash"]
  },
  "metadata": {
    "engine": "codex",
    "model": "gpt-5.4",
    "role": "lifter",
    "profile": "planner",
    "skills": ["agent-mux"],
    "tokens": {
      "input": 1234,
      "output": 567,
      "reasoning": 89
    },
    "turns": 3,
    "cost_usd": 0,
    "session_id": "thread_..."
  },
  "duration_ms": 84231
}
```

### Status values

| `status` | Meaning |
|----------|---------|
| `completed` | Worker exited cleanly |
| `timed_out` | Soft timeout fired, grace expired, worker was stopped |
| `failed` | Validation error, startup problem, hook denial, watchdog kill, or adapter failure |

### Top-level fields

| Field | Type | Notes |
|-------|------|-------|
| `schema_version` | int | Always `1` |
| `status` | string | `completed`, `timed_out`, `failed` |
| `dispatch_id` | string | Dispatch ID |
| `response` | string | Final response text |
| `response_truncated` | bool | Compatibility field; current result path keeps the full response inline |
| `full_output` | string/null | Compatibility field; currently `null` in normal results |
| `full_output_path` | string/null | Deprecated compatibility stub; omitted or `null` |
| `handoff_summary` | string | Extracted from `## Summary` or `## Handoff`, else truncated response text |
| `artifacts` | string[] | Non-internal files from the artifact dir |
| `partial` | bool | Present on timed-out runs |
| `recoverable` | bool | Present on timed-out runs |
| `reason` | string | Reason for non-completed status |
| `error` | object/null | Present on failed runs |
| `activity` | object | Files, commands, and tools observed |
| `metadata` | object | Engine, model, tokens, session info |
| `duration_ms` | int64 | End-to-end duration |

### DispatchError

Present when `status` is `failed`.

```json
{
  "code": "engine_not_found",
  "message": "Engine \"bogus\" not found.",
  "hint": "Valid engines: [codex, claude, gemini]",
  "example": "",
  "retryable": true,
  "partial_artifacts": []
}
```

| Field | Type | Notes |
|-------|------|-------|
| `code` | string | Machine-readable error code |
| `message` | string | Human-readable message |
| `hint` | string | Suggested corrective action |
| `example` | string | Example invocation when available |
| `retryable` | bool | Whether retry may succeed |
| `partial_artifacts` | string[] | Files written before failure |

### DispatchActivity

| Field | Type |
|-------|------|
| `files_changed` | string[] |
| `files_read` | string[] |
| `commands_run` | string[] |
| `tool_calls` | string[] |

### DispatchMetadata

| Field | Type | Notes |
|-------|------|-------|
| `engine` | string | Engine used |
| `model` | string | Model used |
| `role` | string | Role name |
| `profile` | string | Profile name |
| `skills` | string[] | Injected skills |
| `tokens` | object | Token usage |
| `turns` | int | Conversation turns |
| `cost_usd` | float | Estimated cost |
| `session_id` | string | Harness session ID |

There is no `variant` field in current metadata.

### TokenUsage

| Field | Type | Notes |
|-------|------|-------|
| `input` | int | Input tokens |
| `output` | int | Output tokens |
| `reasoning` | int | Codex reasoning tokens when available |
| `cache_read` | int | Claude cache-read tokens when available |
| `cache_write` | int | Claude cache-write tokens when available |

---

## Async Ack

When `--async` is set, stdout receives:

```json
{
  "schema_version": 1,
  "kind": "async_started",
  "dispatch_id": "01KMY...",
  "artifact_dir": "/tmp/agent-mux-501/01KMY.../"
}
```

By the time this ack is emitted:

- `~/.agent-mux/dispatches/<id>/meta.json` already exists (via `RegisterDispatchSpec`)
- `host.pid` is on disk and fsynced
- `status.json` is on disk (state `running`, last_activity `initializing`)

NOT guaranteed before ack:

- `_dispatch_ref.json` — written later during engine startup

---

## Preview Output

`agent-mux preview` returns the resolved dispatch shape without executing.

```json
{
  "schema_version": 1,
  "kind": "preview",
  "dispatch_spec": {
    "dispatch_id": "01KM...",
    "engine": "codex",
    "model": "gpt-5.4",
    "effort": "high",
    "cwd": "/repo",
    "context_file": "/tmp/brief.md",
    "artifact_dir": "/tmp/agent-mux-501/01KM.../",
    "timeout_sec": 1800,
    "grace_sec": 60,
    "max_depth": 2,
    "depth": 0,
    "full_access": true
  },
  "result_metadata": {
    "role": "lifter",
    "profile": "planner",
    "skills": ["agent-mux"]
  },
  "prompt": {
    "excerpt": "First 280 chars of prompt...",
    "chars": 1500,
    "truncated": true,
    "system_prompt_chars": 200
  },
  "control": {
    "control_record": "/Users/alice/.agent-mux/dispatches/01KM.../meta.json",
    "artifact_dir": "/tmp/agent-mux-501/01KM.../"
  },
  "prompt_preamble": [
    "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting.",
    "If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR."
  ],
  "warnings": [],
  "confirmation_required": false
}
```

Notes:

- `control.control_record` points at the durable `meta.json`
- `prompt_preamble` is derived from `context_file` and `artifact_dir`
- `warnings` is currently emitted as an empty array

---

## Persistent Store Shapes

All durable records live in `~/.agent-mux/dispatches/<id>/`.

### meta.json

Persistent dispatch metadata is written to `meta.json`.

```json
{
  "dispatch_id": "01KM...",
  "session_id": "thread_...",
  "engine": "codex",
  "model": "gpt-5.4",
  "effort": "high",
  "role": "lifter",
  "profile": "planner",
  "cwd": "/repo",
  "artifact_dir": "/tmp/agent-mux-501/01KM.../",
  "started_at": "2026-04-03T10:30:00Z",
  "timeout_sec": 1800,
  "prompt_hash": "sha256:deadbeefcafebabe"
}
```

### result.json

`result.json` stores the `DispatchResult` plus a small persistence envelope:

| Field | Type |
|-------|------|
| `started_at` | string |
| `ended_at` | string |
| `artifact_dir` | string |
| `cwd` | string |
| `engine` | string |
| `model` | string |
| `role` | string |
| `profile` | string |
| `effort` | string |
| `session_id` | string |
| `response_chars` | int |
| `timeout_sec` | int |

---

## Lifecycle JSON

### DispatchRecord

`list --json` emits one `DispatchRecord` per line. `status --json` may return
either a `DispatchRecord` or a live `status.json` view.

```json
{
  "id": "01KM...",
  "session_id": "thread_...",
  "status": "completed",
  "engine": "codex",
  "model": "gpt-5.4",
  "role": "lifter",
  "started": "2026-04-03T10:30:00Z",
  "ended": "2026-04-03T10:45:00Z",
  "duration_ms": 900000,
  "cwd": "/repo",
  "truncated": false,
  "response_chars": 1234,
  "artifact_dir": "/tmp/agent-mux-501/01KM.../",
  "effort": "high",
  "profile": "planner",
  "timeout_sec": 1800
}
```

`truncated` currently mirrors the compatibility truncation field and is
normally `false`.

### result --json and wait --json

Compact lifecycle JSON:

```json
{
  "dispatch_id": "01KM...",
  "session_id": "thread_...",
  "response": "Worker response text",
  "status": "completed"
}
```

Possible extra fields:

- `kill_reason`: added for failed runs when agent-mux can identify a
  kill-related event (frozen_killed, signal_killed, etc.) from `events.jsonl`
  or status metadata. Present only when `status` is `failed`.
- `session_id`: harness session ID when available

### result --json --artifacts

```json
{
  "dispatch_id": "01KM...",
  "artifact_dir": "/tmp/agent-mux-501/01KM.../",
  "artifacts": ["/tmp/agent-mux-501/01KM.../notes.md"]
}
```

### inspect --json

```json
{
  "dispatch_id": "01KM...",
  "session_id": "thread_...",
  "record": { "...": "DispatchRecord" },
  "response": "Worker response text",
  "artifact_dir": "/tmp/agent-mux-501/01KM.../",
  "artifacts": ["/tmp/agent-mux-501/01KM.../notes.md"],
  "meta": { "...": "DispatchMeta" }
}
```

`meta` is included when agent-mux can resolve dispatch metadata from the
artifact dir via `_dispatch_ref.json`.

---

## Live Status

`<artifact_dir>/status.json` is the pull-based live status file.

```json
{
  "state": "running",
  "elapsed_s": 45,
  "last_activity": "tool:Edit",
  "tools_used": 12,
  "files_changed": 3,
  "stdin_pipe_ready": true,
  "ts": "2026-04-03T10:30:45Z",
  "dispatch_id": "01KMY...",
  "session_id": "thread_..."
}
```

| Field | Type | Notes |
|-------|------|-------|
| `state` | string | `running`, `completed`, `failed`, `timed_out` |
| `elapsed_s` | int | Seconds since start |
| `last_activity` | string | Most recent activity summary |
| `tools_used` | int | Tool-call count |
| `files_changed` | int | File-write count |
| `stdin_pipe_ready` | bool | Codex stdin FIFO bridge ready |
| `ts` | string | Timestamp of this status snapshot |
| `dispatch_id` | string | Dispatch ID |
| `session_id` | string | Harness session ID |

`status` may report `orphaned` when it reads `status.json` and finds a dead
`host.pid`.

---

## Signal Ack

`agent-mux --signal <id> "message"` returns:

```json
{
  "status": "ok",
  "dispatch_id": "01KM...",
  "artifact_dir": "/tmp/agent-mux-501/01KM.../",
  "message": "Signal delivered to inbox"
}
```

Failure shape:

```json
{
  "status": "error",
  "dispatch_id": "01KM...",
  "message": "...",
  "error": {
    "code": "recovery_failed",
    "message": "...",
    "hint": "...",
    "example": "",
    "retryable": false
  }
}
```

---

## Events

`events.jsonl` is NDJSON. Event records always include:

- `schema_version`
- `type`
- `dispatch_id`
- `ts`

Common event types:

| Type | Key fields |
|------|------------|
| `dispatch_start` | `engine`, `model`, `effort`, `timeout_sec`, `grace_sec`, `cwd` |
| `dispatch_end` | `status`, `duration_ms` |
| `heartbeat` | `elapsed_s`, `interval_s`, `last_activity` |
| `tool_start` / `tool_end` | `tool`, `args`, `duration_ms` |
| `file_write` / `file_read` | `path` |
| `command_run` | `command` |
| `progress` | `message` |
| `timeout_warning` | `message` |
| `frozen_warning` | `silence_seconds`, `message` |
| `long_command_detected` | `command`, `timeout_seconds`, `message` |
| `error` | `error_code`, `message` |
| `info` | `error_code`, `message` |
| `coordinator_inject` | `message` |
| `warning` | `error_code`, `message` |
| `steer_deferred` / `steer_forced` | `message`, `command` |

`response_truncated` remains in the event schema for compatibility but is
currently inactive because response truncation is a deprecated stub.

Default stderr mode is quiet; use `--stream` for the full event stream.

---

## Error Codes

| Code | Retryable | Meaning |
|------|-----------|---------|
| `abort_requested` | no | Dispatch aborted via `control.json` |
| `artifact_dir_unwritable` | no | Cannot create or write artifact/persistence paths |
| `binary_not_found` | no | Harness binary not found on PATH |
| `cancelled` | no | Dispatch cancelled at confirmation |
| `config_error` | yes | Config, role, or control-path problem |
| `engine_not_found` | yes | Unknown engine |
| `event_denied` | no | Hook denied a harness event |
| `frozen_killed` | yes | Worker killed after prolonged silence |
| `internal_error` | no | Internal invariant failure |
| `interrupted` | no | Context cancelled or signal received |
| `invalid_args` | yes | Invalid arguments |
| `invalid_input` | yes | Input validation failed |
| `max_depth_exceeded` | no | Recursive dispatch limit reached |
| `model_not_found` | yes | Unknown model for engine |
| `output_parse_error` | no | Failed to parse streaming harness output |
| `parse_error` | no | Malformed final harness output |
| `process_killed` | no | Generic killed-process fallback |
| `prompt_denied` | no | `pre_dispatch` hook blocked launch |
| `recovery_failed` | yes | Previous dispatch state could not be recovered |
| `resume_session_missing` | no | No resumable session ID available |
| `resume_start_failed` | yes | Resume process failed to start |
| `resume_unsupported` | no | Adapter does not support resume |
| `signal_killed` | no | Harness killed by OS signal |
| `startup_failed` | yes | Harness process failed to start |
