package recovery

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/types"
)

const legacyArtifactRoot = "/tmp/agent-mux"

type RecoveryContext struct {
	DispatchID   string
	OriginalMeta *dispatch.DispatchMeta
	Artifacts    []string
	ArtifactDir  string
}

type controlRecord struct {
	DispatchID   string `json:"dispatch_id"`
	ArtifactDir  string `json:"artifact_dir"`
	DispatchSalt string `json:"dispatch_salt,omitempty"`
	TraceToken   string `json:"trace_token,omitempty"`
}

func DefaultArtifactDir(dispatchID string) (string, error) {
	return artifactDirPath(currentArtifactRoot(), dispatchID)
}

func RegisterDispatch(dispatchID, artifactDir string) error {
	return writeControlRecord(controlRecord{
		DispatchID:  dispatchID,
		ArtifactDir: artifactDir,
	})
}

func RegisterDispatchSpec(spec *types.DispatchSpec) error {
	if spec == nil {
		return fmt.Errorf("missing dispatch spec for control-path registration")
	}
	dispatch.EnsureTraceability(spec)
	return writeControlRecord(controlRecord{
		DispatchID:   spec.DispatchID,
		ArtifactDir:  spec.ArtifactDir,
		DispatchSalt: spec.Salt,
		TraceToken:   spec.TraceToken,
	})
}

func writeControlRecord(record controlRecord) error {
	dispatchID, err := validateDispatchID(record.DispatchID)
	if err != nil {
		return err
	}
	artifactDir := strings.TrimSpace(record.ArtifactDir)
	record.DispatchID = dispatchID
	record.ArtifactDir = artifactDir
	artifactDir = strings.TrimSpace(artifactDir)
	if artifactDir == "" {
		return fmt.Errorf("missing artifact dir for dispatch %q", dispatchID)
	}

	artifactDirAbs, err := filepath.Abs(artifactDir)
	if err != nil {
		return fmt.Errorf("resolve artifact dir %q: %w", artifactDir, err)
	}
	controlRoot := currentControlRoot()
	if err := os.MkdirAll(controlRoot, 0o700); err != nil {
		return fmt.Errorf("create control root: %w", err)
	}

	record.ArtifactDir = filepath.Clean(artifactDirAbs)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal control record: %w", err)
	}

	path, err := controlRecordPathE(controlRoot, dispatchID)
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write control record: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install control record: %w", err)
	}
	return nil
}

func ResolveArtifactDir(dispatchID string) (string, error) {
	dispatchID, err := validateDispatchID(dispatchID)
	if err != nil {
		return "", err
	}

	currentRoot := currentArtifactRoot()
	currentControlPath, err := controlRecordPathE(currentControlRoot(), dispatchID)
	if err != nil {
		return "", err
	}
	legacyControlPath, err := controlRecordPathE(legacyControlRoot(), dispatchID)
	if err != nil {
		return "", err
	}

	if dir, found, err := resolveArtifactDirFromControlRecord(currentControlPath); err != nil {
		return "", err
	} else if found {
		return dir, nil
	}
	if dir, found, err := resolveArtifactDirFromControlRecord(legacyControlPath); err != nil {
		return "", err
	} else if found {
		return dir, nil
	}

	currentDir, err := artifactDirPath(currentRoot, dispatchID)
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

	return "", fmt.Errorf(
		"no artifact directory found for dispatch %q via control paths %q and %q or artifact paths %q and %q",
		dispatchID,
		currentControlPath,
		legacyControlPath,
		currentDir,
		legacyDir,
	)
}

func RecoverDispatch(dispatchID string) (*RecoveryContext, error) {
	dir, err := ResolveArtifactDir(dispatchID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no artifact directory found for dispatch %q at %s. Previous dispatch may not have run or used a custom --artifact-dir.", dispatchID, dir)
		}
		return nil, fmt.Errorf("stat artifact directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("artifact path %q is not a directory", dir)
	}

	metaPath := filepath.Join(dir, "_dispatch_meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("read dispatch meta %q: %w", metaPath, err)
	}

	var meta dispatch.DispatchMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse dispatch meta %q: %w", metaPath, err)
	}

	return &RecoveryContext{
		DispatchID:   dispatchID,
		OriginalMeta: &meta,
		Artifacts:    dispatch.ScanArtifacts(dir),
		ArtifactDir:  dir,
	}, nil
}

func currentArtifactRoot() string {
	return sanitize.SecureArtifactRoot()
}

func currentControlRoot() string {
	return filepath.Join(currentArtifactRoot(), "control")
}

func legacyControlRoot() string {
	return filepath.Join(legacyArtifactRoot, "control")
}

func ControlRecordPath(dispatchID string) string {
	path, err := controlRecordPathE(currentControlRoot(), dispatchID)
	if err != nil {
		return filepath.Join(currentControlRoot(), url.PathEscape(strings.TrimSpace(dispatchID))+".json")
	}
	return path
}

func controlRecordPathE(root, dispatchID string) (string, error) {
	dispatchID, err := validateDispatchID(dispatchID)
	if err != nil {
		return "", err
	}

	path, err := sanitize.SafeJoinPath(root, url.PathEscape(dispatchID)+".json")
	if err != nil {
		return "", fmt.Errorf("build control record path for dispatch %q: %w", dispatchID, err)
	}
	return path, nil
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

func resolveArtifactDirFromControlRecord(recordPath string) (string, bool, error) {
	data, err := os.ReadFile(recordPath)
	if err == nil {
		var record controlRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return "", true, fmt.Errorf("parse control record %q: %w", recordPath, err)
		}
		if strings.TrimSpace(record.ArtifactDir) == "" {
			return "", true, fmt.Errorf("control record %q is missing artifact_dir", recordPath)
		}
		return filepath.Clean(record.ArtifactDir), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("read control record %q: %w", recordPath, err)
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
