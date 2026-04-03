# Backlog

Open bugs, feature requests, spec gaps, and known limitations for agent-mux.
Replaces `FEATURES.md` (preserved at `_archive/FEATURES.md`).

**Prefix key:** `B-` bugs · `F-` features · `S-` spec gaps · `L-` limitations
**Priority:** P0 (next up) · P1 (soon) · P2 (planned) · P3 (parked)
**Rule:** Every decision, prioritization change, or "done" mark includes the session ID where it was decided.

---

## Strategic Direction

*Added 2026-03-30*

### Engine Priority Order
1. **Codex** — current focus. Perfect the dispatch, ax-eval coverage, steering, all rough edges. The bar must be top-notch before moving on.
2. **Gemini** — next engine after Codex reaches quality gate.
3. **Other harnesses** — after Gemini parity.

### Post-Freeze Plan
After this cleanup session, agent-mux enters a **multi-week development freeze**. No new features, no polish.

First post-freeze activity (1-2 weeks out): **Dissect the Codex CLI SDK (TypeScript)** end-to-end. Goal: hunt for patterns, undocumented capabilities, and integration points that agent-mux could leverage. Deep reading, not building.

### Current Posture
Codex-first. Ship quality. Resist the infinite polish trap.

---

## P0 — Next Up

### B-5: Config path resolution bug (`--cwd` doesn't pick up fixture config) — FIXED
**Type:** bug | **Priority:** P0 | **Status:** fixed — session `coordinator` (2026-03-30)
**Decided:** `coordinator` (2026-03-30)

`--cwd fixture/` doesn't pick up `.agent-mux/config.toml` from the fixture
directory. Config path resolution is anchored to the process working directory
rather than the `--cwd` value, so role/variant resolution silently falls back
to defaults (or fails) whenever a dispatch uses `--cwd` with a fixture that
has its own config.

**Root cause (dual):** (1) `configPaths()` did not absolutize relative `--cwd`
values before joining with `.agent-mux/config.toml`, so relative paths resolved
against process CWD. (2) The ax-eval `testdata/fixture/` seed directory was
missing the `.agent-mux/` config dir — `SetupFixtureDir()` copies testdata to a
temp dir, so the config was never present in test runs.

**Fix:** Absolutize cwd in `configPaths()` + copy `.agent-mux/` to `testdata/fixture/`.

---

### B-6: `host.pid` / `status.json` write-before-ack race — FIXED
**Type:** bug | **Priority:** P0 | **Status:** fixed — session `coordinator` (2026-03-30)
**Decided:** `coordinator` (2026-03-30)

After `--async` emits its ack JSON to stdout, `host.pid` and `status.json`
are not yet written to disk. Consumers that immediately check the filesystem
after receiving the ack find nothing — no PID file, no status file.

**Root cause:** Both `os.WriteFile` (host.pid) and `WriteStatusJSON` (atomic
rename) did not fsync before proceeding. The write ordering was already correct
(files before ack), but OS buffering meant consumers could see the ack before
the files were durable on disk.

**Fix:** Added explicit `f.Sync()` to both `WriteStatusJSON` and `writeAndSync`
(new helper for host.pid). The ack is now guaranteed to follow durable writes.

---

### B-7: `result --json` missing terminal status field — FIXED
**Type:** bug | **Priority:** P0 | **Status:** fixed — session `coordinator` (2026-03-30)
**Decided:** `coordinator` (2026-03-30)

`agent-mux result --json` output does not include a terminal status field
indicating whether the dispatch completed successfully, failed, or was killed.
Machine consumers (GSD agents, scripts) cannot distinguish outcomes without
parsing free-form log text.

**Fix:** Added `enrichResultStatus()` to both `runResultCommand` and `showResult`.
Derives `"status"` from store record > dispatch meta > status.json (priority
order). Adds `"kill_reason"` (frozen_killed, signal_killed, oom_killed,
startup_failed) for failed dispatches by scanning events.jsonl. Removed the
`t.Skip` workaround in `TestAsyncDispatchAndCollect`.

---

### S-4: 3 weak-assertion ax-eval cases
**Type:** spec gap | **Priority:** P0 | **Status:** partially resolved
**Decided:** `coordinator` (2026-03-30)

Three ax-eval cases have inadequate assertions:

1. **`handoff-summary-extraction`** — scored 1.0 in latest run (2026-03-30).
   `handoff_summary` field is now present in the output contract and
   correctly extracted. **Likely resolved.**

2. **`variant-resolution`** — was 0.5, blocked by B-5 (config path
   resolution). B-5 is now fixed. **Needs re-run** — expect score to improve
   post-fix. Assertions still check stderr events for model confirmation; may
   need hardening if `--stream` is not active during tests.

3. **`response-truncation`** — skipped. The assertion direction is inverted:
   the case checks `truncation=true` but truncation is disabled by design.
   **Fix needed: invert the assertion** to confirm truncation does NOT occur.
   Not a bug fix — assertion logic must be corrected.

**Target:** all three cases at score ≥ 0.7 after fixes.

---

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

### F-13: Session traceability and lookup normalization
**Type:** feature | **Priority:** P0 | **Status:** open
**Decided:** `rollout-2026-04-03T11-12-57-019d5230-18fd-7a01-927a-3aceb81a153a` (2026-04-03)

`agent-mux` already captures the harness `session_id` inside the engine loop,
but durable and first-mile surfaces stop at `dispatch_id`, `salt`, and
`trace_token`. That makes Codex / Claude resume correlation asymmetric: the
system knows the canonical session identity, but callers cannot retrieve it
reliably from the normal lifecycle commands.

**Intent:**
- Persist harness `session_id` into `status.json`, `_dispatch_meta.json`, and
  dispatch store records (`dispatches.jsonl`).
- Surface `session_id` in `status`, `inspect`, `list --json`, and
  `result --json`.
- Accept `trace_token` as a first-class lookup key anywhere a dispatch ref is
  accepted.
- Make clear that `salt` is a convenience label, not reliable identity. If a
  salt lookup is ever offered, it must fail on ambiguity rather than pretend
  uniqueness.

**Behavior gate:** after worker start, `agent-mux status --json <dispatch_id>`
returns `session_id`, and the same `session_id` is visible via `inspect`,
`list --json`, and `result --json`.

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

### F-14: Bare `agent-mux` should show curated help, not dispatch semantics
**Type:** feature | **Priority:** P1 | **Status:** open
**Decided:** `rollout-2026-04-03T11-12-57-019d5230-18fd-7a01-927a-3aceb81a153a` (2026-04-03)

Bare invocation currently falls into implicit dispatch mode and only later
fails on "missing prompt." That is backwards for a coordinator-facing CLI. A
zero-argument call should act like a front door, not a malformed dispatch.

**Intent:**
- Bare `agent-mux` and `agent-mux help` show a curated help menu instead of
  entering dispatch mode.
- Preserve `agent-mux <prompt>` shorthand for actual dispatches.

**Behavior gate:** `agent-mux` with no args exits `0`, emits structured help,
and creates no artifact dir or control record.

---

### B-9: `steer <dispatch_id> status` lives on the wrong command
**Type:** bug | **Priority:** P1 | **Status:** open
**Decided:** `rollout-2026-04-03T11-12-57-019d5230-18fd-7a01-927a-3aceb81a153a` (2026-04-03)

`agent-mux steer <id> status` duplicates the existing `status` surface while
placing an observational read under a mutating verb. The syntax is awkward,
the mental model is wrong, and the docs now have two ways to ask the same
question.

**Intent:**
- Make `agent-mux status <id>` the canonical live-status command.
- Keep `agent-mux steer <id> status` only as a compatibility alias for one
  release, then remove it.

**Behavior gate:** docs, help, and examples use `agent-mux status <id>`; the
deprecated alias returns the same payload during the transition window.

---

### B-10: `-cwd` long flag eats `-stdin` as its string value
**Type:** bug | **Priority:** P1 | **Status:** open
**Decided:** `coordinator` (2026-04-03)

Go's `flag` package treats any unrecognized token after a string flag as its
value. When flags are ordered `-cwd /path -stdin`, the parser correctly assigns
`/path` to `-cwd` — but if the path is omitted or the user writes
`-cwd /path -stdin` and the space-separated value resolution rolls over, the
flag package greedily consumes `-stdin` as the string value for `-cwd`. Result:
`cwd` is set to `"-stdin"`, the boolean `-stdin` is never set, and the dispatch
spec is silently corrupted. The single-character shorthand `-C /path` is
unaffected because `flag` handles single-char flags differently.

**Reproduction:**
```
echo '{"prompt":"test"}' | agent-mux -E codex -cwd /some/path -stdin
# preview shows "cwd":"-stdin" and prompt becomes the path
```

**Impact:** Any programmatic caller using `-cwd` before `-stdin` silently
receives a wrong cwd and an empty/wrong prompt. The dispatch proceeds — there
is no error — so callers have no signal that anything went wrong. Breaking for
all coordinator-driven Codex dispatches that use `-cwd` with `-stdin`.

**Fix direction:** Switch the long-form cwd flag to `--cwd` (double-dash) via
a POSIX-compliant flag library (e.g. `pflag`), or enforce flag ordering
convention so `-stdin` always precedes positional/path flags, or document and
enforce `--` separator usage. The cleanest fix is `pflag` with `--cwd` long
form and `-C` short form, matching the existing shorthand and eliminating the
ambiguity entirely.

---

## P2 — Planned

### B-8: Pipeline result assembly returns empty response
**Type:** bug | **Priority:** P2 | **Status:** open (experimental/deferred)
**Decided:** `coordinator` (2026-03-30)

Pipeline dispatches (`-P=name`) sometimes return an empty `response` in the
final `PipelineResult`. Individual step workers complete successfully and
produce output, but the pipeline result assembly fails to thread the final
step's response into the top-level result. This appears to be a race in
how step results are collected and merged.

**Impact:** Low — pipelines are experimental and GSD-Heavy manually
orchestrates rather than using the pipeline primitive. Deferred until pipeline
usage justifies investigation.

---

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

### F-12: `gc --dry-run` structured output
**Type:** feature | **Priority:** P2 | **Status:** open
**Decided:** `coordinator` (2026-03-30) — under discussion, may park if no real usage demand emerges.

`gc --dry-run` currently prints human-readable text. Machine consumers need
structured JSON output (list of paths that would be deleted + size recovered)
to build safe cleanup UIs or integrate with other tooling.

---

## P3 — Parked

### L-1: `response_max_chars` / truncation
**Type:** limitation | **Priority:** P3 | **Status:** parked — truncation removed by design
**Decided:** `acabe588` (2026-03-29) — truncation deleted in commit `51dbb23`. `coordinator` (2026-03-30) — confirmed not a priority.

Truncation is destructive and has been removed entirely. The `response_max_chars`
config field and related truncation logic no longer exist in the codebase.
The ax-eval `response-truncation` case (see S-4) should assert absence of
truncation, not presence. No further work on truncation as a feature.

---

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
| B-5: config path resolution (`--cwd`) | 3.2.1 | `coordinator` | Absolutize cwd in configPaths + copy .agent-mux to testdata/fixture |
| B-6: write-before-ack race | 3.2.1 | `coordinator` | Fsync host.pid + status.json before async ack emission |
| B-7: `result --json` status field | 3.2.1 | `coordinator` | enrichResultStatus() + kill_reason from events.jsonl |
| F-11: Codex soft stdin steering via named pipe | 3.2.0 | `1daaa1d6` | Commit `079b41a`. Protocol layer on F-6 plumbing — structured steering envelopes, tool-boundary-aware deferred delivery, state machine for steer vs. abort. |
| FM-7: process exit race (final EventResponse lost) | 3.2.2 | `coordinator` (2026-03-31) | Commit `6342c92`. Second drain pass after streamDone captures events emitted between last scanner read and process exit. |
| FM-4: hard timeout grace hardcoded to 5s | 3.2.2 | `coordinator` (2026-03-31) | Commit `6342c92`. GracefulStop now uses spec.GraceSec (floored at 10s). |
| FM-9: failed dispatches discarded accumulated response | 3.2.2 | `coordinator` (2026-03-31) | Commit `6342c92`. BuildFailedResult now preserves lastResponse/lastProgressText. Auditor pass (`04a6a18`): meta-write failure path also preserves partial response. |
| FM-15: status written before store record | 3.2.2 | `coordinator` (2026-03-31) | Commit `6342c92`. store.WriteResult completes before terminal status event is emitted. Auditor pass (`04a6a18`): store errors logged + fallback to full_output.md. |
| FM-8: non-atomic store.WriteResult | 3.2.2 | `coordinator` (2026-03-31) | Commit `6342c92`. Result records written via os.CreateTemp + fsync + rename. Auditor pass (`04a6a18`): unique temp files via os.CreateTemp (no fixed suffix). |
| Codex sandbox value validation | 3.2.2 | `coordinator` (2026-03-31) | Commit `6539644`. Whitelist: danger-full-access, workspace-write, read-only. Unknown values return structured error. |
| bufio scanner overflow graceful handling | 3.2.2 | `coordinator` (2026-03-31) | Commit `6539644`. Buffer raised 1MB→4MB; ErrTooLong detected and handled gracefully (line skipped with warning event). |
| ax-eval promoted to repo root | 3.2.2 | `coordinator` (2026-03-31) | Commit `4799d5e`. ax-eval/MANIFEST.md + ax-eval/PROTOCOL.md at repo root. Not a skill. |
| SKILL.md: artifact-dir escaping, flag syntax, sandbox anti-patterns | 3.2.2 | `coordinator` (2026-03-31) | Commit `c6178b0`. Three new reference sections in skill/SKILL.md. |
| Remove phantom --network flag from skill docs | 3.2.2 | `coordinator` (2026-03-31) | Commit `e4403f7`. Flag never existed in the binary; removed from docs. |
