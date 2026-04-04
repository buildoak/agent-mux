package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
