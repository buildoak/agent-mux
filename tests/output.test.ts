/**
 * output.test.ts — Output contract shape validation
 *
 * Verifies that success, error, and timeout outputs conform to the
 * SuccessOutput / ErrorOutput types and are valid JSON.
 */

import { describe, test, expect } from "bun:test";
import type {
  SuccessOutput,
  ErrorOutput,
  Output,
  Activity,
  EngineName,
} from "../src/types.ts";

// ---------------------------------------------------------------------------
// Helpers — build canonical output shapes
// ---------------------------------------------------------------------------

function makeActivity(overrides: Partial<Activity> = {}): Activity {
  return {
    files_changed: [],
    commands_run: [],
    files_read: [],
    mcp_calls: [],
    heartbeat_count: 0,
    ...overrides,
  };
}

function makeSuccessOutput(overrides: Partial<SuccessOutput> = {}): SuccessOutput {
  return {
    success: true,
    engine: "codex" as EngineName,
    response: "Done.",
    timed_out: false,
    duration_ms: 1234,
    activity: makeActivity(),
    metadata: {},
    ...overrides,
  };
}

function makeErrorOutput(overrides: Partial<ErrorOutput> = {}): ErrorOutput {
  return {
    success: false,
    engine: "codex" as EngineName,
    error: "Something went wrong",
    code: "SDK_ERROR",
    duration_ms: 500,
    activity: makeActivity(),
    ...overrides,
  };
}

function makeTimeoutOutput(): SuccessOutput {
  return makeSuccessOutput({
    timed_out: true,
    response: "(timed out -- partial results may be available in activity log)",
  });
}

// ---------------------------------------------------------------------------
// Success output shape
// ---------------------------------------------------------------------------

describe("SuccessOutput shape", () => {
  test("has success: true", () => {
    const output = makeSuccessOutput();
    expect(output.success).toBe(true);
  });

  test("has response as string", () => {
    const output = makeSuccessOutput({ response: "Hello world" });
    expect(typeof output.response).toBe("string");
    expect(output.response).toBe("Hello world");
  });

  test("has activity object with expected keys", () => {
    const output = makeSuccessOutput({
      activity: makeActivity({
        files_changed: ["src/foo.ts"],
        commands_run: ["ls -la"],
        files_read: ["README.md"],
        mcp_calls: ["exa/search"],
        heartbeat_count: 5,
      }),
    });
    expect(output.activity.files_changed).toEqual(["src/foo.ts"]);
    expect(output.activity.commands_run).toEqual(["ls -la"]);
    expect(output.activity.files_read).toEqual(["README.md"]);
    expect(output.activity.mcp_calls).toEqual(["exa/search"]);
    expect(output.activity.heartbeat_count).toBe(5);
  });

  test("has engine name", () => {
    const output = makeSuccessOutput({ engine: "claude" });
    expect(output.engine).toBe("claude");
  });

  test("has timed_out: false for normal completion", () => {
    const output = makeSuccessOutput();
    expect(output.timed_out).toBe(false);
  });

  test("has duration_ms as number", () => {
    const output = makeSuccessOutput({ duration_ms: 42000 });
    expect(typeof output.duration_ms).toBe("number");
    expect(output.duration_ms).toBe(42000);
  });

  test("has metadata object", () => {
    const output = makeSuccessOutput({
      metadata: {
        session_id: "abc123",
        cost_usd: 0.05,
        tokens: { input: 1000, output: 500 },
        turns: 3,
        model: "gpt-5.3-codex",
      },
    });
    expect(output.metadata.session_id).toBe("abc123");
    expect(output.metadata.cost_usd).toBe(0.05);
    expect(output.metadata.tokens?.input).toBe(1000);
    expect(output.metadata.tokens?.output).toBe(500);
    expect(output.metadata.turns).toBe(3);
    expect(output.metadata.model).toBe("gpt-5.3-codex");
  });
});

// ---------------------------------------------------------------------------
// Error output shape
// ---------------------------------------------------------------------------

describe("ErrorOutput shape", () => {
  test("has success: false", () => {
    const output = makeErrorOutput();
    expect(output.success).toBe(false);
  });

  test("has error as string", () => {
    const output = makeErrorOutput({ error: "Missing API key" });
    expect(typeof output.error).toBe("string");
    expect(output.error).toBe("Missing API key");
  });

  test("has code as known error code", () => {
    const validCodes = ["INVALID_ARGS", "MISSING_API_KEY", "SDK_ERROR"] as const;
    for (const code of validCodes) {
      const output = makeErrorOutput({ code });
      expect(output.code).toBe(code);
    }
  });

  test("has engine name", () => {
    const output = makeErrorOutput({ engine: "opencode" });
    expect(output.engine).toBe("opencode");
  });

  test("has duration_ms as number", () => {
    const output = makeErrorOutput({ duration_ms: 0 });
    expect(typeof output.duration_ms).toBe("number");
  });

  test("has activity object", () => {
    const output = makeErrorOutput();
    expect(output.activity).toBeDefined();
    expect(Array.isArray(output.activity.files_changed)).toBe(true);
    expect(Array.isArray(output.activity.commands_run)).toBe(true);
    expect(Array.isArray(output.activity.files_read)).toBe(true);
    expect(Array.isArray(output.activity.mcp_calls)).toBe(true);
    expect(typeof output.activity.heartbeat_count).toBe("number");
  });
});

// ---------------------------------------------------------------------------
// Timeout output shape
// ---------------------------------------------------------------------------

describe("Timeout output shape", () => {
  test("has success: true (timeout is not a failure)", () => {
    const output = makeTimeoutOutput();
    expect(output.success).toBe(true);
  });

  test("has timed_out: true", () => {
    const output = makeTimeoutOutput();
    expect(output.timed_out).toBe(true);
  });

  test("has response (partial results message)", () => {
    const output = makeTimeoutOutput();
    expect(typeof output.response).toBe("string");
    expect(output.response.length).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// JSON roundtrip
// ---------------------------------------------------------------------------

describe("JSON roundtrip", () => {
  test("success output survives JSON.parse(JSON.stringify())", () => {
    const output = makeSuccessOutput({
      activity: makeActivity({
        files_changed: ["a.ts", "b.ts"],
        heartbeat_count: 3,
      }),
      metadata: {
        session_id: "s1",
        tokens: { input: 100, output: 200, reasoning: 50 },
      },
    });
    const json = JSON.stringify(output);
    const parsed = JSON.parse(json) as SuccessOutput;
    expect(parsed.success).toBe(true);
    expect(parsed.engine).toBe("codex");
    expect(parsed.response).toBe("Done.");
    expect(parsed.activity.files_changed).toEqual(["a.ts", "b.ts"]);
    expect(parsed.metadata.tokens?.reasoning).toBe(50);
  });

  test("error output survives JSON.parse(JSON.stringify())", () => {
    const output = makeErrorOutput({ code: "MISSING_API_KEY", error: "no key" });
    const json = JSON.stringify(output);
    const parsed = JSON.parse(json) as ErrorOutput;
    expect(parsed.success).toBe(false);
    expect(parsed.code).toBe("MISSING_API_KEY");
    expect(parsed.error).toBe("no key");
  });

  test("timeout output survives JSON.parse(JSON.stringify())", () => {
    const output = makeTimeoutOutput();
    const json = JSON.stringify(output);
    const parsed = JSON.parse(json) as SuccessOutput;
    expect(parsed.success).toBe(true);
    expect(parsed.timed_out).toBe(true);
  });

  test("Output union type can be discriminated by success field", () => {
    const success: Output = makeSuccessOutput();
    const error: Output = makeErrorOutput();

    // Discriminated union check
    if (success.success) {
      // TypeScript narrows to SuccessOutput
      expect(success.response).toBeDefined();
      expect(success.timed_out).toBeDefined();
    }
    if (!error.success) {
      // TypeScript narrows to ErrorOutput
      expect(error.error).toBeDefined();
      expect(error.code).toBeDefined();
    }
  });
});

// ---------------------------------------------------------------------------
// Activity shape
// ---------------------------------------------------------------------------

describe("Activity shape", () => {
  test("empty activity has all arrays empty and heartbeat_count 0", () => {
    const activity = makeActivity();
    expect(activity.files_changed).toEqual([]);
    expect(activity.commands_run).toEqual([]);
    expect(activity.files_read).toEqual([]);
    expect(activity.mcp_calls).toEqual([]);
    expect(activity.heartbeat_count).toBe(0);
  });

  test("activity arrays contain strings", () => {
    const activity = makeActivity({
      files_changed: ["src/a.ts"],
      commands_run: ["npm test"],
      files_read: ["README.md"],
      mcp_calls: ["exa/search"],
    });
    for (const arr of [
      activity.files_changed,
      activity.commands_run,
      activity.files_read,
      activity.mcp_calls,
    ]) {
      for (const item of arr) {
        expect(typeof item).toBe("string");
      }
    }
  });
});
