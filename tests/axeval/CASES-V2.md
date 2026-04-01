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
| response_max_chars truncation | yes | response-truncation | — |
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
| Artifact dir creation + _dispatch_meta.json | **no** | — | Metadata silently missing |
| full_output.md fallback (via result cmd) | yes | response-truncation | — |
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
| gc (--older-than, --dry-run) | **no** | — | Store grows unbounded |
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
| config introspection (roles, pipelines, skills, models) | **no** | — | Agents can't discover capabilities |
| **Pipelines** | | | |
| Sequential 2-step execution | partial | pipeline-e2e (accepts failure) | — |
| Handoff modes (summary_and_refs vs full_concat vs refs_only) | **no** | — | Wrong context passed between steps |
| Per-step role overrides | **no** | — | Step role silently ignored |
| Fan-out (parallel > 1) | **no** | — | Parallel workers broken |
| **Error Handling** | | | |
| engine_not_found | yes | bad-engine | — |
| model_not_found | yes | bad-model | — |
| frozen_killed | yes | freeze-watchdog | — |
| max_depth_exceeded | **no** | — | Recursive dispatches loop forever |

---

## Part 2: New Test Cases

### M1: `output-contract-schema`
**Tests:** JSON output contract fields match spec (schema_version, dispatch_id, salt, trace_token, activity, metadata, artifacts)
**Prompt:** `"What is 2+2? Answer with just the number."`
**Evaluators:**
- `statusIs("completed")`
- Parse raw stdout JSON: assert `schema_version == 1`
- Assert `dispatch_id` is non-empty ULID format
- Assert `dispatch_salt` matches `word-word-word` pattern
- Assert `trace_token` starts with `AGENT_MUX_GO_`
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

### M4: `response-truncation`
**Tests:** response_max_chars truncates response, writes full_output.md, sets response_truncated=true
**Prompt:** `"Write exactly 500 words about the Go programming language. Do not stop early."`
**EngineOpts:** `response_max_chars: 200`
**Evaluators:**
- `statusIs("completed")`
- Parse raw stdout: assert `response_truncated == true`
- Parse raw stdout: assert `full_output_path` is non-empty string
- Assert file at `full_output_path` exists and len > 200

### M5: `artifact-dir-metadata`
**Tests:** _dispatch_meta.json written with correct fields, events.jsonl exists, status.json written
**Prompt:** `"Create a file called proof.txt containing 'exists'"`
**Evaluators:**
- `statusIs("completed")`
- Read `_dispatch_meta.json` from artifact dir: assert `dispatch_id`, `engine`, `model`, `started_at`, `ended_at`, `status == "completed"` all present
- Assert `events.jsonl` exists in artifact dir
- Assert `status.json` exists with `state == "completed"`

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

### M9: `gc-dry-run`
**Tests:** gc --dry-run lists candidates without deleting
**Step 1:** Dispatch simple task
**Step 2:** Run `agent-mux gc --dry-run --older-than 0h` → assert `kind == "gc_dry_run"`, `would_remove >= 1`
**Step 3:** Run `agent-mux list --json` → assert dispatch still present (not deleted)

### M10: `config-introspection`
**Tests:** `config`, `config roles --json`, `config skills --json`, `config pipelines --json` all return valid JSON
**Implementation:** Run each subcommand, parse output, assert non-empty and structurally valid
**Evaluators:**
- `config`: has `defaults`, `timeout`, `_sources` keys
- `config roles --json`: array with at least one entry having `name`, `engine`
- `config skills --json`: array (may be empty but valid JSON)
- `config pipelines --json`: array

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

## Part 3: Pipeline Test Designs

### P1: `pipeline-2step-summary-handoff`
**Tests:** Step 1 output flows to Step 2 via summary_and_refs handoff
**Pipeline config (fixture):**
```toml
[pipelines.test-handoff]
steps = [
  { name = "produce", prompt = "Write a file analysis.md containing exactly 'PIPELINE_CANARY_7721'. Report what you wrote." },
  { name = "consume", prompt = "Read the previous step's output. What canary string did it mention? Report verbatim.", receives = ["produce"], handoff_mode = "summary_and_refs" }
]
```
**Evaluators:**
- Parse pipeline result JSON: `status == "completed"` or `status == "partial"`
- `steps[1].workers[0].summary` contains `PIPELINE_CANARY_7721`
- OR final step response contains `PIPELINE_CANARY_7721`

### P2: `pipeline-2step-refs-only`
**Tests:** refs_only handoff passes file paths but not content
**Pipeline config:**
```toml
[pipelines.test-refs]
steps = [
  { name = "write", prompt = "Create refs_proof.txt containing 'REF_CANARY_8832'" },
  { name = "read", prompt = "You received file references from the previous step. Read them and report what you find.", receives = ["write"], handoff_mode = "refs_only" }
]
```
**Evaluators:**
- Parse pipeline result: both steps have workers
- Step 1 writes refs_proof.txt (check artifact dir)
- Step 2 response references file content or path

### P3: `pipeline-fanout`
**Tests:** parallel > 1 dispatches multiple workers for one step
**Pipeline config:**
```toml
[pipelines.test-fanout]
steps = [
  { name = "parallel-work", prompt = "What is {n}+{n}?", parallel = 2, worker_prompts = ["What is 3+3?", "What is 7+7?"] }
]
```
**Evaluators:**
- Parse pipeline result: `steps[0].workers` has length 2
- `steps[0].succeeded >= 1`
- Worker 0 response contains "6", Worker 1 response contains "14" (or vice versa)

---

## Part 4: Priority Ranking

| Priority | Case | Coverage Added | Effort |
|----------|------|----------------|--------|
| **P0** | M1: output-contract-schema | Catches any JSON contract regression — every caller depends on this | Low |
| **P0** | M5: artifact-dir-metadata | Catches metadata write failures silently breaking recovery + inspect | Low |
| **P0** | M8: lifecycle-list-status-inspect | First test of the entire lifecycle query path agents rely on | Med |
| **P1** | M2: role-system-prompt-delivery | Catches role system prompt silently dropped — breaks all role-based dispatch | Low |
| **P1** | M4: response-truncation | Catches truncation + full_output.md path — breaks large response handling | Low |
| **P1** | M12: async-host-pid-status-json | Catches async observability failures — orphan detection depends on this | Med |
| **P1** | M6: stdin-json-dispatch | Catches the entire programmatic dispatch path (every coordinator uses this) | Low |
| **P1** | P1: pipeline-2step-summary-handoff | First real pipeline handoff test — catches silent context loss between steps | Med |
| **P2** | M3: variant-resolution | Catches variant override silently ignored | Low |
| **P2** | M11: handoff-summary-extraction | Catches summary extraction bugs that corrupt pipeline handoffs | Low |
| **P2** | M13: skill-scripts-on-path | Catches skill scripts silently unavailable | Low |
| **P2** | M7: preview-dry-run | Catches preview command broken — coordinators use this for pre-flight | Low |
| **P2** | M10: config-introspection | Catches config query commands returning garbage | Low |
| **P3** | M9: gc-dry-run | Catches gc deleting when it shouldn't | Low |
| **P3** | P2: pipeline-refs-only | Handoff mode variant coverage | Med |
| **P3** | P3: pipeline-fanout | Fan-out coverage | Med |
