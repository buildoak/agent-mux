package adapter

import (
	"reflect"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestClaudeBuildArgs(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		Model:  "claude-opus-4-6",
		Prompt: "Build the parser",
	}

	args := a.BuildArgs(spec)
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--model", "claude-opus-4-6", "Build the parser"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestClaudeBuildArgsMaxTurns(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "Build the parser",
		EngineOpts: map[string]any{
			"max-turns": float64(12),
		},
	}

	args := a.BuildArgs(spec)
	assertContains(t, args, "--max-turns")
	assertContains(t, args, "12")
}

func TestClaudeBuildArgsPermissionMode(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "Build the parser",
		EngineOpts: map[string]any{
			"permission-mode": "plan",
		},
	}

	args := a.BuildArgs(spec)
	assertContains(t, args, "--permission-mode")
	assertContains(t, args, "plan")
}

func TestClaudeBuildArgsEmptyPermissionMode(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "Build the parser",
		EngineOpts: map[string]any{
			"permission-mode": "",
		},
	}

	args := a.BuildArgs(spec)
	assertNotContains(t, args, "--permission-mode")
}

func TestClaudeBuildArgsWithAddDirs(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "test prompt",
		EngineOpts: map[string]any{
			"add-dir": []any{"/tmp/scripts", "/tmp/helpers"},
		},
	}

	args := a.BuildArgs(spec)
	assertContains(t, args, "--add-dir")
	assertContains(t, args, "/tmp/scripts")
	assertContains(t, args, "/tmp/helpers")
}

func TestClaudeBuildArgsNoAddDirsWhenEmpty(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		Prompt:     "test prompt",
		EngineOpts: map[string]any{},
	}

	args := a.BuildArgs(spec)
	assertNotContains(t, args, "--add-dir")
}

func TestClaudeBuildArgsSystemPrompt(t *testing.T) {
	a := &ClaudeAdapter{}

	spec := &types.DispatchSpec{
		SystemPrompt: "You are a Go expert.",
		Prompt:       "Build the parser",
	}

	args := a.BuildArgs(spec)
	assertContains(t, args, "--system-prompt")
	assertContains(t, args, "You are a Go expert.")
	if got := args[len(args)-1]; got != "Build the parser" {
		t.Fatalf("last arg = %q, want prompt", got)
	}
}

func TestClaudeParseSystemInit(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"system","subtype":"init","session_id":"abc123-def456","model":"claude-opus-4-6"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventSessionStart {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventSessionStart)
	}
	if evt.SessionID != "abc123-def456" {
		t.Fatalf("session_id = %q, want abc123-def456", evt.SessionID)
	}
}

func TestClaudeParseAssistantText(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"I'll start by reading the main configuration file."}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventProgress {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventProgress)
	}
	if evt.Text != "I'll start by reading the main configuration file." {
		t.Fatalf("text = %q", evt.Text)
	}
}

func TestClaudeParseToolUseRead(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Read","input":{"file_path":"/path/to/project/src/main.go"}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventFileRead {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventFileRead)
	}
	if evt.SecondaryKind != types.EventToolStart {
		t.Fatalf("secondary kind = %d, want %d", evt.SecondaryKind, types.EventToolStart)
	}
	if evt.FilePath != "/path/to/project/src/main.go" {
		t.Fatalf("file_path = %q", evt.FilePath)
	}
}

func TestClaudeParseToolUseGlob(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Glob","input":{}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventFileRead {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventFileRead)
	}
}

func TestClaudeParseToolUseGrep(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Grep","input":{}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventFileRead {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventFileRead)
	}
}

func TestClaudeParseToolUseEdit(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Edit","input":{"file_path":"/path/to/project/src/main.go"}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventToolStart)
	}
	if evt.Tool != "Edit" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

func TestClaudeParseToolUseWrite(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Write","input":{"file_path":"/path/to/project/src/main.go"}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventToolStart)
	}
	if evt.Tool != "Write" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

func TestClaudeParseToolUseBash(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Bash","input":{"command":"go test ./..."}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventCommandRun {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventCommandRun)
	}
	if evt.Command != "go test ./..." {
		t.Fatalf("command = %q", evt.Command)
	}
}

func TestClaudeParseToolUseOther(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"WebSearch","input":{}}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolStart {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventToolStart)
	}
	if evt.Tool != "WebSearch" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

func TestClaudeParseToolResultEditSuccess(t *testing.T) {
	a := &ClaudeAdapter{}

	_, err := a.ParseEvent(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_01ABC","name":"Edit","input":{"file_path":"/path/to/project/src/main.go"}}]}}`)
	if err != nil {
		t.Fatalf("ParseEvent tool_use: %v", err)
	}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"tool_01ABC","content":"file written","is_error":false}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent tool_result: %v", err)
	}
	if evt.Kind != types.EventFileWrite {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventFileWrite)
	}
	if evt.FilePath != "/path/to/project/src/main.go" {
		t.Fatalf("file_path = %q", evt.FilePath)
	}
}

func TestClaudeParseToolResultReadOther(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"tool_01ABC","name":"Read","content":"package main...","is_error":false}]}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventToolEnd {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventToolEnd)
	}
	if evt.Tool != "Read" {
		t.Fatalf("tool = %q", evt.Tool)
	}
}

func TestClaudeParseResultSuccess(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"result","subtype":"success","result":"Built the parser. 3 files modified.","session_id":"abc123-def456","usage":{"input_tokens":45000,"output_tokens":8200,"cache_read_input_tokens":12000,"cache_creation_input_tokens":3000},"duration_ms":45200,"num_turns":12}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventResponse {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventResponse)
	}
	if evt.Text != "Built the parser. 3 files modified." {
		t.Fatalf("text = %q", evt.Text)
	}
	if evt.SessionID != "abc123-def456" {
		t.Fatalf("session_id = %q", evt.SessionID)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 45000 {
		t.Fatalf("tokens.input = %d", evt.Tokens.Input)
	}
	if evt.Tokens.Output != 8200 {
		t.Fatalf("tokens.output = %d", evt.Tokens.Output)
	}
	if evt.Tokens.CacheRead != 12000 || evt.Tokens.CacheWrite != 3000 {
		t.Fatalf("cache tokens = %+v", evt.Tokens)
	}
	if evt.Turns != 12 {
		t.Fatalf("turns = %d, want 12", evt.Turns)
	}
}

func TestClaudeParseResultError(t *testing.T) {
	a := &ClaudeAdapter{}

	line := `{"type":"result","subtype":"error","error":"Model not found: claude-opus-99","session_id":"abc123-def456"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventTurnFailed {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventTurnFailed)
	}
	if evt.ErrorCode != "result_error" {
		t.Fatalf("error_code = %q", evt.ErrorCode)
	}
	if evt.Text != "Model not found: claude-opus-99" {
		t.Fatalf("text = %q", evt.Text)
	}
}

func TestClaudeParseNonJSON(t *testing.T) {
	a := &ClaudeAdapter{}

	evt, err := a.ParseEvent("not json at all")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventRawPassthrough {
		t.Fatalf("kind = %d, want %d", evt.Kind, types.EventRawPassthrough)
	}
}

func TestClaudeParseEmptyLine(t *testing.T) {
	a := &ClaudeAdapter{}

	evt, err := a.ParseEvent("")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Fatalf("evt = %#v, want nil", evt)
	}
}

func TestClaudeSupportsResume(t *testing.T) {
	a := &ClaudeAdapter{}
	if !a.SupportsResume() {
		t.Fatal("SupportsResume() = false, want true")
	}
}

func TestClaudeResumeArgs(t *testing.T) {
	a := &ClaudeAdapter{}

	args := a.ResumeArgs(nil, "abc123-def456", "continue from there")
	want := []string{"--resume", "abc123-def456", "--continue", "continue from there"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
