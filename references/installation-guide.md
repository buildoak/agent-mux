# Installation Guide

Install and configure `agent-mux` -- the Go binary that dispatches work across Codex, Claude, and Gemini engines.

## Prerequisites

- **Go 1.21+** (for building from source)
- **API keys** for the engines you plan to use:

| Engine | Env Var | Required When |
|--------|---------|---------------|
| Codex | `OPENAI_API_KEY` | Using `--engine codex` or any Codex role |
| Claude | `ANTHROPIC_API_KEY` | Using `--engine claude` or any Claude role |
| Gemini | `GEMINI_API_KEY` | Using `--engine gemini` or any Gemini role |

- **Engine CLIs on PATH:** `codex`, `claude`, and/or `gemini` binaries must be installed separately. agent-mux dispatches to these; it does not bundle them.

## Build and Install

Clone the repo and build:

```bash
git clone https://github.com/buildoak/agent-mux.git
cd agent-mux
go build -o ~/bin/agent-mux ./cmd/agent-mux/
```

Ensure `~/bin` is on your PATH:

```bash
# Add to ~/.bashrc or ~/.zshrc if not already present
export PATH="$HOME/bin:$PATH"
```

## Config Setup

Create the global config directory and a minimal config file:

```bash
mkdir -p ~/.agent-mux
```

Create `~/.agent-mux/config.toml` with defaults:

```toml
[defaults]
engine = "codex"
model = "gpt-5.4"
effort = "high"

[timeout]
low = 120
medium = 600
high = 1800
xhigh = 2700
grace = 60

[models]
codex  = ["gpt-5.4", "gpt-5.4-mini"]
claude = ["claude-sonnet-4-6", "claude-opus-4-6"]
gemini = ["gemini-2.5-pro"]
```

Project-level config goes in `<project>/.agent-mux/config.toml` and overrides global settings. An explicit `--config <path>` flag takes highest precedence.

## API Keys

Export the keys for engines you use:

```bash
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."
export GEMINI_API_KEY="..."
```

Add these to your shell profile for persistence. Only the key for the engine you invoke is required -- you do not need all three.

## Verification

```bash
# Check binary is on PATH
agent-mux --version

# View available flags and modes
agent-mux --help

# Smoke test: single dispatch to Codex
printf '{"role":"scout","prompt":"Say hello","cwd":"/tmp"}' | agent-mux --stdin
```

Expected: JSON output on stdout with `"status": "completed"` and a `response` field.

## Claude Code Integration

Symlink the agent-mux repo into your Claude Code skills directory so Claude can read the SKILL.md and reference docs:

```bash
ln -s /absolute/path/to/agent-mux ~/.claude/skills/agent-mux
```

To use GSD coordinators, copy the agent spec into your project:

```bash
mkdir -p /path/to/project/.claude/agents
cp /absolute/path/to/agent-mux/references/get-shit-done-agent.md \
   /path/to/project/.claude/agents/get-shit-done-agent.md
```

Then customize the spec for your project (add project-specific skills, output paths, role preferences).

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `agent-mux: command not found` | Ensure `~/bin` is on PATH and binary was built |
| `engine_not_found` | Check `--engine` value is `codex`, `claude`, or `gemini` |
| `model_not_found` | Check model name; error includes fuzzy-match suggestions |
| API key errors | Verify the env var for your engine is exported |
| `codex: command not found` | Install the Codex CLI separately |
| Config not loading | Check `~/.agent-mux/config.toml` exists; use `--verbose` to see resolution |
