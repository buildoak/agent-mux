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

type Event struct {
	SchemaVersion  int      `json:"schema_version"`
	Type           string   `json:"type"`
	DispatchID     string   `json:"dispatch_id,omitempty"`
	Salt           string   `json:"salt,omitempty"`
	TraceToken     string   `json:"trace_token,omitempty"`
	Timestamp      string   `json:"ts"`
	Engine         string   `json:"engine,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	TimeoutSec     int      `json:"timeout_sec,omitempty"`
	GraceSec       int      `json:"grace_sec,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	ElapsedS       int      `json:"elapsed_s,omitempty"`
	IntervalS      int      `json:"interval_s,omitempty"`
	LastActivity   string   `json:"last_activity,omitempty"`
	Tool           string   `json:"tool,omitempty"`
	Args           string   `json:"args,omitempty"`
	DurationMS     int64    `json:"duration_ms,omitempty"`
	Path           string   `json:"path,omitempty"`
	Command        string   `json:"command,omitempty"`
	Message        string   `json:"message,omitempty"`
	Status         string   `json:"status,omitempty"`
	SilenceSeconds int      `json:"silence_seconds,omitempty"`
	ErrorCode      string   `json:"error_code,omitempty"`
	FullOutputPath string   `json:"full_output_path,omitempty"`
}
type Emitter struct {
	mu          sync.Mutex
	dispatchID  string
	salt        string
	traceToken  string
	eventWriter io.Writer
	eventLog    *os.File // append-only events.jsonl
}

func NewEmitter(dispatchID, salt, traceToken string, eventWriter io.Writer, eventLogPath string) (*Emitter, error) {
	e := &Emitter{
		dispatchID:  dispatchID,
		salt:        salt,
		traceToken:  traceToken,
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
func (e *Emitter) Emit(evt Event) error {
	evt.SchemaVersion = SchemaVersion
	evt.DispatchID = e.dispatchID
	evt.Salt = e.salt
	evt.TraceToken = e.traceToken
	evt.Timestamp = time.Now().UTC().Format(time.RFC3339)

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	line := append(data, '\n')

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.eventWriter != nil {
		if _, err := e.eventWriter.Write(line); err != nil {
			return fmt.Errorf("write event stream: %w", err)
		}
	}

	if e.eventLog != nil {
		if _, err := e.eventLog.Write(line); err != nil {
			return fmt.Errorf("write event log: %w", err)
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
		Skills:     append([]string(nil), spec.Skills...),
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
func (e *Emitter) EmitError(code, message string) error {
	return e.emitType("error", Event{ErrorCode: code, Message: message})
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
