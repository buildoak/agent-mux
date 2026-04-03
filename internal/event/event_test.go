package event

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestEmitEvent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	emitter, err := NewEmitter("01JQXYZ", io.Discard, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	if err := emitter.EmitDispatchStart(&types.DispatchSpec{
		DispatchID: "01JQXYZ",
		Engine:     "codex",
		Model:      "gpt-5.4",
		Effort:     "high",
		TimeoutSec: 600,
		GraceSec:   60,
		Cwd:        "/tmp/project",
	}); err != nil {
		t.Fatalf("EmitDispatchStart: %v", err)
	}

	// Read the event log
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var evt Event
	if err := json.Unmarshal([]byte(lines[0]), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if evt.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", evt.SchemaVersion)
	}
	if evt.Type != "dispatch_start" {
		t.Errorf("type = %q, want dispatch_start", evt.Type)
	}
	if evt.DispatchID != "01JQXYZ" {
		t.Errorf("dispatch_id = %q, want 01JQXYZ", evt.DispatchID)
	}
	if evt.Engine != "codex" {
		t.Errorf("engine = %q, want codex", evt.Engine)
	}
	if evt.TimeoutSec != 600 {
		t.Errorf("timeout_sec = %d, want 600", evt.TimeoutSec)
	}
	if evt.Cwd != "/tmp/project" {
		t.Errorf("cwd = %q, want /tmp/project", evt.Cwd)
	}
	if evt.Timestamp == "" {
		t.Error("ts should not be empty")
	}
}

func TestEmitMultipleEvents(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	emitter, err := NewEmitter("01JQXYZ", io.Discard, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	_ = emitter.EmitDispatchStart(&types.DispatchSpec{DispatchID: "01JQXYZ", Engine: "codex", Model: "gpt-5.4"})
	emitter.EmitToolStart("Read", "src/main.go")
	emitter.EmitToolEnd("Read", 120)
	emitter.EmitFileWrite("src/parser.go")
	emitter.EmitCommandRun("go test ./...")
	emitter.EmitDispatchEnd("completed", 45200)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines, got %d", len(lines))
	}

	expectedTypes := []string{"dispatch_start", "tool_start", "tool_end", "file_write", "command_run", "dispatch_end"}
	for i, line := range lines {
		var evt Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if evt.Type != expectedTypes[i] {
			t.Errorf("line %d: type = %q, want %q", i, evt.Type, expectedTypes[i])
		}
	}
}

func TestHeartbeatTicker(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	emitter, err := NewEmitter("01JQXYZ", io.Discard, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	stop, updateActivity := emitter.HeartbeatTicker(1) // 1 second for testing
	updateActivity("reading files")

	time.Sleep(1500 * time.Millisecond) // wait for at least 1 heartbeat
	stop()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		t.Fatal("expected at least 1 heartbeat event")
	}

	var evt Event
	if err := json.Unmarshal([]byte(lines[0]), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if evt.Type != "heartbeat" {
		t.Errorf("type = %q, want heartbeat", evt.Type)
	}
	if evt.IntervalS != 1 {
		t.Errorf("interval_s = %d, want 1", evt.IntervalS)
	}
	if evt.LastActivity != "reading files" {
		t.Errorf("last_activity = %q, want 'reading files'", evt.LastActivity)
	}
}

func TestEmitterWithoutLog(t *testing.T) {
	emitter, err := NewEmitter("01JQXYZ", io.Discard, "")
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	// Should not panic even without log file
	_ = emitter.EmitDispatchStart(&types.DispatchSpec{DispatchID: "01JQXYZ", Engine: "codex", Model: "gpt-5.4"})
}

func TestEmitResponseTruncated(t *testing.T) {
	var stream bytes.Buffer
	fullOutputPath := filepath.Join(t.TempDir(), "full_output.md")

	emitter, err := NewEmitter("01JQXYZ", &stream, "")
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	if err := emitter.EmitResponseTruncated(fullOutputPath); err != nil {
		t.Fatalf("EmitResponseTruncated: %v", err)
	}

	var evt Event
	if err := json.Unmarshal(bytes.TrimSpace(stream.Bytes()), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.Type != "response_truncated" {
		t.Fatalf("type = %q, want response_truncated", evt.Type)
	}
	if evt.FullOutputPath != fullOutputPath {
		t.Fatalf("full_output_path = %q, want %q", evt.FullOutputPath, fullOutputPath)
	}
}

func TestStreamModeSilent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")
	var stderrBuf bytes.Buffer

	emitter, err := NewEmitter("01JTEST", &stderrBuf, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()
	emitter.SetStreamMode(StreamSilent)

	// Emit 10 different event types
	_ = emitter.Emit(Event{Type: "heartbeat"})
	_ = emitter.Emit(Event{Type: "tool_start"})
	_ = emitter.Emit(Event{Type: "tool_end"})
	_ = emitter.Emit(Event{Type: "file_write"})
	_ = emitter.Emit(Event{Type: "command_run"})
	_ = emitter.Emit(Event{Type: "dispatch_start"})
	_ = emitter.Emit(Event{Type: "dispatch_end"})
	_ = emitter.Emit(Event{Type: "error"})
	_ = emitter.Emit(Event{Type: "frozen_warning"})
	_ = emitter.Emit(Event{Type: "progress"})

	// Check stderr: should only contain dispatch_start, dispatch_end, error, frozen_warning
	stderrLines := strings.Split(strings.TrimSpace(stderrBuf.String()), "\n")
	stderrTypes := make(map[string]bool)
	for _, line := range stderrLines {
		var evt Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("unmarshal stderr line: %v", err)
		}
		stderrTypes[evt.Type] = true
	}

	allowedInSilent := map[string]bool{
		"dispatch_start": true,
		"dispatch_end":   true,
		"error":          true,
		"frozen_warning": true,
	}
	for typ := range stderrTypes {
		if !allowedInSilent[typ] {
			t.Errorf("stderr contains %q which should be suppressed in silent mode", typ)
		}
	}
	for typ := range allowedInSilent {
		if !stderrTypes[typ] {
			t.Errorf("stderr missing %q which should be present in silent mode", typ)
		}
	}
	if len(stderrTypes) != 4 {
		t.Errorf("expected 4 event types in stderr, got %d: %v", len(stderrTypes), stderrTypes)
	}

	// Check event log: should contain all 10
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logLines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(logLines) != 10 {
		t.Fatalf("expected 10 lines in event log, got %d", len(logLines))
	}
}

func TestStreamModeNormal(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")
	var stderrBuf bytes.Buffer

	emitter, err := NewEmitter("01JTEST", &stderrBuf, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()
	emitter.SetStreamMode(StreamNormal)

	// Emit the same 10 event types
	_ = emitter.Emit(Event{Type: "heartbeat"})
	_ = emitter.Emit(Event{Type: "tool_start"})
	_ = emitter.Emit(Event{Type: "tool_end"})
	_ = emitter.Emit(Event{Type: "file_write"})
	_ = emitter.Emit(Event{Type: "command_run"})
	_ = emitter.Emit(Event{Type: "dispatch_start"})
	_ = emitter.Emit(Event{Type: "dispatch_end"})
	_ = emitter.Emit(Event{Type: "error"})
	_ = emitter.Emit(Event{Type: "frozen_warning"})
	_ = emitter.Emit(Event{Type: "progress"})

	// In Normal mode: all 10 should appear in stderr
	stderrLines := strings.Split(strings.TrimSpace(stderrBuf.String()), "\n")
	if len(stderrLines) != 10 {
		t.Fatalf("expected 10 lines in stderr (normal mode), got %d", len(stderrLines))
	}

	// Event log should also have all 10
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logLines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(logLines) != 10 {
		t.Fatalf("expected 10 lines in event log, got %d", len(logLines))
	}
}
