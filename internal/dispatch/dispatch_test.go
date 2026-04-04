package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/buildoak/agent-mux/internal/types"
)

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

func TestPromptPreamble(t *testing.T) {
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		ContextFile: "/tmp/context.md",
		ArtifactDir: "/tmp/agent-mux/01JQXYZ",
	}

	lines := PromptPreamble(spec)
	want := []string{
		"Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting.",
		"If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.",
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

func TestReadDispatchMetaRoundTripsThroughPersistentStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "Build the parser",
		Cwd:         "/path/to/project",
		ArtifactDir: dir,
	}
	annotations := types.DispatchAnnotations{}

	if err := WritePersistentMeta(spec, annotations); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := WriteDispatchRef(dir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}
	result := &types.DispatchResult{
		SchemaVersion: 1,
		Status:        types.StatusCompleted,
		DispatchID:    spec.DispatchID,
		Artifacts:     []string{"/tmp/parser.go"},
		Metadata: &types.DispatchMetadata{
			Engine: spec.Engine,
			Model:  spec.Model,
			Tokens: &types.TokenUsage{},
		},
	}
	if err := WritePersistentResult(spec, annotations, result, "Done.", "2026-04-03T10:00:00Z", "2026-04-03T10:05:00Z"); err != nil {
		t.Fatalf("WritePersistentResult: %v", err)
	}

	meta, err := ReadDispatchMeta(dir)
	if err != nil {
		t.Fatalf("ReadDispatchMeta: %v", err)
	}
	if meta.DispatchID != "01JQXYZ" {
		t.Errorf("dispatch_id = %q, want 01JQXYZ", meta.DispatchID)
	}
	if meta.Engine != "codex" {
		t.Errorf("engine = %q, want codex", meta.Engine)
	}
	if !strings.HasPrefix(meta.PromptHash, "sha256:") {
		t.Errorf("prompt_hash should start with sha256:, got %q", meta.PromptHash)
	}
	if meta.Status != "completed" {
		t.Errorf("status = %q, want completed", meta.Status)
	}
	if meta.EndedAt != "2026-04-03T10:05:00Z" {
		t.Errorf("ended_at = %q, want 2026-04-03T10:05:00Z", meta.EndedAt)
	}
	if len(meta.Artifacts) != 1 || meta.Artifacts[0] != "/tmp/parser.go" {
		t.Errorf("artifacts = %#v, want [/tmp/parser.go]", meta.Artifacts)
	}
	if _, err := os.Stat(filepath.Join(dir, "_dispatch_meta.json")); !os.IsNotExist(err) {
		t.Fatalf("dispatch meta file stat error = %v, want not exists", err)
	}
}

func TestReadDispatchMetaFallsBackToLegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := DispatchMeta{
		DispatchID: "legacy-dispatch",
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
		Engine:     "codex",
		Model:      "gpt-5.4",
		PromptHash: "sha256:deadbeef",
		Cwd:        "/path/to/project",
		Status:     "failed",
		Artifacts:  []string{"/tmp/legacy.txt"},
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_dispatch_meta.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	meta, err := ReadDispatchMeta(dir)
	if err == nil {
		if meta.DispatchID != legacy.DispatchID {
			t.Fatalf("dispatch_id = %q, want %q", meta.DispatchID, legacy.DispatchID)
		}
		if meta.PromptHash != legacy.PromptHash {
			t.Fatalf("prompt_hash = %q, want %q", meta.PromptHash, legacy.PromptHash)
		}
		if meta.Status != legacy.Status {
			t.Fatalf("status = %q, want %q", meta.Status, legacy.Status)
		}
		return
	}
	t.Fatalf("ReadDispatchMeta: %v", err)
}

func TestReadDispatchMetaPrefersDispatchRefOverLegacyFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "preferred-dispatch",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "new path",
		Cwd:         "/new/path",
		ArtifactDir: dir,
	}
	if err := WritePersistentMeta(spec, types.DispatchAnnotations{}); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := WriteDispatchRef(dir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}

	legacy := DispatchMeta{
		DispatchID: "legacy-dispatch",
		StartedAt:  "2026-01-01T00:00:00Z",
		Engine:     "claude",
		Model:      "legacy-model",
		PromptHash: "sha256:legacy",
		Cwd:        "/legacy/path",
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "_dispatch_meta.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	meta, err := ReadDispatchMeta(dir)
	if err != nil {
		t.Fatalf("ReadDispatchMeta: %v", err)
	}
	if meta.DispatchID != spec.DispatchID {
		t.Fatalf("dispatch_id = %q, want %q", meta.DispatchID, spec.DispatchID)
	}
	if meta.Cwd != spec.Cwd {
		t.Fatalf("cwd = %q, want %q", meta.Cwd, spec.Cwd)
	}
	if meta.PromptHash == legacy.PromptHash {
		t.Fatalf("prompt_hash = %q, should come from persistent meta", meta.PromptHash)
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
	err := NewDispatchError("model_not_found", "Model 'gpt-99' not found.", "Did you mean 'gpt-5.4'?")
	if err.Code != "model_not_found" {
		t.Errorf("code = %q, want model_not_found", err.Code)
	}
	if !err.Retryable {
		t.Error("model_not_found should be retryable")
	}
	if err.Hint != "Did you mean 'gpt-5.4'?" {
		t.Errorf("hint = %q", err.Hint)
	}
	if err.Example != "" {
		t.Errorf("example = %q, want empty for override path", err.Example)
	}

	err = NewDispatchError("frozen_killed", "", "")
	if err.Message != "Worker killed after prolonged silence." {
		t.Errorf("message = %q", err.Message)
	}
	if err.Hint == "" || err.Example == "" {
		t.Fatalf("catalog-backed hint/example should both be populated: %+v", err)
	}
	err = NewDispatchError("unknown_error", "Something broke", "Try again")
	if err.Code != "unknown_error" {
		t.Errorf("code = %q, want unknown_error", err.Code)
	}
	if err.Retryable {
		t.Error("unknown error should not be retryable by default")
	}
	if err.Hint != "Try again" || err.Example != "" {
		t.Errorf("unknown override path = %+v", err)
	}
}

func TestBuildCompletedResult(t *testing.T) {
	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
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

	result := BuildCompletedResult(spec, "Done.", activity, metadata, 5000)

	if result.Status != types.StatusCompleted {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", result.SchemaVersion)
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

	result := BuildFailedResult(spec, "", dispatchErr, activity, metadata, 430)

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

func TestBuildFailedResultKeepsFullSummary(t *testing.T) {
	dir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "01JQXYZ",
		ArtifactDir: dir,
	}
	activity := &types.DispatchActivity{}
	metadata := &types.DispatchMetadata{Engine: "codex", Tokens: &types.TokenUsage{}}
	dispatchErr := NewDispatchError("internal_error", "", "")
	response := "## Summary\nunicode canary: 你好 alpha beta gamma.\n## Details\nmore"

	result := BuildFailedResult(spec, response, dispatchErr, activity, metadata, 430)

	if !strings.Contains(result.HandoffSummary, "unicode canary: 你好") {
		t.Fatalf("handoff_summary = %q, want summary from full response", result.HandoffSummary)
	}
	if !utf8.ValidString(result.Response) {
		t.Fatalf("response = %q, want valid UTF-8", result.Response)
	}
	if result.Response != response {
		t.Fatalf("response = %q, want full response", result.Response)
	}
}

func TestTruncateAtBoundaryIsRuneSafe(t *testing.T) {
	input := "你好世界。\nSecond line."
	got := truncateAtBoundary(input, 3)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateAtBoundary returned invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) > 3 {
		t.Fatalf("rune count = %d, want <= 3", utf8.RuneCountInString(got))
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
