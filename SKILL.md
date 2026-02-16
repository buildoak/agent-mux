---
name: agent-mux
description: |
  Unified subagent system — three engines (Codex, Claude Code, OpenCode) through a single
  entry point. One CLI, one output contract, three SDKs.

  Engines:
  - codex: OpenAI Codex (GPT-5.3-Codex) — code-focused execution
  - claude: Claude Code (Opus 4.6) — complex reasoning, architecture
  - opencode: 170+ models via OpenRouter — Kimi K2.5, GLM 5, MiniMax M2.5
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
# Codex — precise execution, code review, debugging
agent-mux --engine codex --cwd /path/to/repo --reasoning high "Review auth flow in src/auth/"

# Codex Spark — fast iteration, filesystem scanning, focused tasks (1000+ tok/s)
agent-mux --engine codex --model gpt-5.3-codex-spark --reasoning high --cwd /path/to/repo "Generate docstrings for all functions in src/utils/"

# Claude — architecture, orchestration, open-ended exploration
agent-mux --engine claude --cwd /path/to/repo --effort high "Design the API schema for..."

# OpenCode — third-opinion verification, different model lineage
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
| `--skill` | — | string (repeatable) | none | Load skill from `<cwd>/.claude/skills/<name>/SKILL.md` |
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

**Codex model variants:**
- `gpt-5.3-codex` (default) — full-capability Codex. Thorough, pedantic, strong on complex multi-step tasks.
- `gpt-5.3-codex-spark` — 1000+ tok/s on Cerebras WSE-3. 128K context (smaller). Equivalent on straightforward coding (SWE-Bench Pro: 56% vs 56.8%), weaker on complex tasks (Terminal-Bench: 58.4% vs 77.3%). Use `--model gpt-5.3-codex-spark`.

**Reasoning level guidance:**
- `high` — sweet spot for implementation. Fast, detail-oriented, reliable.
- `xhigh` — deep audits and architecture only. Overthinks routine tasks.
- `minimal`/`low` — quick checks. Note: `minimal` is incompatible with MCP tools (Codex rejects them).

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

**OpenCode model presets:** `kimi`, `kimi-k2.5`, `glm`, `glm-5`, `deepseek`, `deepseek-r1`, `qwen`, `qwen-coder`, `qwen-max`, `free`
Additional: `kimi-free`, `glm-free` (free-tier variants), `opencode-minimax`, `opencode-kimi` (OpenCode native)

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

### Bundled: agent-browser MCP (25 tools)

agent-mux includes a built-in agent-browser MCP wrapper at `src/mcp-servers/agent-browser.mjs`. It provides 25 browser automation tools with interactive snapshot mode (`-i` flag) for 5-10x token savings.

```yaml
# In mcp-clusters.yaml
clusters:
  browser:
    servers:
      agent-browser:
        command: node
        args:
          - ./src/mcp-servers/agent-browser.mjs
```

Requires `agent-browser` CLI installed separately.

## Skills

Skills are injectable prompt packages that live in `<cwd>/.claude/skills/<name>/`. Each skill has a `SKILL.md` file whose content is prepended to the worker's prompt.

```bash
# Single skill
agent-mux --engine codex --skill pratchett-read "Search for auth docs"

# Multiple skills
agent-mux --engine codex --skill pratchett-read --skill pratchett-write "Migrate docs"
```

**How it works:**
1. Resolves `<cwd>/.claude/skills/<name>/SKILL.md`
2. Prepends SKILL.md content to the prompt (wrapped in `<skill>` tags)
3. If `<skill-dir>/scripts/` exists, adds it to PATH
4. For Codex: adds the skill directory to `--add-dir` for sandbox read access

---

## Engine Selection Guide

| Need | Engine | Model | Why |
|------|--------|-------|-----|
| Code review, audit | codex | default | Sandbox isolation, pedantic attention to detail |
| Deep architecture audit | codex | default (`xhigh`) | Thorough, catches edge cases |
| Implementation | codex | default (`high`) | Precise executor, detail-oriented |
| Fast iteration, refactoring | codex | spark | 1000+ tok/s, fast feedback loops |
| Filesystem scanning, docstrings | codex | spark | Speed advantage for broad reads |
| Architecture, design | claude | — | Best reasoning, thrives on ambiguity |
| Writing, documentation | claude | — | Highest prose quality |
| Open-ended exploration | claude | — | Handles uncertainty, decides from available info |
| Prompt crafting for pipelines | claude | — | Natural orchestrator |
| Third opinion (agentic) | opencode | `glm-5` | Strong agentic engineering, tool-calling |
| Third opinion (long-context) | opencode | `kimi` | 262K context, multimodal, agent swarm |
| Third opinion (cost-effective) | opencode | `opencode-minimax` | 80% SWE-bench, absurd value |
| Smoke test, zero-cost check | opencode | `free` | Free-tier models, basic validation |
| Multi-model pipeline | All three | — | Maximum blind spot coverage |

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

### Prompting Codex Spark

Spark is 15x faster but has a smaller context window (128K) and weaker performance on complex multi-step tasks. Same prompting discipline as regular Codex, tighter scope.

**What works:**
- Focused, single-goal tasks (even more important than regular Codex)
- Fast iteration cycles — submit, review, refine
- Filesystem-wide reads where speed matters (scanning, generating docs)
- Parallel batch operations (multiple Spark workers on independent subtasks)

**What fails:**
- Complex multi-file refactors (use regular Codex)
- Tasks requiring deep reasoning or architecture (use regular Codex `xhigh`, or Claude)
- Large context requirements beyond 128K (use regular Codex or Claude)

**When to use Spark vs Regular Codex:**

| Factor | Spark | Regular Codex |
|--------|-------|---------------|
| Task complexity | Straightforward to medium | Hard, multi-step |
| Context needed | <128K | >128K |
| Iteration speed | Critical | Not critical |
| Multi-file refactor | No | Yes |
| Parallel workers | Yes (fast) | Yes (thorough) |

### Prompting Claude (Opus 4.6)

**What works:**
- Open-ended exploration (Claude thrives on ambiguity — decides from available info)
- Multi-goal when needed (Claude manages complexity)
- Writing-focused (Claude's prose quality is highest)
- Prompt crafting for other engines (natural orchestrator)
- Architecture and system design where tradeoffs need reasoning

### Prompting OpenCode Models

**What works:**
- End-to-end deliverable framing (not discussions)
- Richer context than Codex prompts (especially GLM-5)
- Structured output requests for pipeline integration
- Chinese/multilingual content

**Model selection within OpenCode:**

| Model | Preset | Pricing (per M tokens) | Best For | Avoid For |
|-------|--------|------------------------|----------|-----------|
| Kimi K2.5 | `kimi` | $0.45 in / $2.25 out | Long-context (262K), multimodal, agent swarm, visual coding | Cost-sensitive runs |
| GLM-5 | `glm-5` | $0.80 in / $2.56 out | Agentic engineering, tool-heavy workflows, self-correction | Quick checks, creative writing |
| MiniMax M2.5 | `opencode-minimax` | Free (native) | Cost-effective coding verification (80% SWE-bench) | Deep architectural reasoning |
| DeepSeek R1 | `deepseek-r1` | Free | Code reasoning, algorithm analysis | Document generation |
| Qwen Coder | `qwen-coder` | varies | Code-focused tasks, test generation | Non-code tasks |
| Free tier | `free` | $0 | Smoke tests, zero-cost validation | Production-critical decisions |

**Note:** Most OpenCode models incur per-token costs via OpenRouter. Only `free`, `deepseek-r1`, `kimi-free`, `glm-free`, and `opencode-*` presets are zero-cost. Budget accordingly for named models.

### Engine Comparison

| Aspect | Claude (Opus 4.6) | Codex (5.3) | Codex Spark | OpenCode (varies) |
|--------|-------------------|-------------|-------------|-------------------|
| Speed | ~65-70 tok/s | ~65-70 tok/s | 1000+ tok/s | Varies |
| Context window | 1M (beta) | Standard | 128K | 200-262K |
| Exploration | Thrives on ambiguity | Needs tight scope | Needs tight scope | Moderate (GLM-5 handles autonomy) |
| Tool calling | Good | Good | Good (tends to over-tool) | Excellent (GLM-5, Kimi) |
| Planning | Natural orchestrator | Skip planning, bias to action | Skip planning, bias to speed | Good autonomous (GLM-5) |
| Coding (SWE-bench) | 72-81% | Frontier | ~equivalent on SWE-bench Pro | 77-80% (GLM-5, MiniMax) |
| Prompting style | Open-ended, multi-goal OK | One goal, explicit files, action-biased | Same as Codex, simpler tasks | End-to-end deliverables |
| Cost model | Subscription (Max plan) | Subscription (Pro) | Subscription (Pro) | Per-token via OpenRouter |

---

## Files

- **Entry point:** `src/agent.ts`
- **Core (CLI, heartbeat, output):** `src/core.ts`
- **Types:** `src/types.ts`
- **MCP clusters:** `src/mcp-clusters.ts`
- **Codex engine:** `src/engines/codex.ts`
- **Claude engine:** `src/engines/claude.ts`
- **OpenCode engine:** `src/engines/opencode.ts`
