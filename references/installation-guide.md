# Installation Guide

Practical install guide for `agent-mux` in both environments:
- Claude Code (recommended)
- Codex CLI

This guide also covers coordinator mode (`--coordinator`) and a shared verification flow.

## Prerequisites

1. `Bun >= 1.0.0` (required runtime for `agent-mux` itself)
2. `Node.js` (only required if you use `--browser` / `agent-browser`)
3. API auth for the engine(s) you plan to run

| Engine | Env Var | Required When | Auth Notes |
| --- | --- | --- | --- |
| Codex (`--engine codex`) | `OPENAI_API_KEY` | Only when using Codex | Codex CLI can also use OAuth fallback via `codex auth` |
| Claude (`--engine claude`) | `ANTHROPIC_API_KEY` | Only when using Claude | Claude SDK supports device OAuth when no API key is set |
| OpenCode (`--engine opencode`) | `OPENROUTER_API_KEY` | Only when using OpenCode | Use only for OpenCode runs |

## Claude Code Setup (Recommended)

Use this when Claude is your coordinator and `agent-mux` is a worker dispatcher.

1. Clone into your Claude skills directory:

```bash
git clone https://github.com/buildoak/agent-mux.git ~/.claude/skills/agent-mux
```

2. Run setup:

```bash
cd ~/.claude/skills/agent-mux
./setup.sh
```

3. Register global CLI command:

```bash
bun link
```

4. Copy the GSD coordinator spec into your target project:

```bash
mkdir -p /path/to/project/.claude/agents
cp references/get-shit-done-agent.md /path/to/project/.claude/agents/get-shit-done-agent.md
```

5. From a Claude coordinator/subagent, dispatch a worker:

```bash
agent-mux --engine codex --cwd /path/to/project "prompt"
```

6. To spawn GSD from the main Claude thread, invoke:

```text
Task(subagent_type="gsd-coordinator")
```

7. Run coordinator mode directly from shell:

```bash
agent-mux --coordinator get-shit-done-agent --cwd /path/to/project "task"
```

## Codex CLI Setup

Use this when Codex is your primary agent environment.

1. Clone the repository:

```bash
git clone https://github.com/buildoak/agent-mux.git
cd agent-mux
```

2. Run setup and register CLI:

```bash
./setup.sh
bun link
```

3. Place skill in `.agents/skills/agent-mux/` (copy or symlink):

```bash
mkdir -p /path/to/project/.agents/skills
ln -s /absolute/path/to/agent-mux /path/to/project/.agents/skills/agent-mux
```

Alternative (copy):

```bash
cp -R /absolute/path/to/agent-mux /path/to/project/.agents/skills/agent-mux
```

4. Use coordinator mode with a project that has `.claude/agents/` definitions:

```bash
agent-mux --coordinator get-shit-done-agent --cwd /path/to/dir-with-claude-agents "task"
```

5. Or load coordinator prompt directly as a system prompt file:

```bash
agent-mux --system-prompt-file references/get-shit-done-agent.md "task"
```

6. Important behavior difference:
- Codex does not have Claude’s built-in `Task` tool.
- In Codex flows, GSD dispatch happens through shell execution of `agent-mux`, not via an internal `Task(...)` primitive.

## Coordinator Mode Notes (Both Setups)

- `--coordinator <name>` resolves: `<cwd>/.claude/agents/<name>.md`
- For `get-shit-done-agent`, the expected file is:
  - `/path/to/project/.claude/agents/get-shit-done-agent.md`
- Use `--cwd` to point `agent-mux` at the project that contains `.claude/agents/`.

## Verification (Both Setups)

Run these three checks:

1. Basic dispatch:

```bash
agent-mux --engine codex "Say hello"
```

2. Skill injection:

```bash
agent-mux --engine codex --cwd /path/to/project --skill agent-mux "What skills do I have?"
```

3. Coordinator:

```bash
agent-mux --engine claude --cwd /path/to/project --coordinator get-shit-done-agent "List your capabilities"
```

Expected result for successful runs:
- JSON output on stdout
- Includes `"success": true`

## Troubleshooting

- `agent-mux: command not found`
  - Run `bun link` in the `agent-mux` repo.
- `MISSING_API_KEY`
  - Verify required env var for your selected engine.
  - For Codex, run `codex auth` to use OAuth fallback.
  - For Claude, device OAuth is supported when key is not set.
- TypeScript errors during setup
  - Usually non-blocking; Bun runs TypeScript directly.
- `bun: command not found`
  - Install Bun from `https://bun.sh`.
- `Unknown MCP cluster: ...`
  - Copy template config and edit it:

```bash
cp mcp-clusters.example.yaml ~/.config/agent-mux/mcp-clusters.yaml
```

- Coordinator not found
  - Confirm file exists at `<cwd>/.claude/agents/<name>.md`.
  - Confirm `--cwd` points at that project root.
- SDK-specific errors
  - Test with a simple prompt first:

```bash
agent-mux --engine codex "Say hello"
```

## What setup.sh Does

`setup.sh` is idempotent and performs:
1. Installs Bun dependencies.
2. Runs TypeScript type-check.
3. Copies MCP config template if none exists.
4. Reports API key status.
