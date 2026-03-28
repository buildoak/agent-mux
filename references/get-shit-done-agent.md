# GSD Coordinator Reference

The GSD (Get Shit Done) coordinator is a multi-step task coordinator that orchestrates agent-mux workers for complex pipelines. It receives a task from a parent thread, decomposes it into steps, dispatches workers via agent-mux, verifies output, and returns a clean summary.

## When to Use GSD

- Task has 3+ dependent steps
- Multi-model pipeline needed (e.g., Codex implements, Claude reviews)
- Research synthesis requiring fan-out across sources
- Complex audit or analysis requiring file artifacts

Do NOT use for: single-step tasks, quick lookups, conversational responses. A single `agent-mux` dispatch is sufficient for those.

## How GSD Works with agent-mux v2

GSD coordinators dispatch all work through agent-mux using JSON `--stdin`. The coordinator itself is an LLM (typically Opus) that reads its spec, plans steps, and executes them sequentially or in parallel.

### Role-based dispatch

Every worker dispatch uses a role from `config.toml`. The coordinator selects the role matching each step's needs:

```bash
printf '{"role":"lifter","prompt":"Implement retries in src/http/client.ts","cwd":"/repo"}' | agent-mux --stdin
```

### Pipeline templates

For common multi-step patterns, dispatch a named pipeline instead of individual workers:

```bash
printf '{"pipeline":"build","prompt":"Redesign the auth flow","cwd":"/repo"}' | agent-mux --stdin
```

Three built-in pipelines: `build` (architect -> lifter -> auditor), `research` (scout fan-out -> researcher -> architect synthesis), `tenx` (grunt fan-out -> auditor merge).

## Orchestration Patterns

### The 10x Pattern (primary for implementation)

Different engines, different blind spots, high confidence.

1. Dispatch `lifter` (Codex) to implement -- exact files, inlined context, verification gate
2. Check `status === "completed"` and `activity.files_changed`
3. Dispatch `auditor` to review changed files
4. If issues found: second `lifter` pass with auditor findings inlined
5. Return summary of what shipped and what the auditor confirmed

### Fan-Out

Spawn N parallel workers on independent subtasks using `grunt` or `batch` roles. Workers return inline by default. Over 200 lines, workers write to `_workbench/YYYY-MM-DD-{engine}-{topic}.md`. Coordinator synthesizes all returns into one output.

### Handoff Modes (between pipeline steps)

| Mode | Content passed to next step |
|------|----------------------------|
| `summary_and_refs` | Summary + path to `output.md` + artifact dir (default) |
| `full_concat` | Full content of `output.md` |
| `refs_only` | Only `output.md` path and artifact dir |

## Setting Up a GSD Coordinator

### 1. Create the agent spec

Place a `.md` file in `.claude/agents/` within your project:

```
your-project/
  .claude/
    agents/
      get-shit-done-agent.md    # coordinator spec
      get-shit-done-agent.toml  # optional companion config
```

### 2. Minimal coordinator spec

```markdown
---
name: gsd-coordinator
model: claude-opus-4-6
skills:
  - agent-mux
---

You are a GSD coordinator. You receive a task and execute it end-to-end
via agent-mux worker dispatches.

## Dispatch Roles

Use role-based dispatch for all workers. Match the role to the step:
- `scout` / `explorer` for context gathering
- `lifter` / `lifter-deep` for implementation
- `auditor` for verification
- `researcher` / `architect` for analysis and planning
- `grunt` / `batch` for parallel fan-out

## Playbook

1. Triage: identify inputs, outputs, constraints
2. Scout: pre-extract context before heavy work
3. Dispatch: run workers with skills injected
4. Verify: check status, confirm gate was met
5. Return: summary + files + status
```

### 3. Optional companion TOML

A sibling `.toml` file adds project-specific roles or pipeline overrides:

```toml
[roles.project-lifter]
engine = "codex"
model = "gpt-5.4"
effort = "high"
timeout = 1800
skills = ["agent-mux", "your-project-write"]
```

### 4. Invoke the GSD coordinator

From Claude Code, spawn as a subagent:
```
Task(subagent_type="gsd-coordinator")
```

From shell via agent-mux:
```bash
agent-mux --profile get-shit-done-agent --cwd /path/to/project "task description"
```

Profile resolution searches: `<cwd>/.claude/agents/<name>.md`, then `<cwd>/agents/<name>.md`, then `<cwd>/.agent-mux/agents/<name>.md`, then `~/.agent-mux/agents/<name>.md`.

## Production Reference

The live v2 GSD coordinator used in pratchett-os is at:
`coordinator/.claude/agents/get-shit-done-agent.md`

It defines 10 role families, escalation ladders, recovery decision trees, verification gates, and a full worker communication protocol. Use it as the reference implementation when building project-specific coordinators.

## Output Contract

Workers return inline by default (focused summaries, not dumps). Over 200 lines, write to `_workbench/YYYY-MM-DD-{engine}-{description}.md`. File naming: `YYYY-MM-DD-{engine}-{description}.md` where engine is `codex`, `claude`, `gemini`, `spark`, or `coordinator`.

The GSD coordinator returns to its parent:
1. **Status:** `done` | `blocked` | `needs-decision`
2. **Summary:** 3-5 sentences covering what was done, findings, decisions
3. **Files changed:** absolute paths to artifacts
4. **Dispatch log:** salt + status per worker dispatched

## Key Anti-Patterns

- **Blind retry.** Diagnose why a worker failed before re-dispatching.
- **Context bombing.** Write to disk, pass the path. Never paste full artifacts into prompts.
- **Wrong worker.** Don't send exploration to Codex. Don't send focused implementation to Claude.
- **Spawning Claude for more Claude.** The coordinator IS Claude. Use Codex for diversity.
- **xHigh for routine work.** `high` is the workhorse. Reserve `xhigh` for audits and deep analysis.
- **Skillless dispatch.** If a skill exists for the task, inject it via `--skill`.
