package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/types"
)

const legacyArtifactRoot = "/tmp/agent-mux"

type RecoveryContext struct {
	DispatchID   string
	OriginalMeta *DispatchMeta
	Artifacts    []string
	ArtifactDir  string
}

type ControlRecord struct {
	DispatchID  string `json:"dispatch_id"`
	ArtifactDir string `json:"artifact_dir"`
}

func DefaultArtifactDir(dispatchID string) (string, error) {
	return artifactDirPath(currentArtifactRoot(), dispatchID)
}

func RegisterDispatch(dispatchID, artifactDir string) error {
	return registerDispatchMeta(&types.DispatchSpec{
		DispatchID:  dispatchID,
		ArtifactDir: strings.TrimSpace(artifactDir),
	})
}

func RegisterDispatchSpec(spec *types.DispatchSpec) error {
	if spec == nil {
		return fmt.Errorf("missing dispatch spec for registration")
	}
	return registerDispatchMeta(spec)
}

func ResolveArtifactDir(dispatchID string) (string, error) {
	dispatchID, err := validateDispatchID(dispatchID)
	if err != nil {
		return "", err
	}

	if meta, err := ReadPersistentMeta(dispatchID); err == nil {
		if artifactDir := strings.TrimSpace(meta.ArtifactDir); artifactDir != "" {
			return filepath.Clean(artifactDir), nil
		}
	}

	currentDir, err := artifactDirPath(currentArtifactRoot(), dispatchID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(currentDir); err == nil {
		return currentDir, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat artifact directory %q: %w", currentDir, err)
	}

	legacyDir, err := artifactDirPath(legacyArtifactRoot, dispatchID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(legacyDir); err == nil {
		return legacyDir, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat legacy artifact directory %q: %w", legacyDir, err)
	}

	return "", fmt.Errorf("no artifact directory found for dispatch %q", dispatchID)
}

func ResolveControlRecord(ref string) (*ControlRecord, error) {
	record, err := FindDispatchRecordByRef(ref)
	if err != nil || record == nil {
		return nil, err
	}
	return &ControlRecord{
		DispatchID:  record.ID,
		ArtifactDir: record.ArtifactDir,
	}, nil
}

func RecoverDispatch(dispatchID string) (*RecoveryContext, error) {
	dir, err := ResolveArtifactDir(dispatchID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no artifact directory found for dispatch %q at %s", dispatchID, dir)
		}
		return nil, fmt.Errorf("stat artifact directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("artifact path %q is not a directory", dir)
	}

	meta, err := ReadDispatchMeta(dir)
	if err != nil {
		if persistentMeta, persistentErr := ReadPersistentMeta(dispatchID); persistentErr == nil {
			meta = &DispatchMeta{
				DispatchID: persistentMeta.DispatchID,
				SessionID:  persistentMeta.SessionID,
				StartedAt:  persistentMeta.StartedAt,
				Engine:     persistentMeta.Engine,
				Model:      persistentMeta.Model,
				Cwd:        persistentMeta.Cwd,
			}
		} else {
			return nil, fmt.Errorf("read dispatch meta for %q: %w", dispatchID, err)
		}
	}

	return &RecoveryContext{
		DispatchID:   dispatchID,
		OriginalMeta: meta,
		Artifacts:    ScanArtifacts(dir),
		ArtifactDir:  dir,
	}, nil
}

func currentArtifactRoot() string {
	return sanitize.SecureArtifactRoot()
}

func ControlRecordPath(dispatchID string) string {
	dir, err := DispatchDir(strings.TrimSpace(dispatchID))
	if err != nil {
		root := DispatchesDir()
		return filepath.Join(root, strings.TrimSpace(dispatchID), "meta.json")
	}
	return filepath.Join(dir, "meta.json")
}

func artifactDirPath(root, dispatchID string) (string, error) {
	dispatchID, err := validateDispatchID(dispatchID)
	if err != nil {
		return "", err
	}

	path, err := sanitize.SafeJoinPath(root, dispatchID)
	if err != nil {
		return "", fmt.Errorf("build artifact dir for dispatch %q: %w", dispatchID, err)
	}
	return path, nil
}

func validateDispatchID(dispatchID string) (string, error) {
	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return "", fmt.Errorf("missing dispatch ID")
	}
	if err := sanitize.ValidateDispatchID(dispatchID); err != nil {
		return "", fmt.Errorf("invalid dispatch ID %q: %w", dispatchID, err)
	}
	return dispatchID, nil
}

func registerDispatchMeta(spec *types.DispatchSpec) error {
	if spec == nil {
		return fmt.Errorf("missing dispatch spec")
	}
	artifactDir := strings.TrimSpace(spec.ArtifactDir)
	if artifactDir == "" {
		return fmt.Errorf("missing artifact dir for dispatch %q", spec.DispatchID)
	}
	artifactDirAbs, err := filepath.Abs(artifactDir)
	if err != nil {
		return fmt.Errorf("resolve artifact dir %q: %w", artifactDir, err)
	}
	specCopy := *spec
	specCopy.ArtifactDir = filepath.Clean(artifactDirAbs)
	return WritePersistentMeta(&specCopy, types.DispatchAnnotations{})
}

func BuildRecoveryPrompt(ctx *RecoveryContext, additionalInstruction string) string {
	var b strings.Builder
	status := "unknown"
	engine := "unknown"
	model := "unknown"
	if ctx != nil && ctx.OriginalMeta != nil && ctx.OriginalMeta.Status != "" {
		status = ctx.OriginalMeta.Status
	}
	if ctx != nil && ctx.OriginalMeta != nil && ctx.OriginalMeta.Engine != "" {
		engine = ctx.OriginalMeta.Engine
	}
	if ctx != nil && ctx.OriginalMeta != nil && ctx.OriginalMeta.Model != "" {
		model = ctx.OriginalMeta.Model
	}

	fmt.Fprintf(&b, "You are continuing a previous dispatch (ID: %s).\n", ctx.DispatchID)
	fmt.Fprintf(&b, "Engine: %s, Model: %s\n", engine, model)
	fmt.Fprintf(&b, "Previous status: %s.\n", status)
	b.WriteString("Artifacts from previous run:\n")
	for _, artifact := range ctx.Artifacts {
		fmt.Fprintf(&b, "- %s\n", artifact)
	}
	if len(ctx.Artifacts) == 0 {
		b.WriteString("- none\n")
	}
	b.WriteString("\n")

	promptHashNote := "unknown"
	if ctx != nil && ctx.OriginalMeta != nil && ctx.OriginalMeta.PromptHash != "" {
		promptHashNote = ctx.OriginalMeta.PromptHash
	}
	fmt.Fprintf(&b, "\n(Original prompt hash: %s — re-read artifacts for context.)\n\n", promptHashNote)
	b.WriteString("Please continue from where the previous run left off.")

	additionalInstruction = strings.TrimSpace(additionalInstruction)
	if additionalInstruction != "" {
		b.WriteString("\n\n")
		b.WriteString(additionalInstruction)
	}

	return b.String()
}
