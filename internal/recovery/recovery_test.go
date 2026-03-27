package recovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/types"
)

func TestRecoverDispatch_NotFound(t *testing.T) {
	dispatchID := "missing-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")

	_, err := RecoverDispatch(dispatchID)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no artifact directory") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestRecoverDispatch_ValidDir(t *testing.T) {
	dispatchID := "valid-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")
	dir := DefaultArtifactDir(dispatchID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	meta := `{
  "dispatch_id": "` + dispatchID + `",
  "dispatch_salt": "salt",
  "started_at": "2026-03-26T00:00:00Z",
  "engine": "codex",
  "model": "gpt-5.4",
  "prompt_hash": "sha256:deadbeef",
  "cwd": "/tmp",
  "status": "failed"
}`
	if err := os.WriteFile(filepath.Join(dir, "_dispatch_meta.json"), []byte(meta), 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	artifactPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(artifactPath, []byte("artifact"), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	ctx, err := RecoverDispatch(dispatchID)
	if err != nil {
		t.Fatalf("RecoverDispatch: %v", err)
	}
	if ctx.DispatchID != dispatchID {
		t.Fatalf("DispatchID = %q, want %q", ctx.DispatchID, dispatchID)
	}
	if ctx.ArtifactDir != dir {
		t.Fatalf("ArtifactDir = %q, want %q", ctx.ArtifactDir, dir)
	}
	if ctx.OriginalMeta == nil {
		t.Fatal("OriginalMeta is nil")
	}
	if len(ctx.Artifacts) == 0 {
		t.Fatal("expected artifacts")
	}
	if ctx.Artifacts[0] != artifactPath {
		t.Fatalf("Artifacts[0] = %q, want %q", ctx.Artifacts[0], artifactPath)
	}
}

func TestResolveArtifactDirUsesAbsoluteRegisteredPath(t *testing.T) {
	startDir := t.TempDir()
	otherDir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(startDir); err != nil {
		t.Fatalf("chdir startDir: %v", err)
	}
	defer func() {
		if err := os.Chdir(prevWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	dispatchID := "relative-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")
	relativeDir := filepath.Join("artifacts", "custom")
	absoluteDir := filepath.Join(startDir, relativeDir)
	if err := os.MkdirAll(absoluteDir, 0755); err != nil {
		t.Fatalf("mkdir absoluteDir: %v", err)
	}
	if err := RegisterDispatch(dispatchID, relativeDir); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(controlRecordPath(dispatchID))
	})

	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("chdir otherDir: %v", err)
	}

	resolved, err := ResolveArtifactDir(dispatchID)
	if err != nil {
		t.Fatalf("ResolveArtifactDir: %v", err)
	}
	resolvedReal, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		t.Fatalf("EvalSymlinks(resolved): %v", err)
	}
	absoluteReal, err := filepath.EvalSymlinks(absoluteDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(absoluteDir): %v", err)
	}
	if resolvedReal != absoluteReal {
		t.Fatalf("resolved = %q (%q), want %q (%q)", resolved, resolvedReal, absoluteDir, absoluteReal)
	}
}

func TestRegisterDispatchSpecPersistsTraceability(t *testing.T) {
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "traceable-dispatch",
		Salt:        "coral-fox-nine",
		TraceToken:  "AGENT_MUX_GO_traceable-dispatch",
		ArtifactDir: artifactDir,
	}

	if err := RegisterDispatchSpec(spec); err != nil {
		t.Fatalf("RegisterDispatchSpec: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(controlRecordPath(spec.DispatchID))
	})

	data, err := os.ReadFile(controlRecordPath(spec.DispatchID))
	if err != nil {
		t.Fatalf("ReadFile(controlRecord): %v", err)
	}
	var record struct {
		DispatchID   string `json:"dispatch_id"`
		ArtifactDir  string `json:"artifact_dir"`
		DispatchSalt string `json:"dispatch_salt"`
		TraceToken   string `json:"trace_token"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal(controlRecord): %v", err)
	}
	if record.DispatchSalt != spec.Salt {
		t.Fatalf("dispatch_salt = %q, want %q", record.DispatchSalt, spec.Salt)
	}
	if record.TraceToken != spec.TraceToken {
		t.Fatalf("trace_token = %q, want %q", record.TraceToken, spec.TraceToken)
	}
}

func TestBuildRecoveryPrompt_ContainsDispatchID(t *testing.T) {
	ctx := &RecoveryContext{
		DispatchID: "abc123",
		OriginalMeta: &dispatch.DispatchMeta{
			Status:     "timed_out",
			PromptHash: "sha256:1234",
		},
	}

	prompt := BuildRecoveryPrompt(ctx, "")
	if !strings.Contains(prompt, "abc123") {
		t.Fatalf("prompt missing dispatch ID: %q", prompt)
	}
}

func TestBuildRecoveryPrompt_ContainsArtifacts(t *testing.T) {
	ctx := &RecoveryContext{
		DispatchID: "abc123",
		OriginalMeta: &dispatch.DispatchMeta{
			Status:     "timed_out",
			PromptHash: "sha256:1234",
		},
		Artifacts: []string{"/tmp/agent-mux/abc123/out.txt", "/tmp/agent-mux/abc123/log.txt"},
	}

	prompt := BuildRecoveryPrompt(ctx, "")
	for _, artifact := range ctx.Artifacts {
		if !strings.Contains(prompt, artifact) {
			t.Fatalf("prompt missing artifact %q: %q", artifact, prompt)
		}
	}
}
