package adapter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestAgyBinary(t *testing.T) {
	a := &AgyAdapter{}
	if got := a.Binary(); got != "agy" {
		t.Fatalf("Binary() = %q, want agy", got)
	}
}

func TestAgyBuildArgs(t *testing.T) {
	a := &AgyAdapter{}

	spec := &types.DispatchSpec{
		Model:       "Claude Sonnet 4.5",
		Prompt:      "Build the parser",
		ArtifactDir: "/tmp/dispatch",
		TimeoutSec:  42,
		GraceSec:    10,
		EngineOpts: map[string]any{
			"add-dir": []any{"/tmp/scripts", "/tmp/helpers"},
		},
	}

	got := a.BuildArgs(spec)
	want := []string{
		"--sandbox",
		"--print-timeout", "57s",
		"--log-file", "/tmp/dispatch/agy.log",
		"--model", "Claude Sonnet 4.5",
		"--add-dir", "/tmp/scripts",
		"--add-dir", "/tmp/helpers",
		"-p", "Build the parser",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestAgyBuildArgsOmitsModelWhenUnset(t *testing.T) {
	a := &AgyAdapter{}

	args := a.BuildArgs(&types.DispatchSpec{
		Prompt:     "Build it",
		EngineOpts: map[string]any{},
	})

	assertNotContains(t, args, "--model")
}

func TestAgyBuildArgsPrependsSystemPrompt(t *testing.T) {
	a := &AgyAdapter{}

	args := a.BuildArgs(&types.DispatchSpec{
		SystemPrompt: "You are a Go expert.",
		Prompt:       "Build it",
		EngineOpts:   map[string]any{},
	})

	if got := args[len(args)-1]; got != "You are a Go expert.\n\nBuild it" {
		t.Fatalf("prompt arg = %q, want system prompt prepended", got)
	}
}

func TestAgyBuildArgsRepeatedAddDir(t *testing.T) {
	a := &AgyAdapter{}

	args := a.BuildArgs(&types.DispatchSpec{
		Prompt: "Build it",
		EngineOpts: map[string]any{
			"add-dir": []string{"/tmp/a", "/tmp/b"},
		},
	})

	want := []string{"--sandbox", "--print-timeout", "300s", "--add-dir", "/tmp/a", "--add-dir", "/tmp/b", "-p", "Build it"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestAgyBuildArgsLogFileUnderArtifactDir(t *testing.T) {
	a := &AgyAdapter{}

	args := a.BuildArgs(&types.DispatchSpec{
		Prompt:      "Build it",
		ArtifactDir: "/tmp/dispatch-artifacts",
		EngineOpts:  map[string]any{},
	})

	assertContains(t, args, "--log-file")
	assertContains(t, args, "/tmp/dispatch-artifacts/agy.log")
}

func TestAgyBuildArgsNeverEmitsDangerousBypass(t *testing.T) {
	a := &AgyAdapter{}

	args := a.BuildArgs(&types.DispatchSpec{
		Prompt:     "Build it",
		FullAccess: true,
		EngineOpts: map[string]any{
			"dangerously-skip-permissions": true,
		},
	})

	assertContains(t, args, "--sandbox")
	assertNotContains(t, args, "--dangerously-skip-permissions")
}

func TestAgyRuntimePolicy(t *testing.T) {
	a := &AgyAdapter{}

	policy := types.ResolveAdapterRuntimePolicy("agy", a)
	if policy.StdinMode != types.AdapterStdinEOF {
		t.Fatalf("StdinMode = %q, want %q", policy.StdinMode, types.AdapterStdinEOF)
	}
	if policy.OutputMode != types.AdapterOutputPlainStdout {
		t.Fatalf("OutputMode = %q, want %q", policy.OutputMode, types.AdapterOutputPlainStdout)
	}
	if !policy.RequireNonEmptyResponse {
		t.Fatal("RequireNonEmptyResponse = false, want true")
	}
	if policy.SoftTimeoutWrapupMode != types.AdapterSoftTimeoutNoWrapup {
		t.Fatalf("SoftTimeoutWrapupMode = %q, want %q", policy.SoftTimeoutWrapupMode, types.AdapterSoftTimeoutNoWrapup)
	}
	if policy.FailureContextMode != types.AdapterFailureContextPrivateDiagnostics {
		t.Fatalf("FailureContextMode = %q, want %q", policy.FailureContextMode, types.AdapterFailureContextPrivateDiagnostics)
	}
}

func TestAgyResumeArgs(t *testing.T) {
	a := &AgyAdapter{}

	if !a.SupportsResume() {
		t.Fatal("SupportsResume() = false, want true")
	}
	spec := &types.DispatchSpec{
		Model:       "Claude Sonnet 4.5",
		ArtifactDir: "/tmp/dispatch",
		TimeoutSec:  42,
		GraceSec:    10,
		EngineOpts: map[string]any{
			"add-dir": []string{"/tmp/scripts"},
		},
	}
	args := a.ResumeArgs(spec, "550e8400-e29b-41d4-a716-446655440000", "continue")
	want := []string{
		"--sandbox",
		"--print-timeout", "57s",
		"--log-file", "/tmp/dispatch/agy.log",
		"--model", "Claude Sonnet 4.5",
		"--add-dir", "/tmp/scripts",
		"--conversation", "550e8400-e29b-41d4-a716-446655440000",
		"-p", "continue",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("ResumeArgs() = %#v, want %#v", args, want)
	}
}

func TestAgyDiscoverSessionIDFromLog(t *testing.T) {
	a := &AgyAdapter{}
	artifactDir := t.TempDir()
	logPath := filepath.Join(artifactDir, "agy.log")
	if err := os.WriteFile(logPath, []byte(strings.Join([]string{
		"Created conversation 11111111-1111-1111-1111-111111111111",
		"Print mode: conversation=22222222-2222-2222-2222-222222222222",
		"Streaming conversation 33333333-3333-3333-3333-333333333333",
	}, "\n")), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	got, err := a.DiscoverSessionID(&types.DispatchSpec{ArtifactDir: artifactDir})
	if err != nil {
		t.Fatalf("DiscoverSessionID: %v", err)
	}
	if got != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("session_id = %q, want last conversation id", got)
	}
}

func TestAgyDiscoverSessionIDMissingLog(t *testing.T) {
	a := &AgyAdapter{}

	got, err := a.DiscoverSessionID(&types.DispatchSpec{ArtifactDir: t.TempDir()})
	if err != nil {
		t.Fatalf("DiscoverSessionID: %v", err)
	}
	if got != "" {
		t.Fatalf("session_id = %q, want empty", got)
	}
}

func TestAgyDiagnoseFailureClassifiesPrivateTranscript429(t *testing.T) {
	a := &AgyAdapter{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactDir := t.TempDir()
	conversationID := "550e8400-e29b-41d4-a716-446655440000"
	rawSecret := "private prompt: ship the unreleased roadmap"
	transcriptPath := filepath.Join(home, ".gemini", "antigravity-cli", "brain", conversationID, ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "agy.log"), []byte("Created conversation "+conversationID+"\n"), 0o644); err != nil {
		t.Fatalf("write agy log: %v", err)
	}
	transcript := strings.Join([]string{
		`{"source":"USER","type":"TEXT","text":"` + rawSecret + `"}`,
		`{"source":"SYSTEM","type":"ERROR_MESSAGE","error_code":429,"error":"` + rawSecret + `"}`,
	}, "\n")
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	diagnosis := a.DiagnoseFailure(types.AdapterFailureDiagnosticContext{
		Spec:                  &types.DispatchSpec{ArtifactDir: artifactDir},
		EmptyRequiredResponse: true,
	})
	if diagnosis == nil {
		t.Fatal("diagnosis = nil, want provider_rate_limited")
	}
	if diagnosis.Code != "provider_rate_limited" {
		t.Fatalf("code = %q, want provider_rate_limited", diagnosis.Code)
	}
	public := diagnosis.Code + diagnosis.Message + diagnosis.Suggestion
	for _, forbidden := range []string{rawSecret, conversationID, transcriptPath, "transcript_full.jsonl"} {
		if strings.Contains(public, forbidden) {
			t.Fatalf("diagnosis leaked private diagnostic content %q in %+v", forbidden, diagnosis)
		}
	}
}

func TestAgyDiagnoseFailureFallsBackWithoutPrivate429(t *testing.T) {
	a := &AgyAdapter{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactDir := t.TempDir()
	conversationID := "550e8400-e29b-41d4-a716-446655440000"
	transcriptPath := filepath.Join(home, ".gemini", "antigravity-cli", "brain", conversationID, ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "agy.log"), []byte("Created conversation "+conversationID+"\n"), 0o644); err != nil {
		t.Fatalf("write agy log: %v", err)
	}
	if err := os.WriteFile(transcriptPath, []byte(`{"source":"SYSTEM","type":"ERROR_MESSAGE","error_code":500,"error":"provider exploded"}`), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	diagnosis := a.DiagnoseFailure(types.AdapterFailureDiagnosticContext{
		Spec:                  &types.DispatchSpec{ArtifactDir: artifactDir},
		EmptyRequiredResponse: true,
	})
	if diagnosis != nil {
		t.Fatalf("diagnosis = %+v, want nil", diagnosis)
	}
}

func TestAgyDiagnoseFailureClassifiesPrivateOverloadText(t *testing.T) {
	a := &AgyAdapter{}
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactDir := t.TempDir()
	conversationID := "550e8400-e29b-41d4-a716-446655440000"
	logPath := filepath.Join(home, ".gemini", "antigravity-cli", "brain", conversationID, ".system_generated", "logs", "worker.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir diagnostic dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "agy.log"), []byte("Streaming conversation "+conversationID+"\n"), 0o644); err != nil {
		t.Fatalf("write agy log: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("The model API is currently overloaded; private detail omitted."), 0o644); err != nil {
		t.Fatalf("write private log: %v", err)
	}

	diagnosis := a.DiagnoseFailure(types.AdapterFailureDiagnosticContext{Spec: &types.DispatchSpec{ArtifactDir: artifactDir}})
	if diagnosis == nil || diagnosis.Code != "provider_rate_limited" {
		t.Fatalf("diagnosis = %+v, want provider_rate_limited", diagnosis)
	}
}

func TestAgyParseEventConservativePlainStdout(t *testing.T) {
	a := &AgyAdapter{}

	evt, err := a.ParseEvent("final answer text")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventRawPassthrough {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventRawPassthrough)
	}
	if evt.Text != "" {
		t.Fatalf("text = %q, want empty", evt.Text)
	}
	if string(evt.Raw) != "final answer text" {
		t.Fatalf("raw = %q, want original line", string(evt.Raw))
	}
}

func TestAgyParseEventEmptyLine(t *testing.T) {
	a := &AgyAdapter{}

	evt, err := a.ParseEvent("   ")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Fatalf("event = %#v, want nil", evt)
	}
}
