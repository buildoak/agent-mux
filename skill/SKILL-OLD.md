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

**Profile dispatch on a different engine** (same profile, explicit engine+model):

```bash
agent-mux -P=researcher -E=gemini -m gemini-3.1-pro-preview -C=/repo "Analyze the auth module for hidden coupling" 2>/dev/null
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

**Effort levels per engine:** `low`, `medium`, `high`, and `xhigh` are standard agent-mux effort values. Codex supports all four. Claude maps effort to thinking budget — higher effort = more tokens for reasoning. Gemini silently ignores the effort flag (agent-mux logs a warning internally but proceeds); use model selection (`flash` vs `pro`) to control Gemini depth instead.

**When to pick Gemini specifically:**

- Second opinion on a Codex/Claude result (diversity of reasoning)
- Paper or document analysis (strong at structured extraction)
- Review pass where you want a different perspective from the primary engine
- Lightweight reads and analysis where Flash models are cost-effective

**Gemini dispatch patterns:**

```bash
# Profile with engine+model override
agent-mux -P=researcher -E=gemini -m gemini-2.5-pro -C=/repo "Synthesize findings"

# Direct engine override (no profile)
agent-mux -E=gemini -m gemini-2.5-pro -C=/repo "Review the migration plan"

# Via --stdin JSON
printf '%s' '{"engine":"gemini","model":"gemini-3.1-pro-preview","prompt":"...","cwd":"/repo"}' \
  | agent-mux --stdin 2>/dev/null
```

Note: Gemini defaults to `yolo` approval mode (no confirmations). Override
with `--permission-mode` when human supervision is needed. See
[gemini-specifics.md](references/gemini-specifics.md) for full details.

## Profile Roster

Discover the live roster before dispatching, especially before setting effort
overrides:

```bash
agent-mux config prompts           # human table
agent-mux config prompts --json    # structured JSON array
```

Current profiles and when to pick each:

| Profile | Engine | Model | Effort | Use When |
|---------|--------|-------|--------|----------|
| `scout` | codex | gpt-5.4-mini | xhigh | Quick read-only probe. Status checks, file reads, grep-and-report |
| `explorer` | codex | gpt-5.4 | high | Broad codebase exploration. Map structure, find patterns, survey |
| `researcher` | claude | claude-opus-4-6 | high | Deep analysis and synthesis. Multi-file reasoning, comparisons |
| `architect` | codex | gpt-5.4 | xhigh | System design and migration planning. Plans, not code |
| `lifter` | codex | gpt-5.4 | high | Scoped implementation. The workhorse -- build, test, verify |
| `auditor` | codex | gpt-5.4 | xhigh | Adversarial code review. Finds bugs, missing tests, unsafe assumptions |
| `writer` | codex | gpt-5.4 | high | Documentation and writing |
| `grunt` | codex | gpt-5.4-mini | xhigh | Mechanical edits, renames, bulk changes. Cheapest and fastest |
| `ticket-worker` | codex | gpt-5.4 | xhigh | Standard ticket execution |
| `ticket-worker-heavy` | codex | gpt-5.4 | high | Complex ticket execution |
| `day-checkout-worker` | codex | gpt-5.4 | xhigh | Day checkout pipeline orchestration |
| `guardian-worker` | codex | gpt-5.4 | xhigh | Guardian audit for completed PAPER-OPS tickets |
| `paper-ops-worker` | codex | gpt-5.4 | xhigh | Academic PDF ingestion into structured knowledge cards |

**Selection heuristic:** scout for reads, lifter for writes, architect for
plans, grunt for bulk edits, researcher for analysis. When in doubt, scout
first, then dispatch the right worker with what you learned.

**Gemini dispatch** is available for any profile. Use `-E=gemini` with a model
override to dispatch on Gemini instead of the profile's default engine. Flash
models for quick work, Pro models for deep analysis -- select the model
explicitly via `-m`.

## Essential Flags

| Flag | Short | What it does |
|------|-------|-------------|
| `-P` / `--profile` | `-P` | Load a prompt file by name. The primary dispatch flag |
| `-E` / `--engine` | `-E` | Override engine: `codex`, `claude`, `gemini` |
| `-m` / `--model` | `-m` | Override model |
| `-e` / `--effort` | `-e` | `low`, `medium`, `high`, `xhigh`. Gemini ignores this (warns); use model selection instead |
| `-C` / `--cwd` | `-C` | Working directory for the worker |
| `-t` / `--timeout` | `-t` | Dispatch timeout in seconds. NOT a flag on `wait` — `wait` accepts only `--poll` for cadence |
| `--async` | | Return ack immediately, run in current process |
| `--context-file` | | File path -- sets `AGENT_MUX_CONTEXT`, worker told to read it |
| `--skill` | | Repeatable skill name to inject |
| `--recover` | | Continue from a prior dispatch ID |

Precedence: CLI flags > profile frontmatter > hardcoded defaults. `-P=lifter
-m gpt-5.4-mini` uses lifter's engine/effort/timeout but overrides the model.

---

## Async and Fan-Out

`--async` emits the ack JSON (containing `dispatch_id`) to stdout
IMMEDIATELY after writing `host.pid` + `status.json` — the capture of
`dispatch_id` is async. The CLI THEN continues running in the same
process until the dispatch completes. It does NOT daemonize. `host.pid`
is the agent-mux CLI PID, not the engine-child PID.

Practical consequences:
- `ID=$(agent-mux --async ... | jq -r .dispatch_id)` BLOCKS until the
  dispatch finishes (because `$(...)` waits for the CLI to exit). Fine
  if you want to wait anyway; WRONG if you want immediate fan-out.
- To truly background the CLI, wrap the whole invocation with shell `&`.
  Bash `wait` on a backgrounded agent-mux call waits for FULL dispatch
  completion, not just for the ack.
- Do NOT pipe `--async` output through `head -N`: `head` buffers lines
  until it sees N OR EOF, and EOF only arrives when the CLI exits at
  dispatch end. The ack stays hidden until the dispatch ends (see
  pitfall 11).
- Recover lost dispatch IDs via `agent-mux list --limit N --json`.
- No native multi-ID `wait` — call `agent-mux wait <id>` per dispatch
  (or script a loop).

**Canonical fan-out** (parallelize engine startup, collect results later):

```bash
# Fire N dispatches in parallel. Each CLI stays alive for the full run.
for svc in auth billing orders; do
  agent-mux --async -P=scout -C="/repo/$svc" "Audit $svc" \
    > "/tmp/$svc.out" 2>/dev/null &
done
wait  # blocks until all dispatches finish — --async does NOT daemonize

# Collect IDs + results from the per-call output files
for svc in auth billing orders; do
  ID=$(jq -r .dispatch_id < "/tmp/$svc.out")
  agent-mux result --json "$ID" 2>/dev/null | jq -r .response > "/tmp/$svc.audit.md"
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
agent-mux steer <id> abort
# Both argument orderings work:
agent-mux steer abort <id>
```

Delivery varies by engine:
- **Codex**: inbox + `codex exec resume` (FIFO soft-steer disabled since codex-cli 0.121 — see `docs/steering.md`)
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

### Heredoc Quoting Fallback

Claude Code falls back to temp files when prompts contain shell-unsafe characters (`'`, backticks, `$`, newlines). Use `--stdin` JSON to bypass shell quoting entirely — the prompt travels over stdin untouched. Rule: **positional arg for simple prompts; `--stdin` JSON for anything with quotes or code.**

## Bash Timeout

Claude Code's Bash tool defaults to 120s (2 minutes). Agent-mux dispatches can run much longer (up to 45 minutes for auditor). Always set an explicit `timeout` parameter on the Bash tool call that matches or exceeds the worker's expected runtime. For long-running dispatches, use `run_in_background: true` on the Bash tool call.

## Anti-Patterns & Landmines

Quick-scan list. For the full audit-grounded set with session evidence, read [references/pitfalls.md](references/pitfalls.md) BEFORE aborting, recovering, or steering.

- `kill` / `cancel` / `stop` / `terminate` / bare `abort` / `signal` as top-level verbs — use `agent-mux steer abort <id>` (rejected since commit `eafd044`; historically caused phantom dispatches)
- `--recover <id>` for fetching a finished result — that's `agent-mux result <id>`; `--recover` resumes with a NEW prompt
- `agent-mux wait <id> --json` — flags must come before the positional ID; the error hint blames the ID and misleads
- `steer nudge` on a live codex dispatch — post-`1fc0e0c` it queues for next resume, not live
- `steer nudge` on a Gemini worker mid-generation — destructive, wipes session state
- Trusting `status: completed` alone — it means engine exited cleanly, not that the task was accomplished; check `response_chars > 0` and your own completion handshake
- `-e` vs `-E` confusion — `-e` is effort, `-E` is engine; the slash-path `codex/gpt-5.4/xhigh` is not parsed
- Passing `-e=high` to profiles that default to `xhigh` (`scout`, `auditor`, `grunt`, `architect`) — this is a DOWNGRADE, not a safe floor. Check `agent-mux config prompts --json` first.
- CPU=0% / `tools_used: 0` / `files_changed: 0` as "stuck" signals — all normal; use `events.jsonl` byte-growth for liveness
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
| [gemini-specifics.md](references/gemini-specifics.md) | Gemini approval mode, model selection, filesystem access, resume quirks, tool support |
| [worker-diagnostics.md](references/worker-diagnostics.md) | Diagnosing silent workers, false-alarm patterns, decision framework |
| [pitfalls.md](references/pitfalls.md) | Landmines: phantom dispatches, completed-lies, steer semantics, stuck heuristics (audit-grounded, session-cited) |
