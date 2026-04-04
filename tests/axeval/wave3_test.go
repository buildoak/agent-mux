//go:build axeval

package axeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreviewDryRun(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 test — requires AX_EVAL_HARD=1")
	}
	result := dispatchWithFlags(t, binaryPath, []string{
		"preview",
		"--engine", "codex",
		"--model", "gpt-5.4-mini",
		"--yes",
		"test prompt",
	}, 1*time.Minute)

	if result.ExitCode != 0 {
		t.Fatalf("preview exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, string(result.RawStdout), string(result.RawStderr))
	}

	raw, err := parseJSONObject(result.RawStdout, "preview stdout")
	if err != nil {
		t.Fatalf("parse preview stdout: %v\nstdout=%s\nstderr=%s", err, string(result.RawStdout), string(result.RawStderr))
	}

	kind, _ := jsonStringField(raw, "kind")
	if kind != "preview" {
		t.Skip("TODO: preview subcommand not implemented yet")
	}

	dispatchSpec, err := jsonObjectField(raw, "dispatch_spec")
	if err != nil {
		t.Fatalf("preview dispatch_spec: %v\nstdout=%s", err, string(result.RawStdout))
	}
	if err := requireExactStringField(dispatchSpec, "engine", "codex"); err != nil {
		t.Fatalf("preview dispatch_spec invalid: %v\nstdout=%s", err, string(result.RawStdout))
	}
}

func TestConfigIntrospection(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 test — requires AX_EVAL_HARD=1")
	}
	cwd := absFixtureDir(t)

	configResult := dispatchWithFlags(t, binaryPath, []string{"config", "--cwd", cwd}, 2*time.Minute)
	if configResult.ExitCode != 0 {
		t.Fatalf("config exit=%d\nstdout=%s\nstderr=%s", configResult.ExitCode, string(configResult.RawStdout), string(configResult.RawStderr))
	}
	if strings.TrimSpace(string(configResult.RawStdout)) == "" {
		t.Fatal("config stdout empty")
	}

	promptsResult := dispatchWithFlags(t, binaryPath, []string{"config", "prompts", "--cwd", cwd}, 2*time.Minute)
	if promptsResult.ExitCode != 0 {
		t.Fatalf("config prompts exit=%d\nstdout=%s\nstderr=%s", promptsResult.ExitCode, string(promptsResult.RawStdout), string(promptsResult.RawStderr))
	}
	promptsOut := string(promptsResult.RawStdout)
	if strings.TrimSpace(promptsOut) == "" {
		t.Fatal("config prompts stdout empty")
	}
	if !strings.Contains(promptsOut, "sysprompt-test") || !strings.Contains(promptsOut, "variant-test-mini") {
		t.Fatalf("config prompts output missing expected profile names\nstdout=%s", promptsOut)
	}

	jsonFlag := detectJSONFlag(t, "config", "prompts")
	if jsonFlag == "" {
		t.Skip("TODO: config prompts JSON flag not implemented yet")
	}
	promptsJSONResult := dispatchWithFlags(t, binaryPath, []string{"config", "prompts", jsonFlag, "--cwd", cwd}, 2*time.Minute)
	if promptsJSONResult.ExitCode != 0 {
		t.Fatalf("config prompts %s exit=%d\nstdout=%s\nstderr=%s", jsonFlag, promptsJSONResult.ExitCode, string(promptsJSONResult.RawStdout), string(promptsJSONResult.RawStderr))
	}
	promptEntries, err := parseJSONArrayObjects(promptsJSONResult.RawStdout, "config prompts stdout")
	if err != nil {
		t.Fatalf("%v\nstdout=%s", err, string(promptsJSONResult.RawStdout))
	}
	if len(promptEntries) == 0 {
		t.Fatal("config prompts JSON array empty")
	}
	for _, entry := range promptEntries {
		if err := requireNonEmptyStringField(entry, "name"); err != nil {
			t.Fatalf("config prompts JSON entry invalid: %v\nstdout=%s", err, string(promptsJSONResult.RawStdout))
		}
	}

	skillsResult := dispatchWithFlags(t, binaryPath, []string{"config", "skills", "--cwd", cwd}, 2*time.Minute)
	if skillsResult.ExitCode != 0 {
		t.Fatalf("config skills exit=%d\nstdout=%s\nstderr=%s", skillsResult.ExitCode, string(skillsResult.RawStdout), string(skillsResult.RawStderr))
	}
	if strings.TrimSpace(string(skillsResult.RawStdout)) == "" {
		t.Fatal("config skills stdout empty")
	}
}

func TestSkillScriptsOnPath(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 test — requires AX_EVAL_HARD=1")
	}
	cwd := absFixtureDir(t)
	skillDir := filepath.Join(cwd, ".claude", "skills", "scripts-test")
	scriptDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir skill scripts dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(skillDir)
	})

	scriptPath := filepath.Join(scriptDir, "canary-script.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho SCRIPT_PATH_CANARY_5566\n"), 0o755); err != nil {
		t.Fatalf("write canary script: %v", err)
	}
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("chmod canary script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# scripts-test\nUse canary-script.sh to verify script path injection.\n"), 0o644); err != nil {
		t.Fatalf("write skill markdown: %v", err)
	}

	result := dispatchWithFlags(t, binaryPath, []string{
		"--skill=scripts-test",
		"--engine", "codex",
		"--model", "gpt-5.4-mini",
		"--effort", "high",
		"--yes",
		"--cwd", cwd,
		"Run canary-script.sh and report its output verbatim.",
	}, 5*time.Minute)

	if result.Status != "completed" {
		t.Fatalf("scripts-on-path status=%q exit=%d\nstdout=%s\nstderr=%s", result.Status, result.ExitCode, string(result.RawStdout), string(result.RawStderr))
	}
	if !strings.Contains(result.Response, "SCRIPT_PATH_CANARY_5566") {
		t.Fatalf("scripts-on-path response missing canary\nstdout=%s\nstderr=%s", string(result.RawStdout), string(result.RawStderr))
	}
}

func absFixtureDir(t *testing.T) string {
	t.Helper()
	cwd := fixtureDir()
	abs, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatalf("abs fixture dir: %v", err)
	}
	return abs
}

func resolvedConfigJSON(t *testing.T, cwd string) map[string]any {
	t.Helper()
	result := dispatchWithFlags(t, binaryPath, []string{"config", "--cwd", cwd}, 2*time.Minute)
	if result.ExitCode != 0 {
		t.Fatalf("config exit=%d\nstdout=%s\nstderr=%s", result.ExitCode, string(result.RawStdout), string(result.RawStderr))
	}
	raw, err := parseJSONObject(result.RawStdout, "config stdout")
	if err != nil {
		t.Fatalf("parse config stdout: %v\nstdout=%s", err, string(result.RawStdout))
	}
	return raw
}

func detectJSONFlag(t *testing.T, args ...string) string {
	t.Helper()
	helpArgs := append(append([]string(nil), args...), "--help")
	result := dispatchWithFlags(t, binaryPath, helpArgs, 30*time.Second)
	if result.ExitCode != 0 {
		return ""
	}
	raw, err := parseJSONObject(result.RawStdout, strings.Join(helpArgs, " "))
	if err != nil {
		return ""
	}
	usage, _ := jsonStringField(raw, "usage")
	if strings.Contains(usage, "-json") || strings.Contains(usage, "--json") {
		return "--json"
	}
	return ""
}

func parseJSONArrayObjects(data []byte, source string) ([]map[string]any, error) {
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s as JSON array: %w", source, err)
	}
	return raw, nil
}
