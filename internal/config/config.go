package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Defaults DefaultsConfig        `toml:"defaults"`
	Skills   SkillsConfig          `toml:"skills"`
	Models   map[string][]string   `toml:"models"`
	Roles    map[string]RoleConfig `toml:"roles"`
	Liveness LivenessConfig        `toml:"liveness"`
	Timeout  TimeoutConfig         `toml:"timeout"`
	Hooks    HooksConfig           `toml:"hooks"`
	Async    AsyncConfig           `toml:"async"`

	meta *toml.MetaData
}

type SkillsConfig struct {
	SearchPaths []string `toml:"search_paths"`
}

type DefaultsConfig struct {
	Engine         string `toml:"engine"`
	Model          string `toml:"model"`
	Effort         string `toml:"effort"`
	Sandbox        string `toml:"sandbox"`
	PermissionMode string `toml:"permission_mode"`
	MaxDepth       int    `toml:"max_depth"`
}

type RoleConfig struct {
	Engine           string                 `toml:"engine"`
	Model            string                 `toml:"model"`
	Effort           string                 `toml:"effort"`
	Timeout          int                    `toml:"timeout"`
	Skills           []string               `toml:"skills"`
	SystemPromptFile string                 `toml:"system_prompt_file"`
	Variants         map[string]RoleVariant `toml:"variants"`
	SourceDir        string                 `toml:"-"`
}

type RoleVariant struct {
	Engine           string   `toml:"engine"`
	Model            string   `toml:"model"`
	Effort           string   `toml:"effort"`
	Timeout          int      `toml:"timeout"`
	Skills           []string `toml:"skills"`
	SystemPromptFile string   `toml:"system_prompt_file"`
}

type LivenessConfig struct {
	HeartbeatIntervalSec int `toml:"heartbeat_interval_sec"`
	SilenceWarnSeconds   int `toml:"silence_warn_seconds"`
	SilenceKillSeconds   int `toml:"silence_kill_seconds"`
}

type TimeoutConfig struct {
	Low    int `toml:"low"`
	Medium int `toml:"medium"`
	High   int `toml:"high"`
	XHigh  int `toml:"xhigh"`
	Grace  int `toml:"grace"`
}

type HooksConfig struct {
	Deny            []string `toml:"deny"`
	Warn            []string `toml:"warn"`
	EventDenyAction string   `toml:"event_deny_action"`
}

type AsyncConfig struct {
	PollInterval string `toml:"poll_interval"`
}

type ValidationError struct {
	Field  string
	Source string
	Value  int
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Source != "" {
		return fmt.Sprintf("invalid %s in %q: must be > 0 (got %d)", e.Field, e.Source, e.Value)
	}
	return fmt.Sprintf("invalid %s: must be > 0 (got %d)", e.Field, e.Value)
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

func DefaultConfig() *Config {
	return &Config{
		Defaults: DefaultsConfig{
			Effort:         "high",
			Sandbox:        "danger-full-access",
			PermissionMode: "",
			MaxDepth:       2,
		},
		Models: make(map[string][]string),
		Roles:  make(map[string]RoleConfig),
		Liveness: LivenessConfig{
			HeartbeatIntervalSec: 15,
			SilenceWarnSeconds:   90,
			SilenceKillSeconds:   180,
		},
		Timeout: TimeoutConfig{
			Low:    120,
			Medium: 600,
			High:   1800,
			XHigh:  2700,
			Grace:  60,
		},
	}
}

func LoadConfig(configPath string, cwd string) (*Config, error) {
	cfg, _, err := LoadConfigWithSources(configPath, cwd)
	return cfg, err
}

// LoadConfigWithSources loads the resolved config and returns the ordered list
// of config file paths that were actually found and loaded.
func LoadConfigWithSources(configPath string, cwd string) (*Config, []string, error) {
	paths, err := configPaths(configPath, cwd)
	if err != nil {
		return nil, nil, err
	}

	cfg := DefaultConfig()
	var loaded []string
	for _, path := range paths {
		if path == "" {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("stat config %q: %w", path, err)
		}
		if info.IsDir() {
			return nil, nil, fmt.Errorf("config path %q is a directory", path)
		}

		var overlay Config
		meta, err := toml.DecodeFile(path, &overlay)
		if err != nil {
			return nil, nil, fmt.Errorf("decode config %q: %w", path, err)
		}
		overlay.meta = &meta
		for name, role := range overlay.Roles {
			role.SourceDir = filepath.Dir(path)
			overlay.Roles[name] = role
		}
		if err := validateExplicitTimeoutValues(path, &overlay); err != nil {
			return nil, nil, err
		}
		mergeConfig(cfg, &overlay)
		loaded = append(loaded, path)
	}

	return cfg, loaded, nil
}

func MergeConfigInto(base, overlay *Config) {
	mergeConfig(base, overlay)
}

func mergeConfig(base, overlay *Config) {
	if base == nil || overlay == nil {
		return
	}

	merge(&base.Defaults.Engine, overlay.Defaults.Engine, overlay.defined("defaults", "engine"))
	merge(&base.Defaults.Model, overlay.Defaults.Model, overlay.defined("defaults", "model"))
	merge(&base.Defaults.Effort, overlay.Defaults.Effort, overlay.defined("defaults", "effort"))
	merge(&base.Defaults.Sandbox, overlay.Defaults.Sandbox, overlay.defined("defaults", "sandbox"))
	merge(&base.Defaults.PermissionMode, overlay.Defaults.PermissionMode, overlay.defined("defaults", "permission_mode"))
	merge(&base.Defaults.MaxDepth, overlay.Defaults.MaxDepth, overlay.defined("defaults", "max_depth"))

	if overlay.defined("skills", "search_paths") || len(overlay.Skills.SearchPaths) > 0 {
		base.Skills.SearchPaths = deduplicateStrings(append(base.Skills.SearchPaths, overlay.Skills.SearchPaths...))
	}

	if len(overlay.Models) > 0 {
		if base.Models == nil {
			base.Models = make(map[string][]string, len(overlay.Models))
		}
		for engine, models := range overlay.Models {
			base.Models[engine] = deduplicateStrings(append(base.Models[engine], models...))
		}
	}

	if len(overlay.Roles) > 0 {
		if base.Roles == nil {
			base.Roles = make(map[string]RoleConfig, len(overlay.Roles))
		}
		for name, role := range overlay.Roles {
			existing, ok := base.Roles[name]
			if !ok {
				base.Roles[name] = cloneRoleConfig(role)
				continue
			}
			base.Roles[name] = mergeRoleConfig(existing, role, overlay, name)
		}
	}
	merge(&base.Liveness.HeartbeatIntervalSec, overlay.Liveness.HeartbeatIntervalSec, overlay.defined("liveness", "heartbeat_interval_sec"))
	merge(&base.Liveness.SilenceWarnSeconds, overlay.Liveness.SilenceWarnSeconds, overlay.defined("liveness", "silence_warn_seconds"))
	merge(&base.Liveness.SilenceKillSeconds, overlay.Liveness.SilenceKillSeconds, overlay.defined("liveness", "silence_kill_seconds"))
	merge(&base.Timeout.Low, overlay.Timeout.Low, overlay.defined("timeout", "low"))
	merge(&base.Timeout.Medium, overlay.Timeout.Medium, overlay.defined("timeout", "medium"))
	merge(&base.Timeout.High, overlay.Timeout.High, overlay.defined("timeout", "high"))
	merge(&base.Timeout.XHigh, overlay.Timeout.XHigh, overlay.defined("timeout", "xhigh"))
	merge(&base.Timeout.Grace, overlay.Timeout.Grace, overlay.defined("timeout", "grace"))

	if overlay.defined("hooks", "deny") || len(overlay.Hooks.Deny) > 0 {
		base.Hooks.Deny = deduplicateStrings(append(base.Hooks.Deny, overlay.Hooks.Deny...))
	}
	if overlay.defined("hooks", "warn") || len(overlay.Hooks.Warn) > 0 {
		base.Hooks.Warn = deduplicateStrings(append(base.Hooks.Warn, overlay.Hooks.Warn...))
	}
	merge(&base.Hooks.EventDenyAction, overlay.Hooks.EventDenyAction, overlay.defined("hooks", "event_deny_action"))

	merge(&base.Async.PollInterval, overlay.Async.PollInterval, overlay.defined("async", "poll_interval"))
}

func ResolveRole(cfg *Config, roleName string) (*RoleConfig, error) {
	if cfg != nil {
		if role, ok := cfg.Roles[roleName]; ok {
			resolved := role
			return &resolved, nil
		}
	}

	var available []string
	if cfg != nil {
		available = make([]string, 0, len(cfg.Roles))
		for name := range cfg.Roles {
			available = append(available, name)
		}
		sort.Strings(available)
	}

	return nil, fmt.Errorf("role %q not found. Available roles: %v", roleName, available)
}

func TimeoutForEffort(cfg *Config, effort string) int {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return cfg.Timeout.Low
	case "medium":
		return cfg.Timeout.Medium
	case "high":
		return cfg.Timeout.High
	case "xhigh":
		return cfg.Timeout.XHigh
	default:
		return cfg.Timeout.High
	}
}

// DefaultAsyncPollInterval is the hardcoded default when neither CLI flag
// nor config.toml provides a value.
const DefaultAsyncPollInterval = 60 * time.Second

// AsyncPollInterval returns the poll interval from [async].poll_interval in
// config.toml, falling back to DefaultAsyncPollInterval if unset or invalid.
func AsyncPollInterval(cfg *Config) time.Duration {
	if cfg == nil {
		return DefaultAsyncPollInterval
	}
	raw := strings.TrimSpace(cfg.Async.PollInterval)
	if raw == "" {
		return DefaultAsyncPollInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 1*time.Second {
		return DefaultAsyncPollInterval
	}
	return d
}

func configPaths(configPath string, cwd string) ([]string, error) {
	// Explicit --config is the sole source; skip implicit global/project lookup.
	if configPath != "" {
		return resolveExplicitConfigPath(configPath)
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	// Absolutize cwd so that config discovery works regardless of
	// whether --cwd was passed as a relative or absolute path.
	if !filepath.IsAbs(cwd) {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return nil, fmt.Errorf("resolve absolute cwd %q: %w", cwd, err)
		}
		cwd = abs
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	paths := make([]string, 0, 4)

	// 1. Global config: ~/.agent-mux/config.toml (new canonical location).
	// 2. Global machine-local config: ~/.agent-mux/config.local.toml
	newGlobal := filepath.Join(homeDir, ".agent-mux", "config.toml")
	newGlobalLocal := filepath.Join(homeDir, ".agent-mux", "config.local.toml")
	oldGlobal := filepath.Join(homeDir, ".config", "agent-mux", "config.toml")

	if _, err := os.Stat(newGlobal); err == nil {
		// New location exists — use it.
		paths = append(paths, newGlobal, newGlobalLocal)
	} else if os.IsNotExist(err) {
		// New location absent — check old XDG location for backward compat.
		if _, statErr := os.Stat(oldGlobal); statErr == nil {
			fmt.Fprintf(os.Stderr, "agent-mux: deprecation: config found at %q; migrate to %q\n", oldGlobal, newGlobal)
			paths = append(paths, oldGlobal)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("stat global config %q: %w", oldGlobal, statErr)
		}
		paths = append(paths, newGlobalLocal)
	} else {
		return nil, fmt.Errorf("stat global config %q: %w", newGlobal, err)
	}

	// 3. Project config: <cwd>/.agent-mux/config.toml
	// 4. Project machine-local config: <cwd>/.agent-mux/config.local.toml
	paths = append(paths,
		filepath.Join(cwd, ".agent-mux", "config.toml"),
		filepath.Join(cwd, ".agent-mux", "config.local.toml"),
	)

	return paths, nil
}

// resolveExplicitConfigPath resolves an explicit --config path to a toml file.
// - If the path ends in .toml → load directly.
// - If the path is a directory → look for .agent-mux/config.toml, then config.toml inside it.
func resolveExplicitConfigPath(configPath string) ([]string, error) {
	info, err := os.Stat(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config path %q does not exist", configPath)
		}
		return nil, fmt.Errorf("stat config %q: %w", configPath, err)
	}

	if !info.IsDir() {
		// Direct file path — use as-is.
		return []string{configPath}, nil
	}

	// Directory: try <dir>/.agent-mux/config.toml then <dir>/config.toml.
	candidates := []string{
		filepath.Join(configPath, ".agent-mux", "config.toml"),
		filepath.Join(configPath, "config.toml"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return []string{candidate}, nil
		}
	}
	return nil, fmt.Errorf("config directory %q contains no config.toml or .agent-mux/config.toml", configPath)
}

func (c *Config) defined(path ...string) bool {
	return c != nil && c.meta != nil && c.meta.IsDefined(path...)
}

func merge[T comparable](dst *T, value T, defined bool) {
	var zero T
	if defined || value != zero {
		*dst = value
	}
}

func cloneRoleConfig(role RoleConfig) RoleConfig {
	cloned := role
	cloned.Skills = append([]string(nil), role.Skills...)
	if len(role.Variants) > 0 {
		cloned.Variants = make(map[string]RoleVariant, len(role.Variants))
		for name, variant := range role.Variants {
			cloned.Variants[name] = cloneRoleVariant(variant)
		}
	}
	return cloned
}

func cloneRoleVariant(variant RoleVariant) RoleVariant {
	cloned := variant
	cloned.Skills = append([]string(nil), variant.Skills...)
	return cloned
}

func mergeRoleConfig(baseRole, overlayRole RoleConfig, overlay *Config, roleName string) RoleConfig {
	merged := cloneRoleConfig(baseRole)

	merge(&merged.Engine, overlayRole.Engine, overlay.defined("roles", roleName, "engine"))
	merge(&merged.Model, overlayRole.Model, overlay.defined("roles", roleName, "model"))
	merge(&merged.Effort, overlayRole.Effort, overlay.defined("roles", roleName, "effort"))
	merge(&merged.Timeout, overlayRole.Timeout, overlay.defined("roles", roleName, "timeout"))
	merge(&merged.SystemPromptFile, overlayRole.SystemPromptFile, overlay.defined("roles", roleName, "system_prompt_file"))
	if overlay.defined("roles", roleName, "skills") || len(overlayRole.Skills) > 0 {
		merged.Skills = append([]string(nil), overlayRole.Skills...)
	}
	if overlayRole.SourceDir != "" {
		merged.SourceDir = overlayRole.SourceDir
	}

	if len(overlayRole.Variants) > 0 {
		if merged.Variants == nil {
			merged.Variants = make(map[string]RoleVariant, len(overlayRole.Variants))
		}
		for name, variant := range overlayRole.Variants {
			if existing, ok := merged.Variants[name]; ok {
				merged.Variants[name] = mergeRoleVariant(existing, variant, overlay, roleName, name)
				continue
			}
			merged.Variants[name] = cloneRoleVariant(variant)
		}
	}

	return merged
}

func mergeRoleVariant(baseVariant, overlayVariant RoleVariant, overlay *Config, roleName, variantName string) RoleVariant {
	merged := cloneRoleVariant(baseVariant)

	merge(&merged.Engine, overlayVariant.Engine, overlay.defined("roles", roleName, "variants", variantName, "engine"))
	merge(&merged.Model, overlayVariant.Model, overlay.defined("roles", roleName, "variants", variantName, "model"))
	merge(&merged.Effort, overlayVariant.Effort, overlay.defined("roles", roleName, "variants", variantName, "effort"))
	merge(&merged.Timeout, overlayVariant.Timeout, overlay.defined("roles", roleName, "variants", variantName, "timeout"))
	merge(&merged.SystemPromptFile, overlayVariant.SystemPromptFile, overlay.defined("roles", roleName, "variants", variantName, "system_prompt_file"))
	if overlay.defined("roles", roleName, "variants", variantName, "skills") || len(overlayVariant.Skills) > 0 {
		merged.Skills = append([]string(nil), overlayVariant.Skills...)
	}

	return merged
}

func validateExplicitTimeoutValues(source string, cfg *Config) error {
	if cfg == nil {
		return nil
	}

	for _, field := range []struct {
		name    string
		value   int
		defined bool
	}{
		{name: "timeout.low", value: cfg.Timeout.Low, defined: cfg.defined("timeout", "low")},
		{name: "timeout.medium", value: cfg.Timeout.Medium, defined: cfg.defined("timeout", "medium")},
		{name: "timeout.high", value: cfg.Timeout.High, defined: cfg.defined("timeout", "high")},
		{name: "timeout.xhigh", value: cfg.Timeout.XHigh, defined: cfg.defined("timeout", "xhigh")},
		{name: "timeout.grace", value: cfg.Timeout.Grace, defined: cfg.defined("timeout", "grace")},
	} {
		if !field.defined {
			continue
		}
		if err := validatePositiveInt(field.name, source, field.value); err != nil {
			return err
		}
	}

	for roleName, role := range cfg.Roles {
		if cfg.defined("roles", roleName, "timeout") {
			if err := validatePositiveInt("roles."+roleName+".timeout", source, role.Timeout); err != nil {
				return err
			}
		}
		for variantName, variant := range role.Variants {
			if cfg.defined("roles", roleName, "variants", variantName, "timeout") {
				field := "roles." + roleName + ".variants." + variantName + ".timeout"
				if err := validatePositiveInt(field, source, variant.Timeout); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func validatePositiveInt(field, source string, value int) error {
	if value > 0 {
		return nil
	}
	return &ValidationError{
		Field:  field,
		Source: source,
		Value:  value,
	}
}

// deduplicateStrings returns a new slice with duplicate entries removed,
// preserving the order of first occurrence.
func deduplicateStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
