package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProfileFrontmatterAndBody(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "planner.md"), `---
model: gpt-5.4
effort: medium
engine: codex
skills:
  - repo-map
  - test-runner
timeout: 900
temperature: 0.2
---
You are the planning coordinator.
`)

	spec, err := LoadProfile("planner")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if spec.Name != "planner" {
		t.Fatalf("Name = %q, want %q", spec.Name, "planner")
	}
	if spec.Model != "gpt-5.4" {
		t.Fatalf("Model = %q, want %q", spec.Model, "gpt-5.4")
	}
	if spec.Effort != "medium" {
		t.Fatalf("Effort = %q, want %q", spec.Effort, "medium")
	}
	if spec.Engine != "codex" {
		t.Fatalf("Engine = %q, want %q", spec.Engine, "codex")
	}
	if spec.Timeout != 900 {
		t.Fatalf("Timeout = %d, want %d", spec.Timeout, 900)
	}
	if got := spec.Skills; len(got) != 2 || got[0] != "repo-map" || got[1] != "test-runner" {
		t.Fatalf("Skills = %#v, want %#v", got, []string{"repo-map", "test-runner"})
	}
	if spec.SystemPrompt != "You are the planning coordinator.\n" {
		t.Fatalf("SystemPrompt = %q, want body after frontmatter", spec.SystemPrompt)
	}
}

func TestLoadProfileIgnoresCompanionConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "builder.md"), `---
model: gpt-5.4-mini
skills: [repo-map]
---
Build things.
`)
	writeTestFile(t, filepath.Join(promptsDir, "builder.toml"), `
not valid toml = [
`)

	spec, err := LoadProfile("builder")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if spec.Model != "gpt-5.4-mini" {
		t.Fatalf("frontmatter Model = %q, want %q", spec.Model, "gpt-5.4-mini")
	}
}

func TestLoadProfileRejectsNonPositiveFrontmatterTimeout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "planner.md"), `---
timeout: 0
---
planner
`)

	_, err := LoadProfile("planner")
	if err == nil {
		t.Fatal("LoadProfile error = nil, want validation error")
	}
	if !IsValidationError(err) {
		t.Fatalf("error = %T %v, want validation error", err, err)
	}
	if !strings.Contains(err.Error(), "invalid timeout") {
		t.Fatalf("error = %q, want invalid timeout message", err)
	}
}

func TestLoadProfileNotFoundListsAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "alpha.md"), "Alpha prompt.")
	writeTestFile(t, filepath.Join(promptsDir, "beta.md"), "Beta prompt.")

	_, err := LoadProfile("missing")
	if err == nil {
		t.Fatal("LoadProfile(missing) error = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `profile "missing" not found`) {
		t.Fatalf("error = %q, want missing profile message", msg)
	}
	if !strings.Contains(msg, "alpha") || !strings.Contains(msg, "beta") {
		t.Fatalf("error = %q, want available profiles listed", msg)
	}
}

func TestLoadProfileRejectsInvalidName(t *testing.T) {
	_, err := LoadProfile("../planner")
	if err == nil {
		t.Fatal("LoadProfile error = nil, want invalid profile name")
	}
	if !strings.Contains(err.Error(), `invalid profile name "../planner"`) {
		t.Fatalf("error = %q, want invalid profile name message", err)
	}
}

func TestLoadProfileGlobalOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "shared.md"), `---
model: claude-sonnet-4-6
---
global prompt
`)

	spec, err := LoadProfile("shared")
	if err != nil {
		t.Fatalf("LoadProfile(shared): %v", err)
	}
	if spec.Model != "claude-sonnet-4-6" || spec.SystemPrompt != "global prompt\n" {
		t.Fatalf("spec = %#v, want global file", spec)
	}
}

func TestLoadProfileWithoutFrontmatterUsesBodyOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "plain.md"), "Just the prompt body.\nSecond line.\n")

	spec, err := LoadProfile("plain")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if spec.Model != "" || spec.Engine != "" || spec.Effort != "" || spec.Timeout != 0 || len(spec.Skills) != 0 {
		t.Fatalf("spec fields = %#v, want empty frontmatter fields", spec)
	}
	if spec.SystemPrompt != "Just the prompt body.\nSecond line.\n" {
		t.Fatalf("SystemPrompt = %q, want full body", spec.SystemPrompt)
	}
}

func TestDiscoverPromptFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	promptsDir := filepath.Join(home, ".agent-mux", "prompts")
	mustMkdirAll(t, promptsDir)

	writeTestFile(t, filepath.Join(promptsDir, "alpha.md"), `---
engine: codex
---
alpha prompt
`)
	writeTestFile(t, filepath.Join(promptsDir, "beta.md"), "beta prompt\n")
	writeTestFile(t, filepath.Join(promptsDir, "readme.txt"), "not a prompt")

	results := DiscoverPromptFiles()
	if len(results) != 2 {
		t.Fatalf("got %d prompts, want 2", len(results))
	}
	if results[0].Name != "alpha" || results[1].Name != "beta" {
		t.Fatalf("names = [%s, %s], want [alpha, beta]", results[0].Name, results[1].Name)
	}
	if results[0].Engine != "codex" {
		t.Fatalf("alpha engine = %q, want codex", results[0].Engine)
	}
	if results[0].Source != "~/.agent-mux/prompts" {
		t.Fatalf("source = %q, want ~/.agent-mux/prompts", results[0].Source)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
