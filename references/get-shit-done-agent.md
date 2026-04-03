# GSD Coordinator Reference

The GSD (Get Shit Done) coordinator is a multi-step task coordinator that orchestrates agent-mux workers for complex tasks. It receives a task from a parent thread, decomposes it into steps, dispatches workers via agent-mux, verifies output, and returns a clean summary.

## When to Use GSD

- Task has 3+ dependent steps
- Multi-model work needed (e.g., Codex implements, Claude reviews)
- Research synthesis requiring fan-out across sources
- Complex audit or analysis requiring file artifacts

Do NOT use for: single-step tasks, quick lookups, conversational responses. A single `agent-mux` dispatch is sufficient for those.

## How GSD Works with agent-mux

GSD coordinators dispatch all work through agent-mux using JSON `--stdin`. The coordinator itself is an LLM (typically Opus) that reads its spec, plans steps, and executes them sequentially or in parallel.

### Role-based dispatch

Every worker dispatch uses a role from `config.toml`. The coordinator selects the role matching each step's needs:

```bash
printf '{"role":"lifter","prompt":"Implement retries in src/http/client.ts","cwd":"/repo"}' | agent-mux --stdin
```

### Async dispatch with wait/result

For parallel or long-running work, use `--async` and poll with `wait`/`result`:

```bash
# Dispatch async
printf '{"role":"lifter","prompt":"Implement retries","cwd":"/repo"}' | agent-mux --stdin --async
# Returns: {"schema_version":1,"kind":"async_started","dispatch_id":"01KM...","artifact_dir":"..."}

# Wait for completion (emits status to stderr)
agent-mux wait 01KM...

# Read the result
agent-mux result 01KM... --json
```

### Sequential multi-step dispatch

For dependent steps, dispatch synchronously and chain results:

```bash
# Step 1: Scout
SCOUT=$(printf '{"role":"scout","prompt":"Find all auth files","cwd":"/repo"}' | agent-mux --stdin)
SCOUT_RESPONSE=$(echo "$SCOUT" | jq -r '.response')

# Step 2: Implement (uses scout output)
printf '{"role":"lifter","prompt":"Implement auth refactor. Context:\n%s","cwd":"/repo"}' "$SCOUT_RESPONSE" \
  | agent-mux --stdin

# Step 3: Audit
printf '{"role":"auditor","prompt":"Review the auth refactor for regressions","cwd":"/repo"}' \
  | agent-mux --stdin
```

## Orchestration Patterns

### The 10x Pattern (primary for implementation)

Different engines, different blind spots, high confidence.

1. Dispatch `lifter` (Codex) to implement -- exact files, inlined context, verification gate
2. Check `status === "completed"` and `activity.files_changed`
3. Dispatch `auditor` to review changed files
4. If issues found: second `lifter` pass with auditor findings inlined
5. Return summary of what shipped and what the auditor confirmed

### Fan-Out

Spawn N parallel workers on independent subtasks using `grunt` or `batch` roles with `--async`. Workers return inline by default. Over 200 lines, workers write to `_workbench/YYYY-MM-DD-{engine}-{topic}.md`. Coordinator collects results via `ax wait` and `ax result`, then synthesizes all returns into one output.

```bash
# Dispatch parallel workers
for topic in "auth" "billing" "search"; do
  printf '{"role":"grunt","prompt":"Scan %s module","cwd":"/repo"}' "$topic" \
    | agent-mux --stdin --async
done

# Collect all results
for id in $IDS; do
  agent-mux wait "$id"
  agent-mux result "$id" --json
done
```

### Mid-flight Steering

Use `steer` to redirect a running worker without killing it:

```bash
# Nudge a slow worker to wrap up
agent-mux steer 01KM... nudge "Focus on the critical path, skip edge cases"

# Redirect a worker that went off track
agent-mux steer 01KM... redirect "Stop the refactor, just fix the failing test"

# Extend timeout for a worker doing good work
agent-mux steer 01KM... extend 600

# Abort a stuck worker
agent-mux steer 01KM... abort
```

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

A sibling `.toml` file adds project-specific roles:

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

## Durable Join Key

Use `session_id` from dispatch result metadata as the durable join key for correlating work across dispatches within a GSD run. It is present in both `result --json` output and `inspect --json` output.

```bash
agent-mux result 01KM... --json | jq -r '.session_id'
```

## Output Contract

Workers return inline by default (focused summaries, not dumps). Over 200 lines, write to `_workbench/YYYY-MM-DD-{engine}-{description}.md`. File naming: `YYYY-MM-DD-{engine}-{description}.md` where engine is `codex`, `claude`, `gemini`, `spark`, or `coordinator`.

The GSD coordinator returns to its parent:
1. **Status:** `done` | `blocked` | `needs-decision`
2. **Summary:** 3-5 sentences covering what was done, findings, decisions
3. **Files changed:** absolute paths to artifacts
4. **Dispatch log:** dispatch_id + status per worker dispatched

## Key Anti-Patterns

- **Blind retry.** Diagnose why a worker failed before re-dispatching.
- **Context bombing.** Write to disk, pass the path. Never paste full artifacts into prompts.
- **Wrong worker.** Don't send exploration to Codex. Don't send focused implementation to Claude.
- **Spawning Claude for more Claude.** The coordinator IS Claude. Use Codex for diversity.
- **xHigh for routine work.** `high` is the workhorse. Reserve `xhigh` for audits and deep analysis.
- **Skillless dispatch.** If a skill exists for the task, inject it via `--skill`.
