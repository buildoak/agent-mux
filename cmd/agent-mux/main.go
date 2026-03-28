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
	"strconv"
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
	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/types"
	"github.com/oklog/ulid/v2"
	"golang.org/x/term"
)

const version = "agent-mux v2.0.0-dev"
const contextFilePromptPreamble = "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting."
const unsetResponseMaxChars = -1

type cliCommand string

const (
	commandDispatch cliCommand = "dispatch"
	commandPreview  cliCommand = "preview"
	commandList     cliCommand = "list"
	commandStatus   cliCommand = "status"
	commandResult   cliCommand = "result"
	commandInspect  cliCommand = "inspect"
	commandGC       cliCommand = "gc"
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
	engine, role, variant, profile, coordinator, cwd, model, effort, systemPrompt, systemPromptFile string
	contextFile, artifactDir, salt, config, promptFile, recover                                     string
	signal                                                                                          string
	permissionMode, sandbox, reasoning                                                              string
	output, pipeline                                                                                string
	timeout, maxDepth, responseMaxChars, maxTurns                                                   int
	full, noFull, noSubdispatch, stdin, version, verbose, yes                                       bool
	skills, addDirs                                                                                 stringSlice
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
	Variant             string   `json:"variant,omitempty"`
	Profile             string   `json:"profile,omitempty"`
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
	ResponseMaxChars    int      `json:"response_max_chars"`
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

type SignalAck struct {
	Status      string               `json:"status"`
	DispatchID  string               `json:"dispatch_id,omitempty"`
	ArtifactDir string               `json:"artifact_dir,omitempty"`
	Message     string               `json:"message,omitempty"`
	Error       *types.DispatchError `json:"error,omitempty"`
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
	switch command {
	case commandList:
		return runListCommand(args, stdout)
	case commandStatus:
		return runStatusCommand(args, stdout)
	case commandResult:
		return runResultCommand(args, stdout)
	case commandInspect:
		return runInspectCommand(args, stdout)
	case commandGC:
		return runGCCommand(args, stdout)
	}

	var flagOutput bytes.Buffer
	fs, parsed := newFlagSet(&flagOutput)
	err := fs.Parse(normalizeArgs(args))
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			emitResult(stdout, map[string]any{
				"kind":  "help",
				"usage": strings.TrimSpace(flagOutput.String()),
			})
			return 0
		}
		emitResult(stdout, buildFailedResult(&types.DispatchSpec{}, "invalid_args", err.Error(), strings.TrimSpace(flagOutput.String())))
		return 2
	}
	flags := *parsed
	positional := fs.Args()
	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})
	flags.signal = strings.TrimSpace(flags.signal)
	if flags.stdin && !flagsSet["yes"] {
		flags.yes = true
	}

	if flags.version {
		emitResult(stdout, map[string]any{"version": version})
		return 0
	}
	if flags.signal != "" {
		if err := sanitize.ValidateDispatchID(flags.signal); err != nil {
			emitResult(stdout, buildSignalErrorAck(flags.signal, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", flags.signal, err), "Provide a dispatch ID without path separators or traversal segments."))
			return 1
		}
		if len(positional) == 0 {
			emitResult(stdout, buildSignalErrorAck(flags.signal, "invalid_args", "--signal requires a message as the first positional argument", "Provide the signal message as the first positional argument."))
			return 2
		}
		msg := positional[0]
		artifactDir, err := recovery.ResolveArtifactDir(flags.signal)
		if err != nil {
			emitResult(stdout, buildSignalErrorAck(flags.signal, "recovery_failed", err.Error(), "Check the dispatch ID and control record, then retry."))
			return 1
		}
		if err := inbox.WriteInbox(artifactDir, msg); err != nil {
			emitResult(stdout, buildSignalErrorAck(flags.signal, "config_error", err.Error(), "Ensure the inbox path is writable and retry."))
			return 1
		}
		emitResult(stdout, buildSignalAck(flags.signal, artifactDir))
		return 0
	}
	if explicitCommand && !flags.stdin && flags.promptFile == "" && len(positional) == 0 {
		return emitFailureResult(stdout, &types.DispatchSpec{}, 1, "invalid_args",
			fmt.Sprintf("missing prompt: provide the first positional arg or --prompt-file"),
			fmt.Sprintf("If you meant the literal prompt %q, pass it after -- (for example: agent-mux -- %s) or use --prompt-file/--stdin.", command, command))
	}

	failResult := func(spec *types.DispatchSpec, code, msg, suggestion string) int {
		return emitFailureResult(stdout, spec, 1, code, msg, suggestion)
	}

	var spec *types.DispatchSpec
	if flags.stdin {
		spec, err = decodeStdinDispatchSpec(stdin)
		if err != nil {
			code := "invalid_args"
			if isInputValidationError(err) {
				code = "invalid_input"
			}
			return emitFailureResult(stdout, &types.DispatchSpec{}, 1, code, err.Error(), "")
		}
	} else {
		normalizeDispatchFlags(&flags)
		if err := validateDispatchFlags(flags, flagsSet); err != nil {
			return emitFailureResult(stdout, &types.DispatchSpec{}, 1, "invalid_input", err.Error(), "")
		}
		spec, err = buildDispatchSpecE(flags, positional)
		if err != nil {
			return emitFailureResult(stdout, &types.DispatchSpec{}, 1, "invalid_args", err.Error(), "")
		}
	}

	if flags.stdin && stdinDispatchFlagsSet(flagsSet) {
		fmt.Fprintf(stderr, "Warning: --stdin mode active; CLI dispatch flags are ignored.\n")
	}

	cfg, err := config.LoadConfig(flags.config, spec.Cwd)
	if err != nil {
		return emitFailureResult(stdout, spec, 1, configFailureCode(err), fmt.Sprintf("load config: %v", err), "")
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

	profileName := spec.Profile
	if profileName != "" {
		coordSpec, companionCfg, err := config.LoadProfile(profileName, spec.Cwd)
		if err != nil {
			return failResult(spec, configFailureCode(err), err.Error(), "")
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
	variantName := flags.variant
	if flags.stdin {
		roleName = spec.Role
		variantName = spec.Variant
	}
	if roleName == "" && variantName != "" {
		return failResult(spec, "invalid_args", "--variant requires --role", "")
	}
	if roleName != "" {
		role, err := config.ResolveRole(cfg, roleName)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		if variantName != "" {
			resolvedRole, err := resolveVariant(*role, variantName)
			if err != nil {
				return failResult(spec, "config_error", fmt.Sprintf("variant %q not found in role %q", variantName, roleName), "")
			}
			role = &resolvedRole
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
			if spec.TimeoutSec == 0 && role.Timeout > 0 {
				spec.TimeoutSec = role.Timeout
			}
		} else {
			applyPreset(role.Engine, role.Model, role.Effort)
			if !flagsSet["timeout"] && !flagsSet["t"] && role.Timeout > 0 {
				spec.TimeoutSec = role.Timeout
			}
		}
		roleSystemPrompt, err := loadSystemPromptFile(role.SourceDir, role.SystemPromptFile)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		spec.SystemPrompt = prependSystemPrompt(roleSystemPrompt, spec.SystemPrompt)
		spec.Skills = mergeSkills(role.Skills, spec.Skills)
		spec.Role = roleName
		spec.Variant = variantName
	}

	applyDefaults()
	if spec.Effort == "" {
		spec.Effort = "high"
	}
	if spec.ResponseMaxChars < 0 {
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
	if err := validateResolvedDispatchTimeouts(spec); err != nil {
		return failResult(spec, "invalid_input", err.Error(), "")
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
			return emitFailureResult(stdout, spec, 1, "recovery_failed", err.Error(), "")
		}
		spec.ContinuesDispatchID = recoverDispatchID
		spec.Prompt = recovery.BuildRecoveryPrompt(recoveryCtx, spec.Prompt)
	}

	hookEval := hooks.NewEvaluator(cfg.Hooks)
	if denied, matched := checkPromptDenied(spec, hookEval); denied {
		code, msg, suggestion := promptDeniedFailure(matched)
		return failResult(spec, code, msg, suggestion)
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
		emitResult(stdout, preview)
		return 0
	}
	if !flags.yes {
		writeCompactJSON(stderr, preview)
	}
	if preview.ConfirmationRequired {
		confirmed, err := confirmTTYDispatch(stdin, stderr)
		if err != nil {
			return emitFailureResult(stdout, spec, 1, "config_error", fmt.Sprintf("confirmation: %v", err), "")
		}
		if !confirmed {
			emitResult(stdout, buildCancelledResult(spec))
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
		emitResult(stdout, result)
		return 0
	}

	result, err := dispatchSpec(ctx, spec, cfg, stderr, flags.verbose, hookEval)
	if err != nil {
		return emitFailureResult(stdout, spec, 1, "process_killed", err.Error(), "")
	}

	emitResult(stdout, result)
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

func mergeSkills(base, overlay []string) []string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}

	merged := make([]string, 0, len(base)+len(overlay))
	seen := make(map[string]struct{}, len(base)+len(overlay))

	for _, skill := range overlay {
		if _, ok := seen[skill]; ok {
			continue
		}
		seen[skill] = struct{}{}
		merged = append(merged, skill)
	}
	for _, skill := range base {
		if _, ok := seen[skill]; ok {
			continue
		}
		seen[skill] = struct{}{}
		merged = append(merged, skill)
	}

	return merged
}

func resolveVariant(role config.RoleConfig, variantName string) (config.RoleConfig, error) {
	variantName = strings.TrimSpace(variantName)
	if variantName == "" {
		return role, nil
	}

	variant, ok := role.Variants[variantName]
	if !ok {
		return config.RoleConfig{}, fmt.Errorf("variant %q not found", variantName)
	}

	resolved := role
	if variant.Engine != "" {
		resolved.Engine = variant.Engine
	}
	if variant.Model != "" {
		resolved.Model = variant.Model
	}
	if variant.Effort != "" {
		resolved.Effort = variant.Effort
	}
	if variant.Timeout > 0 {
		resolved.Timeout = variant.Timeout
	}
	resolved.Skills = mergeSkills(role.Skills, variant.Skills)
	if variant.SystemPromptFile != "" {
		resolved.SystemPromptFile = variant.SystemPromptFile
	}

	return resolved, nil
}

func loadSystemPromptFile(sourceDir, promptFile string) (string, error) {
	promptFile = strings.TrimSpace(promptFile)
	if promptFile == "" {
		return "", nil
	}
	if filepath.IsAbs(promptFile) {
		return "", fmt.Errorf("system prompt file %q must be relative to the config source directory; absolute paths are not allowed", promptFile)
	}
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		return "", fmt.Errorf("system prompt file %q cannot be resolved without a config source directory", promptFile)
	}

	candidateChildren := []string{promptFile}
	if filepath.Dir(promptFile) == "." {
		candidateChildren = append(candidateChildren, filepath.Join("prompts", promptFile))
	}

	tried := make([]string, 0, len(candidateChildren))
	for _, child := range candidateChildren {
		path, err := sanitize.SafeJoinPath(sourceDir, child)
		if err != nil {
			return "", fmt.Errorf("invalid system prompt file %q: %w", promptFile, err)
		}
		tried = append(tried, path)

		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read system prompt file %q: %w", path, err)
		}
	}
	return "", fmt.Errorf("read system prompt file %q: not found under %q (tried: %s)", promptFile, sourceDir, strings.Join(tried, ", "))
}

func prependSystemPrompt(prefix, existing string) string {
	switch {
	case strings.TrimSpace(prefix) == "":
		return existing
	case strings.TrimSpace(existing) == "":
		return prefix
	default:
		return prefix + "\n\n" + existing
	}
}

func checkPromptDenied(spec *types.DispatchSpec, hookEval *hooks.Evaluator) (bool, string) {
	if spec == nil || hookEval == nil {
		return false, ""
	}
	return hookEval.CheckPrompt(stripHookPromptInjection(spec.Prompt, hookEval), spec.SystemPrompt)
}

func stripHookPromptInjection(prompt string, hookEval *hooks.Evaluator) string {
	if hookEval == nil {
		return prompt
	}
	inj := hookEval.PromptInjection()
	if inj == "" {
		return prompt
	}
	withSpacing := inj + "\n\n"
	if strings.HasPrefix(prompt, withSpacing) {
		return strings.TrimPrefix(prompt, withSpacing)
	}
	if strings.HasPrefix(prompt, inj) {
		return strings.TrimPrefix(prompt, inj)
	}
	return prompt
}

func promptDeniedFailure(matched string) (code, msg, suggestion string) {
	return "prompt_denied",
		fmt.Sprintf("prompt blocked by hooks policy (matched: %q)", matched),
		"Remove the matching content from your prompt or adjust hook configuration."
}

func emitResult(w io.Writer, result interface{}) {
	payload := result
	switch value := result.(type) {
	case nil:
		payload = buildFailedResult(&types.DispatchSpec{}, "internal_error", "missing terminal result", "")
	case *types.DispatchResult:
		if value == nil {
			payload = buildFailedResult(&types.DispatchSpec{}, "internal_error", "missing dispatch result", "")
		}
	case *pipeline.PipelineResult:
		if value == nil {
			payload = buildFailedResult(&types.DispatchSpec{}, "internal_error", "missing pipeline result", "")
		}
	case *SignalAck:
		if value == nil {
			payload = buildSignalErrorAck("", "internal_error", "missing signal acknowledgement", "")
		}
	}
	writeCompactJSON(w, payload)
}

func writeCompactJSON(w io.Writer, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	_, _ = w.Write(buf.Bytes())
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

func buildSignalAck(dispatchID, artifactDir string) SignalAck {
	return SignalAck{
		Status:      "ok",
		DispatchID:  dispatchID,
		ArtifactDir: artifactDir,
		Message:     "Signal delivered to inbox",
	}
}

func buildSignalErrorAck(dispatchID, code, message, suggestion string) SignalAck {
	return SignalAck{
		Status:     "error",
		DispatchID: dispatchID,
		Message:    message,
		Error:      dispatch.NewDispatchError(code, message, suggestion),
	}
}

func buildFailedResult(spec *types.DispatchSpec, code, msg, suggestion string) *types.DispatchResult {
	if spec == nil {
		spec = &types.DispatchSpec{}
	}
	return dispatch.BuildFailedResult(
		spec,
		dispatch.NewDispatchError(code, msg, suggestion),
		&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: spec.Role, Tokens: &types.TokenUsage{}},
		0,
	)
}

func emitFailureResult(stdout io.Writer, spec *types.DispatchSpec, exitCode int, code, msg, suggestion string) int {
	emitResult(stdout, buildFailedResult(spec, code, msg, suggestion))
	return exitCode
}

func normalizeDispatchFlags(flags *cliFlags) {
	if flags == nil {
		return
	}

	flags.profile = strings.TrimSpace(flags.profile)
	flags.coordinator = strings.TrimSpace(flags.coordinator)
	for i, name := range flags.skills {
		flags.skills[i] = strings.TrimSpace(name)
	}
}

func validateDispatchFlags(flags cliFlags, flagsSet map[string]bool) error {
	if flagsSet["timeout"] || flagsSet["t"] {
		if err := validatePositiveDispatchValue("timeout", flags.timeout); err != nil {
			return err
		}
	}
	for _, name := range flags.skills {
		if err := sanitize.ValidateBasename(name); err != nil {
			return newInputValidationError("skill", name, err)
		}
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "profile", value: flags.profile},
		{label: "coordinator", value: flags.coordinator},
	} {
		if field.value == "" {
			continue
		}
		if err := sanitize.ValidateBasename(field.value); err != nil {
			return newInputValidationError(field.label, field.value, err)
		}
	}
	return nil
}

type inputValidationError struct {
	field string
	value string
	err   error
}

func (e *inputValidationError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("invalid %s %q: %v", e.field, e.value, e.err)
}

func (e *inputValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newInputValidationError(field, value string, err error) error {
	return &inputValidationError{
		field: field,
		value: value,
		err:   err,
	}
}

func isInputValidationError(err error) bool {
	var target *inputValidationError
	return errors.As(err, &target)
}

func validatePositiveDispatchValue(field string, value int) error {
	if value > 0 {
		return nil
	}
	return newInputValidationError(field, strconv.Itoa(value), errors.New("must be > 0"))
}

func validateResolvedDispatchTimeouts(spec *types.DispatchSpec) error {
	if spec == nil {
		return errors.New("missing DispatchSpec")
	}
	if err := validatePositiveDispatchValue("timeout_sec", spec.TimeoutSec); err != nil {
		return err
	}
	if err := validatePositiveDispatchValue("grace_sec", spec.GraceSec); err != nil {
		return err
	}
	return nil
}

func configFailureCode(err error) string {
	if config.IsValidationError(err) {
		return "invalid_input"
	}
	return "config_error"
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
	if err := validateStdinProfileAliases(fields); err != nil {
		return nil, err
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
	if spec.DispatchID != "" {
		if err := sanitize.ValidateDispatchID(spec.DispatchID); err != nil {
			return newInputValidationError("dispatch_id", spec.DispatchID, err)
		}
	}
	if spec.DispatchID == "" {
		spec.DispatchID = ulid.Make().String()
	}

	spec.Profile = strings.TrimSpace(spec.Profile)
	if spec.Profile != "" {
		if err := sanitize.ValidateBasename(spec.Profile); err != nil {
			return newInputValidationError("profile", spec.Profile, err)
		}
	}
	for i, name := range spec.Skills {
		name = strings.TrimSpace(name)
		if err := sanitize.ValidateBasename(name); err != nil {
			return newInputValidationError("skill", name, err)
		}
		spec.Skills[i] = name
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
		artifactDir, err := recovery.DefaultArtifactDir(spec.DispatchID)
		if err != nil {
			return fmt.Errorf("default artifact dir for dispatch %q: %w", spec.DispatchID, err)
		}
		spec.ArtifactDir = filepath.ToSlash(artifactDir) + "/"
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
	if jsonFieldSet(fields, "timeout_sec") {
		if err := validatePositiveDispatchValue("timeout_sec", spec.TimeoutSec); err != nil {
			return err
		}
	}
	if !jsonFieldSet(fields, "grace_sec") {
		spec.GraceSec = 60
	} else {
		if err := validatePositiveDispatchValue("grace_sec", spec.GraceSec); err != nil {
			return err
		}
	}
	if !jsonFieldSet(fields, "response_max_chars") {
		spec.ResponseMaxChars = unsetResponseMaxChars
	}
	if !jsonFieldSet(fields, "handoff_mode") && spec.HandoffMode == "" {
		spec.HandoffMode = "summary_and_refs"
	}

	return nil
}

func validateStdinProfileAliases(fields map[string]json.RawMessage) error {
	for _, field := range []struct {
		label string
		key   string
	}{
		{label: "profile", key: "profile"},
		{label: "coordinator", key: "coordinator"},
	} {
		raw, ok := fields[field.key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if err := sanitize.ValidateBasename(value); err != nil {
			return newInputValidationError(field.label, value, err)
		}
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
	case string(commandList):
		return commandList, args[1:], true
	case string(commandStatus):
		return commandStatus, args[1:], true
	case string(commandResult):
		return commandResult, args[1:], true
	case string(commandInspect):
		return commandInspect, args[1:], true
	case string(commandGC):
		return commandGC, args[1:], true
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
		Variant:             spec.Variant,
		Profile:             spec.Profile,
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
		dispatch.NewDispatchError("cancelled", "Dispatch cancelled at confirmation prompt before launch.", "Re-run with --yes to skip preview and confirmation."),
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

func confirmTTYDispatch(stdin io.Reader, stderr io.Writer) (bool, error) {
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
		effort:           "",
		full:             true,
		maxDepth:         2,
		output:           "json",
		responseMaxChars: unsetResponseMaxChars,
		sandbox:          "danger-full-access",
		reasoning:        "medium",
	}

	fs := flag.NewFlagSet("agent-mux", flag.ContinueOnError)
	fs.SetOutput(stderr)

	bindStr(fs, &flags.engine, "Engine name", "", "engine", "E")
	bindStr(fs, &flags.role, "Role", "", "role", "R")
	fs.StringVar(&flags.variant, "variant", "", "Role variant")
	fs.StringVar(&flags.profile, "profile", "", "Profile")
	fs.StringVar(&flags.coordinator, "coordinator", "", "Legacy alias for --profile")
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
	fs.IntVar(&flags.responseMaxChars, "response-max-chars", flags.responseMaxChars, "Maximum response characters")
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
	if flags.variant != "" && flags.role == "" {
		return nil, errors.New("--variant requires --role")
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
	profile, err := resolveProfileName(flags.profile, flags.coordinator)
	if err != nil {
		return nil, err
	}
	cwd := flags.cwd
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	artifactDir := flags.artifactDir
	if artifactDir == "" {
		artifactDirPath, err := recovery.DefaultArtifactDir(dispatchID)
		if err != nil {
			return nil, fmt.Errorf("default artifact dir for dispatch %q: %w", dispatchID, err)
		}
		artifactDir = filepath.ToSlash(artifactDirPath) + "/"
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
		Profile:          profile,
		Pipeline:         flags.pipeline,
		ContextFile:      flags.contextFile,
		ArtifactDir:      artifactDir,
		TimeoutSec:       flags.timeout,
		GraceSec:         60,
		Role:             flags.role,
		Variant:          flags.variant,
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

func resolveProfileName(profile, coordinator string) (string, error) {
	profile = strings.TrimSpace(profile)
	coordinator = strings.TrimSpace(coordinator)
	switch {
	case profile == "":
		return coordinator, nil
	case coordinator == "" || coordinator == profile:
		return profile, nil
	default:
		return "", fmt.Errorf("conflicting profile values: --profile=%q --coordinator=%q", profile, coordinator)
	}
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
		"--variant",
		"--profile",
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
		"--limit",
		"--status",
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

func runPipeline(ctx context.Context, pipelineCfg pipeline.PipelineConfig, baseSpec *types.DispatchSpec, cfg *config.Config, stderr io.Writer, verbose bool) (*pipeline.PipelineResult, error) {
	hookEval := hooks.NewEvaluator(cfg.Hooks)
	for i, step := range pipelineCfg.Steps {
		if step.Role == "" {
			if step.Variant != "" {
				return nil, fmt.Errorf("resolve pipeline step[%d]: variant %q requires role", i, step.Variant)
			}
			continue
		}
		roleCfg, err := config.ResolveRole(cfg, step.Role)
		if err != nil {
			return nil, fmt.Errorf("resolve pipeline step[%d] role %q: %w", i, step.Role, err)
		}
		resolvedRole := *roleCfg
		if step.Variant != "" {
			resolvedRole, err = resolveVariant(resolvedRole, step.Variant)
			if err != nil {
				return nil, fmt.Errorf("resolve pipeline step[%d]: variant %q not found in role %q", i, step.Variant, step.Role)
			}
		}
		if pipelineCfg.Steps[i].Engine == "" {
			pipelineCfg.Steps[i].Engine = resolvedRole.Engine
		}
		if pipelineCfg.Steps[i].Model == "" {
			pipelineCfg.Steps[i].Model = resolvedRole.Model
		}
		if pipelineCfg.Steps[i].Effort == "" {
			pipelineCfg.Steps[i].Effort = resolvedRole.Effort
		}
		if pipelineCfg.Steps[i].Timeout == 0 && resolvedRole.Timeout > 0 {
			pipelineCfg.Steps[i].Timeout = resolvedRole.Timeout
		}
		pipelineCfg.Steps[i].ResolvedSkills = mergeSkills(resolvedRole.Skills, baseSpec.Skills)
		roleSystemPrompt, err := loadSystemPromptFile(resolvedRole.SourceDir, resolvedRole.SystemPromptFile)
		if err != nil {
			return nil, fmt.Errorf("resolve pipeline step[%d] system prompt: %w", i, err)
		}
		pipelineCfg.Steps[i].ResolvedSystemPrompt = prependSystemPrompt(roleSystemPrompt, baseSpec.SystemPrompt)
	}

	pipelineArtifactDir := filepath.Join(baseSpec.ArtifactDir, "pipeline")
	if err := dispatch.EnsureArtifactDir(pipelineArtifactDir); err != nil {
		return nil, fmt.Errorf("create pipeline artifact dir: %w", err)
	}

	dispatchFn := func(ctx context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		if denied, matched := checkPromptDenied(spec, hookEval); denied {
			code, msg, suggestion := promptDeniedFailure(matched)
			result := buildFailedResult(spec, code, msg, suggestion)
			if result != nil && result.Metadata != nil {
				result.Metadata.PipelineID = spec.PipelineID
				result.Metadata.ParentDispatchID = spec.ParentDispatchID
			}
			return result
		}

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
		"variant",
		"profile",
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
		models["gemini"] = []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview", "gemini-3.1-pro-preview"}
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
