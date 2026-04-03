# Configuration Setup Guide

Practical walkthrough for configuring agent-mux from scratch. For the full TOML schema reference, see [config-guide.md](config-guide.md).

## Directory Structure

```
.agent-mux/
  config.toml       # roles, models, timeouts, hooks
  config.local.toml # machine-local overrides (gitignored)
  prompts/          # system prompt files referenced by roles
  agents/           # profile persona files (.md + optional .toml)
```

- **Global:** `~/.agent-mux/` -- shared defaults across all projects
- **Project:** `<cwd>/.agent-mux/` -- project-specific overrides

Both are optional. If neither exists, hardcoded defaults apply.

## Getting Started

```bash
mkdir -p .agent-mux/prompts
```

Write `.agent-mux/config.toml`:

```toml
[defaults]
engine = "codex"
model = "gpt-5.4"
effort = "high"

[roles.scout]
model = "gpt-5.4-mini"
effort = "low"
timeout = 180
system_prompt_file = "prompts/scout.md"
```

Create `.agent-mux/prompts/scout.md` with your role's system prompt, then verify:

```bash
agent-mux preview -R=scout "Find all TODOs in src/"
```

The `preview` subcommand resolves config and shows final dispatch parameters without launching a harness.

---

## Global vs Project Config

**Resolution order** (later wins):

```
hardcoded defaults
  > ~/.agent-mux/config.toml (global)
  > ~/.agent-mux/config.local.toml (global machine-local)
  > <cwd>/.agent-mux/config.toml (project)
  > <cwd>/.agent-mux/config.local.toml (project machine-local)
  > --config <path> (explicit -- skips implicit lookup)
```

**Merge rule: defined-wins.** An explicitly set field in a later file overrides the earlier value. An absent field preserves the base. This is per-field, not per-section.

| What | Merge behavior |
|------|---------------|
| Scalar fields (`engine`, `model`, `timeout`) | Last explicit definition wins |
| `[models].<engine>`, `skills.search_paths`, `hooks.deny/warn` | Union-merged with dedup |
| `[roles.<name>]` | Deep-merged per field |
| `[roles.<name>.variants.<v>]` | Additive -- new variants added, collisions deep-merged |

**Global** is for defaults that apply everywhere: engine preferences, model lists, liveness tuning, timeout tiers.
**Project** is for repo-specific roles, system prompts, skills, and hooks.

---

## Defining Roles

A role bundles engine, model, effort, timeout, skills, and system prompt into one name.

```toml
[roles.researcher]
engine = "claude"
model = "claude-opus-4-6"
effort = "high"
timeout = 900
skills = ["web-search", "pratchett-read"]
system_prompt_file = "prompts/researcher.md"
```

`system_prompt_file` is relative to the config directory. `prompts/researcher.md` resolves to `.agent-mux/prompts/researcher.md`. If not in `prompts/`, it falls back to bare filename.

---

## Defining Variants

Variants swap engines within a role. They inherit all parent fields and override only what they set.

```toml
[roles.lifter]
engine = "codex"
model = "gpt-5.4"
effort = "high"
timeout = 1800
system_prompt_file = "prompts/lifter.md"

[roles.lifter.variants.claude]
engine = "claude"
model = "claude-sonnet-4-6"
# effort, timeout, system_prompt_file inherited

[roles.lifter.variants.spark]
model = "gpt-5.3-codex-spark"
effort = "medium"
timeout = 600
```

Dispatch: `{"role":"lifter","variant":"claude","prompt":"...","cwd":"/repo"}`

Use variants when task semantics are the same but you want a different engine/model. Use separate roles when the system prompt, skills, or effort differ fundamentally.

---

## Profile/Coordinator Personas

Profiles load an orchestrator persona from markdown with YAML frontmatter. Search order for `--profile=reviewer`:

1. `<cwd>/.claude/agents/reviewer.md`
2. `<cwd>/agents/reviewer.md`
3. `<cwd>/.agent-mux/agents/reviewer.md`
4. `~/.agent-mux/agents/reviewer.md`

```markdown
---
engine: claude
model: claude-opus-4-6
effort: high
timeout: 900
skills:
  - web-search
---
You are a senior code reviewer. Focus on correctness, edge cases, and test coverage.
```

If `reviewer.toml` exists beside `reviewer.md`, it loads as a config overlay -- the profile can bring its own roles and hooks. When profile, `system_prompt_file`, and inline `system_prompt` coexist, they compose in order: role prompt file, then profile body, then inline text.

In `--stdin` mode, `"coordinator"` is a legacy alias for `"profile"`. Both set the same field; providing both with different values is an error.

---

## Skills Configuration

Skills are SKILL.md files discovered by name from search paths.

```toml
[skills]
search_paths = [
  "~/.claude/skills",
  "~/thinking/pratchett-os/coordinator/.claude/skills",
]
```

Search paths from all config layers are union-merged with dedup. Each skill is resolved as `<search_root>/<name>/SKILL.md`. First match wins across all search roots.

Discover available skills:

```bash
agent-mux config skills           # tabular: NAME, PATH, SOURCE
agent-mux config skills --json    # JSON array
```

File-based persistence for dispatch records:

```bash
agent-mux config                  # full resolved config as JSON
agent-mux config --sources        # just the loaded config file paths
agent-mux config roles            # tabular role/variant table
agent-mux config models           # model lists per engine
```

---

## Hooks

Pattern-based deny/warn rules on prompts and harness events.

```toml
[hooks]
deny = ["DROP TABLE", "vault.sh export"]
warn = ["rm -rf", "git push --force", "curl", "wget"]
event_deny_action = "kill"   # "kill" kills dispatch; "warn" injects caution text
```

**Limitation:** Event-level matching can false-positive during harness orientation (e.g., Codex reading workspace files containing denied patterns). Prompt-level deny is reliable; event-level deny is experimental.

---

## Common Patterns

**Symlinking config to version control:**

```bash
ln -s /path/to/repo/coordinator/.agent-mux ~/.agent-mux
```

One source of truth, globally available. Repo changes are immediately live.

**Per-project model overrides** -- global sets defaults, project overrides only what differs:

```toml
# Project .agent-mux/config.toml
[defaults]
model = "gpt-5.4-mini"   # cost control for this project
```

**Sharing roles across a team:** Check `.agent-mux/` into the repo. Team members get the same roles, prompts, and skill paths on clone. Global config holds personal preferences; project config holds shared definitions.
