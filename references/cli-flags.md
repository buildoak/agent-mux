# CLI Flags and DispatchSpec Reference

## Contents

- CLI flag table
- Subcommands
- DispatchSpec JSON fields (--stdin)
- Defaults and precedence
- Persistence layout

---

## CLI Flags

Source of truth: `cmd/agent-mux/main.go` — `newFlagSet()` at line 1150, `cliFlags` struct at line 67.

### Dispatch flags (all engines)

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--engine` | `-E` | string | from config | `codex`, `claude`, `gemini` |
| `--role` | `-R` | string | unset | Role name from config.toml |
| `--variant` | | string | unset | Variant within a role (requires `--role`) |
| `--profile` | | string | unset | Coordinator persona (loads `.claude/agents/<name>.md`) |
| `--cwd` | `-C` | string | current dir | Working directory for the harness |
| `--model` | `-m` | string | from role/config | Model override |
| `--effort` | `-e` | string | `high` | `low`, `medium`, `high`, `xhigh` |
| `--timeout` | `-t` | int | effort-mapped | Timeout in seconds |
| `--system-prompt` | `-s` | string | unset | Appended system context |
| `--system-prompt-file` | | string | unset | File loaded as system prompt (resolved from shell cwd) |
| `--prompt-file` | | string | unset | Prompt from file instead of positional arg |
| `--context-file` | | string | unset | Large context file; injects read preamble (`$AGENT_MUX_CONTEXT`) |
| `--skill` | | string[] | `[]` | Repeatable; loads `<cwd>/.claude/skills/<name>/SKILL.md` |
| `--config` | | string | unset | Explicit config path (overrides default lookup) |
| `--artifact-dir` | | string | auto | Override artifact directory |
| `--recover` | | string | unset | Dispatch ID to continue from |
| `--signal` | | string | unset | Dispatch ID to send a message to |
| `--full` | `-f` | bool | `true` | Full access mode (Codex sandbox bypass) |
| `--no-full` | | bool | `false` | Disable full access |
| `--stdin` | | bool | `false` | Read DispatchSpec JSON from stdin |
| `--max-depth` | | int | `2` | Maximum recursive dispatch depth |
| `--skip-skills` | | bool | `false` | Skip skill injection (keep role engine/model/effort) |
| `--yes` | | bool | `false` | Skip TTY confirmation (auto-set when `--stdin`) |
| `--async` | | bool | `false` | Return immediately with dispatch ID, run worker in background |
| `--verbose` | `-v` | bool | `false` | Raw harness lines on stderr |
| `--stream` | `-S` | bool | `false` | Stream all events to stderr |
| `--version` | | bool | | Print version |

### Codex-specific

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--sandbox` | | string | `danger-full-access` | `danger-full-access`, `workspace-write`, `read-only` |
| `--reasoning` | `-r` | string | `medium` | Codex reasoning effort (`model_reasoning_effort`) |
| `--add-dir` | | string[] | `[]` | Repeatable additional writable directories |

### Claude-specific

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--permission-mode` | | string | from config | `default`, `acceptEdits`, `bypassPermissions`, `plan` |
| `--max-turns` | | int | unset | Maximum conversation turns |

### Gemini-specific

Gemini reuses `--permission-mode` for its `--approval-mode` flag (default: `yolo`).

---

## Subcommands

Source of truth: `cmd/agent-mux/main.go` — `splitCommand()`, `help.go`.

```
agent-mux [flags] <prompt>        # dispatch (implicit)
agent-mux dispatch [flags] <prompt>
agent-mux preview [flags] <prompt>
agent-mux help

agent-mux list [--limit N] [--status <filter>] [--engine <filter>] [--json]
agent-mux status <dispatch_id> [--json]
agent-mux result <dispatch_id> [--json] [--artifacts] [--no-wait]
agent-mux inspect <dispatch_id> [--json]
agent-mux wait [--poll 60s] <dispatch_id> [--json]

agent-mux steer <dispatch_id> abort
agent-mux steer <dispatch_id> nudge ["message"]
agent-mux steer <dispatch_id> redirect "<instructions>"
agent-mux steer <dispatch_id> extend <seconds>

agent-mux config [--sources]
agent-mux config roles [--json]
agent-mux config models [--json]
agent-mux config skills [--json]

agent-mux --signal <dispatch_id> "<message>"
agent-mux --stdin < spec.json
agent-mux --version
agent-mux -- help          # literal prompt escape
```

### Config subcommands

Shared flags for all config modes: `--config`, `--cwd`.

#### `config` (root)

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--sources` | bool | `false` | Emit only the `config_sources` object (loaded file list) |

Always emits JSON. The output includes a top-level `_sources` array.

#### `config roles`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON array instead of tabular output |

Default: tabular table of NAME, ENGINE, MODEL, EFFORT, TIMEOUT. Variants shown indented under their parent role.

#### `config models`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON object instead of plain text |

Default: one line per engine -- `<engine>: <model>, <model>, ...`.

#### `config skills`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON array instead of tabular output |

Default: tabular table of NAME, PATH, SOURCE. Scans cwd, configDir, and `[skills] search_paths`. Deduplicated: first match wins.

### Lifecycle subcommands

#### `list`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--limit` | int | `20` | Maximum records to print; `0` = all |
| `--status` | string | unset | Filter: `completed`, `failed`, `timed_out` |
| `--engine` | string | unset | Filter: `codex`, `claude`, `gemini` |
| `--json` | bool | `false` | Emit NDJSON (one JSON object per line) |

No positional arguments. Default output is tabular: ID (12 chars), STATUS, ENGINE, MODEL, DURATION, CWD (48 chars). Sorted by start time (newest first).

#### `status <dispatch_id>`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit full record as JSON |

Accepts full dispatch ID or unique prefix. For completed dispatches shows: Status, Engine/Model, Duration, Started, Truncated, ArtifactDir. For running/live dispatches shows: State, Elapsed, Last Activity, Tools Used, Files Changed, ArtifactDir. Detects orphaned processes via `host.pid`.

#### `result <dispatch_id>`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON |
| `--artifacts` | bool | `false` | List artifact directory contents |
| `--no-wait` | bool | `false` | Return error if dispatch is still running instead of blocking |

Accepts full dispatch ID or unique prefix. Without `--artifacts`, prints stored result text. Falls back to `full_output.md` from artifact dir. If dispatch is still running and `--no-wait` is not set, polls until done.

#### `inspect <dispatch_id>`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit full inspection payload as JSON |

Full view: record, response, artifacts, and dispatch metadata. JSON mode keys: `dispatch_id`, `session_id`, `record`, `response`, `artifact_dir`, `artifacts`, and optionally `meta`.

#### `wait <dispatch_id>`

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--json` | bool | `false` | Emit JSON result when done |
| `--poll` | string | `60s` or config | Status poll interval (Go duration: `5s`, `1m`) |
| `--config` | string | unset | Config path for poll interval resolution |
| `--cwd` | string | unset | Working directory for config discovery |

Poll interval precedence: CLI `--poll` > `[async].poll_interval` from config > 60s default. Emits status lines to stderr during wait. Detects orphaned processes.

### Steer subcommands

| Action | Syntax | Notes |
|--------|--------|-------|
| `abort` | `steer <id> abort` | SIGTERM to host PID, or `control.json` for foreground |
| `nudge` | `steer <id> nudge ["msg"]` | Default: "Please wrap up..." |
| `redirect` | `steer <id> redirect "<instructions>"` | Instructions required |
| `extend` | `steer <id> extend <seconds>` | Positive integer required |

Delivery mechanism priority: stdin FIFO (codex only) > inbox file.

---

## DispatchSpec JSON Fields (--stdin)

When using `--stdin`, pipe a JSON object. Source of truth: `types.DispatchSpec` in `internal/types/types.go` and `materializeStdinDispatchSpec` in `main.go`.

### Core fields

| Field | JSON key | Type | Required | Default | Notes |
|-------|----------|------|----------|---------|-------|
| Prompt | `prompt` | string | yes | - | The task prompt |
| Working directory | `cwd` | string | - | shell cwd | Harness working directory |
| Engine | `engine` | string | role or this | - | `codex`, `claude`, `gemini` |
| Model | `model` | string | - | from role/config | Model override |
| Effort | `effort` | string | - | `high` | `low`, `medium`, `high`, `xhigh` |
| System prompt | `system_prompt` | string | - | - | Appended system context |
| Role | `role` | string | - | - | Resolves engine/model/effort/timeout |
| Variant | `variant` | string | - | - | Engine swap within a role |
| Profile | `profile` | string | - | - | Coordinator persona name |
| Skills | `skills` | string[] | - | `[]` | Skill names to inject |
| Skip skills | `skip_skills` | bool | - | `false` | Skip skill injection |
| Context file | `context_file` | string | - | - | Path to large context file |

### Control fields

| Field | JSON key | Type | Default | Notes |
|-------|----------|------|---------|-------|
| Dispatch ID | `dispatch_id` | string | auto ULID | Unique dispatch identifier |
| Timeout | `timeout_sec` | int | effort-mapped | Override in seconds |
| Grace period | `grace_sec` | int | 60 | Grace period in seconds |
| Max depth | `max_depth` | int | 2 | Recursive dispatch limit |
| Depth | `depth` | int | 0 | Current recursion depth |
| Full access | `full_access` | bool | true | Full filesystem access (Codex sandbox) |
| Artifact dir | `artifact_dir` | string | auto | Override artifact directory |
| Engine options | `engine_opts` | map | `{}` | Adapter-specific overrides |

### Recovery

| Field | JSON key | Type | Notes |
|-------|----------|------|-------|
| Recover from | `recover` | string | Prior dispatch ID for recovery |

### stdin-only alias

| Field | JSON key | Notes |
|-------|----------|-------|
| Coordinator | `coordinator` | Legacy alias for `profile` (conflicts = error) |

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
  > profile frontmatter timeout
  > timeout table for chosen effort level
```

Config file loading order (later wins on conflicts):

```
~/.agent-mux/config.toml (global)
  > ~/.agent-mux/config.local.toml (global machine-local)
  > <cwd>/.agent-mux/config.toml (project)
  > <cwd>/.agent-mux/config.local.toml (project machine-local)
  > --config path (explicit -- skips implicit lookup above)
  > profile companion .toml (if --profile is set)
```

---

## Persistence Layout

Dispatch records are stored as JSON files under `~/.agent-mux/dispatches/<dispatch_id>/`.

```
~/.agent-mux/dispatches/<dispatch_id>/
  meta.json       # PersistentDispatchMeta — written at dispatch start
  result.json     # PersistentDispatchResult — written at dispatch end
```

Runtime artifacts during dispatch:

```
<artifact_dir>/                  # default: /tmp/agent-mux-<uid>/<dispatch_id>/
  meta.json                      # DispatchMeta (artifact-local copy)
  status.json                    # LiveStatus (updated during dispatch)
  events.jsonl                   # NDJSON event log
  host.pid                       # PID of host process (async dispatches)
  control.json                   # Steering control file (written by steer)
  inbox.md                       # Coordinator mailbox
  full_output.md                 # Full response when truncation occurred
  (worker files)                 # Any files the worker created
```
