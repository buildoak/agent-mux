# AX Evaluation Manifest

Single source of truth for agent-mux agent experience testing. Add a test = add a section. Edit a checklist = edit this doc.

## How This Works

The coordinator reads this manifest, dispatches agents with scenarios, evaluates their responses against checklists, and reports scores. No compiled test code. No separate judge dispatch. The coordinator IS the evaluator — it reads the response and checks each item.

## Evaluation Protocol

For each scenario in the requested tier(s):

1. **Setup** — Prepare materials: generate real dispatch results (L0), construct error JSON from the ErrorCatalog (L1), load SKILL.md (L2), load GSD prompts (L3).
2. **Dispatch** — Send the agent the scenario prompt plus all specified materials. The agent must have enough context to produce a real answer.
3. **Collect** — Capture the agent's full response.
4. **Evaluate** — Walk the checklist. Each item is binary: met (1) or not-met (0).
5. **Score** — `score = items_met / total_items`.
6. **Report** — Per-scenario score, per-tier summary, overall AX Health.

## Scoring

| Rating | Range | Meaning |
|--------|-------|---------|
| Pass | >= 0.7 | Agent demonstrates competence |
| Marginal | 0.5 - 0.69 | Functional but unreliable |
| Fail | < 0.5 | Agent cannot use agent-mux at this tier |

**Tier weights for AX Health:** L0 = 0.15, L1 = 0.30, L2 = 0.35, L3 = 0.20.

`AX Health = (L0_avg * 0.15) + (L1_avg * 0.30) + (L2_avg * 0.35) + (L3_avg * 0.20)`

---

## L0 -- Contract Comprehension

Tests whether an agent can read the output contract and correctly parse/explain dispatch results. Grounding: the agent always receives the full output-contract.md alongside the result JSON.

### L0.1 — Parse completed result

**Setup:** Run a real dispatch to produce a completed result.
```bash
printf '{"engine":"codex","model":"gpt-5.4-mini","effort":"high","prompt":"What is 2+2? Answer with just the number.","cwd":"/tmp","skip_skills":true,"timeout_sec":120}' \
  | agent-mux --stdin --yes 2>/dev/null
```
Capture the stdout JSON. Pretty-print it.

**Materials:** `references/output-contract.md` + the real completed result JSON.

**Prompt to agent:**
```
You are given the output-contract documentation for agent-mux and a real dispatch result JSON.

Your task:
1. Parse every top-level field in the result JSON.
2. For each field, explain what it means according to the documentation.
3. Confirm the status value is valid per the contract.
4. Confirm metadata.engine and metadata.model are present and valid.
5. Confirm activity has all four required arrays (files_changed, files_read, commands_run, tool_calls).
6. State whether schema_version is correct.
7. Flag any field that appears confusing or misaligned with the docs.

Be precise. Name every field you see.

## Output Contract Documentation
{output-contract.md content}

## Real Dispatch Result
{pretty-printed result JSON}
```

**Checklist (10 items):**
- [ ] Correctly identifies `schema_version` as 1
- [ ] Correctly identifies `status` as "completed" and explains its meaning
- [ ] Identifies and explains `dispatch_id`, `dispatch_salt`, `trace_token` as distinct fields
- [ ] Explains that `response` contains the worker's answer text
- [ ] Identifies the `activity` object with its four arrays (`files_changed`, `files_read`, `commands_run`, `tool_calls`)
- [ ] Identifies the `metadata` object and explains `engine`, `model`, `tokens`, `turns`
- [ ] Identifies `duration_ms` as end-to-end duration in milliseconds
- [ ] Notes that `error` is null for completed dispatches
- [ ] Explains `handoff_summary` as extracted from `## Summary`/`## Handoff` headers
- [ ] Does NOT hallucinate field meanings that contradict the documentation

---

### L0.2 — Parse failed result

**Setup:** Run a dispatch designed to fail.
```bash
printf '{"engine":"bogus_engine","model":"gpt-5.4-mini","effort":"high","prompt":"This should fail.","cwd":"/tmp","skip_skills":true,"timeout_sec":30}' \
  | agent-mux --stdin --yes 2>/dev/null
```
Capture stdout (non-zero exit expected).

**Materials:** `references/output-contract.md` + the real failed result JSON.

**Prompt to agent:**
```
You are given the output-contract documentation for agent-mux and a real dispatch result that FAILED.

Your task:
1. Parse every top-level field in the result JSON.
2. Identify that the status is "failed" and explain what that means.
3. Parse the error object: identify code, message, suggestion, hint, example, retryable fields.
4. Based on the error.retryable field, state whether this error can be retried.
5. Based on error.hint and error.example, suggest what the caller should do next.
6. Confirm the metadata and activity objects are present even on failure.

Be precise. Name every field you see.

## Output Contract Documentation
{output-contract.md content}

## Real Failed Dispatch Result
{pretty-printed result JSON}
```

**Checklist (8 items):**
- [ ] Identifies that `status` is "failed"
- [ ] Parses the error object and identifies the error code
- [ ] Explains what the error code means per the error code table
- [ ] Identifies `hint` and `example` fields in the error object
- [ ] States whether the error is retryable based on the `retryable` field
- [ ] Suggests a corrective action derived from `hint`/`example`
- [ ] Does NOT hallucinate error codes or field meanings
- [ ] Notes that `metadata` and `activity` are present even on failed results

---

### L0.3 — Field meaning accuracy

**Setup:** No live dispatch needed. Documentation-only test.

**Materials:** `references/output-contract.md`.

**Prompt to agent:**
```
You are given the output-contract documentation for agent-mux.

Answer these specific questions precisely:

1. What is the difference between "response" and "handoff_summary"?
2. What is the difference between "dispatch_id" and "trace_token"?
3. What does "dispatch_salt" look like and what is it for?
4. When is "full_output_path" set vs null?
5. What three values can "status" take and what does each mean?
6. What is the difference between "partial" and "recoverable"?
7. In the error object, what is the difference between "hint", "example", and "suggestion"?

## Output Contract Documentation
{output-contract.md content}
```

**Checklist (7 items):**
- [ ] `response` is the full worker response text; `handoff_summary` is extracted from `## Summary`/`## Handoff` headers or is a shortened version. They are NOT the same field
- [ ] `dispatch_id` is a ULID; `trace_token` is `AGENT_MUX_GO_` + dispatch_id. Related but distinct
- [ ] `dispatch_salt` is `adjective-noun-digit` format (e.g. "mint-ant-five") for human-greppable identification
- [ ] `full_output_path` is set when response was truncated due to `response_max_chars`; null otherwise
- [ ] `status` values: `completed` (clean exit), `timed_out` (timeout), `failed` (validation/startup/adapter error)
- [ ] `partial` and `recoverable` appear on `timed_out` runs; partial means work is incomplete, recoverable means it can be resumed
- [ ] `hint` is guidance text, `example` is a corrective command example, `suggestion` is the backward-compat concatenation of hint + " " + example

---

### L0.4 — Pipeline result vs dispatch result

**Setup:** No live dispatch needed. Documentation-only test.

**Materials:** `references/output-contract.md`.

**Prompt to agent:**
```
You are given the output-contract documentation for agent-mux.

A coordinator dispatches work in two ways: single dispatch and pipeline. Answer:

1. What top-level key distinguishes a PipelineResult from a DispatchResult?
2. What are the three possible pipeline status values and how do they differ from dispatch status values?
3. In a pipeline, what does each worker result contain? List the fields.
4. What is `handoff_mode` and what values can it take?
5. If you call `agent-mux -P=build -C=/repo "task"`, should you parse the result as a DispatchResult or PipelineResult?

## Output Contract Documentation
{output-contract.md content}
```

**Checklist (5 items):**
- [ ] Identifies `pipeline_id` and `steps` as distinguishing keys (PipelineResult has `pipeline_id` + `steps[]`; DispatchResult has `dispatch_id` + `response`)
- [ ] Pipeline statuses: `completed` (no failures), `partial` (some failed but each step had at least one success), `failed` (a step had zero successes)
- [ ] Worker result fields: `worker_index`, `dispatch_id`, `status`, `summary`, `artifact_dir`, `output_file`, `duration_ms` (plus `error_code`/`error_msg` on failure)
- [ ] `handoff_mode` controls how output flows between steps: `summary_and_refs`, `full_concat`, `refs_only`
- [ ] `-P=build` returns a PipelineResult, NOT a DispatchResult

---

## L1 -- Error Self-Correction

Tests whether an agent can read an error response (with code, message, hint, example) and produce a corrected command. The ErrorCatalog's hint+example fields are the AX feature under test.

**Common prompt template for all L1 scenarios:**
```
You ran the following agent-mux command:

{original_command}

And got this error response (JSON):

{error_json}

Using the error's hint and example fields, write the corrected command that would avoid this error.
Explain briefly WHY the original command failed and what you changed.

Write the corrected command on its own line starting with "CORRECTED: ".
```

### L1.1 — engine_not_found

**Original command:** `agent-mux -e openai --cwd /repo "Fix the bug in parser.go"`

**Error JSON:**
```json
{
  "code": "engine_not_found",
  "message": "Unknown engine name.",
  "suggestion": "agent-mux only supports the built-in engines codex, claude, and gemini. Retry with a valid engine. Example: agent-mux -e codex --cwd /repo \"<prompt>\".",
  "hint": "agent-mux only supports the built-in engines codex, claude, and gemini.",
  "example": "Retry with a valid engine. Example: agent-mux -e codex --cwd /repo \"<prompt>\".",
  "retryable": true
}
```

**Checklist (5 items):**
- [ ] Uses a valid engine (`codex`, `claude`, or `gemini`)
- [ ] Preserves the original prompt intent ("Fix the bug in parser.go")
- [ ] Includes `--cwd`
- [ ] Command is syntactically valid
- [ ] Explains WHY the original engine ("openai") was wrong

---

### L1.2 — model_not_found

**Original command:** `agent-mux -e codex -m gpt-4-turbo --cwd /repo "Scan for SQL injection"`

**Error JSON:**
```json
{
  "code": "model_not_found",
  "message": "Unknown model for engine.",
  "suggestion": "The selected model is not available for the current engine. Retry with a supported model. Example: agent-mux -e codex -m gpt-5.4 --cwd /repo \"<prompt>\".",
  "hint": "The selected model is not available for the current engine.",
  "example": "Retry with a supported model. Example: agent-mux -e codex -m gpt-5.4 --cwd /repo \"<prompt>\".",
  "retryable": true
}
```

**Checklist (5 items):**
- [ ] Uses a valid model for codex engine (e.g. `gpt-5.4`, `gpt-5.4-mini`)
- [ ] Keeps the same engine (`codex`)
- [ ] Preserves the original prompt
- [ ] Uses the hint to understand the model was wrong for this engine
- [ ] Command is syntactically valid

---

### L1.3 — invalid_args (missing cwd)

**Original command:** `agent-mux -e codex "Fix failing tests"`

**Error JSON:**
```json
{
  "code": "invalid_args",
  "message": "Invalid dispatch arguments.",
  "suggestion": "The dispatch request is missing required fields or contains invalid flag combinations. Provide a valid engine, prompt, and working directory. Example: agent-mux -e codex --cwd /repo \"Fix failing test\".",
  "hint": "The dispatch request is missing required fields or contains invalid flag combinations.",
  "example": "Provide a valid engine, prompt, and working directory. Example: agent-mux -e codex --cwd /repo \"Fix failing test\".",
  "retryable": true
}
```

**Checklist (4 items):**
- [ ] Includes `--cwd` with a directory path
- [ ] Preserves the original engine and prompt
- [ ] Identifies that `--cwd` was the missing required field
- [ ] Command is syntactically valid

---

### L1.4 — frozen_killed

**Original command:** `agent-mux -e codex --cwd /repo "Analyze every file in this repository and write comprehensive documentation"`

**Error JSON:**
```json
{
  "code": "frozen_killed",
  "message": "Worker killed after prolonged silence.",
  "suggestion": "Worker was killed after prolonged silence - likely stuck in a hanging tool call. Partial work was preserved in the artifact directory. Retry with a narrower task: agent-mux -R=lifter --cwd /repo \"<narrowed prompt>\". Or extend silence timeout: add silence_kill_seconds=300 to config.",
  "hint": "Worker was killed after prolonged silence - likely stuck in a hanging tool call. Partial work was preserved in the artifact directory.",
  "example": "Retry with a narrower task: agent-mux -R=lifter --cwd /repo \"<narrowed prompt>\". Or extend silence timeout: add silence_kill_seconds=300 to config.",
  "retryable": true
}
```

**Checklist (5 items):**
- [ ] Corrected command has a narrower/more specific prompt than the original
- [ ] Still targets the same general goal (documentation or analysis)
- [ ] Either narrows the prompt OR extends the timeout (not blind retry)
- [ ] Recognizes this was a frozen/stuck issue, not a prompt syntax error
- [ ] Command is syntactically valid

---

### L1.5 — config_error (bad role)

**Original command:** `agent-mux -R=super-worker --cwd /repo "Build the feature"`

**Error JSON:**
```json
{
  "code": "config_error",
  "message": "Configuration is invalid.",
  "suggestion": "agent-mux could not load or validate the referenced config, role, or control path. Fix the config file or role name, then retry. Example: agent-mux -R lifter --config /path/to/agent-mux.yaml --cwd /repo \"<prompt>\".",
  "hint": "agent-mux could not load or validate the referenced config, role, or control path.",
  "example": "Fix the config file or role name, then retry. Example: agent-mux -R lifter --config /path/to/agent-mux.yaml --cwd /repo \"<prompt>\".",
  "retryable": true
}
```

**Checklist (4 items):**
- [ ] Uses a different (plausibly valid) role name (e.g. `lifter`, `scout`, `architect`)
- [ ] Suggests checking available roles (`agent-mux config roles`) or acknowledges the role was invalid
- [ ] Preserves the original prompt intent
- [ ] Command is syntactically valid

---

### L1.6 — max_depth_exceeded (not retryable)

**Original command:** `agent-mux -e codex --cwd /repo "Recursively analyze all modules"`

**Error JSON:**
```json
{
  "code": "max_depth_exceeded",
  "message": "Max dispatch depth reached.",
  "suggestion": "This task tried to spawn more nested dispatches than the configured safety limit allows. Complete the work in the current agent, or raise the depth limit only if the nesting is intentional.",
  "hint": "This task tried to spawn more nested dispatches than the configured safety limit allows.",
  "example": "Complete the work in the current agent, or raise the depth limit only if the nesting is intentional.",
  "retryable": false
}
```

**Checklist (4 items):**
- [ ] Recognizes this is NOT retryable (`retryable: false`)
- [ ] Suggests completing work in the current agent instead of nesting, OR raising the depth limit with appropriate caution
- [ ] Does NOT simply retry the same command unchanged
- [ ] Explains the structural nature of the failure (depth limit, not a transient error)

---

### L1.7 — startup_failed

**Original command:** `agent-mux -e gemini --cwd /repo "Research market trends"`

**Error JSON:**
```json
{
  "code": "startup_failed",
  "message": "Harness process failed to start.",
  "suggestion": "The harness process failed before a working session started. Check the harness install and arguments, then retry. Example: verify the engine binary runs directly from the same shell.",
  "hint": "The harness process failed before a working session started.",
  "example": "Check the harness install and arguments, then retry. Example: verify the engine binary runs directly from the same shell.",
  "retryable": true
}
```

**Checklist (4 items):**
- [ ] Suggests verifying the engine binary (`gemini`) is installed and on PATH
- [ ] Suggests a diagnostic step (not just blind retry)
- [ ] Includes a verification/check before retrying
- [ ] Does NOT just retry the exact same command without investigation

---

## L2 -- Skill Comprehension

Tests whether an agent can read the full SKILL.md and produce valid, grounded dispatch plans. Every scenario injects the complete skill documentation as context -- the failure mode being tested is planning quality, not context availability.

**Common materials for all L2 scenarios:** The full content of `skill/SKILL.md`.

**Common system prompt for dispatch:**
```
You are a coordinator agent. You plan and dispatch agent-mux commands. Show exact commands.
```

### L2.1 — Audit-fix-verify pipeline

**Prompt to agent:**
```
You have access to agent-mux. Here is the complete skill documentation:

{skill/SKILL.md content}

---

Now complete this task:

You are a coordinator agent with access to agent-mux. A user asks you to:
"Audit the authentication module in /home/user/webapp for security issues, fix any critical issues found, and verify the fixes pass tests."

Plan a 3-step pipeline using agent-mux. Show the exact commands you would run.
Each step should use --async, wait for completion, and check the result before proceeding.
```

**Checklist (11 items):**
- [ ] Uses `--async` for each dispatch
- [ ] Uses `agent-mux wait --poll <duration> <id>` to wait (NOT polling `steer status` in a loop)
- [ ] Uses `agent-mux result <id> --json` to collect results
- [ ] Uses `--cwd` or `-C=` to set working directory
- [ ] Uses valid engines (`codex`, `claude`, or `gemini`)
- [ ] Uses roles (`-R=`) or at minimum valid engine flags
- [ ] Does NOT use invalid flags (`--sandbox none`, `--output`, or other non-existent flags)
- [ ] Each step has a distinct, specific prompt (not vague)
- [ ] Redirects stderr (`2>/dev/null`) on agent-mux calls
- [ ] Checks `status` in the result before proceeding to the next step
- [ ] 3-step flow is logically ordered (audit -> fix -> verify)

---

### L2.2 — Parallel fan-out with synthesis

**Prompt to agent:**
```
You have access to agent-mux. Here is the complete skill documentation:

{skill/SKILL.md content}

---

Now complete this task:

You are a coordinator agent with access to agent-mux. A user asks you to:
"Research three different approaches to implementing rate limiting: token bucket, sliding window, and leaky bucket. Then synthesize the findings into a recommendation."

Plan a multi-step pipeline:
- Step 1: Fan out 3 parallel research dispatches (one per approach).
- Step 2: Synthesize all three results into a recommendation.

Show the exact commands.
```

**Checklist (10 items):**
- [ ] Step 1 dispatches exactly 3 parallel `--async` commands
- [ ] Waits for all 3 to complete before starting Step 2
- [ ] Collects results from all 3 dispatches (`result <id> --json`)
- [ ] Step 2 receives context from Step 1 results (via prompt injection or `--context-file`)
- [ ] Uses appropriate roles/engines (research -> Claude or Codex; synthesis -> Claude)
- [ ] Uses `--cwd` for each dispatch
- [ ] Does NOT use invalid flags
- [ ] Redirects stderr (`2>/dev/null`)
- [ ] Handles the case where a research dispatch fails (at least acknowledges the possibility)
- [ ] Prompts are specific to each approach (not generic copy-paste with different names only)

---

### L2.3 — Recovery workflow

**Prompt to agent:**
```
You have access to agent-mux. Here is the complete skill documentation:

{skill/SKILL.md content}

---

Now complete this task:

You are a coordinator agent with access to agent-mux. A user asks you to:
"Implement the payment processing module. If the worker times out, recover and continue from where it left off."

Plan a dispatch with recovery using agent-mux:
- Step 1: Dispatch the initial implementation task.
- Step 2: Check the result. If timed_out with files_changed, use --recover to continue.
- Step 3: If timed_out with empty files_changed, reframe with a narrower scope.

Show the exact commands and decision logic.
```

**Checklist (10 items):**
- [ ] Dispatches the initial task with `--async`
- [ ] Uses `wait` to wait for completion
- [ ] Checks the `status` field in the result
- [ ] Differentiates between `timed_out` + `files_changed` non-empty vs `timed_out` + `files_changed` empty
- [ ] For timed_out with files_changed: uses `--recover=<dispatch_id>`
- [ ] For timed_out with empty files_changed: reframes with a narrower prompt
- [ ] Does NOT blindly retry the same prompt on timeout
- [ ] Uses valid agent-mux syntax throughout
- [ ] Mentions checking `activity.files_changed` specifically
- [ ] Overall recovery flow matches the skill doc's failure decision tree

---

### L2.4 — Steering mid-flight

**Prompt to agent:**
```
You have access to agent-mux. Here is the complete skill documentation:

{skill/SKILL.md content}

---

Now complete this task:

You are a coordinator agent with access to agent-mux. A worker is running a long analysis task that you dispatched 5 minutes ago. You realize the scope needs to be narrowed.

Using agent-mux, show the exact commands to:
1. Check if the worker is still running.
2. Redirect the worker to a narrower scope.
3. Wait for the updated result.
4. If redirect fails, abort and redispatch with the new scope.

The dispatch ID is "01KMY3ABC".
```

**Checklist (8 items):**
- [ ] Uses `agent-mux steer 01KMY3ABC status` for the live check (NOT a loop)
- [ ] Uses `agent-mux steer 01KMY3ABC redirect "<message>"` with a specific redirect message
- [ ] Uses `agent-mux wait --poll <duration> 01KMY3ABC` to wait after redirect
- [ ] Has a fallback using `agent-mux steer 01KMY3ABC abort` if redirect fails
- [ ] Abort fallback includes a redispatch with narrower scope
- [ ] Redirects stderr (`2>/dev/null`) on all commands
- [ ] Does NOT poll `steer status` in a loop (uses `wait` instead)
- [ ] Steer commands have correct syntax (subcommand before arguments)

---

### L2.5 — Role and context passing

**Prompt to agent:**
```
You have access to agent-mux. Here is the complete skill documentation:

{skill/SKILL.md content}

---

Now complete this task:

You are a coordinator agent with access to agent-mux. A user asks you to:
"We have a detailed specification in /tmp/spec.md (2000 lines). Have a scout quickly scan it, then have an architect plan the implementation, then have a lifter implement the first module."

Plan this 3-step pipeline using agent-mux roles and context passing.
Show the exact commands, including how context flows between steps.
```

**Checklist (10 items):**
- [ ] Uses `-R=scout` for the first step (scanning)
- [ ] Uses `-R=architect` for the second step (planning)
- [ ] Uses `-R=lifter` for the third step (implementation)
- [ ] Passes the spec to the first step via `--context-file` or prompt inclusion
- [ ] Passes output from step 1 to step 2 (via `--context-file`, prompt injection, or result piping)
- [ ] Passes output from step 2 to step 3
- [ ] Uses `--async` + `wait` + `result` collection pattern
- [ ] Uses `--cwd` for each dispatch
- [ ] Does NOT use roles that don't exist in the skill doc
- [ ] Role selection is appropriate for each step's cognitive demand

---

## L3 -- GSD Comprehension

Tests whether GSD-Heavy and GSD-Light agents, given their actual system prompts plus the skill documentation, produce dispatch plans aligned with their defined cognitive style. The key distinction: Heavy designs strategy; Light executes mechanics.

### L3.1 — Heavy: novel problem requiring strategy

**Materials:** GSD-Heavy prompt (`coordinator/.claude/agents/gsd-heavy.md`) + `skill/SKILL.md`.

**System prompt for dispatch:** The full GSD-Heavy agent definition (truncated to 8000 chars if needed).

**Prompt to agent:**
```
A user says: "I need to reverse-engineer an undocumented API by analyzing network traffic logs, then generate a client SDK from the findings."

You are GSD-Heavy. This is a novel problem with no clear pipeline.
Plan your approach using agent-mux. Show:
1. How you would explore the problem (which engine/role for what).
2. Your dispatch commands with exact flags.
3. Your verification gates.

You have access to agent-mux. Here is the agent-mux skill documentation:

{skill/SKILL.md content}
```

**Checklist (10 items):**
- [ ] Plan shows strategic thinking (not just mechanical step execution)
- [ ] Uses appropriate roles (e.g. `researcher`/`explorer` for analysis, `lifter` for implementation)
- [ ] Uses `--async` + `wait` + `result` collection pattern
- [ ] Defines verification gates (concrete, testable conditions for "done")
- [ ] Uses valid agent-mux syntax and flags
- [ ] Uses `--cwd` for dispatches
- [ ] Shows awareness of engine cognitive styles (Claude for exploration, Codex for precision)
- [ ] Has a clear multi-step structure with decision points
- [ ] Handles potential failures between steps (not just happy path)
- [ ] Does NOT use invalid flags (`--sandbox none`, `--output`, etc.)

---

### L3.2 — Light: known pipeline execution

**Materials:** GSD-Light prompt (`coordinator/.claude/agents/gsd-light.md`) + `skill/SKILL.md`.

**System prompt for dispatch:** The full GSD-Light agent definition.

**Prompt to agent:**
```
A user says: "Run the standard build pipeline: have an architect plan the changes to add retry logic to the HTTP client, then have a lifter implement it, then have an auditor verify it."

You are GSD-Light. This is a known pipeline (plan -> implement -> verify).
Execute the plan using agent-mux. Show the exact commands.

You have access to agent-mux. Here is the agent-mux skill documentation:

{skill/SKILL.md content}
```

**Checklist (10 items):**
- [ ] Follows a clear sequential pipeline (plan -> implement -> verify)
- [ ] Uses appropriate roles (`-R=architect`, `-R=lifter`, `-R=auditor`)
- [ ] Uses `--async` + `wait` + `result` collection for each step
- [ ] Passes context from one step to the next (via `--context-file`, prompt, or result)
- [ ] Uses valid agent-mux syntax throughout
- [ ] Uses `--cwd` for each dispatch
- [ ] Checks result `status` before proceeding to next step
- [ ] Redirects stderr (`2>/dev/null`)
- [ ] Shows mechanical execution style, not strategic pivoting
- [ ] Does NOT over-engineer or add unnecessary steps

---

### L3.3 — Heavy: dispatch with anticipated recovery

**Materials:** GSD-Heavy prompt + `skill/SKILL.md`.

**System prompt for dispatch:** The full GSD-Heavy agent definition (truncated to 8000 chars if needed).

**Prompt to agent:**
```
A user says: "Migrate the database schema from v2 to v3. The migration involves 15 tables and typically takes the worker 20+ minutes, so timeouts are expected."

You are GSD-Heavy. Plan how you would handle this with agent-mux, knowing workers will likely time out.
Show your dispatch plan with recovery strategy.

You have access to agent-mux. Here is the agent-mux skill documentation:

{skill/SKILL.md content}
```

**Checklist (10 items):**
- [ ] Anticipates timeouts and plans for them upfront (not reactive)
- [ ] Breaks the work into smaller chunks (e.g. groups of tables) OR uses `--recover` for continuation
- [ ] Checks `activity.files_changed` to decide whether to recover or reframe
- [ ] Uses appropriate effort tiers (`high` or `xhigh` for long tasks)
- [ ] Sets appropriate timeouts or uses roles with long timeouts
- [ ] Uses valid agent-mux syntax
- [ ] Has a clear escalation path (what to do if recovery also fails)
- [ ] Plan demonstrates strategic depth, not just mechanical retry
- [ ] Uses `--async` + `wait` + `result` collection pattern
- [ ] Follows the two-failure rule (after two failures on same step, stop and reframe or escalate)

---

### L3.4 — Light: parallel fan-out execution

**Materials:** GSD-Light prompt + `skill/SKILL.md`.

**System prompt for dispatch:** The full GSD-Light agent definition.

**Prompt to agent:**
```
A user says: "Scan these 4 microservices for deprecated API usage: auth-service, user-service, billing-service, notification-service. All are in /home/user/services/<name>/."

You are GSD-Light. Execute parallel scans using agent-mux.
Show the exact commands for parallel dispatch and result collection.

You have access to agent-mux. Here is the agent-mux skill documentation:

{skill/SKILL.md content}
```

**Checklist (10 items):**
- [ ] Dispatches exactly 4 parallel `--async` commands
- [ ] Each dispatch targets a different service directory via `--cwd`
- [ ] Uses an appropriate role (`-R=scout` for scanning)
- [ ] Waits for all 4 to complete
- [ ] Collects results from all 4 dispatches
- [ ] Aggregates or summarizes the findings
- [ ] Uses valid agent-mux syntax
- [ ] Redirects stderr (`2>/dev/null`)
- [ ] Handles the case where one scan fails
- [ ] Prompts are specific (not generic "scan this service")

---

## L4 -- Hard (manual trigger only)

Live dispatch tests. Require real agent-mux execution, real API calls, real time. Run interactively when needed, not in routine evaluation.

### L4.1 — Live dispatch + result parsing

**Protocol:** Dispatch a real scout task. Verify the result JSON parses correctly. Verify the response is substantive. Verify files_changed/files_read are populated if the worker touched files.

**Trigger:** Manual only. `ax-eval --tier L4 --scenario 4.1`

### L4.2 — Live recovery after timeout

**Protocol:** Dispatch a deliberately broad task with a short timeout. Wait for timeout. Verify the timed_out result. If files_changed is non-empty, issue `--recover` continuation. Verify the recovery dispatch completes.

**Trigger:** Manual only. `ax-eval --tier L4 --scenario 4.2`

### L4.3 — Live steering

**Protocol:** Dispatch a long-running task. After 30 seconds, issue a redirect steer. Verify the steer acknowledgment. Wait for completion. Verify the response reflects the redirected scope.

**Trigger:** Manual only. `ax-eval --tier L4 --scenario 4.3`

### L4.4 — Live parallel fan-out

**Protocol:** Dispatch 3 parallel scouts. Wait for all 3. Collect all 3 results. Verify each completed independently. Verify dispatch IDs are distinct.

**Trigger:** Manual only. `ax-eval --tier L4 --scenario 4.4`
