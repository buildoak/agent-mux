# Design: Config Simplification

**Date:** 2026-04-04  
**Status:** Analysis

## Conclusion

Yes: `config.toml` can die.

Prompt/profile markdown files already carry worker identity in `internal/config/coordinator.go`:

- `engine`
- `model`
- `effort`
- `skills`
- `timeout`
- body as system prompt

That overlaps almost exactly with legacy `roles.*`. The rest of `config.toml` is operational policy and is better expressed as CLI/env overrides or directory conventions.

## Current `config.toml` surface

From `internal/config/config.go`, the live schema is:

- `[defaults]`: `engine`, `model`, `effort`, `sandbox`, `permission_mode`, `max_depth`
- `[skills]`: `search_paths`
- `[models]`: per-engine model lists
- `[roles.<name>]`: `engine`, `model`, `effort`, `timeout`, `skills`, `system_prompt_file`
- `[liveness]`: `heartbeat_interval_sec`, `silence_warn_seconds`, `silence_kill_seconds`
- `[timeout]`: `low`, `medium`, `high`, `xhigh`, `grace`
- `[hooks]`: `pre_dispatch`, `on_event`, `event_deny_action`
- `[async]`: `poll_interval`

Observed in the real `~/.agent-mux/config.toml` but already dead:

- `[defaults].response_max_chars`

It is ignored by decode because the field no longer exists in `Config`.

## Classification

### `[roles.<name>]`

| Field | Classification | Reason |
|---|---|---|
| `engine` | MOVES TO PROMPT FILES | Already supported in frontmatter. |
| `model` | MOVES TO PROMPT FILES | Already supported in frontmatter. |
| `effort` | MOVES TO PROMPT FILES | Already supported in frontmatter. |
| `timeout` | MOVES TO PROMPT FILES | Already supported in frontmatter. |
| `skills` | MOVES TO PROMPT FILES | Already supported in frontmatter. |
| `system_prompt_file` | MOVES TO PROMPT FILES | Prompt body should hold the system prompt directly. |

Verdict: remove the entire `[roles]` section after migration.

### `[defaults]`

| Field | Classification | Reason |
|---|---|---|
| `engine` | DROPS ENTIRELY | Prompt files now carry worker identity; ad hoc calls can use CLI. |
| `model` | DROPS ENTIRELY | Same. |
| `effort` | DROPS ENTIRELY | Same. |
| `sandbox` | DROPS ENTIRELY | Not actually applied from config in `cmd/agent-mux/main.go`; CLI hardcodes sandbox default. |
| `permission_mode` | STAYS AS CONFIG | Operational runtime policy, not worker identity. Better as CLI/env than TOML. |
| `max_depth` | STAYS AS CONFIG | Operational recursion limit, not worker identity. Better as CLI/env or hardcoded default. |

### `[timeout]`

| Field | Classification | Reason |
|---|---|---|
| `low` | DROPS ENTIRELY | Only used for effort-to-timeout fallback; prompt files can carry explicit timeout. |
| `medium` | DROPS ENTIRELY | Same. |
| `high` | DROPS ENTIRELY | Same. |
| `xhigh` | DROPS ENTIRELY | Same. |
| `grace` | STAYS AS CONFIG | Runtime policy only; better as CLI/env or hardcoded default. |

Recommendation: hardcode the effort timeout table and keep per-worker timeout in prompt frontmatter.

### `[liveness]`

| Field | Classification | Reason |
|---|---|---|
| `heartbeat_interval_sec` | STAYS AS CONFIG | Runtime watchdog setting. |
| `silence_warn_seconds` | STAYS AS CONFIG | Runtime watchdog setting. |
| `silence_kill_seconds` | STAYS AS CONFIG | Runtime watchdog setting. |

These do not need `config.toml`. `internal/engine/loop.go` only reads them from `spec.EngineOpts`, so they can move cleanly to CLI/env.

The loop already supports these runtime knobs:

- `heartbeat_interval_sec`
- `silence_warn_seconds`
- `silence_kill_seconds`
- `long_command_silence_seconds`
- `max_steer_wait_seconds`
- `long_command_prefixes`

Only the first three come from config today. All six should use the same runtime mechanism.

### `[hooks]`

| Field | Classification | Reason |
|---|---|---|
| `pre_dispatch` | STAYS AS CONFIG | Local policy, not worker identity. |
| `on_event` | STAYS AS CONFIG | Local policy, not worker identity. |
| `event_deny_action` | DROPS ENTIRELY | Becomes unnecessary if hook exit codes already encode `deny` vs `warn`. |

`internal/hooks/hooks.go` is already script-first. Config only provides script path lists plus one downgrade toggle.

Best replacement: directory convention.

- project: `.agent-mux/hooks/pre-dispatch/*`, `.agent-mux/hooks/on-event/*`
- global: `~/.agent-mux/hooks/pre-dispatch/*`, `~/.agent-mux/hooks/on-event/*`

Run executable files in lexical order.

Hook contract:

- exit `0`: allow
- exit `1`: block/deny
- exit `2`: warn

With that contract, `event_deny_action` can disappear.

### `[skills]`

| Field | Classification | Reason |
|---|---|---|
| `search_paths` | STAYS AS CONFIG | Operational discovery path, not worker identity. |

This also does not require `config.toml`.

`internal/config/skills.go` already has ordered path discovery. Replace config search paths with conventions plus env:

1. `<cwd>/.agent-mux/skills`
2. `<cwd>/.claude/skills`
3. `~/.agent-mux/skills`
4. `~/.claude/skills`
5. `AGENT_MUX_SKILL_PATH`

### `[models]`

| Field | Classification | Reason |
|---|---|---|
| `<engine> = [...]` | STAYS AS CONFIG, but optional | Operational allowlist/validation only. |

This is not structurally required. `cmd/agent-mux/main.go` already has built-in fallback registries in `configuredModels(cfg)`.

Recommendation:

- built-in model registries by default
- optional env override if teams want local allowlists
- do not keep `config.toml` alive just for `[models]`

### `[async]`

| Field | Classification | Reason |
|---|---|---|
| `poll_interval` | DROPS ENTIRELY | `wait` already has `--poll` and already has a hardcoded default. |

### Dead observed field

| Field | Classification | Reason |
|---|---|---|
| `defaults.response_max_chars` | DROPS ENTIRELY | Removed feature, already ignored by code. |

## Minimal surviving surface

If the goal is the smallest coherent system, the surviving surface is:

1. Prompt files for worker identity
2. Hook directories for local policy
3. Skill directories plus optional env search path
4. CLI/env runtime knobs for watchdog behavior

That means there does not need to be any required config file at all.

## Recommended replacement

### 1. Prompt files replace roles

```md
---
engine: codex
model: gpt-5.4
effort: high
timeout: 900
skills:
  - think-protocol
  - pre-mortem
---
You are the architect worker...
```

This replaces:

- role engine/model/effort/timeout/skills
- role `system_prompt_file`

### 2. Hooks move to directory convention

Use:

- `<cwd>/.agent-mux/hooks/pre-dispatch`
- `~/.agent-mux/hooks/pre-dispatch`
- `<cwd>/.agent-mux/hooks/on-event`
- `~/.agent-mux/hooks/on-event`

This replaces:

- `[hooks].pre_dispatch`
- `[hooks].on_event`
- likely `[hooks].event_deny_action`

### 3. Skills move to directory convention plus env

Use:

- `<cwd>/.agent-mux/skills`
- `<cwd>/.claude/skills`
- `~/.agent-mux/skills`
- `~/.claude/skills`
- `AGENT_MUX_SKILL_PATH`

This replaces `[skills].search_paths`.

### 4. Runtime knobs move to CLI/env

Move these out of config:

- `heartbeat_interval_sec`
- `silence_warn_seconds`
- `silence_kill_seconds`
- `long_command_silence_seconds`
- `max_steer_wait_seconds`
- `long_command_prefixes`
- `poll_interval`
- `permission_mode`
- `max_depth`
- `grace`

Recommended precedence:

- CLI flag
- env var
- hardcoded default

Example env names:

- `AGENT_MUX_HEARTBEAT_INTERVAL_SEC`
- `AGENT_MUX_SILENCE_WARN_SECONDS`
- `AGENT_MUX_SILENCE_KILL_SECONDS`
- `AGENT_MUX_LONG_COMMAND_SILENCE_SECONDS`
- `AGENT_MUX_MAX_STEER_WAIT_SECONDS`
- `AGENT_MUX_LONG_COMMAND_PREFIXES`
- `AGENT_MUX_WAIT_POLL`
- `AGENT_MUX_PERMISSION_MODE`
- `AGENT_MUX_MAX_DEPTH`
- `AGENT_MUX_GRACE_SECONDS`

## Can it be a single file?

It can be zero files.

If you still want one optional runtime file for teams that dislike env vars, keep it runtime-only, not role-shaped:

```toml
[watchdog]
heartbeat_interval_sec = 15
silence_warn_seconds = 90
silence_kill_seconds = 180
long_command_silence_seconds = 540
max_steer_wait_seconds = 120
long_command_prefixes = ["npm install", "pnpm install"]

[runtime]
permission_mode = ""
max_depth = 2
grace_seconds = 60
wait_poll = "60s"
```

But the cleaner architecture is still:

- no config file
- prompt files for identity
- directories for hooks and skills
- CLI/env for runtime policy

## If `config.toml` must survive

The minimal defensible schema is runtime-only:

```toml
[watchdog]
heartbeat_interval_sec = 15
silence_warn_seconds = 90
silence_kill_seconds = 180
long_command_silence_seconds = 540
max_steer_wait_seconds = 120
long_command_prefixes = ["npm install", "pnpm install"]

[runtime]
permission_mode = ""
max_depth = 2
grace_seconds = 60
wait_poll = "60s"
```

Optional only if local model allowlists matter:

```toml
[models]
codex = ["gpt-5.4", "gpt-5.4-mini"]
claude = ["claude-opus-4-6"]
gemini = ["gemini-3.1-pro-preview"]
```

Everything else should be removed:

- no `[roles]`
- no `[defaults].engine/model/effort`
- no `[defaults].sandbox`
- no `[timeout]`
- no `[skills].search_paths`
- no `[hooks]` path lists

## Final recommendation

Kill `config.toml`.

Replace it with:

- prompt/profile markdown files for worker identity
- hook directory conventions
- skill directory conventions plus `AGENT_MUX_SKILL_PATH`
- CLI/env runtime controls for watchdog and async behavior
- built-in model registries

If a softer migration is needed, keep a tiny runtime-only file temporarily, but do not preserve role-era concepts under a new name.
