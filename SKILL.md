---
name: agent-mux
description: |
  Unified subagent dispatch layer for Codex, Claude Code, and OpenCode engines.
  Spawn workers, run parallel execution pipelines, and get one strict JSON output contract.
  Use this skill when you need to: dispatch a subagent, spawn an agent worker,
  run multi-model pipelines, invoke codex/claude/opencode engines, or coordinate
  parallel execution across AI coding engines. Covers unified output parsing,
  timeout/heartbeat behavior, skill injection, and MCP cluster configuration.
  Keywords: subagent, dispatch, worker, codex, claude, opencode, parallel execution,
  multi-model, spawn agent, engine, unified output.
---

# agent-mux

One CLI for Codex, Claude, and OpenCode with one strict JSON contract.

```bash
agent-mux --engine <codex|claude|opencode> [options] "prompt"
```

> If `agent-mux` is not linked globally, run `bun run src/agent.ts ...` from this repo.

---

## First Time Setup

```bash
cd /path/to/agent-mux && ./setup.sh && bun link
```

---

## How to install this skill

Pick one option below. Option 1 is fastest if you already have an AI coding agent running.

### Option 1: Tell your AI agent (easiest)

Paste this into your AI agent chat:

> Install the agent-mux skill from https://github.com/buildoak/agent-mux

The agent will read this `SKILL.md` and install it for your environment.

### Option 2: Clone and copy

```bash
# 1. Clone the repo
git clone https://github.com/buildoak/agent-mux.git /tmp/agent-mux

# 2A. Claude Code: copy this skill folder into your project
mkdir -p /path/to/your-project/.claude/skills
cp -R /tmp/agent-mux /path/to/your-project/.claude/skills/agent-mux

# 2B. Codex CLI: Codex reads AGENTS.md only
touch /path/to/your-project/AGENTS.md
{
  echo
  echo "<!-- fieldwork-skill:agent-mux -->"
  cat /tmp/agent-mux/SKILL.md
} >> /path/to/your-project/AGENTS.md

# 3. Run setup (Bun runtime install + typecheck)
cd /tmp/agent-mux && ./setup.sh
```

### Option 3: Download just this skill

```bash
# 1. Download and extract the repo zip
curl -L -o /tmp/agent-mux.zip https://github.com/buildoak/agent-mux/archive/refs/heads/main.zip
unzip -q /tmp/agent-mux.zip -d /tmp

# 2A. Claude Code: copy this skill folder into your project
mkdir -p /path/to/your-project/.claude/skills
cp -R /tmp/agent-mux-main /path/to/your-project/.claude/skills/agent-mux

# 2B. Codex CLI: Codex reads AGENTS.md only
touch /path/to/your-project/AGENTS.md
{
  echo
  echo "<!-- fieldwork-skill:agent-mux -->"
  cat /tmp/agent-mux-main/SKILL.md
} >> /path/to/your-project/AGENTS.md

# 3. Run setup (Bun runtime install + typecheck)
cd /tmp/agent-mux-main && ./setup.sh
```

For Codex CLI, do not use `codex.md` or `.codex/skills/`. Root `AGENTS.md` is the only instruction source.

For a detailed installation walkthrough, see [references/installation-guide.md](references/installation-guide.md).

---

## Quick Reference

```bash
# Codex: implementation, debugging, concrete code changes
agent-mux --engine codex --cwd /repo --reasoning high --effort high "Implement retries in src/http/client.ts"

# Codex Spark: fast grunt work and broad scan/edit tasks
agent-mux --engine codex --model gpt-5.3-codex-spark --cwd /repo --reasoning high "Add doc comments across src/"

# Claude: architecture, reasoning, synthesis
agent-mux --engine claude --cwd /repo --effort high --permission-mode bypassPermissions "Design rollout plan for auth refactor"

# OpenCode: third opinion, model diversity, cost-flexible checks
agent-mux --engine opencode --cwd /repo --model kimi "Review this patch and challenge assumptions"

# Skill injection (repeatable)
agent-mux --engine codex --cwd /repo --skill react --skill test-writer "Implement + test dark mode"

# MCP clusters
agent-mux --engine claude --cwd /repo --mcp-cluster knowledge "Find canonical docs for token rotation"

# --browser sugar for browser cluster
agent-mux --engine codex --cwd /repo --browser "Open app, inspect controls, summarize findings"

# Full access mode
agent-mux --engine codex --cwd /repo --full "Install deps and implement requested fix"
```

---

## Engine Selection Protocol

Use this decision tree:

1. Code execution, file edits, implementation -> **Codex** with `--reasoning high`.
2. Fast grunt work, filesystem scanning, parallel worker throughput -> **Codex Spark** with `--model gpt-5.3-codex-spark`.
3. Architecture, deep reasoning, multi-file analysis, synthesis, writing -> **Claude**.
4. Model diversity, third-opinion checks, cost-flexible runs -> **OpenCode**.

_Note: Pratchett-OS coordinator uses Codex + Claude only._

> For engine-specific prompting tips, model variants, and comparison tables, see [references/prompting-guide.md](references/prompting-guide.md).

---

## CLI Flags

Source of truth: `src/core.ts` (`parseCliArgs`) + `src/types.ts`.

### Common (all engines)

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--engine` | `-E` | string | `codex`, `claude`, `opencode` | required | Engine selector |
| `--cwd` | `-C` | string | path | current directory | Working directory |
| `--model` | `-m` | string | model id | engine default | Model override |
| `--effort` | `-e` | string | `low`, `medium`, `high`, `xhigh` | `medium` | Effort level |
| `--timeout` | `-t` | string | positive integer (ms) | effort-mapped | Hard timeout override |
| `--system-prompt` | `-s` | string | text | unset | Appended system context |
| `--skill` | -- | string[] | repeatable names | `[]` | Loads `<cwd>/.claude/skills/<name>/SKILL.md` |
| `--mcp-cluster` | -- | string[] | repeatable names | `[]` | Enables MCP cluster(s) |
| `--browser` | `-b` | boolean | true/false | `false` | Adds `browser` cluster |
| `--full` | `-f` | boolean | true/false | `false` | Full access mode |
| `--version` | `-V` | boolean | true/false | `false` | Print version |
| `--help` | `-h` | boolean | true/false | `false` | Print help |

### Codex-specific

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--sandbox` | -- | string | `read-only`, `workspace-write`, `danger-full-access` | `read-only` | `--full` forces `danger-full-access` |
| `--reasoning` | `-r` | string | `minimal`, `low`, `medium`, `high`, `xhigh` | `medium` | Model reasoning effort |
| `--network` | `-n` | boolean | true/false | `false` | `--full` forces `true` |
| `--add-dir` | `-d` | string[] | repeatable paths | `[]` | Additional writable dirs |

### Claude-specific

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--permission-mode` | `-p` | string | `default`, `acceptEdits`, `bypassPermissions`, `plan` | `bypassPermissions` | `--full` also resolves to `bypassPermissions` |
| `--max-turns` | -- | string | positive integer | effort-derived if unset | Parsed to number when valid |
| `--max-budget` | -- | string | positive number (USD) | unset | Parsed to `maxBudgetUsd` |
| `--allowed-tools` | -- | string | comma-separated tool list | unset | Split into string array |

### OpenCode-specific

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--variant` | -- | string | preset/model string | unset | Used if `--model` absent |
| `--agent` | -- | string | agent name | unset | OpenCode agent selection |

### Canonical enum values (from `src/types.ts`)

- Engine names: `codex`, `claude`, `opencode`
- Effort levels: `low`, `medium`, `high`, `xhigh`

> For timeout/effort mapping, sandbox modes, and permission details, see [references/engine-comparison.md](references/engine-comparison.md).

---

## Output Contract

All engines emit one JSON payload to `stdout`. Parse JSON, never text.

Success shape: `{ success: true, engine, response, timed_out, duration_ms, activity, metadata }`
Error shape: `{ success: false, engine, error, code, duration_ms, activity }`

Error codes: `INVALID_ARGS`, `MISSING_API_KEY`, `SDK_ERROR`.

Heartbeat: every 15s on `stderr` (`[heartbeat] 45s -- processing`). `stdout` is reserved for final JSON.

> For full JSON schema, field descriptions, and examples, see [references/output-contract.md](references/output-contract.md).

---

## Skills

Use `--skill <name>` (repeatable). Resolves from `<cwd>/.claude/skills/<name>/SKILL.md`.

- Skill content prepended as `<skill>` XML blocks
- If `<skillDir>/scripts` exists, prepended to `PATH`
- For Codex, skill directories auto-appended to sandbox `addDirs`
- Path traversal names are rejected

---

## MCP Clusters

Config search: `./mcp-clusters.yaml` then `~/.config/agent-mux/mcp-clusters.yaml`.

`--mcp-cluster` is repeatable. `all` merges all clusters. `--browser` is sugar for `--mcp-cluster browser`.

Bundled server: `src/mcp-servers/agent-browser.mjs` (browser automation).

See `mcp-clusters.example.yaml` for config format.

---

## Bundled Resources Index

| Path | What | When to load |
| --- | --- | --- |
| `references/output-contract.md` | Full output schema, examples, field descriptions | Parsing agent output, debugging response shape |
| `references/prompting-guide.md` | Engine-specific prompting tips, model variants, comparison | Crafting prompts for specific engines |
| `references/engine-comparison.md` | Detailed engine table, timeouts, sandbox/permission modes | Choosing engine config, debugging options |
| `src/agent.ts` | CLI entrypoint and adapter dispatch | Trace invocation path |
| `src/core.ts` | parseCliArgs, timeout, heartbeat, output assembly | Always for behavior truth |
| `src/types.ts` | Canonical engine/effort/output types | Always for contract truth |
| `src/mcp-clusters.ts` | MCP config discovery and merge logic | MCP cluster setup/debug |
| `src/engines/codex.ts` | Codex adapter | Codex option/event behavior |
| `src/engines/claude.ts` | Claude adapter | Claude permissions/turn behavior |
| `src/engines/opencode.ts` | OpenCode adapter + model presets | OpenCode model routing |
| `src/mcp-servers/agent-browser.mjs` | Bundled browser MCP wrapper | Browser automation integration |
| `setup.sh` | Bootstrap script | First install or environment repair |
| `mcp-clusters.example.yaml` | Starter MCP config | Creating cluster config |
| `CHANGELOG.md` | Release history | Verify version-specific behavior |
| `tests/` | Test suite | Validate changes/regressions |

---

## Anti-Patterns

- Do not use `--sandbox danger-full-access` unless explicitly authorized.
- Do not parse agent-mux output as text. Always parse JSON from stdout.
- Do not run parallel browser workers. One browser session at a time.
- Do not read agent output files with full Read; use `tail -n 20` via Bash.
- Do not use `--reasoning minimal` with MCP tools (Codex rejects them).
- Do not send exploration tasks to Codex; use Claude for open-ended work.
- Do not use `xhigh` effort for routine tasks; `high` is the workhorse.

---

## Staying Updated

This skill ships with an `UPDATES.md` changelog and `UPDATE-GUIDE.md` for your AI agent.

After installing, tell your agent: "Check `UPDATES.md` in the agent-mux skill for any new features or changes."

When updating, tell your agent: "Read `UPDATE-GUIDE.md` and apply the latest changes from `UPDATES.md`."

Follow `UPDATE-GUIDE.md` so customized local files are diffed before any overwrite.
