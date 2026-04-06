---
name: agent-mux
description: |
  Cross-engine dispatch layer for AI coding agents. Use when you need to:
  launch a worker on Codex/Claude/Gemini, recover a timed-out dispatch, steer
  a running worker mid-flight, or coordinate multi-model work. Trigger on:
  agent-mux, dispatch, spawn worker, codex worker, profile dispatch, async
  dispatch, steer agent, recover timeout, multi-engine.
---

# agent-mux

One CLI, three engines (Codex, Claude, Gemini), one JSON contract. Worker
identity lives in prompt files at `~/.agent-mux/prompts/` -- markdown with
YAML frontmatter that sets engine, model, effort, timeout, and system prompt.
No config files, no role tables. The prompt IS the worker.

## Quick Dispatch

Three patterns cover 95% of dispatches.

**Profile dispatch** (the standard path -- one flag resolves everything):

```bash
agent-mux -P=lifter -C=/repo "Fix the retry logic in src/client/retry.go" 2>/dev/null
```

**Profile dispatch with engine variant** (same profile, different engine):

```bash
agent-mux -P=researcher -V=gemini -C=/repo "Analyze the auth module for hidden coupling" 2>/dev/null
```

**Async dispatch** (fire, collect later):

```bash
ID=$(agent-mux -P=scout --async -C=/repo "Find deprecated API usages" 2>/dev/null | jq -r .dispatch_id)
agent-mux wait --poll 30s "$ID" 2>/dev/null
agent-mux result --json "$ID" 2>/dev/null
```

**Direct engine override** (no profile, explicit engine+model):

```bash
agent-mux -E=gemini -m gemini-2.5-pro -C=/repo "Review this diff for rollout risks" 2>/dev/null
```

**Structured dispatch via stdin** (canonical machine invocation):

```bash
printf '%s' '{"profile":"lifter","prompt":"Implement the fix","cwd":"/repo"}' \
  | agent-mux --stdin --async 2>/dev/null
```

Parse stdout JSON. Every result has `status`, `response`,
`activity.files_changed`, and `metadata.engine`. Always check `status` first.

## Engine Selection

Pick the engine before the profile:

| Engine | Best for | Depth lever |
|--------|----------|-------------|
| **Codex** | Implementation, debugging, precise edits, bulk changes | `--effort` (`low`/`medium`/`high`/`xhigh`) |
| **Claude** | Planning, synthesis, review, ambiguity reduction, parallel reasoning | `--effort` + model tier (Sonnet vs Opus) |
| **Gemini** | Analysis, review, paper processing, second-opinion contrast, diversity pass | Model selection (`flash` vs `pro`); effort flag is ignored |

**When to pick Gemini specifically:**

- Second opinion on a Codex/Claude result (diversity of reasoning)
- Paper or document analysis (strong at structured extraction)
- Review pass where you want a different perspective from the primary engine
- Lightweight reads and analysis where Flash models are cost-effective

**Gemini dispatch patterns:**

```bash
# Via variant (preferred when variant exists in config)
agent-mux -P=researcher -V=gemini -C=/repo "Synthesize findings"

# Via direct engine override
agent-mux -E=gemini -m gemini-2.5-pro -C=/repo "Review the migration plan"

# Via --stdin JSON
printf '%s' '{"engine":"gemini","model":"gemini-3.1-pro-preview","prompt":"...","cwd":"/repo"}' \
  | agent-mux --stdin 2>/dev/null
```

Note: Gemini defaults to `yolo` approval mode (no confirmations). Override
with `--permission-mode` when human supervision is needed. See
[gemini-specifics.md](references/gemini-specifics.md) for full details.

## Profile Roster

Discover the live roster:

```bash
agent-mux config prompts           # human table
agent-mux config prompts --json    # structured JSON array
```

Current profiles and when to pick each:

| Profile | Engine | Model | Use When |
|---------|--------|-------|----------|
| `scout` | codex | gpt-5.4-mini | Quick read-only probe. Status checks, file reads, grep-and-report |
| `explorer` | codex | gpt-5.4 | Broad codebase exploration. Map structure, find patterns, survey |
| `researcher` | claude | claude-opus-4-6 | Deep analysis and synthesis. Multi-file reasoning, comparisons |
| `architect` | claude | claude-opus-4-6 | System design and migration planning. Plans, not code |
| `lifter` | codex | gpt-5.4 | Scoped implementation. The workhorse -- build, test, verify |
| `auditor` | codex | gpt-5.4 | Adversarial code review. Finds bugs, missing tests, unsafe assumptions |
| `writer` | codex | gpt-5.4 | Documentation and writing |
| `grunt` | codex | gpt-5.4-mini | Mechanical edits, renames, bulk changes. Cheapest and fastest |
| `ticket-worker` | codex | gpt-5.4-mini | Standard ticket execution |
| `ticket-worker-heavy` | codex | gpt-5.4 | Complex ticket execution |

**Selection heuristic:** scout for reads, lifter for writes, architect for
plans, grunt for bulk edits, researcher for analysis. When in doubt, scout
first, then dispatch the right worker with what you learned.

**Gemini variants** exist for most roles (scout, explorer, researcher,
architect, lifter, lifter-deep, grunt, auditor). Use `-V=gemini` to dispatch
any of these on Gemini instead of their default engine. Flash models for
quick work, Pro models for deep analysis -- the variant config handles this
mapping.

## Essential Flags

| Flag | Short | What it does |
|------|-------|-------------|
| `-P` / `--profile` | `-P` | Load a prompt file by name. The primary dispatch flag |
| `-V` / `--variant` | `-V` | Select a role variant (e.g., `-V=gemini` for Gemini engine variant) |
| `-E` / `--engine` | `-E` | Override engine: `codex`, `claude`, `gemini` |
| `-m` / `--model` | `-m` | Override model |
| `-e` / `--effort` | `-e` | `low`, `medium`, `high`, `xhigh`. Gemini ignores this (warns); use model selection instead |
| `-C` / `--cwd` | `-C` | Working directory for the worker |
| `-t` / `--timeout` | `-t` | Timeout in seconds |
| `--async` | | Return ack immediately, run in current process |
| `--context-file` | | File path -- sets `AGENT_MUX_CONTEXT`, worker told to read it |
| `--skill` | | Repeatable skill name to inject |
| `--recover` | | Continue from a prior dispatch ID |

Precedence: CLI flags > profile frontmatter > hardcoded defaults. `-P=lifter
-m gpt-5.4-mini` uses lifter's engine/effort/timeout but overrides the model.

---

## Async and Fan-Out

`--async` emits an ack JSON, detaches stdio, then runs synchronously in the
current process. It does NOT daemonize. For true background execution, the
caller must background the process (`&` or `run_in_background`).

**Fan-out with shell `&`:** `--async` ack takes engine startup time (~10-20s
for Codex). Sequential fan-out pays that cost serially. Parallelize with `&`:

```bash
for svc in auth billing orders; do
  { agent-mux --async -P=scout -C="/repo/$svc" "Audit $svc" 2>/dev/null | jq -r .dispatch_id > "/tmp/$svc.id"; } &
done
wait  # all acks received
for svc in auth billing orders; do
  agent-mux wait "$(cat /tmp/$svc.id)"
done
```

**Sequential handoff** (plan then implement):

```bash
ID1=$(agent-mux -P=architect --async -C=/repo "Design the auth migration" 2>/dev/null | jq -r .dispatch_id)
agent-mux wait "$ID1" 2>/dev/null
agent-mux result "$ID1" 2>/dev/null > /tmp/plan.md
agent-mux -P=lifter --context-file=/tmp/plan.md -C=/repo \
  'Implement the migration plan. Tests must pass before reporting done.' 2>/dev/null
```

## Reading Results

Always check these fields on every result:

- `status` -- `completed`, `timed_out`, or `failed`
- `response` -- worker's final text
- `activity.files_changed` -- files the worker modified
- `metadata.engine`, `metadata.model` -- what ran
- `kill_reason` -- present on some `failed` results (via `result --json`)

`wait --json` returns the same shape as `result --json`. Exception: orphaned
dispatches emit raw `LiveStatus` JSON and exit nonzero.

## Steering

Mid-flight control for running dispatches:

```bash
agent-mux steer <id> redirect "Narrow to the parser module only"
agent-mux steer <id> nudge
agent-mux steer <id> extend 300
agent-mux steer <id> abort
```

Delivery varies by engine:
- **Codex**: FIFO pipe (`stdin.pipe`) when ready, else inbox
- **Claude/Gemini**: inbox triggers session resume via `ResumeArgs()`

## Recovery

```bash
agent-mux -P=lifter --recover=<id> -C=/repo "Finish the remaining parser tests" 2>/dev/null
```

Decision rule:

- `timed_out` + useful artifacts -> `--recover`
- `timed_out` + no artifacts -> tighten prompt, retry once
- `failed` + `retryable` -> fix cause, retry once
- `failed` + not retryable -> escalate

Recovery prompt describes only the delta. agent-mux injects prior context
automatically.

## Auto-Injected Preamble

agent-mux may prepend to the worker's prompt:

- `Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting.`
- `If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.`

Do not repeat these unless you need a specific filename:

```text
Write your work log to $AGENT_MUX_ARTIFACT_DIR/review-notes.md.
```

## Prompt Discipline

1. One job per dispatch
2. Name exact files or directories
3. State hard constraints
4. Provide a verification gate
5. State the expected output shape

**Codex** -- implementation, debugging, precise edits. Narrow scope, exact paths.
**Claude** -- planning, synthesis, review, ambiguity reduction.
**Gemini** -- analysis, review, paper processing, second-opinion diversity.
Keep Gemini prompts focused. For deep analysis, use Pro models (`gemini-2.5-pro`,
`gemini-3.1-pro-preview`). For fast reads, use Flash (`gemini-2.5-flash`,
`gemini-3-flash-preview`). See [gemini-specifics.md](references/gemini-specifics.md).

## `--stdin` Mode

In `--stdin` mode, dispatch fields go in JSON. CLI carries only transport
flags: `--stdin`, `--async`, `--stream`, `--verbose`, `--yes`.

```bash
printf '%s' '{"profile":"lifter","prompt":"Implement the fix","cwd":"/repo"}' \
  | agent-mux --stdin --async 2>/dev/null
```

Do not mix CLI dispatch flags into `--stdin` mode.

## Bash Timeout

Claude Code's Bash tool defaults to 120s (2 minutes). Agent-mux dispatches can run much longer (up to 45 minutes for auditor). Always set an explicit `timeout` parameter on the Bash tool call that matches or exceeds the worker's expected runtime. For long-running dispatches, use `run_in_background: true` on the Bash tool call.

## Anti-Patterns

- Treating `_dispatch_ref.json` as available at async ack time
- Polling `status --json` instead of using `wait`
- Assuming `--async` daemonizes (it does not)
- Mixing CLI dispatch flags into `--stdin` mode
- Ignoring `status` and reading only `response`

## Reference Index

| Reference | Read when |
|-----------|-----------|
| [cli-flags.md](references/cli-flags.md) | flags, commands, JSON fields, precedence |
| [async-and-steering.md](references/async-and-steering.md) | async launch, wait, steer, status |
| [output-contract.md](references/output-contract.md) | result schema, preview, lifecycle JSON |
| [recovery-guide.md](references/recovery-guide.md) | recovery flow, runtime layout, watchdog |
| [prompting-guide.md](references/prompting-guide.md) | prompt shapes, auto preamble, workflows |
| [config-and-profiles.md](references/config-and-profiles.md) | profile discovery, frontmatter, hooks, skills |
| [gemini-specifics.md](references/gemini-specifics.md) | Gemini approval mode, model selection, stall timeout, resume quirks, tool support |
