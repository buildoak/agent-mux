# Dispatch

Dispatch is the core execution path in agent-mux: one resolved `DispatchSpec` goes in, one normalized `DispatchResult` comes out.

## Resolution Flow

The current CLI path is:

1. Parse either normal dispatch flags or `--stdin` JSON.
2. Load config with `config.LoadConfig(flags.config, spec.Cwd)`.
3. Apply profile defaults when `profile` is set.
4. Apply profile defaults (engine, model, effort, timeout, system prompt, skills) when profile is set.
5. Fill unresolved `engine`, `model`, `effort`, `max_depth`, `timeout_sec`, and `grace_sec`.
6. Inject default `engine_opts` values for liveness and `permission-mode` when they were not set explicitly.
7. Load skill prompts and skill `scripts/` directories unless `skip_skills` is true.
8. Validate the context file path when `context_file` is set.
9. If `recover` or `--recover` is set, replace `spec.Prompt` with `BuildRecoveryPrompt(...)`.
10. Build preview output and, for interactive TTY use, optionally confirm before launch.
11. Resolve the adapter, ensure the artifact directory exists, persist `meta.json`, write `_dispatch_ref.json`, and run the loop engine.

Inside the loop engine, `dispatch.WithPromptPreamble` prepends runtime instructions for `$AGENT_MUX_CONTEXT` and `$AGENT_MUX_ARTIFACT_DIR` just before the adapter command is built.

## `DispatchSpec`

Source of truth: `internal/types/types.go`.

```go
type DispatchSpec struct {
	DispatchID   string         `json:"dispatch_id"`
	Engine       string         `json:"engine"`
	Model        string         `json:"model,omitempty"`
	Effort       string         `json:"effort"`
	Prompt       string         `json:"prompt"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	Cwd          string         `json:"cwd"`
	ArtifactDir  string         `json:"artifact_dir"`
	ContextFile  string         `json:"context_file,omitempty"`
	TimeoutSec   int            `json:"timeout_sec,omitempty"`
	GraceSec     int            `json:"grace_sec,omitempty"`
	MaxDepth     int            `json:"max_depth"`
	Depth        int            `json:"depth"`
	FullAccess   bool           `json:"full_access"`
	EngineOpts   map[string]any `json:"engine_opts,omitempty"`
}
```

### Core JSON Fields

These are the structured fields decoded directly into `types.DispatchSpec`, including `--stdin` mode:

| JSON key | Type | Meaning |
| --- | --- | --- |
| `dispatch_id` | `string` | Stable dispatch identifier |
| `engine` | `string` | `codex`, `claude`, or `gemini` |
| `model` | `string` | Model override |
| `effort` | `string` | Effort bucket |
| `prompt` | `string` | Required user prompt |
| `system_prompt` | `string` | Inline system prompt content |
| `cwd` | `string` | Working directory |
| `artifact_dir` | `string` | Runtime artifact directory |
| `context_file` | `string` | Optional context file path |
| `timeout_sec` | `int` | Hard timeout in seconds |
| `grace_sec` | `int` | Grace period in seconds |
| `max_depth` | `int` | Recursive dispatch limit |
| `depth` | `int` | Current recursion depth |
| `full_access` | `bool` | Codex full-access toggle |
| `engine_opts` | `map[string]any` | Adapter-specific options |

### Additional `--stdin` Metadata Keys

`--stdin` accepts extra top-level keys that are not part of `DispatchSpec` itself but are still consumed by `main.go`:

| JSON key | Meaning |
| --- | --- |
| `role` | Legacy role name field, ignored by current stdin parsing |
| `profile` | Profile name |
| `coordinator` | Alias for `profile` |
| `skills` | Explicit skill list |
| `skip_skills` | Skip skill injection |
| `recover` | Prior dispatch ID to continue |

### `--stdin` Defaults

When the corresponding JSON key is absent:

- `dispatch_id`: generated ULID
- `cwd`: current working directory
- `artifact_dir`: `dispatch.DefaultArtifactDir(dispatch_id) + "/"` under the runtime artifact root
- `full_access`: `true`
- `grace_sec`: `60`

`prompt` is required in `--stdin` mode.

## `DispatchResult`

Source of truth: `internal/types/types.go`.

```go
type DispatchResult struct {
	SchemaVersion     int               `json:"schema_version"`
	Status            DispatchStatus    `json:"status"`
	DispatchID        string            `json:"dispatch_id"`
	Response          string            `json:"response"`
	ResponseTruncated bool              `json:"response_truncated"`
	FullOutput        *string           `json:"full_output"`
	FullOutputPath    *string           `json:"full_output_path,omitempty"`
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

### Important Result Fields

| JSON key | Meaning |
| --- | --- |
| `status` | `completed`, `timed_out`, or `failed` |
| `response` | Final response text |
| `artifacts` | Non-internal files from the artifact directory |
| `partial` | Usable partial result was preserved |
| `recoverable` | The run can likely be resumed |
| `reason` | Short terminal reason |
| `error` | Structured failure payload |
| `activity` | Files, commands, and tools observed |
| `metadata` | Engine, model, role, profile, skills, tokens, turns, cost, session |
| `duration_ms` | End-to-end runtime |

`full_output_path` is a deprecated compatibility field. It still exists in the struct, but current documentation should treat it as a dead legacy stub rather than an active persistence contract.

## Durable Persistence

The artifact directory is not the persistent store.

Current dispatch persistence is:

```text
~/.agent-mux/dispatches/<dispatch_id>/
  meta.json
  result.json
```

- `meta.json` stores durable dispatch metadata such as engine, model, effort, profile, cwd, artifact directory, started time, timeout, prompt hash, and session ID.
- `result.json` stores the terminal `DispatchResult` plus persisted fields such as `started_at`, `ended_at`, `artifact_dir`, `cwd`, `engine`, `model`, `profile`, `effort`, `session_id`, `response_chars`, and `timeout_sec`.

The artifact-local `_dispatch_ref.json` is only a pointer back to that store.

## Cross-References

- [async.md](./async.md) for `--async`, `status.json`, and event streaming
- [recovery.md](./recovery.md) for `_dispatch_ref.json`, `meta.json`, and recovery prompts
- [cli-reference.md](./cli-reference.md) for command and flag usage
