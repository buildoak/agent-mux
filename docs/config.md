# Configuration
agent-mux resolves one dispatch spec from TOML config, optional profile overlays, and caller inputs.
This document describes the TOML configuration system as implemented in `internal/config` and the dispatch path that consumes it.
Use it as the source of truth for config discovery, schema, merge behavior, roles, profiles, and skill injection.

## Config File Discovery
Config sources load in this precedence order, lowest to highest:
1. `~/.agent-mux/config.toml`
2. `~/.agent-mux/config.local.toml`
3. `<cwd>/.agent-mux/config.toml`
4. `<cwd>/.agent-mux/config.local.toml`
5. `--config <path>`
Hardcoded defaults from `DefaultConfig()` sit underneath every file.
Rules:
- Later sources win on conflicts.
- Missing files are skipped.
- `cwd` is absolutized before project lookup.
- `config.local.toml` is intended for machine-local overrides and should stay out of version control.

### Legacy Fallback
The canonical global file is `~/.agent-mux/config.toml`. If it is absent, agent-mux checks `~/.config/agent-mux/config.toml`. When that legacy path is used, agent-mux emits a deprecation warning to stderr. Only one global base file is used, but `~/.agent-mux/config.local.toml` is still considered as the machine-local overlay.

### `--config` Directory Behavior
When `--config` is set, implicit global and project discovery is skipped entirely.
- If the path is a file, agent-mux loads that file.
- If the path is a directory, agent-mux tries:
  1. `<dir>/.agent-mux/config.toml`
  2. `<dir>/config.toml`
- If neither exists, loading fails.
## TOML Schema Reference
Top-level sections:
```toml
[defaults]
[models]
[timeout]
[liveness]
[hooks]
[async]
[skills]
[roles.NAME]
[roles.NAME.variants.VARIANT]
```
### `[defaults]`
`[defaults]` maps to `DefaultsConfig`:
```go
type DefaultsConfig struct {
	Engine         string `toml:"engine"`
	Model          string `toml:"model"`
	Effort         string `toml:"effort"`
	Sandbox        string `toml:"sandbox"`
	PermissionMode string `toml:"permission_mode"`
	MaxDepth       int    `toml:"max_depth"`
}
```
| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `engine` | string | `""` | Default engine if dispatch input did not set one |
| `model` | string | `""` | Default model |
| `effort` | string | `"high"` | Default effort |
| `sandbox` | string | `"danger-full-access"` | Default Codex sandbox mode |
| `permission_mode` | string | `""` | Default permission mode passed through engine options |
| `max_depth` | int | `2` | Default recursive dispatch depth |
Notes:
- Defaults are applied after role and profile inputs if the dispatch still leaves a field unset.
- `permission_mode` only applies when `engine_opts["permission-mode"]` is missing or empty.
- `max_depth` only applies when the dispatch still has `0`.
### `[models]`
`[models]` is `map[string][]string`, keyed by engine:
```toml
[models]
codex = ["gpt-5.4", "gpt-5.4-mini"]
claude = ["claude-sonnet-4-6", "claude-opus-4-6"]
```
Merge behavior is union-merge by engine:
- base list preserved
- overlay models appended
- duplicates removed in first-seen order
- new engines added
### `[timeout]`
`[timeout]` maps to `TimeoutConfig`:
```go
type TimeoutConfig struct {
	Low    int `toml:"low"`
	Medium int `toml:"medium"`
	High   int `toml:"high"`
	XHigh  int `toml:"xhigh"`
	Grace  int `toml:"grace"`
}
```
| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `low` | int | `120` | Seconds for `effort="low"` |
| `medium` | int | `600` | Seconds for `effort="medium"` |
| `high` | int | `1800` | Seconds for `effort="high"` and unknown effort fallback |
| `xhigh` | int | `2700` | Seconds for `effort="xhigh"` |
| `grace` | int | `60` | Post-timeout grace before hard kill |
Rules:
- `TimeoutForEffort()` trims and lowercases the effort.
- Unknown effort values fall back to `timeout.high`.
- Explicitly defined timeout values must be positive.
- The same validation applies to `roles.<name>.timeout` and `roles.<name>.variants.<v>.timeout`.
### `[liveness]`
`[liveness]` maps to `LivenessConfig`:
```go
type LivenessConfig struct {
	HeartbeatIntervalSec int `toml:"heartbeat_interval_sec"`
	SilenceWarnSeconds   int `toml:"silence_warn_seconds"`
	SilenceKillSeconds   int `toml:"silence_kill_seconds"`
}
```
| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `heartbeat_interval_sec` | int | `15` | Heartbeat emission interval |
| `silence_warn_seconds` | int | `90` | Frozen-warning threshold |
| `silence_kill_seconds` | int | `180` | Forced-kill threshold |
These values are copied into `DispatchSpec.EngineOpts` when the dispatch did not set them explicitly.
### `[hooks]`
`[hooks]` maps to `HooksConfig`:
```go
type HooksConfig struct {
	Deny            []string `toml:"deny"`
	Warn            []string `toml:"warn"`
	EventDenyAction string   `toml:"event_deny_action"`
}
```
| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `deny` | `[]string` | `nil` | Deny-match patterns |
| `warn` | `[]string` | `nil` | Warn-match patterns |
| `event_deny_action` | string | `""` | Event match action consumed by hook evaluation |
`deny` and `warn` are additive with deduplication across overlays.
### `[async]`
`[async]` maps to `AsyncConfig`:
```go
type AsyncConfig struct {
	PollInterval string `toml:"poll_interval"`
}
```
Only field:
| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `poll_interval` | string | `""` in config, `60s` effective fallback | Go duration string for async wait polling |
`AsyncPollInterval()` falls back to `60s` when the value is empty, invalid, or below `1s`.
### `[skills]`
`[skills]` maps to `SkillsConfig`:
```go
type SkillsConfig struct {
	SearchPaths []string `toml:"search_paths"`
}
```
| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `search_paths` | `[]string` | `nil` | Extra skill roots searched after cwd and configDir |
Resolution order for a skill named `foo`:
1. `<cwd>/.claude/skills/foo/SKILL.md`
2. `<configDir>/.claude/skills/foo/SKILL.md`
3. Each configured `search_path` as `<search_path>/foo/SKILL.md`
`search_paths` union-merge across config layers, and `~` expansion is supported.
## Role System
Roles live under `[roles.<name>]`:
```go
type RoleConfig struct {
	Engine           string                 `toml:"engine"`
	Model            string                 `toml:"model"`
	Effort           string                 `toml:"effort"`
	Timeout          int                    `toml:"timeout"`
	Skills           []string               `toml:"skills"`
	SystemPromptFile string                 `toml:"system_prompt_file"`
	Variants         map[string]RoleVariant `toml:"variants"`
	SourceDir        string                 `toml:"-"`
}

type RoleVariant struct {
	Engine           string   `toml:"engine"`
	Model            string   `toml:"model"`
	Effort           string   `toml:"effort"`
	Timeout          int      `toml:"timeout"`
	Skills           []string `toml:"skills"`
	SystemPromptFile string   `toml:"system_prompt_file"`
}
```
### Resolution Behavior
`ResolveRole()` only looks up `cfg.Roles[roleName]` and returns that stored role.
It does not:
- inherit from another role
- apply defaults
- apply a variant
- read `system_prompt_file`
- merge CLI skills
That is the no-inheritance rule: roles are independent named records. The only overlay mechanism is an explicitly selected variant, and that overlay is applied later during dispatch assembly.
### Variant Overlay Mechanics
Variants live under `[roles.<name>.variants.<variant>]` and are applied at runtime by `resolveVariant()` in `cmd/agent-mux/main.go`.
Overlay rules:
- non-empty `engine`, `model`, `effort` replace the base role value
- `timeout > 0` replaces the base timeout
- non-empty `system_prompt_file` replaces the base prompt file
- `skills` are additive at runtime: variant skills come before role skills, with first-seen deduplication
Example:
```toml
[roles.lifter]
engine = "codex"
model = "gpt-5.4"
skills = ["role-skill"]

[roles.lifter.variants.claude]
engine = "claude"
model = "claude-sonnet-4-6"
skills = ["variant-skill"]
```
Effective skills for `role=lifter`, `variant=claude`:
```toml
skills = ["variant-skill", "role-skill"]
```
## Merge Semantics
Config merging is implemented in `mergeConfig()` with a defined-wins helper:
```go
func merge[T comparable](dst *T, value T, defined bool) {
	var zero T
	if defined || value != zero {
		*dst = value
	}
}
```
Meaning:
- explicit overlay values always win, even when they are `false`, `0`, or `""`
- absent zero values do not clear the base
- TOML metadata is used to distinguish absent from explicitly set
### Conflict Resolution Table
| Section or type | Rule |
| --- | --- |
| Scalars in `[defaults]`, `[timeout]`, `[liveness]`, `[async]` | Last explicit definition wins per field |
| `[skills].search_paths` | Append and deduplicate |
| `[models].<engine>` | Append and deduplicate |
| `[hooks].deny`, `[hooks].warn` | Append and deduplicate |
| `[hooks].event_deny_action` | Last explicit definition wins |
| Role scalar fields | Last explicit definition wins |
| `roles.<name>.skills` | Replace the whole list when explicitly defined |
| Variant map entries | Add new variants; deep-merge name collisions |
| Variant scalar fields | Last explicit definition wins |
| `roles.<name>.variants.<v>.skills` | Replace the whole list when explicitly defined |
Practical consequences:
- lists do not all behave the same
- `[models]`, `[hooks]`, and `[skills].search_paths` are additive
- role and variant `skills` are replacement lists during config merge
- variant-to-role skill addition happens later at dispatch time, not during config merge
## Profile System
Profiles are loaded by `LoadProfile(name, cwd)` and return:
- `CoordinatorSpec` from markdown frontmatter plus body
- optional companion `*Config` from `<name>.toml`

The `coordinator` key is accepted as a stdin JSON alias for `profile`. If both keys appear in the same JSON object with different values, decoding fails. The `--coordinator` CLI flag does not exist; use `--profile`.
### Search Order
For `--profile=name`, agent-mux searches `name.md` in:
1. `<cwd>/.claude/agents/`
2. `<cwd>/agents/`
3. `<cwd>/.agent-mux/agents/`
4. `~/.agent-mux/agents/`
First match wins.
### Frontmatter Fields
Recognized YAML frontmatter fields:
- `engine`
- `model`
- `effort`
- `timeout`
- `skills`
The markdown body becomes `CoordinatorSpec.SystemPrompt`. If `timeout` is explicitly present, it must be positive.
### Companion `.toml`
If `name.toml` exists beside the matched profile file, it is loaded as a normal config overlay. That companion config:
- uses the same `Config` struct
- sets `SourceDir` for roles it defines
- runs the same explicit timeout validation
At dispatch assembly time it is merged after the main config load and before role resolution.

## Skill Injection
`LoadSkills()` runs only when the resolved dispatch includes skills and `SkipSkills` is false.
Inputs:
- ordered skill names
- `cwd`
- `configDir`
- `[skills].search_paths`
- `roleName` for diagnostics
### Search Order
Roots are built in this order:
1. `filepath.Join(cwd, ".claude", "skills")`
2. `filepath.Join(configDir, ".claude", "skills")`, only when `configDir != ""` and `configDir != cwd`
3. each configured search path after `~` expansion
For each requested skill `name`, agent-mux reads `<root>/<name>/SKILL.md`. The first readable match wins.
### `SKILL.md` Loading and XML Wrapping
Each loaded skill is wrapped as:
```text
<skill name="NAME">
...contents...
</skill>
```
Trailing newline-only suffixes are trimmed before the closing tag. Skills are emitted in request order, with duplicate names removed by first occurrence. The combined skill block is prepended to the user prompt.
### Script Directory Handling
If the resolved skill directory also contains `scripts/`, that directory is returned in `pathDirs`. The dispatch layer prepends those directories into `engine_opts["add-dir"]`, which makes helper scripts available on `PATH` for the harness.

### Cross-CWD Resolution Failure Diagnosis
The common failure pattern is a role loaded from one config tree while the current cwd points at another tree. In that case:
- cwd-relative lookup runs first
- configDir-relative lookup runs second
- explicit `search_paths` run last
If the skill is still missing, the error includes every searched path and the available skills discovered in those roots. Check that error before changing `search_paths`; it usually shows whether the wrong cwd or wrong config source directory was used.
## Cross-References
- [Dispatch](./dispatch.md)
- [Architecture](./architecture.md)
- [CLI Reference](./cli-reference.md)
