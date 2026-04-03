---
date: 2026-03-30
engine: codex/gpt-5.4-mini
status: complete
evaluator: claude-opus-4-6
---

> **Historical report — pre-foundation-cleanup. Cases and expectations below are outdated.**
> Pipeline system, response truncation, dispatch_salt, trace_token, gc subcommand, and _dispatch_meta.json
> have all been removed since this run. L0.3 field questions and L0.4 (pipeline vs dispatch) no longer apply.
> L1 error JSONs no longer include a `suggestion` field. Do not use this report as a baseline for current runs.

**AX Health: 95.6%**

## L0 -- Contract Comprehension -- avg 93.3%

| Scenario | Score | Items Met | Notes |
|----------|-------|-----------|-------|
| L0.1 Parse completed result | 90% | 9/10 | Missed: did not explicitly note error=null for completed dispatches |
| L0.2 Parse failed result | 100% | 8/8 | Clean pass. Correctly flagged full_output vs full_output_path mismatch |
| L0.3 Field meaning accuracy | 100% | 7/7 | All 7 distinctions correct. Partial on trace_token format but captured essence |
| L0.4 Pipeline vs dispatch | 80% | 4/5 | Missed: only listed summary_and_refs for handoff_mode; missed full_concat and refs_only |

## L1 -- Error Self-Correction -- avg 100%

| Scenario | Score | Items Met | Notes |
|----------|-------|-----------|-------|
| L1.1 engine_not_found | 100% | 5/5 | Switched to codex, clean explanation |
| L1.2 model_not_found | 100% | 5/5 | Switched to gpt-5.4, kept codex engine |
| L1.3 invalid_args (missing cwd) | 100% | 4/4 | Added --cwd /tmp, identified missing field |
| L1.4 frozen_killed | 100% | 5/5 | Narrowed prompt scope, recognized frozen issue |
| L1.5 config_error (bad role) | 100% | 4/4 | Switched to lifter, acknowledged invalid role |
| L1.6 max_depth_exceeded | 100% | 4/4 | Recognized not-retryable, suggested current-agent approach |
| L1.7 startup_failed | 100% | 4/4 | Listed 6 diagnostic steps, verified binary check before retry |

## L2 -- Skill Comprehension -- avg 95.9%

| Scenario | Score | Items Met | Notes |
|----------|-------|-----------|-------|
| L2.1 Audit-fix-verify pipeline | 100% | 11/11 | Perfect. jq status gates, correct roles, stderr redirect |
| L2.2 Parallel fan-out + synthesis | 90% | 9/10 | Missed: no failure handling for research dispatches |
| L2.3 Recovery workflow | 90% | 9/10 | Missed: no explicit `wait` before initial result collection |
| L2.4 Steering mid-flight | 100% | 8/8 | Correct steer syntax, wait after redirect, abort fallback |
| L2.5 Role and context passing | 100% | 10/10 | Scout->architect->lifter with context-file chaining |

## L3 -- GSD Comprehension -- avg 90%

| Scenario | Score | Items Met | Notes |
|----------|-------|-----------|-------|
| L3.1 Heavy: novel problem | 100% | 10/10 | Pre-mortem, 5 verification gates, engine cognitive style awareness |
| L3.2 Light: known pipeline | 80% | 8/10 | Missed: no context passing between steps, no status check between steps |
| L3.3 Heavy: recovery strategy | 100% | 10/10 | Sharding, idempotent design, two-failure rule, escalation path |
| L3.4 Light: parallel fan-out | 80% | 8/10 | Missed: no result aggregation, no failure handling for individual scans |

## Failed Items

**L0.1** -- Did not explicitly note that `error` is null for completed dispatches (the JSON lacked the field, and the agent didn't flag its absence).

**L0.4** -- Only identified `summary_and_refs` for handoff_mode. The docs also mention `full_concat` and `refs_only`.

**L2.2** -- No mention of what happens if one research dispatch fails (no conditional or fallback).

**L2.3** -- Missing explicit `wait` call before initial result collection. Shows `--async` dispatch and then jumps to `result` without `wait`.

**L3.2** -- Steps execute sequentially but no `-context-file` shown passing architect output to lifter, or lifter output to auditor. No status check between steps.

**L3.4** -- Collects all 4 results but does not aggregate/summarize findings. No handling for case where one scan fails.

## Delta from Previous Run

Previous: L0=86%, L1=75%, L2=57%, L3=skipped

| Tier | Previous | Current | Delta | Notes |
|------|----------|---------|-------|-------|
| L0 | 86% | 93.3% | +7.3pp | Better field-by-field parsing |
| L1 | 75% | 100% | +25pp | L1.7 fix (no-execute instruction) eliminated env investigation failure |
| L2 | 57% | 95.9% | +38.9pp | No-execute instruction eliminated timeout failures; all 5 completed |
| L3 | skipped | 90% | new | GSD prompts now available; Heavy scored 100% both scenarios |
| **AX Health** | **N/A** | **95.6%** | -- | First full 4-tier run |

Key improvements:
- **L1.7 fixed:** "Do not investigate" instruction prevented the agent from wasting time probing the environment. Clean diagnostic plan returned.
- **L2 no-execute instruction eliminated timeouts:** Previous run had 2/5 L2 scenarios time out because agents tried to execute commands in the sandbox. New prompt prefix completely fixed this. All 5 completed with high quality.
- **L3 now evaluable:** GSD-Heavy and GSD-Light prompts injected. Heavy showed strong strategic depth (pre-mortems, verification gates, engine awareness). Light showed appropriate mechanical execution with room to improve on context passing and status checking.
- **Weakest pattern:** Failure handling in fan-out scenarios (L2.2, L3.4). Agents plan the happy path well but don't proactively plan for partial fan-out failures.
