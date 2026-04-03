# Lifecycle

Lifecycle subcommands provide post-dispatch introspection and management. They read durable records from `~/.agent-mux/dispatches/<id>/` and artifact directories created during dispatch — no running process required.

All lifecycle subcommands output human-readable tables by default. Pass `--json` for structured JSON output. Errors follow the standard envelope: `{"kind":"error","error":{...}}`.

## List

```bash
agent-mux list [--limit N] [--status completed|failed|timed_out] [--engine codex|claude|gemini] [--json]
```

Lists recent dispatches. Default limit is 20 (pass 0 for all).

Output columns: ID (12-character prefix), STATUS, ENGINE, MODEL, DURATION, CWD.

With `--json`, emits NDJSON (one record per line).

Example — show the last 5 failed Codex dispatches:

```bash
agent-mux list --limit 5 --status failed --engine codex
```

## Status

```bash
agent-mux status [--json] <dispatch_id>
```

Shows status for a single dispatch. Accepts full ID or unique prefix.

Fields shown: Status, Engine/Model, Duration, Started, Truncated, ArtifactDir.

For running dispatches, reads live `status.json` from the artifact directory. Detects orphaned processes where the host PID is dead but the dispatch was never marked terminal.

Example:

```bash
agent-mux status 01JA
```

## Result

```bash
agent-mux result [--json] [--artifacts] [--no-wait] <dispatch_id>
```

Retrieves the dispatch response. Accepts full ID or unique prefix.

Default behavior: prints the stored result text. Falls back to `full_output.md` in the artifact directory for truncated or legacy dispatches.

| Flag | Effect |
| --- | --- |
| `--artifacts` | Lists files in the artifact directory instead of printing the response |
| `--no-wait` | Returns an error if the dispatch is still running instead of blocking |
| `--json` | Structured JSON output |

If the dispatch is still running, blocks until completion by default.

Example — list artifacts:

```bash
agent-mux result --artifacts 01JARQ8X
```

## Inspect

```bash
agent-mux inspect [--json] <dispatch_id>
```

Deep view of a dispatch. Accepts full ID or unique prefix.

Shows all record fields: ID, Status, Engine, Model, Role, Variant, Started, Ended, Duration, Truncated, Cwd, ArtifactDir. Also includes artifact listing and full response text.

JSON mode adds `meta` from `~/.agent-mux/dispatches/<id>/meta.json` when present.

Example:

```bash
agent-mux inspect 01JARQ8X
```

## Wait

```bash
agent-mux wait [--json] [--poll <duration>] [--config <path>] [--cwd <dir>] <dispatch_id>
```

Blocks until an async dispatch reaches a terminal state. Polls `~/.agent-mux/dispatches/<id>/result.json` on each interval. Completion is defined by the presence of `result.json`, not by `status.json` state alone.

| Flag | Default | Purpose |
| --- | --- | --- |
| `--poll` | `60s` | Status poll interval (e.g. `5s`, `1m`) |
| `--json` | off | Emit JSON result when done |
| `--config` | standard | Config resolution for `poll_interval` fallback |
| `--cwd` | current dir | Working directory for config lookup |

Poll interval precedence: CLI `--poll` > `[async].poll_interval` in config.toml > hardcoded `60s`.

On each tick `wait` emits a status line to stderr:

```
[<elapsed_s>s] running | <N> tools | <N> files changed
```

`wait` exits with code `1` and emits an error if the dispatch is orphaned (host PID dead, no `result.json`) or timed out before `result.json` appeared. On success it prints the response text (or JSON with `--json`) and exits `0`.

Example:

```bash
agent-mux wait --poll 10s --json 01JARQ8X
```

## Cross-References

- [Dispatch](./dispatch.md) for the DispatchResult contract
- [Recovery](./recovery.md) for artifact directory layout and durable persistence
- [Async](./async.md) for `--async` dispatch and status.json semantics
- [CLI Reference](./cli-reference.md) for the complete flag table
