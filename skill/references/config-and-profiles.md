# Configuration and Profiles

Profile discovery, frontmatter schema, resolution order, hooks, and skills.

---

## Prompt Files

Worker identity lives in `~/.agent-mux/prompts/<name>.md`. Single global
directory. No per-project config, no TOML, no merge chain.

```markdown
---
engine: codex
model: gpt-5.4
effort: high
timeout: 1800
description: "Scoped implementation with built-in verification"
---

# Lifter

You are a lifter. You build what was specified, verify it works, and report back.
```

The YAML frontmatter sets dispatch defaults. The markdown body becomes the
system prompt when no explicit system prompt is supplied.

---

## Frontmatter Schema

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `engine` | string | no | `codex`, `claude`, or `gemini` |
| `model` | string | no | Model name for the engine |
| `effort` | string | no | `low`, `medium`, `high`, `xhigh` |
| `timeout` | int | no | Timeout in seconds; must be > 0 when set |
| `description` | string | no | Human-readable purpose line for `config prompts` |
| `skills` | string[] | no | Skill names to inject automatically |

All fields are optional. A file with no frontmatter is valid -- the body is
used as the system prompt and all dispatch parameters must come from CLI
flags or JSON fields.

---

## Resolution Order

CLI flags and JSON fields always win. Frontmatter wins over hardcoded
defaults.

```text
hardcoded defaults
  |
  v
frontmatter (from prompt file)
  |
  v
CLI flags / --stdin JSON fields
```

| Field | Hardcoded Default | Frontmatter | CLI / JSON |
|-------|-------------------|-------------|------------|
| `engine` | *(none -- required)* | `engine:` | `--engine` / `-E` / `"engine"` |
| `model` | *(none)* | `model:` | `--model` / `-m` / `"model"` |
| `effort` | `high` | `effort:` | `--effort` / `-e` / `"effort"` |
| `timeout` | `1800` | `timeout:` | `--timeout` / `-t` / `"timeout_sec"` |
| `grace` | `timeout / 2` | *(not in frontmatter)* | `"grace_sec"` |
| `max_depth` | `2` | *(not in frontmatter)* | `--max-depth` / `"max_depth"` |
| `system_prompt` | *(none)* | markdown body | `--system-prompt` / `-s` |
| `skills` | *(none)* | `skills:` | `--skill` / `"skills"` |

Key behaviors:

- **Engine is required.** If no engine is set after resolution, the dispatch
  fails with `invalid_args`.
- **Frontmatter timeout must be positive.** `timeout: 0` or negative is a
  validation error.
- **Grace period is proportional.** `grace_sec = timeout_sec / 2` (minimum
  1) when not set explicitly.
- **Skills merge.** Frontmatter skills prepended, request skills follow.
  Duplicates removed.
- **System prompt from frontmatter is the default.** An explicit
  `--system-prompt` or `system_prompt` JSON field replaces it entirely.

---

## Profile Discovery

```bash
agent-mux config prompts           # human table
agent-mux config prompts --json    # JSON array for programmatic use
```

Example output:

```
NAME              ENGINE  MODEL              EFFORT  TIMEOUT  DESCRIPTION
architect         claude  claude-sonnet-4-6  high    900      System design and migration planning
lifter            codex   gpt-5.4            high    1800     Scoped implementation with verification
scout             codex   gpt-5.4-mini       low     180      Quick read-only probe and status reads
```

JSON shape:

```json
[
  {
    "name": "lifter",
    "path": "/Users/you/.agent-mux/prompts/lifter.md",
    "source": "~/.agent-mux/prompts",
    "engine": "codex",
    "model": "gpt-5.4",
    "effort": "high",
    "timeout": 1800,
    "description": "Scoped implementation with built-in verification"
  }
]
```

---

## Hardcoded Defaults

When frontmatter and CLI leave a field unset:

| Parameter | Default | Source |
|-----------|---------|-------|
| `effort` | `high` | hardcoded in `main.go` |
| `timeout_sec` | `1800` | `config.DefaultTimeoutSec` |
| `grace_sec` | `timeout_sec / 2` | proportional, minimum 1 |
| `max_depth` | `2` | `config.MaxDepth()`, overridable via `AGENT_MUX_MAX_DEPTH` |

### Liveness Defaults

| Parameter | Default | Env Override |
|-----------|---------|-------------|
| `heartbeat_interval_sec` | `15` | `AGENT_MUX_HEARTBEAT_INTERVAL_SEC` |
| `silence_warn_seconds` | `90` | `AGENT_MUX_SILENCE_WARN_SECONDS` |
| `silence_kill_seconds` | `180` | `AGENT_MUX_SILENCE_KILL_SECONDS` |

### Model Validation

| Engine | Fallback models |
|--------|----------------|
| `codex` | `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex-spark`, `gpt-5.2-codex` |
| `claude` | `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5` |
| `gemini` | `gemini-2.5-flash`, `gemini-2.5-pro`, `gemini-3-flash-preview`, `gemini-3.1-pro-preview` |

---

## Hooks

Hooks live in `.agent-mux/hooks/` directories (project-local discovery).

| Script | Trigger |
|--------|---------|
| `pre-dispatch.sh` | Before harness launch |
| `on-event.sh` | On each harness event |

### pre_dispatch

Receives JSON on stdin:

```json
{
  "phase": "pre_dispatch",
  "prompt": "...",
  "system_prompt": "..."
}
```

Environment: `HOOK_PHASE`, `HOOK_PROMPT`, `HOOK_SYSTEM_PROMPT`.

### on_event

Receives JSON on stdin:

```json
{
  "phase": "event",
  "text": "...",
  "command": "...",
  "tool": "...",
  "file_path": "/absolute/path"
}
```

Environment: `HOOK_PHASE`, `HOOK_COMMAND`, `HOOK_FILE_PATH`, `HOOK_TOOL`,
`HOOK_TEXT`.

### Exit codes

| Exit code | Meaning |
|-----------|---------|
| `0` | allow |
| `1` | block |
| `2` | warn |

stderr becomes the reason string. `event_deny_action` controls whether a
blocked event kills the dispatch (`kill`) or downgrades to a warning (`warn`).

---

## Skill Injection

### Resolution order

1. `<cwd>/.claude/skills/<name>/SKILL.md`
2. Each configured search path: `<search_path>/<name>/SKILL.md`

First readable match wins.

### Behavior

- Skill content is wrapped in `<skill name="...">` blocks and prepended to
  the prompt
- If `<skillRoot>/<name>/scripts/` exists, that directory is added to the
  dispatch
- Duplicate skill names are removed
- `--skip-skills` disables skill injection but does not disable profile
  resolution

Discover skills:

```bash
agent-mux config skills
agent-mux config skills --json
```
