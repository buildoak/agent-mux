package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Defaults.Effort != "high" {
		t.Fatalf("Defaults.Effort = %q, want %q", cfg.Defaults.Effort, "high")
	}
	if cfg.Defaults.Sandbox != "danger-full-access" {
		t.Fatalf("Defaults.Sandbox = %q, want %q", cfg.Defaults.Sandbox, "danger-full-access")
	}
	if cfg.Defaults.PermissionMode != "bypassPermissions" {
		t.Fatalf("Defaults.PermissionMode = %q, want %q", cfg.Defaults.PermissionMode, "bypassPermissions")
	}
	if cfg.Defaults.ResponseMaxChars != 2000 {
		t.Fatalf("Defaults.ResponseMaxChars = %d, want %d", cfg.Defaults.ResponseMaxChars, 2000)
	}
	if cfg.Defaults.MaxDepth != 2 {
		t.Fatalf("Defaults.MaxDepth = %d, want %d", cfg.Defaults.MaxDepth, 2)
	}
	if !cfg.Defaults.AllowSubdispatch {
		t.Fatal("Defaults.AllowSubdispatch = false, want true")
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
response_max_chars = 0
max_depth = 5
allow_subdispatch = false

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
	if cfg.Defaults.ResponseMaxChars != 0 {
		t.Fatalf("Defaults.ResponseMaxChars = %d, want %d", cfg.Defaults.ResponseMaxChars, 0)
	}
	if cfg.Defaults.MaxDepth != 5 {
		t.Fatalf("Defaults.MaxDepth = %d, want %d", cfg.Defaults.MaxDepth, 5)
	}
	if cfg.Defaults.AllowSubdispatch {
		t.Fatal("Defaults.AllowSubdispatch = true, want false")
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
	cwd := t.TempDir()
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	globalDir := filepath.Join(xdg, "agent-mux")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
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
	projectPath := filepath.Join(cwd, ".agent-mux.toml")
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

func TestModelOverridesReplaceEarlierList(t *testing.T) {
	base := DefaultConfig()
	base.Models["codex"] = []string{"old-a", "old-b"}
	overlay := &Config{Models: map[string][]string{"codex": []string{"new-a"}}}

	mergeConfig(base, overlay)

	got := base.Models["codex"]
	if len(got) != 1 || got[0] != "new-a" {
		t.Fatalf("Models[codex] = %#v, want %#v", got, []string{"new-a"})
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
