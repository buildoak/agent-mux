/**
 * types.ts — Shared types for agent-mux
 */

// --- Effort & Engine ---

export type EffortLevel = "low" | "medium" | "high" | "xhigh";
export type EngineName = "codex" | "claude" | "opencode";

// --- Engine Adapter Interface ---

export interface RunConfig {
  prompt: string;
  cwd: string;
  timeout: number;
  signal: AbortSignal;
  model: string;
  effort: EffortLevel;
  mcpServers: Record<string, import("./mcp-clusters.ts").McpServerConfig>;
  systemPrompt?: string;
  engineOptions: Record<string, unknown>;
}

export interface ActivityItem {
  type: "file_change" | "command" | "file_read" | "mcp_call" | "message";
  summary: string;
  detail?: string;
}

export interface EngineCallbacks {
  /** Called by engine to report what it's doing — drives heartbeat messages */
  onHeartbeat(activity: string): void;
  /** Called for structured activity items (file changes, commands, messages) */
  onItem(item: ActivityItem): void;
}

export interface EngineResult {
  response: string;
  items: ActivityItem[];
  metadata: {
    session_id?: string;
    cost_usd?: number;
    tokens?: { input: number; output: number; reasoning?: number };
    turns?: number;
    model?: string;
  };
}

export interface EngineAdapter {
  run(config: RunConfig, callbacks: EngineCallbacks): Promise<EngineResult>;
}

// --- Output Contract ---

export interface Activity {
  files_changed: string[];
  commands_run: string[];
  files_read: string[];
  mcp_calls: string[];
  heartbeat_count: number;
}

export interface SuccessOutput {
  success: true;
  engine: EngineName;
  response: string;
  timed_out: boolean;
  duration_ms: number;
  activity: Activity;
  metadata: EngineResult["metadata"];
}

export interface ErrorOutput {
  success: false;
  engine: EngineName;
  error: string;
  code: "INVALID_ARGS" | "SDK_ERROR";
  duration_ms: number;
  activity: Activity;
}

export type Output = SuccessOutput | ErrorOutput;

// --- Default Timeouts ---

export const TIMEOUT_BY_EFFORT: Record<EffortLevel, number> = {
  low: 120_000,       // 2 min
  medium: 600_000,    // 10 min
  high: 1_200_000,    // 20 min
  xhigh: 2_400_000,   // 40 min
};
