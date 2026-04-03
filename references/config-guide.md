# TOML Config Guide

## Contents

- Config file locations and resolution
- Config structure
- Roles and variants
- Coordinator/profile system
- Skill injection
- Async configuration

---

## Config File Locations

Source of truth: `internal/config/config.go` -- `configPaths()`.

Config files are TOML. Loaded in order (later wins on conflicts):

1. **Global:** `~/.agent-mux/config.toml`
   (legacy fallback: `~/.config/agent-mux/config.toml` -- emits deprecation warning)
2. **Global machine-local:** `~/.agent-mux/config.local.toml`
3. **Project:** `<cwd>/.agent-mux/config.toml`
4. **Project machine-local:** `<cwd>/.agent-mux/config.local.toml`
5. **Explicit:** `--config <path>` (file or directory -- skips implicit lookup above)

The `config.local.toml` files are machine-local overlays for per-machine
secrets, model overrides, or environment-specific settings. Add them to
`.gitignore`.

When `--config` points to a directory, it looks for
`<dir>/.agent-mux/config.toml` then `<dir>/config.toml`.

When `--profile` is set and a companion `<name>.toml` exists beside
`<name>.md`, that TOML is merged into config after project config but before
role resolution.

---

## Config Structure

Source of truth: `config.Config` struct in `internal/config/config.go`.

```toml
[defaults]
engine = "codex"
model = "gpt-5.4"
effort = "high"
sandbox = "danger-full-access"
permission_mode = ""
max_depth = 2

[skills]
search_paths = [
  "~/.claude/skills",
  "~/thinking/pratchett-os/coordinator/.claude/skills",
]

[liveness]
heartbeat_interval_sec = 15
silence_warn_seconds = 90
silence_kill_seconds = 180

[timeout]
low = 120
medium = 600
high = 1800
xhigh = 2700
grace = 60

[models]
codex = ["gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark"]
claude = ["claude-opus-4-6", "claude-sonnet-4-6"]
gemini = ["gemini-3-flash-preview", "gemini-3.1-pro-preview"]

[hooks]
deny = ["DROP TABLE", "vault.sh export"]
warn = ["rm -rf", "git push --force", "curl", "wget"]
event_deny_action = "kill"

[async]
poll_interval = "60s"

[roles.NAME]
engine = "codex"
model = "gpt-5.4"
effort = "high"
timeout = 1800
skills = ["skill-name"]
system_prompt_file = "prompts/role.md"

[roles.NAME.variants.VARIANT]
engine = "claude"
model = "claude-sonnet-4-6"
# Any field from the role can be overridden
```

### Defaults Section

Source: `config.DefaultsConfig` struct.

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `engine` | string | - | Default engine when not specified |
| `model` | string | - | Default model |
| `effort` | string | `high` | Default effort level |
| `sandbox` | string | `danger-full-access` | Codex sandbox mode |
| `permission_mode` | string | - | Claude/Gemini permission mode |
| `max_depth` | int | 2 | Recursive dispatch limit |

### Skills Section

Source: `config.SkillsConfig` struct.

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `search_paths` | string[] | `[]` | Additional directories to search for skills. Tilde (`~`) expansion supported. |

Search paths from all config layers are union-merged with dedup.

### Liveness Section

Source: `config.LivenessConfig` struct.

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `heartbeat_interval_sec` | int | 15 | Heartbeat emission interval |
| `silence_warn_seconds` | int | 90 | Emit `frozen_warning` after this silence |
| `silence_kill_seconds` | int | 180 | Kill harness after this silence |

### Timeout Section

Source: `config.TimeoutConfig` struct.

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `low` | int | 120 | Timeout for low effort (seconds) |
| `medium` | int | 600 | Timeout for medium effort |
| `high` | int | 1800 | Timeout for high effort |
| `xhigh` | int | 2700 | Timeout for xhigh effort |
| `grace` | int | 60 | Grace period after soft timeout |

All timeout values are validated to be positive integers when explicitly set in config. Zero/negative values produce a `ValidationError`.

### Models Section

Maps engine name to list of valid model strings. Used for model validation.

### Hooks Section

Source: `config.HooksConfig` struct.

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `deny` | string[] | `[]` | Block prompts/events matching these patterns |
| `warn` | string[] | `[]` | Inject caution text for matching patterns |
| `event_deny_action` | string | `""` | `"kill"` or `"warn"` for event matches (empty defaults to kill) |

### Async Section

Source: `config.AsyncConfig` struct.

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `poll_interval` | string | `""` | Go duration (e.g. `60s`, `5m`). Used by `ax wait`. Falls back to 60s. |

---

## Roles and Variants

### Role Fields

Source: `config.RoleConfig` struct.

| Field | Type | Notes |
|-------|------|-------|
| `engine` | string | Override default engine |
| `model` | string | Override default model |
| `effort` | string | Override default effort |
| `timeout` | int | Override timeout (seconds); must be > 0 when set |
| `skills` | string[] | Skills to inject (merged with CLI `--skill`) |
| `system_prompt_file` | string | Path to system prompt file (relative to config dir) |

### Variant Fields

Source: `config.RoleVariant` struct. Same fields as `RoleConfig` (except no nested variants).

### Variant Resolution

Variants inherit all fields from the parent role, then override only the
fields they explicitly set.

```toml
[roles.lifter]
engine = "codex"
model = "gpt-5.4"
effort = "high"
timeout = 1800

[roles.lifter.variants.claude]
engine = "claude"
model = "claude-sonnet-4-6"
# effort and timeout inherited from parent
```

Dispatch with variant:
```json
{"role":"lifter","variant":"claude","prompt":"...","cwd":"/repo"}
```

---

## Coordinator/Profile System

### Profile Search Order

Source: `internal/config/coordinator.go` -- `LoadProfile()`.

When `--profile=name` (or JSON `"profile":"name"` / `"coordinator":"name"`),
searches for `<name>.md` in:

1. `<cwd>/.claude/agents/`
2. `<cwd>/agents/`
3. `<cwd>/.agent-mux/agents/`
4. `~/.agent-mux/agents/`

### CoordinatorSpec Fields

Source: `config.CoordinatorSpec` struct.

| Field | Type | Notes |
|-------|------|-------|
| `Name` | string | Profile name |
| `Model` | string | From YAML frontmatter |
| `Effort` | string | From YAML frontmatter |
| `Engine` | string | From YAML frontmatter |
| `Skills` | string[] | From YAML frontmatter |
| `Timeout` | int | From YAML frontmatter |
| `SystemPrompt` | string | Markdown body (below frontmatter) |

### Profile File Format

Markdown with optional YAML frontmatter:

```markdown
---
engine: claude
model: claude-opus-4-6
effort: high
timeout: 900
skills:
  - web-search
  - pratchett-read
---
You are a senior code reviewer. Focus on correctness, edge cases, and test coverage.
```

Recognized frontmatter fields: `engine`, `model`, `effort`, `timeout`, `skills`.

### Companion TOML

If `<name>.toml` exists beside `<name>.md`, it is loaded as a config overlay.
This lets a profile bring its own roles and hooks.

### Prompt Composition

When profile, system-prompt-file, and system-prompt all coexist:

```
1. Role system_prompt_file content (from role config)
2. Profile body (from --profile markdown)
3. System prompt file content (from --system-prompt-file CLI)
4. Inline system prompt text (from --system-prompt / system_prompt JSON)
```

---

## Skill Injection

### Resolution Order

Source: `internal/config/skills.go` -- `LoadSkills()`.

Skills are resolved by searching for `<name>/SKILL.md` in this order:

1. `<cwd>/.claude/skills/<name>/SKILL.md`
2. `<configDir>/.claude/skills/<name>/SKILL.md` (configDir = directory of the config file that defined the active role)
3. Each path in `[skills] search_paths`: `<search_path>/<name>/SKILL.md`

First match wins. Tilde expansion (`~`) is supported in search_paths.

### Behavior

- Content is wrapped in `<skill name="...">` XML blocks and prepended to prompt
- If `<skillDir>/scripts/` exists, it is added to the engine `add-dir` list (Codex `--add-dir`, Claude `--add-dir`, Gemini `--include-directories`)
- Role skills merge with CLI/JSON skills (CLI skills first, then role skills)
- Duplicate skill names are deduplicated
- `--skip-skills` bypasses skill injection entirely while keeping the role's engine/model/effort/timeout
- When a skill is not found, the error names: the missing skill, the requesting role, and all paths searched

### Discovering Skills

```bash
agent-mux config skills        # tabular: NAME, PATH, SOURCE
agent-mux config skills --json # JSON array of {name, path, source}
```
