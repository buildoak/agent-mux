package main

import (
	"testing"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/types"
)

func TestFinalAsyncStatusPreservesLoopFields(t *testing.T) {
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{DispatchID: "01TEST", ArtifactDir: artifactDir}
	if err := dispatch.WriteStatusJSON(artifactDir, dispatch.LiveStatus{
		State:          "running",
		ElapsedS:       1,
		LastActivity:   "session discovered",
		ToolsUsed:      2,
		FilesChanged:   3,
		StdinPipeReady: true,
		DispatchID:     spec.DispatchID,
		SessionID:      "session-123",
	}); err != nil {
		t.Fatal(err)
	}

	status := finalAsyncStatus(spec, &types.DispatchResult{
		DurationMS: 42000,
		Metadata:   &types.DispatchMetadata{SessionID: "result-session"},
	}, "completed")

	if status.State != "completed" {
		t.Fatalf("State=%q, want completed", status.State)
	}
	if status.ElapsedS != 42 {
		t.Fatalf("ElapsedS=%d, want 42", status.ElapsedS)
	}
	if status.LastActivity != "done" {
		t.Fatalf("LastActivity=%q, want done", status.LastActivity)
	}
	if status.SessionID != "session-123" {
		t.Fatalf("SessionID=%q, want preserved session-123", status.SessionID)
	}
	if !status.StdinPipeReady {
		t.Fatal("StdinPipeReady=false, want preserved true")
	}
	if status.ToolsUsed != 2 || status.FilesChanged != 3 {
		t.Fatalf("activity=%d/%d, want preserved 2/3", status.ToolsUsed, status.FilesChanged)
	}
}

func TestFinalAsyncStatusFallsBackToResultSession(t *testing.T) {
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{DispatchID: "01TEST", ArtifactDir: artifactDir}

	status := finalAsyncStatus(spec, &types.DispatchResult{
		DurationMS: 1000,
		Metadata:   &types.DispatchMetadata{SessionID: "result-session"},
	}, "completed")

	if status.SessionID != "result-session" {
		t.Fatalf("SessionID=%q, want result-session", status.SessionID)
	}
	if status.DispatchID != spec.DispatchID {
		t.Fatalf("DispatchID=%q, want %q", status.DispatchID, spec.DispatchID)
	}
}
