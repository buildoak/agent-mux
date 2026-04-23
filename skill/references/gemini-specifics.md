# Gemini Engine Specifics

Operational details for dispatching to the Gemini engine and interpreting its
results. Read when dispatching Gemini workers or debugging Gemini-specific
behavior.

---

## CLI Flag Mapping

| agent-mux flag | Gemini CLI flag | Notes |
|----------------|-----------------|-------|
| `-E=gemini` | *(selects adapter)* | |
| `-m <model>` | `-m <model>` | Required for explicit model control |
| `--permission-mode <mode>` | `--approval-mode <mode>` | Renamed; defaults to `yolo` |
| `--add-dir <dir>` | `--include-directories <dir1,dir2,...>` | Joined into comma-separated value |
| `--effort` / `-e` | *(not supported)* | Resolved on `spec.Effort` but not passed as a Gemini CLI flag |
| `--reasoning` / `-r` | *(not supported)* | Gemini ignores `EngineOpts["reasoning"]` |
| prompt (positional) | `-p "<prompt>"` | Always passed via `-p` flag |
| *(output format)* | `-o stream-json` | Always set by adapter |

### Default `--include-directories`

Gemini dispatches append the home directory when available and `/tmp` in
`--include-directories`. Additional directories from `--add-dir` come before
those defaults in the comma-separated include list.

Invocation shape produced by the adapter:

```bash
gemini -p "<prompt>" -o stream-json [-m <model>] \
  --approval-mode <mode> --include-directories <extra-dirs>,$HOME,/tmp
```

---

## Approval Mode

Gemini maps agent-mux's `permission-mode` to `--approval-mode`.

| Value | Behavior |
|-------|----------|
| `yolo` | No confirmations. **Default for all Gemini dispatches.** |
| `auto_edit` | Auto-approve edits, confirm destructive ops |
| `default` | Confirm all tool calls |
| `plan` | Planning mode, no execution |

Override via `engine_opts["permission-mode"]` or `--permission-mode` CLI flag.
For unattended worker dispatches, `yolo` is correct. Override to `auto_edit`
or `default` only when a human is supervising.

---

## Effort and Reasoning Depth

Gemini CLI has no effort or reasoning-effort flag. `--effort` and profile
frontmatter resolve onto `spec.Effort` but are not passed as CLI flags. A
reasoning override in `EngineOpts["reasoning"]` logs a warning and is ignored:

```
[gemini] Gemini CLI does not support effort flag; ignoring effort=high -- use model selection for thinking depth control
```

**Model selection is the depth lever:**

| Model | Use when |
|-------|----------|
| `gemini-2.5-flash` | Fast, cheap. Quick checks, light analysis |
| `gemini-2.5-pro` | Deep analysis, complex reasoning |
| `gemini-3-flash-preview` | Latest fast model. Good default for quick checks |
| `gemini-3.1-pro-preview` | Latest deep model. Good default for deep analysis |

Rule of thumb: Flash for reads and light tasks, Pro for synthesis and review.

---

## System Prompt

Gemini CLI does not accept a `--system-prompt` flag. The adapter delivers the
system prompt via an environment variable pointing to a temp file:

1. Writes `spec.SystemPrompt` to `<artifact_dir>/system_prompt.md`
2. Sets `GEMINI_SYSTEM_MD=<artifact_dir>/system_prompt.md` in the process env

**Edge case:** If `ArtifactDir` is empty, the system prompt is silently
dropped. This should not happen in normal dispatches but can occur in
malformed `--stdin` payloads.

---

## Resume

Gemini resume is supported but has a UUID degradation quirk.

Resume command shape:

```bash
gemini --resume <session_id> -p "<message>"
```

Gemini CLI `--resume` accepts `"latest"` or a numeric index, not UUID session
IDs. When the session ID from the init event looks like a UUID, the adapter
logs a warning and substitutes `"latest"`:

```
[gemini] session ID "550e8400-..." looks like a UUID; using "latest" for --resume
```

This means recovery and steering resumes can target the most recent Gemini
session rather than a specific one. Avoid Gemini steering/resume when multiple
Gemini sessions overlap or when preserving loaded context matters.

---

## Event Schema

The adapter handles two event schemas transparently:

| Field | v0.34.0+ | Legacy |
|-------|----------|--------|
| Tool name | `tool_name` | `name` |
| Parameters | `parameters` | `input` |
| Error detection | `status == "error"` | `is_error == true` |

Non-JSON stdout lines are surfaced as `raw_passthrough` events (not errors).
Empty lines are silently ignored.

Delta streaming: assistant message fragments arrive as `message` events with
`delta: true`. The adapter accumulates them in a buffer and flushes the full
text on the `result` event. Falls back to `raw.Result` when no deltas
accumulated.

---

## Tool Support

Tracked tools:

| Gemini tool | agent-mux event | Notes |
|-------------|-----------------|-------|
| `read_file` | `file_read` | |
| `write_file` | `file_write` | Tracked via `pendingFiles` for path attribution |
| `replace` | `file_write` | Same pending-file tracking as `write_file` |
| `shell` | `command_run` | |
| `run_shell_command` | `command_run` | Legacy tool name, same behavior |
| `list_directory` | `tool_start` | |

Tool support is functional but less battle-tested than Codex and Claude
adapters. File-write attribution depends on correlating `tool_use` and
`tool_result` events via `tool_id`.

---

## Configured Models

Fallback models come from `config.DefaultModels()`:

- `gemini-2.5-flash`
- `gemini-2.5-pro`
- `gemini-3-flash-preview`
- `gemini-3.1-pro-preview`

Dispatching a model not in this list fails with `model_not_found`.

---

## Dispatching Profiles on Gemini

Any profile can be dispatched on Gemini by overriding the engine and model
via CLI flags. Profile frontmatter/body and skills still apply unless explicit
CLI or JSON fields override them; Gemini receives system prompt via
`GEMINI_SYSTEM_MD`.

```bash
# Use researcher profile on Gemini Pro
agent-mux -P=researcher -E=gemini -m gemini-3.1-pro-preview -C=/repo "Synthesize findings"

# Use scout profile on Gemini Flash
agent-mux -P=scout -E=gemini -m gemini-3-flash-preview -C=/repo "Quick status check"
```

Use Gemini primarily for analysis and second-opinion passes; prefer Codex for
implementation work unless you have a narrow Gemini-specific reason.
