# Output Contract Reference

All engines emit one JSON payload to `stdout`. `stderr` carries heartbeat lines only.

---

## Success Shape

```json
{
  "success": true,
  "engine": "codex",
  "response": "Implemented retries and added tests.",
  "timed_out": false,
  "duration_ms": 84231,
  "activity": {
    "files_changed": ["src/http/client.ts"],
    "commands_run": ["bun test"],
    "files_read": ["src/http/types.ts"],
    "mcp_calls": ["docs-search/search"],
    "heartbeat_count": 5
  },
  "metadata": {
    "model": "gpt-5.3-codex",
    "session_id": "sess_...",
    "cost_usd": 0.18,
    "tokens": { "input": 12840, "output": 2104, "reasoning": 512 },
    "turns": 4
  }
}
```

## Error Shape

```json
{
  "success": false,
  "engine": "codex",
  "error": "--engine is required. Use: codex, claude, opencode",
  "code": "INVALID_ARGS",
  "duration_ms": 0,
  "activity": {
    "files_changed": [],
    "commands_run": [],
    "files_read": [],
    "mcp_calls": [],
    "heartbeat_count": 0
  }
}
```

---

## Field-by-Field Description

### Top-level fields

| Field | Type | Present | Description |
| --- | --- | --- | --- |
| `success` | `boolean` | always | `true` = run completed (possibly with timeout); `false` = error |
| `engine` | `string` | always | One of `codex`, `claude`, `opencode` |
| `response` | `string` | success only | Agent text response. On timeout this can be a placeholder |
| `timed_out` | `boolean` | success only | `true` if timeout fired and run was aborted via AbortSignal |
| `error` | `string` | error only | Human-readable error message |
| `code` | `string` | error only | Failure class: `INVALID_ARGS`, `MISSING_API_KEY`, `SDK_ERROR` |
| `duration_ms` | `number` | always | End-to-end runtime in milliseconds |
| `activity` | `object` | always | Structured activity log (see below) |
| `metadata` | `object` | success only | Engine-reported metadata (shape varies by SDK) |

### Activity fields

| Field | Type | Description |
| --- | --- | --- |
| `files_changed` | `string[]` | Files written or modified during the run |
| `commands_run` | `string[]` | Shell commands executed by the agent |
| `files_read` | `string[]` | Files read during the run |
| `mcp_calls` | `string[]` | MCP tool invocations (format: `server/tool`) |
| `heartbeat_count` | `number` | Number of heartbeat lines emitted to stderr |

### Metadata fields

| Field | Type | Description |
| --- | --- | --- |
| `model` | `string` | Model ID used for the run |
| `session_id` | `string` | Engine session identifier |
| `cost_usd` | `number` | Estimated cost in USD |
| `tokens.input` | `number` | Input tokens consumed |
| `tokens.output` | `number` | Output tokens generated |
| `tokens.reasoning` | `number` | Reasoning tokens (Codex only) |
| `turns` | `number` | Number of agent turns |

> `metadata` has `additionalProperties: true` -- engines may add extra fields.

---

## Full JSON Schema

Copied from `README.md`. Canonical source: `src/types.ts`.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "agent-mux output",
  "oneOf": [
    {
      "type": "object",
      "description": "Successful run (including timeout with partial results).",
      "required": ["success", "engine", "response", "timed_out", "duration_ms", "activity", "metadata"],
      "properties": {
        "success": { "const": true, "description": "Always true for success payloads." },
        "engine": { "enum": ["codex", "claude", "opencode"], "description": "Engine used for the run." },
        "response": { "type": "string", "description": "Agent text response. On timeout this can be a placeholder." },
        "timed_out": { "type": "boolean", "description": "True if timeout fired and run was aborted via AbortSignal." },
        "duration_ms": { "type": "number", "description": "End-to-end runtime in milliseconds." },
        "activity": { "$ref": "#/$defs/activity" },
        "metadata": {
          "type": "object",
          "description": "Engine-reported metadata (shape varies by SDK).",
          "properties": {
            "session_id": { "type": "string" },
            "cost_usd": { "type": "number" },
            "tokens": {
              "type": "object",
              "properties": {
                "input": { "type": "number" },
                "output": { "type": "number" },
                "reasoning": { "type": "number" }
              }
            },
            "turns": { "type": "number" },
            "model": { "type": "string" }
          },
          "additionalProperties": true
        }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "description": "Failure payload.",
      "required": ["success", "engine", "error", "code", "duration_ms", "activity"],
      "properties": {
        "success": { "const": false, "description": "Always false for error payloads." },
        "engine": { "enum": ["codex", "claude", "opencode"] },
        "error": { "type": "string", "description": "Human-readable error." },
        "code": { "enum": ["INVALID_ARGS", "MISSING_API_KEY", "SDK_ERROR"], "description": "Failure class." },
        "duration_ms": { "type": "number" },
        "activity": { "$ref": "#/$defs/activity" }
      },
      "additionalProperties": false
    }
  ],
  "$defs": {
    "activity": {
      "type": "object",
      "description": "Structured activity log collected during execution.",
      "required": ["files_changed", "commands_run", "files_read", "mcp_calls", "heartbeat_count"],
      "properties": {
        "files_changed": { "type": "array", "items": { "type": "string" } },
        "commands_run": { "type": "array", "items": { "type": "string" } },
        "files_read": { "type": "array", "items": { "type": "string" } },
        "mcp_calls": { "type": "array", "items": { "type": "string" } },
        "heartbeat_count": { "type": "number", "description": "Heartbeat lines emitted to stderr." }
      },
      "additionalProperties": false
    }
  }
}
```

---

## Heartbeat Protocol

- Interval: every 15s.
- Channel: `stderr` only.
- Format:

```text
[heartbeat] 45s -- processing file changes
```

`stdout` is reserved for final JSON output.
