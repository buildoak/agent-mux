# agent-mux
[![CI](https://github.com/buildoak/agent-mux/actions/workflows/ci.yml/badge.svg)](https://github.com/buildoak/agent-mux/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Three problems this solves:

1. **Claude Code can't natively use Codex as a subagent.** Claude already has `Task` subagents and is a natural prompt master — it knows how to delegate. But it can't reach Codex or OpenCode out of the box. agent-mux bridges that gap: Claude dispatches Codex workers the same way it dispatches its own subagents.

2. **Codex has no subagent system at all.** No `Task` tool, no nested agents, no orchestration primitives. agent-mux gives Codex the ability to spawn workers across any engine — including Claude — through one CLI command with one JSON contract.

3. **The 10x pattern.** Inside Claude Code's `Task` subagents, you can spawn agent-mux workers. Claude architects the plan, Codex executes the code, a second Claude verifies the result — all within one coordinated pipeline. This is how [gsd-coordinator](https://github.com/buildoak/fieldwork-skills/tree/main/skills/gsd-coordinator) works.

One CLI. One output contract. Any engine. Runtime: Bun only (`#!/usr/bin/env bun`).

## What you get
- **Unified output contract** — all engines return the same JSON shape, no format translation.
- **Skill injection** — load reusable `SKILL.md` runbooks with `--skill`, dispatch through any engine.
- **Heartbeat protocol** — progress signals every 15s on stderr for long-running tasks.
- **Effort-scaled timeouts** — task duration automatically adjusts based on complexity level.

## Prerequisites
**Runtime:** [Bun](https://bun.sh) >= 1.0.0

**API keys** (only the engine you use needs its key):

| Engine | Env Var | Notes |
| --- | --- | --- |
| `codex` | `OPENAI_API_KEY` | API key **or** OAuth device auth via `codex auth` (`~/.codex/auth.json`) |
| `claude` | `ANTHROPIC_API_KEY` | Claude Code SDK also supports device OAuth when no key is set |
| `opencode` | `OPENROUTER_API_KEY` | Or configure provider-specific keys directly in OpenCode |

**MCP clusters** are optional and only needed with `--mcp-cluster`.

## Quick Start
```bash
git clone https://github.com/buildoak/agent-mux && cd agent-mux
./setup.sh
bun run src/agent.ts --engine codex "Review src/core.ts for timeout edge cases"
```

To register the `agent-mux` command globally: `bun link`

## Engines

| Engine | SDK | Best At | Default Model |
| --- | --- | --- | --- |
| `codex` | `@openai/codex-sdk` | Precise implementation and code edits | `gpt-5.3-codex` |
| `claude` | `@anthropic-ai/claude-agent-sdk` | Planning, architecture, long-form reasoning | `claude-sonnet-4-20250514` |
| `opencode` | `@opencode-ai/sdk` | Model diversity and cross-checking | `openrouter/moonshotai/kimi-k2.5` |

## CLI Reference
Prompt is required as positional text:

```bash
agent-mux --engine <codex|claude|opencode> [flags] "your prompt"
```

### Common Flags
| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--engine` | `-E` | `codex`, `claude`, `opencode` | required | Engine selection |
| `--cwd` | `-C` | path | current directory | Working directory |
| `--model` | `-m` | string | engine-specific | Model override |
| `--effort` | `-e` | `low`, `medium`, `high`, `xhigh` | `medium` | Drives default timeout |
| `--timeout` | `-t` | positive integer ms | effort-scaled | Hard timeout override |
| `--system-prompt` | `-s` | string | unset | Appended system prompt |
| `--skill` |  | string (repeatable) | none | Loads `<cwd>/.claude/skills/<name>/SKILL.md` |
| `--mcp-cluster` |  | string (repeatable) | none | Enables named cluster(s) |
| `--browser` | `-b` | boolean | `false` | Sugar for `--mcp-cluster browser` |
| `--full` | `-f` | boolean | `false` | Full-access mode |
| `--help` | `-h` | boolean | `false` | Show help |
| `--version` | `-V` | boolean | `false` | Show version |

Effort defaults:

| Effort | Timeout |
| --- | --- |
| `low` | `120000` ms (2 min) |
| `medium` | `600000` ms (10 min) |
| `high` | `1200000` ms (20 min) |
| `xhigh` | `2400000` ms (40 min) |

### Codex Flags
| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--sandbox` |  | `read-only`, `workspace-write`, `danger-full-access` | `read-only` | `--full` forces `danger-full-access` |
| `--reasoning` | `-r` | `minimal`, `low`, `medium`, `high`, `xhigh` | `medium` | Reasoning effort |
| `--network` | `-n` | boolean | `false` | `--full` forces `true` |
| `--add-dir` | `-d` | path (repeatable) | none | Additional writable dirs |

### Claude Flags
| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--permission-mode` | `-p` | `default`, `acceptEdits`, `bypassPermissions`, `plan` | `bypassPermissions` | `--full` forces `bypassPermissions` |
| `--max-turns` |  | positive integer | effort-scaled (`5/15/30/50`) | Runtime default set by effort |
| `--max-budget` |  | positive number (USD) | unset | Budget cap |
| `--allowed-tools` |  | comma-separated list | unset | Tool whitelist |

### OpenCode Flags
| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--variant` |  | preset/model string | unset | Shorthand model selector |
| `--agent` |  | string | unset | OpenCode agent name |

OpenCode presets include `kimi`, `kimi-k2.5`, `glm`, `glm-5`, `deepseek`, `deepseek-r1`, `qwen`, `qwen-coder`, `qwen-max`, `free`, plus `kimi-free`, `glm-free`, `opencode-kimi`, `opencode-minimax`.

## Output Contract
`agent-mux` returns one unified JSON contract for all engines.

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

## Skill System
`agent-mux` is both a skill host and a skill-style package.

It includes `SKILL.md`, `setup.sh`, and bundled runtime scripts (including MCP wrappers under `src/mcp-servers/`).

Load external skills with repeatable `--skill` flags:

```bash
agent-mux --engine codex --skill reviewer --skill migrations "Review and harden schema migration"
```

Skill resolution path and behavior:
- Reads `<cwd>/.claude/skills/<name>/SKILL.md`.
- Injects each skill into the prompt as a `<skill ...>` block.
- Adds `<skill>/scripts/` to `PATH` if present.
- On Codex, adds each skill directory to writable `--add-dir` paths.

## MCP Clusters
Define clusters in YAML and enable them per run.

Search order:
1. `./mcp-clusters.yaml`
2. `~/.config/agent-mux/mcp-clusters.yaml`

Example:

```yaml
clusters:
  browser:
    description: "Browser automation"
    servers:
      agent-browser:
        command: node
        args:
          - ./src/mcp-servers/agent-browser.mjs
  research:
    description: "Web research"
    servers:
      exa:
        command: bunx
        args: [exa-mcp-server]
        env:
          EXA_API_KEY: "your-api-key"
```

`--browser` is sugar for `--mcp-cluster browser`.

```bash
agent-mux --engine codex --browser "Capture a screenshot"
agent-mux --engine claude --mcp-cluster research "Find OAuth rotation docs"
agent-mux --engine opencode --mcp-cluster all "Cross-check findings"
```

## Installation
### 1) As a Claude Code skill
```bash
git clone https://github.com/buildoak/agent-mux ~/.claude/skills/agent-mux
cd ~/.claude/skills/agent-mux
./setup.sh
```

### 2) As a standalone CLI
```bash
git clone https://github.com/buildoak/agent-mux
cd agent-mux
./setup.sh
bun run src/agent.ts --engine codex "Summarize this repo"
```

## Troubleshooting
**`agent-mux: command not found`**

Run `bun link` in the repo or invoke directly:

```bash
bun run /path/to/agent-mux/src/agent.ts --engine codex "your prompt"
```

**`MISSING_API_KEY` error**

Set the right env var or use SDK auth:

```bash
export OPENAI_API_KEY="sk-..."        # codex
export ANTHROPIC_API_KEY="sk-ant-..." # claude
export OPENROUTER_API_KEY="sk-or-..." # opencode
codex auth
```

**`Unknown MCP cluster: '...'`**

Create config from the template and update it:

```bash
cp mcp-clusters.example.yaml ~/.config/agent-mux/mcp-clusters.yaml
```

**`OpenCode binary not found`**

Install OpenCode CLI and ensure `opencode` is on `PATH`.

**Timeout with no output**

Use `--effort high`/`xhigh` or increase `--timeout`.

**SDK-specific errors**

Inspect stderr and test engine wiring with:

```bash
bun run src/agent.ts --engine codex "Say hello"
```

## Contributing
```bash
git clone https://github.com/buildoak/agent-mux && cd agent-mux
bun install
bun test
bunx tsc --noEmit
```

## License
[MIT](./LICENSE)
