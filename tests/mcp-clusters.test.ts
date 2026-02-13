/**
 * mcp-clusters.test.ts — YAML config loading, cluster resolution
 *
 * Tests the MCP cluster resolution logic. We mock the file system
 * by writing temp YAML files and overriding process.cwd() so the
 * module picks them up from the "project-local" search path.
 *
 * Since mcp-clusters.ts uses a lazy singleton (_clusters), we need
 * to re-import the module fresh for each test to reset state. We do
 * this by dynamically importing with a cache-busting query param —
 * but Bun doesn't support that, so instead we test the YAML parsing
 * logic indirectly through the public API.
 *
 * Strategy: Create a temp dir with an mcp-clusters.yaml, change cwd
 * there, then test. We reset the singleton by importing a helper that
 * clears the cache.
 */

import { describe, test, expect, beforeAll, afterAll } from "bun:test";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";

/**
 * Since the mcp-clusters module uses a lazy singleton, we need to
 * test at the integration level — write a real YAML file, then
 * call the public functions.
 *
 * However, the singleton means we can only test one config per process.
 * We solve this by testing through parseCliArgs which calls resolveClusters
 * internally, OR by testing the YAML parsing and toOpenCodeMcp conversion
 * functions that don't rely on the singleton.
 */

// We can test toOpenCodeMcp directly since it's a pure function
import { toOpenCodeMcp } from "../src/mcp-clusters.ts";
import type { McpServerConfig } from "../src/mcp-clusters.ts";

// ---------------------------------------------------------------------------
// toOpenCodeMcp — pure function, no singleton dependency
// ---------------------------------------------------------------------------

describe("toOpenCodeMcp", () => {
  test("converts McpServerConfig to OpenCode format", () => {
    const input: Record<string, McpServerConfig> = {
      myserver: {
        command: "npx",
        args: ["-y", "my-server"],
      },
    };
    const result = toOpenCodeMcp(input);
    expect(result.myserver).toEqual({
      type: "local",
      command: ["npx", "-y", "my-server"],
    });
  });

  test("includes environment when env is set", () => {
    const input: Record<string, McpServerConfig> = {
      srv: {
        command: "node",
        args: ["server.js"],
        env: { API_KEY: "abc123" },
      },
    };
    const result = toOpenCodeMcp(input);
    expect(result.srv.environment).toEqual({ API_KEY: "abc123" });
  });

  test("omits environment when no env", () => {
    const input: Record<string, McpServerConfig> = {
      srv: {
        command: "node",
        args: [],
      },
    };
    const result = toOpenCodeMcp(input);
    expect(result.srv.environment).toBeUndefined();
  });

  test("handles multiple servers", () => {
    const input: Record<string, McpServerConfig> = {
      a: { command: "cmd-a", args: ["--flag"] },
      b: { command: "cmd-b", args: [] },
    };
    const result = toOpenCodeMcp(input);
    expect(Object.keys(result)).toEqual(["a", "b"]);
    expect(result.a.command).toEqual(["cmd-a", "--flag"]);
    expect(result.b.command).toEqual(["cmd-b"]);
  });

  test("handles empty input", () => {
    const result = toOpenCodeMcp({});
    expect(result).toEqual({});
  });
});

// ---------------------------------------------------------------------------
// YAML parsing integration — uses a real temp file
// ---------------------------------------------------------------------------

describe("YAML config loading (integration)", () => {
  let tempDir: string;
  let origCwd: string;

  beforeAll(() => {
    tempDir = mkdtempSync(join(tmpdir(), "agent-mux-test-"));
    origCwd = process.cwd();
  });

  afterAll(() => {
    process.chdir(origCwd);
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("resolveClusters loads from YAML and resolves named cluster", async () => {
    // Write a test YAML config
    const yamlContent = `
clusters:
  testcluster:
    description: "Test cluster"
    servers:
      test-server:
        command: echo
        args:
          - hello
  othercluster:
    description: "Other cluster"
    servers:
      other-server:
        command: cat
        args:
          - /dev/null
`;
    writeFileSync(join(tempDir, "mcp-clusters.yaml"), yamlContent);
    process.chdir(tempDir);

    // We need a fresh import to reset the singleton.
    // Use Bun's dynamic import with a random query string for cache busting.
    const mod = await reimportModule();

    const result = mod.resolveClusters(["testcluster"]);
    expect(result["test-server"]).toBeDefined();
    expect(result["test-server"].command).toBe("echo");
    expect(result["test-server"].args).toEqual(["hello"]);
    // other-server should NOT be included
    expect(result["other-server"]).toBeUndefined();
  });

  test("resolveClusters with 'all' returns all servers merged", async () => {
    // Relies on same YAML from previous test (same tempDir)
    const mod = await reimportModule();

    const result = mod.resolveClusters(["all"]);
    expect(result["test-server"]).toBeDefined();
    expect(result["other-server"]).toBeDefined();
  });

  test("resolveClusters with multiple clusters merges them", async () => {
    const mod = await reimportModule();

    const result = mod.resolveClusters(["testcluster", "othercluster"]);
    expect(result["test-server"]).toBeDefined();
    expect(result["other-server"]).toBeDefined();
  });

  test("resolveClusters throws for unknown cluster name", async () => {
    const mod = await reimportModule();

    expect(() => mod.resolveClusters(["nonexistent"])).toThrow(/Unknown MCP cluster/);
  });

  test("unknown cluster error lists available clusters", async () => {
    const mod = await reimportModule();

    try {
      mod.resolveClusters(["nonexistent"]);
      expect(true).toBe(false); // should not reach
    } catch (err) {
      const message = (err as Error).message;
      expect(message).toContain("testcluster");
      expect(message).toContain("othercluster");
      expect(message).toContain("all");
    }
  });

  test("getAllServerNames returns all servers across clusters", async () => {
    const mod = await reimportModule();

    const names = mod.getAllServerNames();
    expect(names).toContain("test-server");
    expect(names).toContain("other-server");
  });

  test("listClusters returns formatted list", async () => {
    const mod = await reimportModule();

    const list = mod.listClusters();
    expect(list).toContain("testcluster");
    expect(list).toContain("Test cluster");
    expect(list).toContain("othercluster");
    expect(list).toContain("all");
  });

  test("env vars in YAML are preserved", async () => {
    const yamlContent = `
clusters:
  envtest:
    description: "Env test"
    servers:
      env-server:
        command: node
        args:
          - server.js
        env:
          MY_SECRET: "from-yaml"
          PORT: "3000"
`;
    writeFileSync(join(tempDir, "mcp-clusters.yaml"), yamlContent);

    const mod = await reimportModule();

    const result = mod.resolveClusters(["envtest"]);
    expect(result["env-server"].env).toEqual({
      MY_SECRET: "from-yaml",
      PORT: "3000",
    });
  });
});

// ---------------------------------------------------------------------------
// Empty / missing config
// ---------------------------------------------------------------------------

describe("YAML config — empty/missing", () => {
  let tempDir: string;
  let origCwd: string;

  beforeAll(() => {
    tempDir = mkdtempSync(join(tmpdir(), "agent-mux-empty-"));
    origCwd = process.cwd();
  });

  afterAll(() => {
    process.chdir(origCwd);
    rmSync(tempDir, { recursive: true, force: true });
  });

  test("no config file returns empty clusters (no error)", async () => {
    // chdir to a dir with no mcp-clusters.yaml
    process.chdir(tempDir);
    const mod = await reimportModule();

    // resolveClusters with empty array should return empty
    const result = mod.resolveClusters([]);
    expect(result).toEqual({});
  });

  test("listClusters with no config shows none message", async () => {
    process.chdir(tempDir);
    const mod = await reimportModule();

    const list = mod.listClusters();
    expect(list).toContain("none");
  });
});

// ---------------------------------------------------------------------------
// Helper: re-import module to reset singleton
// ---------------------------------------------------------------------------

let importCounter = 0;

async function reimportModule() {
  importCounter++;
  // Bun supports cache-busting via query string on dynamic imports
  const mod = await import(`../src/mcp-clusters.ts?v=${importCounter}&t=${Date.now()}`) as typeof import("../src/mcp-clusters.ts");
  return mod;
}
