package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProfileFrontmatterAndBody(t *testing.T) {
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	mustMkdirAll(t, agentsDir)

	writeTestFile(t, filepath.Join(agentsDir, "planner.md"), `---
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

	spec, err := LoadProfile("planner", cwd)
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
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	mustMkdirAll(t, agentsDir)

	writeTestFile(t, filepath.Join(agentsDir, "builder.md"), `---
model: gpt-5.4-mini
skills: [repo-map]
---
Build things.
`)
	writeTestFile(t, filepath.Join(agentsDir, "builder.toml"), `
not valid toml = [
`)

	spec, err := LoadProfile("builder", cwd)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if spec.Model != "gpt-5.4-mini" {
		t.Fatalf("frontmatter Model = %q, want %q", spec.Model, "gpt-5.4-mini")
	}
}

func TestLoadProfileRejectsNonPositiveFrontmatterTimeout(t *testing.T) {
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	mustMkdirAll(t, agentsDir)

	writeTestFile(t, filepath.Join(agentsDir, "planner.md"), `---
timeout: 0
---
planner
`)

	_, err := LoadProfile("planner", cwd)
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
	cwd := t.TempDir()
	projectAgents := filepath.Join(cwd, ".claude", "agents")
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalAgents := filepath.Join(home, ".agent-mux", "agents")
	mustMkdirAll(t, projectAgents)
	mustMkdirAll(t, globalAgents)

	writeTestFile(t, filepath.Join(projectAgents, "alpha.md"), "Project alpha.")
	writeTestFile(t, filepath.Join(globalAgents, "beta.md"), "Global beta.")

	_, err := LoadProfile("missing", cwd)
	if err == nil {
		t.Fatal("LoadProfile(missing) error = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `profile "missing" not found`) {
		t.Fatalf("error = %q, want missing profile message", msg)
	}
	if !strings.Contains(msg, "alpha") || !strings.Contains(msg, "beta") {
		t.Fatalf("error = %q, want available profiles from both dirs", msg)
	}
}

func TestLoadProfileRejectsInvalidName(t *testing.T) {
	cwd := t.TempDir()

	_, err := LoadProfile("../planner", cwd)
	if err == nil {
		t.Fatal("LoadProfile error = nil, want invalid profile name")
	}
	if !strings.Contains(err.Error(), `invalid profile name "../planner"`) {
		t.Fatalf("error = %q, want invalid profile name message", err)
	}
}

func TestLoadProfileSearchOrderProjectThenGlobal(t *testing.T) {
	cwd := t.TempDir()
	projectAgents := filepath.Join(cwd, ".claude", "agents")
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalAgents := filepath.Join(home, ".agent-mux", "agents")
	mustMkdirAll(t, projectAgents)
	mustMkdirAll(t, globalAgents)

	writeTestFile(t, filepath.Join(projectAgents, "shared.md"), `---
model: gpt-5.4
---
project prompt
`)
	writeTestFile(t, filepath.Join(globalAgents, "shared.md"), `---
model: claude-sonnet-4-6
---
global prompt
`)
	writeTestFile(t, filepath.Join(globalAgents, "fallback.md"), `---
engine: gemini
---
fallback prompt
`)

	spec, err := LoadProfile("shared", cwd)
	if err != nil {
		t.Fatalf("LoadProfile(shared): %v", err)
	}
	if spec.Model != "gpt-5.4" || spec.SystemPrompt != "project prompt\n" {
		t.Fatalf("project coordinator = %#v, want project file to win", spec)
	}

	spec, err = LoadProfile("fallback", cwd)
	if err != nil {
		t.Fatalf("LoadProfile(fallback): %v", err)
	}
	if spec.Engine != "gemini" || spec.SystemPrompt != "fallback prompt\n" {
		t.Fatalf("fallback coordinator = %#v, want global file used", spec)
	}
}

// TestLoadProfileAgentMuxDirSearchPaths verifies that the new .agent-mux/agents
// project-level and ~/.agent-mux/agents global paths are searched.
func TestLoadProfileAgentMuxDirSearchPaths(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("cwd-dot-agent-mux-agents", func(t *testing.T) {
		agentMuxAgents := filepath.Join(cwd, ".agent-mux", "agents")
		mustMkdirAll(t, agentMuxAgents)
		writeTestFile(t, filepath.Join(agentMuxAgents, "lifter.md"), `---
engine: codex
---
lifted prompt
`)
		spec, err := LoadProfile("lifter", cwd)
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if spec.Engine != "codex" || spec.SystemPrompt != "lifted prompt\n" {
			t.Fatalf("spec = %#v, want .agent-mux/agents file", spec)
		}
	})

	t.Run("home-dot-agent-mux-agents", func(t *testing.T) {
		globalAgents := filepath.Join(home, ".agent-mux", "agents")
		mustMkdirAll(t, globalAgents)
		writeTestFile(t, filepath.Join(globalAgents, "global-agent.md"), `---
engine: claude
---
global agent prompt
`)
		spec, err := LoadProfile("global-agent", cwd)
		if err != nil {
			t.Fatalf("LoadProfile: %v", err)
		}
		if spec.Engine != "claude" || spec.SystemPrompt != "global agent prompt\n" {
			t.Fatalf("spec = %#v, want ~/.agent-mux/agents file", spec)
		}
	})
}

func TestLoadProfileWithoutFrontmatterUsesBodyOnly(t *testing.T) {
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	mustMkdirAll(t, agentsDir)

	writeTestFile(t, filepath.Join(agentsDir, "plain.md"), "Just the prompt body.\nSecond line.\n")

	spec, err := LoadProfile("plain", cwd)
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
