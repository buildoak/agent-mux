# agent-mux Technical Documentation

The full technical reference lives in `docs/`. Each document is self-contained for its scope.

## Documentation Index

| Document | Scope |
| --- | --- |
| [Architecture](docs/architecture.md) | Design principles, system diagram, package map, data flow, concurrency model |
| [Dispatch](docs/dispatch.md) | Dispatch flow, prompt composition, DispatchSpec/DispatchResult contracts, mode detection |
| [Engines](docs/engines.md) | HarnessAdapter interface, Codex/Claude/Gemini adapters, model validation, authentication |
| [Config](docs/config.md) | TOML schema, config resolution, roles, variants, merge semantics, profiles, skill injection |
| [Recovery](docs/recovery.md) | Artifact directories, persistence, timeout system, liveness watchdog, supervisor |
| [Async](docs/async.md) | Async dispatch, status.json, streaming protocol v2, event types |
| [Steering](docs/steering.md) | Signal/inbox system, steer commands (abort, nudge, redirect, extend, status) |
| [Lifecycle](docs/lifecycle.md) | list, status, result, inspect, wait subcommands |
| [CLI Reference](docs/cli-reference.md) | Complete flag table, --stdin JSON, config subcommand, preview, exit codes |
| [Prompting Guide](docs/prompting-guide.md) | Per-engine prompting patterns, context tools, recovery/signal phrasing, sequential patterns |
| [ax-eval](docs/ax-eval.md) | Behavioral test framework, case model, evaluation waves, judge, trace system |

## Reference Files

Deeper reference material lives in `references/`:

| Reference | Read when |
| --- | --- |
| [cli-flags.md](references/cli-flags.md) | You need the complete flag table or DispatchSpec JSON fields |
| [config-guide.md](references/config-guide.md) | You need TOML structure, variant tables, config resolution order |
| [output-contract.md](references/output-contract.md) | You need exact JSON schemas for dispatch, signal, events |
| [engine-comparison.md](references/engine-comparison.md) | You need engine harness details, permission/sandbox mapping |
| [prompting-guide.md](references/prompting-guide.md) | You are crafting prompts, phrasing recovery, sequential dispatch patterns |
| [recovery-signal.md](references/recovery-signal.md) | You need recovery continuation, signal delivery, artifact layout |
| [streaming-protocol-v2.md](references/streaming-protocol-v2.md) | You need streaming event details and protocol specification |
