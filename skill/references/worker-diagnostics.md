# Worker Diagnostics & Silence Forensics

How to diagnose whether a silent worker is genuinely stalled or legitimately
working. This replaces automatic stall/freeze detection, which was removed
because it killed healthy workers more often than stuck ones.

---

## Why We Don't Auto-Kill Silent Workers

agent-mux used to monitor stdout silence and kill workers that went quiet for
too long. This was the single largest source of false kills in production:

- **Codex CLI emits zero NDJSON during API roundtrips.** When Codex sends a
  106K-190K token context to the API, the roundtrip takes 2-8 minutes. During
  that window, the NDJSON stream is completely silent -- no heartbeats, no
  events, nothing. The old stall detector treated this as a hang and killed
  the worker, losing all progress.

- **Gemini enters extended thinking phases.** Gemini 3.1 Pro can spend 1-3
  minutes in deep reasoning with zero output. The stall detector couldn't
  distinguish thinking from hanging.

- **False kills cost more than patience.** A killed worker loses all
  accumulated reasoning, tool state, and partial artifacts. Restarting from
  scratch (or even with `--recover`) is always more expensive than waiting an
  extra few minutes for a legitimate API roundtrip to complete.

- **The global dispatch timeout is the safety net.** Every dispatch has a hard
  timeout (`timeout_sec` + `grace_sec`). If a worker is truly stuck, the
  timeout will kill it. The timeout is honest about its intent: "this task
  should complete within N seconds." The stall detector was pretending to know
  something it couldn't: "this worker is stuck right now."

The new model: trust the worker to complete within its timeout. Provide
forensic tools for operators who want to investigate silence. Kill manually
when investigation confirms a genuine hang.

---

## How to Diagnose a Silent Worker

### 1. Check status.json

```bash
agent-mux status <id> --json
```

The `last_activity` field shows what the worker was last doing, and the `ts`
timestamp shows when that snapshot was taken. `status.json` updates on the
5-second watchdog tick.

Key fields to examine:

| Field | What it tells you |
|-------|-------------------|
| `last_activity` | The last thing the worker reported doing |
| `elapsed_s` | How long the dispatch has been running |
| `tools_used` | How many tool calls have happened so far |
| `files_changed` | How many files have been written |
| `state` | `running`, `completed`, `failed`, `timed_out` |

If `last_activity` is `initializing`, the worker may still be in its first API
roundtrip. This can be normal; the threshold depends on timeout, context size,
and model.

### 2. Read events.jsonl

The event log is the most informative diagnostic artifact. The last few events
reveal the worker's state before silence began.

```bash
# Read the last 10 events
tail -10 "$(agent-mux inspect <id> --json | jq -r .artifact_dir)/events.jsonl"
```

**Key patterns in the event stream:**

| Last event before silence | Likely cause | Action |
|---------------------------|--------------|--------|
| `tool_end` | API roundtrip after tool use. Normal for large contexts. | Wait. |
| `dispatch_start` with no subsequent events | Model hasn't responded to first turn yet. Normal with large system prompts. | Wait up to 5 min. |
| `response` with no subsequent events | Worker may have finished but exit wasn't clean, or may be genuinely stuck. | Probe with nudge. |
| `heartbeat` with `last_activity` unchanged for 3+ beats | Worker is alive but not making progress. Suspicious. | Probe with nudge. |
| `error` event | Worker hit an error and may be in a retry loop or stalled. | Read the error, then decide. |
| `tool_start` with no `tool_end` | Long-running tool (cargo build, large test suite). | Wait -- check if the tool's process is alive. |

### 3. Check Engine-Specific Session Files

Each engine maintains internal state that may show activity even when the
NDJSON stream is silent.

**Codex:**
- Inspect harness-native session logs under `~/.codex/sessions/YYYY/MM/DD/`
  when available
- These logs can expose activity that is not visible in the adapter NDJSON
  stream, but they are not guaranteed for every run

**Gemini:**
- Check if the process is still alive: `agent-mux status <id> --json` will
  show `state: running` if the host process exists
- Gemini's deep thinking phases produce no output at all -- process liveness
  is the primary health signal

**Claude:**
- Subagent JSONL files show internal activity
- Claude sessions resume cleanly, so a nudge is low-cost for diagnosis

### 4. Probe the Worker

If passive checks are inconclusive, send a nudge:

```bash
agent-mux steer <id> nudge "Status check -- are you still working?"
```

If the worker responds (you'll see new events in `events.jsonl` or the nudge
triggers a resume), it's alive. A nudge may queue until a turn boundary or
resume path; no response after 60 seconds is suggestive, not definitive.

Both argument orderings work:

```bash
agent-mux steer <id> nudge "status check"
agent-mux steer nudge <id> "status check"
```

### 5. Kill If Needed

When investigation confirms a genuine hang:

```bash
agent-mux steer <id> abort
agent-mux steer abort <id>
```

`agent-mux steer <id> abort` first tries SIGTERM via `host.pid`; if no live host
exists it writes `control.json`. Check `result --json` and `events.jsonl`
afterward for the actual classification.

---

## Decision Framework

```text
Worker is silent. What do I do?

1. How long has it been silent?
   < 5 min after tool use    -> Almost certainly API roundtrip. Wait.
   < 5 min after dispatch    -> First-turn latency. Wait.
   5-10 min                  -> Check events.jsonl for context.
   > 10 min with no tool use -> Suspicious. Go to step 2.
   > global timeout          -> Will be killed automatically. No action needed.

2. What was the last event?
   tool_end / tool_start     -> Likely working. Wait or nudge.
   dispatch_start only       -> Model may be struggling. Nudge.
   response                  -> May be stuck post-response. Nudge.
   error                     -> Read it. May need abort + retry.

3. Nudge response?
   New events appeared       -> Alive. Let it work.
   No response after 60s     -> Suggestive, not definitive. Re-check status/events before aborting.
```

---

## Common False-Alarm Patterns

These empirical silence durations are common examples, not guarantees:

| Pattern | Expected silence | Why |
|---------|------------------|-----|
| Codex GPT-5.4 with `xhigh` reasoning, 100K+ token context | 2-8 min | API roundtrip for large context + high reasoning effort |
| Codex GPT-5.4-mini with `high` reasoning, 50K+ context | 1-3 min | Smaller model but still substantial roundtrip |
| Gemini 3.1 Pro deep thinking | 1-3 min | Extended reasoning phase with no output |
| Gemini 2.5 Pro complex analysis | 1-2 min | Similar deep thinking pattern |
| First turn with large system prompt + context file | 3-5 min | Initial model response includes reading and planning |
| Claude Opus with large skill injection | 1-3 min | Processing injected skill content |
| Any engine after file write + test execution | 1-5 min | Running test suites or build commands |

**Rule of thumb:** If the worker was actively using tools before going silent,
it's almost certainly in an API roundtrip. The larger the context window and
the higher the reasoning effort, the longer the silence.

---

## Relationship to Global Timeout

The global dispatch timeout (`timeout_sec` + `grace_sec`) is the only
automatic kill mechanism. It works like this:

1. At `timeout_sec`, emit `timeout_warning` event
2. Write a wrap-up message to the worker's inbox
3. Start the grace timer
4. If grace expires, stop the worker and return `timed_out`; hard kill uses at
   least 10s final stop grace even if `grace_sec` is smaller

The timeout is a contract: "this task should complete within N seconds." It
doesn't care about silence patterns -- it cares about total wall time. This
is the right abstraction because:

- API roundtrip duration is unpredictable and engine-dependent
- Reasoning depth is a quality dial, not a liveness signal
- The operator who sets the timeout knows how long the task should take
- False timeout kills (task genuinely needs more time) are recoverable via
  `--recover`, unlike false stall kills which lost all context
