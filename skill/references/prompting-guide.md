# Prompting Guide

Per-engine prompt patterns, auto-injected context, and reliable dispatch
workflows.

---

## General Rules

Every dispatch prompt should provide:

1. one job
2. explicit scope
3. hard constraints
4. a verification gate
5. a clear output shape

Good default frame:

```text
Goal:
Scope:
Constraints:
Verification gate:
Expected output:
```

Do not spend tokens explaining agent-mux to the worker. The worker already has
the harness, artifact dir, and any injected preamble.

---

## Auto-Injected Preamble

agent-mux may prepend one or both of these lines before the prompt:

- `Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting.`
- `If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.`

Implications:

- if you pass `--context-file` or `context_file`, the worker is already told to read it
- if the run has an artifact dir, the worker is already told where to write scratch files

Do not repeat those lines unless you need something more specific, such as a
required filename under `$AGENT_MUX_ARTIFACT_DIR`.

---

## Codex Prompting

Codex works best with narrow, execution-oriented prompts.

### What works

- exact file paths
- concrete edits with clear boundaries
- explicit commands to run after the edit
- a hard verification gate
- one deliverable per dispatch

### What fails

- open-ended repo exploration
- multiple goals with fuzzy priorities
- implied tests or output requirements
- prompts that require the worker to discover scope first

### Recommended shape

```text
Implement retry backoff in:
- src/client/retry.go
- src/client/retry_test.go

Constraints:
- do not change public API names
- keep the retry schedule deterministic in tests

Verification gate:
- go test ./src/client passes
- retry count remains capped at 5

Expected output:
- short summary
- tests run
- remaining risk
```

If you need scratch notes, say so directly:

```text
Write any work log or intermediate notes to $AGENT_MUX_ARTIFACT_DIR/retry-plan.md.
```

---

## Claude Prompting

Claude is better for planning, synthesis, review, and ambiguous problem
framing.

### Recommended shape

```text
Task: Pressure-test the auth migration plan.
Scope: read the current plan, identify the top 3 failure modes, propose a safer rollout.
Expected output: verdict first, then revised sequence, then open questions.
```

Use Claude when the main work is reasoning. If the end goal is code changes,
plan with Claude and hand the implementation to Codex in a second dispatch.

---

## Gemini Prompting

Gemini is best used as a narrow contrast pass.

### Recommended shape

```text
Review this patch for hidden rollout risks.
Focus on: backwards compatibility, deployment hazards, missing tests.
Keep the answer to 5 bullets max.
```

Keep Gemini prompts narrower than Claude prompts.

---

## Context Loading Tools

### `--skill` / `skills`

Use named skill directories for reusable runbooks.

```json
{
  "profile": "lifter",
  "skills": ["react", "test-writer"],
  "prompt": "...",
  "cwd": "/repo"
}
```

### `--context-file` / `context_file`

Use for large briefs already written to disk. agent-mux sets
`AGENT_MUX_CONTEXT` and adds the read preamble automatically.

### `system_prompt` / `--system-prompt`

Use for run-level framing, not giant manuals.

```json
{
  "profile": "auditor",
  "system_prompt": "Prioritize regressions over style.",
  "prompt": "Review the patch."
}
```

### `--profile` / `profile`

Use for stable coordinator personas with default engine/model/skills.

### `--stdin`

When using `--stdin`, put dispatch fields in JSON. The CLI is only carrying
transport flags like `--stdin`, `--async`, `--stream`, `--verbose`, `--yes`,
and `--config`.

---

## Dispatch -> Wait -> Result

### Sequential handoff

```bash
ID1=$(agent-mux -P=architect --async -C=/repo "Design the auth migration" 2>/dev/null | jq -r .dispatch_id)

agent-mux wait --poll 30s "$ID1" 2>/dev/null

PLAN=$(agent-mux result --json "$ID1" 2>/dev/null | jq -r .response)

printf '%s' "{\"profile\":\"lifter\",\"prompt\":\"Implement this plan:\\n$PLAN\",\"cwd\":\"/repo\"}" \
  | agent-mux --stdin --async 2>/dev/null
```

### Handoff via context file

```bash
ID1=$(agent-mux -P=architect --async -C=/repo "Design the auth migration" 2>/dev/null | jq -r .dispatch_id)
agent-mux wait "$ID1" 2>/dev/null
agent-mux result "$ID1" 2>/dev/null > /tmp/plan.md

agent-mux -P=lifter --async --context-file=/tmp/plan.md -C=/repo \
  'Implement the plan at $AGENT_MUX_CONTEXT' 2>/dev/null
```

### Parallel fan-out

```bash
ID1=$(agent-mux -P=scout --async -C=/repo "Scan auth module" 2>/dev/null | jq -r .dispatch_id)
ID2=$(agent-mux -P=scout --async -C=/repo "Scan API layer" 2>/dev/null | jq -r .dispatch_id)
ID3=$(agent-mux -P=scout --async -C=/repo "Scan DB layer" 2>/dev/null | jq -r .dispatch_id)

agent-mux wait "$ID1" 2>/dev/null
agent-mux wait "$ID2" 2>/dev/null
agent-mux wait "$ID3" 2>/dev/null
```

`wait` is the completion primitive. Do not poll `status --json` in a loop.

---

## Recovery and Steering Phrasing

### Recovery prompt

`--recover` already injects the prior dispatch ID, engine/model, status, and
artifact list. Your prompt should only describe the delta.

- Good: `Finish the remaining parser tests and summarize what is still missing.`
- Bad: `Re-explain the whole project from scratch.`

### Signal / steer phrasing

Signals and steer messages should be short.

- Good: `Focus on auth paths only; skip docs.`
- Good: `Stop after green tests and summarize remaining edge cases.`
- Bad: multi-paragraph redesign instructions

---

## Flag Hygiene

1. Put flags before positional args. `agent-mux wait --poll 30s <id>`, not `agent-mux wait <id> --poll 30s`.
2. In `--stdin` mode, put dispatch fields in JSON rather than mixing in CLI dispatch flags.
3. Escape worker env vars in coordinator-shell prompts when needed. Use `\$AGENT_MUX_CONTEXT` or single quotes so your shell does not expand them early.
4. When you need machine-readable output, redirect stderr away and parse stdout JSON. Example: `2>/dev/null`.
