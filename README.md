# agent-mux

Cross-engine dispatch layer for built-in harness adapters: codex, claude, gemini, and experimental agy. One CLI, one JSON result contract, adapter boundary for more.

> **Go rewrite.** agent-mux was recently rewritten from TypeScript to Go -- static binary, goroutine-based supervision, no runtime dependencies.
> The TS version is preserved on the `agent-mux-ts` branch.
> See [docs/](docs/) for the full technical reference.

## What It Does

AI coding harnesses (Codex, Claude Code, Gemini CLI, and experimental agy) are powerful but isolated -- each has its own CLI flags, event format, sandbox model, and session lifecycle. agent-mux connects them through one JSON contract and prompt-driven worker identity.

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
./agent-mux config prompts --json
./agent-mux config engines
./agent-mux config engines --json
./agent-mux config engines --refresh-models --json  # explicit agy model-cache refresh
```

`config prompts` reflects your local `~/.agent-mux/prompts/` directory. `config engines --json` is the active engine/model/capability source of truth for dispatch validation.

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

| Engine | Resume | Structured events | Activity/tokens | Steering | Multimodal/image |
|--------|--------|-------------------|-----------------|----------|------------------|
| `codex` | yes | yes | activity + tokens, no cost | inbox resume / abort | no |
| `claude` | yes | yes | activity + tokens, no cost | inbox resume / abort | no |
| `gemini` | yes, UUID may fall back to latest | yes | activity + tokens, no cost | inbox resume / abort | no |
| `agy` | yes, after `agy.log` conversation ID | no, plain stdout | no structured activity/tokens/cost | inbox + `--conversation`, not live interrupt | smoke-verified, provider/model-dependent |

Engine CLIs must be installed separately -- agent-mux dispatches to them, it does not bundle them.

`agy` is CLI-first experimental support. agent-mux internally invokes the local `agy` CLI with its fixed `--sandbox`, rejects operator-supplied portable sandbox/permission/reasoning/max-turn/full-access options, stores provider diagnostics in private `agy.log`, and does not imply plugins, MCP, browser automation, Google services, or provider service actions.

## Features

- **Profile-based dispatch** -- `-P=<name>` loads engine, model, effort, timeout, skills, and system prompt from a single markdown file. One flag replaces six.
- **Recovery and signals** -- Start a follow-up dispatch with `--recover=<id>` to include prior context. Live nudge/redirect require a live FIFO or resume-capable engine; agy uses resume-backed inbox delivery after its Antigravity conversation ID is discovered.
- **Two-phase timeout** -- Soft timeout fires a wrap-up signal when the adapter supports it, grace period allows clean exit, hard timeout kills. Agy uses plain stdout and does not receive a soft-timeout wrap-up prompt. Artifacts are preserved at every phase.
- **Async dispatch** -- Fire and forget with `--async`. Collect results later with `wait` or `result`.
- **Event streaming** -- Event-stream engines emit NDJSON on stderr: `dispatch_start`, `heartbeat`, `tool_start`, `tool_end`, `file_write`, `timeout_warning`, and more. Agy is plain stdout only, so agent-mux reports the final response plus discovered artifacts.
- **Hooks** -- Pattern-based deny/warn rules evaluated on harness events.
- **Skill injection** -- Load `SKILL.md` runbooks into the dispatch prompt. Skills carry scripts, references, and setup.
- **Timeout and diagnostics** -- Global dispatch timeout is the sole hard backstop. Workers are trusted to complete within their timeout. 5-second watchdog cycle updates `status.json` for live observability. Process-group signals ensure grandchildren die with the harness. Forensic tools (`status.json`, `events.jsonl`, `steer nudge`) replace automatic kills for silent-worker diagnosis.
- **Durable persistence** -- Every dispatch writes `meta.json` and `result.json` under `~/.agent-mux/dispatches/<id>/`. Artifact directory created before the harness starts.
- **config prompts** -- `agent-mux config prompts` lists all discoverable profiles with engine, model, effort, timeout, and description.

## Authentication

| Engine | Personal use (OAuth) | Otherwise (`env var`) |
|--------|---------------------|----------------------|
| Codex | OAuth device auth via `codex auth` (`~/.codex/auth.json`) | `OPENAI_API_KEY` |
| Claude | OAuth via `claude` binary login (subscription) | `ANTHROPIC_API_KEY` |
| Gemini | `gcloud auth` application-default credentials | `GEMINI_API_KEY` |
| agy | Owned by the local `agy` CLI configuration | No agent-mux-managed fallback |

For personal use, OAuth tokens from your existing subscriptions are the primary
auth path -- no API keys needed. Set the env var when OAuth is not available.
agent-mux will attempt dispatch if any auth path is available --
`MISSING_API_KEY` is a warning, not a hard failure.

> **Note on the Claude engine:** agent-mux invokes the `claude` CLI binary
> directly -- it is not an SDK or API wrapper. For personal use with your own
> Claude Code subscription, OAuth login is the natural path. Set
> `ANTHROPIC_API_KEY` when OAuth is not an option.

## Documentation

| Doc | What |
|-----|------|
| [docs/](docs/) | Full technical reference -- architecture, config, dispatch lifecycle |
| [docs/agy.md](docs/agy.md) | Agy engine contract, limitations, model cache, steering, and AX gates |
| [SKILL.md](skill/SKILL.md) | Operational manual for AI agents using agent-mux |
| [BACKLOG.md](BACKLOG.md) | Open bugs, feature requests, spec gaps, and known limitations |
| [references/](skill/references/) | Engine comparison, prompting guide, output contract, config guides, installation |

## License

[MIT](./LICENSE)
