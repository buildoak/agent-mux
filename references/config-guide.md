# TOML Config Guide

## Contents

- Config file locations and resolution
- Config structure
- Roles and variants
- Full variant table (live config)
- Pipeline definitions
- Coordinator/profile system
- Skill injection

---

## Config File Locations

Config files are TOML. Loaded in order (later wins on conflicts):

1. **Global:** `~/.agent-mux/config.toml`
   (legacy fallback: `~/.config/agent-mux/config.toml` — emits deprecation warning)
2. **Project:** `<cwd>/.agent-mux/config.toml`
3. **Explicit:** `--config <path>` (file or directory)

When `--config` points to a directory, v2 looks for
`<dir>/.agent-mux/config.toml` then `<dir>/config.toml`.

When `--profile` is set and a companion `<name>.toml` exists beside
`<name>.md`, that TOML is merged into config after project config but before
role resolution.

---

## Config Structure

```toml
[defaults]
engine = "codex"
model = "gpt-5.4"
effort = "high"
sandbox = "danger-full-access"
permission_mode = ""
response_max_chars = 4000
max_depth = 2
allow_subdispatch = true

[liveness]
heartbeat_interval_sec = 15
silence_warn_seconds = 120
silence_kill_seconds = 240

[timeout]
low = 120
medium = 600
high = 1800
xhigh = 2700
grace = 60

[models]
codex = ["gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"]
claude = ["claude-opus-4-6", "claude-sonnet-4-6"]
gemini = ["gemini-3-flash-preview", "gemini-3.1-pro-preview", "gemini-2.5-pro", "gemini-2.5-flash"]

[hooks]
deny = ["DROP TABLE", "vault.sh export"]
warn = ["rm -rf", "git push --force", "curl", "wget"]
event_deny_action = "deny"

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

[pipelines.NAME]
max_parallel = 4
[[pipelines.NAME.steps]]
name = "step-name"
role = "lifter"
# ... step fields
```

### Defaults Section

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `engine` | string | - | Default engine when not specified |
| `model` | string | - | Default model |
| `effort` | string | `high` | Default effort level |
| `sandbox` | string | `danger-full-access` | Codex sandbox mode |
| `permission_mode` | string | - | Claude permission mode |
| `response_max_chars` | int | 2000 | Truncation threshold |
| `max_depth` | int | 2 | Recursive dispatch limit |
| `allow_subdispatch` | bool | true | Allow recursive dispatches |

### Liveness Section

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `heartbeat_interval_sec` | int | 15 | Heartbeat emission interval |
| `silence_warn_seconds` | int | 90 | Emit frozen_warning after this silence |
| `silence_kill_seconds` | int | 180 | Kill harness after this silence |

### Timeout Section

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `low` | int | 120 | Timeout for low effort (seconds) |
| `medium` | int | 600 | Timeout for medium effort |
| `high` | int | 1800 | Timeout for high effort |
| `xhigh` | int | 2700 | Timeout for xhigh effort |
| `grace` | int | 60 | Grace period after soft timeout |

### Models Section

Maps engine name to list of valid model strings. Used for model validation.
If absent, hardcoded fallback lists are used.

### Hooks Section

| Key | Type | Default | Notes |
|-----|------|---------|-------|
| `deny` | string[] | `[]` | Block prompts/events matching these patterns |
| `warn` | string[] | `[]` | Inject caution text for matching patterns |
| `event_deny_action` | string | `deny` | `deny` (kill) or `warn` for event matches |

---

## Roles and Variants

### Role Fields

| Field | Type | Notes |
|-------|------|-------|
| `engine` | string | Override default engine |
| `model` | string | Override default model |
| `effort` | string | Override default effort |
| `timeout` | int | Override timeout (seconds) |
| `skills` | string[] | Skills to inject (merged with CLI `--skill`) |
| `system_prompt_file` | string | Path to system prompt file (relative to config dir) |

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

## Full Variant Table (Live Coordinator Config)

| Role | Base Engine | Variants |
|------|------------|----------|
| `scout` | codex/gpt-5.4-mini | `gemini` (gemini-3-flash-preview) |
| `explorer` | codex/gpt-5.4 | `claude` (claude-sonnet-4-6), `gemini` (gemini-3-flash-preview) |
| `researcher` | claude/claude-opus-4-6 | `codex` (gpt-5.4), `gemini` (gemini-3.1-pro-preview) |
| `architect` | claude/claude-opus-4-6 | `codex` (gpt-5.4, xhigh), `gemini` (gemini-3.1-pro-preview) |
| `lifter` | codex/gpt-5.4 | `claude` (claude-sonnet-4-6), `gemini` (gemini-3-flash-preview), `mini` (gpt-5.4-mini), `spark` (gpt-5.3-codex-spark) |
| `lifter-deep` | codex/gpt-5.4 xhigh | `claude` (claude-opus-4-6), `gemini` (gemini-3.1-pro-preview) |
| `grunt` | codex/gpt-5.4-mini | `gemini` (gemini-3-flash-preview), `spark` (gpt-5.3-codex-spark) |
| `batch` | codex/gpt-5.4-mini | (none) |
| `auditor` | codex/gpt-5.4 xhigh | `claude` (claude-opus-4-6), `gemini` (gemini-3.1-pro-preview) |
| `writer` | codex/gpt-5.4 | (none) |
| `handoff-extractor` | codex/gpt-5.4-mini | `deep` (gpt-5.4, xhigh), `claude` (claude-sonnet-4-6) |

---

## Coordinator/Profile System

### Profile Search Order

When `--profile=name` (or `--coordinator=name` or JSON `"profile":"name"`),
v2 searches for `<name>.md` in:

1. `<cwd>/.claude/agents/`
2. `<cwd>/agents/`
3. `<cwd>/.agent-mux/agents/`
4. `~/.agent-mux/agents/`

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
Extra fields are stored but ignored by the binary.

### Companion TOML

If `<name>.toml` exists beside `<name>.md`, it is loaded as a config overlay.
This lets a coordinator bring its own roles, pipelines, and hooks.

### Prompt Composition

When profile, system-prompt-file, and system-prompt all coexist:

```
1. Profile body (from --profile)
2. System prompt file content (from --system-prompt-file)
3. Inline system prompt text (from --system-prompt / system_prompt JSON)
```

All three compose in order and are concatenated into the final system prompt.

---

## Skill Injection

Skills are loaded from `<cwd>/.claude/skills/<name>/SKILL.md`.

Behavior:
- Content is wrapped in `<skill name="...">` XML blocks and prepended to prompt
- If `<skillDir>/scripts/` exists, it is added to the engine `add-dir` list (Codex `--add-dir`)
- Role skills merge with CLI/JSON skills (role skills first, then explicit)
- Duplicate skill names are deduplicated
