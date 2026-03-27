package adapter

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestGeminiCLIAllowsCurrentFlashModel(t *testing.T) {
	result, markerPath := runAgentMuxGeminiValidation(t, "gemini-2.5-flash")

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want %q (error=%#v)", result.Status, types.StatusCompleted, result.Error)
	}
	if result.Error != nil {
		t.Fatalf("error = %#v, want nil", result.Error)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("stub gemini binary was not invoked: %v", err)
	}
}

func TestGeminiCLIRejectsRetiredFlashModelBeforeLaunch(t *testing.T) {
	result, markerPath := runAgentMuxGeminiValidation(t, "gemini-3.1-flash")

	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil {
		t.Fatal("error = nil, want model_not_found")
	}
	if result.Error.Code != "model_not_found" {
		t.Fatalf("error.code = %q, want %q", result.Error.Code, "model_not_found")
	}
	if !strings.Contains(result.Error.Suggestion, "gemini-2.5-flash") {
		t.Fatalf("error.suggestion = %q, want to mention gemini-2.5-flash", result.Error.Suggestion)
	}
	if _, err := os.Stat(markerPath); err == nil {
		t.Fatal("stub gemini binary should not be invoked for a retired model slug")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker path: %v", err)
	}
}

func runAgentMuxGeminiValidation(t *testing.T, model string) (*types.DispatchResult, string) {
	t.Helper()

	stubDir := t.TempDir()
	markerPath := filepath.Join(stubDir, "gemini.called")
	stubPath := filepath.Join(stubDir, "gemini")
	stubScript := `#!/bin/sh
: > "$GEMINI_STUB_MARKER"
printf '%s\n' '{"type":"init","session_id":"gemini-stub"}'
printf '%s\n' '{"type":"result","session_id":"gemini-stub","result":"stub ok"}'
`
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write gemini stub: %v", err)
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	cmd := exec.Command("go", "run", "./cmd/agent-mux", "--engine", "gemini", "--model", model, "stub prompt")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GEMINI_STUB_MARKER="+markerPath,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("go run ./cmd/agent-mux: %v\nstderr:\n%s\nstdout:\n%s", err, stderr.String(), stdout.String())
	}

	var result types.DispatchResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal dispatch result: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	return &result, markerPath
}
