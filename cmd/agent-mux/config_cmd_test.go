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

[roles.scout.variants.claude]
engine = "claude"
model = "sonnet-4"
effort = "medium"
timeout = 300

[roles.lifter]
engine = "claude"
model = "opus-4"
effort = "high"
timeout = 1800

[pipelines.research]
max_parallel = 4

[[pipelines.research.steps]]
name = "gather"
role = "scout"
parallel = 3
pass_output_as = "gathered"

[[pipelines.research.steps]]
name = "synthesize"
role = "lifter"
receives = "gathered"
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
	// Variant shows as indented sub-row.
	if !strings.Contains(out, "\u2514 claude") {
		t.Fatalf("missing variant sub-row for 'claude' in output:\n%s", out)
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

	// Should have: scout, scout/claude variant, lifter = 3 entries.
	if len(entries) != 3 {
		t.Fatalf("expected 3 role entries (2 roles + 1 variant), got %d", len(entries))
	}

	// Check the variant entry.
	found := false
	for _, e := range entries {
		if e["variant"] == "claude" {
			found = true
			if e["engine"] != "claude" {
				t.Fatalf("variant engine = %v, want claude", e["engine"])
			}
		}
	}
	if !found {
		t.Fatal("missing variant entry for 'claude'")
	}
}

func TestConfigPipelines_Table(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"pipelines", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "NAME") {
		t.Fatalf("missing table header:\n%s", out)
	}
	if !strings.Contains(out, "research") {
		t.Fatalf("missing pipeline 'research':\n%s", out)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("expected 2 steps for research pipeline:\n%s", out)
	}
}

func TestConfigPipelines_JSON(t *testing.T) {
	isolateHome(t)
	cfgPath := writeTestConfig(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"pipelines", "--json", "--config", cfgPath}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 pipeline, got %d", len(entries))
	}
	if entries[0]["name"] != "research" {
		t.Fatalf("pipeline name = %v, want research", entries[0]["name"])
	}
	// Steps comes as float64 from JSON.
	if int(entries[0]["steps"].(float64)) != 2 {
		t.Fatalf("pipeline steps = %v, want 2", entries[0]["steps"])
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
