package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/buildoak/agent-mux/internal/pipeline"
)

type Config struct {
	Defaults  DefaultsConfig                     `toml:"defaults"`
	Models    map[string][]string                `toml:"models"`
	Roles     map[string]RoleConfig              `toml:"roles"`
	Pipelines map[string]pipeline.PipelineConfig `toml:"pipelines"`
	Liveness  LivenessConfig                     `toml:"liveness"`
	Timeout   TimeoutConfig                      `toml:"timeout"`
	Hooks     HooksConfig                        `toml:"hooks"`

	meta *toml.MetaData
}

type DefaultsConfig struct {
	Engine           string `toml:"engine"`
	Model            string `toml:"model"`
	Effort           string `toml:"effort"`
	Sandbox          string `toml:"sandbox"`
	PermissionMode   string `toml:"permission_mode"`
	ResponseMaxChars int    `toml:"response_max_chars"`
	MaxDepth         int    `toml:"max_depth"`
	AllowSubdispatch bool   `toml:"allow_subdispatch"`
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

func DefaultConfig() *Config {
	return &Config{
		Defaults: DefaultsConfig{
			Effort:           "high",
			Sandbox:          "danger-full-access",
			PermissionMode:   "",
			ResponseMaxChars: 2000,
			MaxDepth:         2,
			AllowSubdispatch: true,
		},
		Models:    make(map[string][]string),
		Roles:     make(map[string]RoleConfig),
		Pipelines: make(map[string]pipeline.PipelineConfig),
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
	paths, err := configPaths(configPath, cwd)
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	for _, path := range paths {
		if path == "" {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat config %q: %w", path, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("config path %q is a directory", path)
		}

		var overlay Config
		meta, err := toml.DecodeFile(path, &overlay)
		if err != nil {
			return nil, fmt.Errorf("decode config %q: %w", path, err)
		}
		overlay.meta = &meta
		for name, role := range overlay.Roles {
			role.SourceDir = filepath.Dir(path)
			overlay.Roles[name] = role
		}
		mergeConfig(cfg, &overlay)
	}

	return cfg, nil
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
	merge(&base.Defaults.ResponseMaxChars, overlay.Defaults.ResponseMaxChars, overlay.defined("defaults", "response_max_chars"))
	merge(&base.Defaults.MaxDepth, overlay.Defaults.MaxDepth, overlay.defined("defaults", "max_depth"))
	merge(&base.Defaults.AllowSubdispatch, overlay.Defaults.AllowSubdispatch, overlay.defined("defaults", "allow_subdispatch"))

	if len(overlay.Models) > 0 {
		if base.Models == nil {
			base.Models = make(map[string][]string, len(overlay.Models))
		}
		for engine, models := range overlay.Models {
			base.Models[engine] = append([]string(nil), models...)
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
	if len(overlay.Pipelines) > 0 {
		if base.Pipelines == nil {
			base.Pipelines = make(map[string]pipeline.PipelineConfig, len(overlay.Pipelines))
		}
		for name, cfg := range overlay.Pipelines {
			base.Pipelines[name] = cfg
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
		base.Hooks.Deny = append([]string(nil), overlay.Hooks.Deny...)
	}
	if overlay.defined("hooks", "warn") || len(overlay.Hooks.Warn) > 0 {
		base.Hooks.Warn = append([]string(nil), overlay.Hooks.Warn...)
	}
	merge(&base.Hooks.EventDenyAction, overlay.Hooks.EventDenyAction, overlay.defined("hooks", "event_deny_action"))
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

func configPaths(configPath string, cwd string) ([]string, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	paths := make([]string, 0, 3)

	// 1. Global config: ~/.agent-mux/config.toml (new canonical location).
	newGlobal := filepath.Join(homeDir, ".agent-mux", "config.toml")
	oldGlobal := filepath.Join(homeDir, ".config", "agent-mux", "config.toml")

	if _, err := os.Stat(newGlobal); err == nil {
		// New location exists — use it.
		paths = append(paths, newGlobal)
	} else if os.IsNotExist(err) {
		// New location absent — check old XDG location for backward compat.
		if _, statErr := os.Stat(oldGlobal); statErr == nil {
			fmt.Fprintf(os.Stderr, "agent-mux: deprecation: config found at %q; migrate to %q\n", oldGlobal, newGlobal)
			paths = append(paths, oldGlobal)
		}
	} else {
		return nil, fmt.Errorf("stat global config %q: %w", newGlobal, err)
	}

	// 2. Project config: <cwd>/.agent-mux/config.toml
	paths = append(paths, filepath.Join(cwd, ".agent-mux", "config.toml"))

	// 3. Explicit --config overlay (loaded last, highest precedence).
	if configPath != "" {
		explicit, err := resolveExplicitConfigPath(configPath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, explicit...)
	}

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
