package recovery

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/buildoak/agent-mux/internal/dispatch"
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

func DefaultArtifactDir(dispatchID string) string {
	return filepath.Join(legacyArtifactRoot, dispatchID)
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
	dispatchID := strings.TrimSpace(record.DispatchID)
	artifactDir := strings.TrimSpace(record.ArtifactDir)
	record.DispatchID = dispatchID
	record.ArtifactDir = artifactDir
	dispatchID = strings.TrimSpace(dispatchID)
	artifactDir = strings.TrimSpace(artifactDir)
	if dispatchID == "" {
		return fmt.Errorf("missing dispatch ID for control-path registration")
	}
	if artifactDir == "" {
		return fmt.Errorf("missing artifact dir for dispatch %q", dispatchID)
	}

	artifactDirAbs, err := filepath.Abs(artifactDir)
	if err != nil {
		return fmt.Errorf("resolve artifact dir %q: %w", artifactDir, err)
	}
	if err := os.MkdirAll(controlRoot(), 0o755); err != nil {
		return fmt.Errorf("create control root: %w", err)
	}

	record.ArtifactDir = filepath.Clean(artifactDirAbs)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal control record: %w", err)
	}

	path := controlRecordPath(dispatchID)
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
	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return "", fmt.Errorf("missing dispatch ID")
	}

	recordPath := controlRecordPath(dispatchID)
	data, err := os.ReadFile(recordPath)
	if err == nil {
		var record controlRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return "", fmt.Errorf("parse control record %q: %w", recordPath, err)
		}
		if strings.TrimSpace(record.ArtifactDir) == "" {
			return "", fmt.Errorf("control record %q is missing artifact_dir", recordPath)
		}
		return filepath.Clean(record.ArtifactDir), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read control record %q: %w", recordPath, err)
	}

	dir := DefaultArtifactDir(dispatchID)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat legacy artifact directory %q: %w", dir, err)
	}

	return "", fmt.Errorf("no artifact directory found for dispatch %q via control path %q or legacy path %q", dispatchID, recordPath, dir)
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

func controlRoot() string {
	return filepath.Join(legacyArtifactRoot, "control")
}

func ControlRecordPath(dispatchID string) string {
	return controlRecordPath(dispatchID)
}

func controlRecordPath(dispatchID string) string {
	return filepath.Join(controlRoot(), url.PathEscape(dispatchID)+".json")
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
