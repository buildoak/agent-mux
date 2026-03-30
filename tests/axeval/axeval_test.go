//go:build axeval

// ax-eval: Agent Experience evaluation suite for agent-mux.
//
// 5 Tiers:
//   L0 · Contract Comprehension — can an agent parse our output?
//   L1 · Error Self-Correction — can an agent fix its invocation from our errors?
//   L2 · Skill Comprehension — can an agent plan dispatches from our skill doc?
//   L3 · GSD Comprehension — can GSD agents use agent-mux per their prompts?
//   L4 · Hard — real end-to-end dispatches (AX_EVAL_HARD=1 only)
//
// L0-L3 run by default with `go test -tags axeval ./tests/axeval/`.
// L4 additionally requires AX_EVAL_HARD=1 env var.

package axeval

import (
	"os"
	"os/exec"
	"testing"
)

var binaryPath string

func TestMain(m *testing.M) {
	// Set up fixture temp dir (copies testdata/fixture/ to /tmp).
	SetupFixtureDir()

	// Initialize legacy test cases now that fixture dir is ready.
	InitCases()

	// Build agent-mux binary to a temp location.
	tmpBin, err := os.CreateTemp("", "agent-mux-test-*")
	if err != nil {
		panic("create temp binary: " + err.Error())
	}
	tmpBin.Close()
	binaryPath = tmpBin.Name()

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/agent-mux/")
	cmd.Dir = "../../" // back to repo root from tests/axeval/
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build agent-mux: " + string(out))
	}

	code := m.Run()

	// Write reports.
	if err := writeReport(); err != nil {
		os.Stderr.WriteString("ax-eval: write legacy report: " + err.Error() + "\n")
	}
	if err := writeAXReport(); err != nil {
		os.Stderr.WriteString("ax-eval: write AX report: " + err.Error() + "\n")
	}

	// Print AX summary.
	axCollector.mu.Lock()
	if len(axCollector.verdicts) > 0 {
		summary := printAXSummary(axCollector.verdicts)
		os.Stderr.WriteString(summary)
	}
	axCollector.mu.Unlock()

	os.Remove(binaryPath)
	CleanupFixtureDir()
	os.Exit(code)
}
