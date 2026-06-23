package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	t.Setenv("HOME", t.TempDir())

	dispatchID := "valid-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  dispatchID,
		Engine:      "codex",
		Model:       "gpt-5.4",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		Prompt:      "recover test",
	}
	annotations := types.DispatchAnnotations{}

	if err := WritePersistentMeta(spec, annotations); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := WriteDispatchRef(artifactDir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}
	artifactPath := filepath.Join(artifactDir, "notes.txt")
	if err := os.WriteFile(artifactPath, []byte("artifact"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	ctx, err := RecoverDispatch(dispatchID)
	if err != nil {
		t.Fatalf("RecoverDispatch: %v", err)
	}
	if ctx.DispatchID != dispatchID {
		t.Fatalf("DispatchID = %q, want %q", ctx.DispatchID, dispatchID)
	}
	if ctx.ArtifactDir != artifactDir {
		t.Fatalf("ArtifactDir = %q, want %q", ctx.ArtifactDir, artifactDir)
	}
	if ctx.OriginalMeta == nil {
		t.Fatal("OriginalMeta is nil")
	}
	if len(ctx.Artifacts) != 1 || ctx.Artifacts[0] != artifactPath {
		t.Fatalf("Artifacts = %#v, want %q", ctx.Artifacts, artifactPath)
	}
}

func TestRecoverDispatchExcludesPrivateDiagnosticsFromPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dispatchID := "private-recovery"
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  dispatchID,
		Engine:      "agy",
		Model:       "agy-provider",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		Prompt:      "recover without private diagnostics",
	}
	if err := WritePersistentMeta(spec, types.DispatchAnnotations{}); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := WriteDispatchRef(artifactDir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}

	publicArtifact := filepath.Join(artifactDir, "worker-notes.md")
	for _, path := range []string{
		filepath.Join(artifactDir, "agy.log"),
		filepath.Join(artifactDir, "raw_stdout.txt"),
		filepath.Join(artifactDir, "provider-internal.trace"),
		publicArtifact,
	} {
		if err := os.WriteFile(path, []byte("artifact"), 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", path, err)
		}
	}

	ctx, err := RecoverDispatch(dispatchID)
	if err != nil {
		t.Fatalf("RecoverDispatch: %v", err)
	}
	if len(ctx.Artifacts) != 1 || ctx.Artifacts[0] != publicArtifact {
		t.Fatalf("Artifacts = %#v, want only %q", ctx.Artifacts, publicArtifact)
	}

	prompt := BuildRecoveryPrompt(ctx, "")
	for _, privateName := range []string{"agy.log", "raw_stdout.txt", "provider-internal.trace"} {
		if strings.Contains(prompt, privateName) {
			t.Fatalf("recovery prompt leaked %s: %q", privateName, prompt)
		}
	}
	if !strings.Contains(prompt, publicArtifact) {
		t.Fatalf("recovery prompt missing public artifact %q: %q", publicArtifact, prompt)
	}
}

func TestRegisterDispatchSpecPersistsMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  "traceable-dispatch",
		ArtifactDir: artifactDir,
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "traceability",
	}

	if err := RegisterDispatchSpec(spec); err != nil {
		t.Fatalf("RegisterDispatchSpec: %v", err)
	}

	meta, err := ReadPersistentMeta(spec.DispatchID)
	if err != nil {
		t.Fatalf("ReadPersistentMeta: %v", err)
	}
	if meta.ArtifactDir != artifactDir {
		t.Fatalf("artifact_dir = %q, want %q", meta.ArtifactDir, artifactDir)
	}
}

func TestResolveArtifactDirUsesPersistentMeta(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dispatchID := "persisted-artifact"
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  dispatchID,
		ArtifactDir: artifactDir,
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "artifact dir",
	}
	if err := RegisterDispatchSpec(spec); err != nil {
		t.Fatalf("RegisterDispatchSpec: %v", err)
	}

	resolved, err := ResolveArtifactDir(dispatchID)
	if err != nil {
		t.Fatalf("ResolveArtifactDir: %v", err)
	}
	if resolved != artifactDir {
		t.Fatalf("resolved = %q, want %q", resolved, artifactDir)
	}
}

func TestResolveControlRecordUsesDispatchDirMeta(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dispatchID := "control-ref"
	spec := &types.DispatchSpec{
		DispatchID:  dispatchID,
		ArtifactDir: t.TempDir(),
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "control record",
	}
	if err := RegisterDispatchSpec(spec); err != nil {
		t.Fatalf("RegisterDispatchSpec: %v", err)
	}

	record, err := ResolveControlRecord(dispatchID[:8])
	if err != nil {
		t.Fatalf("ResolveControlRecord: %v", err)
	}
	if record.DispatchID != dispatchID {
		t.Fatalf("dispatch_id = %q, want %q", record.DispatchID, dispatchID)
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
		OriginalMeta: &DispatchMeta{
			Status:     "timed_out",
			PromptHash: "sha256:1234",
		},
	}

	prompt := BuildRecoveryPrompt(ctx, "")
	if !strings.Contains(prompt, "abc123") {
		t.Fatalf("prompt missing dispatch ID: %q", prompt)
	}
}
