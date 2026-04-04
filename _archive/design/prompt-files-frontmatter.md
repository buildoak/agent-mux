# Design: Prompt Files with Frontmatter Dispatch Configuration

**Status:** Draft
**Date:** 2026-04-04
**Author:** R. Jenkins + user

## Problem

agent-mux has three overlapping worker-identity mechanisms:

1. **Roles** (`-R scout`) — `[roles.scout]` in config.toml. Bundles engine + model + effort + timeout + skills + system_prompt_file. Couples identity with infrastructure. Config resolution is cwd-dependent and fragile.
2. **Profiles** (`--profile scout`) — markdown files in `agents/` with YAML frontmatter. Already parse frontmatter for model/effort/engine/skills/timeout. Body becomes system prompt. This is the closest existing mechanism to what we want.
3. **CLI flags** (`--system-prompt-file` + `--skill` + `--effort`) — fully explicit, no bundling. Verbose but unambiguous.

Profiles already do 90% of what "prompt files with frontmatter" describes. The gap is:
- `--prompt-file` reads a file as the **task prompt**, not as system prompt + config.
- `--system-prompt-file` reads a file as system prompt but doesn't parse frontmatter.
- `--profile` parses frontmatter and uses body as system prompt, but lives in a separate `agents/` directory hierarchy with a separate name.

**Core insight:** Profiles ARE prompt files with frontmatter. The feature is already built. What's missing is: (a) unifying the naming, (b) making `--prompt-file` parse frontmatter too, and (c) deprecating roles in favor of this.

---

## Design

### Principle: Don't Invent, Converge

Rather than building a new "prompt file" concept, we converge the existing `--profile` system (which already does frontmatter parsing via `coordinator.go`) with `--prompt-file` (which currently reads raw text) and deprecate roles.

### 1. Frontmatter Schema

Frontmatter fields (YAML between `---` fences). All optional.

```yaml
---
skills: [web-search, gaal]    # skill names to load
effort: high                   # default effort level
timeout: 300                   # default timeout in seconds
---
```

**Included in frontmatter** (defaults the caller can override):
| Field     | Type       | CLI Override          | Semantics |
|-----------|------------|-----------------------|-----------|
| `skills`  | `[]string` | `--skill` (additive)  | Skill names to load |
| `effort`  | `string`   | `--effort` / `-e`     | low/medium/high/xhigh |
| `timeout` | `int`      | `--timeout` / `-t`    | seconds, must be > 0 |

**Excluded from frontmatter** (always CLI/caller's choice):
| Field    | Why |
|----------|-----|
| `engine` | Infrastructure. Caller picks the engine. |
| `model`  | Infrastructure. Caller picks the model. |

This is a **breaking change from profiles**, which currently allow `engine` and `model` in frontmatter. See migration section.

**Wait. Pressure-test: Should engine/model stay in frontmatter?**

Arguments for keeping them:
- Profiles already support them. Removing is a regression.
- Some workers genuinely need a specific engine (e.g., a Codex-only scout that uses `codex exec`).
- The caller can always override with CLI flags. Frontmatter is just a default.

Arguments for removing:
- Coupling identity to infrastructure is the exact problem roles have.
- A "scout" shouldn't care whether it runs on Codex or Claude.
- Forces callers to be explicit about resource allocation.

**Decision: Keep engine/model in frontmatter as overridable defaults.** The pragmatic reality is that some prompt files are tuned for specific engines (Codex scouts that depend on `codex exec` semantics, Claude workers that use MCP). The caller always wins via CLI flags. The anti-pattern isn't "a default engine in the file" -- it's "the only way to set the engine is in the file." As long as CLI overrides work, the coupling is opt-in, not structural.

Revised schema:

```yaml
---
skills: [web-search, gaal]
effort: high
timeout: 300
engine: codex          # default, overridable
model: gpt-5.4-mini   # default, overridable
---
```

This is **identical to the existing `CoordinatorSpec` frontmatter** in `coordinator.go`. No new fields needed.

### 2. Resolution Order

```
CLI flags  >  frontmatter defaults  >  config.toml [defaults]  >  hardcoded defaults
```

Specifically, in `runDispatch()`:

```
1. buildDispatchSpecE() captures CLI flags into DispatchSpec
2. If --prompt-file is set:
   a. Parse frontmatter from the file
   b. Body becomes system prompt (spec.SystemPrompt)
   c. Frontmatter skills prepend to annotations.Skills
   d. Frontmatter effort/timeout/engine/model apply only if CLI didn't set them
3. If --profile is set:
   a. Same logic as today (LoadProfile, apply to spec)
   b. --prompt-file and --profile are mutually exclusive
4. If -R is set:
   a. Same logic as today (ResolveRole, apply to spec) [deprecated]
5. Config.toml defaults fill remaining gaps
6. Hardcoded defaults fill the rest
```

**Skill composition:** Frontmatter skills + CLI `--skill` flags are **additive** and **deduplicated**. Frontmatter skills come first (they define the worker's baseline capabilities), CLI skills append (caller augments for this specific dispatch). This matches how roles already merge skills.

```
final_skills = deduplicate(frontmatter.skills + cli.skills)
```

### 3. `--prompt-file` Behavior Change

Currently, `--prompt-file` reads the entire file as the **task prompt** (the user message). This is the key change:

**New behavior:** `--prompt-file` parses frontmatter. If frontmatter is present:
- Frontmatter fields become dispatch defaults (skills, effort, timeout, engine, model)
- Body becomes the **system prompt** (not the task prompt)
- The task prompt must come from positional args (required)

If no frontmatter is present (file doesn't start with `---`):
- Entire file content becomes the task prompt (backward compatible)
- This is the existing behavior, unchanged

```
# Frontmatter present: body = system prompt, task prompt from args
agent-mux -E codex --prompt-file prompts/scout.md "audit this service"

# No frontmatter: entire file = task prompt (backward compat)
agent-mux -E codex --prompt-file task.md
```

**Wait. Pressure-test: Is this mode-switching dangerous?**

Yes. A file that happens to start with `---` (e.g., a markdown task description with a horizontal rule) would be silently misinterpreted. The existing `splitFrontmatter()` function already handles this -- it requires the first line to be exactly `---` followed by a newline, and requires a closing `---`. A stray horizontal rule mid-file won't trigger it. The risk is real but narrow: a task file that genuinely starts with `---\n...\n---\n` as decorative markdown. This is rare enough that the dual-mode is acceptable.

**Alternative considered:** A separate flag `--worker-file` that always parses frontmatter, keeping `--prompt-file` for raw task prompts. Rejected because it fragments the interface further. The frontmatter detection is robust enough.

### 4. `--prompt-file` Becomes the Unified Interface

With this change, `--prompt-file` subsumes three things:
- `--profile` (frontmatter + system prompt from a named file)
- `--system-prompt-file` (system prompt from a file)
- `--prompt-file` current behavior (task prompt from a file)

The mode depends on content:
```
File has frontmatter?
  YES -> body = system prompt, fields = dispatch config, task prompt from args
  NO  -> entire file = task prompt (legacy behavior)
```

### 5. Short-Name Resolution (`-P scout`)

Currently, `--profile scout` searches these directories for `scout.md`:
```
<cwd>/.claude/agents/
<cwd>/agents/
<cwd>/.agent-mux/agents/
~/.agent-mux/agents/
```

Proposal: Add `prompts/` as an additional search directory, and alias `-P` to `--prompt-file` with name resolution:

```
agent-mux -P scout -E codex "audit this service"
```

Resolves `scout` by searching:
```
<cwd>/.agent-mux/prompts/scout.md    # NEW: project prompt files
<cwd>/.claude/agents/scout.md         # existing profile path
<cwd>/agents/scout.md                 # existing profile path
~/.agent-mux/prompts/scout.md         # NEW: global prompt files
~/.agent-mux/agents/scout.md          # existing profile path
```

**Implementation:** `-P` is syntactic sugar. When the value is not an absolute/relative path (no `/` or `.` prefix), treat it as a name and resolve via search directories. When it contains a path separator, treat it as a direct file path.

```
-P scout                -> name resolution -> prompts/scout.md
-P ./my-prompts/scout.md -> direct file path
-P /abs/path/scout.md    -> direct file path
```

### 6. `agent-mux config prompts`

New config subcommand. Discovers all prompt files across search directories.

```
$ agent-mux config prompts
NAME               PATH                                          SOURCE
scout              ~/.agent-mux/prompts/scout.md                 global (prompts)
gsd-coordinator    /repo/agents/gsd-coordinator.md               project (agents)
heavy-lifter       /repo/agents/heavy-lifter.md                  project (agents)
```

JSON output with `--json`:
```json
[
  {"name":"scout","path":"~/.agent-mux/prompts/scout.md","source":"global (prompts)","skills":["web-search"],"effort":"low","timeout":60},
  {"name":"gsd-coordinator","path":"/repo/agents/gsd-coordinator.md","source":"project (agents)","skills":[],"effort":"xhigh","timeout":1800}
]
```

### 7. Role Deprecation

**Phase 1 (this PR):** `--prompt-file` gains frontmatter parsing. `-P` alias added. `agent-mux config prompts` added. Roles and profiles continue working unchanged.

**Phase 2 (next minor):** `-R scout` emits a stderr warning: `warning: -R is deprecated, use -P scout instead`. If a role name matches a prompt file name, the prompt file wins. Roles still work but are second-class.

**Phase 3 (next major):** `-R` removed. `[roles.*]` sections in config.toml ignored with a warning. Migration script: `agent-mux migrate-roles` converts each `[roles.X]` into a prompt file at `.agent-mux/prompts/X.md`.

**Migration script logic:**
```
For each [roles.X] in config.toml:
  1. Create .agent-mux/prompts/X.md
  2. Frontmatter: engine, model, effort, timeout, skills from role config
  3. Body: content of system_prompt_file if set, else empty
  4. Print: "Migrated role 'X' -> .agent-mux/prompts/X.md"
```

### 8. Interaction with `--profile`

`--profile` and `--prompt-file` (with frontmatter) do the same thing. During Phase 1-2, both work. During Phase 3, `--profile` becomes an alias for `-P`.

**Mutual exclusivity during Phase 1:**
```
--prompt-file + --profile  -> error: "use --prompt-file or --profile, not both"
--prompt-file + -R         -> error: "use --prompt-file or -R, not both"
--profile + -R             -> already handled (existing code)
```

---

## Code Changes

### Files Modified

| File | Change | Lines |
|------|--------|-------|
| `cmd/agent-mux/main.go` | `buildDispatchSpecE`: parse frontmatter when `--prompt-file` has `---`. Add `-P` flag. Add mutual exclusivity checks. | ~40 |
| `cmd/agent-mux/main.go` | `runDispatch`: insert prompt-file frontmatter application between CLI flags and role/profile application. | ~30 |
| `cmd/agent-mux/config_cmd.go` | Add `runConfigPrompts` subcommand, wire into `runConfigCommand`. | ~60 |
| `internal/config/coordinator.go` | Extract `splitFrontmatter` and `loadCoordinatorSpec` parsing into a reusable `ParsePromptFile(data []byte, name string) (*CoordinatorSpec, error)` — currently this is already `loadCoordinatorSpec` but takes a path. Make it accept raw bytes. | ~15 |
| `internal/config/coordinator.go` | Add `DiscoverPromptFiles(cwd, searchPaths)` scanning both `prompts/` and `agents/` directories. | ~40 |

### Files Unchanged

| File | Why |
|------|-----|
| `internal/types/types.go` | No new fields on DispatchSpec. Frontmatter maps onto existing fields. |
| `internal/config/config.go` | Roles stay as-is during Phase 1. Config loading unchanged. |
| `internal/config/skills.go` | Skill loading is already generic. Prompt files produce skill names, skill loading consumes them. No change needed. |
| `internal/dispatch/dispatch.go` | Dispatch logic operates on DispatchSpec which is already fully resolved. |

### New Code: Frontmatter Parsing in `--prompt-file`

In `buildDispatchSpecE`, the `--prompt-file` block changes from:

```go
// BEFORE
if flags.promptFile != "" {
    data, readErr := os.ReadFile(flags.promptFile)
    if readErr != nil { ... }
    prompt = string(data)
}
```

To:

```go
// AFTER
if flags.promptFile != "" {
    data, readErr := os.ReadFile(resolvedPath)
    if readErr != nil { ... }
    frontmatter, body, fmErr := splitFrontmatter(data)
    if fmErr != nil { ... }
    if len(frontmatter) > 0 {
        // Frontmatter mode: body = system prompt, task from args
        systemPrompt = strings.TrimSpace(body)
        pfSpec, parseErr := parsePromptFileFrontmatter(frontmatter)
        if parseErr != nil { ... }
        promptFileSpec = pfSpec  // stashed for later application
    } else {
        // Legacy mode: entire file = task prompt
        prompt = string(data)
    }
}
```

Then in `runDispatch`, between CLI flag capture and role/profile application:

```go
if promptFileSpec != nil {
    applyPreset(promptFileSpec.Engine, promptFileSpec.Model, promptFileSpec.Effort)
    if !flagsSet["timeout"] && !flagsSet["t"] && promptFileSpec.Timeout > 0 {
        spec.TimeoutSec = promptFileSpec.Timeout
    }
    req.DispatchAnnotations.Skills = mergeSkills(promptFileSpec.Skills, req.DispatchAnnotations.Skills)
}
```

This is structurally identical to the existing profile application block. The resolution order is preserved: CLI > prompt-file > config defaults.

### Estimated Complexity

- **~185 lines of new/modified Go code** across 3 files
- **~80 lines of new tests** (frontmatter detection, skill merge, mutual exclusivity)
- **0 new dependencies** (reuses existing `gopkg.in/yaml.v3` and `splitFrontmatter`)
- **0 changes to the dispatch pipeline** (all changes are in config resolution)

---

## Pressure-Test

### Is frontmatter-in-prompt-files over-engineering?

**The honest answer: this feature already exists.** It's called `--profile`. Profiles are markdown files with YAML frontmatter where the body is the system prompt. The entire `coordinator.go` file implements exactly this.

What this design actually proposes is:
1. Making `--prompt-file` aware of frontmatter (dual-mode: raw task prompt or system-prompt-with-config)
2. Adding `-P` as a short alias with name resolution
3. Deprecating roles in favor of prompt files
4. Adding `config prompts` for discoverability

This is convergence, not invention. The risk is low because the parsing code and resolution logic are already battle-tested in the profile system.

### Would `--system-prompt-file` + explicit `--skill` flags be simpler?

Yes, strictly simpler. But it means every dispatch call looks like:
```
agent-mux -E codex --system-prompt-file prompts/scout.md --skill web-search --skill gaal --effort low --timeout 60 "audit this"
```
vs:
```
agent-mux -E codex -P scout "audit this"
```

The prompt file bundles the worker's identity (system prompt + default skills + default effort). The caller only specifies infrastructure (engine) and task (positional arg). This is the right separation of concerns.

### What breaks?

**`--prompt-file` behavior change when file has frontmatter.** If someone is using `--prompt-file` with a file that happens to have valid YAML frontmatter, the body would become system prompt instead of task prompt. Mitigation: this is unlikely in practice (task prompts don't start with `---\nskills: [...]\n---`) and the frontmatter must contain known YAML keys to parse.

**Actually, wait.** Even `splitFrontmatter` doesn't validate the YAML keys -- it just splits on `---` fences. A markdown file starting with `---\ntitle: My Task\n---\n\nDo the thing` would split, the frontmatter would parse as an empty `CoordinatorSpec` (unknown fields ignored by `yaml.v3`), and the body "Do the thing" would become the system prompt instead of the task prompt.

**This is a real risk.** Mitigation options:
1. **Require at least one known field** in frontmatter to activate prompt-file mode. If frontmatter parses but contains zero recognized fields (no skills, no effort, no timeout, no engine, no model), treat the entire file as a raw task prompt.
2. **Use a sentinel field** like `kind: prompt-file` to opt in.
3. **Don't change `--prompt-file` at all.** Just use `-P` for frontmatter-aware loading.

**Decision: Option 3.** `--prompt-file` stays as-is (raw task prompt). `-P` / `--prompt-file-with-config` is the new flag that parses frontmatter. Actually, even simpler: `-P` is just a short form of `--profile` with expanded search paths. The profile system already does everything. We just need to:
- Add `prompts/` to the profile search path
- Add `-P` as a short alias for `--profile`
- Add `config prompts` for discovery
- Plan role deprecation

**Revised design:** No behavior change to `--prompt-file`. The "prompt files with frontmatter" feature is `--profile` / `-P` with better naming and broader search paths.

### What happens when a prompt file references a nonexistent skill?

Same as today: `LoadSkills` returns an error with the skill name, search paths tried, and available skills. The dispatch fails before launch. No change needed.

### What happens when two prompt files are specified?

`-P scout -P lifter` -- the flag is a string, so the second value wins. This matches Go's `flag` package behavior. If we want to support composition, that's a future feature (and the right answer would be composing skills, not composing system prompts).

### What happens with `-P scout -R lifter`?

Error: mutually exclusive. Same as `--profile` + `-R` should be today (though I see the current code doesn't enforce this -- both apply sequentially with role overriding profile). This should be tightened.

### Edge case: prompt file with frontmatter but empty body

Valid. System prompt is empty string. Only frontmatter fields (skills, defaults) apply. The worker runs with no system prompt but with the configured skills. This is fine -- some dispatches don't need a system prompt.

---

## CLI Examples

```bash
# Named prompt file (searches prompts/ and agents/ dirs)
agent-mux -E codex -P scout "audit internal/config for dead code"

# Direct path to prompt file
agent-mux -E codex -P ./my-prompts/analyst.md "review this PR"

# Override frontmatter defaults
agent-mux -E claude -m sonnet-4.6 -P scout --effort high "deep audit"

# Add skills beyond what the prompt file bundles
agent-mux -E codex -P scout --skill gaal "find recent session about X"

# Raw task prompt (unchanged behavior)
agent-mux -E codex --prompt-file task-description.md

# System prompt from file (unchanged behavior)
agent-mux -E codex --system-prompt-file custom-prompt.md "do the thing"

# Discovery
agent-mux config prompts
agent-mux config prompts --json
```

---

## Summary of Actual Changes

The "prompt files with frontmatter" feature is a **rename and polish of the existing profile system**, not a new system. Concrete changes:

1. **`-P` flag** as short alias for `--profile` (~5 lines in flag registration)
2. **Expanded search paths** for profile resolution: add `<cwd>/.agent-mux/prompts/` and `~/.agent-mux/prompts/` (~10 lines in `coordinator.go`)
3. **`agent-mux config prompts`** subcommand (~60 lines in `config_cmd.go`)
4. **Mutual exclusivity enforcement** for `-P` + `-R` (~10 lines in `runDispatch`)
5. **Role deprecation warning** in Phase 2 (~15 lines in `runDispatch`)
6. **`agent-mux migrate-roles`** script in Phase 3 (~80 lines, new subcommand)
7. **`--prompt-file` unchanged** -- it stays as raw task prompt reader
8. **Remove engine/model from frontmatter?** No. Keep them as overridable defaults.

Total new code: ~100 lines for Phase 1. The hard part (frontmatter parsing, skill loading, resolution order) is already done.
