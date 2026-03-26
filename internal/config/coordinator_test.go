package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCoordinatorFrontmatterAndBody(t *testing.T) {
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

	spec, companion, err := LoadCoordinator("planner", cwd)
	if err != nil {
		t.Fatalf("LoadCoordinator: %v", err)
	}
	if companion != nil {
		t.Fatalf("companion config = %#v, want nil", companion)
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
	if got, ok := spec.ExtraFields["temperature"]; !ok || got != 0.2 {
		t.Fatalf("ExtraFields[temperature] = %#v, want %v", got, 0.2)
	}
}

func TestLoadCoordinatorLoadsCompanionConfig(t *testing.T) {
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
[defaults]
engine = "claude"
model = "claude-sonnet-4-6"
max_depth = 7

[roles.reviewer]
engine = "gemini"
model = "gemini-3.1-pro"
effort = "medium"
`)

	spec, companion, err := LoadCoordinator("builder", cwd)
	if err != nil {
		t.Fatalf("LoadCoordinator: %v", err)
	}
	if spec.Model != "gpt-5.4-mini" {
		t.Fatalf("frontmatter Model = %q, want %q", spec.Model, "gpt-5.4-mini")
	}
	if companion == nil {
		t.Fatal("companion config = nil, want loaded config")
	}
	if companion.Defaults.Engine != "claude" {
		t.Fatalf("companion Defaults.Engine = %q, want %q", companion.Defaults.Engine, "claude")
	}
	if companion.Defaults.Model != "claude-sonnet-4-6" {
		t.Fatalf("companion Defaults.Model = %q, want %q", companion.Defaults.Model, "claude-sonnet-4-6")
	}
	if companion.Defaults.MaxDepth != 7 {
		t.Fatalf("companion Defaults.MaxDepth = %d, want %d", companion.Defaults.MaxDepth, 7)
	}
	if role := companion.Roles["reviewer"]; role.Engine != "gemini" || role.Model != "gemini-3.1-pro" || role.Effort != "medium" {
		t.Fatalf("companion reviewer role = %#v, want decoded role", role)
	}
}

func TestLoadCoordinatorReturnsSeparateSourcesForPrecedence(t *testing.T) {
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	mustMkdirAll(t, agentsDir)

	writeTestFile(t, filepath.Join(agentsDir, "orchestrator.md"), `---
engine: codex
model: gpt-5.4
effort: high
skills: [plan]
---
Coordinate the run.
`)
	writeTestFile(t, filepath.Join(agentsDir, "orchestrator.toml"), `
[defaults]
engine = "claude"
model = "claude-sonnet-4-6"
effort = "medium"
`)

	spec, companion, err := LoadCoordinator("orchestrator", cwd)
	if err != nil {
		t.Fatalf("LoadCoordinator: %v", err)
	}
	if spec.Engine != "codex" || spec.Model != "gpt-5.4" || spec.Effort != "high" {
		t.Fatalf("frontmatter fields = %#v, want coordinator overrides preserved separately", spec)
	}
	if companion == nil {
		t.Fatal("companion config = nil, want config for lower-precedence merge")
	}
	if companion.Defaults.Engine != "claude" || companion.Defaults.Model != "claude-sonnet-4-6" || companion.Defaults.Effort != "medium" {
		t.Fatalf("companion defaults = %#v, want TOML values", companion.Defaults)
	}
}

func TestLoadCoordinatorNotFoundListsAvailable(t *testing.T) {
	cwd := t.TempDir()
	projectAgents := filepath.Join(cwd, ".claude", "agents")
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalAgents := filepath.Join(home, ".config", "agent-mux", "agents")
	mustMkdirAll(t, projectAgents)
	mustMkdirAll(t, globalAgents)

	writeTestFile(t, filepath.Join(projectAgents, "alpha.md"), "Project alpha.")
	writeTestFile(t, filepath.Join(globalAgents, "beta.md"), "Global beta.")

	_, _, err := LoadCoordinator("missing", cwd)
	if err == nil {
		t.Fatal("LoadCoordinator(missing) error = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `coordinator "missing" not found`) {
		t.Fatalf("error = %q, want missing coordinator message", msg)
	}
	if !strings.Contains(msg, "alpha") || !strings.Contains(msg, "beta") {
		t.Fatalf("error = %q, want available coordinators from both dirs", msg)
	}
}

func TestLoadCoordinatorSearchOrderProjectThenGlobal(t *testing.T) {
	cwd := t.TempDir()
	projectAgents := filepath.Join(cwd, ".claude", "agents")
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalAgents := filepath.Join(home, ".config", "agent-mux", "agents")
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

	spec, _, err := LoadCoordinator("shared", cwd)
	if err != nil {
		t.Fatalf("LoadCoordinator(shared): %v", err)
	}
	if spec.Model != "gpt-5.4" || spec.SystemPrompt != "project prompt\n" {
		t.Fatalf("project coordinator = %#v, want project file to win", spec)
	}

	spec, _, err = LoadCoordinator("fallback", cwd)
	if err != nil {
		t.Fatalf("LoadCoordinator(fallback): %v", err)
	}
	if spec.Engine != "gemini" || spec.SystemPrompt != "fallback prompt\n" {
		t.Fatalf("fallback coordinator = %#v, want global file used", spec)
	}
}

func TestLoadCoordinatorWithoutFrontmatterUsesBodyOnly(t *testing.T) {
	cwd := t.TempDir()
	agentsDir := filepath.Join(cwd, ".claude", "agents")
	mustMkdirAll(t, agentsDir)

	writeTestFile(t, filepath.Join(agentsDir, "plain.md"), "Just the prompt body.\nSecond line.\n")

	spec, companion, err := LoadCoordinator("plain", cwd)
	if err != nil {
		t.Fatalf("LoadCoordinator: %v", err)
	}
	if companion != nil {
		t.Fatalf("companion config = %#v, want nil", companion)
	}
	if spec.Model != "" || spec.Engine != "" || spec.Effort != "" || spec.Timeout != 0 || len(spec.Skills) != 0 {
		t.Fatalf("spec fields = %#v, want empty frontmatter fields", spec)
	}
	if spec.SystemPrompt != "Just the prompt body.\nSecond line.\n" {
		t.Fatalf("SystemPrompt = %q, want full body", spec.SystemPrompt)
	}
	if len(spec.ExtraFields) != 0 {
		t.Fatalf("ExtraFields = %#v, want empty map", spec.ExtraFields)
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
