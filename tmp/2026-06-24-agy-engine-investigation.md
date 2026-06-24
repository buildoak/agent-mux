# 2026-06-24 agy engine investigation

## Observed current behavior

- Repo-root binary before rebuild: `./agent-mux --version` returned `agent-mux v3.3.0`.
- Repo-root binary before rebuild:
  - `./agent-mux help` advertised `Engine: codex, claude, gemini`.
  - `./agent-mux config` omitted `models.agy`.
  - `./agent-mux config engines` fell through to the root config summary instead of the engines capability table, also omitting `agy`.
- Installed PATH binary: `which agent-mux` resolved to `/Users/otonashi/.local/bin/agent-mux`.
- Installed PATH binary: `agent-mux --version` returned `agent-mux v3.4.2`.
- Installed PATH binary:
  - `agent-mux help` advertised `Engine: codex, claude, gemini`.
  - `agent-mux config engines` fell through to root config summary and omitted `agy`.
- Source-built behavior via `go run ./cmd/agent-mux ...` advertised and reported `agy`.
- After rebuilding the repo-local binary with `go build -o ./agent-mux ./cmd/agent-mux`, `./agent-mux --version` returned `agent-mux v3.5.0`; help advertised `Engine: agy, claude, codex, gemini`; config summary and `config engines` both included `agy`.
- Installed PATH binary remains stale at `v3.4.2`; I did not overwrite `/Users/otonashi/.local/bin/agent-mux`.

## Root cause

Stale binaries, not docs drift, adapter registration, or source CLI/config logic.

The current source registers `agy`, includes `agy` in default/cached model handling, includes `agy` in the engine capability matrix, and has tests asserting `agy` appears in help/config/registry behavior. The observed installed behavior comes from an older binary that predates the `config engines` subcommand and `agy` engine surface.

## Files inspected

- `cmd/agent-mux/help.go`
- `cmd/agent-mux/config_cmd.go`
- `cmd/agent-mux/main.go`
- `cmd/agent-mux/main_test.go`
- `cmd/agent-mux/config_cmd_test.go`
- `cmd/agent-mux/preflight.go`
- `internal/engine/adapter/registry.go`
- `internal/engine/adapter/registry_test.go`
- `internal/engine/adapter/agy.go`
- `internal/engine/adapter/agy_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/agy_models.go`
- `README.md`
- `docs/agy.md`
- `docs/engines.md`
- `docs/cli-reference.md`
- `docs/dispatch.md`
- `docs/lifecycle.md`
- `docs/prompting-guide.md`
- `docs/steering.md`
- `skill/SKILL.md`
- `tests/axeval/agy_contract_test.go`

## Files changed

- `agent-mux` was rebuilt in place with `go build -o ./agent-mux ./cmd/agent-mux`. It is ignored by git, so `git status --short` remains clean.
- `tmp/2026-06-24-agy-engine-investigation.md` was added as this report.

No source code or docs needed changes.

## Exact commands run

```sh
pwd && rg --files -g 'AGENTS.md' -g 'package.json' -g 'Cargo.toml' -g 'pyproject.toml' -g 'go.mod' -g 'README*'
sed -n '1,240p' /Users/otonashi/thinking/pratchett-os/coordinator/AGENTS.md
find .. -name AGENTS.md -print
rg -n "agy|codex|claude|gemini|engine|Engine|config|help|Usage" .
ls -la && find . -maxdepth 3 -type f | sort | sed -n '1,220p'
sed -n '1,220p' cmd/agent-mux/help.go
sed -n '1,260p' cmd/agent-mux/config_cmd.go
sed -n '1,180p' internal/engine/adapter/registry.go && sed -n '1,220p' internal/engine/adapter/agy.go && sed -n '1,170p' internal/config/config.go
git status --short
rg -n "validEngineCSV|ValidEngines|DefaultModels|EngineCapabilityMatrix|config engines|refresh-models|agy" cmd internal tests docs README.md skill/SKILL.md
which agent-mux; command -v agent-mux; ./agent-mux --version; agent-mux --version
./agent-mux help; ./agent-mux config; ./agent-mux config engines
go run ./cmd/agent-mux help; go run ./cmd/agent-mux config; go run ./cmd/agent-mux config engines
agent-mux help; agent-mux config; agent-mux config engines
rg -n "v3\\.4\\.2|v3\\.3\\.0|Version|version" cmd internal README.md CHANGELOG.md
go test ./cmd/agent-mux ./internal/config ./internal/engine/adapter
go build -o ./agent-mux ./cmd/agent-mux
./agent-mux --version; ./agent-mux help; ./agent-mux config; ./agent-mux config engines
agent-mux --version; agent-mux help; agent-mux config engines
go test ./cmd/agent-mux ./internal/config ./internal/engine/adapter
git status --short
git ls-files --stage agent-mux; git status --short --ignored agent-mux tmp
sed -n '1510,1522p' cmd/agent-mux/main.go; sed -n '239,310p' internal/config/config_test.go; sed -n '78,145p' cmd/agent-mux/config_cmd_test.go; sed -n '78,92p' internal/engine/adapter/registry_test.go
ls -la tmp && test -f tmp/2026-06-24-agy-engine-investigation.md && sed -n '1,220p' tmp/2026-06-24-agy-engine-investigation.md || true
```

## Rebuild or code fix?

Rebuild/install is enough. No code fix is needed for the inspected behavior.

Evidence:

- `internal/engine/adapter/registry.go` registers `agy`.
- `cmd/agent-mux/main.go` derives help engine text from `adapter.NewRegistry(config.DefaultModels()).ValidEngines()`, so help is not hard-coded to three engines in current source.
- `internal/config/config.go` includes `agy` in `DefaultModels()` and `EngineCapabilityMatrix()`.
- `cmd/agent-mux/config_cmd.go` exposes `config engines` and uses `config.EngineCapabilityMatrix()`.
- Focused tests passed:
  - `go test ./cmd/agent-mux ./internal/config ./internal/engine/adapter`
- Source-run and rebuilt repo binary both advertise/report `agy`.

## Next command Jenkins should run

```sh
cd /Users/otonashi/thinking/building/agent-mux && go build -o /Users/otonashi/.local/bin/agent-mux ./cmd/agent-mux && agent-mux --version && agent-mux help && agent-mux config engines
```

Expected gate:

- `agent-mux --version` returns `agent-mux v3.5.0`.
- `agent-mux help` includes `Engine: agy, claude, codex, gemini`.
- `agent-mux config engines` prints a table with an `agy` row.

## Post-install verification

Jenkins ran the install command after this investigation:

```sh
go build -o /Users/otonashi/.local/bin/agent-mux ./cmd/agent-mux
/Users/otonashi/.local/bin/agent-mux --version
/Users/otonashi/.local/bin/agent-mux help
/Users/otonashi/.local/bin/agent-mux config engines
```

Observed:

- Installed binary now returns `agent-mux v3.5.0`.
- Help now includes `Engine: agy, claude, codex, gemini`.
- `config engines` now prints an `agy` row with Gemini-family, Claude, and GPT-OSS models.

Smoke tests:

- Direct Gemini route failed:
  `agent-mux -E=gemini -m gemini-3-flash-preview -C=/tmp "Reply with exactly: gemini-smoke-ok"`
  exited with Gemini CLI error saying the client is no longer supported for Gemini Code Assist for individuals.
- Agy Gemini-family route succeeded:
  `agent-mux -E=agy -m "Gemini 3.5 Flash (Medium)" -C=/tmp "Reply with exactly: agy-gemini-smoke-ok"`
  returned `agy-gemini-smoke-ok`.

Operational conclusion:

- For Gemini-family second opinions right now, route through `agy`, not direct `gemini`.
  Direct `gemini` needs separate Gemini CLI migration/repair.
