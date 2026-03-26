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
	Engine string `toml:"engine"`
	Model  string `toml:"model"`
	Effort string `toml:"effort"`
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
			base.Roles[name] = role
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
	if configPath != "" {
		return []string{configPath}, nil
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	paths := make([]string, 0, 2)
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		paths = append(paths, filepath.Join(xdgConfigHome, "agent-mux", "config.toml"))
	} else if homeDir, err := os.UserHomeDir(); err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	} else {
		paths = append(paths, filepath.Join(homeDir, ".config", "agent-mux", "config.toml"))
	}
	paths = append(paths, filepath.Join(cwd, ".agent-mux.toml"))
	return paths, nil
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
