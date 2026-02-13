/**
 * opencode.test.ts — OpenCode engine adapter unit tests
 *
 * Mocks the @opencode-ai/sdk to avoid real API calls.
 * Tests model preset resolution, config transformation, and session flow.
 */

import { describe, test, expect, mock, beforeEach } from "bun:test";
import type {
  RunConfig,
  EngineCallbacks,
  ActivityItem,
  EffortLevel,
} from "../../src/types.ts";

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

function makeCallbacks(): EngineCallbacks & {
  heartbeats: string[];
  items: ActivityItem[];
} {
  const heartbeats: string[] = [];
  const items: ActivityItem[] = [];
  return {
    heartbeats,
    items,
    onHeartbeat(activity: string) {
      heartbeats.push(activity);
    },
    onItem(item: ActivityItem) {
      items.push(item);
    },
  };
}

function makeConfig(overrides: Partial<RunConfig> = {}): RunConfig {
  return {
    prompt: "test prompt",
    cwd: "/tmp/test",
    timeout: 60_000,
    signal: new AbortController().signal,
    model: "",
    effort: "medium" as EffortLevel,
    mcpServers: {},
    engineOptions: {},
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// SDK mock
// ---------------------------------------------------------------------------

interface CapturedSession {
  createArgs: Record<string, unknown>;
  promptAsyncArgs: Record<string, unknown>;
  eventCalled: boolean;
}

let captured: CapturedSession;
let mockSseEvents: Array<Record<string, unknown>>;

const mockAbort = mock(() => Promise.resolve());
const mockMessages = mock(() => Promise.resolve({ data: [] }));

const mockClient = {
  session: {
    create: mock(async (args: Record<string, unknown>) => {
      captured.createArgs = args;
      return { data: { id: "test-session-id" }, error: null };
    }),
    promptAsync: mock(async (args: Record<string, unknown>) => {
      captured.promptAsyncArgs = args;
      return { error: null };
    }),
    abort: mockAbort,
    messages: mockMessages,
  },
  global: {
    event: mock(async () => ({
      stream: (async function* () {
        for (const event of mockSseEvents) {
          yield event;
        }
      })(),
    })),
  },
};

const mockServer = {
  url: "http://localhost:12345",
  close: mock(() => {}),
};

let capturedCreateOpencodeArgs: Record<string, unknown> = {};

const mockCreateOpencode = mock(async (args: Record<string, unknown>) => {
  capturedCreateOpencodeArgs = args;
  return {
    client: mockClient,
    server: mockServer,
  };
});

mock.module("@opencode-ai/sdk", () => ({
  createOpencode: mockCreateOpencode,
}));

// Mock toOpenCodeMcp
mock.module("../../src/mcp-clusters.ts", () => ({
  toOpenCodeMcp: (servers: Record<string, unknown>) => {
    const result: Record<string, unknown> = {};
    for (const [name, config] of Object.entries(servers)) {
      const cfg = config as { command: string; args: string[]; env?: Record<string, string> };
      result[name] = {
        type: "local",
        command: [cfg.command, ...cfg.args],
        ...(cfg.env ? { environment: cfg.env } : {}),
      };
    }
    return result;
  },
  getAllServerNames: () => [],
  resolveClusters: () => ({}),
  listClusters: () => "",
}));

// Import after mocking
const { OpenCodeEngine } = await import("../../src/engines/opencode.ts");

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

beforeEach(() => {
  captured = {
    createArgs: {},
    promptAsyncArgs: {},
    eventCalled: false,
  };
  mockSseEvents = [];
  capturedCreateOpencodeArgs = {};

  // Reset all mocks
  mockCreateOpencode.mockClear();
  mockClient.session.create.mockClear();
  mockClient.session.promptAsync.mockClear();
  mockClient.global.event.mockClear();
  mockServer.close.mockClear();
  mockAbort.mockClear();
  mockMessages.mockClear();

  // Reconfigure mock implementations (mockClear resets call tracking but not implementation)
  mockClient.session.create.mockImplementation(async (args: Record<string, unknown>) => {
    captured.createArgs = args;
    return { data: { id: "test-session-id" }, error: null };
  });
  mockClient.session.promptAsync.mockImplementation(async (args: Record<string, unknown>) => {
    captured.promptAsyncArgs = args;
    return { error: null };
  });
  mockClient.global.event.mockImplementation(async () => ({
    stream: (async function* () {
      for (const event of mockSseEvents) {
        yield event;
      }
    })(),
  }));
  mockCreateOpencode.mockImplementation(async (args: Record<string, unknown>) => {
    capturedCreateOpencodeArgs = args;
    return {
      client: mockClient,
      server: mockServer,
    };
  });
});

describe("OpenCodeEngine — interface compliance", () => {
  test("implements EngineAdapter.run()", () => {
    const engine = new OpenCodeEngine();
    expect(typeof engine.run).toBe("function");
  });
});

// ---------------------------------------------------------------------------
// Model preset resolution
// ---------------------------------------------------------------------------

describe("OpenCodeEngine — model preset resolution", () => {
  test("default model is kimi-k2.5 via openrouter", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    // Check the prompt body has the correct provider/model
    const body = captured.promptAsyncArgs.body as Record<string, unknown>;
    const model = body.model as { providerID: string; modelID: string };
    expect(model.providerID).toBe("openrouter");
    expect(model.modelID).toBe("moonshotai/kimi-k2.5");
    expect(result.metadata.model).toBe("openrouter/moonshotai/kimi-k2.5");
  });

  test("'kimi' preset resolves to kimi-k2.5", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({ model: "kimi" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/moonshotai/kimi-k2.5");
  });

  test("'glm' preset resolves to glm-5", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({ model: "glm" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/z-ai/glm-5");
  });

  test("'deepseek' preset resolves to deepseek-v3.2", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({ model: "deepseek" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/deepseek/deepseek-v3.2");
  });

  test("'qwen' preset resolves to qwen3-coder", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({ model: "qwen" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/qwen/qwen3-coder");
  });

  test("'free' preset resolves to glm-4.5-air:free", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({ model: "free" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/z-ai/glm-4.5-air:free");
  });

  test("full model string is passed through when not a preset", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({ model: "openrouter/custom/model-v1" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/custom/model-v1");
  });

  test("--variant acts as model preset when no model specified", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({
        model: "",
        engineOptions: { variant: "glm-5" },
      }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/z-ai/glm-5");
  });

  test("explicit model takes precedence over variant", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(
      makeConfig({
        model: "deepseek",
        engineOptions: { variant: "glm" },
      }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("openrouter/deepseek/deepseek-v3.2");
  });
});

// ---------------------------------------------------------------------------
// Config transformation
// ---------------------------------------------------------------------------

describe("OpenCodeEngine — config transformation", () => {
  test("session is created with the correct working directory", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    await engine.run(makeConfig({ cwd: "/my/project" }), makeCallbacks());

    expect(captured.createArgs.query).toEqual({ directory: "/my/project" });
  });

  test("prompt text is sent in parts array", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    await engine.run(
      makeConfig({ prompt: "write a function" }),
      makeCallbacks()
    );

    const body = captured.promptAsyncArgs.body as Record<string, unknown>;
    const parts = body.parts as Array<{ type: string; text: string }>;
    expect(parts).toHaveLength(1);
    expect(parts[0].type).toBe("text");
    expect(parts[0].text).toBe("write a function");
  });

  test("system prompt is passed in body", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    await engine.run(
      makeConfig({ systemPrompt: "be brief" }),
      makeCallbacks()
    );

    const body = captured.promptAsyncArgs.body as Record<string, unknown>;
    expect(body.system).toBe("be brief");
  });

  test("agent name is passed when specified", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    await engine.run(
      makeConfig({ engineOptions: { agent: "coder" } }),
      makeCallbacks()
    );

    const body = captured.promptAsyncArgs.body as Record<string, unknown>;
    expect(body.agent).toBe("coder");
  });

  test("MCP servers are passed in createOpencode config", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    await engine.run(
      makeConfig({
        mcpServers: {
          myserver: { command: "npx", args: ["-y", "srv"] },
        },
      }),
      makeCallbacks()
    );

    const config = capturedCreateOpencodeArgs.config as Record<string, unknown>;
    const mcp = config.mcp as Record<string, unknown>;
    expect(mcp.myserver).toEqual({
      type: "local",
      command: ["npx", "-y", "srv"],
    });
  });
});

// ---------------------------------------------------------------------------
// SSE event handling
// ---------------------------------------------------------------------------

describe("OpenCodeEngine — SSE event handling", () => {
  test("collects text from message.part.updated events", async () => {
    mockSseEvents = [
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "test-session-id",
            id: "p1",
            text: "Hello, world!",
          },
        },
      },
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("Hello, world!");
  });

  test("tracks tool invocations from SSE events", async () => {
    mockSseEvents = [
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "tool-invocation",
            sessionID: "test-session-id",
            toolName: "file_editor",
          },
        },
      },
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "test-session-id",
            id: "p1",
            text: "done",
          },
        },
      },
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new OpenCodeEngine();
    await engine.run(makeConfig(), callbacks);

    const mcpItems = callbacks.items.filter((i) => i.type === "mcp_call");
    expect(mcpItems).toHaveLength(1);
    expect(mcpItems[0].summary).toBe("file_editor");
  });

  test("collects cost and tokens from message.updated events", async () => {
    mockSseEvents = [
      {
        type: "message.updated",
        properties: {
          info: {
            role: "assistant",
            sessionID: "test-session-id",
            cost: 0.03,
            tokens: { input: 500, output: 200, reasoning: 100 },
          },
        },
      },
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "test-session-id",
            id: "p1",
            text: "result",
          },
        },
      },
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.metadata.cost_usd).toBe(0.03);
    expect(result.metadata.tokens).toEqual({
      input: 500,
      output: 200,
      reasoning: 100,
    });
  });

  test("session.error throws", async () => {
    mockSseEvents = [
      {
        type: "session.error",
        properties: {
          sessionID: "test-session-id",
          message: "model not available",
        },
      },
    ];

    const engine = new OpenCodeEngine();
    await expect(engine.run(makeConfig(), makeCallbacks())).rejects.toThrow(
      /OpenCode session error/
    );
  });

  test("ignores events from other sessions", async () => {
    mockSseEvents = [
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "other-session",
            id: "p1",
            text: "should be ignored",
          },
        },
      },
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "test-session-id",
            id: "p2",
            text: "correct response",
          },
        },
      },
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("correct response");
  });

  test("session.idle for other session does NOT break out of loop", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "other-session" },
      },
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "test-session-id",
            id: "p1",
            text: "after other idle",
          },
        },
      },
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("after other idle");
  });
});

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

describe("OpenCodeEngine — server cleanup", () => {
  test("server.close() is called after successful run", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    await engine.run(makeConfig(), makeCallbacks());

    expect(mockServer.close).toHaveBeenCalled();
  });

  test("server.close() is called even after error", async () => {
    mockClient.session.create.mockImplementation(async () => {
      throw new Error("connection refused");
    });

    const engine = new OpenCodeEngine();
    await expect(engine.run(makeConfig(), makeCallbacks())).rejects.toThrow(
      /connection refused/
    );

    expect(mockServer.close).toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// Heartbeats
// ---------------------------------------------------------------------------

describe("OpenCodeEngine — heartbeats", () => {
  test("heartbeats fire during session lifecycle", async () => {
    mockSseEvents = [
      {
        type: "message.part.updated",
        properties: {
          part: {
            type: "text",
            sessionID: "test-session-id",
            id: "p1",
            text: "ok",
          },
        },
      },
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new OpenCodeEngine();
    await engine.run(makeConfig(), callbacks);

    expect(callbacks.heartbeats.length).toBeGreaterThanOrEqual(3);
    expect(callbacks.heartbeats[0]).toBe("starting opencode server");
  });
});

// ---------------------------------------------------------------------------
// Result shape
// ---------------------------------------------------------------------------

describe("OpenCodeEngine — result shape", () => {
  test("returns (no text response) when no text collected", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("(no text response)");
  });

  test("result includes session_id", async () => {
    mockSseEvents = [
      {
        type: "session.idle",
        properties: { sessionID: "test-session-id" },
      },
    ];

    const engine = new OpenCodeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.metadata.session_id).toBe("test-session-id");
  });
});
