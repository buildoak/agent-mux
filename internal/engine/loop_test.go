package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/event"
	"github.com/buildoak/agent-mux/internal/steer"
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

	engine := NewLoopEngine(adapter, io.Discard, nil)
	engine.SetAnnotations(types.DispatchAnnotations{})
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}

	record, err := dispatch.FindDispatchRecordByRef(spec.DispatchID)
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

	storedResponse, err := dispatch.ReadResult(spec.DispatchID)
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

	record, err := dispatch.FindDispatchRecordByRef(spec.DispatchID)
	if err != nil {
		t.Fatalf("FindRecord: %v", err)
	}
	if record == nil {
		t.Fatal("FindRecord = nil, want record")
	}
	if record.Status != "failed" {
		t.Fatalf("status = %q, want failed", record.Status)
	}

	storedResponse, err := dispatch.ReadResult(spec.DispatchID)
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

func TestLoopEngineInjectsUsefulPreambleIntoPrompt(t *testing.T) {
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'RESPONSE:ok'",
		"echo 'TURN:1,1,0'",
	}, "\n"))

	spec := testDispatchSpec(artifactDir)
	spec.DispatchID = "01TRACE"
	spec.ContextFile = "/tmp/context.md"
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
		"Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting.",
		"If you need a temporary directory for intermediate files, use $AGENT_MUX_ARTIFACT_DIR.",
		"",
		"build the parser",
	}, "\n")
	if got := adapter.InitialPrompt(); got != wantPrefix {
		t.Fatalf("initial prompt = %q, want %q", got, wantPrefix)
	}
}

func TestLoopEngineExportsDispatchEnvVars(t *testing.T) {
	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		`echo "RESPONSE:$AGENT_MUX_DISPATCH_ID|$AGENT_MUX_ARTIFACT_DIR"`,
		"echo 'TURN:1,1,0'",
	}, "\n"))

	spec := testDispatchSpec(artifactDir)
	spec.DispatchID = "01TRACEENV"

	engine := NewLoopEngine(adapter, io.Discard, nil)
	result, err := engine.Dispatch(context.Background(), spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	want := "01TRACEENV|" + artifactDir
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
	if err := steer.CreateInbox(artifactDir); err != nil {
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

	if err := steer.WriteInbox(artifactDir, "resume during burst"); err != nil {
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

	if steer.HasMessages(artifactDir) {
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
	if err := steer.WriteInbox(artifactDir, message); err != nil {
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
		Engine:      "codex",
		Model:       "gpt-5.4",
		Prompt:      "ignored by scripted adapter",
		Cwd:         "/tmp",
		ArtifactDir: artifactDir,
		GraceSec:    1,
	}
}

func testMetadata() *types.DispatchMetadata {
	return &types.DispatchMetadata{
		Engine: "codex",
		Model:  "gpt-5.4",
		Tokens: &types.TokenUsage{},
	}
}

func newTestEmitter(t *testing.T, spec *types.DispatchSpec, stream *strings.Builder) *event.Emitter {
	t.Helper()
	if err := dispatch.WritePersistentMeta(spec, types.DispatchAnnotations{}); err != nil {
		t.Fatalf("WritePersistentMeta: %v", err)
	}
	if err := dispatch.WriteDispatchRef(spec.ArtifactDir, spec.DispatchID); err != nil {
		t.Fatalf("WriteDispatchRef: %v", err)
	}
	emitter, err := event.NewEmitter(spec.DispatchID, stream, filepath.Join(spec.ArtifactDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	emitter.SetStreamMode(event.StreamSilent)
	return emitter
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

func TestFinalizeCompletedStoresFullResponse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	artifactDir := t.TempDir()
	spec := testDispatchSpec(artifactDir)
	response := strings.Repeat("A", 50000)

	var stream strings.Builder
	emitter := newTestEmitter(t, spec, &stream)
	defer func() { _ = emitter.Close() }()

	result := finalizeCompleted(spec, types.DispatchAnnotations{}, emitter, response, emptyActivity(), testMetadata(), 25)

	if result.Response != response {
		t.Fatalf("response len = %d, want %d", len(result.Response), len(response))
	}
	storedResponse, err := dispatch.ReadResult(spec.DispatchID)
	if err != nil {
		t.Fatalf("ReadResult: %v", err)
	}
	if storedResponse != response {
		t.Fatalf("stored response len = %d, want %d", len(storedResponse), len(response))
	}
}

func TestFinalizeTimedOutStoresFullResponse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	artifactDir := t.TempDir()
	spec := testDispatchSpec(artifactDir)
	spec.TimeoutSec = 1
	response := strings.Repeat("B", 50000)

	var stream strings.Builder
	emitter := newTestEmitter(t, spec, &stream)
	defer func() { _ = emitter.Close() }()

	result := finalizeTimedOut(spec, types.DispatchAnnotations{}, emitter, response, emptyActivity(), testMetadata(), 25)

	if result.Status != types.StatusTimedOut {
		t.Fatalf("status = %q, want timed_out", result.Status)
	}
	if result.Response != response {
		t.Fatalf("response len = %d, want %d", len(result.Response), len(response))
	}
	storedResponse, err := dispatch.ReadResult(spec.DispatchID)
	if err != nil {
		t.Fatalf("ReadResult: %v", err)
	}
	if storedResponse != response {
		t.Fatalf("stored response len = %d, want %d", len(storedResponse), len(response))
	}
}

func TestFinalizeFailedStoresFullResponse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	artifactDir := t.TempDir()
	spec := testDispatchSpec(artifactDir)
	response := "你好" + strings.Repeat("C", 50000)

	var stream strings.Builder
	emitter := newTestEmitter(t, spec, &stream)
	defer func() { _ = emitter.Close() }()

	result := finalizeFailed(spec, types.DispatchAnnotations{}, emitter, response, emptyActivity(), testMetadata(), 25, dispatch.NewDispatchError("internal_error", "", ""))

	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Response != response {
		t.Fatalf("response len = %d, want %d", len(result.Response), len(response))
	}
	storedResponse, err := dispatch.ReadResult(spec.DispatchID)
	if err != nil {
		t.Fatalf("ReadResult: %v", err)
	}
	if storedResponse != response {
		t.Fatalf("stored response len = %d, want %d", len(storedResponse), len(response))
	}
}

func TestSoftSteerFIFOInjectsWithoutResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO steering is Unix-only")
	}

	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:fifo-live'",
		"read -t 10 line",
		"case \"$line\" in",
		"  *'Note from coordinator: FIFO nudge message'*) echo 'RESPONSE:fifo-live-ok' ;;",
		"  *) echo \"RESPONSE:unexpected:$line\" ;;",
		"esac",
		"echo 'TURN:1,1,0'",
	}, "\n"))

	// Use a non-codex engine: codex ≥0.121 disables the FIFO→stdin bridge
	// (see loop.go startRun) so this mechanism is only exercised for other
	// engines today. The scripted adapter models a generic engine that
	// reads from stdin.
	spec := testDispatchSpec(artifactDir)
	spec.Engine = "claude"
	var eventBuf strings.Builder
	engine := NewLoopEngine(adapter, &eventBuf, nil)
	engine.SetStreamMode(event.StreamNormal)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	waitForPath(t, steer.Path(artifactDir))
	writeSoftSteerFIFO(t, artifactDir, "nudge", "FIFO nudge message")

	select {
	case outcome := <-outcomeCh:
		if outcome.err != nil {
			t.Fatalf("Dispatch: %v", outcome.err)
		}
		if outcome.result.Status != types.StatusCompleted {
			t.Fatalf("status = %q, want completed; error = %+v", outcome.result.Status, outcome.result.Error)
		}
		if outcome.result.Response != "fifo-live-ok" {
			t.Fatalf("response = %q, want fifo-live-ok", outcome.result.Response)
		}
	case <-ctx.Done():
		t.Fatal("dispatch timed out")
	}

	if got := len(adapter.Calls()); got != 0 {
		t.Fatalf("resume calls = %d, want 0", got)
	}
	if !strings.Contains(eventBuf.String(), "coordinator_inject") {
		t.Fatalf("event stream missing coordinator_inject; got:\n%s", eventBuf.String())
	}
}

func TestSoftSteerFIFODeferredUntilToolEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO steering is Unix-only")
	}

	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:fifo-deferred'",
		"echo 'TOOL_START:long running tool'",
		"sleep 1",
		"echo 'TOOL_END:command_execution'",
		"read -t 10 line",
		"case \"$line\" in",
		"  *'Note from coordinator: deferred message'*) echo 'RESPONSE:deferred-ok' ;;",
		"  *) echo \"RESPONSE:unexpected:$line\" ;;",
		"esac",
		"echo 'TURN:1,1,0'",
	}, "\n"))

	// See TestSoftSteerFIFOInjectsWithoutResume for why we use a non-codex engine here.
	spec := testDispatchSpec(artifactDir)
	spec.Engine = "claude"
	var eventBuf strings.Builder
	engine := NewLoopEngine(adapter, &eventBuf, nil)
	engine.SetStreamMode(event.StreamNormal)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	waitForPath(t, steer.Path(artifactDir))
	time.Sleep(150 * time.Millisecond)
	writeSoftSteerFIFO(t, artifactDir, "nudge", "deferred message")

	select {
	case outcome := <-outcomeCh:
		if outcome.err != nil {
			t.Fatalf("Dispatch: %v", outcome.err)
		}
		if outcome.result.Status != types.StatusCompleted {
			t.Fatalf("status = %q, want completed; error = %+v", outcome.result.Status, outcome.result.Error)
		}
		if outcome.result.Response != "deferred-ok" {
			t.Fatalf("response = %q, want deferred-ok", outcome.result.Response)
		}
	case <-ctx.Done():
		t.Fatal("dispatch timed out")
	}

	if got := len(adapter.Calls()); got != 0 {
		t.Fatalf("resume calls = %d, want 0", got)
	}
	events := eventBuf.String()
	if !strings.Contains(events, "steer_deferred") {
		t.Fatalf("event stream missing steer_deferred; got:\n%s", events)
	}
	if !strings.Contains(events, "tool_active: deferring stdin steer") {
		t.Fatalf("event stream missing deferred stdin steer message; got:\n%s", events)
	}
}

func TestSoftSteerFIFOCleanupRemovesPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO steering is Unix-only")
	}

	artifactDir := t.TempDir()
	adapter := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:fifo-cleanup'",
		"echo 'RESPONSE:done'",
		"echo 'TURN:1,1,0'",
	}, "\n"))

	engine := NewLoopEngine(adapter, io.Discard, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, testDispatchSpec(artifactDir))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed; error = %+v", result.Status, result.Error)
	}

	if _, err := os.Stat(steer.Path(artifactDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stdin.pipe stat error = %v, want not exists", err)
	}
}

func TestScanHarnessOutputBufferOverflow(t *testing.T) {
	sa := newScriptedAdapter("exit 0")
	eng := NewLoopEngine(sa, io.Discard, nil)

	// Use a small buffer to trigger ErrTooLong without allocating megabytes.
	// We override the scanner inside scanHarnessOutput by feeding a line longer
	// than the 4MB limit through a pipe. Instead, we test the exported behaviour
	// by writing an oversized line to a pipe and calling scanHarnessOutput.
	pr, pw := io.Pipe()
	signals := make(chan loopSignal, 16)
	artifactDir := t.TempDir()

	go func() {
		// Write a single line that exceeds 4MB (the new buffer max).
		oversized := strings.Repeat("x", 4*1024*1024+1)
		_, _ = pw.Write([]byte(oversized + "\n"))
		pw.Close()
	}()

	eng.scanHarnessOutput(pr, 1, artifactDir, signals)

	// Drain signals and look for the ErrTooLong scan error.
	found := false
	for {
		select {
		case sig := <-signals:
			if sig.kind == loopSignalScanError && strings.Contains(sig.err.Error(), "buffer limit") {
				found = true
			}
		default:
			goto done
		}
	}
done:
	if !found {
		t.Error("expected a loopSignalScanError mentioning buffer limit for oversized output line")
	}
}

func TestFM7ProcessExitRaceFinalResponseCaptured(t *testing.T) {
	// FM-7: When the harness process exits immediately after emitting the final
	// RESPONSE line, the scanner goroutine may not have pushed the signal to the
	// channel before drainCurrentSignals runs on the procDone path. The fix adds
	// a second drain after <-streamDone.  This test verifies the response is
	// captured even when it races with process exit.
	//
	// Strategy: the script sleeps briefly so procDone fires before the scanner
	// goroutine finishes pushing the RESPONSE signal.  We repeat the test several
	// times to increase the chance of hitting the race window.
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			artifactDir := t.TempDir()
			adp := newScriptedAdapter(strings.Join([]string{
				"echo 'SESSION:fm7-race'",
				"echo 'PROGRESS:working'",
				// Emit the RESPONSE and immediately exit — this creates
				// the race between procDone and the scanner goroutine
				// pushing the EventResponse signal.
				"echo 'RESPONSE:final-answer-fm7'",
			}, "\n"))
			adp.supportsResume = false

			spec := testDispatchSpec(artifactDir)
			engine := NewLoopEngine(adp, io.Discard, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			result, err := engine.Dispatch(ctx, spec)
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if result.Status != types.StatusCompleted {
				t.Fatalf("status = %q, want completed; error = %+v", result.Status, result.Error)
			}
			if result.Response != "final-answer-fm7" {
				t.Fatalf("response = %q, want final-answer-fm7 (FM-7 race lost)", result.Response)
			}
		})
	}
}

func TestFM9FailedDispatchPreservesPartialResponse(t *testing.T) {
	// FM-9: When a dispatch fails after accumulating a partial response,
	// the DispatchResult should include that response instead of empty string.
	t.Setenv("HOME", t.TempDir())

	artifactDir := t.TempDir()
	adp := newScriptedAdapter(strings.Join([]string{
		"echo 'SESSION:fm9-partial'",
		"echo 'RESPONSE:partial work done'",
		"echo 'ERROR:test_error:simulated failure'",
		"exit 1",
	}, "\n"))
	adp.supportsResume = false

	spec := testDispatchSpec(artifactDir)
	engine := NewLoopEngine(adp, io.Discard, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Response != "partial work done" {
		t.Fatalf("response = %q, want 'partial work done' (FM-9: partial response should be preserved)", result.Response)
	}

	// Also verify the response was persisted to the dispatch dir.
	storedResponse, readErr := dispatch.ReadResult(spec.DispatchID)
	if readErr != nil {
		t.Fatalf("ReadResult: %v", readErr)
	}
	if storedResponse != "partial work done" {
		t.Fatalf("stored response = %q, want 'partial work done'", storedResponse)
	}
}

// TestCodex0121StdinClosedImmediately verifies the fix for codex-cli ≥0.121
// stdin drain (upstream PR openai/codex#15917). agent-mux must wire the
// child's stdin to an already-EOF reader, otherwise codex blocks forever in
// read(2) before processing the prompt.
//
// The scripted adapter mimics the codex behavior: it reads stdin to EOF via
// `cat` before emitting any events. Under the pre-fix behavior the engine
// attached an open, unwritten stdin pipe and this test would hang until the
// context timed out. Under the fix, stdin hits EOF immediately and the
// dispatch completes within milliseconds.
func TestCodex0121StdinClosedImmediately(t *testing.T) {
	artifactDir := t.TempDir()
	adp := newScriptedAdapter(strings.Join([]string{
		// Read stdin to EOF — same behavior codex 0.121 has. If the
		// engine leaves stdin open, this blocks forever.
		"cat >/dev/null",
		"echo 'SESSION:codex-stdin-test'",
		"echo 'RESPONSE:READY'",
		"echo 'TURN:1,1,0'",
	}, "\n"))
	adp.supportsResume = false

	spec := testDispatchSpec(artifactDir)
	// testDispatchSpec already sets Engine: "codex" — this is the path we want.
	if spec.Engine != "codex" {
		t.Fatalf("precondition: testDispatchSpec must use codex engine, got %q", spec.Engine)
	}

	engine := NewLoopEngine(adp, io.Discard, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := engine.Dispatch(ctx, spec)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Status != types.StatusCompleted {
		t.Fatalf("status = %q, want completed; error = %+v", result.Status, result.Error)
	}
	if result.Response != "READY" {
		t.Fatalf("response = %q, want READY", result.Response)
	}

	// For codex the FIFO bridge is skipped, so stdin.pipe must not exist.
	if _, statErr := os.Stat(steer.Path(artifactDir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stdin.pipe stat error = %v, want not exists (codex skips FIFO bridge)", statErr)
	}
}

// TestDeliverSoftSteerReturnsUnsupportedOnNilPipe verifies the deliverSoftSteer
// helper surfaces ErrSoftSteerUnsupported (not a generic error) when stdinPipe
// is nil — the condition we hit for codex ≥0.121 dispatches.
func TestDeliverSoftSteerReturnsUnsupportedOnNilPipe(t *testing.T) {
	run := &runHandle{stdinPipe: nil}
	err := deliverSoftSteer(run, adapter.CodexSoftSteerEnvelope{Action: "nudge", Message: "x"})
	if !errors.Is(err, ErrSoftSteerUnsupported) {
		t.Fatalf("err = %v, want ErrSoftSteerUnsupported", err)
	}
}

func writeSoftSteerFIFO(t *testing.T, artifactDir, action, message string) {
	t.Helper()

	payload, err := adapter.EncodeSoftSteerEnvelope(action, message)
	if err != nil {
		t.Fatalf("EncodeSoftSteerEnvelope: %v", err)
	}
	pipe, err := steer.OpenWriteNonblock(steer.Path(artifactDir))
	if err != nil {
		t.Fatalf("OpenWriteNonblock(%q): %v", steer.Path(artifactDir), err)
	}
	defer pipe.Close()
	if _, err := pipe.Write(payload); err != nil {
		t.Fatalf("write FIFO payload: %v", err)
	}
}


