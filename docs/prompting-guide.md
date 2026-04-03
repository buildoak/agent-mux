# Prompting Guide

agent-mux dispatch quality is a direct function of prompt specificity. This guide covers how to write prompts that produce clean results across all three engines.

The core rule: prompt the task, not the tool. The worker already has the harness, the artifact directory, and any injected skills. Your prompt should specify what to do, with what scope, and how to prove it worked.

## General Rules

Every dispatch prompt should provide:

1. **One job** — a single deliverable, not a compound task
2. **Explicit scope** — files, directories, boundaries
3. **A verification gate** — how the worker proves it is done
4. **A clear output shape** — what to report back

Default prompt frame:

```text
Goal:
Scope:
Constraints:
Verification gate:
Expected output:
```

Do not spend tokens explaining agent-mux to the worker. Do not include giant context blobs that should be a `--context-file`.

## Codex Prompting

Codex is the implementation engine. It executes precision. Give it surgical scope and it delivers clean work. Give it ambiguity and it either hallucinates scope or stalls.

### What works

- Exact file paths (never "explore src/")
- Concrete edits with clear boundaries
- Specific commands to run after the edit
- A hard verification gate
- One deliverable per dispatch

### What fails

- "Explore the repo and tell me what you find"
- Multi-goal prompts with fuzzy priorities
- Hidden verification expectations
- Prompts that require the worker to discover its own scope

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
| --- | --- |
| `gpt-5.4` | Primary worker for real code work |
| `gpt-5.4-mini` | Cheaper high-volume parallel workers |
| `gpt-5.3-codex-spark` | Quick scans, light edits, broad fan-out |

Use `--reasoning=high` for most code work. Reserve `xhigh` for deep audits or tricky failures.

### The pre-extraction pattern

For complex tasks, scout first:

1. Use a `scout` or `explorer` to extract: file paths, function signatures, error traces, type definitions
2. Inline the extracted context into the Codex prompt
3. Codex executes with zero exploration overhead

This turns an ambiguous prompt into a surgical one.

## Claude Prompting

Claude resolves ambiguity. It handles planning, synthesis, review, and open-ended analysis.

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

For read-only work, use `--permission-mode=plan` or an appropriate role.

If the job is implementation at its core, plan in Claude and hand the coding to Codex via a second dispatch.

## Gemini Prompting

Gemini is a contrast probe, not the primary implementation engine.

### Best uses

- Second opinion on a patch or plan
- Quick challenge of assumptions
- Fast comparison against Codex or Claude output

### Recommended shape

```text
Review this patch for hidden risks.

Focus on:
- backwards compatibility
- deployment hazards
- missing tests

Keep the answer to 5 bullets max.
```

Keep Gemini prompts narrower than Claude prompts.

### Limitation

Gemini variants are reasoning-only on this machine. No file reads, no commands, no tool calls. All context must be embedded in the prompt or passed via `--context-file`.

## Context-Loading Tools

### --skill (JSON: "skills")

Reusable runbooks from a named skill directory. Keeps prompts short and composable.

```bash
agent-mux -R=lifter --skill react --skill test-writer -C /repo "Implement error boundary"
```

### --context-file (JSON: "context_file")

For large briefs the coordinator wrote to disk. agent-mux injects a preamble telling the worker to read `$AGENT_MUX_CONTEXT` before starting.

Good uses: bulky handoff notes, long specs, structured research dumps.

### system_prompt (JSON field)

Run-level framing, not giant project manuals:

```json
{"role":"auditor","system_prompt":"Prioritize regressions over style","prompt":"...","cwd":"/repo"}
```

### --profile / "profile" (JSON)

Persistent personas with default engine/model settings. Put stable persona text in the profile body, dynamic task details in the prompt.

## Recovery Prompting

`--recover` builds a continuation prompt listing previous artifacts. Your extra prompt should be the delta, not a re-brief.

Good:

```text
Continue by finishing test coverage for the parser and summarize what remains.
```

Bad:

```text
Re-explain the entire project and restate the old run from scratch.
```

## Signal Phrasing

Signals become a short resumed turn inside the harness. Keep them to one crisp sentence.

The `--signal` ack confirms inbox write, not delivery. Injection waits for an event boundary.

Good:

- `Focus on auth paths only; skip docs.`
- `Stop after green tests and summarize remaining edge cases.`
- `Do not touch migrations; stay inside src/parser/.`

Bad:

- Multi-paragraph redesigns
- Contradictory instructions
- Giant specs that should have been written to disk

## Sequential Dispatch Patterns

Multi-step work is expressed as a sequence of dispatches connected by the coordinator — not by pipelines. The coordinator reads each result and decides what to dispatch next.

### The plan-then-implement pattern

1. **Scout/architect dispatch** (Claude, read-only) — produces a plan or analysis
2. **Coordinator reads** the result via `agent-mux result <id>`
3. **Lifter dispatch** (Codex) — implements from the plan; the plan is injected via `--context-file` or inlined into the prompt
4. **Auditor dispatch** (Claude or Gemini) — verifies the implementation

Each dispatch is independent: one `DispatchSpec`, one `DispatchResult`. Handoff context is explicit — the coordinator decides what to pass and how.

### Async sequential pattern

For long steps, use `--async` and `wait`:

```bash
# Fire the planning step
ID=$(agent-mux --async --engine claude --role architect -C /repo "Produce a migration plan" \
  | jq -r .dispatch_id)

# Wait for it
agent-mux wait "$ID" --poll 30s

# Read the result and dispatch the next step
PLAN=$(agent-mux result "$ID")
agent-mux --engine codex --role lifter --context-file <(echo "$PLAN") -C /repo "Implement the plan"
```

### Fan-out pattern

Dispatch independent workers in parallel; collect results after all complete:

```bash
ID1=$(agent-mux --async --engine codex -C /repo "Audit src/auth/" | jq -r .dispatch_id)
ID2=$(agent-mux --async --engine codex -C /repo "Audit src/payments/" | jq -r .dispatch_id)

agent-mux wait "$ID1"
agent-mux wait "$ID2"

agent-mux result "$ID1"
agent-mux result "$ID2"
```

Use fan-out only when workers are truly independent (separate files, separate review slices, independent research threads). Dependent work must be sequential.

## Cross-References

- [Dispatch](./dispatch.md) for prompt composition order and DispatchSpec fields
- [Engines](./engines.md) for per-engine system prompt handling
- [Config](./config.md) for skill injection and profile loading
- [Async](./async.md) for `--async` dispatch, `wait`, and `result`
- [Steering](./steering.md) for signal delivery mechanics
- [Recovery](./recovery.md) for recovery continuation prompts
