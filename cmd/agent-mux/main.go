package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/hooks"
	"github.com/buildoak/agent-mux/internal/inbox"
	"github.com/buildoak/agent-mux/internal/pipeline"
	"github.com/buildoak/agent-mux/internal/recovery"
	"github.com/buildoak/agent-mux/internal/types"
	"github.com/oklog/ulid/v2"
	"golang.org/x/term"
)

const version = "agent-mux v2.0.0-dev"
const contextFilePromptPreamble = "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting."

type cliCommand string

const (
	commandDispatch cliCommand = "dispatch"
	commandPreview  cliCommand = "preview"
)

type stringSlice []string

func (s *stringSlice) String() string {
	if s == nil {
		return ""
	}
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type cliFlags struct {
	engine, role, coordinator, cwd, model, effort, systemPrompt, systemPromptFile string
	contextFile, artifactDir, salt, config, promptFile, recover                   string
	signal                                                                        string
	permissionMode, sandbox, reasoning                                            string
	output, pipeline                                                              string
	timeout, maxDepth, responseMaxChars, maxTurns                                 int
	full, noFull, noSubdispatch, stdin, version, verbose, yes                     bool
	skills, addDirs                                                               stringSlice
}

type previewResult struct {
	SchemaVersion        int                 `json:"schema_version"`
	Kind                 string              `json:"kind"`
	DispatchSpec         previewDispatchSpec `json:"dispatch_spec"`
	Prompt               previewPrompt       `json:"prompt"`
	Control              previewControl      `json:"control"`
	PromptPreamble       []string            `json:"prompt_preamble"`
	Warnings             []string            `json:"warnings"`
	ConfirmationRequired bool                `json:"confirmation_required"`
}

type previewDispatchSpec struct {
	DispatchID          string   `json:"dispatch_id"`
	Salt                string   `json:"salt,omitempty"`
	TraceToken          string   `json:"trace_token,omitempty"`
	Engine              string   `json:"engine"`
	Model               string   `json:"model,omitempty"`
	Effort              string   `json:"effort,omitempty"`
	Role                string   `json:"role,omitempty"`
	Coordinator         string   `json:"coordinator,omitempty"`
	Pipeline            string   `json:"pipeline,omitempty"`
	Cwd                 string   `json:"cwd"`
	Skills              []string `json:"skills,omitempty"`
	ContextFile         string   `json:"context_file,omitempty"`
	ArtifactDir         string   `json:"artifact_dir"`
	TimeoutSec          int      `json:"timeout_sec,omitempty"`
	GraceSec            int      `json:"grace_sec,omitempty"`
	MaxDepth            int      `json:"max_depth,omitempty"`
	AllowSubdispatch    bool     `json:"allow_subdispatch"`
	ContinuesDispatchID string   `json:"continues_dispatch_id,omitempty"`
	ResponseMaxChars    int      `json:"response_max_chars,omitempty"`
	FullAccess          bool     `json:"full_access"`
}

type previewPrompt struct {
	Excerpt           string `json:"excerpt,omitempty"`
	Chars             int    `json:"chars"`
	Truncated         bool   `json:"truncated"`
	SystemPromptChars int    `json:"system_prompt_chars,omitempty"`
}

type previewControl struct {
	ControlRecord string `json:"control_record"`
	ArtifactDir   string `json:"artifact_dir"`
}

type terminalChecker func(any) bool

const (
	exitCodeCancelled         = 130
	previewPromptExcerptRunes = 280
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithTerminalCheck(args, stdin, stdout, stderr, isTerminalStream)
}

func runWithTerminalCheck(args []string, stdin io.Reader, stdout, stderr io.Writer, isTerminal terminalChecker) int {
	command, args, explicitCommand := splitCommand(args)
	fs, parsed := newFlagSet(stderr)
	err := fs.Parse(normalizeArgs(args))
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	flags := *parsed
	positional := fs.Args()

	if flags.version {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if flags.signal != "" {
		if len(positional) == 0 {
			fmt.Fprintln(stderr, "--signal requires a message as the first positional argument")
			return 2
		}
		msg := positional[0]
		artifactDir, err := recovery.ResolveArtifactDir(flags.signal)
		if err != nil {
			fmt.Fprintf(stderr, "signal: %v\n", err)
			return 1
		}
		if err := inbox.WriteInbox(artifactDir, msg); err != nil {
			fmt.Fprintf(stderr, "signal: %v\n", err)
			return 1
		}
		writeSignalResult(stdout, flags.signal, artifactDir)
		return 0
	}
	if explicitCommand && !flags.stdin && flags.promptFile == "" && len(positional) == 0 {
		fmt.Fprintf(stderr, "missing prompt: provide the first positional arg or --prompt-file\nIf you meant the literal prompt %q, pass it after -- (for example: agent-mux -- %s) or use --prompt-file/--stdin.\n", command, command)
		return 1
	}

	failResult := func(spec *types.DispatchSpec, code, msg, suggestion string) int {
		result := dispatch.BuildFailedResult(
			spec,
			dispatch.NewDispatchError(code, msg, suggestion),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Tokens: &types.TokenUsage{}},
			0,
		)
		if flags.output == "text" {
			writeTextResult(stdout, result)
		} else {
			writeResult(stdout, result)
		}
		return 1
	}

	var spec *types.DispatchSpec
	if flags.stdin {
		spec, err = decodeStdinDispatchSpec(stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		spec, err = buildDispatchSpecE(flags, positional)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})
	if flags.stdin && stdinDispatchFlagsSet(flagsSet) {
		fmt.Fprintf(stderr, "Warning: --stdin mode active; CLI dispatch flags are ignored.\n")
	}

	cfg, err := config.LoadConfig(flags.config, spec.Cwd)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	applyPreset := func(engine, model, effort string) {
		if !flagsSet["engine"] && !flagsSet["E"] && engine != "" {
			spec.Engine = engine
		}
		if !flagsSet["model"] && !flagsSet["m"] && model != "" {
			spec.Model = model
		}
		if !flagsSet["effort"] && !flagsSet["e"] && effort != "" {
			spec.Effort = effort
		}
	}
	applyDefaults := func() {
		if spec.Engine == "" {
			spec.Engine = cfg.Defaults.Engine
		}
		if spec.Model == "" {
			spec.Model = cfg.Defaults.Model
		}
		if spec.Effort == "" {
			spec.Effort = cfg.Defaults.Effort
		}
	}

	coordinatorName := flags.coordinator
	if flags.stdin {
		coordinatorName = spec.Coordinator
	}
	if coordinatorName != "" {
		coordSpec, companionCfg, err := config.LoadCoordinator(coordinatorName, spec.Cwd)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		if companionCfg != nil {
			config.MergeConfigInto(cfg, companionCfg)
		}
		if flags.stdin {
			if spec.Engine == "" && coordSpec.Engine != "" {
				spec.Engine = coordSpec.Engine
			}
			if spec.Model == "" && coordSpec.Model != "" {
				spec.Model = coordSpec.Model
			}
			if spec.Effort == "" && coordSpec.Effort != "" {
				spec.Effort = coordSpec.Effort
			}
		} else {
			applyPreset(coordSpec.Engine, coordSpec.Model, coordSpec.Effort)
		}
		if ((flags.stdin && spec.TimeoutSec == 0) || (!flags.stdin && !flagsSet["timeout"] && !flagsSet["t"])) && coordSpec.Timeout > 0 {
			spec.TimeoutSec = coordSpec.Timeout
		}
		if spec.SystemPrompt == "" && coordSpec.SystemPrompt != "" {
			spec.SystemPrompt = coordSpec.SystemPrompt
		}
		spec.Skills = append(coordSpec.Skills, spec.Skills...)
	}

	roleName := flags.role
	if flags.stdin {
		roleName = spec.Role
	}
	if roleName != "" {
		role, err := config.ResolveRole(cfg, roleName)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		if flags.stdin {
			if spec.Engine == "" && role.Engine != "" {
				spec.Engine = role.Engine
			}
			if spec.Model == "" && role.Model != "" {
				spec.Model = role.Model
			}
			if spec.Effort == "" && role.Effort != "" {
				spec.Effort = role.Effort
			}
		} else {
			applyPreset(role.Engine, role.Model, role.Effort)
		}
	}

	applyDefaults()
	if spec.Effort == "" {
		spec.Effort = "high"
	}
	if spec.ResponseMaxChars == 0 {
		spec.ResponseMaxChars = cfg.Defaults.ResponseMaxChars
	}
	if spec.MaxDepth == 0 {
		spec.MaxDepth = cfg.Defaults.MaxDepth
	}
	if !flags.stdin && !flags.noSubdispatch {
		spec.AllowSubdispatch = cfg.Defaults.AllowSubdispatch
	}

	if spec.TimeoutSec == 0 {
		spec.TimeoutSec = config.TimeoutForEffort(cfg, spec.Effort)
	}
	if spec.GraceSec == 0 {
		spec.GraceSec = cfg.Timeout.Grace
	}

	if spec.EngineOpts == nil {
		spec.EngineOpts = map[string]any{}
	}
	spec.EngineOpts["heartbeat_interval_sec"] = cfg.Liveness.HeartbeatIntervalSec
	spec.EngineOpts["silence_warn_seconds"] = cfg.Liveness.SilenceWarnSeconds
	spec.EngineOpts["silence_kill_seconds"] = cfg.Liveness.SilenceKillSeconds
	// Apply default permission mode from config if not set by CLI.
	if _, ok := spec.EngineOpts["permission-mode"]; !ok || spec.EngineOpts["permission-mode"] == "" {
		if !flagsSet["permission-mode"] && cfg.Defaults.PermissionMode != "" {
			spec.EngineOpts["permission-mode"] = cfg.Defaults.PermissionMode
		}
	}

	if len(spec.Skills) > 0 {
		skillPrompt, pathDirs, err := config.LoadSkills(spec.Skills, spec.Cwd)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		if skillPrompt != "" {
			spec.Prompt = skillPrompt + "\n" + spec.Prompt
		}
		if len(pathDirs) > 0 {
			existing := anySliceOrEmpty(spec.EngineOpts["add-dir"])
			spec.EngineOpts["add-dir"] = append(pathDirs, existing...)
		}
	}

	if spec.Engine == "" {
		return failResult(spec, "invalid_args", "No engine specified.", "Use --engine codex, --engine claude, or --engine gemini.")
	}

	if spec.ContextFile != "" {
		if _, err := os.Stat(spec.ContextFile); err != nil {
			if os.IsNotExist(err) {
				return failResult(spec, "config_error",
					fmt.Sprintf("context file not found: %s", spec.ContextFile),
					"Check the --context-file path exists before dispatching.")
			}
			return failResult(spec, "config_error",
				fmt.Sprintf("cannot stat context file %s: %v", spec.ContextFile, err), "")
		}
		spec.Prompt = contextFilePromptPreamble + "\n" + spec.Prompt
	}

	recoverDispatchID := flags.recover
	if flags.stdin {
		recoverDispatchID = spec.ContinuesDispatchID
	}
	if recoverDispatchID != "" {
		recoveryCtx, err := recovery.RecoverDispatch(recoverDispatchID)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		spec.ContinuesDispatchID = recoverDispatchID
		spec.Prompt = recovery.BuildRecoveryPrompt(recoveryCtx, spec.Prompt)
	}

	hookEval := hooks.NewEvaluator(cfg.Hooks)
	if denied, matched := hookEval.CheckPrompt(spec.Prompt); denied {
		return failResult(spec, "prompt_denied",
			fmt.Sprintf("prompt blocked by hooks policy (matched: %q)", matched),
			"Remove the matching content from your prompt or adjust hook configuration.")
	}
	if hookEval.HasRules() {
		if inj := hookEval.PromptInjection(); inj != "" {
			spec.Prompt = inj + "\n\n" + spec.Prompt
		}
	}

	dispatch.EnsureTraceability(spec)

	var pipelineCfg pipeline.PipelineConfig
	if spec.Pipeline != "" {
		var ok bool
		pipelineCfg, ok = cfg.Pipelines[spec.Pipeline]
		if !ok {
			return failResult(spec, "config_error",
				fmt.Sprintf("Pipeline %q not found in config.", spec.Pipeline),
				fmt.Sprintf("Available pipelines: %v", availablePipelines(cfg)))
		}
		if err := pipeline.ValidatePipeline(pipelineCfg); err != nil {
			return failResult(spec, "config_error",
				fmt.Sprintf("Pipeline %q validation failed: %v", spec.Pipeline, err), "")
		}
	}

	preview := buildPreviewResult(spec, shouldRequireConfirmation(flags.yes, stdin, stdout, stderr, isTerminal))
	if command == commandPreview {
		writeJSON(stdout, preview)
		return 0
	}
	if preview.ConfirmationRequired {
		confirmed, err := confirmTTYDispatch(stdin, stderr, preview)
		if err != nil {
			fmt.Fprintf(stderr, "confirmation: %v\n", err)
			return 1
		}
		if !confirmed {
			result := buildCancelledResult(spec)
			if flags.output == "text" {
				writeTextResult(stdout, result)
			} else {
				writeResult(stdout, result)
			}
			fmt.Fprintln(stderr, "dispatch cancelled")
			return exitCodeCancelled
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if spec.Pipeline != "" {
		result, err := runPipeline(ctx, pipelineCfg, spec, cfg, stderr, flags.verbose)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		writePipelineResult(stdout, result)
		return 0
	}

	result, err := dispatchSpec(ctx, spec, cfg, stderr, flags.verbose, hookEval)
	if err != nil {
		fmt.Fprintf(stderr, "dispatch error: %v\n", err)
		return 1
	}

	if flags.output == "text" {
		writeTextResult(stdout, result)
	} else {
		writeResult(stdout, result)
	}
	return 0
}

func anySliceOrEmpty(v any) []string {
	switch value := v.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func writeResult(w io.Writer, result *types.DispatchResult) {
	writeJSON(w, result)
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeTextResult(w io.Writer, result *types.DispatchResult) {
	fmt.Fprintf(w, "Status: %s\n", result.Status)
	if result.Metadata != nil {
		fmt.Fprintf(w, "Engine: %s\n", result.Metadata.Engine)
		if result.Metadata.Model != "" {
			fmt.Fprintf(w, "Model: %s\n", result.Metadata.Model)
		}
		if result.Metadata.Tokens != nil {
			fmt.Fprintf(w, "Tokens: input=%d output=%d\n", result.Metadata.Tokens.Input, result.Metadata.Tokens.Output)
		}
	}
	fmt.Fprintf(w, "Duration: %dms\n", result.DurationMS)
	if result.Response != "" {
		fmt.Fprintf(w, "\n--- Response ---\n%s\n", result.Response)
	}
	if result.Error != nil {
		fmt.Fprintf(w, "\n--- Error ---\n%s: %s\n", result.Error.Code, result.Error.Message)
		if result.Error.Suggestion != "" {
			fmt.Fprintf(w, "Suggestion: %s\n", result.Error.Suggestion)
		}
	}
}

func writeSignalResult(w io.Writer, dispatchID, artifactDir string) {
	writeJSON(w, map[string]any{
		"status":       "ok",
		"dispatch_id":  dispatchID,
		"artifact_dir": artifactDir,
		"message":      "Signal delivered to inbox",
	})
}

func decodeStdinDispatchSpec(stdin io.Reader) (*types.DispatchSpec, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("missing stdin JSON: pipe a DispatchSpec object when using --stdin")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("parse stdin JSON: %w", err)
	}

	var spec types.DispatchSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("decode stdin DispatchSpec: %w", err)
	}
	if err := materializeStdinDispatchSpec(&spec, fields); err != nil {
		return nil, err
	}
	return &spec, nil
}

func materializeStdinDispatchSpec(spec *types.DispatchSpec, fields map[string]json.RawMessage) error {
	if spec == nil {
		return errors.New("missing DispatchSpec")
	}

	spec.DispatchID = strings.TrimSpace(spec.DispatchID)
	if spec.DispatchID == "" {
		spec.DispatchID = ulid.Make().String()
	}

	if strings.TrimSpace(spec.Prompt) == "" {
		return errors.New("missing prompt: DispatchSpec.prompt is required in --stdin mode")
	}

	spec.Cwd = strings.TrimSpace(spec.Cwd)
	if spec.Cwd == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		spec.Cwd = cwd
	}

	spec.ArtifactDir = strings.TrimSpace(spec.ArtifactDir)
	if spec.ArtifactDir == "" {
		spec.ArtifactDir = filepath.ToSlash(recovery.DefaultArtifactDir(spec.DispatchID)) + "/"
	}

	if !jsonFieldSet(fields, "allow_subdispatch") {
		spec.AllowSubdispatch = true
	}
	if !jsonFieldSet(fields, "full_access") {
		spec.FullAccess = true
	}
	if !jsonFieldSet(fields, "pipeline_step") {
		spec.PipelineStep = -1
	}
	if !jsonFieldSet(fields, "grace_sec") {
		spec.GraceSec = 60
	}
	if !jsonFieldSet(fields, "handoff_mode") && spec.HandoffMode == "" {
		spec.HandoffMode = "summary_and_refs"
	}

	return nil
}

func jsonFieldSet(fields map[string]json.RawMessage, name string) bool {
	if len(fields) == 0 {
		return false
	}
	_, ok := fields[name]
	return ok
}

func splitCommand(args []string) (cliCommand, []string, bool) {
	if len(args) == 0 {
		return commandDispatch, args, false
	}
	switch args[0] {
	case string(commandPreview):
		return commandPreview, args[1:], true
	case string(commandDispatch):
		return commandDispatch, args[1:], true
	default:
		return commandDispatch, args, false
	}
}

func buildPreviewResult(spec *types.DispatchSpec, confirmationRequired bool) previewResult {
	return previewResult{
		SchemaVersion: 1,
		Kind:          "preview",
		DispatchSpec:  previewDispatchSpecFrom(spec),
		Prompt:        previewPromptFrom(spec),
		Control: previewControl{
			ControlRecord: recovery.ControlRecordPath(spec.DispatchID),
			ArtifactDir:   spec.ArtifactDir,
		},
		PromptPreamble:       dispatch.PromptPreamble(spec),
		Warnings:             []string{},
		ConfirmationRequired: confirmationRequired,
	}
}

func previewDispatchSpecFrom(spec *types.DispatchSpec) previewDispatchSpec {
	if spec == nil {
		return previewDispatchSpec{}
	}
	return previewDispatchSpec{
		DispatchID:          spec.DispatchID,
		Salt:                spec.Salt,
		TraceToken:          spec.TraceToken,
		Engine:              spec.Engine,
		Model:               spec.Model,
		Effort:              spec.Effort,
		Role:                spec.Role,
		Coordinator:         spec.Coordinator,
		Pipeline:            spec.Pipeline,
		Cwd:                 spec.Cwd,
		Skills:              append([]string(nil), spec.Skills...),
		ContextFile:         spec.ContextFile,
		ArtifactDir:         spec.ArtifactDir,
		TimeoutSec:          spec.TimeoutSec,
		GraceSec:            spec.GraceSec,
		MaxDepth:            spec.MaxDepth,
		AllowSubdispatch:    spec.AllowSubdispatch,
		ContinuesDispatchID: spec.ContinuesDispatchID,
		ResponseMaxChars:    spec.ResponseMaxChars,
		FullAccess:          spec.FullAccess,
	}
}

func previewPromptFrom(spec *types.DispatchSpec) previewPrompt {
	if spec == nil {
		return previewPrompt{}
	}
	excerpt, truncated := previewExcerpt(spec.Prompt, previewPromptExcerptRunes)
	return previewPrompt{
		Excerpt:           excerpt,
		Chars:             utf8.RuneCountInString(spec.Prompt),
		Truncated:         truncated,
		SystemPromptChars: utf8.RuneCountInString(spec.SystemPrompt),
	}
}

func previewExcerpt(text string, maxRunes int) (string, bool) {
	compact := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if compact == "" {
		return "", false
	}
	runes := []rune(compact)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return compact, false
	}
	ellipsis := []rune(" ... ")
	if maxRunes <= len(ellipsis)+2 {
		return string(runes[:maxRunes]), true
	}
	headLen := (maxRunes - len(ellipsis)) * 2 / 3
	tailLen := maxRunes - len(ellipsis) - headLen
	if headLen < 1 {
		headLen = 1
	}
	if tailLen < 1 {
		tailLen = 1
		headLen = maxRunes - len(ellipsis) - tailLen
	}
	head := strings.TrimSpace(string(runes[:headLen]))
	tail := strings.TrimSpace(string(runes[len(runes)-tailLen:]))
	return head + string(ellipsis) + tail, true
}

func buildCancelledResult(spec *types.DispatchSpec) *types.DispatchResult {
	return dispatch.BuildFailedResult(
		spec,
		dispatch.NewDispatchError("cancelled", "Dispatch cancelled at confirmation prompt before launch.", "Re-run with --yes to skip the confirmation prompt."),
		&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}},
		0,
	)
}

func shouldRequireConfirmation(skip bool, stdin io.Reader, stdout, stderr io.Writer, isTerminal terminalChecker) bool {
	if skip || isTerminal == nil {
		return false
	}
	return isTerminal(stdin) && isTerminal(stdout) && isTerminal(stderr)
}

func confirmTTYDispatch(stdin io.Reader, stderr io.Writer, preview previewResult) (bool, error) {
	writeJSON(stderr, preview)
	if _, err := fmt.Fprint(stderr, "Proceed with dispatch? [y/N]: "); err != nil {
		return false, fmt.Errorf("write confirmation prompt: %w", err)
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation reply: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func isTerminalStream(stream any) bool {
	fdStream, ok := stream.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(fdStream.Fd()))
}

func newFlagSet(stderr io.Writer) (*flag.FlagSet, *cliFlags) {
	flags := &cliFlags{
		effort:    "",
		full:      true,
		maxDepth:  2,
		output:    "json",
		sandbox:   "danger-full-access",
		reasoning: "medium",
	}

	fs := flag.NewFlagSet("agent-mux", flag.ContinueOnError)
	fs.SetOutput(stderr)

	bindStr(fs, &flags.engine, "Engine name", "", "engine", "E")
	bindStr(fs, &flags.role, "Role", "", "role", "R")
	fs.StringVar(&flags.coordinator, "coordinator", "", "Coordinator")
	bindStr(fs, &flags.cwd, "Working directory", "", "cwd", "C")
	bindStr(fs, &flags.model, "Model", "", "model", "m")
	bindStr(fs, &flags.effort, "Effort", flags.effort, "effort", "e")
	fs.IntVar(&flags.timeout, "timeout", 0, "Timeout seconds")
	fs.IntVar(&flags.timeout, "t", 0, "Timeout seconds")
	bindStr(fs, &flags.systemPrompt, "System prompt", "", "system-prompt", "s")
	fs.StringVar(&flags.systemPromptFile, "system-prompt-file", "", "System prompt file")
	fs.Var(&flags.skills, "skill", "Skill name")
	fs.StringVar(&flags.contextFile, "context-file", "", "Context file")
	fs.StringVar(&flags.artifactDir, "artifact-dir", "", "Artifact directory")
	fs.StringVar(&flags.recover, "recover", "", "Previous dispatch ID to continue")
	fs.StringVar(&flags.signal, "signal", "", "Dispatch ID to send signal to")
	fs.StringVar(&flags.salt, "salt", "", "Dispatch salt")
	fs.StringVar(&flags.config, "config", "", "Config path")
	bindStr(fs, &flags.pipeline, "Pipeline name", "", "pipeline", "P")
	bindBool(fs, &flags.full, "Full access mode", flags.full, "full", "f")
	fs.BoolVar(&flags.noFull, "no-full", false, "Disable full access mode")
	fs.StringVar(&flags.promptFile, "prompt-file", "", "Prompt file")
	fs.IntVar(&flags.maxDepth, "max-depth", flags.maxDepth, "Maximum recursive depth")
	fs.BoolVar(&flags.noSubdispatch, "no-subdispatch", false, "Disable recursive dispatch")
	fs.StringVar(&flags.permissionMode, "permission-mode", "", "Permission mode")
	fs.BoolVar(&flags.stdin, "stdin", false, "Read DispatchSpec JSON from stdin")
	fs.BoolVar(&flags.yes, "yes", false, "Skip interactive confirmation for TTY dispatches")
	fs.IntVar(&flags.responseMaxChars, "response-max-chars", 0, "Maximum response characters")
	bindBool(fs, &flags.version, "Show version", false, "version", "V")
	fs.StringVar(&flags.sandbox, "sandbox", flags.sandbox, "Sandbox mode")
	bindStr(fs, &flags.reasoning, "Reasoning effort", flags.reasoning, "reasoning", "r")
	fs.IntVar(&flags.maxTurns, "max-turns", 0, "Maximum turns")
	fs.Var(&flags.addDirs, "add-dir", "Additional writable directory")
	bindStr(fs, &flags.output, "Output format (json, text)", "json", "output", "o")
	bindBool(fs, &flags.verbose, "Verbose mode", false, "verbose", "v")

	return fs, flags
}

func buildDispatchSpecE(flags cliFlags, args []string) (*types.DispatchSpec, error) {
	if flags.promptFile != "" && len(args) > 0 {
		return nil, errors.New("prompt must come from either the first positional arg or --prompt-file, not both")
	}
	var (
		prompt, systemPrompt string
		err                  error
	)
	if flags.promptFile != "" {
		data, readErr := os.ReadFile(flags.promptFile)
		if readErr != nil {
			return nil, fmt.Errorf("read prompt file %q: %w", flags.promptFile, readErr)
		}
		prompt = string(data)
	} else if len(args) > 0 {
		prompt = args[0]
	}
	if prompt == "" {
		return nil, errors.New("missing prompt: provide the first positional arg or --prompt-file")
	}
	if flags.systemPromptFile != "" {
		data, readErr := os.ReadFile(flags.systemPromptFile)
		if readErr != nil {
			return nil, fmt.Errorf("read system prompt file %q: %w", flags.systemPromptFile, readErr)
		}
		systemPrompt = string(data)
	}
	if flags.systemPrompt != "" {
		if systemPrompt == "" {
			systemPrompt = flags.systemPrompt
		} else {
			systemPrompt = systemPrompt + "\n\n" + flags.systemPrompt
		}
	}
	dispatchID := ulid.Make().String()
	cwd := flags.cwd
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	artifactDir := flags.artifactDir
	if artifactDir == "" {
		artifactDir = filepath.ToSlash(recovery.DefaultArtifactDir(dispatchID)) + "/"
	}

	fullAccess := flags.full
	if flags.noFull {
		fullAccess = false
	}

	allowSubdispatch := true
	if flags.noSubdispatch {
		allowSubdispatch = false
	}

	engineOpts := map[string]any{
		"sandbox":         flags.sandbox,
		"reasoning":       flags.reasoning,
		"max-turns":       flags.maxTurns,
		"add-dir":         []string(flags.addDirs),
		"permission-mode": flags.permissionMode,
	}

	spec := &types.DispatchSpec{
		DispatchID:       dispatchID,
		Salt:             flags.salt,
		Engine:           flags.engine,
		Model:            flags.model,
		Effort:           flags.effort,
		Prompt:           prompt,
		SystemPrompt:     systemPrompt,
		Cwd:              cwd,
		Skills:           append([]string(nil), flags.skills...),
		Coordinator:      flags.coordinator,
		Pipeline:         flags.pipeline,
		ContextFile:      flags.contextFile,
		ArtifactDir:      artifactDir,
		TimeoutSec:       flags.timeout,
		GraceSec:         60,
		Role:             flags.role,
		MaxDepth:         flags.maxDepth,
		AllowSubdispatch: allowSubdispatch,
		PipelineStep:     -1,
		HandoffMode:      "summary_and_refs",
		ResponseMaxChars: flags.responseMaxChars,
		EngineOpts:       engineOpts,
		FullAccess:       fullAccess,
	}

	return spec, nil
}

func normalizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}

	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		flags = append(flags, arg)
		if strings.Contains(arg, "=") || !flagTakesValue(arg) {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}

	return append(flags, positionals...)
}

func flagTakesValue(name string) bool {
	switch name {
	case "--engine", "-E",
		"--role", "-R",
		"--coordinator",
		"--cwd", "-C",
		"--model", "-m",
		"--effort", "-e",
		"--timeout", "-t",
		"--system-prompt", "-s",
		"--system-prompt-file",
		"--skill",
		"--context-file",
		"--artifact-dir",
		"--recover",
		"--signal",
		"--salt",
		"--config",
		"--pipeline", "-P",
		"--prompt-file",
		"--max-depth",
		"--permission-mode",
		"--response-max-chars",
		"--sandbox",
		"--reasoning", "-r",
		"--max-turns",
		"--add-dir",
		"--output", "-o":
		return true
	default:
		return false
	}
}

func bindStr(fs *flag.FlagSet, dst *string, usage, def string, names ...string) {
	for _, name := range names {
		fs.StringVar(dst, name, def, usage)
	}
}

func bindBool(fs *flag.FlagSet, dst *bool, usage string, def bool, names ...string) {
	for _, name := range names {
		fs.BoolVar(dst, name, def, usage)
	}
}

func writePipelineResult(w io.Writer, result *pipeline.PipelineResult) {
	writeJSON(w, result)
}

func runPipeline(ctx context.Context, pipelineCfg pipeline.PipelineConfig, baseSpec *types.DispatchSpec, cfg *config.Config, stderr io.Writer, verbose bool) (*pipeline.PipelineResult, error) {
	hookEval := hooks.NewEvaluator(cfg.Hooks)
	for i, step := range pipelineCfg.Steps {
		if step.Role == "" {
			continue
		}
		roleCfg, err := config.ResolveRole(cfg, step.Role)
		if err != nil {
			return nil, fmt.Errorf("resolve pipeline step[%d] role %q: %w", i, step.Role, err)
		}
		if pipelineCfg.Steps[i].Engine == "" {
			pipelineCfg.Steps[i].Engine = roleCfg.Engine
		}
		if pipelineCfg.Steps[i].Model == "" {
			pipelineCfg.Steps[i].Model = roleCfg.Model
		}
		if pipelineCfg.Steps[i].Effort == "" {
			pipelineCfg.Steps[i].Effort = roleCfg.Effort
		}
	}

	pipelineArtifactDir := filepath.Join(baseSpec.ArtifactDir, "pipeline")
	if err := dispatch.EnsureArtifactDir(pipelineArtifactDir); err != nil {
		return nil, fmt.Errorf("create pipeline artifact dir: %w", err)
	}

	dispatchFn := func(ctx context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		result, err := dispatchSpec(ctx, spec, cfg, stderr, verbose, hookEval)
		if err != nil {
			return dispatch.BuildFailedResult(
				spec,
				dispatch.NewDispatchError("process_killed", err.Error(), ""),
				&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
				&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}, PipelineID: spec.PipelineID, ParentDispatchID: spec.ParentDispatchID},
				0,
			)
		}
		if result != nil && result.Metadata != nil {
			result.Metadata.PipelineID = spec.PipelineID
			result.Metadata.ParentDispatchID = spec.ParentDispatchID
		}
		return result
	}

	return pipeline.ExecutePipeline(ctx, pipelineCfg, baseSpec, pipelineArtifactDir, dispatchFn)
}

func dispatchSpec(ctx context.Context, spec *types.DispatchSpec, cfg *config.Config, stderr io.Writer, verbose bool, hookEval *hooks.Evaluator) (*types.DispatchResult, error) {
	dispatch.EnsureTraceability(spec)
	reg := adapter.NewRegistry(configuredModels(cfg))

	adp, err := reg.Get(spec.Engine)
	if err != nil {
		return dispatch.BuildFailedResult(
			spec,
			dispatch.NewDispatchError("engine_not_found", fmt.Sprintf("Engine %q not found.", spec.Engine), "Valid engines: [codex, claude, gemini]"),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}},
			0,
		), nil
	}
	validModels := reg.ValidModels(spec.Engine)
	if spec.Model != "" && len(validModels) > 0 && !slices.Contains(validModels, spec.Model) {
		suggestion := dispatch.FuzzyMatchModel(spec.Model, validModels)
		suggestionText := fmt.Sprintf("Valid models for %s: %v", spec.Engine, validModels)
		if suggestion != "" {
			suggestionText = fmt.Sprintf("Did you mean %q? %s", suggestion, suggestionText)
		}
		return dispatch.BuildFailedResult(
			spec,
			dispatch.NewDispatchError("model_not_found", fmt.Sprintf("Model %q not found for engine %s.", spec.Model, spec.Engine), suggestionText),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}},
			0,
		), nil
	}

	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return dispatch.BuildFailedResult(
			spec,
			dispatch.NewDispatchError("artifact_dir_unwritable", fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err), "Choose a writable --artifact-dir path."),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}},
			0,
		), nil
	}
	if err := recovery.RegisterDispatchSpec(spec); err != nil {
		return dispatch.BuildFailedResult(
			spec,
			dispatch.NewDispatchError("config_error", fmt.Sprintf("Register control path for dispatch %q: %v", spec.DispatchID, err), "Ensure the control path is writable."),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}},
			0,
		), nil
	}

	eng := engine.NewLoopEngine(adp, stderr, hookEval)
	eng.SetVerbose(verbose)
	return eng.Dispatch(ctx, spec)
}

func stdinDispatchFlagsSet(flagsSet map[string]bool) bool {
	for _, name := range []string{
		"engine", "E",
		"role", "R",
		"coordinator",
		"cwd", "C",
		"model", "m",
		"effort", "e",
		"timeout", "t",
		"system-prompt", "s",
		"system-prompt-file",
		"skill",
		"context-file",
		"artifact-dir",
		"recover",
		"salt",
		"config",
		"pipeline", "P",
		"full", "f",
		"no-full",
		"prompt-file",
		"max-depth",
		"no-subdispatch",
		"permission-mode",
		"response-max-chars",
		"sandbox",
		"reasoning", "r",
		"max-turns",
		"add-dir",
	} {
		if flagsSet[name] {
			return true
		}
	}
	return false
}

func configuredModels(cfg *config.Config) map[string][]string {
	models := make(map[string][]string, len(cfg.Models)+3)
	for engineName, engineModels := range cfg.Models {
		models[engineName] = append([]string(nil), engineModels...)
	}
	if len(models["codex"]) == 0 {
		models["codex"] = []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"}
	}
	if len(models["claude"]) == 0 {
		models["claude"] = []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"}
	}
	if len(models["gemini"]) == 0 {
		models["gemini"] = []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview"}
	}
	return models
}

func availablePipelines(cfg *config.Config) []string {
	if cfg == nil || len(cfg.Pipelines) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Pipelines))
	for name := range cfg.Pipelines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
