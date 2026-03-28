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

	emitter, err := NewEmitter("01JQXYZ", "coral-fox-nine", "AGENT_MUX_GO_01JQXYZ", io.Discard, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	if err := emitter.EmitDispatchStart(&types.DispatchSpec{
		DispatchID: "01JQXYZ",
		Salt:       "coral-fox-nine",
		TraceToken: "AGENT_MUX_GO_01JQXYZ",
		Engine:     "codex",
		Model:      "gpt-5.4",
		Effort:     "high",
		TimeoutSec: 600,
		GraceSec:   60,
		Cwd:        "/tmp/project",
		Skills:     []string{"go"},
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
	if evt.Salt != "coral-fox-nine" {
		t.Errorf("salt = %q, want coral-fox-nine", evt.Salt)
	}
	if evt.TraceToken != "AGENT_MUX_GO_01JQXYZ" {
		t.Errorf("trace_token = %q, want AGENT_MUX_GO_01JQXYZ", evt.TraceToken)
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
	if len(evt.Skills) != 1 || evt.Skills[0] != "go" {
		t.Errorf("skills = %#v, want []string{\"go\"}", evt.Skills)
	}
	if evt.Timestamp == "" {
		t.Error("ts should not be empty")
	}
}

func TestEmitMultipleEvents(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	emitter, err := NewEmitter("01JQXYZ", "coral-fox-nine", "AGENT_MUX_GO_01JQXYZ", io.Discard, logPath)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	_ = emitter.EmitDispatchStart(&types.DispatchSpec{DispatchID: "01JQXYZ", Salt: "coral-fox-nine", TraceToken: "AGENT_MUX_GO_01JQXYZ", Engine: "codex", Model: "gpt-5.4"})
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
		if evt.TraceToken != "AGENT_MUX_GO_01JQXYZ" {
			t.Errorf("line %d: trace_token = %q, want AGENT_MUX_GO_01JQXYZ", i, evt.TraceToken)
		}
	}
}

func TestHeartbeatTicker(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	emitter, err := NewEmitter("01JQXYZ", "coral-fox-nine", "AGENT_MUX_GO_01JQXYZ", io.Discard, logPath)
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
	emitter, err := NewEmitter("01JQXYZ", "coral-fox-nine", "AGENT_MUX_GO_01JQXYZ", io.Discard, "")
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	// Should not panic even without log file
	_ = emitter.EmitDispatchStart(&types.DispatchSpec{DispatchID: "01JQXYZ", Salt: "coral-fox-nine", TraceToken: "AGENT_MUX_GO_01JQXYZ", Engine: "codex", Model: "gpt-5.4"})
}

func TestEmitResponseTruncated(t *testing.T) {
	var stream bytes.Buffer
	fullOutputPath := filepath.Join(t.TempDir(), "full_output.md")

	emitter, err := NewEmitter("01JQXYZ", "coral-fox-nine", "AGENT_MUX_GO_01JQXYZ", &stream, "")
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
