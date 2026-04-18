# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [3.4.1] - 2026-04-19

### Fixed

- **codex-cli 0.121 compatibility** — codex ≥0.121 (upstream PR [openai/codex#15917](https://github.com/openai/codex/pull/15917)) unconditionally reads stdin to EOF before processing the prompt whenever stdin is non-TTY. agent-mux previously attached an open, unwritten stdin pipe to the codex child, causing every `codex exec` dispatch to hang at init with 0% CPU, no rollout JSONL, and no network traffic. The engine now wires codex's child stdin to an already-EOF reader so the drain completes immediately. Applies to both `codex exec` and `codex exec resume` (recovery path). Claude and Gemini are unaffected.

### Changed

- **Soft-steer via stdin unavailable for codex** — as a consequence of the 0.121 stdin drain, the mid-flight FIFO → stdin soft-steer channel (`stdin.pipe`) is no longer wired for codex dispatches. `agent-mux steer <id> nudge|redirect` against a running codex dispatch now falls back to the inbox + session-resume path automatically; `stdin_pipe_ready` is reported as `false` in `status.json` for codex so the CLI routes correctly. `steer abort` and steering against running claude/gemini dispatches are unchanged. A future version will wire `<artifact_dir>/inbox.md` as a proper out-of-band steering channel for live codex sessions.

## [3.4.0] - 2026-04-08

### Removed
- **Automatic stall/freeze detection** — workers are no longer killed for stdout silence. Global dispatch timeout is the sole hard backstop. This eliminates false kills during Codex API roundtrips (106K-190K token contexts take 2-8 min with zero NDJSON output) and Gemini deep thinking.
- **`steer extend` command** — no kill threshold to extend.
- **`StdinNudge` from adapter interface** — was only used by stall detection.

### Added
- **Gemini `--include-directories $HOME,/tmp` by default** — broad filesystem access for context files and artifacts. Gemini workers can now read `--context-file` content and any file under the home directory.
- **`steer` accepts both argument orderings** — `steer <id> <action>` and `steer <action> <id>` both work. AI agents consistently got the argument order wrong; this fixes it.
- **`killed_by_user` status for SIGTERM/SIGKILL exits** — replaces generic `failed`. Uses proper Go WaitStatus signal detection (exit 137/143).
- **Worker diagnostics guide** — `skill/references/worker-diagnostics.md` teaches operators how to diagnose silent workers vs. genuinely stalled ones, replacing automatic kills with forensic tools.

### Fixed
- **Gemini dispatches can now read `--context-file` content** — previously blocked by workspace sandbox restricting reads to `--cwd`.
- **Signal-terminated workers properly classified** — via Go WaitStatus detection instead of generic error codes.

## [3.3.0] - 2026-04-06

### Added

- **Gemini stall detection** — engine loop now detects silent Gemini sessions and kills them after a configurable timeout (default 60s). Stall timer pauses during tool execution to avoid killing legitimate long-running tools. New `stall_timeout` state in lifecycle.
- **Gemini effort warning** — adapter logs a warning when effort flag is set for Gemini dispatch, since Gemini CLI has no effort/thinking-budget knob. Model selection is the depth lever.
- **CLI help discoverability** — engine names (`codex`, `claude`, `gemini`) now listed in flag descriptions. Valid values for `--effort`, `--permission-mode`, and `--sandbox` enumerated. Multi-step pipeline example added to help output. Flag format note clarifying double-dash requirement for long flags.
- **Gemini v0.34.0 test coverage** — 644 lines of new tests covering dual event schema, delta buffer accumulation, tool ID correlation, approval mode validation, per-model token stats, and raw passthrough events. Adapter coverage at 86.3%.

### Fixed

- **5 CLI UX improvements** — phantom `-R` example removed, single-dash long-form flags clarified, canonical dispatch example added, `--async` backgrounding warning added.

### Changed

- **Documentation overhaul** — Gemini adapter section in engines.md fully expanded (stall timeout, approval mode defaults, dual event schema, tool tracking, delta buffer, UUID resume degradation). Factual errors fixed across config.md (timeout 1800→900), prompting-guide.md (Gemini tool claims), and engines.md (passthrough behavior). Profile rosters updated to match `config prompts --json` ground truth across README, skill, and config docs. Authentication reframed around personal-use OAuth as primary path.
- **Skill update** — Gemini integrated into quickstart and dispatch logic. New `gemini-specifics.md` reference file. Phantom `-V/--variant` flag removed (variants are coordinator-layer, not binary). `config --sources` and `config models` phantom subcommands removed. Output contract schema aligned (`role`→`profile`). Dispatch docs updated to current `LoadProfile` path.
- **Config clarity** — `config.toml` header added clarifying coordinator-layer vs binary scope. Dead `effort = "medium"` removed from Gemini variant.

### Removed

- **`agents/` directory** — legacy agent definitions archived to `_archive/agents/`. Binary reads from `~/.agent-mux/prompts/` only.

## [3.2.3] - 2026-04-04

### Fixed

- **Stale role/variant references in eval rubrics** — `ax-eval/MANIFEST.md` checklist items and error examples updated from `-R=` (removed role flag) to `-P=` (profile flag). These rubrics are consumed by LLM evaluators; stale references were actively misleading eval scoring.
- **Stale references in test documentation** — `tests/axeval/CASES-V2.md` updated: `config roles --json` to `config prompts --json`, `-R=` to `-P=`, "role" vocabulary to "profile" where it described the current system.
- **Version constant out of sync** — `main.go` version bumped from `v3.2.0` to `v3.2.3` to match the release timeline.

### Removed

- **Pipeline system** — `internal/pipeline/` and all pipeline orchestration removed. The `-P=` flag, `PipelineResult`, `PipelineStep`, and `config.toml` `[pipelines]` block no longer exist. Multi-step coordination is the caller's responsibility via chained individual dispatches.
- **Response truncation** — `response_max_chars`, `--response-max-chars` CLI flag, `TruncateResponse`, `ResponseTruncated` semantics, and `full_output.md` spill path removed. The `response_truncated` and `full_output_path` fields remain in `DispatchResult` for schema compatibility but are never set to non-default values.
- **`dispatch_salt` and `trace_token`** — removed from `DispatchSpec`, output contract, and all persistence paths. Dispatch identity is `dispatch_id` (ULID) only.
- **`gc` subcommand** — garbage-collect command removed. Dispatch records under `~/.agent-mux/dispatches/` must be cleaned up manually.
- **`--coordinator` flag** — removed. Coordinator/profile loading via `.claude/agents/<name>.md` frontmatter no longer supported.
- **`allow_subdispatch` / `--no-subdispatch` / `--salt`** — removed flags with no replacement.
- **`DispatchError.Suggestion`** — removed from `DispatchError` struct. Callers should use `hint` and `example` fields directly.
- **`_dispatch_meta.json` in artifact dir** — replaced by file-based persistence. Dispatch metadata is now written to `~/.agent-mux/dispatches/<dispatch_id>/meta.json` and `~/.agent-mux/dispatches/<dispatch_id>/result.json`.
- **SQLite/JSONL dispatch store** — removed. File-based persistence under `~/.agent-mux/dispatches/<id>/` is the sole store. `list`, `status`, `result`, `inspect` read from this directory tree.

### Changed

- **`DispatchSpec` reduced to 15 fields** — `DispatchID`, `Engine`, `Model`, `Effort`, `Prompt`, `SystemPrompt`, `Cwd`, `ArtifactDir`, `ContextFile`, `TimeoutSec`, `GraceSec`, `MaxDepth`, `Depth`, `FullAccess`, `EngineOpts`. Salt, trace_token, pipeline, coordinator, and subdispatch fields removed.
- **File-based persistence** — each dispatch writes two files on completion: `~/.agent-mux/dispatches/<id>/meta.json` (written at dispatch start) and `~/.agent-mux/dispatches/<id>/result.json` (written at dispatch end). `list`, `status`, `result`, and `inspect` derive their data from these files.
- **`preview` command includes `result_metadata`** — `agent-mux preview` stdout now includes a `result_metadata` object describing where `meta.json` and `result.json` will be written for the prospective dispatch.

### Changed (Simplification Waves)

- **Wave 1 (0d9ac3a):** Unified `internal/inbox/` + `internal/fifo/` into `internal/steer/` package
- **Wave 2 (eb04388):** Absorbed `internal/recovery/` into `internal/dispatch/`
- **Wave 3 (49625c1):** Unified persistence — `_dispatch_ref.json` pointer in artifact dir; sole source of truth is `~/.agent-mux/dispatches/<id>/meta.json` + `result.json`
- **Wave 4 (dfde796):** Hooks redesigned — executable scripts replace pattern matching; scripts receive env vars + JSON stdin; exit 0=allow, 1=block, 2=warn; default: `scripts/block-dangerous.sh`
- **Wave 5 (07ea163):** Config stripped — role variants removed (flat roles only), companion TOML removed, `config.local.toml` removed, old XDG fallback removed; 2-file config merge (global → project)
- **Wave 6 (7ee19d1):** Flag surface split — `--stdin` mode gets 6 flags only; CLI mode keeps full set
- **Wave 7 (634673e):** `_dispatch_meta.json` compat shim removed; `PromptHash` added to `PersistentDispatchMeta`

## [3.2.2] - 2026-03-31

### Fixed

- **FM-7: process exit race — final EventResponse lost** (`6342c92`) — a second drain pass now runs after `streamDone` to capture events emitted between the last scanner read and process exit. Previously the final response event was silently dropped on clean exits.
- **FM-4: hard timeout grace hardcoded to 5s** (`6342c92`) — `GracefulStop` now uses `spec.GraceSec` (floored at 10s) instead of a hardcoded 5-second SIGKILL window. Per-dispatch grace periods are respected end-to-end.
- **FM-9: failed dispatches discarded accumulated response** (`6342c92`) — `BuildFailedResult` now preserves `lastResponse` and `lastProgressText` collected before the failure. Previously any partial response was thrown away on the error path.
- **FM-15: status written before store record** (`6342c92`) — store `WriteResult` now completes before the terminal status event is emitted. Previously a caller reading status could see `completed` before the record was queryable.
- **FM-8: non-atomic store.WriteResult** (`6342c92`) — result records are now written via `os.CreateTemp` + `fsync` + `rename`. Partial or corrupt records can no longer appear on disk during a write.
- **FM-15 (auditor pass): store errors now logged + fallback to full_output.md** (`04a6a18`) — store write failures no longer silently drop results; a warning is logged and response is persisted to `full_output.md` as a fallback.
- **FM-9 (auditor pass): meta-write failure preserves partial response** (`04a6a18`) — metadata write errors on the failed-dispatch path no longer discard the accumulated response.
- **FM-8 (auditor pass): unique temp files via os.CreateTemp** (`04a6a18`) — temp file names are now generated by `os.CreateTemp` rather than a fixed suffix, preventing collision under concurrent writes.
- **Codex sandbox value validation** (`6539644`) — `sandbox` engine opt is now validated against an explicit whitelist (`danger-full-access`, `workspace-write`, `read-only`) before dispatch; unknown values return a structured error instead of passing through to the CLI.
- **bufio scanner overflow: graceful handling** (`6539644`) — scanner buffer raised from 1 MB to 4 MB; `bufio.ErrTooLong` is now detected and handled gracefully (line skipped with warning event) rather than terminating the parse loop.

### Changed

- **ax-eval promoted to repo root** (`4799d5e`) — `ax-eval/` now lives at the repository root as `ax-eval/MANIFEST.md` + `ax-eval/PROTOCOL.md`. It is an evaluation protocol, not a dispatchable skill; the `skill/` subtree is no longer the right home for it.

### Added

- **SKILL.md: artifact-dir escaping, flag syntax rules, sandbox anti-patterns** (`c6178b0`) — three new reference sections in `skill/SKILL.md` covering path quoting for artifact dirs with spaces, correct flag syntax examples, and a sandbox anti-patterns table.

### Removed

- **`--network` flag removed from skill docs** (`e4403f7`) — the flag was documented in `skill/SKILL.md` but was cut during CLI design and has never existed in the binary. Reference removed.

---

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
