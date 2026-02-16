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
import { readFileSync, existsSync } from "node:fs";
import { resolve, dirname, join, delimiter } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";
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
  skills: Array<{ name: string; dir: string; content: string; hasScripts: boolean }>;
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
      --skill <name>         Load skill (repeatable, reads <cwd>/.claude/skills/<name>/SKILL.md)
      --mcp-cluster <name>   Enable MCP cluster (repeatable)
  -b, --browser              Sugar for --mcp-cluster browser
  -f, --full                 Full access mode
  -V, --version              Show version
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
  | { kind: "version" }
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
        skill: { type: "string", multiple: true },
        "mcp-cluster": { type: "string", multiple: true },
        browser: { type: "boolean", short: "b" },
        full: { type: "boolean", short: "f" },
        help: { type: "boolean", short: "h" },
        version: { type: "boolean", short: "V" },
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

    // --version: handle before any engine-specific parsing
    if (values.version) {
      return { kind: "version" };
    }

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
    const cwd = values.cwd || process.cwd();

    // Codex options
    if (engine === "codex") {
      const sandbox = fullMode
        ? "danger-full-access"
        : (values.sandbox as string) || "read-only";
      engineOptions.sandbox = sandbox;
      engineOptions.reasoning = (values.reasoning as string) || "medium";
      engineOptions.network = fullMode || values.network === true;
      engineOptions.addDirs = (values["add-dir"] as string[] | undefined) ?? [];
      // Codex: add skill directories for sandbox access (will be populated after skill resolution below)
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

    // Skill resolution
    const skillNames = (values["skill"] as string[] | undefined) ?? [];
    const resolvedSkills: Array<{ name: string; dir: string; content: string; hasScripts: boolean }> = [];

    for (const name of skillNames) {
      const skillsRoot = resolve(cwd, ".claude", "skills");
      const skillDir = resolve(skillsRoot, name);
      // Validate the resolved path is inside skills root
      if (!skillDir.startsWith(skillsRoot + "/") && skillDir !== skillsRoot) {
        return {
          kind: "invalid",
          error: `Invalid skill name '${name}': path traversal detected`,
          engine,
        };
      }
      const skillMdPath = join(skillDir, "SKILL.md");
      if (!existsSync(skillMdPath)) {
        return {
          kind: "invalid",
          error: `Skill '${name}' not found: expected SKILL.md at ${skillMdPath}`,
          engine,
        };
      }
      const content = readFileSync(skillMdPath, "utf-8");
      const scriptsDir = join(skillDir, "scripts");
      const hasScripts = existsSync(scriptsDir);
      resolvedSkills.push({ name, dir: skillDir, content, hasScripts });
    }

    if (engine === "codex" && resolvedSkills.length > 0) {
      const existingDirs = (engineOptions.addDirs as string[]) || [];
      for (const skill of resolvedSkills) {
        existingDirs.push(skill.dir);
      }
      engineOptions.addDirs = existingDirs;
    }

    return {
      kind: "ok",
      config: {
        engine,
        prompt,
        cwd,
        skills: resolvedSkills,
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

// --- Version ---

let _cachedVersion: string | null = null;

export function getVersion(): string {
  if (_cachedVersion) return _cachedVersion;
  try {
    const __dirname = dirname(fileURLToPath(import.meta.url));
    const pkgPath = resolve(__dirname, "..", "package.json");
    const pkg = JSON.parse(readFileSync(pkgPath, "utf-8"));
    _cachedVersion = pkg.version || "unknown";
  } catch {
    _cachedVersion = "unknown";
  }
  return _cachedVersion!;
}

// --- Output ---

function writeOutput(result: Output): never {
  console.log(JSON.stringify(result, null, 2));
  process.exit(result.success ? 0 : 1);
}

// --- Execute Options ---

export interface ExecuteOptions {
  /** Filter SDK stderr noise (default: false). Set true in CLI entry path. */
  filterStderr?: boolean;
  /** Install SIGINT/SIGTERM shutdown handlers (default: false). Set true in CLI entry path. */
  handleSignals?: boolean;
}

// --- Main Execution ---

export async function execute(
  config: ParsedConfig,
  adapter: EngineAdapter,
  options: ExecuteOptions = {},
): Promise<never> {
  const startTime = Date.now();
  const heartbeat = new HeartbeatManager();
  const collector = new ActivityCollector();

  // Optionally suppress SDK stderr noise — only when running as CLI
  let originalStderrWrite: typeof process.stderr.write | null = null;
  if (options.filterStderr) {
    originalStderrWrite = process.stderr.write.bind(process.stderr);
    const savedWrite = originalStderrWrite;
    const stderrFilter = function (this: typeof process.stderr, chunk: unknown, ...rest: unknown[]): boolean {
      const str = typeof chunk === "string" ? chunk : String(chunk);
      if (str.startsWith("[heartbeat]") || str.startsWith("[agent-mux]")) {
        return savedWrite(chunk as string, ...(rest as []));
      }
      return true; // swallow SDK noise
    };
    process.stderr.write = stderrFilter as typeof process.stderr.write;
  }

  /** Restore stderr if it was patched */
  const restoreStderr = () => {
    if (originalStderrWrite) {
      process.stderr.write = originalStderrWrite as typeof process.stderr.write;
      originalStderrWrite = null;
    }
  };
  const originalPath = process.env.PATH;

  // AbortController for timeout and graceful shutdown
  const abortController = new AbortController();
  let didTimeout = false;
  let didShutdown = false;

  // Graceful shutdown handler — treat SIGINT/SIGTERM as user-initiated timeout
  const shutdownHandler = () => {
    if (didShutdown || didTimeout) return; // already aborting
    didShutdown = true;
    abortController.abort();
  };

  if (options.handleSignals) {
    process.on("SIGINT", shutdownHandler);
    process.on("SIGTERM", shutdownHandler);
  }

  /** Clean up all registered handlers and timers */
  const cleanup = (timeoutId: ReturnType<typeof setTimeout>) => {
    clearTimeout(timeoutId);
    heartbeat.stop();
    restoreStderr();
    if (originalPath !== undefined) {
      process.env.PATH = originalPath;
    }
    if (options.handleSignals) {
      process.removeListener("SIGINT", shutdownHandler);
      process.removeListener("SIGTERM", shutdownHandler);
    }
  };

  // Build skill-augmented prompt
  let skillPrefix = "";
  for (const skill of config.skills) {
    skillPrefix += `<skill name="${skill.name.replace(/"/g, "&quot;")}" source="${skill.dir.replace(/"/g, "&quot;")}/SKILL.md">\n${skill.content}\n</skill>\n\n`;
  }
  const basePrompt = skillPrefix + config.prompt;
  const timeAwarePrompt = `You have a time budget of ${config.timeout / 1000} seconds. Prioritize delivering complete output over exploration.\n\n${basePrompt}`;

  // Add skill scripts/ directories to PATH
  const skillScriptDirs = config.skills
    .filter((s) => s.hasScripts)
    .map((s) => join(s.dir, "scripts"));
  if (skillScriptDirs.length > 0) {
    process.env.PATH = skillScriptDirs.join(delimiter) + delimiter + (process.env.PATH || "");
  }

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

    cleanup(timeoutId);

    const output: Output = {
      success: true,
      engine: config.engine,
      response: result.response,
      timed_out: didTimeout || didShutdown,
      duration_ms: Date.now() - startTime,
      activity: collector.getActivity(heartbeat.getCount()),
      metadata: result.metadata,
    };

    return writeOutput(output);
  } catch (err) {
    cleanup(timeoutId);

    // Abort detection: timeout or shutdown or AbortError
    const isAbort =
      didTimeout ||
      didShutdown ||
      (err instanceof Error && err.name === "AbortError") ||
      abortController.signal.aborted;

    if (isAbort) {
      // Timeout or shutdown — return partial results with activity data
      const output: Output = {
        success: true,
        engine: config.engine,
        response: didShutdown
          ? "(shutdown requested — partial results may be available in activity log)"
          : "(timed out — partial results may be available in activity log)",
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

// --- Pre-flight API Key Check ---

const API_KEY_MAP: Record<EngineName, { envVar: string; hardError: boolean; hint: string }> = {
  codex: {
    envVar: "OPENAI_API_KEY",
    hardError: false,
    hint: "Get one at https://platform.openai.com/api-keys — or run `codex auth` to set up OAuth device auth",
  },
  claude: {
    envVar: "ANTHROPIC_API_KEY",
    hardError: false,
    hint: "Get one at https://console.anthropic.com/ — or use Claude Code device OAuth (SDK handles auth automatically)",
  },
  opencode: {
    envVar: "OPENROUTER_API_KEY",
    hardError: false,
    hint: "Get one at https://openrouter.ai/keys — or configure provider keys directly in OpenCode",
  },
};

/** Check if Codex CLI has valid OAuth tokens in ~/.codex/auth.json */
function hasCodexOAuth(): boolean {
  try {
    const authPath = join(homedir(), ".codex", "auth.json");
    if (!existsSync(authPath)) return false;
    const auth = JSON.parse(readFileSync(authPath, "utf-8"));
    return !!(auth.tokens && auth.tokens.access_token);
  } catch {
    return false;
  }
}

function checkApiKey(engine: EngineName): { ok: boolean; warning?: string; error?: string } {
  const spec = API_KEY_MAP[engine];
  const value = process.env[spec.envVar];

  if (value && value.trim().length > 0) {
    return { ok: true };
  }

  // Codex supports OAuth device auth via ~/.codex/auth.json
  if (engine === "codex" && hasCodexOAuth()) {
    return { ok: true };
  }

  const message = `${spec.envVar} is not set. ${spec.hint}`;

  if (spec.hardError) {
    return { ok: false, error: message };
  }
  return { ok: true, warning: message };
}

// --- Entry Point Helper ---

export function run(getAdapter: (engine: EngineName) => EngineAdapter): void {
  const args = parseCliArgs();

  if (args.kind === "version") {
    console.log(getVersion());
    process.exit(0);
  }

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

  // At this point, only "ok" remains
  const { config } = args as { kind: "ok"; config: ParsedConfig };

  // Pre-flight: check API key for the selected engine
  const keyCheck = checkApiKey(config.engine);
  if (!keyCheck.ok) {
    const errorOutput: Output = {
      success: false,
      engine: config.engine,
      error: keyCheck.error!,
      code: "MISSING_API_KEY",
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
  if (keyCheck.warning) {
    process.stderr.write(`[agent-mux] warning: ${keyCheck.warning}\n`);
  }

  const adapter = getAdapter(config.engine);
  execute(config, adapter, { filterStderr: true, handleSignals: true }).catch((err) => {
    const output: Output = {
      success: false,
      engine: config.engine,
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
