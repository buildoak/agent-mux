# Prompting Guide

## Contents

- General rules
- Codex prompting
- Claude prompting
- Gemini prompting
- Context-loading tools
- Recovery and signal phrasing
- Sequential dispatch patterns

---

## General Rules

Each worker dispatch should provide:

1. One job
2. Explicit scope (files, directories, boundaries)
3. A verification gate (how the worker proves it is done)
4. A clear output shape (what to report back)

Default prompt frame:

```text
Goal:
Scope:
Constraints:
Verification gate:
Expected output:
```

Do not spend tokens explaining agent-mux to the worker. The worker already
has the harness and artifact directory. Prompt the task, not the tool.

---

## Codex Prompting

Codex is the implementation hammer. It wants precision.

### What works

- Exact files or directories
- Concrete edits with clear boundaries
- Specific commands to run
- A hard verification gate
- One deliverable per dispatch

### What fails

- "Explore the repo and tell me what you find"
- Multi-goal prompts with fuzzy priorities
- Hidden verification expectations
- Giant context blobs that should have been a `--context-file`

### Recommended shape

```text
Implement X in these files:
- src/parser.go
- internal/parser/types.go

Constraints:
- do not change public API names
- keep changes under 150 LOC if possible

Verification gate:
- go test ./internal/parser ./cmd/tool passes
- malformed input now returns ErrInvalidToken

Report:
- what changed
- which tests ran
- any follow-up risk
```

### Model guidance

| Model | Use for |
|-------|---------|
| `gpt-5.4` | Primary worker for real code work |
| `gpt-5.4-mini` | Cheaper high-volume parallel workers |
| `gpt-5.3-codex-spark` | Quick scans, light edits, broad fan-out |

Use `--reasoning=high` for most code work. Reserve `xhigh` for deep audits
or very tricky failures.

---

## Claude Prompting

Claude handles ambiguity, planning, synthesis, and review.

### What works

- Open-ended analysis and tradeoff evaluation
- Review framing and assumption challenging
- Prompt crafting for downstream workers
- Writing, restructuring, and documentation

### Recommended shape

```text
Task:
Pressure-test the migration plan for the auth subsystem.

Scope:
- read the plan
- identify the top 3 failure modes
- propose a safer rollout sequence

Output:
- verdict first
- then the revised sequence
- then open questions
```

For read-only work, use `--permission-mode=plan` or the appropriate role.

If the job is implementation at its core, plan in Claude and hand the coding
to Codex via a second dispatch.

---

## Gemini Prompting

Gemini is best as a fast alternate take, not the primary implementation engine.

### Best uses

- Second opinion on a patch or plan
- Quick challenge of assumptions
- Fast comparison against Codex/Claude output

### Recommended shape

```text
Review this patch for hidden risks.

Focus on:
- backwards compatibility
- deployment hazards
- missing tests

Keep the answer to 5 bullets max.
```

Keep Gemini prompts narrower than Claude prompts. Treat as a contrast probe.

---

## Context-Loading Tools

### --skill (JSON: "skills")

Use for reusable runbooks or domain instructions from a named skill directory.
Keeps prompts short and composable.

```json
{"role":"lifter","skills":["react","test-writer"],"prompt":"...","cwd":"/repo"}
```

### --context-file (JSON: "context_file")

Use when the coordinator already wrote a large brief to disk. agent-mux injects a
preamble telling the worker to read `$AGENT_MUX_CONTEXT` before starting.

Good uses: bulky handoff notes, long specs, structured research dumps.

### system_prompt (JSON field)

Use for run-level framing, not giant project manuals:

```json
{"role":"auditor","system_prompt":"Prioritize regressions over style","prompt":"...","cwd":"/repo"}
```

### --profile / "profile" (JSON)

Use for persistent personas with default engine/model settings. Put stable
persona text in the coordinator body, dynamic task details in the prompt.

---

## Recovery and Signal Phrasing

### Recovery

`--recover` / `"recover"` JSON key already builds a continuation prompt
listing old artifacts. Your extra prompt should be the delta, not a re-brief.

Good:
```text
Continue by finishing test coverage for the parser and summarize what remains.
```

Bad:
```text
Re-explain the entire project and restate the old run from scratch.
```

### Signal

Signals become a short resumed turn inside the harness. Keep them crisp.
The `--signal` ack only confirms the inbox write; injection waits for an
event boundary.

Good:
- `Focus on auth paths only; skip docs.`
- `Stop after green tests and summarize remaining edge cases.`
- `Do not touch migrations; stay inside src/parser/.`

Bad:
- Multi-paragraph redesigns
- Contradictory instructions
- Giant specs that should have been written to disk

### Steer

`steer redirect` is the stronger form of signal -- it prefixes the message
with `[REDIRECT]` and attempts stdin FIFO injection (Codex) before falling
back to inbox. Use it when the worker needs to change course mid-flight.

`steer nudge` is gentler -- default message is "Please wrap up your current
work and provide a final summary."

---

## Sequential Dispatch Patterns

For multi-step work, use explicit dispatch/wait/result sequences. Each step
is a separate `--stdin` dispatch with the coordinator reading results between steps.

### Pattern: Scout then Implement

```bash
# Step 1: Scout for context
printf '{"role":"scout","prompt":"Find all auth-related files and their dependencies","cwd":"/repo"}' \
  | agent-mux --stdin --async

# Wait for scout to finish
agent-mux wait <scout_dispatch_id>

# Read scout result
SCOUT_RESULT=$(agent-mux result <scout_dispatch_id>)

# Step 2: Implement with scout context
printf '{"role":"lifter","prompt":"Implement retry logic. Context from scout:\n%s","cwd":"/repo"}' "$SCOUT_RESULT" \
  | agent-mux --stdin --async
```

### Pattern: Implement then Audit

```bash
# Step 1: Implement
printf '{"role":"lifter","prompt":"Add rate limiting to API endpoints","cwd":"/repo"}' \
  | agent-mux --stdin

# Check result
agent-mux result <impl_id> --json | jq -r '.status'

# Step 2: Audit the changes
printf '{"role":"auditor","prompt":"Review the rate limiting implementation for edge cases","cwd":"/repo"}' \
  | agent-mux --stdin
```

### Pattern: Fan-out with Multiple Workers

```bash
# Dispatch N parallel workers
for module in auth billing payments; do
  printf '{"role":"grunt","prompt":"Scan %s module for security issues","cwd":"/repo"}' "$module" \
    | agent-mux --stdin --async
done

# Wait and collect results
for id in $DISPATCH_IDS; do
  agent-mux wait "$id"
  agent-mux result "$id"
done
```

### Key Rules

- Each dispatch is independent -- no implicit state sharing between steps
- Use `--async` for parallel work, synchronous for sequential chains
- The coordinator reads results and constructs the next prompt explicitly
- Write large context to disk and use `--context-file` instead of inlining
- Use `session_id` from result metadata as the durable join key across dispatches
