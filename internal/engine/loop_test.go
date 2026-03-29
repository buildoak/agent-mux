package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/inbox"
	"github.com/buildoak/agent-mux/internal/store"
	"github.com/buildoak/agent-mux/internal/types"
)

type resumeCall struct {
	sessionID string
	message   string
	model     string
}

type scriptedAdapter struct {
	mu              sync.Mutex
	baseBinary      string
	resumeBinary    string
	supportsResume  bool
	stdinNudgeBytes []byte
	initialScript   string
	initialPrompt   string
	env             []string
	envErr          error
	resumeScript    func(sessionID, message string) string
	resumeCalls     []resumeCall
	failResumeStart bool
}

func newScriptedAdapter(initialScript string) *scriptedAdapter {
	return &scriptedAdapter{
		baseBinary:     "bash",
		supportsResume: true,
		initialScript:  initialScript,
		resumeScript: func(sessionID, message string) string {
			return "exit 0"
		},
	}
}

func (a *scriptedAdapter) Binary() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.resumeBinary != "" {
		return a.resumeBinary
	}
	return a.baseBinary
}

func (a *scriptedAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	a.mu.Lock()
	if spec != nil {
		a.initialPrompt = spec.Prompt
	}
	a.mu.Unlock()
	return []string{"-c", a.initialScript}
}

func (a *scriptedAdapter) EnvVars(spec *types.DispatchSpec) ([]string, error) {
	if a.envErr != nil {
		return nil, a.envErr
	}
	if len(a.env) == 0 {
		return nil, nil
	}
	return append([]string(nil), a.env...), nil
}

func (a *scriptedAdapter) StdinNudge() []byte {
	return a.stdinNudgeBytes
}

func (a *scriptedAdapter) SupportsResume() bool {
	return a.supportsResume
}

func (a *scriptedAdapter) ResumeArgs(spec *types.DispatchSpec, sessionID, message string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	model := ""
	if spec != nil {
		model = spec.Model
	}
	a.resumeCalls = append(a.resumeCalls, resumeCall{sessionID: sessionID, message: message, model: model})
	if a.failResumeStart {
		a.resumeBinary = "nonexistent-binary-for-resume"
	}
	return []string{"-c", a.resumeScript(sessionID, message)}
}

func (a *scriptedAdapter) Calls() []resumeCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]resumeCall, len(a.resumeCalls))
	copy(out, a.resumeCalls)
	return out
}

func (a *scriptedAdapter) InitialPrompt() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.initialPrompt
}

func (a *scriptedAdapter) ParseEvent(line string) (*types.HarnessEvent, error) {
	if line == "" {
		return nil, nil
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return nil, nil
	}
	value := parts[1]
	switch parts[0] {
	case "SESSION":
		return &types.HarnessEvent{Kind: types.EventSessionStart, SessionID: value}, nil
	case "FILE_READ":
		return &types.HarnessEvent{Kind: types.EventFileRead, FilePath: value}, nil
	case "FILE_WRITE":
		return &types.HarnessEvent{Kind: types.EventFileWrite, FilePath: value}, nil
	case "COMMAND":
		return &types.HarnessEvent{Kind: types.EventCommandRun, Tool: "shell", Command: value}, nil
	case "TOOL_START":
		return &types.HarnessEvent{Kind: types.EventToolStart, Tool: "command_execution", Command: value}, nil
	case "TOOL_END":
		return &types.HarnessEvent{Kind: types.EventToolEnd, Tool: value}, nil
	case "PROGRESS":
		return &types.HarnessEvent{Kind: types.EventProgress, Text: value}, nil
	case "RESPONSE":
		return &types.HarnessEvent{Kind: types.EventResponse, Text: value}, nil
	case "TURN":
		tokenParts := strings.Split(value, ",")
		if len(tokenParts) != 3 {
			return nil, fmt.Errorf("invalid TURN payload %q", value)
		}
		input, err := strconv.Atoi(tokenParts[0])
		if err != nil {
			return nil, err
		}
		output, err := strconv.Atoi(tokenParts[1])
		if err != nil {
			return nil, err
		}
		reasoning, err := strconv.Atoi(tokenParts[2])
		if err != nil {
			return nil, err
		}
		return &types.HarnessEvent{
			Kind:   types.EventTurnComplete,
			Tokens: &types.TokenUsage{Input: input, Output: output, Reasoning: reasoning},
		}, nil
	case "ERROR":
		errParts := strings.SplitN(value, ":", 2)
		if len(errParts) != 2 {
			return nil, fmt.Errorf("invalid ERROR payload %q", value)
		}
		return &types.HarnessEvent{Kind: types.EventError, ErrorCode: errParts[0], Text: errParts[1]}, nil
	default:
		return nil, nil
	}
}

func TestLoopEngineInboxResumeHappyPath(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-1'",
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.resumeScript = func(sessionID, message string) string {
		return strings.Join([]string{
			fmt.Sprintf("echo %q", "PROGRESS:resuming"),
			fmt.Sprintf("echo %q", "RESPONSE:resumed response"),
			fmt.Sprintf("echo %q", "TURN:3,2,1"),
		}, "\n")
	}

	result := runDispatchWithInboxMessage(t, adapter, artifactDir, readyPath, "inject now")

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed, error = %+v", result.Status, result.Error)
	}
	if result.Response != "resumed response" {
		t.Fatalf("response = %q, want resumed response", result.Response)
	}
	calls := adapter.Calls()
	if len(calls) != 1 {
		t.Fatalf("resume calls = %d, want 1", len(calls))
	}
	if calls[0].sessionID != "session-1" || calls[0].message != "inject now" {
		t.Fatalf("resume call = %+v, want session-1/inject now", calls[0])
	}
	if calls[0].model != "gpt-5.4" {
		t.Fatalf("resume model = %q, want gpt-5.4", calls[0].model)
	}
	eventsPath := filepath.Join(artifactDir, "events.jsonl")
	eventsData, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events log: %v", err)
	}
	if !strings.Contains(string(eventsData), "\"type\":\"coordinator_inject\"") {
		t.Fatalf("events log missing coordinator_inject: %s", string(eventsData))
	}
}

func TestLoopEngineResumeUnsupportedFailsDispatch(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-unsupported'",
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.supportsResume = false

	result := runDispatchWithInboxMessage(t, adapter, artifactDir, readyPath, "cannot resume")

	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "resume_unsupported" {
		t.Fatalf("error = %+v, want resume_unsupported", result.Error)
	}
}

func TestLoopEnginePersistsCompletedDispatchToStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	artifactDir := t.TempDir()
	response := strings.Repeat("x", 64)
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("echo %q", "RESPONSE:"+response),
		"echo 'TURN:1,1,0'",
	}, "\n"))

	spec := testDispatchSpec(artifactDir)
	spec.ResponseMaxChars = 0

	engine := NewLoopEngine(adapter, io.Discard, nil)
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}

	record, err := store.FindRecord("", spec.DispatchID)
	if err != nil {
		t.Fatalf("FindRecord: %v", err)
	}
	if record == nil {
		t.Fatal("FindRecord = nil, want record")
	}
	if record.Status != "completed" {
		t.Fatalf("status = %q, want completed", record.Status)
	}
	if record.Truncated {
		t.Fatal("truncated = true, want false (truncation removed)")
	}
	if record.ResponseChars != len(response) {
		t.Fatalf("response_chars = %d, want %d", record.ResponseChars, len(response))
	}
	if record.StartedAt == "" || record.EndedAt == "" {
		t.Fatalf("timestamps = %#v, want started_at and ended_at", record)
	}

	storedResponse, err := store.ReadResult("", spec.DispatchID)
	if err != nil {
		t.Fatalf("ReadResult: %v", err)
	}
	if storedResponse != response {
		t.Fatalf("stored response = %q, want %q", storedResponse, response)
	}
}

func TestLoopEnginePersistsFailedDispatchToStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	adapter := newScriptedAdapter("exit 0")
	adapter.baseBinary = "nonexistent-binary-that-does-not-exist"
	engine := NewLoopEngine(adapter, io.Discard, nil)

	spec := testDispatchSpec(t.TempDir())
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}

	record, err := store.FindRecord("", spec.DispatchID)
	if err != nil {
		t.Fatalf("FindRecord: %v", err)
	}
	if record == nil {
		t.Fatal("FindRecord = nil, want record")
	}
	if record.Status != "failed" {
		t.Fatalf("status = %q, want failed", record.Status)
	}

	storedResponse, err := store.ReadResult("", spec.DispatchID)
	if err != nil {
		t.Fatalf("ReadResult: %v", err)
	}
	if storedResponse != "" {
		t.Fatalf("stored response = %q, want empty string", storedResponse)
	}
}

func TestLoopEngineFailsParseErrorWithoutFinalResponse(t *testing.T) {
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter("echo 'TURN:broken'")
	engine := NewLoopEngine(adapter, io.Discard, nil)

	result, err := engine.Dispatch(context.Background(), testDispatchSpec(artifactDir))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "parse_error" {
		t.Fatalf("error = %+v, want parse_error", result.Error)
	}
	if !strings.Contains(result.Error.Message, "no final response could be trusted") {
		t.Fatalf("error.message = %q, want parse error summary", result.Error.Message)
	}
}

func TestLoopEngineParseErrorExitZeroNoResponseIsFailed(t *testing.T) {
	// Engine exits 0 but produces only malformed NDJSON — no valid response.
	// This should be marked failed with parse_error code.
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'TURN:not,a,number'",
		"echo 'ERROR:badformat'",
		"exit 0",
	}, "\n"))
	engine := NewLoopEngine(adapter, io.Discard, nil)

	result, err := engine.Dispatch(context.Background(), testDispatchSpec(artifactDir))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "parse_error" {
		t.Fatalf("error = %+v, want parse_error", result.Error)
	}
	if !strings.Contains(result.Error.Message, "parse error") {
		t.Fatalf("error.message = %q, want parse error explanation", result.Error.Message)
	}
}

func TestLoopEngineKeepsCompletedStatusWhenParseErrorHasFinalResponse(t *testing.T) {
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'TURN:broken'",
		"echo 'RESPONSE:real answer'",
	}, "\n"))
	engine := NewLoopEngine(adapter, io.Discard, nil)

	result, err := engine.Dispatch(context.Background(), testDispatchSpec(artifactDir))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed, error = %+v", result.Status, result.Error)
	}
	if result.Response != "real answer" {
		t.Fatalf("response = %q, want real answer", result.Response)
	}
}

func TestLoopEngineFailsWhenAdapterEnvSetupFails(t *testing.T) {
	artifactDir := t.TempDir()
	markerPath := filepath.Join(artifactDir, "started")
	adapter := newScriptedAdapter(fmt.Sprintf("touch %q", markerPath))
	adapter.envErr = fmt.Errorf("write Gemini system prompt %q: permission denied", filepath.Join(artifactDir, "system_prompt.md"))
	engine := NewLoopEngine(adapter, io.Discard, nil)

	result, err := engine.Dispatch(context.Background(), testDispatchSpec(artifactDir))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "artifact_dir_unwritable" {
		t.Fatalf("error = %+v, want artifact_dir_unwritable", result.Error)
	}
	if !strings.Contains(result.Error.Message, "write Gemini system prompt") {
		t.Fatalf("error.message = %q, want surfaced env setup error", result.Error.Message)
	}
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatalf("marker file exists or unexpected stat error = %v, harness should not have started", statErr)
	}
}

func TestLoopEngineGeminiPromptWriteFailure(t *testing.T) {
	artifactDir := t.TempDir()
	markerPath := filepath.Join(artifactDir, "started")
	adapter := newScriptedAdapter(fmt.Sprintf("touch %q", markerPath))
	adapter.envErr = fmt.Errorf("write system prompt to %q: permission denied", filepath.Join(artifactDir, "system_prompt.md"))
	engine := NewLoopEngine(adapter, io.Discard, nil)

	result, err := engine.Dispatch(context.Background(), testDispatchSpec(artifactDir))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "artifact_dir_unwritable" {
		t.Fatalf("error = %+v, want artifact_dir_unwritable", result.Error)
	}
	if !strings.Contains(result.Error.Message, "write system prompt") {
		t.Fatalf("error.message = %q, want system prompt write error", result.Error.Message)
	}
	// Verify harness was never started.
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatalf("marker file exists, harness should not have started")
	}
}

func TestLoopEngineResumePreservesActivityAndTokens(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-activity'",
		"echo 'FILE_READ:first.txt'",
		"echo 'COMMAND:git status'",
		"echo 'TURN:2,1,0'",
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.resumeScript = func(sessionID, message string) string {
		return strings.Join([]string{
			"echo 'FILE_WRITE:second.txt'",
			"echo 'RESPONSE:done after resume'",
			"echo 'TURN:5,3,1'",
		}, "\n")
	}

	result := runDispatchWithInboxMessage(t, adapter, artifactDir, readyPath, "resume and finish")

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed, error = %+v", result.Status, result.Error)
	}
	if result.Activity == nil {
		t.Fatal("activity is nil")
	}
	if !containsString(result.Activity.FilesRead, "first.txt") {
		t.Fatalf("files_read = %#v, want first.txt", result.Activity.FilesRead)
	}
	if !containsString(result.Activity.FilesChanged, "second.txt") {
		t.Fatalf("files_changed = %#v, want second.txt", result.Activity.FilesChanged)
	}
	if !containsString(result.Activity.CommandsRun, "git status") {
		t.Fatalf("commands_run = %#v, want git status", result.Activity.CommandsRun)
	}
	if result.Metadata == nil || result.Metadata.Tokens == nil {
		t.Fatalf("metadata tokens missing: %+v", result.Metadata)
	}
	if result.Metadata.Tokens.Input != 5 || result.Metadata.Tokens.Output != 3 || result.Metadata.Tokens.Reasoning != 1 {
		t.Fatalf("tokens = %+v, want cumulative resumed tokens", result.Metadata.Tokens)
	}
	if result.Metadata.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Metadata.Turns)
	}
	if result.Metadata.SessionID != "session-activity" {
		t.Fatalf("session_id = %q, want session-activity", result.Metadata.SessionID)
	}
}

func TestLoopEngineNaturalExitWhileRestartPendingStillResumes(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-natural-exit'",
		"while [ ! -s \"$AGENT_MUX_ARTIFACT_DIR/inbox.md\" ]; do sleep 0.01; done",
		"echo 'PROGRESS:about to exit'",
		"exit 0",
	}, "\n"))
	adapter.resumeScript = func(sessionID, message string) string {
		return strings.Join([]string{
			"echo 'RESPONSE:resumed after exit'",
			"echo 'TURN:4,3,2'",
		}, "\n")
	}

	result := runDispatchWithInboxMessage(t, adapter, artifactDir, readyPath, "restart after exit")

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed, error = %+v", result.Status, result.Error)
	}
	if result.Response != "resumed after exit" {
		t.Fatalf("response = %q, want resumed after exit", result.Response)
	}
	calls := adapter.Calls()
	if len(calls) != 1 {
		t.Fatalf("resume calls = %d, want 1", len(calls))
	}
	if calls[0].sessionID != "session-natural-exit" || calls[0].message != "restart after exit" {
		t.Fatalf("resume call = %+v, want session-natural-exit/restart after exit", calls[0])
	}
}

func TestLoopEngineResumePassesOriginalModel(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-model'",
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.resumeScript = func(sessionID, message string) string {
		return strings.Join([]string{
			"echo 'RESPONSE:model preserved'",
			"echo 'TURN:1,1,0'",
		}, "\n")
	}

	spec := testDispatchSpec(artifactDir)
	spec.Model = "gpt-5.4-mini"
	result := runDispatchWithInboxMessageAndSpec(t, adapter, spec, readyPath, "resume on same model")

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed, error = %+v", result.Status, result.Error)
	}
	calls := adapter.Calls()
	if len(calls) != 1 {
		t.Fatalf("resume calls = %d, want 1", len(calls))
	}
	if calls[0].model != "gpt-5.4-mini" {
		t.Fatalf("resume model = %q, want gpt-5.4-mini", calls[0].model)
	}
}

func TestLoopEngineInjectsTracePreambleIntoPrompt(t *testing.T) {
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'RESPONSE:ok'",
		"echo 'TURN:1,1,0'",
	}, "\n"))

	spec := testDispatchSpec(artifactDir)
	spec.DispatchID = "01TRACE"
	spec.Salt = "coral-fox-nine"
	spec.TraceToken = "AGENT_MUX_GO_01TRACE"
	spec.Prompt = "build the parser"

	engine := NewLoopEngine(adapter, io.Discard, nil)
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}

	wantPrefix := strings.Join([]string{
		"Trace token: AGENT_MUX_GO_01TRACE",
		"Dispatch ID: 01TRACE",
		"Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR.",
		"",
		"build the parser",
	}, "\n")
	if got := adapter.InitialPrompt(); got != wantPrefix {
		t.Fatalf("initial prompt = %q, want %q", got, wantPrefix)
	}
}

func TestLoopEngineExportsTraceEnvVars(t *testing.T) {
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		`echo "RESPONSE:$AGENT_MUX_TRACE_TOKEN|$AGENT_MUX_SALT|$AGENT_MUX_DISPATCH_ID"`,
		"echo 'TURN:1,1,0'",
	}, "\n"))

	spec := testDispatchSpec(artifactDir)
	spec.DispatchID = "01TRACEENV"
	spec.Salt = "coral-fox-nine"
	spec.TraceToken = "AGENT_MUX_GO_01TRACEENV"

	engine := NewLoopEngine(adapter, io.Discard, nil)
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	want := "AGENT_MUX_GO_01TRACEENV|coral-fox-nine|01TRACEENV"
	if result.Response != want {
		t.Fatalf("response = %q, want %q", result.Response, want)
	}
}

func TestLoopEngineDiscardStaleSignalsFromOldRun(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-stale'",
		"trap 'echo \"FILE_WRITE:stale.txt\"; echo \"TURN:9,9,9\"; exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.resumeScript = func(sessionID, message string) string {
		return strings.Join([]string{
			"echo 'FILE_WRITE:fresh.txt'",
			"echo 'RESPONSE:clean resume'",
			"echo 'TURN:5,4,3'",
		}, "\n")
	}

	result := runDispatchWithInboxMessage(t, adapter, artifactDir, readyPath, "discard stale")

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed, error = %+v", result.Status, result.Error)
	}
	if result.Response != "clean resume" {
		t.Fatalf("response = %q, want clean resume", result.Response)
	}
	if result.Activity == nil {
		t.Fatal("activity is nil")
	}
	if containsString(result.Activity.FilesChanged, "stale.txt") {
		t.Fatalf("files_changed = %#v, stale.txt should have been discarded", result.Activity.FilesChanged)
	}
	if !containsString(result.Activity.FilesChanged, "fresh.txt") {
		t.Fatalf("files_changed = %#v, want fresh.txt", result.Activity.FilesChanged)
	}
	if result.Metadata == nil || result.Metadata.Tokens == nil {
		t.Fatalf("metadata tokens missing: %+v", result.Metadata)
	}
	if result.Metadata.Tokens.Input != 5 || result.Metadata.Tokens.Output != 4 || result.Metadata.Tokens.Reasoning != 3 {
		t.Fatalf("tokens = %+v, want resumed tokens only", result.Metadata.Tokens)
	}
	if result.Metadata.Turns != 1 {
		t.Fatalf("turns = %d, want 1", result.Metadata.Turns)
	}
}

func TestScanHarnessOutputDeliversInboxSignalWhenChannelWasFull(t *testing.T) {
	artifactDir := t.TempDir()
	if err := inbox.CreateInbox(artifactDir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	adapter := newScriptedAdapter("exit 0")
	engine := NewLoopEngine(adapter, io.Discard, nil)
	signals := make(chan loopSignal, 1)
	reader, writer := io.Pipe()
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.scanHarnessOutput(reader, 7, artifactDir, signals)
	}()

	if _, err := io.WriteString(writer, "SESSION:session-burst\n"); err != nil {
		t.Fatalf("write session line: %v", err)
	}

	select {
	case sig := <-signals:
		if sig.kind != loopSignalEvent || sig.event == nil || sig.event.Kind != types.EventSessionStart || sig.event.SessionID != "session-burst" {
			t.Fatalf("first signal = %+v, want session-start event", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session signal")
	}

	if err := inbox.WriteInbox(artifactDir, "resume during burst"); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	signals <- loopSignal{kind: loopSignalEvent, runGen: 7}
	if _, err := io.WriteString(writer, "noop\n"); err != nil {
		t.Fatalf("write scanner line: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	select {
	case sig := <-signals:
		if sig.kind != loopSignalEvent || sig.runGen != 7 {
			t.Fatalf("unexpected buffered signal before inbox delivery: %+v", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting to drain blocking signal")
	}

	select {
	case sig := <-signals:
		if sig.kind != loopSignalInbox || sig.runGen != 7 || sig.message != "resume during burst" {
			t.Fatalf("inbox signal = %+v, want loopSignalInbox/resume during burst", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbox signal")
	}

	if inbox.HasMessages(artifactDir) {
		t.Fatal("inbox still has messages after delivery")
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not exit")
	}
}

func TestIsIgnorableStreamScanErr(t *testing.T) {
	t.Parallel()

	if !isIgnorableStreamScanErr(os.ErrClosed) {
		t.Fatal("os.ErrClosed should be ignored")
	}
	if !isIgnorableStreamScanErr(fmt.Errorf("read |0: %w", os.ErrClosed)) {
		t.Fatal("wrapped os.ErrClosed should be ignored")
	}
	if isIgnorableStreamScanErr(io.EOF) {
		t.Fatal("io.EOF should not be treated as an ignorable scanner error")
	}
}

func TestLoopEngineResumeStartFailureFailsCleanly(t *testing.T) {
	artifactDir := t.TempDir()
	readyPath := filepath.Join(artifactDir, "ready")
	adapter := newScriptedAdapter(strings.Join([]string{
		fmt.Sprintf("touch %q", readyPath),
		"echo 'SESSION:session-start-fail'",
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.failResumeStart = true

	result := runDispatchWithInboxMessage(t, adapter, artifactDir, readyPath, "resume should fail")

	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "resume_start_failed" {
		t.Fatalf("error = %+v, want resume_start_failed", result.Error)
	}
}

func TestLoopEngineBinaryNotFound(t *testing.T) {
	adapter := newScriptedAdapter("exit 0")
	adapter.baseBinary = "nonexistent-binary-that-does-not-exist"
	engine := NewLoopEngine(adapter, io.Discard, nil)

	spec := testDispatchSpec(t.TempDir())
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "binary_not_found" {
		t.Fatalf("error = %+v, want binary_not_found", result.Error)
	}
}

func runDispatchWithInboxMessage(t *testing.T, adapter *scriptedAdapter, artifactDir, readyPath, message string) *types.DispatchResult {
	t.Helper()
	return runDispatchWithInboxMessageAndSpec(t, adapter, testDispatchSpec(artifactDir), readyPath, message)
}

func runDispatchWithInboxMessageAndSpec(t *testing.T, adapter *scriptedAdapter, spec *types.DispatchSpec, readyPath, message string) *types.DispatchResult {
	t.Helper()
	engine := NewLoopEngine(adapter, io.Discard, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type dispatchOutcome struct {
		result *types.DispatchResult
		err    error
	}
	outcomeCh := make(chan dispatchOutcome, 1)
	go func() {
		result, err := engine.Dispatch(ctx, spec)
		outcomeCh <- dispatchOutcome{result: result, err: err}
	}()

	artifactDir := spec.ArtifactDir
	waitForPath(t, readyPath)
	if err := inbox.WriteInbox(artifactDir, message); err != nil {
		t.Fatalf("write inbox: %v", err)
	}

	select {
	case outcome := <-outcomeCh:
		if outcome.err != nil {
			t.Fatalf("Dispatch: %v", outcome.err)
		}
		return outcome.result
	case <-ctx.Done():
		t.Fatal("dispatch timed out")
		return nil
	}
}

func testDispatchSpec(artifactDir string) *types.DispatchSpec {
	return &types.DispatchSpec{
		DispatchID:  "01TEST",
		Salt:        "test-salt",
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "ignored by scripted adapter",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    1,
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("path did not appear: %s", path)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestLongCommandExtendsSilenceThreshold(t *testing.T) {
	// A TOOL_START with "cargo build" should extend the silence kill threshold.
	// With silence_kill=2 and long_command_silence=20, the watchdog must NOT
	// kill at 2s while a cargo build is active. Sleep must exceed 5s (watchdog
	// ticker interval) so the watchdog fires during the silent period.
	artifactDir := t.TempDir()

	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:lc-extend'",
		"echo 'TOOL_START:cargo build --release'",
		"sleep 7",
		"echo 'TOOL_END:command_execution'",
		"echo 'RESPONSE:build completed'",
		"echo 'TURN:1,1,0'",
	}, "\n"))
	adapter.supportsResume = false

	spec := &types.DispatchSpec{
		DispatchID:  "01LONGCMD",
		Salt:        "test-salt",
		Engine:      "codex",
		Model:       "test",
		Prompt:      "ignored",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    1,
		TimeoutSec:  30,
		EngineOpts: map[string]any{
			"silence_warn_seconds":          1,
			"silence_kill_seconds":          2,
			"long_command_silence_seconds":  20,
		},
	}

	var eventBuf strings.Builder
	engine := NewLoopEngine(adapter, &eventBuf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed; error = %+v", result.Status, result.Error)
	}
	if result.Response != "build completed" {
		t.Fatalf("response = %q, want 'build completed'", result.Response)
	}
	events := eventBuf.String()
	if !strings.Contains(events, "long_command_detected") {
		t.Fatalf("event stream missing long_command_detected; got:\n%s", events)
	}
}

func TestUnknownCommandKilledAtNormalThreshold(t *testing.T) {
	// A TOOL_START with "curl" (not a known long-running prefix) should NOT
	// extend the threshold. With silence_kill=2, the process should be killed.
	artifactDir := t.TempDir()

	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:lc-unknown'",
		"echo 'TOOL_START:curl http://example.com'",
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.supportsResume = false

	spec := &types.DispatchSpec{
		DispatchID:  "01SHORTCMD",
		Salt:        "test-salt",
		Engine:      "codex",
		Model:       "test",
		Prompt:      "ignored",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    1,
		TimeoutSec:  30,
		EngineOpts: map[string]any{
			"silence_warn_seconds":          1,
			"silence_kill_seconds":          3,
			"long_command_silence_seconds":  30,
		},
	}

	var eventBuf strings.Builder
	engine := NewLoopEngine(adapter, &eventBuf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed (frozen kill)", result.Status)
	}
	if result.Error == nil || result.Error.Code != "frozen_killed" {
		t.Fatalf("error = %+v, want frozen_killed", result.Error)
	}
	events := eventBuf.String()
	if strings.Contains(events, "long_command_detected") {
		t.Fatalf("long_command_detected should NOT appear for curl command")
	}
}

func TestLongCommandEndResumesNormalThreshold(t *testing.T) {
	// After TOOL_END, the normal silence threshold should resume. A period
	// of silence exceeding normal kill should terminate the process even
	// though a long command was previously active.
	artifactDir := t.TempDir()

	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:lc-resume'",
		"echo 'TOOL_START:go build ./...'",
		"sleep 0.5",
		"echo 'TOOL_END:command_execution'",
		// Now go silent without an active long command — should be killed at normal threshold
		"trap 'exit 0' TERM",
		"while :; do sleep 0.1; done",
	}, "\n"))
	adapter.supportsResume = false

	spec := &types.DispatchSpec{
		DispatchID:  "01LONGEND",
		Salt:        "test-salt",
		Engine:      "codex",
		Model:       "test",
		Prompt:      "ignored",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    1,
		TimeoutSec:  30,
		EngineOpts: map[string]any{
			"silence_warn_seconds":          1,
			"silence_kill_seconds":          3,
			"long_command_silence_seconds":  30,
		},
	}

	engine := NewLoopEngine(adapter, io.Discard, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed (frozen kill after tool_end)", result.Status)
	}
	if result.Error == nil || result.Error.Code != "frozen_killed" {
		t.Fatalf("error = %+v, want frozen_killed", result.Error)
	}
}

func TestStdinNudgeOnFrozenWarning(t *testing.T) {
	artifactDir := t.TempDir()

	// Script: emit one event then go silent. The process reads stdin and if
	// it receives a nudge, emits a RESPONSE and exits. This proves the nudge
	// was delivered via the stdin pipe.
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:nudge-test'",
		"read -t 10 line",          // block until stdin nudge or 10s
		"echo 'RESPONSE:nudge-ok'", // emit response after nudge
		"echo 'TURN:1,1,0'",
	}, "\n"))
	adapter.supportsResume = false
	adapter.stdinNudgeBytes = []byte("\n")

	spec := &types.DispatchSpec{
		DispatchID:  "01NUDGE",
		Salt:        "test-salt",
		Engine:      "codex",
		Model:       "test",
		Prompt:      "ignored",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    1,
		TimeoutSec:  30,
		EngineOpts: map[string]any{
			"silence_warn_seconds": 2,
			"silence_kill_seconds": 20,
		},
	}

	var eventBuf strings.Builder
	engine := NewLoopEngine(adapter, &eventBuf, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed; error = %+v", result.Status, result.Error)
	}
	if result.Response != "nudge-ok" {
		t.Fatalf("response = %q, want nudge-ok", result.Response)
	}

	events := eventBuf.String()
	if !strings.Contains(events, "stdin_nudge") {
		t.Fatalf("event stream missing stdin_nudge event; got:\n%s", events)
	}
	if !strings.Contains(events, "frozen_warning") {
		t.Fatalf("event stream missing frozen_warning event; got:\n%s", events)
	}
}
