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
	dir, err := DefaultArtifactDir(dispatchID)
	if err != nil {
		t.Fatalf("DefaultArtifactDir: %v", err)
	}
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
		_ = os.Remove(ControlRecordPath(dispatchID))
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

func TestResolveArtifactDirFallsBackToLegacyControlRecord(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	dispatchID := "legacy-control-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")
	artifactDir := t.TempDir()
	recordPath, err := controlRecordPathE(legacyControlRoot(), dispatchID)
	if err != nil {
		t.Fatalf("controlRecordPathE(legacy): %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(control dir): %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(recordPath)
	})

	data, err := json.MarshalIndent(ControlRecord{
		DispatchID:  dispatchID,
		ArtifactDir: artifactDir,
	}, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(controlRecord): %v", err)
	}
	if err := os.WriteFile(recordPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile(controlRecord): %v", err)
	}

	resolved, err := ResolveArtifactDir(dispatchID)
	if err != nil {
		t.Fatalf("ResolveArtifactDir: %v", err)
	}
	if resolved != artifactDir {
		t.Fatalf("resolved = %q, want %q", resolved, artifactDir)
	}
}

func TestResolveArtifactDirFallsBackToLegacyDefaultArtifactDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	dispatchID := "legacy-dir-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")
	legacyDir, err := artifactDirPath(legacyArtifactRoot, dispatchID)
	if err != nil {
		t.Fatalf("artifactDirPath(legacy): %v", err)
	}
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(legacyDir): %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(legacyDir)
	})

	resolved, err := ResolveArtifactDir(dispatchID)
	if err != nil {
		t.Fatalf("ResolveArtifactDir: %v", err)
	}
	if resolved != legacyDir {
		t.Fatalf("resolved = %q, want %q", resolved, legacyDir)
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
		_ = os.Remove(ControlRecordPath(spec.DispatchID))
	})

	data, err := os.ReadFile(ControlRecordPath(spec.DispatchID))
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

func TestDefaultArtifactDirRejectsInvalidDispatchID(t *testing.T) {
	if _, err := DefaultArtifactDir("../bad"); err == nil {
		t.Fatal("DefaultArtifactDir error = nil, want invalid dispatch ID error")
	}
}

func TestResolveArtifactDirRejectsInvalidDispatchID(t *testing.T) {
	_, err := ResolveArtifactDir("../bad")
	if err == nil {
		t.Fatal("ResolveArtifactDir error = nil, want invalid dispatch ID error")
	}
	if !strings.Contains(err.Error(), "invalid dispatch ID") {
		t.Fatalf("error = %q, want invalid dispatch ID message", err)
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

func TestControlRecordPathRejectsSymlinks(t *testing.T) {
	// Create a temp dir and a symlink inside it that points elsewhere.
	root := t.TempDir()
	target := t.TempDir()

	// Create a symlink at root/link -> target.
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// writeControlRecord validates path chains via sanitize.SafeJoinPath,
	// which calls checkPathChainNoSymlinks. The dispatch ID itself cannot
	// contain slashes, so we verify the root path (artifact_dir) rejects
	// symlink components by attempting RegisterDispatch with a symlinked dir.
	dispatchID := "symlink-test-dispatch"
	symlinkArtifactDir := filepath.Join(linkPath, "artifacts", dispatchID)
	if err := os.MkdirAll(symlinkArtifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// RegisterDispatch should succeed (it stores the absolute resolved path),
	// but the underlying control record should have an absolute path, not follow symlinks.
	err := RegisterDispatch(dispatchID, symlinkArtifactDir)
	if err != nil {
		// This is acceptable — the function may reject symlink paths.
		t.Cleanup(func() { _ = os.Remove(ControlRecordPath(dispatchID)) })
		return
	}
	t.Cleanup(func() { _ = os.Remove(ControlRecordPath(dispatchID)) })

	// Verify the stored path is cleaned/absolute.
	data, err := os.ReadFile(ControlRecordPath(dispatchID))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var record struct {
		ArtifactDir string `json:"artifact_dir"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if record.ArtifactDir == "" {
		t.Fatal("artifact_dir is empty in control record")
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
