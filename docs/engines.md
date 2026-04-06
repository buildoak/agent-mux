# Engines
agent-mux runs one supervision loop against three different harness CLIs.
This document covers the adapter boundary that makes that possible: how each engine is invoked, how its events are parsed, and where behavior intentionally differs.
The scope here is only the engine layer. For dispatch assembly, config merge rules, and the broader system shape, use the cross-references at the end.

## HarnessAdapter Interface
Every engine adapter implements the same Go interface:
```go
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
Each method has a narrow responsibility:
| Method | What it does |
| --- | --- |
| `Binary()` | Returns the executable name expected on `PATH`. |
| `BuildArgs()` | Builds `argv[1:]` for the initial harness invocation from a resolved `DispatchSpec`. |
| `EnvVars()` | Returns additional `KEY=VALUE` entries needed by the harness for this dispatch. |
| `ParseEvent()` | Parses one stdout line into a normalized `HarnessEvent`. This is the main translation boundary between engine-native streams and agent-mux event types. |
| `SupportsResume()` | Declares whether the adapter can restart from a prior session with `ResumeArgs()`. |
| `ResumeArgs()` | Builds the engine-specific argv for resuming a known session and passing an inbox message back into the harness. |
| `StdinNudge()` | Returns bytes to write to stdin for liveness nudging, or `nil` when the engine does not use stdin nudges. |
Implementation notes:
- `CodexAdapter` is effectively stateless.
- `ClaudeAdapter` keeps a `sync.Mutex`-protected `toolInputs` map so `tool_result` events can be correlated back to the earlier `tool_use`.
- `GeminiAdapter` keeps a `pendingFiles` map so a later `write_file` result can be attributed to the original path.

## Side-by-Side Summary
| Engine | Binary | Best for | Key flags | Resume support | Tool calling | Event streaming format |
| --- | --- | --- | --- | --- | --- | --- |
| Codex | `codex` | Implementation, debugging, edits | `--json`, `-s` / `--dangerously-bypass-approvals-and-sandbox`, `-c model_reasoning_effort=...`, `--add-dir` | Yes, after `thread.started` | Full | `codex exec --json` NDJSON |
| Claude | `claude` | Planning, synthesis, review | `-p`, `--output-format stream-json`, `--verbose`, `--permission-mode`, `--system-prompt`, `--max-turns` | Yes, after `system` `init` | Full | `stream-json` |
| Gemini | `gemini` | Second opinion, contrast check | `-p`, `-o stream-json`, `-m`, `--approval-mode`, `--include-directories` | Yes, after `init` | Functional; `read_file`, `write_file`, `replace`, `shell` tracked | `stream-json`, with non-JSON stdout surfaced as raw passthrough |
All three adapters plug into the same supervision loop:
- process spawn and process-group shutdown
- stdout event parsing into normalized harness events
- artifact-first result assembly
- timeout, liveness, and inbox handling
- recovery and resume wiring

## Codex Adapter
`CodexAdapter` maps agent-mux dispatch state onto `codex exec`.
### Binary
`Binary()` returns:
```text
codex
```
### Command Construction
Initial invocation shape:
```bash
codex exec --json [-m <model>] <sandbox-flag> [-C <cwd>] \
  [-c model_reasoning_effort=<level>] [--add-dir <dir> ...] "<prompt>"
```
The adapter builds this in `BuildArgs()` with these rules:
- always starts with `exec --json`
- adds `-m <model>` only when `spec.Model` is non-empty
- resolves sandbox flags from `permission-mode`, `sandbox`, and `FullAccess`
- adds `-C <cwd>` when `spec.Cwd` is set
- maps `EngineOpts["reasoning"]` to `-c model_reasoning_effort=<level>`
- forwards additional directories as repeated `--add-dir`
- prepends the system prompt directly into the final prompt text
System prompt handling is not a dedicated Codex CLI flag. The adapter does:
```text
finalPrompt = SystemPrompt + "\n\n" + Prompt
```
That matters for prompt composition order: by the time the adapter sees `spec.SystemPrompt`, the higher-level dispatch path has already merged role, profile, and CLI prompt layers.
### Sandbox Resolution
Codex does not use `--permission-mode` directly. The adapter resolves the sandbox flag with four conditions:
| Condition | Flag emitted |
| --- | --- |
| `EngineOpts["permission-mode"]` is set | `-s <permission-mode>` |
| sandbox is `danger-full-access` and `spec.FullAccess == true` | `--dangerously-bypass-approvals-and-sandbox` |
| sandbox is `danger-full-access` and `spec.FullAccess == false` | `-s danger-full-access` |
| any other sandbox value | `-s <sandbox>` |
If no sandbox option is present, the default base value is `danger-full-access`.

`sandbox` values are validated against a whitelist (`danger-full-access`, `workspace-write`, `read-only`) before the adapter is invoked. An unrecognized value causes a structured dispatch error rather than passing an unknown flag to the CLI.
### Event Parsing
Codex emits NDJSON under `--json`. The adapter recognizes these main event families:
- `thread.started` -> session start
- `item.started` / `item.completed` -> command runs, tool starts, file writes, or agent messages
- `item.updated` for `agent_message` -> progress text
- `turn.completed` -> turn-complete with token usage
- `turn.failed` and `error` -> failure and error events
Unknown or non-matching JSON is passed through as raw events instead of failing the run.
### Resume
Codex resume is supported.
Resume command shape:
```bash
codex exec resume [-m <model>] --json <session_id> "<message>"
```
Behavior details:
- `-m <model>` is included only when `spec.Model` is set
- `session_id` is positional, not a named flag
- the inbox message is passed as the final positional argument

## Claude Adapter
`ClaudeAdapter` maps agent-mux dispatch state onto `claude` with streamed JSON output.
### Binary
`Binary()` returns:
```text
claude
```
### Command Construction
Initial invocation shape:
```bash
claude -p --output-format stream-json --verbose [--model <model>] \
  [--max-turns <n>] [--permission-mode <mode>] \
  [--system-prompt <text>] [--add-dir <dir> ...] "<prompt>"
```
`BuildArgs()` applies these rules:
- always starts with `-p --output-format stream-json --verbose`
- forwards `--model` when `spec.Model` is set
- forwards `--max-turns` when `EngineOpts["max-turns"]` resolves to a positive integer
- forwards `--permission-mode` when present
- forwards the system prompt via a dedicated `--system-prompt` flag
- forwards additional directories as repeated `--add-dir`
- appends `spec.Prompt` as the final argument without merging in the system prompt
Claude is the only adapter here with a dedicated system prompt flag. Unlike Codex, it does not prepend the system prompt into the user prompt body.
### Event Parsing and Tool Correlation
Claude emits `stream-json` messages. The adapter recognizes three main top-level event classes:
- `system` with subtype `init` -> session start
- `assistant` -> progress text or tool activity
- `result` -> final response or result-level error
The non-obvious implementation detail is tool correlation. Claude emits `tool_use` and `tool_result` as separate content items. To map a later `tool_result` back to the originating file path or tool name, `ClaudeAdapter` stores tool metadata in a `sync.Mutex`-protected `toolInputs` map keyed by tool ID.
- file path for `Edit` and `Write`
- tool name when the result event omits it
- correct write attribution when a `tool_result` arrives after the original `tool_use`
Without that correlation layer, file-write events would lose path context.
### Resume
Claude resume is supported.
Resume command shape:
```bash
claude --resume <session_id> --continue "<message>"
```
The adapter does not add model or prompt flags to the resume invocation.

## Gemini Adapter
`GeminiAdapter` maps agent-mux dispatch state onto the Gemini CLI. It is the thinnest adapter operationally and the most constrained behaviorally.
### Binary
`Binary()` returns:
```text
gemini
```
### Command Construction
Initial invocation shape:
```bash
gemini -p "<prompt>" -o stream-json [-m <model>] \
  --approval-mode <mode> [--include-directories <dir1,dir2,...>]
```
`BuildArgs()` applies these rules:
- always starts with `-p <prompt> -o stream-json`
- forwards `-m <model>` when `spec.Model` is set
- maps `EngineOpts["permission-mode"]` to Gemini `--approval-mode`
- `--approval-mode` defaults to `yolo` when no `permission-mode` engine opt is configured. Valid values: `default`, `auto_edit`, `yolo`, `plan`.
- joins additional directories into a single comma-separated `--include-directories` value

### Effort / Reasoning

Gemini CLI does not support an effort or reasoning effort flag. When `EngineOpts["reasoning"]` is set (via `--reasoning` flag or profile config), `BuildArgs()` logs a warning and ignores the value. The warning message directs users to model selection as the alternative for controlling thinking depth. This matches the pattern in `CodexAdapter` where `EngineOpts["reasoning"]` maps to `-c model_reasoning_effort=<level>` — except Gemini has no equivalent flag, so the value is discarded with a diagnostic.

### Stall Timeout Default

`BuildArgs()` injects `stall_timeout_seconds: 60` into `engine_opts` for all Gemini dispatches unless already set. Runs that go silent for 60s are killed. Override by setting `stall_timeout_seconds` explicitly in `engine_opts`.

### System Prompt Handling
Gemini does not get a direct system prompt flag. The adapter uses an environment-file path instead:
- writes `spec.SystemPrompt` to `<artifact_dir>/system_prompt.md`
- returns `GEMINI_SYSTEM_MD=<artifact_dir>/system_prompt.md` from `EnvVars()`
This has an important edge condition: if `spec.ArtifactDir` is empty, the system prompt is dropped. The adapter returns no env var and does not fail the dispatch.
### Event Parsing and Limitations
Gemini parsing is intentionally defensive:
- empty lines are ignored
- non-JSON stdout lines are surfaced as `raw_passthrough` events
- JSON parse failures are returned as adapter errors

**Dual event schema:** The adapter handles both v0.34.0+ schema (`tool_name`/`parameters`) and legacy schema (`name`/`input`). `resolvedName()` and `resolvedParams()` abstract this. Error detection: `Status == "error"` (new) vs `IsError` (legacy).

**Tool tracking:** `write_file` and `replace` are both tracked through `pendingFiles` for path attribution on `tool_result`. `shell` and `run_shell_command` are both recognized as command-run events.

**Delta buffer:** Streaming assistant messages are accumulated in `deltaBuffer` and flushed as response text on `result` events. Falls back to `raw.Result` when no deltas have accumulated.

**Actual model resolution:** `resolveActualModel()` extracts the model that actually served the request from the per-model stats map. Relevant for auto-routing profiles.

Known limitations:

- Resume session IDs silently degrade to `"latest"` for UUID inputs
- System prompt depends on `ArtifactDir`; without it, silently dropped
- Tool support present but less battle-tested than Codex/Claude adapters

### Resume
Gemini resume is supported.
Resume command shape:
```bash
gemini --resume <session_id> -p "<message>"
```
When `sessionID` matches a UUID pattern, the adapter logs a warning and uses `"latest"` instead — Gemini CLI `--resume` only accepts numeric indices or `"latest"`.

## Model Validation
Model validation happens before dispatch, after adapter lookup.
Flow:
1. `dispatchSpec()` builds an adapter registry with `configuredModels(cfg)`.
2. `configuredModels(cfg)` starts with `[models]` from config.
3. If an engine has no configured list, agent-mux fills a hardcoded fallback list for that engine.
4. If `spec.Model` is non-empty and not present in the active list for the selected engine, dispatch fails with `model_not_found`.
Fallback model sets are currently:
| Engine | Fallback models |
| --- | --- |
| `codex` | `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.3-codex-spark`, `gpt-5.2-codex` |
| `claude` | `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5` |
| `gemini` | `gemini-2.5-flash`, `gemini-2.5-pro`, `gemini-3-flash-preview`, `gemini-3.1-pro-preview` |
On a miss, the error path uses `dispatch.FuzzyMatchModel()`. That function runs a case-insensitive Levenshtein comparison across the valid models for the chosen engine and returns the best match when distance is `<= 3`.
The resulting error message includes:
- the requested engine
- the rejected model
- the valid model list
- a `Did you mean "<model>"?` suggestion when fuzzy matching finds one
Validation is engine-specific because each engine gets its own allowed list from config or fallback defaults.

## Authentication
Authentication is owned by the underlying harness CLIs, but agent-mux documents the expected environment and known fallbacks.
| Engine | Primary env var | Fallback |
| --- | --- | --- |
| Codex | `OPENAI_API_KEY` | OAuth device auth via `codex auth` using `~/.codex/auth.json` |
| Claude | `ANTHROPIC_API_KEY` | Device OAuth subscription login exists, but should not be used for automation |
| Gemini | `GEMINI_API_KEY` | No documented fallback in this repo |
Operational notes:
- agent-mux does not inject provider credentials itself; it expects the harness environment to already be usable
- Codex and Claude can still dispatch when their CLI has a valid non-key auth path available
- Gemini is documented here with `GEMINI_API_KEY` only
Anthropic ToS compliance matters here: for the `claude` engine, automated use should go through `ANTHROPIC_API_KEY`. Device OAuth subscription login falls under Anthropic consumer terms and is not the compliant path for scripted workflows.
## Cross-References
- [dispatch.md](dispatch.md) for dispatch assembly and lifecycle outside the adapter boundary
- [config.md](config.md) for model lists, engine defaults, profile frontmatter, and engine option sources
- [architecture.md](architecture.md) for the wider supervision loop, package map, and system rationale
