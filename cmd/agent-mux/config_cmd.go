package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/buildoak/agent-mux/internal/config"
)

// runConfigCommand is the entry point for `agent-mux config [subcommand]`.
func runConfigCommand(args []string, stdout io.Writer) int {
	sub, rest := splitConfigSub(args)
	switch sub {
	case "roles":
		return runConfigRoles(rest, stdout)
	case "models":
		return runConfigModels(rest, stdout)
	case "skills":
		return runConfigSkills(rest, stdout)
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
	case "roles", "models", "skills":
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

	var configPath, cwd string
	var sourcesOnly bool
	fs.StringVar(&configPath, "config", "", "Override config path")
	fs.StringVar(&cwd, "cwd", "", "Working directory for project config discovery")
	fs.BoolVar(&sourcesOnly, "sources", false, "Show only the list of loaded config files")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	cfg, sources, err := config.LoadConfigWithSources(configPath, cwd)
	if err != nil {
		return emitLifecycleError(stdout, 1, "config_error", fmt.Sprintf("load config: %v", err), "")
	}

	if sourcesOnly {
		writeCompactJSON(stdout, map[string]any{
			"kind":    "config_sources",
			"sources": sources,
		})
		return 0
	}

	// Marshal the full resolved config with _sources appended.
	raw, err := configToJSONMap(cfg)
	if err != nil {
		return emitLifecycleError(stdout, 1, "internal_error", fmt.Sprintf("marshal config: %v", err), "")
	}
	raw["_sources"] = sources
	writeCompactJSON(stdout, raw)
	return 0
}

// --- agent-mux config roles ---

func runConfigRoles(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux config roles", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	var configPath, cwd string
	var jsonOutput bool
	fs.StringVar(&configPath, "config", "", "Override config path")
	fs.StringVar(&cwd, "cwd", "", "Working directory for project config discovery")
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON array")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	cfg, _, err := config.LoadConfigWithSources(configPath, cwd)
	if err != nil {
		return emitLifecycleError(stdout, 1, "config_error", fmt.Sprintf("load config: %v", err), "")
	}

	type roleEntry struct {
		Name    string `json:"name"`
		Engine  string `json:"engine"`
		Model   string `json:"model"`
		Effort  string `json:"effort"`
		Timeout int    `json:"timeout"`
		Variant string `json:"variant,omitempty"`
	}

	names := sortedKeys(cfg.Roles)
	var entries []roleEntry
	for _, name := range names {
		role := cfg.Roles[name]
		entries = append(entries, roleEntry{
			Name:    name,
			Engine:  role.Engine,
			Model:   role.Model,
			Effort:  role.Effort,
			Timeout: role.Timeout,
		})
		for _, vName := range sortedKeys(role.Variants) {
			v := role.Variants[vName]
			entries = append(entries, roleEntry{
				Name:    name,
				Engine:  coalesce(v.Engine, role.Engine),
				Model:   coalesce(v.Model, role.Model),
				Effort:  coalesce(v.Effort, role.Effort),
				Timeout: coalesceInt(v.Timeout, role.Timeout),
				Variant: vName,
			})
		}
	}

	if jsonOutput {
		writeCompactJSON(stdout, entries)
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENGINE\tMODEL\tEFFORT\tTIMEOUT")
	for _, e := range entries {
		display := e.Name
		if e.Variant != "" {
			display = fmt.Sprintf("  \u2514 %s", e.Variant)
		}
		timeout := "-"
		if e.Timeout > 0 {
			timeout = fmt.Sprintf("%ds", e.Timeout)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			display,
			dashIfEmpty(e.Engine),
			dashIfEmpty(e.Model),
			dashIfEmpty(e.Effort),
			timeout,
		)
	}
	_ = tw.Flush()
	return 0
}

// --- agent-mux config models ---

func runConfigModels(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux config models", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	var configPath, cwd string
	var jsonOutput bool
	fs.StringVar(&configPath, "config", "", "Override config path")
	fs.StringVar(&cwd, "cwd", "", "Working directory for project config discovery")
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON object")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	cfg, _, err := config.LoadConfigWithSources(configPath, cwd)
	if err != nil {
		return emitLifecycleError(stdout, 1, "config_error", fmt.Sprintf("load config: %v", err), "")
	}

	if jsonOutput {
		writeCompactJSON(stdout, cfg.Models)
		return 0
	}

	engines := sortedKeys(cfg.Models)
	for _, engine := range engines {
		models := cfg.Models[engine]
		fmt.Fprintf(stdout, "%s: %s\n", engine, strings.Join(models, ", "))
	}
	return 0
}

// --- agent-mux config skills ---

func runConfigSkills(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux config skills", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	var configPath, cwd string
	var jsonOutput bool
	fs.StringVar(&configPath, "config", "", "Override config path")
	fs.StringVar(&cwd, "cwd", "", "Working directory for project config discovery")
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON array")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}

	cfg, sources, err := config.LoadConfigWithSources(configPath, cwd)
	if err != nil {
		return emitLifecycleError(stdout, 1, "config_error", fmt.Sprintf("load config: %v", err), "")
	}

	// Determine effective cwd for skill discovery.
	effectiveCwd := cwd
	if effectiveCwd == "" {
		effectiveCwd, _ = os.Getwd()
	}

	// Determine configDir from the first loaded source (if any).
	var configDir string
	if len(sources) > 0 {
		configDir = filepath.Dir(filepath.Dir(sources[0])) // strip .agent-mux/config.toml → parent
	}

	skills := config.DiscoverSkills(effectiveCwd, configDir, cfg.Skills.SearchPaths)

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

// --- helpers ---

// configToJSONMap builds a JSON-friendly map from the Config using TOML-style
// key names so the output matches what users see in their config files.
func configToJSONMap(cfg *config.Config) (map[string]any, error) {
	defaults := map[string]any{
		"engine":          cfg.Defaults.Engine,
		"model":           cfg.Defaults.Model,
		"effort":          cfg.Defaults.Effort,
		"sandbox":         cfg.Defaults.Sandbox,
		"permission_mode": cfg.Defaults.PermissionMode,
		"max_depth":       cfg.Defaults.MaxDepth,
	}

	roles := make(map[string]any, len(cfg.Roles))
	for name, role := range cfg.Roles {
		r := map[string]any{
			"engine":  role.Engine,
			"model":   role.Model,
			"effort":  role.Effort,
			"timeout": role.Timeout,
			"skills":  role.Skills,
		}
		if role.SystemPromptFile != "" {
			r["system_prompt_file"] = role.SystemPromptFile
		}
		if len(role.Variants) > 0 {
			variants := make(map[string]any, len(role.Variants))
			for vName, v := range role.Variants {
				vm := map[string]any{
					"engine":  v.Engine,
					"model":   v.Model,
					"effort":  v.Effort,
					"timeout": v.Timeout,
					"skills":  v.Skills,
				}
				if v.SystemPromptFile != "" {
					vm["system_prompt_file"] = v.SystemPromptFile
				}
				variants[vName] = vm
			}
			r["variants"] = variants
		}
		roles[name] = r
	}

	timeout := map[string]any{
		"low":    cfg.Timeout.Low,
		"medium": cfg.Timeout.Medium,
		"high":   cfg.Timeout.High,
		"xhigh":  cfg.Timeout.XHigh,
		"grace":  cfg.Timeout.Grace,
	}

	liveness := map[string]any{
		"heartbeat_interval_sec": cfg.Liveness.HeartbeatIntervalSec,
		"silence_warn_seconds":   cfg.Liveness.SilenceWarnSeconds,
		"silence_kill_seconds":   cfg.Liveness.SilenceKillSeconds,
	}

	hooks := map[string]any{
		"deny":              cfg.Hooks.Deny,
		"warn":              cfg.Hooks.Warn,
		"event_deny_action": cfg.Hooks.EventDenyAction,
	}

	async := map[string]any{
		"poll_interval": cfg.Async.PollInterval,
	}

	m := map[string]any{
		"defaults": defaults,
		"models":   cfg.Models,
		"roles":    roles,
		"timeout":  timeout,
		"liveness": liveness,
		"hooks":    hooks,
		"async":    async,
	}
	return m, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func coalesceInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}
