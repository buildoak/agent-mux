/**
 * codex.test.ts — Codex engine adapter unit tests
 *
 * Mocks the @openai/codex-sdk to avoid real API calls.
 * Tests config transformation and activity classification.
 */

import { describe, test, expect, mock, beforeEach } from "bun:test";
import type {
  RunConfig,
  EngineCallbacks,
  EngineResult,
  ActivityItem,
  EffortLevel,
} from "../../src/types.ts";

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

/** Collects callback invocations */
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
    engineOptions: {
      sandbox: "read-only",
      reasoning: "medium",
      network: false,
      addDirs: [],
    },
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// SDK mock setup
// ---------------------------------------------------------------------------

// We build a mock Codex class that captures the options it receives
// and returns a controllable stream of events.

interface CapturedOptions {
  codexOptions: Record<string, unknown>;
  threadOptions: Record<string, unknown>;
  prompt: string;
  runOptions: Record<string, unknown>;
}

let captured: CapturedOptions;
let mockEvents: Array<Record<string, unknown>>;

// Create mock before importing the module
const mockStartThread = mock((threadOptions: Record<string, unknown>) => {
  captured.threadOptions = threadOptions;
  return {
    runStreamed: mock((prompt: string, runOptions: Record<string, unknown>) => {
      captured.prompt = prompt;
      captured.runOptions = runOptions;
      return {
        events: {
          async *[Symbol.asyncIterator]() {
            for (const event of mockEvents) {
              yield event;
            }
          },
        },
      };
    }),
  };
});

const MockCodex = mock((codexOptions: Record<string, unknown>) => {
  captured.codexOptions = codexOptions;
  return {
    startThread: mockStartThread,
  };
});

// Mock the SDK module
mock.module("@openai/codex-sdk", () => ({
  Codex: MockCodex,
}));

// Mock getAllServerNames to return a known set
mock.module("../../src/mcp-clusters.ts", () => ({
  getAllServerNames: () => ["server-a", "server-b"],
  resolveClusters: () => ({}),
  listClusters: () => "",
  toOpenCodeMcp: () => ({}),
}));

// Import the engine AFTER mocking
const { CodexEngine } = await import("../../src/engines/codex.ts");

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

beforeEach(() => {
  captured = {
    codexOptions: {},
    threadOptions: {},
    prompt: "",
    runOptions: {},
  };
  mockEvents = [];
  MockCodex.mockClear();
  mockStartThread.mockClear();
});

describe("CodexEngine — interface compliance", () => {
  test("implements EngineAdapter.run()", () => {
    const engine = new CodexEngine();
    expect(typeof engine.run).toBe("function");
  });
});

describe("CodexEngine — config transformation", () => {
  test("uses default model when none specified", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "done" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 10, output_tokens: 20 },
      },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({ model: "" });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.model).toBe("gpt-5.3-codex");
  });

  test("uses specified model when provided", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 10, output_tokens: 20 },
      },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({ model: "gpt-4o" });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.model).toBe("gpt-4o");
  });

  test("sandbox mode is passed to thread options", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({
      engineOptions: {
        sandbox: "workspace-write",
        reasoning: "medium",
        network: false,
        addDirs: [],
      },
    });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.sandboxMode).toBe("workspace-write");
  });

  test("reasoning effort is passed to thread options", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({
      engineOptions: {
        sandbox: "read-only",
        reasoning: "high",
        network: false,
        addDirs: [],
      },
    });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.modelReasoningEffort).toBe("high");
  });

  test("working directory is passed to thread options", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({ cwd: "/my/project" });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.workingDirectory).toBe("/my/project");
  });

  test("network access is passed to thread options", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({
      engineOptions: {
        sandbox: "read-only",
        reasoning: "medium",
        network: true,
        addDirs: [],
      },
    });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.networkAccessEnabled).toBe(true);
  });

  test("additional directories are passed when non-empty", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({
      engineOptions: {
        sandbox: "read-only",
        reasoning: "medium",
        network: false,
        addDirs: ["/extra/dir"],
      },
    });
    await engine.run(config, makeCallbacks());

    expect(captured.threadOptions.additionalDirectories).toEqual(["/extra/dir"]);
  });

  test("skipGitRepoCheck is always true", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    await engine.run(makeConfig(), makeCallbacks());

    expect(captured.threadOptions.skipGitRepoCheck).toBe(true);
  });
});

describe("CodexEngine — MCP server override logic", () => {
  test("disables all known servers by default", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    await engine.run(makeConfig(), makeCallbacks());

    const mcpConfig = (captured.codexOptions as Record<string, Record<string, unknown>>)?.config?.mcp_servers as Record<string, unknown>;
    expect(mcpConfig).toBeDefined();
    // Known servers from mock: server-a, server-b — both should be disabled
    expect(mcpConfig["server-a"]).toEqual({ enabled: false });
    expect(mcpConfig["server-b"]).toEqual({ enabled: false });
  });

  test("enables requested MCP servers from clusters", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      { type: "turn.completed", usage: { input_tokens: 0, output_tokens: 0 } },
    ];

    const engine = new CodexEngine();
    const config = makeConfig({
      mcpServers: {
        "server-a": {
          command: "npx",
          args: ["-y", "my-server"],
        },
      },
    });
    await engine.run(config, makeCallbacks());

    const mcpConfig = (captured.codexOptions as Record<string, Record<string, unknown>>)?.config?.mcp_servers as Record<string, Record<string, unknown>>;
    expect(mcpConfig["server-a"].enabled).toBe(true);
    expect(mcpConfig["server-a"].command).toBe("npx");
    // server-b should still be disabled
    expect(mcpConfig["server-b"]).toEqual({ enabled: false });
  });
});

describe("CodexEngine — event handling and results", () => {
  test("extracts response from agent_message item", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "Hello, world!" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 100, output_tokens: 50 },
      },
    ];

    const engine = new CodexEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("Hello, world!");
  });

  test("returns (no response) when no agent_message", async () => {
    mockEvents = [
      {
        type: "turn.completed",
        usage: { input_tokens: 10, output_tokens: 5 },
      },
    ];

    const engine = new CodexEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("(no response)");
  });

  test("accumulates token counts across turns", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "step 1" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 100, output_tokens: 50 },
      },
      {
        type: "item.completed",
        item: { type: "agent_message", text: "step 2" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 200, output_tokens: 100 },
      },
    ];

    const engine = new CodexEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.metadata.tokens?.input).toBe(300);
    expect(result.metadata.tokens?.output).toBe(150);
  });

  test("classifies file_change items", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: {
          type: "file_change",
          status: "completed",
          changes: [
            { path: "src/foo.ts", kind: "modified" },
            { path: "src/bar.ts", kind: "created" },
          ],
        },
      },
      {
        type: "item.completed",
        item: { type: "agent_message", text: "done" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 0, output_tokens: 0 },
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new CodexEngine();
    await engine.run(makeConfig(), callbacks);

    const fileItems = callbacks.items.filter((i) => i.type === "file_change");
    expect(fileItems).toHaveLength(2);
    expect(fileItems[0].summary).toBe("src/foo.ts");
    expect(fileItems[1].summary).toBe("src/bar.ts");
  });

  test("classifies command_execution items", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: {
          type: "command_execution",
          command: "npm test",
          aggregated_output: "all tests passed",
        },
      },
      {
        type: "item.completed",
        item: { type: "agent_message", text: "done" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 0, output_tokens: 0 },
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new CodexEngine();
    await engine.run(makeConfig(), callbacks);

    const cmdItems = callbacks.items.filter((i) => i.type === "command");
    expect(cmdItems).toHaveLength(1);
    expect(cmdItems[0].summary).toBe("npm test");
  });

  test("classifies mcp_tool_call items", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: {
          type: "mcp_tool_call",
          server: "exa",
          tool: "search",
        },
      },
      {
        type: "item.completed",
        item: { type: "agent_message", text: "done" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 0, output_tokens: 0 },
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new CodexEngine();
    await engine.run(makeConfig(), callbacks);

    const mcpItems = callbacks.items.filter((i) => i.type === "mcp_call");
    expect(mcpItems).toHaveLength(1);
    expect(mcpItems[0].summary).toBe("exa/search");
  });

  test("heartbeat is called on every event", async () => {
    mockEvents = [
      { type: "item.started", item: { type: "agent_message" } },
      { type: "item.updated", item: { type: "agent_message" } },
      {
        type: "item.completed",
        item: { type: "agent_message", text: "done" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 0, output_tokens: 0 },
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new CodexEngine();
    await engine.run(makeConfig(), callbacks);

    // "starting codex agent" + 4 events = 5 heartbeats
    expect(callbacks.heartbeats.length).toBe(5);
    expect(callbacks.heartbeats[0]).toBe("starting codex agent");
  });

  test("turn.failed throws an error", async () => {
    mockEvents = [
      {
        type: "turn.failed",
        error: { message: "rate limited" },
      },
    ];

    const engine = new CodexEngine();
    await expect(engine.run(makeConfig(), makeCallbacks())).rejects.toThrow(
      /Codex turn failed: rate limited/
    );
  });

  test("error event throws an error", async () => {
    mockEvents = [
      {
        type: "error",
        message: "connection lost",
      },
    ];

    const engine = new CodexEngine();
    await expect(engine.run(makeConfig(), makeCallbacks())).rejects.toThrow(
      /Codex stream error: connection lost/
    );
  });
});

describe("CodexEngine — result metadata", () => {
  test("includes model in metadata", async () => {
    mockEvents = [
      {
        type: "item.completed",
        item: { type: "agent_message", text: "ok" },
      },
      {
        type: "turn.completed",
        usage: { input_tokens: 10, output_tokens: 20 },
      },
    ];

    const engine = new CodexEngine();
    const result = await engine.run(
      makeConfig({ model: "gpt-4o" }),
      makeCallbacks()
    );

    expect(result.metadata.model).toBe("gpt-4o");
  });
});
