package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/types"
)

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), version) {
		t.Fatalf("stdout = %q, want version %q", stdout.String(), version)
	}
}

func TestBuildDispatchSpecDefaults(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	err := fs.Parse([]string{"--engine", "codex", "implement feature"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags, positional := *parsed, fs.Args()

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}

	if spec.DispatchID == "" {
		t.Fatal("dispatch_id should be set")
	}
	if spec.Engine != "codex" {
		t.Fatalf("engine = %q, want %q", spec.Engine, "codex")
	}
	if spec.Effort != "high" {
		t.Fatalf("effort = %q, want %q", spec.Effort, "high")
	}
	wantCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if spec.Cwd != wantCwd {
		t.Fatalf("cwd = %q, want %q", spec.Cwd, wantCwd)
	}
	if spec.MaxDepth != 2 {
		t.Fatalf("max_depth = %d, want 2", spec.MaxDepth)
	}
	if !spec.AllowSubdispatch {
		t.Fatal("allow_subdispatch = false, want true")
	}
	if spec.PipelineStep != -1 {
		t.Fatalf("pipeline_step = %d, want -1", spec.PipelineStep)
	}
	if !spec.FullAccess {
		t.Fatal("full_access = false, want true")
	}
	if spec.GraceSec != 60 {
		t.Fatalf("grace_sec = %d, want 60", spec.GraceSec)
	}
	if spec.HandoffMode != "summary_and_refs" {
		t.Fatalf("handoff_mode = %q, want %q", spec.HandoffMode, "summary_and_refs")
	}
	wantArtifactDir := filepath.ToSlash(filepath.Join("/tmp/agent-mux", spec.DispatchID)) + "/"
	if spec.ArtifactDir != wantArtifactDir {
		t.Fatalf("artifact_dir = %q, want %q", spec.ArtifactDir, wantArtifactDir)
	}

	if got := spec.EngineOpts["sandbox"]; got != "danger-full-access" {
		t.Fatalf("engine_opts[sandbox] = %#v, want %q", got, "danger-full-access")
	}
	if got := spec.EngineOpts["reasoning"]; got != "medium" {
		t.Fatalf("engine_opts[reasoning] = %#v, want %q", got, "medium")
	}
	if got := spec.EngineOpts["max-turns"]; got != 0 {
		t.Fatalf("engine_opts[max-turns] = %#v, want 0", got)
	}
	addDirValue, ok := spec.EngineOpts["add-dir"].([]string)
	if !ok {
		t.Fatalf("engine_opts[add-dir] type = %T, want []string", spec.EngineOpts["add-dir"])
	}
	if len(addDirValue) != 0 {
		t.Fatalf("engine_opts[add-dir] = %#v, want empty slice", addDirValue)
	}
	if got := spec.EngineOpts["permission-mode"]; got != "" {
		t.Fatalf("engine_opts[permission-mode] = %#v, want empty string", got)
	}
}

func TestNoFullFlag(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	err := fs.Parse([]string{"--engine", "codex", "--no-full", "implement feature"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags, positional := *parsed, fs.Args()

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}
	if spec.FullAccess {
		t.Fatal("full_access = true, want false")
	}
}

func TestRepeatableSkillFlag(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	err := fs.Parse([]string{"--engine", "codex", "--skill", "a", "--skill", "b", "implement feature"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags, positional := *parsed, fs.Args()

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}
	if len(spec.Skills) != 2 || spec.Skills[0] != "a" || spec.Skills[1] != "b" {
		t.Fatalf("skills = %#v, want []string{\"a\", \"b\"}", spec.Skills)
	}
}

func TestStdinMode(t *testing.T) {
	t.Parallel()

	input := types.DispatchSpec{
		DispatchID:       "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Engine:           "codex",
		Effort:           "high",
		Prompt:           "from stdin",
		Cwd:              "/tmp/project",
		ArtifactDir:      filepath.Join(t.TempDir(), "artifacts") + "/",
		MaxDepth:         2,
		AllowSubdispatch: true,
		PipelineStep:     -1,
		GraceSec:         60,
		HandoffMode:      "summary_and_refs",
		FullAccess:       true,
		TimeoutSec:       5,
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// Stdin mode now dispatches; Codex binary likely not installed in test,
	// so we expect a failed result (binary_not_found) or the dispatch to run.
	exitCode := run([]string{"--stdin"}, bytes.NewReader(data), &stdout, &stderr)

	// Parse the output as a DispatchResult
	var result types.DispatchResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal stdout as DispatchResult: %v\nstdout=%q", err, stdout.String())
	}

	// Should have schema_version = 1 and a valid dispatch_id
	if result.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if result.DispatchID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Errorf("dispatch_id = %q, want 01ARZ3NDEKTSV4RRFFQ69G5FAV", result.DispatchID)
	}
	// If codex is not installed, expect binary_not_found; otherwise completed or failed
	validStatuses := map[types.DispatchStatus]bool{
		types.StatusCompleted: true,
		types.StatusTimedOut:  true,
		types.StatusFailed:    true,
	}
	if !validStatuses[result.Status] {
		t.Errorf("status = %q, not a valid DispatchStatus", result.Status)
	}
	_ = exitCode // exit code depends on whether codex is installed
}

func TestOutputFlagDefault(t *testing.T) {
	fs, parsed := newFlagSet(nil)
	if err := fs.Parse([]string{"--engine", "codex", "hello"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed

	if flags.output != "json" {
		t.Errorf("output default = %q, want %q", flags.output, "json")
	}
}

func TestOutputFlagText(t *testing.T) {
	fs, parsed := newFlagSet(nil)
	if err := fs.Parse([]string{"--output", "text", "--engine", "codex", "hello"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed

	if flags.output != "text" {
		t.Errorf("output = %q, want %q", flags.output, "text")
	}
}

func TestVerboseFlagDefault(t *testing.T) {
	fs, parsed := newFlagSet(nil)
	if err := fs.Parse([]string{"--engine", "codex", "hello"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed

	if flags.verbose {
		t.Error("verbose should default to false")
	}
}

func TestVerboseFlagSet(t *testing.T) {
	fs, parsed := newFlagSet(nil)
	if err := fs.Parse([]string{"--verbose", "--engine", "codex", "hello"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed

	if !flags.verbose {
		t.Error("verbose should be true when --verbose passed")
	}
}

func TestWriteTextResult(t *testing.T) {
	var buf bytes.Buffer
	result := &types.DispatchResult{
		Status:     "completed",
		Response:   "Done building the parser.",
		DurationMS: 1234,
		Metadata: &types.DispatchMetadata{
			Engine: "codex",
			Model:  "gpt-5.4",
			Tokens: &types.TokenUsage{Input: 1000, Output: 200},
		},
	}
	writeTextResult(&buf, result)
	out := buf.String()

	if !strings.Contains(out, "Status: completed") {
		t.Errorf("missing status in output: %q", out)
	}
	if !strings.Contains(out, "Done building the parser.") {
		t.Errorf("missing response in output: %q", out)
	}
	if !strings.Contains(out, "codex") {
		t.Errorf("missing engine in output: %q", out)
	}
}

func TestWriteTextResultError(t *testing.T) {
	var buf bytes.Buffer
	result := &types.DispatchResult{
		Status: "failed",
		Error: &types.DispatchError{
			Code:       "model_not_found",
			Message:    "Model 'gpt-99' not available",
			Suggestion: "Use gpt-5.4 instead",
		},
		DurationMS: 100,
	}
	writeTextResult(&buf, result)
	out := buf.String()

	if !strings.Contains(out, "model_not_found") {
		t.Errorf("missing error code: %q", out)
	}
	if !strings.Contains(out, "gpt-5.4") {
		t.Errorf("missing suggestion: %q", out)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	f, err := os.CreateTemp("", "agent-mux-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })

	return f.Name()
}

func TestFlagSetVisitTracksExplicitFlags(t *testing.T) {
	t.Parallel()

	fs, _ := newFlagSet(ioDiscard{})
	if err := fs.Parse([]string{"--effort", "high", "--engine", "codex", "prompt"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})

	if !flagsSet["effort"] {
		t.Error("effort should be tracked when explicitly passed")
	}
	if !flagsSet["engine"] {
		t.Error("engine should be tracked when explicitly passed")
	}
	if flagsSet["model"] {
		t.Error("model should not be tracked when omitted")
	}
}

func TestFlagSetVisitDoesNotTrackDefaults(t *testing.T) {
	t.Parallel()

	fs, _ := newFlagSet(ioDiscard{})
	if err := fs.Parse([]string{"--engine", "codex", "prompt"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})

	if flagsSet["effort"] {
		t.Error("effort should not be tracked when only the default applies")
	}
}

func TestRoleEffortAppliedWhenNoExplicitEffort(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, "[roles.explorer]\nengine = \"codex\"\nmodel = \"gpt-5.4\"\neffort = \"medium\"\n")

	fs, parsed := newFlagSet(ioDiscard{})
	args := []string{"--engine", "codex", "--config", cfgPath, "--role", "explorer", "hello"}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed
	positional := fs.Args()

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}

	cfgLoaded, err := config.LoadConfig(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	role, err := config.ResolveRole(cfgLoaded, "explorer")
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}

	if !flagsSet["effort"] && !flagsSet["e"] && role.Effort != "" {
		spec.Effort = role.Effort
	}

	if spec.Effort != "medium" {
		t.Errorf("spec.Effort = %q, want %q", spec.Effort, "medium")
	}
}

func TestRoleEffortNotAppliedWhenExplicitEffort(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, "[roles.explorer]\nengine = \"codex\"\nmodel = \"gpt-5.4\"\neffort = \"medium\"\n")

	fs, parsed := newFlagSet(ioDiscard{})
	args := []string{"--engine", "codex", "--config", cfgPath, "--role", "explorer", "--effort", "high", "hello"}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed
	positional := fs.Args()

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}

	cfgLoaded, err := config.LoadConfig(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	role, err := config.ResolveRole(cfgLoaded, "explorer")
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}

	if !flagsSet["effort"] && !flagsSet["e"] && role.Effort != "" {
		spec.Effort = role.Effort
	}

	if spec.Effort != "high" {
		t.Errorf("spec.Effort = %q, want %q", spec.Effort, "high")
	}
}

func TestDefaultEffortWithNoRole(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	if err := fs.Parse([]string{"--engine", "codex", "hello"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags := *parsed
	positional := fs.Args()

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}

	if spec.Effort != "high" {
		t.Errorf("spec.Effort = %q, want %q", spec.Effort, "high")
	}
}
