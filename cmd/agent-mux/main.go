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

const version = "agent-mux v3.4.1"
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
	engine, profile, cwd, model, effort, systemPrompt, systemPromptFile string
	contextFile, artifactDir, promptFile, recover                       string
	signal                                                              string
	permissionMode, sandbox, reasoning                                  string
	timeout, maxDepth, maxTurns                                         int
	full, noFull, skipSkills, stdin, version, verbose, yes, stream, async bool
	skills, addDirs                                                      stringSlice
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

	// Pre-parse: catch anti-pattern verbs (kill, cancel, stop, terminate, signal)
	// that would otherwise fall through to `dispatch` and become the PROMPT of a
	// brand-new worker. Historical incident: `agent-mux kill <id>` allocated a
	// Codex worker that burned 18.5K tokens answering the literal prompt "kill".
	if !explicitCommand {
		if msg, hint, ok := detectAntiPatternVerb(args); ok {
			emitResult(stdout, buildFailedResult(&types.DispatchSpec{}, "unknown_command", msg, hint))
			return 2
		}
	}

	// Pre-parse: catch single-dash long-form flags (e.g. -profile, -effort)
	// that Go's flag parser would silently misparse into short flags.
	if suggestion, ok := detectMisdashedFlag(args); ok {
		emitResult(stdout, buildFailedResult(&types.DispatchSpec{}, "invalid_args", suggestion, "Use double-dash (--flag) for long flags, or the short alias (-X) shown above."))
		return 2
	}

	var flagOutput bytes.Buffer
	stdinMode := hasEnabledStdinFlag(args)
	var (
		fs     *flag.FlagSet
		parsed *cliFlags
	)
	if stdinMode {
		fs, parsed = newStdinFlagSet("agent-mux")
	} else {
		fs, parsed = newCLIFlagSet("agent-mux")
	}
	fs.SetOutput(&flagOutput)
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
	if err := validateStringFlagValues(fs); err != nil {
		emitResult(stdout, buildFailedResult(&types.DispatchSpec{}, "invalid_args", err.Error(), ""))
		return 2
	}
	flags := *parsed
	positional := fs.Args()
	var flagsSet map[string]bool
	if !stdinMode {
		flagsSet = explicitFlags(fs)
	}
	flags.signal = strings.TrimSpace(flags.signal)
	if flags.stdin && !flagWasSet(fs, "yes", "y") {
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

	profileName := req.DispatchAnnotations.Profile
	if profileName != "" {
		coordSpec, err := config.LoadProfile(profileName)
		if err != nil {
			return failResult(spec, configFailureCode(err), err.Error(), "")
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

	// Apply hardcoded defaults.
	if spec.Effort == "" {
		spec.Effort = "high"
	}
	if spec.MaxDepth == 0 {
		spec.MaxDepth = config.MaxDepth()
	}
	if spec.TimeoutSec == 0 {
		spec.TimeoutSec = config.DefaultTimeoutSec
	}
	if spec.GraceSec == 0 {
		spec.GraceSec = spec.TimeoutSec / 2
		if spec.GraceSec < 1 {
			spec.GraceSec = 1
		}
	}
	if err := validateResolvedDispatchTimeouts(spec); err != nil {
		return failResult(spec, "invalid_input", err.Error(), "")
	}

	if spec.EngineOpts == nil {
		spec.EngineOpts = map[string]any{}
	}
	// Apply liveness defaults (hardcoded + env) when not set by dispatch spec.
	if _, ok := spec.EngineOpts["heartbeat_interval_sec"]; !ok {
		spec.EngineOpts["heartbeat_interval_sec"] = config.HeartbeatIntervalSec()
	}
	// Apply default permission mode from env if not set by CLI.
	if _, ok := spec.EngineOpts["permission-mode"]; !ok || spec.EngineOpts["permission-mode"] == "" {
		if !flagsSet["permission-mode"] {
			if pm := config.PermissionMode(); pm != "" {
				spec.EngineOpts["permission-mode"] = pm
			}
		}
	}

	if len(req.DispatchAnnotations.Skills) > 0 && !req.SkipSkills {
		skillPrompt, pathDirs, err := config.LoadSkills(req.DispatchAnnotations.Skills, spec.Cwd, profileName)
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

	hookEval := hooks.NewEvaluatorFromDirs(spec.Cwd)
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
		return runAsyncDispatch(ctx, spec, req.DispatchAnnotations, stderr, stdout, flags.verbose, flags.stream, hookEval)
	}

	// Warn when stdout is not a terminal and --async is not set.
	// This catches the common case where a dispatching agent backgrounds
	// the process and gets empty output at poll time.
	if isTerminal != nil && !isTerminal(stdout) {
		fmt.Fprintf(stderr, "agent-mux: warning: stdout is not a terminal; output may be lost if this process is backgrounded. Consider --async for background dispatch.\n")
	}

	result, err := dispatchSync(ctx, spec, req.DispatchAnnotations, stderr, flags.verbose, flags.stream, hookEval, false)
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

func validateStringFlagValues(fs *flag.FlagSet) error {
	var errs []string
	fs.Visit(func(f *flag.Flag) {
		if f.DefValue == "false" || f.DefValue == "true" {
			return
		}
		val := f.Value.String()
		if val != "" && strings.HasPrefix(val, "-") && val != "-" {
			errs = append(errs, fmt.Sprintf("flag -%s has value %q which looks like another flag — did you forget to provide a value?", f.Name, val))
		}
	})
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
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
		// Grace will be computed proportionally in the apply-defaults path.
		// Leave as 0 here; the caller fills it in after timeout is resolved.
		spec.GraceSec = 0
	} else {
		if err := validatePositiveDispatchValue("grace_sec", spec.GraceSec); err != nil {
			return err
		}
	}

	var aux struct {
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
	req.DispatchAnnotations.Profile = strings.TrimSpace(profile)
	req.SkipSkills = aux.SkipSkills
	req.RecoverDispatchID = strings.TrimSpace(aux.Recover)
	if req.DispatchAnnotations.Profile != "" {
		if err := sanitize.ValidateBasename(req.DispatchAnnotations.Profile); err != nil {
			return newInputValidationError("profile", req.DispatchAnnotations.Profile, err)
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

func newCLIFlags() *cliFlags {
	return &cliFlags{
		effort:    "",
		full:      true,
		maxDepth:  2,
		sandbox:   "danger-full-access",
		reasoning: "medium",
	}
}

func newStdinFlagSet(name string) (*flag.FlagSet, *cliFlags) {
	flags := newCLIFlags()
	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	fs.BoolVar(&flags.stdin, "stdin", false, "Read DispatchSpec JSON from stdin")
	bindBool(fs, &flags.yes, "Skip interactive confirmation for TTY dispatches", false, "yes", "y")
	bindBool(fs, &flags.verbose, "Verbose mode", false, "verbose", "v")
	fs.BoolVar(&flags.stream, "stream", false, "Stream all events to stderr (default: silent)")
	fs.BoolVar(&flags.async, "async", false, "Return immediately with dispatch ID, run worker in background")

	return fs, flags
}

func newCLIFlagSet(name string) (*flag.FlagSet, *cliFlags) {
	flags := newCLIFlags()
	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	bindStr(fs, &flags.engine, "Engine: codex, claude, gemini", "", "engine", "E")
	bindStr(fs, &flags.profile, "Profile / prompt file", "", "profile", "P")
	bindStr(fs, &flags.cwd, "Working directory", "", "cwd", "C")
	bindStr(fs, &flags.model, "Model override (engine-specific)", "", "model", "m")
	bindStr(fs, &flags.effort, "Effort: low, medium, high", flags.effort, "effort", "e")
	fs.IntVar(&flags.timeout, "timeout", 0, "Timeout seconds")
	fs.IntVar(&flags.timeout, "t", 0, "Timeout seconds")
	bindStr(fs, &flags.systemPrompt, "System prompt", "", "system-prompt", "s")
	fs.StringVar(&flags.systemPromptFile, "system-prompt-file", "", "System prompt file")
	fs.Var(&flags.skills, "skill", "Skill name (repeatable)")
	fs.StringVar(&flags.contextFile, "context-file", "", "Context file to prepend (for pipeline handoffs)")
	fs.StringVar(&flags.artifactDir, "artifact-dir", "", "Artifact directory")
	fs.StringVar(&flags.recover, "recover", "", "Previous dispatch ID to continue")
	fs.StringVar(&flags.signal, "signal", "", "Dispatch ID to send signal to")
	bindBool(fs, &flags.full, "Full access mode", flags.full, "full", "f")
	fs.BoolVar(&flags.noFull, "no-full", false, "Disable full access mode")
	fs.StringVar(&flags.promptFile, "prompt-file", "", "Read prompt from file instead of positional arg")
	fs.IntVar(&flags.maxDepth, "max-depth", flags.maxDepth, "Maximum recursive depth")
	fs.StringVar(&flags.permissionMode, "permission-mode", "", "Permission mode: default, auto_edit, yolo, plan (Gemini defaults to yolo)")
	fs.BoolVar(&flags.stdin, "stdin", false, "Read DispatchSpec JSON from stdin")
	fs.BoolVar(&flags.yes, "yes", false, "Skip interactive confirmation for TTY dispatches")
	fs.BoolVar(&flags.skipSkills, "skip-skills", false, "Skip skill injection")
	fs.BoolVar(&flags.version, "version", false, "Show version")
	fs.StringVar(&flags.sandbox, "sandbox", flags.sandbox, "Codex sandbox: danger-full-access, workspace-write")
	bindStr(fs, &flags.reasoning, "Reasoning effort: low, medium, high", flags.reasoning, "reasoning", "r")
	fs.IntVar(&flags.maxTurns, "max-turns", 0, "Maximum turns")
	fs.Var(&flags.addDirs, "add-dir", "Additional writable directory")
	bindBool(fs, &flags.verbose, "Verbose mode", false, "verbose", "v")
	bindBool(fs, &flags.stream, "Stream all events to stderr (default: silent)", false, "stream", "S")
	fs.BoolVar(&flags.async, "async", false, "Return immediately with dispatch ID, run worker in background")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: agent-mux [dispatch] [flags] <prompt>\n\n")
		fmt.Fprintf(fs.Output(), "Example:\n  agent-mux -P=auditor -E=codex -e=high -C=/repo \"Your prompt here\"\n\n")
		fmt.Fprintf(fs.Output(), "Engines: codex, claude, gemini\n\n")
		fmt.Fprintf(fs.Output(), "Note: use double-dash for long flags (--engine) or short alias (-E).\n")
		fmt.Fprintf(fs.Output(), "The flag listing below shows Go's single-dash format, but both work.\n\n")
		fmt.Fprintf(fs.Output(), "Flags:\n")
		fs.PrintDefaults()
	}

	return fs, flags
}

func newFlagSet(stderr io.Writer) (*flag.FlagSet, *cliFlags) {
	fs, flags := newCLIFlagSet("agent-mux")
	fs.SetOutput(stderr)
	return fs, flags
}

func buildDispatchSpecE(flags cliFlags, args []string) (*dispatchRequest, error) {
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
		GraceSec:     0, // computed proportionally after timeout is resolved
		MaxDepth:     flags.maxDepth,
		EngineOpts:   engineOpts,
		FullAccess:   fullAccess,
	}

	return &dispatchRequest{
		DispatchSpec: spec,
		DispatchAnnotations: types.DispatchAnnotations{
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
		"--profile", "-P",
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

func hasEnabledStdinFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		switch {
		case arg == "--stdin", arg == "-stdin":
			return true
		case strings.HasPrefix(arg, "--stdin="):
			enabled, err := strconv.ParseBool(strings.TrimSpace(strings.TrimPrefix(arg, "--stdin=")))
			return err == nil && enabled
		case strings.HasPrefix(arg, "-stdin="):
			enabled, err := strconv.ParseBool(strings.TrimSpace(strings.TrimPrefix(arg, "-stdin=")))
			return err == nil && enabled
		}
	}
	return false
}

// antiPatternVerbs maps natural-language abort/control verbs that coordinators
// reach for but that agent-mux does NOT accept as top-level commands. Left
// alone, these fall through to `dispatch` and become the prompt of a new
// worker — one such incident burned 18.5K Codex tokens answering "kill".
// The fix here is minimal: refuse them up front with a clear hint pointing
// at the real abort path (`steer abort`). Promoting `abort` itself to a
// top-level verb is explicitly out of scope (tracked separately).
var antiPatternVerbs = map[string]string{
	"kill":      "Use `agent-mux steer abort <id>` to kill a running dispatch.",
	"cancel":    "Use `agent-mux steer abort <id>` to cancel a running dispatch.",
	"stop":      "Use `agent-mux steer abort <id>` to stop a running dispatch.",
	"terminate": "Use `agent-mux steer abort <id>` to terminate a running dispatch.",
	"abort":     "Use `agent-mux steer abort <id>` to abort a running dispatch.",
	"signal":    "Use `--signal <id>` (flag form) or `agent-mux steer <id> nudge|redirect` to steer a running dispatch.",
}

// detectAntiPatternVerb scans the first positional arg for a known
// anti-pattern verb. It only matches when args[0] is a bare word (no leading
// dash) so real dispatch prompts like `agent-mux "kill the retry loop"`
// still reach the normal fallthrough. Returns (message, hint, true) on hit.
func detectAntiPatternVerb(args []string) (string, string, bool) {
	if len(args) == 0 {
		return "", "", false
	}
	first := args[0]
	if first == "" || strings.HasPrefix(first, "-") {
		return "", "", false
	}
	hint, ok := antiPatternVerbs[strings.ToLower(first)]
	if !ok {
		return "", "", false
	}
	msg := fmt.Sprintf("%q is not a top-level agent-mux command.", first)
	return msg, hint, true
}

// misdashedFlags maps single-dash long-form flags that Go's flag parser would
// silently misparse (e.g. -profile → -p with value "rofile") to the correct
// double-dash form and short alias.
var misdashedFlags = map[string]struct{ long, short string }{
	"-profile":         {"--profile", "-P"},
	"-effort":          {"--effort", "-e"},
	"-engine":          {"--engine", "-E"},
	"-cwd":             {"--cwd", "-C"},
	"-model":           {"--model", "-m"},
	"-timeout":         {"--timeout", "-t"},
	"-system-prompt":   {"--system-prompt", "-s"},
	"-reasoning":       {"--reasoning", "-r"},
	"-verbose":         {"--verbose", "-v"},
	"-stream":          {"--stream", "-S"},
	"-full":            {"--full", "-f"},
	"-skill":           {"--skill", ""},
	"-context-file":    {"--context-file", ""},
	"-artifact-dir":    {"--artifact-dir", ""},
	"-recover":         {"--recover", ""},
	"-signal":          {"--signal", ""},
	"-prompt-file":     {"--prompt-file", ""},
	"-max-depth":       {"--max-depth", ""},
	"-permission-mode": {"--permission-mode", ""},
	"-sandbox":         {"--sandbox", ""},
	"-max-turns":       {"--max-turns", ""},
	"-add-dir":         {"--add-dir", ""},
	"-skip-skills":     {"--skip-skills", ""},
	"-no-full":         {"--no-full", ""},
}

// detectMisdashedFlag scans args for single-dash flags longer than 2 chars
// (excluding "--" long-form) that match known flag names. Returns a helpful
// error message and true if a misdashed flag is found.
func detectMisdashedFlag(args []string) (string, bool) {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		// Strip =value suffix for matching.
		name := arg
		if idx := strings.Index(arg, "="); idx > 0 {
			name = arg[:idx]
		}
		// Only check single-dash flags longer than 2 chars (e.g. "-profile")
		// that are NOT already double-dash (e.g. "--profile" is fine).
		if !strings.HasPrefix(name, "-") || strings.HasPrefix(name, "--") || len(name) <= 2 {
			continue
		}
		if entry, ok := misdashedFlags[name]; ok {
			hint := fmt.Sprintf("unknown flag %s; did you mean %s", name, entry.long)
			if entry.short != "" {
				hint += fmt.Sprintf(" (%s)", entry.short)
			}
			hint += "?"
			return hint, true
		}
	}
	return "", false
}

func explicitFlags(fs *flag.FlagSet) map[string]bool {
	flagsSet := make(map[string]bool)
	if fs == nil {
		return flagsSet
	}
	fs.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})
	return flagsSet
}

func flagWasSet(fs *flag.FlagSet, names ...string) bool {
	if fs == nil {
		return false
	}

	var seen bool
	fs.Visit(func(f *flag.Flag) {
		if seen {
			return
		}
		seen = slices.Contains(names, f.Name)
	})
	return seen
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

func dispatchSync(ctx context.Context, spec *types.DispatchSpec, annotations types.DispatchAnnotations, stderr io.Writer, verbose bool, stream bool, hookEval *hooks.Evaluator, detached bool) (*types.DispatchResult, error) {
	reg := adapter.NewRegistry(config.DefaultModels())

	adp, err := reg.Get(spec.Engine)
	if err != nil {
		return dispatch.BuildFailedResult(
			spec,
			"",
			dispatch.NewDispatchError("engine_not_found", fmt.Sprintf("Engine %q not found.", spec.Engine), "Valid engines: [codex, claude, gemini]"),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
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
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
			0,
		), nil
	}

	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return dispatch.BuildFailedResult(
			spec,
			"",
			dispatch.NewDispatchError("artifact_dir_unwritable", fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err), "Choose a writable --artifact-dir path."),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
			0,
		), nil
	}
	if err := dispatch.RegisterDispatchSpec(spec); err != nil {
		return dispatch.BuildFailedResult(
			spec,
			"",
			dispatch.NewDispatchError("config_error", fmt.Sprintf("Register control path for dispatch %q: %v", spec.DispatchID, err), "Ensure the control path is writable."),
			&types.DispatchActivity{FilesChanged: []string{}, FilesRead: []string{}, CommandsRun: []string{}, ToolCalls: []string{}},
			&types.DispatchMetadata{Engine: spec.Engine, Model: spec.Model, Profile: annotations.Profile, Skills: append([]string(nil), annotations.Skills...), Tokens: &types.TokenUsage{}},
			0,
		), nil
	}

	eng := engine.NewLoopEngine(adp, stderr, hookEval)
	eng.SetAnnotations(annotations)
	eng.SetVerbose(verbose)
	eng.SetDetached(detached)
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

