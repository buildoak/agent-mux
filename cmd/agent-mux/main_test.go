package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/steer"
	"github.com/buildoak/agent-mux/internal/types"
)

func isolateHome(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	raw := decodeJSONMap(t, stdout.Bytes())
	if raw["version"] != version {
		t.Fatalf("version = %#v, want %q", raw["version"], version)
	}
}

func TestUnknownFlagReturnsJSONError(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--definitely-not-a-flag"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "invalid_args" {
		t.Fatalf("error = %#v, want invalid_args", result.Error)
	}
	if !strings.Contains(result.Error.Message, "flag provided but not defined") {
		t.Fatalf("error.message = %q, want parse failure", result.Error.Message)
	}
	if !strings.Contains(result.Error.Hint, "Usage of agent-mux") {
		t.Fatalf("error.hint = %q, want usage text", result.Error.Hint)
	}
}

func TestHelpFlagReturnsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	if raw["kind"] != "help" {
		t.Fatalf("kind = %#v, want help", raw["kind"])
	}
	usage, ok := raw["usage"].(string)
	if !ok {
		t.Fatalf("usage = %#v, want string", raw["usage"])
	}
	if !strings.Contains(usage, "Usage:\n  agent-mux [flags] <prompt>") {
		t.Fatalf("usage = %q, want curated usage text", usage)
	}
	if !strings.Contains(usage, "Literal prompt escape:\n  agent-mux -- help") {
		t.Fatalf("usage = %q, want literal prompt escape guidance", usage)
	}
	if strings.Contains(usage, "Usage of agent-mux") {
		t.Fatalf("usage = %q, want curated help instead of raw flag output", usage)
	}
}

func TestTopLevelHelpSurfacesMatch(t *testing.T) {
	t.Parallel()

	cases := [][]string{
		{},
		{"help"},
		{"--help"},
	}

	var expected string
	for _, args := range cases {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := run(args, strings.NewReader(""), &stdout, &stderr)
		if exitCode != 0 {
			t.Fatalf("args=%v exit code = %d, want 0; stderr=%q stdout=%q", args, exitCode, stderr.String(), stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("args=%v stderr = %q, want empty", args, stderr.String())
		}
		if expected == "" {
			expected = stdout.String()
			continue
		}
		if stdout.String() != expected {
			t.Fatalf("args=%v stdout = %q, want %q", args, stdout.String(), expected)
		}
	}
}

func TestBareHelpHasNoDispatchSideEffects(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".agent-mux")); !os.IsNotExist(err) {
		t.Fatalf(".agent-mux should not exist after help path, stat err=%v", err)
	}
}

func TestDoubleDashPreservesLiteralHelpPrompt(t *testing.T) {
	isolateHome(t)
	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--engine", "codex", "--", "help"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), `"kind":"help"`) {
		t.Fatalf("stdout = %q, want dispatch result instead of help payload", stdout.String())
	}
	if !strings.Contains(stderr.String(), `"kind":"preview"`) {
		t.Fatalf("stderr = %q, want preview JSON from dispatch path", stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found from dispatch attempt", result.Error)
	}
}

func TestSignalRequiresMessageReturnsJSONAck(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--signal", "dispatch-123"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var ack SignalAck
	if err := json.Unmarshal(stdout.Bytes(), &ack); err != nil {
		t.Fatalf("unmarshal SignalAck: %v\nstdout=%q", err, stdout.String())
	}
	if ack.Status != "error" {
		t.Fatalf("status = %q, want error", ack.Status)
	}
	if ack.DispatchID != "dispatch-123" {
		t.Fatalf("dispatch_id = %q, want dispatch-123", ack.DispatchID)
	}
	if ack.Error == nil || ack.Error.Code != "invalid_args" {
		t.Fatalf("error = %#v, want invalid_args", ack.Error)
	}
}

func TestSignalRejectsInvalidDispatchID(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"--signal", "../dispatch", "resume"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	var ack SignalAck
	if err := json.Unmarshal(stdout.Bytes(), &ack); err != nil {
		t.Fatalf("unmarshal SignalAck: %v\nstdout=%q", err, stdout.String())
	}
	if ack.Error == nil || ack.Error.Code != "invalid_input" {
		t.Fatalf("error = %#v, want invalid_input", ack.Error)
	}
}

func TestListCommandOutputsFilteredTable(t *testing.T) {
	isolateHome(t)

	first := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	second := testStoreRecord("01KMT4E7CDDD1KQEC8MYJRW9Z9", "failed")
	third := testStoreRecord("01KMT4E7DFFF1KQEC8MYJRW2A2", "completed")

	writeStoreRecord(t, first, "first result", true)
	writeStoreRecord(t, second, "", true)
	writeStoreRecord(t, third, "third result", true)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"list", "--status", "completed", "--limit", "1"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "STATUS") || !strings.Contains(out, "CWD") {
		t.Fatalf("stdout = %q, want table header", out)
	}
	if !strings.Contains(out, third.ID[:12]) {
		t.Fatalf("stdout = %q, want latest completed dispatch %q", out, third.ID[:12])
	}
	if strings.Contains(out, first.ID[:12]) {
		t.Fatalf("stdout = %q, want first completed dispatch filtered out by --limit 1", out)
	}
	if strings.Contains(out, second.ID[:12]) {
		t.Fatalf("stdout = %q, want failed dispatch filtered out", out)
	}
}

func TestListCommandJSONOutputsNDJSON(t *testing.T) {
	isolateHome(t)

	first := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	second := testStoreRecord("01KMT4E7CDDD1KQEC8MYJRW9Z9", "failed")

	writeStoreRecord(t, first, "first result", true)
	writeStoreRecord(t, second, "", true)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"list", "--json", "--limit", "2"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson lines = %d, want 2; stdout=%q", len(lines), stdout.String())
	}

	for _, line := range lines {
		var record dispatch.DispatchRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal NDJSON line %q: %v", line, err)
		}
		if record.ID == "" {
			t.Fatalf("record = %#v, want id", record)
		}
	}
}

func TestStatusCommandOutputsRecordSummary(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	writeStoreRecord(t, record, "stored response", true)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"status", record.ID[:12]}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"Status:",
		record.Status,
		"Engine/Model:",
		record.Engine + " / " + record.Model,
		"Duration:",
		"824s",
		"Started:",
		record.StartedAt,
		"Truncated:",
		"true",
		"ArtifactDir:",
		record.ArtifactDir,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
}

func TestResultCommandReadsStoredResultByPrefix(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	response := "# Result\n\nStored response text."
	writeStoreRecord(t, record, response, true)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"result", record.ID[:12]}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stdout.String() != response {
		t.Fatalf("stdout = %q, want %q", stdout.String(), response)
	}
}

func TestResultCommandFallsBackToLegacyFullOutput(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	writeStoreRecord(t, record, "", false)

	artifactDir, err := dispatch.DefaultArtifactDir(record.ID)
	if err != nil {
		t.Fatalf("DefaultArtifactDir: %v", err)
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", artifactDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(artifactDir) })

	response := "# Result\n\nRecovered from legacy full_output."
	if err := os.WriteFile(filepath.Join(artifactDir, "full_output.md"), []byte(response), 0o644); err != nil {
		t.Fatalf("WriteFile(full_output.md): %v", err)
	}
	if err := dispatch.WriteStatusJSON(artifactDir, dispatch.LiveStatus{
		State:        "completed",
		ElapsedS:     1,
		LastActivity: "done",
		DispatchID:   record.ID,
	}); err != nil {
		t.Fatalf("WriteStatusJSON: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"result", "--json", record.ID[:12]}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	if raw["dispatch_id"] != record.ID {
		t.Fatalf("dispatch_id = %#v, want %q", raw["dispatch_id"], record.ID)
	}
	if raw["response"] != response {
		t.Fatalf("response = %#v, want %q", raw["response"], response)
	}
}

func TestPreviewCommandOutputsResolvedJSONShape(t *testing.T) {
	isolateHome(t)

	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", agentsDir, err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "planner.md"), []byte("Planner profile.\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}

	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	prompt := "implement feature"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"preview",
		"--engine", "codex",
		"--cwd", cwd,
		"--profile", "planner",
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
	if preview.ResultMetadata.Profile != "planner" {
		t.Fatalf("result_metadata.profile = %q, want planner", preview.ResultMetadata.Profile)
	}
	if preview.Control.ControlRecord != dispatch.ControlRecordPath(preview.DispatchSpec.DispatchID) {
		t.Fatalf("control_record = %q, want %q", preview.Control.ControlRecord, dispatch.ControlRecordPath(preview.DispatchSpec.DispatchID))
	}
	if preview.Control.ArtifactDir != artifactDir {
		t.Fatalf("control.artifact_dir = %q, want %q", preview.Control.ArtifactDir, artifactDir)
	}
	if len(preview.PromptPreamble) != 1 {
		t.Fatalf("prompt_preamble len = %d, want 1 (%v)", len(preview.PromptPreamble), preview.PromptPreamble)
	}
	if preview.PromptPreamble[0] != "If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR." {
		t.Fatalf("prompt_preamble[0] = %q, want artifact preamble", preview.PromptPreamble[0])
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

	raw := decodeJSONMap(t, stdout.Bytes())
	resultMetadata, ok := raw["result_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("result_metadata = %#v, want object", raw["result_metadata"])
	}
	if got := resultMetadata["profile"]; got != "planner" {
		t.Fatalf("result_metadata.profile = %#v, want planner", got)
	}
}

func TestDispatchTTYConfirmationCancelsBeforeDispatch(t *testing.T) {
	isolateHome(t)

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
	if !strings.Contains(stderr.String(), `"kind":"preview"`) {
		t.Fatalf("stderr = %q, want preview JSON", stderr.String())
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

func TestDispatchNonTTYEmitsPreviewBeforeDispatch(t *testing.T) {
	isolateHome(t)

	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"--engine", "codex",
		"implement feature",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), `"kind":"preview"`) {
		t.Fatalf("stderr = %q, want preview JSON", stderr.String())
	}
	previewIdx := strings.Index(stderr.String(), `"kind": "preview"`)
	startIdx := strings.Index(stderr.String(), `"type":"dispatch_start"`)
	if startIdx == -1 {
		t.Fatalf("stderr = %q, want dispatch_start event after preview", stderr.String())
	}
	if previewIdx > startIdx {
		t.Fatalf("stderr = %q, want preview before dispatch_start", stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
	}
}

func TestDispatchTTYYesSkipsPreviewAndConfirmation(t *testing.T) {
	isolateHome(t)

	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithTerminalCheck([]string{
		"--engine", "codex",
		"--yes",
		"implement feature",
	}, strings.NewReader("n\n"), &stdout, &stderr, func(any) bool { return true })
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if strings.Contains(stderr.String(), `"kind":"preview"`) {
		t.Fatalf("stderr = %q, want no preview JSON when --yes is set", stderr.String())
	}
	if strings.Contains(stderr.String(), "Proceed with dispatch?") {
		t.Fatalf("stderr = %q, want no confirmation prompt when --yes is set", stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
	}
}

func TestPreviewCommandCompactsPromptSummary(t *testing.T) {
	isolateHome(t)

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
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			result := decodeResult(t, stdout.Bytes())
			if result.Error == nil || result.Error.Code != "invalid_args" {
				t.Fatalf("error = %#v, want invalid_args", result.Error)
			}
			if result.Error.Message != "missing prompt: provide the first positional arg or --prompt-file" {
				t.Fatalf("error.message = %q, want missing prompt", result.Error.Message)
			}
			if !strings.Contains(result.Error.Hint, fmt.Sprintf("If you meant the literal prompt %q", tc.command)) {
				t.Fatalf("error.hint = %q, want literal prompt guidance", result.Error.Hint)
			}
			if !strings.Contains(result.Error.Hint, fmt.Sprintf("agent-mux -- %s", tc.command)) {
				t.Fatalf("error.hint = %q, want -- escape hatch guidance", result.Error.Hint)
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
	if !spec.FullAccess {
		t.Fatal("full_access = false, want true")
	}
	// buildDispatchSpecE leaves GraceSec=0; defaults (proportional) are applied in run().
	if spec.GraceSec != 0 {
		t.Fatalf("grace_sec = %d, want 0 (computed later in run)", spec.GraceSec)
	}
	wantArtifactDirPath, err := dispatch.DefaultArtifactDir(spec.DispatchID)
	if err != nil {
		t.Fatalf("DefaultArtifactDir: %v", err)
	}
	wantArtifactDir := filepath.ToSlash(wantArtifactDirPath) + "/"
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

func TestBuildDispatchSpecPrefersProfileFlag(t *testing.T) {
	t.Parallel()

	fs, parsed := newFlagSet(ioDiscard{})
	err := fs.Parse([]string{"--engine", "codex", "--profile", "planner", "implement feature"})
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	flags, positional := *parsed, fs.Args()

	spec, err := buildDispatchSpecE(flags, positional)
	if err != nil {
		t.Fatalf("buildDispatchSpecE: %v", err)
	}
	if spec.DispatchAnnotations.Profile != "planner" {
		t.Fatalf("profile = %q, want %q", spec.DispatchAnnotations.Profile, "planner")
	}
}

// Tests for loadSystemPromptFile, prependSystemPrompt, and role-based hooks
// were removed when config.toml and roles were eliminated.


func TestRunRejectsDeniedSystemPromptContentViaHookDir(t *testing.T) {
	isolateHome(t)

	cwd := t.TempDir()
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTempHookScript(t, preDir, "deny-system-prompt.sh", `#!/bin/bash
if [[ "${HOOK_SYSTEM_PROMPT}" == *"blocked secret"* ]]; then
	echo "blocked secret" >&2
	exit 1
fi
exit 0
`)
	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{
		"--engine", "codex",
		"--cwd", cwd,
		"--artifact-dir", artifactDir,
		"--system-prompt", "do not expose blocked secret",
		"summarize the repository",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q", exitCode, stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, types.StatusFailed)
	}
	if result.Error == nil || result.Error.Code != "prompt_denied" {
		t.Fatalf("error = %#v, want prompt_denied", result.Error)
	}
	if !strings.Contains(result.Error.Message, `matched: "blocked secret"`) {
		t.Fatalf("error.message = %q, want matched hook reason", result.Error.Message)
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
	if len(spec.DispatchAnnotations.Skills) != 2 || spec.DispatchAnnotations.Skills[0] != "a" || spec.DispatchAnnotations.Skills[1] != "b" {
		t.Fatalf("skills = %#v, want []string{\"a\", \"b\"}", spec.DispatchAnnotations.Skills)
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
	isolateHome(t)

	t.Setenv("PATH", t.TempDir())

	input := types.DispatchSpec{
		DispatchID:  "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Engine:      "codex",
		Effort:      "high",
		Prompt:      "from stdin",
		Cwd:         "/tmp/project",
		ArtifactDir: filepath.Join(t.TempDir(), "artifacts") + "/",
		MaxDepth:    2,
		GraceSec:    60,
		FullAccess:  true,
		TimeoutSec:  5,
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

func TestStdinModeDefaultsYesForTTY(t *testing.T) {
	isolateHome(t)

	t.Setenv("PATH", t.TempDir())

	input := map[string]any{
		"dispatch_id":  "stdin-defaults-yes",
		"engine":       "codex",
		"prompt":       "from stdin",
		"cwd":          t.TempDir(),
		"artifact_dir": filepath.Join(t.TempDir(), "artifacts") + "/",
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithTerminalCheck([]string{"--stdin"}, bytes.NewReader(data), &stdout, &stderr, func(any) bool { return true })
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if strings.Contains(stderr.String(), `"kind":"preview"`) {
		t.Fatalf("stderr = %q, want no preview JSON when --stdin defaults --yes", stderr.String())
	}
	if strings.Contains(stderr.String(), "Proceed with dispatch?") {
		t.Fatalf("stderr = %q, want no confirmation prompt when --stdin defaults --yes", stderr.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %#v, want binary_not_found", result.Error)
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
	defaultArtifactDir, err := dispatch.DefaultArtifactDir(spec.DispatchID)
	if err != nil {
		t.Fatalf("DefaultArtifactDir: %v", err)
	}
	if spec.ArtifactDir != filepath.ToSlash(defaultArtifactDir)+"/" {
		t.Fatalf("artifact_dir = %q, want default path", spec.ArtifactDir)
	}
	if !spec.FullAccess {
		t.Fatal("full_access = false, want true")
	}
	// decodeStdinDispatchSpec leaves GraceSec=0 when not provided; proportional default applied in run().
	if spec.GraceSec != 0 {
		t.Fatalf("grace_sec = %d, want 0 (computed later in run)", spec.GraceSec)
	}
}

func TestDecodeStdinDispatchSpecPreservesExplicitFalse(t *testing.T) {
	spec, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","full_access":false}`))
	if err != nil {
		t.Fatalf("decodeStdinDispatchSpec: %v", err)
	}

	if spec.FullAccess {
		t.Fatal("full_access = true, want false")
	}
	// decodeStdinDispatchSpec leaves GraceSec=0 when not provided; proportional default applied in run().
	if spec.GraceSec != 0 {
		t.Fatalf("grace_sec = %d, want 0 (computed later in run)", spec.GraceSec)
	}
}

func TestDecodeStdinDispatchSpecRejectsNonPositiveTimeout(t *testing.T) {
	_, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","timeout_sec":0}`))
	if err == nil {
		t.Fatal("decodeStdinDispatchSpec error = nil, want invalid timeout_sec")
	}
	if !strings.Contains(err.Error(), `invalid timeout_sec "0"`) {
		t.Fatalf("error = %q, want invalid timeout_sec message", err)
	}
}

func TestDecodeStdinDispatchSpecRejectsNonPositiveGrace(t *testing.T) {
	_, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","grace_sec":0}`))
	if err == nil {
		t.Fatal("decodeStdinDispatchSpec error = nil, want invalid grace_sec")
	}
	if !strings.Contains(err.Error(), `invalid grace_sec "0"`) {
		t.Fatalf("error = %q, want invalid grace_sec message", err)
	}
}

func TestDecodeStdinDispatchSpecAcceptsLegacyCoordinatorAlias(t *testing.T) {
	spec, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","coordinator":"planner"}`))
	if err != nil {
		t.Fatalf("decodeStdinDispatchSpec: %v", err)
	}
	if spec.Profile != "planner" {
		t.Fatalf("profile = %q, want %q", spec.Profile, "planner")
	}
}

func TestDecodeStdinDispatchSpecRejectsConflictingProfileAlias(t *testing.T) {
	_, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","profile":"planner","coordinator":"legacy"}`))
	if err == nil {
		t.Fatal("decodeStdinDispatchSpec error = nil, want conflict")
	}
	if !strings.Contains(err.Error(), "conflicting profile values") {
		t.Fatalf("error = %q, want conflicting profile values", err)
	}
}

func TestDecodeStdinDispatchSpecRejectsInvalidDispatchID(t *testing.T) {
	_, err := decodeStdinDispatchSpec(strings.NewReader(`{"dispatch_id":"../bad","engine":"codex","prompt":"from stdin"}`))
	if err == nil {
		t.Fatal("decodeStdinDispatchSpec error = nil, want invalid dispatch_id")
	}
	if !strings.Contains(err.Error(), `invalid dispatch_id "../bad"`) {
		t.Fatalf("error = %q, want invalid dispatch_id message", err)
	}
}

func TestDecodeStdinDispatchSpecRejectsInvalidCoordinatorAliasBeforeConflictResolution(t *testing.T) {
	_, err := decodeStdinDispatchSpec(strings.NewReader(`{"engine":"codex","prompt":"from stdin","profile":"planner","coordinator":"../legacy"}`))
	if err == nil {
		t.Fatal("decodeStdinDispatchSpec error = nil, want conflict")
	}
	if !strings.Contains(err.Error(), `conflicting profile values`) {
		t.Fatalf("error = %q, want conflicting profile values message", err)
	}
}

func TestRunStdinRejectsInvalidDispatchID(t *testing.T) {
	input := []byte(`{"dispatch_id":"../bad","engine":"codex","prompt":"from stdin"}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--stdin"}, bytes.NewReader(input), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("error = %#v, want invalid_input", result.Error)
	}
}

func TestRunPreviewRejectsInvalidSkillName(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"preview", "--engine", "codex", "--skill", "../bad", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("error = %#v, want invalid_input", result.Error)
	}
}

func TestRunPreviewRejectsNonPositiveTimeoutFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"preview", "--engine", "codex", "--timeout", "0", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("error = %#v, want invalid_input", result.Error)
	}
}

func TestRunStdinRejectsNonPositiveGrace(t *testing.T) {
	input := []byte(`{"engine":"codex","prompt":"from stdin","grace_sec":0}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--stdin"}, bytes.NewReader(input), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("error = %#v, want invalid_input", result.Error)
	}
}

// TestRunPreviewRejectsConfigWithNonPositiveGrace removed — grace is now hardcoded.

func TestRunPreviewRejectsProfileWithNonPositiveTimeout(t *testing.T) {
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", agentsDir, err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "planner.md"), []byte("---\ntimeout: 0\n---\nplanner\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(planner.md): %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"preview", "--cwd", cwd, "--profile", "planner", "--engine", "codex", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("error = %#v, want invalid_input", result.Error)
	}
}

func TestSignalAndRecoverResolveCustomArtifactDispatch(t *testing.T) {
	isolateHome(t)

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
		_ = os.Remove(dispatch.ControlRecordPath(dispatchID))
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
	if _, err := os.Stat(filepath.Join(absoluteArtifactDir, "_dispatch_ref.json")); err != nil {
		t.Fatalf("stat dispatch ref: %v", err)
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

	messages, err := steer.ReadInbox(absoluteArtifactDir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Message != "focus on auth" {
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

func TestSteerNudgeUsesFIFOWhenReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO steering is Unix-only")
	}

	dispatchID, artifactDir := prepareSteerDispatchFixture(t, true)
	if err := steer.Create(steer.Path(artifactDir)); err != nil {
		t.Fatalf("Create(stdin.pipe): %v", err)
	}
	reader, err := steer.OpenReadNonblock(steer.Path(artifactDir))
	if err != nil {
		t.Fatalf("OpenReadNonblock(%q): %v", steer.Path(artifactDir), err)
	}
	defer reader.Close()

	var stdout bytes.Buffer
	exitCode := runSteerCommand([]string{dispatchID, "nudge", "fifo ready"}, &stdout, ioDiscard{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%q", exitCode, stdout.String())
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	if raw["mechanism"] != "stdin_fifo" {
		t.Fatalf("mechanism = %#v, want stdin_fifo", raw["mechanism"])
	}

	got := readFIFOTestPayload(t, reader)
	if !strings.Contains(got, `"action":"nudge"`) || !strings.Contains(got, `"message":"fifo ready"`) {
		t.Fatalf("FIFO payload = %q, want nudge envelope", got)
	}
}

func TestSteerNudgeFallsBackToInboxWhenFIFOUnavailable(t *testing.T) {
	dispatchID, artifactDir := prepareSteerDispatchFixture(t, false)

	var stdout bytes.Buffer
	exitCode := runSteerCommand([]string{dispatchID, "nudge", "fallback nudge"}, &stdout, ioDiscard{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%q", exitCode, stdout.String())
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	if raw["mechanism"] != "inbox" {
		t.Fatalf("mechanism = %#v, want inbox", raw["mechanism"])
	}

	messages, err := steer.ReadInbox(artifactDir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Message != "[NUDGE] fallback nudge" {
		t.Fatalf("messages = %#v, want [NUDGE] fallback nudge", messages)
	}
}

func TestSteerRedirectFIFOWriteErrorsFallbackToInbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO steering is Unix-only")
	}

	dispatchID, artifactDir := prepareSteerDispatchFixture(t, true)
	if err := steer.Create(steer.Path(artifactDir)); err != nil {
		t.Fatalf("Create(stdin.pipe): %v", err)
	}

	var stdout bytes.Buffer
	exitCode := runSteerCommand([]string{dispatchID, "redirect", "switch focus"}, &stdout, ioDiscard{})
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%q", exitCode, stdout.String())
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	if raw["mechanism"] != "inbox" {
		t.Fatalf("mechanism = %#v, want inbox", raw["mechanism"])
	}

	messages, err := steer.ReadInbox(artifactDir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(messages) != 1 || messages[0].Message != "[REDIRECT] switch focus" {
		t.Fatalf("messages = %#v, want [REDIRECT] switch focus", messages)
	}
}

func TestSteerUsageNoLongerAdvertisesStatus(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{"steer", "only-id"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	raw := decodeJSONMap(t, stdout.Bytes())
	errorEnvelope, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatalf("error = %#v, want object", raw["error"])
	}
	hint, ok := errorEnvelope["hint"].(string)
	if !ok {
		t.Fatalf("hint = %#v, want string", errorEnvelope["hint"])
	}
	if strings.Contains(hint, "status") {
		t.Fatalf("hint = %q, want steer actions without status", hint)
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

func TestRunPrependsContextFilePreamble(t *testing.T) {
	isolateHome(t)

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
	if meta.PromptHash != promptHash(prompt) {
		t.Fatalf("prompt_hash = %q, want %q", meta.PromptHash, promptHash(prompt))
	}
}

func TestRunFailsWhenContextFileMissing(t *testing.T) {
	isolateHome(t)

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
	isolateHome(t)

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

func TestRunHookScriptDoesNotInjectIntoPrompt(t *testing.T) {
	isolateHome(t)

	cwd := t.TempDir()
	artifactDir := filepath.Join(t.TempDir(), "artifacts") + "/"
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTempHookScript(t, preDir, "allow.sh", "#!/bin/bash\nexit 0\n")
	t.Setenv("PATH", t.TempDir())

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	prompt := "summarize the current repository state"
	exitCode := run([]string{
		"--engine", "codex",
		"--cwd", cwd,
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
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func writeTempHookScript(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTestSkillFile(t *testing.T, cwd, name, content string) {
	t.Helper()

	path := filepath.Join(cwd, ".claude", "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func writeStoreRecord(t *testing.T, record dispatch.DispatchRecord, response string, writeResult bool) {
	t.Helper()

	spec := &types.DispatchSpec{
		DispatchID:  record.ID,
		Engine:      record.Engine,
		Model:       record.Model,
		Effort:      record.Effort,
		Cwd:         record.Cwd,
		ArtifactDir: record.ArtifactDir,
		TimeoutSec:  record.TimeoutSec,
		Prompt:      "test prompt",
	}
	if err := os.MkdirAll(record.ArtifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(artifactDir): %v", err)
	}
	annotations := types.DispatchAnnotations{Profile: record.Profile}
	if err := dispatch.WritePersistentMeta(spec, annotations); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := dispatch.WriteDispatchRef(record.ArtifactDir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}
	if record.SessionID != "" {
		if err := dispatch.UpdateDispatchSessionID(record.ArtifactDir, record.SessionID); err != nil {
			t.Fatalf("UpdateDispatchSessionID: %v", err)
		}
	}
	if writeResult {
		result := &types.DispatchResult{
			SchemaVersion:     1,
			Status:            types.DispatchStatus(record.Status),
			DispatchID:        record.ID,
			Response:          response,
			ResponseTruncated: record.Truncated,
			Metadata: &types.DispatchMetadata{
				Engine:    record.Engine,
				Model:     record.Model,
				Profile:   record.Profile,
				SessionID: record.SessionID,
				Tokens:    &types.TokenUsage{},
			},
			Activity:   &types.DispatchActivity{},
			DurationMS: record.DurationMs,
		}
		if err := dispatch.WritePersistentResult(spec, annotations, result, response, record.StartedAt, record.EndedAt); err != nil {
			t.Fatalf("WritePersistentResult: %v", err)
		}
	}
}

func testStoreRecord(id, status string) dispatch.DispatchRecord {
	artifactDir, err := dispatch.DefaultArtifactDir(id)
	if err != nil {
		panic(err)
	}

	return dispatch.DispatchRecord{
		ID:            id,
		Status:        status,
		Engine:        "codex",
		Model:         "gpt-5.4",
		StartedAt:     "2026-03-28T13:45:00Z",
		EndedAt:       "2026-03-28T13:58:44Z",
		DurationMs:    824000,
		Cwd:           "/Users/otonashi/thinking/building/agent-mux",
		Truncated:     true,
		ResponseChars: 3817,
		ArtifactDir:   artifactDir,
	}
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
		Model      string `json:"model"`
		Effort     string `json:"effort"`
		TimeoutSec int    `json:"timeout_sec"`
	} `json:"dispatch_spec"`
	ResultMetadata struct {
		Profile string   `json:"profile"`
		Skills  []string `json:"skills"`
	} `json:"result_metadata"`
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

func prepareSteerDispatchFixture(t *testing.T, stdinPipeReady bool) (string, string) {
	t.Helper()

	isolateHome(t)

	dispatchID := fmt.Sprintf("01STEER%018d", time.Now().UnixNano()%1_000_000_000_000_000_000)
	artifactDir := t.TempDir()
	spec := &types.DispatchSpec{
		DispatchID:  dispatchID,
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "steer test",
		Cwd:         artifactDir,
		ArtifactDir: artifactDir,
	}
	if err := dispatch.EnsureArtifactDir(artifactDir); err != nil {
		t.Fatalf("EnsureArtifactDir: %v", err)
	}
	if err := dispatch.WritePersistentMeta(spec, types.DispatchAnnotations{}); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := dispatch.WriteDispatchRef(artifactDir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}
	if err := dispatch.WriteStatusJSON(artifactDir, dispatch.LiveStatus{
		State:          "running",
		ElapsedS:       1,
		LastActivity:   "testing",
		ToolsUsed:      0,
		FilesChanged:   0,
		StdinPipeReady: stdinPipeReady,
		DispatchID:     dispatchID,
	}); err != nil {
		t.Fatalf("WriteStatusJSON: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "host.pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatalf("write host.pid: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(dispatch.ControlRecordPath(dispatchID))
	})
	return dispatchID, artifactDir
}

func readFIFOTestPayload(t *testing.T, reader *os.File) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := reader.Read(buf)
		if n > 0 {
			return string(buf[:n])
		}
		if err == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out reading FIFO payload")
	return ""
}

func readDispatchMeta(t *testing.T, artifactDir string) dispatchMetaForTest {
	t.Helper()

	meta, err := dispatch.ReadDispatchMeta(artifactDir)
	if err != nil {
		t.Fatalf("read dispatch meta: %v", err)
	}
	return dispatchMetaForTest{
		PromptHash: meta.PromptHash,
		Cwd:        meta.Cwd,
	}
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

// Role effort/skills tests removed — roles are eliminated.


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
