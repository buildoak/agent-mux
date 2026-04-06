# agent-mux

Cross-engine dispatch layer. One CLI, one JSON contract, any LLM engine.

> **Go rewrite.** agent-mux was recently rewritten from TypeScript to Go -- static binary, goroutine-based supervision, no runtime dependencies.
> The TS version is preserved on the `agent-mux-ts` branch.
> See [docs/](docs/) for the full technical reference.

## What It Does

AI coding harnesses (Codex, Claude Code, Gemini CLI) are powerful but isolated -- each has its own CLI flags, event format, sandbox model, and session lifecycle. agent-mux connects them: any LLM can dispatch work to any other LLM through one JSON contract and prompt-driven worker identity.

Workers are defined as markdown files with YAML frontmatter. The prompt is the worker. No config files, no role tables, no indirection.

## Core Principles

1. **Tool, not orchestrator.** The calling LLM decides what to do. agent-mux handles the how.
2. **Job done is holy.** Artifacts persist across timeout and process death. Every dispatch has an artifact path.
3. **Errors are steering signals.** Every error tells the caller what failed, why, and what to try next.
4. **Single-shot with curated context.** One well-prompted dispatch beats a swarm of under-specified workers.
5. **Prompt over config.** Worker identity lives in `.md` files with frontmatter defaults. The binary is generic.
6. **Simplest viable dispatch.** CLI flags > frontmatter > hardcoded defaults. Escalate only when needed.

## Quick Start

```bash
git clone https://github.com/buildoak/agent-mux && cd agent-mux
go build -o agent-mux ./cmd/agent-mux
```

**See what workers are available:**

```bash
./agent-mux config prompts
# NAME                  ENGINE  MODEL             EFFORT  TIMEOUT  DESCRIPTION
# architect             claude  claude-opus-4-6   high    900      Strategic plans with verification gates
# auditor               codex   gpt-5.4           xhigh   2700     Adversarial review -- finds bugs, missing tests, unsafe assumptions
# explorer              codex   gpt-5.4           high    600      Multi-file internal investigation
# grunt                 codex   gpt-5.4-mini      medium  600      Mechanical execution -- rote changes, fan-out units
# lifter                codex   gpt-5.4           high    1800     Scoped implementation -- code changes with built-in verification
# researcher            claude  claude-opus-4-6   high    900      External synthesis with confidence
# scout                 codex   gpt-5.4-mini      low     180      Quick read-only probe
# ticket-worker         codex   gpt-5.4-mini      xhigh   -        Ticket execution (standard)
# ticket-worker-heavy   codex   gpt-5.4           high    -        Ticket execution (complex)
# writer                codex   gpt-5.4           high    1500     Publishable prose in the user's voice
```

**Profile-based dispatch** (engine, model, effort, timeout, system prompt all resolved from the profile):

```bash
./agent-mux -P=lifter -C /repo "Add retry logic to client.ts with exponential backoff"
```

**Minimal dispatch** (JSON via `--stdin` -- the canonical machine invocation):

```bash
printf '{"engine":"codex","prompt":"Review src/core.go for timeout edge cases","cwd":"/repo"}' \
  | ./agent-mux --stdin
```

**Async dispatch** (fire, collect later):

```bash
./agent-mux -P=lifter --async -C /repo "Implement retries in client.ts"
# => {"kind":"async_started","dispatch_id":"01K...","artifact_dir":"..."}

./agent-mux wait 01K...
./agent-mux result 01K... --json
```

Dispatch output is always a single JSON object on stdout. Lifecycle subcommands (`list`, `status`, `result`, `inspect`, `wait`) default to human-readable but accept `--json`. stderr carries NDJSON event stream and heartbeat lines.

## Profiles

Worker identity lives in `~/.agent-mux/prompts/<name>.md`. Each file is a markdown document with optional YAML frontmatter that sets dispatch defaults:

```markdown
---
engine: codex
model: gpt-5.4
effort: high
timeout: 1800
description: "Scoped implementation with built-in verification"
---

# Lifter

You are a lifter. You build what was specified, verify it works, and report back.
...
```

`-P=lifter` loads `~/.agent-mux/prompts/lifter.md`, applies frontmatter defaults, and injects the markdown body as the system prompt.

**Resolution order** (later wins): hardcoded defaults -> frontmatter -> CLI flags / JSON fields. Explicit flags always override frontmatter.

`agent-mux config prompts` discovers all profiles with full metadata. `agent-mux config prompts --json` emits the catalog as a JSON array for programmatic consumption.

## Engines

| Engine | Binary | Best For | Default Model |
|--------|--------|----------|---------------|
| `codex` | `codex` | Implementation, debugging, code edits | `gpt-5.4` |
| `claude` | `claude` | Planning, synthesis, long-form reasoning | `claude-sonnet-4-6` |
| `gemini` | `gemini` | Second opinion, contrast checks | `gemini-2.5-pro` |

Engine CLIs must be installed separately -- agent-mux dispatches to them, it does not bundle them.

## Features

- **Profile-based dispatch** -- `-P=<name>` loads engine, model, effort, timeout, skills, and system prompt from a single markdown file. One flag replaces six.
- **Recovery and signals** -- Continue timed-out work with `--recover`. Steer live dispatches with inbox signals.
- **Two-phase timeout** -- Soft timeout fires a wrap-up signal, grace period allows clean exit, hard timeout kills. Artifacts are preserved at every phase.
- **Async dispatch** -- Fire and forget with `--async`. Collect results later with `wait` or `result`.
- **Event streaming** -- 15 NDJSON event types on stderr: `dispatch_start`, `heartbeat`, `tool_start`, `tool_end`, `file_write`, `timeout_warning`, and more.
- **Hooks** -- Pattern-based deny/warn rules evaluated on harness events.
- **Skill injection** -- Load `SKILL.md` runbooks into the dispatch prompt. Skills carry scripts, references, and setup.
- **Liveness supervision** -- 5-second watchdog cycle detects hung harnesses. Process-group signals ensure grandchildren die with the harness.
- **Durable persistence** -- Every dispatch writes `meta.json` and `result.json` under `~/.agent-mux/dispatches/<id>/`. Artifact directory created before the harness starts.
- **config prompts** -- `agent-mux config prompts` lists all discoverable profiles with engine, model, effort, timeout, and description.

## Authentication

| Engine | Personal use (OAuth) | Automated / CI (`env var`) |
|--------|---------------------|---------------------------|
| Codex | OAuth device auth via `codex auth` (`~/.codex/auth.json`) | `OPENAI_API_KEY` |
| Claude | OAuth via `claude` binary login (subscription) | `ANTHROPIC_API_KEY` |
| Gemini | `gcloud auth` application-default credentials | `GEMINI_API_KEY` |

For personal use, OAuth tokens from your existing subscriptions are the primary
auth path -- no API keys needed. API keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
`GEMINI_API_KEY`) are for automated workflows, CI pipelines, and headless setups.
agent-mux will attempt dispatch if any auth path is available --
`MISSING_API_KEY` is a warning, not a hard failure.

> **Note on the Claude engine:** agent-mux invokes the `claude` CLI binary
> directly -- it is not an SDK or API wrapper. For personal use with your own
> Claude Code subscription, OAuth login is the natural path. For automated or
> headless workflows, set `ANTHROPIC_API_KEY` to use the API path instead.

## Documentation

| Doc | What |
|-----|------|
| [docs/](docs/) | Full technical reference -- architecture, config, dispatch lifecycle |
| [SKILL.md](SKILL.md) | Operational manual for AI agents using agent-mux |
| [BACKLOG.md](BACKLOG.md) | Open bugs, feature requests, spec gaps, and known limitations |
| [references/](references/) | Engine comparison, prompting guide, output contract, config guides, installation |

## License

[MIT](./LICENSE)
