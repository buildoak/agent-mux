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
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/event"
	"github.com/buildoak/agent-mux/internal/hooks"
	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/steer"
	"github.com/buildoak/agent-mux/internal/types"
	"github.com/oklog/ulid/v2"
	"golang.org/x/term"
)

const version = "agent-mux v3.2.0"
const contextFilePromptPreamble = "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting."

type cliCommand string

const (
	commandDispatch cliCommand = "dispatch"
	commandPreview  cliCommand = "preview"
	commandHelp     cliCommand = "help"
	commandList     cliCommand = "list"
	commandStatus   cliCommand = "status"
	commandResult   cliCommand = "result"
	commandInspect  cliCommand = "inspect"
	commandWait     cliCommand = "wait"
	commandSteer    cliCommand = "steer"
	commandConfig   cliCommand = "config"
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
	engine, role, variant, profile, cwd, model, effort, systemPrompt, systemPromptFile string
	contextFile, artifactDir, config, promptFile, recover                              string
	signal                                                                             string
	permissionMode, sandbox, reasoning                                                 string
	timeout, maxDepth, maxTurns                                                        int
	full, noFull, skipSkills, stdin, version, verbose, yes, stream, async              bool
	skills, addDirs                                                                    stringSlice
}

type previewResult struct {
	SchemaVersion        int                 `json:"schema_version"`
	Kind                 string              `json:"kind"`
	DispatchSpec         previewDispatchSpec `json:"dispatch_spec"`
	ResultMetadata       previewResultMeta   `json:"result_metadata"`
	Prompt               previewPrompt       `json:"prompt"`
	Control              previewControl      `json:"control"`
	PromptPreamble       []string            `json:"prompt_preamble"`
	Warnings             []string            `json:"warnings"`
	ConfirmationRequired bool                `json:"confirmation_required"`
}

type previewDispatchSpec struct {
	DispatchID  string `json:"dispatch_id"`
	Engine      string `json:"engine"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Cwd         string `json:"cwd"`
	ContextFile string `json:"context_file,omitempty"`
	ArtifactDir string `json:"artifact_dir"`
	TimeoutSec  int    `json:"timeout_sec,omitempty"`
	GraceSec    int    `json:"grace_sec,omitempty"`
	MaxDepth    int    `json:"max_depth,omitempty"`
	Depth       int    `json:"depth,omitempty"`
	FullAccess  bool   `json:"full_access"`
}

type previewResultMeta struct {
	Role    string   `json:"role,omitempty"`
	Variant string   `json:"variant,omitempty"`
	Profile string   `json:"profile,omitempty"`
	Skills  []string `json:"skills,omitempty"`
}

type dispatchRequest struct {
	*types.DispatchSpec
	types.DispatchAnnotations
	SkipSkills        bool
	RecoverDispatchID string
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
	case commandHelp:
		return emitTopLevelHelp(stdout)
	case commandList:
		return runListCommand(args, stdout)
	case commandStatus:
		return runStatusCommand(args, stdout)
	case commandResult:
		return runResultCommand(args, stdout)
	case commandInspect:
		return runInspectCommand(args, stdout)
	case commandWait:
		return runWaitCommand(args, stdout, stderr)
	case commandSteer:
		return runSteerCommand(args, stdout, stderr)
	case commandConfig:
		return runConfigCommand(args, stdout)
	}

	var flagOutput bytes.Buffer
	fs, parsed := newFlagSet(&flagOutput)
	err := fs.Parse(normalizeArgs(args))
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if !explicitCommand {
				return emitTopLevelHelp(stdout)
			}
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
		if len(positional) == 0 {
			emitResult(stdout, buildSignalErrorAck(flags.signal, "invalid_args", "--signal requires a message as the first positional argument", "Provide the signal message as the first positional argument."))
			return 2
		}
		resolved, err := resolveDispatchReference(flags.signal)
		if err != nil {
			if validateErr := sanitize.ValidateDispatchID(flags.signal); validateErr != nil {
				emitResult(stdout, buildSignalErrorAck(flags.signal, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", flags.signal, validateErr), "Provide a dispatch ID without path separators or traversal segments."))
				return 1
			}
			emitResult(stdout, buildSignalErrorAck(flags.signal, "recovery_failed", err.Error(), "Check the dispatch reference and control record, then retry."))
			return 1
		}
		msg := positional[0]
		if err := steer.WriteInbox(resolved.ArtifactDir, msg); err != nil {
			emitResult(stdout, buildSignalErrorAck(flags.signal, "config_error", err.Error(), "Ensure the inbox path is writable and retry."))
			return 1
		}
		emitResult(stdout, buildSignalAck(resolved.DispatchID, resolved.ArtifactDir))
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

	var req *dispatchRequest
	if flags.stdin {
		req, err = decodeStdinDispatchSpec(stdin)
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
		req, err = buildDispatchSpecE(flags, positional)
		if err != nil {
			return emitFailureResult(stdout, &types.DispatchSpec{}, 1, "invalid_args", err.Error(), "")
		}
	}
	spec := req.DispatchSpec

	if flags.stdin {
		mergeStdinCLIFlags(req, flags, flagsSet)
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

	profileName := req.DispatchAnnotations.Profile
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
		req.DispatchAnnotations.Skills = append(coordSpec.Skills, req.DispatchAnnotations.Skills...)
	}

	roleName := flags.role
	variantName := flags.variant
	if flags.stdin {
		roleName = req.DispatchAnnotations.Role
		variantName = req.DispatchAnnotations.Variant
	}
	if roleName == "" && variantName != "" {
		return failResult(spec, "invalid_args", "--variant requires --role", "")
	}
	// roleConfigDir is the directory of the config file that defined the active
	// role. It is used as a fallback skill search root when spec.Cwd does not
	// contain the skill (Bug 2 fix).
	var roleConfigDir string
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
		req.DispatchAnnotations.Skills = mergeSkills(role.Skills, req.DispatchAnnotations.Skills)
		req.DispatchAnnotations.Role = roleName
		req.DispatchAnnotations.Variant = variantName
		roleConfigDir = role.SourceDir
	}

	applyDefaults()
	if spec.Effort == "" {
		spec.Effort = "high"
	}
	if spec.MaxDepth == 0 {
		spec.MaxDepth = cfg.Defaults.MaxDepth
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
	// Per-dispatch engine_opts take precedence over config defaults.
	// Only apply config defaults when the dispatch spec doesn't set them.
	if _, ok := spec.EngineOpts["heartbeat_interval_sec"]; !ok {
		spec.EngineOpts["heartbeat_interval_sec"] = cfg.Liveness.HeartbeatIntervalSec
	}
	if _, ok := spec.EngineOpts["silence_warn_seconds"]; !ok {
		spec.EngineOpts["silence_warn_seconds"] = cfg.Liveness.SilenceWarnSeconds
	}
	if _, ok := spec.EngineOpts["silence_kill_seconds"]; !ok {
		spec.EngineOpts["silence_kill_seconds"] = cfg.Liveness.SilenceKillSeconds
	}
	// Apply default permission mode from config if not set by CLI.
	if _, ok := spec.EngineOpts["permission-mode"]; !ok || spec.EngineOpts["permission-mode"] == "" {
		if !flagsSet["permission-mode"] && cfg.Defaults.PermissionMode != "" {
			spec.EngineOpts["permission-mode"] = cfg.Defaults.PermissionMode
		}
	}

	if len(req.DispatchAnnotations.Skills) > 0 && !req.SkipSkills {
		skillPrompt, pathDirs, err := config.LoadSkills(req.DispatchAnnotations.Skills, spec.Cwd, roleConfigDir, cfg.Skills.SearchPaths, roleName)
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
	}

	recoverDispatchID := req.RecoverDispatchID
	if recoverDispatchID != "" {
		recoveryCtx, err := dispatch.RecoverDispatch(recoverDispatchID)
		if err != nil {
			return emitFailureResult(stdout, spec, 1, "recovery_failed", err.Error(), "")
		}
		spec.Prompt = dispatch.BuildRecoveryPrompt(recoveryCtx, spec.Prompt)
	}

	hookEval := hooks.NewEvaluator(cfg.Hooks)
	if denied, matched := checkPromptDenied(spec, hookEval); denied {
		code, msg, suggestion := promptDeniedFailure(matched)
		return failResult(spec, code, msg, suggestion)
	}

	preview := buildPreviewResult(req, shouldRequireConfirmation(flags.yes, stdin, stdout, stderr, isTerminal))
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

	if flags.async {
		return runAsyncDispatch(ctx, spec, req.DispatchAnnotations, cfg, stderr, stdout, flags.verbose, flags.stream, hookEval)
	}

	result, err := dispatchSpec(ctx, spec, req.DispatchAnnotations, cfg, stderr, flags.verbose, flags.stream, hookEval)
	if err != nil {
		return emitFailureResult(stdout, spec, 1, "startup_failed", err.Error(), "")
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
	return hookEval.CheckPrompt(spec.Prompt, spec.SystemPrompt)
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
		"",
		dispatch.NewDispatchError(code, msg, suggestion),
		&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Tokens: &types.TokenUsage{}},
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

func decodeStdinDispatchSpec(stdin io.Reader) (*dispatchRequest, error) {
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
	req := &dispatchRequest{DispatchSpec: &spec}
	if err := materializeStdinDispatchSpec(req, data, fields); err != nil {
		return nil, err
	}
	return req, nil
}

func materializeStdinDispatchSpec(req *dispatchRequest, data []byte, fields map[string]json.RawMessage) error {
	if req == nil || req.DispatchSpec == nil {
		return errors.New("missing DispatchSpec")
	}
	spec := req.DispatchSpec

	spec.DispatchID = strings.TrimSpace(spec.DispatchID)
	if spec.DispatchID != "" {
		if err := sanitize.ValidateDispatchID(spec.DispatchID); err != nil {
			return newInputValidationError("dispatch_id", spec.DispatchID, err)
		}
	}
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
		artifactDir, err := dispatch.DefaultArtifactDir(spec.DispatchID)
		if err != nil {
			return fmt.Errorf("default artifact dir for dispatch %q: %w", spec.DispatchID, err)
		}
		spec.ArtifactDir = filepath.ToSlash(artifactDir) + "/"
	}

	if !jsonFieldSet(fields, "full_access") {
		spec.FullAccess = true
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

	var aux struct {
		Role        string   `json:"role"`
		Variant     string   `json:"variant"`
		Profile     string   `json:"profile"`
		Coordinator string   `json:"coordinator"`
		Skills      []string `json:"skills"`
		SkipSkills  bool     `json:"skip_skills"`
		Recover     string   `json:"recover"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("decode stdin dispatch metadata: %w", err)
	}
	profile, err := resolveProfileAlias(aux.Profile, aux.Coordinator)
	if err != nil {
		return err
	}
	req.DispatchAnnotations.Role = strings.TrimSpace(aux.Role)
	req.DispatchAnnotations.Variant = strings.TrimSpace(aux.Variant)
	req.DispatchAnnotations.Profile = strings.TrimSpace(profile)
	req.SkipSkills = aux.SkipSkills
	req.RecoverDispatchID = strings.TrimSpace(aux.Recover)
	if req.DispatchAnnotations.Profile != "" {
		if err := sanitize.ValidateBasename(req.DispatchAnnotations.Profile); err != nil {
			return newInputValidationError("profile", req.DispatchAnnotations.Profile, err)
		}
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "role", value: req.DispatchAnnotations.Role},
		{label: "variant", value: req.DispatchAnnotations.Variant},
	} {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		if err := sanitize.ValidateBasename(field.value); err != nil {
			return newInputValidationError(field.label, field.value, err)
		}
	}
	for i, name := range aux.Skills {
		name = strings.TrimSpace(name)
		if err := sanitize.ValidateBasename(name); err != nil {
			return newInputValidationError("skill", name, err)
		}
		aux.Skills[i] = name
	}
	req.DispatchAnnotations.Skills = aux.Skills
	return nil
}

func validateStdinProfileAliases(fields map[string]json.RawMessage) error {
	for _, field := range []struct {
		label string
		key   string
	}{
		{label: "profile", key: "profile"},
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

func resolveProfileAlias(profile, coordinator string) (string, error) {
	profile = strings.TrimSpace(profile)
	coordinator = strings.TrimSpace(coordinator)
	switch {
	case profile == "":
		return coordinator, nil
	case coordinator == "" || coordinator == profile:
		return profile, nil
	default:
		return "", fmt.Errorf("conflicting profile values: profile=%q coordinator=%q", profile, coordinator)
	}
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
		return commandHelp, args, false
	}
	switch args[0] {
	case string(commandHelp):
		return commandHelp, args[1:], true
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
	case string(commandWait):
		return commandWait, args[1:], true
	case string(commandSteer):
		return commandSteer, args[1:], true
	case string(commandConfig):
		return commandConfig, args[1:], true
	default:
		return commandDispatch, args, false
	}
}

func buildPreviewResult(req *dispatchRequest, confirmationRequired bool) previewResult {
	spec := req.DispatchSpec
	return previewResult{
		SchemaVersion: 1,
		Kind:          "preview",
		DispatchSpec:  previewDispatchSpecFrom(spec),
		ResultMetadata: previewResultMeta{
			Role:    req.DispatchAnnotations.Role,
			Variant: req.DispatchAnnotations.Variant,
			Profile: req.DispatchAnnotations.Profile,
			Skills:  append([]string(nil), req.DispatchAnnotations.Skills...),
		},
		Prompt: previewPromptFrom(spec),
		Control: previewControl{
			ControlRecord: dispatch.ControlRecordPath(spec.DispatchID),
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
		DispatchID:  spec.DispatchID,
		Engine:      spec.Engine,
		Model:       spec.Model,
		Effort:      spec.Effort,
		Cwd:         spec.Cwd,
		ContextFile: spec.ContextFile,
		ArtifactDir: spec.ArtifactDir,
		TimeoutSec:  spec.TimeoutSec,
		GraceSec:    spec.GraceSec,
		MaxDepth:    spec.MaxDepth,
		Depth:       spec.Depth,
		FullAccess:  spec.FullAccess,
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
		"",
		dispatch.NewDispatchError("cancelled", "Dispatch cancelled at confirmation prompt before launch.", "Re-run with --yes to skip preview and confirmation."),
		&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
		&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Tokens: &types.TokenUsage{}},
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
		effort:    "",
		full:      true,
		maxDepth:  2,
		sandbox:   "danger-full-access",
		reasoning: "medium",
	}

	fs := flag.NewFlagSet("agent-mux", flag.ContinueOnError)
	fs.SetOutput(stderr)

	bindStr(fs, &flags.engine, "Engine name", "", "engine", "E")
	bindStr(fs, &flags.role, "Role", "", "role", "R")
	fs.StringVar(&flags.variant, "variant", "", "Role variant")
	fs.StringVar(&flags.profile, "profile", "", "Profile")
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
	fs.StringVar(&flags.config, "config", "", "Config path")
	bindBool(fs, &flags.full, "Full access mode", flags.full, "full", "f")
	fs.BoolVar(&flags.noFull, "no-full", false, "Disable full access mode")
	fs.StringVar(&flags.promptFile, "prompt-file", "", "Prompt file")
	fs.IntVar(&flags.maxDepth, "max-depth", flags.maxDepth, "Maximum recursive depth")
	fs.StringVar(&flags.permissionMode, "permission-mode", "", "Permission mode")
	fs.BoolVar(&flags.stdin, "stdin", false, "Read DispatchSpec JSON from stdin")
	fs.BoolVar(&flags.yes, "yes", false, "Skip interactive confirmation for TTY dispatches")
	fs.BoolVar(&flags.skipSkills, "skip-skills", false, "Skip skill injection")
	fs.BoolVar(&flags.version, "version", false, "Show version")
	fs.StringVar(&flags.sandbox, "sandbox", flags.sandbox, "Sandbox mode")
	bindStr(fs, &flags.reasoning, "Reasoning effort", flags.reasoning, "reasoning", "r")
	fs.IntVar(&flags.maxTurns, "max-turns", 0, "Maximum turns")
	fs.Var(&flags.addDirs, "add-dir", "Additional writable directory")
	bindBool(fs, &flags.verbose, "Verbose mode", false, "verbose", "v")
	bindBool(fs, &flags.stream, "Stream all events to stderr (default: silent)", false, "stream", "S")
	fs.BoolVar(&flags.async, "async", false, "Return immediately with dispatch ID, run worker in background")

	return fs, flags
}

func buildDispatchSpecE(flags cliFlags, args []string) (*dispatchRequest, error) {
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
	cwd := flags.cwd
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	artifactDir := flags.artifactDir
	if artifactDir == "" {
		artifactDirPath, err := dispatch.DefaultArtifactDir(dispatchID)
		if err != nil {
			return nil, fmt.Errorf("default artifact dir for dispatch %q: %w", dispatchID, err)
		}
		artifactDir = filepath.ToSlash(artifactDirPath) + "/"
	}

	fullAccess := flags.full
	if flags.noFull {
		fullAccess = false
	}

	engineOpts := map[string]any{
		"sandbox":         flags.sandbox,
		"reasoning":       flags.reasoning,
		"max-turns":       flags.maxTurns,
		"add-dir":         []string(flags.addDirs),
		"permission-mode": flags.permissionMode,
	}

	spec := &types.DispatchSpec{
		DispatchID:   dispatchID,
		Engine:       flags.engine,
		Model:        flags.model,
		Effort:       flags.effort,
		Prompt:       prompt,
		SystemPrompt: systemPrompt,
		Cwd:          cwd,
		ContextFile:  flags.contextFile,
		ArtifactDir:  artifactDir,
		TimeoutSec:   flags.timeout,
		GraceSec:     60,
		MaxDepth:     flags.maxDepth,
		EngineOpts:   engineOpts,
		FullAccess:   fullAccess,
	}

	return &dispatchRequest{
		DispatchSpec: spec,
		DispatchAnnotations: types.DispatchAnnotations{
			Role:    flags.role,
			Variant: flags.variant,
			Profile: flags.profile,
			Skills:  append([]string(nil), flags.skills...),
		},
		SkipSkills:        flags.skipSkills,
		RecoverDispatchID: flags.recover,
	}, nil
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
		"--config",
		"--prompt-file",
		"--max-depth",
		"--permission-mode",
		"--sandbox",
		"--reasoning", "-r",
		"--max-turns",
		"--add-dir",
		"--limit",
		"--status",
		"--older-than":
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

func dispatchSpec(ctx context.Context, spec *types.DispatchSpec, annotations types.DispatchAnnotations, cfg *config.Config, stderr io.Writer, verbose bool, stream bool, hookEval *hooks.Evaluator) (*types.DispatchResult, error) {
	reg := adapter.NewRegistry(configuredModels(cfg))

	adp, err := reg.Get(spec.Engine)
	if err != nil {
		return dispatch.BuildFailedResult(
			spec,
			"",
			dispatch.NewDispatchError("engine_not_found", fmt.Sprintf("Engine %q not found.", spec.Engine), "Valid engines: [codex, claude, gemini]"),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: annotations.Role, Variant: annotations.Variant, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
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
			"",
			dispatch.NewDispatchError("model_not_found", fmt.Sprintf("Model %q not found for engine %s.", spec.Model, spec.Engine), suggestionText),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: annotations.Role, Variant: annotations.Variant, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
			0,
		), nil
	}

	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return dispatch.BuildFailedResult(
			spec,
			"",
			dispatch.NewDispatchError("artifact_dir_unwritable", fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err), "Choose a writable --artifact-dir path."),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: annotations.Role, Variant: annotations.Variant, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
			0,
		), nil
	}
	if err := dispatch.RegisterDispatchSpec(spec); err != nil {
		return dispatch.BuildFailedResult(
			spec,
			"",
			dispatch.NewDispatchError("config_error", fmt.Sprintf("Register control path for dispatch %q: %v", spec.DispatchID, err), "Ensure the control path is writable."),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Role: annotations.Role, Variant: annotations.Variant, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
			0,
		), nil
	}

	eng := engine.NewLoopEngine(adp, stderr, hookEval)
	eng.SetAnnotations(annotations)
	eng.SetVerbose(verbose)
	switch {
	case verbose:
		eng.SetStreamMode(event.StreamVerbose)
	case stream:
		eng.SetStreamMode(event.StreamNormal)
	default:
		eng.SetStreamMode(event.StreamSilent)
	}
	return eng.Dispatch(ctx, spec)
}

// mergeStdinCLIFlags merges explicitly-set CLI flags into a DispatchSpec that
// was decoded from --stdin JSON. CLI flags take precedence over JSON fields,
// allowing callers to override specific spec fields without rewriting the JSON.
func mergeStdinCLIFlags(req *dispatchRequest, flags cliFlags, flagsSet map[string]bool) {
	spec := req.DispatchSpec
	if flagsSet["skill"] {
		req.DispatchAnnotations.Skills = append(append([]string(nil), flags.skills...), req.DispatchAnnotations.Skills...)
	}
	if flagsSet["skip-skills"] {
		req.SkipSkills = flags.skipSkills
	}
	if flagsSet["context-file"] {
		spec.ContextFile = flags.contextFile
	}
	if flagsSet["recover"] {
		req.RecoverDispatchID = flags.recover
	}
	if flagsSet["engine"] || flagsSet["E"] {
		spec.Engine = flags.engine
	}
	if flagsSet["model"] || flagsSet["m"] {
		spec.Model = flags.model
	}
	if flagsSet["effort"] || flagsSet["e"] {
		spec.Effort = flags.effort
	}
	if flagsSet["role"] || flagsSet["R"] {
		req.DispatchAnnotations.Role = flags.role
	}
	if flagsSet["variant"] {
		req.DispatchAnnotations.Variant = flags.variant
	}
	if flagsSet["profile"] {
		req.DispatchAnnotations.Profile = flags.profile
	}
	if flagsSet["cwd"] || flagsSet["C"] {
		spec.Cwd = flags.cwd
	}
	if flagsSet["timeout"] || flagsSet["t"] {
		spec.TimeoutSec = flags.timeout
	}
	if flagsSet["system-prompt"] || flagsSet["s"] {
		if spec.SystemPrompt == "" {
			spec.SystemPrompt = flags.systemPrompt
		} else {
			spec.SystemPrompt = spec.SystemPrompt + "\n\n" + flags.systemPrompt
		}
	}
	if flagsSet["system-prompt-file"] {
		data, err := os.ReadFile(flags.systemPromptFile)
		if err == nil {
			if spec.SystemPrompt == "" {
				spec.SystemPrompt = string(data)
			} else {
				spec.SystemPrompt = string(data) + "\n\n" + spec.SystemPrompt
			}
		}
	}
	if flagsSet["max-depth"] {
		spec.MaxDepth = flags.maxDepth
	}
	if flagsSet["full"] || flagsSet["f"] {
		spec.FullAccess = flags.full
	}
	if flagsSet["no-full"] && flags.noFull {
		spec.FullAccess = false
	}
	if flagsSet["artifact-dir"] {
		spec.ArtifactDir = flags.artifactDir
	}
	// Engine opts that live in the map.
	if flagsSet["sandbox"] {
		if spec.EngineOpts == nil {
			spec.EngineOpts = make(map[string]any)
		}
		spec.EngineOpts["sandbox"] = flags.sandbox
	}
	if flagsSet["reasoning"] || flagsSet["r"] {
		if spec.EngineOpts == nil {
			spec.EngineOpts = make(map[string]any)
		}
		spec.EngineOpts["reasoning"] = flags.reasoning
	}
	if flagsSet["max-turns"] {
		if spec.EngineOpts == nil {
			spec.EngineOpts = make(map[string]any)
		}
		spec.EngineOpts["max-turns"] = flags.maxTurns
	}
	if flagsSet["permission-mode"] {
		if spec.EngineOpts == nil {
			spec.EngineOpts = make(map[string]any)
		}
		spec.EngineOpts["permission-mode"] = flags.permissionMode
	}
	if flagsSet["add-dir"] {
		if spec.EngineOpts == nil {
			spec.EngineOpts = make(map[string]any)
		}
		existing := anySliceOrEmpty(spec.EngineOpts["add-dir"])
		spec.EngineOpts["add-dir"] = append([]string(flags.addDirs), existing...)
	}
}

func stdinDispatchFlagsSet(flagsSet map[string]bool) bool {
	for _, name := range []string{
		"engine", "E",
		"role", "R",
		"variant",
		"profile",
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
		"config",
		"full", "f",
		"no-full",
		"prompt-file",
		"max-depth",
		"permission-mode",
		"sandbox",
		"reasoning", "r",
		"max-turns",
		"add-dir",
		"stream", "S",
		"async",
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
