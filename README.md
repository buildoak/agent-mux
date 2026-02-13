# agent-mux

One CLI to spawn Codex, Claude Code, or OpenCode agents.  
Unified output. Proper timeouts. Activity tracking.

Runtime: Bun only (`#!/usr/bin/env bun`).

## Prerequisites

**Runtime:** [Bun](https://bun.sh) >= 1.0.0

**API keys** (only the engine you use needs its key):

| Engine | Env Var | Required? | Notes |
| --- | --- | --- | --- |
| `codex` | `OPENAI_API_KEY` | Yes | [Get one here](https://platform.openai.com/api-keys) |
| `claude` | `ANTHROPIC_API_KEY` | Recommended | Claude Code SDK also supports device OAuth — if no key is set, the SDK will prompt for browser-based auth |
| `opencode` | `OPENROUTER_API_KEY` | Recommended | Or configure provider-specific keys directly in OpenCode |

**MCP clusters** are optional. Only needed if you use `--mcp-cluster` to attach MCP servers at runtime. See [MCP Clusters](#mcp-clusters).

## Quick Start

```bash
git clone https://github.com/buildoak/agent-mux && cd agent-mux
./setup.sh
bun run src/agent.ts --engine codex "Review src/core.ts for timeout edge cases"
```

To use the `agent-mux` command from anywhere:

```bash
cd agent-mux && bun link
agent-mux --engine codex "Review src/core.ts for timeout edge cases"
```

## Why This Exists

Every agent SDK solves the same task differently: different event streams, different output shapes, different tool-call metadata, and different failure behavior. If you are orchestrating multiple agents, you end up writing translation glue before you can build product logic.

Timeouts and long-running task behavior are usually where things break in production: no clean abort path, no heartbeat for supervisors, and no partial payload on timeout. `agent-mux` standardizes this infrastructure layer so you can treat Codex, Claude Code, and OpenCode as interchangeable workers behind one CLI contract.

## Engines

| Engine | SDK | Best At | Default Model |
| --- | --- | --- | --- |
| `codex` | `@openai/codex-sdk` | Code edits, debugging, implementation | `gpt-5.3-codex` |
| `claude` | `@anthropic-ai/claude-agent-sdk` | Architecture, deep reasoning, writing | `claude-sonnet-4-20250514` |
| `opencode` | `@opencode-ai/sdk` | Model diversity, second opinions, cost-flexible runs | `kimi-k2.5` (`openrouter/moonshotai/kimi-k2.5`) |

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

Optionally, register the `agent-mux` command globally:

```bash
bun link
agent-mux --engine codex "Summarize this repo"
```

## Usage

All JSON output is written to `stdout`. Heartbeats are written to `stderr` every 15s.

```text
[heartbeat] 45s — turn started
```

### Codex

```bash
bun run src/agent.ts \
  --engine codex \
  --cwd /path/to/repo \
  --effort high \
  --reasoning high \
  --timeout 900000 \
  "Implement retries for src/http/client.ts and add tests"
```

### Claude Code

```bash
bun run src/agent.ts \
  --engine claude \
  --cwd /path/to/repo \
  --permission-mode bypassPermissions \
  --max-turns 25 \
  "Design a migration plan from REST to GraphQL with rollout stages"
```

### OpenCode

```bash
bun run src/agent.ts \
  --engine opencode \
  --cwd /path/to/repo \
  --variant glm-5 \
  "Find regressions in this PR and propose minimal patches"
```

### Example JSON Output

```json
{
  "success": true,
  "engine": "codex",
  "response": "Implemented retry middleware and added coverage for timeout/backoff paths.",
  "timed_out": false,
  "duration_ms": 84231,
  "activity": {
    "files_changed": ["src/http/client.ts", "test/http/client.test.ts"],
    "commands_run": ["bun test test/http/client.test.ts"],
    "files_read": ["src/http/client.ts", "src/http/types.ts"],
    "mcp_calls": ["docs-search/search"],
    "heartbeat_count": 5
  },
  "metadata": {
    "model": "gpt-5.3-codex",
    "session_id": "sess_...",
    "cost_usd": 0.18,
    "tokens": {
      "input": 12840,
      "output": 2104,
      "reasoning": 512
    },
    "turns": 4
  }
}
```

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

Field notes:
- `success: true` and `timed_out: true` means partial results (not a hard failure).
- On timeout, `response` may be a placeholder and `activity` still contains what happened before abort.
- Timeout is enforced with `AbortSignal`.

## CLI Reference

### Common Flags

| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--engine` | `-E` | `codex`, `claude`, `opencode` | required | Engine selection |
| `--cwd` | `-C` | path | current directory | Working directory |
| `--model` | `-m` | string | engine-specific | Model identifier |
| `--effort` | `-e` | `low`, `medium`, `high`, `xhigh` | `medium` | Effort levels map to timeout |
| `--timeout` | `-t` | ms | effort-scaled | Override timeout directly |
| `--system-prompt` | `-s` | string | unset | Appended system prompt |
| `--mcp-cluster` |  | cluster name (repeatable) | none | Enables cluster(s) |
| `--browser` | `-b` | boolean | `false` | Sugar for `--mcp-cluster browser` |
| `--full` | `-f` | boolean | `false` | Full access mode |
| `--version` | `-V` | boolean | `false` | Show version |
| `--help` | `-h` | boolean | `false` | Show help |

Effort defaults:
- `low` = 120000 ms (2 min)
- `medium` = 600000 ms (10 min)
- `high` = 1200000 ms (20 min)
- `xhigh` = 2400000 ms (40 min)

### Codex Flags

| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--sandbox` |  | `read-only`, `workspace-write`, `danger-full-access` | `read-only` | Sandbox mode |
| `--reasoning` | `-r` | `minimal`, `low`, `medium`, `high`, `xhigh` | `medium` | Codex reasoning effort |
| `--network` | `-n` | boolean | `false` | Enable network access |
| `--add-dir` | `-d` | path (repeatable) | none | Extra writable directories |

### Claude Flags

| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--permission-mode` | `-p` | `default`, `acceptEdits`, `bypassPermissions`, `plan` | `bypassPermissions` | Claude permission mode |
| `--max-turns` |  | integer | effort-scaled (`5/15/30/50`) | Conversation turn cap |
| `--max-budget` |  | number (USD) | unset | Max cost budget |
| `--allowed-tools` |  | comma-separated list | unset | Tool whitelist |

### OpenCode Flags

| Flag | Short | Values | Default | Notes |
| --- | --- | --- | --- | --- |
| `--variant` |  | preset/model string | unset | Variant shorthand |
| `--agent` |  | string | unset | OpenCode agent name |

OpenCode presets:
- `kimi`, `kimi-k2.5`, `glm`, `glm-5`, `deepseek`, `deepseek-r1`, `qwen`, `qwen-coder`, `free`

## MCP Clusters

MCP servers are configured in YAML, then enabled by cluster name at runtime.

Search order:
1. `./mcp-clusters.yaml`
2. `~/.config/agent-mux/mcp-clusters.yaml`

Example schema:

```yaml
clusters:
  browser:
    description: "Browser automation"
    servers:
      agent-browser:
        command: node
        args:
          - /path/to/agent-browser-mcp/server.mjs

  research:
    description: "Web research"
    servers:
      exa:
        command: bunx
        args:
          - exa-mcp-server
        env:
          EXA_API_KEY: "your-api-key"
```

Usage:

```bash
agent-mux --engine codex --mcp-cluster browser "Capture a screenshot of the dashboard"
agent-mux --engine claude --mcp-cluster research "Find docs on OAuth token rotation"
agent-mux --engine opencode --mcp-cluster all "Compare findings from all MCP sources"
```

> If you haven't run `bun link`, replace `agent-mux` with `bun run src/agent.ts`.

## Prompting Guide

Full guide: [`SKILL.md`](./SKILL.md)

### Codex

- Give a tight scope and explicit file targets.
- Ask for concrete deliverables (patches/tests), not exploration.
- Prefer one objective per run.
- Specify output format when integrating downstream.

### Claude Code

- Use for broader reasoning and architecture tradeoffs.
- Provide constraints (budget, turns, tool limits) for predictable runs.
- Ask for structured artifacts (plans, RFC sections, migration checklists).
- Reserve `bypassPermissions` for trusted workflows.

### OpenCode

- Use model presets for fast switching during orchestration.
- Good as a second pass for validation or dissenting review.
- Pick cheaper presets for smoke tests, stronger presets for deep tasks.
- Use `--agent` when your OpenCode setup defines specialized agents.

## Troubleshooting

**`agent-mux: command not found`**

The CLI isn't on your PATH. Either run `bun link` inside the repo to register the command globally, or invoke directly:

```bash
bun run /path/to/agent-mux/src/agent.ts --engine codex "your prompt"
```

**`MISSING_API_KEY` error**

Set the environment variable for the engine you're using:

```bash
export OPENAI_API_KEY="sk-..."        # for codex
export ANTHROPIC_API_KEY="sk-ant-..." # for claude (optional — SDK supports device OAuth)
export OPENROUTER_API_KEY="sk-or-..." # for opencode (optional — or use provider keys)
```

**`Unknown MCP cluster: '...'`**

No `mcp-clusters.yaml` was found, or the cluster name doesn't match. Create one from the example:

```bash
cp mcp-clusters.example.yaml ~/.config/agent-mux/mcp-clusters.yaml
```

Then edit the file to define your clusters. See [MCP Clusters](#mcp-clusters).

**`OpenCode binary not found`**

The OpenCode engine requires the `opencode` CLI to be installed and on your PATH. Install it from [opencode.ai](https://opencode.ai).

**Timeout with no output**

The agent may need more time. Try increasing the effort level (`--effort high` or `--effort xhigh`) or setting an explicit timeout (`--timeout 1200000`). Also check that the prompt isn't too broad — narrow the scope for faster results.

**SDK-specific errors**

Check stderr output for details. The heartbeat protocol suppresses SDK noise by default, but raw errors from the underlying SDK are captured in the JSON output's `error` field. Run with a simple prompt first to verify the engine works:

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

PRs welcome. Please open an issue for bug reports or feature requests.

## License

[MIT](./LICENSE)
