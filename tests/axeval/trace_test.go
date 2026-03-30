//go:build axeval

package axeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTraceVerification runs behavioral trace analysis on completed ax-eval cases.
// It dispatches the same test cases, then analyzes the agent's behavior trace
// via a Codex trace analyzer.
func TestTraceVerification(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 test — requires AX_EVAL_HARD=1")
	}
	// Select a representative subset of cases for trace analysis.
	// These cover: simple completion, correctness, quality, error handling, multi-step.
	traceCases := selectTraceCases()
	if len(traceCases) == 0 {
		t.Skip("no trace cases selected")
	}

	t.Logf("=== Trace Verification: %d cases ===", len(traceCases))

	var verdicts []TraceVerdict
	skipped := 0
	for _, tc := range traceCases {
		tc := tc
		t.Run("trace/"+tc.Name, func(t *testing.T) {
			// Don't parallelize — trace analyzer dispatches are sequential
			// to avoid overwhelming the Codex API.

			t.Logf("dispatching case %s...", tc.Name)
			result := dispatch(t, binaryPath, tc)

			// Skip trace analysis for failed dispatches where stdout isn't JSON.
			if result.Status == "parse_error" {
				t.Logf("SKIP: case %s had parse_error, no trace to analyze", tc.Name)
				skipped++
				return
			}

			verdict, err := RunTraceVerification(binaryPath, tc.Name, tc.Prompt, &result)
			if err != nil {
				t.Logf("WARN: trace verification failed for %s: %v", tc.Name, err)
				skipped++
				return
			}

			verdicts = append(verdicts, *verdict)

			// Print summary line.
			passStr := "PASS"
			if !verdict.Pass {
				passStr = "FAIL"
			}
			t.Logf("[TRACE %s] %s: source=%s flags=%v first_action=%s turns=%d tools=%d errors=%d",
				passStr, tc.Name, verdict.Source, verdict.Flags, verdict.FirstAction,
				verdict.TurnsUsed, verdict.ToolCalls, verdict.ErrorCount)
			t.Logf("  reasoning: %s", verdict.Reasoning)
		})
	}

	// Write trace report.
	if len(verdicts) > 0 {
		if err := writeTraceReportWithSkipped(verdicts, skipped); err != nil {
			t.Errorf("failed to write trace report: %v", err)
		}
		printTraceSummary(t, verdicts)
	}
}

// selectTraceCases picks representative cases for trace analysis.
func selectTraceCases() []TestCase {
	// Target cases by name — diverse coverage of categories.
	targetNames := map[string]bool{
		"complete-simple":   true, // completion
		"analyze-repo":     true, // correctness + judge
		"count-loc":        true, // correctness (deterministic)
		"run-bash":         true, // command execution
		"multi-step-reason": true, // quality + multi-step
	}

	var selected []TestCase
	for _, tc := range AllCases {
		if targetNames[tc.Name] {
			// Skip async/steer cases — they need different dispatch flow.
			if tc.IsAsync || tc.SteerSpec != nil {
				continue
			}
			selected = append(selected, tc)
		}
	}
	return selected
}

// printTraceSummary prints a formatted summary table of trace verdicts.
func printTraceSummary(t *testing.T, verdicts []TraceVerdict) {
	t.Helper()

	passed := 0
	failed := 0
	flagCounts := make(map[string]int)

	t.Logf("")
	t.Logf("╔══════════════════════════╦════════╦═══════════════════════╦═══════╦═══════╦════════╗")
	t.Logf("║ Case                     ║ Result ║ Flags                 ║ Turns ║ Tools ║ Errors ║")
	t.Logf("╠══════════════════════════╬════════╬═══════════════════════╬═══════╬═══════╬════════╣")

	for _, v := range verdicts {
		result := "PASS"
		if !v.Pass {
			result = "FAIL"
			failed++
		} else {
			passed++
		}

		flags := strings.Join(v.Flags, ",")
		if len(flags) > 21 {
			flags = flags[:18] + "..."
		}

		name := v.Case
		if len(name) > 24 {
			name = name[:21] + "..."
		}

		t.Logf("║ %-24s ║ %-6s ║ %-21s ║ %5d ║ %5d ║ %6d ║",
			name, result, flags, v.TurnsUsed, v.ToolCalls, v.ErrorCount)

		for _, f := range v.Flags {
			flagCounts[f]++
		}
	}

	t.Logf("╚══════════════════════════╩════════╩═══════════════════════╩═══════╩═══════╩════════╝")
	t.Logf("")
	t.Logf("Summary: %d/%d passed, %d failed", passed, len(verdicts), failed)

	if len(flagCounts) > 0 {
		t.Logf("Flag distribution:")
		for flag, count := range flagCounts {
			t.Logf("  %s: %d/%d (%.0f%%)", flag, count, len(verdicts), float64(count)/float64(len(verdicts))*100)
		}
	}
}

// TestTraceReportFormat validates that trace report JSON is well-formed.
// This is a fast unit test that doesn't dispatch any agents.
func TestTraceReportFormat(t *testing.T) {
	verdicts := []TraceVerdict{
		{
			Case:        "test-case-1",
			DispatchID:  "01TEST001",
			Pass:        true,
			Flags:       []string{"efficient", "clean_completion"},
			Reasoning:   "Agent completed the task efficiently.",
			TurnsUsed:   3,
			ToolCalls:   5,
			ErrorCount:  0,
			FirstAction: "tool:read_file",
		},
		{
			Case:        "test-case-2",
			DispatchID:  "01TEST002",
			Pass:        false,
			Flags:       []string{"error_spiral", "wasteful"},
			Reasoning:   "Agent repeated the same failing command 4 times.",
			TurnsUsed:   8,
			ToolCalls:   15,
			ErrorCount:  4,
			FirstAction: "command:ls",
		},
	}

	// Write to a temp dir.
	tmpDir := t.TempDir()
	t.Setenv("AX_EVAL_REPORT_DIR", tmpDir)

	if err := writeTraceReport(verdicts); err != nil {
		t.Fatalf("writeTraceReport: %v", err)
	}

	// Read and validate.
	data, err := os.ReadFile(filepath.Join(tmpDir, "trace-report.json"))
	if err != nil {
		t.Fatalf("read trace report: %v", err)
	}

	var report TraceReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal trace report: %v", err)
	}

	if report.Summary.Total != 2 {
		t.Errorf("summary.total = %d, want 2", report.Summary.Total)
	}
	if report.Summary.Passed != 1 {
		t.Errorf("summary.passed = %d, want 1", report.Summary.Passed)
	}
	if report.Summary.Failed != 1 {
		t.Errorf("summary.failed = %d, want 1", report.Summary.Failed)
	}
	if len(report.Verdicts) != 2 {
		t.Errorf("verdicts count = %d, want 2", len(report.Verdicts))
	}

	t.Logf("trace report format valid: %d verdicts, run_id=%s", len(report.Verdicts), report.RunID)
}

// TestTimelineSummary validates the event timeline formatting.
func TestTimelineSummary(t *testing.T) {
	events := []traceEvent{
		{Type: "dispatch_start", Timestamp: "2026-01-01T00:00:00Z", Engine: "codex", Model: "gpt-5.4-mini"},
		{Type: "tool_start", Timestamp: "2026-01-01T00:00:01Z", Tool: "read_file", Args: "/tmp/test.go"},
		{Type: "tool_end", Timestamp: "2026-01-01T00:00:02Z", Tool: "read_file", DurationMS: 1000},
		{Type: "file_read", Timestamp: "2026-01-01T00:00:02Z", Path: "/tmp/test.go"},
		{Type: "command_run", Timestamp: "2026-01-01T00:00:03Z", Command: "wc -l test.go"},
		{Type: "error", Timestamp: "2026-01-01T00:00:04Z", ErrorCode: "test_error", Message: "something went wrong"},
		{Type: "dispatch_end", Timestamp: "2026-01-01T00:00:05Z", Status: "completed", DurationMS: 5000},
	}

	summary := buildTimelineSummary(events)

	if !strings.Contains(summary, "START engine=codex") {
		t.Error("timeline missing dispatch_start")
	}
	if !strings.Contains(summary, "TOOL_START read_file") {
		t.Error("timeline missing tool_start")
	}
	if !strings.Contains(summary, "ERROR code=test_error") {
		t.Error("timeline missing error")
	}
	if !strings.Contains(summary, "END status=completed") {
		t.Error("timeline missing dispatch_end")
	}

	t.Logf("timeline summary:\n%s", summary)
}

// TestDeterministicFlags validates flag assignment without the LLM analyzer.
func TestDeterministicFlags(t *testing.T) {
	tests := []struct {
		name           string
		events         []traceEvent
		expectedFlags  []string
		unexpectedFlags []string
	}{
		{
			name: "clean_efficient",
			events: []traceEvent{
				{Type: "tool_start"}, {Type: "tool_end"},
				{Type: "tool_start"}, {Type: "tool_end"},
			},
			expectedFlags:  []string{"clean_completion", "efficient"},
			unexpectedFlags: []string{"error_spiral", "wasteful"},
		},
		{
			name: "error_spiral",
			events: []traceEvent{
				{Type: "error"}, {Type: "error"}, {Type: "error"}, {Type: "error"},
			},
			expectedFlags:  []string{"error_spiral", "efficient"},
			unexpectedFlags: []string{"clean_completion"},
		},
		{
			name: "wasteful",
			events: func() []traceEvent {
				var evts []traceEvent
				for i := 0; i < 22; i++ {
					evts = append(evts, traceEvent{Type: "tool_start"})
				}
				return evts
			}(),
			expectedFlags:  []string{"clean_completion", "wasteful"},
			unexpectedFlags: []string{"efficient"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := &TraceVerdict{Flags: []string{}}
			applyDeterministicFlags(verdict, tt.events)

			flagSet := make(map[string]bool)
			for _, f := range verdict.Flags {
				flagSet[f] = true
			}

			for _, expected := range tt.expectedFlags {
				if !flagSet[expected] {
					t.Errorf("expected flag %q not found in %v", expected, verdict.Flags)
				}
			}
			for _, unexpected := range tt.unexpectedFlags {
				if flagSet[unexpected] {
					t.Errorf("unexpected flag %q found in %v", unexpected, verdict.Flags)
				}
			}
		})
	}
}

// TestFirstActionIdentification validates first action extraction from events.
func TestFirstActionIdentification(t *testing.T) {
	tests := []struct {
		name     string
		events   []traceEvent
		expected string
	}{
		{
			name:     "tool_first",
			events:   []traceEvent{{Type: "dispatch_start"}, {Type: "tool_start", Tool: "read_file"}},
			expected: "tool:read_file",
		},
		{
			name:     "command_first",
			events:   []traceEvent{{Type: "dispatch_start"}, {Type: "command_run", Command: "ls -la"}},
			expected: "command:ls -la",
		},
		{
			name:     "file_read_first",
			events:   []traceEvent{{Type: "dispatch_start"}, {Type: "file_read", Path: "/tmp/main.go"}},
			expected: "read:main.go",
		},
		{
			name:     "no_actions",
			events:   []traceEvent{{Type: "dispatch_start"}, {Type: "dispatch_end"}},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := identifyFirstAction(tt.events)
			if got != tt.expected {
				t.Errorf("identifyFirstAction = %q, want %q", got, tt.expected)
			}
		})
	}
}

// Unused import guard — ensure all imports are used.
var _ = fmt.Sprintf
var _ = time.Now
