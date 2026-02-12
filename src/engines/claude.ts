/**
 * claude.ts — Claude Code engine adapter
 *
 * Thin wrapper around @anthropic-ai/claude-agent-sdk.
 * Uses query() async generator for heartbeat-compatible streaming.
 */

import { query } from "@anthropic-ai/claude-agent-sdk";
import type {
  EngineAdapter,
  RunConfig,
  EngineCallbacks,
  EngineResult,
  ActivityItem,
  EffortLevel,
} from "../types.ts";

const DEFAULT_MODEL = "claude-sonnet-4-20250514";

// Max turns scaled by effort level
const MAX_TURNS_BY_EFFORT: Record<EffortLevel, number> = {
  low: 5,
  medium: 15,
  high: 30,
  xhigh: 50,
};

export class ClaudeEngine implements EngineAdapter {
  async run(config: RunConfig, callbacks: EngineCallbacks): Promise<EngineResult> {
    const model = config.model || DEFAULT_MODEL;
    const permissionMode = (config.engineOptions.permissionMode as string) || "bypassPermissions";
    const maxTurns = (config.engineOptions.maxTurns as number) || MAX_TURNS_BY_EFFORT[config.effort] || 15;
    const maxBudget = config.engineOptions.maxBudget as number | undefined;
    const allowedTools = config.engineOptions.allowedTools as string[] | undefined;

    // Build SDK options
    const options: Record<string, unknown> = {
      model,
      permissionMode,
      maxTurns,
    };

    // bypassPermissions requires the safety flag
    if (permissionMode === "bypassPermissions") {
      options.allowDangerouslySkipPermissions = true;
    }

    if (config.cwd) {
      options.cwd = config.cwd;
    }

    if (maxBudget !== undefined) {
      options.maxBudgetUsd = maxBudget;
    }

    if (allowedTools) {
      options.allowedTools = allowedTools;
    }

    // System prompt
    if (config.systemPrompt) {
      options.systemPrompt = {
        type: "preset",
        preset: "claude_code",
        append: config.systemPrompt,
      };
    }

    // MCP servers
    if (Object.keys(config.mcpServers).length > 0) {
      const mcpServers: Record<string, unknown> = {};
      for (const [name, serverConfig] of Object.entries(config.mcpServers)) {
        mcpServers[name] = { type: "stdio", ...serverConfig };
      }
      options.mcpServers = mcpServers;

      // Ensure MCP tools are allowed if tools are restricted
      if (allowedTools) {
        for (const serverName of Object.keys(mcpServers)) {
          allowedTools.push(`mcp__${serverName}__*`);
        }
      }
    }

    // AbortController
    const abortController = new AbortController();
    // Forward the parent signal to our local controller
    config.signal.addEventListener("abort", () => abortController.abort(), { once: true });
    options.abortController = abortController;

    callbacks.onHeartbeat("starting claude agent");

    const items: ActivityItem[] = [];
    let finalResult = "";
    let sessionId = "";
    let costUsd = 0;
    let numTurns = 0;

    for await (const message of query({
      prompt: config.prompt,
      options: options as Parameters<typeof query>[0]["options"],
    })) {
      // Heartbeat on every message
      callbacks.onHeartbeat(describeMessage(message));

      switch (message.type) {
        case "system": {
          callbacks.onHeartbeat(`system init: ${(message as Record<string, unknown>).tools || "ready"}`);
          break;
        }
        case "assistant": {
          // Extract tool usage info for activity tracking
          const msg = message as Record<string, unknown>;
          const apiMessage = msg.message as Record<string, unknown> | undefined;
          if (apiMessage?.content) {
            const content = apiMessage.content as Array<Record<string, unknown>>;
            for (const block of content) {
              if (block.type === "tool_use") {
                const toolName = block.name as string;
                const toolInput = block.input as Record<string, unknown>;
                const item = classifyToolUse(toolName, toolInput);
                if (item) {
                  callbacks.onItem(item);
                  items.push(item);
                }
              }
            }
          }
          break;
        }
        case "result": {
          const result = message as Record<string, unknown>;
          sessionId = (result.session_id as string) || "";
          costUsd = (result.total_cost_usd as number) || 0;
          numTurns = (result.num_turns as number) || 0;

          if (message.subtype === "success") {
            finalResult = (result.result as string) || "(no response)";
          } else {
            // Error subtypes
            const errors = (result.errors as string[]) || [];
            throw new Error(
              `Claude agent error (${message.subtype}): ${errors.join("; ") || "unknown error"}`
            );
          }
          break;
        }
        case "tool_progress": {
          const tp = message as Record<string, unknown>;
          callbacks.onHeartbeat(
            `tool progress: ${tp.tool_name} (${tp.elapsed_time_seconds}s)`
          );
          break;
        }
      }
    }

    if (!finalResult) {
      throw new Error("No result message received from Claude agent");
    }

    return {
      response: finalResult,
      items,
      metadata: {
        session_id: sessionId,
        cost_usd: costUsd,
        turns: numTurns,
        model,
      },
    };
  }
}

function classifyToolUse(toolName: string, input: Record<string, unknown>): ActivityItem | null {
  if (toolName === "Edit" || toolName === "Write") {
    return {
      type: "file_change",
      summary: (input.file_path as string) || (input.path as string) || toolName,
    };
  }
  if (toolName === "Bash") {
    return {
      type: "command",
      summary: ((input.command as string) || "").slice(0, 200),
    };
  }
  if (toolName === "Read") {
    return {
      type: "file_read",
      summary: (input.file_path as string) || (input.path as string) || toolName,
    };
  }
  if (toolName.startsWith("mcp__")) {
    return {
      type: "mcp_call",
      summary: toolName,
    };
  }
  return null;
}

function describeMessage(message: Record<string, unknown>): string {
  const type = message.type as string;
  switch (type) {
    case "system":
      return "system init";
    case "assistant":
      return "assistant response";
    case "user":
      return "user message (tool result)";
    case "result":
      return `result: ${message.subtype}`;
    case "tool_progress":
      return `tool progress: ${message.tool_name}`;
    default:
      return `event: ${type}`;
  }
}
