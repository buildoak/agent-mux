/**
 * core.ts — Shared execution core for agent-mux
 *
 * Handles everything that isn't SDK-specific:
 * - CLI argument parsing
 * - AbortController lifecycle
 * - Heartbeat protocol (stderr)
 * - Activity collection
 * - Timeout enforcement
 * - JSON output (stdout)
 */

import { parseArgs } from "node:util";
import { resolveClusters, listClusters } from "./mcp-clusters.ts";
import type { McpServerConfig } from "./mcp-clusters.ts";
import type {
  EffortLevel,
  EngineName,
  EngineAdapter,
  EngineCallbacks,
  ActivityItem,
  Activity,
  Output,
} from "./types.ts";
import { TIMEOUT_BY_EFFORT as timeoutByEffort } from "./types.ts";

// --- Constants ---

const HEARTBEAT_INTERVAL_MS = 15_000;
const VALID_ENGINES: EngineName[] = ["codex", "claude", "opencode"];
const VALID_EFFORTS: EffortLevel[] = ["low", "medium", "high", "xhigh"];

// --- Parsed Config ---

export interface ParsedConfig {
  engine: EngineName;
  prompt: string;
  cwd: string;
  model?: string;
  effort: EffortLevel;
  timeout: number;
  systemPrompt?: string;
  mcpClusters: string[];
  mcpServers: Record<string, McpServerConfig>;
  engineOptions: Record<string, unknown>;
}

// --- Help Text ---

function buildHelpText(engine?: EngineName): string {
  const base = `Usage: agent-mux --engine <engine> [options] "prompt"

Engines: codex, claude, opencode

Common Options:
  -E, --engine <name>        Engine: codex, claude, opencode (required)
  -C, --cwd <dir>            Working directory (default: current dir)
  -m, --model <name>         Model string (engine-specific)
  -e, --effort <level>       Effort: low, medium (default), high, xhigh
  -t, --timeout <ms>         Timeout in ms (default: effort-scaled)
  -s, --system-prompt <text> System prompt (appended)
      --mcp-cluster <name>   Enable MCP cluster (repeatable)
  -b, --browser              Sugar for --mcp-cluster browser
  -f, --full                 Full access mode
  -h, --help                 Show this help

MCP Clusters:
${listClusters()}`;

  const codexOpts = `

Codex Options:
      --sandbox <mode>       read-only (default), workspace-write, danger-full-access
  -r, --reasoning <level>    Codex reasoning: minimal, low, medium, high, xhigh
  -n, --network              Enable network access
  -d, --add-dir <path>       Additional writable directory (repeatable)`;

  const claudeOpts = `

Claude Options:
  -p, --permission-mode <m>  default, acceptEdits, bypassPermissions (default), plan
      --max-turns <n>        Max conversation turns
      --max-budget <usd>     Max budget in USD
      --allowed-tools <list> Comma-separated tool whitelist`;

  const opencodeOpts = `

OpenCode Options:
      --variant <level>      Model variant / reasoning effort
      --agent <name>         OpenCode agent name

OpenCode Model Presets:
  kimi, kimi-k2.5, glm, glm-5, deepseek, deepseek-r1, qwen, qwen-coder, free`;

  if (engine === "codex") return base + codexOpts;
  if (engine === "claude") return base + claudeOpts;
  if (engine === "opencode") return base + opencodeOpts;
  return base + codexOpts + claudeOpts + opencodeOpts;
}

// --- Argument Parsing ---

type ParseResult =
  | { kind: "ok"; config: ParsedConfig }
  | { kind: "help"; engine?: EngineName }
  | { kind: "invalid"; error: string; engine?: EngineName };

export function parseCliArgs(): ParseResult {
  try {
    const { values, positionals } = parseArgs({
      allowPositionals: true,
      options: {
        // Common
        engine: { type: "string", short: "E" },
        cwd: { type: "string", short: "C" },
        model: { type: "string", short: "m" },
        effort: { type: "string", short: "e" },
        timeout: { type: "string", short: "t" },
        "system-prompt": { type: "string", short: "s" },
        "mcp-cluster": { type: "string", multiple: true },
        browser: { type: "boolean", short: "b" },
        full: { type: "boolean", short: "f" },
        help: { type: "boolean", short: "h" },
        // Codex-specific
        sandbox: { type: "string" },
        reasoning: { type: "string", short: "r" },
        network: { type: "boolean", short: "n" },
        "add-dir": { type: "string", short: "d", multiple: true },
        // Claude-specific
        "permission-mode": { type: "string", short: "p" },
        "max-turns": { type: "string" },
        "max-budget": { type: "string" },
        "allowed-tools": { type: "string" },
        // OpenCode-specific
        variant: { type: "string" },
        agent: { type: "string" },
      },
    });

    const engineStr = values.engine as string | undefined;

    if (values.help) {
      const engine = engineStr && VALID_ENGINES.includes(engineStr as EngineName)
        ? (engineStr as EngineName)
        : undefined;
      return { kind: "help", engine };
    }

    // Engine is required
    if (!engineStr) {
      return { kind: "invalid", error: "--engine is required. Use: codex, claude, opencode" };
    }
    if (!VALID_ENGINES.includes(engineStr as EngineName)) {
      return { kind: "invalid", error: `Invalid engine: ${engineStr}. Use: codex, claude, opencode` };
    }
    const engine = engineStr as EngineName;

    // Prompt is required
    const prompt = positionals.join(" ").trim();
    if (!prompt) {
      return { kind: "invalid", error: "A prompt is required.", engine };
    }

    // Effort
    const effort = (values.effort as EffortLevel) || "medium";
    if (!VALID_EFFORTS.includes(effort)) {
      return { kind: "invalid", error: `Invalid effort: ${effort}. Use: ${VALID_EFFORTS.join(", ")}`, engine };
    }

    // Timeout
    let timeout = timeoutByEffort[effort];
    if (values.timeout !== undefined) {
      const t = values.timeout.trim();
      if (!/^\d+$/.test(t)) {
        return { kind: "invalid", error: "--timeout must be a positive integer in milliseconds." };
      }
      const parsed = parseInt(t, 10);
      if (!Number.isFinite(parsed) || parsed <= 0) {
        return { kind: "invalid", error: "--timeout must be a positive integer in milliseconds." };
      }
      timeout = parsed;
    }

    // MCP clusters
    const mcpClusters: string[] = (values["mcp-cluster"] as string[] | undefined) ?? [];
    if (values.browser === true && !mcpClusters.includes("browser")) {
      mcpClusters.push("browser");
    }

    // Resolve MCP servers
    let mcpServers: Record<string, McpServerConfig> = {};
    if (mcpClusters.length > 0) {
      mcpServers = resolveClusters(mcpClusters);
    }

    // Engine-specific options bag
    const engineOptions: Record<string, unknown> = {};
    const fullMode = values.full === true;

    // Codex options
    if (engine === "codex") {
      const sandbox = fullMode
        ? "danger-full-access"
        : (values.sandbox as string) || "read-only";
      engineOptions.sandbox = sandbox;
      engineOptions.reasoning = (values.reasoning as string) || "medium";
      engineOptions.network = fullMode || values.network === true;
      engineOptions.addDirs = (values["add-dir"] as string[] | undefined) ?? [];
    }

    // Claude options
    if (engine === "claude") {
      engineOptions.permissionMode = fullMode
        ? "bypassPermissions"
        : (values["permission-mode"] as string) || "bypassPermissions";
      if (values["max-turns"] !== undefined) {
        const parsed = parseInt(values["max-turns"], 10);
        if (Number.isFinite(parsed) && parsed > 0) {
          engineOptions.maxTurns = parsed;
        }
      }
      if (values["max-budget"] !== undefined) {
        const parsed = parseFloat(values["max-budget"]);
        if (Number.isFinite(parsed) && parsed > 0) {
          engineOptions.maxBudget = parsed;
        }
      }
      if (values["allowed-tools"]) {
        engineOptions.allowedTools = (values["allowed-tools"] as string)
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean);
      }
      engineOptions.full = fullMode;
    }

    // OpenCode options
    if (engine === "opencode") {
      if (values.variant) engineOptions.variant = values.variant;
      if (values.agent) engineOptions.agent = values.agent;
    }

    return {
      kind: "ok",
      config: {
        engine,
        prompt,
        cwd: values.cwd || process.cwd(),
        model: values.model || undefined,
        effort,
        timeout,
        systemPrompt: values["system-prompt"] || undefined,
        mcpClusters,
        mcpServers,
        engineOptions,
      },
    };
  } catch (err) {
    return {
      kind: "invalid",
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

// --- Heartbeat Protocol ---

class HeartbeatManager {
  private intervalId: ReturnType<typeof setInterval> | null = null;
  private startTime: number;
  private lastActivity = "initializing";
  private heartbeatCount = 0;
  private _stderrWrite: typeof process.stderr.write;

  constructor() {
    this.startTime = Date.now();
    this._stderrWrite = process.stderr.write.bind(process.stderr);
  }

  start(): void {
    this.intervalId = setInterval(() => {
      this.heartbeatCount++;
      const elapsed = Math.round((Date.now() - this.startTime) / 1000);
      this._stderrWrite(
        `[heartbeat] ${elapsed}s — ${this.lastActivity}\n`
      );
    }, HEARTBEAT_INTERVAL_MS);
  }

  updateActivity(activity: string): void {
    this.lastActivity = activity;
  }

  stop(): void {
    if (this.intervalId) {
      clearInterval(this.intervalId);
      this.intervalId = null;
    }
  }

  getCount(): number {
    return this.heartbeatCount;
  }
}

// --- Activity Collector ---

class ActivityCollector {
  private items: ActivityItem[] = [];
  private filesChanged = new Set<string>();
  private commandsRun: string[] = [];
  private filesRead = new Set<string>();
  private mcpCalls: string[] = [];

  addItem(item: ActivityItem): void {
    this.items.push(item);
    switch (item.type) {
      case "file_change":
        this.filesChanged.add(item.summary);
        break;
      case "command":
        this.commandsRun.push(item.summary);
        break;
      case "file_read":
        this.filesRead.add(item.summary);
        break;
      case "mcp_call":
        this.mcpCalls.push(item.summary);
        break;
    }
  }

  getActivity(heartbeatCount: number): Activity {
    return {
      files_changed: [...this.filesChanged],
      commands_run: this.commandsRun,
      files_read: [...this.filesRead],
      mcp_calls: this.mcpCalls,
      heartbeat_count: heartbeatCount,
    };
  }

  getItems(): ActivityItem[] {
    return this.items;
  }
}

// --- Output ---

function writeOutput(result: Output): never {
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.success ? 0 : 1);
}

// --- Main Execution ---

export async function execute(
  config: ParsedConfig,
  adapter: EngineAdapter
): Promise<never> {
  const startTime = Date.now();
  const heartbeat = new HeartbeatManager();
  const collector = new ActivityCollector();

  // Suppress SDK stderr noise — but keep heartbeats flowing
  const originalStderrWrite = process.stderr.write.bind(process.stderr);
  const stderrFilter = function (this: typeof process.stderr, chunk: unknown, ...rest: unknown[]): boolean {
    const str = typeof chunk === "string" ? chunk : String(chunk);
    if (str.startsWith("[heartbeat]")) {
      return originalStderrWrite(chunk as string, ...(rest as []));
    }
    return true; // swallow SDK noise
  };
  process.stderr.write = stderrFilter as typeof process.stderr.write;

  // AbortController for timeout
  const abortController = new AbortController();
  let didTimeout = false;

  // Prepend time budget to prompt
  const timeAwarePrompt = `You have a time budget of ${config.timeout / 1000} seconds. Prioritize delivering complete output over exploration.\n\n${config.prompt}`;

  const callbacks: EngineCallbacks = {
    onHeartbeat(activity: string) {
      heartbeat.updateActivity(activity);
    },
    onItem(item: ActivityItem) {
      collector.addItem(item);
    },
  };

  const runConfig = {
    prompt: timeAwarePrompt,
    cwd: config.cwd,
    timeout: config.timeout,
    signal: abortController.signal,
    model: config.model || "",
    effort: config.effort,
    mcpServers: config.mcpServers,
    systemPrompt: config.systemPrompt,
    engineOptions: config.engineOptions,
  };

  // Start heartbeat
  heartbeat.start();

  // Set timeout
  const timeoutId = setTimeout(() => {
    didTimeout = true;
    abortController.abort();
  }, config.timeout);

  try {
    const result = await adapter.run(runConfig, callbacks);

    clearTimeout(timeoutId);
    heartbeat.stop();
    process.stderr.write = originalStderrWrite as typeof process.stderr.write;

    const output: Output = {
      success: true,
      engine: config.engine,
      response: result.response,
      timed_out: didTimeout,
      duration_ms: Date.now() - startTime,
      activity: collector.getActivity(heartbeat.getCount()),
      metadata: result.metadata,
    };

    return writeOutput(output);
  } catch (err) {
    clearTimeout(timeoutId);
    heartbeat.stop();
    process.stderr.write = originalStderrWrite as typeof process.stderr.write;

    // Tighter abort detection: didTimeout is authoritative
    const isAbort =
      didTimeout ||
      (err instanceof Error && err.name === "AbortError") ||
      abortController.signal.aborted;

    if (isAbort) {
      // Timeout — return partial results with activity data
      const output: Output = {
        success: true,
        engine: config.engine,
        response: "(timed out — partial results may be available in activity log)",
        timed_out: true,
        duration_ms: Date.now() - startTime,
        activity: collector.getActivity(heartbeat.getCount()),
        metadata: {},
      };
      return writeOutput(output);
    }

    // SDK error
    const output: Output = {
      success: false,
      engine: config.engine,
      error: err instanceof Error ? err.message : String(err),
      code: "SDK_ERROR",
      duration_ms: Date.now() - startTime,
      activity: collector.getActivity(heartbeat.getCount()),
    };
    return writeOutput(output);
  }
}

// --- Entry Point Helper ---

export function run(getAdapter: (engine: EngineName) => EngineAdapter): void {
  const args = parseCliArgs();

  if (args.kind === "help") {
    console.log(buildHelpText(args.engine));
    process.exit(0);
  }

  if (args.kind === "invalid") {
    const errorOutput: Output = {
      success: false,
      engine: args.engine || "codex",
      error: args.error,
      code: "INVALID_ARGS",
      duration_ms: 0,
      activity: {
        files_changed: [],
        commands_run: [],
        files_read: [],
        mcp_calls: [],
        heartbeat_count: 0,
      },
    };
    console.log(JSON.stringify(errorOutput, null, 2));
    process.exit(1);
  }

  const adapter = getAdapter(args.config.engine);
  execute(args.config, adapter).catch((err) => {
    const output: Output = {
      success: false,
      engine: args.config.engine,
      error: err instanceof Error ? err.message : String(err),
      code: "SDK_ERROR",
      duration_ms: 0,
      activity: {
        files_changed: [],
        commands_run: [],
        files_read: [],
        mcp_calls: [],
        heartbeat_count: 0,
      },
    };
    console.log(JSON.stringify(output, null, 2));
    process.exit(1);
  });
}
