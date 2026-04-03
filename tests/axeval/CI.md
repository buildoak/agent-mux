# ax-eval CI Integration

## Running ax-eval

ax-eval uses a Go build tag to keep it out of normal `go test ./...` runs:

```bash
go test -tags axeval -timeout 600s ./tests/axeval/
```

The `-timeout 600s` is important — each case dispatches a real Codex worker and
waits for completion. Default Go test timeout (10m) is fine for the full suite,
but individual cases can take 30-90s depending on Codex queue depth.

To run a single case:

```bash
go test -tags axeval -timeout 300s -run TestAxEval/error-handling ./tests/axeval/
```

Set `AX_EVAL_REPORT_DIR` to write a JSON report per run:

```bash
AX_EVAL_REPORT_DIR=./eval-reports go test -tags axeval ./tests/axeval/
```

## Cost considerations

Each ax-eval run dispatches ~15+ real Codex workers. At current pricing this
costs roughly $0.50–$2.00 per full suite run depending on token usage and
model. Running on every PR commit adds up fast — treat it as a nightly or
weekly gate, not a per-commit check.

## Flakiness management

LLM behavioral variance means some cases are inherently flaky:

- **`cross-lang-read`** — depends on the model correctly reading and
  cross-referencing files in multiple languages. Passes ~80% of runs.
- **`freeze-stdin-nudge`** — tests stdin-based liveness recovery, which
  depends on timing. Passes ~85% of runs.

All other cases (error handling, streaming, async, lifecycle) are deterministic
at the harness level and should not flake.

When a flaky case fails, check the trace output before investigating — if the
model produced a reasonable but differently-worded response, it is likely
behavioral variance rather than a regression.

## Recommended CI strategy

| Trigger       | What to run                                            |
|---------------|--------------------------------------------------------|
| Every PR      | Deterministic cases only: `error`, `streaming`, `async`, `signal` |
| Nightly       | Full suite (`go test -tags axeval ./tests/axeval/`)    |
| Pre-release   | Full suite, 3 runs, all must pass                      |

Example PR-only CI step:

```bash
go test -tags axeval -timeout 300s \
  -run 'TestAxEval/(error|streaming|async|signal)' \
  ./tests/axeval/
```

## Environment requirements

- **`CODEX_API_KEY`** (or equivalent) must be set — ax-eval dispatches real
  Codex workers. Without it, all cases fail with `binary_not_found` or
  auth errors.
- The `codex` binary must be on `$PATH` (or installed via `npm i -g @openai/codex`).
- Go 1.22+ for the build tag syntax.
- Network access to the Codex API endpoint.
