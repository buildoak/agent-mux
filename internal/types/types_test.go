package types

import (
	"encoding/json"
	"testing"
)

func TestDispatchResultCompleted(t *testing.T) {
	r := &DispatchResult{
		SchemaVersion:     1,
		Status:            StatusCompleted,
		DispatchID:        "01JQXYZ",
		Response:          "Built the parser.",
		ResponseTruncated: false,
		FullOutput:        nil,
		HandoffSummary:    "Implemented Go parser.",
		Artifacts:         []string{"/tmp/agent-mux/01JQXYZ/src/parser.go"},
		Activity: &DispatchActivity{
			FilesChanged: []string{"src/parser.go"},
			FilesRead:    []string{"src/types.go"},
			CommandsRun:  []string{"go build ./..."},
			ToolCalls:    []string{"Read", "Edit"},
		},
		Metadata: &DispatchMetadata{
			Engine:  "codex",
			Model:   "gpt-5.4",
			Tokens:  &TokenUsage{Input: 45000, Output: 8200},
			Turns:   12,
			CostUSD: 0.23,
		},
		DurationMS: 45200,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DispatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Status != StatusCompleted {
		t.Errorf("status = %q, want %q", decoded.Status, StatusCompleted)
	}
	if decoded.DispatchID != "01JQXYZ" {
		t.Errorf("dispatch_id = %q, want %q", decoded.DispatchID, "01JQXYZ")
	}
	if decoded.FullOutput != nil {
		t.Errorf("full_output should be nil")
	}
	if decoded.FullOutputPath != nil {
		t.Errorf("full_output_path should be nil")
	}
	if decoded.Error != nil {
		t.Errorf("error should be nil for completed")
	}
	if decoded.Partial {
		t.Errorf("partial should be false for completed")
	}
}

func TestDispatchResultTimedOut(t *testing.T) {
	r := &DispatchResult{
		SchemaVersion:  1,
		Status:         StatusTimedOut,
		DispatchID:     "01JQXYZ",
		Response:       "Was building parser when timeout hit.",
		HandoffSummary: "Parser partially built.",
		Artifacts:      []string{"/tmp/agent-mux/01JQXYZ/src/parser.go"},
		Partial:        true,
		Recoverable:    true,
		Reason:         "Soft timeout at 600s.",
		Activity:       &DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		Metadata:       &DispatchMetadata{Engine: "codex", Model: "gpt-5.4", Tokens: &TokenUsage{Input: 1000, Output: 500}},
		DurationMS:     660000,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DispatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Status != StatusTimedOut {
		t.Errorf("status = %q, want %q", decoded.Status, StatusTimedOut)
	}
	if !decoded.Partial {
		t.Error("partial should be true for timed_out")
	}
	if !decoded.Recoverable {
		t.Error("recoverable should be true for timed_out")
	}
}

func TestDispatchResultFailed(t *testing.T) {
	r := &DispatchResult{
		SchemaVersion:  1,
		Status:         StatusFailed,
		DispatchID:     "01JQXYZ",
		Response:       "",
		HandoffSummary: "",
		Artifacts:      []string{},
		Error: &DispatchError{
			Code:      "model_not_found",
			Message:   "Model 'gpt-99' not found.",
			Hint:      "The selected model is not available for the current engine.",
			Example:   "Retry with a supported model. Example: agent-mux -e codex -m gpt-5.4 --cwd /repo \"<prompt>\".",
			Retryable: true,
		},
		Activity:   &DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		Metadata:   &DispatchMetadata{Engine: "codex", Model: "", Tokens: &TokenUsage{}},
		DurationMS: 430,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DispatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Status != StatusFailed {
		t.Errorf("status = %q, want %q", decoded.Status, StatusFailed)
	}
	if decoded.Error == nil {
		t.Fatal("error should not be nil for failed")
	}
	if decoded.Error.Code != "model_not_found" {
		t.Errorf("error.code = %q, want %q", decoded.Error.Code, "model_not_found")
	}
	if !decoded.Error.Retryable {
		t.Error("error.retryable should be true")
	}
	if decoded.Error.Hint != "The selected model is not available for the current engine." {
		t.Errorf("error.hint = %q", decoded.Error.Hint)
	}
	if decoded.Error.Example != "Retry with a supported model. Example: agent-mux -e codex -m gpt-5.4 --cwd /repo \"<prompt>\"." {
		t.Errorf("error.example = %q", decoded.Error.Example)
	}
}

func TestFullOutputNullMarshal(t *testing.T) {
	r := &DispatchResult{
		SchemaVersion: 1,
		Status:        StatusCompleted,
		FullOutput:    nil,
		Artifacts:     []string{},
		Activity:      &DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		Metadata:      &DispatchMetadata{Engine: "codex", Tokens: &TokenUsage{}},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// full_output should be null in JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if string(raw["full_output"]) != "null" {
		t.Errorf("full_output = %s, want null", string(raw["full_output"]))
	}
}

func TestFullOutputStringMarshal(t *testing.T) {
	path := "/tmp/agent-mux/01JQXYZ/full_output.md"
	r := &DispatchResult{
		SchemaVersion:  1,
		Status:         StatusCompleted,
		FullOutput:     &path,
		FullOutputPath: &path,
		Artifacts:      []string{},
		Activity:       &DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		Metadata:       &DispatchMetadata{Engine: "codex", Tokens: &TokenUsage{}},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DispatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.FullOutput == nil || *decoded.FullOutput != path {
		t.Errorf("full_output = %v, want %q", decoded.FullOutput, path)
	}
	if decoded.FullOutputPath == nil || *decoded.FullOutputPath != path {
		t.Errorf("full_output_path = %v, want %q", decoded.FullOutputPath, path)
	}
}

func TestOmitemptyFields(t *testing.T) {
	r := &DispatchResult{
		SchemaVersion: 1,
		Status:        StatusCompleted,
		Artifacts:     []string{},
		Activity:      &DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		Metadata:      &DispatchMetadata{Engine: "codex", Tokens: &TokenUsage{}},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// These should be omitted when zero-valued
	for _, key := range []string{"partial", "recoverable", "reason", "error", "full_output_path"} {
		if _, exists := raw[key]; exists {
			t.Errorf("%q should be omitted when zero-valued, got %s", key, string(raw[key]))
		}
	}

	// These should always be present (no omitempty)
	for _, key := range []string{"schema_version", "status", "dispatch_id", "response", "response_truncated", "full_output", "handoff_summary", "artifacts", "activity", "metadata", "duration_ms"} {
		if _, exists := raw[key]; !exists {
			t.Errorf("%q should always be present", key)
		}
	}
}

func TestDispatchSpecRoundTrip(t *testing.T) {
	spec := &DispatchSpec{
		DispatchID:  "01JQXYZ",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Effort:      "high",
		Prompt:      "Build the parser",
		Cwd:         "/path/to/project",
		ContextFile: "/tmp/context.md",
		ArtifactDir: "/tmp/agent-mux/01JQXYZ/",
		MaxDepth:    2,
		Depth:       1,
		FullAccess:  true,
		GraceSec:    60,
		EngineOpts:  map[string]any{"sandbox": "danger-full-access"},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DispatchSpec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Engine != "codex" {
		t.Errorf("engine = %q, want %q", decoded.Engine, "codex")
	}
	if decoded.ContextFile != "/tmp/context.md" {
		t.Errorf("context_file = %q, want %q", decoded.ContextFile, "/tmp/context.md")
	}
	if decoded.FullAccess != true {
		t.Error("full_access should be true")
	}
	if decoded.EngineOpts["sandbox"] != "danger-full-access" {
		t.Errorf("engine_opts.sandbox = %v, want danger-full-access", decoded.EngineOpts["sandbox"])
	}
}

func TestDispatchSpecJSONTagNames(t *testing.T) {
	spec := &DispatchSpec{
		DispatchID:  "01JQXYZ",
		Engine:      "codex",
		Effort:      "high",
		Prompt:      "test",
		Cwd:         "/tmp",
		ContextFile: "/tmp/context.md",
		ArtifactDir: "/tmp/agent-mux/01JQXYZ/",
		MaxDepth:    2,
		Depth:       1,
		FullAccess:  true,
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	expectedKeys := []string{
		"dispatch_id", "engine", "effort", "prompt", "cwd",
		"context_file", "artifact_dir", "max_depth", "depth", "full_access",
	}
	for _, key := range expectedKeys {
		if _, exists := raw[key]; !exists {
			t.Errorf("expected JSON key %q not found", key)
		}
	}
	if _, exists := raw["coordinator"]; exists {
		t.Errorf("legacy JSON key %q should be omitted when marshaling", "coordinator")
	}
}

func TestTokenUsageOmitempty(t *testing.T) {
	tok := &TokenUsage{Input: 100, Output: 50}
	data, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// reasoning=0 should be omitted
	if _, exists := raw["reasoning"]; exists {
		t.Error("reasoning should be omitted when zero")
	}
	if _, exists := raw["cache_read"]; exists {
		t.Error("cache_read should be omitted when zero")
	}
	if _, exists := raw["cache_write"]; exists {
		t.Error("cache_write should be omitted when zero")
	}

	// With reasoning set
	tok.Reasoning = 1200
	data, err = json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, exists := raw["reasoning"]; !exists {
		t.Error("reasoning should be present when non-zero")
	}
	tok.CacheRead = 12
	tok.CacheWrite = 34
	data, err = json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, exists := raw["cache_read"]; !exists {
		t.Error("cache_read should be present when non-zero")
	}
	if _, exists := raw["cache_write"]; !exists {
		t.Error("cache_write should be present when non-zero")
	}
}
