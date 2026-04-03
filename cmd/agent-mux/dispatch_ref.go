package main

import (
	"fmt"
	"strings"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/recovery"
	"github.com/buildoak/agent-mux/internal/sanitize"
)

type dispatchRefResolution struct {
	InputRef    string
	DispatchID  string
	ArtifactDir string
	Record      *dispatch.DispatchRecord
}

func resolveDispatchReference(ref string) (*dispatchRefResolution, error) {
	ref = strings.TrimSpace(ref)
	if err := sanitize.ValidateDispatchID(ref); err != nil {
		return nil, err
	}

	record, err := dispatch.FindDispatchRecordByRef(ref)
	if err != nil {
		return nil, err
	}
	controlRecord, err := recovery.ResolveControlRecord(ref)
	if err != nil {
		return nil, err
	}

	resolved := &dispatchRefResolution{InputRef: ref}
	if record != nil {
		resolved.Record = record
		resolved.DispatchID = strings.TrimSpace(record.ID)
		resolved.ArtifactDir = strings.TrimSpace(record.ArtifactDir)
	}
	if controlRecord != nil {
		if resolved.DispatchID != "" && resolved.DispatchID != strings.TrimSpace(controlRecord.DispatchID) {
			return nil, fmt.Errorf("reference %q resolved to conflicting dispatch IDs %q and %q", ref, resolved.DispatchID, controlRecord.DispatchID)
		}
		resolved.DispatchID = firstNonEmptyString(resolved.DispatchID, controlRecord.DispatchID)
		resolved.ArtifactDir = firstNonEmptyString(controlRecord.ArtifactDir, resolved.ArtifactDir)
	}
	if resolved.DispatchID == "" && record == nil && controlRecord == nil {
		if artifactDir, err := recovery.ResolveArtifactDir(ref); err == nil {
			resolved.DispatchID = ref
			resolved.ArtifactDir = artifactDir
		}
	}
	if resolved.DispatchID == "" {
		return nil, fmt.Errorf("no dispatch found for reference %q", ref)
	}
	if resolved.ArtifactDir == "" {
		if artifactDir, err := recovery.ResolveArtifactDir(resolved.DispatchID); err == nil {
			resolved.ArtifactDir = artifactDir
		}
	}
	return resolved, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
