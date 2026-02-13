/**
 * claude.test.ts — Claude Code engine adapter unit tests
 *
 * Mocks the @anthropic-ai/claude-agent-sdk to avoid real API calls.
 * Tests config transformation, effort→maxTurns mapping, and message handling.
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
    engineOptions: {
      permissionMode: "bypassPermissions",
    },
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// SDK mock
// ---------------------------------------------------------------------------

let capturedQueryArgs: Record<string, unknown> = {};
let mockMessages: Array<Record<string, unknown>> = [];

const mockQuery = mock(async function* (args: Record<string, unknown>) {
  capturedQueryArgs = args;
  for (const msg of mockMessages) {
    yield msg;
  }
});

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: mockQuery,
}));

// Import after mocking
const { ClaudeEngine } = await import("../../src/engines/claude.ts");

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

beforeEach(() => {
  capturedQueryArgs = {};
  mockMessages = [];
  mockQuery.mockClear();
});

describe("ClaudeEngine — interface compliance", () => {
  test("implements EngineAdapter.run()", () => {
    const engine = new ClaudeEngine();
    expect(typeof engine.run).toBe("function");
  });
});

describe("ClaudeEngine — config transformation", () => {
  test("uses default model when none specified", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), makeCallbacks());

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.model).toBe("claude-sonnet-4-20250514");
  });

  test("uses specified model when provided", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(makeConfig({ model: "claude-opus-4" }), makeCallbacks());

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.model).toBe("claude-opus-4");
  });

  test("sets permissionMode from engineOptions", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        engineOptions: { permissionMode: "acceptEdits" },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.permissionMode).toBe("acceptEdits");
  });

  test("bypassPermissions sets allowDangerouslySkipPermissions", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        engineOptions: { permissionMode: "bypassPermissions" },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.allowDangerouslySkipPermissions).toBe(true);
  });

  test("cwd is passed as options.cwd", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(makeConfig({ cwd: "/my/project" }), makeCallbacks());

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.cwd).toBe("/my/project");
  });

  test("maxBudget is passed as maxBudgetUsd", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        engineOptions: { permissionMode: "bypassPermissions", maxBudget: 2.5 },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.maxBudgetUsd).toBe(2.5);
  });

  test("allowedTools is passed through", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        engineOptions: {
          permissionMode: "bypassPermissions",
          allowedTools: ["Bash", "Read"],
        },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.allowedTools).toEqual(["Bash", "Read"]);
  });

  test("system prompt is set with claude_code preset and append", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({ systemPrompt: "be concise" }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.systemPrompt).toEqual({
      type: "preset",
      preset: "claude_code",
      append: "be concise",
    });
  });

  test("no system prompt when not configured", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), makeCallbacks());

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.systemPrompt).toBeUndefined();
  });
});

// ---------------------------------------------------------------------------
// Effort → maxTurns mapping
// ---------------------------------------------------------------------------

describe("ClaudeEngine — effort to maxTurns mapping", () => {
  const effortTurns: Array<[EffortLevel, number]> = [
    ["low", 5],
    ["medium", 15],
    ["high", 30],
    ["xhigh", 50],
  ];

  for (const [effort, expectedTurns] of effortTurns) {
    test(`effort=${effort} → maxTurns=${expectedTurns}`, async () => {
      mockMessages = [
        {
          type: "result",
          subtype: "success",
          result: "done",
          session_id: "s1",
          total_cost_usd: 0,
          num_turns: 1,
        },
      ];

      const engine = new ClaudeEngine();
      await engine.run(
        makeConfig({
          effort,
          engineOptions: { permissionMode: "bypassPermissions" },
        }),
        makeCallbacks()
      );

      const options = capturedQueryArgs.options as Record<string, unknown>;
      expect(options.maxTurns).toBe(expectedTurns);
    });
  }

  test("explicit maxTurns in engineOptions overrides effort mapping", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        effort: "low",
        engineOptions: { permissionMode: "bypassPermissions", maxTurns: 99 },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    expect(options.maxTurns).toBe(99);
  });
});

// ---------------------------------------------------------------------------
// MCP servers
// ---------------------------------------------------------------------------

describe("ClaudeEngine — MCP server configuration", () => {
  test("MCP servers are passed with type:stdio", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        mcpServers: {
          myserver: { command: "npx", args: ["-y", "srv"] },
        },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    const servers = options.mcpServers as Record<string, Record<string, unknown>>;
    expect(servers.myserver.type).toBe("stdio");
    expect(servers.myserver.command).toBe("npx");
    expect(servers.myserver.args).toEqual(["-y", "srv"]);
  });

  test("MCP tools are added to allowedTools when restricted", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const engine = new ClaudeEngine();
    await engine.run(
      makeConfig({
        mcpServers: {
          exa: { command: "npx", args: [] },
        },
        engineOptions: {
          permissionMode: "bypassPermissions",
          allowedTools: ["Bash"],
        },
      }),
      makeCallbacks()
    );

    const options = capturedQueryArgs.options as Record<string, unknown>;
    const tools = options.allowedTools as string[];
    expect(tools).toContain("Bash");
    expect(tools).toContain("mcp__exa__*");
  });
});

// ---------------------------------------------------------------------------
// Message handling
// ---------------------------------------------------------------------------

describe("ClaudeEngine — message handling", () => {
  test("extracts result from success message", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "success",
        result: "All tests pass!",
        session_id: "session-123",
        total_cost_usd: 0.05,
        num_turns: 3,
      },
    ];

    const engine = new ClaudeEngine();
    const result = await engine.run(makeConfig(), makeCallbacks());

    expect(result.response).toBe("All tests pass!");
    expect(result.metadata.session_id).toBe("session-123");
    expect(result.metadata.cost_usd).toBe(0.05);
    expect(result.metadata.turns).toBe(3);
  });

  test("throws on error result subtype", async () => {
    mockMessages = [
      {
        type: "result",
        subtype: "error",
        errors: ["token limit exceeded"],
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 0,
      },
    ];

    const engine = new ClaudeEngine();
    await expect(engine.run(makeConfig(), makeCallbacks())).rejects.toThrow(
      /Claude agent error.*token limit exceeded/
    );
  });

  test("throws when no result message received", async () => {
    mockMessages = [
      { type: "system" },
    ];

    const engine = new ClaudeEngine();
    await expect(engine.run(makeConfig(), makeCallbacks())).rejects.toThrow(
      /No result message received/
    );
  });

  test("classifies Edit tool use as file_change", async () => {
    mockMessages = [
      {
        type: "assistant",
        message: {
          content: [
            {
              type: "tool_use",
              name: "Edit",
              input: { file_path: "/tmp/foo.ts" },
            },
          ],
        },
      },
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), callbacks);

    const fileItems = callbacks.items.filter((i) => i.type === "file_change");
    expect(fileItems).toHaveLength(1);
    expect(fileItems[0].summary).toBe("/tmp/foo.ts");
  });

  test("classifies Write tool use as file_change", async () => {
    mockMessages = [
      {
        type: "assistant",
        message: {
          content: [
            {
              type: "tool_use",
              name: "Write",
              input: { file_path: "/tmp/new.ts" },
            },
          ],
        },
      },
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), callbacks);

    const fileItems = callbacks.items.filter((i) => i.type === "file_change");
    expect(fileItems).toHaveLength(1);
    expect(fileItems[0].summary).toBe("/tmp/new.ts");
  });

  test("classifies Bash tool use as command", async () => {
    mockMessages = [
      {
        type: "assistant",
        message: {
          content: [
            {
              type: "tool_use",
              name: "Bash",
              input: { command: "npm test" },
            },
          ],
        },
      },
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), callbacks);

    const cmdItems = callbacks.items.filter((i) => i.type === "command");
    expect(cmdItems).toHaveLength(1);
    expect(cmdItems[0].summary).toBe("npm test");
  });

  test("classifies Read tool use as file_read", async () => {
    mockMessages = [
      {
        type: "assistant",
        message: {
          content: [
            {
              type: "tool_use",
              name: "Read",
              input: { file_path: "/tmp/readme.md" },
            },
          ],
        },
      },
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), callbacks);

    const readItems = callbacks.items.filter((i) => i.type === "file_read");
    expect(readItems).toHaveLength(1);
    expect(readItems[0].summary).toBe("/tmp/readme.md");
  });

  test("classifies mcp__ tools as mcp_call", async () => {
    mockMessages = [
      {
        type: "assistant",
        message: {
          content: [
            {
              type: "tool_use",
              name: "mcp__exa__search",
              input: { query: "test" },
            },
          ],
        },
      },
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), callbacks);

    const mcpItems = callbacks.items.filter((i) => i.type === "mcp_call");
    expect(mcpItems).toHaveLength(1);
    expect(mcpItems[0].summary).toBe("mcp__exa__search");
  });

  test("heartbeats fire on every message", async () => {
    mockMessages = [
      { type: "system" },
      { type: "assistant", message: { content: [] } },
      {
        type: "result",
        subtype: "success",
        result: "done",
        session_id: "s1",
        total_cost_usd: 0,
        num_turns: 1,
      },
    ];

    const callbacks = makeCallbacks();
    const engine = new ClaudeEngine();
    await engine.run(makeConfig(), callbacks);

    // "starting claude agent" + 3 messages = 4+
    expect(callbacks.heartbeats.length).toBeGreaterThanOrEqual(4);
    expect(callbacks.heartbeats[0]).toBe("starting claude agent");
  });
});
