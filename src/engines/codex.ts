/**
 * codex.ts — Codex engine adapter
 *
 * Thin wrapper around @openai/codex-sdk. Uses runStreamed() for
 * heartbeat-compatible streaming execution.
 */

import {
  Codex,
  type CodexOptions,
  type ModelReasoningEffort,
  type SandboxMode,
  type ThreadEvent,
  type ThreadItem,
} from "@openai/codex-sdk";
import { getAllServerNames } from "../mcp-clusters.ts";
import type {
  EngineAdapter,
  RunConfig,
  EngineCallbacks,
  EngineResult,
  ActivityItem,
} from "../types.ts";

const DEFAULT_MODEL = "gpt-5.3-codex";
const DEFAULT_REASONING: ModelReasoningEffort = "medium";
const VALID_REASONING: ModelReasoningEffort[] = ["minimal", "low", "medium", "high", "xhigh"];

/** Classify a ThreadItem into one or more ActivityItems */
function classifyItem(item: ThreadItem): ActivityItem[] {
  switch (item.type) {
    case "file_change":
      return item.changes.map((c) => ({
        type: "file_change" as const,
        summary: c.path,
        detail: `${item.status}: ${c.kind} ${c.path}`,
      }));
    case "command_execution":
      return [{
        type: "command",
        summary: item.command,
        detail: item.aggregated_output?.slice(0, 500),
      }];
    case "mcp_tool_call":
      return [{
        type: "mcp_call",
        summary: `${item.server}/${item.tool}`,
      }];
    case "agent_message":
      return [{
        type: "message",
        summary: item.text.slice(0, 200),
      }];
    default:
      return [];
  }
}

export class CodexEngine implements EngineAdapter {
  async run(config: RunConfig, callbacks: EngineCallbacks): Promise<EngineResult> {
    const model = config.model || DEFAULT_MODEL;
    const reasoningInput = (config.engineOptions.reasoning as string) || "medium";
    const reasoning: ModelReasoningEffort = VALID_REASONING.includes(reasoningInput as ModelReasoningEffort)
      ? (reasoningInput as ModelReasoningEffort)
      : DEFAULT_REASONING;
    const sandbox = (config.engineOptions.sandbox as SandboxMode) || "read-only";
    const network = (config.engineOptions.network as boolean) || false;
    const addDirs = (config.engineOptions.addDirs as string[]) || [];

    // Build MCP config overrides
    // Strategy: when clusters are requested, disable non-requested cluster servers
    // and enable only the requested ones. When no clusters are requested, don't
    // inject any MCP overrides — let the user's config.toml load as-is.
    type CodexConfig = NonNullable<CodexOptions["config"]>;
    const mcpOverride: CodexConfig = {};
    const hasClusters = Object.keys(config.mcpServers).length > 0;

    if (hasClusters) {
      const enabledNames = new Set(Object.keys(config.mcpServers));

      // Disable cluster-defined servers that are NOT in the requested set.
      // We must include a dummy command so Codex SDK validation doesn't fail
      // on servers that only exist in mcp-clusters.yaml (not in config.toml).
      for (const name of getAllServerNames()) {
        if (!enabledNames.has(name)) {
          mcpOverride[name] = { enabled: false, command: "true", args: [] };
        }
      }

      // Enable the requested cluster servers with full config
      for (const [name, serverConfig] of Object.entries(config.mcpServers)) {
        mcpOverride[name] = { enabled: true, ...serverConfig } as CodexConfig;
      }
    }

    const codexOptions: CodexOptions = {
      config: hasClusters ? { mcp_servers: mcpOverride } : undefined,
    };

    const codex = new Codex(codexOptions);
    const thread = codex.startThread({
      model,
      sandboxMode: sandbox,
      modelReasoningEffort: reasoning,
      workingDirectory: config.cwd,
      skipGitRepoCheck: true,
      networkAccessEnabled: network,
      additionalDirectories: addDirs.length ? addDirs : undefined,
    });

    callbacks.onHeartbeat("starting codex agent");

    // Use runStreamed for heartbeat-compatible execution
    const fullPrompt = config.systemPrompt
      ? `<system-context>\n${config.systemPrompt}\n</system-context>\n\n${config.prompt}`
      : config.prompt;
    const streamedTurn = await thread.runStreamed(fullPrompt, {
      signal: config.signal,
    });

    let response = "";
    const items: ActivityItem[] = [];
    let totalInputTokens = 0;
    let totalOutputTokens = 0;

    for await (const event of streamedTurn.events) {
      // Heartbeat on every event
      callbacks.onHeartbeat(describeEvent(event));

      switch (event.type) {
        case "item.started":
        case "item.updated": {
          break;
        }
        case "item.completed": {
          const classified = classifyItem(event.item);
          for (const item of classified) {
            callbacks.onItem(item);
            items.push(item);
          }
          // Extract response text from agent messages
          if (event.item.type === "agent_message") {
            response = event.item.text;
          }
          break;
        }
        case "turn.completed": {
          if (event.usage) {
            totalInputTokens += event.usage.input_tokens;
            totalOutputTokens += event.usage.output_tokens;
          }
          break;
        }
        case "turn.failed": {
          throw new Error(`Codex turn failed: ${event.error.message}`);
        }
        case "error": {
          throw new Error(`Codex stream error: ${event.message}`);
        }
      }
    }

    return {
      response: response || "(no response)",
      items,
      metadata: {
        model,
        tokens: { input: totalInputTokens, output: totalOutputTokens },
      },
    };
  }
}

function describeEvent(event: ThreadEvent): string {
  switch (event.type) {
    case "thread.started":
      return "thread started";
    case "turn.started":
      return "turn started";
    case "turn.completed":
      return `turn completed (${event.usage?.input_tokens ?? 0} in, ${event.usage?.output_tokens ?? 0} out)`;
    case "turn.failed":
      return `turn failed: ${event.error.message}`;
    case "item.started":
      return `${event.item.type} started`;
    case "item.updated":
      return `${event.item.type} updating`;
    case "item.completed":
      return `${event.item.type} completed`;
    case "error":
      return `error: ${event.message}`;
    default:
      return "unknown event";
  }
}
