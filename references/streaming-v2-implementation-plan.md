---
date: 2026-03-29
status: draft
engine: claude
---

# Streaming v2: Implementation Plan

## Architecture Summary

**Event emission path:** `Emitter.Emit()` (event.go:79) is the single choke point. Every event — heartbeat, tool_start, file_write, error — flows through `Emit()`, which writes to both `eventWriter` (stderr) and `eventLog` (events.jsonl). There is no branching today; both destinations get identical output.

**Key insight:** The `eventWriter` is `stderr`, passed from `dispatchSpec()` (main.go:1538). The `Emitter` owns both outputs. Adding a stream mode filter inside `Emit()` is clean — one code path, one gate.

**Stdin/steering path:** `stdinPipe` is open for all engines (loop.go:351). Codex responds to `StdinNudge() → "\n"`. Claude and Gemini return `nil` — they don't read stdin during execution. All three support resume via `ResumeArgs()`. The inbox→resume cycle (loop.go:475-542) is the universal steering channel.

---

## Wave 1: StreamMode in Emitter + CLI Flag

### Files to Modify

**`internal/event/event.go`** — Add `StreamMode` type and filter logic to `Emitter`.

1. Add type and constants after line 16:
   ```go
   type StreamMode int
   const (
       StreamSilent  StreamMode = iota // default: bookend + failure only
       StreamNormal                     // current behavior: all events to stderr
       StreamVerbose                    // raw harness lines + all events
   )
   ```
2. Add `streamMode StreamMode` field to `Emitter` struct (line 46).
3. Add `func (e *Emitter) SetStreamMode(m StreamMode)` setter.
4. Modify `Emit()` (line 79): after writing to `eventLog`, gate `eventWriter` writes:
   ```go
   // Always write to event log
   if e.eventLog != nil { ... }
   // Gate stderr based on stream mode
   if e.eventWriter != nil && e.shouldEmitToStderr(evt.Type) { ... }
   ```
5. Add `shouldEmitToStderr(eventType string) bool`:
   - `StreamNormal`/`StreamVerbose`: return true (all events)
   - `StreamSilent`: return true only for: `dispatch_start`, `dispatch_end`, `error`, `frozen_warning`, `frozen_killed`, `timeout_warning`, `long_command_detected`, `response_truncated`

**`internal/engine/loop.go`** — Pass stream mode to emitter.

1. Add `streamMode event.StreamMode` field to `LoopEngine` struct (line 26).
2. Add `func (e *LoopEngine) SetStreamMode(m event.StreamMode)` setter.
3. In `Dispatch()`, after `NewEmitter()` call (line 136), add: `emitter.SetStreamMode(e.streamMode)`.

**`cmd/agent-mux/main.go`** — Add `--stream` flag, wire to engine.

1. Add `stream bool` to `cliFlags` struct (line 74).
2. Register flag (near line 1204): `bindBool(fs, &flags.stream, "Stream all events to stderr", false, "stream", "S")`
3. In `dispatchSpec()` (line 1538-1540), after `eng.SetVerbose()`:
   ```go
   switch {
   case verbose:
       eng.SetStreamMode(event.StreamVerbose)
   case flags.stream:  // need to thread flags.stream through
       eng.SetStreamMode(event.StreamNormal)
   default:
       eng.SetStreamMode(event.StreamSilent)
   }
   ```
4. Thread the stream flag: change `dispatchSpec` signature to accept `stream bool` alongside `verbose bool`. Update the two call sites (lines 498, 506).
5. Add `"stream"` and `"S"` to the `stdinDispatchFlagsSet` ignore list.

### Verification Gate

1. `go build ./...` — compiles.
2. `go test ./internal/event/...` — add test: emit 10 event types in Silent mode, assert only bookend+failure types appear in a `bytes.Buffer` eventWriter, while all 10 appear in the eventLog file.
3. `go test ./internal/engine/...` — existing tests pass (they don't check stderr content).
4. Manual: `ax run -E codex "echo hello" 2>/tmp/stderr.log` — stderr has only `dispatch_start` + `dispatch_end`. `events.jsonl` has heartbeats, tool_start, etc.
5. Manual: `ax run -E codex --stream "echo hello"` — stderr has all events (current behavior).

---

## Wave 2: Preview Compression + ax-eval Cases

### Files to Modify

**`cmd/agent-mux/main.go`** — Compress preview event prompt echo.

1. In the preview command handler (find `commandPreview` case), the `previewPrompt.Excerpt` already truncates to 280 runes. Verify this is the only place prompt text leaks to stderr. If `dispatch_start` event includes prompt text — check `EmitDispatchStart` (event.go:109). It does NOT include prompt text; it only has engine/model/timeout/skills. No change needed here.

**`tests/axeval/cases.go`** — Add streaming mode test cases.

1. Add `CatStreaming Category = "streaming"` to types.go.
2. Add two new cases:
   ```
   "silent-default" — dispatch with no --stream flag, verify:
     - Result.RawStderr contains "dispatch_start" and "dispatch_end"
     - Result.RawStderr does NOT contain "heartbeat" or "tool_start"
     - events.jsonl contains "heartbeat" (full log unaffected)

   "stream-flag" — dispatch with --stream flag, verify:
     - Result.RawStderr contains "heartbeat" and "tool_start"
   ```
3. Implementation: the runner (runner.go:58) currently uses `--stdin --yes`. For the stream test, append `"--stream"` to cmd args. Add an optional `ExtraArgs []string` field to `TestCase`.

**`tests/axeval/eval.go`** — Add stderr content checkers.

1. `stderrContains(substr string)` — checks `Result.RawStderr`.
2. `stderrNotContains(substr string)` — inverse.
3. `eventLogContains(eventType string)` — reads from `Result.Events` (already parsed from events.jsonl).

### Verification Gate

1. `go test -tags=axeval ./tests/axeval/ -run TestSilentDefault` — passes.
2. `go test -tags=axeval ./tests/axeval/ -run TestStreamFlag` — passes.
3. All 12 existing ax-eval cases pass unchanged (they read from events.jsonl, not stderr).

---

## Soft Steering Design

### Per-Engine Capability Matrix

| Capability | Codex | Claude Code | Gemini CLI |
|---|---|---|---|
| `StdinNudge()` | `"\n"` | `nil` | `nil` |
| Stdin pipe open? | Yes | Yes (unused) | Yes (unused) |
| Stdin responsive? | **Yes** — Codex reads stdin | **No** — Claude ignores stdin | **No** — Gemini ignores stdin |
| Resume supported? | Yes (`exec resume <thread> <msg>`) | Yes (`--resume <sid> --continue <msg>`) | Yes (`--resume <sid> -p <msg>`) |
| Soft steer (no restart)? | **Yes** — write to stdin | **No** | **No** |
| Hard steer (restart)? | Yes — inbox → resume | Yes — inbox → resume | Yes — inbox → resume |

### UX Design: `ax steer <id> <action>`

**`ax steer <id> nudge ["message"]`:**
- Codex: write message (or `"\n"` if empty) directly to `stdinPipe`. No restart. Fast. Worker sees it as new user input mid-conversation.
- Claude/Gemini: write to `inbox.md`. Triggers resume cycle on next inbox poll (≤250ms). Costs a harness restart but session resumes fast.

**`ax steer <id> redirect "instructions"`:**
- All engines: write to `inbox.md` → resume cycle. Redirect always needs rich context injection, which only the resume cycle provides.

**`ax steer <id> abort`:**
- Foreground: write `abort` to a new `control.json` in artifact dir. Watchdog reads it on next tick (≤5s) and sends SIGTERM.
- Async (Tier 2, future): send SIGTERM to `host.pid`.

**`ax steer <id> extend <seconds>`:**
- Write `control.json` with `extend_kill_seconds`. Watchdog reads on next tick.

**`ax steer <id> status`:**
- Read last event from `events.jsonl`. No process interaction.

### Implementation Sketch (Tier 1 scope: nudge + redirect only)

**New file: `cmd/agent-mux/steer.go`** — Handles `ax steer` subcommand parsing.

1. Parse: `ax steer <dispatch_id> <action> [message]`
2. Resolve artifact dir via `recovery.ResolveArtifactDir(dispatchID)`.
3. For `nudge` on Codex: need a way to access the stdin pipe. Problem: the pipe lives in the `ax run` process, not the `ax steer` process. **Solution:** use a named pipe (FIFO) or Unix socket in the artifact dir. The dispatch loop reads from it and forwards to `stdinPipe`.
4. Simpler alternative for Tier 1: **all steering goes through inbox.** Codex stdin nudge is a nice-to-have optimization; inbox→resume works for all engines. Defer direct stdin injection to Tier 2 when we have the async host process.

**Tier 1 steering = inbox-only.** `ax steer <id> nudge "wrap up"` writes to inbox. All engines get it via resume cycle. Codex loses the "no restart" advantage temporarily — acceptable because resume is fast and the inbox mechanism is already battle-tested.

### Control File: `control.json`

**`internal/engine/loop.go`** — Read `control.json` in watchdog tick (line 604).

1. After `enqueueInboxMessages()`, read `control.json` from artifact dir.
2. If `extend_kill_seconds` is set, override `effectiveKill`.
3. If `abort` is set, trigger graceful stop.

---

## ax-eval Integration

### Existing Cases That Implicitly Test This

- `freeze-watchdog` and `freeze-stdin-nudge` — verify frozen_warning/frozen_killed events. These read from events.jsonl, so silent stderr doesn't break them.
- All 12 cases use `--stdin` mode which doesn't set `--stream` — they'll run in silent mode after Wave 1. Events.jsonl is unaffected, so all evaluators work unchanged.

### New Cases Needed

1. **`silent-default`** (Wave 2) — verify stderr filtering.
2. **`stream-flag`** (Wave 2) — verify `--stream` restores full stderr.
3. **`steer-nudge`** (post-Tier 1 steering) — dispatch a long task, write to inbox mid-flight, verify `coordinator_inject` event appears in events.jsonl and the worker resumes.

---

## Risk Assessment

**Risk 1: Breaking callers that parse stderr.** Coordinators (R. Jenkins) currently parse stderr for heartbeats to detect liveness. After Tier 1, stderr goes silent by default. **Mitigation:** Coordinators should use `--stream` flag explicitly, or switch to reading `events.jsonl`. The spec says `--stream` restores current behavior — zero breakage for callers that opt in.

**Risk 2: Emitter thread safety.** `Emit()` already holds `e.mu` for both writes. Adding a `streamMode` field read doesn't need the lock (it's set once before dispatch starts and never changes). Safe.

**Risk 3: ax-eval test flakiness.** New streaming tests depend on stderr content, which includes timestamps and dispatch IDs. Use `strings.Contains` on event type strings, not exact line matching.

**Risk 4: Inbox-only steering latency.** Resume cycle takes 1-3s (stop process + restart harness). For "wrap up" nudges this is acceptable. For latency-critical steering, direct stdin (Codex only, Tier 2) is the answer.

**Rollback:** Revert the `shouldEmitToStderr` filter. All events flow to stderr again. The `--stream` flag becomes a no-op. Clean single-commit revert.
