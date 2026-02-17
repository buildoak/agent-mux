# Installation Guide

Detailed installation instructions for AI agents. This guide covers both Claude Code and Codex CLI environments.

## Claude Code Installation

Path: `.claude/skills/agent-mux/`

1. Clone into your project skills directory:
```bash
git clone https://github.com/buildoak/agent-mux.git /path/to/your-project/.claude/skills/agent-mux
```
2. Change into the skill directory:
```bash
cd /path/to/your-project/.claude/skills/agent-mux
```
3. Run setup:
```bash
./setup.sh
```
4. Optional: register global CLI command:
```bash
bun link
```

## Codex CLI Installation

Codex reads `AGENTS.md` only (not `codex.md`, not `.codex/skills/`).

1. Clone the repository:
```bash
git clone https://github.com/buildoak/agent-mux.git /path/to/agent-mux
```
2. Run setup:
```bash
cd /path/to/agent-mux && ./setup.sh
```
3. Append `SKILL.md` to your project `AGENTS.md` with a marker:
```bash
touch /path/to/your-project/AGENTS.md
{
  echo
  echo "<!-- fieldwork-skill:agent-mux -->"
  cat /path/to/agent-mux/SKILL.md
} >> /path/to/your-project/AGENTS.md
```

## Prerequisites

- Bun >= 1.0.0 (required runtime)
- API keys:
  - `OPENAI_API_KEY` (Codex)
  - `ANTHROPIC_API_KEY` (Claude, optional)
  - `OPENROUTER_API_KEY` (OpenCode, optional)
- Node.js (only needed for `--browser` flag / `agent-browser`)

## What setup.sh Does

- Checks for Bun
- Installs Bun dependencies (`bun install`)
- Runs TypeScript type-check
- Copies `mcp-clusters.example.yaml` to `~/.config/agent-mux/` if no config exists
- Reports API key status

## Verification

Run:

```bash
bun run src/agent.ts --engine codex "Say hello"
```

Or (if `bun link` was run):

```bash
agent-mux --engine codex "Say hello"
```

Expect JSON output with `success: true`.

Check these fields:

- `engine` should be `"codex"`
- `response` should contain text

## Troubleshooting

- `agent-mux: command not found` -> run `bun link` or invoke directly with `bun run src/agent.ts`
- `MISSING_API_KEY` -> check env vars; Codex also supports `codex auth` OAuth
- TypeScript errors during setup -> usually non-blocking; Bun runs TS directly
- `bun: command not found` -> install Bun: `curl -fsSL https://bun.sh/install | bash`

## Global CLI Registration

- `bun link` registers `agent-mux` as a global command
- After linking, use `agent-mux` directly instead of `bun run src/agent.ts`
