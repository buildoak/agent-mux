package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

// mockAdapter wraps a shell script as a HarnessAdapter for testing.
type mockAdapter struct {
	binary string
}

func (a *mockAdapter) Binary() string { return a.binary }
func (a *mockAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	return []string{"-c", spec.Prompt}
}
func (a *mockAdapter) SupportsResume() bool                { return false }
func (a *mockAdapter) ResumeArgs(sid, msg string) []string { return nil }

func (a *mockAdapter) ParseEvent(line string) (*types.HarnessEvent, error) {
	// Delegate to the real Codex adapter's ParseEvent for testing
	// since our mock scripts emit Codex-format events
	return (&codexParseHelper{}).ParseEvent(line)
}

// codexParseHelper uses the adapter package's codex parsing.
// We inline a minimal version here to avoid circular imports.
type codexParseHelper struct{}

func (h *codexParseHelper) ParseEvent(line string) (*types.HarnessEvent, error) {
	// Import the adapter package in the test
	// Since we're in the engine package, we can't import adapter without cycle.
	// Use a simple inline parser for test purposes.
	if line == "" {
		return nil, nil
	}

	import_json := func() {}
	_ = import_json

	// Minimal JSON parsing for test events
	evt := &types.HarnessEvent{
		Timestamp: time.Now(),
		Raw:       []byte(line),
	}

	// Very simple type extraction
	if len(line) < 10 {
		evt.Kind = types.EventRawPassthrough
		return evt, nil
	}

	if contains(line, `"type":"thread.started"`) {
		evt.Kind = types.EventSessionStart
		// Extract thread_id
		if idx := indexOf(line, `"thread_id":"`); idx >= 0 {
			start := idx + len(`"thread_id":"`)
			end := indexOf(line[start:], `"`)
			if end >= 0 {
				evt.SessionID = line[start : start+end]
			}
		}
		return evt, nil
	}

	if contains(line, `"type":"turn.started"`) {
		return nil, nil // skip
	}

	if contains(line, `"type":"item.completed"`) {
		if contains(line, `"item_type":"agent_message"`) {
			evt.Kind = types.EventResponse
			if idx := indexOf(line, `"content":"`); idx >= 0 {
				start := idx + len(`"content":"`)
				end := indexOf(line[start:], `"`)
				if end >= 0 {
					evt.Text = line[start : start+end]
				}
			}
		} else if contains(line, `"item_type":"file_change"`) {
			evt.Kind = types.EventFileWrite
			if idx := indexOf(line, `"file_path":"`); idx >= 0 {
				start := idx + len(`"file_path":"`)
				end := indexOf(line[start:], `"`)
				if end >= 0 {
					evt.FilePath = line[start : start+end]
				}
			}
		} else if contains(line, `"item_type":"command_execution"`) {
			evt.Kind = types.EventToolEnd
			evt.Tool = "command_execution"
		} else {
			evt.Kind = types.EventToolEnd
		}
		return evt, nil
	}

	if contains(line, `"type":"item.started"`) {
		if contains(line, `"item_type":"command_execution"`) {
			evt.Kind = types.EventCommandRun
			evt.Tool = "command_execution"
		} else {
			evt.Kind = types.EventToolStart
		}
		return evt, nil
	}

	if contains(line, `"type":"item.updated"`) {
		if contains(line, `"item_type":"agent_message"`) {
			evt.Kind = types.EventProgress
		} else {
			return nil, nil
		}
		return evt, nil
	}

	if contains(line, `"type":"turn.completed"`) {
		evt.Kind = types.EventTurnComplete
		evt.Tokens = &types.TokenUsage{Input: 1000, Output: 200, Reasoning: 50}
		return evt, nil
	}

	if contains(line, `"type":"turn.failed"`) {
		evt.Kind = types.EventTurnFailed
		return evt, nil
	}

	if contains(line, `"type":"error"`) {
		evt.Kind = types.EventError
		if idx := indexOf(line, `"code":"`); idx >= 0 {
			start := idx + len(`"code":"`)
			end := indexOf(line[start:], `"`)
			if end >= 0 {
				evt.ErrorCode = line[start : start+end]
			}
		}
		return evt, nil
	}

	evt.Kind = types.EventRawPassthrough
	return evt, nil
}

func contains(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestLoopEngineHappyPath(t *testing.T) {
	// Get absolute path to mock script
	fixtureDir, err := filepath.Abs("../../test/fixtures")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	mockScript := filepath.Join(fixtureDir, "mock-codex.sh")

	if _, err := os.Stat(mockScript); err != nil {
		t.Skipf("mock script not found: %s", mockScript)
	}

	adapter := &mockAdapter{binary: "bash"}
	engine := NewLoopEngine("codex", adapter, nil, io.Discard)

	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01TEST",
		Salt:        "test-fox-one",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Effort:      "high",
		Prompt:      mockScript, // The prompt is the script path for our mock
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if result.Status != types.StatusCompleted {
		t.Errorf("status = %q, want completed. Error: %+v", result.Status, result.Error)
	}
	if result.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if result.DispatchID != "01TEST" {
		t.Errorf("dispatch_id = %q, want 01TEST", result.DispatchID)
	}

	// Check _dispatch_meta.json was written
	metaPath := filepath.Join(artifactDir, "_dispatch_meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("_dispatch_meta.json not found: %v", err)
	}

	// Check events.jsonl was written
	eventsPath := filepath.Join(artifactDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err != nil {
		t.Errorf("events.jsonl not found: %v", err)
	}
}

func TestLoopEngineErrorPath(t *testing.T) {
	fixtureDir, err := filepath.Abs("../../test/fixtures")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	mockScript := filepath.Join(fixtureDir, "mock-codex-error.sh")

	if _, err := os.Stat(mockScript); err != nil {
		t.Skipf("mock script not found: %s", mockScript)
	}

	adapter := &mockAdapter{binary: "bash"}
	engine := NewLoopEngine("codex", adapter, nil, io.Discard)

	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01TESTERR",
		Salt:        "test-fox-two",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Effort:      "high",
		Prompt:      mockScript,
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if result.Status != types.StatusFailed {
		t.Errorf("status = %q, want failed", result.Status)
	}
	if result.Error == nil {
		t.Error("error should not be nil")
	}
}

func TestLoopEngineTimeout(t *testing.T) {
	adapter := &mockAdapter{binary: "bash"}
	engine := NewLoopEngine("codex", adapter, nil, io.Discard)

	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01TESTTMOUT",
		Salt:        "test-fox-three",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Effort:      "high",
		Prompt:      "sleep 30",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		TimeoutSec:  2, // 2 second soft timeout
		GraceSec:    1, // 1 second grace
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if result.Status != types.StatusTimedOut {
		t.Errorf("status = %q, want timed_out", result.Status)
	}
	if !result.Recoverable {
		t.Error("recoverable should be true for timed_out")
	}
}

func TestLoopEngineSoftTimeoutCanStillCompleteWithinGrace(t *testing.T) {
	adapter := &mockAdapter{binary: "bash"}
	engine := NewLoopEngine("codex", adapter, nil, io.Discard)

	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01TESTSOFT",
		Salt:        "test-fox-soft",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Effort:      "high",
		Prompt:      "sleep 2; echo '{\"type\":\"item.completed\",\"item_type\":\"agent_message\",\"content\":\"done\"}'; echo '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		TimeoutSec:  1,
		GraceSec:    3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
}

func TestLoopEngineBinaryNotFound(t *testing.T) {
	adapter := &mockAdapter{binary: "nonexistent-binary-that-does-not-exist"}
	engine := NewLoopEngine("codex", adapter, nil, io.Discard)

	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01TESTNOBIN",
		Salt:        "test-fox-four",
		Engine:      "codex",
		Prompt:      "test",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    5,
	}

	ctx := context.Background()
	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if result.Status != types.StatusFailed {
		t.Errorf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Errorf("error code = %v, want binary_not_found", result.Error)
	}
}
