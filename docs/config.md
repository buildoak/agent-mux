# Configuration

agent-mux configuration is prompt-driven. Worker identity, engine defaults, and dispatch parameters live in markdown files with YAML frontmatter at `~/.agent-mux/prompts/`. There is no TOML config, no per-project config, no merge chain. One global directory, one file per worker.

The binary uses prompt files and environment variables. `~/.agent-mux/config.toml` is a coordinator-layer configuration consumed by R. Jenkins for role routing, variant selection, and pipeline definitions. The binary does not parse TOML.

## Prompt Files

Each prompt file is a markdown document at `~/.agent-mux/prompts/<name>.md`:

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

## How You Think

Read before you write. Understand the existing code, its conventions...
```

The YAML frontmatter sets dispatch defaults. The markdown body becomes the system prompt.

## Frontmatter Schema

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `engine` | string | no | `codex`, `claude`, or `gemini` |
| `model` | string | no | Model name for the engine |
| `effort` | string | no | `low`, `medium`, `high`, `xhigh`. Gemini ignores this (logs a warning); use model selection for thinking depth |
| `timeout` | int | no | Timeout in seconds; must be > 0 when set |
| `description` | string | no | Human-readable purpose line for `config prompts` |
| `skills` | string[] | no | Skill names to inject automatically |

All fields are optional. A prompt file with no frontmatter at all is valid -- the markdown body is used as the system prompt, and all dispatch parameters must come from CLI flags or JSON fields.

## Resolution Order

CLI flags and JSON fields always win over frontmatter. Frontmatter wins over hardcoded defaults.

```text
hardcoded defaults
  |
  v
frontmatter (from prompt file)
  |
  v
CLI flags / --stdin JSON fields
```

Concretely for each field:

| Field | Hardcoded Default | Frontmatter | CLI / JSON |
| --- | --- | --- | --- |
| `engine` | *(none -- required)* | `engine:` | `--engine` / `-E` / `"engine"` |
| `model` | *(none)* | `model:` | `--model` / `-m` / `"model"` |
| `effort` | `high` | `effort:` | `--effort` / `-e` / `"effort"` |
| `timeout` | `900` | `timeout:` | `--timeout` / `-t` / `"timeout_sec"` |
| `grace` | `timeout / 2` | *(not in frontmatter)* | `"grace_sec"` |
| `max_depth` | `2` | *(not in frontmatter)* | `--max-depth` / `"max_depth"` |
| `system_prompt` | *(none)* | markdown body | `--system-prompt` / `-s` |
| `skills` | *(none)* | `skills:` | `--skill` / `"skills"` |

Key behaviors:

- **Engine is required.** If no engine is set after resolution, the dispatch fails with `invalid_args`.
- **Frontmatter timeout must be positive.** A `timeout: 0` or negative value in frontmatter is a validation error.
- **Grace period is proportional.** When not set explicitly, `grace_sec = timeout_sec / 2` (minimum 1).
- **Skills merge.** Frontmatter skills are prepended; request skills follow. Duplicates are removed.
- **System prompt from frontmatter is the default.** An explicit `--system-prompt` or `system_prompt` JSON field replaces it entirely.

### `--stdin` Mode Resolution

In `--stdin` mode, the same resolution applies but using JSON field presence instead of CLI flag tracking:

```json
{
  "profile": "lifter",
  "prompt": "Add retry logic to client.ts",
  "cwd": "/repo",
  "model": "gpt-5.4-mini"
}
```

Here `model` overrides the frontmatter value (`gpt-5.4`), while `engine`, `effort`, and `timeout` come from the lifter profile's frontmatter. `coordinator` is accepted as an alias for `profile`.

## Profile Discovery

`agent-mux config prompts` discovers all `.md` files in `~/.agent-mux/prompts/` and displays their metadata:

```bash
$ agent-mux config prompts
NAME                  ENGINE  MODEL             EFFORT  TIMEOUT  DESCRIPTION
architect             claude  claude-opus-4-6   high    900      Strategic plans with verification gates -- tradeoffs, risks, step-by-step specs
auditor               codex   gpt-5.4           xhigh   2700     Adversarial review -- finds bugs, missing tests, unsafe assumptions
explorer              codex   gpt-5.4           high    600      Multi-file internal investigation -- traces chains across code, config, knowledge base
grunt                 codex   gpt-5.4-mini      medium  600      Mechanical execution -- rote changes, fan-out units, pattern application
lifter                codex   gpt-5.4           high    1800     Scoped implementation -- code changes with built-in verification
researcher            claude  claude-opus-4-6   high    900      External synthesis with confidence -- web search, triangulation, sourced verdicts
scout                 codex   gpt-5.4-mini      low     180      Quick read-only probe -- existence checks, single-fact lookups, status reads
ticket-worker         codex   gpt-5.4-mini      xhigh   -        Ticket execution (standard)
ticket-worker-heavy   codex   gpt-5.4           high    -        Ticket execution (complex)
writer                codex   gpt-5.4           high    1500     Publishable prose in the user's voice -- blog posts, docs, public writing
```

`agent-mux config prompts --json` emits a JSON array for programmatic use:

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

## Hardcoded Defaults

When frontmatter and CLI leave a field unset, these hardcoded values apply:

| Parameter | Default | Source |
| --- | --- | --- |
| `effort` | `high` | hardcoded in `main.go` |
| `timeout_sec` | `900` | `config.DefaultTimeoutSec` |
| `grace_sec` | `timeout_sec / 2` | proportional, minimum 1 |
| `max_depth` | `2` | `config.MaxDepth()`, overridable via `AGENT_MUX_MAX_DEPTH` |
| `permission_mode` | *(engine-specific)* | `config.PermissionMode()`, overridable via `AGENT_MUX_PERMISSION_MODE` |

### Liveness Defaults

Liveness supervision uses hardcoded defaults overridable via environment variables:

| Parameter | Default | Env Override |
| --- | --- | --- |
| `heartbeat_interval_sec` | `15` | `AGENT_MUX_HEARTBEAT_INTERVAL_SEC` |
| `silence_warn_seconds` | `90` | `AGENT_MUX_SILENCE_WARN_SECONDS` |
| `silence_kill_seconds` | `180` | `AGENT_MUX_SILENCE_KILL_SECONDS` |

### Model Validation

Each engine has a fallback model allowlist used when no model list is configured:

| Engine | Fallback models |
| --- | --- |
| `codex` | `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex-spark`, `gpt-5.2-codex` |
| `claude` | `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5` |
| `gemini` | `gemini-2.5-flash`, `gemini-2.5-pro`, `gemini-3-flash-preview`, `gemini-3.1-pro-preview` |

An unrecognized model produces a `model_not_found` error with fuzzy suggestions.

## Hooks

Hooks are configured through `.agent-mux/hooks/` directories (project-local discovery) and operate on harness events. Hook scripts receive JSON on stdin and return exit codes to allow, block, or warn.

| Script | Trigger |
| --- | --- |
| `pre-dispatch.sh` | Before harness launch |
| `on-event.sh` | On each harness event |

Exit codes: `0` = allow, `1` = block, `2` = warn.

`event_deny_action` controls whether a denied event kills the dispatch or downgrades to a warning.

Hook environment variables:

- Pre-dispatch: `HOOK_PHASE`, `HOOK_PROMPT`, `HOOK_SYSTEM_PROMPT`
- Event: `HOOK_PHASE`, `HOOK_COMMAND`, `HOOK_FILE_PATH`, `HOOK_TOOL`, `HOOK_TEXT`

## Skill Injection

`LoadSkills()` resolves skill names in this order:

1. `<cwd>/.claude/skills/<name>/SKILL.md`
2. Each configured search path as `<search_path>/<name>/SKILL.md`

First readable match wins. Loaded content is wrapped as `<skill name="NAME"> ... </skill>`. If the resolved skill directory contains `scripts/`, that path is added to the dispatch.

Skills from profile frontmatter are prepended to request skills. Duplicates are removed.

## Config Inspection Commands

```bash
agent-mux config                    # summary of hardcoded defaults and env overrides
agent-mux config prompts            # list all profiles with metadata
agent-mux config prompts --json     # JSON array of profile catalog
agent-mux config skills             # list discoverable skills
agent-mux config skills --json      # JSON array of skills
```

## Cross-References

- [Dispatch](./dispatch.md) for the `DispatchSpec` and `DispatchResult` contract
- [Engines](./engines.md) for adapter behavior and per-harness differences
- [CLI Reference](./cli-reference.md) for commands, flags, and stdin mode
- [Architecture](./architecture.md) for the system design and package map
