# agent-mux Technical Documentation

## 1. Design Philosophy

### The Three Problems

agent-mux exists because the AI coding harness landscape (Claude Code, Codex CLI, Gemini CLI) is structurally isomorphic but operationally diverse. Three problems emerge when a coordinating LLM needs to dispatch work across them:

1. **Dispatch heterogeneity.** Each harness has different CLI flags, sandbox models, event formats, and resume protocols. The coordinator should not encode per-harness knowledge.
2. **Supervision opacity.** Spawning a binary and waiting for exit gives no visibility into liveness, progress, or partial results. A 30-minute worker stuck on a hanging tool call is indistinguishable from one doing useful work.
3. **Artifact fragility.** Process death (timeout, OOM, SIGKILL) destroys in-flight work unless the system treats every write as potentially the last.

### The Six Design Principles

All six are confirmed implemented in code. None are aspirational.

| # | Principle | Implication |
|---|-----------|-------------|
| 1 | **Tool, not orchestrator.** LLM decides; agent-mux dispatches. | No decision logic in Go. Pipelines are data-driven TOML. |
| 2 | **Job done is holy.** Artifacts persist across timeout and process death. | Artifact dir created before harness starts. `ScanArtifacts()` on every terminal state. |
| 3 | **Errors are steering signals.** Every error has code, message, suggestion, retryable. | 15+ error codes in catalog. Each written as a self-correction instruction. |
| 4 | **Single-shot with curated context is the default.** | `--pipeline` required to escalate beyond single dispatch. |
| 5 | **Simplest viable dispatch.** CLI call > file > config > code. | No runtime-generated dispatch logic. |
| 6 | **Config over code.** Roles, pipelines, timeouts all TOML. | Binary is generic. All behavior from config files. |

### Key Architecture Decisions

**Why Go.** Goroutines power the LoopEngine: stdout scanner, watchdog ticker (5s), inbox ticker (250ms), soft/hard timeout timers -- all concurrent via `select`. `context.Context` propagates SIGINT/SIGTERM through dispatch to subprocess. Process groups (`Setpgid: true` + `Kill(-pgid, sig)`) ensure grandchildren die with the harness. Static binary, no runtime dependencies.

**Why single binary with adapters.** The three harnesses are structurally identical: spawn binary, read event stream, parse events, supervise. One `LoopEngine` + one `HarnessAdapter` interface covers all three. Adding a new harness is ~120 lines implementing 6 methods.

**Why artifact-first.** Origin: 15+ real failures where harness hard-kills destroyed completed work that existed on disk but was invisible to the coordinator. Fix: treat every write as potentially the last. Prompt preamble instructs incremental writes; artifact dir exists before the harness starts; `ScanArtifacts` runs on every terminal state.

**Why no runtime budget enforcement.** Token/cost fields exist for reporting only. Wall-time timeout is the only hard limit that doesn't require real-time API introspection. Token counts arrive at coarse event boundaries; killing mid-tool-call based on count destroys more work than it saves.

---

## 2. Architecture Overview

### System Diagram

```
                          ┌─────────────────────────────────────┐
  caller (LLM/CLI)        │           agent-mux (Go)            │
  ───────────────────►    │                                     │
  DispatchSpec (JSON)     │  ┌─────────┐   ┌──────────────┐    │
  or CLI flags            │  │  config  │   │   dispatch    │    │
                          │  │ resolver │──►│  router       │    │
                          │  └─────────┘   └──────┬───────┘    │
                          │                       │             │
                          │  ┌────────────────────▼──────────┐ │
                          │  │         LoopEngine             │ │
                          │  │  ┌─────────┐  ┌────────────┐  │ │
                          │  │  │supervisor│  │  event      │  │ │
                          │  │  │ (proc    │  │  emitter    │  │ │
                          │  │  │  groups) │  │  (NDJSON)   │  │ │
                          │  │  └────┬────┘  └────────────┘  │ │
                          │  │       │                        │ │
                          │  │  ┌────▼────────────────────┐   │ │
                          │  │  │   HarnessAdapter        │   │ │
                          │  │  │  ┌──────┬──────┬──────┐ │   │ │
                          │  │  │  │Codex │Claude│Gemini│ │   │ │
                          │  │  │  └──┬───┴──┬───┴──┬───┘ │   │ │
                          │  │  └─────┼──────┼──────┼─────┘   │ │
                          │  └────────┼──────┼──────┼─────────┘ │
                          └───────────┼──────┼──────┼───────────┘
                                      ▼      ▼      ▼
                                   codex  claude  gemini
                                   (bin)  (bin)   (bin)
```

### Package Map

| Package | Owns |
|---------|------|
| `cmd/agent-mux/main.go` | CLI flag parsing, mode detection, spec construction, dispatch orchestration |
| `internal/config` | TOML loading, merge semantics, role/variant resolution, profile/skill loading |
| `internal/types` | All shared types: `DispatchSpec`, `DispatchResult`, `HarnessEvent`, `HarnessAdapter` |
| `internal/engine` | `LoopEngine`: process lifecycle, event loop, timeout/watchdog/inbox coordination |
| `internal/engine/adapter` | `Registry` + `CodexAdapter`, `ClaudeAdapter`, `GeminiAdapter` |
| `internal/supervisor` | `Process`: exec.Cmd wrapper with process-group signals and graceful shutdown |
| `internal/pipeline` | `ExecutePipeline`: step chaining, fan-out, handoff rendering |
| `internal/event` | `Emitter`: NDJSON event formatting, heartbeat ticker, dual-sink (stderr + file) |
| `internal/dispatch` | Traceability (salt/trace token), artifact dir management, dispatch metadata |
| `internal/recovery` | Control records, artifact dir resolution, recovery prompt construction |
| `internal/inbox` | Append-under-lock inbox file, atomic read-and-clear |
| `internal/hooks` | Deny/warn pattern evaluation, safety preamble injection |

### Data Flow

```
DispatchSpec (JSON or CLI flags)
       │
       ▼
  Config resolution: global TOML → project TOML → --config overlay
       │
       ▼
  Profile resolution: .md frontmatter + companion .toml
       │
       ▼
  Role resolution: role lookup → variant overlay
       │
       ▼
  Defaults application: engine, model, effort from [defaults]
       │
       ▼
  Timeout resolution: effort → timeout bucket
       │
       ▼
  Skill injection: SKILL.md → XML blocks prepended to prompt
       │
       ▼
  Hook check: deny → abort | inject safety preamble
       │
       ▼
  Adapter selection: registry.Get(engine) → HarnessAdapter
       │
       ▼
  Process spawn: adapter.BuildArgs() → supervisor.NewProcess → Start
       │
       ▼
  Event loop: scanHarnessOutput → adapter.ParseEvent → event stream
       │         ├── watchdog: 90s warn, 180s kill
       │         ├── timeout: soft → grace → hard
       │         └── inbox: poll at event boundary + 250ms ticker
       │
       ▼
  Result assembly: DispatchResult (status, response, artifacts, tokens)
```

---

## 3. Configuration

### Config File Discovery and Resolution Order

Precedence (lowest to highest):

```
hardcoded defaults (DefaultConfig())
      ↓
~/.agent-mux/config.toml              (global)
      ↓
<cwd>/.agent-mux/config.toml          (project)
      ↓
--config <path>                        (explicit overlay, highest precedence)
```

Global path fallback: if `~/.agent-mux/config.toml` is absent, falls back to deprecated `~/.config/agent-mux/config.toml` with a stderr warning. Only one global file per invocation.

`--config` resolution: if a `.toml` file, loaded directly. If a directory, tries `<dir>/.agent-mux/config.toml` then `<dir>/config.toml`. Error if neither exists.

### TOML Schema Reference

#### `[defaults]` -- DefaultsConfig

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| `engine` | string | `""` | Default engine: `"codex"`, `"claude"`, `"gemini"` |
| `model` | string | `""` | Default model identifier |
| `effort` | string | `"high"` | Effort tier; maps to timeout bucket |
| `sandbox` | string | `"danger-full-access"` | Sandbox mode for harness |
| `permission_mode` | string | `""` | Permission mode for harness |
| `response_max_chars` | int | `2000` | Max chars before response truncation |
| `max_depth` | int | `2` | Max recursive sub-dispatch depth |
| `allow_subdispatch` | bool | `true` | Whether workers may sub-dispatch |

#### `[models]` -- map[string][]string

Engine name to ordered model list. Used for validation with Levenshtein fuzzy-match on miss.

```toml
[models]
codex  = ["gpt-5.4", "gpt-5.4-mini"]
claude = ["claude-sonnet-4-6", "claude-opus-4-6"]
```

Overlay replaces the entire list per engine key (not appended).

#### `[timeout]` -- TimeoutConfig

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| `low` | int | `120` | Timeout for effort="low" (2 min) |
| `medium` | int | `600` | Timeout for effort="medium" (10 min) |
| `high` | int | `1800` | Timeout for effort="high" (30 min) -- also fallback |
| `xhigh` | int | `2700` | Timeout for effort="xhigh" (45 min) |
| `grace` | int | `60` | Grace period after soft timeout before SIGKILL |

#### `[liveness]` -- LivenessConfig

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| `heartbeat_interval_sec` | int | `15` | Heartbeat emit interval |
| `silence_warn_seconds` | int | `90` | Silence before frozen_warning event |
| `silence_kill_seconds` | int | `180` | Silence before process kill |

#### `[hooks]` -- HooksConfig

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| `deny` | []string | nil | Patterns that trigger hard block |
| `warn` | []string | nil | Patterns that trigger warning |
| `event_deny_action` | string | `""` | Action on deny match in events: `"kill"` (default) or `"warn"` |

#### `[pipelines.<name>]` -- PipelineConfig

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| `max_parallel` | int | `8` | Max concurrent workers across all steps |
| `steps` | []PipelineStep | required | Ordered step definitions |

See Section 5 for PipelineStep fields.

### Role System

```go
type RoleConfig struct {
    Engine           string                 `toml:"engine"`
    Model            string                 `toml:"model"`
    Effort           string                 `toml:"effort"`
    Timeout          int                    `toml:"timeout"`
    Skills           []string               `toml:"skills"`
    SystemPromptFile string                 `toml:"system_prompt_file"`
    Variants         map[string]RoleVariant `toml:"variants"`
    SourceDir        string                 `toml:"-"`  // dir of defining config file
}

type RoleVariant struct {
    Engine           string   `toml:"engine"`
    Model            string   `toml:"model"`
    Effort           string   `toml:"effort"`
    Timeout          int      `toml:"timeout"`
    Skills           []string `toml:"skills"`
    SystemPromptFile string   `toml:"system_prompt_file"`
}
```

`RoleVariant` is a strict subset of `RoleConfig` (no nested Variants, no SourceDir). Resolution: `ResolveRole(cfg, roleName)` does a direct map lookup. No inheritance between roles. Variants inherit from their parent role at dispatch time -- the caller applies the variant's non-zero fields on top of the base role.

### Merge Semantics: The "Defined-Wins" Pattern

```go
func merge[T comparable](dst *T, value T, defined bool) {
    var zero T
    if defined || value != zero {
        *dst = value
    }
}
```

`defined` is determined by `toml.MetaData.IsDefined()`. An explicitly-set zero value wins. An absent key preserves the base value.

| Section | Conflict Resolution |
|---------|-------------------|
| `[defaults]` scalars | Last file with explicit definition wins |
| `[models].<engine>` | Overlay list fully replaces base list |
| `[roles.<name>]` scalars | Last file with explicit definition wins |
| `[roles.<name>].skills` | Overlay list fully replaces base list (no append) |
| `[roles.<name>.variants.<v>]` | Additive map; name collisions deep-merged |
| `[pipelines.<name>]` | Overlay fully replaces entire pipeline |
| `[liveness]` / `[timeout]` scalars | Last file with explicit definition wins |
| `[hooks].deny` / `.warn` | Overlay list fully replaces base list |

### Profile Loading

`LoadProfile(name, cwd)` searches for `<name>.md`:

1. `<cwd>/.claude/agents/<name>.md`
2. `<cwd>/agents/<name>.md`
3. `<cwd>/.agent-mux/agents/<name>.md`
4. `~/.agent-mux/agents/<name>.md`

First match wins. YAML frontmatter sets `model`, `effort`, `engine`, `skills`, `timeout`. Body becomes `SystemPrompt`. A sibling `<name>.toml` is loaded as companion config. `ExtraFields map[string]any` holds arbitrary frontmatter keys passed through verbatim.

---

## 4. Dispatch & Engines

### Dispatch Flow

1. **Config load** -- `LoadConfig(flags.config, spec.Cwd)`
2. **Profile resolution** -- frontmatter fields applied; profile skills prepended to `spec.Skills`
3. **Role resolution** -- `ResolveRole` + variant overlay; role `system_prompt_file` prepended; skills merged (CLI-first, then role)
4. **Defaults application** -- fill engine/model/effort from `[defaults]`; effort defaults to `"high"`
5. **Timeout resolution** -- `TimeoutForEffort(cfg, spec.Effort)` maps effort to seconds
6. **EngineOpts injection** -- liveness config + permission-mode written to `spec.EngineOpts`
7. **Skill injection** -- `LoadSkills` reads `SKILL.md` files, wraps in XML, prepends to prompt; script dirs added to `add-dir`
8. **Context file preamble** -- reference to `$AGENT_MUX_CONTEXT` prepended if set
9. **Hook check** -- deny match aborts; safety preamble injected if rules exist
10. **Traceability** -- generate salt (adjective-noun-digit) and trace token

Then: adapter lookup via `Registry.Get(engine)` -> model validation -> control record written -> `LoopEngine.Dispatch(ctx, spec)`.

### Engine Adapter Contract

```go
type HarnessAdapter interface {
    Binary() string                                                    // executable name on PATH
    BuildArgs(spec *DispatchSpec) []string                             // argv[1:] for initial invocation
    EnvVars(spec *DispatchSpec) []string                               // additional KEY=VALUE env vars
    ParseEvent(line string) (*HarnessEvent, error)                     // parse one stdout line
    SupportsResume() bool                                              // can resume via ResumeArgs?
    ResumeArgs(spec *DispatchSpec, sessionID string, msg string) []string // argv for resume invocation
}
```

Adapter lifecycle: Codex is stateless. Claude carries a `sync.Mutex`-protected `toolInputs` map for tool_use/tool_result correlation. Gemini carries `pendingFiles` for write_file correlation.

### Codex Adapter

**Binary:** `codex`

**Command construction:**
```
codex exec --json [-m <model>] <sandbox-flag> [-C <cwd>]
      [-c model_reasoning_effort=<r>] [--add-dir <dir>...] <prompt>
```

Sandbox resolution:

| Condition | Flag emitted |
|-----------|-------------|
| `permission-mode` set | `-s <permission-mode>` |
| `sandbox="danger-full-access"` + `FullAccess=true` | `--dangerously-bypass-approvals-and-sandbox` |
| `sandbox="danger-full-access"` + `FullAccess=false` | `-s danger-full-access` |
| other sandbox value | `-s <sandbox>` |

System prompt handling: prepended to prompt as `SystemPrompt + "\n\n" + Prompt`.

**Resume:** `["exec", "resume", "-m", <model>, "--json", <sessionID>, <message>]`. Supported.

### Claude Adapter

**Binary:** `claude`

**Command construction:**
```
claude -p --output-format stream-json --verbose [--model <model>]
       [--max-turns <n>] [--permission-mode <mode>]
       [--system-prompt <text>] <prompt>
```

System prompt handling: passed via dedicated `--system-prompt` flag (not merged into prompt).

**Resume:** `["--resume", <sessionID>, "--continue", <message>]`. Supported.

### Gemini Adapter

**Binary:** `gemini`

**Command construction:**
```
gemini -p <prompt> -o stream-json [-m <model>] --approval-mode <mode>
```

`approval-mode` defaults to `"yolo"`, overridable via `EngineOpts["permission-mode"]`.

System prompt handling: written to `<artifactDir>/system_prompt.md`, exported as `GEMINI_SYSTEM_MD=<path>`. Silently dropped if `ArtifactDir` not set.

**Resume:** `["--resume", <sessionID>, "-p", <message>]`. Supported.

**Known limitations:** No direct system prompt flag. Non-JSON stdout lines silently discarded. No tool calling.

### Prompt Composition Order

System prompt layers (outermost first):
1. Role `system_prompt_file`
2. Profile body (`.md` non-frontmatter text)
3. `--system-prompt-file` content
4. `--system-prompt` string

User prompt layers (outermost first):
1. Hook injection (safety preamble)
2. Context file preamble (`$AGENT_MUX_CONTEXT` reference)
3. Skill blocks (XML-wrapped `SKILL.md` files)
4. Recovery prompt (wraps existing prompt if `--recover`)
5. Original user prompt

At dispatch time, `WithPromptPreamble` further prepends trace token, dispatch ID, and artifact dir hint.

---

## 5. Orchestration Layer

### Pipeline Execution

`ExecutePipeline` is the single entry point. Steps run sequentially. Within a step, workers may run concurrently (fan-out). A ULID pipeline ID is stamped on every worker spec.

**PipelineStep fields:**

| Field | Type | Effect |
|-------|------|--------|
| `name` | string | Step label in handoff headers |
| `role` / `variant` | string | Role resolution for this step |
| `engine` / `model` / `effort` | string | Direct overrides (bypass role) |
| `timeout` | int | Overrides `spec.TimeoutSec` when > 0 |
| `parallel` | int | Worker count; 0/1 = sequential |
| `worker_prompts` | []string | Per-worker focus directives; len must equal `parallel` |
| `receives` | string | Name of prior step's output to consume |
| `pass_output_as` | string | Name for this step's output |
| `handoff_mode` | string | `"summary_and_refs"` (default), `"full_concat"`, `"refs_only"` |

**Fan-out** uses a semaphore channel of size `MaxParallel` (default 8) with `sync.WaitGroup` -- not `errgroup`. Rationale: `errgroup.WithContext` cancels on first error (fail-fast). Fan-out needs all-workers-complete-then-collect-partial-results. Results land in a pre-allocated slice indexed by worker position.

**Partial success rules:**
- All failed, none succeeded -> status `"failed"`, pipeline stops
- Some succeeded, some failed -> status `"partial"`, pipeline continues
- All succeeded -> status `"completed"`

`WorkerCompleted` and `WorkerTimedOut` both count as succeeded.

**Handoff modes:**

| Mode | Content |
|------|---------|
| `summary_and_refs` | Summary + path to `output.md` + artifact dir. Default. |
| `full_concat` | Full content of `output.md`. Falls back to refs if missing. |
| `refs_only` | Only `output.md` path and artifact dir. |

**Validation** (`ValidatePipeline`): rejects zero steps, forward references in `receives`, duplicate `pass_output_as` names, mismatched `worker_prompts`/`parallel` lengths.

### Supervisor

`supervisor.Process` wraps `*exec.Cmd` with process-group awareness:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
```

The child gets its own process group. `signalGroup` sends signals to `-pgid`, killing the entire tree atomically. `ESRCH` and "already exited" errors are silently swallowed.

**Graceful shutdown sequence** (`GracefulStop(graceSec)`):
1. `SIGTERM` to process group
2. Wait up to `graceSec` seconds
3. `SIGKILL` to process group if still alive
4. Block on wait to reap zombie

Two goroutines per run: scanner (stdout line reader) and waiter (`proc.Wait()` result).

### Event Streaming

Events emitted as NDJSON to stderr AND `<artifact_dir>/events.jsonl`. Every event carries:

```json
{
  "schema_version": 1,
  "type": "<event_type>",
  "dispatch_id": "<ulid>",
  "salt": "<adj-noun-digit>",
  "trace_token": "AGENT_MUX_GO_<dispatch_id>",
  "ts": "2026-03-28T10:00:00Z"
}
```

**All 13 event types:**

| Type | Key Fields |
|------|-----------|
| `dispatch_start` | `engine`, `model`, `effort`, `timeout_sec`, `grace_sec`, `cwd`, `skills` |
| `dispatch_end` | `status`, `duration_ms` |
| `heartbeat` | `elapsed_s`, `interval_s`, `last_activity` |
| `tool_start` | `tool`, `args` |
| `tool_end` | `tool`, `duration_ms` |
| `file_write` | `path` |
| `file_read` | `path` |
| `command_run` | `command` |
| `progress` | `message` |
| `timeout_warning` | `message` |
| `frozen_warning` | `silence_seconds`, `message` |
| `error` | `error_code`, `message` |
| `coordinator_inject` | `message` |

Additional types used internally: `warning` (non-fatal conditions).

**Heartbeat protocol:** Background goroutine emits heartbeat every 15s (configurable). Carries `elapsed_s`, `interval_s`, `last_activity` (e.g., `"tool: Bash"`, `"wrote: src/foo.go"`). Stopped via deferred cancel before result return.

### Timeout System

**Effort to timeout mapping** (`TimeoutForEffort`):

| Effort | Default Timeout |
|--------|----------------|
| `low` | 120s (2 min) |
| `medium` | 600s (10 min) |
| `high` | 1800s (30 min) -- fallback for unknown strings |
| `xhigh` | 2700s (45 min) |

Priority chain (highest wins): step-level `timeout` -> role-level `timeout` -> `TimeoutForEffort(effort)`.

**Two-phase timeout:**

Phase 1 -- Soft timeout fires:
1. Emit `timeout_warning` event
2. Write wrap-up message to inbox: "Soft timeout reached. Wrap up your current work..."
3. Start grace timer (default 60s from `[timeout].grace`)

Phase 2 -- Hard timeout fires:
1. Set terminal state `"timed_out"`
2. `GracefulStop(5)` -- SIGTERM, then SIGKILL after 5s
3. Drain remaining events from scanner

If the process exits cleanly during grace (harness wrapped up on its own), it routes to the normal success path -- a completed result, not `StatusTimedOut`.

**Partial result preservation:** `lastResponse` (or `lastProgressText` if empty) is preserved as `Response`. `ScanArtifacts` walks the artifact dir. Result carries `Partial: true`, `Recoverable: true`.

### Liveness Watchdog

Watchdog ticker fires every 5 seconds. `lastActivity` is reset on every parsed harness event.

| Threshold | Default | Action |
|-----------|---------|--------|
| `silence_warn_seconds` | 90s | Emit `frozen_warning` event. Set `frozenWarned` to prevent repeats. |
| `silence_kill_seconds` | 180s | Emit `error` with code `frozen_tool_call`. `GracefulStop(5)`. Result: `"failed"`. |

Both thresholds are also readable from `spec.EngineOpts` for per-dispatch tuning without config changes.

`frozen_tool_call` error: `Suggestion: "Worker may be stuck in a hanging command or tool call. Retry with a narrower task or longer timeout. Partial work was preserved."`, `Retryable: true`.

---

## 6. Control Plane

### Recovery System

**Artifact directory layout:**

```
/tmp/agent-mux/<dispatch_id>/
  _dispatch_meta.json    # dispatch metadata (engine, model, status, timestamps)
  events.jsonl           # one JSON object per harness event
  full_output.md         # full streamed output text
  inbox.md               # pending signal messages
  <worker artifacts>     # files written by the harness
```

Default path: `/tmp/agent-mux/<dispatch_id>/` (from `recovery.DefaultArtifactDir`). Overridable via `--artifact-dir`.

**Control records** at `/tmp/agent-mux/control/<url-escaped-dispatch-id>.json`:

```json
{
  "dispatch_id": "01JQXYZ...",
  "artifact_dir": "/absolute/path/to/artifact/dir",
  "dispatch_salt": "coral-fox-nine",
  "trace_token": "AGENT_MUX_GO_01JQXYZ..."
}
```

Written atomically (tmp + rename). `artifact_dir` resolved to absolute path at registration. This indirection allows `--signal` and `--recover` to find the right artifact dir even when `--artifact-dir` was customized.

**Recovery flow** (`--recover <dispatch_id>`):
1. `ResolveArtifactDir` -- read control record; fall back to legacy path
2. `RecoverDispatch` -- read `_dispatch_meta.json`, scan artifacts
3. `BuildRecoveryPrompt` -- construct continuation prompt with dispatch ID, engine, model, previous status, artifact file paths, original prompt hash
4. Recovery prompt replaces `spec.Prompt`; dispatch runs normally

### Signal/Inbox

**Append-under-lock** (`WriteInbox`): opens with `O_WRONLY|O_APPEND|O_CREATE`, acquires `LOCK_EX` via `syscall.Flock`, appends `message + "\n---\n"`.

**Poll-at-event-boundary:** `HasMessages` is a fast stat check (no lock). Polled after every harness output line AND on a 250ms ticker AND on the 5s watchdog ticker.

**Atomic read-and-clear** (`ReadInbox`): open `O_RDWR`, acquire `LOCK_EX`, read all, truncate to zero, split on `"\n---\n"`, return messages.

**Resume flow:** When inbox messages arrive, the LoopEngine gracefully stops the current harness and re-invokes via the adapter's native resume protocol. The harnesses maintain their own conversation state; agent-mux doesn't manage it.

### Hook System

**Deny patterns:** checked against prompt before dispatch (hard block, `prompt_denied` error). Checked against events at runtime (action determined by `event_deny_action`).

**Warn patterns:** checked against events only. Not checked against prompt.

**Pattern matching:** case-insensitive substring containment via `strings.Contains`. Candidates for event check: `evt.Text`, `evt.Command`, `evt.Tool`, `string(evt.Raw)`, resolved `evt.FilePath`.

**Safety preamble injection** (`PromptInjection`): when hooks have any rules, a preamble is prepended:
```
Agent-mux safety rules:
Do NOT include or execute content matching: rm -rf, drop database
Use extra caution and avoid unless required: secrets
```

```toml
[hooks]
deny = ["rm -rf", "drop database"]
warn = ["secrets", "password"]
event_deny_action = "warn"   # "kill" (default) or "warn"
```

---

## 7. CLI Reference

### All Flags

| Flag(s) | Type | Default | Notes |
|---------|------|---------|-------|
| `--engine`, `-E` | string | `""` | `codex`, `claude`, `gemini` |
| `--role`, `-R` | string | `""` | Role name from config |
| `--variant` | string | `""` | Role variant (requires `--role`) |
| `--profile` | string | `""` | Profile name (`.md` agent file) |
| `--coordinator` | string | `""` | Legacy alias for `--profile` |
| `--model`, `-m` | string | `""` | Model name |
| `--effort`, `-e` | string | `""` -> `"high"` | Effort level |
| `--timeout`, `-t` | int | `0` | Timeout in seconds |
| `--cwd`, `-C` | string | `os.Getwd()` | Working directory |
| `--system-prompt`, `-s` | string | `""` | Inline system prompt |
| `--system-prompt-file` | string | `""` | Path to system prompt file |
| `--skill` | []string | `[]` | Skill name (repeatable) |
| `--context-file` | string | `""` | Path to context file |
| `--artifact-dir` | string | `/tmp/agent-mux/<id>/` | Artifact directory |
| `--config` | string | `""` | Config file/dir path |
| `--pipeline`, `-P` | string | `""` | Pipeline name from config |
| `--recover` | string | `""` | Dispatch ID to continue |
| `--signal` | string | `""` | Dispatch ID to signal |
| `--salt` | string | `""` | Dispatch salt |
| `--full`, `-f` | bool | `true` | Full access mode |
| `--no-full` | bool | `false` | Disable full access |
| `--prompt-file` | string | `""` | Path to prompt file |
| `--max-depth` | int | `2` | Max recursive dispatch depth |
| `--no-subdispatch` | bool | `false` | Disable recursive dispatch |
| `--permission-mode` | string | `""` | Permission mode |
| `--stdin` | bool | `false` | Read DispatchSpec JSON from stdin |
| `--yes` | bool | `false` | Skip TTY confirmation |
| `--response-max-chars` | int | `0` | Max response characters |
| `--sandbox` | string | `"danger-full-access"` | Sandbox mode |
| `--reasoning`, `-r` | string | `"medium"` | Reasoning effort (Codex) |
| `--max-turns` | int | `0` | Max agent turns (Claude) |
| `--add-dir` | []string | `[]` | Additional writable dir (repeatable, Codex) |
| `--output`, `-o` | string | `"json"` | Output format: `json` or `text` |
| `--verbose`, `-v` | bool | `false` | Verbose mode |
| `--version`, `-V` | bool | `false` | Print version |

### Mode Detection

| Invocation | Mode |
|-----------|------|
| `agent-mux [flags] <prompt>` | dispatch (default) |
| `agent-mux dispatch [flags] <prompt>` | dispatch (explicit) |
| `agent-mux preview [flags] <prompt>` | preview (print resolved spec, no execution) |
| `agent-mux --pipeline <name> [flags] <prompt>` | pipeline |
| `agent-mux --recover <id> [flags] <prompt>` | recover + dispatch |
| `agent-mux --signal <id> <message>` | signal |
| `agent-mux --stdin [flags]` | stdin dispatch |
| `agent-mux --version` | version |

### --stdin JSON

Reads a `DispatchSpec` JSON object from stdin. Required: `prompt` non-empty.

Defaults when field is absent from JSON (absent, not zero):

| Field | Default |
|-------|---------|
| `dispatch_id` | Generated ULID |
| `cwd` | `os.Getwd()` |
| `artifact_dir` | `/tmp/agent-mux/<dispatch_id>/` |
| `allow_subdispatch` | `true` |
| `full_access` | `true` |
| `pipeline_step` | `-1` |
| `grace_sec` | `60` |
| `handoff_mode` | `"summary_and_refs"` |

CLI dispatch flags are ignored when `--stdin` is active (warning printed to stderr).

### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Error (config, dispatch failed, signal failed, recovery failed) |
| `2` | Usage error (bad flags, missing prompt) |
| `130` | Cancelled at TTY confirmation prompt |

---

## 8. Contracts

### Input: DispatchSpec

```go
type DispatchSpec struct {
    DispatchID          string         `json:"dispatch_id"`
    Salt                string         `json:"salt,omitempty"`
    TraceToken          string         `json:"trace_token,omitempty"`
    Engine              string         `json:"engine"`
    Model               string         `json:"model,omitempty"`
    Effort              string         `json:"effort"`
    Prompt              string         `json:"prompt"`
    SystemPrompt        string         `json:"system_prompt,omitempty"`
    Cwd                 string         `json:"cwd"`
    Skills              []string       `json:"skills,omitempty"`
    Profile             string         `json:"-"`              // wire key: "profile"; alias: "coordinator"
    Pipeline            string         `json:"pipeline,omitempty"`
    ContextFile         string         `json:"context_file,omitempty"`
    ArtifactDir         string         `json:"artifact_dir"`
    TimeoutSec          int            `json:"timeout_sec,omitempty"`
    GraceSec            int            `json:"grace_sec,omitempty"`
    Role                string         `json:"role,omitempty"`
    Variant             string         `json:"variant,omitempty"`
    MaxDepth            int            `json:"max_depth"`
    AllowSubdispatch    bool           `json:"allow_subdispatch"`
    Depth               int            `json:"depth"`
    ParentDispatchID    string         `json:"parent_dispatch_id,omitempty"`
    PipelineID          string         `json:"pipeline_id,omitempty"`
    PipelineStep        int            `json:"pipeline_step"`
    ContinuesDispatchID string         `json:"continues_dispatch_id,omitempty"`
    Receives            string         `json:"receives,omitempty"`
    PassOutputAs        string         `json:"pass_output_as,omitempty"`
    Parallel            int            `json:"parallel,omitempty"`
    HandoffMode         string         `json:"handoff_mode,omitempty"`
    ResponseMaxChars    int            `json:"response_max_chars,omitempty"`
    EngineOpts          map[string]any `json:"engine_opts,omitempty"`
    FullAccess          bool           `json:"full_access"`
}
```

`Profile` uses `json:"-"` but has custom marshal/unmarshal. Wire key is `"profile"`; `"coordinator"` accepted as alias. Error if both present with different values.

### Output: DispatchResult

```go
type DispatchResult struct {
    SchemaVersion     int               `json:"schema_version"`
    Status            DispatchStatus    `json:"status"`             // "completed" | "timed_out" | "failed"
    DispatchID        string            `json:"dispatch_id"`
    DispatchSalt      string            `json:"dispatch_salt"`
    TraceToken        string            `json:"trace_token"`
    Response          string            `json:"response"`
    ResponseTruncated bool              `json:"response_truncated"`
    FullOutput        *string           `json:"full_output"`
    HandoffSummary    string            `json:"handoff_summary"`
    Artifacts         []string          `json:"artifacts"`
    Partial           bool              `json:"partial,omitempty"`
    Recoverable       bool              `json:"recoverable,omitempty"`
    Reason            string            `json:"reason,omitempty"`
    Error             *DispatchError    `json:"error,omitempty"`
    Activity          *DispatchActivity `json:"activity"`
    Metadata          *DispatchMetadata `json:"metadata"`
    DurationMS        int64             `json:"duration_ms"`
}
```

**DispatchError:** `Code string`, `Message string`, `Suggestion string`, `Retryable bool`, `PartialArtifacts []string` (omitempty).

**DispatchActivity:** `FilesChanged`, `FilesRead`, `CommandsRun`, `ToolCalls` -- all `[]string`.

**DispatchMetadata:** `Engine`, `Model`, `Role` (omitempty), `Tokens *TokenUsage`, `Turns int`, `CostUSD float64`, `SessionID` (omitempty), `PipelineID` (omitempty), `ParentDispatchID` (omitempty).

**TokenUsage:** `Input int`, `Output int`, `Reasoning int` (omitempty), `CacheRead int` (omitempty), `CacheWrite int` (omitempty).
```

### Output: PipelineResult

**PipelineResult:** `PipelineID string` (ULID), `Status string` (`"completed"` | `"partial"` | `"failed"`), `Steps []StepOutput`, `FinalStep *StepOutput`, `DurationMS int64`.

**StepOutput:** `StepName`, `StepIndex int`, `PipelineID`, `HandoffMode`, `Workers []WorkerResult`, `HandoffText string` (rendered handoff for downstream), `Succeeded int`, `Failed int`, `TotalMS int64`.

**WorkerResult:** `WorkerIndex int`, `DispatchID`, `Status WorkerStatus` (`"completed"` | `"timed_out"` | `"failed"`), `Summary string` (truncated, max 2000 chars), `ArtifactDir`, `OutputFile` (path to `output.md`; omitted if failed), `ErrorCode`, `ErrorMsg`, `DurationMS int64`.

### Output: SignalAck

```json
{
  "status": "ok",
  "dispatch_id": "<dispatch_id>",
  "artifact_dir": "<resolved_artifact_dir>",
  "message": "Signal delivered to inbox"
}
```

No confirmation that the worker has consumed the message -- only that it was appended.

### Events

See Section 5 for the full table of 13 event types with fields. All events carry `schema_version`, `type`, `dispatch_id`, `salt`, `trace_token`, `ts`.

### HarnessEvent (internal)

```go
type HarnessEvent struct {
    Kind          EventKind
    SecondaryKind EventKind   // for dual-kind events
    Timestamp     time.Time
    Tool          string
    FilePath      string
    Command       string
    Text          string
    SessionID     string
    DurationMS    int64
    Tokens        *TokenUsage
    Turns         int
    ErrorCode     string
    Raw           []byte      // original harness output line
}
```

EventKind constants: `EventUnknown(0)`, `EventToolStart`, `EventToolEnd`, `EventFileWrite`, `EventFileRead`, `EventCommandRun`, `EventProgress`, `EventResponse`, `EventError`, `EventSessionStart`, `EventTurnComplete`, `EventTurnFailed`, `EventRawPassthrough`.

### Error Codes

All 15 spec-defined error codes are implemented. Key codes:

| Code | Meaning | Retryable |
|------|---------|-----------|
| `engine_not_found` | Unknown engine name | No |
| `model_not_found` | Unknown model (with fuzzy-match suggestions) | No |
| `prompt_denied` | Hook deny pattern matched prompt | No |
| `frozen_tool_call` | Silence kill threshold exceeded | Yes |
| `timed_out` | Hard timeout after grace period | Yes |
| `interrupted` | `ctx.Done()` fired (SIGINT/SIGTERM) | No |
| `cancelled` | TTY confirmation declined | No |
| `resume_unsupported` | Adapter does not support resume | No |

---

## 9. Known Limitations & Future

### Gemini Limitations

- **Response capture incomplete:** Non-JSON stdout lines silently discarded (not passed through as `EventRawPassthrough`).
- **No tool calling** (confirmed by spec and code).
- **System prompt silently dropped** if `ArtifactDir` is not set (file-based injection requires a write target).
- **Prompt passed as flag value** (`-p <prompt>`), not piped via stdin.

### Spec Promises Not Yet Implemented

1. **`repeat_escalation` liveness config** (SPEC-V2 SS7.2): If the same harness freezes twice, escalate differently. `LivenessConfig` has no such field.
2. **ax-eval instrumentation** (SPEC-V2 SS15.6): `first_attempt` and `error_correction` events for behavioral convergence testing. No ax-eval machinery in codebase.
3. **ax-eval error-as-teaching CI tests** (SPEC-V2 SS15.5): LLM-in-the-loop behavioral tests per error code. Not implemented.
4. **Bundled agent auto-install** (SPEC-V2 SS13): `agents/` directory exists in repo with 6 templates but no `setup` command. Users must manually place them.

### Code Features Not in Spec

1. **Role variants** (`--variant` flag): `[roles.X.variants.Y]` in TOML. Implementation-time addition.
2. **`Profile` field** (alias for `coordinator`): code-level abstraction not in spec.
3. **`EnvVars()` on HarnessAdapter**: spec interface (SS6.1) does not include this method. Clean extension for per-adapter env injection.
4. **`SecondaryKind` on HarnessEvent**: dual-kind event handling not in spec.
5. **Inbox checked on every stdout line**: spec says "at event boundaries"; code is more aggressive (every line + 250ms ticker + 5s watchdog).
6. **Per-dispatch liveness tuning via `EngineOpts`**: `intEngineOpt()` reads thresholds from spec, not only from config.
