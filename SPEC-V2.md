# Agent-Mux v2 — Go Rewrite Spec

**Date:** 2026-03-25
**Status:** Draft
**Authors:** Nikita + R. Jenkins

---

## 1. Problem Statement

Agent-mux v1 (TypeScript/Bun) works but has structural problems that config changes can't fix. Evidence from 146+ session handoffs and ecosystem research:

**What breaks in practice (our own ground truth, ranked by frequency):**

1. **Timeout kills completed work** (15+ sessions). Claude Code `Task()` enforces 5-min hard kill overriding agent-mux's 10-20 min config. Workers write files, build features, then get killed. Work exists on disk but is invisible to the caller.

2. **Stale worker notifications flood coordinator context** (20+ sessions). Failed/timed-out workers push late notifications. Coordinator must repeatedly dismiss "stale worker, already covered."

3. **Engine misuse from bad prompting** (8+ sessions). Codex dispatched for web-search-heavy tasks without proper skills or timeout config. The engine is capable — the dispatch was under-specified.

4. **Parent death orphans workers** (5+ sessions). Socket disconnect kills the reporting channel. Cloud resources keep billing. No final report written.

5. **SDK_ERROR with no guidance** (6+ sessions). `model_not_found` returns a generic error. Agent can't self-correct. Wastes a dispatch cycle.

**What breaks in the ecosystem (150KB research corpus):**

- Silent failure is the #1 unsolved problem across all harnesses. No tool has structured failure propagation with recovery paths.
- Multi-agent coordination is unsolved. CooperBench: cooperative parallel = 30-50% worse than solo.
- Context starvation kills workers. Orientation burns 20-40% of context before first useful edit.
- Harness quality > model quality. Same Opus 4.6 weights: 58.0% in Claude Code, 81.8% in ForgeCode harness. 23.8-point gap from scaffolding alone.
- Tool catalog complexity is a trap. Manus backend lead (1,886 upvotes) abandoned function calling for single shell passthrough.

---

## 2. Design Principles

### 2.1 Agent-mux is a tool, not an orchestrator

Agent-mux is a flexible dispatch layer where the LLM makes decisions. It is NOT a deterministic orchestration framework. Pre-configured pipelines and roles are presets that condense good practices into config — the LLM decides when and how to use them. Orchestration logic is never baked at the code level.

### 2.2 Job done is holy

Never discard completed work. Timeout kills the process, not the artifacts. Every dispatch has an artifact path. Workers write incrementally. Recovery is a first-class primitive.

### 2.3 Errors are steering signals

Every error response is crafted for the calling LLM to self-correct. Not `"SDK_ERROR"` — instead: what failed, why, what to try next. The error channel is a gold mine for nudging agent behavior.

### 2.4 Single-shot with curated context is the default

Orchestration is explicit escalation, not baseline. One well-prompted dispatch with narrow context beats a swarm of under-specified workers.

### 2.5 Simplest viable dispatch

If it can be a CLI call, it's a CLI call. If it can be a single file, it's a single file. If it can be config, it's config. Code is the last resort.

### 2.6 Config over code

Roles, pipelines, default models, timeout behavior, sandbox settings — all in config files, not hardcoded. The Go binary is generic; the config makes it specific to a project.

---

## 3. Architecture Overview

```
┌─────────────────────────────────────────────────┐
│  Calling LLM (Claude Code, Jenkins, any agent)  │
│  Invokes agent-mux as a tool / CLI command       │
└──────────────────────┬──────────────────────────┘
                       │ CLI args + prompt (stdin or arg)
                       ▼
┌─────────────────────────────────────────────────┐
│              agent-mux (Go binary)              │
│                                                 │
│  ┌───────────┐  ┌──────────┐  ┌─────────────┐  │
│  │  Config   │  │ Dispatch │  │  Supervisor  │  │
│  │  Loader   │  │  Router  │  │  (goroutine) │  │
│  └───────────┘  └──────────┘  └─────────────┘  │
│  ┌───────────┐  ┌──────────┐  ┌─────────────┐  │
│  │  Event    │  │ Recovery │  │  Liveness    │  │
│  │  Stream   │  │  Manager │  │  Watchdog    │  │
│  └───────────┘  └──────────┘  └─────────────┘  │
└──────────────────────┬────────────────────────┘
                       │ spawn binary + read event stream
                       ▼
              ┌──────────────────┐
              │    LoopEngine    │
              │ (unified engine) │
              │                  │
              │ ┌──────────────┐ │
              │ │ Harness      │ │
              │ │ Adapters     │ │
              │ │              │ │
              │ │ claude.go    │ │
              │ │ codex.go     │ │
              │ │ gemini.go    │ │
              │ └──────────────┘ │
              └──────────────────┘
                       │
                       ▼
         ┌─────────────────────────┐
         │  Harness binaries       │
         │  (Claude Code, Codex,   │
         │   Gemini CLI)           │
         │                         │
         │  They handle:           │
         │  - LLM API calls        │
         │  - Tool execution       │
         │  - Context management   │
         │  - Conversation loop    │
         └─────────────────────────┘
```

### 3.1 Language: Go

- Goroutines for process supervision, event stream reading, liveness watchdog, fan-out
- `context.Context` propagates cancellation through the entire dispatch chain — no orphan processes
- `errgroup.WithContext` for fail-fast parallel tasks (e.g., config loading). Fan-out uses a custom partial-success collector (see §11.4)
- Process group kill (`syscall.Kill(-pgid, ...)`) ensures grandchildren die with parent
- Supervision model: kill the process, preserve the artifacts. When the parent dies or timeout hits, the harness subprocess is terminated, but all written artifacts persist at the artifact path. The process dies; the work lives.
- Single static binary. No runtime dependencies. `go install` + GoReleaser + Homebrew tap.

### 3.2 Project Layout

```
cmd/agent-mux/main.go     # entry point
internal/
  engine/
    loop.go                # LoopEngine: event stream reader + interaction hooks
    adapter/
      claude.go            # Claude Code event parsing + resume protocol
      codex.go             # Codex event parsing + session protocol
      gemini.go            # Gemini event parsing
    registry.go            # engine registry + model validation + fuzzy matching
  config/                  # TOML loader, role/pipeline resolution
  dispatch/                # dispatch spec builder, result normalizer
  event/                   # agent-mux event stream (stderr NDJSON)
  inbox/                   # coordinator mailbox (file-based + --signal)
  hooks/                   # pre-dispatch validation, dangerous pattern detection
  liveness/                # watchdog, frozen detection
  recovery/                # artifact scanning, continuation
  supervisor/              # process lifecycle, signal handling
  types/                   # shared types
```

### 3.3 Engine Architecture

**Agent-mux is NOT a harness. It is a structured dispatch layer that sits on top of existing harnesses.**

The harnesses are: Claude Code, Codex CLI, Gemini CLI. They handle tool execution, context management, LLM API calls, conversation loops, doom loops — everything. Agent-mux provides unified contract, supervision, config, hooks, traceability, and inbox on top.

> **Note:** Forge adapter deferred pending upstream event streaming support (see experiment: forgecode-ndjson-streaming).

#### LoopEngine — SDK-level harness communication

One `LoopEngine` struct parameterized by a `HarnessAdapter` interface. Agent-mux does NOT call LLM APIs, execute tools, or manage conversation context. The harness does all that. LoopEngine starts the harness binary, reads its live structured event stream, and interacts at event boundaries. ~400-500 lines of Go.

```
v1 subprocess:  spawn binary → wait → read final output → done (opaque)
LoopEngine:     spawn binary → read live event stream → interact at boundaries → done (transparent, interactive)
```

Same binary. Same harness. But LoopEngine reads the LIVE EVENT STREAM and can interact at event boundaries (inbox check, hooks, liveness, structured events).

**What this gives us:**
- Observability: parse the harness event stream in real time — tool calls, file writes, responses, errors
- Liveness: detect frozen harnesses from event stream silence, not opaque stderr
- Inbox delivery: at event boundaries, check inbox and inject messages via harness-native resume mechanisms
- Event emission: translate harness events into agent-mux's own NDJSON event stream
- Hooks: detect dangerous patterns in the harness event stream in real time
- TG bot integration: bot writes to inbox → next event boundary picks it up

**Per-harness event protocol:**

| Harness | Binary | Event Stream Flag | Event Format | Resume Support |
|---------|--------|-------------------|--------------|----------------|
| Claude Code | `claude` | `--output-format stream-json` | JSON lines on stdout (tool_use, text, result, etc.) | `--resume` + `--continue` with session ID |
| Codex CLI | `codex` | `--json` | NDJSON (thread.started, item.started, item.completed, turn.completed, etc.) | `codex exec resume --id <thread_id> --json ...` after `thread.started` |
| Gemini CLI | `gemini` | `-o stream-json` / `--output-format stream-json` | NDJSON, 6 types: init, message, tool_use, tool_result, error, result | `gemini --resume <session_id> -p ...` after `init` emits `session_id` |

**Gemini CLI details:**
- Batch mode: `-p <prompt>` or piped stdin
- Model: `-m <name>`, aliases: auto/pro/flash/flash-lite
- System prompt: `$GEMINI_SYSTEM_MD` env var (path to .md file), no CLI flag
- CWD: No flag — use subprocess cwd
- Permissions: `--approval-mode yolo|default|auto_edit|plan`
- Tool events: tool_use + tool_result with tool_id correlation
- Stats: result event carries total_tokens, input_tokens, output_tokens, duration_ms, tool_calls
- Exit codes: 0=success, 41=auth, 42=input, 52=config, 130=cancel
- Resume path: agent-mux uses `gemini --resume <session_id> -p <message>` once the run has emitted an `init` event with `session_id`

**For injection:** LoopEngine uses harness-native mechanisms. When an inbox message arrives at an event boundary and the harness has already emitted a resumable session/thread ID, the engine gracefully stops the current process and resumes via the harness's own native continuation protocol (for example, `claude --resume <session> --continue "Coordinator: <message>"`). A successful `--signal` only confirms the inbox write; actual injection happens later at an event boundary.

**Contrast with v1 SubprocessEngine:** v1 spawned the harness binary, waited for it to finish, then parsed the final output. Fully opaque — no visibility into what the harness was doing, no way to interact mid-flight, no liveness detection beyond process death. LoopEngine replaces this with a transparent, interactive communication layer.

#### Engine Interface

```go
type InboxMode int
const (
    InboxResumeCapable InboxMode = iota  // adapter can resume once a session/thread ID has been observed
    InboxNone                             // engine does not support inbox resume
)

type Engine interface {
    Name() string
    ValidModels() []string
    Dispatch(ctx context.Context, spec *DispatchSpec) (*DispatchResult, error)
    InboxMode() InboxMode  // resume-capable or none
}
```

Callers use the same interface regardless of which harness runs underneath. `InboxMode()` tells the coordinator whether `--signal` can use a harness-native resume path or is unsupported. Claude Code, Codex, and Gemini all sit in the resume-capable tier today, but `resume_session_missing` is still possible if no session/thread ID has been emitted yet.

---

## 4. Config System

### 4.1 Config Location

**Lookup order** (where agent-mux searches for config files):
1. `--config <path>` (explicit override — skips all other lookups)
2. `<cwd>/.agent-mux.toml` (project-level)
3. `$XDG_CONFIG_HOME/agent-mux/config.toml` or `~/.config/agent-mux/config.toml` (global)

All config files in the lookup chain are loaded and merged (later values override earlier ones). For role definitions specifically, the first definition found wins — a role defined in companion `.toml` is not merged with a same-named role in global config, it replaces it entirely. This prevents partial role definitions from combining unexpectedly. **Merge order** (which values win) is separate — see §4.4.

Project-level config (`.agent-mux.toml` in repo root) always beats global config. This is the merge rule, not the lookup rule. Lookup finds files; merge resolves conflicts.

### 4.2 Config Shape

```toml
# ~/.config/agent-mux/config.toml

[defaults]
engine = "codex"
model = "gpt-5.4"
effort = "high"
sandbox = "danger-full-access"      # full access is the default
permission_mode = "bypassPermissions"
response_max_chars = 2000            # truncate response field (0 = unlimited)
max_depth = 2                        # Maximum recursive dispatch depth
allow_subdispatch = true             # Whether workers can invoke agent-mux

[models]
# Known valid slugs per engine. Used for fuzzy matching on model_not_found.
codex = ["gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"]
claude = ["claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"]
gemini = ["gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview"]

# ─── Roles ───────────────────────────────────────
# Named roles map to engine/model/effort defaults.
# Dispatch via: agent-mux --role heavy_lifter "build the parser"

[roles.explorer]
engine = "codex"
model = "gpt-5.4-mini"
effort = "medium"

[roles.heavy_lifter]
engine = "codex"
model = "gpt-5.4"
effort = "high"

[roles.architect]
engine = "claude"
model = "claude-opus-4-6"
effort = "high"

[roles.auditor]
engine = "gemini"
model = "gemini-2.5-pro"
effort = "medium"

[roles.scout]
engine = "gemini"
model = "gemini-2.5-flash"
effort = "low"

[roles.grunt]
engine = "codex"
model = "gpt-5.3-codex-spark"
effort = "low"

# ─── Pipelines ───────────────────────────────────
# Named step sequences. Each step references a role.
# Dispatch via: agent-mux --pipeline plan-execute-review "redesign auth"
# max_parallel caps total concurrent worker processes across all fan-out steps
# in a pipeline. Default: 8. This is a resource guard, not a performance tuning knob.

[pipelines.plan-execute-review]
max_parallel = 4

[[pipelines.plan-execute-review.steps]]
role = "architect"
pass_output_as = "plan"

[[pipelines.plan-execute-review.steps]]
role = "heavy_lifter"
receives = "plan"
pass_output_as = "artifacts"

[[pipelines.plan-execute-review.steps]]
role = "auditor"
receives = "artifacts"

[[pipelines.plan-execute.steps]]
role = "architect"
pass_output_as = "plan"

[[pipelines.plan-execute.steps]]
role = "heavy_lifter"
receives = "plan"

# ─── Tiered Thinking Roles ──────────────────────
# Presets for multi-tier orchestration (plan/execute/verify pattern).
# Tiered thinking is config presets + pipeline definitions, not hardcoded
# orchestration logic. The GSD coordinator prompt encodes behavioral policy;
# agent-mux stays a dumb tool.

[roles.planning]
engine = "claude"
model = "claude-opus-4-6"
effort = "xhigh"

[roles.execution]
engine = "codex"
model = "gpt-5.4"
effort = "medium"

[roles.verification]
engine = "claude"
model = "claude-sonnet-4-6"
effort = "high"

[roles.synthesis]
engine = "codex"
model = "gpt-5.4"
effort = "high"

# Pipeline preset using tiered roles (ForgeCode-inspired pattern):

[[pipelines.forgestyle.steps]]
name = "plan"
role = "planning"
parallel = 1
pass_output_as = "plan"

[[pipelines.forgestyle.steps]]
name = "execute"
role = "execution"
parallel = 3
receives = "plan"
pass_output_as = "results"

[[pipelines.forgestyle.steps]]
name = "verify"
role = "verification"
parallel = 1
receives = "results"

# ─── Liveness ────────────────────────────────────

[liveness]
heartbeat_interval_sec = 15     # heartbeat emission interval (default 15)
silence_warn_seconds = 90       # first silence: warn in event stream
silence_kill_seconds = 180      # second silence: SIGTERM + collect artifacts
repeat_escalation = true        # if same pattern freezes twice, report to caller

# ─── Timeout ─────────────────────────────────────

[timeout]
low = 120           # seconds
medium = 600
high = 1800
xhigh = 2700
grace = 60          # additional seconds after soft timeout signal
```

### 4.3 Coordinator Specs

Coordinator specs remain markdown files with YAML frontmatter at `<cwd>/.claude/agents/<name>.md`. In the current implementation that frontmatter is scalar-only:

```yaml
---
skills: [satellite-data, pratchett-read]
engine: claude
model: claude-opus-4-6
effort: high
timeout: 900
---

System prompt body here...
```

Frontmatter contributes only `skills`, `engine`, `model`, `effort`, and `timeout`. Optional companion `<name>.toml` is merged into config when the coordinator loads; that companion TOML is where role definitions, pipeline definitions, model lists, and other config blocks live.

### 4.4 Override Precedence

Normal CLI dispatches resolve scalar defaults with this merge chain (highest wins first):

```
Explicit CLI flags (--engine, --model, --effort, etc.)
  > Role config (resolved from merged TOML config, including optional companion <coordinator>.toml)
  > Coordinator frontmatter scalar defaults (.md)
  > Merged config defaults (.toml chain: --config or global+project, then companion merge)
  > Hardcoded defaults
```

`timeout` is separate:

```
CLI --timeout > coordinator frontmatter timeout > timeout table from merged config for the chosen effort
```

`--config <path>` changes which base TOML config is loaded; it does not outrank coordinator frontmatter or role resolution. Role definitions and pipelines always come from merged TOML config, not from markdown frontmatter.

Project always beats global. CLI always beats everything.

---

## 5. Dispatch Contract

### 5.1 Input (CLI)

**Preferred machine interface:**

```
agent-mux preview --stdin
agent-mux dispatch --stdin
```

`preview` is a non-executing preflight step. It resolves config, coordinator,
role, pipeline, defaults, artifact paths, prompt preamble, and traceability
fields into one JSON object without launching a harness process. Programmatic
callers (bots, coordinators, wrappers) should prefer `preview` + `dispatch`
with stdin JSON over positional prompt strings.

**Compatibility interface (still supported):**

```
agent-mux [flags] "prompt text"
agent-mux [flags] --prompt-file path/to/prompt.md
agent-mux [flags] --recover <dispatch_id>
agent-mux [flags] --pipeline <name> "prompt text"
```

`preview` and `dispatch` are reserved first-position subcommands. If the literal
one-token prompt is exactly `"preview"` or `"dispatch"`, pass it after `--`
(`agent-mux -- preview`) or use `--prompt-file` / `--stdin`.

**Common flags:**

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--engine` | `-E` | string | config default | codex, claude, gemini |
| `--role` | `-R` | string | — | Named role from config. Sets engine/model/effort defaults (see §4.4). |
| `--cwd` | `-C` | string | `$PWD` | Working directory |
| `--model` | `-m` | string | role/config default | Model override |
| `--effort` | `-e` | string | `"high"` | low, medium, high, xhigh |
| `--timeout` | `-t` | int | effort-derived | Soft timeout in seconds (triggers grace period before hard kill) |
| `--system-prompt` | `-s` | string | — | Inline system prompt |
| `--system-prompt-file` | — | string | — | Load system prompt from file |
| `--coordinator` | — | string | — | Load `<cwd>/.claude/agents/<name>.md` |
| `--skill` | — | string[] | — | Load `<cwd>/.claude/skills/<name>/SKILL.md` |
| `--pipeline` | `-P` | string | — | Named pipeline from config |
| `--recover` | — | string | — | Previous dispatch_id to continue |
| `--context-file` | — | string | — | Path to context file for $AGENT_MUX_CONTEXT injection |
| `--artifact-dir` | — | string | `/tmp/agent-mux/<dispatch_id>/` | Where workers write artifacts |
| `--salt` | — | string | auto-generated | Human-greppable dispatch salt |
| `--yes` | — | bool | `false` | Skip interactive confirmation after preview when stdout/stderr are TTYs |
| `--config` | — | string | XDG default | Config file override |
| `--output` | `-o` | string | `"json"` | `json` (structured) or `text` (human-readable) |
| `--full` | `-f` | bool | `true` | Full access mode. On by default. Use `--no-full` to restrict. README documents the security posture. |
| `--no-full` | — | bool | `false` | Disable full access mode (sets FullAccess=false) |
| `--prompt-file` | — | string | — | Load prompt text from file (mutually exclusive with positional prompt) |
| `--max-depth` | — | int | `2` | Maximum recursive dispatch depth |
| `--no-subdispatch` | — | bool | `false` | Disable recursive dispatch (sets AllowSubdispatch=false) |
| `--signal` | — | string[] | — | Send message to running dispatch's inbox. Args: `<dispatch_id> <message>` |
| `--permission-mode` | — | string | — | Engine-specific permission mode (passed through EngineOpts) |
| `--stdin` | — | bool | `false` | Read full DispatchSpec as JSON from stdin. Recommended for programmatic callers. |
| `--response-max-chars` | — | int | `0` | Maximum characters in response before truncation (0 = no limit, default from config) |
| `--verbose` | `-v` | bool | `false` | Verbose event stream on stderr |

**Interactive preflight behavior:**

- `preview` never launches a harness. It prints the fully resolved plan as JSON
  and exits.
- `dispatch` is machine-safe: non-interactive callers can invoke it directly.
- When `dispatch` is launched from a TTY and `--yes` is not set, agent-mux
  should first show the same resolved preview summary a human operator would
  want to inspect, then require confirmation before the harness starts.
- If that confirmation is declined, no harness is launched. Agent-mux returns a
  structured failed result with `error.code = "cancelled"` and exits with code
  `130`.
- TTY confirmation is a UX layer, not a protocol requirement. Bots and other
  non-interactive callers should use `preview` explicitly and then `dispatch`.

**Engine-specific flags** (passed through to harness binary):

| Flag | Engine | Purpose |
|------|--------|---------|
| `--sandbox` | codex | Sandbox mode (default: danger-full-access) |
| `--reasoning` | codex | Reasoning effort level |
| `--max-turns` | claude | Max conversation turns |
| `--add-dir` | codex | Additional writable directories |

### 5.2 Prompt Transport

Three input modes, in order of preference for programmatic callers:

1. **Stdin JSON** (`--stdin`): Full DispatchSpec as JSON piped to stdin. Recommended for LLM-to-agent-mux dispatch. Zero escaping issues.
   ```
   echo '{"engine":"codex","prompt":"...","role":"heavy_lifter"}' | agent-mux --stdin
   ```

2. **Prompt file** (`--prompt-file path`): Complex prompts with code snippets, quotes, newlines. Already in v1.

3. **CLI argument**: Simple one-liners only. Escaping fragile at 2+ nesting levels.

If the caller passes `--context-file <path>`, agent-mux validates that file, sets `AGENT_MUX_CONTEXT=<path>`, and prepends a one-line prompt instruction: "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting." `context.md` is only a caller-side convention; agent-mux does not auto-write it from `artifact_dir`.

### 5.2.1 DispatchSpec

```go
// DispatchSpec is the fully-resolved input to Engine.Dispatch().
// Built by CLI parser, --stdin JSON decoder, or pipeline orchestrator.
// All transport-layer concerns (prompt-file, stdin JSON) are resolved
// before this struct is constructed.
type DispatchSpec struct {
    // ── Identity ──────────────────────────────────────────
    DispatchID string `json:"dispatch_id"`          // ULID, assigned at construction
    Salt       string `json:"salt,omitempty"`        // Human-greppable three-word phrase
    TraceToken string `json:"trace_token,omitempty"` // Deterministic grep token; default "AGENT_MUX_GO_<dispatch_id>"

    // ── Core dispatch parameters ──────────────────────────
    Engine       string `json:"engine"`              // "codex", "claude", "gemini" (required)
    Model        string `json:"model,omitempty"`     // Model slug, empty = engine default
    Effort       string `json:"effort"`              // "low"|"medium"|"high"|"xhigh", default "high"
    Prompt       string `json:"prompt"`              // Resolved user prompt text (always populated)
    SystemPrompt string `json:"system_prompt,omitempty"` // Resolved system prompt
    Cwd          string `json:"cwd"`                 // Working directory, default $PWD

    // ── Context & skills ──────────────────────────────────
    Skills      []string `json:"skills,omitempty"`       // Resolved skill names (content already in prompt)
    Coordinator string   `json:"coordinator,omitempty"`  // Coordinator spec name (metadata)
    ContextFile string   `json:"context_file,omitempty"` // Path to caller-managed context file for $AGENT_MUX_CONTEXT
    ArtifactDir string   `json:"artifact_dir"`           // Default: /tmp/agent-mux/<dispatch_id>/

    // ── Timeout ───────────────────────────────────────────
    TimeoutSec int `json:"timeout_sec,omitempty"` // Soft timeout (0 = effort-derived default)
    GraceSec   int `json:"grace_sec,omitempty"`   // Grace period after soft timeout, default 60

    // ── Role ──────────────────────────────────────────────
    Role string `json:"role,omitempty"` // Named role from config (metadata, already resolved)

    // ── Recursive dispatch controls ───────────────────────
    MaxDepth         int  `json:"max_depth"`          // Max recursion depth, default 2
    AllowSubdispatch bool `json:"allow_subdispatch"`  // Default true
    Depth            int  `json:"depth"`              // Current depth (auto from AGENT_MUX_DEPTH)

    // ── Lineage (pipeline & recovery) ─────────────────────
    ParentDispatchID    string `json:"parent_dispatch_id,omitempty"`
    PipelineID          string `json:"pipeline_id,omitempty"`
    PipelineStep        int    `json:"pipeline_step"`             // 0-based, -1 for non-pipeline
    ContinuesDispatchID string `json:"continues_dispatch_id,omitempty"` // --recover target

    // ── Pipeline step data flow ───────────────────────────
    Receives     string `json:"receives,omitempty"`      // Named output from prior step
    PassOutputAs string `json:"pass_output_as,omitempty"` // Name for this step's output
    Parallel     int    `json:"parallel,omitempty"`       // Fan-out count (0 or 1 = sequential)
    HandoffMode  string `json:"handoff_mode,omitempty"`   // "summary_and_refs"|"full_concat"|"refs_only"

    // ── Response control ─────────────────────────────────
    ResponseMaxChars int `json:"response_max_chars,omitempty"` // 0 = no truncation (use config default)

    // ── Engine-specific passthrough ───────────────────────
    EngineOpts map[string]any `json:"engine_opts,omitempty"` // Untyped bag for HarnessAdapter.BuildArgs()

    // ── Access mode ───────────────────────────────────────
    FullAccess bool `json:"full_access"` // Default true, --no-full sets false
}
```

**DispatchSpec defaults:**

| Field | Default |
|-------|---------|
| TraceToken | `AGENT_MUX_GO_<dispatch_id>` |
| Effort | "high" |
| Cwd | $PWD |
| ArtifactDir | /tmp/agent-mux/<dispatch_id>/ |
| MaxDepth | 2 |
| AllowSubdispatch | true |
| PipelineStep | -1 |
| FullAccess | true |
| GraceSec | 60 |
| HandoffMode | "summary_and_refs" |

DispatchSpec is always fully resolved before dispatch. The CLI layer resolves all transport concerns (--prompt-file, --stdin JSON, inline args) into the Prompt field. Engine.Dispatch() receives a ready-to-use struct.

The caller may omit `trace_token`. Agent-mux derives it deterministically from
`dispatch_id` before preview or dispatch execution. `salt` remains a human
alias; `trace_token` is the machine-greppable token that must survive into
child session logs.

**Artifact directory lifecycle:** agent-mux creates `<artifact_dir>` at dispatch start with mode 0755. Parent directories are created as needed (equivalent to `mkdir -p`). If the directory already exists, it is reused (no error). Symlinks are followed but not created. agent-mux does not perform cleanup — artifact directories persist until the caller or user removes them.

### 5.2.2 Flag Classification

- **Dispatch spec flags** (the "what"): --engine, --model, --effort, --cwd, --system-prompt, --coordinator, --skill[], --pipeline, --artifact-dir, --salt, --timeout, --max-depth, --role, --prompt-file, --recover, positional prompt. Settable via CLI OR --stdin JSON. (`--recover` is a dispatch spec concern because it sets `ContinuesDispatchID` and changes prompt construction.)
- **Runtime flags** (the "how"): --verbose, --output, --config, --stdin, --signal, --version, --help. Only settable via CLI. Ignored in --stdin JSON.

**Conflict resolution:** When --stdin is provided, it IS the DispatchSpec. All dispatch-related CLI flags are ignored. Runtime flags still apply. A warning is emitted if dispatch flags coexist. No merge — complete override for the dispatch spec domain.

### 5.3 Output (stdout JSON)

**Single JSON object on stdout. One `status` field. No ambiguity.**

Every stdout result and every stderr event includes `schema_version`. Consumers should check this field and warn on unknown versions.

**Schema Versioning:**
- `schema_version` is an integer included in every stdout result and stderr event
- Increments on breaking changes: field removal, type change, renamed field, changed semantics
- Does NOT increment on additive changes: new fields, new event types, new error codes, new status values
- Contract: schema_version 1 is stable for all 2.x releases. No fields removed or renamed within a major version
- Callers should warn on unknown version, never hard-fail. Ignore unknown fields (standard JSON forward-compatibility)

**Response truncation:** Agent-mux returns a compact result by default to prevent caller context bloat. The `response` field is capped at `response_max_chars` (default: 2000). If the full response exceeds the cap, it is written to `<artifact_dir>/full_output.md` and the `response` field contains the truncated version. Fields: `response_truncated` (bool) indicates truncation occurred; `full_output` (string, file path) points to the untruncated response. Set `response_max_chars: 0` in config for unlimited.

### 5.3.0 Preview Output

`preview` returns JSON, not text. Its purpose is to show the exact resolved
dispatch plan before any harness starts, without dumping the full resolved
prompt body or noisy derived engine state.

```json
{
  "schema_version": 1,
  "kind": "preview",
  "dispatch_spec": {
    "dispatch_id": "01JQXYZ...",
    "salt": "coral-fox-nine",
    "trace_token": "AGENT_MUX_GO_01JQXYZ...",
    "engine": "codex",
    "model": "gpt-5.4-mini",
    "effort": "high",
    "role": "heavy_lifter",
    "cwd": "/repo",
    "artifact_dir": "/tmp/agent-mux/01JQXYZ/",
    "timeout_sec": 600,
    "grace_sec": 60,
    "max_depth": 2,
    "allow_subdispatch": true,
    "full_access": true
  },
  "prompt": {
    "excerpt": "Implement the parser upgrade ... keep tests green.",
    "chars": 1840,
    "truncated": true,
    "system_prompt_chars": 420
  },
  "control": {
    "control_record": "/tmp/agent-mux/control/01JQXYZ.json",
    "artifact_dir": "/tmp/agent-mux/01JQXYZ/"
  },
  "prompt_preamble": [
    "Trace token: AGENT_MUX_GO_01JQXYZ...",
    "Dispatch ID: 01JQXYZ...",
    "Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR."
  ],
  "warnings": [],
  "confirmation_required": true
}
```

`confirmation_required` is a UI hint for interactive callers. Non-interactive
callers can ignore it.

`dispatch_spec` is intentionally compact. It includes the resolved execution
plan and identity fields, but not the full `prompt`, `system_prompt`, or
derived `engine_opts` map. Prompt content is summarized separately under
`prompt`.

#### Completed:
```json
{
  "schema_version": 1,
  "status": "completed",
  "dispatch_id": "01JQXYZ...",
  "dispatch_salt": "coral-fox-nine",
  "response": "Built the parser. 3 files modified.",
  "response_truncated": false,
  "full_output": null,
  "handoff_summary": "Implemented Go parser with 3 source files. All tests pass. Ready for review.",
  "artifacts": [
    "/tmp/agent-mux/01JQXYZ/src/parser.go",
    "/tmp/agent-mux/01JQXYZ/src/parser_test.go"
  ],
  "duration_ms": 45200,
  "activity": {
    "files_changed": ["src/parser.go", "src/parser_test.go"],
    "files_read": ["src/types.go", "src/main.go"],
    "commands_run": ["go build ./...", "go test ./..."],
    "tool_calls": ["Read", "Edit", "Bash", "Bash"]
  },
  "metadata": {
    "engine": "codex",
    "model": "gpt-5.4",
    "role": "heavy_lifter",
    "tokens": { "input": 45000, "output": 8200 },
    "turns": 12,
    "cost_usd": 0.23,
    "session_id": "..."
  }
}
```

#### Timed out (with preserved work):
```json
{
  "schema_version": 1,
  "status": "timed_out",
  "dispatch_id": "01JQXYZ...",
  "dispatch_salt": "coral-fox-nine",
  "response": "Was building parser when timeout hit. 2 of 3 files complete.",
  "response_truncated": false,
  "full_output": null,
  "handoff_summary": "Parser partially built — 2 of 3 files written. parser.go is complete, parser_test.go missing.",
  "artifacts": ["/tmp/agent-mux/01JQXYZ/src/parser.go"],
  "partial": true,
  "recoverable": true,
  "reason": "Soft timeout at 600s, hard kill after 660s grace.",
  "duration_ms": 660000,
  "activity": { ... },
  "metadata": { ... }
}
```

#### Failed (with agent-friendly guidance):
```json
{
  "schema_version": 1,
  "status": "failed",
  "dispatch_id": "01JQXYZ...",
  "dispatch_salt": "coral-fox-nine",
  "response": "",
  "response_truncated": false,
  "full_output": null,
  "handoff_summary": "",
  "error": {
    "code": "model_not_found",
    "message": "Model 'gpt-5.3-codex-spark' not found for engine codex.",
    "suggestion": "Did you mean 'gpt-5.4-mini'? Valid models: gpt-5.4, gpt-5.4-mini, gpt-5.3-codex-spark, gpt-5.2-codex.",
    "retryable": true
  },
  "artifacts": [],
  "duration_ms": 430,
  "activity": { ... },
  "metadata": { ... }
}
```

#### Failed (frozen tool):
```json
{
  "schema_version": 1,
  "status": "failed",
  "dispatch_id": "01JQXYZ...",
  "dispatch_salt": "coral-fox-nine",
  "response": "",
  "response_truncated": false,
  "full_output": null,
  "handoff_summary": "",
  "error": {
    "code": "frozen_tool_call",
    "message": "No harness events for 180s. Likely frozen tool call in harness. Process terminated.",
    "suggestion": "Worker may have hit a hanging network request or infinite loop. Retry with --timeout or try a different approach. Partial work preserved.",
    "retryable": true,
    "partial_artifacts": ["/tmp/agent-mux/01JQXYZ/draft.md"]
  },
  "artifacts": ["/tmp/agent-mux/01JQXYZ/draft.md"],
  "duration_ms": 180000,
  "activity": { ... },
  "metadata": { ... }
}
```

### DispatchResult Type Definition

```go
type DispatchStatus string
const (
    StatusCompleted DispatchStatus = "completed"
    StatusTimedOut  DispatchStatus = "timed_out"
    StatusFailed    DispatchStatus = "failed"
)

type DispatchResult struct {
    SchemaVersion     int              `json:"schema_version"`
    Status            DispatchStatus   `json:"status"`
    DispatchID        string           `json:"dispatch_id"`
    DispatchSalt      string           `json:"dispatch_salt"`
    Response          string           `json:"response"`
    ResponseTruncated bool             `json:"response_truncated"`
    FullOutput        *string          `json:"full_output"`
    HandoffSummary    string           `json:"handoff_summary"`
    Artifacts         []string         `json:"artifacts"`
    Partial           bool             `json:"partial,omitempty"`
    Recoverable       bool             `json:"recoverable,omitempty"`
    Reason            string           `json:"reason,omitempty"`
    Error             *DispatchError   `json:"error,omitempty"`
    Activity          *DispatchActivity `json:"activity"`
    Metadata          *DispatchMetadata `json:"metadata"`
    DurationMS        int64            `json:"duration_ms"`
}

type DispatchError struct {
    Code             string   `json:"code"`
    Message          string   `json:"message"`
    Suggestion       string   `json:"suggestion"`
    Retryable        bool     `json:"retryable"`
    PartialArtifacts []string `json:"partial_artifacts,omitempty"`
}

type DispatchActivity struct {
    FilesChanged []string `json:"files_changed"`
    FilesRead    []string `json:"files_read"`
    CommandsRun  []string `json:"commands_run"`
    ToolCalls    []string `json:"tool_calls"`
}

type DispatchMetadata struct {
    Engine           string      `json:"engine"`
    Model            string      `json:"model"`
    Role             string      `json:"role,omitempty"`
    Tokens           *TokenUsage `json:"tokens"`
    Turns            int         `json:"turns"`
    CostUSD          float64     `json:"cost_usd"`
    SessionID        string      `json:"session_id,omitempty"`
    PipelineID       string      `json:"pipeline_id,omitempty"`        // Shared across pipeline steps
    ParentDispatchID string      `json:"parent_dispatch_id,omitempty"` // Parent in pipeline chain
}
```

`TokenUsage` is defined in §6.1.1 (`HarnessEvent`). `DispatchResult` is the Go struct backing all three JSON shapes above (completed, timed_out, failed). Fields like `Partial`, `Recoverable`, `Reason`, and `Error` are zero-valued for completed dispatches and populated for timeout/failure cases.

**HandoffSummary generation:** Agent-mux extracts the handoff summary from the worker's response using a two-tier strategy: (1) If the response contains a `## Summary` or `## Handoff` markdown section, that section's content is used (up to 2000 chars). (2) Otherwise, the first 2000 characters of `Response` are used, truncated at the last sentence boundary. `HandoffSummary` is always populated for completed dispatches (even non-pipeline ones, for use by the calling LLM). For failed dispatches, it contains the error message.

### 5.4 Error Code Catalog

Every error code is designed as a steering signal for the calling LLM.

| Code | Meaning | Suggestion Pattern |
|------|---------|-------------------|
| `model_not_found` | Invalid model slug | Fuzzy match → "Did you mean X?" + valid list |
| `engine_not_found` | Unknown engine name | "Valid engines: codex, claude, gemini" |
| `binary_not_found` | Engine CLI not installed | "Install X: brew install X / go install X" |
| `api_key_missing` | No auth for engine | "Set $KEY_NAME or run `engine auth login`" |
| `api_overloaded` | Provider 429/529 | "Provider overloaded. Retry in 30s or try --engine Y" |
| `api_error` | Other provider error | Raw error + "Check provider status page" |
| `frozen_tool_call` | No harness events for silence_kill_seconds | "Likely frozen. Partial work preserved at path." |
| `invalid_args` | Bad CLI args | Specific: "Unknown flag --foo. Did you mean --full?" |
| `config_error` | Bad TOML / missing role | "Role 'X' not found in config. Available: [...]" |
| `process_killed` | Engine process died unexpectedly | "Exit code N. stderr: [last 5 lines]. Check engine logs." |
| `recovery_failed` | --recover couldn't find artifacts | "No artifacts found via control record or legacy root. Check the dispatch ID and artifact directory." |
| `output_parse_error` | Harness event stream contained malformed data that could not be parsed into a DispatchResult. May indicate a harness crash mid-stream, corrupted output, or incompatible harness version. | "Check engine version. Raw output preserved at artifact path." |
| `skill_not_found` | Skill directory not found | "Available skills: [...]. Check --skill name and --cwd." |
| `coordinator_not_found` | Coordinator file not found | "Searched: project agents/, bundled agents/. Check --coordinator name." |
| `prompt_file_missing` | --prompt-file path doesn't exist | "File not found: [path]." |
| `artifact_dir_unwritable` | Can't write to artifact directory | "Check permissions on [path]." |
| `cancelled` | Operator declined TTY confirmation before launch | "Re-run with --yes to skip confirmation, or answer y to proceed." |
| `interrupted` | SIGINT/SIGTERM received | "Dispatch interrupted. Partial artifacts at [path]." |

---

## 6. Engine Interface

### 6.1 Go Interface

```go
type InboxMode int
const (
    InboxResumeCapable InboxMode = iota  // adapter can resume once a session/thread ID has been observed
    InboxNone                             // engine does not support inbox resume
)

type Engine interface {
    Name() string
    ValidModels() []string
    Dispatch(ctx context.Context, spec *DispatchSpec) (*DispatchResult, error)
    InboxMode() InboxMode  // resume-capable or none
}

// HarnessAdapter interface — one implementation per harness (internal/engine/adapter/)
// Encapsulates per-harness differences: binary name, CLI flags, event stream format, resume protocol.
type HarnessAdapter interface {
    Binary() string                                          // e.g., "claude", "codex", "gemini"
    BuildArgs(spec *DispatchSpec) []string                   // construct CLI flags from dispatch spec
    ParseEvent(line string) (*HarnessEvent, error)           // parse one line from harness event stream
    SupportsResume() bool                                    // can the harness resume a session with injected message?
    ResumeArgs(sessionID string, message string) []string    // build CLI args for resume-with-injection
}
// Implementations: ClaudeAdapter, CodexAdapter, GeminiAdapter
// Adapters can be partially config-driven via harness config files
// (e.g., Gemini's settings) for flags that vary by project.
```

- One `LoopEngine` struct + one `HarnessAdapter` interface. Adding a new harness = implement `HarnessAdapter`.
- `HarnessAdapter` implementations are ~100-150 lines each: CLI flag construction, event stream parsing, resume protocol.
- The `LoopEngine` is generic: process supervision + event stream reading + inbox checking + event emission. It delegates all harness-specific behavior to the adapter.

### 6.1.1 HarnessEvent

```go
type EventKind int

const (
    EventUnknown       EventKind = iota
    EventToolStart               // Harness began a tool call
    EventToolEnd                 // Harness finished a tool call
    EventFileWrite               // Harness wrote a file
    EventFileRead                // Harness read a file
    EventCommandRun              // Harness ran a shell command
    EventProgress                // Free-form progress
    EventResponse                // Final or partial response text
    EventError                   // Harness-reported error
    EventSessionStart            // Session initialized (carries session ID)
    EventTurnComplete            // Turn finished (carries token counts)
    EventTurnFailed              // Turn failed
    EventRawPassthrough          // Unclassifiable line (--verbose passthrough)
)

type HarnessEvent struct {
    Kind      EventKind
    Timestamp time.Time
    Tool      string       // Set for ToolStart/ToolEnd
    FilePath  string       // Set for FileWrite/FileRead
    Command   string       // Set for CommandRun
    Text      string       // Set for Progress/Response/Error
    SessionID string       // Set for SessionStart
    DurationMS int64       // Set for ToolEnd/TurnComplete
    Tokens    *TokenUsage  // Set for TurnComplete
    ErrorCode string       // Set for Error
    Raw       []byte       // Always set (original harness line)
}

type TokenUsage struct {
    Input     int `json:"input"`
    Output    int `json:"output"`
    Reasoning int `json:"reasoning,omitempty"`
}
```

Flat struct chosen over interface hierarchy. One consumer (LoopEngine), one switch on Kind. Each HarnessAdapter.ParseEvent() populates the relevant fields for the Kind it returns; irrelevant fields are zero-valued.

### 6.2 LoopEngine Lifecycle

The LoopEngine does NOT call LLM APIs, execute tools, or manage conversation context. The harness does all that. LoopEngine starts the harness binary with event streaming enabled, reads its live event stream, and interacts at event boundaries.

```
1.  Write _dispatch_meta.json to artifact dir
2.  Build command via HarnessAdapter.BuildArgs()
    - Includes event streaming flag (e.g., --output-format stream-json for Claude)
    - Includes prompt, system prompt, model, cwd, sandbox settings
3.  Start harness binary with process group (Setpgid: true)
4.  Goroutine: read stdout event stream line-by-line
    - Parse each line via HarnessAdapter.ParseEvent()
    - Classify: tool_start, tool_end, file_write, response, error, etc.
    - Emit corresponding agent-mux events on stderr
    - Track activity (files_changed, commands_run, tool_calls)
    - Update liveness watchdog timestamp
5.  Goroutine: liveness watchdog
    - If no harness events for silence_warn_seconds → emit "frozen_warning" event
    - If continued silence for silence_kill_seconds → SIGTERM + collect artifacts
6.  Between harness event boundaries: CHECK INBOX
    - If message present:
      a. Graceful stop current harness process
      b. Resume via adapter.ResumeArgs(sessionID, message) with inbox content
      c. Emit "coordinator_inject" event
    - A successful `--signal` only guarantees the inbox append. Delivery still requires a resumable session/thread ID and the next event boundary.
7.  Soft timeout → emit timeout_warning event; hard timeout → SIGTERM + collect artifacts
8.  On harness completion: read final output from event stream
9.  Normalize via HarnessAdapter → DispatchResult
10. Scan artifact directory
11. Return
```

All harnesses go through this same lifecycle. The `HarnessAdapter` encapsulates the differences — how to build the CLI args, how to parse the event stream, whether resume is supported. The LoopEngine itself is harness-agnostic.

### 6.3 Skill Injection

Same mechanism as v1, carried to Go:
- `--skill <name>` loads `<cwd>/.claude/skills/<name>/SKILL.md`
- Content prepended to prompt as tagged blocks
- If `scripts/` exists, directory prepended to `PATH` of harness subprocess
- Deduplication: coordinator + CLI skills merged, loaded once

**MCP tools are NOT passed to harnesses.** MCP tools are wrapped as CLI scripts in skill `scripts/` directories. The harness sees available commands described in the skill text. Agent-mux has zero MCP awareness. Skills are the universal interface.

### 6.3.1 Context Sharing Contract

- The **caller** provides the context file path explicitly via `DispatchSpec.ContextFile` (CLI: `--context-file <path>`). If the caller omits the path, no context file is injected. Agent-mux does not auto-generate a context path from `artifact_dir` -- the caller must know the path before constructing the DispatchSpec.
- The **caller** writes the context file (not agent-mux). Agent-mux never generates context from scratch.
- Agent-mux validates the file exists, sets `AGENT_MUX_CONTEXT=<path>` in subprocess env, and prepends a one-line instruction to the prompt: "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting."
- Agent-mux also exports `AGENT_MUX_DISPATCH_ID`, `AGENT_MUX_TRACE_TOKEN`,
  `AGENT_MUX_SALT`, and `AGENT_MUX_ARTIFACT_DIR` to the harness subprocess.
- The prompt preamble must include `trace_token` and `dispatch_id` in the first
  injected lines so the child engine session log contains a deterministic grep
  marker even if the worker never mentions it explicitly later.
- **Pull model:** the worker reads the file when the model chooses to. Agent-mux does not force-inject file contents into the prompt.
- Pipelines do not auto-write `context.md` for `receives`. Step handoff is rendered into worker prompts, and any existing `ContextFile` on the base dispatch is inherited unchanged by worker specs.
- Context file format is a convention (Markdown recommended), not enforced by agent-mux.

### 6.4 Recursive Dispatch Controls

Workers can self-dispatch via agent-mux if the binary is on PATH and skills teach them how. This is a feature (emergent multi-agent coordination) but needs explicit controls:

- `max_depth` (default: 2): Maximum recursion depth. Agent-mux injects `AGENT_MUX_DEPTH=N` into child env. At max_depth, agent-mux refuses to dispatch and returns an error: "Max dispatch depth reached. Complete work directly."
- `allow_subdispatch` (default: true): Set to false for analysis/audit tasks. Worker cannot invoke agent-mux.

These fields are part of DispatchSpec and can be set via CLI (`--max-depth`, `--no-subdispatch`) or config.

### 6.5 Hooks (Pre-Dispatch Validation + Event-Level Detection)

Agent-mux does not execute tools — the harness does. Hooks operate at two levels:

**Level 1: Pre-dispatch validation** — before the harness binary starts.
Checks the prompt text and CLI flags against configurable pattern lists. Catches dangerous instructions before they reach the harness.

**Level 2: Event-level pattern detection** — while reading the harness event stream.
As the LoopEngine parses harness events (tool_start, command_run, file_write), it checks them against pattern lists. Detection is observational — agent-mux cannot block individual tool calls inside the harness. But it CAN: emit warning events, log to the event stream, and in extreme cases kill the harness process.

**Pattern matching semantics:** Hook patterns use substring matching (case-insensitive). A deny pattern `rm -rf` matches any command containing that substring. For path patterns, matching is against the absolute resolved path. Regex is not supported — use multiple substring patterns instead. Simpler is better.

**Deny list** (pre-dispatch: reject the dispatch; event-level: emit error + kill process):
- `rm -rf /`, `rm -rf ~`, `rm -rf *` (path-destructive)
- `DROP TABLE`, `DROP DATABASE` (data-destructive SQL)
- `git push --force`, `git push -f` (history-destructive)
- `sudo` (privilege escalation)

**Warn list** (pre-dispatch: log warning, proceed; event-level: emit warning event):
- `curl`, `wget` (network access)
- `pip install`, `npm install -g` (global package install)

**Config-driven via `[hooks]` table:**
```toml
[hooks]
deny = ["rm -rf /", "DROP TABLE", "git push --force", "sudo"]
warn = ["curl", "wget", "pip install"]
event_deny_action = "kill"     # "kill" or "warn" — what to do when deny pattern detected in event stream
```

**Prompt injection:** Deny/warn lists are also injected into the prompt as instructions to the harness ("Do NOT run these commands: ..."). This is defense-in-depth — the prompt tells the model not to do it, and the event-level hook catches it if the model does it anyway.

---

## 7. Timeout & Liveness

### 7.1 Soft Timeout Model

Hard kills are the #1 cause of lost work. The new model:

```
T=0                    T=soft_limit          T=soft+grace
 │                          │                      │
 │  normal execution        │  "wrap up" signal     │  SIGTERM
 │  ─────────────────────>  │  ──────────────────>  │  ──────>
 │                          │  grace period         │  collect
 │                          │  worker can finish    │  artifacts
```

1. **Soft limit** (effort-derived from config): Emit `timeout_warning` event on stderr. The harness process is NOT killed. For resume-capable adapters (Claude Code, Codex, Gemini), agent-mux also writes a "wrap up" message to the inbox and attempts the same harness-native continuation path used for coordinator signals. This still depends on the run having emitted a resumable session/thread ID; otherwise the dispatch can fail with `resume_session_missing`.

2. **Grace period** (configurable, default 60s): Engine keeps running. Agent-mux watches for completion. If the engine finishes within grace, result is `status: "completed"` (not timed_out).

3. **Hard kill** (soft + grace exhausted): SIGTERM to process group. Collect artifacts from `--artifact-dir`. Return `status: "timed_out"` with `recoverable: true`.

### 7.2 Liveness Watchdog

Orthogonal to timeout. Detects silence regardless of elapsed time. Based on the harness event stream — if no harness events for N seconds, the harness is likely frozen.

```
Harness event stream ──> LoopEngine parser ──> Watchdog
                                                  │
                         silence_warn_seconds exceeded?
                                                  │
                           YES ─────> emit "frozen_warning" event
                                      tell caller: "no harness events for Ns"
                                                  │
                         silence_kill_seconds exceeded?
                                                  │
                           YES ─────> SIGTERM process
                                      collect artifacts
                                      status: "failed"
                                      code: "frozen_tool_call"
```

The liveness watchdog tracks only harness-originated events (parsed via `HarnessAdapter.ParseEvent()`). Agent-mux's own heartbeat emissions on stderr are NOT counted as activity. This prevents the watchdog from masking harness silence behind its own heartbeat. Same mechanism for all harnesses — the adapter parses the events, the watchdog watches the timestamps.

**Gentle escalation:**
- First freeze in a session: stop the dispatch, return artifacts + error with suggestion
- Calling LLM sees the `frozen_tool_call` error and decides: retry, use different approach, or skip
- Agent-mux does NOT auto-retry. The LLM's judgment handles escalation.

**Liveness vs timeout: first terminal condition wins.** If both soft timeout and frozen detection trigger during the same dispatch, the first terminal condition wins. Once a terminal condition fires (timeout_warning -> grace -> kill, or frozen_warning -> kill), subsequent conditions are suppressed. The `status` and `reason` in `DispatchResult` reflect whichever condition triggered the kill.

### 7.3 Coordinator Mailbox

Agent-mux creates `<artifact_dir>/inbox.md` at dispatch start. The coordinator LLM (or any external system — TG bot, CI, human) can send direction to a running worker via:

    agent-mux --signal <dispatch_id> "focus on the auth module, skip the tests"

This resolves `<dispatch_id>` through the control record at `/tmp/agent-mux/control/<dispatch_id>.json` first, then the legacy default artifact path, and appends the message to `inbox.md`. On success the CLI returns a small JSON acknowledgement with the resolved artifact directory. The `--signal` CLI interface is the same regardless of harness.

**Inbox file concurrency:** Inbox delivery uses POSIX `O_APPEND` writes. Each `--signal` call opens `inbox.md` with `O_WRONLY|O_APPEND|O_CREAT`, writes the message as a single write(2) call (<= PIPE_BUF = 4096 bytes on most systems, guaranteeing atomicity), and closes the file. Multiple concurrent `--signal` calls are safe -- messages may interleave in order but none are lost. The receiver (LoopEngine inbox check) acquires an advisory `flock(LOCK_EX)` on the file, reads all content, truncates to zero, and releases the lock. This ensures each message is read exactly once.

**Delivery depends on harness resume support and session discovery:** At event boundaries, the LoopEngine checks the inbox. If a message is present and the harness has already emitted a resumable session/thread ID, the engine gracefully stops the current harness process and resumes via `HarnessAdapter.ResumeArgs(sessionID, message)`. If the message arrives before that ID exists, the run fails with `resume_session_missing`.

**TG bot integration path:** The inbox file is the universal interface. A TG bot writes to it via `--signal`. The JSON ack means the inbox write succeeded; the actual resumed turn happens later at an event boundary. This makes agent-mux the protocol layer between any external system and a running worker without custom bot-to-worker wiring.

---

## 8. Recovery Protocol

### 8.1 "Job Done Is Holy"

Every dispatch writes work incrementally to `--artifact-dir` (default: `/tmp/agent-mux/<dispatch_id>/`). This directory persists across process death and timeout. When the parent dies or the process is killed, the artifacts remain on disk — the process dies, the work lives.

Agent-mux injects `AGENT_MUX_ARTIFACT_DIR` as an environment variable into the harness subprocess and mentions the artifact path in the prompt preamble: "Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR."

The same preamble also carries the deterministic trace marker:

```
Trace token: AGENT_MUX_GO_<dispatch_id>
Dispatch ID: <dispatch_id>
Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR.
```

This is intentionally redundant. The trace token must be grep-visible inside
the child engine session log, not only in agent-mux's own artifacts.

### 8.2 Recovery Flow

```
agent-mux --recover 01JQXYZ "continue building the parser"
```

1. Read artifact directory for `01JQXYZ`
   - First resolve `/tmp/agent-mux/control/01JQXYZ.json` to find the registered artifact dir.
   - If no control record exists, fall back to the legacy default `/tmp/agent-mux/01JQXYZ/`.
2. List files, read any `_dispatch_meta.json` (written at dispatch start)
3. Build continuation prompt: "Previous worker (dispatch 01JQXYZ) completed the following before timeout: [file list + summary]. Continue from here: [user prompt]"
4. Dispatch to same or different engine (caller decides via flags)
5. New dispatch gets its own `dispatch_id` but carries `continues_dispatch_id: "01JQXYZ"` in metadata

### 8.3 Dispatch Metadata File

Written by agent-mux at dispatch start to `<artifact_dir>/_dispatch_meta.json`:

```json
{
  "dispatch_id": "01JQXYZ...",
  "dispatch_salt": "coral-fox-nine",
  "trace_token": "AGENT_MUX_GO_01JQXYZ...",
  "started_at": "2026-03-25T10:00:00Z",
  "engine": "codex",
  "model": "gpt-5.4",
  "role": "heavy_lifter",
  "prompt_hash": "sha256:abc123...",
  "cwd": "/path/to/project",
  "continues_dispatch_id": null
}
```

Updated on completion/timeout/failure with `ended_at`, `status`, `artifacts[]`.

At dispatch start agent-mux also writes a control record at `/tmp/agent-mux/control/<dispatch_id>.json` pointing to the resolved artifact directory and carrying the same trace token. `--recover` and `--signal` consult that record first, so custom `--artifact-dir` runs remain discoverable unless the control record is removed.

---

## 9. Streaming Events

### 9.1 Stderr NDJSON

Structured JSON lines on stderr. Each line is a self-contained event.

```jsonl
{"schema_version":1,"type":"dispatch_start","dispatch_id":"01JQXYZ","salt":"coral-fox-nine","engine":"codex","model":"gpt-5.4","ts":"..."}
{"schema_version":1,"type":"heartbeat","elapsed_s":15,"interval_s":15,"last_activity":"reading src/main.go","ts":"..."}
{"schema_version":1,"type":"tool_start","tool":"Read","args":"src/parser.go","ts":"..."}
{"schema_version":1,"type":"tool_end","tool":"Read","duration_ms":120,"ts":"..."}
{"schema_version":1,"type":"file_write","path":"src/parser.go","ts":"..."}
{"schema_version":1,"type":"command_run","command":"go test ./...","ts":"..."}
{"schema_version":1,"type":"timeout_warning","message":"Soft timeout reached. Grace period: 60s.","ts":"..."}
{"schema_version":1,"type":"frozen_warning","silence_seconds":90,"message":"No output for 90s.","ts":"..."}
{"schema_version":1,"type":"progress","message":"Worker reports: 2 of 3 files complete","ts":"..."}
{"schema_version":1,"type":"dispatch_end","status":"completed","duration_ms":45200,"ts":"..."}
```

### 9.2 Event Types

| Type | Source | When | Purpose |
|------|--------|------|---------|
| `dispatch_start` | agent-mux | Harness process spawned | Traceability. Includes all dispatch metadata. |
| `heartbeat` | agent-mux | Every `heartbeat_interval_sec` (default 15s) | Liveness proof. Includes last activity summary. Heartbeats stop when the dispatch reaches a terminal state (completed, timed_out, failed). During grace period, heartbeats continue (the dispatch is still alive). |
| `tool_start` | harness stream | Harness begins tool call | Streaming progress. Parsed from harness event stream. |
| `tool_end` | harness stream | Harness finishes tool call | Duration tracking. |
| `file_write` | harness stream | Harness writes a file | Artifact tracking in real time. |
| `file_read` | harness stream | Harness reads a file | Context tracking. |
| `command_run` | harness stream | Harness runs a shell command | Activity tracking. |
| `progress` | harness stream | Harness reports progress | Free-form status from worker. |
| `coordinator_inject` | agent-mux | Inbox message delivered | Confirms coordinator message reached worker. |
| `timeout_warning` | agent-mux | Soft timeout reached | Grace period signal. |
| `frozen_warning` | agent-mux | Silence threshold exceeded | Liveness alert. |
| `dispatch_end` | agent-mux | Harness process exited | Final status + summary. |
| `error` | agent-mux | Something broke | Structured error event. |

Agent-mux events are generated by parsing the harness event stream via `HarnessAdapter.ParseEvent()`, not by direct tool execution. Harness-derived events (`tool_start`, `tool_end`, `file_write`, `file_read`, `command_run`) are best-effort — availability depends on what the harness emits. Agent-mux emits what it can parse; missing events are not errors. Only `heartbeat` and `dispatch_start`/`dispatch_end` are guaranteed by agent-mux itself.

**Per-harness event capability matrix:**

| Event Type | Claude Code | Codex CLI | Gemini CLI |
|------------|-------------|-----------|------------|
| `tool_start` | Yes (tool_use event) | Yes (item.created) | Yes (tool_use NDJSON) |
| `tool_end` | Yes (result event) | Yes (item.completed) | Yes (tool_result NDJSON) |
| `file_write` | Yes (file_write event) | Best-effort (inferred) | Best-effort (inferred from tool_result) |
| `file_read` | Yes (file_read event) | Best-effort (inferred) | Best-effort (inferred from tool_result) |
| `command_run` | Yes (bash tool_use) | Yes (shell item) | Yes (tool_use with shell tool) |
| `progress` | Yes (text streaming) | Yes (text streaming) | Yes (message NDJSON) |
| `error` | Yes (error event) | Yes (error event) | Yes (error NDJSON) |

### 9.3 Caller Integration

- **Simple callers** (scripts, wrappers): ignore stderr, read stdout JSON.
- **Coordinators** (Jenkins, GSD): filter stderr for `heartbeat` → alive check.
- **Rich callers** (TG bot, TUI): consume full event stream for real-time display.
- **`--verbose`** flag: also emit engine's raw stderr lines (prefixed with `[engine]`).

---

## 10. Traceability

### 10.1 Dispatch Identity

Every dispatch gets two IDs:
- **`dispatch_id`** (ULID): machine-joinable, sortable by time, globally unique
- **`dispatch_salt`** (three-word human phrase): greppable in logs, artifacts, conversation. Format: `adjective-noun-digit` (e.g., `coral-fox-nine`). Auto-generated or `--salt` override.
- **`trace_token`** (deterministic string): defaults to `AGENT_MUX_GO_<dispatch_id>`. This is the canonical grep token for child engine logs.

All three identifiers appear on `dispatch_start` and `dispatch_end` events, in `_dispatch_meta.json`, in the control record, and in the final stdout JSON result. Other stderr events inherit identity from their containing dispatch — callers correlate by stream (stderr of the dispatch process). `trace_token` and `dispatch_id` are also injected into the prompt preamble and subprocess env so the worker's own engine session log contains a deterministic join key.

### 10.2 Lineage

- `dispatch_id`: this dispatch
- `parent_dispatch_id`: who called this (if pipeline step)
- `continues_dispatch_id`: previous dispatch this recovers from
- `pipeline_id`: shared ID across all steps in a pipeline run

### 10.3 Event Log

Append-only file at `<artifact_dir>/events.jsonl`. Every stderr event also written here. Survives process death (flushed per-line). Each event carries `dispatch_id`, `dispatch_salt`, and `trace_token`. Post-mortem debugging reads this file.

---

## 11. Pipeline System

### 11.1 Config-Driven Pipelines

Pipelines are TOML-defined step sequences. Each step references a role.

```toml
[[pipelines.plan-execute-review.steps]]
role = "architect"
pass_output_as = "plan"         # name this step's output

[[pipelines.plan-execute-review.steps]]
role = "heavy_lifter"
receives = "plan"               # inject previous step's output
pass_output_as = "artifacts"

[[pipelines.plan-execute-review.steps]]
role = "auditor"
receives = "artifacts"          # review the implementation
```

### 11.2 Pipeline Validation

Pipeline config is validated at load time. Validation rules:

1. `receives` must reference a `pass_output_as` from a preceding step (not self, not future steps). This prevents cycles and forward references.
2. `pass_output_as` must be unique across all steps in the pipeline.
3. `worker_prompts` length must equal `parallel` when both are set.
4. `parallel` must be >= 1.
5. At least one step must exist.
6. Validation errors are reported at startup with the specific field and rule violated (e.g., `"step[2].receives: 'plan' not found in preceding steps' pass_output_as"`).

### 11.3 Pipeline Execution

```
agent-mux --pipeline plan-execute-review "redesign the auth module"
```

1. Load and validate pipeline definition from config (§11.2)
2. Generate `pipeline_id` (shared ULID)
3. Execute steps sequentially (blocking — Forge-style)
4. Each step:
   - Resolve role → engine/model/effort
   - Build prompt: user prompt + injected output from `receives` step
   - Dispatch as a normal agent-mux call (recursive)
   - Collect result
   - Store output for next step's `receives`
5. Final result = last step's result, with full pipeline lineage in metadata

### 11.4 Fan-Out (Parallel Steps)

**Fan-out config shape:**

```toml
[pipelines.research]
max_parallel = 4  # Maximum concurrent workers across all fan-out steps (default: 8)

[[pipelines.research.steps]]
name = "gather"
role = "researcher"
parallel = 3
handoff_mode = "summary_and_refs"
pass_output_as = "findings"
worker_prompts = [
  "Focus on academic papers and formal specs",
  "Focus on Reddit, HN, community discussions",
  "Focus on GitHub repos and source code"
]

[[pipelines.research.steps]]
name = "synthesize"
role = "synthesizer"
receives = "findings"
parallel = 1
```

Parallel steps use a custom **partial-success collector** (WaitGroup-based), NOT `errgroup.WithContext`. errgroup cancels the context on first error, which is fail-fast — wrong for fan-out where we want all workers to finish and collect partial results. The collector waits for all goroutines, gathers successes and failures separately, and passes the combined results downstream.

**Partial failure semantics:** Binary threshold — at least one worker succeeds = continue with partial data, all fail = pipeline halts. No configurable min_success knob. Failed worker info is included in the handoff to the next step.

**Fan-out semantics:**
- Each parallel worker gets its own artifact subdirectory: `<artifact_dir>/step-0/worker-0/`, `worker-1/`, etc.
- Each worker returns a two-part result: inline summary (~500 tokens) + full output written to artifact file. The next step receives summaries + artifact paths, not raw concatenation. The synthesizer reads full files on demand.
- Cost: N workers × per-worker cost (reported in metadata, not enforced). The pipeline config can set `max_parallel` to cap fan-out.

**HandoffMode options:**
- `summary_and_refs` (default): ~500-token inline summaries + artifact paths per worker
- `full_concat`: Verbatim concatenation of all worker responses
- `refs_only`: Only artifact paths, no inline content

Default handoff format (summary_and_refs):
```
--- Worker 0 (completed) ---
Summary: [inline summary]
Full output: /tmp/agent-mux/<id>/step-0/worker-0/output.md

--- Worker 1 (failed) ---
Error: frozen_tool_call. Partial: /tmp/agent-mux/<id>/step-0/worker-1/output.md
```

**Step prompt composition:** Each step's prompt = pipeline position header + prior step output (if receives is set) + user's original prompt (carried through every step unchanged) + output instruction (if pass_output_as is set).

**`worker_prompts`:** Optional array. When provided, length must equal `parallel`. Worker[i] receives `worker_prompts[i]` as an additional instruction appended to the step prompt — it does not replace the user's original prompt, it adds focus direction. Enables directed fan-out (e.g., different research angles).

### 11.5 Pipeline Data Types

#### Type Definitions

```go
type WorkerStatus string
const (
    WorkerCompleted WorkerStatus = "completed"
    WorkerTimedOut  WorkerStatus = "timed_out"
    WorkerFailed    WorkerStatus = "failed"
)

type WorkerResult struct {
    WorkerIndex int          `json:"worker_index"`
    DispatchID  string       `json:"dispatch_id"`
    Status      WorkerStatus `json:"status"`
    Summary     string       `json:"summary"`
    ArtifactDir string       `json:"artifact_dir"`
    OutputFile  string       `json:"output_file,omitempty"`
    ErrorCode   string       `json:"error_code,omitempty"`
    ErrorMsg    string       `json:"error_msg,omitempty"`
    DurationMS  int64        `json:"duration_ms"`
}

type StepOutput struct {
    StepName    string         `json:"step_name"`
    StepIndex   int            `json:"step_index"`
    PipelineID  string         `json:"pipeline_id"`
    HandoffMode HandoffMode    `json:"handoff_mode"`
    Workers     []WorkerResult `json:"workers"`
    HandoffText string         `json:"handoff_text"`
    Succeeded   int            `json:"succeeded"`
    Failed      int            `json:"failed"`
    TotalMS     int64          `json:"total_ms"`
}

type HandoffMode string
const (
    HandoffSummaryAndRefs HandoffMode = "summary_and_refs"
    HandoffFullConcat     HandoffMode = "full_concat"
    HandoffRefsOnly       HandoffMode = "refs_only"
)
```

**Summary size limit:** Worker summaries in `WorkerResult.Summary` are capped at 2000 characters (~500 tokens). If the raw summary exceeds 2000 chars, it is truncated at the nearest sentence boundary and suffixed with ` [truncated — full output: <OutputFile>]`. The full untruncated output is always available at the artifact path.

**Artifact reference format:** `/tmp/agent-mux/<pipeline_id>/step-<N>/worker-<M>/output.md` — where `N` is the 0-based step index and `M` is the 0-based worker index within a fan-out step.

**Failure shape:** When `WorkerResult.Status` is `timed_out` or `failed`, the `Summary` field contains the last known progress (if any), `ErrorCode` carries the agent-mux error code (from §5.4), and `ErrorMsg` carries the human-readable message. `OutputFile` may still be populated if the worker wrote partial output before failure. The `ArtifactDir` always exists and may contain partial artifacts.

#### Handoff Text Templates

The `StepOutput.HandoffText` field is a pre-rendered string injected into the next step's prompt via the `receives` mechanism. Its format depends on `HandoffMode` and whether the step was sequential or fan-out.

**Sequential + summary_and_refs:**
```
=== Output from step "<StepName>" (completed, <DurationMS>ms) ===

Summary:
<WorkerResult.Summary>

Full output: <WorkerResult.OutputFile>
Artifact directory: <WorkerResult.ArtifactDir>
```

**Sequential + full_concat:**
```
=== Output from step "<StepName>" (completed, <DurationMS>ms) ===

<full verbatim content of WorkerResult.OutputFile>
```

**Sequential + refs_only:**
```
=== Output from step "<StepName>" (completed, <DurationMS>ms) ===

Full output: <WorkerResult.OutputFile>
Artifact directory: <WorkerResult.ArtifactDir>
```

**Sequential + failed (any mode):**
```
=== Output from step "<StepName>" (FAILED, <DurationMS>ms) ===

Error: <ErrorCode> — <ErrorMsg>
Partial artifacts: <ArtifactDir>
```

**Fan-out + summary_and_refs:**
```
=== Output from step "<StepName>" (<Succeeded> succeeded, <Failed> failed, <TotalMS>ms) ===

--- Worker 0 (completed, <DurationMS>ms) ---
Summary: <WorkerResult.Summary>
Full output: <WorkerResult.OutputFile>

--- Worker 1 (completed, <DurationMS>ms) ---
Summary: <WorkerResult.Summary>
Full output: <WorkerResult.OutputFile>

--- Worker 2 (failed, <DurationMS>ms) ---
Error: <ErrorCode> — <ErrorMsg>
Partial artifacts: <WorkerResult.ArtifactDir>
```

**Fan-out + full_concat:**
```
=== Output from step "<StepName>" (<Succeeded> succeeded, <Failed> failed, <TotalMS>ms) ===

--- Worker 0 (completed) ---
<full verbatim content of worker 0 OutputFile>

--- Worker 1 (completed) ---
<full verbatim content of worker 1 OutputFile>

--- Worker 2 (failed) ---
Error: <ErrorCode> — <ErrorMsg>
Partial artifacts: <WorkerResult.ArtifactDir>
```

**Fan-out + refs_only:**
```
=== Output from step "<StepName>" (<Succeeded> succeeded, <Failed> failed, <TotalMS>ms) ===

--- Worker 0 (completed) ---
Full output: <WorkerResult.OutputFile>

--- Worker 1 (completed) ---
Full output: <WorkerResult.OutputFile>

--- Worker 2 (failed) ---
Partial artifacts: <WorkerResult.ArtifactDir>
```

---

## 12. Distribution & Migration

### 12.1 Go Binary Distribution

- **GoReleaser**: cross-compile for darwin-arm64, darwin-amd64, linux-amd64, linux-arm64
- **GitHub Releases**: binary + checksums on each tagged release
- **Homebrew tap**: `brew install <tap>/agent-mux`
- **`go install`**: `go install github.com/<org>/agent-mux/cmd/agent-mux@latest`

### 12.2 TS Shim (Option B)

The existing npm package becomes a thin wrapper:

```typescript
// src/agent.ts (v2 shim)
import { spawnSync } from "child_process";

const result = spawnSync("agent-mux", args, {
  shell: false,
  stdio: ["pipe", "pipe", "inherit"], // stderr passes through for events
  encoding: "utf-8",
});

const output = JSON.parse(result.stdout);
process.stdout.write(JSON.stringify(output));
```

- `bun x agent-mux` still works
- Requires Go binary on PATH (shim checks + prints install instructions if missing)
- npm version bumped to 3.0.0 (breaking, clearly signals the change)
- README documents both installation paths

### 12.3 Repo Structure

Same repo. Go code at root. TS shim in `shim/` directory.

```
cmd/agent-mux/main.go
internal/
  engine/
    loop.go
    adapter/
      claude.go
      codex.go
      gemini.go
    registry.go
  config/
  dispatch/
  event/
  inbox/
  hooks/
  liveness/
  recovery/
  supervisor/
  types/
go.mod
go.sum
agents/                  # bundled agent templates
  gsd-coordinator.md
  gsd-coordinator.toml
  explorer.md
  explorer.toml
  heavy-lifter.md
  heavy-lifter.toml
  reviewer.md
  reviewer.toml
  writer.md
  writer.toml
  scout.md
  scout.toml
shim/                    # TS npm shim
  src/agent.ts
  package.json
  tsconfig.json
test/
  fixtures/              # mock harness scripts
config.example.toml
SPEC-V2.md              # this document
README.md               # updated for v2
CHANGELOG.md
LICENSE
```

Current TS source moves to `_archive/v1/` for reference. Not deleted — preserved for anyone who needs to trace history.

---

## 13. Bundled Agents

Bundled agents are a fat rich default config shipped alongside the binary, not compiled into it. They live in `~/.config/agent-mux/agents/` (installed by setup) or `<repo>/agents/` (in the source tree). Not embedded in the Go binary.

### 13.1 Agent Templates

Agent-mux ships with pre-configured agent templates — `.md` persona files with companion `.toml` config. Users get working multi-agent pipelines out of the box.

```
agents/
  gsd-coordinator.md           # GSD swarm coordinator (reference implementation)
  gsd-coordinator.toml         # roles + pipeline for GSD workflows
  explorer.md                  # research/discovery — web search, code exploration
  explorer.toml
  heavy-lifter.md              # implementation — code writing, file editing, building
  heavy-lifter.toml
  reviewer.md                  # code review, quality audit, cross-provider verification
  reviewer.toml
  writer.md                    # writing/documentation — blog posts, READMEs, specs
  writer.toml
  scout.md                     # lightweight reconnaissance — quick checks, small reads
  scout.toml
```

### 13.2 Template Anatomy

Each agent template is a pair. `.toml` holds full definitions (role details, pipeline steps, timeouts, model lists). `.md` holds persona/system prompt text plus scalar dispatch defaults in frontmatter (`skills`, `engine`, `model`, `effort`, `timeout`). Roles and pipelines live in the companion `.toml`, which is merged into config when the coordinator loads.

**`agents/explorer.md`** — persona/prompt (loaded by `--coordinator`):
```markdown
---
skills: [web-search, pratchett-read]
model: gpt-5.4-mini
effort: medium
---

You are a research agent. Your job is to find information, not to build things.

Rules:
- Search broadly first, then narrow
- Return findings as structured bullet points
- Include source URLs for every claim
- If a search returns nothing useful, say so — don't hallucinate
- Write artifacts to the artifact directory if output exceeds 200 lines
```

**`agents/explorer.toml`** — companion config (merged with global config when this agent is active):
```toml
[defaults]
engine = "codex"
model = "gpt-5.4-mini"
effort = "medium"

[liveness]
heartbeat_interval_sec = 15     # default heartbeat interval
silence_warn_seconds = 120      # research can be slow — generous threshold
silence_kill_seconds = 240

[timeout]
medium = 900                    # research gets 15 min, not 10
```

### 13.3 Usage

```bash
# Single dispatch with bundled agent persona
agent-mux --coordinator explorer "find all Rust HTTP frameworks with >1k stars"

# Pipeline using bundled agents at each step
agent-mux --pipeline plan-execute-review "add pagination to the API"

# GSD coordinator (the reference multi-agent workflow)
agent-mux --coordinator gsd-coordinator "build the satellite data pipeline"
```

### 13.4 Customization

Bundled agents are defaults, not constraints:
- Users copy an agent template to their project's `.claude/agents/` and modify it
- Companion agent `.toml` outranks project `.agent-mux.toml` in the precedence chain. A bundled agent installed at `~/.config/agent-mux/agents/` ships with defaults that work out of the box. The project config can override global config, but the companion `.toml` (loaded when that agent is active via `--coordinator`) takes precedence over project config. This ensures the agent author's tested configuration is not silently overridden by generic project settings.
- CLI flags override everything
- `--coordinator` searches: project agents first → bundled agents as fallback

### 13.5 GSD Coordinator (Reference Implementation)

The GSD coordinator is the most complex bundled agent. It demonstrates:
- Fan-out dispatch (multiple workers in parallel)
- Role-based worker assignment
- Partial failure handling (proceed with N-1 results)
- Synthesis from gathered artifacts
- The full pipeline lifecycle

Its `.toml` defines a `research-gather` pipeline and custom roles. Its `.md` encodes the GSD prompting strategy. Together they replace the current 10KB `get-shit-done-agent.md` reference doc with executable config.

---

## 14. Testing Strategy

### 14.1 Agent Testing as First-Class

The error response from a tool is a gold mine for steering and nudging agent behavior. Testing must verify that errors are agent-parseable and actionable.

**Error response tests:**
- Every error code in the catalog (§5.4) has a test that verifies:
  - The `suggestion` field is present and contains actionable guidance
  - The `retryable` field is accurate
  - Fuzzy matching returns correct suggestions (Levenshtein distance ≤ 2)
- "Can an LLM self-correct from this error?" is the acceptance criterion

**Integration tests (per harness):**
- Spawn real harness process with a trivial prompt
- Verify output shape matches contract
- Verify event stream includes `dispatch_start`, `heartbeat`, `dispatch_end`
- Verify harness event stream is parsed correctly by adapter
- Verify artifact dir contains `_dispatch_meta.json`

**Timeout/recovery tests:**
- Dispatch with 5s timeout, prompt that takes 10s
- Verify `status: "timed_out"`, `recoverable: true`
- Verify artifacts preserved
- Run `--recover` and verify continuation works

**Liveness tests:**
- Mock a hanging harness (no event stream output)
- Verify `frozen_warning` event at silence_warn threshold
- Verify `frozen_tool_call` error at silence_kill threshold
- Verify artifacts preserved after kill

**Pipeline tests:**
- Define a 2-step pipeline in test config
- Verify step 2 receives step 1's output
- Verify pipeline_id shared across steps
- Verify partial failure when step 2 fails (step 1 artifacts preserved)

**Fuzzy matching tests:**
- `gpt-5.3-codex-spark` → suggests `gpt-5.4-mini` or `gpt-5.3-codex-spark` (if in valid list)
- `claudee-opus` → suggests `claude-opus-4-6`
- Empty model → suggests engine default

**Precedence resolution tests:**
- Verify the 8-level config chain (§4.4) with conflicting values at each level
- Test `--config` override behavior (values override all sources except explicit CLI flags)
- Test `--role` + explicit `--model` interaction (CLI flag wins)

**Resume/inbox tests per adapter:**
- Test `--resume` for Claude, Codex, and Gemini
- Verify session ID extraction from event stream
- Verify inbox message delivery via resume mechanism

**--stdin conflict tests:**
- Verify dispatch flags are ignored when `--stdin` is provided
- Verify runtime flags are preserved
- Verify warning emitted when dispatch flags coexist with `--stdin`

**--signal concurrency tests:**
- Multiple concurrent `--signal` writes to the same dispatch
- Verify no message loss or corruption in `inbox.md`

**Liveness vs timeout tests:**
- Verify first-terminal-wins behavior: if liveness kills before timeout, status is `failed` (not `timed_out`)
- If timeout fires before liveness, status is `timed_out` (not `failed`)
- Grace period: verify heartbeats continue, liveness watchdog still active

**Event log completeness tests:**
- Verify `events.jsonl` mirrors stderr events
- Verify per-line flush (events survive process death)

**Response truncation tests:**
- Test `response_max_chars=0` (no truncation)
- Test normal truncation (response exceeds cap)
- Verify `full_output.md` created when truncation occurs
- Verify `response_truncated` field accuracy

**Hook behavior tests:**
- Pre-dispatch deny: dispatch rejected before harness starts
- Event-level deny: harness killed on pattern match
- Warn injection: warning event emitted, dispatch continues
- Case-insensitive substring matching verified

### 14.2 Test Fixtures

Harness mocks as shell scripts that emit structured event streams:
```bash
#!/bin/bash
# test/fixtures/mock-harness.sh — simulates a harness event stream
echo '{"type":"system","subtype":"init","session_id":"test-001"}'
echo '{"type":"assistant","subtype":"tool_use","name":"Read","input":{"file_path":"src/main.go"}}'
echo '{"type":"assistant","subtype":"tool_result","name":"Read","output":"..."}'
echo '{"type":"result","subtype":"success","result":"mock done","cost":{"input_tokens":100,"output_tokens":50}}'
```

Configurable: exit code, delay, event sequence, stream format (per-harness). Tests don't need real API keys — the harness binary is mocked.

**Adapter ParseEvent tests must include:**
- Valid events (happy path per harness — Claude, Codex, Gemini)
- Malformed JSON (truncated, invalid UTF-8, empty lines)
- Unknown event types (new harness version adding events we don't know)
- Missing required fields (e.g., `tool_use` without `tool_name`)
- Non-JSON lines before JSON stream (Gemini keychain warnings, Python deprecation notices)

All malformed/unknown inputs must produce `EventRawPassthrough`, never panic or return an error that stops the LoopEngine.

---

## 15. ax-eval: Behavioral Convergence Testing

### 15.1 Philosophy

Agent-mux's consumers are LLMs. Every `DispatchResult`, every error code, every stderr event, every default value is a training signal for the calling agent. Testing correctness is necessary but insufficient — the primary quality metric is **behavioral variance across independent agents**.

Core principles:

- **Error messages are the only reliable teaching channel.** Documentation, examples, and skill injection help — but when an agent hits a wall, the error response is the sole feedback loop. If the error doesn't teach, the agent is blind.
- **Behavioral variance is the quality metric.** Not "does it work?" but "do N agents converge on the same behavior?" High variance means the interface is ambiguous. Low variance means the environment (errors, defaults, output format) is doing the teaching.
- **Testing the environment is as important as testing correctness.** The defaults are temperature control — they bound the space of reasonable first attempts. The error messages are the reward signal — they steer recovery. The output format is the observation space — it determines what the agent learns from success.
- **Agent-mux's consumers ARE LLMs.** Every interface surface — CLI flags, JSON output, error suggestions, event stream — is consumed by a model, not a human. Design for parseability, not readability.
- **Reference:** noninteractive.org/blog/agent-experience — 13 rounds, 130 agents, 78% call reduction, 9x variance reduction from environment-only changes. The methodology: spawn agents cold, measure convergence, fix the tool (not the agents), re-measure.

### 15.2 ax-eval Protocol

The core evaluation loop:

1. **Define a scenario** — a task that requires using agent-mux (see §15.3).
2. **Spawn N agents** (default: 5 mini + 5 high = 10 total) via agent-mux itself (dogfooding).
3. **Cold start** — each agent gets an identical minimal prompt. NO documentation, NO examples, NO skill injection. The agent must discover the CLI from `--help`, error messages, and trial-and-error.
4. **Measure** — tool calls to success, variance (max/min ratio), first-attempt flags, error recovery rate, where agents get stuck (see §15.4).
5. **Analyze transcripts** — what patterns emerge? What flags did agents invent? Where did confusion escalate? What error messages were unhelpful?
6. **Fix the environment** (error messages, defaults, output format) — NOT the agents. Never patch the prompt. Never add documentation to the eval. The interface must teach.
7. **Re-run. Compare metrics.** Each round should show convergence improvement.

**Cadence:** Not CI — too expensive (10 agents × 5 scenarios = 50 dispatches per round). Run before major releases and after significant CLI/error changes. The eval is itself dispatched through agent-mux (meta-testing).

**Two model tiers:**
- **Codex gpt-5.4 mini** (`--effort medium`) — tests the learnability floor. If a lighter model can't learn the interface from errors alone, the interface is broken.
- **Codex gpt-5.4 high** (`--effort high`) — tests the convergence ceiling. If a strong model doesn't converge tightly, the error messages are broken.

Both tiers are high-capability models. The gap between them reveals whether the interface rewards capability or punishes exploration.

### 15.3 Scenarios

Five scenarios that test different interface surfaces:

**Scenario 1: Cold dispatch**
> "Use agent-mux to dispatch a Codex worker that creates a Go function to reverse a string."

Tests: basic CLI discovery, `--help` quality, flag syntax (`--engine`, `--model`), engine/model selection, output parsing (JSON on stdout). This is the baseline — if agents can't do a single dispatch cold, nothing else matters.

**Scenario 2: Pipeline chain**
> "Use agent-mux to run a 2-step pipeline: step 1 writes code, step 2 tests it."

Tests: pipeline config discovery (`--pipeline`), step chaining, `receives`/`pass_output_as` semantics, result parsing across steps. Requires the agent to either find an existing pipeline in config or construct one.

**Scenario 3: Error recovery**
> "Use agent-mux to dispatch a worker with an invalid model name, then fix the error and retry."

Tests: error message quality (does `model_not_found` suggestion lead to self-correction?), suggestion actionability (does the agent use the suggested model?), self-correction speed (one attempt or many?). The purest test of the error-as-teaching principle.

**Scenario 4: Timeout handling**
> "Use agent-mux to dispatch a worker with a 10s timeout on a task that takes 30s, then recover the partial work."

Tests: timeout behavior understanding, `--timeout` flag discovery, `timed_out` status parsing, `recoverable: true` interpretation, `--recover` flow, artifact preservation. Tests the full timeout → recovery lifecycle.

**Scenario 5: Multi-engine fan-out**
> "Use agent-mux to dispatch 3 parallel workers (one Claude, one Codex, one Gemini) to research a topic, then synthesize results."

Tests: multi-engine dispatch (`--engine` variation), fan-out (pipeline `parallel` or manual dispatch), result collection across engines, handoff composition. The most complex scenario — tests whether agents can compose agent-mux primitives.

### 15.4 Metrics

Six metrics collected per scenario per round:

```
calls_to_success    int     Total agent-mux invocations before task completion
call_range          float   max/min ratio across N agents (1.0 = perfect convergence)
first_attempt_flags []str   What CLI flags each agent tries on first invocation
error_recovery_rate float   Fraction of errors self-corrected in one attempt
escalation_events   int     Count of destructive actions (kill, rm, reset)
time_to_success     int     Wall clock seconds to task completion
```

**Target thresholds** (inspired by noninteractive.org final round metrics):

```
calls_to_success:    ≤ 5    (dispatch is a single-command tool, not a wizard)
call_range:          ≤ 1.5x (agents should converge tightly)
first_attempt_flags: >60%   match actual flag names
error_recovery_rate: 100%   for all 15 error codes (§5.4)
escalation_events:   0      (no destructive behavior)
```

**Interpretation guide:**
- `calls_to_success > 5` → the happy path is too hard to discover. Fix defaults or `--help`.
- `call_range > 2.0x` → the interface is ambiguous. Some agents find the right path fast, others flail. Fix error messages to steer faster.
- `first_attempt_flags < 40%` match → flag names are unintuitive. Consider aliases or rename.
- `error_recovery_rate < 100%` for any error code → that error message is broken. Rewrite it.
- `escalation_events > 0` → the error path drove an agent to destructive behavior. Critical failure.

### 15.5 Error-as-Teaching Tests (CI-compatible)

These run in CI, not ax-eval rounds. Lightweight and deterministic.

For each of the 15 error codes in §5.4:

1. **Trigger** the error via agent-mux CLI (e.g., `agent-mux --engine codex --model nonexistent-model "test"` for `model_not_found`).
2. **Capture** the JSON error response (stdout).
3. **Feed** the error JSON to a Codex gpt-5.4 mini worker with the prompt: `"You ran agent-mux and got this error. What command would you run next to fix it?"`
4. **Assert** the corrected command would succeed — parse the agent's suggested command, validate flags and args against the config schema and model registry.

This tests the reward signal quality in isolation. If a mini-tier model can't self-correct from the error in one shot, the error message is broken. Binary pass/fail per error code.

**CI integration:**
- Run on every release (not every commit — requires API calls).
- Each error code test is independent — can run in parallel.
- Total cost: ~15 mini-tier dispatches per run (~$0.50).
- Failure blocks release. An error message that doesn't teach is a shipping defect.

### 15.6 First-Attempt Instrumentation

Agent-mux should log first-attempt data to `events.jsonl` (§10.3) for every dispatch:

- **First invocation flags and args** — what the calling process tried before any error correction.
- **Flag names that were attempted but don't exist** — captured from `invalid_args` error events. These are the purest signal about expected API shape.
- **The correction path** — error → retry → success (or error → error → ...). The full sequence of attempts before the task succeeds.

**Event format:**

```jsonl
{"schema_version":1,"type":"first_attempt","flags":["--engine","codex","--model","gpt5.4","--timeout","30"],"unknown_flags":["--name","--verbose-output"],"ts":"..."}
{"schema_version":1,"type":"error_correction","attempt":1,"error_code":"model_not_found","retry_flags":["--model","gpt-5.4"],"ts":"..."}
{"schema_version":1,"type":"error_correction","attempt":2,"error_code":null,"success":true,"ts":"..."}
```

**Design feedback loop:** The article's principle — "If 10/10 agents try `--name`, build `--name`." First attempts are the purest signal about expected API shape. Review first-attempt logs after each ax-eval round. If a flag name appears in `unknown_flags` across >50% of agents, add it as an alias.

### 15.7 Running ax-eval

Concrete invocation pattern — agent-mux dispatching agents that use agent-mux (intentional dogfooding):

```bash
# Dispatch 10 agents for scenario 1 (cold dispatch)
# Mini tier — tests learnability floor
for i in $(seq 1 5); do
  agent-mux --engine codex --model gpt-5.4-mini --effort medium \
    --artifact-dir /tmp/ax-eval/round-N/mini-$i \
    "Use agent-mux to dispatch a Codex worker that creates a Go function to reverse a string. Do not read any documentation — figure out the CLI from the tool itself."
done

# High tier — tests convergence ceiling
for i in $(seq 1 5); do
  agent-mux --engine codex --model gpt-5.4 --effort high \
    --artifact-dir /tmp/ax-eval/round-N/high-$i \
    "Use agent-mux to dispatch a Codex worker that creates a Go function to reverse a string. Do not read any documentation — figure out the CLI from the tool itself."
done

# Collect transcripts and compute metrics
# Post-processing script reads artifact dirs, extracts:
#   - tool call counts from events.jsonl
#   - first_attempt flags from first_attempt events
#   - error codes and recovery paths from error_correction events
#   - escalation events (grep for rm, kill, reset in command_run events)
```

**Meta-testing property:** The outer dispatch tests agent-mux's infrastructure reliability (process supervision, timeouts, artifact collection). The inner dispatch (what the spawned agent does) tests agent-mux's interface learnability. Both layers produce useful signal.

**Round workflow:**
1. Run all 5 scenarios × 10 agents = 50 dispatches.
2. Collect metrics into a round report at `/tmp/ax-eval/round-N/report.json`.
3. Identify worst-performing scenario and error code.
4. Fix the environment (error message, default, output format).
5. Re-run only the affected scenario to verify improvement.
6. Full re-run before release to confirm no regressions.

---

## 16. What's Explicitly Out of Scope

- **Orchestration brain.** Agent-mux is a tool. The LLM decides. Orchestration layer is a separate project.
- **Harness reimplementation.** Agent-mux does NOT call LLM APIs, execute tools, or manage conversation context. The harnesses (Claude Code, Codex, Gemini) do all that. Agent-mux reads their event streams and interacts at boundaries.
- **Budget enforcement at runtime.** Token/cost fields exist in DispatchResult metadata (`tokens`, `cost_usd`) for reporting only. No runtime budget ceilings, no lineage budgets, no token-limit kills, no cost-limit kills. Wall-time timeout is the only hard limit in v2. Specifically out of scope: `budget_exceeded` errors, per-dispatch token ceilings, parent-to-child budget propagation, and any budget-based kill signal.
- **Automatic engine/model selection.** Caller picks via `--engine`, `--role`, or config defaults. No AI-in-the-loop choosing engines.
- **TG bot integration.** Separate concern. Agent-mux returns JSON; the bot formats it.
- **Ticket/kanban layer.** Orchestration concern, not dispatch concern.
- **MCP passthrough.** Replaced by CLI wrappers in skills. Agent-mux has zero MCP awareness.

---

## Appendix A: Migration Checklist (v1 → v2)

| v1 Feature | v2 Status | Notes |
|------------|-----------|-------|
| `--engine codex/claude` | **Keep** + add gemini (drop opencode, forge deferred) | Same flag |
| JSON output on stdout | **Redesigned** | Single `status` field, `error` object, `artifacts[]` |
| `success: true/false` | **Cut** | Replaced by `status` enum |
| `timed_out` + `completed` fields | **Cut** | `status: "timed_out"` is unambiguous |
| Heartbeat on stderr (text) | **Replaced** | NDJSON event stream |
| `--skill` injection | **Keep** | Same mechanism |
| `--coordinator` mode | **Enhanced** | Now holds roles, pipelines, model defaults |
| `--effort` tiers | **Keep** | Soft timeout + grace instead of hard kill |
| `--mcp-cluster` | **Cut** | MCP tools wrapped as CLI in skills |
| `--browser` | **Cut** | Browser skill provides CLI commands |
| `--sandbox` (default workspace-write) | **Changed** | Default danger-full-access, restrict via config |
| `--permission-mode` | **Changed** | Default bypassPermissions, restrict via config |
| `--full` flag | **Changed** | On by default. `--no-full` to restrict. |
| Activity tracking | **Enhanced** | `tool_calls` added, real-time via events |
| SDK dependencies (3 packages) | **Cut** | LoopEngine reads harness event streams; no SDK or API client dependencies |
| `dist/agent.js` | **Cut** | Dead artifact |
| `--network` flag | **Cut** | Always on |
| npm package | **Shim** | Thin wrapper calling Go binary |

## Appendix B: Research Corpus

This spec was informed by:
- 146+ session handoffs scanned via `gaal search` (§1)
- 150KB research corpus from session b6084fb3 (2026-03-24)
- ForgeCode source analysis (antinomyhq/forgecode)
- 70+ Reddit/HN/GitHub sources on agent pain points
- MAST paper (1,642 traces, 41-86.7% failure rates)
- Stanford CooperBench (cooperative parallel = 30-50% worse)
- 8 discovery reports + 2 synthesis documents

All source files at `centerpiece/_workbench/2026-03-24-*.md`.

## Appendix C: Harness Event Schemas

Each `HarnessAdapter.ParseEvent()` implementation parses the raw event stream from its harness binary and maps events to the unified `HarnessEvent` / `EventKind` types defined in §6.1.1. This appendix documents the raw event formats, JSON examples, and mapping contracts for each supported harness.

### C.1 Claude Code

**Invocation:**
```
claude -p --output-format stream-json --verbose "<prompt>"
```

Additional flags as needed: `--model`, `--max-turns`, `--system-prompt`, `--resume`, `--continue`.

**Event types:**

Claude Code emits JSON lines on stdout. Each line has a `type` field and a `subtype` field.

| Type | Subtype | Description |
|------|---------|-------------|
| `system` | `init` | Session initialization. Carries `session_id`, model, tools list. |
| `assistant` | `text` | Content block: partial text streaming from the model. |
| `assistant` | `tool_use` | Content block: model invokes a tool. Carries `name`, `input`. |
| `assistant` | `tool_result` | Content block: tool execution result. Carries `name`, `output`, `is_error`. |
| `result` | `success` | Final result. Carries `result` (response text), `cost` (token usage), `session_id`. |
| `result` | `error` | Terminal error from Claude Code. Carries `error` message. |

**JSON examples:**

System init:
```json
{
  "type": "system",
  "subtype": "init",
  "session_id": "abc123-def456",
  "model": "claude-opus-4-6",
  "tools": ["Read", "Edit", "Write", "Bash", "Glob", "Grep"],
  "cwd": "/path/to/project"
}
```

Assistant text:
```json
{
  "type": "assistant",
  "subtype": "text",
  "text": "I'll start by reading the main configuration file."
}
```

Tool use:
```json
{
  "type": "assistant",
  "subtype": "tool_use",
  "id": "tool_01ABC",
  "name": "Read",
  "input": {
    "file_path": "/path/to/project/src/main.go"
  }
}
```

Tool result:
```json
{
  "type": "assistant",
  "subtype": "tool_result",
  "id": "tool_01ABC",
  "name": "Read",
  "output": "package main\n\nimport (\n\t\"fmt\"\n)\n...",
  "is_error": false
}
```

Result success:
```json
{
  "type": "result",
  "subtype": "success",
  "result": "Built the parser. 3 files modified, all tests pass.",
  "session_id": "abc123-def456",
  "cost": {
    "input_tokens": 45000,
    "output_tokens": 8200,
    "cache_read_tokens": 12000,
    "cache_write_tokens": 3000
  },
  "duration_ms": 45200,
  "turns": 12
}
```

Result error:
```json
{
  "type": "result",
  "subtype": "error",
  "error": "Model not found: claude-opus-99",
  "session_id": "abc123-def456"
}
```

**Mapping table (Claude event -> EventKind):**

| Claude Event | Field/Condition | EventKind |
|-------------|-----------------|-----------|
| `system` / `init` | — | `EventSessionStart` (extract `session_id`) |
| `assistant` / `text` | — | `EventProgress` (extract `text`) |
| `assistant` / `tool_use` | `name` = "Read" | `EventToolStart` + `EventFileRead` (extract `input.file_path`) |
| `assistant` / `tool_use` | `name` = "Edit" or "Write" | `EventToolStart` (extract `input.file_path`, file write confirmed on tool_result) |
| `assistant` / `tool_use` | `name` = "Bash" | `EventToolStart` + `EventCommandRun` (extract `input.command`) |
| `assistant` / `tool_use` | `name` = "Glob" or "Grep" | `EventToolStart` |
| `assistant` / `tool_result` | `name` = "Edit" or "Write", `is_error` = false | `EventToolEnd` + `EventFileWrite` (extract file path from input) |
| `assistant` / `tool_result` | any other | `EventToolEnd` |
| `result` / `success` | — | `EventResponse` (extract `result`) + `EventTurnComplete` (extract `cost` -> `TokenUsage`) |
| `result` / `error` | — | `EventError` (extract `error`) + `EventTurnFailed` |
| unrecognized line | — | `EventRawPassthrough` |

**Adapter contract:**

| Aspect | Value |
|--------|-------|
| Completion signal | `result` event (type = "result") |
| Final response text | `result.result` field |
| Session ID | `system.session_id` (init event) |
| Token usage | `result.cost` -> mapped to `TokenUsage{Input: input_tokens, Output: output_tokens}` |
| Malformed line handling | Log as `EventRawPassthrough`, do not error. Claude Code may emit non-JSON lines in verbose mode. |

### C.2 Codex

**Invocation:**
```
codex exec --json "<prompt>"
```

Additional flags as needed: `--model`, `--sandbox`, `--reasoning`, `--add-dir`.

**Event types:**

Codex emits JSON lines on stdout when invoked with `--json`. Each line has a `type` field.

| Type | Description |
|------|-------------|
| `thread.started` | Session initialization. Carries `thread_id`. |
| `turn.started` | New conversation turn begins. |
| `item.started` | An item (tool call, message, reasoning) begins. Carries `item_type`, `item_id`. |
| `item.updated` | Incremental update to an in-progress item. Carries `item_id`, partial content. |
| `item.completed` | An item finishes. Carries full item content, `item_type`, `item_id`. |
| `turn.completed` | Turn finishes. Carries `usage` (token counts). |
| `turn.failed` | Turn failed. Carries `error`. |
| `error` | Top-level error. Carries `code`, `message`. |

**Item type catalog:**

| Item Type | Description | EventKind Mapping |
|-----------|-------------|-------------------|
| `agent_message` | Model's text response | `EventProgress` (streaming) / `EventResponse` (final) |
| `reasoning` | Model's internal reasoning trace | `EventProgress` |
| `command_execution` | Shell command execution | `EventCommandRun` + `EventToolStart`/`EventToolEnd` |
| `file_change` | File modification | `EventFileWrite` |
| `mcp_tool_call` | MCP tool invocation | `EventToolStart`/`EventToolEnd` |
| `collab_tool_call` | Collaborative tool call | `EventToolStart`/`EventToolEnd` |
| `web_search` | Web search operation | `EventToolStart`/`EventToolEnd` |
| `todo_list` | Internal task tracking | `EventProgress` |
| `error` | Item-level error | `EventError` |

**JSON examples:**

Thread started:
```json
{
  "type": "thread.started",
  "thread_id": "thread_abc123",
  "model": "gpt-5.4",
  "created_at": "2026-03-25T10:00:00Z"
}
```

Turn started:
```json
{
  "type": "turn.started",
  "turn_index": 0
}
```

Item started (command execution):
```json
{
  "type": "item.started",
  "item_id": "item_001",
  "item_type": "command_execution",
  "command": "go test ./..."
}
```

Item updated (streaming agent message):
```json
{
  "type": "item.updated",
  "item_id": "item_002",
  "item_type": "agent_message",
  "content_delta": "I'll run the tests to verify"
}
```

Item completed (agent message):
```json
{
  "type": "item.completed",
  "item_id": "item_002",
  "item_type": "agent_message",
  "content": "I'll run the tests to verify the parser handles edge cases correctly.",
  "duration_ms": 2300
}
```

Item completed (command execution):
```json
{
  "type": "item.completed",
  "item_id": "item_001",
  "item_type": "command_execution",
  "command": "go test ./...",
  "exit_code": 0,
  "stdout": "ok  \tgithub.com/org/repo/internal/parser\t0.340s\n",
  "stderr": "",
  "duration_ms": 1200
}
```

Item completed (file change):
```json
{
  "type": "item.completed",
  "item_id": "item_003",
  "item_type": "file_change",
  "file_path": "internal/parser/parser.go",
  "change_type": "modified",
  "duration_ms": 150
}
```

Turn completed:
```json
{
  "type": "turn.completed",
  "turn_index": 0,
  "usage": {
    "input_tokens": 23000,
    "output_tokens": 4500,
    "reasoning_tokens": 1200
  },
  "duration_ms": 34000
}
```

Turn failed:
```json
{
  "type": "turn.failed",
  "turn_index": 0,
  "error": {
    "code": "context_length_exceeded",
    "message": "Conversation exceeded maximum context length."
  }
}
```

Top-level error:
```json
{
  "type": "error",
  "code": "model_not_found",
  "message": "Model 'gpt-99' is not available."
}
```

**Mapping table (Codex event -> EventKind):**

| Codex Event | Field/Condition | EventKind |
|------------|-----------------|-----------|
| `thread.started` | — | `EventSessionStart` (extract `thread_id`) |
| `turn.started` | — | (no direct mapping — internal bookkeeping) |
| `item.started` | `item_type` = "command_execution" | `EventToolStart` + `EventCommandRun` (extract `command`) |
| `item.started` | `item_type` = "file_change" | `EventToolStart` |
| `item.started` | `item_type` = "agent_message" | (no mapping — wait for completed) |
| `item.started` | `item_type` = "web_search" / "mcp_tool_call" / "collab_tool_call" | `EventToolStart` |
| `item.updated` | `item_type` = "agent_message" | `EventProgress` (extract `content_delta`) |
| `item.completed` | `item_type` = "agent_message" | `EventResponse` (extract `content`) |
| `item.completed` | `item_type` = "command_execution" | `EventToolEnd` + `EventCommandRun` |
| `item.completed` | `item_type` = "file_change" | `EventToolEnd` + `EventFileWrite` (extract `file_path`) |
| `item.completed` | `item_type` = "reasoning" | `EventProgress` |
| `item.completed` | other item types | `EventToolEnd` |
| `turn.completed` | — | `EventTurnComplete` (extract `usage` -> `TokenUsage`) |
| `turn.failed` | — | `EventTurnFailed` + `EventError` (extract `error`) |
| `error` | — | `EventError` (extract `code`, `message`) |
| unrecognized line | — | `EventRawPassthrough` |

**Adapter contract:**

| Aspect | Value |
|--------|-------|
| Completion signal | `turn.completed` or `turn.failed` event |
| Final response text | Last `item.completed` where `item_type` = "agent_message" — extract `content` |
| Session ID | `thread.started.thread_id` |
| Token usage | `turn.completed.usage` -> mapped to `TokenUsage{Input: input_tokens, Output: output_tokens, Reasoning: reasoning_tokens}` |
| Malformed line handling | Log as `EventRawPassthrough`. Codex `--json` mode should not emit non-JSON lines. |
| Resume message injection | The exact CLI mechanism for injecting a message during resume needs implementation-time discovery. Known options: (a) pipe the message via stdin to `codex exec resume --id <id>`, (b) a `--message` flag if supported. The CodexAdapter.ResumeArgs() implementation must verify the actual mechanism against the installed Codex CLI version. If neither stdin nor flag works, Codex InboxMode degrades to InboxNone for that version. |

### C.3 Gemini

**Invocation:**
```
gemini -p -o stream-json "<prompt>"
```

Additional flags as needed: `-m <model>`, `--approval-mode`, `--resume`. System prompt via `$GEMINI_SYSTEM_MD` env var (path to .md file).

**Event types:**

Gemini CLI emits JSON lines on stdout when invoked with `-o stream-json`. Each line has a `type` field.

| Type | Description |
|------|-------------|
| `init` | Session initialization. Carries `session_id`, model info. |
| `message` | Model text output. Carries `role`, `content`. |
| `tool_use` | Model invokes a tool. Carries `tool_id`, `name`, `input`. |
| `tool_result` | Tool execution result. Carries `tool_id`, `name`, `output`, `is_error`. |
| `error` | Error event. Carries `code`, `message`. |
| `result` | Final result. Carries stats (tokens, duration), session summary. |

**Note on non-JSON lines:** Gemini CLI may emit non-JSON lines before the JSON stream begins. Common sources: macOS keychain access warnings (`security: SecKeychainSearchCopyNext`), Python deprecation notices, gcloud auth messages. The adapter must skip non-JSON lines gracefully without erroring.

**JSON examples:**

Init:
```json
{
  "type": "init",
  "session_id": "gem-session-789xyz",
  "model": "gemini-2.5-pro",
  "tools": ["shell", "read_file", "write_file", "glob", "grep"],
  "cwd": "/path/to/project"
}
```

Message (assistant):
```json
{
  "type": "message",
  "role": "assistant",
  "content": "I'll analyze the project structure first.",
  "ts": "2026-03-25T10:00:05Z"
}
```

Tool use:
```json
{
  "type": "tool_use",
  "tool_id": "call_001",
  "name": "read_file",
  "input": {
    "path": "src/main.go"
  },
  "ts": "2026-03-25T10:00:06Z"
}
```

Tool result:
```json
{
  "type": "tool_result",
  "tool_id": "call_001",
  "name": "read_file",
  "output": "package main\n\nimport (\n\t\"fmt\"\n)\n...",
  "is_error": false,
  "duration_ms": 45,
  "ts": "2026-03-25T10:00:06Z"
}
```

Tool use (shell):
```json
{
  "type": "tool_use",
  "tool_id": "call_002",
  "name": "shell",
  "input": {
    "command": "go test ./..."
  },
  "ts": "2026-03-25T10:00:10Z"
}
```

Tool result (shell):
```json
{
  "type": "tool_result",
  "tool_id": "call_002",
  "name": "shell",
  "output": "ok  \tgithub.com/org/repo/...\t0.340s\n",
  "exit_code": 0,
  "is_error": false,
  "duration_ms": 1200,
  "ts": "2026-03-25T10:00:11Z"
}
```

Error:
```json
{
  "type": "error",
  "code": "auth_failed",
  "message": "Google Cloud authentication failed. Run `gcloud auth login`.",
  "ts": "2026-03-25T10:00:01Z"
}
```

Result:
```json
{
  "type": "result",
  "session_id": "gem-session-789xyz",
  "result": "Parser built and tested. 3 files modified.",
  "stats": {
    "total_tokens": 52000,
    "input_tokens": 38000,
    "output_tokens": 14000,
    "duration_ms": 62000,
    "tool_calls": 8,
    "turns": 6
  },
  "ts": "2026-03-25T10:01:02Z"
}
```

**Mapping table (Gemini event -> EventKind):**

| Gemini Event | Field/Condition | EventKind |
|-------------|-----------------|-----------|
| `init` | — | `EventSessionStart` (extract `session_id`) |
| `message` | `role` = "assistant" | `EventProgress` (extract `content`) |
| `tool_use` | `name` = "read_file" | `EventToolStart` + `EventFileRead` (extract `input.path`) |
| `tool_use` | `name` = "write_file" | `EventToolStart` (file write confirmed on tool_result) |
| `tool_use` | `name` = "shell" | `EventToolStart` + `EventCommandRun` (extract `input.command`) |
| `tool_use` | other tools | `EventToolStart` (extract `name`) |
| `tool_result` | `name` = "write_file", `is_error` = false | `EventToolEnd` + `EventFileWrite` (extract file path from corresponding tool_use input) |
| `tool_result` | `name` = "shell" | `EventToolEnd` + `EventCommandRun` |
| `tool_result` | `is_error` = true | `EventToolEnd` + `EventError` |
| `tool_result` | other | `EventToolEnd` |
| `error` | — | `EventError` (extract `code`, `message`) |
| `result` | — | `EventResponse` (extract `result`) + `EventTurnComplete` (extract `stats` -> `TokenUsage`) |
| non-JSON line | — | `EventRawPassthrough` (skip silently in non-verbose mode) |

**Adapter contract:**

| Aspect | Value |
|--------|-------|
| Completion signal | `result` event (type = "result") |
| Final response text | Concatenation of all `message` events where `role` = "assistant", or `result.result` if present |
| Session ID | `init.session_id` |
| Token usage | `result.stats` -> mapped to `TokenUsage{Input: input_tokens, Output: output_tokens}` |
| Non-JSON tolerance | Required. Skip non-JSON lines before and during the stream. Common: keychain warnings, gcloud messages. |
| Tool ID correlation | `tool_use.tool_id` matches `tool_result.tool_id` — adapter tracks in-flight tools by ID |

### C.4 Cross-Adapter Summary Table

| Aspect | Claude Code | Codex | Gemini |
|--------|------------|-------|--------|
| Stream flag | `--output-format stream-json --verbose` | `--json` | `-o stream-json` |
| First event | `system` (init) | `thread.started` | `init` |
| Last event | `result` | `turn.completed` / `turn.failed` | `result` |
| Session ID | `system.session_id` | `thread.started.thread_id` | `init.session_id` |
| Response text | `result.result` | last `item.completed` (`agent_message`) | concatenated assistant messages / `result.result` |
| Token usage | `result.cost` | `turn.completed.usage` | `result.stats` |
| Resume args | `--resume <id> --continue "<msg>"` | `codex exec resume --id <id>` | `--resume <id>` |
| Non-JSON tolerance | rare | none expected | common (keychain warnings) |
