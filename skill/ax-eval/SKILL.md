---
name: ax-eval
description: |
  Agent Experience evaluation for agent-mux. Tests whether agents can understand
  the CLI, parse output, self-correct from errors, and plan dispatches using the
  skill documentation. Trigger on: ax-eval, test agent-mux, run ax eval,
  ax health, agent experience evaluation.
---

# AX Evaluation Skill

## What This Does

Runs agent-first evaluation of the agent-mux tool. Measures four capabilities:

| Tier | What it tests | Cost |
|------|---------------|------|
| L0 | Contract comprehension — can the agent parse output JSON? | 1-2 dispatches for real results + 4 agent dispatches |
| L1 | Error self-correction — can the agent fix commands from error hints? | 7 agent dispatches (one per error code) |
| L2 | Skill comprehension — can the agent plan multi-step dispatches from the skill doc? | 5 agent dispatches |
| L3 | GSD comprehension — can GSD agents plan dispatches given their system prompts? | 4 agent dispatches |
| L4 | Hard mode — live dispatches, steering, recovery | Manual, real API costs |

## How to Run

### Full run (L0-L3)

```
Run ax-eval, all tiers.
```

The coordinator:
1. Reads `skill/ax-eval/manifest.md`
2. Runs L0 through L3 sequentially
3. Reports per-scenario scores, per-tier averages, and overall AX Health

### Single tier

```
Run ax-eval L1 only.
Run ax-eval L2.
```

Runs only the specified tier. Same protocol, narrower scope.

### Hard mode (L4)

```
Run ax-eval L4, scenario 4.1.
```

L4 tests require live dispatches. Run only when explicitly requested. Each scenario in L4 has its own trigger protocol described in the manifest.

---

## Protocol

### 1. Read the manifest

Read `skill/ax-eval/manifest.md`. This is the single source of truth for all scenarios, prompts, and checklists.

### 2. Prepare materials per tier

**L0 — Contract Comprehension:**
- For L0.1: Run a real dispatch to generate a completed result JSON.
  ```bash
  printf '{"engine":"codex","model":"gpt-5.4-mini","effort":"high","prompt":"What is 2+2? Answer with just the number.","cwd":"/tmp","skip_skills":true,"timeout_sec":120}' \
    | agent-mux --stdin --yes 2>/dev/null
  ```
  Capture and pretty-print the stdout JSON.
- For L0.2: Run a dispatch with `"engine":"bogus_engine"` to generate a failed result. Capture stdout (expect non-zero exit).
- For L0.3 and L0.4: No live dispatch. Load `references/output-contract.md` only.
- All L0 scenarios need `references/output-contract.md` loaded.

**L1 — Error Self-Correction:**
- No live dispatch needed. Each scenario provides a pre-built error JSON (from the ErrorCatalog) and an original command string. These are defined in the manifest.

**L2 — Skill Comprehension:**
- Load `skill/SKILL.md`. This is injected into every L2 agent prompt as context.

**L3 — GSD Comprehension:**
- Load the GSD agent definitions:
  - Heavy: `coordinator/.claude/agents/gsd-heavy.md`
  - Light: `coordinator/.claude/agents/gsd-light.md`
- Load `skill/SKILL.md` for injection into agent prompts.
- Use the GSD definition as the system prompt for the dispatched agent.

### 3. Dispatch agents

For each scenario:

1. Construct the prompt from the manifest template, filling in materials.
2. Dispatch via agent-mux:
   - **L0/L1:** Single agent, codex gpt-5.4-mini, high effort.
     ```bash
     agent-mux -E=codex -m=gpt-5.4-mini -e=high --async --skip-skills \
       -C=/tmp "<scenario prompt>" 2>/dev/null
     ```
   - **L2:** Single agent with skill/SKILL.md as context.
     ```bash
     agent-mux -E=codex -m=gpt-5.4-mini -e=high --async --skip-skills \
       -s="You are a coordinator agent. You plan and dispatch agent-mux commands. Show exact commands." \
       -C=/tmp "<prompt with SKILL.md injected>" 2>/dev/null
     ```
   - **L3:** Single agent with GSD prompt as system context + SKILL.md in prompt.
     ```bash
     agent-mux -E=codex -m=gpt-5.4-mini -e=high --async --skip-skills \
       --system-prompt-file=<path-to-gsd-prompt> \
       -C=/tmp "<prompt with SKILL.md injected>" 2>/dev/null
     ```
3. Wait: `agent-mux wait --poll 30s <id> 2>/dev/null`
4. Collect: `agent-mux result <id> --json 2>/dev/null`

### 4. Evaluate

The coordinator IS the judge. For each scenario:

1. Read the agent's response from the result JSON.
2. Walk the checklist from the manifest. For each item:
   - **Met (1):** The response clearly satisfies the criterion.
   - **Not-met (0):** The response omits, contradicts, or fails the criterion.
3. Calculate: `score = items_met / total_items`.
4. Record: scenario name, score, items met/missed, any notes.

**Evaluation guidelines:**
- Be strict on syntax. `--sandbox none` is not a valid flag — that's a fail.
- Be strict on anti-patterns. Polling `steer status` in a loop instead of using `wait` is a critical failure.
- Be lenient on style. Different valid approaches to the same task are all acceptable.
- Hallucinations are automatic fails for the affected checklist items.

### 5. Report

Output an inline structured report:

```
**AX Health: X%**

## L0 · Contract Comprehension — avg X%
| Scenario | Score | Items Met | Notes |
|----------|-------|-----------|-------|
| L0.1 completed-result-parse | 0.90 | 9/10 | missed handoff_summary |
| ... | ... | ... | ... |

## L1 · Error Self-Correction — avg X%
| Scenario | Score | Items Met | Notes |
| ... | ... | ... | ... |

## L2 · Skill Comprehension — avg X%
| ... | ... | ... | ... |

## L3 · GSD Comprehension — avg X%
| ... | ... | ... | ... |

## Failed Items
[List specific checklist items that were not met, grouped by scenario, for debugging]
```

---

## Dispatch Mechanics Summary

| Tier | Agent engine | Model | Effort | System prompt | Context injection |
|------|-------------|-------|--------|---------------|-------------------|
| L0 | codex | gpt-5.4-mini | high | none | output-contract.md + result JSON in prompt |
| L1 | codex | gpt-5.4-mini | high | none | error JSON + original command in prompt |
| L2 | codex | gpt-5.4-mini | high | "You are a coordinator agent..." | SKILL.md in prompt |
| L3 | codex | gpt-5.4-mini | high | GSD agent definition file | SKILL.md in prompt |
| L4 | varies | varies | varies | varies | live dispatch — manual |

---

## Key Design Principles

1. **The manifest is the single source of truth.** This skill tells you HOW to run; the manifest tells you WHAT to run. Add a test = add a section to the manifest. Edit a checklist = edit the manifest.

2. **The coordinator evaluates directly.** No judge-as-dispatch. Read the response, check the checklist, score. This is simpler, gives better feedback, and avoids the meta-problem of testing whether the judge understands the criteria.

3. **Prompts must be grounding.** Every scenario prompt includes enough material for the agent to produce a real answer. The L2/L3 failures in the Go test suite happened because agents lacked context. In this system, SKILL.md is always injected for L2/L3, and the full output-contract.md is always injected for L0.

4. **Checklists are binary.** Each item is met or not-met. Score = met/total. No partial credit, no subjective ratings.

5. **Keep what works.** L1 error self-correction (86% pass rate in Go tests) is ported faithfully. The seven error codes that proved effective are preserved with the same scenario structure.
