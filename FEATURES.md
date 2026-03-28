# Features

Open feature requests and follow-up ideas for agent-mux.

## Proposed

- ~~Role-level skills~~ — **SHIPPED (2026-03-28)**. `Skills []string` on
  `RoleConfig`, merged with CLI `--skill` flags via `mergeSkills()`.

- Gemini adapter: response capture broken
  - Current state: Gemini dispatches return truncated/empty `response` field
    despite generating 1000+ output tokens. `turns: 0`, `tool_calls: []`.
  - Root cause: the adapter calls `gemini -p <prompt> -o stream-json` but
    stdout parsing drops the actual content. Only tail fragments survive.
  - Fix needed: audit `internal/engine/adapter/gemini.go` stream-json
    parsing — the NDJSON scanner likely misses the final response event
    or conflates stream events with the terminal result.

- Gemini adapter: no tool calling
  - Current state: Gemini dispatches produce 0 file reads, 0 commands,
    0 tool calls. The gemini CLI doesn't expose a tool-use surface
    comparable to Codex or Claude.
  - Impact: Gemini variants are reasoning-only (all context must be in
    the prompt). Cannot read files or run commands.
  - Options: (a) accept this as a known limitation and document Gemini
    as prompt-contained-reasoning-only, (b) investigate if `gemini` CLI
    supports tool definitions via config/flags, (c) build a tool-use
    shim that pre-reads files into the prompt context.

- Hanging bash detection (per-command timeout)
  - Current state: only global silence watchdog. A legit 10-min Rust build
    is indistinguishable from a hung curl. No per-command timeout.
  - Proposed: track `tool_start` → `tool_end` pairs in `loop.go`. If
    `tool_end` hasn't arrived in N seconds, emit `long_command_warning`
    event with command name. Optionally classify known-long commands
    (cargo, make, nvcc) for extended grace.
  - Effort: ~40 lines in `loop.go`, no harness changes.

- Session-local daemon / JSON-RPC control plane
  - Current state: one-shot CLI dispatch is enough for basic bot integration.
  - Possible extension: optional per-session daemon with a small JSON-RPC
    surface for centralized streaming/control plus `attach`, `list`,
    `inspect`, and `signal` across multiple live dispatches.
  - Why: not needed now, but potentially useful for Jenkins/operator workflows
    and better caller-death tolerance.

- Response truncation: make configurable, not silent
  - Current state: `response_max_chars` defaults to 2000. When exceeded,
    response is silently truncated and `full_output.md` is written to the
    artifact directory. The caller gets `response_truncated: true` but
    must know to check the artifact path for the full text.
  - Impact: auditor dispatches, research tasks, and any verbose worker
    output gets silently cut. Callers that don't check `response_truncated`
    lose data without warning.
  - Proposed: (a) raise default to 8000-16000 chars (LLM context windows are
    large enough), (b) emit a structured warning event when truncation occurs,
    (c) add a `--no-truncate` flag or `response_max_chars: 0` to disable,
    (d) include the `full_output` artifact path in the result JSON when
    truncation occurs so callers don't have to construct it.

- BUG: result JSON goes to stderr, not stdout
  - Current state: dispatch result JSON consistently lands on stderr instead
    of stdout. The documented contract says "single JSON object on stdout"
    but the binary writes it to stderr. This means `> result.json` captures
    nothing; callers must parse stderr.
  - Impact: every programmatic consumer must check stderr for results.
    Breaks the documented contract. Discovered during 10+ dispatches in
    a single coordination session.

- Post-dispatch lifecycle commands (status, result, list)
  - Current state: after a dispatch completes, the only way to inspect
    results is to parse raw JSON files in /tmp/agent-mux/<dispatch_id>/.
    No built-in commands for checking status, extracting the response text,
    or listing recent dispatches.
  - Proposed commands:
    - `agent-mux status <dispatch_id>` — status, duration, truncated, artifact count
    - `agent-mux result <dispatch_id>` — print response text (or full_output.md if truncated)
    - `agent-mux list` — table of recent dispatches: id, role, status, duration
  - Why: agent-mux has good dispatch ergonomics but zero post-dispatch
    ergonomics. The dispatch-preview-signal-recover lifecycle is solid.
    After completion, you're parsing JSON by hand. These three commands
    close the loop.
  - Note: `-o=text` exists for dispatch-time output but there's no
    equivalent for post-dispatch inspection.

- Hooks: context-aware matching
  - Current state: event-level deny/warn patterns use case-insensitive
    substring matching on ALL event content, including files read during
    harness workspace orientation. This causes false positives — e.g., a
    `deny = ["DROP TABLE"]` pattern triggers when Codex reads a documentation
    file that mentions SQL injection examples.
  - Impact: hooks are effectively unusable for repos containing documentation
    or test fixtures with deny-pattern strings. Currently disabled in
    production config (marked WIP).
  - Proposed: distinguish event sources. Only match deny patterns against
    (a) the user prompt, (b) commands the worker executes, (c) code the
    worker writes. Do NOT match against files the harness reads during
    orientation or files the worker reads for context.
  - Alternative: allow per-pattern scope: `deny = [{pattern = "DROP TABLE",
    scope = "prompt+commands"}]` instead of flat strings.

- Role dispatch: `skip_skills` / repo-agnostic mode
  - Current state: roles in config.toml can have `skills = ["pratchett-read",
    ...]` which resolve from `<cwd>/.claude/skills/<name>/SKILL.md`. When
    dispatching against a repo that doesn't have those skills, the dispatch
    fails with `config_error: skill not found`.
  - Impact: roles are project-scoped — they can't be used for cross-repo
    dispatches without falling back to raw engine/model/effort overrides.
  - Proposed: add `"skip_skills": true` to DispatchSpec JSON (or `--no-skills`
    CLI flag) that suppresses role-level skill injection. Alternatively,
    make missing skills a warning rather than a fatal error.
  - Alternative: per-dispatch skill override that replaces (not merges with)
    role skills: `"skills": []` in JSON should mean "no skills" rather than
    "use role defaults".
