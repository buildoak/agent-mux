package main

import (
	"io"
	"strings"
)

const topLevelHelpText = `
Usage:
  agent-mux [flags] <prompt>
  agent-mux dispatch [flags] <prompt>
  agent-mux preview [flags] <prompt>
  agent-mux help

  "dispatch" is the default subcommand — both forms are equivalent:
    agent-mux -P=auditor "Review the code"
    agent-mux dispatch -P=auditor "Review the code"

Quickstart:
  agent-mux config prompts
  agent-mux -P=lifter -E=codex -e=high -C=/repo "Implement retries in client.ts"
  agent-mux wait <dispatch_id>
  agent-mux result <dispatch_id> --json

Key flags:
  -E, --engine       Engine: codex, claude, gemini
  -P, --profile      Profile / prompt file
  -e, --effort       Effort: low, medium, high (default when omitted)
  -m, --model        Model override (engine-specific)
  -C, --cwd          Working directory
  -r, --reasoning    Reasoning effort: low, medium (default), high
  --permission-mode  Permission mode: default, auto_edit, yolo, plan
                     (Gemini defaults to yolo; Codex uses --sandbox instead)
  --sandbox          Codex sandbox: danger-full-access (default), workspace-write
  --context-file     File to prepend as context (for pipeline handoffs)
  --prompt-file      Read prompt from file instead of positional arg
  --async            Return dispatch ID immediately, run in background

  Note: use double-dash for long flags (--engine) or short alias (-E).
  Single-dash long flags (-engine) are rejected with a suggestion.

Multi-step pipeline example:
  # Gemini analysis → Codex writing via context-file handoff
  ID=$(agent-mux -P=researcher -E=gemini --async "Analyze the paper")
  agent-mux wait "$ID"
  agent-mux result "$ID" > /tmp/analysis.md
  agent-mux -P=writer -E=codex --context-file /tmp/analysis.md "Write a summary"

Lifecycle:
  agent-mux list [--json]
  agent-mux status <dispatch_id> [--json]
  agent-mux result <dispatch_id> [--json]
  agent-mux inspect <dispatch_id> [--json]
  agent-mux wait [--poll 30s] <dispatch_id>

Steer actions:
  agent-mux steer <dispatch_id> abort
  agent-mux steer <dispatch_id> nudge ["message"]
  agent-mux steer <dispatch_id> redirect "<instructions>"
  agent-mux steer <dispatch_id> extend <seconds>

Other control paths:
  agent-mux --signal <dispatch_id> "<message>"
  agent-mux --stdin < spec.json
  agent-mux --version

Literal prompt escape:
  agent-mux -- help
`

func emitTopLevelHelp(stdout io.Writer) int {
	emitResult(stdout, map[string]any{
		"kind":  "help",
		"usage": strings.TrimSpace(topLevelHelpText),
	})
	return 0
}
