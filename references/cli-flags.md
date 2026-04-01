# CLI Flags and DispatchSpec Reference

## Contents

- CLI flag table
- DispatchSpec JSON fields (--stdin)
- Flag/JSON field mapping
- Defaults and precedence

---

## CLI Flags

Source of truth: `cmd/agent-mux/main.go` (cliFlags struct).

### Common (all engines)

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--engine` | `-E` | string | from config | `codex`, `claude`, `gemini` |
| `--cwd` | `-C` | string | current dir | Working directory for the harness |
| `--model` | `-m` | string | from role/config | Model override |
| `--effort` | `-e` | string | `high` | `low`, `medium`, `high`, `xhigh` |
| `--timeout` | `-t` | int | effort-mapped | Timeout in seconds |
| `--role` | `-R` | string | unset | Role name from config.toml |
| `--variant` | | string | unset | Variant within a role |
| `--system-prompt` | `-s` | string | unset | Appended system context |
| `--system-prompt-file` | | string | unset | File loaded as system prompt (resolved from shell cwd) |
| `--prompt-file` | | string | unset | Prompt from file instead of positional arg |
| `--context-file` | | string | unset | Large context file; injects read preamble |
| `--skill` | | string[] | `[]` | Repeatable; loads `<cwd>/.claude/skills/<name>/SKILL.md` |
| `--profile` | | string | unset | Coordinator persona (loads `.claude/agents/<name>.md`) |
| `--coordinator` | | string | unset | Legacy alias for `--profile` |
| `--pipeline` | `-P` | string | unset | Named pipeline from config |
| `--config` | | string | unset | Explicit config path (overrides default lookup) |
| `--artifact-dir` | | string | auto | Override artifact directory |
| `--salt` | | string | auto-generated | Human-readable dispatch salt |
| `--recover` | | string | unset | Dispatch ID to continue from |
| `--signal` | | string | unset | Dispatch ID to send a message to |
| `--output` | `-o` | string | `json` | Output format: `json` or `text` |
| `--full` | `-f` | bool | `true` | Full access mode |
| `--no-full` | | bool | `false` | Disable full access |
| `--stdin` | | bool | `false` | Read DispatchSpec JSON from stdin |
| `--max-depth` | | int | `2` | Maximum recursive dispatch depth |
| `--response-max-chars` | | int | from config | Truncate response beyond this |
| `--no-subdispatch` | | bool | `false` | Disable recursive dispatch |
| `--verbose` | `-v` | bool | `false` | Raw harness lines on stderr |
| `--yes` | | bool | `false` | Skip TTY confirmation |
| `--skip-skills` | | bool | `false` | Skip skill injection (keep role engine/model/effort) |
| `--version` | | bool | | Print version (no short flag) |
| `--help` | | bool | | Print help |

### Codex-specific

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--sandbox` | | string | `danger-full-access` | `danger-full-access`, `workspace-write`, `read-only` |
| `--reasoning` | `-r` | string | `medium` | Codex reasoning effort |
| `--add-dir` | | string[] | `[]` | Repeatable additional writable directories |

### Claude-specific

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--permission-mode` | | string | from config | `default`, `acceptEdits`, `bypassPermissions`, `plan` |
| `--max-turns` | | int | unset | Maximum conversation turns |

### Gemini-specific

Gemini reuses `--permission-mode` for its `--approval-mode` flag.

---

## Config Subcommand

```
agent-mux config [flags]
agent-mux config roles [flags]
agent-mux config pipelines [flags]
agent-mux config models [flags]
agent-mux config skills [flags]
```

Introspect the fully-resolved, merged configuration. No dispatch is performed.

### Shared flags (all config modes)

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--config` | string | unset | Explicit config path (overrides default lookup) |
| `--cwd` | string | unset | Working directory for project config discovery |

### `config` (root)

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--sources` | bool | `false` | Emit only the `config_sources` object (loaded file list) |

Always emits JSON. No `--json` flag. The output includes a top-level `_sources` array.

### `config roles`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON array instead of tabular output |

Default: tabular table of NAME, ENGINE, MODEL, EFFORT, TIMEOUT. Variants shown indented under their parent role.

### `config pipelines`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON array instead of tabular output |

Default: tabular NAME, STEPS.

### `config models`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON object instead of plain text |

Default: one line per engine — `<engine>: <model>, <model>, ...`.

### `config skills`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON array instead of tabular output |

Default: tabular table of NAME, PATH, SOURCE. Scans cwd, configDir, and `[skills] search_paths`. Deduplicated: first match wins.

---

## DispatchSpec JSON Fields (--stdin)

When using `--stdin`, pipe a JSON object with these fields.

### Core fields

| Field | JSON key | Type | Required | Default | Notes |
|-------|----------|------|----------|---------|-------|
| Prompt | `prompt` | string | yes | - | The task prompt |
| Working directory | `cwd` | string | yes | shell cwd | Harness working directory |
| Role | `role` | string | - | - | Resolves engine/model/effort/timeout |
| Variant | `variant` | string | - | - | Engine swap within a role |
| Engine | `engine` | string | role or this | - | `codex`, `claude`, `gemini` |
| Model | `model` | string | - | from role/config | Model override |
| Effort | `effort` | string | - | `high` | `low`, `medium`, `high`, `xhigh` |
| System prompt | `system_prompt` | string | - | - | Appended system context |
| Skills | `skills` | string[] | - | `[]` | Skill names to inject |
| Skip skills | `skip_skills` | bool | - | `false` | Skip skill injection |
| Pipeline | `pipeline` | string | - | - | Named pipeline from config |
| Profile | `profile` | string | - | - | Coordinator persona name |
| Context file | `context_file` | string | - | - | Path to large context file |

### Control fields

| Field | JSON key | Type | Default | Notes |
|-------|----------|------|---------|-------|
| Timeout | `timeout_sec` | int | effort-mapped | Override in seconds |
| Grace period | `grace_sec` | int | 60 | Grace period in seconds |
| Max depth | `max_depth` | int | 2 | Recursive dispatch limit |
| Allow subdispatch | `allow_subdispatch` | bool | true | Recursive dispatch toggle |
| Full access | `full_access` | bool | true | Full filesystem access |
| Response max chars | `response_max_chars` | int | 128000 | Uniform dispatch response cap; `0` disables capping |
| Salt | `salt` | string | auto | Human-readable identifier |
| Dispatch ID | `dispatch_id` | string | auto ULID | Unique dispatch identifier |
| Artifact dir | `artifact_dir` | string | auto | Override artifact directory |

### Recovery and continuation

| Field | JSON key | Type | Notes |
|-------|----------|------|-------|
| Continue from | `continues_dispatch_id` | string | Prior dispatch ID for recovery |

### Pipeline-internal (set by pipeline orchestrator, not by callers)

| Field | JSON key | Type | Notes |
|-------|----------|------|-------|
| Pipeline ID | `pipeline_id` | string | Set for steps within a pipeline |
| Pipeline step | `pipeline_step` | int | Step index within pipeline |
| Parent dispatch | `parent_dispatch_id` | string | Parent dispatch for pipeline steps |
| Receives | `receives` | string | Named output from prior step |
| Pass output as | `pass_output_as` | string | Name for this step's output |
| Parallel | `parallel` | int | Fan-out count |
| Handoff mode | `handoff_mode` | string | `summary_and_refs`, `full_concat`, `refs_only` |
| Depth | `depth` | int | Current recursion depth |

### Engine options

| Field | JSON key | Type | Notes |
|-------|----------|------|-------|
| Engine options | `engine_opts` | map | Adapter-specific overrides |

---

## Flag/JSON Mapping

In `--stdin` mode, CLI dispatch flags are ignored (a warning is printed if
both are present). The JSON payload is the sole source of dispatch parameters.

Key differences from CLI mode:

- `full_access` defaults to `true` in stdin mode (unless explicitly set to `false`)
- `allow_subdispatch` defaults to `true` in stdin mode
- `handoff_mode` defaults to `summary_and_refs` in stdin mode
- `grace_sec` defaults to `60` in stdin mode
- `dispatch_id` is auto-generated as a ULID if not provided
- `cwd` falls back to the shell's current directory if empty

---

## Precedence Order

For `engine`, `model`, and `effort`:

```
CLI flags / JSON explicit values
  > --role (resolved from merged TOML config)
  > --profile coordinator frontmatter scalars
  > merged config [defaults]
  > hardcoded defaults (effort="high")
```

For `timeout`:

```
Explicit timeout_sec in JSON / CLI --timeout
  > role.timeout from config
  > profile/coordinator frontmatter timeout
  > timeout table for chosen effort level
```

Config file loading order (later wins on conflicts):

```
~/.agent-mux/config.toml (global)
  > ~/.agent-mux/config.local.toml (global machine-local)
  > <cwd>/.agent-mux/config.toml (project)
  > <cwd>/.agent-mux/config.local.toml (project machine-local)
  > --config path (explicit overlay — skips implicit lookup above)
  > coordinator companion .toml (if --profile is set)
```

---

## Lifecycle Subcommands

Post-dispatch introspection and maintenance. All lifecycle subcommands emit structured JSON errors on failure (`{"kind":"error","error":{...}}`). Dispatch records are stored in the durable JSONL index at `~/.agent-mux/store/`.

### `agent-mux list`

List recent dispatches from the durable store.

```
agent-mux list [--limit N] [--status <filter>] [--engine <filter>] [--json]
```

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--limit` | int | `20` | Maximum records to print; `0` = all |
| `--status` | string | unset | Filter by status: `completed`, `failed`, `timed_out` |
| `--engine` | string | unset | Filter by engine: `codex`, `claude`, `gemini` |
| `--json` | bool | `false` | Emit NDJSON (one JSON object per line) |

No positional arguments accepted. Default output is a tabular table with columns: ID (truncated to 12 chars), SALT, STATUS, ENGINE, MODEL, DURATION, CWD (truncated to 48 chars). Records are shown in chronological order; `--limit` takes the last N.

### `agent-mux status <dispatch_id>`

Show status details for a single dispatch.

```
agent-mux status [--json] <dispatch_id>
```

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit full record as JSON |

Accepts a full dispatch ID or a unique prefix. The prefix is validated via `sanitize.ValidateDispatchID`. Default output shows:

| Field | Description |
|-------|-------------|
| Status | `completed`, `failed`, or `timed_out` |
| Engine/Model | Combined `engine / model` display |
| Duration | Formatted as `Nms` (< 1s) or `Ns` (>= 1s) |
| Started | RFC3339 timestamp |
| Truncated | `true` or `false` |
| Salt | Human-readable dispatch salt |
| ArtifactDir | Path to artifact directory |

### `agent-mux result <dispatch_id>`

Retrieve the response text or artifact listing for a dispatch.

```
agent-mux result [--json] [--artifacts] <dispatch_id>
```

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON |
| `--artifacts` | bool | `false` | List artifact directory contents instead of result text |

Accepts a full dispatch ID or a unique prefix. When `--artifacts` is set, lists files in the artifact directory (resolved from the dispatch record or via `recovery.ResolveArtifactDir`). Without `--artifacts`, prints the stored result text. Falls back to reading `full_output.md` from the artifact directory when the primary result file is missing (covers truncated dispatches and legacy records).

### `agent-mux inspect <dispatch_id>`

Deep view of a dispatch: full record, response, artifacts, and dispatch metadata.

```
agent-mux inspect [--json] <dispatch_id>
```

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit full inspection payload as JSON |

Accepts a full dispatch ID or a unique prefix. The dispatch record must exist (no prefix-only fallback). Default output shows all record fields including Role, Variant, Started, Ended, Duration, Truncated, Salt, Cwd, and ArtifactDir. If artifacts exist, they are listed below the record. If a response is stored, it is printed under a `--- Response ---` separator.

JSON mode emits a single object with keys: `dispatch_id`, `record`, `response`, `artifact_dir`, `artifacts`, and optionally `meta` (the `dispatch_meta.json` from the artifact directory, if present).

### `agent-mux gc --older-than <duration>`

Garbage-collect old dispatch records, result files, and artifact directories.

```
agent-mux gc --older-than <duration> [--dry-run]
```

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--older-than` | string | **required** | Duration threshold; format: `Nd` (days) or `Nh` (hours). Examples: `7d`, `24h` |
| `--dry-run` | bool | `false` | List what would be deleted without deleting |

No positional arguments accepted. `--older-than` is required; omitting it is an error. Duration must be positive. Supported units: `d`/`D` (days), `h`/`H` (hours).

Records with unparseable `started` timestamps are always kept (never deleted).

**What gets cleaned:**
- JSONL records from `~/.agent-mux/store/dispatches.jsonl`
- Result files from `~/.agent-mux/store/results/<id>.md`
- Artifact directories (the full `artifact_dir` path from each record)

**Output (JSON):**
- Normal: `{"kind":"gc","removed":N,"kept":N,"cutoff":"<RFC3339>"}`
- Dry run: `{"kind":"gc_dry_run","would_remove":N,"dispatches":[{"id":"...","started":"...","engine":"...","status":"..."},...]}`
- Nothing to clean: `{"kind":"gc","removed":0,"message":"No dispatches older than <dur> found."}`
