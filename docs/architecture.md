# Architecture

agent-mux is a Go dispatch binary for AI coding harnesses. It gives the caller one execution contract while preserving the operational differences between Codex CLI, Claude Code, and Gemini CLI behind adapter boundaries.

It exists to solve three concrete problems: harness heterogeneity, poor supervision of long-running workers, and fragile artifact recovery when a run times out or dies mid-flight.

## Design Principles

The principles below are already reflected in code. Each one has an implementation consequence, not just a slogan.

| Principle | Implementation implication |
| --- | --- |
| Tool, not orchestrator | `cmd/agent-mux/main.go` resolves inputs and dispatches; it does not decide strategy beyond materializing one dispatch request. |
| Job done is holy | `internal/dispatch.EnsureArtifactDir` and `WriteDispatchMeta` run before process spawn so artifacts exist even if the harness later fails. |
| Errors are steering signals | `internal/dispatch.ErrorCatalog` normalizes failures into codes, messages, suggestions, and retryability for the caller. |
| Single-shot with curated context first | The default path is one `types.DispatchSpec` into one `engine.LoopEngine`. |
| Simplest viable dispatch | CLI flags, stdin JSON, role overlays, and timeout buckets resolve into a single materialized spec before execution. The engine loop does not keep reinterpreting config at runtime. |
| Config over code | `internal/config` owns merge semantics, role definitions, skill search paths, and timeout buckets; the binary stays generic. |

## Key Architecture Decisions

**Why Go.** The hard part of agent-mux is supervision, not string templating. The engine has to manage subprocess lifecycles, streamed event parsing, timeout escalation, inbox steering, and artifact persistence with clear failure boundaries. Go fits that shape directly: `LoopEngine.Dispatch` coordinates goroutines, `select`, `context.Context`, and process-group control without a runtime dependency chain. The result is a single deployable binary with concurrency as a first-class implementation tool rather than a framework feature.

**Why a single binary with adapters.** Codex CLI, Claude Code, and Gemini CLI differ in flags, auth expectations, and event formats, but their control loop is the same: build args, start a process, parse streaming output, supervise liveness, and assemble a normalized result. `types.HarnessAdapter` captures exactly that seam. `internal/engine` owns lifecycle and restart logic once, while `internal/engine/adapter` owns only engine-specific translation.

**Why artifact-first.** A dispatch can produce valuable output before it produces a clean final response. agent-mux therefore treats the artifact directory as part of the execution contract, not as post-processing. The prompt preamble points workers at `$AGENT_MUX_ARTIFACT_DIR`, `_dispatch_meta.json` is written at start, `events.jsonl` is appended throughout the run, and result assembly scans artifacts regardless of terminal state. This keeps partial work observable after timeouts, freezes, or caller interruption.

**Why no runtime budget enforcement.** The system reports token and cost metadata, but it does not kill a run based on live budget counters. The underlying harnesses emit usage information only at coarse boundaries, often after meaningful work has already happened. Wall-clock timeout is enforceable and predictable; runtime token policing would be approximate, engine-specific, and likely to terminate useful work mid-tool-call. The design keeps budgets as telemetry and leaves policy to the caller.

## System Diagram

```text
caller
  |
  v
+------------------+
| config resolver  |
| internal/config  |
+------------------+
  |
  v
+------------------+          +----------------------+
| dispatch router  |--------->| event emitter        |
| cmd/main +       |          | internal/event       |
| internal/dispatch|          | stderr + events.jsonl|
+------------------+          +----------------------+
  |
  v
+------------------+          +----------------------+
| LoopEngine       |--------->| supervisor           |
| internal/engine  |          | internal/supervisor  |
+------------------+          +----------------------+
  |
  v
+------------------+
| HarnessAdapter   |
| internal/engine/ |
| adapter          |
+------------------+
  |
  +-------------------+-------------------+
  |                   |                   |
  v                   v                   v
codex binary      claude binary      gemini binary
```

## Package Map

| Package | Owns |
| --- | --- |
| `cmd/agent-mux/main.go` | CLI flag parsing, mode detection, spec construction |
| `internal/config` | TOML loading, merge semantics, role and variant resolution |
| `internal/types` | Shared contracts: `DispatchSpec`, `DispatchResult`, `HarnessEvent`, `HarnessAdapter` |
| `internal/engine` | `LoopEngine` process lifecycle, event loop, timeout/watchdog/inbox handling |
| `internal/engine/adapter` | Adapter registry plus `CodexAdapter`, `ClaudeAdapter`, `GeminiAdapter` |
| `internal/supervisor` | `exec.Cmd` wrapper with process-group startup and signal handling |
| `internal/event` | `Emitter` NDJSON formatting, heartbeat ticker, dual-sink streaming |
| `internal/dispatch` | Traceability, artifact directory management, dispatch metadata |
| `internal/recovery` | Control records, artifact directory resolution, recovery prompt construction |
| `internal/inbox` | Append-under-lock inbox writes and atomic read-and-clear |
| `internal/hooks` | Deny and warn pattern evaluation, prompt safety preamble injection |

## Real Types

The architecture is organized around a small shared contract in `internal/types`.

```go
package types

type DispatchSpec struct {
	DispatchID   string
	Engine       string
	Model        string
	Effort       string
	Prompt       string
	SystemPrompt string
	Cwd          string
	ArtifactDir  string
	ContextFile  string
	TimeoutSec   int
	GraceSec     int
	MaxDepth     int
	Depth        int
	EngineOpts   map[string]any
	FullAccess   bool
}

type HarnessAdapter interface {
	Binary() string
	BuildArgs(spec *DispatchSpec) []string
	EnvVars(spec *DispatchSpec) ([]string, error)
	ParseEvent(line string) (*HarnessEvent, error)
	SupportsResume() bool
	ResumeArgs(spec *DispatchSpec, sessionID string, message string) []string
	StdinNudge() []byte
}
```

The CLI resolves one of these specs and then selects the adapter explicitly.

```go
registry := adapter.NewRegistry(cfg.Models)
adp, err := registry.Get(spec.Engine)
if err != nil {
	return err
}

hookEval := hooks.NewEvaluator(cfg.Hooks)
eng := engine.NewLoopEngine(adp, stderr, hookEval)
result, err := eng.Dispatch(ctx, spec)
```

## Data Flow

The dispatch path is intentionally front-loaded. By the time `LoopEngine` starts, the spec is already resolved and stable.

```text
DispatchSpec input
  |
  v
CLI or stdin decode
  |
  v
config.LoadConfig(...)
  |
  v
optional profile load
  `-> config.LoadProfile(...)
  |
  v
optional role lookup
  `-> config.ResolveRole(...)
  |
  v
optional variant overlay
  `-> resolveVariant(...)
  |
  v
defaults application
  `-> engine/model/effort/max_depth
  |
  v
timeout resolution
  `-> config.TimeoutForEffort(...) + timeout.grace
  |
  v
skill injection
  `-> config.LoadSkills(...) prepends <skill ...> blocks
  |
  v
context and recovery augmentation
  `-> context preamble + recovery.BuildRecoveryPrompt(...)
  |
  v
hook prompt check
  `-> hooks.Evaluator.CheckPrompt(...)
  |
  v
safety preamble injection
  `-> hooks.Evaluator.PromptInjection()
  |
  v
traceability and artifact setup
  `-> dispatch.WriteDispatchMeta(...)
  `-> recovery.RegisterDispatchSpec(...)
  |
  v
adapter selection
  `-> adapter.Registry.Get(spec.Engine)
  |
single dispatch path
  `-> engine.NewLoopEngine(...)
  `-> LoopEngine.Dispatch(...)
  |
  v
process spawn
  `-> adapter.BuildArgs(...)
  `-> adapter.EnvVars(...)
  `-> supervisor.NewProcess(...).Start()
  |
  v
event loop
  `-> adapter.ParseEvent(...)
  `-> event.Emitter.Emit(...)
  |
  v
result assembly
  `-> dispatch.BuildCompletedResult / BuildTimedOutResult / BuildFailedResult
```

## Concurrency Model

`internal/engine/loop.go` is the core concurrency boundary. One run starts one supervised harness process plus a small set of goroutines and timers around it.

For each active harness run, `startRun` creates:

- one scanner goroutine that reads stdout line by line and feeds parsed `loopSignal` values back to the main select loop
- one waiter goroutine that blocks on `proc.Wait()` and reports the exit result
- one parent-death watcher goroutine via `supervisor.WatchParentDeath(...)`

Separately, the event emitter starts its own heartbeat goroutine when `HeartbeatTicker(...)` is enabled.

The main dispatch loop stays single-threaded for state mutation. It multiplexes:

- harness signals from the scanner
- process completion from the waiter
- a watchdog ticker every `5s`
- an inbox ticker every `250ms`
- a soft timeout timer based on `spec.TimeoutSec`
- a hard timeout timer based on `spec.GraceSec`
- caller cancellation from `ctx.Done()`

The important invariant is that mutable run state such as `lastResponse`, `sessionID`, `activeCommand`, timeout state, and restart decisions is owned by the select loop and guarded where needed by a local mutex for helper closures. That keeps resume and timeout transitions deterministic even though signal sources are concurrent.

Process termination is handled at the process-group level. `internal/supervisor.NewProcess` sets `SysProcAttr{Setpgid: true}` so a harness and any subprocesses share a group, and `signalGroup` uses `syscall.Kill(-pgid, sig)` when available. `GracefulStop` sends `SIGTERM`, waits for the configured grace interval, then escalates to `SIGKILL` if the process group is still alive.

Inbox steering is intentionally decoupled from stdout parsing. Messages may be observed in two places:

- opportunistically inside `scanHarnessOutput`, immediately after a line arrives
- on the `250ms` inbox ticker, which prevents a quiet harness from starving coordinator input

That dual path matters because resume only works if steering can arrive while the harness is idle as well as while it is actively emitting output.

## Lifecycle Notes

The engine loop handles four terminal outcomes:

- completed
- timed out
- failed
- interrupted

Each terminal path writes status, updates `_dispatch_meta.json`, persists the store record, and keeps artifact discovery on the result. The only difference is which `dispatch.Build*Result` constructor is used and what error payload, if any, is attached. Store records are written via tmp-file + fsync + rename before the terminal status event is emitted to prevent observers from reading a status ahead of a queryable record.

After the waiter goroutine signals process exit (`streamDone`), the loop performs a second drain pass on the scanner channel. This catches `EventResponse` and other events that were emitted between the last scanner read and process exit — a narrow race on clean harness exits that previously caused the final response to be silently dropped.

Soft timeout is not immediate process death. On soft timeout, the loop writes a wrap-up message into the inbox and opens a hard-timeout timer for the grace period. That allows the harness to summarize and flush artifacts before the process group is stopped.

## Package Boundaries That Matter

Some package splits are structural rather than cosmetic:

- `internal/config` resolves intent; it does not execute anything
- `internal/engine` executes one run; it does not know TOML merge rules
- `internal/engine/adapter` normalizes engine-specific behavior; it does not own lifecycle
- `internal/dispatch` owns traceability and artifact metadata; it is used before and after process execution
- `internal/recovery` rehydrates prior runs using control records and artifact metadata, not in-memory state

This separation keeps new engine support, new config surfaces, and loop changes from collapsing into one package.

## Cross-References

- [Dispatch](./dispatch.md) for the `DispatchSpec` and `DispatchResult` contract
- [Engines](./engines.md) for adapter behavior and per-harness differences
- [Config](./config.md) for merge order, roles, variants, skills, and timeout buckets
- [Pipelines](./pipelines.md) for `PipelineConfig`, fan-out, and handoff modes
- [Recovery](./recovery.md) for control records, inbox signaling, and recovery prompts
- [Lifecycle](./lifecycle.md) for statuses, events, timeout escalation, and supervision states
- [CLI Reference](./cli-reference.md) for commands, flags, and stdin mode
