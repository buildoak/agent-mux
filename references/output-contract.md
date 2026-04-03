# Output Contract

## Contents

- Single dispatch JSON
- Preview output
- Async ack
- Live status (status.json)
- Control-path responses
- stderr event stream
- Lifecycle subcommand JSON
- Error codes

---

All dispatch results use `schema_version: 1`. Control-path responses
(`--signal`, `--version`) are simpler and do not include `schema_version`.

## Single Dispatch JSON

Source of truth: `types.DispatchResult` in `internal/types/types.go`.

Normal dispatch writes one JSON object to `stdout`:

```json
{
  "schema_version": 1,
  "status": "completed",
  "dispatch_id": "01KM...",
  "response": "Worker response text",
  "response_truncated": false,
  "full_output": null,
  "full_output_path": null,
  "handoff_summary": "Short summary for handoff",
  "artifacts": ["/tmp/agent-mux-501/01KM.../notes.md"],
  "partial": false,
  "recoverable": false,
  "reason": "",
  "error": null,
  "activity": {
    "files_changed": [],
    "files_read": [],
    "commands_run": [],
    "tool_calls": []
  },
  "metadata": {
    "engine": "codex",
    "model": "gpt-5.4",
    "role": "lifter",
    "variant": "",
    "profile": "",
    "skills": ["agent-mux"],
    "tokens": {
      "input": 1234,
      "output": 567,
      "reasoning": 89,
      "cache_read": 0,
      "cache_write": 0
    },
    "turns": 3,
    "cost_usd": 0,
    "session_id": "thread_..."
  },
  "duration_ms": 84231
}
```

### Status Values

| `status` | Meaning |
|----------|---------|
| `completed` | Worker exited cleanly (including clean exit during grace window) |
| `timed_out` | Soft timeout fired, grace expired, harness was stopped |
| `failed` | Validation error, startup problem, adapter failure, or policy denial |

### Top-Level Fields

Source: `types.DispatchResult` struct tags.

| Field | Type | Notes |
|-------|------|-------|
| `schema_version` | int | Always `1` |
| `status` | string | `completed`, `timed_out`, `failed` |
| `dispatch_id` | string | ULID for this run |
| `response` | string | Final response text |
| `response_truncated` | bool | True when response was shortened and full body spilled to disk |
| `full_output` | string/null | Always `null` |
| `full_output_path` | string/null | Path to `full_output.md` when `response_truncated=true` (omitted when null) |
| `handoff_summary` | string | Extracted from `## Summary`/`## Handoff` or shortened response |
| `artifacts` | string[] | Files under artifact dir (excludes internal files) |
| `partial` | bool | Present on timed-out runs |
| `recoverable` | bool | Present on timed-out runs; currently always true |
| `reason` | string | Human explanation for timed-out runs |
| `error` | object/null | Present on failed runs (see below) |
| `activity` | object | Files/commands/tool calls observed |
| `metadata` | object | Engine, model, tokens, session info |
| `duration_ms` | int | End-to-end duration in milliseconds |

### Error Object

Source: `types.DispatchError` struct.

Present when `status` is `failed`:

```json
{
  "code": "binary_not_found",
  "message": "Binary \"codex\" not found on PATH.",
  "hint": "Install codex: see the engine documentation for installation instructions.",
  "example": "",
  "retryable": true,
  "partial_artifacts": []
}
```

| Field | Type | Notes |
|-------|------|-------|
| `code` | string | Machine-readable error code |
| `message` | string | Human-readable error description |
| `hint` | string | Guidance on how to fix the error |
| `example` | string | Example of correct usage (may be empty) |
| `retryable` | bool | Whether the error is transient |
| `partial_artifacts` | string[] | Artifact paths from partial work (may be empty) |

### Activity Object

Source: `types.DispatchActivity` struct.

| Field | Type | Notes |
|-------|------|-------|
| `files_changed` | string[] | Unique file paths written |
| `files_read` | string[] | Unique file paths read |
| `commands_run` | string[] | Unique shell commands observed |
| `tool_calls` | string[] | Tool names observed (not guaranteed unique) |

### Metadata Object

Source: `types.DispatchMetadata` struct.

| Field | Type | Notes |
|-------|------|-------|
| `engine` | string | Requested engine |
| `model` | string | Requested model (can be empty if harness default used) |
| `role` | string | Role name if dispatched via role |
| `variant` | string | Variant name if dispatched via variant |
| `profile` | string | Profile name if dispatched via profile |
| `skills` | string[] | Injected skill names |
| `tokens` | object | Best-effort token accounting |
| `turns` | int | Best-effort turn count |
| `cost_usd` | float | Currently zero-filled |
| `session_id` | string | Harness session/thread ID when available |

### Tokens Object

Source: `types.TokenUsage` struct.

| Field | Type | Notes |
|-------|------|-------|
| `input` | int | Input tokens |
| `output` | int | Output tokens |
| `reasoning` | int | Reasoning tokens (Codex) |
| `cache_read` | int | Cache read tokens (Claude) |
| `cache_write` | int | Cache write tokens (Claude) |

---

## Preview Output

`agent-mux preview` returns a `previewResult` without executing the dispatch.

Source: `previewResult` struct in `main.go`.

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
    "artifact_dir": "/tmp/agent-mux-501/01KM.../",
    "timeout_sec": 1800,
    "grace_sec": 60,
    "max_depth": 2,
    "depth": 0,
    "full_access": true
  },
  "result_metadata": {
    "role": "lifter",
    "variant": "",
    "profile": "",
    "skills": ["agent-mux"]
  },
  "prompt": {
    "excerpt": "Implement retries in ... client.ts",
    "chars": 245,
    "truncated": false,
    "system_prompt_chars": 0
  },
  "control": {
    "control_record": "~/.agent-mux/dispatches/01KM.../meta.json",
    "artifact_dir": "/tmp/agent-mux-501/01KM.../"
  },
  "prompt_preamble": [],
  "warnings": [],
  "confirmation_required": false
}
```

---

## Async Ack

When `--async` is set, the dispatch emits an async acknowledgement to stdout and then runs the worker in the background.

Source: `runAsyncDispatch` in `async.go`.

```json
{
  "schema_version": 1,
  "kind": "async_started",
  "dispatch_id": "01KM...",
  "artifact_dir": "/tmp/agent-mux-501/01KM.../"
}
```

After this ack, `host.pid` and `status.json` are guaranteed on-disk. Use `ax status`, `ax wait`, or `ax result` to track the dispatch.

---

## Live Status (status.json)

Source: `dispatch.LiveStatus` struct in `internal/dispatch/status.go`.

Written atomically to `<artifact_dir>/status.json` during dispatch execution and read by `ax status` and `ax wait`.

```json
{
  "state": "running",
  "elapsed_s": 42,
  "last_activity": "tool_call: Bash",
  "tools_used": 7,
  "files_changed": 3,
  "stdin_pipe_ready": true,
  "ts": "2026-03-28T10:00:42Z",
  "dispatch_id": "01KM...",
  "session_id": "thread_..."
}
```

| Field | Type | Notes |
|-------|------|-------|
| `state` | string | `running`, `initializing`, `completed`, `failed`, `timed_out`, `orphaned` |
| `elapsed_s` | int | Seconds since dispatch start |
| `last_activity` | string | Description of last observed activity |
| `tools_used` | int | Count of tool calls observed |
| `files_changed` | int | Count of files written |
| `stdin_pipe_ready` | bool | Whether stdin FIFO is available for steering (omitted when false) |
| `ts` | string | RFC3339 timestamp of this status write |
| `dispatch_id` | string | Dispatch identifier (omitted if empty) |
| `session_id` | string | Harness session/thread ID (omitted if empty) |

The `orphaned` state is set by `ax status` when `host.pid` exists but the process is dead.

---

## Control-Path Responses

### --signal

Success:
```json
{
  "status": "ok",
  "dispatch_id": "01KM...",
  "artifact_dir": "/tmp/agent-mux-501/01KM...",
  "message": "Signal delivered to inbox"
}
```

Failure:
```json
{
  "status": "error",
  "dispatch_id": "01KM...",
  "message": "invalid dispatch_id: ...",
  "error": {
    "code": "invalid_input",
    "message": "invalid dispatch_id: ...",
    "hint": "Provide a dispatch ID without path separators or traversal segments.",
    "example": "",
    "retryable": true,
    "partial_artifacts": []
  }
}
```

### --version

```json
{"version":"agent-mux v3.2.0"}
```

### Steer responses

All steer actions return:
```json
{
  "action": "abort",
  "dispatch_id": "01KM...",
  "mechanism": "sigterm",
  "pid": 12345,
  "delivered": true
}
```

Mechanisms: `sigterm` (async PID kill), `control_file` (control.json), `stdin_fifo` (Codex stdin pipe), `inbox` (inbox file).

---

## Lifecycle Subcommand JSON

Lifecycle subcommands (`list`, `status`, `result`, `inspect`, `wait`) default to human-readable tables. Pass `--json` for machine-parseable output.

### list --json

NDJSON -- one `DispatchRecord` per line:

Source: `dispatch.DispatchRecord` struct in `internal/dispatch/persistence.go`.

```json
{"id":"01KM...","session_id":"thread_...","status":"completed","engine":"codex","model":"gpt-5.4","role":"lifter","started":"2026-03-28T10:00:00Z","ended":"2026-03-28T10:01:24Z","duration_ms":84231,"cwd":"/repo","truncated":false,"response_chars":1250,"artifact_dir":"/tmp/agent-mux-501/01KM...","effort":"high","timeout_sec":1800}
```

### status --json

For completed dispatches: same `DispatchRecord` shape. For running/live dispatches: `LiveStatus` shape (see above).

### result --json

```json
{"dispatch_id":"01KM...","response":"Worker response text...","status":"completed","session_id":"thread_..."}
```

With `--artifacts`:
```json
{"dispatch_id":"01KM...","artifact_dir":"/tmp/agent-mux-501/01KM...","artifacts":["notes.md"]}
```

### inspect --json

Combines record, response, artifacts, and dispatch meta:

```json
{
  "dispatch_id": "01KM...",
  "session_id": "thread_...",
  "record": {"id":"01KM...","status":"completed","engine":"codex","model":"gpt-5.4",...},
  "response": "Worker response text...",
  "artifact_dir": "/tmp/agent-mux-501/01KM...",
  "artifacts": ["notes.md"],
  "meta": {"dispatch_id":"01KM...","engine":"codex","model":"gpt-5.4","status":"completed",...}
}
```

### wait

Emits periodic status lines to stderr during polling:
```
[42s] running | 7 tools | 3 files changed
```

On completion, emits the result to stdout (same as `result --json` when `--json` is set).

### config

`config` always emits JSON. The top-level `_sources` array lists the config files that were merged.

```json
{
  "defaults": {"engine":"codex","model":"","effort":"high","sandbox":"danger-full-access","permission_mode":"","max_depth":2},
  "models": {"claude":["claude-opus-4-6"],"codex":["gpt-5.4"]},
  "roles": {"lifter":{"engine":"codex","model":"gpt-5.4","effort":"high","timeout":1800,"skills":[]}},
  "timeout": {"low":120,"medium":600,"high":1800,"xhigh":2700,"grace":60},
  "liveness": {"heartbeat_interval_sec":15,"silence_warn_seconds":90,"silence_kill_seconds":180},
  "hooks": {"deny":[],"warn":[],"event_deny_action":""},
  "async": {"poll_interval":""},
  "_sources": ["/Users/alice/.agent-mux/config.toml"]
}
```

`config --sources`:
```json
{"kind":"config_sources","sources":["/Users/alice/.agent-mux/config.toml"]}
```

### config roles --json

JSON array -- one entry per role, then one entry per variant:

```json
[
  {"name":"lifter","engine":"codex","model":"gpt-5.4","effort":"high","timeout":1800},
  {"name":"lifter","engine":"claude","model":"claude-sonnet-4-6","effort":"high","timeout":1800,"variant":"claude"}
]
```

### config models --json

JSON object -- engine name to model list:
```json
{"claude":["claude-opus-4-6","claude-sonnet-4-6"],"codex":["gpt-5.4","gpt-5.4-mini"]}
```

### config skills --json

JSON array of skill discovery results:
```json
[{"name":"gaal","path":"/home/user/.claude/skills/gaal/SKILL.md","source":"search_path (~/.claude/skills)"}]
```

### Lifecycle Errors

All lifecycle errors emit:
```json
{"kind":"error","error":{"code":"not_found","message":"no dispatch found for prefix \"01KM\"","hint":"","example":"","retryable":true,"partial_artifacts":[]}}
```

---

## stderr Event Stream

During dispatch, `stderr` carries NDJSON events (with `--stream`/`-S`). Also mirrored to `<artifact_dir>/events.jsonl`.

Source: `event.Event` struct in `internal/event/event.go`.

Every event includes:

| Field | Notes |
|-------|-------|
| `schema_version` | Always `1` |
| `type` | Event type string |
| `dispatch_id` | Dispatch identifier |
| `ts` | RFC3339 timestamp |

### Event Types

| Type | Extra fields | Notes |
|------|-------------|-------|
| `dispatch_start` | `engine`, `model`, `effort`, `timeout_sec`, `grace_sec`, `cwd` | Emitted at dispatch begin |
| `dispatch_end` | `status`, `duration_ms` | Emitted at dispatch end |
| `heartbeat` | `elapsed_s`, `interval_s`, `last_activity` | Periodic liveness signal |
| `tool_start` | `tool`, `args` | Harness started a tool call |
| `tool_end` | `tool`, `duration_ms` | Harness finished a tool call |
| `file_write` | `path` | Harness wrote a file |
| `file_read` | `path` | Harness read a file |
| `command_run` | `command` | Harness ran a shell command |
| `progress` | `message` | Free-form progress update |
| `timeout_warning` | `message` | Approaching timeout |
| `frozen_warning` | `silence_seconds`, `message` | Extended harness silence |
| `info` | `error_code` (info code), `message` | Diagnostic info (e.g. `stdin_nudge`) |
| `error` | `error_code`, `message` | Error during dispatch |
| `coordinator_inject` | `message` | Inbox message injected |
| `warning` | `error_code`, `message` | Non-fatal warning |

With `--verbose`, raw harness lines are also written to stderr prefixed with
`[engine]`. This breaks pure NDJSON parsing of stderr.

---

## Error Codes

### Built-in Codes

| Code | Meaning |
|------|---------|
| `abort_requested` | Dispatch aborted via `ax steer abort` or control file |
| `artifact_dir_unwritable` | Cannot create/write artifact directory |
| `binary_not_found` | Harness binary not found on PATH |
| `cancelled` | Dispatch cancelled before launch at confirmation |
| `config_error` | Config loading or validation failure |
| `engine_not_found` | Unknown engine name |
| `event_denied` | Hook denied a harness event |
| `frozen_killed` | Harness killed after prolonged silence |
| `internal_error` | agent-mux hit an internal invariant failure |
| `interrupted` | Context cancelled or signal received |
| `invalid_args` | Invalid arguments or missing required fields |
| `invalid_input` | Input failed validation |
| `max_depth_exceeded` | Recursive dispatch depth limit hit |
| `model_not_found` | Unknown model for engine |
| `output_parse_error` | Failed to parse streaming harness output |
| `parse_error` | Malformed final harness output prevented a trusted result |
| `process_killed` | Harness process killed (generic fallback) |
| `prompt_denied` | Hook denied the prompt before launch |
| `recovery_failed` | Existing dispatch state could not be recovered |
| `resume_session_missing` | No session ID available for resume |
| `resume_start_failed` | Resume process failed to start |
| `resume_unsupported` | Engine does not support resume |
| `signal_killed` | Harness killed by OS signal (exit 137/143) |
| `startup_failed` | Harness binary failed to start |

### Harness-Native Codes

Additional codes surface directly from the underlying harness:

- Codex: `context_length_exceeded`
- Claude: `result_error`
- Gemini: `tool_error`
