---
name: agent-mux
description: |
  Cross-engine dispatch layer for AI coding agents. Use when you need to:
  launch a worker on Codex/Claude/Gemini/agy, recover a timed-out dispatch, steer
  a running agent, or fan out parallel work. Profiles live in ~/.agent-mux/prompts/.
---

# agent-mux

## QUICKSTART (95% of use cases)

**1. Discover workers:**
```bash
agent-mux config prompts        # live roster with effort defaults
agent-mux config prompts --json # structured
agent-mux config engines --json # engine capabilities + model allowlists
```
Selection: scout for reads, lifter for writes, architect for plans, grunt for bulk edits.

**2. Profile dispatch (the 80% case):**
```bash
agent-mux -P=lifter -C=/repo "Fix retry logic in src/client.go" 2>/dev/null
```
Blocks until done. Result is stdout JSON with `status`, `response`, `activity.files_changed`.

**3. Prompt-file dispatch (for heredoc quoting issues):**
```bash
cat > /tmp/prompt.md << 'EOF'
Fix the `parseConfig()` function. Handle edge cases:
- Empty input returns `{}`
- Nested backticks in strings
EOF
agent-mux -P=lifter -C=/repo --prompt-file=/tmp/prompt.md
```
Or use `--stdin` JSON mode for full quoting control.

**4. Effort note:**
Don't override `-e` unless upgrading. Check `config prompts` for profile defaults -- passing `-e=high` to an xhigh profile (scout, auditor, grunt, architect) is a DOWNGRADE.

**Key flags:** `-P` profile, `-E` engine, `-m` model, `-e` effort, `-C` cwd, `--async`. Note: `-E` (engine) and `-e` (effort) are different — case matters.

**5. Sync wait + result:**
```bash
agent-mux -P=lifter -C=/repo "task"  # blocks until done
# stdout is JSON: {"status":"completed","response":"...","activity":{"files_changed":2},...}
```

**Async-first:** Prefer `--async` for dispatches over 2 minutes — lets you monitor, steer, or bail early. Sync is fine for quick probes.

**6. Async pattern:**
```bash
agent-mux -P=scout --async -C=/repo "probe" > /tmp/dispatch.json 2>/dev/null &
# ... do other work ...
wait
ID=$(jq -r .dispatch_id < /tmp/dispatch.json)
agent-mux result "$ID"
```
Note: `--async` does NOT daemonize -- the CLI runs to completion in background. Shell `&` is required for true parallelism.

---

## DETAILS (remaining 5%)

**1. Engine override:**
```bash
agent-mux -P=researcher -E=gemini -m gemini-3.1-pro-preview -C=/repo "Analyze auth module"
```
- **Codex**: implementation, debugging, precise edits
- **Claude**: planning, synthesis, review
- **Gemini**: analysis, second opinion (models: `gemini-3-flash-preview`, `gemini-3.1-pro-preview`)
- **agy**: experimental CLI-first model access. Plain stdout; agent-mux internally invokes the local agy CLI with its fixed `--sandbox`, discovers Antigravity conversation IDs from `agy.log`, and uses inbox + `--conversation` for nudge/redirect resume. Operator-supplied sandbox/permission/reasoning/max-turn/full-access options are rejected. This does not imply plugins, MCP, browser automation, Google services, or provider service actions.

Gemini ignores `-e` -- use model selection for depth control.

**2. Steering:**
- `steer abort <id>` -- SIGTERMs async host when live; otherwise writes control.json for watchdog
- `steer redirect <id> "new direction"` -- requires live FIFO or a resume-capable engine; `agy` uses inbox + conversation resume
- `steer nudge <id>` -- requires live FIFO or a resume-capable engine; `agy` uses inbox + conversation resume

**3. Data model (3 paths):**
- `~/.agent-mux/dispatches/<ULID>/` -- durable (meta.json, result.json)
- `$XDG_RUNTIME_DIR/agent-mux/<ULID>/` or `/tmp/agent-mux-$UID/<ULID>/` -- runtime (events.jsonl, inbox.md)
- `~/.agent-mux/prompts/` -- profiles; `<cwd>/.agent-mux/` -- skills/, hooks/

**4. Gaal tracing:** (session_id available after session start; absent for startup failures)
```bash
jq -r .session_id ~/.agent-mux/dispatches/<id>/meta.json
gaal inspect <session_id>
```

**5. Bash timeout:**
Claude Code defaults to 120s. Long dispatches need explicit `timeout: 300000` or `run_in_background: true` on the Bash tool call.

**6. Recovery:**
- `timed_out` + useful artifacts -> `--recover=<id>` with delta prompt
- `failed` -> check cause, retry once or escalate

```bash
agent-mux -P=lifter --recover=<id> -C=/repo "Finish remaining parser tests"
```

**7. Landmines:**
- `--async` does NOT daemonize -- use shell `&`
- `status: completed` does not mean task success -- check `response`
- `-e` is effort, `-E` is engine (case matters)
- agy rejects explicit portable sandbox, permission, reasoning, max-turn, and full-access options; use model/add-dir/prompt only
- `--recover` resumes with NEW prompt, does not fetch result (use `result <id>`)
- `-e=high` on xhigh profiles is a downgrade, not a floor
- `wait` has no `--timeout` — only `--poll` for cadence (e.g., `wait --poll 30s <id>`)

**8. References:** Deep dives in `references/`:
- pitfalls.md -- audit-grounded landmines with session evidence
- async-and-steering.md -- lifecycle, fan-out, steering mechanics
- recovery-guide.md -- timeout/failure handling
- gemini-specifics.md -- Gemini approval mode, resume quirks
