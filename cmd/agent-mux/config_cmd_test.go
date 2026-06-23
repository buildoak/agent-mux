package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/config"
)

func TestConfigRoot_Summary(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	exit := runConfigCommand(nil, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	if result["kind"] != "config_summary" {
		t.Fatalf("kind = %v, want config_summary", result["kind"])
	}

	defaults, _ := result["defaults"].(map[string]any)
	if defaults["effort"] != "high" {
		t.Fatalf("defaults.effort = %v, want high", defaults["effort"])
	}

	engines, _ := result["engines"].([]any)
	if len(engines) == 0 {
		t.Fatalf("engines missing from config summary: %#v", result["engines"])
	}
}

func TestConfigRoot_SummaryUsesCachedAgyModels(t *testing.T) {
	isolateHome(t)

	cachePath, err := config.AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"version":1,"source":"agy_models","status":"ok","models":["Gemini Cached Root 1.0"],"refreshed_at":"2026-06-23T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand(nil, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}
	models, _ := result["models"].(map[string]any)
	agyModels, _ := models["agy"].([]any)
	if len(agyModels) != 1 || agyModels[0] != "Gemini Cached Root 1.0" {
		t.Fatalf("models.agy = %#v, want cached root model", models["agy"])
	}
}

// ---------------------------------------------------------------------------
// config engines tests
// ---------------------------------------------------------------------------

func TestConfigEngines_Table(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"engines"}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	out := stdout.String()
	for _, want := range []string{"ENGINE", "MODELS", "MODEL_SOURCE", "MODEL_STATUS", "RESUME", "STEER", "agy", "resume_inbox_or_abort", "Gemini 3.1 Pro (High)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestConfigEngines_JSON(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"engines", "--json"}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	if len(entries) != 4 {
		t.Fatalf("expected 4 engines, got %d: %#v", len(entries), entries)
	}

	byEngine := make(map[string]map[string]any)
	for _, e := range entries {
		name, _ := e["engine"].(string)
		byEngine[name] = e
	}

	agy := byEngine["agy"]
	if agy == nil {
		t.Fatalf("missing agy entry: %#v", entries)
	}
	if agy["supports_resume"] != true {
		t.Fatalf("agy.supports_resume = %v, want true", agy["supports_resume"])
	}
	if agy["steer_semantics"] != "resume_inbox_or_abort" {
		t.Fatalf("agy.steer_semantics = %v, want resume_inbox_or_abort", agy["steer_semantics"])
	}
	if agy["event_stream"] != false {
		t.Fatalf("agy.event_stream = %v, want false", agy["event_stream"])
	}
	models, _ := agy["models"].([]any)
	if len(models) == 0 {
		t.Fatalf("agy.models missing: %#v", agy["models"])
	}
	if agy["model_source"] != "built_in" {
		t.Fatalf("agy.model_source = %v, want built_in", agy["model_source"])
	}
	if agy["model_status"] != "fallback" {
		t.Fatalf("agy.model_status = %v, want fallback", agy["model_status"])
	}

	codex := byEngine["codex"]
	if codex == nil {
		t.Fatalf("missing codex entry: %#v", entries)
	}
	if codex["supports_resume"] != true {
		t.Fatalf("codex.supports_resume = %v, want true", codex["supports_resume"])
	}
	if codex["token_usage"] != true {
		t.Fatalf("codex.token_usage = %v, want true", codex["token_usage"])
	}
	if codex["cost_usage"] != false {
		t.Fatalf("codex.cost_usage = %v, want false", codex["cost_usage"])
	}
}

func TestConfigEngines_UsesCachedAgyModels(t *testing.T) {
	isolateHome(t)

	cachePath, err := config.AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"version":1,"source":"agy_models","status":"ok","models":["Gemini Cached 1.0"],"refreshed_at":"2026-06-23T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"engines", "--json"}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	var agy map[string]any
	for _, e := range entries {
		if e["engine"] == "agy" {
			agy = e
			break
		}
	}
	if agy == nil {
		t.Fatalf("missing agy entry: %#v", entries)
	}
	models, _ := agy["models"].([]any)
	if len(models) != 1 || models[0] != "Gemini Cached 1.0" {
		t.Fatalf("agy.models = %#v, want cached model", agy["models"])
	}
	if agy["model_source"] != "cache" || agy["model_status"] != "ok" {
		t.Fatalf("agy source/status = %v/%v, want cache/ok", agy["model_source"], agy["model_status"])
	}
}

func TestConfigEngines_RefreshModelsWritesCache(t *testing.T) {
	isolateHome(t)
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)

	fakeAgy := filepath.Join(binDir, "agy")
	if err := os.WriteFile(fakeAgy, []byte("#!/bin/sh\nprintf '%s\\n' '- Gemini Refreshed 1.0' '- Claude Refreshed 2.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"engines", "--refresh-models", "--json"}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	var agy map[string]any
	for _, e := range entries {
		if e["engine"] == "agy" {
			agy = e
			break
		}
	}
	if agy == nil {
		t.Fatalf("missing agy entry: %#v", entries)
	}
	if agy["model_source"] != "agy_models" || agy["model_status"] != "refreshed" {
		t.Fatalf("agy source/status = %v/%v, want agy_models/refreshed", agy["model_source"], agy["model_status"])
	}
	models, _ := agy["models"].([]any)
	if len(models) != 2 || models[0] != "Gemini Refreshed 1.0" {
		t.Fatalf("agy.models = %#v, want refreshed models", agy["models"])
	}

	cachePath, err := config.AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected cache at %s: %v", cachePath, err)
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

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--cwd", dir}, &stdout)
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

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--json", "--cwd", dir}, &stdout)
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

func TestConfigSkills_EnvSearchPath(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	extraDir := filepath.Join(t.TempDir(), "extra-skills")
	setupTestSkillDirAt(t, extraDir, "extra-skill")

	t.Setenv("AGENT_MUX_SKILL_PATH", extraDir)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--json", "--cwd", dir}, &stdout)
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
		t.Fatalf("missing 'extra-skill' from AGENT_MUX_SKILL_PATH in JSON output")
	}
}

func TestConfigSkills_Deduplication(t *testing.T) {
	isolateHome(t)

	dir := t.TempDir()
	// Place the same skill in both cwd/.claude/skills and an extra search path.
	setupTestSkillDir(t, dir, "shared-skill")

	extraDir := filepath.Join(t.TempDir(), "extra-skills")
	setupTestSkillDirAt(t, extraDir, "shared-skill")

	t.Setenv("AGENT_MUX_SKILL_PATH", extraDir)

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"skills", "--json", "--cwd", dir}, &stdout)
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
// config prompts tests
// ---------------------------------------------------------------------------

func TestConfigPrompts_Table(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	promptsDir := filepath.Join(homeDir, ".agent-mux", "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "scout.md"), []byte("---\neffort: low\n---\nScout prompt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "lifter.md"), []byte("---\neffort: high\n---\nLifter prompt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"prompts"}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "NAME") {
		t.Fatalf("missing table header in output:\n%s", out)
	}
	if !strings.Contains(out, "scout") {
		t.Fatalf("missing prompt 'scout' in output:\n%s", out)
	}
	if !strings.Contains(out, "lifter") {
		t.Fatalf("missing prompt 'lifter' in output:\n%s", out)
	}
}

func TestConfigPrompts_JSON(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	promptsDir := filepath.Join(homeDir, ".agent-mux", "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptsDir, "scout.md"), []byte("---\neffort: low\nskills:\n  - web-search\n---\nScout prompt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	exit := runConfigCommand([]string{"prompts", "--json"}, &stdout)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", exit, stdout.String())
	}

	var entries []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	if len(entries) < 1 {
		t.Fatalf("expected at least 1 prompt entry, got %d", len(entries))
	}
	if entries[0]["name"] != "scout" {
		t.Fatalf("entries[0].name = %v, want 'scout'", entries[0]["name"])
	}
	if entries[0]["effort"] != "low" {
		t.Fatalf("entries[0].effort = %v, want 'low'", entries[0]["effort"])
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
