package event

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

const SchemaVersion = 1

// StreamMode controls which events are emitted to stderr (eventWriter).
// All events are always written to the event log file regardless of mode.
type StreamMode int

const (
	StreamSilent  StreamMode = iota // default: bookend + failure events only
	StreamNormal                    // all events to stderr (current behavior)
	StreamVerbose                   // raw harness lines + all events
)

type Event struct {
	SchemaVersion  int    `json:"schema_version"`
	Type           string `json:"type"`
	DispatchID     string `json:"dispatch_id,omitempty"`
	Timestamp      string `json:"ts"`
	Engine         string `json:"engine,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	TimeoutSec     int    `json:"timeout_sec,omitempty"`
	GraceSec       int    `json:"grace_sec,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	ElapsedS       int    `json:"elapsed_s,omitempty"`
	IntervalS      int    `json:"interval_s,omitempty"`
	LastActivity   string `json:"last_activity,omitempty"`
	Tool           string `json:"tool,omitempty"`
	Args           string `json:"args,omitempty"`
	DurationMS     int64  `json:"duration_ms,omitempty"`
	Path           string `json:"path,omitempty"`
	Command        string `json:"command,omitempty"`
	Message        string `json:"message,omitempty"`
	Status         string `json:"status,omitempty"`
	SilenceSeconds int    `json:"silence_seconds,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	FullOutputPath string `json:"full_output_path,omitempty"`
}
type Emitter struct {
	mu          sync.Mutex
	dispatchID  string
	eventWriter io.Writer
	eventLog    *os.File // append-only events.jsonl
	streamMode  StreamMode
}

func NewEmitter(dispatchID string, eventWriter io.Writer, eventLogPath string) (*Emitter, error) {
	e := &Emitter{
		dispatchID:  dispatchID,
		eventWriter: eventWriter,
	}

	if eventLogPath != "" {
		f, err := os.OpenFile(eventLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("open event log: %w", err)
		}
		e.eventLog = f
	}

	return e, nil
}
func (e *Emitter) Close() error {
	if e.eventLog != nil {
		return e.eventLog.Close()
	}
	return nil
}

// SetStreamMode sets which events are emitted to stderr.
// Must be called before any Emit calls (not thread-safe by design — set once at init).
func (e *Emitter) SetStreamMode(m StreamMode) {
	e.streamMode = m
}

// silentAllowedTypes lists event types emitted to stderr in StreamSilent mode.
var silentAllowedTypes = map[string]bool{
	"dispatch_start":        true,
	"dispatch_end":          true,
	"error":                 true,
	"frozen_warning":        true,
	"frozen_killed":         true,
	"timeout_warning":       true,
	"long_command_detected": true,
	"response_truncated":    true,
	"preview":               true,
}

func (e *Emitter) shouldEmitToStderr(eventType string) bool {
	if e.streamMode != StreamSilent {
		return true
	}
	return silentAllowedTypes[eventType]
}

func (e *Emitter) Emit(evt Event) error {
	evt.SchemaVersion = SchemaVersion
	evt.DispatchID = e.dispatchID
	evt.Timestamp = time.Now().UTC().Format(time.RFC3339)

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	line := append(data, '\n')

	e.mu.Lock()
	defer e.mu.Unlock()

	// Always write to event log (full telemetry)
	if e.eventLog != nil {
		if _, err := e.eventLog.Write(line); err != nil {
			return fmt.Errorf("write event log: %w", err)
		}
	}

	// Gate stderr based on stream mode
	if e.eventWriter != nil && e.shouldEmitToStderr(evt.Type) {
		if _, err := e.eventWriter.Write(line); err != nil {
			return fmt.Errorf("write event stream: %w", err)
		}
	}

	return nil
}
func (e *Emitter) EmitDispatchStart(spec *types.DispatchSpec) error {
	return e.Emit(Event{
		Type:       "dispatch_start",
		Engine:     spec.Engine,
		Model:      spec.Model,
		Effort:     spec.Effort,
		TimeoutSec: spec.TimeoutSec,
		GraceSec:   spec.GraceSec,
		Cwd:        spec.Cwd,
	})
}
func (e *Emitter) EmitDispatchEnd(status string, durationMS int64) error {
	return e.emitType("dispatch_end", Event{Status: status, DurationMS: durationMS})
}
func (e *Emitter) EmitHeartbeat(elapsedS, intervalS int, lastActivity string) error {
	return e.emitType("heartbeat", Event{ElapsedS: elapsedS, IntervalS: intervalS, LastActivity: lastActivity})
}
func (e *Emitter) EmitToolStart(tool, args string) error {
	return e.emitType("tool_start", Event{Tool: tool, Args: args})
}
func (e *Emitter) EmitToolEnd(tool string, durationMS int64) error {
	return e.emitType("tool_end", Event{Tool: tool, DurationMS: durationMS})
}
func (e *Emitter) EmitFileWrite(path string) error {
	return e.emitType("file_write", Event{Path: path})
}
func (e *Emitter) EmitFileRead(path string) error {
	return e.emitType("file_read", Event{Path: path})
}
func (e *Emitter) EmitCommandRun(command string) error {
	return e.emitType("command_run", Event{Command: command})
}
func (e *Emitter) EmitProgress(message string) error {
	return e.emitType("progress", Event{Message: message})
}
func (e *Emitter) EmitTimeoutWarning(message string) error {
	return e.emitType("timeout_warning", Event{Message: message})
}
func (e *Emitter) EmitFrozenWarning(silenceSeconds int, message string) error {
	return e.emitType("frozen_warning", Event{SilenceSeconds: silenceSeconds, Message: message})
}
func (e *Emitter) EmitLongCommandDetected(command string, timeoutSeconds int) error {
	return e.emitType("long_command_detected", Event{
		Command:        command,
		TimeoutSeconds: timeoutSeconds,
		Message:        fmt.Sprintf("Long-running command detected: %s. Silence threshold extended to %ds.", command, timeoutSeconds),
	})
}
func (e *Emitter) EmitError(code, message string) error {
	return e.emitType("error", Event{ErrorCode: code, Message: message})
}
func (e *Emitter) EmitInfo(code, message string) error {
	return e.emitType("info", Event{ErrorCode: code, Message: message})
}
func (e *Emitter) EmitResponseTruncated(fullOutputPath string) error {
	return e.emitType("response_truncated", Event{FullOutputPath: fullOutputPath})
}
func (e *Emitter) HeartbeatTicker(intervalSec int) (stop func(), updateActivity func(string)) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	startTime := time.Now()

	var activityMu sync.Mutex
	lastActivity := "initializing"

	go func() {
		for {
			select {
			case <-ticker.C:
				activityMu.Lock()
				act := lastActivity
				activityMu.Unlock()
				elapsed := int(time.Since(startTime).Seconds())
				_ = e.EmitHeartbeat(elapsed, intervalSec, act)
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
	updateFn := func(activity string) {
		activityMu.Lock()
		lastActivity = activity
		activityMu.Unlock()
	}
	return cancel, updateFn
}

func (e *Emitter) emitType(typ string, evt Event) error {
	evt.Type = typ
	return e.Emit(evt)
}
