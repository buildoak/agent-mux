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
	"github.com/buildoak/agent-mux/internal/types"
)

type resumeCall struct {
	sessionID string
	message   string
}

type scriptedAdapter struct {
	mu              sync.Mutex
	baseBinary      string
	resumeBinary    string
	supportsResume  bool
	initialScript   string
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
	return []string{"-c", a.initialScript}
}

func (a *scriptedAdapter) EnvVars(spec *types.DispatchSpec) []string {
	return nil
}

func (a *scriptedAdapter) SupportsResume() bool {
	return a.supportsResume
}

func (a *scriptedAdapter) ResumeArgs(sessionID, message string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resumeCalls = append(a.resumeCalls, resumeCall{sessionID: sessionID, message: message})
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
	engine := NewLoopEngine(adapter, io.Discard, nil)
	spec := testDispatchSpec(artifactDir)

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
