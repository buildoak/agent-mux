# Architecture

agent-mux is a Go dispatch binary for AI coding harnesses. It gives the caller one execution contract while preserving the operational differences between Codex CLI, Claude Code, Gemini CLI, and experimental agy behind adapter boundaries.

It exists to solve three concrete problems: harness heterogeneity, poor supervision of long-running workers, and fragile artifact recovery when a run times out or dies mid-flight.

## Design Principles

The principles below are already reflected in code. Each one has an implementation consequence, not just a slogan.

| Principle | Implementation implication |
| --- | --- |
| Tool, not orchestrator | `cmd/agent-mux/main.go` resolves inputs and dispatches; it does not decide strategy beyond materializing one dispatch request. |
| Job done is holy | `internal/dispatch.EnsureArtifactDir` and `dispatch.RegisterDispatchSpec(...)` establish the artifact/control path before the harness starts, and `LoopEngine.Dispatch` persists dispatch refs before process spawn. |
| Errors are steering signals | `internal/dispatch.ErrorCatalog` normalizes failures into codes, messages, suggestions, and retryability for the caller. |
| Single-shot with curated context first | The default path is one `types.DispatchSpec` into one `engine.LoopEngine`. |
| Prompt over config | Worker identity lives in `.md` files at `~/.agent-mux/prompts/` with YAML frontmatter defaults. No TOML, no merge chain, no role resolution. The binary is generic; the prompt is the worker. |
| Simplest viable dispatch | CLI flags, frontmatter defaults, and timeout resolution produce a single materialized spec before execution. The engine loop does not reinterpret config at runtime. |

## The Prompt-Driven Model

agent-mux's configuration model is: **prompt files define worker identity, CLI flags override infrastructure, engines are interchangeable execution backends.**

A prompt file at `~/.agent-mux/prompts/<name>.md` carries everything a worker needs to be itself:

```text
YAML frontmatter          ->  engine, model, effort, timeout, skills
Markdown body             ->  system prompt (the worker's personality and instructions)
```

Resolution is a single pass: hardcoded defaults, then frontmatter overlay, then CLI/JSON overrides. No merge chain, no layered config files. The binary loads one prompt file and materializes one `DispatchSpec`.

`config prompts` discovers all available profiles from `~/.agent-mux/prompts/` and surfaces their metadata for programmatic selection.

## Key Architecture Decisions

**Why Go.** The hard part of agent-mux is supervision, not string templating. The engine has to manage subprocess lifecycles, streamed event parsing, timeout escalation, inbox steering, and artifact persistence with clear failure boundaries. Go fits that shape directly: `LoopEngine.Dispatch` coordinates goroutines, `select`, `context.Context`, and process-group control without a runtime dependency chain. The result is a single deployable binary with concurrency as a first-class implementation tool rather than a framework feature.

**Why a single binary with adapters.** Codex CLI, Claude Code, Gemini CLI, and agy differ in flags, auth expectations, event formats, and resume semantics, but their control loop is the same: build args, start a process, parse available output, supervise liveness, and assemble a normalized result. `types.HarnessAdapter` captures exactly that seam. `internal/engine` owns lifecycle and restart logic once, while `internal/engine/adapter` owns only engine-specific translation. Agy is the plain-stdout outlier: it has no structured event stream, token/cost telemetry, or live FIFO steering, so the adapter relies on final stdout, artifact scanning, and `agy.log` conversation discovery.

**Why artifact-first.** A dispatch can produce valuable output before it produces a clean final response. agent-mux therefore treats the artifact directory as part of the execution contract, not as post-processing. The prompt preamble points workers at `$AGENT_MUX_ARTIFACT_DIR`, `cmd/agent-mux` registers the dispatch before launch, `LoopEngine.Dispatch` writes `_dispatch_ref.json` and starts `events.jsonl`, and result assembly scans artifacts regardless of terminal state. This keeps partial work observable after timeouts, freezes, or caller interruption. A separate durable store at `~/.agent-mux/dispatches/<dispatch_id>/` (`meta.json` + `result.json`) provides a queryable, home-directory-stable record of every dispatch independent of the artifact directory lifecycle, while the artifact directory carries run-local files such as `status.json`, `inbox.md`, and user-written artifacts.

**Why a unified steering package.** Steering now lives under `internal/steer`. The package exposes `Delivery{InboxDir, FIFOPath}` plus the lower-level inbox and FIFO helpers used by the engine and CLI. The inbox path is the durable, portable steering mechanism; the FIFO path is a Unix-only optimization for live Codex nudges and redirects. Keeping both channels in one package avoids split ownership between durable coordinator messages and low-latency soft steering.

**Why no runtime budget enforcement.** The system reports token and cost metadata, but it does not kill a run based on live budget counters. The underlying harnesses emit usage information only at coarse boundaries, often after meaningful work has already happened. Wall-clock timeout is enforceable and predictable; runtime token policing would be approximate, engine-specific, and likely to terminate useful work mid-tool-call. The design keeps budgets as telemetry and leaves policy to the caller.

## System Diagram

```text
caller
  |
  v
+------------------+
| prompt resolver  |
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
  +-------------------+-------------------+-------------------+
  |                   |                   |                   |
  v                   v                   v                   v
codex binary      claude binary      gemini binary      agy binary
```

## Package Map

| Package | Owns |
| --- | --- |
| `cmd/agent-mux` | CLI commands, spec construction, stdin/async/recover/steer entry points |
| `internal/config` | Profile loading, frontmatter parsing, skill discovery, timeout/liveness defaults |
| `internal/types` | Shared contracts: `DispatchSpec`, `DispatchResult`, `HarnessEvent`, `HarnessAdapter` |
| `internal/dispatch` | Artifact directory setup, persistent meta/result store, recovery helpers, live status I/O |
| `internal/steer` | Unified steering delivery: `Delivery`, inbox helpers, FIFO helpers |
| `internal/hooks` | Deny and warn pattern evaluation, prompt safety preamble injection |
| `internal/engine` | `LoopEngine` process lifecycle, event loop, timeout/watchdog logic, resume/restart, soft-stdin bridge |
| `internal/engine/adapter` | Adapter registry plus `CodexAdapter`, `ClaudeAdapter`, `GeminiAdapter`, `AgyAdapter` |
| `internal/supervisor` | `exec.Cmd` wrapper with process-group startup and signal handling |
| `internal/event` | `Emitter` NDJSON formatting, heartbeat ticker, dual-sink streaming |

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
optional profile load
  `-> config.LoadProfile(...)
  |
  v
defaults application
  `-> engine/model/effort/max_depth
  |
  v
timeout resolution
  `-> hardcoded default + proportional grace
  |
  v
skill injection
  `-> config.LoadSkills(...) prepends <skill ...> blocks
  |
  v
context and recovery augmentation
  `-> context preamble + dispatch.BuildRecoveryPrompt(...) when --recover is set
  |
  v
hook prompt check
  `-> hooks.Evaluator.CheckPrompt(...)
  |
  v
adapter selection
  `-> adapter.Registry.Get(spec.Engine)
  |
  v
dispatch registration
  `-> dispatch.EnsureArtifactDir(...)
  `-> dispatch.RegisterDispatchSpec(...)
  |
  v
single dispatch path
  `-> engine.NewLoopEngine(...)
  `-> LoopEngine.Dispatch(...)
     -> dispatch.WritePersistentMeta(...)
     -> dispatch.WriteDispatchRef(...)
     -> steer.CreateInbox(...)
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

Steering is intentionally split into two delivery paths inside `internal/steer`: the durable inbox and the optional `stdin.pipe` FIFO bridge for live Codex soft steering.

Inbox messages may be observed in three places:

- opportunistically inside `scanHarnessOutput`, immediately after a line arrives
- on the `250ms` inbox ticker, which prevents a quiet harness from starving coordinator input
- on the `5s` watchdog ticker, which gives the loop another chance to drain queued coordinator input

That dual path matters because resume only works if steering can arrive while the harness is idle as well as while it is actively emitting output. The FIFO path is separate: when `status.json` reports `stdin_pipe_ready`, the loop can decode a soft-steer envelope and write formatted input directly to the live Codex stdin pipe without a stop-and-resume cycle.

## Lifecycle Notes

The engine loop handles four terminal outcomes:

- completed
- timed out
- failed
- interrupted

Each terminal path writes `result.json` to the durable store at `~/.agent-mux/dispatches/<dispatch_id>/` and then writes terminal `status.json` in the artifact directory, keeping artifact discovery on the result. The only difference is which `dispatch.Build*Result` constructor is used and what error payload, if any, is attached. Store records are written via tmp-file + fsync + rename before the terminal status event is emitted to prevent observers from reading a status ahead of a queryable record. `ReadDispatchMeta` still understands legacy `_dispatch_meta.json`, but current runs use `_dispatch_ref.json` plus the persistent store as the primary metadata path.

After the waiter goroutine signals process exit (`streamDone`), the loop performs a second drain pass on the scanner channel. This catches `EventResponse` and other events that were emitted between the last scanner read and process exit -- a narrow race on clean harness exits that previously caused the final response to be silently dropped.

Soft timeout is not immediate process death. On soft timeout, the loop writes a wrap-up message into the inbox and opens a hard-timeout timer for the grace period. That allows the harness to summarize and flush artifacts before the process group is stopped.

## Package Boundaries That Matter

Some package splits are structural rather than cosmetic:

- `internal/config` resolves intent; it does not execute anything
- `internal/engine` executes one run; it does not know frontmatter parsing
- `internal/engine/adapter` normalizes engine-specific behavior; it does not own lifecycle
- `internal/dispatch` owns traceability, persistent records, artifact resolution, recovery prompt construction, and live status; there is no separate `internal/recovery` package
- `internal/steer` owns inbox and FIFO steering together; there are no separate `internal/inbox` or `internal/fifo` packages

This separation keeps new engine support, new config surfaces, and loop changes from collapsing into one package.

## Cross-References

- [Dispatch](./dispatch.md) for the `DispatchSpec` and `DispatchResult` contract
- [Engines](./engines.md) for adapter behavior and per-harness differences
- [Config](./config.md) for frontmatter schema, resolution order, and profile discovery
- [Recovery](./recovery.md) for control records, dispatch recovery, and continuation prompts
- [Lifecycle](./lifecycle.md) for statuses, events, timeout escalation, and supervision states
- [CLI Reference](./cli-reference.md) for commands, flags, and stdin mode
