# agent-mux Updates

Structured changelog for AI agents. Read this to determine what changed and whether updates are safe to apply.

## 2026-02-17

### new-files
Files that didn't exist before. Safe to copy without conflict risk.

| File | Description |
|------|-------------|
| `references/installation-guide.md` | Detailed agent-readable installation guide for Claude Code and Codex CLI |
| `UPDATES.md` | This file -- structured changelog for AI agents |
| `UPDATE-GUIDE.md` | Instructions for AI agents performing skill updates |

### changed-files
Files that were modified. Review diff before applying if you have local edits.

| File | What changed | Breaking? |
|------|-------------|-----------|
| `SKILL.md` | +How to install section (3 options), +Staying Updated section | No -- additive |
| `README.md` | +`references/installation-guide.md` in Bundled Reference Docs, +Staying Updated section | No -- additive |

### removed-files
(none)

### breaking-changes
(none)

### migration-notes
- New `references/installation-guide.md` -- if updating from a previous version, this is a new file (safe to copy)
- `SKILL.md` has two new sections but no existing content was changed

## 2026-02-13

### Initial release
All files are new. Copy the entire agent-mux directory.

| Category | Files |
|----------|-------|
| Core | `SKILL.md`, `README.md`, `setup.sh`, `CHANGELOG.md` |
| Source | `src/agent.ts`, `src/core.ts`, `src/types.ts`, `src/mcp-clusters.ts` |
| Engines | `src/engines/codex.ts`, `src/engines/claude.ts`, `src/engines/opencode.ts` |
| References | `references/output-contract.md`, `references/prompting-guide.md`, `references/engine-comparison.md` |
| MCP | `src/mcp-servers/agent-browser.mjs`, `mcp-clusters.example.yaml` |
| Tests | `tests/` (full test suite) |
| Config | `package.json`, `tsconfig.json`, `bun.lock` |
