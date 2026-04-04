package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/event"
	"github.com/buildoak/agent-mux/internal/hooks"
	"github.com/buildoak/agent-mux/internal/steer"
	"github.com/buildoak/agent-mux/internal/supervisor"
	"github.com/buildoak/agent-mux/internal/types"
)

type LoopEngine struct {
	adapter     types.HarnessAdapter
	eventWriter io.Writer
	verbose     bool
	streamMode  event.StreamMode
	hookEval    *hooks.Evaluator
	annotations types.DispatchAnnotations
}

type runHandle struct {
	proc       *supervisor.Process
	stdout     io.ReadCloser
	stdinPipe  io.WriteCloser
	streamDone chan struct{}
	procDone   chan error
}

type loopSignalKind int

const (
	loopSignalEvent loopSignalKind = iota
	loopSignalInbox
	loopSignalSoftSteer
	loopSignalParseError
	loopSignalScanError
)

type loopSignal struct {
	kind    loopSignalKind
	runGen  uint64
	event   *types.HarnessEvent
	message string
	steer   adapter.CodexSoftSteerEnvelope
	err     error
}

type softStdinBridge struct {
	path          string
	readFile      *os.File
	keepaliveFile *os.File
}

func (e *LoopEngine) scanHarnessOutput(stdout io.Reader, runGen uint64, artifactDir string, signals chan<- loopSignal) {
	const scanBufMax = 4 * 1024 * 1024 // 4MB — large tool outputs (base64 images, grep on big dirs)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, scanBufMax), scanBufMax)
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

		if steer.HasMessages(artifactDir) {
			messages, err := steer.ReadInbox(artifactDir)
			if err != nil {
				signals <- loopSignal{kind: loopSignalScanError, runGen: runGen, err: fmt.Errorf("read coordinator inbox: %w", err)}
				continue
			}
			for _, msg := range messages {
				signals <- loopSignal{kind: loopSignalInbox, runGen: runGen, message: msg.Message}
			}
		}
	}
	if err := scanner.Err(); err != nil && !isIgnorableStreamScanErr(err) {
		if errors.Is(err, bufio.ErrTooLong) {
			signals <- loopSignal{
				kind:   loopSignalScanError,
				runGen: runGen,
				err:    fmt.Errorf("harness output line exceeded %dMB buffer limit — a tool likely produced oversized output: %w", scanBufMax/(1024*1024), err),
			}
		} else {
			signals <- loopSignal{kind: loopSignalScanError, runGen: runGen, err: err}
		}
	}
}

func isIgnorableStreamScanErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "file already closed")
}

func NewLoopEngine(adapter types.HarnessAdapter, eventWriter io.Writer, hookEval *hooks.Evaluator) *LoopEngine {
	return &LoopEngine{
		adapter:     adapter,
		eventWriter: eventWriter,
		hookEval:    hookEval,
	}
}

func (e *LoopEngine) SetAnnotations(annotations types.DispatchAnnotations) {
	e.annotations = annotations
}

func (e *LoopEngine) SetVerbose(v bool) {
	e.verbose = v
}

func (e *LoopEngine) SetStreamMode(m event.StreamMode) {
	e.streamMode = m
}

func (e *LoopEngine) Dispatch(ctx context.Context, spec *types.DispatchSpec) (*types.DispatchResult, error) {
	startTime := time.Now()
	if spec.MaxDepth > 0 && spec.Depth >= spec.MaxDepth {
		metadata := &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: e.annotations.Profile, Skills: append([]string(nil), e.annotations.Skills...), Tokens: &types.TokenUsage{}}
		return buildFailureResult(spec, e.annotations, metadata, startTime, nil, "max_depth_exceeded", fmt.Sprintf("Max dispatch depth %d reached. Complete work directly.", spec.MaxDepth), ""), nil
	}
	dispatchSpec := *spec
	dispatchSpec.Prompt = dispatch.WithPromptPreamble(dispatchSpec.Prompt, &dispatchSpec)

	metadata := &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: e.annotations.Profile, Skills: append([]string(nil), e.annotations.Skills...), Tokens: &types.TokenUsage{}}
	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return buildFailureResult(spec, e.annotations, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err), "Choose a writable --artifact-dir path."), nil
	}
	if err := dispatch.WritePersistentMeta(spec, e.annotations); err != nil {
		return buildFailureResult(spec, e.annotations, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Write persistent dispatch metadata for %q: %v", spec.DispatchID, err), "Ensure ~/.agent-mux/dispatches is writable."), nil
	}
	if err := dispatch.WriteDispatchRef(spec.ArtifactDir, spec.DispatchID); err != nil {
		return buildFailureResult(spec, e.annotations, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Write dispatch reference in %q: %v", spec.ArtifactDir, err), "Ensure the artifact directory is writable."), nil
	}
	inboxCreateErr := steer.CreateInbox(spec.ArtifactDir)
	if inboxCreateErr != nil {
		if e.verbose && e.eventWriter != nil {
			fmt.Fprintf(e.eventWriter, "[engine] create inbox: %v\n", inboxCreateErr)
		}
	}
	eventLogPath := filepath.Join(spec.ArtifactDir, "events.jsonl")
	emitter, err := event.NewEmitter(spec.DispatchID, e.eventWriter, eventLogPath)
	if err != nil {
		return buildFailureResult(spec, e.annotations, metadata, startTime, nil, "artifact_dir_unwritable", fmt.Sprintf("Create event log %q: %v", eventLogPath, err), "Ensure the artifact directory is writable."), nil
	}
	defer emitter.Close()
	emitter.SetStreamMode(e.streamMode)
	if inboxCreateErr != nil {
		_ = emitter.Emit(event.Event{
			Type:      "warning",
			ErrorCode: "coordinator_inbox_create_failed",
			Message:   fmt.Sprintf("Create coordinator inbox failed: %v", inboxCreateErr),
		})
	}

	_ = emitter.EmitDispatchStart(spec)
	if _, ok := e.adapter.(*adapter.CodexAdapter); ok {
		if badVal, valid := adapter.ValidateCodexSandbox(&dispatchSpec); !valid {
			return buildFailureResult(
				spec, e.annotations, metadata, startTime, emitter,
				"invalid_args",
				fmt.Sprintf("Invalid sandbox value %q for codex engine.", badVal),
				"Valid sandbox values: danger-full-access, workspace-write, read-only. Example: agent-mux -e codex --sandbox workspace-write --cwd /repo \"<prompt>\".",
			), nil
		}
	}
	args := e.adapter.BuildArgs(&dispatchSpec)
	binary := e.adapter.Binary()
	if _, err := exec.LookPath(binary); err != nil {
		return buildFailureResult(
			spec, e.annotations, metadata, startTime, emitter,
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
	adapterEnv, err := e.adapter.EnvVars(&dispatchSpec)
	if err != nil {
		return buildFailureResult(
			spec, e.annotations, metadata, startTime, emitter,
			"artifact_dir_unwritable",
			fmt.Sprintf("Set up %s adapter environment: %v", binary, err),
			"Ensure the artifact directory is writable before retrying.",
		), nil
	}
	env = append(env, adapterEnv...)
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
		mu                sync.Mutex
		lastResponse      string
		lastProgressText  string
		sessionID         string
		totalTokens       *types.TokenUsage
		turnCount         int
		lastError         *types.HarnessEvent
		lastActivity      = time.Now()
		frozenWarned      bool
		terminalState     string // "", "timed_out", "failed", "interrupted"
		softTimedOut      bool
		streamScanErr     error
		dispatchErr       *types.DispatchError
		sawResponse       bool
		toolsUsedCount    int
		filesChangedCount int
	)
	parseErrorCount := 0

	setTerminal := func(state string) bool {
		if terminalState != "" {
			return false
		}
		terminalState = state
		return true
	}

	silenceWarn := intEngineOpt(spec, "silence_warn_seconds", 90)
	silenceKill := intEngineOpt(spec, "silence_kill_seconds", 180)
	longCommandSilence := intEngineOpt(spec, "long_command_silence_seconds", 540)
	maxSteerWait := intEngineOpt(spec, "max_steer_wait_seconds", 120)
	longCommandExtraPrefixes := parseLongCommandPrefixes(spec)
	var (
		activeCommand       string
		commandStartTime    time.Time
		longCommandExtended bool
		steerPendingSince   time.Time // when pendingMessages first became non-empty
	)
	_ = commandStartTime // used for future diagnostics
	stopHeartbeat, updateActivity := emitter.HeartbeatTicker(intEngineOpt(spec, "heartbeat_interval_sec", 15))
	defer stopHeartbeat()
	signals := make(chan loopSignal, 512)
	var procErr error
	forceBuildResult := false
	runReadyForRestart := false
	pendingMessages := make([]string, 0)
	pendingSoftSteer := make([]adapter.CodexSoftSteerEnvelope, 0)
	restarting := false
	var currentGen uint64 = 1
	var currentRun *runHandle
	var currentStderr *strings.Builder
	var stdinBridge *softStdinBridge
	stdinPipeReady := false

	handleHarnessEvent := func(evt *types.HarnessEvent) {
		var observedSessionID string
		statusLastActivity := ""
		statusToolsUsed := 0
		statusFilesChanged := 0
		mu.Lock()
		lastActivity = time.Now()
		frozenWarned = false
		runReadyForRestart = true
		switch evt.Kind {
		case types.EventSessionStart:
			sessionID = evt.SessionID
			observedSessionID = evt.SessionID
			updateActivity("session started")
			statusLastActivity = "session started"

		case types.EventToolStart:
			if evt.Tool != "" {
				activity.ToolCalls = append(activity.ToolCalls, evt.Tool)
				toolsUsedCount++
			}
			if evt.Command != "" {
				activeCommand = evt.Command
				commandStartTime = time.Now()
				longCommandExtended = false
				_ = emitter.EmitToolStart(evt.Tool, evt.Command)
				updateActivity(fmt.Sprintf("running: %s", truncate(evt.Command, 60)))
			} else {
				_ = emitter.EmitToolStart(evt.Tool, "")
				updateActivity(fmt.Sprintf("tool: %s", evt.Tool))
			}

		case types.EventToolEnd:
			activeCommand = ""
			commandStartTime = time.Time{}
			longCommandExtended = false
			_ = emitter.EmitToolEnd(evt.Tool, evt.DurationMS)

		case types.EventFileWrite:
			activity.FilesChanged = appendUnique(activity.FilesChanged, evt.FilePath)
			filesChangedCount++
			_ = emitter.EmitFileWrite(evt.FilePath)
			updateActivity(fmt.Sprintf("wrote: %s", evt.FilePath))

		case types.EventFileRead:
			activity.FilesRead = appendUnique(activity.FilesRead, evt.FilePath)
			_ = emitter.EmitFileRead(evt.FilePath)

		case types.EventCommandRun:
			activity.ToolCalls = appendUnique(activity.ToolCalls, evt.Tool)
			activity.CommandsRun = appendUnique(activity.CommandsRun, evt.Command)
			if evt.Command != "" {
				activeCommand = evt.Command
				commandStartTime = time.Now()
				longCommandExtended = false
			}
			_ = emitter.EmitCommandRun(evt.Command)
			updateActivity(fmt.Sprintf("running: %s", truncate(evt.Command, 60)))

		case types.EventProgress:
			if evt.Text != "" {
				lastProgressText += evt.Text
			}
			_ = emitter.EmitProgress(truncate(evt.Text, 200))

		case types.EventResponse:
			lastResponse = evt.Text
			sawResponse = true
			// A response implies any in-flight tool has completed.
			activeCommand = ""
			commandStartTime = time.Time{}
			longCommandExtended = false
			if evt.Tokens != nil {
				totalTokens = evt.Tokens
			}
			if evt.Turns > 0 {
				turnCount = evt.Turns
			}
			if evt.SessionID != "" {
				sessionID = evt.SessionID
				observedSessionID = evt.SessionID
			}
			updateActivity("received response")
			statusLastActivity = "received response"

		case types.EventTurnComplete:
			turnCount++
			// A turn completing implies any in-flight tool has also finished.
			activeCommand = ""
			commandStartTime = time.Time{}
			longCommandExtended = false
			if evt.Tokens != nil {
				totalTokens = evt.Tokens
			}
			updateActivity("turn completed")
			statusLastActivity = "turn completed"

		case types.EventTurnFailed:
			lastError = evt
			updateActivity("turn failed")
			statusLastActivity = "turn failed"

		case types.EventError:
			lastError = evt
			_ = emitter.EmitError(evt.ErrorCode, evt.Text)
			updateActivity(fmt.Sprintf("error: %s", evt.ErrorCode))
			statusLastActivity = fmt.Sprintf("error: %s", evt.ErrorCode)

		case types.EventRawPassthrough:
		}
		statusToolsUsed = toolsUsedCount
		statusFilesChanged = filesChangedCount
		mu.Unlock()
		if observedSessionID != "" {
			persistObservedSession(spec.ArtifactDir, spec.DispatchID, startTime, observedSessionID, statusLastActivity, statusToolsUsed, statusFilesChanged, stdinPipeReady)
		}
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
		stdinPipe, err := cmd.StdinPipe()
		if err != nil {
			_ = stdout.Close()
			return nil, nil, fmt.Errorf("set up stdin pipe for %s: %w", runBinary, err)
		}
		var stderrBuf strings.Builder
		cmd.Stderr = &stderrBuf
		if err := proc.Start(); err != nil {
			_ = stdout.Close()
			_ = stdinPipe.Close()
			return nil, &stderrBuf, fmt.Errorf("failed to start %s: %w", runBinary, err)
		}

		run := &runHandle{
			proc:       proc,
			stdout:     stdout,
			stdinPipe:  stdinPipe,
			streamDone: make(chan struct{}),
			procDone:   make(chan error, 1),
		}
		// Best-effort orphan guard: kill child process group if coordinator dies.
		go supervisor.WatchParentDeath(proc.Cmd().Process.Pid)
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
		if !steer.HasMessages(spec.ArtifactDir) {
			return
		}
		messages, err := steer.ReadInbox(spec.ArtifactDir)
		if err != nil {
			_ = emitter.EmitError("coordinator_inbox_read_failed", fmt.Sprintf("Read coordinator inbox: %v", err))
			return
		}
		for _, msg := range messages {
			_ = emitter.Emit(event.Event{Type: "coordinator_inject", Message: msg.Message})
			if len(pendingMessages) == 0 {
				steerPendingSince = time.Now()
			}
			pendingMessages = append(pendingMessages, msg.Message)
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
		if currentRun.stdinPipe != nil {
			_ = currentRun.stdinPipe.Close()
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
			if len(pendingMessages) == 0 {
				steerPendingSince = time.Now()
			}
			pendingMessages = append(pendingMessages, sig.message)

		case loopSignalSoftSteer:
			formatted := string(adapter.FormatSoftSteerInput(sig.steer.Action, sig.steer.Message))
			_ = emitter.Emit(event.Event{Type: "coordinator_inject", Message: strings.TrimRight(formatted, "\n")})
			if len(pendingSoftSteer) == 0 {
				steerPendingSince = time.Now()
			}
			pendingSoftSteer = append(pendingSoftSteer, sig.steer)

		case loopSignalParseError:
			parseErrorCount++
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
				if sig.runGen != 0 && sig.runGen != runGen {
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

		// If a tool is currently executing and the process hasn't exited,
		// defer the restart until the tool completes (next EventToolEnd clears activeCommand).
		// The event loop retries restartRun() on every event and inbox tick,
		// so when EventToolEnd fires, the next iteration will proceed.
		mu.Lock()
		cmd := activeCommand
		mu.Unlock()
		if cmd != "" && !alreadyExited {
			exceeded := !steerPendingSince.IsZero() && time.Since(steerPendingSince).Seconds() >= float64(maxSteerWait)
			if exceeded {
				_ = emitter.Emit(event.Event{
					Type:    "steer_forced",
					Message: fmt.Sprintf("max_steer_wait_exceeded: waited %ds, force-proceeding with restart", int(time.Since(steerPendingSince).Seconds())),
					Command: cmd,
				})
			} else {
				_ = emitter.Emit(event.Event{
					Type:    "steer_deferred",
					Message: fmt.Sprintf("tool_active: deferring restart while %q is running", cmd),
					Command: cmd,
				})
				return false
			}
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
		if len(pendingMessages) == 0 {
			steerPendingSince = time.Time{}
		}
		restarting = true
		runReadyForRestart = false

		// Differentiate nudge vs redirect via inbox prefix.
		message = formatSteerMessage(message)

		_ = emitter.EmitProgress("Coordinator injection received; restarting harness session.")

		if !alreadyExited {
			_ = currentRun.proc.GracefulStop(spec.GraceSec)
			<-currentRun.streamDone
			<-currentRun.procDone
		} else {
			<-currentRun.streamDone
		}

		resumeArgs := e.adapter.ResumeArgs(spec, sid, message)
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

	deliverSoftSteer := func() bool {
		if terminalState != "" || len(pendingSoftSteer) == 0 || currentRun == nil || currentRun.stdinPipe == nil {
			return false
		}

		mu.Lock()
		cmd := activeCommand
		mu.Unlock()
		if cmd != "" {
			exceeded := !steerPendingSince.IsZero() && time.Since(steerPendingSince).Seconds() >= float64(maxSteerWait)
			if !exceeded {
				_ = emitter.Emit(event.Event{
					Type:    "steer_deferred",
					Message: fmt.Sprintf("tool_active: deferring stdin steer while %q is running", cmd),
					Command: cmd,
				})
				return false
			}
			_ = emitter.Emit(event.Event{
				Type:    "steer_forced",
				Message: fmt.Sprintf("max_steer_wait_exceeded: waited %ds, force-proceeding with stdin steer", int(time.Since(steerPendingSince).Seconds())),
				Command: cmd,
			})
		}

		req := pendingSoftSteer[0]
		if err := deliverSoftSteer(currentRun, req); err != nil {
			_ = emitter.EmitError("stdin_steer_failed", fmt.Sprintf("Deliver stdin steer: %v", err))
			return false
		}
		pendingSoftSteer = pendingSoftSteer[1:]
		if len(pendingSoftSteer) == 0 && len(pendingMessages) == 0 {
			steerPendingSince = time.Time{}
		}
		return true
	}

	closeSoftBridge := func() {
		if stdinBridge == nil {
			return
		}
		_ = stdinBridge.close()
		stdinBridge = nil
		stdinPipeReady = false
	}

	if bridge, err := startSoftStdinBridge(spec.ArtifactDir, signals); err == nil {
		stdinBridge = bridge
		stdinPipeReady = true
	} else if !errors.Is(err, steer.ErrUnsupported) {
		return buildFailureResult(
			spec, e.annotations, metadata, startTime, emitter,
			"startup_failed",
			fmt.Sprintf("start stdin fifo bridge: %v", err),
			"Check that the artifact directory is writable and supports FIFOs.",
		), nil
	}

	currentRun, currentStderr, err = startRun(currentGen, args)
	if err != nil {
		closeSoftBridge()
		return buildFailureResult(
			spec, e.annotations, metadata, startTime, emitter,
			"startup_failed",
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
			if sig.runGen != 0 && sig.runGen != currentGen {
				continue
			}
			processSignal(sig)
			if deliverSoftSteer() {
				continue
			}
			if restartRun(false) {
				continue
			}
			if forceBuildResult {
				goto buildResult
			}

		case procErr = <-currentRun.procDone:
			drainCurrentSignals(currentGen)
			if deliverSoftSteer() {
				continue
			}
			if forceBuildResult {
				closeSoftBridge()
				goto buildResult
			}
			<-currentRun.streamDone
			// FM-7: The scanner goroutine may have sent final signals (e.g. the
			// last EventResponse) AFTER drainCurrentSignals returned but before
			// streamDone closed.  Now that streamDone is closed the scanner is
			// guaranteed finished, so drain one more time to capture every signal.
			drainCurrentSignals(currentGen)
			enqueueInboxMessages()
			if len(pendingMessages) > 0 && restartRun(true) {
				continue
			}
			closeSoftBridge()
			goto buildResult

		case <-softTimer:
			softTimedOut = true
			_ = emitter.EmitTimeoutWarning(fmt.Sprintf("Soft timeout reached. Grace period: %ds.", spec.GraceSec))
			_ = steer.WriteInbox(spec.ArtifactDir, "Soft timeout reached. Wrap up your current work, write any final artifacts to $AGENT_MUX_ARTIFACT_DIR, and return a summary of what you completed and what remains.")
			if gracePeriod > 0 {
				softTimer = nil
				hardTimer = time.After(gracePeriod)
			} else {
				hardTimer = time.After(0)
			}

		case <-hardTimer:
			setTerminal("timed_out")
			// FM-4: Use configured grace period instead of hardcoded 5s.
			// Floor at 10s so workers have time to flush final result events.
			hardGrace := spec.GraceSec
			if hardGrace < 10 {
				hardGrace = 10
			}
			_ = currentRun.proc.GracefulStop(hardGrace)
			<-currentRun.streamDone
			closeSoftBridge()
			goto buildResult

		case <-watchdogTicker.C:
			enqueueInboxMessages()
			if deliverSoftSteer() {
				continue
			}
			if restartRun(false) {
				continue
			}
			if forceBuildResult {
				goto buildResult
			}
			// Read control.json for mid-flight steering signals.
			if cf := readControlFile(spec.ArtifactDir); cf != nil {
				if cf.Abort && setTerminal("failed") {
					_ = emitter.EmitError("abort_requested", "Abort requested via control file.")
					dispatchErr = dispatch.NewDispatchError("abort_requested", "Abort requested via ax steer abort.", "")
					_ = currentRun.proc.GracefulStop(5)
					<-currentRun.streamDone
					goto buildResult
				}
				if cf.ExtendKillSeconds > 0 && time.Since(cf.UpdatedAt).Seconds() < 120 {
					if cf.ExtendKillSeconds > silenceKill {
						silenceKill = cf.ExtendKillSeconds
					}
				}
			}

			mu.Lock()
			silence := int(time.Since(lastActivity).Seconds())
			effectiveKill := silenceKill
			if activeCommand != "" && isLongRunningCommand(activeCommand, longCommandExtraPrefixes) {
				effectiveKill = longCommandSilence
				if !longCommandExtended {
					longCommandExtended = true
					_ = emitter.EmitLongCommandDetected(activeCommand, longCommandSilence)
				}
			}
			shouldWarn := silence >= silenceWarn && !frozenWarned
			if shouldWarn {
				frozenWarned = true
			}
			statusActivity := fmt.Sprintf("last activity %ds ago", silence)
			if activeCommand != "" {
				statusActivity = fmt.Sprintf("running: %s", truncate(activeCommand, 60))
			}
			statusToolsUsed := toolsUsedCount
			statusFilesChanged := filesChangedCount
			mu.Unlock()
			// Atomic status.json write for pull-based status.
			_ = writeRunningStatus(spec.ArtifactDir, spec.DispatchID, sessionID, startTime, statusActivity, statusToolsUsed, statusFilesChanged, stdinPipeReady)
			if silence >= effectiveKill && setTerminal("failed") {
				_ = emitter.EmitError("frozen_killed", fmt.Sprintf("No harness events for %ds. Likely frozen. Process terminated.", silence))
				dispatchErr = dispatch.NewDispatchError("frozen_killed", fmt.Sprintf("No harness events for %ds. Likely frozen. Process terminated.", silence), "")
				_ = currentRun.proc.GracefulStop(5)
				<-currentRun.streamDone
				closeSoftBridge()
				goto buildResult
			}
			if shouldWarn {
				_ = emitter.EmitFrozenWarning(silence, fmt.Sprintf("No harness events for %ds.", silence))
				if nudge := e.adapter.StdinNudge(); nudge != nil && currentRun != nil && currentRun.stdinPipe != nil {
					if _, err := currentRun.stdinPipe.Write(nudge); err == nil {
						_ = emitter.EmitInfo("stdin_nudge", "Sent stdin nudge to frozen process")
					}
				}
			}

		case <-inboxTicker.C:
			enqueueInboxMessages()
			if deliverSoftSteer() {
				continue
			}
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
				closeSoftBridge()
				goto buildResult
			}
		}
	}

buildResult:
	closeSoftBridge()
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
	haveFinalResponse := sawResponse
	mu.Unlock()

	if tokens == nil {
		tokens = &types.TokenUsage{}
	}
	metadata = &types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: e.annotations.Profile, Skills: append([]string(nil), e.annotations.Skills...), Tokens: tokens, Turns: turns}
	metadata.SessionID = sid

	switch state {
	case "timed_out":
		return finalizeTimedOut(spec, e.annotations, emitter, response, act, metadata, durationMS), nil

	case "failed":
		if dispatchErr != nil {
			return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, dispatchErr), nil
		}
		return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, failureFromEventOrProcess(errEvt, currentRun.proc.ExitCode(), currentStderr.String(), false)), nil

	case "interrupted":
		return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, dispatch.NewDispatchError("interrupted", "Dispatch interrupted by caller cancellation.", "")), nil

	default:
		if dispatchErr != nil {
			return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, dispatchErr), nil
		}
		if parseErrorCount > 0 && missingFinalResponse(response, haveFinalResponse) {
			return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, dispatch.NewDispatchError(
				"parse_error",
				fmt.Sprintf("Harness output contained %d parse error(s) and no final response could be trusted.", parseErrorCount),
				"",
			)), nil
		}
		if softTimedOut {
			return finalizeCompleted(spec, e.annotations, emitter, response, act, metadata, durationMS), nil
		}

		if streamScanErr != nil && procErr == nil {
			return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, dispatch.NewDispatchError("output_parse_error", fmt.Sprintf("Read harness event stream: %v", streamScanErr), "")), nil
		}

		if procErr != nil {
			return finalizeFailed(spec, e.annotations, emitter, response, act, metadata, durationMS, failureFromEventOrProcess(errEvt, currentRun.proc.ExitCode(), currentStderr.String(), true)), nil
		}
		return finalizeCompleted(spec, e.annotations, emitter, response, act, metadata, durationMS), nil
	}
}

func buildFailureResult(spec *types.DispatchSpec, annotations types.DispatchAnnotations, metadata *types.DispatchMetadata, startTime time.Time, emitter *event.Emitter, code, message, suggestion string) *types.DispatchResult {
	durationMS := time.Since(startTime).Milliseconds()
	if emitter != nil {
		_ = emitter.EmitDispatchEnd("failed", durationMS)
	}
	result := dispatch.BuildFailedResult(spec, "", dispatch.NewDispatchError(code, message, suggestion), emptyActivity(), metadata, durationMS)
	persistDispatchRecord(spec, annotations, result, "", emitter)
	return result
}

func emptyActivity() *types.DispatchActivity {
	return &types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}}
}

func finalizeCompleted(spec *types.DispatchSpec, annotations types.DispatchAnnotations, emitter *event.Emitter, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	result := dispatch.BuildCompletedResult(spec, response, activity, metadata, durationMS)
	// FM-15: Write status.json AFTER persistDispatchRecord so pollers
	// never see "completed" before the store record exists.
	persistDispatchRecord(spec, annotations, result, response, emitter)
	_ = dispatch.WriteStatusJSON(spec.ArtifactDir, dispatch.LiveStatus{
		State:          "completed",
		ElapsedS:       int(durationMS / 1000),
		LastActivity:   "done",
		ToolsUsed:      len(activity.ToolCalls),
		FilesChanged:   len(activity.FilesChanged),
		StdinPipeReady: false,
		DispatchID:     spec.DispatchID,
	})
	emitResponseTruncated(emitter, result)
	if emitter != nil {
		_ = emitter.EmitDispatchEnd("completed", durationMS)
	}
	return result
}

func finalizeTimedOut(spec *types.DispatchSpec, annotations types.DispatchAnnotations, emitter *event.Emitter, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	result := dispatch.BuildTimedOutResult(spec, response, fmt.Sprintf("Soft timeout at %ds, hard kill after %ds grace.", spec.TimeoutSec, spec.GraceSec), activity, metadata, durationMS)
	// FM-15: Write status.json AFTER persistDispatchRecord.
	persistDispatchRecord(spec, annotations, result, response, emitter)
	_ = dispatch.WriteStatusJSON(spec.ArtifactDir, dispatch.LiveStatus{
		State:          "timed_out",
		ElapsedS:       int(durationMS / 1000),
		LastActivity:   "timed_out",
		ToolsUsed:      len(activity.ToolCalls),
		FilesChanged:   len(activity.FilesChanged),
		StdinPipeReady: false,
		DispatchID:     spec.DispatchID,
	})
	emitResponseTruncated(emitter, result)
	if emitter != nil {
		_ = emitter.EmitDispatchEnd("timed_out", durationMS)
	}
	return result
}

func finalizeFailed(spec *types.DispatchSpec, annotations types.DispatchAnnotations, emitter *event.Emitter, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64, dispErr *types.DispatchError) *types.DispatchResult {
	// FM-9: Pass accumulated response to BuildFailedResult so partial work is preserved.
	result := dispatch.BuildFailedResult(spec, response, dispErr, activity, metadata, durationMS)
	dispErr.PartialArtifacts = result.Artifacts
	// FM-15: Write status.json AFTER persistDispatchRecord.
	persistDispatchRecord(spec, annotations, result, response, emitter)
	_ = dispatch.WriteStatusJSON(spec.ArtifactDir, dispatch.LiveStatus{
		State:          "failed",
		ElapsedS:       int(durationMS / 1000),
		LastActivity:   "failed",
		ToolsUsed:      len(activity.ToolCalls),
		FilesChanged:   len(activity.FilesChanged),
		StdinPipeReady: false,
		DispatchID:     spec.DispatchID,
	})
	emitResponseTruncated(emitter, result)
	if emitter != nil {
		_ = emitter.EmitDispatchEnd("failed", durationMS)
	}
	return result
}

func emitResponseTruncated(emitter *event.Emitter, result *types.DispatchResult) {
	if emitter == nil || result == nil || !result.ResponseTruncated || result.FullOutputPath == nil {
		return
	}
	_ = emitter.EmitResponseTruncated(*result.FullOutputPath)
}

func persistDispatchRecord(spec *types.DispatchSpec, annotations types.DispatchAnnotations, result *types.DispatchResult, responseText string, emitter *event.Emitter) {
	if spec == nil || result == nil {
		return
	}

	startedAt, endedAt := dispatchWindow(spec.ArtifactDir, result.DurationMS)
	if err := dispatch.WritePersistentResult(spec, annotations, result, responseText, startedAt, endedAt); err != nil {
		if emitter != nil {
			_ = emitter.Emit(event.Event{
				Type:      "warning",
				ErrorCode: "persist_result_failed",
				Message:   fmt.Sprintf("Persist dispatch result: %v", err),
			})
		}
	}
}

func dispatchWindow(artifactDir string, durationMS int64) (string, string) {
	meta, err := dispatch.ReadDispatchMeta(artifactDir)
	if err == nil && meta != nil {
		startedAt := strings.TrimSpace(meta.StartedAt)
		endedAt := strings.TrimSpace(meta.EndedAt)
		if startedAt != "" || endedAt != "" {
			if startedAt == "" && endedAt != "" {
				startedAt = backfillStartedAt(endedAt, durationMS)
			}
			if endedAt == "" && startedAt != "" {
				if started, parseErr := time.Parse(time.RFC3339, startedAt); parseErr == nil {
					endedAt = started.Add(time.Duration(durationMS) * time.Millisecond).Format(time.RFC3339)
				}
			}
			return startedAt, endedAt
		}
	}

	ended := time.Now().UTC()
	return ended.Add(-time.Duration(durationMS) * time.Millisecond).Format(time.RFC3339), ended.Format(time.RFC3339)
}

func backfillStartedAt(endedAt string, durationMS int64) string {
	ended, err := time.Parse(time.RFC3339, endedAt)
	if err != nil {
		return ""
	}
	return ended.Add(-time.Duration(durationMS) * time.Millisecond).Format(time.RFC3339)
}

func metadataEngine(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return result.Metadata.Engine
}

func metadataSessionID(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return result.Metadata.SessionID
}

func metadataModel(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return result.Metadata.Model
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
	code := "process_killed"
	if exitCode == 137 || exitCode == 143 {
		code = "signal_killed"
	}
	return dispatch.NewDispatchError(code, base, "Check engine logs.")
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

func missingFinalResponse(response string, sawResponse bool) bool {
	if !sawResponse {
		return true
	}
	return strings.TrimSpace(response) == ""
}

// longCommandPrefixes lists command prefixes known to produce long silence
// (build tools, package managers, compilers). The watchdog extends its kill
// threshold when the active command matches one of these.
var longCommandPrefixes = []string{
	"cargo",
	"make",
	"nvcc",
	"go build",
	"go test",
	"cmake",
	"npm install",
	"npm run build",
	"pip install",
	"docker build",
	"rustc",
	"gcc",
	"g++",
	"clang",
}

// isLongRunningCommand returns true if cmd starts with a known long-running
// build/install prefix.
func isLongRunningCommand(cmd string, extraPrefixes []string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return false
	}
	for _, prefix := range longCommandPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	for _, prefix := range extraPrefixes {
		if prefix != "" && strings.HasPrefix(trimmed, strings.TrimSpace(prefix)) {
			return true
		}
	}
	return false
}

// parseLongCommandPrefixes splits a comma-separated engine opt string into
// a slice of prefix strings, trimming whitespace from each entry.
func parseLongCommandPrefixes(spec *types.DispatchSpec) []string {
	if spec == nil || spec.EngineOpts == nil {
		return nil
	}
	raw, ok := spec.EngineOpts["long_command_prefixes"].(string)
	if !ok || raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// controlFile is the steering control structure read from control.json.
type controlFile struct {
	Abort             bool      `json:"abort,omitempty"`
	ExtendKillSeconds int       `json:"extend_kill_seconds,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// readControlFile reads control.json from artifact dir. Returns nil on miss (no alloc).
func readControlFile(artifactDir string) *controlFile {
	data, err := os.ReadFile(filepath.Join(artifactDir, "control.json"))
	if err != nil {
		return nil
	}
	var cf controlFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil
	}
	return &cf
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
	case string:
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// formatSteerMessage detects [NUDGE] or [REDIRECT] prefixes on inbox messages
// and reformats them with appropriate framing for the resume cycle.
// Messages without a recognized prefix pass through unchanged.
func formatSteerMessage(message string) string {
	if strings.HasPrefix(message, "[REDIRECT] ") {
		body := strings.TrimPrefix(message, "[REDIRECT] ")
		return strings.TrimRight(string(adapter.FormatSoftSteerInput("redirect", body)), "\n")
	}
	if strings.HasPrefix(message, "[NUDGE] ") {
		body := strings.TrimPrefix(message, "[NUDGE] ")
		return strings.TrimRight(string(adapter.FormatSoftSteerInput("nudge", body)), "\n")
	}
	return message
}

func startSoftStdinBridge(artifactDir string, signals chan<- loopSignal) (*softStdinBridge, error) {
	path := steer.Path(artifactDir)
	if err := steer.Create(path); err != nil {
		return nil, err
	}
	readFile, err := steer.OpenReadNonblock(path)
	if err != nil {
		_ = steer.Remove(path)
		return nil, err
	}
	keepaliveFile, err := steer.OpenWriteNonblock(path)
	if err != nil {
		_ = readFile.Close()
		_ = steer.Remove(path)
		return nil, err
	}
	bridge := &softStdinBridge{
		path:          path,
		readFile:      readFile,
		keepaliveFile: keepaliveFile,
	}
	go scanSoftStdinFIFO(readFile, signals)
	return bridge, nil
}

func (b *softStdinBridge) close() error {
	if b == nil {
		return nil
	}
	var errs []error
	if b.readFile != nil {
		if err := b.readFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if b.keepaliveFile != nil {
		if err := b.keepaliveFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if err := steer.Remove(b.path); err != nil && !errors.Is(err, steer.ErrUnsupported) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func scanSoftStdinFIFO(r io.Reader, signals chan<- loopSignal) {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return
			}
			var pathErr *os.PathError
			if errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.EAGAIN) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if errors.Is(err, syscall.EAGAIN) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if len(line) == 0 {
				signals <- loopSignal{kind: loopSignalScanError, err: err}
				return
			}
		}
		line = bytesTrimSpace(line)
		if len(line) == 0 {
			continue
		}
		req, decodeErr := adapter.DecodeSoftSteerEnvelope(line)
		if decodeErr != nil {
			signals <- loopSignal{kind: loopSignalParseError, err: decodeErr}
			continue
		}
		signals <- loopSignal{kind: loopSignalSoftSteer, steer: req}
	}
}

func deliverSoftSteer(run *runHandle, req adapter.CodexSoftSteerEnvelope) error {
	if run == nil || run.stdinPipe == nil {
		return errors.New("stdin pipe unavailable")
	}
	_, err := run.stdinPipe.Write(adapter.FormatSoftSteerInput(req.Action, req.Message))
	return err
}

func writeRunningStatus(artifactDir, dispatchID, sessionID string, startTime time.Time, lastActivity string, toolsUsed, filesChanged int, stdinPipeReady bool) error {
	return dispatch.WriteStatusJSON(artifactDir, dispatch.LiveStatus{
		State:          "running",
		ElapsedS:       int(time.Since(startTime).Seconds()),
		LastActivity:   lastActivity,
		ToolsUsed:      toolsUsed,
		FilesChanged:   filesChanged,
		StdinPipeReady: stdinPipeReady,
		DispatchID:     dispatchID,
		SessionID:      sessionID,
	})
}

func persistObservedSession(artifactDir, dispatchID string, startTime time.Time, sessionID, lastActivity string, toolsUsed, filesChanged int, stdinPipeReady bool) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = dispatch.UpdateDispatchSessionID(artifactDir, sessionID)
	_ = writeRunningStatus(artifactDir, dispatchID, sessionID, startTime, lastActivity, toolsUsed, filesChanged, stdinPipeReady)
}

func bytesTrimSpace(line []byte) []byte {
	return []byte(strings.TrimSpace(string(line)))
}
