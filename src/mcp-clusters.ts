/**
 * mcp-clusters.ts — Config-file-driven MCP Cluster Registry
 *
 * Loads MCP cluster definitions from a YAML config file instead of
 * hardcoded paths. Search order:
 *   1. ./mcp-clusters.yaml (project-local)
 *   2. ~/.config/agent-mux/mcp-clusters.yaml (user-global)
 *   3. Empty default (no clusters available)
 *
 * Clusters are named groups of MCP servers that can be enabled via
 * the --mcp-cluster flag on any engine.
 */

import { readFileSync, existsSync } from "node:fs";
import { resolve, join } from "node:path";
import { homedir } from "node:os";
import * as yaml from "js-yaml";

// --- Types ---

export interface McpServerConfig {
  command: string;
  args: string[];
  cwd?: string;
  env?: Record<string, string>;
}

export interface McpCluster {
  name: string;
  description: string;
  servers: Record<string, McpServerConfig>;
}

// --- Config File Schema (YAML) ---

interface McpServerYaml {
  command: string;
  args?: string[];
  cwd?: string;
  env?: Record<string, string>;
}

interface McpClusterYaml {
  description?: string;
  servers: Record<string, McpServerYaml>;
}

interface McpConfigYaml {
  clusters: Record<string, McpClusterYaml>;
}

// --- Config File Loading ---

const CONFIG_FILENAME = "mcp-clusters.yaml";

function findConfigFile(): string | null {
  // 1. Project-local
  const localPath = resolve(process.cwd(), CONFIG_FILENAME);
  if (existsSync(localPath)) return localPath;

  // 2. User-global
  const globalPath = join(homedir(), ".config", "agent-mux", CONFIG_FILENAME);
  if (existsSync(globalPath)) return globalPath;

  return null;
}

function loadConfig(): Record<string, McpCluster> {
  const configPath = findConfigFile();
  if (!configPath) return {};

  try {
    const raw = readFileSync(configPath, "utf-8");
    const parsed = yaml.load(raw) as McpConfigYaml;

    if (!parsed || !parsed.clusters) return {};

    const clusters: Record<string, McpCluster> = {};

    for (const [name, clusterYaml] of Object.entries(parsed.clusters)) {
      const servers: Record<string, McpServerConfig> = {};

      for (const [serverName, serverYaml] of Object.entries(clusterYaml.servers || {})) {
        servers[serverName] = {
          command: serverYaml.command,
          args: serverYaml.args || [],
          ...(serverYaml.cwd ? { cwd: serverYaml.cwd } : {}),
          ...(serverYaml.env ? { env: serverYaml.env } : {}),
        };
      }

      clusters[name] = {
        name,
        description: clusterYaml.description || name,
        servers,
      };
    }

    return clusters;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    process.stderr.write(`[agent-mux] Warning: failed to load ${configPath}: ${msg}\n`);
    return {};
  }
}

// --- Lazy-loaded singleton ---

let _clusters: Record<string, McpCluster> | null = null;

function getClusters(): Record<string, McpCluster> {
  if (_clusters === null) {
    _clusters = loadConfig();
  }
  return _clusters;
}

// --- Public API ---

/**
 * Get all known server names across all clusters.
 * Used by Codex engine to disable servers not in the requested clusters.
 */
export function getAllServerNames(): string[] {
  const names = new Set<string>();
  for (const cluster of Object.values(getClusters())) {
    for (const serverName of Object.keys(cluster.servers)) {
      names.add(serverName);
    }
  }
  return [...names];
}

/**
 * Resolve cluster names to a merged MCP server config.
 * Supports named clusters + 'all' (union of everything).
 */
export function resolveClusters(clusterNames: string[]): Record<string, McpServerConfig> {
  const clusters = getClusters();
  const servers: Record<string, McpServerConfig> = {};

  for (const name of clusterNames) {
    if (name === "all") {
      // Merge everything
      for (const cluster of Object.values(clusters)) {
        Object.assign(servers, cluster.servers);
      }
    } else if (clusters[name]) {
      Object.assign(servers, clusters[name].servers);
    } else {
      const available = [...Object.keys(clusters), "all"].join(", ");
      throw new Error(
        `Unknown MCP cluster: '${name}'. Available: ${available || "(none — no config file found)"}`
      );
    }
  }

  return servers;
}

/**
 * Convert to OpenCode McpLocalConfig format.
 * OpenCode uses: { type: "local", command: string[], environment?: Record<string, string> }
 */
export function toOpenCodeMcp(
  servers: Record<string, McpServerConfig>
): Record<string, { type: "local"; command: string[]; environment?: Record<string, string> }> {
  const result: Record<string, { type: "local"; command: string[]; environment?: Record<string, string> }> = {};
  for (const [name, config] of Object.entries(servers)) {
    result[name] = {
      type: "local",
      command: [config.command, ...config.args],
      ...(config.env ? { environment: config.env } : {}),
    };
  }
  return result;
}

/**
 * List available clusters for --help output.
 */
export function listClusters(): string {
  const clusters = getClusters();
  const entries = Object.values(clusters);

  if (entries.length === 0) {
    return "  (none — create mcp-clusters.yaml to define clusters)";
  }

  const lines = entries.map(
    (c) => `  ${c.name.padEnd(12)} ${c.description}`
  );
  lines.push(`  ${"all".padEnd(12)} All clusters combined`);
  return lines.join("\n");
}
