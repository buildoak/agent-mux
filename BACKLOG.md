# Backlog

Open bugs, feature requests, spec gaps, and known limitations for agent-mux.
Replaces `FEATURES.md` (preserved at `_archive/FEATURES.md`).

**Prefix key:** `B-` bugs · `F-` features · `S-` spec gaps · `L-` limitations

---

## Bugs

### B-1: Gemini response capture broken
**Type:** bug | **Priority:** high | **Status:** open
**Location:** `internal/engine/adapter/gemini.go`

Gemini dispatches return a truncated or empty `response` field despite
generating 1000+ output tokens. `turns: 0`, `tool_calls: []` in the result.

Root cause: the adapter calls `gemini -p <prompt> -o stream-json` and scans
stdout as NDJSON, but the scanner likely drops the final response event or
conflates stream events with the terminal result. Only tail fragments survive.

**Fix needed:** audit `gemini.go` stream-json parsing — identify which event
type carries the completed response text and ensure the scanner accumulates it
correctly before emitting the dispatch result.

---

### B-2: Hooks false positives on workspace reads
**Type:** bug | **Priority:** medium | **Status:** open (WIP — disabled in production)
**Location:** `internal/hooks/` (event matching logic)

`deny` / `warn` patterns are evaluated against ALL event content, including
files the harness reads during workspace orientation (not just agent-authored
output). A `deny = ["DROP TABLE"]` pattern fires when Codex reads a doc file
that mentions SQL injection examples.

**Impact:** hooks are unusable for repos containing documentation or test
fixtures that contain deny-pattern strings. Currently disabled in production
config as WIP.

**Proposed fix:** distinguish event sources — match deny patterns only against:
(a) the user prompt, (b) commands the worker executes, (c) code the worker
writes. Do NOT match against files the harness reads for context.

**Alternative:** per-pattern scope config:
```toml
deny = [{pattern = "DROP TABLE", scope = "prompt+commands"}]
```
instead of flat strings.

---

## Features

### F-1: Per-command timeout / hanging bash detection
**Type:** feature | **Priority:** medium | **Status:** open
**Location:** `internal/loop/loop.go`

Only a global silence watchdog exists. A legitimate 10-minute Rust build is
indistinguishable from a hung `curl`. No per-command timeout.

**Proposed:** track `tool_start` → `tool_end` pairs in `loop.go`. If
`tool_end` hasn't arrived within N seconds, emit a `long_command_warning`
event with the command name. Optionally classify known-long commands (`cargo`,
`make`, `nvcc`) for an extended grace period.

**Effort:** ~40 lines in `loop.go`, no harness changes.

---

### F-2: `--no-truncate` hard-disable flag
**Type:** feature | **Priority:** low | **Status:** open
**Location:** CLI flag parsing, `internal/dispatch/`

Response truncation is now configurable (`response_max_chars` default raised
to 16000, `response_truncated` event emitted, `full_output_path` in result
JSON). Remaining gap: no dedicated `--no-truncate` CLI flag or `response_max_chars: 0`
short-circuit to fully disable truncation without manually setting a large
integer.

**Proposed:** add `--no-truncate` flag (CLI) and honour `"no_truncate": true`
in the DispatchSpec JSON; map both to `response_max_chars = 0` internally.

---

### F-3: Pipeline orchestration enhancements
**Type:** feature | **Priority:** low | **Status:** open (core shipped in 3.0.0)
**Location:** `internal/pipeline/`

Core multi-step sequential pipelines shipped in 3.0.0. Follow-up items not
yet implemented:

- **Conditional branching** — skip or reroute a step based on the previous
  step's status or response content.
- **Fan-in aggregation** — collect results from parallel fan-out steps into
  a structured merge before continuing.
- **Pipeline-level timeout** — a ceiling on the total wall-clock time for the
  full pipeline, independent of per-step timeouts.

---

### F-4: Bundled agent auto-install / setup command
**Type:** feature | **Priority:** low | **Status:** open
**Location:** `agents/` directory, CLI

The `agents/` directory ships 6 role template files (researcher, coder,
reviewer, planner, debugger, writer) as starting points, but there is no
`agent-mux setup` or `agent-mux init` command to scaffold them into a new
project's config.

**Proposed:** `agent-mux init [--dir=<path>]` that:
1. Creates `.agent-mux/` directory.
2. Copies bundled role templates into `.agent-mux/config.toml`.
3. Creates `.agent-mux/prompts/` with placeholder system prompt files.
4. Prints a getting-started checklist.

---

### F-5: Session-local daemon / JSON-RPC control plane
**Type:** feature | **Priority:** low | **Status:** proposed
**Location:** new `internal/daemon/`

Current state: one-shot CLI dispatch is the only execution model.

**Proposed optional extension:** per-session daemon with a small JSON-RPC
surface for:
- Centralized streaming and control across multiple live dispatches.
- `attach`, `list`, `inspect`, and `signal` without polling the JSONL store.
- Better caller-death tolerance (daemon outlives the parent process).

**Why not now:** not needed for current Jenkins/operator workflows. The
JSONL-based lifecycle subcommands (`list`, `status`, `result`) cover the
immediate need.

---

## Spec Gaps

### S-1: `repeat_escalation` liveness
**Type:** spec gap | **Priority:** low | **Status:** open
**Reference:** `_archive/SPEC-V2.md` — freeze-twice escalation section

SPEC-V2 described a `repeat_escalation` behaviour: if the silence watchdog
fires twice in a row on the same dispatch (frozen twice), agent-mux escalates
by emitting a `frozen_escalation` event and optionally injecting a recovery
signal automatically.

Current implementation emits `frozen_warning` once and relies on the caller
to act. The double-freeze escalation path is not implemented.

---

### S-2: ax-eval instrumentation
**Type:** spec gap | **Priority:** low | **Status:** open
**Reference:** `_archive/SPEC-V2.md` — ax-eval section

SPEC-V2 proposed structured `ax_eval` behavioral events emitted during
dispatch:
- `error_correction` — agent noticed and self-corrected an error.
- `tool_retry` — a tool call was retried after failure.
- `scope_reduction` — agent narrowed scope mid-task.

These events would feed an evaluation pipeline. Not implemented — no
`ax_eval` event type exists in the current 15-type event catalog.

---

### S-3: ax-eval CI tests (LLM-in-the-loop behavioral tests)
**Type:** spec gap | **Priority:** low | **Status:** open
**Reference:** `_archive/SPEC-V2.md`

SPEC-V2 called for CI tests that run a live dispatch against a small fixture
repo and validate behavioral outcomes (files changed, commands run,
self-correction events) using an LLM-as-judge. These are distinct from unit
tests — they exercise the full dispatch loop end-to-end.

Not implemented. Current CI covers unit tests only. Blocked on S-2
(ax-eval events must exist before they can be asserted against).

---

## Known Limitations

### L-1: Gemini — no tool calling
**Type:** limitation | **Priority:** n/a | **Status:** accepted (documented)
**Location:** `internal/engine/adapter/gemini.go`

Gemini dispatches produce zero file reads, zero commands, zero tool calls.
The `gemini` CLI does not expose a tool-use surface comparable to Codex or
Claude Code.

**Impact:** Gemini variants are reasoning-only. All context must be supplied
in the prompt. Cannot read files or run commands during dispatch.

**Options (not committed):**
- (a) Accept as a known limitation — document Gemini as prompt-contained
  reasoning only. *Current stance.*
- (b) Investigate if `gemini` CLI supports tool definitions via config/flags.
- (c) Build a tool-use shim that pre-reads files into the prompt context.

---

## Shipped (reference)

Items recently completed — listed here for traceability.

| Item | Shipped | Notes |
|------|---------|-------|
| Role-level skills (`skills = [...]` in config.toml) | 3.0.0 | `Skills []string` on `RoleConfig`, merged with CLI `--skill` via `mergeSkills()` |
| `skip_skills` / `--skip-skills` (repo-agnostic dispatch) | 3.1.0 | CLI flag + DispatchSpec JSON field; enhanced skill-not-found error messages |
| `[skills] search_paths` config key | 3.1.0 | Union-merged across config layers with dedup; `agent-mux config skills` subcommand |
| Response truncation configurable | 3.0.0 | Default raised to 16000; `response_truncated` event; `full_output_path` field |
| Result JSON to stdout (was stderr) | 3.0.0 | `emitResult` now writes to stdout |
| Lifecycle subcommands (`list`, `status`, `result`, `inspect`, `gc`) | 3.0.0 | Human-readable default; `--json` flag; filter flags on `list` and `gc` |
| Pipeline orchestration (core) | 3.0.0 | Multi-step sequential pipelines in TOML |
| `-V` short flag removed | 3.1.0 | `-V` conflicted with `--variant`; use `--version` only |
