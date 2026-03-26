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
	"strings"
	"syscall"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/types"
	"github.com/oklog/ulid/v2"
)

const version = "agent-mux v2.0.0-dev"

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
	engine, role, cwd, model, effort, systemPrompt, systemPromptFile string
	contextFile, artifactDir, salt, config, promptFile               string
	permissionMode, sandbox, reasoning                               string
	output                                                           string
	timeout, maxDepth, responseMaxChars, maxTurns                    int
	full, noFull, noSubdispatch, stdin, version, verbose             bool
	skills, addDirs                                                  stringSlice
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs, parsed := newFlagSet(stderr)
	err := fs.Parse(args)
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

	cfg, err := config.LoadConfig(flags.config, spec.Cwd)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	if flags.role != "" {
		role, err := config.ResolveRole(cfg, flags.role)
		if err != nil {
			return failResult(spec, "config_error", err.Error(), "")
		}
		if !flagsSet["engine"] && !flagsSet["E"] && role.Engine != "" {
			spec.Engine = role.Engine
		}
		if !flagsSet["model"] && !flagsSet["m"] && role.Model != "" {
			spec.Model = role.Model
		}
		if !flagsSet["effort"] && !flagsSet["e"] && role.Effort != "" {
			spec.Effort = role.Effort
		}
	}

	if spec.Engine == "" {
		spec.Engine = cfg.Defaults.Engine
	}
	if spec.Model == "" {
		spec.Model = cfg.Defaults.Model
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

	if spec.Engine == "" {
		return failResult(spec, "invalid_args", "No engine specified.", "Use --engine codex, --engine claude, or --engine gemini.")
	}

	codexModels := cfg.Models["codex"]
	if len(codexModels) == 0 {
		codexModels = []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"}
	}
	var (
		adp         types.HarnessAdapter
		validModels []string
	)
	switch spec.Engine {
	case "codex":
		adp = &adapter.CodexAdapter{}
		validModels = codexModels
	case "claude":
		adp = &adapter.ClaudeAdapter{}
		claudeModels := cfg.Models["claude"]
		if len(claudeModels) == 0 {
			claudeModels = []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"}
		}
		validModels = claudeModels
	case "gemini":
		adp = &adapter.GeminiAdapter{}
		geminiModels := cfg.Models["gemini"]
		if len(geminiModels) == 0 {
			geminiModels = []string{"gemini-3.1-pro", "gemini-3.1-flash"}
		}
		validModels = geminiModels
	default:
		return failResult(spec, "engine_not_found", fmt.Sprintf("Engine %q not found.", spec.Engine), "Valid engines: [codex, claude, gemini]")
	}

	if spec.Model != "" && len(validModels) > 0 && !slices.Contains(validModels, spec.Model) {
		suggestion := dispatch.FuzzyMatchModel(spec.Model, validModels)
		suggestionText := fmt.Sprintf("Valid models for %s: %v", spec.Engine, validModels)
		if suggestion != "" {
			suggestionText = fmt.Sprintf("Did you mean %q? %s", suggestion, suggestionText)
		}
		return failResult(spec, "model_not_found", fmt.Sprintf("Model %q not found for engine %s.", spec.Model, spec.Engine), suggestionText)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	eng := engine.NewLoopEngine(spec.Engine, adp, validModels, stderr)
	eng.SetVerbose(flags.verbose)
	result, err := eng.Dispatch(ctx, spec)
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

func writeResult(w io.Writer, result *types.DispatchResult) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(result)
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
		effort:    "high",
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
	fs.StringVar(&flags.salt, "salt", "", "Dispatch salt")
	fs.StringVar(&flags.config, "config", "", "Config path")
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
