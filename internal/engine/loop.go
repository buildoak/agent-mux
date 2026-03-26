package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/event"
	"github.com/buildoak/agent-mux/internal/supervisor"
	"github.com/buildoak/agent-mux/internal/types"
)

type LoopEngine struct {
	adapter     types.HarnessAdapter
	eventWriter io.Writer
	verbose     bool
}

func NewLoopEngine(engineName string, adapter types.HarnessAdapter, validModels []string, eventWriter io.Writer) *LoopEngine {
	_, _ = engineName, validModels
	return &LoopEngine{
		adapter:     adapter,
		eventWriter: eventWriter,
	}
}

func (e *LoopEngine) SetVerbose(v bool) {
	e.verbose = v
}

func (e *LoopEngine) Dispatch(ctx context.Context, spec *types.DispatchSpec) (*types.DispatchResult, error) {
	startTime := time.Now()
	metadata := &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}}
	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return buildFailureResult(spec, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err), "Choose a writable --artifact-dir path."), nil
	}
	if err := dispatch.WriteDispatchMeta(spec.ArtifactDir, spec); err != nil {
		return buildFailureResult(spec, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Write dispatch metadata in %q: %v", spec.ArtifactDir, err), "Ensure the artifact directory is writable."), nil
	}
	eventLogPath := filepath.Join(spec.ArtifactDir, "events.jsonl")
	emitter, err := event.NewEmitter(spec.DispatchID, spec.Salt, e.eventWriter, eventLogPath)
	if err != nil {
		return buildFailureResult(spec, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Create event log %q: %v", eventLogPath, err), "Ensure the artifact directory is writable."), nil
	}
	defer emitter.Close()

	_ = emitter.EmitDispatchStart(spec)
	args := e.adapter.BuildArgs(spec)
	binary := e.adapter.Binary()
	if _, err := exec.LookPath(binary); err != nil {
		return buildFailureResult(
			spec, metadata, startTime, emitter,
			"binary_not_found",
			fmt.Sprintf("Binary %q not found on PATH.", binary),
			fmt.Sprintf("Install %s: see the engine documentation for installation instructions.", binary),
		), nil
	}
	env := append(os.Environ(),
		fmt.Sprintf("AGENT_MUX_DISPATCH_ID=%s", spec.DispatchID),
		fmt.Sprintf("AGENT_MUX_ARTIFACT_DIR=%s", spec.ArtifactDir),
		fmt.Sprintf("AGENT_MUX_DEPTH=%d", spec.Depth),
	)
	if spec.ContextFile != "" {
		env = append(env, fmt.Sprintf("AGENT_MUX_CONTEXT=%s", spec.ContextFile))
	}
	proc := supervisor.NewProcess(binary, args, spec.Cwd, env)
	cmd := proc.Cmd()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return buildFailureResult(spec, metadata, startTime, emitter, "process_killed", fmt.Sprintf("Set up stdout pipe for %s: %v", binary, err), "Check that the engine binary can be started."), nil
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := proc.Start(); err != nil {
		return buildFailureResult(
			spec, metadata, startTime, emitter,
			"process_killed",
			fmt.Sprintf("Failed to start %s: %v", binary, err),
			"Check that the binary is installed and accessible.",
		), nil
	}
	softTimeout := time.Duration(spec.TimeoutSec) * time.Second
	gracePeriod := time.Duration(spec.GraceSec) * time.Second
	activity := &types.DispatchActivity{
		FilesChanged: []string{},
		FilesRead:    []string{},
		CommandsRun:  []string{},
		ToolCalls:    []string{},
	}
	var (
		mu            sync.Mutex
		lastResponse  string
		sessionID     string
		totalTokens   *types.TokenUsage
		turnCount     int
		lastError     *types.HarnessEvent
		lastActivity  = time.Now()
		frozenWarned  bool
		terminalState string // "", "timed_out", "failed", "interrupted"
		softTimedOut  bool
		streamScanErr error
	)

	setTerminal := func(state string) bool {
		if terminalState != "" {
			return false
		}
		terminalState = state
		return true
	}

	silenceWarn := intEngineOpt(spec, "silence_warn_seconds", 90)
	silenceKill := intEngineOpt(spec, "silence_kill_seconds", 180)
	stopHeartbeat, updateActivity := emitter.HeartbeatTicker(intEngineOpt(spec, "heartbeat_interval_sec", 15))
	defer stopHeartbeat()
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if e.verbose {
				fmt.Fprintf(e.eventWriter, "[engine] %s\n", line)
			}

			evt, err := e.adapter.ParseEvent(line)
			if err != nil {
				_ = emitter.EmitError("output_parse_error", fmt.Sprintf("Parse harness event: %v", err))
				continue
			}
			if evt == nil {
				continue
			}

			mu.Lock()
			lastActivity = time.Now()
			frozenWarned = false
			switch evt.Kind {
			case types.EventSessionStart:
				sessionID = evt.SessionID
				updateActivity("session started")

			case types.EventToolStart:
				if evt.Tool != "" {
					activity.ToolCalls = append(activity.ToolCalls, evt.Tool)
				}
				if evt.Command != "" {
					_ = emitter.EmitToolStart(evt.Tool, evt.Command)
					updateActivity(fmt.Sprintf("running: %s", truncate(evt.Command, 60)))
				} else {
					_ = emitter.EmitToolStart(evt.Tool, "")
					updateActivity(fmt.Sprintf("tool: %s", evt.Tool))
				}

			case types.EventToolEnd:
				_ = emitter.EmitToolEnd(evt.Tool, evt.DurationMS)

			case types.EventFileWrite:
				activity.FilesChanged = appendUnique(activity.FilesChanged, evt.FilePath)
				_ = emitter.EmitFileWrite(evt.FilePath)
				updateActivity(fmt.Sprintf("wrote: %s", evt.FilePath))

			case types.EventFileRead:
				activity.FilesRead = appendUnique(activity.FilesRead, evt.FilePath)
				_ = emitter.EmitFileRead(evt.FilePath)

			case types.EventCommandRun:
				activity.ToolCalls = appendUnique(activity.ToolCalls, evt.Tool)
				activity.CommandsRun = appendUnique(activity.CommandsRun, evt.Command)
				_ = emitter.EmitCommandRun(evt.Command)
				updateActivity(fmt.Sprintf("running: %s", truncate(evt.Command, 60)))

			case types.EventProgress:
				_ = emitter.EmitProgress(truncate(evt.Text, 200))

			case types.EventResponse:
				lastResponse = evt.Text
				if evt.Tokens != nil {
					totalTokens = evt.Tokens
				}
				if evt.SessionID != "" {
					sessionID = evt.SessionID
				}
				updateActivity("received response")

			case types.EventTurnComplete:
				turnCount++
				if evt.Tokens != nil {
					totalTokens = evt.Tokens
				}
				updateActivity("turn completed")

			case types.EventTurnFailed:
				lastError = evt
				updateActivity("turn failed")

			case types.EventError:
				lastError = evt
				_ = emitter.EmitError(evt.ErrorCode, evt.Text)
				updateActivity(fmt.Sprintf("error: %s", evt.ErrorCode))

			case types.EventRawPassthrough:
			}
			mu.Unlock()
		}
		if err := scanner.Err(); err != nil {
			streamScanErr = err
			_ = emitter.EmitError("output_parse_error", fmt.Sprintf("Read harness event stream: %v", err))
		}
	}()

	watchdogTicker := time.NewTicker(5 * time.Second)
	defer watchdogTicker.Stop()
	var softTimer, hardTimer <-chan time.Time
	if softTimeout > 0 {
		softTimer = time.After(softTimeout)
	}
	procDone := make(chan error, 1)
	go func() {
		procDone <- proc.Wait()
	}()
	var procErr error
	for {
		select {
		case procErr = <-procDone:
			<-streamDone // Wait for stream to finish
			goto buildResult

		case <-softTimer:
			softTimedOut = true
			_ = emitter.EmitTimeoutWarning(fmt.Sprintf("Soft timeout reached. Grace period: %ds.", spec.GraceSec))
			if gracePeriod > 0 {
				softTimer = nil
				hardTimer = time.After(gracePeriod)
			} else {
				hardTimer = time.After(0)
			}

		case <-hardTimer:
			setTerminal("timed_out")
			_ = proc.GracefulStop(5)
			<-streamDone
			goto buildResult

		case <-watchdogTicker.C:
			mu.Lock()
			silence := int(time.Since(lastActivity).Seconds())
			shouldWarn := silence >= silenceWarn && !frozenWarned
			if shouldWarn {
				frozenWarned = true
			}
			mu.Unlock()
			if silence >= silenceKill && setTerminal("failed") {
				_ = emitter.EmitError("frozen_tool_call", fmt.Sprintf("No harness events for %ds. Likely frozen. Process terminated.", silence))
				_ = proc.GracefulStop(5)
				<-streamDone
				goto buildResult
			}
			if shouldWarn {
				_ = emitter.EmitFrozenWarning(silence, fmt.Sprintf("No harness events for %ds.", silence))
			}

		case <-ctx.Done():
			if setTerminal("interrupted") {
				_ = emitter.EmitError("interrupted", "Dispatch interrupted by caller cancellation.")
				_ = proc.GracefulStop(5)
				<-streamDone
				goto buildResult
			}
		}
	}

buildResult:
	stopHeartbeat()

	durationMS := time.Since(startTime).Milliseconds()

	mu.Lock()
	state := terminalState
	response := lastResponse
	errEvt := lastError
	tokens := totalTokens
	turns := turnCount
	sid := sessionID
	act := activity
	mu.Unlock()

	if tokens == nil {
		tokens = &types.TokenUsage{}
	}
	metadata = &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: tokens, Turns: turns}
	metadata.SessionID = sid

	switch state {
	case "timed_out":
		return finalizeTimedOut(spec, emitter, response, act, metadata, durationMS), nil

	case "failed":
		return finalizeFailed(spec, emitter, act, metadata, durationMS, failureFromEventOrProcess(errEvt, proc.ExitCode(), stderrBuf.String(), false)), nil

	case "interrupted":
		return finalizeFailed(spec, emitter, act, metadata, durationMS, dispatch.NewDispatchError("interrupted", "Dispatch interrupted by caller cancellation.", "")), nil

	default:
		if softTimedOut {
			return finalizeCompleted(spec, emitter, response, act, metadata, durationMS), nil
		}

		if streamScanErr != nil && procErr == nil {
			return finalizeFailed(spec, emitter, act, metadata, durationMS, dispatch.NewDispatchError("output_parse_error", fmt.Sprintf("Read harness event stream: %v", streamScanErr), "")), nil
		}

		if procErr != nil {
			return finalizeFailed(spec, emitter, act, metadata, durationMS, failureFromEventOrProcess(errEvt, proc.ExitCode(), stderrBuf.String(), true)), nil
		}
		return finalizeCompleted(spec, emitter, response, act, metadata, durationMS), nil
	}
}

func buildFailureResult(spec *types.DispatchSpec, metadata *types.DispatchMetadata, startTime time.Time, emitter *event.Emitter, code, message, suggestion string) *types.DispatchResult {
	durationMS := time.Since(startTime).Milliseconds()
	if emitter != nil {
		_ = emitter.EmitDispatchEnd("failed", durationMS)
	}
	return dispatch.BuildFailedResult(spec, dispatch.NewDispatchError(code, message, suggestion), emptyActivity(), metadata, durationMS)
}

func emptyActivity() *types.DispatchActivity {
	return &types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}}
}

func finalizeCompleted(spec *types.DispatchSpec, emitter *event.Emitter, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	_ = emitter.EmitDispatchEnd("completed", durationMS)
	_ = dispatch.UpdateDispatchMeta(spec.ArtifactDir, "completed", dispatch.ScanArtifacts(spec.ArtifactDir))
	return dispatch.BuildCompletedResult(spec, response, activity, metadata, durationMS, spec.ResponseMaxChars)
}

func finalizeTimedOut(spec *types.DispatchSpec, emitter *event.Emitter, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	_ = emitter.EmitDispatchEnd("timed_out", durationMS)
	_ = dispatch.UpdateDispatchMeta(spec.ArtifactDir, "timed_out", dispatch.ScanArtifacts(spec.ArtifactDir))
	return dispatch.BuildTimedOutResult(spec, response, fmt.Sprintf("Soft timeout at %ds, hard kill after %ds grace.", spec.TimeoutSec, spec.GraceSec), activity, metadata, durationMS)
}

func finalizeFailed(spec *types.DispatchSpec, emitter *event.Emitter, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64, dispErr *types.DispatchError) *types.DispatchResult {
	_ = emitter.EmitDispatchEnd("failed", durationMS)
	artifacts := dispatch.ScanArtifacts(spec.ArtifactDir)
	_ = dispatch.UpdateDispatchMeta(spec.ArtifactDir, "failed", artifacts)
	dispErr.PartialArtifacts = artifacts
	return dispatch.BuildFailedResult(spec, dispErr, activity, metadata, durationMS)
}

func failureFromEventOrProcess(errEvt *types.HarnessEvent, exitCode int, stderr string, includeExitPrefix bool) *types.DispatchError {
	if errEvt != nil {
		return dispatch.NewDispatchError(errEvt.ErrorCode, errEvt.Text, "")
	}
	base := "Process failed."
	if includeExitPrefix {
		base = fmt.Sprintf("Exit code %d.", exitCode)
	}
	tail := ""
	if strings.TrimSpace(stderr) != "" {
		lines := strings.Split(stderr, "\n")
		if len(lines) > 5 {
			lines = lines[len(lines)-5:]
		}
		tail = strings.Join(lines, "\n")
	}
	if tail != "" {
		if includeExitPrefix {
			base += " stderr: " + tail
		} else {
			base = fmt.Sprintf("Exit code %d. stderr: %s", exitCode, tail)
		}
	}
	return dispatch.NewDispatchError("process_killed", base, "Check engine logs.")
}

func appendUnique(slice []string, item string) []string {
	if item == "" {
		return slice
	}
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func intEngineOpt(spec *types.DispatchSpec, key string, fallback int) int {
	if spec == nil || spec.EngineOpts == nil {
		return fallback
	}
	switch v := spec.EngineOpts[key].(type) {
	case int:
		if v > 0 {
			return v
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return fallback
}
