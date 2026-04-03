package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testConfigTOML = `
[defaults]
engine = "claude"
model = "opus-4"
effort = "high"

[models]
claude = ["opus-4", "sonnet-4"]
codex = ["codex-mini"]

[roles.scout]
engine = "codex"
model = "codex-mini"
effort = "low"
timeout = 120
skills = ["web-search"]

[roles.lifter]
engine = "claude"
model = "opus-4"
effort = "high"
timeout = 1800

`

func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	muxDir := filepath.Join(dir, ".agent-mux")
	if err := os.MkdirAll(muxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(muxDir, "config.toml")
	if err := os.WriteFile(path, []byte(testConfigTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigRoot_FullJSON(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	// Check _sources is present and contains our config path.
	sources, ok := result["_sources"]
	if !ok {
		t.Fatal("missing _sources in output")
	}
	arr, ok := sources.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("_sources should be a non-empty array, got %v", sources)
	}
	if arr[0].(string) != cfgPath {
		t.Fatalf("_sources[0] = %q, want %q", arr[0], cfgPath)
	}

	// Check defaults.engine is resolved.
	defaults, _ := result["defaults"].(map[string]any)
	if defaults["engine"] != "claude" {
		t.Fatalf("defaults.engine = %v, want \"claude\"", defaults["engine"])
	}
}

func TestConfigRoot_SourcesOnly(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"--sources", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["kind"] != "config_sources" {
		t.Fatalf("kind = %v, want config_sources", result["kind"])
	}
	sources, _ := result["sources"].([]any)
	if len(sources) != 1 || sources[0].(string) != cfgPath {
		t.Fatalf("sources = %v, want [%q]", sources, cfgPath)
	}

	// Ensure the full config is NOT present.
	if _, ok := result["defaults"]; ok {
		t.Fatal("--sources should not include the full config")
	}
}

func TestConfigRoles_Table(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"roles", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	out := stdout.String()
	// Header present.
	if !strings.Contains(out, "NAME") {
		t.Fatalf("missing table header in output:\n%s", out)
	}
	// Both roles present.
	if !strings.Contains(out, "scout") {
		t.Fatalf("missing role 'scout' in output:\n%s", out)
	}
	if !strings.Contains(out, "lifter") {
		t.Fatalf("missing role 'lifter' in output:\n%s", out)
	}
}

func TestConfigRoles_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"roles", "--json", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON array: %v\noutput: %s", err, stdout.String())
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 role entries, got %d", len(entries))
	}
}

func TestConfigModels_Text(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"models", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "claude:") {
		t.Fatalf("missing engine 'claude' in output:\n%s", out)
	}
	if !strings.Contains(out, "opus-4") {
		t.Fatalf("missing model 'opus-4' in output:\n%s", out)
	}
	if !strings.Contains(out, "codex:") {
		t.Fatalf("missing engine 'codex' in output:\n%s", out)
	}
}

func TestConfigModels_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"models", "--json", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string][]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result["claude"]) != 2 {
		t.Fatalf("claude models = %v, want 2 entries", result["claude"])
	}
	if len(result["codex"]) != 1 {
		t.Fatalf("codex models = %v, want 1 entry", result["codex"])
	}
}

func TestConfigRoot_CwdDiscovery(t *testing.T) {
	isolateHome(t)

	// Create a project-level config.
	dir := t.TempDir()
	muxDir := filepath.Join(dir, ".agent-mux")
	if err := os.MkdirAll(muxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projConfig := filepath.Join(muxDir, "config.toml")
	if err := os.WriteFile(projConfig, []byte(`
[defaults]
engine = "gemini"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"--cwd", dir}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	sources, _ := result["_sources"].([]any)
	found := false
	for _, s := range sources {
		if s.(string) == projConfig {
			found = true
		}
	}
	if !found {
		t.Fatalf("_sources %v should contain project config %q", sources, projConfig)
	}
}

func TestConfigRoot_InvalidConfig(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"--config", "/nonexistent/path/config.toml"}, &stdout)
	if exit != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["kind"] != "error" {
		t.Fatalf("kind = %v, want error", result["kind"])
	}
}

func TestConfigRoot_EmptyConfig(t *testing.T) {
	isolateHome(t)

	// Write a minimal empty config to ensure defaults are shown.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Should still have defaults resolved.
	defaults, _ := result["defaults"].(map[string]any)
	if defaults["effort"] != "high" {
		t.Fatalf("default effort = %v, want 'high'", defaults["effort"])
	}
}

// ---------------------------------------------------------------------------
// config skills tests
// ---------------------------------------------------------------------------

func TestConfigSkills_Table(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	setupTestSkillDir(t, dir, "alpha-skill")
	setupTestSkillDir(t, dir, "beta-skill")

	cfgPath := writeTestConfigWithSkillPaths(t, dir)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--config", cfgPath, "--cwd", dir}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "NAME") {
		t.Fatalf("missing table header in output:\n%s", out)
	}
	if !strings.Contains(out, "alpha-skill") {
		t.Fatalf("missing skill 'alpha-skill' in output:\n%s", out)
	}
	if !strings.Contains(out, "beta-skill") {
		t.Fatalf("missing skill 'beta-skill' in output:\n%s", out)
	}
}

func TestConfigSkills_JSON(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	setupTestSkillDir(t, dir, "alpha-skill")
	setupTestSkillDir(t, dir, "beta-skill")

	cfgPath := writeTestConfigWithSkillPaths(t, dir)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--json", "--config", cfgPath, "--cwd", dir}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	if len(entries) < 2 {
		t.Fatalf("expected at least 2 skill entries, got %d", len(entries))
	}

	names := make(map[string]bool)
	for _, e := range entries {
		name, _ := e["name"].(string)
		names[name] = true
	}
	if !names["alpha-skill"] {
		t.Fatalf("missing 'alpha-skill' in JSON output")
	}
	if !names["beta-skill"] {
		t.Fatalf("missing 'beta-skill' in JSON output")
	}
}

func TestConfigSkills_SearchPaths(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	extraDir := filepath.Join(t.TempDir(), "extra-skills")
	setupTestSkillDirAt(t, extraDir, "extra-skill")

	cfgPath := writeTestConfigWithExtraSearchPath(t, dir, extraDir)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--json", "--config", cfgPath, "--cwd", dir}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	found := false
	for _, e := range entries {
		if e["name"] == "extra-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing 'extra-skill' from search_paths in JSON output")
	}
}

func TestConfigSkills_Deduplication(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	// Place the same skill in both cwd/.claude/skills and an extra search path.
	setupTestSkillDir(t, dir, "shared-skill")

	extraDir := filepath.Join(t.TempDir(), "extra-skills")
	setupTestSkillDirAt(t, extraDir, "shared-skill")

	cfgPath := writeTestConfigWithExtraSearchPath(t, dir, extraDir)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--json", "--config", cfgPath, "--cwd", dir}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	count := 0
	for _, e := range entries {
		if e["name"] == "shared-skill" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 entry for 'shared-skill' (dedup), got %d", count)
	}
}

// ---------------------------------------------------------------------------
// config skills helpers
// ---------------------------------------------------------------------------

// setupTestSkillDir creates a skill under <dir>/.claude/skills/<name>/SKILL.md.
func setupTestSkillDir(t *testing.T, dir, name string) {
	t.Helper()
	skillDir := filepath.Join(dir, ".claude", "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupTestSkillDirAt creates a skill under <root>/<name>/SKILL.md (flat search_path layout).
func setupTestSkillDirAt(t *testing.T, root, name string) {
	t.Helper()
	skillDir := filepath.Join(root, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestConfigWithSkillPaths(t *testing.T, dir string) string {
	t.Helper()
	muxDir := filepath.Join(dir, ".agent-mux")
	if err := os.MkdirAll(muxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(muxDir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[defaults]
engine = "claude"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTestConfigWithExtraSearchPath(t *testing.T, dir, extraPath string) string {
	t.Helper()
	muxDir := filepath.Join(dir, ".agent-mux")
	if err := os.MkdirAll(muxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(muxDir, "config.toml")
	content := `
[defaults]
engine = "claude"

[skills]
search_paths = ["` + extraPath + `"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
