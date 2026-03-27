package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestGenerateSalt(t *testing.T) {
	salt := GenerateSalt()
	parts := strings.Split(salt, "-")
	if len(parts) != 3 {
		t.Errorf("salt should have 3 parts, got %d: %q", len(parts), salt)
	}
}

func TestEnsureArtifactDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := EnsureArtifactDir(dir); err != nil {
		t.Fatalf("EnsureArtifactDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("should be a directory")
	}
}

func TestDefaultTraceToken(t *testing.T) {
	const dispatchID = "01JQXYZ"
	if got := DefaultTraceToken(dispatchID); got != "AGENT_MUX_GO_01JQXYZ" {
		t.Fatalf("DefaultTraceToken(%q) = %q, want %q", dispatchID, got, "AGENT_MUX_GO_01JQXYZ")
	}
}

func TestEnsureTraceabilityDerivesMissingFields(t *testing.T) {
	spec := &types.DispatchSpec{DispatchID: "01JQXYZ"}

	EnsureTraceability(spec)

	if spec.Salt == "" {
		t.Fatal("salt should be populated")
	}
	if spec.TraceToken != "AGENT_MUX_GO_01JQXYZ" {
		t.Fatalf("trace_token = %q, want %q", spec.TraceToken, "AGENT_MUX_GO_01JQXYZ")
	}
}

func TestPromptPreamble(t *testing.T) {
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		TraceToken:  "AGENT_MUX_GO_01JQXYZ",
		ArtifactDir: "/tmp/agent-mux/01JQXYZ",
	}

	lines := PromptPreamble(spec)
	want := []string{
		"Trace token: AGENT_MUX_GO_01JQXYZ",
		"Dispatch ID: 01JQXYZ",
		"Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR.",
	}
	if len(lines) != len(want) {
		t.Fatalf("len(lines) = %d, want %d (%v)", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestWriteAndUpdateDispatchMeta(t *testing.T) {
	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID: "01JQXYZ",
		Salt:       "coral-fox-nine",
		TraceToken: "AGENT_MUX_GO_01JQXYZ",
		Engine:     "codex",
		Model:      "gpt-5.4",
		Prompt:     "Build the parser",
		Cwd:        "/path/to/project",
	}

	if err := WriteDispatchMeta(dir, spec); err != nil {
		t.Fatalf("WriteDispatchMeta: %v", err)
	}

	path := filepath.Join(dir, "_dispatch_meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var meta DispatchMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if meta.DispatchID != "01JQXYZ" {
		t.Errorf("dispatch_id = %q, want 01JQXYZ", meta.DispatchID)
	}
	if meta.Engine != "codex" {
		t.Errorf("engine = %q, want codex", meta.Engine)
	}
	if meta.TraceToken != "AGENT_MUX_GO_01JQXYZ" {
		t.Errorf("trace_token = %q, want AGENT_MUX_GO_01JQXYZ", meta.TraceToken)
	}
	if !strings.HasPrefix(meta.PromptHash, "sha256:") {
		t.Errorf("prompt_hash should start with sha256:, got %q", meta.PromptHash)
	}

	// Update
	if err := UpdateDispatchMeta(dir, "completed", []string{"/tmp/parser.go"}); err != nil {
		t.Fatalf("UpdateDispatchMeta: %v", err)
	}

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after update: %v", err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal after update: %v", err)
	}
	if meta.Status != "completed" {
		t.Errorf("status = %q, want completed", meta.Status)
	}
	if meta.EndedAt == "" {
		t.Error("ended_at should be set")
	}
}

func TestTruncateResponse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		max       int
		wantTrunc bool
	}{
		{"no truncation needed", "short", 100, false},
		{"exact limit", "hello", 5, false},
		{"unlimited", "anything", 0, false},
		{"truncate at sentence", "First sentence. Second sentence. Third sentence.", 20, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, truncated := TruncateResponse(tt.input, tt.max)
			if truncated != tt.wantTrunc {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTrunc)
			}
			if tt.wantTrunc && len(result) > tt.max {
				t.Errorf("result length %d exceeds max %d", len(result), tt.max)
			}
			if !tt.wantTrunc && result != tt.input {
				t.Errorf("result = %q, want %q", result, tt.input)
			}
		})
	}
}

func TestExtractHandoffSummary(t *testing.T) {
	// With ## Summary section
	withSummary := "Some text\n\n## Summary\n\nThis is the summary.\n\n## Next Section\n\nMore text."
	summary := ExtractHandoffSummary(withSummary, 2000)
	if !strings.Contains(summary, "This is the summary.") {
		t.Errorf("should contain summary section, got %q", summary)
	}
	if strings.Contains(summary, "More text") {
		t.Errorf("should not contain next section")
	}

	// Without section header - uses first chars
	withoutSummary := "Just some plain text response."
	summary = ExtractHandoffSummary(withoutSummary, 2000)
	if summary != "Just some plain text response." {
		t.Errorf("should return full text when short, got %q", summary)
	}
}

func TestFuzzyMatchModel(t *testing.T) {
	models := []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"}

	tests := []struct {
		input string
		want  string
	}{
		{"gpt-5.4", "gpt-5.4"},                               // exact match
		{"gpt-5.3", "gpt-5.4"},                               // close match
		{"gpt-5.4-mni", "gpt-5.4-mini"},                      // typo
		{"completely-wrong-model-name-that-is-very-far", ""}, // too far
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := FuzzyMatchModel(tt.input, models)
			if got != tt.want {
				t.Errorf("FuzzyMatchModel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewDispatchError(t *testing.T) {
	// Known error code
	err := NewDispatchError("model_not_found", "Model 'gpt-99' not found.", "Did you mean 'gpt-5.4'?")
	if err.Code != "model_not_found" {
		t.Errorf("code = %q, want model_not_found", err.Code)
	}
	if !err.Retryable {
		t.Error("model_not_found should be retryable")
	}
	if err.Suggestion != "Did you mean 'gpt-5.4'?" {
		t.Errorf("suggestion = %q", err.Suggestion)
	}

	// Unknown error code
	err = NewDispatchError("unknown_error", "Something broke", "Try again")
	if err.Code != "unknown_error" {
		t.Errorf("code = %q, want unknown_error", err.Code)
	}
	if err.Retryable {
		t.Error("unknown error should not be retryable by default")
	}
}

func TestBuildCompletedResult(t *testing.T) {
	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		Salt:        "coral-fox-nine",
		TraceToken:  "AGENT_MUX_GO_01JQXYZ",
		ArtifactDir: dir,
	}
	activity := &types.DispatchActivity{
		FilesChanged: []string{"a.go"},
		FilesRead:    []string{},
		CommandsRun:  []string{},
		ToolCalls:    []string{},
	}
	metadata := &types.DispatchMetadata{
		Engine: "codex",
		Model:  "gpt-5.4",
		Tokens: &types.TokenUsage{Input: 100, Output: 50},
	}

	result := BuildCompletedResult(spec, "Done.", activity, metadata, 5000, 0)

	if result.Status != types.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if result.TraceToken != "AGENT_MUX_GO_01JQXYZ" {
		t.Errorf("trace_token = %q, want AGENT_MUX_GO_01JQXYZ", result.TraceToken)
	}
	if result.Response != "Done." {
		t.Errorf("response = %q, want 'Done.'", result.Response)
	}
	if result.ResponseTruncated {
		t.Error("response should not be truncated")
	}
}

func TestBuildTimedOutResult(t *testing.T) {
	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		Salt:        "coral-fox-nine",
		TraceToken:  "AGENT_MUX_GO_01JQXYZ",
		ArtifactDir: dir,
	}
	activity := &types.DispatchActivity{
		FilesChanged: []string{},
		FilesRead:    []string{},
		CommandsRun:  []string{},
		ToolCalls:    []string{},
	}
	metadata := &types.DispatchMetadata{
		Engine: "codex",
		Tokens: &types.TokenUsage{},
	}

	result := BuildTimedOutResult(spec, "partial", "Soft timeout at 600s.", activity, metadata, 660000)

	if result.Status != types.StatusTimedOut {
		t.Errorf("status = %q, want timed_out", result.Status)
	}
	if !result.Partial {
		t.Error("partial should be true")
	}
	if !result.Recoverable {
		t.Error("recoverable should be true")
	}
}

func TestBuildFailedResult(t *testing.T) {
	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		Salt:        "coral-fox-nine",
		TraceToken:  "AGENT_MUX_GO_01JQXYZ",
		ArtifactDir: dir,
	}
	activity := &types.DispatchActivity{
		FilesChanged: []string{},
		FilesRead:    []string{},
		CommandsRun:  []string{},
		ToolCalls:    []string{},
	}
	metadata := &types.DispatchMetadata{
		Engine: "codex",
		Tokens: &types.TokenUsage{},
	}
	dispatchErr := NewDispatchError("model_not_found", "Model not found.", "Try gpt-5.4")

	result := BuildFailedResult(spec, dispatchErr, activity, metadata, 430)

	if result.Status != types.StatusFailed {
		t.Errorf("status = %q, want failed", result.Status)
	}
	if result.Error == nil {
		t.Fatal("error should not be nil")
	}
	if result.Error.Code != "model_not_found" {
		t.Errorf("error.code = %q, want model_not_found", result.Error.Code)
	}
}

func TestScanArtifactsRecursive(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dir, "_dispatch_meta.json"),
		filepath.Join(dir, "events.jsonl"),
		filepath.Join(nested, "result.txt"),
	} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}

	artifacts := ScanArtifacts(dir)
	if len(artifacts) != 1 || artifacts[0] != filepath.Join(nested, "result.txt") {
		t.Fatalf("ScanArtifacts = %#v, want nested artifact only", artifacts)
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"kitten", "sitting", 3},
	}

	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
