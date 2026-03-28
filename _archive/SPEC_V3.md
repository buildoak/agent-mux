# agent-mux v3 Spec — Post-Dispatch Lifecycle & Hardening

**Date:** 2026-03-28
**Status:** Draft
**Context:** Findings from a 10+ dispatch coordination session, Codex xhigh audit, and TS/Go harness research.

---

## 1. Output Contract Fix

### Problem

The documented contract says "single JSON object on stdout." The code at `main.go:476` correctly calls `writeResult(stdout, result)`. Yet in observed behavior across 10+ dispatches, stdout was consistently empty and the result JSON appeared on stderr.

**Investigation needed:** The code path looks correct. Possible causes:
- Early error exits that write to stderr and skip the writeResult call (auditor found "many early failures print plain text to stderr with no JSON")
- The `--yes` flag interaction — preview is written to stderr unconditionally when `--yes` is false
- Interaction between `--stdin` mode and the confirmation flow
- Error paths in pipeline/signal/recover modes that bypass writeResult

### Fix

1. **Audit every exit path** in `main.go` — ensure EVERY terminal state (success, error, timeout, config error, hook deny) writes a valid JSON result to stdout. Zero plain-text stderr exits.
2. **Centralize result emission** behind one function: `emitResult(stdout, result)`. All paths call it. No direct `fmt.Fprintf(stderr, ...)` for terminal states.
3. **`--yes` should be the default for `--stdin` mode.** When input comes from stdin (programmatic), skip the interactive preview. Preview is for TTY use.
4. **Test:** `printf '{"engine":"codex","prompt":"echo hello","cwd":"/tmp"}' | agent-mux --stdin | jq .status` must produce `"completed"` or `"failed"` — never empty.

### Reference

v1 got this right: `console.log(JSON.stringify(result))` — one line, always stdout, every path. The Go version should match this simplicity.

---

## 2. Post-Dispatch Lifecycle Commands

### Problem

After a dispatch completes, the caller is alone with raw JSON files in `/tmp/agent-mux/<dispatch_id>/`. No built-in way to check status, extract the response, or list past dispatches. This session required custom Python scripts for every result inspection.

### Existing Infrastructure (no new storage needed)

| Source | What's there | Records |
|--------|-------------|---------|
| `/tmp/agent-mux/<id>/_dispatch_meta.json` | Full lifecycle: id, salt, trace_token, status, engine, model, timestamps, cwd, artifacts | 259 dispatch dirs |
| `/tmp/agent-mux/control/<id>.json` | Lightweight pointer: id, artifact_dir, salt, trace_token | 260 records |
| `/tmp/agent-mux/<id>/full_output.md` | Full response when truncated | Written on truncation |
| `/tmp/agent-mux/<id>/events.jsonl` | Full NDJSON event stream | Every dispatch |
| Gaal `find-salt` | Maps dispatch_salt → harness session → full transcript | Confirmed working |

### Proposed Commands

**`agent-mux list`**
Scan `/tmp/agent-mux/*/_dispatch_meta.json`. Output table:

```
ID              SALT            STATUS     ENGINE  MODEL       DURATION  CWD
01KMT0WYB55F    quick-newt-zero completed  codex   gpt-5.4     824s     /Users/.../agent-mux
01KMT253Q3NZ    fair-ant-nine   completed  codex   gpt-5.4     217s     /Users/.../agent-mux
```

Flags: `--status=completed|failed|timed_out`, `--engine=codex|claude|gemini`, `--limit=N`, `--json`

**`agent-mux status <dispatch_id>`**
Read `_dispatch_meta.json`. Output:

```
Status:    completed
Engine:    codex / gpt-5.4
Duration:  824s
Started:   2026-03-28T13:45:00Z
Truncated: true
Artifacts: 3 files
Salt:      quick-newt-zero
```

Accept short IDs (prefix match against control records).

**`agent-mux result <dispatch_id>`**
Extract and print the response text:
- If `full_output.md` exists (truncated): print that
- Otherwise: read the result JSON from the dispatch, print `.response`
- `--json` flag: print the full result JSON instead
- `--artifacts` flag: list artifact files

**`agent-mux inspect <dispatch_id>`**
Bridge to gaal: read `dispatch_salt` from meta → `gaal find-salt <salt>` → `gaal inspect <session_id>`. Surfaces: tokens, peak context, tool counts, file attribution, full transcript. Falls back to local `events.jsonl` if gaal isn't available.

### Design Principles

- **No new storage.** Everything reads from `/tmp/agent-mux/` which already exists.
- **Short IDs.** Accept prefix matches (first 8+ chars of dispatch_id). Control records enable fast lookup.
- **Gaal is the deep inspector.** agent-mux owns the dispatch lifecycle; gaal owns the session transcript. The salt is the bridge. Don't duplicate gaal's indexing.
- **`-o=json` everywhere.** All lifecycle commands support `--json` for programmatic consumption.

---

## 3. Response Truncation Rethink

### Problem

`response_max_chars` defaults to 2000. Auditor and research dispatches get silently truncated. The caller sees `response_truncated: true` but must manually find and read `full_output.md`.

### Fix

1. **Raise default to 16000.** LLM context windows are 128K-1M tokens. 2000 chars is absurdly conservative.
2. **`response_max_chars: 0` disables truncation entirely.** For callers who want the full response inline.
3. **Include `full_output_path` in result JSON when truncated.** Currently the caller must construct the path from dispatch_id. The result should tell them where to find it.
4. **Emit a `response_truncated` event** on the stderr event stream when truncation occurs, with the full_output path.
5. **`agent-mux result <id>` always returns the full response** — it transparently reads full_output.md when the dispatch was truncated.

---

## 4. Security Hardening (Path Traversal & Input Sanitization)

### Source

Codex xhigh audit found 5 HIGH and 8 MEDIUM issues. A 792-line implementation plan exists at `/tmp/agent-mux-docs/security-fix-plan.md`.

### Summary of Fixes

**New package: `internal/sanitize/`**

```go
func ValidateDispatchID(id string) error    // reject ../, separators, empty, >128 bytes
func ValidateBasename(name string) error     // for skill/profile names
func SafeJoinPath(root, child string) error  // verify result stays under root
func SecureArtifactRoot() string             // per-user runtime dir, 0700
```

**Issue-by-issue:**

| # | Issue | Fix |
|---|-------|-----|
| 1 | dispatch_id path traversal | `ValidateDispatchID` at stdin parse + CLI flag parse |
| 2 | Shared /tmp with predictable paths | `SecureArtifactRoot()` → `$XDG_RUNTIME_DIR/agent-mux/` or `/tmp/agent-mux-$UID/` with 0700 |
| 3 | Hook bypass via system_prompt | Run hooks on combined `Prompt + SystemPrompt`, constrain `system_prompt_file` to config root |
| 4 | Skill/profile path traversal | `ValidateBasename` before path join |
| 5 | Config merge SourceDir ambiguity | Resolve `system_prompt_file` to absolute path during config load, not at dispatch time |

### Behavioral Changes

- Existing dispatch_ids with `/` or `..` will be rejected (breaking but necessary)
- Artifact root moves from `/tmp/agent-mux/` to per-user dir (existing dispatches in old location remain readable via fallback)
- Hook deny now checks system prompt content (previously only user prompt)

---

## 5. Additional Audit Findings

### Contract Issues

| # | Issue | Severity | Fix |
|---|-------|----------|-----|
| 8 | Pipeline setup errors emit DispatchResult, success emits PipelineResult, no schema_version on PipelineResult | MEDIUM | Unified versioned pipeline envelope |
| 9 | Plain-text stderr exits violate JSON contract | MEDIUM | Centralized result emitter (covered in §1) |
| 10 | Inbox delimiter poisoning (`\n---\n` in signals splits messages) | MEDIUM | Switch to NDJSON or length-prefixed framing |
| 12 | Negative timeout disables enforcement | MEDIUM | Reject non-positive timeout/grace at parse time |

### Gemini Adapter

| # | Issue | Severity | Fix |
|---|-------|----------|-----|
| 11 | Malformed NDJSON + exit 0 → reports "completed" | MEDIUM | Track parse errors as terminal failures |
| 13 | Silent system prompt drop on write failure | MEDIUM | Surface error, fail dispatch setup |

### Correctness

| # | Issue | Severity | Fix |
|---|-------|----------|-----|
| 5 | Orphan processes on SIGKILL (no parent-death reaper) | HIGH | Platform-specific reaper (Linux: PR_SET_PDEATHSIG, macOS: kqueue EVFILT_PROC) |
| 14 | Non-atomic `_dispatch_meta.json` writes | MEDIUM | Write via temp file + rename |
| 15 | Dead parameters (`workerIdx`, `stepIdx`) | LOW | Remove |

### Docs Drift

| # | Claim | Reality |
|---|-------|---------|
| README:43 | "stdout is always JSON" | CLI has plain-text failure exits |
| README:62 | Event names `artifact_written`, `soft_timeout` | Code emits `file_write`, `timeout_warning` |
| README:47 | Default models listed | Code validates against different hardcoded sets |
| output-contract.md:13 | PipelineResult has `schema_version` | It doesn't |
| config-guide.md:248 | Skill scripts/ prepended to PATH | Only added to Codex add-dir |

### Test Gaps

- No tests for path traversal/escape in dispatch_id, skill name, profile name, system_prompt_file
- No tests for inbox delimiter poisoning, control-record symlink attacks, shared-/tmp permissions
- No tests for negative timeout, Gemini prompt write failure, "parse error but exit 0"
- CLI test suite not hermetic: `go test ./...` fails when `~/.agent-mux/config.toml` exists (passes with `HOME=$(mktemp -d)`)

---

## 6. Persistent Dispatch Store (`~/.agent-mux/data/`)

### Problem

Dispatch artifacts live in `/tmp/agent-mux/` — ephemeral (cleared on reboot), shared (security risk), invisible to the user. After reboot, all dispatch history is gone.

### Design

Move the durable dispatch record to `~/.agent-mux/data/`. `/tmp` stays as the live workspace during dispatch (events, inbox, scratch) but the permanent record lives in the user's home directory.

```
~/.agent-mux/
  config.toml            # how to dispatch (existing)
  config.local.toml      # machine-local overrides, git-ignored (NEW, see §7)
  prompts/               # what to inject (existing)
  data/                  # what happened (NEW)
    dispatches.jsonl     # append-only index, one JSON line per dispatch
    results/
      <dispatch_id>.md   # response text for completed dispatches
```

**`dispatches.jsonl`** — one line per dispatch, ~200 bytes:
```json
{"id":"01KMT0WY","salt":"quick-newt-zero","status":"completed","engine":"codex","model":"gpt-5.4","role":"explorer","started":"2026-03-28T13:45:00Z","duration_ms":824000,"cwd":"/repo","truncated":true,"response_chars":3817}
```

**`results/<id>.md`** — the actual response text. Written on dispatch completion. If the dispatch was truncated, stores the FULL text (not the truncated version). This is what `agent-mux result <id>` reads.

### Lifecycle

1. **Dispatch starts** → `/tmp/agent-mux/<id>/` created (live workspace: events, inbox, artifacts)
2. **Dispatch ends** → append record to `~/.agent-mux/data/dispatches.jsonl` + write `results/<id>.md`
3. **Reboot** → `/tmp` cleared, doesn't matter — durable record in `~/.agent-mux/data/`
4. **Cleanup** → `agent-mux gc --older-than 30d` removes old results (future)

### Command mapping

| Command | Reads from |
|---------|------------|
| `agent-mux list` | `dispatches.jsonl` — tail N lines, filter by status/engine/role |
| `agent-mux status <id>` | `dispatches.jsonl` — grep for id prefix |
| `agent-mux result <id>` | `results/<id>.md` — print the response text |
| `agent-mux inspect <id>` | Extract salt from record → `gaal find-salt` → full session |

### Properties

- **Append-only.** Concurrent dispatches just append (file lock on write).
- **Unix-friendly.** `tail`, `grep`, `jq` all work on the JSONL.
- **No SQLite.** No binary dependency for a text file.
- **Permissions.** `~/.agent-mux/data/` is `0700`, files are `0600`. Per-user, private.
- **Survivable.** If `dispatches.jsonl` is lost, dispatches still work — it's a convenience index, not a dependency.

---

## 7. Config Layer Model (aligned with Claude Code)

### Current State

agent-mux already has two-layer config:
```
~/.agent-mux/config.toml           # global defaults
<project>/.agent-mux/config.toml   # project overrides (defined-wins merge)
```

### Missing: Machine-Local Layer

Claude Code has `settings.local.json` at both global and project level — git-ignored, for machine-specific overrides. agent-mux needs the same.

### Proposed: Three-Layer Config

```
Resolution order (later wins on defined fields):

1. ~/.agent-mux/config.toml           # global defaults (committed to dotfiles)
2. ~/.agent-mux/config.local.toml     # machine-local global (git-ignored)
3. <cwd>/.agent-mux/config.toml       # project config (committed to project repo)
4. <cwd>/.agent-mux/config.local.toml # machine-local project (git-ignored)
5. --config=/explicit/path.toml        # explicit override (sole source, no merge)
```

### What goes where

| Content | Layer | Git? |
|---------|-------|------|
| Default engine/model preferences | Global config | Yes |
| Role definitions for a team | Project config | Yes |
| Custom binary paths (e.g., codex at non-standard location) | Local config | No |
| Personal model overrides (prefer opus over sonnet) | Local config | No |
| API key env var overrides | Local config | No |
| Sandbox paths specific to this machine | Local config | No |
| Pipeline definitions | Project config | Yes |
| Hooks | Project config | Yes (but currently WIP) |

### Merge Semantics (matching Claude Code patterns)

| Type | Behavior |
|------|----------|
| Scalar defaults (engine, model, effort) | Later wins |
| Role definitions | Project overrides global by role name |
| Role skills arrays | Replacement, not union (current behavior) |
| Pipeline definitions | Project overrides global by pipeline name |
| Model validation lists | Union (both contribute valid models) |
| Hooks deny/warn | Additive union (both apply) |

### Agents and Prompts directories

```
~/.agent-mux/
  agents/          # global coordinator/profile definitions
  prompts/         # global system prompt files

<project>/.agent-mux/
  agents/          # project agents (override global by name)
  prompts/         # project prompts (override global by filename)
```

Same pattern as Claude Code's `agents/` and `skills/`: both levels scanned, project wins on name collision.

---

## 8. Implementation Priority

### Phase 1: Foundation (unblocks everything else)
1. Output contract fix (§1) — centralized result emitter, all paths write JSON to stdout
2. `--yes` default for `--stdin` mode
3. Test suite hermetic fix (`HOME` isolation)
4. `config.local.toml` support (§7) — machine-local git-ignored layer

### Phase 2: Post-Dispatch Lifecycle (highest user-facing impact)
5. `~/.agent-mux/data/` persistent store (§6) — dispatches.jsonl + results/
6. `agent-mux list` command
7. `agent-mux status <id>` command
8. `agent-mux result <id>` command
9. Response truncation fixes (§3) — raise default, include full_output_path

### Phase 3: Security Hardening
10. `internal/sanitize/` package
11. Dispatch ID validation
12. Per-user artifact root (live workspace moves to `$XDG_RUNTIME_DIR` or `/tmp/agent-mux-$UID/`)
13. Hook + system_prompt fixes
14. Skill/profile basename validation

### Phase 4: Contract & Correctness
15. Pipeline output envelope unification
16. Inbox framing fix (NDJSON)
17. Timeout validation
18. Non-atomic write fixes
19. Gemini adapter fixes

### Phase 5: Polish
20. Orphan process reaper
21. Docs drift fixes (§5)
22. Dead parameter cleanup
23. `agent-mux inspect` (gaal bridge)
24. `agent-mux gc` — cleanup old data/results
