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
	"github.com/buildoak/agent-mux/internal/hooks"
	"github.com/buildoak/agent-mux/internal/inbox"
	"github.com/buildoak/agent-mux/internal/supervisor"
	"github.com/buildoak/agent-mux/internal/types"
)

type LoopEngine struct {
	adapter     types.HarnessAdapter
	eventWriter io.Writer
	verbose     bool
	hookEval    *hooks.Evaluator
}

type runHandle struct {
	proc       *supervisor.Process
	stdout     io.ReadCloser
	streamDone chan struct{}
	procDone   chan error
}

type loopSignalKind int

const (
	loopSignalEvent loopSignalKind = iota
	loopSignalInbox
	loopSignalParseError
	loopSignalScanError
)

type loopSignal struct {
	kind    loopSignalKind
	runGen  uint64
	event   *types.HarnessEvent
	message string
	err     error
}

func (e *LoopEngine) scanHarnessOutput(stdout io.Reader, runGen uint64, artifactDir string, signals chan<- loopSignal) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if e.verbose {
			fmt.Fprintf(e.eventWriter, "[engine] %s\n", line)
		}
		evt, err := e.adapter.ParseEvent(line)
		if err != nil {
			signals <- loopSignal{kind: loopSignalParseError, runGen: runGen, err: err}
		} else if evt != nil {
			signals <- loopSignal{kind: loopSignalEvent, runGen: runGen, event: evt}
		}

		if inbox.HasMessages(artifactDir) {
			messages, err := inbox.ReadInbox(artifactDir)
			if err != nil {
				signals <- loopSignal{kind: loopSignalScanError, runGen: runGen, err: fmt.Errorf("read coordinator inbox: %w", err)}
				continue
			}
			for _, msg := range messages {
				signals <- loopSignal{kind: loopSignalInbox, runGen: runGen, message: msg}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		signals <- loopSignal{kind: loopSignalScanError, runGen: runGen, err: err}
	}
}

func NewLoopEngine(adapter types.HarnessAdapter, eventWriter io.Writer, hookEval *hooks.Evaluator) *LoopEngine {
	return &LoopEngine{
		adapter:     adapter,
		eventWriter: eventWriter,
		hookEval:    hookEval,
	}
}

func (e *LoopEngine) SetVerbose(v bool) {
	e.verbose = v
}

func (e *LoopEngine) Dispatch(ctx context.Context, spec *types.DispatchSpec) (*types.DispatchResult, error) {
	startTime := time.Now()
	if spec.MaxDepth > 0 && spec.Depth >= spec.MaxDepth {
		metadata := &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}}
		return buildFailureResult(spec, metadata, startTime, nil, "max_depth_exceeded", fmt.Sprintf("Max dispatch depth %d reached. Complete work directly.", spec.MaxDepth), ""), nil
	}
	dispatchSpec := *spec
	if dispatchSpec.ArtifactDir != "" {
		dispatchSpec.Prompt = "Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR.\n\n" + dispatchSpec.Prompt
	}

	metadata := &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}}
	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return buildFailureResult(spec, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err), "Choose a writable --artifact-dir path."), nil
	}
	if err := dispatch.WriteDispatchMeta(spec.ArtifactDir, spec); err != nil {
		return buildFailureResult(spec, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Write dispatch metadata in %q: %v", spec.ArtifactDir, err), "Ensure the artifact directory is writable."), nil
	}
	inboxCreateErr := inbox.CreateInbox(spec.ArtifactDir)
	if inboxCreateErr != nil {
		if e.verbose && e.eventWriter != nil {
			fmt.Fprintf(e.eventWriter, "[engine] create inbox: %v\n", inboxCreateErr)
		}
	}
	eventLogPath := filepath.Join(spec.ArtifactDir, "events.jsonl")
	emitter, err := event.NewEmitter(spec.DispatchID, spec.Salt, e.eventWriter, eventLogPath)
	if err != nil {
		return buildFailureResult(spec, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Create event log %q: %v", eventLogPath, err), "Ensure the artifact directory is writable."), nil
	}
	defer emitter.Close()
	if inboxCreateErr != nil {
		_ = emitter.Emit(event.Event{
			Type:      "warning",
			ErrorCode: "coordinator_inbox_create_failed",
			Message:   fmt.Sprintf("Create coordinator inbox failed: %v", inboxCreateErr),
		})
	}

	_ = emitter.EmitDispatchStart(spec)
	args := e.adapter.BuildArgs(&dispatchSpec)
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
	env = append(env, e.adapter.EnvVars(&dispatchSpec)...)
	if spec.ContextFile != "" {
		env = append(env, fmt.Sprintf("AGENT_MUX_CONTEXT=%s", spec.ContextFile))
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
		mu               sync.Mutex
		lastResponse     string
		lastProgressText string
		sessionID        string
		totalTokens      *types.TokenUsage
		turnCount        int
		lastError        *types.HarnessEvent
		lastActivity     = time.Now()
		frozenWarned     bool
		terminalState    string // "", "timed_out", "failed", "interrupted"
		softTimedOut     bool
		streamScanErr    error
		dispatchErr      *types.DispatchError
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
	signals := make(chan loopSignal, 512)
	var procErr error
	forceBuildResult := false
	runReadyForRestart := false
	pendingMessages := make([]string, 0)
	restarting := false
	var currentGen uint64 = 1
	var currentRun *runHandle
	var currentStderr *strings.Builder

	handleHarnessEvent := func(evt *types.HarnessEvent) {
		mu.Lock()
		lastActivity = time.Now()
		frozenWarned = false
		runReadyForRestart = true
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
			if evt.Text != "" {
				lastProgressText = evt.Text
			}
			_ = emitter.EmitProgress(truncate(evt.Text, 200))

		case types.EventResponse:
			lastResponse = evt.Text
			if evt.Tokens != nil {
				totalTokens = evt.Tokens
			}
			if evt.Turns > 0 {
				turnCount = evt.Turns
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

	emitHarnessEvent := func(evt *types.HarnessEvent) {
		if evt == nil {
			return
		}
		handleHarnessEvent(evt)
		if evt.SecondaryKind == types.EventUnknown {
			return
		}
		secondary := *evt
		secondary.Kind = evt.SecondaryKind
		secondary.SecondaryKind = types.EventUnknown
		handleHarnessEvent(&secondary)
	}

	startRun := func(runGen uint64, runArgs []string) (*runHandle, *strings.Builder, error) {
		runBinary := e.adapter.Binary()
		proc := supervisor.NewProcess(runBinary, runArgs, spec.Cwd, env)
		cmd := proc.Cmd()
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, fmt.Errorf("set up stdout pipe for %s: %w", runBinary, err)
		}
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf
		if err := proc.Start(); err != nil {
			_ = stdout.Close()
			return nil, &stderrBuf, fmt.Errorf("failed to start %s: %w", runBinary, err)
		}

		run := &runHandle{
			proc:       proc,
			stdout:     stdout,
			streamDone: make(chan struct{}),
			procDone:   make(chan error, 1),
		}
		go func(run *runHandle) {
			defer close(run.streamDone)
			e.scanHarnessOutput(run.stdout, runGen, spec.ArtifactDir, signals)
		}(run)
		go func() {
			run.procDone <- proc.Wait()
		}()
		return run, &stderrBuf, nil
	}

	enqueueInboxMessages := func() {
		if !inbox.HasMessages(spec.ArtifactDir) {
			return
		}
		messages, err := inbox.ReadInbox(spec.ArtifactDir)
		if err != nil {
			_ = emitter.EmitError("coordinator_inbox_read_failed", fmt.Sprintf("Read coordinator inbox: %v", err))
			return
		}
		for _, msg := range messages {
			_ = emitter.Emit(event.Event{Type: "coordinator_inject", Message: msg})
			pendingMessages = append(pendingMessages, msg)
		}
	}

	startRestartFailure := func(code, message, suggestion string) {
		if setTerminal("failed") {
			dispatchErr = dispatch.NewDispatchError(code, message, suggestion)
			_ = emitter.EmitError(code, message)
		}
	}

	stopCurrentRun := func() {
		if currentRun == nil {
			return
		}
		_ = currentRun.proc.GracefulStop(spec.GraceSec)
		<-currentRun.streamDone
		select {
		case procErr = <-currentRun.procDone:
		default:
		}
	}

	processSignal := func(sig loopSignal) {
		switch sig.kind {
		case loopSignalEvent:
			if e.hookEval != nil {
				action, matched := e.hookEval.CheckEvent(sig.event)
				if action == "deny" {
					setTerminal("failed")
					dispatchErr = dispatch.NewDispatchError("event_denied",
						fmt.Sprintf("event blocked by hooks policy (matched: %q)", matched),
						"Remove the matching content from your prompt or adjust hook configuration.")
					_ = emitter.EmitError("event_denied", fmt.Sprintf("hooks policy violation: matched %q", matched))
					stopCurrentRun()
					forceBuildResult = true
					return
				} else if action == "warn" {
					_ = emitter.Emit(event.Event{
						Type:      "warning",
						ErrorCode: "hook_warning",
						Message:   fmt.Sprintf("hooks warning: matched pattern %q", matched),
					})
				}
			}
			emitHarnessEvent(sig.event)

		case loopSignalInbox:
			_ = emitter.Emit(event.Event{Type: "coordinator_inject", Message: sig.message})
			pendingMessages = append(pendingMessages, sig.message)

		case loopSignalParseError:
			_ = emitter.EmitError("output_parse_error", fmt.Sprintf("Parse harness event: %v", sig.err))

		case loopSignalScanError:
			streamScanErr = sig.err
			if strings.Contains(strings.ToLower(sig.err.Error()), "coordinator inbox") {
				_ = emitter.EmitError("coordinator_inbox_read_failed", fmt.Sprintf("Read coordinator inbox: %v", sig.err))
			} else {
				_ = emitter.EmitError("output_parse_error", fmt.Sprintf("Read harness event stream: %v", sig.err))
			}
		}
	}

	drainCurrentSignals := func(runGen uint64) {
		for {
			select {
			case sig := <-signals:
				if sig.runGen != runGen {
					continue
				}
				processSignal(sig)
			default:
				return
			}
		}
	}

	restartRun := func(alreadyExited bool) bool {
		if restarting || terminalState != "" || len(pendingMessages) == 0 {
			return false
		}
		if !e.adapter.SupportsResume() {
			startRestartFailure("resume_unsupported", "Coordinator injection requires resume support from the harness adapter.", "Use an adapter that implements session resume.")
			stopCurrentRun()
			forceBuildResult = true
			return false
		}
		if !runReadyForRestart {
			return false
		}

		mu.Lock()
		sid := sessionID
		mu.Unlock()
		if sid == "" {
			if alreadyExited {
				startRestartFailure("resume_session_missing", "Coordinator injection arrived before the harness reported a resumable session ID.", "Ensure the harness emits a session start event before becoming idle or exiting.")
				forceBuildResult = true
			}
			return false
		}

		message := pendingMessages[0]
		pendingMessages = pendingMessages[1:]
		restarting = true
		runReadyForRestart = false
		_ = emitter.EmitProgress("Coordinator injection received; restarting harness session.")

		if !alreadyExited {
			_ = currentRun.proc.GracefulStop(spec.GraceSec)
			<-currentRun.streamDone
			<-currentRun.procDone
		} else {
			<-currentRun.streamDone
		}

		resumeArgs := e.adapter.ResumeArgs(sid, message)
		currentGen++
		nextRun, nextStderr, err := startRun(currentGen, resumeArgs)
		if err != nil {
			stderrText := ""
			if nextStderr != nil {
				stderrText = strings.TrimSpace(nextStderr.String())
			}
			errMessage := err.Error()
			if stderrText != "" {
				errMessage += ". stderr: " + stderrText
			}
			restarting = false
			startRestartFailure("resume_start_failed", errMessage, "Check the adapter resume arguments and harness installation.")
			forceBuildResult = true
			return false
		}

		currentRun = nextRun
		currentStderr = nextStderr
		streamScanErr = nil
		restarting = false
		mu.Lock()
		lastActivity = time.Now()
		frozenWarned = false
		mu.Unlock()
		updateActivity("resumed session")
		return true
	}

	currentRun, currentStderr, err = startRun(currentGen, args)
	if err != nil {
		return buildFailureResult(
			spec, metadata, startTime, emitter,
			"process_killed",
			err.Error(),
			"Check that the binary is installed and accessible.",
		), nil
	}

	watchdogTicker := time.NewTicker(5 * time.Second)
	defer watchdogTicker.Stop()
	inboxTicker := time.NewTicker(250 * time.Millisecond)
	defer inboxTicker.Stop()
	var softTimer, hardTimer <-chan time.Time
	if softTimeout > 0 {
		softTimer = time.After(softTimeout)
	}
	for {
		select {
		case sig := <-signals:
			if sig.runGen != currentGen {
				continue
			}
			processSignal(sig)
			if restartRun(false) {
				continue
			}
			if forceBuildResult {
				goto buildResult
			}

		case procErr = <-currentRun.procDone:
			drainCurrentSignals(currentGen)
			if restartRun(true) {
				continue
			}
			if forceBuildResult {
				goto buildResult
			}
			<-currentRun.streamDone
			goto buildResult

		case <-softTimer:
			softTimedOut = true
			_ = emitter.EmitTimeoutWarning(fmt.Sprintf("Soft timeout reached. Grace period: %ds.", spec.GraceSec))
			_ = inbox.WriteInbox(spec.ArtifactDir, "Soft timeout reached. Wrap up your current work, write any final artifacts to $AGENT_MUX_ARTIFACT_DIR, and return a summary of what you completed and what remains.")
			if gracePeriod > 0 {
				softTimer = nil
				hardTimer = time.After(gracePeriod)
			} else {
				hardTimer = time.After(0)
			}

		case <-hardTimer:
			setTerminal("timed_out")
			_ = currentRun.proc.GracefulStop(5)
			<-currentRun.streamDone
			goto buildResult

		case <-watchdogTicker.C:
			enqueueInboxMessages()
			if restartRun(false) {
				continue
			}
			if forceBuildResult {
				goto buildResult
			}
			mu.Lock()
			silence := int(time.Since(lastActivity).Seconds())
			shouldWarn := silence >= silenceWarn && !frozenWarned
			if shouldWarn {
				frozenWarned = true
			}
			mu.Unlock()
			if silence >= silenceKill && setTerminal("failed") {
				_ = emitter.EmitError("frozen_tool_call", fmt.Sprintf("No harness events for %ds. Likely frozen. Process terminated.", silence))
				_ = currentRun.proc.GracefulStop(5)
				<-currentRun.streamDone
				goto buildResult
			}
			if shouldWarn {
				_ = emitter.EmitFrozenWarning(silence, fmt.Sprintf("No harness events for %ds.", silence))
			}

		case <-inboxTicker.C:
			enqueueInboxMessages()
			if restartRun(false) {
				continue
			}
			if forceBuildResult {
				goto buildResult
			}

		case <-ctx.Done():
			if setTerminal("interrupted") {
				_ = emitter.EmitError("interrupted", "Dispatch interrupted by caller cancellation.")
				_ = currentRun.proc.GracefulStop(5)
				<-currentRun.streamDone
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
	if response == "" {
		response = lastProgressText
	}
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
		if dispatchErr != nil {
			return finalizeFailed(spec, emitter, act, metadata, durationMS, dispatchErr), nil
		}
		return finalizeFailed(spec, emitter, act, metadata, durationMS, failureFromEventOrProcess(errEvt, currentRun.proc.ExitCode(), currentStderr.String(), false)), nil

	case "interrupted":
		return finalizeFailed(spec, emitter, act, metadata, durationMS, dispatch.NewDispatchError("interrupted", "Dispatch interrupted by caller cancellation.", "")), nil

	default:
		if dispatchErr != nil {
			return finalizeFailed(spec, emitter, act, metadata, durationMS, dispatchErr), nil
		}
		if softTimedOut {
			return finalizeCompleted(spec, emitter, response, act, metadata, durationMS), nil
		}

		if streamScanErr != nil && procErr == nil {
			return finalizeFailed(spec, emitter, act, metadata, durationMS, dispatch.NewDispatchError("output_parse_error", fmt.Sprintf("Read harness event stream: %v", streamScanErr), "")), nil
		}

		if procErr != nil {
			return finalizeFailed(spec, emitter, act, metadata, durationMS, failureFromEventOrProcess(errEvt, currentRun.proc.ExitCode(), currentStderr.String(), true)), nil
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
