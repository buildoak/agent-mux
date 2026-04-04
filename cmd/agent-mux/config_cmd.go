package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/buildoak/agent-mux/internal/config"
)

// runConfigCommand is the entry point for `agent-mux config [subcommand]`.
func runConfigCommand(args []string, stdout io.Writer) int {
	sub, rest := splitConfigSub(args)
	switch sub {
	case "skills":
		return runConfigSkills(rest, stdout)
	case "prompts":
		return runConfigPrompts(rest, stdout)
	default:
		return runConfigRoot(args, stdout)
	}
}

// splitConfigSub extracts the first positional arg if it matches a known
// config subcommand, otherwise returns "" and the original args.
func splitConfigSub(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	switch args[0] {
	case "skills", "prompts":
		return args[0], args[1:]
	default:
		return "", args
	}
}

// --- agent-mux config (root) ---

func runConfigRoot(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux config", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	var cwd string
	fs.StringVar(&cwd, "cwd", "", "Working directory for discovery")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	effectiveCwd := cwd
	if effectiveCwd == "" {
		effectiveCwd, _ = os.Getwd()
	}

	// Emit a summary of hardcoded defaults + env overrides.
	writeCompactJSON(stdout, map[string]any{
		"kind": "config_summary",
		"defaults": map[string]any{
			"effort":          "high",
			"timeout_sec":     config.DefaultTimeoutSec,
			"grace_sec":       "timeout_sec * 0.5 (proportional)",
			"max_depth":       config.MaxDepth(),
			"permission_mode": config.PermissionMode(),
		},
		"liveness": map[string]any{
			"heartbeat_interval_sec": config.HeartbeatIntervalSec(),
			"silence_warn_seconds":   config.SilenceWarnSeconds(),
			"silence_kill_seconds":   config.SilenceKillSeconds(),
		},
		"models": config.DefaultModels(),
	})
	return 0
}

// --- agent-mux config skills ---

func runConfigSkills(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux config skills", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	var cwd string
	var jsonOutput bool
	fs.StringVar(&cwd, "cwd", "", "Working directory for skill discovery")
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON array")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	effectiveCwd := cwd
	if effectiveCwd == "" {
		effectiveCwd, _ = os.Getwd()
	}

	skills := config.DiscoverSkills(effectiveCwd)

	if jsonOutput {
		writeCompactJSON(stdout, skills)
		return 0
	}

	if len(skills) == 0 {
		fmt.Fprintln(stdout, "No skills found.")
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATH\tSOURCE")
	for _, s := range skills {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Path, s.Source)
	}
	_ = tw.Flush()
	return 0
}

// --- agent-mux config prompts ---

func runConfigPrompts(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux config prompts", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON array")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	prompts := config.DiscoverPromptFiles()

	if jsonOutput {
		writeCompactJSON(stdout, prompts)
		return 0
	}

	if len(prompts) == 0 {
		fmt.Fprintln(stdout, "No prompt files found.")
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATH\tSOURCE")
	for _, p := range prompts {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Path, p.Source)
	}
	_ = tw.Flush()
	return 0
}
