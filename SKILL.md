---
name: agent-mux
description: |
  Unified subagent execution layer for Codex, Claude Code, and OpenCode.
  One CLI surface, one JSON output contract, strict timeout/heartbeat behavior,
  and predictable skill + MCP injection for automation workflows.
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
cd /path/to/agent-mux
./setup.sh
bun link
```

`setup.sh` validates Bun/deps and local build prerequisites. `bun link` exposes `agent-mux` globally.

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
| `--skill` | — | string[] | repeatable names | `[]` | Loads `<cwd>/.claude/skills/<name>/SKILL.md` |
| `--mcp-cluster` | — | string[] | repeatable names | `[]` | Enables MCP cluster(s) |
| `--browser` | `-b` | boolean | true/false | `false` | Adds `browser` cluster |
| `--full` | `-f` | boolean | true/false | `false` | Full access mode |
| `--version` | `-V` | boolean | true/false | `false` | Print version |
| `--help` | `-h` | boolean | true/false | `false` | Print help |

### Codex-specific

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--sandbox` | — | string | `read-only`, `workspace-write`, `danger-full-access` | `read-only` | `--full` forces `danger-full-access` |
| `--reasoning` | `-r` | string | `minimal`, `low`, `medium`, `high`, `xhigh` | `medium` | Model reasoning effort |
| `--network` | `-n` | boolean | true/false | `false` | `--full` forces `true` |
| `--add-dir` | `-d` | string[] | repeatable paths | `[]` | Additional writable dirs |

### Claude-specific

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--permission-mode` | `-p` | string | `default`, `acceptEdits`, `bypassPermissions`, `plan` | `bypassPermissions` | `--full` also resolves to `bypassPermissions` |
| `--max-turns` | — | string | positive integer | effort-derived if unset | Parsed to number when valid |
| `--max-budget` | — | string | positive number (USD) | unset | Parsed to `maxBudgetUsd` |
| `--allowed-tools` | — | string | comma-separated tool list | unset | Split into string array |

### OpenCode-specific

| Flag | Short | Type | Values | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--variant` | — | string | preset/model string | unset | Used if `--model` absent |
| `--agent` | — | string | agent name | unset | OpenCode agent selection |

### Canonical enum values from `src/types.ts`

- Engine names: `codex`, `claude`, `opencode`
- Effort levels: `low`, `medium`, `high`, `xhigh`

---

## Output Contract

All engines emit one JSON payload to `stdout`.

### Success example

```json
{
  "success": true,
  "engine": "codex",
  "response": "Implemented retries and added tests.",
  "timed_out": false,
  "duration_ms": 84231,
  "activity": {
    "files_changed": ["src/http/client.ts"],
    "commands_run": ["bun test"],
    "files_read": ["src/http/types.ts"],
    "mcp_calls": ["docs-search/search"],
    "heartbeat_count": 5
  },
  "metadata": {
    "model": "gpt-5.3-codex",
    "session_id": "sess_...",
    "cost_usd": 0.18,
    "tokens": { "input": 12840, "output": 2104, "reasoning": 512 },
    "turns": 4
  }
}
```

### Error example

```json
{
  "success": false,
  "engine": "codex",
  "error": "--engine is required. Use: codex, claude, opencode",
  "code": "INVALID_ARGS",
  "duration_ms": 0,
  "activity": {
    "files_changed": [],
    "commands_run": [],
    "files_read": [],
    "mcp_calls": [],
    "heartbeat_count": 0
  }
}
```

### Full JSON Schema (copied from `README.md`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "agent-mux output",
  "oneOf": [
    {
      "type": "object",
      "description": "Successful run (including timeout with partial results).",
      "required": ["success", "engine", "response", "timed_out", "duration_ms", "activity", "metadata"],
      "properties": {
        "success": { "const": true, "description": "Always true for success payloads." },
        "engine": { "enum": ["codex", "claude", "opencode"], "description": "Engine used for the run." },
        "response": { "type": "string", "description": "Agent text response. On timeout this can be a placeholder." },
        "timed_out": { "type": "boolean", "description": "True if timeout fired and run was aborted via AbortSignal." },
        "duration_ms": { "type": "number", "description": "End-to-end runtime in milliseconds." },
        "activity": { "$ref": "#/$defs/activity" },
        "metadata": {
          "type": "object",
          "description": "Engine-reported metadata (shape varies by SDK).",
          "properties": {
            "session_id": { "type": "string" },
            "cost_usd": { "type": "number" },
            "tokens": {
              "type": "object",
              "properties": {
                "input": { "type": "number" },
                "output": { "type": "number" },
                "reasoning": { "type": "number" }
              }
            },
            "turns": { "type": "number" },
            "model": { "type": "string" }
          },
          "additionalProperties": true
        }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "description": "Failure payload.",
      "required": ["success", "engine", "error", "code", "duration_ms", "activity"],
      "properties": {
        "success": { "const": false, "description": "Always false for error payloads." },
        "engine": { "enum": ["codex", "claude", "opencode"] },
        "error": { "type": "string", "description": "Human-readable error." },
        "code": { "enum": ["INVALID_ARGS", "MISSING_API_KEY", "SDK_ERROR"], "description": "Failure class." },
        "duration_ms": { "type": "number" },
        "activity": { "$ref": "#/$defs/activity" }
      },
      "additionalProperties": false
    }
  ],
  "$defs": {
    "activity": {
      "type": "object",
      "description": "Structured activity log collected during execution.",
      "required": ["files_changed", "commands_run", "files_read", "mcp_calls", "heartbeat_count"],
      "properties": {
        "files_changed": { "type": "array", "items": { "type": "string" } },
        "commands_run": { "type": "array", "items": { "type": "string" } },
        "files_read": { "type": "array", "items": { "type": "string" } },
        "mcp_calls": { "type": "array", "items": { "type": "string" } },
        "heartbeat_count": { "type": "number", "description": "Heartbeat lines emitted to stderr." }
      },
      "additionalProperties": false
    }
  }
}
```

---

## Timeout & Effort

| Effort | Timeout (ms) | Timeout | Guidance |
| --- | ---: | --- | --- |
| `low` | `120000` | 2m | quick checks |
| `medium` | `600000` | 10m | routine tasks |
| `high` | `1200000` | 20m | workhorse for implementation |
| `xhigh` | `2400000` | 40m | deep analysis only |

---

## Heartbeat Protocol

- Interval: every 15s.
- Channel: `stderr` only.
- Format:

```text
[heartbeat] 45s — processing file changes
```

`stdout` is reserved for final JSON output.

---

## Skills

Use `--skill <name>` (repeatable).

Resolution and safety:
1. Resolve from `<cwd>/.claude/skills/<name>`.
2. Require `<skillDir>/SKILL.md`.
3. Reject path traversal names.

Injection behavior:
- Skill content is prepended as `<skill name="..." source=".../SKILL.md">...</skill>` blocks.
- If `<skillDir>/scripts` exists, it is prepended to `PATH`.
- For Codex, resolved skill directories are auto-appended to internal `addDirs` sandbox access.

---

## MCP Clusters

Config search order:
1. `./mcp-clusters.yaml`
2. `~/.config/agent-mux/mcp-clusters.yaml`

`--mcp-cluster` is repeatable. `all` merges all configured clusters.

Example YAML:

```yaml
clusters:
  browser:
    description: Browser automation
    servers:
      agent-browser:
        command: node
        args:
          - ./src/mcp-servers/agent-browser.mjs

  knowledge:
    description: Docs/search tools
    servers:
      docs-search:
        command: bunx
        args:
          - your-docs-mcp-server
```

`--browser` is sugar for `--mcp-cluster browser`.

Bundled browser server:
- `src/mcp-servers/agent-browser.mjs` (agent-browser MCP wrapper)

---

## Prompting Guide by Engine

### Codex (GPT-5.3)

**The golden rule:** Tell Codex WHAT to read, WHAT to check, and WHERE to write. Never say "explore" or "audit everything."

**What works:** One goal per invocation. Explicit file targets. Concrete deliverables (patches, tests). LOC limits and style constraints. Bias toward action.

**What fails:** "Audit the entire codebase." Multi-goal prompts. Upfront planning announcements (causes premature stopping). Open-ended exploration.

**Model variants:**
- `gpt-5.3-codex` (default) -- thorough, pedantic, strong on complex multi-step tasks
- `gpt-5.3-codex-spark` -- 1000+ tok/s, 128K context, equivalent on SWE-Bench Pro (56% vs 56.8%), weaker on complex tasks (Terminal-Bench: 58.4% vs 77.3%)

**Reasoning levels:** `high` = implementation sweet spot. `xhigh` = deep audits only (overthinks routine work). `minimal` = incompatible with MCP tools.

### Codex Spark

Same prompting discipline as Codex, tighter scope. Use for: parallel workers, filesystem scanning, docstring generation, fast iteration cycles.

Avoid for: complex multi-file refactors, deep reasoning, context beyond 128K.

### Claude (Opus 4.6)

**What works:** Open-ended exploration. Multi-goal when needed. Writing and documentation. Prompt crafting for other engines. Architecture with tradeoff reasoning.

### OpenCode

**What works:** End-to-end deliverable framing. Structured output requests. Cross-checking other engines.

**Key presets:** `kimi` (262K context, multimodal), `glm-5` (agentic engineering, tool-heavy), `opencode-minimax` (free, 80% SWE-bench), `deepseek-r1` (free, code reasoning), `free` (zero-cost smoke tests).

### Engine Comparison

| Aspect | Codex (5.3) | Codex Spark | Claude (Opus 4.6) | OpenCode (varies) |
| --- | --- | --- | --- | --- |
| Speed | ~65-70 tok/s | 1000+ tok/s | ~65-70 tok/s | Varies |
| Context | Standard | 128K | 1M (beta) | 200-262K |
| Prompting | One goal, explicit files | Same, simpler tasks | Open-ended, multi-goal OK | End-to-end deliverables |
| Best for | Implementation, review | Fast grunt work | Architecture, writing | Third opinion, diversity |
| Fails on | Open-ended exploration | Complex multi-step | Drift without constraints | Vague prompts |

---

## Bundled Resources Index

| Path | What | When to load |
| --- | --- | --- |
| `src/agent.ts` | CLI entrypoint and adapter dispatch | Trace invocation path |
| `src/core.ts` | parseCliArgs, timeout, heartbeat, output assembly | Always for behavior truth |
| `src/types.ts` | canonical engine/effort/output types | Always for contract truth |
| `src/mcp-clusters.ts` | MCP config discovery and merge logic | MCP cluster setup/debug |
| `src/engines/codex.ts` | Codex adapter | Codex option/event behavior |
| `src/engines/claude.ts` | Claude adapter | Claude permissions/turn behavior |
| `src/engines/opencode.ts` | OpenCode adapter + model presets | OpenCode model routing |
| `src/mcp-servers/agent-browser.mjs` | Bundled browser MCP wrapper | Browser automation integration |
| `setup.sh` | bootstrap script | First install or environment repair |
| `mcp-clusters.example.yaml` | starter MCP config | Creating cluster config |
| `CHANGELOG.md` | release history | Verify version-specific behavior |
| `tests/` | test suite | Validate changes/regressions |

---

## Anti-Patterns

- Do not use `--sandbox danger-full-access` unless explicitly authorized.
- Do not parse agent-mux output as text. Always parse JSON from stdout.
- Do not run parallel browser workers. One browser session at a time.
- Do not read agent output files with full Read; use `tail -n 20` via Bash.
- Do not use `--reasoning minimal` with MCP tools (Codex rejects them).
- Do not send exploration tasks to Codex; use Claude for open-ended work.
- Do not use `xhigh` effort for routine tasks; `high` is the workhorse.
