# CLI Flags and DispatchSpec Reference

Complete flag table, command surface, `--stdin` JSON fields, and precedence rules.

---

## Dispatch Flags

### Standard dispatch mode

These flags apply to `agent-mux [flags] <prompt>`, `agent-mux dispatch ...`,
and `agent-mux preview ...`.

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--engine` | `-E` | string | from profile | `agy`, `claude`, `codex`, `gemini` |
| `--profile` | `-P` | string | unset | Profile prompt file from `~/.agent-mux/prompts/<name>.md` |
| `--cwd` | `-C` | string | current dir | Working directory for the harness |
| `--model` | `-m` | string | from profile | Model override |
| `--effort` | `-e` | string | from profile | `low`, `medium`, `high`, `xhigh` |
| `--timeout` | `-t` | int | resolved later; hardcoded fallback 900s | Timeout in seconds |
| `--system-prompt` | `-s` | string | unset | Extra system prompt text |
| `--system-prompt-file` | | string | unset | Read system prompt text from file |
| `--prompt-file` | | string | unset | Read prompt from file instead of positional arg |
| `--context-file` | | string | unset | Sets `AGENT_MUX_CONTEXT` and adds the read preamble |
| `--skill` | | string[] | `[]` | Repeatable skill names |
| `--skip-skills` | | bool | `false` | Skip skill injection while keeping profile resolution |
| `--artifact-dir` | | string | auto | Override artifact directory |
| `--recover` | | string | unset | Continue from a prior dispatch ID |
| `--signal` | | string | unset | Dispatch ID to send a message to a resume-capable engine; message is the first positional arg |
| `--stream` | `-S` | bool | `false` | Stream full NDJSON events to stderr |
| `--async` | | bool | `false` | Emit ack early; process continues in current process, so use shell `&` for true backgrounding |
| `--full` | `-f` | bool | `true` | Codex full-access mode |
| `--no-full` | | bool | `false` | Disable Codex full-access mode |
| `--max-depth` | | int | `2` | Maximum recursive dispatch depth |
| `--stdin` | | bool | `false` | Read a DispatchSpec JSON object from stdin |
| `--yes` | | bool | `false` | Skip TTY confirmation |
| `--verbose` | `-v` | bool | `false` | Include raw harness lines on stderr |
| `--version` | | bool | `false` | Print version JSON |

### Engine-specific flags

| Flag | Short | Type | Default | Applies to | Notes |
|------|-------|------|---------|-----------|-------|
| `--sandbox` | | string | `danger-full-access` | Codex | `danger-full-access`, `workspace-write`, `read-only` |
| `--reasoning` | `-r` | string | empty unless set | Codex | Maps to `-c model_reasoning_effort=<value>` |
| `--permission-mode` | | string | from `AGENT_MUX_PERMISSION_MODE` when set; otherwise empty | Codex, Claude, Gemini | Codex: takes precedence over sandbox. Claude: passed through. Gemini: maps to approval mode and defaults to `yolo`. Explicit values are rejected for `agy`; env defaults are ignored by the agy adapter. |
| `--max-turns` | | int | `0` | Claude | Maximum conversation turns |
| `--add-dir` | | string[] | `[]` | All engines | Codex/Claude/agy forward repeated `--add-dir`; Gemini joins as `--include-directories` and also includes `$HOME,/tmp` |

### --stdin mode

When `--stdin` is enabled, dispatch content comes from JSON, not from CLI
dispatch flags.

Allowed CLI flags in `--stdin` mode:

| Flag | Short | Purpose |
|------|-------|---------|
| `--stdin` | | Enable stdin JSON mode |
| `--yes` | `-y` | Skip TTY confirmation |
| `--verbose` | `-v` | Raw harness lines on stderr |
| `--stream` | | Full event stream on stderr |
| `--async` | | Background dispatch |

Do not expect `--stdin` mode to merge in CLI `--profile`, `--model`, `--cwd`,
or similar dispatch flags. Put those fields in the JSON object.

---

## Commands

| Invocation | Purpose |
|------------|---------|
| `agent-mux [flags] <prompt>` | dispatch (default command) |
| `agent-mux dispatch [flags] <prompt>` | dispatch (explicit) |
| `agent-mux preview [flags] <prompt>` | resolve request without executing |
| `agent-mux help` | top-level help |
| `agent-mux list [flags]` | list recent dispatches |
| `agent-mux status <id> [--json]` | current or final status |
| `agent-mux result <id> [flags]` | stored response or artifact list |
| `agent-mux inspect <id> [--json]` | record + response + artifacts + meta |
| `agent-mux wait <id> [flags]` | block until `result.json` exists |
| `agent-mux steer <id> <action> [args]` | mid-flight control (both `steer <id> <action>` and `steer <action> <id>` work) |
| `agent-mux config [subcommand] [flags]` | config introspection |

### Config subcommands

| Invocation | Purpose |
|------------|---------|
| `agent-mux config` | resolved config summary (defaults, liveness, models, engine capabilities) |
| `agent-mux config engines [--json] [--refresh-models]` | engine capability matrix and active model allowlists; `--refresh-models` refreshes only the agy cache |
| `agent-mux config prompts [--json]` | profile catalog |
| `agent-mux config skills [--json]` | discovered skills and winning paths |

### Lifecycle flags

| Subcommand | Flag | Type | Default | Notes |
|------------|------|------|---------|-------|
| `list` | `--limit` | int | 20 | `0` means all |
| `list` | `--status` | string | unset | `completed`, `failed`, `timed_out` |
| `list` | `--engine` | string | unset | `agy`, `claude`, `codex`, `gemini` |
| `list` | `--json` | bool | `false` | NDJSON output |
| `status` | `--json` | bool | `false` | JSON output |
| `result` | `--json` | bool | `false` | Compact lifecycle JSON |
| `result` | `--artifacts` | bool | `false` | List non-internal artifact files |
| `result` | `--no-wait` | bool | `false` | Error if still running |
| `inspect` | `--json` | bool | `false` | Combined JSON payload |
| `wait` | `--poll` | string | `60s` hardcoded (`config.DefaultAsyncPollInterval`) | Go duration string; minimum effective interval 1s |
| `wait` | `--json` | bool | `false` | JSON output |
| `wait` | `--cwd` | string | unset | Project root for config discovery |
| `config` | `--cwd` | string | unset | Project root for config discovery |

### Steer actions

| Action | Args | Notes |
|--------|------|-------|
| `abort` | none | SIGTERM if `host.pid` is alive, else `control.json` |
| `nudge` | `[message]` | Default wrap-up message if omitted |
| `redirect` | `"<instructions>"` | Required |

`agy` supports `abort` through SIGTERM or `control.json`. `--signal`, `nudge`, and `redirect` use resume-backed inbox delivery: they are appended to `inbox.md`, then the loop restarts agy with `--conversation <session_id>` after an Antigravity conversation ID has been discovered from `agy.log`. This is not a live interrupt.

---

## DispatchSpec JSON Fields

Pipe one JSON object to `agent-mux --stdin`. `prompt` is required.

### Core fields

| JSON key | Type | Required | Default | Notes |
|----------|------|----------|---------|-------|
| `prompt` | string | yes | - | Task prompt |
| `cwd` | string | no | shell cwd | Working directory |
| `engine` | string | no | from profile | `agy`, `claude`, `codex`, `gemini` |
| `model` | string | no | from profile | Model override |
| `effort` | string | no | from profile | `low`, `medium`, `high`, `xhigh` |
| `system_prompt` | string | no | unset | Run-level system prompt |
| `context_file` | string | no | unset | Sets `AGENT_MUX_CONTEXT` |
| `profile` | string | no | unset | Profile/prompt file name |
| `coordinator` | string | no | unset | Alias for `profile`; conflicting values error |
| `skills` | string[] | no | `[]` | Extra skill names |
| `skip_skills` | bool | no | `false` | Disable skill injection |
| `recover` | string | no | unset | Prior dispatch ID to continue |

### Control fields

| JSON key | Type | Default | Notes |
|----------|------|---------|-------|
| `dispatch_id` | string | auto ULID | Must be a valid dispatch ID if supplied |
| `artifact_dir` | string | auto | Runtime artifact directory |
| `timeout_sec` | int | 900s hardcoded fallback after profile/JSON resolution | Must be `> 0` when present |
| `grace_sec` | int | `timeout_sec / 2`, minimum 1, when omitted | Must be `> 0` when present |
| `max_depth` | int | `2` or config default | Recursive dispatch limit |
| `depth` | int | `0` | Current recursion depth |
| `full_access` | bool | `true` | Codex full-access toggle |
| `engine_opts` | object | `{}` | Engine and liveness overrides |

### engine_opts keys

| Key | Type | Notes |
|-----|------|-------|
| `sandbox` | string | Codex sandbox value |
| `reasoning` | string | Codex reasoning effort |
| `permission-mode` | string | Permission/approval mode override |
| `max-turns` | int | Claude turn cap |
| `add-dir` | string[] | Extra writable/include directories |
| `heartbeat_interval_sec` | int | Override heartbeat cadence (default 15s) |

`agy` always starts with the local CLI `--sandbox` flag and does not expose a dangerous sandbox-skip flag through agent-mux. `agy` provider diagnostics are written to private runtime logs, not public result artifacts.

---

## Persistence and Runtime Paths

| Path | Contents |
|------|----------|
| `~/.agent-mux/dispatches/<dispatch_id>/meta.json` | durable dispatch metadata |
| `~/.agent-mux/dispatches/<dispatch_id>/result.json` | durable dispatch result |
| `<artifact_dir>/_dispatch_ref.json` | thin pointer to the durable store |
| `<artifact_dir>/status.json` | live status |
| `<artifact_dir>/events.jsonl` | full NDJSON event log |
| `<artifact_dir>/host.pid` | async host PID |
| `<artifact_dir>/control.json` | abort requests |
| `<artifact_dir>/inbox.md` | NDJSON coordinator inbox |
| `<artifact_dir>/stdin.pipe` | Unix FIFO only when a soft-stdin bridge is active; current Codex and agy runs skip it |
| `<artifact_dir>/*` | worker-created artifact files |

Default artifact root comes from the secure runtime root chosen by agent-mux.
The durable store is always `~/.agent-mux/dispatches/<id>/`.

---

## Precedence

### Profile search order

```
~/.agent-mux/prompts/<name>.md   (single global directory)
```

### Dispatch fields in standard CLI mode

For `engine`, `model`, and `effort`:

```
explicit CLI flags
  > profile frontmatter
  > hardcoded defaults
```

For `timeout`:

```
explicit CLI --timeout
  > profile frontmatter timeout
  > hardcoded default (900s)
```

### Dispatch fields in --stdin mode

For `engine`, `model`, and `effort`:

```
explicit JSON fields
  > profile frontmatter
  > hardcoded defaults
```

For `timeout`:

```
explicit JSON timeout_sec
  > profile frontmatter timeout
  > hardcoded default (900s)
```

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | runtime/config/lifecycle/signal/recovery failure |
| `2` | Usage or parse error |
| `130` | Cancelled at TTY confirmation |
