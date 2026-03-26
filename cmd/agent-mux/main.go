package main

import (
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
)

const version = "agent-mux v2.0.0-dev"
const contextFilePromptPreamble = "Relevant context from the coordinator is at $AGENT_MUX_CONTEXT. Read it before starting."

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
	full, noFull, noSubdispatch, stdin, version, verbose                          bool
	skills, addDirs                                                               stringSlice
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
		artifactDir := filepath.Join("/tmp/agent-mux", flags.signal)
		if err := inbox.WriteInbox(artifactDir, msg); err != nil {
			fmt.Fprintf(stderr, "signal: %v\n", err)
			return 1
		}
		fmt.Fprintf(
			stdout,
			`{"status":"ok","dispatch_id":%q,"artifact_dir":%q,"message":"Signal delivered to inbox","note":"Signals are written to /tmp/agent-mux/<dispatch_id>/inbox.md; dispatches started with custom --artifact-dir may not receive this signal."}`,
			flags.signal,
			artifactDir,
		)
		fmt.Fprintln(stdout)
		return 0
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
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "read stdin: %v\n", err)
			return 1
		}
		var parsed types.DispatchSpec
		if err := json.Unmarshal(data, &parsed); err != nil {
			fmt.Fprintf(stderr, "parse stdin JSON: %v\n", err)
			return 1
		}
		spec = &parsed
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
		fmt.Fprintf(os.Stderr, "Warning: --stdin mode active; CLI dispatch flags are ignored.\n")
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

	if flags.coordinator != "" {
		coordSpec, companionCfg, err := config.LoadCoordinator(flags.coordinator, spec.Cwd)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		if companionCfg != nil {
			config.MergeConfigInto(cfg, companionCfg)
		}
		applyPreset(coordSpec.Engine, coordSpec.Model, coordSpec.Effort)
		if !flagsSet["timeout"] && !flagsSet["t"] && coordSpec.Timeout > 0 {
			spec.TimeoutSec = coordSpec.Timeout
		}
		if spec.SystemPrompt == "" && coordSpec.SystemPrompt != "" {
			spec.SystemPrompt = coordSpec.SystemPrompt
		}
		spec.Skills = append(coordSpec.Skills, spec.Skills...)
	}

	if flags.role != "" {
		role, err := config.ResolveRole(cfg, flags.role)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		applyPreset(role.Engine, role.Model, role.Effort)
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

	if spec.Salt == "" {
		spec.Salt = dispatch.GenerateSalt()
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

	if flags.recover != "" {
		recoveryCtx, err := recovery.RecoverDispatch(flags.recover)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		spec.ContinuesDispatchID = flags.recover
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if flags.pipeline != "" {
		pipelineCfg, ok := cfg.Pipelines[flags.pipeline]
		if !ok {
			return failResult(spec, "config_error",
				fmt.Sprintf("Pipeline %q not found in config.", flags.pipeline),
				fmt.Sprintf("Available pipelines: %v", availablePipelines(cfg)))
		}
		if err := pipeline.ValidatePipeline(pipelineCfg); err != nil {
			return failResult(spec, "config_error",
				fmt.Sprintf("Pipeline %q validation failed: %v", flags.pipeline, err), "")
		}
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
		artifactDir = filepath.ToSlash(filepath.Join("/tmp/agent-mux", dispatchID)) + "/"
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
		models["gemini"] = []string{"gemini-3.1-pro", "gemini-3.1-flash"}
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
