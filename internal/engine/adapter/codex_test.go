package adapter

import (
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestCodexBuildArgs(t *testing.T) {
	a := &CodexAdapter{}

	spec := &types.DispatchSpec{
		Model:  "gpt-5.4",
		Prompt: "Build the parser",
		Cwd:    "/path/to/project",
		EngineOpts: map[string]any{
			"sandbox":   "workspace-write",
			"reasoning": "high",
		},
	}

	args := a.BuildArgs(spec)

	assertContains(t, args, "exec")
	assertContains(t, args, "--json")
	assertContains(t, args, "-m")
	assertContains(t, args, "gpt-5.4")
	assertContains(t, args, "-s")
	assertContains(t, args, "workspace-write")
	assertContains(t, args, "-C")
	assertContains(t, args, "/path/to/project")
	assertContains(t, args, "-c")
	assertContains(t, args, "model_reasoning_effort=high")
	assertContains(t, args, "Build the parser")
}

func TestCodexBuildArgsDefaults(t *testing.T) {
	a := &CodexAdapter{}

	spec := &types.DispatchSpec{
		Prompt:     "test prompt",
		FullAccess: true,
		EngineOpts: map[string]any{},
	}

	args := a.BuildArgs(spec)

	// Default full access -> dangerously bypass
	assertContains(t, args, "--dangerously-bypass-approvals-and-sandbox")
}

func TestCodexBuildArgsWithSystemPrompt(t *testing.T) {
	a := &CodexAdapter{}

	spec := &types.DispatchSpec{
		Prompt:       "Build it",
		SystemPrompt: "You are a Go expert.",
		EngineOpts:   map[string]any{},
	}

	args := a.BuildArgs(spec)

	// Last arg should be the combined prompt
	lastArg := args[len(args)-1]
	if lastArg != "You are a Go expert.\n\nBuild it" {
		t.Errorf("prompt = %q, want system + user combined", lastArg)
	}
}

func TestCodexParseThreadStarted(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"thread.started","thread_id":"thread_abc123","model":"gpt-5.4"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventSessionStart {
		t.Errorf("kind = %d, want EventSessionStart", evt.Kind)
	}
	if evt.SessionID != "thread_abc123" {
		t.Errorf("session_id = %q, want thread_abc123", evt.SessionID)
	}
}

func TestCodexParseTurnStarted(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"turn.started","turn_index":0}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	// turn.started has no direct mapping
	if evt != nil {
		t.Errorf("turn.started should return nil event, got kind=%d", evt.Kind)
	}
}

func TestCodexParseItemStartedCommand(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"item.started","item_id":"item_001","item_type":"command_execution","command":"go test ./..."}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventCommandRun {
		t.Errorf("kind = %d, want EventCommandRun", evt.Kind)
	}
	if evt.Command != "go test ./..." {
		t.Errorf("command = %q, want 'go test ./...'", evt.Command)
	}
}

func TestCodexBuildArgsAcceptsJSONDecodedAddDir(t *testing.T) {
	a := &CodexAdapter{}

	spec := &types.DispatchSpec{
		Prompt: "test prompt",
		EngineOpts: map[string]any{
			"add-dir": []any{"/tmp/a", "/tmp/b"},
		},
	}

	args := a.BuildArgs(spec)
	assertContains(t, args, "--add-dir")
	assertContains(t, args, "/tmp/a")
	assertContains(t, args, "/tmp/b")
}

func TestCodexParseItemUpdatedMessage(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"item.updated","item_id":"item_002","item_type":"agent_message","content_delta":"I'll run the tests"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventProgress {
		t.Errorf("kind = %d, want EventProgress", evt.Kind)
	}
	if evt.Text != "I'll run the tests" {
		t.Errorf("text = %q", evt.Text)
	}
}

func TestCodexParseItemCompletedMessage(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"item.completed","item_id":"item_002","item_type":"agent_message","content":"Done building the parser.","duration_ms":2300}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventResponse {
		t.Errorf("kind = %d, want EventResponse", evt.Kind)
	}
	if evt.Text != "Done building the parser." {
		t.Errorf("text = %q", evt.Text)
	}
}

func TestCodexParseItemCompletedCommand(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"item.completed","item_id":"item_001","item_type":"command_execution","command":"go test ./...","exit_code":0,"duration_ms":1200}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventToolEnd {
		t.Errorf("kind = %d, want EventToolEnd", evt.Kind)
	}
	if evt.DurationMS != 1200 {
		t.Errorf("duration_ms = %d, want 1200", evt.DurationMS)
	}
}

func TestCodexParseItemCompletedFileChange(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"item.completed","item_id":"item_003","item_type":"file_change","file_path":"internal/parser/parser.go","change_type":"modified","duration_ms":150}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventFileWrite {
		t.Errorf("kind = %d, want EventFileWrite", evt.Kind)
	}
	if evt.FilePath != "internal/parser/parser.go" {
		t.Errorf("file_path = %q", evt.FilePath)
	}
}

func TestCodexParseTurnCompleted(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"turn.completed","turn_index":0,"usage":{"input_tokens":23000,"output_tokens":4500,"reasoning_tokens":1200},"duration_ms":34000}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventTurnComplete {
		t.Errorf("kind = %d, want EventTurnComplete", evt.Kind)
	}
	if evt.Tokens == nil {
		t.Fatal("tokens should not be nil")
	}
	if evt.Tokens.Input != 23000 {
		t.Errorf("tokens.input = %d, want 23000", evt.Tokens.Input)
	}
	if evt.Tokens.Output != 4500 {
		t.Errorf("tokens.output = %d, want 4500", evt.Tokens.Output)
	}
	if evt.Tokens.Reasoning != 1200 {
		t.Errorf("tokens.reasoning = %d, want 1200", evt.Tokens.Reasoning)
	}
}

func TestCodexParseTurnFailed(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"turn.failed","turn_index":0,"error":{"code":"context_length_exceeded","message":"Conversation exceeded maximum context length."}}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventTurnFailed {
		t.Errorf("kind = %d, want EventTurnFailed", evt.Kind)
	}
	if evt.ErrorCode != "context_length_exceeded" {
		t.Errorf("error_code = %q", evt.ErrorCode)
	}
}

func TestCodexParseError(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"error","code":"model_not_found","message":"Model 'gpt-99' is not available."}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	if evt.Kind != types.EventError {
		t.Errorf("kind = %d, want EventError", evt.Kind)
	}
	if evt.ErrorCode != "model_not_found" {
		t.Errorf("error_code = %q, want model_not_found", evt.ErrorCode)
	}
}

func TestCodexParseMalformedJSON(t *testing.T) {
	a := &CodexAdapter{}

	line := `not json at all`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent should not error on malformed, got: %v", err)
	}
	if evt.Kind != types.EventRawPassthrough {
		t.Errorf("kind = %d, want EventRawPassthrough", evt.Kind)
	}
}

func TestCodexParseEmptyLine(t *testing.T) {
	a := &CodexAdapter{}

	evt, err := a.ParseEvent("")
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Error("empty line should return nil")
	}
}

func TestCodexParseUnknownType(t *testing.T) {
	a := &CodexAdapter{}

	line := `{"type":"future.new_event","data":"something"}`
	evt, err := a.ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt.Kind != types.EventRawPassthrough {
		t.Errorf("kind = %d, want EventRawPassthrough", evt.Kind)
	}
}

func TestCodexSupportsResume(t *testing.T) {
	a := &CodexAdapter{}
	if !a.SupportsResume() {
		t.Error("Codex should support resume")
	}
}

func TestCodexResumeArgs(t *testing.T) {
	a := &CodexAdapter{}
	args := a.ResumeArgs("thread_abc123", "wrap up")
	assertContains(t, args, "resume")
	assertContains(t, args, "--id")
	assertContains(t, args, "thread_abc123")
}

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("slice %v does not contain %q", slice, want)
}
