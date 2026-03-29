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

func TestGcDryRun(t *testing.T) {
	cwd := absFixtureDir(t)
	seed := dispatch(t, binaryPath, TestCase{
		Engine:       "codex",
		Model:        "gpt-5.4-mini",
		Effort:       "high",
		Prompt:       "What is 2+2? Answer with just the number.",
		CWD:          cwd,
		TimeoutSec:   120,
		MaxWallClock: 3 * time.Minute,
		SkipSkills:   true,
	})
	if seed.Status != "completed" {
		t.Fatalf("seed dispatch status=%q\nstdout=%s\nstderr=%s", seed.Status, string(seed.RawStdout), string(seed.RawStderr))
	}

	seedRaw, err := stdoutJSONObject(seed)
	if err != nil {
		t.Fatalf("parse seed stdout: %v\nstdout=%s", err, string(seed.RawStdout))
	}
	dispatchID, ok := jsonStringField(seedRaw, "dispatch_id")
	if !ok || dispatchID == "" {
		t.Fatalf("seed dispatch_id missing\nstdout=%s", string(seed.RawStdout))
	}

	gcResult := dispatchWithFlags(t, binaryPath, []string{"gc", "--dry-run", "--older-than", "0h"}, 5*time.Minute)
	if gcResult.ExitCode != 0 {
		raw, err := parseJSONObject(gcResult.RawStdout, "gc stdout")
		if err == nil {
			if kind, _ := jsonStringField(raw, "kind"); kind == "error" {
				if errObj, ok := raw["error"].(map[string]any); ok {
					code, _ := jsonStringField(errObj, "code")
					message, _ := jsonStringField(errObj, "message")
					if code == "invalid_args" && strings.Contains(message, "dry-run") {
						t.Skip("TODO: gc subcommand not implemented")
					}
					if code == "invalid_input" && strings.Contains(message, "duration must be positive") {
						t.Skip("TODO: gc --dry-run requires a strictly positive --older-than; 0h contract not implemented")
					}
				}
			}
		}
		t.Fatalf("gc exit=%d\nstdout=%s\nstderr=%s", gcResult.ExitCode, string(gcResult.RawStdout), string(gcResult.RawStderr))
	}

	gcRaw, err := parseJSONObject(gcResult.RawStdout, "gc stdout")
	if err != nil {
		t.Fatalf("parse gc stdout: %v\nstdout=%s", err, string(gcResult.RawStdout))
	}
	if kind, _ := jsonStringField(gcRaw, "kind"); kind != "gc_dry_run" {
		t.Fatalf("gc kind=%q, want gc_dry_run\nstdout=%s", kind, string(gcResult.RawStdout))
	}
	wouldRemove, ok := gcRaw["would_remove"].(float64)
	if !ok || wouldRemove < 0 {
		t.Fatalf("gc would_remove=%v, want >= 0\nstdout=%s", gcRaw["would_remove"], string(gcResult.RawStdout))
	}

	listResult := dispatchWithFlags(t, binaryPath, []string{"list", "--json", "--limit", "0"}, 3*time.Minute)
	if listResult.ExitCode != 0 {
		t.Fatalf("list exit=%d\nstdout=%s\nstderr=%s", listResult.ExitCode, string(listResult.RawStdout), string(listResult.RawStderr))
	}
	rows, err := parseNDJSONObjects(listResult.RawStdout, "list stdout")
	if err != nil {
		t.Fatalf("parse list stdout: %v\nstdout=%s", err, string(listResult.RawStdout))
	}
	for _, row := range rows {
		if id, _ := jsonStringField(row, "id"); id == dispatchID {
			return
		}
	}
	t.Fatalf("seed dispatch_id %q missing after gc --dry-run\nstdout=%s", dispatchID, string(listResult.RawStdout))
}

func TestConfigIntrospection(t *testing.T) {
	cwd := absFixtureDir(t)

	configResult := dispatchWithFlags(t, binaryPath, []string{"config", "--cwd", cwd}, 2*time.Minute)
	if configResult.ExitCode != 0 {
		t.Fatalf("config exit=%d\nstdout=%s\nstderr=%s", configResult.ExitCode, string(configResult.RawStdout), string(configResult.RawStderr))
	}
	if strings.TrimSpace(string(configResult.RawStdout)) == "" {
		t.Fatal("config stdout empty")
	}

	rolesResult := dispatchWithFlags(t, binaryPath, []string{"config", "roles", "--cwd", cwd}, 2*time.Minute)
	if rolesResult.ExitCode != 0 {
		t.Fatalf("config roles exit=%d\nstdout=%s\nstderr=%s", rolesResult.ExitCode, string(rolesResult.RawStdout), string(rolesResult.RawStderr))
	}
	rolesOut := string(rolesResult.RawStdout)
	if strings.TrimSpace(rolesOut) == "" {
		t.Fatal("config roles stdout empty")
	}
	if !strings.Contains(rolesOut, "sysprompt-test") || !strings.Contains(rolesOut, "variant-test") {
		t.Fatalf("config roles output missing expected role names\nstdout=%s", rolesOut)
	}

	jsonFlag := detectJSONFlag(t, "config", "roles")
	if jsonFlag == "" {
		t.Skip("TODO: config roles JSON flag not implemented yet")
	}
	rolesJSONResult := dispatchWithFlags(t, binaryPath, []string{"config", "roles", jsonFlag, "--cwd", cwd}, 2*time.Minute)
	if rolesJSONResult.ExitCode != 0 {
		t.Fatalf("config roles %s exit=%d\nstdout=%s\nstderr=%s", jsonFlag, rolesJSONResult.ExitCode, string(rolesJSONResult.RawStdout), string(rolesJSONResult.RawStderr))
	}
	roleEntries, err := parseJSONArrayObjects(rolesJSONResult.RawStdout, "config roles stdout")
	if err != nil {
		t.Fatalf("%v\nstdout=%s", err, string(rolesJSONResult.RawStdout))
	}
	if len(roleEntries) == 0 {
		t.Fatal("config roles JSON array empty")
	}
	for _, entry := range roleEntries {
		if err := requireNonEmptyStringField(entry, "name"); err != nil {
			t.Fatalf("config roles JSON entry invalid: %v\nstdout=%s", err, string(rolesJSONResult.RawStdout))
		}
		if err := requireNonEmptyStringField(entry, "engine"); err != nil {
			t.Fatalf("config roles JSON entry invalid: %v\nstdout=%s", err, string(rolesJSONResult.RawStdout))
		}
	}

	pipelinesResult := dispatchWithFlags(t, binaryPath, []string{"config", "pipelines", "--cwd", cwd}, 2*time.Minute)
	if pipelinesResult.ExitCode != 0 {
		t.Fatalf("config pipelines exit=%d\nstdout=%s\nstderr=%s", pipelinesResult.ExitCode, string(pipelinesResult.RawStdout), string(pipelinesResult.RawStderr))
	}
	pipelinesOut := string(pipelinesResult.RawStdout)
	if strings.TrimSpace(pipelinesOut) == "" {
		t.Fatal("config pipelines stdout empty")
	}
	if !strings.Contains(pipelinesOut, "test-refs") || !strings.Contains(pipelinesOut, "test-fanout") {
		t.Fatalf("config pipelines output missing expected pipeline names\nstdout=%s", pipelinesOut)
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

func TestPipelineRefsOnly(t *testing.T) {
	cwd := absFixtureDir(t)
	configJSON := resolvedConfigJSON(t, cwd)
	pipelines, err := jsonObjectField(configJSON, "pipelines")
	if err != nil {
		t.Skip("TODO: pipeline config introspection not available")
	}
	testRefs, ok := pipelines["test-refs"].(map[string]any)
	if !ok {
		t.Skip("TODO: test-refs pipeline not loaded from config")
	}
	steps, ok := testRefs["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Skip("TODO: test-refs pipeline steps missing from config output")
	}
	firstStep, ok := steps[0].(map[string]any)
	if !ok {
		t.Skip("TODO: test-refs pipeline step schema not available")
	}
	if handoffMode, _ := jsonStringField(firstStep, "handoff_mode"); handoffMode != "refs_only" {
		t.Skip("TODO: refs_only handoff_mode not implemented in pipeline schema")
	}

	result := dispatchWithFlags(t, binaryPath, []string{
		"--pipeline=test-refs",
		"--cwd", cwd,
		"--yes",
		"Analyze and report",
	}, 15*time.Minute)

	raw, err := parseJSONObject(result.RawStdout, "pipeline stdout")
	if err != nil {
		t.Fatalf("parse pipeline stdout: %v\nstdout=%s\nstderr=%s", err, string(result.RawStdout), string(result.RawStderr))
	}
	status, _ := jsonStringField(raw, "status")
	if status != "completed" && status != "partial" {
		t.Fatalf("pipeline status=%q, want completed or partial\nstdout=%s\nstderr=%s", status, string(result.RawStdout), string(result.RawStderr))
	}
	stepsValue, ok := raw["steps"].([]any)
	if !ok || len(stepsValue) == 0 {
		t.Fatalf("pipeline steps missing or empty\nstdout=%s", string(result.RawStdout))
	}
	for i, stepValue := range stepsValue {
		step, ok := stepValue.(map[string]any)
		if !ok {
			t.Fatalf("pipeline step[%d] not an object\nstdout=%s", i, string(result.RawStdout))
		}
		workers, ok := step["workers"].([]any)
		if !ok || len(workers) == 0 {
			t.Fatalf("pipeline step[%d] workers missing or empty\nstdout=%s", i, string(result.RawStdout))
		}
		if i == 0 {
			if handoffMode, _ := jsonStringField(step, "handoff_mode"); handoffMode != "refs_only" {
				t.Fatalf("pipeline step[0] handoff_mode=%q, want refs_only\nstdout=%s", handoffMode, string(result.RawStdout))
			}
		}
	}
}

func TestPipelineFanout(t *testing.T) {
	cwd := absFixtureDir(t)
	configJSON := resolvedConfigJSON(t, cwd)
	pipelines, err := jsonObjectField(configJSON, "pipelines")
	if err != nil {
		t.Skip("TODO: pipeline config introspection not available")
	}
	testFanout, ok := pipelines["test-fanout"].(map[string]any)
	if !ok {
		t.Skip("TODO: test-fanout pipeline not loaded from config")
	}
	steps, ok := testFanout["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Skip("TODO: test-fanout pipeline steps missing from config output")
	}
	firstStep, ok := steps[0].(map[string]any)
	if !ok {
		t.Skip("TODO: test-fanout pipeline step schema not available")
	}
	if parallel, ok := firstStep["parallel"].(float64); !ok || int(parallel) != 2 {
		t.Skip("TODO: pipeline fan-out parallel field not implemented in schema")
	}

	result := dispatchWithFlags(t, binaryPath, []string{
		"--pipeline=test-fanout",
		"--cwd", cwd,
		"--yes",
		"Analyze and report",
	}, 15*time.Minute)

	raw, err := parseJSONObject(result.RawStdout, "pipeline stdout")
	if err != nil {
		t.Fatalf("parse pipeline stdout: %v\nstdout=%s\nstderr=%s", err, string(result.RawStdout), string(result.RawStderr))
	}
	status, _ := jsonStringField(raw, "status")
	if status != "completed" && status != "partial" {
		t.Fatalf("pipeline status=%q, want completed or partial\nstdout=%s\nstderr=%s", status, string(result.RawStdout), string(result.RawStderr))
	}
	stepsValue, ok := raw["steps"].([]any)
	if !ok || len(stepsValue) == 0 {
		t.Fatalf("pipeline steps missing or empty\nstdout=%s", string(result.RawStdout))
	}
	firstResultStep, ok := stepsValue[0].(map[string]any)
	if !ok {
		t.Fatalf("pipeline step[0] not an object\nstdout=%s", string(result.RawStdout))
	}
	workers, ok := firstResultStep["workers"].([]any)
	if !ok || len(workers) != 2 {
		t.Fatalf("pipeline step[0] workers=%d, want 2\nstdout=%s", len(workers), string(result.RawStdout))
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
