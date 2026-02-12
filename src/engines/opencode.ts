/**
 * opencode.ts — OpenCode engine adapter
 *
 * Thin wrapper around @opencode-ai/sdk. SDK-only (no CLI fallback).
 * Uses SSE events for heartbeat-compatible streaming.
 */

import { createOpencode, type OpencodeClient } from "@opencode-ai/sdk";
import { toOpenCodeMcp } from "../mcp-clusters.ts";
import type {
  EngineAdapter,
  RunConfig,
  EngineCallbacks,
  EngineResult,
  ActivityItem,
} from "../types.ts";

const DEFAULT_MODEL = "openrouter/moonshotai/kimi-k2.5";

// Model presets for quick selection
const MODEL_PRESETS: Record<string, string> = {
  // Kimi models
  kimi: "openrouter/moonshotai/kimi-k2.5",
  "kimi-k2.5": "openrouter/moonshotai/kimi-k2.5",
  "kimi-k2": "openrouter/moonshotai/kimi-k2",
  "kimi-k2-thinking": "openrouter/moonshotai/kimi-k2-thinking",
  "kimi-dev": "openrouter/moonshotai/kimi-dev-72b:free",
  "kimi-free": "openrouter/moonshotai/kimi-k2:free",
  // GLM models
  glm: "openrouter/z-ai/glm-5",
  "glm-5": "openrouter/z-ai/glm-5",
  "glm-4.7": "openrouter/z-ai/glm-4.7",
  "glm-4.7-flash": "openrouter/z-ai/glm-4.7-flash",
  "glm-4.6": "openrouter/z-ai/glm-4.6",
  "glm-4.5": "openrouter/z-ai/glm-4.5",
  "glm-free": "openrouter/z-ai/glm-4.5-air:free",
  // DeepSeek models
  deepseek: "openrouter/deepseek/deepseek-v3.2",
  "deepseek-r1": "openrouter/deepseek/deepseek-r1:free",
  "deepseek-v3.2": "openrouter/deepseek/deepseek-v3.2",
  // Qwen models
  qwen: "openrouter/qwen/qwen3-coder",
  "qwen-coder": "openrouter/qwen/qwen3-coder",
  "qwen-max": "openrouter/qwen/qwen3-max",
  // OpenCode native
  "opencode-kimi": "opencode/kimi-k2.5-free",
  "opencode-minimax": "opencode/minimax-m2.5-free",
  // Free tier quick picks
  free: "openrouter/z-ai/glm-4.5-air:free",
};

function resolveModel(input: string): { providerID: string; modelID: string; fullModel: string } {
  const resolved = MODEL_PRESETS[input] || input;
  const firstSlash = resolved.indexOf("/");
  if (firstSlash === -1) {
    return { providerID: "openrouter", modelID: resolved, fullModel: `openrouter/${resolved}` };
  }
  return {
    providerID: resolved.substring(0, firstSlash),
    modelID: resolved.substring(firstSlash + 1),
    fullModel: resolved,
  };
}

// Ensure ~/.opencode/bin is in PATH so the SDK can find the opencode binary
const opencodeDir = `${process.env.HOME}/.opencode/bin`;
if (!process.env.PATH?.includes(opencodeDir)) {
  process.env.PATH = `${opencodeDir}:${process.env.PATH}`;
}

export class OpenCodeEngine implements EngineAdapter {
  async run(config: RunConfig, callbacks: EngineCallbacks): Promise<EngineResult> {
    // --variant can act as a model preset shorthand
    const variant = config.engineOptions.variant as string | undefined;
    const modelInput = config.model || variant || DEFAULT_MODEL;
    const { providerID, modelID, fullModel } = resolveModel(modelInput);
    const agent = config.engineOptions.agent as string | undefined;

    // Build MCP config from requested clusters
    const mcpConfig: Record<string, { type: "local"; command: string[]; environment?: Record<string, string> }> = {};
    if (Object.keys(config.mcpServers).length > 0) {
      Object.assign(mcpConfig, toOpenCodeMcp(config.mcpServers));
    }

    callbacks.onHeartbeat("starting opencode server");

    // Start the OpenCode server and get a client
    const opencode = await createOpencode({
      timeout: 30_000,
      ...(Object.keys(mcpConfig).length > 0
        ? { config: { mcp: mcpConfig } }
        : {}),
    });

    const server = opencode.server;
    const client = opencode.client;

    try {
      return await this.runWithClient(
        client,
        server,
        config,
        callbacks,
        providerID,
        modelID,
        fullModel,
        agent
      );
    } finally {
      // Always clean up the server
      try {
        server.close();
      } catch {
        // Ignore cleanup errors
      }
    }
  }

  /** Helper: throw if already aborted */
  private checkAbort(signal: AbortSignal): void {
    if (signal.aborted) throw new Error("Aborted");
  }

  /** Helper: abort an OpenCode session on signal fire */
  private registerAbortHandler(
    signal: AbortSignal,
    client: OpencodeClient,
    sessionId: string,
    cwd?: string,
  ): void {
    if (signal.aborted) return;
    signal.addEventListener("abort", () => {
      client.session.abort({
        path: { id: sessionId },
        query: { directory: cwd || process.cwd() },
      }).catch(() => { /* best-effort */ });
    }, { once: true });
  }

  private async runWithClient(
    client: OpencodeClient,
    server: { url: string; close(): void },
    config: RunConfig,
    callbacks: EngineCallbacks,
    providerID: string,
    modelID: string,
    fullModel: string,
    agent?: string,
  ): Promise<EngineResult> {
    this.checkAbort(config.signal);
    callbacks.onHeartbeat("creating session");

    // Create a session
    const sessionResult = await client.session.create({
      query: { directory: config.cwd || process.cwd() },
    });

    if (sessionResult.error) {
      throw new Error(`Failed to create session: ${JSON.stringify(sessionResult.error)}`);
    }

    const session = sessionResult.data;
    if (!session) {
      throw new Error("Session creation returned no data");
    }

    // Register abort handler to cancel the session on timeout
    this.registerAbortHandler(config.signal, client, session.id, config.cwd);

    // Subscribe to SSE events
    this.checkAbort(config.signal);
    callbacks.onHeartbeat("subscribing to events");
    let sseStream: AsyncGenerator<unknown> | null = null;

    try {
      const eventResult = await client.global.event();
      // Extract stream — handle both { stream } (typed) and { data } (runtime) shapes
      const result = eventResult as Record<string, unknown>;
      if (result.stream && typeof (result.stream as AsyncGenerator<unknown>)[Symbol.asyncIterator] === "function") {
        sseStream = result.stream as AsyncGenerator<unknown>;
      } else if (result.data && typeof (result.data as AsyncGenerator<unknown>)[Symbol.asyncIterator] === "function") {
        sseStream = result.data as AsyncGenerator<unknown>;
      }
    } catch {
      // SSE subscription failed — fall through to sync
    }

    if (!sseStream) {
      // Fall back to sync if SSE fails
      return await this.runSync(client, session.id, config, callbacks, providerID, modelID, fullModel, agent);
    }

    // Start the async prompt
    this.checkAbort(config.signal);
    callbacks.onHeartbeat("sending prompt");
    const promptResult = await client.session.promptAsync({
      path: { id: session.id },
      body: {
        parts: [{ type: "text", text: config.prompt }],
        model: { providerID, modelID },
        ...(config.systemPrompt ? { system: config.systemPrompt } : {}),
        ...(agent ? { agent } : {}),
      } as Parameters<typeof client.session.promptAsync>[0]["body"],
      query: { directory: config.cwd || process.cwd() },
    });

    // Check promptAsync result for errors
    if ((promptResult as Record<string, unknown>)?.error) {
      throw new Error(`promptAsync failed: ${JSON.stringify((promptResult as Record<string, unknown>).error)}`);
    }

    // Collect response from SSE events
    const items: ActivityItem[] = [];
    const textParts = new Map<string, string>();
    let cost = 0;
    let tokens = { input: 0, output: 0, reasoning: 0 };

    for await (const event of sseStream) {
      // Check abort signal
      if (config.signal.aborted) {
        break;
      }

      // Handle both direct event shape and wrapped payload shape
      const eventObj = event as Record<string, unknown>;
      const payload = (eventObj.payload ?? eventObj) as Record<string, unknown>;
      if (!payload) continue;

      const eventType = (payload.type as string) || "";
      const props = (payload.properties ?? payload) as Record<string, unknown>;

      callbacks.onHeartbeat(`event: ${eventType}`);

      // Collect text part updates for our session
      if (eventType === "message.part.updated") {
        const part = props.part as Record<string, unknown> | undefined;
        if (part?.type === "text" && part?.sessionID === session.id) {
          const partId = (part.id as string) || "default";
          textParts.set(partId, (part.text as string) || "");
          callbacks.onItem({
            type: "message",
            summary: ((part.text as string) || "").slice(0, 200),
          });
          items.push({
            type: "message",
            summary: ((part.text as string) || "").slice(0, 200),
          });
        }

        // Track tool calls
        if (part?.sessionID === session.id) {
          const partType = part?.type as string;
          if (partType === "tool-invocation" || partType === "tool") {
            const toolName = (part.toolName as string) || (part.tool as string) || "unknown";
            const item: ActivityItem = {
              type: "mcp_call",
              summary: toolName,
            };
            callbacks.onItem(item);
            items.push(item);
          }
        }
      }

      // Collect message metadata updates
      if (eventType === "message.updated") {
        const info = props.info as Record<string, unknown> | undefined;
        if (info?.role === "assistant" && info?.sessionID === session.id) {
          cost = (info.cost as number) || cost;
          const t = info.tokens as Record<string, number> | undefined;
          if (t) {
            tokens = {
              input: t.input || 0,
              output: t.output || 0,
              reasoning: t.reasoning || 0,
            };
          }
        }
      }

      // Detect session idle = agent is done
      if (eventType === "session.idle") {
        const idleSessionId = props.sessionID as string;
        if (idleSessionId === session.id) {
          break;
        }
      }

      // Detect session error
      if (eventType === "session.error") {
        const errorSessionId = props.sessionID as string;
        if (errorSessionId === session.id) {
          throw new Error(`OpenCode session error: ${JSON.stringify(props)}`);
        }
      }
    }

    // Compose final text from accumulated parts
    let textContent = [...textParts.values()].join("\n").trim();

    // If no text was collected from events, try fetching messages
    if (!textContent) {
      textContent = await this.fetchMessages(client, session.id, config.cwd);
    }

    return {
      response: textContent || "(no text response)",
      items,
      metadata: {
        session_id: session.id,
        cost_usd: cost,
        tokens,
        model: fullModel,
      },
    };
  }

  private async runSync(
    client: OpencodeClient,
    sessionId: string,
    config: RunConfig,
    callbacks: EngineCallbacks,
    providerID: string,
    modelID: string,
    fullModel: string,
    agent?: string,
  ): Promise<EngineResult> {
    callbacks.onHeartbeat("using sync prompt (SSE unavailable)");
    this.checkAbort(config.signal);

    const promptResult = await client.session.prompt({
      path: { id: sessionId },
      body: {
        parts: [{ type: "text", text: config.prompt }],
        model: { providerID, modelID },
        ...(config.systemPrompt ? { system: config.systemPrompt } : {}),
        ...(agent ? { agent } : {}),
      } as Parameters<typeof client.session.prompt>[0]["body"],
      query: { directory: config.cwd || process.cwd() },
    });

    if (promptResult.error) {
      throw new Error(`Prompt failed: ${JSON.stringify(promptResult.error)}`);
    }

    const data = promptResult.data;
    if (!data) {
      throw new Error("Prompt returned no data");
    }

    // Extract text and activity from parts
    const textParts: string[] = [];
    const items: ActivityItem[] = [];
    for (const part of data.parts || []) {
      const p = part as Record<string, unknown>;
      if (p.type === "text" && p.text) {
        textParts.push(p.text as string);
      }
      const partType = p.type as string;
      if (partType === "tool-invocation" || partType === "tool") {
        const toolName = (p.toolName as string) || (p.tool as string) || "unknown";
        const item: ActivityItem = { type: "mcp_call", summary: toolName };
        callbacks.onItem(item);
        items.push(item);
      }
    }

    // Try fetching messages if no text parts in direct response
    let textContent = textParts.join("\n");
    if (!textContent) {
      textContent = await this.fetchMessages(client, sessionId, config.cwd);
    }

    const info = (data.info as Record<string, unknown>) || {};
    const tokensData = (info.tokens as Record<string, number>) || {};
    const costData = (info.cost as number) || 0;

    return {
      response: textContent || "(no text response)",
      items,
      metadata: {
        session_id: sessionId,
        cost_usd: costData,
        tokens: {
          input: tokensData.input || 0,
          output: tokensData.output || 0,
          reasoning: tokensData.reasoning || 0,
        },
        model: fullModel,
      },
    };
  }

  private async fetchMessages(
    client: OpencodeClient,
    sessionId: string,
    cwd?: string,
  ): Promise<string> {
    try {
      const messagesResult = await client.session.messages({
        path: { id: sessionId },
        query: { directory: cwd || process.cwd() },
      });

      if (messagesResult.data) {
        const messages = messagesResult.data as Array<{
          info: Record<string, unknown>;
          parts: Array<Record<string, unknown>>;
        }>;
        const textParts: string[] = [];
        for (const msg of messages) {
          if (msg.info?.role === "assistant") {
            for (const part of msg.parts || []) {
              if (part.type === "text" && part.text) {
                textParts.push(part.text as string);
              }
            }
          }
        }
        return textParts.join("\n").trim();
      }
    } catch {
      // Ignore
    }
    return "";
  }
}
