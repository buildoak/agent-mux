# agent-mux skill audit

Date: 2026-04-04

Scope audited:

- Skill docs under `/Users/otonashi/thinking/pratchett-os/coordinator/.claude/skills/agent-mux/`
- Code in:
  - `cmd/agent-mux/{main,async,lifecycle,config_cmd,steer}.go`
  - `internal/dispatch/{dispatch,persistence,recovery,status}.go`
  - `internal/engine/loop.go`
  - `internal/types/types.go`
  - `internal/config/{config,coordinator,skills}.go`
  - `internal/steer/inbox.go`
  - `internal/engine/adapter/{codex,claude,gemini}.go`

## 1. Documented in the skill, but not true in code

### 1.1 `--async` does not daemonize or truly “continue in the background” by itself

Docs:

- `SKILL.md`
- `references/async-and-steering.md`

Code reality:

- `cmd/agent-mux/async.go:16-19` says the dispatch still runs synchronously in the current process and the caller is expected to background it.
- `cmd/agent-mux/async.go:82-107` runs `dispatchSpec(...)` inline after printing the ack.

Impact:

- The docs overstate what `--async` does. It is early-ack plus detached stdio, not daemonization.

### 1.2 `_dispatch_ref.json` is not guaranteed to exist before the async ack

Docs:

- `SKILL.md`
- `references/async-and-steering.md`
- `references/output-contract.md`

Code reality:

- Before ack, `runAsyncDispatch` writes persistent meta, `host.pid`, and `status.json`; `cmd/agent-mux/async.go:28-64`.
- `_dispatch_ref.json` is written later in the engine dispatch path; `internal/engine/loop.go:139-146`, `internal/dispatch/dispatch.go:31-43`.

Impact:

- Consumers cannot rely on `_dispatch_ref.json` being present immediately after `async_started`.

### 1.3 `status.json.state = "initializing"` is documented, but current code does not emit it

Docs:

- `references/async-and-steering.md`

Code reality:

- `LiveStatus.State` is just a string field; `internal/dispatch/status.go:14-26`.
- The async launcher writes `state: "running"` and `last_activity: "initializing"`; `cmd/agent-mux/async.go:45-54`.
- The engine loop writes `running`, `completed`, `failed`, or `timed_out`; `internal/engine/loop.go:850-851`, `internal/engine/loop.go:973-1029`.

Impact:

- `initializing` is a `last_activity` value, not a real `state`.

### 1.4 The docs reference a nonexistent `gaal inspect <session_id>` workflow

Docs:

- `references/async-and-steering.md`

Code reality:

- No `gaal` command or integration exists in this repo.
- Session IDs are persisted in `status.json`, `meta.json`, and results; `internal/dispatch/status.go:23-25`, `internal/dispatch/persistence.go:44-72`, `internal/types/types.go:49-58`.

Impact:

- This is a stale external reference and should be removed.

## 2. In code, but missing from the skill docs

### 2.1 Additional undocumented `engine_opts` keys affect watchdog and steering behavior

Code reality:

- `internal/engine/loop.go:246-249` reads:
  - `long_command_silence_seconds`
  - `max_steer_wait_seconds`
- `internal/engine/loop.go:1222-1240` reads:
  - `long_command_prefixes`

Docs gap:

- `references/cli-flags.md` documents `heartbeat_interval_sec`, `silence_warn_seconds`, and `silence_kill_seconds`, but not these three additional keys.

### 2.2 `result --json` can include `kill_reason`

Code reality:

- `cmd/agent-mux/lifecycle.go:754-758` documents the enrichment.
- `cmd/agent-mux/lifecycle.go:759-805` adds `kill_reason` when the stored status is `failed` and a kill-related event is found.

Docs gap:

- `references/output-contract.md` and `references/async-and-steering.md` do not mention `kill_reason`.

### 2.3 `--config` accepts a directory, not just a file path

Code reality:

- `internal/config/config.go:336-339` says an explicit config path may be:
  - a `.toml` file
  - a directory containing `.agent-mux/config.toml`
  - a directory containing `config.toml`

Docs gap:

- The docs describe `--config` as an explicit source, but never describe the directory resolution behavior.

### 2.4 Artifact resolution has compatibility fallbacks the docs do not mention

Code reality:

- `internal/dispatch/recovery.go:45-77` resolves artifact dirs from:
  - persistent meta's `artifact_dir`
  - the current secure artifact root
  - legacy `/tmp/agent-mux/<id>`

Docs gap:

- The skill docs present only the current secure runtime root and do not mention the legacy `/tmp/agent-mux` fallback.

### 2.5 `result` has a legacy fallback to `full_output.md`

Code reality:

- `cmd/agent-mux/lifecycle.go:231-259` falls back to `full_output.md` if no persisted result exists.
- `cmd/agent-mux/lifecycle.go:394-400` computes the legacy path.

Docs gap:

- The docs say `full_output_path` is a deprecated stub, but do not mention that `result` still contains legacy fallback behavior.

### 2.6 `wait` can return live `orphaned` JSON rather than the normal result shape

Code reality:

- `cmd/agent-mux/lifecycle.go:739-744` writes raw `liveStatus` and exits nonzero when the dispatch is orphaned.

Docs gap:

- `references/async-and-steering.md` says `wait --json` prints the same compact JSON shape as `result --json`, which is not true in the orphaned case.

## 3. Incorrect descriptions, defaults, or examples

### 3.1 The auto-injected artifact-dir preamble text is outdated everywhere it appears

Docs:

- `SKILL.md`
- `references/prompting-guide.md`
- `references/output-contract.md`

Documented text:

- `Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR.`

Code reality:

- `internal/dispatch/dispatch.go:145-156` injects:
  - `If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.`

Impact:

- Multiple examples and the documented prompt preamble do not match current runtime behavior.

### 3.2 The recovery workflow description is too specific and does not match the actual lookup order

Docs:

- `references/recovery-guide.md`

Documented flow:

1. resolve the dispatch ID from the durable store
2. read `meta.json` to recover `artifact_dir`
3. resolve artifact-backed metadata via `_dispatch_ref.json`

Code reality:

- `internal/dispatch/recovery.go:91-129` first resolves the artifact dir via `ResolveArtifactDir(...)`.
- `internal/dispatch/recovery.go:45-77` uses persistent meta first, then current secure root, then legacy root.
- `internal/dispatch/recovery.go:108-123` reads dispatch meta from the artifact dir, but falls back to persistent meta if that fails.

Impact:

- The actual recovery path is more flexible than documented and does not depend on `_dispatch_ref.json`.

### 3.3 `wait --json` is not always “the same compact lifecycle JSON shape as result --json”

Docs:

- `references/async-and-steering.md`
- `references/cli-flags.md`

Code reality:

- Normal completion does go through `showResult(...)`; `cmd/agent-mux/lifecycle.go:682-720`.
- But orphaned runs return raw `LiveStatus`; `cmd/agent-mux/lifecycle.go:739-744`.

Impact:

- The documented guarantee is too strong.

### 3.4 The documented async ack preconditions are incomplete/wrong

Docs:

- `references/async-and-steering.md`
- `references/output-contract.md`

Documented:

- by ack time, `host.pid`, `status.json`, `_dispatch_ref.json`, and durable `meta.json` all exist

Code reality:

- Guaranteed before ack:
  - persistent meta via `RegisterDispatchSpec`; `cmd/agent-mux/async.go:28-33`
  - `host.pid`; `cmd/agent-mux/async.go:35-43`
  - `status.json`; `cmd/agent-mux/async.go:45-54`
- Not guaranteed before ack:
  - `_dispatch_ref.json`; written later in `internal/engine/loop.go:139-146`

### 3.5 The skill docs omit that inbox steering for Claude/Gemini is implemented through resume/restart, not just passive inbox delivery

Docs:

- `SKILL.md`
- `references/async-and-steering.md`

Code reality:

- Codex can receive soft steering via FIFO; `cmd/agent-mux/steer.go:195-262`.
- Claude and Gemini adapters both support resume; `internal/engine/adapter/claude.go:136-143`, `internal/engine/adapter/gemini.go:200-207`.
- The loop restarts the harness with `ResumeArgs(...)` when inbox messages are pending; `internal/engine/loop.go:556-649`.

Impact:

- The docs miss an important behavioral detail: steering is not just “write to inbox and wait”; for resume-capable adapters it actively resumes the session.

### 3.6 `status.json` session visibility is overstated as a near-immediate guarantee

Docs:

- `references/async-and-steering.md`

Code reality:

- Session ID is persisted early when the engine emits a session-start event; `internal/engine/loop.go:289-294`, `internal/engine/loop.go:384-386`.
- But `status.json` is only rewritten on the watchdog cadence, every 5 seconds; `internal/engine/loop.go:731`, `internal/engine/loop.go:802-851`.

Impact:

- `meta.json` updates early; `status.json` is periodic, so the doc language is too strong for a contract.

## Recommended doc changes

1. Replace all artifact-dir preamble text with the exact current string:
   - `If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.`
2. Rewrite the `--async` section to say:
   - it emits an early ack
   - detaches stdio
   - still runs in the current process unless the caller backgrounds it
3. Remove the claim that `_dispatch_ref.json` exists before the async ack.
4. Remove `initializing` from documented `status.json.state` values.
5. Remove the `gaal inspect <session_id>` reference.
6. Document these additional `engine_opts`:
   - `long_command_silence_seconds`
   - `max_steer_wait_seconds`
   - `long_command_prefixes`
7. Note that `wait --json` can emit live `orphaned` status JSON instead of the normal `result --json` shape.
8. Update the recovery guide to reflect the real artifact-dir and meta fallback order.
