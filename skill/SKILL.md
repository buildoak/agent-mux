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

One CLI, three engines (Codex, Claude, Gemini), one JSON contract. agent-mux
resolves config, builds prompts, dispatches workers, and collects results.

## Dispatch

### Sync (blocks until done)

```bash
agent-mux -P=lifter -C=/repo "Fix the retry logic in src/client/retry.go" 2>/dev/null
```

Parse stdout JSON — it is a `DispatchResult` with `status`, `response`,
`activity.files_changed`, and `metadata.engine`.

### Async (early ack, collect later)

```bash
ID=$(agent-mux -P=scout --async -C=/repo "Find deprecated API usages" 2>/dev/null | jq -r .dispatch_id)

agent-mux wait --poll 30s "$ID" 2>/dev/null      # block until done

agent-mux result --json "$ID" 2>/dev/null         # read stored result
```

`--async` emits an ack, detaches stdio, then runs synchronously in the current
process. It does NOT daemonize. If you need background execution, the caller
must background the process (shell `&` or `run_in_background`).

Before the ack is emitted, these exist on disk:
- `~/.agent-mux/dispatches/<id>/meta.json` (persistent metadata)
- `<artifact_dir>/host.pid`
- `<artifact_dir>/status.json`

`_dispatch_ref.json` is written later during engine startup — do NOT rely on
it existing at ack time.

**Fan-out with shell `&`:** `--async` returns control after the ack, but the
ack itself takes engine startup time (~10-20s for Codex). Sequential fan-out
means that cost is paid serially. Use shell `&` to parallelize startup, then
`agent-mux wait` to synchronize:

```bash
# Fan-out: shell & parallelizes engine startup
for svc in auth billing orders; do
  { agent-mux --async -P=scout -C="/repo/$svc" "Audit $svc" 2>/dev/null | jq -r .dispatch_id > "/tmp/$svc.id"; } &
done
wait  # all acks received, all workers running
# Synchronize:
for svc in auth billing orders; do
  agent-mux wait "$(cat /tmp/$svc.id)"
done
```

### Structured dispatch via stdin

```bash
printf '%s' '{"profile":"lifter","prompt":"Implement the fix","cwd":"/repo"}' \
  | agent-mux --stdin --async 2>/dev/null
```

In `--stdin` mode, dispatch fields go in JSON. CLI carries only transport
flags: `--stdin`, `--async`, `--stream`, `--verbose`, `--yes`.

## Reading Results

Always check these fields on every result:

- `status` — `completed`, `timed_out`, or `failed`
- `response` — worker's final text
- `activity.files_changed` — files the worker modified
- `metadata.engine`, `metadata.model` — what ran
- `kill_reason` — present on some `failed` results (via `result --json`)

`wait --json` normally returns the same shape as `result --json`. Exception:
orphaned dispatches emit raw `LiveStatus` JSON and exit nonzero.

## Steering

```bash
agent-mux steer <id> redirect "Narrow to the parser module only"
agent-mux steer <id> nudge
agent-mux steer <id> extend 300
agent-mux steer <id> abort
```

Delivery varies by engine:
- **Codex**: FIFO pipe (`stdin.pipe`) when `stdin_pipe_ready=true`, else inbox
- **Claude/Gemini**: inbox delivery triggers session resume/restart via
  `ResumeArgs()` — not passive file polling

## Profiles

```bash
agent-mux config prompts        # list available profiles for this project
```

Profiles are markdown files with optional YAML frontmatter. Search order:
1. `<cwd>/.agent-mux/prompts/<name>.md` (project-level)
2. `~/.agent-mux/prompts/<name>.md` (global)

Use `-P=<name>` to select a profile (e.g. `lifter`, `scout`, `architect`).

## Auto-Injected Preamble

agent-mux may prepend to the worker's prompt:

- `Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting.`
- `If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.`

If you want a specific scratch file, say so:

```text
Write your work log to $AGENT_MUX_ARTIFACT_DIR/review-notes.md.
```

---

## Prompt Discipline

1. One job per dispatch
2. Name exact files or directories
3. State hard constraints
4. Provide a verification gate
5. State the expected output shape

**Codex** — implementation, debugging, precise edits. Narrow scope, exact paths.
**Claude** — planning, synthesis, review, ambiguity reduction.
**Gemini** — narrow contrast pass, second opinion.

## Recovery

```bash
agent-mux -P=lifter --recover=<id> -C=/repo "Finish the remaining parser tests" 2>/dev/null
```

Decision rule:

- `timed_out` + useful artifacts → `--recover`
- `timed_out` + no artifacts → tighten prompt, retry once
- `failed` + `retryable` → fix cause, retry once
- `failed` + not retryable → escalate

Your recovery prompt describes only the delta. agent-mux injects the prior
dispatch context automatically.

Artifact dir resolution during recovery: persistent meta first, then current
secure root, then legacy `/tmp/agent-mux/<id>`.

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
| [config-and-roles.md](references/config-and-roles.md) | config structure, profiles, hooks |
