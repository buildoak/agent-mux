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
| `--effort` / `-e` | *(not supported)* | Logged warning, value discarded |
| `--reasoning` / `-r` | *(not supported)* | Same as effort -- discarded with warning |
| prompt (positional) | `-p "<prompt>"` | Always passed via `-p` flag |
| *(output format)* | `-o stream-json` | Always set by adapter |

Invocation shape produced by the adapter:

```bash
gemini -p "<prompt>" -o stream-json [-m <model>] \
  --approval-mode <mode> [--include-directories <dir1,dir2,...>]
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

Gemini CLI has no effort or reasoning-effort flag. When set (via `--effort`,
`--reasoning`, or profile frontmatter), the adapter logs a warning and
discards the value:

```
[gemini] Gemini CLI does not support effort flag; ignoring effort=high — use model selection for thinking depth control
```

**Model selection is the depth lever:**

| Model | Use when |
|-------|----------|
| `gemini-2.5-flash` | Fast, cheap. Quick checks, light analysis |
| `gemini-2.5-pro` | Deep analysis, complex reasoning |
| `gemini-3-flash-preview` | Latest fast model. Default for flash-tier variants |
| `gemini-3.1-pro-preview` | Latest deep model. Default for pro-tier variants |

Rule of thumb: Flash for reads and light tasks, Pro for synthesis and review.

---

## Stall Timeout

The adapter injects `stall_timeout_seconds: 60` into `engine_opts` for all
Gemini dispatches unless already set. Runs that go silent for 60 seconds are
killed by the watchdog.

Override:

```bash
# via engine_opts in --stdin JSON
{"engine_opts": {"stall_timeout_seconds": 120}, ...}
```

This is separate from the dispatch-level `timeout_sec` (total run time) and
the liveness `silence_kill_seconds` (general frozen-process threshold). The
stall timeout is Gemini-specific and shorter than the general liveness default.

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

This means recovery and steering resumes target the most recent Gemini
session, not a specific one. Acceptable for single-worker dispatches;
potentially ambiguous if multiple Gemini dispatches overlap.

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

From `~/.agent-mux/config.toml`:

```toml
gemini = ["gemini-3-flash-preview", "gemini-3.1-pro-preview", "gemini-2.5-pro", "gemini-2.5-flash"]
```

Dispatching a model not in this list fails with `model_not_found` (with fuzzy
match suggestion).

---

## Configured Variants

Roles with Gemini variants (from `config.toml`):

| Role | Variant | Model | Notes |
|------|---------|-------|-------|
| `scout` | `gemini` | `gemini-3-flash-preview` | Quick checks |
| `explorer` | `gemini` | `gemini-3-flash-preview` | Codebase exploration, 300s timeout |
| `researcher` | `gemini` | `gemini-3.1-pro-preview` | Deep research with web-search + summarize |
| `architect` | `gemini` | `gemini-3.1-pro-preview` | Strategic reasoning |
| `lifter` | `gemini` | `gemini-3-flash-preview` | Implementation, 1200s timeout |
| `lifter-deep` | `gemini` | `gemini-3.1-pro-preview` | Hard problems with think-protocol |
| `grunt` | `gemini` | `gemini-3-flash-preview` | Mechanical edits |
| `auditor` | `gemini` | `gemini-3.1-pro-preview` | Adversarial review |

Dispatch via variant flag: `-P=researcher -V=gemini`
