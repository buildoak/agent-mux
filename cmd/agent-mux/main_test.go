package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/hooks"
	"github.com/buildoak/agent-mux/internal/inbox"
	"github.com/buildoak/agent-mux/internal/recovery"
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

func TestPreviewCommandOutputsResolvedJSONShape(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	prompt := "implement feature"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"preview",
		"--engine", "codex",
		"--timeout", "123",
		"--artifact-dir", artifactDir,
		prompt,
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	preview := decodePreviewResult(t, stdout.Bytes())
	if preview.Kind != "preview" {
		t.Fatalf("kind = %q, want preview", preview.Kind)
	}
	if preview.DispatchSpec.Engine != "codex" {
		t.Fatalf("dispatch_spec.engine = %q, want codex", preview.DispatchSpec.Engine)
	}
	if preview.DispatchSpec.TraceToken != "AGENT_MUX_GO_"+preview.DispatchSpec.DispatchID {
		t.Fatalf("trace_token = %q, want %q", preview.DispatchSpec.TraceToken, "AGENT_MUX_GO_"+preview.DispatchSpec.DispatchID)
	}
	if preview.Control.ControlRecord != recovery.ControlRecordPath(preview.DispatchSpec.DispatchID) {
		t.Fatalf("control_record = %q, want %q", preview.Control.ControlRecord, recovery.ControlRecordPath(preview.DispatchSpec.DispatchID))
	}
	if preview.Control.ArtifactDir != artifactDir {
		t.Fatalf("control.artifact_dir = %q, want %q", preview.Control.ArtifactDir, artifactDir)
	}
	if len(preview.PromptPreamble) != 3 {
		t.Fatalf("prompt_preamble len = %d, want 3 (%v)", len(preview.PromptPreamble), preview.PromptPreamble)
	}
	if preview.PromptPreamble[0] != "Trace token: "+preview.DispatchSpec.TraceToken {
		t.Fatalf("prompt_preamble[0] = %q, want trace token line", preview.PromptPreamble[0])
	}
	if preview.Prompt.Excerpt != prompt {
		t.Fatalf("prompt.excerpt = %q, want %q", preview.Prompt.Excerpt, prompt)
	}
	if preview.Prompt.Chars != len(prompt) {
		t.Fatalf("prompt.chars = %d, want %d", preview.Prompt.Chars, len(prompt))
	}
	if preview.Prompt.Truncated {
		t.Fatal("prompt.truncated = true, want false")
	}
	if preview.ConfirmationRequired {
		t.Fatal("confirmation_required = true, want false for non-TTY test harness")
	}
}

func TestDispatchTTYConfirmationCancelsBeforeDispatch(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithTerminalCheck([]string{
		"--engine", "codex",
		"--artifact-dir", artifactDir,
		"implement feature",
	}, strings.NewReader("n\n"), &stdout, &stderr, func(any) bool { return true })
	if exitCode != exitCodeCancelled {
		t.Fatalf("exit code = %d, want %d; stderr=%q stdout=%q", exitCode, exitCodeCancelled, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), `"kind": "preview"`) {
		t.Fatalf("stderr = %q, want preview JSON", stderr.String())
	}
	if !strings.Contains(stderr.String(), "dispatch cancelled") {
		t.Fatalf("stderr = %q, want cancellation message", stderr.String())
	}
	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "cancelled" {
		t.Fatalf("error = %#v, want cancelled", result.Error)
	}
	if result.Error.Message != "Dispatch cancelled at confirmation prompt before launch." {
		t.Fatalf("error.message = %q, want cancellation message", result.Error.Message)
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir should not be created before confirmation, stat err=%v", err)
	}
}

func TestPreviewCommandCompactsPromptSummary(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	prompt := strings.Repeat("alpha beta gamma ", 40) + "final instruction"
	systemPrompt := strings.Repeat("system rule ", 20)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"preview",
		"--engine", "codex",
		"--artifact-dir", artifactDir,
		"--system-prompt", systemPrompt,
		prompt,
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	preview := decodePreviewResult(t, stdout.Bytes())
	if !preview.Prompt.Truncated {
		t.Fatal("prompt.truncated = false, want true for long prompt")
	}
	if preview.Prompt.Chars != len(prompt) {
		t.Fatalf("prompt.chars = %d, want %d", preview.Prompt.Chars, len(prompt))
	}
	if preview.Prompt.SystemPromptChars != len(systemPrompt) {
		t.Fatalf("prompt.system_prompt_chars = %d, want %d", preview.Prompt.SystemPromptChars, len(systemPrompt))
	}
	if !strings.Contains(preview.Prompt.Excerpt, "alpha beta gamma") || !strings.Contains(preview.Prompt.Excerpt, "final instruction") {
		t.Fatalf("prompt.excerpt = %q, want compact head/tail summary", preview.Prompt.Excerpt)
	}
	if len([]rune(preview.Prompt.Excerpt)) > previewPromptExcerptRunes {
		t.Fatalf("prompt.excerpt len = %d, want <= %d", len([]rune(preview.Prompt.Excerpt)), previewPromptExcerptRunes)
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	dispatchSpec, ok := raw["dispatch_spec"].(map[string]any)
	if !ok {
		t.Fatalf("dispatch_spec = %#v, want object", raw["dispatch_spec"])
	}
	if _, ok := dispatchSpec["prompt"]; ok {
		t.Fatalf("dispatch_spec should omit prompt body, got %v", dispatchSpec["prompt"])
	}
	if _, ok := dispatchSpec["system_prompt"]; ok {
		t.Fatalf("dispatch_spec should omit system_prompt, got %v", dispatchSpec["system_prompt"])
	}
	if _, ok := dispatchSpec["engine_opts"]; ok {
		t.Fatalf("dispatch_spec should omit engine_opts, got %v", dispatchSpec["engine_opts"])
	}
}

func TestExplicitPreviewLikeCommandShowsLiteralPromptGuidance(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "preview", command: "preview"},
		{name: "dispatch", command: "dispatch"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := run([]string{tc.command}, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 1 {
				t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), fmt.Sprintf("If you meant the literal prompt %q", tc.command)) {
				t.Fatalf("stderr = %q, want literal prompt guidance", stderr.String())
			}
			if !strings.Contains(stderr.String(), fmt.Sprintf("agent-mux -- %s", tc.command)) {
				t.Fatalf("stderr = %q, want -- escape hatch guidance", stderr.String())
			}
		})
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
	if spec.Effort != "" {
		t.Fatalf("effort = %q, want empty default for config fallback", spec.Effort)
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

func TestBuildDispatchSpecIncludesPipeline(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	err := fs.Parse([]string{"--engine", "codex", "--pipeline", "review", "implement feature"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags, positional := *parsed, fs.Args()

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}
	if spec.Pipeline != "review" {
		t.Fatalf("pipeline = %q, want %q", spec.Pipeline, "review")
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

func TestNormalizeArgsAllowsFlagsAfterPrompt(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	err := fs.Parse(normalizeArgs([]string{"--recover", "NONEXISTENT", "continue", "--engine", "codex"}))
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	flags, positional := *parsed, fs.Args()
	if flags.recover != "NONEXISTENT" {
		t.Fatalf("recover = %q, want %q", flags.recover, "NONEXISTENT")
	}
	if flags.engine != "codex" {
		t.Fatalf("engine = %q, want %q", flags.engine, "codex")
	}
	if len(positional) != 1 || positional[0] != "continue" {
		t.Fatalf("positional = %#v, want []string{\"continue\"}", positional)
	}
}

func TestStdinMode(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

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
	if result.Status != types.StatusFailed {
		t.Errorf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
}

func TestDecodeStdinDispatchSpecMaterializesDefaults(t *testing.T) {
	workingDir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(prevWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	spec, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin"}`))
	if err != nil {
		t.Fatalf("decodeStdinDispatchSpec: %v", err)
	}

	if spec.DispatchID == "" {
		t.Fatal("dispatch_id should be materialized")
	}
	specCwdReal, err := filepath.EvalSymlinks(spec.Cwd)
	if err != nil {
		t.Fatalf("EvalSymlinks(spec.Cwd): %v", err)
	}
	workingDirReal, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(workingDir): %v", err)
	}
	if specCwdReal != workingDirReal {
		t.Fatalf("cwd = %q (%q), want %q (%q)", spec.Cwd, specCwdReal, workingDir, workingDirReal)
	}
	if spec.ArtifactDir != filepath.ToSlash(recovery.DefaultArtifactDir(spec.DispatchID))+"/" {
		t.Fatalf("artifact_dir = %q, want default path", spec.ArtifactDir)
	}
	if !spec.AllowSubdispatch {
		t.Fatal("allow_subdispatch = false, want true")
	}
	if !spec.FullAccess {
		t.Fatal("full_access = false, want true")
	}
	if spec.PipelineStep != -1 {
		t.Fatalf("pipeline_step = %d, want -1", spec.PipelineStep)
	}
	if spec.GraceSec != 60 {
		t.Fatalf("grace_sec = %d, want 60", spec.GraceSec)
	}
	if spec.HandoffMode != "summary_and_refs" {
		t.Fatalf("handoff_mode = %q, want %q", spec.HandoffMode, "summary_and_refs")
	}
}

func TestDecodeStdinDispatchSpecPreservesExplicitFalseAndZero(t *testing.T) {
	spec, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","allow_subdispatch":false,"full_access":false,"pipeline_step":0,"grace_sec":0}`))
	if err != nil {
		t.Fatalf("decodeStdinDispatchSpec: %v", err)
	}

	if spec.AllowSubdispatch {
		t.Fatal("allow_subdispatch = true, want false")
	}
	if spec.FullAccess {
		t.Fatal("full_access = true, want false")
	}
	if spec.PipelineStep != 0 {
		t.Fatalf("pipeline_step = %d, want 0", spec.PipelineStep)
	}
	if spec.GraceSec != 0 {
		t.Fatalf("grace_sec = %d, want 0", spec.GraceSec)
	}
}

func TestSignalAndRecoverResolveCustomArtifactDispatch(t *testing.T) {
	startDir := t.TempDir()
	otherDir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(startDir); err != nil {
		t.Fatalf("chdir startDir: %v", err)
	}
	defer func() {
		if err := os.Chdir(prevWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	t.Setenv("PATH", t.TempDir())

	dispatchID := "fixed-dispatch-" + strings.ReplaceAll(t.Name(), "/", "-")
	relativeArtifactDir := filepath.Join("artifacts", "custom-dispatch")
	absoluteArtifactDir := filepath.Join(startDir, relativeArtifactDir)
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join("/tmp/agent-mux/control", url.PathEscape(dispatchID)+".json"))
	})
	input := map[string]any{
		"dispatch_id":  dispatchID,
		"engine":       "codex",
		"prompt":       "from stdin",
		"artifact_dir": relativeArtifactDir,
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--stdin"}, bytes.NewReader(data), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("initial exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("initial error = %#v, want binary_not_found", result.Error)
	}
	if result.DispatchID != dispatchID {
		t.Fatalf("dispatch_id = %q, want %q", result.DispatchID, dispatchID)
	}
	if _, err := os.Stat(filepath.Join(absoluteArtifactDir, "_dispatch_meta.json")); err != nil {
		t.Fatalf("stat dispatch meta: %v", err)
	}

	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("chdir otherDir: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = run([]string{"--signal", dispatchID, "focus on auth"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("signal exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	messages, err := inbox.ReadInbox(absoluteArtifactDir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(messages) != 1 || messages[0] != "focus on auth" {
		t.Fatalf("messages = %v, want [focus on auth]", messages)
	}

	var signalResult struct {
		ArtifactDir string `json:"artifact_dir"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &signalResult); err != nil {
		t.Fatalf("unmarshal signal result: %v\nstdout=%q", err, stdout.String())
	}
	signalArtifactReal, err := filepath.EvalSymlinks(signalResult.ArtifactDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(signal artifact_dir): %v", err)
	}
	absoluteArtifactReal, err := filepath.EvalSymlinks(absoluteArtifactDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(absolute artifact_dir): %v", err)
	}
	if signalArtifactReal != absoluteArtifactReal {
		t.Fatalf("artifact_dir = %q (%q), want %q (%q)", signalResult.ArtifactDir, signalArtifactReal, absoluteArtifactDir, absoluteArtifactReal)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = run([]string{"--engine", "codex", "--recover", dispatchID, "continue"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("recover exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result = decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("recover error = %#v, want binary_not_found", result.Error)
	}
}

func TestStdinPipelineDispatch(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
[pipelines.review]
[[pipelines.review.steps]]
name = "review"
`)
	input := map[string]any{
		"dispatch_id":  "stdin-pipeline-dispatch",
		"engine":       "not-a-real-engine",
		"prompt":       "from stdin",
		"pipeline":     "review",
		"cwd":          t.TempDir(),
		"artifact_dir": filepath.Join(t.TempDir(), "artifacts"),
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--stdin", "--config", cfgPath}, bytes.NewReader(data), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodePipelineResult(t, stdout.Bytes())
	if result.PipelineID == "" {
		t.Fatal("pipeline_id should be set")
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1", len(result.Steps))
	}
	if len(result.Steps[0].Workers) != 1 {
		t.Fatalf("len(steps[0].workers) = %d, want 1", len(result.Steps[0].Workers))
	}
	if result.Steps[0].Workers[0].ErrorCode != "engine_not_found" {
		t.Fatalf("workers[0].error_code = %q, want engine_not_found", result.Steps[0].Workers[0].ErrorCode)
	}
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

func TestRunPrependsContextFilePreamble(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	contextFile := filepath.Join(t.TempDir(), "context.md")
	if err := os.WriteFile(contextFile, []byte("context"), 0644); err != nil {
		t.Fatalf("write context file: %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	prompt := "implement feature"
	exitCode := run([]string{
		"--engine", "codex",
		"--artifact-dir", artifactDir,
		"--context-file", contextFile,
		prompt,
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 && exitCode != 1 {
		t.Fatalf("exit code = %d, want 0 or 1; stderr=%q", exitCode, stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
	}

	meta := readDispatchMeta(t, artifactDir)
	wantPrompt := contextFilePromptPreamble + "\n" + prompt
	if meta.PromptHash != promptHash(wantPrompt) {
		t.Fatalf("prompt_hash = %q, want %q", meta.PromptHash, promptHash(wantPrompt))
	}
}

func TestRunFailsWhenContextFileMissing(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "nonexistent-12345.md")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"--engine", "codex",
		"--context-file", missingPath,
		"implement feature",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q", exitCode, stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil {
		t.Fatal("error = nil, want config_error")
	}
	if result.Error.Code != "config_error" {
		t.Fatalf("error.code = %q, want %q", result.Error.Code, "config_error")
	}
}

func TestRunLeavesPromptUnchangedWithoutContextFile(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	prompt := "implement feature"
	exitCode := run([]string{
		"--engine", "codex",
		"--artifact-dir", artifactDir,
		prompt,
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 && exitCode != 1 {
		t.Fatalf("exit code = %d, want 0 or 1; stderr=%q", exitCode, stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
	}

	meta := readDispatchMeta(t, artifactDir)
	if meta.PromptHash != promptHash(prompt) {
		t.Fatalf("prompt_hash = %q, want %q", meta.PromptHash, promptHash(prompt))
	}
	if meta.PromptHash == promptHash(contextFilePromptPreamble+"\n"+prompt) {
		t.Fatalf("prompt_hash = %q, should not include context preamble", meta.PromptHash)
	}
}

func TestRunInjectsHookRulesWithoutSelfDenying(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	cfgPath := writeTempConfig(t, "[hooks]\ndeny = [\"rm -rf\"]\n")
	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	prompt := "summarize the current repository state"
	exitCode := run([]string{
		"--engine", "codex",
		"--artifact-dir", artifactDir,
		"--config", cfgPath,
		prompt,
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 && exitCode != 1 {
		t.Fatalf("exit code = %d, want 0 or 1; stderr=%q", exitCode, stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
	}

	meta := readDispatchMeta(t, artifactDir)
	injectedPrompt := hooks.NewEvaluator(config.HooksConfig{Deny: []string{"rm -rf"}}).PromptInjection() + "\n\n" + prompt
	if meta.PromptHash != promptHash(injectedPrompt) {
		t.Fatalf("prompt_hash = %q, want %q", meta.PromptHash, promptHash(injectedPrompt))
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

func decodeResult(t *testing.T, data []byte) types.DispatchResult {
	t.Helper()

	var result types.DispatchResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal DispatchResult: %v\nstdout=%q", err, string(data))
	}
	return result
}

type previewResultForTest struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	DispatchSpec  struct {
		DispatchID string `json:"dispatch_id"`
		Engine     string `json:"engine"`
		TraceToken string `json:"trace_token"`
	} `json:"dispatch_spec"`
	Prompt struct {
		Excerpt           string `json:"excerpt"`
		Chars             int    `json:"chars"`
		Truncated         bool   `json:"truncated"`
		SystemPromptChars int    `json:"system_prompt_chars"`
	} `json:"prompt"`
	Control struct {
		ControlRecord string `json:"control_record"`
		ArtifactDir   string `json:"artifact_dir"`
	} `json:"control"`
	PromptPreamble       []string `json:"prompt_preamble"`
	ConfirmationRequired bool     `json:"confirmation_required"`
}

func decodePreviewResult(t *testing.T, data []byte) previewResultForTest {
	t.Helper()

	var result previewResultForTest
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal PreviewResult: %v\nstdout=%q", err, string(data))
	}
	return result
}

func decodeJSONMap(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal JSON map: %v\nstdout=%q", err, string(data))
	}
	return result
}

type pipelineResultForTest struct {
	PipelineID string `json:"pipeline_id"`
	Status     string `json:"status"`
	Steps      []struct {
		Workers []struct {
			ErrorCode string `json:"error_code"`
		} `json:"workers"`
	} `json:"steps"`
}

func decodePipelineResult(t *testing.T, data []byte) pipelineResultForTest {
	t.Helper()

	var result pipelineResultForTest
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal PipelineResult: %v\nstdout=%q", err, string(data))
	}
	return result
}

func readDispatchMeta(t *testing.T, artifactDir string) dispatchMetaForTest {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(artifactDir, "_dispatch_meta.json"))
	if err != nil {
		t.Fatalf("read dispatch meta: %v", err)
	}

	var meta dispatchMetaForTest
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal dispatch meta: %v", err)
	}
	return meta
}

func promptHash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("sha256:%x", sum[:8])
}

type dispatchMetaForTest struct {
	PromptHash string `json:"prompt_hash"`
	Cwd        string `json:"cwd"`
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

func TestBuildDispatchSpecLeavesEffortEmptyWithoutExplicitFlag(t *testing.T) {
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

	if spec.Effort != "" {
		t.Errorf("spec.Effort = %q, want empty string", spec.Effort)
	}
}
