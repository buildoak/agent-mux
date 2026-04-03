package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Defaults.Effort != "high" {
		t.Fatalf("Defaults.Effort = %q, want %q", cfg.Defaults.Effort, "high")
	}
	if cfg.Defaults.Sandbox != "danger-full-access" {
		t.Fatalf("Defaults.Sandbox = %q, want %q", cfg.Defaults.Sandbox, "danger-full-access")
	}
	if cfg.Defaults.PermissionMode != "" {
		t.Fatalf("Defaults.PermissionMode = %q, want %q", cfg.Defaults.PermissionMode, "")
	}
	if cfg.Defaults.MaxDepth != 2 {
		t.Fatalf("Defaults.MaxDepth = %d, want %d", cfg.Defaults.MaxDepth, 2)
	}
	if cfg.Liveness.HeartbeatIntervalSec != 15 {
		t.Fatalf("Liveness.HeartbeatIntervalSec = %d, want %d", cfg.Liveness.HeartbeatIntervalSec, 15)
	}
	if cfg.Liveness.SilenceWarnSeconds != 90 {
		t.Fatalf("Liveness.SilenceWarnSeconds = %d, want %d", cfg.Liveness.SilenceWarnSeconds, 90)
	}
	if cfg.Liveness.SilenceKillSeconds != 180 {
		t.Fatalf("Liveness.SilenceKillSeconds = %d, want %d", cfg.Liveness.SilenceKillSeconds, 180)
	}
	if cfg.Timeout.Low != 120 {
		t.Fatalf("Timeout.Low = %d, want %d", cfg.Timeout.Low, 120)
	}
	if cfg.Timeout.Medium != 600 {
		t.Fatalf("Timeout.Medium = %d, want %d", cfg.Timeout.Medium, 600)
	}
	if cfg.Timeout.High != 1800 {
		t.Fatalf("Timeout.High = %d, want %d", cfg.Timeout.High, 1800)
	}
	if cfg.Timeout.XHigh != 2700 {
		t.Fatalf("Timeout.XHigh = %d, want %d", cfg.Timeout.XHigh, 2700)
	}
	if cfg.Timeout.Grace != 60 {
		t.Fatalf("Timeout.Grace = %d, want %d", cfg.Timeout.Grace, 60)
	}
}

func TestLoadFromTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
[defaults]
engine = "codex"
model = "gpt-5.4"
effort = "medium"
sandbox = "workspace-write"
permission_mode = "default"
max_depth = 5

[models]
codex = ["gpt-5.4", "gpt-5.4-mini"]

[roles.reviewer]
engine = "claude"
model = "claude-sonnet-4-6"
effort = "high"

[liveness]
heartbeat_interval_sec = 30
silence_warn_seconds = 120
silence_kill_seconds = 240

[timeout]
low = 30
medium = 300
high = 900
xhigh = 1200
grace = 15

[hooks]
deny = ["rm -rf", "curl | sh"]
warn = ["git clean -fd"]
event_deny_action = "block"
`

	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Defaults.Engine != "codex" {
		t.Fatalf("Defaults.Engine = %q, want %q", cfg.Defaults.Engine, "codex")
	}
	if cfg.Defaults.Model != "gpt-5.4" {
		t.Fatalf("Defaults.Model = %q, want %q", cfg.Defaults.Model, "gpt-5.4")
	}
	if cfg.Defaults.Effort != "medium" {
		t.Fatalf("Defaults.Effort = %q, want %q", cfg.Defaults.Effort, "medium")
	}
	if cfg.Defaults.Sandbox != "workspace-write" {
		t.Fatalf("Defaults.Sandbox = %q, want %q", cfg.Defaults.Sandbox, "workspace-write")
	}
	if cfg.Defaults.PermissionMode != "default" {
		t.Fatalf("Defaults.PermissionMode = %q, want %q", cfg.Defaults.PermissionMode, "default")
	}
	if cfg.Defaults.MaxDepth != 5 {
		t.Fatalf("Defaults.MaxDepth = %d, want %d", cfg.Defaults.MaxDepth, 5)
	}

	if got := cfg.Models["codex"]; len(got) != 2 || got[0] != "gpt-5.4" || got[1] != "gpt-5.4-mini" {
		t.Fatalf("Models[codex] = %#v, want %#v", got, []string{"gpt-5.4", "gpt-5.4-mini"})
	}

	role, ok := cfg.Roles["reviewer"]
	if !ok {
		t.Fatal("Roles[reviewer] missing")
	}
	if role.Engine != "claude" || role.Model != "claude-sonnet-4-6" || role.Effort != "high" {
		t.Fatalf("Roles[reviewer] = %#v, want engine/model/effort set", role)
	}

	if cfg.Liveness.HeartbeatIntervalSec != 30 {
		t.Fatalf("Liveness.HeartbeatIntervalSec = %d, want %d", cfg.Liveness.HeartbeatIntervalSec, 30)
	}
	if cfg.Liveness.SilenceWarnSeconds != 120 {
		t.Fatalf("Liveness.SilenceWarnSeconds = %d, want %d", cfg.Liveness.SilenceWarnSeconds, 120)
	}
	if cfg.Liveness.SilenceKillSeconds != 240 {
		t.Fatalf("Liveness.SilenceKillSeconds = %d, want %d", cfg.Liveness.SilenceKillSeconds, 240)
	}
	if cfg.Timeout.Low != 30 || cfg.Timeout.Medium != 300 || cfg.Timeout.High != 900 || cfg.Timeout.XHigh != 1200 || cfg.Timeout.Grace != 15 {
		t.Fatalf("Timeout = %#v, want low=30 medium=300 high=900 xhigh=1200 grace=15", cfg.Timeout)
	}

	if len(cfg.Hooks.Deny) != 2 || cfg.Hooks.Deny[0] != "rm -rf" || cfg.Hooks.Deny[1] != "curl | sh" {
		t.Fatalf("Hooks.Deny = %#v, want %#v", cfg.Hooks.Deny, []string{"rm -rf", "curl | sh"})
	}
	if len(cfg.Hooks.Warn) != 1 || cfg.Hooks.Warn[0] != "git clean -fd" {
		t.Fatalf("Hooks.Warn = %#v, want %#v", cfg.Hooks.Warn, []string{"git clean -fd"})
	}
	if cfg.Hooks.EventDenyAction != "block" {
		t.Fatalf("Hooks.EventDenyAction = %q, want %q", cfg.Hooks.EventDenyAction, "block")
	}
}

func TestPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	globalDir := filepath.Join(home, ".agent-mux")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll global: %v", err)
	}
	projectDir := filepath.Join(cwd, ".agent-mux")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}

	globalConfig := `
[defaults]
engine = "claude"
model = "claude-sonnet-4-6"
max_depth = 4

[timeout]
medium = 700
`
	projectConfig := `
[defaults]
model = "gpt-5.4"
max_depth = 9

[liveness]
silence_warn_seconds = 45
`

	globalPath := filepath.Join(globalDir, "config.toml")
	projectPath := filepath.Join(projectDir, "config.toml")
	if err := os.WriteFile(globalPath, []byte(strings.TrimSpace(globalConfig)), 0o644); err != nil {
		t.Fatalf("WriteFile global: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte(strings.TrimSpace(projectConfig)), 0o644); err != nil {
		t.Fatalf("WriteFile project: %v", err)
	}

	cfg, err := LoadConfig("", cwd)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Defaults.Engine != "claude" {
		t.Fatalf("Defaults.Engine = %q, want %q", cfg.Defaults.Engine, "claude")
	}
	if cfg.Defaults.Model != "gpt-5.4" {
		t.Fatalf("Defaults.Model = %q, want %q", cfg.Defaults.Model, "gpt-5.4")
	}
	if cfg.Defaults.MaxDepth != 9 {
		t.Fatalf("Defaults.MaxDepth = %d, want %d", cfg.Defaults.MaxDepth, 9)
	}
	if cfg.Defaults.Effort != "high" {
		t.Fatalf("Defaults.Effort = %q, want %q", cfg.Defaults.Effort, "high")
	}
	if cfg.Timeout.Medium != 700 {
		t.Fatalf("Timeout.Medium = %d, want %d", cfg.Timeout.Medium, 700)
	}
	if cfg.Liveness.SilenceWarnSeconds != 45 {
		t.Fatalf("Liveness.SilenceWarnSeconds = %d, want %d", cfg.Liveness.SilenceWarnSeconds, 45)
	}
	if cfg.Timeout.High != 1800 {
		t.Fatalf("Timeout.High = %d, want default %d", cfg.Timeout.High, 1800)
	}
}

func TestLoadConfigRejectsNonPositiveTimeoutValues(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "timeout low",
			content: `
[timeout]
low = 0
`,
			want: "timeout.low",
		},
		{
			name: "timeout grace",
			content: `
[timeout]
grace = -1
`,
			want: "timeout.grace",
		},
		{
			name: "role timeout",
			content: `
[roles.reviewer]
timeout = 0
`,
			want: "roles.reviewer.timeout",
		},
		{
			name: "variant timeout",
			content: `
[roles.reviewer.variants.fast]
timeout = -5
`,
			want: "roles.reviewer.variants.fast.timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "-")+".toml")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tt.content)), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", path, err)
			}

			_, err := LoadConfig(path, dir)
			if err == nil {
				t.Fatal("LoadConfig error = nil, want validation error")
			}
			if !IsValidationError(err) {
				t.Fatalf("error = %T %v, want validation error", err, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want field %q", err, tt.want)
			}
		})
	}
}

func TestPrecedenceWithLocalConfigs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	globalDir := filepath.Join(home, ".agent-mux")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll global: %v", err)
	}
	projectDir := filepath.Join(cwd, ".agent-mux")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}

	globalConfig := `
[defaults]
engine = "claude"
model = "global-model"
max_depth = 4

[roles.reviewer]
model = "global-role-model"
skills = ["global-skill"]

[timeout]
medium = 700
`
	globalLocalConfig := `
[defaults]
model = "global-local-model"

[roles.reviewer]
model = "global-local-role-model"

[timeout]
medium = 650
`
	projectConfig := `
[defaults]
model = "project-model"
max_depth = 9

[roles.reviewer]
skills = ["project-skill"]

[liveness]
silence_warn_seconds = 45
`
	projectLocalConfig := `
[defaults]
model = "project-local-model"

[roles.reviewer]
engine = "codex"
system_prompt_file = "prompts/reviewer-local.md"
`

	files := map[string]string{
		filepath.Join(globalDir, "config.toml"):        globalConfig,
		filepath.Join(globalDir, "config.local.toml"):  globalLocalConfig,
		filepath.Join(projectDir, "config.toml"):       projectConfig,
		filepath.Join(projectDir, "config.local.toml"): projectLocalConfig,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(strings.TrimSpace(content)), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}

	cfg, err := LoadConfig("", cwd)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Defaults.Engine != "claude" {
		t.Fatalf("Defaults.Engine = %q, want %q", cfg.Defaults.Engine, "claude")
	}
	if cfg.Defaults.Model != "project-local-model" {
		t.Fatalf("Defaults.Model = %q, want %q", cfg.Defaults.Model, "project-local-model")
	}
	if cfg.Defaults.MaxDepth != 9 {
		t.Fatalf("Defaults.MaxDepth = %d, want %d", cfg.Defaults.MaxDepth, 9)
	}
	if cfg.Timeout.Medium != 650 {
		t.Fatalf("Timeout.Medium = %d, want %d", cfg.Timeout.Medium, 650)
	}
	if cfg.Liveness.SilenceWarnSeconds != 45 {
		t.Fatalf("Liveness.SilenceWarnSeconds = %d, want %d", cfg.Liveness.SilenceWarnSeconds, 45)
	}

	role, ok := cfg.Roles["reviewer"]
	if !ok {
		t.Fatal("Roles[reviewer] missing")
	}
	if role.Engine != "codex" {
		t.Fatalf("Roles[reviewer].Engine = %q, want %q", role.Engine, "codex")
	}
	if role.Model != "global-local-role-model" {
		t.Fatalf("Roles[reviewer].Model = %q, want %q", role.Model, "global-local-role-model")
	}
	if !reflect.DeepEqual(role.Skills, []string{"project-skill"}) {
		t.Fatalf("Roles[reviewer].Skills = %#v, want %#v", role.Skills, []string{"project-skill"})
	}
	if role.SystemPromptFile != "prompts/reviewer-local.md" {
		t.Fatalf("Roles[reviewer].SystemPromptFile = %q, want %q", role.SystemPromptFile, "prompts/reviewer-local.md")
	}
	if role.SourceDir != projectDir {
		t.Fatalf("Roles[reviewer].SourceDir = %q, want %q", role.SourceDir, projectDir)
	}
}

// TestBackwardCompatXDGPath verifies that the old ~/.config/agent-mux/config.toml location
// is used when the new ~/.agent-mux/config.toml does not exist.
func TestBackwardCompatXDGPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	// Only create the old XDG path (no new ~/.agent-mux/config.toml).
	oldDir := filepath.Join(home, ".config", "agent-mux")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	oldPath := filepath.Join(oldDir, "config.toml")
	if err := os.WriteFile(oldPath, []byte(`[defaults]
engine = "codex"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig("", cwd)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Defaults.Engine != "codex" {
		t.Fatalf("Defaults.Engine = %q, want %q (backward compat XDG path)", cfg.Defaults.Engine, "codex")
	}
}

// TestNewGlobalPathTakesPrecedenceOverXDG verifies that the new ~/.agent-mux/config.toml
// is preferred over the old XDG path when both exist.
func TestNewGlobalPathTakesPrecedenceOverXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	// Create both paths with different values.
	newDir := filepath.Join(home, ".agent-mux")
	oldDir := filepath.Join(home, ".config", "agent-mux")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("MkdirAll new: %v", err)
	}
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("MkdirAll old: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "config.toml"), []byte("[defaults]\nengine = \"claude\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.toml"), []byte("[defaults]\nengine = \"codex\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}

	cfg, err := LoadConfig("", cwd)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Defaults.Engine != "claude" {
		t.Fatalf("Defaults.Engine = %q, want %q (new path wins)", cfg.Defaults.Engine, "claude")
	}
}

// TestExplicitConfigFileIsSoleSource verifies that --config with a .toml path
// bypasses global + project config lookup.
func TestExplicitConfigFileIsSoleSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	// Global: engine=claude, model=global-model
	globalDir := filepath.Join(home, ".agent-mux")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll global: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte("[defaults]\nengine = \"claude\"\nmodel = \"global-model\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile global: %v", err)
	}

	// Project: model=project-model
	projectDir := filepath.Join(cwd, ".agent-mux")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte("[defaults]\nmodel = \"project-model\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile project: %v", err)
	}

	// Explicit: model=explicit-model
	explicitDir := t.TempDir()
	explicitPath := filepath.Join(explicitDir, "override.toml")
	if err := os.WriteFile(explicitPath, []byte("[defaults]\nmodel = \"explicit-model\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile explicit: %v", err)
	}

	cfg, err := LoadConfig(explicitPath, cwd)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Defaults.Engine != "" {
		t.Fatalf("Defaults.Engine = %q, want empty default when --config is sole source", cfg.Defaults.Engine)
	}
	if cfg.Defaults.Model != "explicit-model" {
		t.Fatalf("Defaults.Model = %q, want %q", cfg.Defaults.Model, "explicit-model")
	}
}

// TestExplicitConfigDirectoryMode verifies that --config with a directory resolves
// to the config.toml or .agent-mux/config.toml inside it.
func TestExplicitConfigDirectoryMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("dot-agent-mux-subdir", func(t *testing.T) {
		dir := t.TempDir()
		subDir := filepath.Join(dir, ".agent-mux")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "config.toml"), []byte("[defaults]\nengine = \"codex\"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cfg, err := LoadConfig(dir, dir)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Defaults.Engine != "codex" {
			t.Fatalf("Defaults.Engine = %q, want %q", cfg.Defaults.Engine, "codex")
		}
	})

	t.Run("flat-config-toml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[defaults]\nengine = \"claude\"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cfg, err := LoadConfig(dir, dir)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Defaults.Engine != "claude" {
			t.Fatalf("Defaults.Engine = %q, want %q", cfg.Defaults.Engine, "claude")
		}
	})

	t.Run("is-agent-mux-dir-itself", func(t *testing.T) {
		parent := t.TempDir()
		agentMuxDir := filepath.Join(parent, ".agent-mux")
		if err := os.MkdirAll(agentMuxDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(agentMuxDir, "config.toml"), []byte("[defaults]\nengine = \"codex\"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		// Pass the .agent-mux dir itself as --config.
		cfg, err := LoadConfig(agentMuxDir, parent)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if cfg.Defaults.Engine != "codex" {
			t.Fatalf("Defaults.Engine = %q, want %q", cfg.Defaults.Engine, "codex")
		}
	})

	t.Run("empty-dir-errors", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadConfig(dir, dir)
		if err == nil {
			t.Fatal("LoadConfig(empty dir) error = nil, want error")
		}
		if !strings.Contains(err.Error(), "no config.toml") {
			t.Fatalf("error = %q, want 'no config.toml' message", err.Error())
		}
	})
}

func TestModelOverridesAreAdditiveUnion(t *testing.T) {
	base := DefaultConfig()
	base.Models["codex"] = []string{"old-a", "old-b"}
	overlay := &Config{Models: map[string][]string{"codex": {"new-a", "old-b"}}}

	mergeConfig(base, overlay)

	got := base.Models["codex"]
	want := []string{"old-a", "old-b", "new-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Models[codex] = %#v, want %#v (union, deduplicated)", got, want)
	}
}

func TestRoleResolution(t *testing.T) {
	cfg := &Config{
		Roles: map[string]RoleConfig{
			"builder": {
				Engine: "codex",
				Model:  "gpt-5.4",
				Effort: "high",
			},
			"reviewer": {
				Engine: "claude",
				Model:  "claude-sonnet-4-6",
				Effort: "medium",
			},
		},
	}

	role, err := ResolveRole(cfg, "builder")
	if err != nil {
		t.Fatalf("ResolveRole(builder): %v", err)
	}
	if role.Engine != "codex" || role.Model != "gpt-5.4" || role.Effort != "high" {
		t.Fatalf("resolved role = %#v, want builder config", role)
	}

	_, err = ResolveRole(cfg, "missing")
	if err == nil {
		t.Fatal("ResolveRole(missing) error = nil, want error")
	}
	if !strings.Contains(err.Error(), `Available roles: [builder reviewer]`) {
		t.Fatalf("ResolveRole(missing) error = %q, want available roles list", err)
	}
}

func TestRoleConfigSkillsRoundTrip(t *testing.T) {
	role := RoleConfig{
		Engine:           "codex",
		Model:            "gpt-5.4",
		Effort:           "medium",
		Timeout:          1800,
		Skills:           []string{"web-search", "pratchett-read"},
		SystemPromptFile: "prompts/reviewer.md",
		Variants: map[string]RoleVariant{
			"claude": {
				Engine:           "claude",
				Model:            "claude-sonnet-4-6",
				SystemPromptFile: "prompts/reviewer-claude.md",
			},
		},
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(role); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var decoded RoleConfig
	if _, err := toml.Decode(buf.String(), &decoded); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !reflect.DeepEqual(decoded, role) {
		t.Fatalf("decoded role = %#v, want %#v", decoded, role)
	}
}

func TestLoadConfigSetsRoleSourceDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[roles.lifter]\nengine = \"codex\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Roles["lifter"].SourceDir; got != dir {
		t.Fatalf("SourceDir = %q, want %q", got, dir)
	}
}

func TestMergeConfigDeepMergesRolesAcrossFiles(t *testing.T) {
	base := DefaultConfig()
	base.meta = &toml.MetaData{}
	base.Roles["lifter"] = RoleConfig{
		Engine:           "codex",
		Model:            "gpt-5.4",
		Effort:           "high",
		Timeout:          1800,
		Skills:           []string{"pratchett-read"},
		SystemPromptFile: "prompts/lifter.md",
		SourceDir:        "/base",
	}

	var overlay Config
	meta, err := toml.Decode(`
[roles.lifter.variants.claude]
engine = "claude"
model = "claude-sonnet-4-6"
system_prompt_file = "prompts/lifter-claude.md"
`, &overlay)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	overlay.meta = &meta
	role := overlay.Roles["lifter"]
	role.SourceDir = "/overlay"
	overlay.Roles["lifter"] = role

	mergeConfig(base, &overlay)

	got := base.Roles["lifter"]
	if got.Engine != "codex" || got.Model != "gpt-5.4" || got.Effort != "high" || got.Timeout != 1800 {
		t.Fatalf("merged base fields = %#v, want original base fields preserved", got)
	}
	if got.SystemPromptFile != "prompts/lifter.md" {
		t.Fatalf("SystemPromptFile = %q, want %q", got.SystemPromptFile, "prompts/lifter.md")
	}
	if got.SourceDir != "/overlay" {
		t.Fatalf("SourceDir = %q, want %q", got.SourceDir, "/overlay")
	}
	variant, ok := got.Variants["claude"]
	if !ok {
		t.Fatal("Variants[claude] missing")
	}
	if variant.Engine != "claude" || variant.Model != "claude-sonnet-4-6" || variant.SystemPromptFile != "prompts/lifter-claude.md" {
		t.Fatalf("variant = %#v, want overlay variant fields", variant)
	}
}

func TestLoadConfigRejectsNegativeTimeoutValues(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "negative low",
			content: `
[timeout]
low = -10
`,
			want: "timeout.low",
		},
		{
			name: "negative medium",
			content: `
[timeout]
medium = -1
`,
			want: "timeout.medium",
		},
		{
			name: "negative high",
			content: `
[timeout]
high = -100
`,
			want: "timeout.high",
		},
		{
			name: "negative xhigh",
			content: `
[timeout]
xhigh = -5
`,
			want: "timeout.xhigh",
		},
		{
			name: "negative role timeout",
			content: `
[roles.slow]
timeout = -30
`,
			want: "roles.slow.timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, "neg-"+strings.ReplaceAll(tt.name, " ", "-")+".toml")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tt.content)), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", path, err)
			}

			_, err := LoadConfig(path, dir)
			if err == nil {
				t.Fatal("LoadConfig error = nil, want validation error")
			}
			if !IsValidationError(err) {
				t.Fatalf("error = %T %v, want ValidationError", err, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want field %q", err, tt.want)
			}
		})
	}
}

func TestTimeoutForEffort(t *testing.T) {
	cfg := &Config{
		Timeout: TimeoutConfig{
			Low:    10,
			Medium: 20,
			High:   30,
			XHigh:  40,
		},
	}

	if got := TimeoutForEffort(cfg, "low"); got != 10 {
		t.Fatalf("TimeoutForEffort(low) = %d, want %d", got, 10)
	}
	if got := TimeoutForEffort(cfg, "medium"); got != 20 {
		t.Fatalf("TimeoutForEffort(medium) = %d, want %d", got, 20)
	}
	if got := TimeoutForEffort(cfg, "high"); got != 30 {
		t.Fatalf("TimeoutForEffort(high) = %d, want %d", got, 30)
	}
	if got := TimeoutForEffort(cfg, "xhigh"); got != 40 {
		t.Fatalf("TimeoutForEffort(xhigh) = %d, want %d", got, 40)
	}
	if got := TimeoutForEffort(cfg, "unknown"); got != 30 {
		t.Fatalf("TimeoutForEffort(unknown) = %d, want fallback %d", got, 30)
	}
}

func TestAsyncPollIntervalDefaults(t *testing.T) {
	// nil config returns hardcoded default.
	if got := AsyncPollInterval(nil); got != DefaultAsyncPollInterval {
		t.Fatalf("AsyncPollInterval(nil) = %v, want %v", got, DefaultAsyncPollInterval)
	}
	// Empty config returns hardcoded default.
	cfg := DefaultConfig()
	if got := AsyncPollInterval(cfg); got != DefaultAsyncPollInterval {
		t.Fatalf("AsyncPollInterval(empty) = %v, want %v", got, DefaultAsyncPollInterval)
	}
}

func TestAsyncPollIntervalFromConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Async.PollInterval = "10s"
	got := AsyncPollInterval(cfg)
	if got != 10*time.Second {
		t.Fatalf("AsyncPollInterval(10s) = %v, want 10s", got)
	}
}

func TestAsyncPollIntervalInvalidFallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Async.PollInterval = "not-a-duration"
	got := AsyncPollInterval(cfg)
	if got != DefaultAsyncPollInterval {
		t.Fatalf("AsyncPollInterval(invalid) = %v, want default %v", got, DefaultAsyncPollInterval)
	}
}

func TestAsyncPollIntervalMinimum(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Async.PollInterval = "100ms"
	got := AsyncPollInterval(cfg)
	// Sub-second values should fall back to default.
	if got != DefaultAsyncPollInterval {
		t.Fatalf("AsyncPollInterval(100ms) = %v, want default %v (sub-second rejected)", got, DefaultAsyncPollInterval)
	}
}

func TestAsyncPollIntervalMerge(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[async]\npoll_interval = \"30s\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath, "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Async.PollInterval != "30s" {
		t.Fatalf("Async.PollInterval = %q, want %q", cfg.Async.PollInterval, "30s")
	}
	got := AsyncPollInterval(cfg)
	if got != 30*time.Second {
		t.Fatalf("AsyncPollInterval = %v, want 30s", got)
	}
}
