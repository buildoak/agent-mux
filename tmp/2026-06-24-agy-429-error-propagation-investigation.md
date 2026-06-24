# Agy 429 Error Propagation Investigation

Date: 2026-06-24

## Decision

Propagate Antigravity/Gemini 429s through a sanitized engine-level diagnostic classifier, exposed as a new cataloged dispatch error code: `provider_rate_limited`.

Do this with a narrow adapter extension that lets plain-stdout adapters inspect private diagnostics at finalization time and return a sanitized `DispatchError`. Implement the first concrete hook in `AgyAdapter` by reading the locally relevant Antigravity transcript/log diagnostics only after the process has ended and only when the public result would otherwise be generic (`harness_empty_output` or process failure).

Do not append private transcript/log content to `response`, `handoff_summary`, public artifacts, stderr tails, or lifecycle result output.

## Current Agy Data Path

The active agy path is:

1. CLI dispatch builds/preflights a `DispatchSpec` in `cmd/agent-mux/main.go`, then calls `dispatchSync(...)`, which constructs `engine.NewLoopEngine(...)` and invokes `eng.Dispatch(ctx, spec)` (`cmd/agent-mux/main.go:419`, `:482`, `:1573`).
2. `AgyAdapter` is registered under engine name `agy` in `internal/engine/adapter/registry.go:20`.
3. `AgyAdapter.BuildArgs` invokes local print mode:
   `agy --sandbox --print-timeout <timeout> --log-file <artifact_dir>/agy.log [--model ...] [--add-dir ...] [-p prompt]`
   (`internal/engine/adapter/agy.go:28-63`).
4. `AgyAdapter.RuntimePolicy` declares EOF stdin, `AdapterOutputPlainStdout`, `RequireNonEmptyResponse: true`, no soft-timeout wrapup, and `AdapterFailureContextPrivateDiagnostics` (`internal/engine/adapter/agy.go:104-111`).
5. In `LoopEngine.Dispatch`, artifact dir, persistent metadata, `_dispatch_ref.json`, inbox, and `events.jsonl` are created before process launch (`internal/engine/loop.go:189-219`).
6. `startRun` wires stdout/stderr and starts the child. Because agy uses plain stdout, the stdout reader calls `captureRawStdout`, not `scanHarnessOutput`; no `ParseEvent` classification occurs for agy output (`internal/engine/loop.go:130-136`, `:797-809`, `:952-954`).
7. At finalization, response becomes `currentRun.rawStdout.String()` (`internal/engine/loop.go:952-954`).
8. If the process exits without terminal state, no dispatch error exists, and response is empty, `LoopEngine.Dispatch` returns `harness_empty_output` (`internal/engine/loop.go:1006-1011`).

This explains the incident: the Antigravity transcript had structured `ERROR_MESSAGE` entries with `error_code: 429`, but those entries were not stdout. agent-mux therefore only saw a successful agy process with empty stdout and applied the generic empty-output rule.

## Existing Error Propagation

Public result errors use `types.DispatchError` with `code`, `message`, `hint`, `example`, `retryable`, and optional `partial_artifacts` (`internal/types/types.go:35-42`). Error defaults and retryability live in `dispatch.ErrorCatalog` (`internal/dispatch/dispatch.go:175-344`), and `NewDispatchError` materializes catalog values (`internal/dispatch/dispatch.go:345-367`).

Codex:

- `CodexAdapter.ParseEvent` parses JSON event stream output.
- `turn.failed` preserves provider `raw.Error.Code` and message as `EventTurnFailed` (`internal/engine/adapter/codex.go:225-230`).
- top-level `error` preserves `raw.Code` and `raw.Message` as `EventError` (`internal/engine/adapter/codex.go:231-234`).
- The loop records `EventError`/`EventTurnFailed` as `lastError`, and `failureFromEventOrProcess` turns that event into `dispatch.NewDispatchError(errEvt.ErrorCode, errEvt.Text, "")` (`internal/engine/loop.go:404-409`, `:1182-1185`).

Claude:

- `ClaudeAdapter.ParseEvent` parses stream JSON.
- `result` with `subtype: success` becomes `EventResponse`.
- `result` with `subtype: error` becomes `EventTurnFailed` with hard-coded `ErrorCode: result_error` and raw error text (`internal/engine/adapter/claude.go:126-138`).
- This preserves text but not a provider-specific code unless Claude's stream shape changes.

Gemini:

- `GeminiAdapter.ParseEvent` parses stream JSON.
- top-level `error` preserves `raw.Code` and `raw.Message` (`internal/engine/adapter/gemini.go:228-231`).
- `result` errors become generic `result_error` (`internal/engine/adapter/gemini.go:232-237`).
- tool failures become generic `tool_error` (`internal/engine/adapter/gemini.go:348-353`).

Loop/process fallback:

- Failed terminal states call `failureFromEventOrProcess` (`internal/engine/loop.go:993-998`, `:1031-1033`).
- If a structured harness error event exists, it wins (`internal/engine/loop.go:1182-1185`).
- Otherwise process status is classified as `killed_by_user` for SIGKILL/SIGTERM/137/143 or generic `process_killed` (`internal/engine/loop.go:1186-1217`).
- If `FailureContextMode` is not private, stderr tail can be included; agy's private mode suppresses it (`internal/engine/loop.go:1192-1198`).

Privacy/artifacts:

- `agy.log`, stdout/stderr variants, raw/provider/harness diagnostic names, `events.jsonl`, `status.json`, `host.pid`, inbox/control files, and diagnostic prefixes are excluded from public artifact scanning (`internal/dispatch/dispatch.go:444-523`).
- Docs explicitly state agy is plain stdout, empty stdout becomes `harness_empty_output`, and `agy.log` stays private (`docs/agy.md:75-83`, `docs/engines.md:238-240`).

## Recommended Design

Add a small adapter API for sanitized post-run failure classification.

Likely files/functions:

1. `internal/types/types.go`
   - Add an optional interface, for example `AdapterFailureDiagnoser`.
   - Shape should accept enough finalization context to inspect private diagnostics without exposing them: `spec`, captured response/stdout emptiness, process exit information if needed, and existing stderr string if already available.
   - Return `*DispatchError` or a neutral adapter diagnostic that the loop converts through `dispatch.NewDispatchError`.

2. `internal/engine/loop.go`
   - In `buildResult`, before the current `RequireNonEmptyResponse` fallback and before generic `failureFromEventOrProcess` fallback, ask the adapter diagnoser for a sanitized failure.
   - Use it only when the run is already failing or suspicious: empty required response, non-zero process exit, or existing terminal failure with no structured event.
   - If the diagnoser returns an error classification, emit/finalize that instead of `harness_empty_output` or generic process failure.
   - Do not add private diagnostic text to events except a sanitized `EmitError(code, message)` if consistent with existing finalization behavior.

3. `internal/engine/adapter/agy.go`
   - Implement the diagnoser.
   - Reuse the already-discovered conversation ID path from `agy.log` (`DiscoverSessionID` and `extractAgyConversationID`) to locate Antigravity's private transcript under `~/.gemini/antigravity-cli/brain/<conversation_id>/.system_generated/logs/transcript_full.jsonl`.
   - Scan bounded tail/content, not unbounded full history.
   - Detect structured JSONL entries where `source == "SYSTEM"`, `type == "ERROR_MESSAGE"`, and `error_code == 429`.
   - Also allow a conservative fallback for the known sanitized text `"model API is currently overloaded"` only when it appears in the Antigravity diagnostic file, not in arbitrary worker output.
   - Return only sanitized public fields via the catalog code; never return raw transcript text, prompt content, file contents, conversation IDs, or paths unless the path is already the user's artifact dir.

4. `internal/dispatch/dispatch.go`
   - Add `provider_rate_limited` to `ErrorCatalog`.

5. `docs/agy.md`, `docs/engines.md`, and `skill/references/output-contract.md` if maintained as the public contract
   - Document that agy may classify private Antigravity 429 diagnostics into a sanitized public error.
   - Preserve the statement that `agy.log` and transcripts remain private and are not appended.

Rejected alternatives:

- Adapter `ParseEvent`: rejected because agy stdout is the final answer stream. Parsing private transcript data as stdout would conflate two channels and miss the incident because the 429 is not on stdout.
- Dispatch catalog only: rejected because a catalog entry gives retryability once a code exists, but it does not discover 429s.
- Public artifact scanner: rejected because private diagnostics are intentionally excluded from public artifacts. Making the scanner read them would risk leaking private provider logs into results/recovery.
- Loop hard-coding `if engine == "agy"`: viable as a quick patch but rejected as a worse boundary. It bakes provider-specific filesystem knowledge into the generic supervisor.
- Redesign all errors around provider/runtime categories: rejected as too broad for this incident. The existing `DispatchError` surface is adequate if the 429 is classified before generic fallback.

## Error Taxonomy

Recommended code: `provider_rate_limited`

Recommended catalog entry:

- Message: `Provider rate limit or overload was reported.`
- Hint: `The provider reported HTTP 429/rate limiting/overload in private diagnostics. Wait for cooldown, reduce agy concurrency, or switch model/provider before retrying.`
- Example: `Retry after backoff with lower concurrency. Example: run agy batches sequentially or with max concurrency 1-2, then increase only after canaries succeed.`
- Retryable: `true`

Reasoning:

- Use one provider-neutral code rather than `agy_429` so future Codex/Claude/Gemini provider rate-limit signals can converge on the same public policy.
- Keep `retryable: true`, but docs must clarify this is not permission for immediate blind retry. It means retry after delay/remediation.
- Do not overload `harness_empty_output`. That code is still valid for truly unclassified empty stdout.

Retry/backoff/circuit-breaker implication:

- agent-mux currently has no automatic retry scheduler or circuit breaker in the inspected code. `retryable` is advisory for callers.
- External schedulers should key on `result.error.code == "provider_rate_limited"`, `result.error.retryable == true`, `metadata.engine`, and `metadata.model`.
- Backoff should be delayed and concurrency-reducing. For agy/Gemini overload, default policy should be cooldown plus max concurrency 1-2, not immediate retry storms.

## Tests To Add

1. Adapter diagnostic parser unit tests in `internal/engine/adapter/agy_test.go`
   - Given an `agy.log` with a conversation UUID and a fake Antigravity transcript containing an `ERROR_MESSAGE` with `error_code: 429`, the diagnoser returns `provider_rate_limited`.
   - Given non-429 transcript errors, malformed JSONL, missing transcript, or missing conversation ID, the diagnoser returns nil and the loop can fall back.
   - Verification gate: no returned public field contains the raw transcript path, conversation ID, prompt text, or arbitrary transcript `error` body beyond approved sanitized text.

2. Loop finalization tests in `internal/engine/loop_test.go`
   - Fake plain-stdout adapter with private diagnostic diagnoser and empty stdout exits 0; final result is failed with `error.code == "provider_rate_limited"` instead of `harness_empty_output`.
   - Fake diagnoser returns nil; existing `harness_empty_output` test still passes.
   - Non-zero exit plus diagnoser classification returns `provider_rate_limited` instead of generic process failure.

3. Dispatch catalog tests in `internal/dispatch/dispatch_test.go`
   - `NewDispatchError("provider_rate_limited", "", "")` has non-empty message/hint/example and `Retryable == true`.

4. Privacy regression tests
   - Extend existing private diagnostics tests around `internal/engine/loop_test.go:952`, `internal/dispatch/recovery_test.go:69`, or `internal/dispatch/dispatch_test.go:461`.
   - Verification gate: result JSON does not include `transcript_full.jsonl`, Antigravity brain path, conversation UUID, raw prompt, or raw private log contents.

5. Docs/contract tests if the repo has generated checks for `skill/references/output-contract.md`
   - Add `provider_rate_limited` to the public code table.

No live provider call is needed for this fix.

## Risks and Tradeoffs

- Antigravity private path stability: the transcript path under `~/.gemini/antigravity-cli/brain/<uuid>/.system_generated/logs/transcript_full.jsonl` may change. Mitigation: keep scanner best-effort and fallback to current behavior when missing.
- Conversation ID availability: if `agy.log` does not contain a conversation ID, transcript lookup cannot happen. Mitigation: also inspect bounded `agy.log` for known sanitized 429 markers if present, but do not parse arbitrary public stdout.
- False positives from text matching: avoid broad string matching. Prefer structured `error_code == 429`. Use text fallback only for known provider overload phrasing and only in private diagnostics.
- Privacy leakage: the biggest risk. Mitigation: return catalog code plus sanitized message only; keep private diagnostic artifacts excluded.
- Retry storms: marking retryable true may encourage immediate retries. Mitigation: hint/example must mention cooldown and concurrency reduction; scheduler/circuit-breaker should treat this as delayed retry.
- API shape churn: adding an adapter extension is a small architectural change. It is reversible and scoped; hard-coding agy into the loop would be faster but less maintainable.

## Minimal Implementation Sequence

1. Add `provider_rate_limited` to `ErrorCatalog`.
   - Verification gate: catalog unit test proves retryable true and non-empty public guidance.

2. Add optional adapter diagnostic interface in `internal/types/types.go`.
   - Verification gate: existing adapters still compile without implementing it; `go test ./internal/types ./internal/engine/adapter` passes.

3. Wire loop finalization to consult the optional diagnoser before `harness_empty_output` and generic process fallback.
   - Verification gate: fake adapter test proves empty stdout can be upgraded to `provider_rate_limited`, while nil diagnosis still yields `harness_empty_output`.

4. Implement agy private diagnostic scanner.
   - Verification gate: agy unit tests classify structured 429 and ignore missing/malformed/non-429 diagnostics without failing dispatch finalization.

5. Add privacy regression assertions.
   - Verification gate: no result/error/artifact list/recovery prompt leaks private transcript/log content.

6. Update docs/output contract.
   - Verification gate: docs state the new code, retry semantics, and privacy boundary.

7. Run focused tests, then broader test sweep.
   - Focused: `go test ./internal/dispatch ./internal/types ./internal/engine ./internal/engine/adapter`
   - Broader: `go test ./...`

## Assumptions

- The Antigravity conversation UUID in `agy.log` corresponds to the `<uuid>` directory used by the private transcript path.
- The private transcript JSONL schema observed in the incident is stable enough to match `source`, `type`, `error`, and `error_code`.
- agent-mux should not itself launch automatic retries in this change; it should make retry/backoff decisions possible for callers.
- `provider_rate_limited` is acceptable as a provider-neutral taxonomy code even though the first implementation is agy-specific.
