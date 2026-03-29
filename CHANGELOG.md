# Changelog

All notable changes to this project will be documented in this file.

## [3.2.0] - 2026-03-29

### Added
- **Streaming Protocol v2** — silent stderr is now the default. Only bookend events (`dispatch_start`, `dispatch_end`) and failure events pass through to stderr. All events still written to `events.jsonl` in the artifact directory. `--stream` / `-S` flag opts in to full event streaming (previous behavior)
- **Async dispatch** — `--async` flag returns immediately with a `{"kind":"async_started","dispatch_id":"...","salt":"..."}` ack. Worker runs in background. New commands:
  - `ax wait <id> [--poll <duration>]` — block until dispatch completes, optional periodic status output
  - `ax result <id> --no-wait` — return error instead of blocking if dispatch still running
- **Mid-flight steering** — `ax steer <id> <action>` for live dispatch control:
  - `abort` — kill worker process (SIGTERM or control.json fallback)
  - `nudge [message]` — send wrap-up message via inbox
  - `redirect "instructions"` — redirect worker via inbox with new instructions
  - `extend <seconds>` — extend watchdog kill threshold via control.json
  - `status` — read live status (detects orphaned processes)
- **`status.json` live observability** — running dispatches write live state (running/completed/failed), elapsed time, last activity, tool count, and files changed to `status.json` in the artifact directory
- **`control.json` watchdog overrides** — watchdog reads `control.json` from artifact directory on each tick for abort and extend-kill-seconds directives
- **ax-eval streaming test cases** — `silent-default`, `stream-flag`, `async-dispatch` (3 new cases, 15 total)

### Changed
- **`ax status <id>`** — now reads live `status.json` for running dispatches and detects orphaned processes
- **`ax result <id>`** — now blocks if dispatch still running; `--no-wait` returns error instead

### Fixed
- **engine_opts per-dispatch precedence** — per-dispatch `engine_opts` (e.g. `silence_warn_seconds`, `silence_kill_seconds` from `--stdin` JSON or role config) now take precedence over config defaults. Previously, config defaults unconditionally overwrote per-dispatch values, breaking liveness test thresholds
- **`intEngineOpt` string type support** — JSON-sourced engine_opts (string type from `--stdin` dispatch) are now correctly parsed as integers for liveness thresholds. Previously only `int` and `float64` types were handled, causing string values like `"10"` to fall back to config defaults

---

## [3.1.0] - 2026-03-28

### Added
- **`[skills] search_paths` config key** — list of directories to search for skill SKILL.md files beyond cwd and configDir. Tilde expansion supported. Union-merged across config layers with dedup. Resolution order: (1) cwd, (2) configDir, (3) each search_path
- **`agent-mux config skills` subcommand** — scans all search roots and displays a deduplicated table of discoverable skills with name, path, and source. `--json` for machine output. First match wins
- **`--skip-skills` flag** — escape hatch to dispatch a role without skill injection. Also supported in `--stdin` JSON as `"skip_skills": true`. Preserves role's engine/model/effort/timeout
- **Enhanced skill error messages** — skill-not-found errors now include: which skill was missing, which role injected it, and all paths searched

### Changed
- **`-V` short flag removed** — `-V` no longer maps to `--version` (agents confused it with `--variant`). Use `--version` (long flag only)

---

## [3.0.0] - 2026-03-28

### Added
- **Go binary** — static single-binary, no runtime dependencies (replaces bun/Node.js)
- **Role/variant system** — named roles in `config.toml` with engine, model, effort, skills, and permission-mode overrides; variants layer per-dispatch mutations on top of a role
- **Pipeline orchestration** — multi-step sequential pipelines where each step can target a different role/variant; step outputs feed into the next step's prompt context
- **Recovery and signal handling** — SIGINT/SIGTERM deliver partial results and a `recovery_hint` to callers; two-phase timeout (grace period before hard kill) ensures partial output is never silently dropped
- **Event streaming** — 15 structured NDJSON event types emitted to stderr: `dispatch_start`, `dispatch_end`, `heartbeat`, `tool_start`, `tool_end`, `file_write`, `file_read`, `command_run`, `progress`, `timeout_warning`, `frozen_warning`, `error`, `response_truncated`, `coordinator_inject`, `warning`
- **Hooks** — global `[hooks]` block in config with `deny`, `warn`, and `event_deny_action` rules; evaluated against agent activity in real time (WIP — see Known Limitations)
- **Liveness supervision** — silence watchdog emits `heartbeat` events at configurable intervals; `frozen_warning` for extended silence; distinguishes long-running work from hung processes
- **Artifact-first design** — workers are prompted to produce file artifacts by default; inline output reserved for short responses; artifact paths surface in the output contract
- **Config resolution** — `.agent-mux/` directory search walks up from `--cwd` to repo root; supports project-local and user-global config layers
- **Coordinator/profile system** — `--coordinator <name>` loads role spec from `.claude/agents/<name>.md` with YAML frontmatter (skills, model, allowedTools) and markdown body as system prompt
- **JSON `--stdin` dispatch** — full `DispatchSpec` JSON on stdin for programmatic callers; supports role, variant, pipeline, skills, and all overrides in one payload
- **Bundled agent templates** — 6 built-in role templates (researcher, coder, reviewer, planner, debugger, writer) as starting points for config.toml
- **Skills pre-loading** — role-level `skills = [...]` in config.toml resolved from `<cwd>/.claude/skills/<name>/SKILL.md` and injected into the system prompt before dispatch
- **Claude and Gemini adapters** — full engine registry with Codex, Claude Code, and Gemini adapters behind a common interface
- **`--version` flag** (emits JSON: `{"version":"agent-mux v2.0.0-dev"}`)
- **Lifecycle subcommands** — `list`, `status`, `result`, `inspect`, `gc` for post-dispatch introspection; human-readable default, `--json` for machine parsing
- **`list --engine` filter** — filter dispatch history by engine
- **`result --artifacts` flag** — list artifact files instead of showing result text
- **`inspect` subcommand** — full dump of dispatch record, response, artifacts, and dispatch metadata
- **`gc` subcommand** — garbage-collect old dispatches with `--older-than` duration and `--dry-run` preview
- **Orphan process reaper** — platform-specific (darwin/linux) reaper kills orphaned harness processes
- **`config.local.toml` support** — machine-local config overlays at both global (`~/.agent-mux/config.local.toml`) and project (`<cwd>/.agent-mux/config.local.toml`) levels
- **`response_max_chars` default raised to 16000** — from 2000; aligns with modern LLM context windows
- **Config merge: union semantics** — `[models]` and `[hooks].deny`/`.warn` now union-merge with dedup (append + deduplicate), not replace
- **`full_output_path` field** — separate field in dispatch result JSON pointing to the `full_output.md` file path when response is truncated
- **`response_truncated` event** — structured NDJSON event emitted when response truncation occurs, with `full_output_path`
- **Durable dispatch store** — JSONL-based dispatch index at `~/.agent-mux/store/` for lifecycle subcommand queries
- **`config` introspection subcommand** — inspect the fully-resolved, merged configuration without running a dispatch:
  - `agent-mux config` — full resolved config as JSON (always JSON; no `--json` flag needed); includes `_sources` array listing loaded config files
  - `agent-mux config --sources` — emit only the `config_sources` JSON object (list of loaded files)
  - `agent-mux config roles [--json]` — tabular role+variant listing (name, engine, model, effort, timeout); `--json` emits JSON array
  - `agent-mux config pipelines [--json]` — tabular pipeline listing (name, step count); `--json` emits JSON array
  - `agent-mux config models [--json]` — engine→models mapping; `--json` emits JSON object
  - All modes respect `--config` and `--cwd` for targeted config resolution

### Changed
- **Rewritten from TypeScript to Go** — entire runtime replaced; bun/Node.js no longer required
- **Output contract** — schema bumped to `schema_version: 1`; top-level `status` field (`completed` | `timed_out` | `failed`) replaces the `success: boolean` field from v2; all paths emit structured JSON
- **Config format** — role and global config now in TOML (`config.toml`); inline CLI flags remain for one-shot dispatches
- **Binary name** — installed as `agent-mux` (was `npx agent-mux` / `bunx agent-mux` in TS era)
- **Timeout-as-partial** — timed-out runs return `status: "timed_out"` with `partial: true`, collected output, and `recoverable: true`; callers no longer need to special-case timeout vs. error

### Removed
- **OpenCode engine** — replaced by Gemini adapter; OpenCode is no longer supported
- **bun runtime dependency** — Go binary is fully self-contained
- **MCP cluster system** — YAML-based MCP cluster configuration from v2.0.0 is not carried forward; MCP tool access is now managed per-engine via permission mode
- **Browser automation integration** — bundled agent-browser MCP server (shipped in v2.1.0) removed from the Go runtime; browser tooling must be wired externally if needed

### Known Limitations
- **Gemini response capture broken** — Gemini dispatches return truncated or empty `response` field despite generating output; `turns: 0`, `tool_calls: []`. Root cause: NDJSON stream parsing in `internal/engine/adapter/gemini.go` likely drops the final response event. Fix tracked in BACKLOG.md (B-1).
- **Gemini no tool calling** — Gemini dispatches produce zero file reads, zero commands, zero tool calls. The `gemini` CLI does not expose a tool-use surface comparable to Codex or Claude. Gemini variants are currently reasoning-only; all context must be supplied in the prompt.
- **Hooks false positives on workspace reads (WIP)** — deny/warn patterns match against all event content including files the harness reads during workspace orientation, not just agent-authored output. Hooks are disabled in production config until scope-aware matching is implemented. Fix tracked in BACKLOG.md (B-2).

---

## [2.2.0] - 2026-02-18

### Added
- --system-prompt-file <path> flag: load system prompt text from a file, composable with --system-prompt (file content first)
- --coordinator <name> flag: load coordinator specs from <cwd>/.claude/agents/<name>.md with YAML frontmatter (skills, model, allowedTools) and markdown body as system prompt
- Prompt composition order: coordinator body -> file content -> inline text
- 29 new tests covering both features, edge cases, and interactions

### Fixed
- extractFrontmatter handles CRLF line endings and closing --- at EOF without trailing newline
- Coordinator loading wrapped in try/catch with engine context in error response
- system-prompt-file validates path is a file (not directory)

## [2.1.0] - 2026-02-13

### Added
- Bundled agent-browser MCP server (25 tools) with interactive snapshot mode
- 7 new browser tools: reload, check, uncheck, dblclick, clear, focus, get_html

## [2.0.0] - 2026-02-13

### Added
- Unified CLI for three AI coding agent engines: Codex, Claude Code, OpenCode
- Engine adapter pattern with shared core orchestration
- YAML-based MCP cluster configuration with project-local and user-global fallback
- Stderr heartbeat protocol (15s intervals) for long-running processes
- Timeout-as-partial-success: timed out runs return `success: true` with partial results
- Activity tracking: file changes, commands, file reads, MCP tool calls
- Structured JSON output contract for all success, error, and timeout paths
- Per-engine effort levels mapping to timeouts and turn limits
- Claude Code skill file (SKILL.md) for skill-based installation
- Pre-flight API key validation with actionable error messages
- Graceful shutdown on SIGINT/SIGTERM with partial result collection
- Setup script for first-run experience
- Comprehensive test suite (165 tests)
- GitHub Actions CI (type-check + tests)
- `--version` flag
