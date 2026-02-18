/**
 * core.test.ts — CLI argument parsing, version, help, effort-timeout mapping
 *
 * Tests parseCliArgs() by overriding process.argv before each call.
 */

import { describe, test, expect, beforeEach, afterEach } from "bun:test";
import { mkdtempSync, writeFileSync, mkdirSync, rmSync, symlinkSync } from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { parseCliArgs } from "../src/core.ts";
import { TIMEOUT_BY_EFFORT } from "../src/types.ts";

let originalArgv: string[];

beforeEach(() => {
  originalArgv = process.argv;
});

afterEach(() => {
  process.argv = originalArgv;
});

/** Helper: set process.argv as if the CLI was invoked with the given args */
function setArgs(...args: string[]): void {
  process.argv = ["bun", "agent.ts", ...args];
}

function createTempWorkspace(prefix = "agent-mux-core-"): string {
  return mkdtempSync(join(tmpdir(), prefix));
}

function withTempWorkspace<T>(run: (cwd: string) => T): T {
  const cwd = createTempWorkspace();
  try {
    return run(cwd);
  } finally {
    rmSync(cwd, { recursive: true, force: true });
  }
}

function setupClaudeDirs(cwd: string): void {
  mkdirSync(join(cwd, ".claude", "agents"), { recursive: true });
  mkdirSync(join(cwd, ".claude", "skills"), { recursive: true });
}

function writeCoordinator(cwd: string, name: string, content: string): void {
  const fileName = name.endsWith(".md") ? name : `${name}.md`;
  writeFileSync(join(cwd, ".claude", "agents", fileName), content);
}

function writeSkill(cwd: string, name: string, content = `# ${name}`): void {
  const skillDir = join(cwd, ".claude", "skills", name);
  mkdirSync(skillDir, { recursive: true });
  writeFileSync(join(skillDir, "SKILL.md"), content);
}

// ---------------------------------------------------------------------------
// Basic parsing
// ---------------------------------------------------------------------------

describe("parseCliArgs — basic parsing", () => {
  test("--engine codex with prompt returns ok", () => {
    setArgs("--engine", "codex", "do something");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engine).toBe("codex");
      expect(result.config.prompt).toBe("do something");
    }
  });

  test("--engine claude with prompt returns ok", () => {
    setArgs("--engine", "claude", "write tests");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engine).toBe("claude");
      expect(result.config.prompt).toBe("write tests");
    }
  });

  test("--engine opencode with prompt returns ok", () => {
    setArgs("--engine", "opencode", "refactor code");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engine).toBe("opencode");
      expect(result.config.prompt).toBe("refactor code");
    }
  });

  test("multiple positional words are joined as the prompt", () => {
    setArgs("--engine", "codex", "do", "something", "complex");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.prompt).toBe("do something complex");
    }
  });

  test("default effort is medium", () => {
    setArgs("--engine", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.effort).toBe("medium");
    }
  });

  test("default timeout matches medium effort", () => {
    setArgs("--engine", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.timeout).toBe(TIMEOUT_BY_EFFORT.medium);
    }
  });

  test("cwd defaults to process.cwd()", () => {
    setArgs("--engine", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.cwd).toBe(process.cwd());
    }
  });
});

// ---------------------------------------------------------------------------
// All flags
// ---------------------------------------------------------------------------

describe("parseCliArgs — common flags", () => {
  test("--effort low sets effort and timeout", () => {
    setArgs("--engine", "codex", "--effort", "low", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.effort).toBe("low");
      expect(result.config.timeout).toBe(TIMEOUT_BY_EFFORT.low);
    }
  });

  test("--effort high sets effort and timeout", () => {
    setArgs("--engine", "codex", "--effort", "high", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.effort).toBe("high");
      expect(result.config.timeout).toBe(TIMEOUT_BY_EFFORT.high);
    }
  });

  test("--effort xhigh sets effort and timeout", () => {
    setArgs("--engine", "codex", "--effort", "xhigh", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.effort).toBe("xhigh");
      expect(result.config.timeout).toBe(TIMEOUT_BY_EFFORT.xhigh);
    }
  });

  test("--timeout overrides effort-based timeout", () => {
    setArgs("--engine", "codex", "--effort", "low", "--timeout", "999999", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.timeout).toBe(999999);
      // effort still set even when timeout is overridden
      expect(result.config.effort).toBe("low");
    }
  });

  test("--cwd sets working directory", () => {
    setArgs("--engine", "codex", "--cwd", "/tmp/test", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.cwd).toBe("/tmp/test");
    }
  });

  test("--model sets model", () => {
    setArgs("--engine", "codex", "--model", "gpt-4o", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.model).toBe("gpt-4o");
    }
  });

  test("--system-prompt sets systemPrompt", () => {
    setArgs("--engine", "codex", "--system-prompt", "be concise", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.systemPrompt).toBe("be concise");
    }
  });

  test("--browser adds browser to mcpClusters", () => {
    setArgs("--engine", "codex", "--browser", "test");
    const result = parseCliArgs();
    // This may fail if no mcp-clusters.yaml exists, but the flag parsing
    // itself should work — the error would be from resolveClusters.
    // We test flag detection, not resolution here.
    // If no config file, resolveClusters throws. That's fine — we test that
    // the flag is detected by checking for the error message mentioning "browser".
    if (result.kind === "ok") {
      expect(result.config.mcpClusters).toContain("browser");
    } else if (result.kind === "invalid") {
      // Unknown cluster error — still proves --browser was parsed into mcpClusters
      expect(result.error).toContain("browser");
    }
  });
});

// ---------------------------------------------------------------------------
// Short flags
// ---------------------------------------------------------------------------

describe("parseCliArgs — short flags", () => {
  test("-E codex works as --engine", () => {
    setArgs("-E", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engine).toBe("codex");
    }
  });

  test("-e high works as --effort", () => {
    setArgs("--engine", "codex", "-e", "high", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.effort).toBe("high");
    }
  });

  test("-C /tmp works as --cwd", () => {
    setArgs("--engine", "codex", "-C", "/tmp", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.cwd).toBe("/tmp");
    }
  });

  test("-m gpt-4o works as --model", () => {
    setArgs("--engine", "codex", "-m", "gpt-4o", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.model).toBe("gpt-4o");
    }
  });

  test("-t 5000 works as --timeout", () => {
    setArgs("--engine", "codex", "-t", "5000", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.timeout).toBe(5000);
    }
  });

  test("-s 'be brief' works as --system-prompt", () => {
    setArgs("--engine", "codex", "-s", "be brief", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.systemPrompt).toBe("be brief");
    }
  });
});

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

describe("parseCliArgs — error cases", () => {
  test("missing --engine returns invalid", () => {
    setArgs("do something");
    const result = parseCliArgs();
    expect(result.kind).toBe("invalid");
    if (result.kind === "invalid") {
      expect(result.error).toContain("--engine is required");
    }
  });

  test("invalid engine name returns invalid", () => {
    setArgs("--engine", "gpt", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("invalid");
    if (result.kind === "invalid") {
      expect(result.error).toContain("Invalid engine: gpt");
    }
  });

  test("missing prompt returns invalid", () => {
    setArgs("--engine", "codex");
    const result = parseCliArgs();
    expect(result.kind).toBe("invalid");
    if (result.kind === "invalid") {
      expect(result.error).toContain("prompt is required");
    }
  });

  test("invalid effort level returns invalid", () => {
    setArgs("--engine", "codex", "--effort", "mega", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("invalid");
    if (result.kind === "invalid") {
      expect(result.error).toContain("Invalid effort");
    }
  });

  test("non-numeric timeout returns invalid", () => {
    setArgs("--engine", "codex", "--timeout", "abc", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("invalid");
    if (result.kind === "invalid") {
      expect(result.error).toContain("--timeout must be a positive integer");
    }
  });

  test("negative timeout returns invalid (caught by parseArgs or validation)", () => {
    // node:util parseArgs intercepts -100 as ambiguous flag usage,
    // so this returns invalid with the parseArgs error message
    setArgs("--engine", "codex", "--timeout", "-100", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("invalid");
  });
});

// ---------------------------------------------------------------------------
// --help and --version
// ---------------------------------------------------------------------------

describe("parseCliArgs — help and version", () => {
  test("--help returns help kind", () => {
    setArgs("--help");
    const result = parseCliArgs();
    expect(result.kind).toBe("help");
  });

  test("--help with engine returns help kind with engine", () => {
    setArgs("--engine", "codex", "--help");
    const result = parseCliArgs();
    expect(result.kind).toBe("help");
    if (result.kind === "help") {
      expect(result.engine).toBe("codex");
    }
  });

  test("-h returns help kind", () => {
    setArgs("-h");
    const result = parseCliArgs();
    expect(result.kind).toBe("help");
  });

  test("--version returns version kind", () => {
    setArgs("--version");
    const result = parseCliArgs();
    expect(result.kind).toBe("version");
  });

  test("-V returns version kind", () => {
    setArgs("-V");
    const result = parseCliArgs();
    expect(result.kind).toBe("version");
  });

  test("--version takes precedence over --help", () => {
    setArgs("--version", "--help");
    const result = parseCliArgs();
    expect(result.kind).toBe("version");
  });
});

// ---------------------------------------------------------------------------
// Engine-specific options
// ---------------------------------------------------------------------------

describe("parseCliArgs — codex-specific options", () => {
  test("--sandbox workspace-write sets engineOptions.sandbox", () => {
    setArgs("--engine", "codex", "--sandbox", "workspace-write", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.sandbox).toBe("workspace-write");
    }
  });

  test("--reasoning high sets engineOptions.reasoning", () => {
    setArgs("--engine", "codex", "--reasoning", "high", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.reasoning).toBe("high");
    }
  });

  test("-r xhigh works as --reasoning shorthand", () => {
    setArgs("--engine", "codex", "-r", "xhigh", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.reasoning).toBe("xhigh");
    }
  });

  test("--network enables network access", () => {
    setArgs("--engine", "codex", "--network", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.network).toBe(true);
    }
  });

  test("default sandbox is read-only for codex", () => {
    setArgs("--engine", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.sandbox).toBe("read-only");
    }
  });

  test("default reasoning is medium for codex", () => {
    setArgs("--engine", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.reasoning).toBe("medium");
    }
  });

  test("codex options are NOT set for claude engine", () => {
    setArgs("--engine", "claude", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.sandbox).toBeUndefined();
      expect(result.config.engineOptions.reasoning).toBeUndefined();
    }
  });
});

describe("parseCliArgs — claude-specific options", () => {
  test("--permission-mode acceptEdits sets engineOptions", () => {
    setArgs("--engine", "claude", "--permission-mode", "acceptEdits", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.permissionMode).toBe("acceptEdits");
    }
  });

  test("default permissionMode is bypassPermissions", () => {
    setArgs("--engine", "claude", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.permissionMode).toBe("bypassPermissions");
    }
  });

  test("--max-turns sets maxTurns", () => {
    setArgs("--engine", "claude", "--max-turns", "25", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.maxTurns).toBe(25);
    }
  });

  test("--max-budget sets maxBudget", () => {
    setArgs("--engine", "claude", "--max-budget", "1.5", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.maxBudget).toBe(1.5);
    }
  });

  test("--allowed-tools parses comma-separated list", () => {
    setArgs("--engine", "claude", "--allowed-tools", "Bash,Read,Write", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.allowedTools).toEqual(["Bash", "Read", "Write"]);
    }
  });

  test("claude options are NOT set for codex engine", () => {
    setArgs("--engine", "codex", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.permissionMode).toBeUndefined();
      expect(result.config.engineOptions.maxTurns).toBeUndefined();
    }
  });
});

describe("parseCliArgs — opencode-specific options", () => {
  test("--variant sets engineOptions.variant", () => {
    setArgs("--engine", "opencode", "--variant", "high", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.variant).toBe("high");
    }
  });

  test("--agent sets engineOptions.agent", () => {
    setArgs("--engine", "opencode", "--agent", "coder", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.agent).toBe("coder");
    }
  });

  test("opencode options are NOT set for claude engine", () => {
    setArgs("--engine", "claude", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.variant).toBeUndefined();
      expect(result.config.engineOptions.agent).toBeUndefined();
    }
  });
});

// ---------------------------------------------------------------------------
// --full mode
// ---------------------------------------------------------------------------

describe("parseCliArgs — full mode", () => {
  test("--full with codex sets danger-full-access sandbox and network", () => {
    setArgs("--engine", "codex", "--full", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.sandbox).toBe("danger-full-access");
      expect(result.config.engineOptions.network).toBe(true);
    }
  });

  test("--full with claude sets bypassPermissions and full flag", () => {
    setArgs("--engine", "claude", "--full", "test");
    const result = parseCliArgs();
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") {
      expect(result.config.engineOptions.permissionMode).toBe("bypassPermissions");
      expect(result.config.engineOptions.full).toBe(true);
    }
  });
});

// ---------------------------------------------------------------------------
// --system-prompt-file
// ---------------------------------------------------------------------------

describe("parseCliArgs -- system-prompt-file", () => {
  test("loads file content as system prompt", () => {
    withTempWorkspace((cwd) => {
      writeFileSync(join(cwd, "prompt.txt"), "from file");
      setArgs("--engine", "codex", "--cwd", cwd, "--system-prompt-file", "prompt.txt", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBe("from file");
      }
    });
  });

  test("missing file returns invalid", () => {
    withTempWorkspace((cwd) => {
      setArgs("--engine", "codex", "--cwd", cwd, "--system-prompt-file", "missing.txt", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("System prompt file not found");
      }
    });
  });

  test("path pointing to directory returns invalid", () => {
    withTempWorkspace((cwd) => {
      mkdirSync(join(cwd, "prompt-dir"), { recursive: true });
      setArgs("--engine", "codex", "--cwd", cwd, "--system-prompt-file", "prompt-dir", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("not a file");
      }
    });
  });

  test("file + inline concatenated (file first)", () => {
    withTempWorkspace((cwd) => {
      writeFileSync(join(cwd, "prompt.txt"), "from file");
      setArgs(
        "--engine",
        "codex",
        "--cwd",
        cwd,
        "--system-prompt-file",
        "prompt.txt",
        "--system-prompt",
        "from inline",
        "test",
      );
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBe("from file\n\nfrom inline");
      }
    });
  });

  test("empty file with inline still yields inline", () => {
    withTempWorkspace((cwd) => {
      writeFileSync(join(cwd, "prompt.txt"), "");
      setArgs(
        "--engine",
        "codex",
        "--cwd",
        cwd,
        "--system-prompt-file",
        "prompt.txt",
        "--system-prompt",
        "from inline",
        "test",
      );
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBe("from inline");
      }
    });
  });
});

// ---------------------------------------------------------------------------
// --coordinator
// ---------------------------------------------------------------------------

describe("parseCliArgs -- coordinator", () => {
  test("loads coordinator spec from <cwd>/.claude/agents/<name>.md", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "coordinator body");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBe("coordinator body");
      }
    });
  });

  test("body becomes system prompt", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nmodel: haiku\n---\nbody prompt");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBe("body prompt");
      }
    });
  });

  test("malformed frontmatter (missing closing marker) returns invalid", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nskills:\n  - from-coordinator\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("missing closing --- marker");
      }
    });
  });

  test("frontmatter with no body leaves systemPrompt undefined", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nmodel: claude-3-7-sonnet\n---\n");
      setArgs("--engine", "claude", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBeUndefined();
      }
    });
  });

  test("frontmatter skills are resolved", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeSkill(cwd, "from-coordinator", "coordinator skill content");
      writeCoordinator(cwd, "planner", "---\nskills:\n  - from-coordinator\n---\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.skills.map((s) => s.name)).toEqual(["from-coordinator"]);
        expect(result.config.skills[0]?.content).toContain("coordinator skill content");
      }
    });
  });

  test("frontmatter model applies when --model absent", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nmodel: claude-3-7-sonnet\n---\n");
      setArgs("--engine", "claude", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.model).toBe("claude-3-7-sonnet");
      }
    });
  });

  test("frontmatter skills non-array returns invalid", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nskills: not-an-array\n---\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("frontmatter.skills must be a string array");
      }
    });
  });

  test("frontmatter model non-string returns invalid", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nmodel: 123\n---\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("frontmatter.model must be a string");
      }
    });
  });

  test("CLI --model overrides coordinator model", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nmodel: claude-3-7-sonnet\n---\n");
      setArgs(
        "--engine",
        "claude",
        "--cwd",
        cwd,
        "--coordinator",
        "planner",
        "--model",
        "claude-4-opus",
        "test",
      );
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.model).toBe("claude-4-opus");
      }
    });
  });

  test("missing coordinator file returns invalid", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "missing", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("Coordinator 'missing' not found");
      }
    });
  });

  test("coordinator + prompt-file + inline prompt order is correct", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "from coordinator");
      writeFileSync(join(cwd, "prompt.txt"), "from file");
      setArgs(
        "--engine",
        "codex",
        "--cwd",
        cwd,
        "--coordinator",
        "planner",
        "--system-prompt-file",
        "prompt.txt",
        "--system-prompt",
        "from inline",
        "test",
      );
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.systemPrompt).toBe("from coordinator\n\nfrom file\n\nfrom inline");
      }
    });
  });

  test("coordinator skills are loaded before CLI --skill entries", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeSkill(cwd, "from-coordinator", "coordinator skill");
      writeSkill(cwd, "from-cli", "cli skill");
      writeCoordinator(cwd, "planner", "---\nskills:\n  - from-coordinator\n---\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "--skill", "from-cli", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.skills.map((s) => s.name)).toEqual(["from-coordinator", "from-cli"]);
      }
    });
  });

  test("coordinator allowedTools reach engineOptions for Claude", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nallowedTools: Bash,Read\n---\n");
      setArgs("--engine", "claude", "--cwd", cwd, "--coordinator", "planner", "--allowed-tools", "Read,Write", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.engineOptions.allowedTools).toEqual(["Bash", "Read", "Write"]);
      }
    });
  });

  test("non-Claude engine with coordinator allowedTools doesn't throw", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nallowedTools:\n  - Bash\n---\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.engineOptions.allowedTools).toBeUndefined();
      }
    });
  });

  test("path traversal in coordinator name returns invalid", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "../evil", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("path traversal detected");
      }
    });
  });

  test("frontmatter closing marker at EOF without trailing newline is supported", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeCoordinator(cwd, "planner", "---\nmodel: claude-3-7-sonnet\n---");
      setArgs("--engine", "claude", "--cwd", cwd, "--coordinator", "planner", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.model).toBe("claude-3-7-sonnet");
        expect(result.config.systemPrompt).toBeUndefined();
      }
    });
  });
});

// ---------------------------------------------------------------------------
// Effort → timeout mapping constants
// ---------------------------------------------------------------------------

describe("TIMEOUT_BY_EFFORT", () => {
  test("low = 120 seconds (2 min)", () => {
    expect(TIMEOUT_BY_EFFORT.low).toBe(120_000);
  });

  test("medium = 600 seconds (10 min)", () => {
    expect(TIMEOUT_BY_EFFORT.medium).toBe(600_000);
  });

  test("high = 1200 seconds (20 min)", () => {
    expect(TIMEOUT_BY_EFFORT.high).toBe(1_200_000);
  });

  test("xhigh = 2400 seconds (40 min)", () => {
    expect(TIMEOUT_BY_EFFORT.xhigh).toBe(2_400_000);
  });
});

// ---------------------------------------------------------------------------
// Symlink handling — symlinks to external repos are legitimate
// ---------------------------------------------------------------------------

describe("parseCliArgs — symlink to external paths (legitimate pattern)", () => {
  test("coordinator symlink pointing outside agents root resolves successfully", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      // Create a coordinator file outside the agents root (simulates symlink to source repo)
      const outsideFile = join(cwd, "external-coordinator.md");
      writeFileSync(outsideFile, "---\nname: external\n---\nExternal coordinator body");
      // Create a symlink inside agents root pointing outside
      const symlinkPath = join(cwd, ".claude", "agents", "escape.md");
      symlinkSync(outsideFile, symlinkPath);
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "escape", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
    });
  });

  test("skill symlink pointing outside skills root resolves successfully", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      // Create a skill directory outside the skills root (simulates symlink to source repo)
      const outsideDir = join(cwd, "external-skill");
      mkdirSync(outsideDir, { recursive: true });
      writeFileSync(join(outsideDir, "SKILL.md"), "# External skill");
      // Create a symlink inside skills root pointing outside
      const symlinkPath = join(cwd, ".claude", "skills", "escape");
      symlinkSync(outsideDir, symlinkPath);
      setArgs("--engine", "codex", "--cwd", cwd, "--skill", "escape", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
    });
  });
});

// ---------------------------------------------------------------------------
// Skill deduplication
// ---------------------------------------------------------------------------

describe("parseCliArgs — skill deduplication", () => {
  test("coordinator + CLI both specify same skill, loaded only once", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeSkill(cwd, "shared-skill", "shared content");
      writeCoordinator(cwd, "planner", "---\nskills:\n  - shared-skill\n---\n");
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "planner", "--skill", "shared-skill", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.skills.map((s) => s.name)).toEqual(["shared-skill"]);
        expect(result.config.skills).toHaveLength(1);
      }
    });
  });

  test("multiple CLI --skill with same name, loaded only once", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      writeSkill(cwd, "dup-skill", "dup content");
      setArgs("--engine", "codex", "--cwd", cwd, "--skill", "dup-skill", "--skill", "dup-skill", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("ok");
      if (result.kind === "ok") {
        expect(result.config.skills).toHaveLength(1);
        expect(result.config.skills[0]?.name).toBe("dup-skill");
      }
    });
  });
});

// ---------------------------------------------------------------------------
// Coordinator isFile check
// ---------------------------------------------------------------------------

describe("parseCliArgs — coordinator isFile check", () => {
  test("coordinator pointing to a directory returns invalid", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      // Create a directory where the coordinator file would be
      mkdirSync(join(cwd, ".claude", "agents", "dir-coord.md"), { recursive: true });
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "dir-coord", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("is not a file");
      }
    });
  });
});

// ---------------------------------------------------------------------------
// Platform-safe containment (isContainedIn via path traversal)
// ---------------------------------------------------------------------------

describe("parseCliArgs — platform-safe containment", () => {
  test("coordinator with path separator in name is rejected", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", "../../etc/passwd", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("path traversal detected");
      }
    });
  });

  test("skill with .. traversal is rejected", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      setArgs("--engine", "codex", "--cwd", cwd, "--skill", "../../../etc", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
      if (result.kind === "invalid") {
        expect(result.error).toContain("path traversal detected");
      }
    });
  });

  test("coordinator resolving to agents root itself is rejected", () => {
    withTempWorkspace((cwd) => {
      setupClaudeDirs(cwd);
      // "." resolves to the agentsRoot itself — isContainedIn rejects rel === ""
      setArgs("--engine", "codex", "--cwd", cwd, "--coordinator", ".", "test");
      const result = parseCliArgs();
      expect(result.kind).toBe("invalid");
    });
  });
});
