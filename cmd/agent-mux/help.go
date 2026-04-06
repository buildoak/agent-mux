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
