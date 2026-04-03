# CLI Reference

Complete flag and invocation reference for agent-mux. This is the canonical table of every CLI flag, every mode detection rule, and every subcommand.

For operational usage patterns, see the other docs. This page is the lookup table.

## Complete Flag Table

### Common Flags (All Engines)

| Flag | Short | Type | Default | Notes |
| --- | --- | --- | --- | --- |
| `--engine` | `-E` | string | from config | `codex`, `claude`, `gemini` |
| `--role` | `-R` | string | — | Role name from config.toml |
| `--variant` | | string | — | Variant within a role (requires `--role`) |
| `--model` | `-m` | string | from role/config | Model override |
| `--effort` | `-e` | string | `high` | `low`, `medium`, `high`, `xhigh` |
| `--timeout` | `-t` | int | effort-mapped | Timeout in seconds |
| `--cwd` | `-C` | string | current dir | Working directory for the harness |
| `--system-prompt` | `-s` | string | — | Inline system prompt |
| `--system-prompt-file` | | string | — | System prompt from file |
| `--prompt-file` | | string | — | Prompt from file instead of positional arg |
| `--skill` | | string[] | `[]` | Repeatable; loads SKILL.md |
| `--skip-skills` | | bool | `false` | Skip skill injection (keep role engine/model/effort) |
| `--context-file` | | string | — | Large context file; injects read preamble |
| `--profile` | | string | — | Coordinator persona from agents/ (`coordinator` is accepted as an alias in `--stdin` JSON input only) |
| `--config` | | string | — | Explicit config path (overrides default lookup) |
| `--artifact-dir` | | string | auto | Override artifact directory |
| `--full` | `-f` | bool | `true` | Full access mode |
| `--no-full` | | bool | `false` | Disable full access |
| `--max-depth` | | int | `2` | Max recursive dispatch depth |
| `--yes` | | bool | `false` | Skip TTY confirmation |
| `--verbose` | `-v` | bool | `false` | Raw harness lines on stderr |
| `--version` | | bool | — | Print version |

### Engine-Specific Flags

| Flag | Short | Engine | Type | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--sandbox` | | Codex | string | `danger-full-access` | Sandbox mode |
| `--reasoning` | `-r` | Codex | string | `medium` | Reasoning effort |
| `--add-dir` | | Codex | string[] | `[]` | Additional writable directories (repeatable) |
| `--permission-mode` | | Claude | string | — | Permission mode |
| `--max-turns` | | Claude | int | 0 | Max agent turns |

### Dispatch Control Flags

| Flag | Short | Type | Default | Notes |
| --- | --- | --- | --- | --- |
| `--recover` | | string | — | Dispatch ID to continue from |
| `--signal` | | string | — | Dispatch ID to send a message to |
| `--stdin` | | bool | `false` | Read DispatchSpec JSON from stdin |
| `--async` | | bool | `false` | Fire-and-forget; emits `async_started` ack and returns immediately |

### Output Flags

| Flag | Short | Type | Default | Notes |
| --- | --- | --- | --- | --- |
| `--stream` | `-S` | bool | `false` | Stream all events to stderr |

## --stdin JSON

Reads a `DispatchSpec` JSON object from stdin. `prompt` must be non-empty; all other fields have defaults.

Defaults when field is absent from JSON:

| Field | Default |
| --- | --- |
| `dispatch_id` | Generated ULID |
| `cwd` | `os.Getwd()` |
| `artifact_dir` | `<sanitize.SecureArtifactRoot()>/<dispatch_id>/` (typically under `~/.agent-mux/` or `/tmp/agent-mux/`) |
| `full_access` | `true` |
| `grace_sec` | `60` |

The `coordinator` key is accepted as an alias for `profile` in stdin JSON. Decoding fails if both keys are present with different values.

Supported dispatch flags still apply in `--stdin` mode as explicit overrides for the decoded JSON fields.

## Config Subcommand

Inspect the fully-resolved configuration without running a dispatch. All modes respect `--config` and `--cwd`.

### config (bare)

```bash
agent-mux config [--config <path>] [--cwd <dir>]
```

Prints the full resolved config as JSON. The root key `_sources` lists loaded config files.

### config --sources

```bash
agent-mux config --sources
```

Prints only the config sources:

```json
{"kind":"config_sources","sources":["/Users/alice/.agent-mux/config.toml","/repo/.agent-mux/config.toml"]}
```

### config roles

```bash
agent-mux config roles [--json]
```

Tabular listing of all roles and variants:

```
NAME            ENGINE  MODEL       EFFORT  TIMEOUT
lifter          codex   gpt-5.4     high    1800s
  └ claude      claude  claude-...  high    1800s
```

### config models

```bash
agent-mux config models [--json]
```

Engine-to-model-list mapping.

### config skills

```bash
agent-mux config skills
```

Discoverable skills from all search paths.

## Preview

```bash
agent-mux preview [flags] <prompt>
```

Prints the fully resolved `DispatchSpec` as JSON without executing. Useful for verifying that config, roles, skills, and prompt composition resolved correctly before committing to a dispatch.

The preview JSON includes:

| Top-level key | Contents |
| --- | --- |
| `dispatch_spec` | Resolved engine, model, effort, cwd, artifact_dir, timeout, depth values |
| `result_metadata` | role, variant, profile, skills that were resolved |
| `prompt` | Excerpt of the composed prompt (first 280 runes), total chars, system_prompt_chars |
| `control` | control_record path and artifact_dir |
| `prompt_preamble` | Any preamble lines injected before the user prompt |
| `warnings` | Non-fatal resolution warnings |
| `confirmation_required` | Whether the dispatch would have required TTY confirmation |

## Mode Detection

| Invocation | Mode |
| --- | --- |
| `agent-mux` | top-level help |
| `agent-mux help` | top-level help |
| `agent-mux --help` | top-level help |
| `agent-mux [flags] <prompt>` | dispatch (default) |
| `agent-mux dispatch [flags] <prompt>` | dispatch (explicit) |
| `agent-mux preview [flags] <prompt>` | preview |
| `agent-mux --recover <id> [flags] <prompt>` | recover + dispatch |
| `agent-mux --signal <id> <message>` | signal |
| `agent-mux --stdin [flags]` | stdin dispatch |
| `agent-mux --version` | version |
| `agent-mux config [sub] [flags]` | config introspection |
| `agent-mux list [flags]` | lifecycle: list dispatches |
| `agent-mux status <id> [flags]` | lifecycle: dispatch status |
| `agent-mux result <id> [flags]` | lifecycle: dispatch result |
| `agent-mux inspect <id> [flags]` | lifecycle: deep dispatch view |
| `agent-mux wait <id> [flags]` | async: block until done |
| `agent-mux steer <id> <action> [args]` | steering: mid-flight control |
| `agent-mux -- help` | dispatch literal prompt `help` |

## Exit Codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Error (config, dispatch failed, signal failed, recovery failed) |
| `2` | Usage error (bad flags, missing prompt) |
| `130` | Cancelled at TTY confirmation prompt |

## Cross-References

- [Dispatch](./dispatch.md) for the DispatchSpec and DispatchResult contracts
- [Config](./config.md) for TOML structure, merge rules, roles
- [Engines](./engines.md) for engine-specific flag behavior
- [Lifecycle](./lifecycle.md) for lifecycle subcommand details
- [Async](./async.md) for `--async` and `wait`
- [Steering](./steering.md) for `steer` subcommand details
