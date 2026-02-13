---
name: agent-mux
description: |
  Unified subagent system — three engines (Codex, Claude Code, OpenCode) through a single
  entry point. One CLI, one output contract, three SDKs.

  Engines:
  - codex: OpenAI Codex (GPT-5.3-Codex) — code-focused execution
  - claude: Claude Code (Sonnet 4) — complex reasoning, architecture
  - opencode: 170+ models via OpenRouter — Kimi, GLM, DeepSeek, Qwen, free tiers
---

# agent-mux

Single entry point for all three subagent engines. One CLI, one output contract, three SDKs.

```bash
agent-mux --engine <codex|claude|opencode> [options] "prompt"
```

> **Invocation:** If you ran `bun link` inside the repo, use `agent-mux` directly. Otherwise, use `bun run $SKILL_DIR/src/agent.ts` where `$SKILL_DIR` is the install location (e.g. `~/.claude/skills/agent-mux`).

---

## First Time Setup

```bash
cd ~/.claude/skills/agent-mux   # or wherever you cloned it
./setup.sh
```

`setup.sh` checks Bun, installs dependencies, verifies TypeScript compilation, copies the MCP cluster config template, and reports API key status. Safe to re-run.

To register the `agent-mux` command globally:

```bash
bun link
```

---

## Quick Reference

```bash
# Codex — code review, implementation, debugging
agent-mux --engine codex --cwd /path/to/repo --reasoning high "Review auth flow in src/auth/"

# Claude — architecture, writing, complex reasoning
agent-mux --engine claude --cwd /path/to/repo --effort high "Design the API schema for..."

# OpenCode — third perspective, free tier, model diversity
agent-mux --engine opencode --model kimi --effort high "Verify the implementation in..."

# Full access (writes + network for Codex, bypassPermissions for Claude)
agent-mux --engine codex --full --cwd /path/to/repo "Install deps and implement feature"

# With MCP clusters
agent-mux --engine codex --browser --cwd /path "Navigate to site and extract data"
agent-mux --engine claude --mcp-cluster knowledge "Search KB for auth docs"
```

---

## CLI Flags

### Common (all engines)

| Flag | Short | Values | Default | Notes |
|------|-------|--------|---------|-------|
| `--engine` | `-E` | `codex`, `claude`, `opencode` | required | Which SDK to use |
| `--cwd` | `-C` | path | current dir | Working directory |
| `--model` | `-m` | string | engine-specific | Model identifier |
| `--effort` | `-e` | `low`, `medium`, `high`, `xhigh` | `medium` | Scales timeout |
| `--timeout` | `-t` | milliseconds | effort-scaled | Override timeout |
| `--system-prompt` | `-s` | text | none | Appended system prompt |
| `--mcp-cluster` | — | string (repeatable) | none | Enable MCP cluster |
| `--browser` | `-b` | boolean | false | Sugar for `--mcp-cluster browser` |
| `--full` | `-f` | boolean | false | Full access mode |
| `--version` | `-V` | boolean | — | Show version |
| `--help` | `-h` | boolean | — | Show help |

### Codex-specific

| Flag | Short | Values | Default |
|------|-------|--------|---------|
| `--sandbox` | — | `read-only`, `workspace-write`, `danger-full-access` | `read-only` |
| `--reasoning` | `-r` | `minimal`, `low`, `medium`, `high`, `xhigh` | `medium` |
| `--network` | `-n` | boolean | false |
| `--add-dir` | `-d` | path (repeatable) | none |

### Claude-specific

| Flag | Short | Values | Default |
|------|-------|--------|---------|
| `--permission-mode` | `-p` | `default`, `acceptEdits`, `bypassPermissions`, `plan` | `bypassPermissions` |
| `--max-turns` | — | number | effort-scaled |
| `--max-budget` | — | USD | none |
| `--allowed-tools` | — | comma-separated | none |

### OpenCode-specific

| Flag | Short | Values | Default |
|------|-------|--------|---------|
| `--variant` | — | model preset name | none |
| `--agent` | — | OpenCode agent name | none |

**OpenCode model presets:** `kimi`, `kimi-k2.5`, `glm`, `glm-5`, `deepseek`, `deepseek-r1`, `qwen`, `qwen-coder`, `free`

---

## Output Contract

All engines produce identical JSON on stdout:

```json
{
  "success": true,
  "engine": "codex",
  "response": "The agent's text output...",
  "timed_out": false,
  "duration_ms": 12345,
  "activity": {
    "files_changed": ["src/foo.ts"],
    "commands_run": ["npm test"],
    "files_read": ["src/bar.ts"],
    "mcp_calls": ["docs-search/search"],
    "heartbeat_count": 3
  },
  "metadata": {
    "model": "gpt-5.3-codex",
    "tokens": { "input": 5000, "output": 1200 },
    "cost_usd": 0.05,
    "session_id": "...",
    "turns": 3
  }
}
```

Error codes: `INVALID_ARGS`, `MISSING_API_KEY`, `SDK_ERROR`

```json
{
  "success": false,
  "engine": "codex",
  "error": "Error message",
  "code": "SDK_ERROR",
  "duration_ms": 500,
  "activity": { "files_changed": [], "commands_run": [], "files_read": [], "mcp_calls": [], "heartbeat_count": 0 }
}
```

**Key design:** `timed_out: true` with `success: true` = partial results. Activity log preserved even on timeout.

---

## Timeout & Effort

| Effort | Timeout | Use Case |
|--------|---------|----------|
| `low` | 2 min | Quick checks, smoke tests |
| `medium` | 10 min | Routine tasks |
| `high` | 20 min | Implementation, review |
| `xhigh` | 40 min | Deep analysis, architecture |

---

## Heartbeat Protocol

Every 15 seconds, the agent writes to stderr:
```
[heartbeat] 45s — processing file changes
```

This keeps parent processes from timing out on long-running tasks. SDK noise is suppressed; only heartbeat lines pass through.

---

## MCP Clusters

MCP clusters are loaded from a YAML config file. No MCP servers enabled by default.

**Config file search order:**
1. `./mcp-clusters.yaml` (project-local)
2. `~/.config/agent-mux/mcp-clusters.yaml` (user-global)

See `mcp-clusters.example.yaml` for the schema.

---

## Engine Selection Guide

| Need | Engine | Why |
|------|--------|-----|
| Code review, audit | codex | Focused, sandbox isolation |
| Implementation | codex | Strong at code generation |
| Architecture, design | claude | Better reasoning, context |
| Writing, documentation | claude | Language strength |
| Third opinion | opencode | Different model family |
| Cost-effective check | opencode `--model free` | Zero-cost verification |
| Multi-model pipeline | All three | Maximum blind spot coverage |

---

## Prompting Guide by Engine

Each engine has different prompting needs. Using Claude-style prompts on Codex causes silent failures.

### Prompting Codex (GPT-5.3)

Codex operates in a sandbox with finite context. Every token spent reading files is a token NOT spent writing output.

**The golden rule:** Tell Codex WHAT to read, WHAT to check, and WHERE to write. Never say "explore" or "audit everything."

**Prompt structure that works:**
1. State the goal in one sentence
2. List specific files to read
3. Define the output format
4. Constrain the scope

**What works:**
- Batch file reads in parallel
- Bias toward action — "deliver working code, not just a plan"
- One clear deliverable per invocation
- Explicit output path
- LOC limits and style constraints

**What fails:**
- "Audit the entire codebase" — burns tokens reading files, produces nothing
- "Read all files and design architecture" — too exploratory
- Upfront planning announcements — causes premature stopping
- Multi-goal prompts — causes partial completion

### Prompting Claude

**What works:**
- More exploratory and open-ended (Claude handles ambiguity well)
- Multi-goal when needed (Claude manages complexity)
- Writing-focused (Claude's prose quality is highest)

### Prompting OpenCode / GLM-5

**What works:**
- End-to-end deliverable framing
- Richer context than Codex prompts
- Structured output requests
- Chinese/multilingual content

**Model selection within OpenCode:**

| Model | Best For | Avoid For |
|-------|----------|-----------|
| `glm-5` | Complex agentic tasks, tool-heavy workflows | Quick checks, creative writing |
| `kimi` | Long-context analysis, general reasoning | Chinese-specific tasks |
| `deepseek-r1` | Code reasoning, algorithm analysis | Document generation |
| `qwen-coder` | Code-focused tasks, test generation | Non-code tasks |
| `free` | Smoke tests, cost-free validation | Production-critical decisions |

### Three-Way Comparison

| Aspect | Claude (Opus) | Codex (GPT-5.3) | GLM-5 |
|--------|--------------|-----------------|-------|
| Exploration | Handles open-ended | Needs tight scope | Moderate |
| Tool calling | Good | Good | Excellent |
| Context window | 200K | Moderate | 200K in / 128K out |
| English writing | Excellent | Good | Adequate |
| Planning | Excellent | Skip planning | Good autonomous |

---

## Files

- **Entry point:** `src/agent.ts`
- **Core (CLI, heartbeat, output):** `src/core.ts`
- **Types:** `src/types.ts`
- **MCP clusters:** `src/mcp-clusters.ts`
- **Codex engine:** `src/engines/codex.ts`
- **Claude engine:** `src/engines/claude.ts`
- **OpenCode engine:** `src/engines/opencode.ts`
