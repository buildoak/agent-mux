# agent-mux

Cross-harness dispatch layer. One CLI, one JSON contract, any LLM engine.

> **Go rewrite.** agent-mux was recently rewritten from TypeScript to Go — static binary, goroutine-based supervision, no runtime dependencies.
> The TS version is preserved on the `agent-mux-ts` branch.
> See [DOCS.md](DOCS.md) for the full technical reference.

## What It Does

AI coding harnesses (Codex, Claude Code, Gemini CLI) are powerful but isolated — each has its own CLI flags, event format, sandbox model, and session lifecycle. agent-mux connects them: any LLM can dispatch work to any other LLM through one JSON contract and one config system. Roles, variants, and pipelines turn good dispatch patterns into reusable TOML config.

## Core Principles

1. **Tool, not orchestrator.** The calling LLM decides what to do. agent-mux handles the how.
2. **Job done is holy.** Artifacts persist across timeout and process death. Every dispatch has an artifact path.
3. **Errors are steering signals.** Every error tells the caller what failed, why, and what to try next.
4. **Single-shot with curated context.** One well-prompted dispatch beats a swarm of under-specified workers.
5. **Config over code.** Roles, pipelines, models, timeouts — all TOML. The binary is generic.
6. **Simplest viable dispatch.** CLI call > config > code. Escalate only when needed.

## Quick Start

```bash
git clone https://github.com/buildoak/agent-mux && cd agent-mux
go build -o agent-mux ./cmd/agent-mux
```

**Minimal dispatch** (JSON via `--stdin` — the canonical invocation pattern):

```bash
printf '{"engine":"codex","prompt":"Review src/core.go for timeout edge cases","cwd":"/repo"}' \
  | ./agent-mux --stdin
```

**Role-based dispatch** (engine, model, effort, timeout resolved from `config.toml`):

```bash
printf '{"role":"scout","prompt":"Find all usages of deprecated API","cwd":"/repo"}' \
  | ./agent-mux --stdin
```

Output is always a single JSON object on stdout. stderr carries NDJSON event stream and heartbeat lines.

## Engines

| Engine | Binary | Best For | Default Model |
|--------|--------|----------|---------------|
| `codex` | `codex` | Implementation, debugging, code edits | `gpt-5.4` |
| `claude` | `claude` | Planning, synthesis, long-form reasoning | `claude-sonnet-4-20250514` |
| `gemini` | `gemini` | Second opinion, contrast checks | `gemini-2.5-pro` |

Engine CLIs must be installed separately — agent-mux dispatches to them, it does not bundle them.

## Features

- **Role-based dispatch** — Roles resolve engine, model, effort, timeout, skills, and system prompt from TOML config. One field replaces six flags.
- **Variant system** — Swap engines within a role while keeping the same semantics. `{"role":"lifter","variant":"claude"}` runs a lifter with Claude instead of Codex.
- **Pipeline orchestration** — Multi-step chains, fan-out parallelism, and handoff rendering between steps. Defined in TOML, not code.
- **Recovery and signals** — Continue timed-out work with `--recover`. Steer live dispatches with inbox signals.
- **Two-phase timeout** — Soft timeout fires a wrap-up signal, grace period allows clean exit, hard timeout kills. Artifacts are preserved at every phase.
- **Event streaming** — 13 NDJSON event types on stderr: `dispatch_start`, `heartbeat`, `tool_start`, `tool_end`, `artifact_written`, `soft_timeout`, and more.
- **Hooks** — Pattern-based deny/warn rules evaluated on harness events. Safety preamble injection.
- **Profiles and coordinators** — Load orchestrator personas from markdown specs with frontmatter-driven defaults.
- **Skill injection** — Load `SKILL.md` runbooks into the dispatch prompt. Skills carry scripts, references, and setup.
- **Liveness supervision** — 5-second watchdog cycle detects hung harnesses. Process-group signals ensure grandchildren die with the harness.
- **Artifact-first design** — Artifact directory created before the harness starts. `ScanArtifacts()` runs on every terminal state.

## Authentication

| Engine | Env Var | Fallback |
|--------|---------|----------|
| Codex | `OPENAI_API_KEY` | OAuth device auth via `codex auth` (`~/.codex/auth.json`) |
| Claude | `ANTHROPIC_API_KEY` | Device OAuth (subscription login) — **not recommended** |
| Gemini | `GEMINI_API_KEY` | — |

Both Codex and Claude support subscription-based OAuth as a fallback when no
API key is set. agent-mux will attempt dispatch if any auth path is available —
`MISSING_API_KEY` is a warning, not a hard failure.

> **Anthropic ToS compliance:** The `claude` engine uses Claude Code SDK with
> your API key (`ANTHROPIC_API_KEY`) — this is the fully compliant path. Device
> OAuth (subscription login) falls under Anthropic's Consumer ToS §3.7 which
> restricts automated/scripted access outside the API. **Always use
> `ANTHROPIC_API_KEY` for the `claude` engine in automated workflows.**

## Documentation

| Doc | What |
|-----|------|
| [DOCS.md](DOCS.md) | Full technical reference — architecture, config schema, dispatch lifecycle |
| [SKILL.md](SKILL.md) | Operational manual for AI agents using agent-mux |
| [FEATURES.md](FEATURES.md) | Open feature requests and known limitations |
| [references/](references/) | Engine comparison, prompting guide, output contract schema, installation guide |

## License

[MIT](./LICENSE)
