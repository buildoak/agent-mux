# Landmines - agent-mux Common Pitfalls

Operational hazards to check before aborting, recovering, steering, or trusting
a dispatch result.

---

## 1. Phantom Dispatches from Unknown Verbs

`kill`, `cancel`, `stop`, `terminate`, `signal`, and bare `abort` are not
lifecycle commands. Current agent-mux rejects those anti-pattern verbs before
dispatch instead of treating them as prompts.

Use the real abort path:

```bash
agent-mux steer abort <id>
```

## 2. `status: completed` Does Not Mean Task Accomplished

`completed` means the engine process exited without an agent-mux runtime error.
It does not prove the worker produced useful output or satisfied your workflow
contract.

Before trusting a result, check non-empty response content and your consumer's
own completion signal, such as an expected artifact, test result, or explicit
handoff field.

## 3. `--recover` Is a Resume Verb, Not a Fetch Verb

Use `agent-mux result <id>` to retrieve a finished async dispatch. `--recover
<id> "<new prompt>"` starts a new continuation dispatch with the new prompt
prepended by recovery context. It is not a result retrieval shortcut.

Recovery uses the new dispatch's resolved engine and profile. Treat it as
"continue from here with these instructions," not "show me what happened."

## 4. `wait` Flag Ordering Matters

Go flag parsing expects flags before positional args, including lifecycle
commands:

```bash
agent-mux wait --poll 30s <id>
agent-mux wait --json <id>
```

`wait` has no wall-clock `--timeout` flag. `--poll` only controls polling
cadence. To cap wall-clock time, wrap with shell `timeout` or use your
tool-level timeout.

## 5. Codex Nudge and Redirect Are Not Live Stdin

Current Codex runs do not expose a live stdin FIFO; `stdin_pipe_ready` stays
false. `steer nudge` and `steer redirect` fall back to inbox plus `codex exec
resume`.

That means delivery waits for a safe boundary and may not interrupt active
reasoning or tools. `steer abort` is still the immediate control path.

## 6. Gemini Resume Can Degrade to `latest`

Gemini CLI `--resume` accepts `"latest"` or a numeric index, not UUID session
IDs. When agent-mux sees a UUID session ID, it degrades resume to `latest`.

Avoid Gemini nudge/redirect when preserving a specific live session context
matters, especially if multiple Gemini sessions overlap.

## 7. `-e` and `-E` Are Different Flags

Lowercase `-e` is effort (`low`, `medium`, `high`, `xhigh`). Uppercase `-E` is
engine (`codex`, `claude`, `gemini`). Lowercase `-m` is model.

The slash-path shorthand `codex/gpt-5.4/xhigh` is not parsed by agent-mux. Use
explicit flags:

```bash
agent-mux -E codex -m gpt-5.4 -e xhigh -C /repo "Prompt"
```

## 8. Stuck-Dispatch Heuristics Lie

These can be normal:

- CPU is 0% during API waits
- `tools_used: 0` for reasoning-heavy workers
- `files_changed: 0` for analysis tasks
- quiet event streams during long model roundtrips

Byte growth in `events.jsonl` is one liveness signal, but lack of growth during
API roundtrips can still be normal. Take two samples and correlate with
`status --json`, process liveness, and recent events before aborting.

## 9. Multi-ID Operations Do Not Exist

Lifecycle and steer operations are single-ID only:

```bash
agent-mux status <id>
agent-mux result <id>
agent-mux wait <id>
agent-mux steer abort <id>
```

Use shell loops for multiple dispatches.

## 10. Recovering a Lost Dispatch ID

Capture async acks to files when backgrounding:

```bash
agent-mux --async -P=scout -C=/repo "Scan auth" > /tmp/auth.json 2>/dev/null &
wait
jq -r .dispatch_id /tmp/auth.json
```

If the ack was lost, list recent dispatches:

```bash
agent-mux list --limit 5 --json
```

## 11. `--async` Does Not Daemonize

`--async` emits an ack early, detaches caller stdio after the ack, and keeps the
same `agent-mux` process running until dispatch completion. Use shell `&` for
true background fan-out.

Avoid pipeline patterns that assume the process exits immediately after the ack.
Capture stdout to a file and background the command instead.

## 12. Liveness from `events.jsonl` Requires Two Samples

A single `wc -c` or `stat` only proves the file exists. Compare two sizes
separated by a delay:

```bash
artifact_dir=$(agent-mux inspect <id> --json | jq -r .artifact_dir)
s1=$(wc -c < "$artifact_dir/events.jsonl")
sleep 30
s2=$(wc -c < "$artifact_dir/events.jsonl")
[ "$s1" = "$s2" ] && echo "no event growth" || echo "events still growing"
```

No event growth is a clue, not a verdict. Correlate it with status, process
liveness, and the last event before deciding to abort.
