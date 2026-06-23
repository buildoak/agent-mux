package adapter

import (
	"reflect"
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

func TestAgyNoResume(t *testing.T) {
	a := &AgyAdapter{}

	if a.SupportsResume() {
		t.Fatal("SupportsResume() = true, want false")
	}
	if args := a.ResumeArgs(nil, "session", "continue"); args != nil {
		t.Fatalf("ResumeArgs() = %#v, want nil", args)
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
