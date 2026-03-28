# Output Contract

## Contents

- Single dispatch JSON
- Pipeline JSON
- Control-path responses
- stderr event stream
- Error codes

---

All dispatch and pipeline results use `schema_version: 1`. Control-path
responses (`--signal`, `--version`) are simpler and do not include
`schema_version`.

## Single Dispatch JSON

Normal dispatch writes one JSON object to `stdout`:

```json
{
  "schema_version": 1,
  "status": "completed",
  "dispatch_id": "01KM...",
  "dispatch_salt": "mint-ant-five",
  "trace_token": "AGENT_MUX_GO_01KM...",
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
    "tokens": {
      "input": 1234,
      "output": 567,
      "reasoning": 89,
      "cache_read": 0,
      "cache_write": 0
    },
    "turns": 3,
    "cost_usd": 0,
    "session_id": "thread_...",
    "pipeline_id": "",
    "parent_dispatch_id": ""
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

| Field | Type | Notes |
|-------|------|-------|
| `schema_version` | int | Always `1` |
| `status` | string | `completed`, `timed_out`, `failed` |
| `dispatch_id` | string | ULID for this run |
| `dispatch_salt` | string | Human-greppable `adjective-noun-digit` salt |
| `trace_token` | string | `AGENT_MUX_GO_<dispatch_id>` |
| `response` | string | Final response text (may be truncated) |
| `response_truncated` | bool | True when shortened to `response_max_chars` |
| `full_output` | string/null | Full response content when truncated (or null) |
| `full_output_path` | string/null | Path to `full_output.md` file when response was truncated (omitted when null) |
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

Present only when `status` is `failed`:

```json
{
  "code": "engine_not_found",
  "message": "Engine \"bogus\" not found.",
  "suggestion": "Valid engines: [codex, claude, gemini]",
  "retryable": true,
  "partial_artifacts": []
}
```

### Activity Object

| Field | Type | Notes |
|-------|------|-------|
| `files_changed` | string[] | Unique file paths written |
| `files_read` | string[] | Unique file paths read |
| `commands_run` | string[] | Unique shell commands observed |
| `tool_calls` | string[] | Tool names observed (not guaranteed unique) |

### Metadata Object

| Field | Type | Notes |
|-------|------|-------|
| `engine` | string | Requested engine |
| `model` | string | Requested model (can be empty if harness default used) |
| `role` | string | Role name if dispatched via role |
| `tokens` | object | Best-effort token accounting |
| `turns` | int | Best-effort turn count |
| `cost_usd` | float | Currently zero-filled |
| `session_id` | string | Harness session/thread ID when available |
| `pipeline_id` | string | Set for pipeline workers |
| `parent_dispatch_id` | string | Set for pipeline workers |

### Tokens Object

| Field | Type | Notes |
|-------|------|-------|
| `input` | int | Input tokens |
| `output` | int | Output tokens |
| `reasoning` | int | Reasoning tokens (Codex) |
| `cache_read` | int | Cache read tokens |
| `cache_write` | int | Cache write tokens |

---

## Pipeline JSON

`--pipeline/-P` returns a `PipelineResult` object, NOT a dispatch result.

```json
{
  "schema_version": 1,
  "pipeline_id": "01KM...",
  "status": "completed",
  "steps": [
    {
      "step_name": "plan",
      "step_index": 0,
      "pipeline_id": "01KM...",
      "handoff_mode": "summary_and_refs",
      "workers": [
        {
          "worker_index": 0,
          "dispatch_id": "01KM...",
          "status": "completed",
          "summary": "Designed the migration plan.",
          "artifact_dir": "/tmp/agent-mux/.../pipeline/step-0/worker-0",
          "output_file": "/tmp/agent-mux/.../pipeline/step-0/worker-0/output.md",
          "duration_ms": 45000
        }
      ],
      "handoff_text": "=== Output from step ...",
      "succeeded": 1,
      "failed": 0,
      "total_ms": 45000
    }
  ],
  "final_step": { "..." },
  "duration_ms": 120000
}
```

### Pipeline Status

| `status` | Meaning |
|----------|---------|
| `completed` | No worker failures in any step |
| `partial` | Some workers failed but each step had at least one success/timeout |
| `failed` | A step had zero successful workers; pipeline stopped |

### Worker Status

| `status` | Meaning |
|----------|---------|
| `completed` | Worker finished cleanly |
| `timed_out` | Worker timed out (counts as success for pipeline progression) |
| `failed` | Worker failed |

### Step Output Object

| Field | Type | Notes |
|-------|------|-------|
| `step_name` | string | From pipeline config |
| `step_index` | int | Zero-based step position |
| `pipeline_id` | string | Pipeline run identifier |
| `handoff_mode` | string | `summary_and_refs`, `full_concat`, `refs_only` |
| `workers` | array | Worker results for this step |
| `handoff_text` | string | Rendered handoff for next step |
| `succeeded` | int | Workers that completed or timed out |
| `failed` | int | Workers that failed |
| `total_ms` | int | Step wall-clock time |

### Worker Result Object

| Field | Type | Notes |
|-------|------|-------|
| `worker_index` | int | Position in fan-out |
| `dispatch_id` | string | ULID for this worker |
| `status` | string | `completed`, `timed_out`, `failed` |
| `summary` | string | Handoff summary (max 2000 chars) |
| `artifact_dir` | string | Worker artifact directory |
| `output_file` | string | Path to `output.md` (empty on failure) |
| `error_code` | string | Error code on failure |
| `error_msg` | string | Error message on failure |
| `duration_ms` | int | Worker duration |

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

Failure: structured JSON on stdout with non-zero exit:
```json
{
  "status": "error",
  "dispatch_id": "01KM...",
  "message": "invalid dispatch_id: ...",
  "error": {
    "code": "invalid_input",
    "message": "invalid dispatch_id: ...",
    "suggestion": "Provide a dispatch ID without path separators or traversal segments.",
    "retryable": true,
    "partial_artifacts": []
  }
}
```

### --version

JSON on stdout:
```json
{"version":"agent-mux v2.0.0-dev"}
```

### -o=text

For normal dispatches only. Prints human-readable summary:

```
Status: completed
Engine: codex
Model: gpt-5.4
Tokens: input=1234 output=567
Duration: 84231ms

--- Response ---
Worker response text
```

Pipeline mode ignores `-o=text` and always outputs JSON.

---

## Lifecycle Subcommand JSON

Lifecycle subcommands (`list`, `status`, `result`, `inspect`, `gc`) default to
human-readable tables. Pass `--json` for machine-parseable output.

### list --json

NDJSON — one `DispatchRecord` per line:

```json
{"id":"01KM...","salt":"mint-ant-five","status":"completed","engine":"codex","model":"gpt-5.4","role":"lifter","started":"2026-03-28T10:00:00Z","ended":"2026-03-28T10:01:24Z","duration_ms":84231,"cwd":"/repo","truncated":false,"artifact_dir":"/tmp/agent-mux-501/01KM..."}
```

Flags: `--limit N`, `--status completed|failed|timed_out`, `--engine codex|claude|gemini`.

### status --json

Single JSON object (same `DispatchRecord` shape):

```json
{"id":"01KM...","salt":"mint-ant-five","status":"completed","engine":"codex","model":"gpt-5.4","role":"lifter","started":"2026-03-28T10:00:00Z","ended":"2026-03-28T10:01:24Z","duration_ms":84231,"cwd":"/repo","truncated":false,"artifact_dir":"/tmp/agent-mux-501/01KM..."}
```

### result --json

```json
{"dispatch_id":"01KM...","response":"Worker response text..."}
```

With `--artifacts`:
```json
{"dispatch_id":"01KM...","artifact_dir":"/tmp/agent-mux-501/01KM...","artifacts":["/tmp/agent-mux-501/01KM.../notes.md"]}
```

### inspect --json

Combines record, response, artifacts, and dispatch meta:

```json
{
  "dispatch_id": "01KM...",
  "record": {"id":"01KM...","salt":"mint-ant-five","status":"completed","engine":"codex","model":"gpt-5.4",...},
  "response": "Worker response text...",
  "artifact_dir": "/tmp/agent-mux-501/01KM...",
  "artifacts": ["notes.md"],
  "meta": {"dispatch_id":"01KM...","engine":"codex","model":"gpt-5.4","status":"completed",...}
}
```

### gc

Always JSON output:

```json
{"kind":"gc","removed":3,"kept":17,"cutoff":"2026-03-21T10:00:00Z"}
```

With `--dry-run`:
```json
{"kind":"gc_dry_run","would_remove":3,"dispatches":[{"id":"01KM...","started":"...","engine":"codex","status":"completed"}]}
```

### Lifecycle Errors

All lifecycle errors emit:
```json
{"kind":"error","error":{"code":"not_found","message":"no dispatch found for prefix \"01KM\"","suggestion":"","retryable":true,"partial_artifacts":[]}}
```

---

## stderr Event Stream

During dispatch, `stderr` carries NDJSON events. Also mirrored to
`<artifact_dir>/events.jsonl`.

Every event includes:

| Field | Notes |
|-------|-------|
| `schema_version` | Always `1` |
| `type` | Event type string |
| `dispatch_id` | Dispatch identifier |
| `salt` | Human-readable salt |
| `trace_token` | Trace token |
| `ts` | RFC3339 timestamp |

### Event Types

| Type | Extra fields | Notes |
|------|-------------|-------|
| `dispatch_start` | `engine`, `model`, `effort`, `timeout_sec`, `grace_sec`, `cwd`, `skills` | Emitted at dispatch begin |
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
| `error` | `error_code`, `message` | Error during dispatch |
| `response_truncated` | `full_output_path` | Response exceeded `response_max_chars`, full output written to file |
| `coordinator_inject` | `message` | Inbox message injected |
| `warning` | `error_code`, `message` | Non-fatal warning |

With `--verbose`, raw harness lines are also written to stderr prefixed with
`[engine]`. This breaks pure NDJSON parsing of stderr.

---

## Error Codes

### Built-in Codes

| Code | Meaning |
|------|---------|
| `artifact_dir_unwritable` | Cannot create/write artifact directory |
| `binary_not_found` | Harness binary not found on PATH |
| `config_error` | Config loading or validation failure |
| `engine_not_found` | Unknown engine name |
| `event_denied` | Hook denied an event |
| `frozen_tool_call` | Harness silence exceeded kill threshold |
| `interrupted` | Context cancelled / signal received |
| `invalid_args` | Invalid arguments or missing required fields |
| `max_depth_exceeded` | Recursive dispatch depth limit hit |
| `model_not_found` | Unknown model for engine |
| `output_parse_error` | Failed to parse harness output |
| `process_killed` | Harness process was killed |
| `prompt_denied` | Hook denied the prompt |
| `resume_session_missing` | No session ID available for resume |
| `resume_start_failed` | Resume process failed to start |
| `resume_unsupported` | Engine does not support resume |

### Harness-Native Codes

Additional codes surface directly from the underlying harness:

- Codex: `context_length_exceeded`
- Claude: `result_error`
- Gemini: `tool_error`

Treat `error.suggestion` as best-effort guidance, not a closed enum.
