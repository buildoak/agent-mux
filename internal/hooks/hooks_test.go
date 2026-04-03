package hooks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0755)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckPromptAllow(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "allow.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	denied, _ := eval.CheckPrompt("hello", "")
	if denied {
		t.Error("expected allow")
	}
}

func TestCheckPromptBlock(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "block.sh", "#!/bin/bash\necho 'blocked reason' >&2\nexit 1\n")
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	denied, reason := eval.CheckPrompt("hello", "")
	if !denied {
		t.Error("expected block")
	}
	if reason != "blocked reason" {
		t.Errorf("reason = %q, want 'blocked reason'", reason)
	}
}

func TestCheckPromptWarn(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "warn.sh", "#!/bin/bash\necho 'warn reason' >&2\nexit 2\n")
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	denied, _ := eval.CheckPrompt("hello", "")
	// Warn in pre_dispatch is NOT deny — it's allow with a note
	if denied {
		t.Error("expected allow on warn (exit 2 in pre_dispatch)")
	}
}

func TestCheckEventBlock(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "block.sh", "#!/bin/bash\necho 'event blocked' >&2\nexit 1\n")
	eval := NewEvaluator(HooksConfig{OnEvent: []string{script}})
	action, reason := eval.CheckEvent(&types.HarnessEvent{Command: "rm -rf /"})
	if action != "deny" {
		t.Errorf("action = %q, want deny", action)
	}
	if reason != "event blocked" {
		t.Errorf("reason = %q", reason)
	}
}

func TestCheckEventWarn(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "warn.sh", "#!/bin/bash\necho 'event warn' >&2\nexit 2\n")
	eval := NewEvaluator(HooksConfig{OnEvent: []string{script}})
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "curl example.com"})
	if action != "warn" {
		t.Errorf("action = %q, want warn", action)
	}
}

func TestCheckEventAllow(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "allow.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluator(HooksConfig{OnEvent: []string{script}})
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "ls"})
	if action != "" {
		t.Errorf("action = %q, want empty (allow)", action)
	}
}

func TestCheckEventDenyActionWarn(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "block.sh", "#!/bin/bash\nexit 1\n")
	eval := NewEvaluator(HooksConfig{OnEvent: []string{script}, EventDenyAction: "warn"})
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "test"})
	if action != "warn" {
		t.Errorf("action = %q, want warn (event_deny_action=warn)", action)
	}
}

func TestNoScriptsConfigured(t *testing.T) {
	eval := NewEvaluator(HooksConfig{})
	denied, _ := eval.CheckPrompt("anything", "")
	if denied {
		t.Error("expected allow with empty config")
	}
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "anything"})
	if action != "" {
		t.Errorf("expected allow, got %q", action)
	}
}

func TestScriptTimeout(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "slow.sh", "#!/bin/bash\nsleep 10\nexit 1\n")
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	denied, _ := eval.CheckPrompt("test", "")
	if denied {
		t.Error("expected allow on timeout (fail-open)")
	}
}

func TestScriptNotFound(t *testing.T) {
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{"/nonexistent/script.sh"}})
	denied, _ := eval.CheckPrompt("test", "")
	if denied {
		t.Error("expected allow on missing script (fail-open)")
	}
}

func TestHasRules(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "test.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	if !eval.HasRules() {
		t.Error("expected HasRules true")
	}
	empty := NewEvaluator(HooksConfig{})
	if empty.HasRules() {
		t.Error("expected HasRules false for empty config")
	}
}

func TestPromptInjectionEmpty(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "test.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	if got := eval.PromptInjection(); got != "" {
		t.Errorf("PromptInjection() = %q, want empty", got)
	}
}

func TestNilEvaluator(t *testing.T) {
	var eval *Evaluator
	denied, _ := eval.CheckPrompt("test", "")
	if denied {
		t.Error("nil evaluator should allow")
	}
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "test"})
	if action != "" {
		t.Error("nil evaluator should allow events")
	}
}

func TestCheckEventPathNormalization(t *testing.T) {
	dir := t.TempDir()
	// Script checks if HOOK_FILE_PATH starts with / (absolute)
	script := writeScript(t, dir, "check_abs.sh", `#!/bin/bash
if [[ "${HOOK_FILE_PATH}" == /* ]]; then
    exit 0
else
    echo "path not absolute: ${HOOK_FILE_PATH}" >&2
    exit 1
fi
`)
	eval := NewEvaluator(HooksConfig{OnEvent: []string{script}})
	// Pass a relative path -- it should be normalized to absolute
	action, reason := eval.CheckEvent(&types.HarnessEvent{FilePath: "relative/path/file.go"})
	if action != "" {
		t.Errorf("expected allow (path should be normalized to absolute), got action=%q reason=%q", action, reason)
	}
}

func TestCheckPromptSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	// Script blocks if HOOK_SYSTEM_PROMPT contains "secret"
	script := writeScript(t, dir, "check_sys.sh", `#!/bin/bash
if [[ "${HOOK_SYSTEM_PROMPT}" == *secret* ]]; then
    echo "system prompt contains secret" >&2
    exit 1
fi
exit 0
`)
	eval := NewEvaluator(HooksConfig{PreDispatch: []string{script}})
	denied, _ := eval.CheckPrompt("hello", "this has a secret word")
	if !denied {
		t.Error("expected block when system prompt contains 'secret'")
	}
	denied, _ = eval.CheckPrompt("hello", "clean system prompt")
	if denied {
		t.Error("expected allow when system prompt is clean")
	}
}

func TestEnvVarsPassedToScript(t *testing.T) {
	dir := t.TempDir()
	// Script checks that all env vars are set
	script := writeScript(t, dir, "check_env.sh", `#!/bin/bash
if [ -z "${HOOK_PHASE}" ]; then echo "HOOK_PHASE not set" >&2; exit 1; fi
if [ -z "${HOOK_COMMAND}" ]; then echo "HOOK_COMMAND not set" >&2; exit 1; fi
exit 0
`)
	eval := NewEvaluator(HooksConfig{OnEvent: []string{script}})
	action, reason := eval.CheckEvent(&types.HarnessEvent{Command: "test-cmd"})
	if action != "" {
		t.Errorf("expected allow, got action=%q reason=%q", action, reason)
	}
}
