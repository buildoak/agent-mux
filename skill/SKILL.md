---
name: agent-mux
description: |
  Cross-engine dispatch layer for AI coding agents. Use when you need to:
  spawn a worker on Codex/Claude/Gemini, recover a timed-out dispatch, steer
  a running worker mid-flight, or coordinate multi-model work. Trigger on:
  agent-mux, dispatch, spawn worker, codex worker, role dispatch, async
  dispatch, steer agent, recover timeout, multi-engine.
---

# agent-mux

Dispatch substrate. One CLI, one JSON contract, three engines. agent-mux
resolves inputs and dispatches — it does not decide strategy. TOML-driven
roles encode engine + model + effort + timeout + skills into one flag.
Artifact directories exist before the harness starts; partial work is
preserved across timeout, crash, or cancel.

## Quickstart

### Discovery (mandatory first step)
```bash
agent-mux config roles        # role catalog: engines, models, timeouts, variants
```
Always run before dispatching. Roles change per project config.

### Dispatch -> Wait -> Collect

`--async` returns instantly. `wait` blocks until done. `result` collects.

```bash
# DISPATCH — instant ack with dispatch_id
agent-mux -R=scout --async -C=/repo "Find all deprecated API usages" 2>/dev/null
# -> {"kind":"async_started","dispatch_id":"01KMY...","artifact_dir":"/tmp/agent-mux/01KMY..."}

# WAIT — blocks until terminal state. THE completion primitive.
# NEVER poll `status --json` in a loop — use wait instead.
agent-mux wait --poll 30s <id> 2>/dev/null

# COLLECT — structured JSON result
agent-mux result <id> --json 2>/dev/null
# -> {"status":"completed","response":"...","activity":{"files_changed":[...]}}

# LIVE CHECK — one-off, not a loop. For "is it still alive?" moments only.
agent-mux status --json <id> 2>/dev/null

# POST-MORTEM — after completion. Role, engine, duration, response, artifacts.
agent-mux inspect <id> 2>/dev/null
```

`2>/dev/null` on every call — stderr carries diagnostic events, not output.
Always parse stdout as JSON.

Result: check `status`, `response`, `response_truncated`, `activity.files_changed`.

### Parallel dispatch

Multiple `--async` in one Bash call — each returns its own ack:
```bash
agent-mux -R=scout --async -C=/repo "Scan auth module" 2>/dev/null
agent-mux -R=scout --async -C=/repo "Scan API layer" 2>/dev/null
agent-mux -R=scout --async -C=/repo "Scan DB layer" 2>/dev/null
```
Collect each by ID: `agent-mux result <id> --json`.

### Structured spec (when CLI flags aren't enough)
```bash
cat > /tmp/spec.json << 'EOF'
{"role":"lifter","prompt":"...","cwd":"/repo","context_file":"/tmp/prior-analysis.md"}
EOF
agent-mux --stdin --async < /tmp/spec.json 2>/dev/null
```

DispatchSpec now stays narrow. The runtime JSON fields are:
`dispatch_id`, `engine`, `model`, `effort`, `prompt`, `system_prompt`, `cwd`,
`artifact_dir`, `context_file`, `timeout_sec`, `grace_sec`, `max_depth`,
`depth`, `full_access`, `engine_opts`.

**Codex workers:** Omit `--sandbox` — the default (`danger-full-access`) maps
to `--dangerously-bypass-approvals-and-sandbox`, giving full filesystem and
network access. Avoid `workspace-write` for multi-file output (known
persistence issues). Use `-C=` to scope the working directory instead.

### Steer mid-flight
```bash
agent-mux steer <id> redirect "Narrow to parser module only" 2>/dev/null
agent-mux steer <id> nudge 2>/dev/null         # gentle reminder
agent-mux steer <id> extend 300 2>/dev/null    # add 5 min
agent-mux steer <id> abort 2>/dev/null          # kill
```

### Wait (block until completion)
```bash
agent-mux wait --poll 60s <id> 2>/dev/null
```
Flags before the dispatch ID. Poll uses Go duration format (`30s`, `1m`).
Precedence: CLI `--poll` > config `[async].poll_interval` > 60s default.

## Engine Cognitive Styles

**Claude** resolves ambiguity. Planning, synthesis, review, prompt crafting.
Roles: `architect`, `researcher`. Watch for: skipping validation, over-abstraction.

**Codex** executes precision. Implementation, debugging, focused audits. Needs
surgical scope — one goal, specific files, explicit gate. Roles: `lifter`,
`auditor`, `scout`. Watch for: paralysis on underspecified prompts.
**Sandbox:** Omit `--sandbox` — default is `danger-full-access` (full filesystem
and network access). Avoid `workspace-write` for multi-file output — known
persistence issues. Restrict scope with `-C=` instead.

**Gemini** provides contrast. Different training, different blind spots. Use as
second opinion or challenge probe. All variants are reasoning-only on this
machine — no file reads, no tool calls. All context must be in the prompt.

**Streaming v2:** stderr is silent by default — only bookend events
(`dispatch_start`, `dispatch_end`) and failures are emitted. Use `--stream`
to restore full event output for human debugging.

**Meta-rule:** exploration -> Claude, precision -> Codex. Don't cross the streams.

## Dispatch Patterns

1. **`-R=role`** — CLI shorthand, most common for simple dispatches.
   ```bash
   agent-mux -R=lifter --async -C=/repo "Implement retries in client.ts"
   ```

2. **`--stdin` JSON** — structured specs with multiple overrides, recovery
   context, programmatic construction.
   ```bash
   printf '{"role":"lifter","prompt":"...","cwd":"/repo"}' | agent-mux --stdin --async
   ```

3. **`--skill=<name>`** — augment a role with additional skills.
   ```bash
   agent-mux -R=lifter --skill=satellite-data --async -C=/repo "Add coordinate overlay"
   ```
   Additive — the role's config-defined skills stay, `--skill` adds on top.
   Stackable: `--skill=web-search --skill=summarize`.

4. **Raw flags** (`-E= -m=`) — escape hatch when no role fits.
   ```bash
   agent-mux -E=codex -m=gpt-5.4 -e=high --async -C=/repo "one-off task"
   ```

## Role Quick-Reference

**Scanning:** `scout` (fast, mini) | `explorer` (deep, multi-file, KB-aware)
**Implementation:** `lifter` (standard) | `lifter-deep` (xhigh, hard problems)
**Cheap parallel:** `grunt` (mini) | `batch` (mini, high-volume)
**Research:** `researcher` (Claude, external) | `explorer` (Codex, internal KB)
**Architecture:** `architect` (Claude, strategic reasoning)
**Verification:** `auditor` (Codex xhigh, adversarial review)
**Writing:** `writer` (Codex, voice-matched, publishing-focused)
**Specialized:** `handoff-extractor` (session handoff extraction)

Variants swap the engine within a role: `-R=lifter --variant=claude`. Common
variants: `claude`, `gemini`, `mini`, `spark`. Run `agent-mux config roles`
for the live catalog with all engines, models, timeouts, and variants.

## Output Parsing

4 fields to check on every result:
- `status`: `completed` | `timed_out` | `failed`. Never treat non-completed as done.
- `response`: The worker's answer. Check it's non-empty and not a placeholder.
- `response_truncated` + `full_output_path`: If truncated, read the full file.
- `activity.files_changed`: What the worker actually modified.

Always parse JSON from stdout. Never parse as text. `result --json` gives
clean single JSON. For full schemas:
[references/output-contract.md](references/output-contract.md).

`preview` returns the resolved runtime shape split into `dispatch_spec` and
`result_metadata`. Use it to confirm role/profile resolution before launch.

## Failure & Recovery

```
status?
 timed_out + files_changed non-empty
   -> --recover=<dispatch_id> with continuation prompt (the delta, not re-brief)
 timed_out + files_changed empty
   -> Prompt too broad. Reframe with tighter scope. Retry ONCE.
 timed_out + heartbeat_count == 0
   -> Worker never started. Config error. Check and retry once.
 failed + error.retryable
   -> Fix cause (wrong flag, missing arg). Retry ONCE.
 failed + not retryable
   -> Structural. Escalate.
 Second failure on same step
   -> STOP. Problem is the prompt or the scope, not the effort level.
      Reframe entirely or escalate.
```

For recovery mechanics, signal delivery, artifact layout, and liveness
watchdog details: [references/recovery-guide.md](references/recovery-guide.md).

## Codex Prompt Discipline

Codex output quality is a direct function of prompt specificity. Rules:

1. **Exact file paths.** Never "explore src/". If unknown, scout first.
2. **Inline critical context.** The function, the type, the error — not "read file X".
3. **One deliverable.** "Write X to Y" or "modify Z in W". Not "audit and fix."
4. **Word budget.** "3-sentence summary" or "file path + verdict only."
5. **State the verification gate.** How the worker proves it's done.
6. **Use `=` for all string-value flags.** `-context-file=/path`, `-C=/repo`,
   `--sandbox=read-only`. Space-separated form (`-context-file /path`) can
   cause Go's flag parser to consume the next flag as the value. Always `=`.
7. **Flags before positional args.** `agent-mux wait --poll 30s <id>`, not
   `agent-mux wait <id> --poll 30s`. Go's flag parser stops at the first
   positional argument — trailing flags are silently ignored.
8. **Escape `$AGENT_MUX_ARTIFACT_DIR` in prompts.** This env var is set in
   the *worker's* environment, not the coordinator's. If your prompt is in
   double quotes, the coordinator's shell expands it to empty before the
   worker ever sees it. Fix: use `\$AGENT_MUX_ARTIFACT_DIR` or single-quote
   the prompt block.

BAD: `"Read all files in src/auth/ and identify issues"`
GOOD: `"In src/auth/middleware.go:45-80, validateToken() doesn't handle expired
refresh tokens. Add a check at line 67. refreshToken is at src/auth/refresh.go:12-30."`

## Timeout Alignment

Wrapper timeout MUST exceed agent-mux timeout by at least 60 seconds.

| Effort | agent-mux timeout | Wrapper minimum |
|--------|-------------------|-----------------|
| `low` | 120s | 180s (180000ms) |
| `medium` | 600s | 660s (660000ms) |
| `high` | 1800s | 1860s (1860000ms) |
| `xhigh` | 2700s | 2760s (2760000ms) |

Roles set their own timeouts. Check with `agent-mux config roles`.

## Anti-Patterns

- **Raw engine/model/effort when a role fits.** Roles exist. Use them.
- **Exploration prompts to Codex.** Use Claude for open-ended work.
- **Codex with `workspace-write` sandbox for multi-file output.** Omit
  `--sandbox` (default is `danger-full-access`) and use `-C=` for scope control.
- **`--sandbox none`.** Not a valid value. Valid: `danger-full-access`,
  `workspace-write`, `read-only`. Omit the flag entirely for full access.
- **Wrapper timeout == worker timeout.** Add 60s slack.
- **Ignoring `status` field.** `timed_out` is not `completed`.
- **Synchronous dispatch from Claude Code coordinators.** Use `--async`.
- **Polling `status --json` in a loop for completion.** Use `wait`.
- **Space-separated string flags.** `-context-file /path` can eat adjacent
  flags. Always use `=`: `-context-file=/path`.
- **Flags after positional args.** `wait <id> --poll 30s` silently ignores
  `--poll`. Put flags first: `wait --poll 30s <id>`.
- **Unescaped `$AGENT_MUX_ARTIFACT_DIR` in double-quoted prompts.** Expands
  to empty in the coordinator's shell. Use `\$` or single quotes.

## Reference Index

**Operational references** (skill/references/):

| Reference | Read when |
|-----------|-----------|
| [cli-flags.md](references/cli-flags.md) | Complete flag table, DispatchSpec JSON fields, precedence |
| [async-and-steering.md](references/async-and-steering.md) | Async dispatch, wait, steer, poll intervals, status.json |
| [output-contract.md](references/output-contract.md) | DispatchResult schema, preview shape, error codes |
| [recovery-guide.md](references/recovery-guide.md) | Recovery decision tree, signal mechanics, artifact layout |
| [prompting-guide.md](references/prompting-guide.md) | Per-engine prompt patterns, word budgets, context loading |
| [config-and-roles.md](references/config-and-roles.md) | TOML structure, role authoring, variants, skill injection |

**Architecture and internals** (docs/):
`docs/architecture.md`, `docs/dispatch.md`, `docs/engines.md`,
`docs/config.md`, `docs/recovery.md`,
`docs/lifecycle.md`, `docs/async.md`, `docs/steering.md`.

Discover paths: `agent-mux config --sources`.
