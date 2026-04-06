package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestGeminiBuildArgs(t *testing.T) {
	a := &GeminiAdapter{}

	spec := &types.DispatchSpec{
		Model:        "gemini-2.5-pro",
		Prompt:       "Build the parser",
		SystemPrompt: "ignored here",
	}

	args := a.BuildArgs(spec)

	assertContains(t, args, "-p")
	assertContains(t, args, "-o")
	assertContains(t, args, "stream-json")
	assertContains(t, args, "-m")
	assertContains(t, args, "gemini-2.5-pro")
	assertContains(t, args, "--approval-mode")
	assertContains(t, args, "yolo")
	promptIndex := indexOf(args, "-p")
	if promptIndex == -1 || promptIndex+1 >= len(args) {
		t.Fatalf("missing -p prompt position in args: %#v", args)
	}
	if args[promptIndex+1] != "Build the parser" {
		t.Fatalf("prompt arg = %q, want prompt", args[promptIndex+1])
	}
}

func TestGeminiBuildArgsPermissionMode(t *testing.T) {
	a := &GeminiAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "Build the parser",
		EngineOpts: map[string]any{
			"permission-mode": "plan",
		},
	}

	args := a.BuildArgs(spec)

	assertContains(t, args, "--approval-mode")
	assertContains(t, args, "plan")
}

func TestGeminiBuildArgsEmptyPermissionModeDefaultsToYolo(t *testing.T) {
	a := &GeminiAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "Build the parser",
		EngineOpts: map[string]any{
			"permission-mode": "",
		},
	}

	args := a.BuildArgs(spec)

	assertContains(t, args, "--approval-mode")
	assertContains(t, args, "yolo")
	assertNotContains(t, args, "plan")
}

func TestGeminiBuildArgsWithAddDirs(t *testing.T) {
	a := &GeminiAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "test prompt",
		EngineOpts: map[string]any{
			"add-dir": []any{"/tmp/scripts", "/tmp/helpers"},
		},
	}

	args := a.BuildArgs(spec)
	assertContains(t, args, "--include-directories")
	idx := indexOf(args, "--include-directories")
	if idx == -1 || idx+1 >= len(args) {
		t.Fatalf("missing --include-directories value in args: %#v", args)
	}
	val := args[idx+1]
	if !strings.Contains(val, "/tmp/scripts") || !strings.Contains(val, "/tmp/helpers") {
		t.Fatalf("--include-directories value = %q, want both dirs", val)
	}
}

func TestGeminiBuildArgsNoIncludeDirsWhenEmpty(t *testing.T) {
	a := &GeminiAdapter{}

	spec := &types.DispatchSpec{
		Prompt:     "test prompt",
		EngineOpts: map[string]any{},
	}

	args := a.BuildArgs(spec)
	assertNotContains(t, args, "--include-directories")
}

func TestGeminiEnvVarsWritesSystemPromptFile(t *testing.T) {
	a := &GeminiAdapter{}
	spec := &types.DispatchSpec{
		SystemPrompt: "You are a Go expert.",
		ArtifactDir:  t.TempDir(),
	}

	env, err := a.EnvVars(spec)
	if err != nil {
		t.Fatalf("EnvVars: %v", err)
	}
	if len(env) != 1 {
		t.Fatalf("env = %#v, want single GEMINI_SYSTEM_MD", env)
	}
	want := "GEMINI_SYSTEM_MD=" + filepath.Join(spec.ArtifactDir, "system_prompt.md")
	if env[0] != want {
		t.Fatalf("env[0] = %q, want %q", env[0], want)
	}
	data, err := os.ReadFile(filepath.Join(spec.ArtifactDir, "system_prompt.md"))
	if err != nil {
		t.Fatalf("read system_prompt.md: %v", err)
	}
	if string(data) != spec.SystemPrompt {
		t.Fatalf("system_prompt.md = %q, want %q", string(data), spec.SystemPrompt)
	}
}

func TestGeminiEnvVarsReturnsWriteError(t *testing.T) {
	a := &GeminiAdapter{}
	artifactPath := filepath.Join(t.TempDir(), "artifact-file")
	if err := os.WriteFile(artifactPath, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
	spec := &types.DispatchSpec{
		SystemPrompt: "You are a Go expert.",
		ArtifactDir:  artifactPath,
	}

	env, err := a.EnvVars(spec)
	if err == nil {
		t.Fatal("EnvVars error = nil, want write failure")
	}
	if env != nil {
		t.Fatalf("env = %#v, want nil", env)
	}
	if !strings.Contains(err.Error(), "write Gemini system prompt") {
		t.Fatalf("error = %q, want Gemini system prompt context", err)
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func TestGeminiParseInit(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"init","session_id":"gem-session-789xyz","model":"gemini-2.5-pro"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventSessionStart {
		t.Fatalf("kind = %d, want EventSessionStart", evt.Kind)
	}
	if evt.SessionID != "gem-session-789xyz" {
		t.Fatalf("session_id = %q", evt.SessionID)
	}
}

// TestGeminiParseMessage tests a non-delta assistant message (backward compat).
func TestGeminiParseMessage(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"message","role":"assistant","content":"I'll analyze the project structure first."}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventProgress {
		t.Fatalf("kind = %d, want EventProgress", evt.Kind)
	}
	if evt.Text != "I'll analyze the project structure first." {
		t.Fatalf("text = %q", evt.Text)
	}
}

// TestGeminiParseDeltaMessage tests that delta:true messages emit EventProgress with the fragment.
func TestGeminiParseDeltaMessage(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"message","role":"assistant","content":"I'll create","delta":true}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventProgress {
		t.Fatalf("kind = %d, want EventProgress", evt.Kind)
	}
	if evt.Text != "I'll create" {
		t.Fatalf("text = %q, want fragment", evt.Text)
	}
}

// TestGeminiDeltaAccumulation tests that a sequence of delta messages is accumulated and
// flushed as the result event's response text.
func TestGeminiDeltaAccumulation(t *testing.T) {
	a := &GeminiAdapter{}

	lines := []string{
		`{"type":"message","timestamp":"2026-04-05T10:00:00Z","role":"assistant","content":"I'll create","delta":true}`,
		`{"type":"message","timestamp":"2026-04-05T10:00:01Z","role":"assistant","content":" a hello world file","delta":true}`,
		`{"type":"result","timestamp":"2026-04-05T10:00:02Z","session_id":"gem-session-789xyz","status":"success","stats":{"input_tokens":500,"output_tokens":200}}`,
	}

	var lastEvt *types.HarnessEvent
	for _, line := range lines {
		evt, err := a.ParseEvent(line)
		if err != nil {
			t.Fatalf("ParseEvent(%q): %v", line, err)
		}
		if evt != nil {
			lastEvt = evt
		}
	}

	if lastEvt == nil {
		t.Fatal("expected a result event, got nil")
	}
	if lastEvt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", lastEvt.Kind)
	}
	want := "I'll create a hello world file"
	if lastEvt.Text != want {
		t.Fatalf("text = %q, want %q", lastEvt.Text, want)
	}
	if lastEvt.SessionID != "gem-session-789xyz" {
		t.Fatalf("session_id = %q", lastEvt.SessionID)
	}
	if lastEvt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if lastEvt.Tokens.Input != 500 || lastEvt.Tokens.Output != 200 {
		t.Fatalf("tokens = %+v", lastEvt.Tokens)
	}

	// Delta buffer should be reset after flush — a second result emits empty text (not accumulated again).
	resultLine := `{"type":"result","session_id":"gem-session-789xyz","status":"success"}`
	evt2, err := a.ParseEvent(resultLine)
	if err != nil {
		t.Fatalf("ParseEvent second result: %v", err)
	}
	if evt2 == nil {
		t.Fatal("expected event from second result")
	}
	if evt2.Text != "" {
		t.Fatalf("delta buffer should be reset; text = %q, want empty", evt2.Text)
	}
}

// TestGeminiParseToolUseReadFile_NewSchema tests read_file using the v0.34.0 field names.
func TestGeminiParseToolUseReadFile_NewSchema(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"tu_001","tool_name":"read_file","parameters":{"file_path":"main.go"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventFileRead {
		t.Fatalf("kind = %d, want EventFileRead", evt.Kind)
	}
	if evt.FilePath != "main.go" {
		t.Fatalf("file_path = %q", evt.FilePath)
	}
}

// TestGeminiParseToolUseReadFile_LegacySchema tests read_file using old field names (backward compat).
func TestGeminiParseToolUseReadFile_LegacySchema(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"call_001","name":"read_file","input":{"path":"src/main.go"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventFileRead {
		t.Fatalf("kind = %d, want EventFileRead", evt.Kind)
	}
	if evt.FilePath != "src/main.go" {
		t.Fatalf("file_path = %q", evt.FilePath)
	}
}

// TestGeminiParseToolUseRunShellCommand tests run_shell_command (new v0.34.0 tool name).
func TestGeminiParseToolUseRunShellCommand(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"tu_003","tool_name":"run_shell_command","parameters":{"command":"node hello.js","description":"Run hello"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventCommandRun {
		t.Fatalf("kind = %d, want EventCommandRun", evt.Kind)
	}
	if evt.Command != "node hello.js" {
		t.Fatalf("command = %q", evt.Command)
	}
}

// TestGeminiParseToolUseShell tests the legacy "shell" tool name still works.
func TestGeminiParseToolUseShell(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"call_002","name":"shell","input":{"command":"go test ./..."}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventCommandRun {
		t.Fatalf("kind = %d, want EventCommandRun", evt.Kind)
	}
	if evt.Command != "go test ./..." {
		t.Fatalf("command = %q", evt.Command)
	}
}

// TestGeminiParseToolUseReplace tests the replace tool.
func TestGeminiParseToolUseReplace(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"tu_004","tool_name":"replace","parameters":{"file_path":"hello.js","old_string":"hello","new_string":"world"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventFileWrite {
		t.Fatalf("kind = %d, want EventFileWrite", evt.Kind)
	}
	if evt.Tool != "replace" {
		t.Fatalf("tool = %q", evt.Tool)
	}

	// Verify pending file was recorded.
	a.mu.Lock()
	path := a.pendingFiles["tu_004"]
	a.mu.Unlock()
	if path != "hello.js" {
		t.Fatalf("pendingFiles[tu_004] = %q, want hello.js", path)
	}
}

// TestGeminiParseToolUseListDirectory tests the list_directory tool.
func TestGeminiParseToolUseListDirectory(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"tu_005","tool_name":"list_directory","parameters":{"dir_path":"."}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want EventToolStart", evt.Kind)
	}
	if evt.Tool != "list_directory" {
		t.Fatalf("tool = %q", evt.Tool)
	}
	if evt.FilePath != "." {
		t.Fatalf("file_path (dir_path) = %q", evt.FilePath)
	}
}

// TestGeminiParseToolUseOther tests an unrecognized tool falls through to EventToolStart.
func TestGeminiParseToolUseOther(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"call_004","name":"glob","input":{"path":"*.go"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want EventToolStart", evt.Kind)
	}
	if evt.Tool != "glob" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

// TestGeminiParseToolUseWriteFile_NewSchema tests write_file using v0.34.0 field names.
func TestGeminiParseToolUseWriteFile_NewSchema(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"tu_002","tool_name":"write_file","parameters":{"file_path":"hello.js","content":"console.log('hello')"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want EventToolStart", evt.Kind)
	}
	if evt.Tool != "write_file" {
		t.Fatalf("tool = %q", evt.Tool)
	}

	a.mu.Lock()
	path := a.pendingFiles["tu_002"]
	a.mu.Unlock()
	if path != "hello.js" {
		t.Fatalf("pendingFiles[tu_002] = %q, want hello.js", path)
	}
}

// TestGeminiParseToolUseWriteFile_LegacySchema tests write_file using old field names.
func TestGeminiParseToolUseWriteFile_LegacySchema(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"tool_use","tool_id":"call_003","name":"write_file","input":{"path":"internal/parser/parser.go","content":"package parser"}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want EventToolStart", evt.Kind)
	}
	if evt.Tool != "write_file" {
		t.Fatalf("tool = %q", evt.Tool)
	}

	a.mu.Lock()
	path := a.pendingFiles["call_003"]
	a.mu.Unlock()
	if path != "internal/parser/parser.go" {
		t.Fatalf("pendingFiles[call_003] = %q", path)
	}
}

// TestGeminiToolIDCorrelation_NewSchema tests write_file → tool_result correlation using v0.34.0 schema.
// The tool_result does NOT include tool_name — the adapter must look it up from toolNames map.
func TestGeminiToolIDCorrelation_NewSchema(t *testing.T) {
	a := &GeminiAdapter{}

	toolUse := `{"type":"tool_use","tool_id":"tu_002","tool_name":"write_file","parameters":{"file_path":"hello.js","content":"console.log('hello')"}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	// tool_result in v0.34.0 has only tool_id and status — no tool_name.
	toolResult := `{"type":"tool_result","tool_id":"tu_002","status":"success"}`
	evt, err := a.ParseEvent(toolResult)
	if err != nil {
		t.Fatalf("ParseEvent(tool_result): %v", err)
	}
	if evt.Kind != types.EventFileWrite {
		t.Fatalf("kind = %d, want EventFileWrite", evt.Kind)
	}
	if evt.FilePath != "hello.js" {
		t.Fatalf("file_path = %q", evt.FilePath)
	}

	a.mu.Lock()
	_, pendingOk := a.pendingFiles["tu_002"]
	_, toolNameOk := a.toolNames["tu_002"]
	a.mu.Unlock()
	if pendingOk {
		t.Fatal("pending file mapping should be removed after correlated tool_result")
	}
	if toolNameOk {
		t.Fatal("tool name mapping should be removed after correlated tool_result")
	}
}

// TestGeminiToolIDCorrelation tests the legacy schema where tool_result carries tool name directly.
func TestGeminiToolIDCorrelation(t *testing.T) {
	a := &GeminiAdapter{}

	toolUse := `{"type":"tool_use","tool_id":"call_003","name":"write_file","input":{"path":"internal/parser/parser.go","content":"package parser"}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	toolResult := `{"type":"tool_result","tool_id":"call_003","name":"write_file","output":"written","is_error":false,"duration_ms":100}`
	evt, err := a.ParseEvent(toolResult)
	if err != nil {
		t.Fatalf("ParseEvent(tool_result): %v", err)
	}
	if evt.Kind != types.EventFileWrite {
		t.Fatalf("kind = %d, want EventFileWrite", evt.Kind)
	}
	if evt.FilePath != "internal/parser/parser.go" {
		t.Fatalf("file_path = %q", evt.FilePath)
	}

	a.mu.Lock()
	_, ok := a.pendingFiles["call_003"]
	a.mu.Unlock()
	if ok {
		t.Fatal("pending file mapping should be removed after correlated tool_result")
	}
}

// TestGeminiParseToolResultRunShellCommand tests tool_result for run_shell_command.
func TestGeminiParseToolResultRunShellCommand(t *testing.T) {
	a := &GeminiAdapter{}

	toolUse := `{"type":"tool_use","tool_id":"tu_003","tool_name":"run_shell_command","parameters":{"command":"node hello.js","description":"Run hello"}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	toolResult := `{"type":"tool_result","tool_id":"tu_003","status":"success","output":"hello"}`
	evt, err := a.ParseEvent(toolResult)
	if err != nil {
		t.Fatalf("ParseEvent(tool_result): %v", err)
	}
	if evt.Kind != types.EventToolEnd {
		t.Fatalf("kind = %d, want EventToolEnd", evt.Kind)
	}
	if evt.SecondaryKind != types.EventCommandRun {
		t.Fatalf("secondary kind = %d, want EventCommandRun", evt.SecondaryKind)
	}
	if evt.Tool != "run_shell_command" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

// TestGeminiParseToolResultReplace tests tool_result for replace tool.
func TestGeminiParseToolResultReplace(t *testing.T) {
	a := &GeminiAdapter{}

	toolUse := `{"type":"tool_use","tool_id":"tu_004","tool_name":"replace","parameters":{"file_path":"hello.js","old_string":"hello","new_string":"world"}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	toolResult := `{"type":"tool_result","tool_id":"tu_004","status":"success"}`
	evt, err := a.ParseEvent(toolResult)
	if err != nil {
		t.Fatalf("ParseEvent(tool_result): %v", err)
	}
	if evt.Kind != types.EventFileWrite {
		t.Fatalf("kind = %d, want EventFileWrite", evt.Kind)
	}
	if evt.FilePath != "hello.js" {
		t.Fatalf("file_path = %q, want hello.js", evt.FilePath)
	}
}

// TestGeminiParseToolResultListDirectory tests tool_result for list_directory.
func TestGeminiParseToolResultListDirectory(t *testing.T) {
	a := &GeminiAdapter{}

	toolUse := `{"type":"tool_use","tool_id":"tu_005","tool_name":"list_directory","parameters":{"dir_path":"."}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	toolResult := `{"type":"tool_result","tool_id":"tu_005","status":"success","output":"hello.js\nmain.go"}`
	evt, err := a.ParseEvent(toolResult)
	if err != nil {
		t.Fatalf("ParseEvent(tool_result): %v", err)
	}
	if evt.Kind != types.EventToolEnd {
		t.Fatalf("kind = %d, want EventToolEnd", evt.Kind)
	}
	if evt.Tool != "list_directory" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

// TestGeminiParseToolResultShell tests the legacy shell tool result.
func TestGeminiParseToolResultShell(t *testing.T) {
	a := &GeminiAdapter{}

	// First register the tool_use so toolNames is populated.
	toolUse := `{"type":"tool_use","tool_id":"call_002","name":"shell","input":{"command":"go test ./..."}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	line := `{"type":"tool_result","tool_id":"call_002","name":"shell","output":"ok\t...","exit_code":0,"is_error":false,"duration_ms":1200}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolEnd {
		t.Fatalf("kind = %d, want EventToolEnd", evt.Kind)
	}
	if evt.SecondaryKind != types.EventCommandRun {
		t.Fatalf("secondary kind = %d, want EventCommandRun", evt.SecondaryKind)
	}
	if evt.Tool != "shell" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

// TestGeminiParseToolResultError_NewSchema tests tool error with new status field.
func TestGeminiParseToolResultError_NewSchema(t *testing.T) {
	a := &GeminiAdapter{}

	toolUse := `{"type":"tool_use","tool_id":"tu_003","tool_name":"run_shell_command","parameters":{"command":"node hello.js"}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	line := `{"type":"tool_result","tool_id":"tu_003","status":"error","output":"command not found"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventError {
		t.Fatalf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "tool_error" {
		t.Fatalf("error_code = %q", evt.ErrorCode)
	}
}

// TestGeminiParseToolResultError tests the legacy is_error field.
func TestGeminiParseToolResultError(t *testing.T) {
	a := &GeminiAdapter{}

	// Register the tool_use first.
	toolUse := `{"type":"tool_use","tool_id":"call_002","name":"shell","input":{"command":"go test ./..."}}`
	if _, err := a.ParseEvent(toolUse); err != nil {
		t.Fatalf("ParseEvent(tool_use): %v", err)
	}

	line := `{"type":"tool_result","tool_id":"call_002","name":"shell","output":"failed","is_error":true,"duration_ms":1200}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventError {
		t.Fatalf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "tool_error" {
		t.Fatalf("error_code = %q", evt.ErrorCode)
	}
}

func TestGeminiParseError(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"error","code":"auth_failed","message":"Google Cloud authentication failed. Run gcloud auth login."}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventError {
		t.Fatalf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "auth_failed" {
		t.Fatalf("error_code = %q", evt.ErrorCode)
	}
	if evt.Text == "" {
		t.Fatal("error text should be populated")
	}
}

// TestGeminiParseResult_NewSchema tests the real v0.34.0 result event format (no text field).
func TestGeminiParseResult_NewSchema(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"gem-session-789xyz","status":"success","stats":{"total_tokens":52000,"input_tokens":38000,"output_tokens":14000,"duration_ms":62000,"tool_calls":8,"turns":6}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.SessionID != "gem-session-789xyz" {
		t.Fatalf("session_id = %q", evt.SessionID)
	}
	// No delta messages were emitted, so text should be empty.
	if evt.Text != "" {
		t.Fatalf("text = %q, want empty (no deltas accumulated)", evt.Text)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 38000 || evt.Tokens.Output != 14000 {
		t.Fatalf("tokens = %+v", evt.Tokens)
	}
}

// TestGeminiParseResult_LegacySchema tests backward compat with old result events that carry "result" text.
func TestGeminiParseResult_LegacySchema(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"gem-session-789xyz","result":"Parser built and tested. 3 files modified.","stats":{"total_tokens":52000,"input_tokens":38000,"output_tokens":14000,"duration_ms":62000,"tool_calls":8,"turns":6}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.SessionID != "gem-session-789xyz" {
		t.Fatalf("session_id = %q", evt.SessionID)
	}
	// No delta buffer content, so falls back to raw.Result.
	if evt.Text != "Parser built and tested. 3 files modified." {
		t.Fatalf("text = %q", evt.Text)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 38000 || evt.Tokens.Output != 14000 {
		t.Fatalf("tokens = %+v", evt.Tokens)
	}
}

// TestGeminiParseResult_ErrorStatus tests that result event with status:"error" emits EventError.
func TestGeminiParseResult_ErrorStatus(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"gem-session-789xyz","status":"error","message":"model overloaded"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventError {
		t.Fatalf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "result_error" {
		t.Fatalf("error_code = %q", evt.ErrorCode)
	}
}

// --- Fix 11: Non-JSON stderr surfaced as EventRawPassthrough ---

func TestGeminiParseNonJSON(t *testing.T) {
	a := &GeminiAdapter{}

	evt, err := a.ParseEvent("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt == nil {
		t.Fatal("expected EventRawPassthrough for non-JSON line, got nil")
	}
	if evt.Kind != types.EventRawPassthrough {
		t.Fatalf("kind = %d, want EventRawPassthrough", evt.Kind)
	}
	if string(evt.Raw) != "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain." {
		t.Fatalf("raw = %q", string(evt.Raw))
	}
}

func TestGeminiParseNonJSONWarning(t *testing.T) {
	a := &GeminiAdapter{}

	evt, err := a.ParseEvent("WARNING: model context window exceeded, truncating")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt == nil {
		t.Fatal("expected EventRawPassthrough for warning line, got nil")
	}
	if evt.Kind != types.EventRawPassthrough {
		t.Fatalf("kind = %d, want EventRawPassthrough", evt.Kind)
	}
}

func TestGeminiParseEmptyLine(t *testing.T) {
	a := &GeminiAdapter{}

	evt, err := a.ParseEvent("")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Fatalf("event = %#v, want nil", evt)
	}
}

func TestGeminiParseWhitespaceLine(t *testing.T) {
	a := &GeminiAdapter{}

	evt, err := a.ParseEvent("   \t  ")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Fatalf("event = %#v, want nil for whitespace-only line", evt)
	}
}

func TestGeminiSupportsResume(t *testing.T) {
	a := &GeminiAdapter{}
	if !a.SupportsResume() {
		t.Fatal("Gemini should support resume")
	}
}

// --- Fix 9: Resume session ID format ---

func TestGeminiResumeArgs_NumericIndex(t *testing.T) {
	a := &GeminiAdapter{}
	args := a.ResumeArgs(nil, "3", "continue")
	want := []string{"--resume", "3", "-p", "continue"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestGeminiResumeArgs_Latest(t *testing.T) {
	a := &GeminiAdapter{}
	args := a.ResumeArgs(nil, "latest", "continue")
	want := []string{"--resume", "latest", "-p", "continue"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestGeminiResumeArgs_NonUUIDSessionID(t *testing.T) {
	a := &GeminiAdapter{}
	// Short non-UUID ID — should be passed through as-is.
	args := a.ResumeArgs(nil, "gem-session-789xyz", "resume")
	want := []string{"--resume", "gem-session-789xyz", "-p", "resume"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestGeminiResumeArgs_UUIDSessionID(t *testing.T) {
	a := &GeminiAdapter{}
	// UUID session ID — should be replaced with "latest".
	args := a.ResumeArgs(nil, "550e8400-e29b-41d4-a716-446655440000", "continue the work")
	if len(args) != 4 {
		t.Fatalf("args = %v, want 4 elements", args)
	}
	if args[0] != "--resume" {
		t.Fatalf("args[0] = %q, want --resume", args[0])
	}
	if args[1] != "latest" {
		t.Fatalf("args[1] = %q, want \"latest\" (UUID should be replaced)", args[1])
	}
	if args[2] != "-p" || args[3] != "continue the work" {
		t.Fatalf("args[2:] = %v, want [-p, continue the work]", args[2:])
	}
}

// --- Fix 7: Approval mode validation ---

func TestValidateGeminiApprovalMode_ValidModes(t *testing.T) {
	validModes := []string{"default", "auto_edit", "yolo", "plan"}
	for _, mode := range validModes {
		if err := ValidateGeminiApprovalMode(mode); err != nil {
			t.Errorf("ValidateGeminiApprovalMode(%q) = %v, want nil", mode, err)
		}
	}
}

func TestValidateGeminiApprovalMode_InvalidMode(t *testing.T) {
	invalidModes := []string{"full_auto", "ask", "", "YOLO", "Default"}
	for _, mode := range invalidModes {
		err := ValidateGeminiApprovalMode(mode)
		if err == nil {
			t.Errorf("ValidateGeminiApprovalMode(%q) = nil, want error", mode)
			continue
		}
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("error = %q, want to mention %q", err, mode)
		}
		if !strings.Contains(err.Error(), "yolo") {
			t.Errorf("error = %q, want to list valid values", err)
		}
	}
}

// --- Fix 8: Result event error status (additional coverage) ---

func TestGeminiParseResult_ErrorStatusWithMessage(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"gem-session-err","status":"error","message":"context length exceeded"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventError {
		t.Fatalf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "result_error" {
		t.Fatalf("error_code = %q, want result_error", evt.ErrorCode)
	}
	if evt.Text != "context length exceeded" {
		t.Fatalf("text = %q, want error message", evt.Text)
	}
}

func TestGeminiParseResult_ErrorStatusEmptyMessage(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"gem-session-err","status":"error"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventError {
		t.Fatalf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "result_error" {
		t.Fatalf("error_code = %q, want result_error", evt.ErrorCode)
	}
}

// --- Per-model token breakdown and auto-routing ---

// TestGeminiStats_SingleModel tests that a single model in the models map
// produces correct token extraction and captures the actual model name.
func TestGeminiStats_SingleModel(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"s1","status":"success","stats":{"total_tokens":700,"input_tokens":500,"output_tokens":200,"cached":50,"input":500,"duration_ms":3200,"tool_calls":2,"models":{"gemini-2.5-flash-preview-04-17":{"total_tokens":700,"input_tokens":500,"output_tokens":200,"cached":50,"input":500}}}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 500 {
		t.Fatalf("tokens.Input = %d, want 500", evt.Tokens.Input)
	}
	if evt.Tokens.Output != 200 {
		t.Fatalf("tokens.Output = %d, want 200", evt.Tokens.Output)
	}
	if evt.Tokens.CacheRead != 50 {
		t.Fatalf("tokens.CacheRead = %d, want 50", evt.Tokens.CacheRead)
	}
	if evt.ActualModel != "gemini-2.5-flash-preview-04-17" {
		t.Fatalf("actual_model = %q, want gemini-2.5-flash-preview-04-17", evt.ActualModel)
	}
}

// TestGeminiStats_MultipleModels tests auto-routing where multiple models appear
// in the stats breakdown. The model with the most output_tokens should be picked.
func TestGeminiStats_MultipleModels(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"s2","status":"success","stats":{"total_tokens":1500,"input_tokens":1000,"output_tokens":500,"cached":0,"input":1000,"duration_ms":5000,"tool_calls":3,"models":{"gemini-2.5-flash-preview-04-17":{"total_tokens":400,"input_tokens":300,"output_tokens":100,"cached":0,"input":300},"gemini-2.5-pro-preview-05-06":{"total_tokens":1100,"input_tokens":700,"output_tokens":400,"cached":0,"input":700}}}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.ActualModel != "gemini-2.5-pro-preview-05-06" {
		t.Fatalf("actual_model = %q, want gemini-2.5-pro-preview-05-06 (most output tokens)", evt.ActualModel)
	}
	if evt.Tokens.Input != 1000 {
		t.Fatalf("tokens.Input = %d, want 1000", evt.Tokens.Input)
	}
	if evt.Tokens.Output != 500 {
		t.Fatalf("tokens.Output = %d, want 500", evt.Tokens.Output)
	}
}

// TestGeminiStats_CachedField tests that the cached field maps to CacheRead in TokenUsage.
func TestGeminiStats_CachedField(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"s3","status":"success","stats":{"total_tokens":1000,"input_tokens":800,"output_tokens":200,"cached":350,"input":800,"duration_ms":2000}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.CacheRead != 350 {
		t.Fatalf("tokens.CacheRead = %d, want 350", evt.Tokens.CacheRead)
	}
}

// TestGeminiStats_NoModelsField tests backward compatibility when the stats
// object has no "models" field at all.
func TestGeminiStats_NoModelsField(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"s4","status":"success","stats":{"total_tokens":52000,"input_tokens":38000,"output_tokens":14000,"duration_ms":62000,"tool_calls":8,"turns":6}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 38000 || evt.Tokens.Output != 14000 {
		t.Fatalf("tokens = %+v, want Input=38000 Output=14000", evt.Tokens)
	}
	if evt.ActualModel != "" {
		t.Fatalf("actual_model = %q, want empty (no models field)", evt.ActualModel)
	}
}

// TestGeminiStats_EmptyModelsMap tests backward compatibility when the models
// map is present but empty.
func TestGeminiStats_EmptyModelsMap(t *testing.T) {
	a := &GeminiAdapter{}

	line := `{"type":"result","session_id":"s5","status":"success","stats":{"total_tokens":1000,"input_tokens":700,"output_tokens":300,"cached":0,"input":700,"duration_ms":1500,"models":{}}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 700 || evt.Tokens.Output != 300 {
		t.Fatalf("tokens = %+v, want Input=700 Output=300", evt.Tokens)
	}
	if evt.ActualModel != "" {
		t.Fatalf("actual_model = %q, want empty (empty models map)", evt.ActualModel)
	}
}
