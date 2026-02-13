#!/bin/bash
# agent-mux setup
# Checks prerequisites, installs dependencies, verifies compilation.
# Safe to run multiple times (idempotent).

set -euo pipefail

# Colors (disabled if not a terminal)
if [ -t 1 ]; then
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  RED='\033[0;31m'
  BOLD='\033[1m'
  RESET='\033[0m'
else
  GREEN='' YELLOW='' RED='' BOLD='' RESET=''
fi

ok()   { echo -e "  ${GREEN}✓${RESET} $1"; }
warn() { echo -e "  ${YELLOW}!${RESET} $1"; }
fail() { echo -e "  ${RED}✗${RESET} $1"; }

echo -e "${BOLD}agent-mux setup${RESET}"
echo ""

# --- 1. Check Bun ---
echo -e "${BOLD}Checking runtime...${RESET}"
if command -v bun &>/dev/null; then
  BUN_VERSION=$(bun --version 2>/dev/null || echo "unknown")
  ok "Bun ${BUN_VERSION}"
else
  fail "Bun is not installed"
  echo ""
  echo "  Install Bun:"
  echo "    curl -fsSL https://bun.sh/install | bash"
  echo ""
  echo "  Then re-run this script."
  exit 1
fi

# --- 2. Install dependencies ---
echo ""
echo -e "${BOLD}Installing dependencies...${RESET}"
bun install --frozen-lockfile 2>/dev/null || bun install
ok "Dependencies installed"

# --- 3. TypeScript check ---
echo ""
echo -e "${BOLD}Checking TypeScript compilation...${RESET}"
if bunx tsc --noEmit 2>/dev/null; then
  ok "TypeScript compiles clean"
else
  warn "TypeScript has errors (may still work at runtime with Bun)"
fi

# --- 4. MCP cluster config ---
echo ""
echo -e "${BOLD}MCP cluster config...${RESET}"
CONFIG_DIR="${HOME}/.config/agent-mux"
CONFIG_FILE="${CONFIG_DIR}/mcp-clusters.yaml"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
EXAMPLE_FILE="${SCRIPT_DIR}/mcp-clusters.example.yaml"

if [ -f "${CONFIG_FILE}" ]; then
  ok "Config exists at ${CONFIG_FILE}"
elif [ -f "${EXAMPLE_FILE}" ]; then
  mkdir -p "${CONFIG_DIR}"
  cp "${EXAMPLE_FILE}" "${CONFIG_FILE}"
  ok "Copied example config to ${CONFIG_FILE}"
  warn "Edit ${CONFIG_FILE} to configure your MCP servers"
else
  ok "No example config found — skipping (MCP clusters are optional)"
fi

# --- 5. API key status ---
echo ""
echo -e "${BOLD}API key status...${RESET}"

check_key() {
  local name="$1" var="$2" required="$3" hint="$4"
  if [ -n "${!var:-}" ]; then
    ok "${name}: ${var} is set"
  elif [ "${required}" = "required" ]; then
    fail "${name}: ${var} is not set — ${hint}"
  else
    warn "${name}: ${var} is not set — ${hint}"
  fi
}

check_key "Codex"    "OPENAI_API_KEY"     "required"    "https://platform.openai.com/api-keys"
check_key "Claude"   "ANTHROPIC_API_KEY"   "optional"    "or use device OAuth (SDK prompts for browser auth)"
check_key "OpenCode" "OPENROUTER_API_KEY"  "optional"    "or configure provider keys in OpenCode"

# --- Done ---
echo ""
echo -e "${GREEN}${BOLD}Setup complete.${RESET}"
echo ""
echo "  Quick start:"
echo "    bun run src/agent.ts --engine codex \"Summarize this repo\""
echo "    bun run src/agent.ts --engine claude \"Design a migration plan\""
echo "    bun run src/agent.ts --engine opencode \"Find regressions in this PR\""
echo ""
