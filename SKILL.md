---
name: agent-mux
description: |
  Dispatch layer that bridges AI coding harnesses — Codex, Claude, and
  Gemini — through one JSON contract and one CLI. Use this skill when you
  need to: spawn a worker agent across any engine, run a TOML-defined
  pipeline, recover or signal a live dispatch, parse schema_version:1
  JSON output contracts, configure roles/variants/pipelines in TOML,
  inject skills or coordinator personas, or coordinate multi-model tasks.
  Covers: JSON-first --stdin invocation, role-based routing from TOML
  config, variant selection, pipeline orchestration with fan-out and
  handoff modes, recovery continuation, mid-flight signaling, hooks,
  event streaming, output contract parsing (dispatch + pipeline + signal),
  timeout alignment, liveness supervision, and context-loading tools.
  Keywords: subagent, dispatch, worker, codex, claude, gemini, pipeline,
  role, variant, recover, signal, agent-mux, spawn agent, engine, multi-model,
  TOML config, fan-out, handoff, orchestration, coordinate workers.
---

# agent-mux

The dispatch substrate that lets any LLM coordinate any other LLM. One CLI,
one JSON contract, three engines (Codex, Claude, Gemini). TOML-driven roles,
variants, and pipelines turn good dispatch practices into reusable config.
The calling LLM decides what to do — agent-mux handles the how.

## Why agent-mux Exists

Three problems motivated building this:

1. **Claude Code cannot natively use Codex as a subagent.** agent-mux bridges
   that gap — Claude architects a plan, dispatches implementation to Codex,
   and verifies the result, all through one CLI.

2. **Codex has no subagent system at all.** agent-mux gives it orchestration
   primitives — pipelines, fan-out, recovery — without baking orchestration
   logic into Codex itself.

3. **The 10x pattern needs a coordinator.** The highest-leverage workflow is
   Claude architects, Codex executes, Claude verifies. agent-mux makes that
   loop a single pipeline invocation instead of manual session juggling.

These three problems collapse into one design:

**Tool, not orchestrator.** agent-mux is a dispatch layer where the LLM makes
all decisions. Roles and pipelines are presets that condense good practices
into config — the LLM decides when and how to use them. No orchestration
logic is baked into the binary.

**Job done is holy.** Never discard completed work. Timeout kills the process,
not the artifacts. Every dispatch has an artifact path. Workers write
incrementally. Recovery is first-class.

**Errors are steering signals.** Every error is crafted for the calling LLM to
self-correct — what failed, why, what to try next. Not generic status codes.

**Single-shot with curated context.** One well-prompted dispatch with narrow
context beats a swarm of under-specified workers. Orchestration is explicit
escalation, not baseline.

**Config over code.** Roles, pipelines, models, timeouts — all in TOML. The
Go binary is generic; the config makes it specific.

**Simplest viable dispatch.** If it can be a CLI call, it stays a CLI call.
If it can be config, it stays config. Code is the last resort.

---

## Canonical Invocation — JSON-first via --stdin

Every dispatch MUST use `--stdin` with a JSON payload. This is the only
sanctioned invocation pattern.

```bash
printf '{"role":"lifter","prompt":"Implement retries in src/http/client.ts","cwd":"/repo"}' | agent-mux --stdin
```

Why JSON-first:
- Every parameter is explicit and visible
- Auditable — the agent shows the JSON before executing
- No flag-ordering bugs or shell escaping issues
- Composable — roles resolve engine/model/effort/timeout from config.toml

CLI flags exist for interactive/debug use but agents MUST use `--stdin` JSON.

---

## Two-Step Verification Protocol

Agents MUST NOT fire-and-forget dispatches. Follow this gate:

**Step 1 — Construct.** Build the JSON dispatch payload.

**Step 2 — Present.** Show the payload to the user (or log it) for review.

```
I'll dispatch this to agent-mux:
{"role":"lifter","prompt":"...","cwd":"/repo"}

Shall I proceed?
```

**Step 3 — Execute.** Only after approval:

```bash
printf '<the-approved-json>' | agent-mux --stdin
```

This is a verification gate, not a suggestion. The agent must show what it
will dispatch before dispatching it.

---

## Role-Based Dispatch (Preferred)

Roles are the primary dispatch mechanism. A role resolves engine, model,
effort, timeout, skills, and system prompt from config.toml.

```json
{"role":"scout","prompt":"Find all usages of deprecated API","cwd":"/repo"}
```

### Live Role Catalog (from coordinator config.toml)

| Role | Engine | Model | Effort | Timeout | Purpose |
|------|--------|-------|--------|---------|---------|
| `scout` | codex | gpt-5.4-mini | low | 180s | Quick scans, file discovery |
| `explorer` | codex | gpt-5.4 | high | 600s | Deep read, multi-file analysis |
| `researcher` | claude | claude-opus-4-6 | high | 900s | Web search, synthesis, analysis |
| `architect` | claude | claude-opus-4-6 | high | 900s | Design, planning, architecture |
| `lifter` | codex | gpt-5.4 | high | 1800s | Implementation, code changes |
| `lifter-deep` | codex | gpt-5.4 | xhigh | 2400s | Complex implementation, deep work |
| `grunt` | codex | gpt-5.4-mini | medium | 600s | Cheap parallel workers |
| `batch` | codex | gpt-5.4-mini | high | 900s | High-volume batch processing |
| `auditor` | codex | gpt-5.4 | xhigh | 2700s | Verification, code audit |
| `writer` | codex | gpt-5.4 | high | 1500s | Blog posts, documentation |
| `handoff-extractor` | codex | gpt-5.4-mini | high | 120s | Session handoff extraction |

### Variants — Engine Swaps Within a Role

Variants let you keep the same role semantics but swap the engine:

```json
{"role":"lifter","variant":"claude","prompt":"...","cwd":"/repo"}
```

Common variants across roles: `claude`, `gemini`, `mini`, `spark`.
See [references/config-guide.md](references/config-guide.md) for the full
variant table.

### Raw Override (Escape Hatch)

When no role fits, specify engine/model/effort directly:

```json
{"engine":"codex","model":"gpt-5.4","effort":"high","prompt":"...","cwd":"/repo"}
```

This is the escape hatch, not the default path.

---

## Core Dispatch Modes

### 1. Single Dispatch

```json
{"role":"lifter","prompt":"Build the auth middleware","cwd":"/repo"}
```

### 2. Pipeline

```json
{"pipeline":"build","prompt":"Redesign the auth flow","cwd":"/repo","engine":"codex"}
```

Pipeline mode returns a different JSON shape. See
[references/output-contract.md](references/output-contract.md).

### 3. Recovery

Continue a timed-out or interrupted dispatch:

```json
{"engine":"codex","continues_dispatch_id":"01KM...","prompt":"Finish the remaining tests","cwd":"/repo"}
```

### 4. Signal

Send a steering message to a running dispatch:

```bash
agent-mux --signal=01KM... "Focus on auth paths; skip tests"
```

Signal returns a compact JSON ack. Actual injection happens at an event
boundary when the harness has a resumable session/thread ID.

---

## Output Contract Summary

All output goes to `stdout` as JSON (`schema_version: 1`).

### Dispatch Result

```json
{
  "schema_version": 1,
  "status": "completed",
  "dispatch_id": "01KM...",
  "dispatch_salt": "mint-ant-five",
  "trace_token": "AGENT_MUX_GO_01KM...",
  "response": "...",
  "response_truncated": false,
  "full_output": null,
  "handoff_summary": "...",
  "artifacts": [],
  "partial": false,
  "recoverable": false,
  "reason": "",
  "error": null,
  "activity": {"files_changed":[],"files_read":[],"commands_run":[],"tool_calls":[]},
  "metadata": {"engine":"codex","model":"gpt-5.4","role":"lifter","tokens":{"input":0,"output":0,"reasoning":0,"cache_read":0,"cache_write":0},"turns":0,"cost_usd":0,"session_id":""},
  "duration_ms": 12345
}
```

**Status values:** `completed`, `timed_out`, `failed`

Callers MUST check `status` before treating output as final. A `timed_out`
result may still have useful `response` and `artifacts`.

### Pipeline Result

```json
{
  "pipeline_id": "01KM...",
  "status": "completed",
  "steps": [...],
  "final_step": {...},
  "duration_ms": 12345
}
```

**Pipeline status:** `completed`, `partial`, `failed`

For full schemas with all fields, see
[references/output-contract.md](references/output-contract.md).

---

## Engine Selection

| Use case | Role | Engine |
|----------|------|--------|
| Implementation, code edits, debugging | `lifter` | codex |
| Cheap high-volume parallel work | `grunt` | codex (mini) |
| Fast scans, light edits | `grunt` variant `spark` | codex (spark) |
| Architecture, synthesis, planning | `architect` | claude |
| Deep analysis, web research | `researcher` | claude |
| Second opinion, contrast check | any role + `gemini` variant | gemini |
| Code audit, verification | `auditor` | codex |

For engine-specific prompting tips and model details, see
[references/engine-comparison.md](references/engine-comparison.md) and
[references/prompting-guide.md](references/prompting-guide.md).

---

## Timeout Alignment

When wrapping agent-mux in another process (Claude Code Task, shell timeout),
the wrapper MUST exceed agent-mux timeout by at least 60 seconds.

```
wrapper_timeout = agent_mux_timeout + 60_000ms
```

| Effort | agent-mux timeout | Wrapper minimum |
|--------|-------------------|-----------------|
| `low` | 120s | 180s |
| `medium` | 600s | 660s |
| `high` | 1800s | 1860s |
| `xhigh` | 2700s | 2760s |

Roles set their own timeouts. Check the role catalog above or
[references/config-guide.md](references/config-guide.md).

---

## DispatchSpec Fields (--stdin JSON)

Essential fields for JSON dispatch:

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `prompt` | string | yes | The task prompt |
| `cwd` | string | yes | Working directory |
| `role` | string | preferred | Resolves engine/model/effort/timeout from config |
| `variant` | string | no | Engine swap within a role |
| `engine` | string | role or this | `codex`, `claude`, `gemini` |
| `model` | string | no | Override role's model |
| `effort` | string | no | `low`, `medium`, `high`, `xhigh` |
| `system_prompt` | string | no | Appended system context |
| `skills` | string[] | no | Skill names to inject |
| `pipeline` | string | no | Named pipeline from config |
| `context_file` | string | no | Path to large context file |
| `timeout_sec` | int | no | Override timeout in seconds |
| `continues_dispatch_id` | string | no | Recovery: prior dispatch ID |
| `profile` | string | no | Coordinator persona name |
| `response_max_chars` | int | no | Truncate response (default 2000) |
| `full_access` | bool | no | Default true |
| `allow_subdispatch` | bool | no | Default true |
| `max_depth` | int | no | Recursive depth limit (default 2) |

For the complete field reference including pipeline-internal fields, see
[references/cli-flags.md](references/cli-flags.md).

---

## Anti-Patterns

- **Do not parse output as text.** Always parse JSON from stdout.
- **Do not use bare CLI flags.** Use `--stdin` JSON for programmatic dispatch.
- **Do not assemble raw engine/model/effort combos.** Use roles.
- **Do not fire-and-forget.** Show the dispatch JSON before executing.
- **Do not use `--permission-mode` with Codex.** Use `--sandbox` instead.
- **Do not send exploration prompts to Codex.** Use Claude for open-ended work.
- **Do not make wrapper timeout equal to worker timeout.** Add 60s slack.
- **Do not treat `--signal` ack as proof of delivery.** It confirms inbox
  write only.
- **Do not ignore `status` field.** A `timed_out` result is not `completed`.
- **Do not assume pipeline output has dispatch fields.** Pipeline returns
  `PipelineResult`, not `DispatchResult`.
- **Do not use `xhigh` effort for routine tasks.** `high` is the workhorse.
- **Do not inline giant context blobs.** Use `--context-file` or `--skill`.

---

## Bundled References

| Path | Read when |
|------|-----------|
| [references/cli-flags.md](references/cli-flags.md) | You need the complete flag table or DispatchSpec field reference |
| [references/config-guide.md](references/config-guide.md) | You need TOML config structure, role/variant definitions, config resolution order |
| [references/output-contract.md](references/output-contract.md) | You need exact JSON schemas for dispatch, pipeline, signal, events, error codes |
| [references/engine-comparison.md](references/engine-comparison.md) | You need engine-specific behavior, harness details, permission/sandbox mapping |
| [references/prompting-guide.md](references/prompting-guide.md) | You are crafting prompts, writing pipeline steps, phrasing recovery/signals |
| [references/pipeline-guide.md](references/pipeline-guide.md) | You need pipeline TOML structure, fan-out, handoff modes, step chaining |
| [references/recovery-signal.md](references/recovery-signal.md) | You need recovery continuation, signal delivery, artifact directory layout |
