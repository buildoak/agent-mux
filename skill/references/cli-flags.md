# CLI Flags and DispatchSpec Reference

Complete flag table, command surface, `--stdin` JSON fields, and precedence rules.

---

## Dispatch Flags

### Standard dispatch mode

These flags apply to `agent-mux [flags] <prompt>`, `agent-mux dispatch ...`,
and `agent-mux preview ...`.

| Flag | Short | Type | Default | Notes |
|------|-------|------|---------|-------|
| `--engine` | `-E` | string | from config | `codex`, `claude`, `gemini` |
| `--profile` | `-P` | string | unset | Profile prompt file from `prompts/<name>.md` |
| `--cwd` | `-C` | string | current dir | Working directory for the harness |
| `--model` | `-m` | string | from profile/config | Model override |
| `--effort` | `-e` | string | from profile/config | `low`, `medium`, `high`, `xhigh` |
| `--timeout` | `-t` | int | effort-mapped | Timeout in seconds |
| `--system-prompt` | `-s` | string | unset | Extra system prompt text |
| `--system-prompt-file` | | string | unset | Read system prompt text from file |
| `--prompt-file` | | string | unset | Read prompt from file instead of positional arg |
| `--context-file` | | string | unset | Sets `AGENT_MUX_CONTEXT` and adds the read preamble |
| `--skill` | | string[] | `[]` | Repeatable skill names |
| `--skip-skills` | | bool | `false` | Skip skill injection while keeping profile resolution |
| `--artifact-dir` | | string | auto | Override artifact directory |
| `--recover` | | string | unset | Continue from a prior dispatch ID |
| `--signal` | | string | unset | Dispatch ID to send a message to; message is the first positional arg |
| `--stream` | `-S` | bool | `false` | Stream full NDJSON events to stderr |
| `--async` | | bool | `false` | Return ack immediately, continue in background |
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
| `--reasoning` | `-r` | string | `medium` | Codex | Passed as Codex reasoning effort |
| `--permission-mode` | | string | from config | Codex, Claude, Gemini | Codex: takes precedence over sandbox. Claude: passed through. Gemini: maps to approval mode. |
| `--max-turns` | | int | `0` | Claude | Maximum conversation turns |
| `--add-dir` | | string[] | `[]` | All engines | Repeatable additional writable/include directories |

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
| `agent-mux steer <id> <action> [args]` | mid-flight control |
| `agent-mux config [subcommand] [flags]` | config introspection |

### Config subcommands

| Invocation | Purpose |
|------------|---------|
| `agent-mux config` | resolved config JSON with `_sources` |
| `agent-mux config --sources` | JSON list of loaded config files |
| `agent-mux config prompts [--json]` | profile catalog |
| `agent-mux config models [--json]` | configured model lists |
| `agent-mux config skills [--json]` | discovered skills and winning paths |

### Lifecycle flags

| Subcommand | Flag | Type | Default | Notes |
|------------|------|------|---------|-------|
| `list` | `--limit` | int | 20 | `0` means all |
| `list` | `--status` | string | unset | `completed`, `failed`, `timed_out` |
| `list` | `--engine` | string | unset | `codex`, `claude`, `gemini` |
| `list` | `--json` | bool | `false` | NDJSON output |
| `status` | `--json` | bool | `false` | JSON output |
| `result` | `--json` | bool | `false` | Compact lifecycle JSON |
| `result` | `--artifacts` | bool | `false` | List non-internal artifact files |
| `result` | `--no-wait` | bool | `false` | Error if still running |
| `inspect` | `--json` | bool | `false` | Combined JSON payload |
| `wait` | `--poll` | string | config or `60s` | Go duration string |
| `wait` | `--json` | bool | `false` | Compact JSON; orphaned dispatches emit raw `LiveStatus` instead |
| `wait` | `--cwd` | string | unset | Project root for config discovery |
| `config` | `--cwd` | string | unset | Project root for config discovery |

### Steer actions

| Action | Args | Notes |
|--------|------|-------|
| `abort` | none | SIGTERM if `host.pid` is alive, else `control.json` |
| `nudge` | `[message]` | Default wrap-up message if omitted |
| `redirect` | `"<instructions>"` | Required |
| `extend` | `<seconds>` | Positive integer |

---

## DispatchSpec JSON Fields

Pipe one JSON object to `agent-mux --stdin`. `prompt` is required.

### Core fields

| JSON key | Type | Required | Default | Notes |
|----------|------|----------|---------|-------|
| `prompt` | string | yes | - | Task prompt |
| `cwd` | string | no | shell cwd | Working directory |
| `engine` | string | no | from profile/config | `codex`, `claude`, `gemini` |
| `model` | string | no | from profile/config | Model override |
| `effort` | string | no | from profile/config | `low`, `medium`, `high`, `xhigh` |
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
| `timeout_sec` | int | effort-mapped | Must be `> 0` when present |
| `grace_sec` | int | `60` | Must be `> 0` when present |
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
| `silence_warn_seconds` | int | Override frozen warning threshold (default 90s) |
| `silence_kill_seconds` | int | Override frozen kill threshold (default 180s) |
| `long_command_silence_seconds` | int | Extended kill threshold while a known long-running command is active (default 540s) |
| `max_steer_wait_seconds` | int | Maximum seconds to wait for a tool to finish before force-delivering a steer message (default 120s) |
| `long_command_prefixes` | string | Comma-separated extra command prefixes that qualify as long-running (extends built-in list) |

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
| `<artifact_dir>/control.json` | abort and extend requests |
| `<artifact_dir>/inbox.md` | NDJSON coordinator inbox |
| `<artifact_dir>/stdin.pipe` | Unix FIFO for soft Codex steering |
| `<artifact_dir>/*` | worker-created artifact files |

Default artifact root comes from the secure runtime root chosen by agent-mux.
The durable store is always `~/.agent-mux/dispatches/<id>/`.

---

## Precedence

### Profile search order

```
<cwd>/.agent-mux/prompts/<name>.md   (project-level)
  > ~/.agent-mux/prompts/<name>.md   (global)
```

### Dispatch fields in standard CLI mode

For `engine`, `model`, and `effort`:

```
explicit CLI flags
  > profile
  > hardcoded defaults
```

For `timeout`:

```
explicit CLI --timeout
  > profile.timeout
  > timeout table for the chosen effort
```

### Dispatch fields in --stdin mode

For `engine`, `model`, and `effort`:

```
explicit JSON fields
  > profile
  > hardcoded defaults
```

For `timeout`:

```
explicit JSON timeout_sec
  > profile.timeout
  > timeout table for the chosen effort
```

### Poll interval

```
wait --poll
  > [async].poll_interval
  > 60s
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Error |
| `2` | Usage or parse error |
| `130` | Cancelled at TTY confirmation |
