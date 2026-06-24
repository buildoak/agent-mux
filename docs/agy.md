# Agy Engine

`agy` support is experimental, CLI-first model access through the local Antigravity CLI. agent-mux invokes the local `agy` binary; local `agy` configuration owns auth, providers, sandbox meaning, model availability, multimodal capability, and provider-side services.

Use this document as the operator contract for dispatching agy through agent-mux.

## Quick Start

```bash
agent-mux config engines
agent-mux config engines --json
agent-mux config engines --refresh-models --json

agent-mux --engine agy \
  --model 'Gemini 3.5 Flash (Low)' \
  --cwd /repo \
  --timeout 90 \
  --yes \
  'Reply exactly: OK'
```

`config engines --json` is the active allowlist for model validation. `--refresh-models` is explicit; normal config reads and dispatches do not call `agy models`.

## Dispatch Contract

The adapter builds print-mode agy invocations:

```bash
agy --sandbox --print-timeout <seconds>s \
  --log-file <artifact_dir>/agy.log \
  [--model <model>] \
  [--add-dir <dir> ...] \
  [--conversation <id>] \
  -p "<prompt>"
```

Rules:

- agent-mux always passes the local agy CLI `--sandbox` flag.
- Operator-supplied portable sandbox, permission, reasoning, max-turn, and full-access options are rejected for agy.
- `--print-timeout` is `timeout + grace + 5s` when a dispatch timeout is set, otherwise `300s`.
- Provider diagnostics go to `<artifact_dir>/agy.log`.
- System prompt text is prepended into the prompt body because agy has no dedicated system-prompt flag in this adapter.
- `--conversation` is added only for resume-backed delivery.

## Models and Cache

Built-in fallback models are deterministic:

- `Gemini 3.1 Pro (High)`
- `Gemini 3.1 Pro (Low)`
- `Gemini 3.5 Flash (High)`
- `Gemini 3.5 Flash (Medium)`
- `Gemini 3.5 Flash (Low)`
- `Claude Sonnet 4.6 (Thinking)`
- `Claude Opus 4.6 (Thinking)`
- `GPT-OSS 120B (Medium)`

Explicit refresh:

```bash
agent-mux config engines --refresh-models --json
```

That command runs `agy models` with a short timeout, parses model names, validates the cache metadata, and writes `~/.agent-mux/cache/agy-models.json` atomically. A valid cache replaces the built-in agy list for config, preflight, and dispatch validation. Invalid or missing cache falls back to built-ins.

If an agy dispatch fails with `model_not_found`, run:

```bash
agent-mux config engines --refresh-models --json
```

Then retry with a model listed in the agy entry.

## Output and Artifacts

Agy is plain stdout, not an event stream.

- Final stdout becomes the normalized response.
- A clean exit with empty stdout fails as `harness_empty_output`.
- Private Antigravity diagnostics may classify a provider 429/overload as `provider_rate_limited`.
- `agy.log` and Antigravity transcripts remain private diagnostics and are not appended to user-visible structured errors.
- No structured tool calls, file reads, file writes, token usage, cache usage, or cost telemetry are exposed to agent-mux.
- Generated files are discovered by final artifact scanning or by checking the dispatch cwd, not by `file_write` events.

## Resume and Steering

Agy supports resume through Antigravity conversation IDs discovered from `<artifact_dir>/agy.log`.

```bash
agent-mux --async --engine agy --model 'Gemini 3.5 Flash (Low)' --cwd /repo --timeout 120 --yes '<prompt>'
agent-mux steer <dispatch_id> nudge 'Reply with exactly: OK'
agent-mux wait --json --poll 1s <dispatch_id>
```

Steering semantics:

- `steer abort` uses SIGTERM or `control.json`.
- `steer nudge`, `steer redirect`, and `--signal` write inbox messages for resume-backed delivery.
- Delivery is not a live interrupt and does not use FIFO/stdin.
- The loop restarts agy with `--conversation <session_id>` after a conversation ID is discoverable.

## Multimodal and Image Generation

The capability matrix marks agy multimodal input and image generation as supported because local live smoke tests verified PDF/PNG consumption and creation of a generated PNG through the local agy setup.

Important caveats:

- Capability depends on the local agy CLI, selected model, provider access, and quota.
- agent-mux does not receive structured multimodal or image-generation events.
- Ask for named file outputs when you need generated artifacts, then verify the file exists.
- The live AX gate verifies existence and non-empty output, not visual quality.

## Unsupported Options

Agy rejects explicit operator-supplied values for:

- portable `--sandbox`
- `--permission-mode`
- `--reasoning`
- `--max-turns`
- full-access toggles

Use model selection, cwd/add-dir file visibility, prompt shape, and timeout instead.

## Verification

Deterministic fake-binary coverage:

```bash
go test -tags axeval -timeout 120s -run 'TestAgyFakeBinary|TestAgyLiveContractRequiresExplicitOptIn' ./tests/axeval/
```

Live gates:

```bash
AX_EVAL_AGY_LIVE=1 go test -tags axeval -timeout 300s -run TestAgyLiveContractRequiresExplicitOptIn ./tests/axeval/
AX_EVAL_AGY_LIVE_ASYNC=1 go test -tags axeval -timeout 300s -run TestAgyLiveAsyncSteerRequiresExplicitOptIn ./tests/axeval/
AX_EVAL_AGY_LIVE_MULTIMODAL=1 go test -tags axeval -timeout 420s -run TestAgyLiveMultimodalAndImageGenerationRequiresExplicitOptIn ./tests/axeval/
```

Live prerequisites: local `agy` binary installed, authenticated, provider/network access available, and expected provider cost/quota accepted. Run `agent-mux config engines --refresh-models --json` before live tests when using `AX_EVAL_AGY_MODEL`.

## Cross-References

- [engines.md](engines.md) for all adapter contracts.
- [config.md](config.md) for model validation and engine capability output.
- [steering.md](steering.md) for inbox/FIFO routing.
- [ax-eval.md](ax-eval.md) for test tiers and live gates.
