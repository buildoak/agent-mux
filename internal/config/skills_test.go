package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillsSingleSkill(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Use Go conventions.")

	prompt, pathDirs, err := LoadSkills([]string{"go"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	want := "<skill name=\"go\">\nUse Go conventions.\n</skill>\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
	if len(pathDirs) != 0 {
		t.Fatalf("pathDirs = %#v, want empty", pathDirs)
	}
}

func TestLoadSkillsTrimsTrailingNewlineBeforeClosingTag(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Use Go conventions.\n")

	prompt, _, err := LoadSkills([]string{"go"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	want := "<skill name=\"go\">\nUse Go conventions.\n</skill>\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestLoadSkillsMultipleSkillsInOrder(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Go only.")
	writeSkillFile(t, cwd, "review", "Review for regressions.")

	prompt, pathDirs, err := LoadSkills([]string{"go", "review"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	want := "<skill name=\"go\">\nGo only.\n</skill>\n\n<skill name=\"review\">\nReview for regressions.\n</skill>\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
	if len(pathDirs) != 0 {
		t.Fatalf("pathDirs = %#v, want empty", pathDirs)
	}
}

func TestLoadSkillsDeduplicatesNames(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Only once.")

	prompt, pathDirs, err := LoadSkills([]string{"go", "go"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if strings.Count(prompt, `<skill name="go">`) != 1 {
		t.Fatalf("prompt = %q, want single wrapped skill", prompt)
	}
	if len(pathDirs) != 0 {
		t.Fatalf("pathDirs = %#v, want empty", pathDirs)
	}
}

func TestLoadSkillsWithScriptsDir(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Use helpers.")
	scriptsDir := filepath.Join(cwd, ".claude", "skills", "go", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll scripts: %v", err)
	}

	_, pathDirs, err := LoadSkills([]string{"go"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if len(pathDirs) != 1 || pathDirs[0] != scriptsDir {
		t.Fatalf("pathDirs = %#v, want [%q]", pathDirs, scriptsDir)
	}
}

func TestLoadSkillsWithoutScriptsDir(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "No scripts.")

	_, pathDirs, err := LoadSkills([]string{"go"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if len(pathDirs) != 0 {
		t.Fatalf("pathDirs = %#v, want empty", pathDirs)
	}
}

func TestLoadSkillsNotFoundIncludesAvailableSkills(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Go only.")
	writeSkillFile(t, cwd, "review", "Review only.")

	_, _, err := LoadSkills([]string{"missing"}, cwd)
	if err == nil {
		t.Fatal("LoadSkills error = nil, want error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Available skills:") {
		t.Fatalf("error = %q, want available skills info", msg)
	}
	if !strings.Contains(msg, "go") || !strings.Contains(msg, "review") {
		t.Fatalf("error = %q, want listed skill names", msg)
	}
}

func TestLoadSkillsEmptyNames(t *testing.T) {
	cwd := t.TempDir()

	prompt, pathDirs, err := LoadSkills(nil, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if prompt != "" {
		t.Fatalf("prompt = %q, want empty", prompt)
	}
	if len(pathDirs) != 0 {
		t.Fatalf("pathDirs = %#v, want empty", pathDirs)
	}
}

func TestLoadSkillsSecondSkillHasScriptsDir(t *testing.T) {
	cwd := t.TempDir()
	writeSkillFile(t, cwd, "go", "Go only.")
	writeSkillFile(t, cwd, "review", "Review only.")
	scriptsDir := filepath.Join(cwd, ".claude", "skills", "review", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll scripts: %v", err)
	}

	_, pathDirs, err := LoadSkills([]string{"go", "review"}, cwd)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if len(pathDirs) != 1 || pathDirs[0] != scriptsDir {
		t.Fatalf("pathDirs = %#v, want [%q]", pathDirs, scriptsDir)
	}
}

func writeSkillFile(t *testing.T, cwd, name, content string) {
	t.Helper()

	path := filepath.Join(cwd, ".claude", "skills", name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
