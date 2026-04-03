# ax-eval
ax-eval is the behavioral test framework for agent-mux. It dispatches the real binary against a controlled fixture repository, evaluates outcomes with deterministic checks and optional LLM judging, and writes JSON reports for regression tracking.
It exists to answer a system-level question unit tests cannot: when agent-mux runs a worker, streams events, stores artifacts, exposes lifecycle commands, or hands work across async and recovery boundaries, does the whole path behave the way supervising agents actually depend on?
See also [Architecture](./architecture.md) for the runtime model ax-eval is exercising.

## Architecture Overview
ax-eval is compiled only with the `axeval` build tag and lives under `tests/axeval/`.
The runtime flow is:
1. `axeval_test.go` runs `TestMain`, copies the fixture repo into `/tmp`, builds the current `agent-mux` binary, initializes the case registry, and runs the suite.
2. `cases.go` and `cases_v2.go` declare behavioral scenarios as `TestCase` values.
3. `runner.go` turns a case into a real `agent-mux` invocation, supporting sync, async, and async-plus-steer execution.
4. `eval.go` and `eval_v2.go` score the result by checking status, response text, events, stderr, stdout JSON, and artifact files.
5. `judge.go` optionally runs a second dispatch that acts as an LLM judge when deterministic matching is not enough.
6. `report.go` writes `report.json`.
7. `trace.go` performs post-run trace analysis and writes `trace-report.json`.
The pieces map cleanly:
| Piece | Owns |
| --- | --- |
| `types.go` | shared case, result, event, verdict, and steering types |
| `runner.go` | dispatch orchestration and result parsing |
| `cases*.go` | executable contract definitions |
| `eval*.go` | reusable assertions |
| `judge.go` | semantic second-pass scoring |
| `report.go` | suite-level JSON aggregation |
| `trace.go` | behavioral analysis over `events.jsonl` and result metadata |
The important design choice is that ax-eval does not mock the dispatch path. It builds the real binary from `./cmd/agent-mux/` and exercises the same CLI and `--stdin` surfaces external callers use.

## Test Case Model
The core type is `TestCase` in `tests/axeval/types.go`. A case specifies:
- identity: `Name`, `Category`
- dispatch contract: `Engine`, `Model`, `Effort`, `Prompt`, `CWD`, `TimeoutSec`, optional `EngineOpts`
- transport mode: sync via `Evaluate(Result)`, async via `IsAsync` + `EvalAsync(ack, collected)`, or async with steering via `SteerSpec` + `EvalAsync`
- CLI modifiers: `ExtraFlags`
- execution controls: `MaxWallClock`, `SkipSkills`, `SkipReason`
- optional semantic rubric: `JudgePrompt`
Current categories are `completion`, `correctness`, `quality`, `liveness`, `error`, `events`, `streaming`, and `steering`.
`Result` captures one dispatch outcome: `Status`, `Response`, `ErrorCode`, `ErrorMessage`, parsed `Events`, `ArtifactDir`, `Duration`, `ExitCode`, plus raw stdout/stderr.
`Verdict` is the evaluator output: `Pass`, `Score`, `Reason`, and optional observed event types.

### How Cases Are Built
`InitCases()` populates `AllCases` by concatenating `buildCasesV1(cwd)` and `buildCasesV2(cwd)`.
V1 cases cover broad behavior:
- completion and simple file creation
- correctness over repo reading and command execution
- quality and multi-step output generation
- liveness and freeze handling
- error handling
- streaming, async dispatch, steering
- role dispatch
V2 cases focus on caller-facing contracts:
- stdout schema validation
- role system prompt delivery
- variant resolution
- artifact metadata
- handoff summary extraction
`CASES-V2.md` is the planning document for that expansion. `cases_v2.go` is the executable subset the suite currently enforces.

### Success Criteria
Cases define success in code, not prose. Typical checks are:
- dispatch status is `completed` or a specific failure class
- expected files exist in the artifact dir or fixture CWD
- stderr contains or omits certain event classes
- `events.jsonl` contains a required event or sequence
- stdout JSON contains required contract fields
- a judge confirms semantic correctness
The evaluator helpers in `eval.go` and `eval_v2.go` are intentionally small and composable. Common ones are `statusIs`, `statusIsOneOf`, `responseContains`, `artifactExists`, `errorCodeIs`, `hasEvent`, `hasEventSequence`, `stderrContains`, `stderrNotContains`, `noErrorEvents`, `hasErrorEvent`, and JSON field assertions for strings, numbers, booleans, and key presence. `compose(...)` ANDs checks and stops at the first failure.

## Fixture Model
The worker does not run against the agent-mux repository itself. `TestMain` copies `tests/axeval/testdata/fixture/` into a temporary directory under `/tmp`, and that copied directory becomes the worker CWD for the suite.
The seeded fixture contains `main.go`, `helpers.py`, `scripts/count.sh`, `scripts/fail.sh`, and `scripts/freeze.sh`.
This is enough to test file reads/writes, shell command execution, cross-language repo analysis, non-zero exit propagation, and freeze detection/watchdog behavior.
`tests/axeval/fixture/README.md` documents the intended isolation model: the fixture is treated as its own git boundary so workers cannot trivially walk up and inspect the parent agent-mux repo.

## Runner Flow
`runner.go` contains the execution helpers.
### Sync
`dispatch(...)` creates a temp artifact dir, marshals a JSON dispatch spec, runs `agent-mux --stdin --yes`, parses stdout as the result object, extracts status/response/error fields, and parses `events.jsonl`. If stdout is not valid JSON, the result is marked `parse_error`.
### Async
`startAsyncDispatch(...)` launches the process, reads exactly one stdout line, and treats that as the async acknowledgement. `dispatchAsync(...)` then parses `dispatch_id` from that ack and runs `agent-mux result <id> --json` to collect the final result.
### Async With Steering
`dispatchAsyncSteer(...)` starts async dispatch, waits `DelayBeforeSteer`, runs `agent-mux steer <dispatch_id> <action> [message]`, then collects with `result --json`, or `status --json` after abort.
Current steering actions covered by cases are `nudge`, `abort`, and `redirect`. `extend` is explicitly left as a TODO because a reliable timing-based integration test has not been built.
### CLI Mode
`dispatchWithFlags(...)` covers features that cannot be expressed purely through `--stdin`, including `--skill`, `--recover`, `--context-file`, lifecycle subcommands, config introspection, and preview subcommands.

## Evaluation Phases and Waves
ax-eval is split between the main case registry and several focused test files.
### Main Sweep: `TestAxEval`
`TestAxEval` iterates `AllCases`, runs deterministic evaluation first, then optional judge evaluation, and records each outcome into the report collector.
Cases in the timing-sensitive categories `liveness`, `events`, `streaming`, and `steering` are intentionally not parallelized.
### `p1_test.go`
This file covers features that need explicit CLI flows or multi-step setup:
- `TestSkillsInjection`: verifies `--skill` content reaches the worker
- `TestRecoveryRedispatch`: runs an initial dispatch, captures `dispatch_id`, then redispatches with `--recover`
- `TestContextFile`: verifies `--context-file` content reaches the worker through `$AGENT_MUX_CONTEXT`
- `TestEffortTiers`: compares low and high effort by parsing `dispatch_start` timeout values from stderr
### `lifecycle_test.go`
`TestLifecycleListStatusInspect` exercises the post-dispatch query path: dispatch a real job, parse its `dispatch_id`, verify `list -json` includes it, verify `status <id> -json` reports a completed state, and verify `inspect <id> -json` exposes `dispatch_id`, `response`, and `artifact_dir`.
### `wave2_test.go`
Wave 2 adds transport and async observability coverage through `TestStdinJsonDispatch` and `TestAsyncHostPidStatusJson`. This is the file that checks the raw `--stdin` path and immediate async host metadata such as `host.pid` and `status.json`.
### `wave3_test.go`
Wave 3 covers operator-facing surfaces and config contracts through `TestPreviewDryRun`, `TestConfigIntrospection`, and `TestSkillScriptsOnPath`.
Some wave-3 tests deliberately call `t.Skip(...)` when the contract is not yet enforced. That means the file documents intended coverage as well as current enforcement.

## The Judge
`judge.go` implements a strict LLM-as-judge pattern.
The judge receives the original task, the worker response, and a rubric. It is dispatched through the same binary using `engine=codex`, `model=gpt-5.4-mini`, `effort=high`, and `skip_skills=true`.
The judge is instructed to return only JSON:
```json
{"pass": true, "score": 0.9, "reason": "brief explanation"}
```
Important behavior from the code:
- judge evaluation runs only if deterministic evaluation already passed
- the helper extracts JSON even if the model wrapped it in Markdown fences
- a judge result counts as passing only when `pass == true` and `score >= 0.7`
- a judge failure downgrades the case in the final report
This tier is used where exact-string checks would be too weak, for example when the worker must identify a bug correctly or compare source files meaningfully.

## Trace System
`trace.go` adds a second evaluation layer based on what the worker actually did, not just what it returned.
### What It Reads
Trace verification combines:
- the dispatch result JSON on stdout
- `events.jsonl` from the artifact dir
- optional `gaal search` and `gaal inspect --tokens` enrichment
- a separate analyzer dispatch that scores a summarized event timeline
The core output is `TraceVerdict`, which records `case`, `dispatch_id`, optional `trace_session`, `pass`, behavioral `flags`, `reasoning`, `cost_usd`, `turns_used`, `tool_calls`, `error_count`, `first_action`, `source` (`llm` or `deterministic`), and optional token counts.
### Timeline and Flags
`parseTraceEvents(...)` reads a richer event shape than the basic `Event` used by regular cases. `buildTimelineSummary(...)` formats those events into a compact summary for the analyzer: heartbeats are skipped, the summary is capped at 100 events, and if the trace is longer it keeps the first 40 and last 60 with an omission marker.
`identifyFirstAction(...)` derives the first meaningful action from the event stream, preferring tool calls, commands, file reads, then file writes.
Possible analyzer flags include `efficient`, `wasteful`, `good_first_attempt`, `poor_first_attempt`, `recovered_from_error`, `error_spiral`, `over_engineered`, `under_delivered`, and `clean_completion`.
If the analyzer dispatch fails, ax-eval still emits a verdict using deterministic fallback logic from the event stream.
### `trace-report.json`
Trace reports are written either to `AX_EVAL_REPORT_DIR` or, if that env var is unset, beside the test files under `tests/axeval/`.
The JSON structure is:
```json
{
  "run_id": "trace-<unix>",
  "timestamp": "RFC3339",
  "verdicts": [],
  "summary": {
    "total": 0,
    "passed": 0,
    "failed": 0,
    "skipped": 0,
    "common_flags": []
  },
  "diff": {
    "regressions": [],
    "improvements": [],
    "stable": 0
  }
}
```
`diff` is computed by comparing the current verdicts with the previous `trace-report.json` at the same path. Regressions and improvements are tracked by case name.
`trace_test.go` contains both low-cost unit tests for formatting and flag logic, and `TestTraceVerification`, which reruns a representative subset of real cases and writes the trace report.

## Running Tests
ax-eval is behind the `axeval` build tag, so a normal `go test ./...` run does not include it.
### Full Suite
```bash
go test -tags axeval -timeout 600s ./tests/axeval/
```
### Single Case Or Focused Test
```bash
go test -tags axeval -timeout 300s -run 'TestAxEval/complete-simple' ./tests/axeval/
go test -tags axeval -timeout 300s -run TestLifecycleListStatusInspect ./tests/axeval/
```
### Reports
```bash
AX_EVAL_REPORT_DIR=./eval-reports go test -tags axeval ./tests/axeval/
```
This causes `report.go` to emit `report.json`, and any trace runs to emit `trace-report.json` to the same directory.
### Environment Requirements
From the code and `CI.md`, a working run needs:
- the repo to build `./cmd/agent-mux/`
- the runtime/auth setup required for real Codex dispatches, such as `CODEX_API_KEY`
- network access to the model backend
Optional:
- `gaal` on `PATH` for trace-session lookup and token/cost enrichment

## CI Integration
`tests/axeval/CI.md` defines the intended CI posture.
Key points:
- ax-eval is opt-in in CI because it is slow and incurs real model cost
- the suite should use longer test timeouts than normal unit tests
- `AX_EVAL_REPORT_DIR` should be set when CI needs machine-readable artifacts
The CI guidance calls out two known flaky cases: `cross-lang-read` and `freeze-stdin-nudge`.
It recommends PRs run deterministic subsets only, nightly runs execute the full suite, and pre-release runs execute the full suite multiple times.
Example from `CI.md`:
```bash
go test -tags axeval -timeout 300s \
  -run 'TestAxEval/(error|streaming|async|signal)' \
  ./tests/axeval/
```
Treat that example as policy guidance, not a generated index. The actual runnable subtests are defined by the case names in `cases.go` and `cases_v2.go`, plus the focused tests in the wave files.

## V2 Additions
V2 is the contract-focused layer added on top of the original behavior suite. The major difference from V1 is that V2 asserts structured caller-facing outputs instead of only checking broad behavior.
Implemented V2 coverage currently includes:
- `output-contract-schema`: validates stdout fields such as `schema_version`, `dispatch_id`, `activity`, `metadata`, and `duration_ms`
- `role-system-prompt-delivery`: verifies that a role-provided system prompt actually reaches the worker
- `variant-resolution`: checks whether the chosen variant is reflected in `dispatch_start` metadata
- `artifact-dir-metadata`: validates `events.jsonl` and `status.json` in the artifact directory, and `meta.json` under `~/.agent-mux/dispatches/<id>/`
- `handoff-summary-extraction`: probes the `handoff_summary` field
The code also makes clear that V2 is still partly aspirational:
- `response-truncation` is skipped because that contract is currently disabled in result assembly
- `variant-resolution` and `handoff-summary-extraction` still accept partial-credit TODO states in some branches
That distinction matters. `CASES-V2.md` is the roadmap; `cases_v2.go` is the current enforcement boundary.

## Reports
There are two JSON report outputs.
### `report.json`
`report.go` writes a suite summary containing `run_id`, `timestamp`, `cases[]`, and `summary`.
Each `CaseResult` stores `name`, `category`, `pass`, `score`, `reason`, `duration_ms`, optional observed `events`, and optional `judge_pass` / `judge_score`.
`summary` aggregates totals and pass/fail counts by category.
### `trace-report.json`
`trace.go` writes a separate report for behavioral trace verification.
Keep the two reports conceptually separate:
- `report.json` answers whether the case contract passed
- `trace-report.json` answers how the worker behaved while getting there

## Cross-References
- [Architecture](./architecture.md): overall dispatch and artifact model
- [`tests/axeval/CI.md`](../tests/axeval/CI.md): CI policy, cost, and flake guidance
- [`tests/axeval/CASES-V2.md`](../tests/axeval/CASES-V2.md): V2 gap analysis and planned contracts
