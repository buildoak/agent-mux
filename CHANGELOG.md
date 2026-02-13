# Changelog

All notable changes to this project will be documented in this file.

## [2.1.0] - 2026-02-13

### Added
- Bundled agent-browser MCP server (25 tools) with interactive snapshot mode
- 7 new browser tools: reload, check, uncheck, dblclick, clear, focus, get_html

## [2.0.0] - 2026-02-13

### Added
- Unified CLI for three AI coding agent engines: Codex, Claude Code, OpenCode
- Engine adapter pattern with shared core orchestration
- YAML-based MCP cluster configuration with project-local and user-global fallback
- Stderr heartbeat protocol (15s intervals) for long-running processes
- Timeout-as-partial-success: timed out runs return `success: true` with partial results
- Activity tracking: file changes, commands, file reads, MCP tool calls
- Structured JSON output contract for all success, error, and timeout paths
- Per-engine effort levels mapping to timeouts and turn limits
- Claude Code skill file (SKILL.md) for skill-based installation
- Pre-flight API key validation with actionable error messages
- Graceful shutdown on SIGINT/SIGTERM with partial result collection
- Setup script for first-run experience
- Comprehensive test suite (165 tests)
- GitHub Actions CI (type-check + tests)
- `--version` flag
