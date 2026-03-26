package recovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/buildoak/agent-mux/internal/dispatch"
)

type RecoveryContext struct {
	DispatchID   string
	OriginalMeta *dispatch.DispatchMeta
	Artifacts    []string
	ArtifactDir  string
}

func RecoverDispatch(dispatchID string) (*RecoveryContext, error) {
	dir := filepath.Join("/tmp/agent-mux", dispatchID)
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
