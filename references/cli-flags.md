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
| `--version` | `-V` | bool | | Print version |
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
| Response max chars | `response_max_chars` | int | 16000 | Truncation threshold |
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
