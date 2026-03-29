# Backlog

Open bugs, feature requests, spec gaps, and known limitations for agent-mux.
Replaces `FEATURES.md` (preserved at `_archive/FEATURES.md`).

**Prefix key:** `B-` bugs · `F-` features · `S-` spec gaps · `L-` limitations
**Priority:** P0 (next up) · P1 (soon) · P2 (planned) · P3 (parked)
**Rule:** Every decision, prioritization change, or "done" mark includes the session ID where it was decided.

---

## P0 — Next Up

### F-6: Soft steering via stdin before hard kill — SHIPPED
**Type:** feature | **Priority:** P0 | **Status:** shipped — commit `2fd7fda`, session `acabe588`
**Location:** `internal/engine/loop.go` (watchdog), adapters
**Decided:** `acabe588` (2026-03-29)

Current liveness: warn at 90s (pure observation), hard kill at 180s. The 90s
gap is dead air — no steering attempt. This was the original design intent of
the LoopEngine architecture, but **stdin pipe was never implemented** — no
`StdinPipe`, no `io.Writer`, no `Write` call to the child process exists
anywhere in `loop.go` or adapters.

The current "recovery" path is inbox-based: write a file → watchdog polls →
kill and relaunch with `ResumeArgs`. This is restart, not steering.

**Implementation needed:**
1. Add `cmd.StdinPipe()` in `startRun` (`loop.go:322-352`)
2. At warn threshold (90s), write nudge to stdin
3. Codex-first: `"\n"` or continuation prompt (stdin-driven by design)
4. Claude/Gemini: lower feasibility, investigate later

**Observed impact:** GSD session `a2608f024a0eb520b` lost a Codex worker that
froze at 244s. A stdin nudge at 90s could have unblocked it without killing.

---

### F-8: Distinct error codes for process_killed — SHIPPED
**Type:** feature | **Priority:** P0 | **Status:** shipped — commit `e58c3a8`, session `acabe588`
**Location:** `internal/engine/loop.go`, `internal/types/types.go`
**Decided:** `acabe588` (2026-03-29)

`process_killed` is a generic status covering: frozen detection, OOM, startup
failure, and signal kill. GSD assumed OOM when the actual cause was frozen
detection (`frozen_tool_call` at 244s silence). Distinct codes
(`frozen_killed`, `oom_killed`, `startup_failed`, `signal_killed`) would
enable correct diagnosis without reading logs. Mechanical fix.

---

### F-1: Per-command timeout / hanging bash detection — SHIPPED
**Type:** feature | **Priority:** P0 | **Status:** shipped as "long command detection" — commit `2fd7fda`, session `acabe588`
**Location:** `internal/engine/loop.go`
**Decided:** `acabe588` (2026-03-29)

Only a global silence watchdog exists. A legitimate 10-minute Rust build is
indistinguishable from a hung `curl`. Risk of false positive kills is too
high — "job done is holy." The watchdog must not kill agents running
legitimate long-running commands.

**Investigation needed:** Can we distinguish between "process is silent because
it's dead" vs "process is silent because `cargo build` takes 8 minutes"?
Options:
- Track `tool_start` → `tool_end` pairs — if a known-long command is running,
  extend the silence threshold automatically
- Check if the child process is still alive (waitpid/WNOHANG) before killing
- Classify commands: `cargo`, `make`, `nvcc`, `go build` get extended grace

**Effort:** ~40 lines in `loop.go`, but design decision on classification
approach needed first.

---

## P1 — Soon

### F-9: `--quiet` output mode — SHIPPED (superseded)
**Type:** feature | **Priority:** P1 | **Status:** shipped — superseded by Streaming Protocol v2 Tier 1, session `current` (2026-03-29)

Superseded by Streaming Protocol v2 (3.2.0): silent stderr is now the default.
`--stream` opt-in restores full event streaming. The original `--quiet` proposal
is no longer needed — the default behavior is what `--quiet` would have been.

---

### S-2: ax-eval instrumentation — SHIPPED
**Type:** spec gap | **Priority:** P1 | **Status:** shipped — 26 cases, gaal trace verification layer, session `current` (2026-03-29)
**Reference:** `_archive/SPEC-V2.md` — ax-eval section

Build a proper ax-eval testing framework using gpt-5.4-mini high as the
judge. Structured `ax_eval` behavioral events emitted during dispatch:
- `error_correction` — agent noticed and self-corrected an error.
- `tool_retry` — a tool call was retried after failure.
- `scope_reduction` — agent narrowed scope mid-task.

These events feed an evaluation pipeline for measuring dispatch quality.

**Shipped:** 26 cases (15 original + 11 new), gaal trace verification layer
confirms behavioral events are being emitted and indexed correctly.

---

### S-3: ax-eval CI tests (LLM-in-the-loop behavioral tests) — SHIPPED
**Type:** spec gap | **Priority:** P1 | **Status:** shipped — CI.md guide written, session `current` (2026-03-29)
**Reference:** `_archive/SPEC-V2.md`

CI tests that run a live dispatch against a small fixture repo and validate
behavioral outcomes (files changed, commands run, self-correction events)
using gpt-5.4-mini high as judge. CI.md guide written covering fixture setup,
test invocation, and expected pass/fail criteria.

---

## P2 — Planned

### F-3: Pipeline orchestration enhancements
**Type:** feature | **Priority:** P2 | **Status:** open (core shipped in 3.0.0)
**Location:** `internal/pipeline/`
**Design:** `references/streaming-protocol-v2.md` § "Future: Pipeline Verification Gates" (branching context)
**Decided:** `acabe588` (2026-03-29)

Core sequential pipelines + within-step fan-out shipped. **Advanced features
never built** (confirmed via code audit — zero fields for conditions,
branching, or fan-in in `PipelineStep`):

- **Conditional branching** — skip or reroute based on previous step status
- **Fan-in aggregation** — merge parallel fan-out results
- **Pipeline-level timeout** — ceiling on total wall-clock time

---

### F-10: Pipeline verification gates
**Type:** feature | **Priority:** P2 | **Status:** open (park alongside F-3) — design documented
**Location:** `internal/pipeline/`
**Design:** `references/streaming-protocol-v2.md` § "Future: Pipeline Verification Gates"
**Decided:** `current` (2026-03-29)

Field analysis of GSD-Heavy usage reveals a structural gap in pipelines: they
are useful for fire-and-forget known sequences but **cannot handle mid-flight
judgment**. The `build` pipeline exists but GSD-Heavy — the most sophisticated
user — does not use it. It manually orchestrates because pipelines lack
verification gates between steps (e.g., `cargo build --release` must succeed
before the next lifter wave launches). The highest actual pipeline value is
`tenx` fan-out.

**Root cause:** there is no mechanism to assert that a prior step's side effects
are correct before proceeding. Adding another LLM auditor step is not the
answer — it's slow, expensive, and introduces hallucination risk.

**Proposed:** executable verification gates between pipeline steps — a shell
command that must exit 0 for the step to be considered complete. If the gate
fails, the pipeline halts with a descriptive error. This is deterministic,
fast, and closes the gap that forces GSD-Heavy into manual orchestration.

**Relation to F-3:** conditional branching (F-3) and verification gates (F-10)
are complementary — gates catch failures, branching handles them. Both park at
P2 until pipeline usage justifies the investment.

---

### S-1: `repeat_escalation` liveness
**Type:** spec gap | **Priority:** P2 | **Status:** open — not implemented, design documented
**Reference:** `_archive/SPEC-V2.md`
**Design:** `references/streaming-protocol-v2.md` § "Future: Repeat Escalation Liveness"
**Decided:** `acabe588` (2026-03-29)

**Not implemented** (confirmed: zero matches for `repeat_escalation`,
`frozen_escalation`, or `escalat` in codebase). The watchdog has a
warn-then-kill two-stage reaction but no repeat-escalation logic. Related to
F-6 (soft steering) — both touch the liveness system.

---

### F-11: Codex soft stdin steering
**Type:** feature | **Priority:** P2 | **Status:** open — design documented
**Design:** `references/streaming-protocol-v2.md` § "Codex Soft Stdin Steering"
**Decided:** `current` (2026-03-29)

The F-6 stdin nudge (warn-threshold write) is implemented. The next layer is
*soft steering*: sending structured prompts via stdin to redirect a Codex
worker mid-flight without killing it — nudge, redirect, extend-scope, all as
continuation input rather than process restart. Design doc covers the envelope
format, tool-boundary-awareness (deferred delivery until active tool
completes), and the state machine for steering vs. aborting.

**Relation to F-6 / S-1:** F-6 is the plumbing (stdin pipe exists, nudge at
warn threshold). F-11 is the protocol layer on top. S-1 (repeat escalation)
would use F-11 as its delivery mechanism.

---

## P3 — Parked

### B-1: Gemini response capture broken
**Type:** bug | **Priority:** P3 | **Status:** parked
**Location:** `internal/engine/adapter/gemini.go`
**Decided:** `acabe588` (2026-03-29) — "P3, we will do it when adding gemini.
For now we work on codex and claude code engines mostly."

Gemini dispatches return truncated or empty responses. NDJSON parser drops
content. Fix when Gemini becomes a primary engine.

---

### B-2: Hooks — needs rethink and re-implementation
**Type:** bug | **Priority:** P3 | **Status:** parked (disabled in production)
**Location:** `internal/hooks/`
**Decided:** `acabe588` (2026-03-29) — "Hooks are P3, feature we need to
rethink and re-implement."

False positives on workspace reads. Entire hooks system needs redesign with
proper event source scoping before re-enabling.

---

### F-4: Bundled agent auto-install / setup command
**Type:** feature | **Priority:** P3 | **Status:** parked
**Decided:** `acabe588` (2026-03-29)

---

### F-5: Session-local daemon / JSON-RPC control plane
**Type:** feature | **Priority:** P3 | **Status:** parked
**Decided:** `acabe588` (2026-03-29)

---

## Closed

### F-2: `--no-truncate` hard-disable flag — CLOSED
**Status:** closed — truncation removed entirely in P0-1 (`51dbb23`)
**Decided:** `acabe588` (2026-03-29)

---

### B-3: freeze-stdin-nudge test flakiness — FIXED
**Type:** bug | **Status:** fixed, session `current` (2026-03-29)

freeze-stdin-nudge test was flaky due to non-deterministic prompt format and
fragile envelope parsing. Fixed: deterministic prompt construction + hardened
envelope parser. Now passes reliably.

---

### B-4: Gaal Codex session indexing — FIXED (misdiagnosis)
**Type:** bug | **Status:** fixed / closed — misdiagnosed, session `current` (2026-03-29)

Reported as "gaal does not index Codex sessions." Root cause was wrong: gaal
*does* index Codex sessions correctly. The bug was in the search result
parsing layer (incorrect field extraction from the index response). Parsing
fixed; no changes needed to the indexer itself.

---

## Shipped (reference)

All items include session ID where the work was done.

| Item | Shipped | Session | Notes |
|------|---------|---------|-------|
| SPEC_V3 Phase 5 sweep (reaper, inspect, gc, tests) | 3.1.0 | `ecca0bdb` | 4 commits, +3918 lines |
| Docs alignment (22 drift items) | 3.1.0 | `acabe588` | 10 files updated |
| SKILL.md redesign | 3.1.0 | `acabe588` | 402→214 lines, decision-tool rewrite |
| `agent-mux config` subcommand | 3.1.0 | `acabe588` | roles, pipelines, models, skills, --sources |
| `skill_paths` config + enhanced errors | 3.1.0 | `acabe588` | Union-merged search paths, role name in errors |
| `--skip-skills` flag + `-V` removal | 3.1.0 | `acabe588` | Escape hatch + flag conflict fix |
| scripts/ dir wired for Claude/Gemini | 3.1.0 | `acabe588` | addDirs in all adapters |
| GSD agent definition updates | 3.1.0 | `acabe588` | Pre-flight, no raw flags, timeout alignment |
| Remove response truncation | 3.1.0 | `acabe588` | TruncateResponse deleted, default 0 |
| Fix `gc --older-than` parsing | 3.1.0 | `acabe588` | flagTakesValue + 4 tests |
| Lifecycle test coverage | 3.1.0 | `acabe588` | 14 new tests (inspect, gc, config skills) |
| Lifecycle docs | 3.1.0 | `acabe588` | cli-flags.md + DOCS.md |
| Archive specs → `_archive/` | 3.1.0 | `acabe588` | Docs are ground truth |
| BACKLOG.md consolidation | 3.1.0 | `acabe588` | Replaces FEATURES.md |
| F-7: skill loading root cause | 3.1.1 | `coordinator` | Root cause was absent search_paths (pre-fix session). search_paths fix covers GSD scenario. Ghost-dir bug in collectSkills fixed + test. |
| F-6: stdin nudge before hard kill | 3.1.x | `acabe588` | Commit `2fd7fda`. Warn-threshold stdin write implemented for Codex. |
| F-8: distinct error codes for process_killed | 3.1.x | `acabe588` | Commit `e58c3a8`. `frozen_killed`, `oom_killed`, `startup_failed`, `signal_killed` added. |
| F-1: long command detection (per-command timeout) | 3.1.x | `acabe588` | Commit `2fd7fda`. Known-long commands extend silence threshold automatically. |
| Streaming Protocol v2 Tier 1: silent stderr default | 3.2.0 | `current` | `--stream` opt-in, silent default, bookend + failure events only |
| Streaming Protocol v2 Tier 2: async dispatch | 3.2.0 | `current` | `--async`, `ax wait`, `status.json`, `host.pid`; background dispatch with polling |
| Streaming Protocol v2 Tier 3: mid-flight steering | 3.2.0 | `current` | `ax steer` abort/nudge/redirect/extend/status via control.json + inbox |
| Tool-boundary-aware steering | 3.2.0 | `current` | Deferred resume until active tool completes; no torn tool calls |
| Fix: engine_opts per-dispatch precedence | 3.2.0 | `current` | Per-dispatch engine_opts no longer overwritten by config defaults |
| Fix: `--stdin` CLI flag merge | 3.2.0 | `current` | Flags now merged into JSON spec; `--stdin` wired correctly |
| S-2: ax-eval expansion (26 cases) | 3.2.0 | `current` | 15 original + 11 new cases; gaal trace verification layer |
| S-3: ax-eval CI (CI.md guide) | 3.2.0 | `current` | CI.md written; fixture setup, invocation, pass/fail criteria |
| Fix: freeze-stdin-nudge flakiness | 3.2.0 | `current` | Deterministic prompt + hardened envelope parsing; test now reliable |
| Fix: fixture git isolation | 3.2.0 | `current` | Test fixtures no longer leak git state between runs |
| Design docs: soft stdin steering, pipeline gates, repeat escalation | 3.2.0 | `current` | `references/streaming-protocol-v2.md` extended with three future-design sections |
| F-9: `--quiet` flag | 3.2.0 | `current` | Superseded — silent stderr is the new default |
