# ax-eval V2: Coverage Gap Analysis & New Test Designs

## Part 1: Coverage Gap Table

| Feature | Tested? | Existing Case | Risk if Untested |
|---------|---------|---------------|------------------|
| **Dispatch Mechanics** | | | |
| Engine selection (codex) | yes | all cases | — |
| Engine selection (bad) | yes | bad-engine | — |
| Model selection | yes | bad-model | — |
| Effort tiers → timeout bucketing | yes | effort-tiers-low, TestEffortTiers | — |
| Role resolution | partial | role-dispatch (verifies completion, not system prompt delivery) | System prompt silently dropped |
| Variant resolution | **no** | — | Variant override silently ignored |
| Profile resolution | **no** | — | Profile engine/model/timeout overrides broken |
| response_max_chars truncation | REMOVED | — | `--response-max-chars` flag no longer exists; truncation removed by design |
| --stdin JSON dispatch | **no** | — | Entire programmatic dispatch path broken |
| --preview dry-run | **no** | — | Coordinators can't preview before dispatch |
| **Worker Interaction** | | | |
| Prompt delivery | yes | all completion cases | — |
| Skill injection (content reaches worker) | yes | TestSkillsInjection | — |
| Skill scripts/ dir added to PATH | **no** | — | Skill scripts silently unavailable |
| Context-file injection | yes | TestContextFile | — |
| System prompt from role | **no** | — | Role system prompt silently dropped |
| System prompt via --system-prompt-file | **no** | — | CLI system prompt path broken |
| **Output Handling** | | | |
| Response capture | yes | all completion cases | — |
| Artifact dir creation + meta.json (persistence) | partial | artifact-dir-metadata | Checks `~/.agent-mux/dispatches/<id>/meta.json` for dispatch_id, engine, model, started_at |
| result.json (persistence) | **no** | — | `~/.agent-mux/dispatches/<id>/result.json` not yet asserted |
| full_output.md fallback (via result cmd) | **no** | — | response-truncation case removed; no replacement yet. scout-role-completion now tests only role completion, not spill behavior |
| handoff_summary extraction | **no** | — | Pipeline handoffs get garbage |
| Output contract schema fields | **no** | — | Callers get wrong JSON shape |
| **Event System** | | | |
| Heartbeat emission | partial | stream-flag (checks stderr) | — |
| Tool tracking (tool_start/tool_end) | partial | silent-default (eventLog check) | — |
| File tracking (file_write) | **no** | — | Activity.files_changed wrong |
| Event log persistence (events.jsonl) | partial | silent-default | — |
| Stream mode filtering (silent vs stream) | yes | silent-default, stream-flag | — |
| **Liveness** | | | |
| Frozen detection + kill | yes | freeze-watchdog | — |
| Stdin nudge on frozen | yes | freeze-stdin-nudge | — |
| Long-command protection | **no** | — | cargo/make builds killed as frozen |
| Tool-boundary-aware steering | **no** | — | Steering fires mid-tool, corrupts state |
| **Lifecycle** | | | |
| list (basic) | **no** | — | Agents can't find prior dispatches |
| status (live + stored) | partial | status-live (checks ack only) | — |
| result (blocking + --no-wait) | **no** | — | Result retrieval broken |
| inspect | **no** | — | Deep dispatch introspection broken |
| wait (--poll) | partial | wait-poll (delegates to result collector) | — |
| **Steering** | | | |
| Nudge delivery | yes | steer-nudge | — |
| Redirect delivery + framing | yes | steer-redirect | — |
| Abort (SIGTERM + control.json) | yes | steer-abort | — |
| Extend (watchdog override) | **no** | — | Extend silently ignored |
| **Async** | | | |
| --async ack shape | yes | async-dispatch | — |
| host.pid written | **no** | — | Orphan detection broken |
| status.json live writes | **no** | — | ax status returns stale data |
| **Recovery** | | | |
| --recover with prior context | yes | TestRecoveryRedispatch | — |
| **Config** | | | |
| Config loading + merge | **no** | — | CWD config silently ignored |
| config introspection (roles, skills, models) | **no** | — | Agents can't discover capabilities |
| **Error Handling** | | | |
| engine_not_found | yes | bad-engine | — |
| model_not_found | yes | bad-model | — |
| frozen_killed | yes | freeze-watchdog | — |
| max_depth_exceeded | **no** | — | Recursive dispatches loop forever |

---

## Part 2: New Test Cases

### M1: `output-contract-schema`
**Tests:** JSON output contract fields match spec (schema_version, dispatch_id, activity, metadata, artifacts)
**Prompt:** `"What is 2+2? Answer with just the number."`
**Evaluators:**
- `statusIs("completed")`
- Parse raw stdout JSON: assert `schema_version == 1`
- Assert `dispatch_id` is non-empty ULID format
- Assert `activity` object has all 4 array fields
- Assert `metadata.engine == "codex"`, `metadata.model == "gpt-5.4-mini"`
- Assert `duration_ms > 0`

### M2: `role-system-prompt-delivery`
**Tests:** Role system_prompt_file content actually reaches the worker
**Setup:** Create fixture role with `system_prompt_file = "test-sysprompt.md"` containing canary `ROLE_SYSPROMPT_CANARY_9931`
**Prompt:** `"Repeat any canary phrases from your system instructions verbatim."`
**ExtraFlags:** `["-R=sysprompt-test"]`
**Evaluators:**
- `statusIs("completed")`
- `responseContains("ROLE_SYSPROMPT_CANARY_9931")`

### M3: `variant-resolution`
**Tests:** --variant overrides engine/model/effort from base role
**Setup:** Fixture role `variant-test` with base `model: gpt-5.4` and variant `mini` with `model: gpt-5.4-mini`
**Prompt:** `"What is 2+2?"`
**ExtraFlags:** `["-R=variant-test", "--variant=mini"]`
**Evaluators:**
- `statusIs("completed")`
- Parse dispatch_start event from stderr: assert `model == "gpt-5.4-mini"`

### M4: `response-truncation` — REMOVED
**Status:** Removed. `--response-max-chars` flag and truncation logic no longer exist in the codebase (removed in 3.1.0). The case was asserting presence of truncation; the correct behavior is that truncation does not happen. No replacement case added yet.

### M5: `artifact-dir-metadata`
**Tests:** `meta.json` written under `~/.agent-mux/dispatches/<id>/`, events.jsonl exists, status.json written
**Prompt:** `"Create a file called proof.txt containing 'exists'"`
**Evaluators:**
- `statusIs("completed")`
- Derive `dispatch_id` from result JSON; read `~/.agent-mux/dispatches/<id>/meta.json`: assert `dispatch_id`, `engine`, `model`, `started_at` all present
- Assert `events.jsonl` exists in artifact dir
- Assert `status.json` exists with `state == "completed"`

**Missing cases (noted for future coverage):**
- `result.json` persistence: assert `~/.agent-mux/dispatches/<id>/result.json` exists and contains correct status/response fields
- `preview result_metadata`: assert `agent-mux preview` stdout includes `result_metadata` shape with `dispatch_id`, `artifact_dir`

### M6: `stdin-json-dispatch`
**Tests:** --stdin mode accepts JSON dispatch spec and completes
**Implementation:** Use `dispatchWithFlags` passing `--stdin --yes` with JSON on stdin containing engine/model/prompt/cwd
**Evaluators:**
- `statusIs("completed")`
- `responseContains("4")` (prompt: "What is 2+2?")

### M7: `preview-dry-run`
**Tests:** `preview` command returns dispatch spec without executing
**Implementation:** Run `agent-mux preview --engine codex --model gpt-5.4-mini "test prompt"`
**Evaluators:**
- Exit code 0
- Parse stdout JSON: assert `kind == "preview"`
- Assert `dispatch_spec.engine == "codex"`
- Assert `prompt.chars > 0`
- Assert `confirmation_required` field exists

### M8: `lifecycle-list-status-inspect`
**Tests:** Multi-stage: dispatch → list → status → inspect → verify consistency
**Step 1:** Dispatch simple task, capture dispatch_id
**Step 2:** Run `agent-mux list --json --limit 5` → assert dispatch_id appears
**Step 3:** Run `agent-mux status --json <id>` → assert status == completed
**Step 4:** Run `agent-mux inspect --json <id>` → assert record, response, artifact_dir, meta all present

### M10: `config-introspection`
**Tests:** `config`, `config roles --json`, `config skills --json` all return valid JSON
**Implementation:** Run each subcommand, parse output, assert non-empty and structurally valid
**Evaluators:**
- `config`: has `defaults`, `timeout`, `_sources` keys
- `config roles --json`: array with at least one entry having `name`, `engine`
- `config skills --json`: array (may be empty but valid JSON)
### M11: `handoff-summary-extraction`
**Tests:** Worker response with `## Summary` header gets extracted to handoff_summary correctly
**Prompt:** `"Write a response with this exact structure:\n## Summary\nThe answer is HANDOFF_CANARY_4488.\n## Details\nMore text here."`
**Evaluators:**
- `statusIs("completed")`
- Parse raw stdout: assert `handoff_summary` contains `HANDOFF_CANARY_4488`
- Assert `handoff_summary` does NOT contain `More text here`

### M12: `async-host-pid-status-json`
**Tests:** Async dispatch writes host.pid and status.json immediately after ack
**Step 1:** Dispatch with --async, parse ack for artifact_dir
**Step 2:** Immediately check artifact_dir for host.pid (file exists, contains numeric PID)
**Step 3:** Check status.json exists with state "running" or "initializing"
**Step 4:** Collect result normally

### M13: `skill-scripts-on-path`
**Tests:** Skill scripts/ directory is added to PATH so worker can execute skill scripts
**Setup:** Create fixture skill `scripts-test` with `scripts/canary-script.sh` that echoes `SCRIPT_PATH_CANARY_5566`
**Prompt:** `"Run canary-script.sh and report its output verbatim."`
**ExtraFlags:** `["--skill=scripts-test"]`
**Evaluators:**
- `statusIs("completed")`
- `responseContains("SCRIPT_PATH_CANARY_5566")`

---

---

## Part 4: Priority Ranking

| Priority | Case | Coverage Added | Effort |
|----------|------|----------------|--------|
| **P0** | M1: output-contract-schema | Catches any JSON contract regression — every caller depends on this | Low |
| **P0** | M5: artifact-dir-metadata | Catches metadata write failures silently breaking recovery + inspect | Low |
| **P0** | M8: lifecycle-list-status-inspect | First test of the entire lifecycle query path agents rely on | Med |
| **P1** | M2: role-system-prompt-delivery | Catches role system prompt silently dropped — breaks all role-based dispatch | Low |
| **P1** | M4: response-truncation | REMOVED — truncation feature removed from codebase | — |
| **P1** | M12: async-host-pid-status-json | Catches async observability failures — orphan detection depends on this | Med |
| **P1** | M6: stdin-json-dispatch | Catches the entire programmatic dispatch path (every coordinator uses this) | Low |
| **P2** | M3: variant-resolution | Catches variant override silently ignored | Low |
| **P2** | M11: handoff-summary-extraction | Catches summary extraction bugs in `handoff_summary` extraction | Low |
| **P2** | M13: skill-scripts-on-path | Catches skill scripts silently unavailable | Low |
| **P2** | M7: preview-dry-run | Catches preview command broken — coordinators use this for pre-flight | Low |
| **P2** | M10: config-introspection | Catches config query commands returning garbage | Low |
