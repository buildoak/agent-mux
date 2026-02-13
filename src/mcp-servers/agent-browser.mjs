#!/usr/bin/env node
/**
 * agent-browser MCP server — bundled with agent-mux.
 * Wraps agent-browser CLI (Vercel Labs) via stdio MCP protocol.
 * 25 tools for full browser automation.
 *
 * Key feature over @coofly/agent-browser-mcp: interactive snapshot support (-i flag)
 * for 5-10x token savings on accessibility tree snapshots.
 *
 * Requires: agent-browser CLI installed separately (npm i -g agent-browser).
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { spawn } from "child_process";

// --- CLI executor ---

function exec(args, timeoutMs = 30000) {
  return new Promise((resolve) => {
    const fullArgs = ["--json", ...args];
    let stdout = "";
    let stderr = "";

    const proc = spawn("agent-browser", fullArgs, { shell: true, timeout: timeoutMs });

    proc.stdout.on("data", (d) => (stdout += d));
    proc.stderr.on("data", (d) => (stderr += d));

    proc.on("close", (code) => {
      if (code === 0) {
        resolve({ success: true, output: stdout.trim() });
      } else {
        resolve({ success: false, output: stdout.trim(), error: stderr.trim() || `exit ${code}` });
      }
    });

    proc.on("error", (err) => {
      resolve({ success: false, output: "", error: err.message });
    });
  });
}

// --- Tool definitions (25 tools) ---

const TOOLS = [
  // Navigation
  {
    name: "browser_navigate",
    description: "Navigate to a URL. Opens the page in the browser.",
    inputSchema: {
      type: "object",
      properties: { url: { type: "string", description: "URL to navigate to" } },
      required: ["url"],
    },
  },
  {
    name: "browser_snapshot",
    description:
      "Get accessibility tree snapshot of the current page. Returns element refs (@e1, @e2...) for use with click/fill. Use mode='interactive' for token-efficient output (interactive elements only). Use mode='full' for complete page content.",
    inputSchema: {
      type: "object",
      properties: {
        mode: {
          type: "string",
          enum: ["full", "interactive", "compact"],
          description:
            "Snapshot mode. 'interactive' = only interactive elements (buttons, links, inputs) — most token-efficient. 'compact' = condensed output. 'full' = complete accessibility tree. Default: interactive.",
        },
      },
    },
  },
  {
    name: "browser_click",
    description: "Click an element by ref (e.g. @e1) or CSS selector.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_dblclick",
    description: "Double-click an element by ref (e.g. @e1) or CSS selector.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_fill",
    description: "Clear and fill an input field with text. Use ref from snapshot.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
        text: { type: "string", description: "Text to fill" },
      },
      required: ["ref", "text"],
    },
  },
  {
    name: "browser_type",
    description: "Type text character by character (for inputs that need keystroke events).",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
        text: { type: "string", description: "Text to type" },
      },
      required: ["ref", "text"],
    },
  },
  {
    name: "browser_press",
    description: "Press a keyboard key (Enter, Tab, Escape, Control+a, etc.).",
    inputSchema: {
      type: "object",
      properties: {
        key: { type: "string", description: "Key name (Enter, Tab, Escape, Control+a, etc.)" },
      },
      required: ["key"],
    },
  },
  {
    name: "browser_select",
    description: "Select a dropdown option by value.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref or CSS selector" },
        value: { type: "string", description: "Option value to select" },
      },
      required: ["ref", "value"],
    },
  },
  {
    name: "browser_hover",
    description: "Hover over an element.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_focus",
    description: "Focus an element by ref (e.g. @e1) or CSS selector.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_clear",
    description: "Clear the value of an input field by ref (e.g. @e1) or CSS selector.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_check",
    description: "Check a checkbox by ref (e.g. @e1) or CSS selector.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_uncheck",
    description: "Uncheck a checkbox by ref (e.g. @e1) or CSS selector.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_scroll",
    description: "Scroll the page in a direction.",
    inputSchema: {
      type: "object",
      properties: {
        direction: { type: "string", enum: ["up", "down", "left", "right"], description: "Scroll direction" },
        pixels: { type: "number", description: "Pixels to scroll (default: viewport height)" },
      },
      required: ["direction"],
    },
  },
  {
    name: "browser_reload",
    description: "Reload the current page.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "browser_get_url",
    description: "Get the current page URL.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "browser_get_title",
    description: "Get the current page title.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "browser_get_text",
    description: "Get text content of an element.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref or CSS selector" },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_get_html",
    description: "Get HTML content of an element. Returns inner HTML by default, or outer HTML with --outer flag.",
    inputSchema: {
      type: "object",
      properties: {
        ref: { type: "string", description: "Element ref (@e1) or CSS selector" },
        outer: { type: "boolean", description: "If true, return outer HTML instead of inner HTML. Default: false." },
      },
      required: ["ref"],
    },
  },
  {
    name: "browser_wait",
    description: "Wait for an element to appear or wait a specified number of milliseconds.",
    inputSchema: {
      type: "object",
      properties: {
        target: { type: "string", description: "CSS selector to wait for, or milliseconds (e.g. '2000')" },
      },
      required: ["target"],
    },
  },
  {
    name: "browser_evaluate",
    description: "Execute JavaScript code in the browser context and return the result.",
    inputSchema: {
      type: "object",
      properties: {
        script: { type: "string", description: "JavaScript code to execute" },
      },
      required: ["script"],
    },
  },
  {
    name: "browser_back",
    description: "Navigate back in browser history.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "browser_forward",
    description: "Navigate forward in browser history.",
    inputSchema: { type: "object", properties: {} },
  },
  {
    name: "browser_screenshot",
    description: "Take a screenshot of the current page. Returns the file path.",
    inputSchema: {
      type: "object",
      properties: {
        path: { type: "string", description: "File path to save screenshot (optional)" },
        fullPage: { type: "boolean", description: "Capture full page (default: false)" },
      },
    },
  },
  {
    name: "browser_close",
    description: "Close the browser.",
    inputSchema: { type: "object", properties: {} },
  },
];

// --- Tool handler ---

async function handleTool(name, args) {
  switch (name) {
    case "browser_navigate":
      return exec(["open", args.url]);

    case "browser_snapshot": {
      const flags = [];
      const mode = args.mode || "interactive";
      if (mode === "interactive") flags.push("-i");
      else if (mode === "compact") flags.push("-c");
      // "full" = no extra flags
      return exec(["snapshot", ...flags]);
    }

    case "browser_click":
      return exec(["click", args.ref]);

    case "browser_dblclick":
      return exec(["dblclick", args.ref]);

    case "browser_fill":
      return exec(["fill", args.ref, args.text]);

    case "browser_type":
      return exec(["type", args.ref, args.text]);

    case "browser_press":
      return exec(["press", args.key]);

    case "browser_select":
      return exec(["select", args.ref, args.value]);

    case "browser_hover":
      return exec(["hover", args.ref]);

    case "browser_focus":
      return exec(["focus", args.ref]);

    case "browser_clear":
      return exec(["clear", args.ref]);

    case "browser_check":
      return exec(["check", args.ref]);

    case "browser_uncheck":
      return exec(["uncheck", args.ref]);

    case "browser_scroll": {
      const scrollArgs = ["scroll", args.direction];
      if (args.pixels) scrollArgs.push(String(args.pixels));
      return exec(scrollArgs);
    }

    case "browser_reload":
      return exec(["reload"]);

    case "browser_get_url":
      return exec(["get", "url"]);

    case "browser_get_title":
      return exec(["get", "title"]);

    case "browser_get_text":
      return exec(["get", "text", args.ref]);

    case "browser_get_html": {
      const htmlArgs = ["get", "html", args.ref];
      if (args.outer) htmlArgs.push("--outer");
      return exec(htmlArgs);
    }

    case "browser_wait":
      return exec(["wait", args.target]);

    case "browser_evaluate":
      return exec(["eval", args.script]);

    case "browser_back":
      return exec(["back"]);

    case "browser_forward":
      return exec(["forward"]);

    case "browser_screenshot": {
      const ssArgs = ["screenshot"];
      if (args.path) ssArgs.push(args.path);
      if (args.fullPage) ssArgs.push("--full");
      return exec(ssArgs);
    }

    case "browser_close":
      return exec(["close"]);

    default:
      return { success: false, error: `Unknown tool: ${name}` };
  }
}

// --- Server setup ---

const server = new Server(
  { name: "agent-browser-mcp", version: "2.1.0" },
  { capabilities: { tools: {} } }
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools: TOOLS }));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const { name, arguments: args } = request.params;
  console.error(`[agent-browser] ${name}${args ? ": " + JSON.stringify(args) : ""}`);

  try {
    const result = await handleTool(name, args || {});
    const text = JSON.stringify(result);
    console.error(`[agent-browser] ${name} -> ${result.success ? "ok" : "fail"}`);
    return { content: [{ type: "text", text }] };
  } catch (err) {
    const text = JSON.stringify({ success: false, error: err.message });
    console.error(`[agent-browser] ${name} -> error: ${err.message}`);
    return { content: [{ type: "text", text }] };
  }
});

// Start
const transport = new StdioServerTransport();
await server.connect(transport);
console.error("[agent-browser-mcp] Server started (stdio mode, 25 tools)");
