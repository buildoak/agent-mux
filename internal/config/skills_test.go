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

	prompt, pathDirs, err := LoadSkills([]string{"go"}, cwd, "")
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

	prompt, _, err := LoadSkills([]string{"go"}, cwd, "")
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

	prompt, pathDirs, err := LoadSkills([]string{"go", "review"}, cwd, "")
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

	prompt, pathDirs, err := LoadSkills([]string{"go", "go"}, cwd, "")
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

	_, pathDirs, err := LoadSkills([]string{"go"}, cwd, "")
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

	_, pathDirs, err := LoadSkills([]string{"go"}, cwd, "")
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

	_, _, err := LoadSkills([]string{"missing"}, cwd, "")
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

	prompt, pathDirs, err := LoadSkills(nil, cwd, "")
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

func TestLoadSkillsRejectsInvalidName(t *testing.T) {
	cwd := t.TempDir()

	_, _, err := LoadSkills([]string{"../bad"}, cwd, "")
	if err == nil {
		t.Fatal("LoadSkills error = nil, want invalid skill name")
	}
	if !strings.Contains(err.Error(), `invalid skill name "../bad"`) {
		t.Fatalf("error = %q, want invalid skill name message", err)
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

	_, pathDirs, err := LoadSkills([]string{"go", "review"}, cwd, "")
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if len(pathDirs) != 1 || pathDirs[0] != scriptsDir {
		t.Fatalf("pathDirs = %#v, want [%q]", pathDirs, scriptsDir)
	}
}

// TestLoadSkillsConfigDirFallback covers Bug 2: a skill defined in the config
// directory (configDir) should be found even when cwd is a completely different
// directory that does not contain a .claude/skills tree.
func TestLoadSkillsConfigDirFallback(t *testing.T) {
	configDir := t.TempDir()
	cwd := t.TempDir() // deliberately different from configDir

	// Skill lives under configDir, NOT under cwd.
	writeSkillFile(t, configDir, "gaal", "Gaal skill content.")

	prompt, _, err := LoadSkills([]string{"gaal"}, cwd, configDir)
	if err != nil {
		t.Fatalf("LoadSkills with configDir fallback: %v", err)
	}

	want := "<skill name=\"gaal\">\nGaal skill content.\n</skill>\n"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

// TestLoadSkillsConfigDirFallbackWithScriptsDir verifies that scripts/ from the
// fallback (configDir-relative) skill are returned in pathDirs.
func TestLoadSkillsConfigDirFallbackWithScriptsDir(t *testing.T) {
	configDir := t.TempDir()
	cwd := t.TempDir()

	writeSkillFile(t, configDir, "gaal", "Gaal skill content.")
	scriptsDir := filepath.Join(configDir, ".claude", "skills", "gaal", "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll scripts: %v", err)
	}

	_, pathDirs, err := LoadSkills([]string{"gaal"}, cwd, configDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if len(pathDirs) != 1 || pathDirs[0] != scriptsDir {
		t.Fatalf("pathDirs = %#v, want [%q]", pathDirs, scriptsDir)
	}
}

// TestLoadSkillsCwdTakesPrecedenceOverConfigDir verifies that a skill found
// in cwd is used even when configDir also has the same skill name.
func TestLoadSkillsCwdTakesPrecedenceOverConfigDir(t *testing.T) {
	configDir := t.TempDir()
	cwd := t.TempDir()

	writeSkillFile(t, cwd, "shared", "cwd version")
	writeSkillFile(t, configDir, "shared", "configDir version")

	prompt, _, err := LoadSkills([]string{"shared"}, cwd, configDir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if prompt != "<skill name=\"shared\">\ncwd version\n</skill>\n" {
		t.Fatalf("prompt = %q, want cwd version to win", prompt)
	}
}

// TestLoadSkillsConfigDirSameAsCwdNoDoubleSearch verifies that when
// configDir == cwd, passing the same dir as fallback doesn't cause duplicate
// work or errors.
func TestLoadSkillsConfigDirSameAsCwd(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "go", "Go conventions.")

	prompt, _, err := LoadSkills([]string{"go"}, dir, dir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if prompt != "<skill name=\"go\">\nGo conventions.\n</skill>\n" {
		t.Fatalf("prompt = %q", prompt)
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
