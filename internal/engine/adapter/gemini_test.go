package adapter

import (
	"path/filepath"
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

func TestGeminiEnvVarsWritesSystemPromptFile(t *testing.T) {
	a := &GeminiAdapter{}
	spec := &types.DispatchSpec{
		SystemPrompt: "You are a Go expert.",
		ArtifactDir:  t.TempDir(),
	}

	env := a.EnvVars(spec)
	if len(env) != 1 {
		t.Fatalf("env = %#v, want single GEMINI_SYSTEM_MD", env)
	}
	want := "GEMINI_SYSTEM_MD=" + filepath.Join(spec.ArtifactDir, "system_prompt.md")
	if env[0] != want {
		t.Fatalf("env[0] = %q, want %q", env[0], want)
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

func TestGeminiParseToolUseReadFile(t *testing.T) {
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

func TestGeminiParseToolUseWriteFile(t *testing.T) {
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

func TestGeminiParseToolResultShell(t *testing.T) {
	a := &GeminiAdapter{}

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

func TestGeminiParseToolResultError(t *testing.T) {
	a := &GeminiAdapter{}

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

func TestGeminiParseResult(t *testing.T) {
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

func TestGeminiParseNonJSON(t *testing.T) {
	a := &GeminiAdapter{}

	evt, err := a.ParseEvent("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Fatalf("event = %#v, want nil", evt)
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

func TestGeminiSupportsResume(t *testing.T) {
	a := &GeminiAdapter{}
	if !a.SupportsResume() {
		t.Fatal("Gemini should support resume")
	}
}

func TestGeminiResumeArgs(t *testing.T) {
	a := &GeminiAdapter{}
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
