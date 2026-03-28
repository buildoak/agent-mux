# Engine Comparison

## Contents

- Side-by-side summary
- Harness invocation details
- Model validation
- Permission and sandbox mapping
- Defaults and timeouts
- Known limitations

---

## Side-By-Side Summary

| | Codex | Claude | Gemini |
|---|-------|--------|--------|
| Binary | `codex` | `claude` | `gemini` |
| Best for | Implementation, debugging, edits | Planning, synthesis, review | Second opinion, contrast check |
| Key flags | `--sandbox`, `--reasoning`, `--add-dir` | `--permission-mode`, `--max-turns` | (permission-mode -> approval-mode) |
| Resume | After `thread.started` | After session start | After `init` emits `session_id` |
| Tool calling | Full (file read/write, bash) | Full (file read/write, bash) | Limited (no tool surface) |
| Event streaming | `--json` NDJSON | `--output-format stream-json` | `-o stream-json` NDJSON |

All three engines participate in:
- Artifact logging and directory management
- stderr NDJSON event streaming
- Recovery and inbox signaling
- Timeout and liveness supervision
- Hook evaluation on events

---

## Harness Invocation Details

### Codex

agent-mux invokes:
```bash
codex exec --json [flags] "prompt"
```

Flag mappings:
- `--sandbox` maps to Codex `-s <mode>`
- `--reasoning` maps to `-c model_reasoning_effort=<level>`
- `--add-dir` is forwarded as repeated `--add-dir`
- `--full` + `danger-full-access` = `--dangerously-bypass-approvals-and-sandbox`

Resume path:
```bash
codex exec resume [-m <model>] --json <session_id> "message"
```

Note: session ID is a positional argument, not a flag. The `-m` flag is only
present when a model is specified.

### Claude

agent-mux invokes:
```bash
claude -p --output-format stream-json --verbose [flags] "prompt"
```

Flag mappings:
- `--model` forwarded directly
- `--max-turns` forwarded when non-zero
- `--permission-mode` forwarded directly
- `--system-prompt` forwarded directly

Resume path:
```bash
claude --resume <session_id> --continue "message"
```

### Gemini

agent-mux invokes:
```bash
gemini -p "<prompt>" -o stream-json [flags]
```

Flag mappings:
- `--model` forwarded as `-m`
- `--permission-mode` mapped to Gemini `--approval-mode`
- System prompt: written to `<artifact_dir>/system_prompt.md`, exported as
  `GEMINI_SYSTEM_MD=<path>`

Resume path:
```bash
gemini --resume <session_id> -p "message"
```

---

## Model Validation

If `[models]` is set in config, agent-mux validates against those lists. Otherwise
it uses hardcoded fallbacks:

| Engine | Fallback models |
|--------|----------------|
| `codex` | `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex-spark`, `gpt-5.2-codex` |
| `claude` | `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5` |
| `gemini` | `gemini-2.5-flash`, `gemini-2.5-pro`, `gemini-3-flash-preview` |

The live coordinator config adds:
- Gemini: `gemini-3.1-pro-preview`

`model_not_found` error includes fuzzy-matched suggestions from the active
model list.

---

## Permission and Sandbox Mapping

### Codex — Use --sandbox

| Mode | Access level |
|------|-------------|
| `danger-full-access` | Full filesystem (default) |
| `workspace-write` | Constrained writable workspace |
| `read-only` | Read-only |

Do NOT use `--permission-mode` with Codex. The adapter uses that slot
internally. Drive Codex with `--sandbox`.

### Claude — Use --permission-mode

| Mode | Behavior |
|------|----------|
| `default` | Normal permission flow |
| `acceptEdits` | Allow edits without approval |
| `bypassPermissions` | Full autonomy |
| `plan` | Read-only planning mode |

### Gemini — permission-mode -> approval-mode

v2 passes `--permission-mode` value through to Gemini `--approval-mode`.
Default adapter behavior is `yolo`. Ensure the value matches what your
local Gemini CLI accepts.

---

## Defaults and Timeouts

### Hardcoded Binary Defaults

| Setting | Value |
|---------|-------|
| Effort | `high` (when nothing else sets it) |
| Sandbox | `danger-full-access` |
| Full access | `true` |
| Max depth | `2` |
| Response max chars | `16000` |
| Heartbeat interval | `15` seconds |
| Silence warning | `90` seconds |
| Silence kill | `180` seconds |

### Timeout Table

| Effort | Timeout | Grace |
|--------|---------|-------|
| `low` | 120s | 60s |
| `medium` | 600s | 60s |
| `high` | 1800s | 60s |
| `xhigh` | 2700s | 60s |

### Wrapper Timeout Rule

```
wrapper_timeout >= agent_mux_timeout + 60s
```

Otherwise the wrapper kills the process before v2 can issue grace-period
cleanup and preserve a clean result.

---

## Known Limitations

### Gemini

1. **Response capture broken:** Gemini dispatches may return truncated/empty
   `response` despite generating output. Root cause: stream-json parsing
   drops content.

2. **No tool calling:** Gemini dispatches produce zero file reads, commands,
   or tool calls. The Gemini CLI lacks a tool-use surface comparable to
   Codex/Claude. Gemini variants are reasoning-only — all context must be
   in the prompt.

### General

- `cost_usd` is currently zero-filled by v2; no live cost calculation.
- `metadata.model` is the requested model string, not necessarily what the
  harness actually used.
