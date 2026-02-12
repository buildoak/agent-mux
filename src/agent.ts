#!/usr/bin/env bun
/**
 * agent.ts — agent-mux entry point
 *
 * One CLI to spawn Codex, Claude Code, or OpenCode agents.
 *   bun run agent.ts --engine codex "prompt"
 *   bun run agent.ts --engine claude "prompt"
 *   bun run agent.ts --engine opencode "prompt"
 */

import { run } from "./core.ts";
import { CodexEngine } from "./engines/codex.ts";
import { ClaudeEngine } from "./engines/claude.ts";
import { OpenCodeEngine } from "./engines/opencode.ts";
import type { EngineName, EngineAdapter } from "./types.ts";

const engines: Record<EngineName, () => EngineAdapter> = {
  codex: () => new CodexEngine(),
  claude: () => new ClaudeEngine(),
  opencode: () => new OpenCodeEngine(),
};

run((engine: EngineName) => engines[engine]());
