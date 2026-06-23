# Lifecycle

Lifecycle commands are the read and control surfaces around stored dispatches:

- `list`
- `status`
- `result`
- `inspect`
- `wait`

They resolve dispatches primarily through `~/.agent-mux/dispatches/<dispatch_id>/` and use artifact directories for live status, artifact listing, and compatibility fallbacks.

Successful output is human-readable by default. Failures are emitted as JSON envelopes of the form `{"kind":"error","error":{...}}`.

## `list`

```bash
agent-mux list [--limit N] [--status completed|failed|timed_out] [--engine agy|claude|codex|gemini] [--json]
```

- Reads dispatch records from `DispatchesDir()`, which resolves to `~/.agent-mux/dispatches/`.
- Sorts records newest-first.
- Defaults to `--limit 20`; `--limit 0` means all.
- `--json` emits NDJSON, one `DispatchRecord` per line.

Default table columns are `ID`, `STATUS`, `ENGINE`, `MODEL`, `DURATION`, and `CWD`.

## `status`

```bash
agent-mux status [--json] <dispatch_id_or_unique_prefix>
```

Behavior:

- If a durable `DispatchRecord` already has a terminal status, `status` reports that record.
- Otherwise it reads `<artifact_dir>/status.json`.
- For live async runs it also checks `host.pid`; if the PID is gone while the state is still running, it reports `orphaned`.

Human output shows either:

- durable fields: status, engine/model, duration, started, truncated, artifact dir
- or live fields: state, elapsed, last activity, tools used, files changed, artifact dir

## `result`

```bash
agent-mux result [--json] [--artifacts] [--no-wait] <dispatch_id_or_unique_prefix>
```

Default behavior:

- read `~/.agent-mux/dispatches/<dispatch_id>/result.json`
- print the stored `response`

If no durable result exists yet:

- `--no-wait` returns a small JSON object with `error: "dispatch_running"` when the run is still live
- otherwise `result` polls every second until the dispatch reaches a terminal state

`--artifacts` switches the command to artifact listing mode and prints files from the resolved artifact directory instead of the response.

If there is still no durable result for an older dispatch, `result` falls back to legacy `full_output.md` under the default artifact path.

`--json` on `result` does not emit the full stored `DispatchResult`; it emits a thinner object containing at least `dispatch_id` and `response`, plus `status`, `session_id`, and sometimes `kill_reason` when those can be resolved.

## `inspect`

```bash
agent-mux inspect [--json] <dispatch_id_or_unique_prefix>
```

`inspect` requires a dispatch that resolves to a durable record.

It combines:

- the dispatch record
- the stored response when present
- the resolved artifact directory
- the scanned artifact list
- `meta` from `_dispatch_ref.json` and the durable store when available

The human-readable form prints the main record fields first, then artifacts, then the response body.

## `wait`

```bash
agent-mux wait [--json] [--poll <duration>] [--config <path>] [--cwd <dir>] <dispatch_id_or_unique_prefix>
```

`wait` blocks on the durable result path:

```text
~/.agent-mux/dispatches/<dispatch_id>/result.json
```

Poll interval precedence is:

1. CLI `--poll`
2. config `[async].poll_interval`
3. built-in default `60s`

Intervals shorter than `1s` are clamped to `1s`.

During polling, `wait` writes progress lines to stderr such as:

```text
[42s] running | 7 tools | 3 files changed
```

Failure cases before `result.json` appears:

- dispatch deadline passed
- `host.pid` exists but the process is dead
- live `status.json` reports `timed_out`
- live `status.json` reports `orphaned`

On success, stdout matches `result`; with `--json`, stdout matches `result --json`.

## Cross-References

- [async.md](./async.md) for async ack and live status semantics
- [recovery.md](./recovery.md) for `_dispatch_ref.json`, `meta.json`, and `result.json`
- [cli-reference.md](./cli-reference.md) for the full command and flag surface
