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
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, preDir, "allow.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluatorFromDirs(cwd)
	denied, _ := eval.CheckPrompt("hello", "")
	if denied {
		t.Error("expected allow")
	}
}

func TestCheckPromptBlock(t *testing.T) {
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, preDir, "block.sh", "#!/bin/bash\necho 'blocked reason' >&2\nexit 1\n")
	eval := NewEvaluatorFromDirs(cwd)
	denied, reason := eval.CheckPrompt("hello", "")
	if !denied {
		t.Error("expected block")
	}
	if reason != "blocked reason" {
		t.Errorf("reason = %q, want 'blocked reason'", reason)
	}
}

func TestCheckPromptWarn(t *testing.T) {
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, preDir, "warn.sh", "#!/bin/bash\necho 'warn reason' >&2\nexit 2\n")
	eval := NewEvaluatorFromDirs(cwd)
	denied, _ := eval.CheckPrompt("hello", "")
	// Warn in pre_dispatch is NOT deny — it's allow with a note
	if denied {
		t.Error("expected allow on warn (exit 2 in pre_dispatch)")
	}
}

func TestCheckEventBlock(t *testing.T) {
	cwd := t.TempDir()
	onDir := filepath.Join(cwd, ".agent-mux", "hooks", "on-event")
	if err := os.MkdirAll(onDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, onDir, "block.sh", "#!/bin/bash\necho 'event blocked' >&2\nexit 1\n")
	eval := NewEvaluatorFromDirs(cwd)
	action, reason := eval.CheckEvent(&types.HarnessEvent{Command: "rm -rf /"})
	if action != "deny" {
		t.Errorf("action = %q, want deny", action)
	}
	if reason != "event blocked" {
		t.Errorf("reason = %q", reason)
	}
}

func TestCheckEventWarn(t *testing.T) {
	cwd := t.TempDir()
	onDir := filepath.Join(cwd, ".agent-mux", "hooks", "on-event")
	if err := os.MkdirAll(onDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, onDir, "warn.sh", "#!/bin/bash\necho 'event warn' >&2\nexit 2\n")
	eval := NewEvaluatorFromDirs(cwd)
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "curl example.com"})
	if action != "warn" {
		t.Errorf("action = %q, want warn", action)
	}
}

func TestCheckEventAllow(t *testing.T) {
	cwd := t.TempDir()
	onDir := filepath.Join(cwd, ".agent-mux", "hooks", "on-event")
	if err := os.MkdirAll(onDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, onDir, "allow.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluatorFromDirs(cwd)
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "ls"})
	if action != "" {
		t.Errorf("action = %q, want empty (allow)", action)
	}
}

func TestNoScriptsConfigured(t *testing.T) {
	cwd := t.TempDir()
	eval := NewEvaluatorFromDirs(cwd)
	denied, _ := eval.CheckPrompt("anything", "")
	if denied {
		t.Error("expected allow with empty dirs")
	}
	action, _ := eval.CheckEvent(&types.HarnessEvent{Command: "anything"})
	if action != "" {
		t.Errorf("expected allow, got %q", action)
	}
}

func TestHasRules(t *testing.T) {
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, preDir, "test.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluatorFromDirs(cwd)
	if !eval.HasRules() {
		t.Error("expected HasRules true")
	}
	empty := NewEvaluatorFromDirs(t.TempDir())
	if empty.HasRules() {
		t.Error("expected HasRules false for empty dirs")
	}
}

func TestPromptInjectionEmpty(t *testing.T) {
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, preDir, "test.sh", "#!/bin/bash\nexit 0\n")
	eval := NewEvaluatorFromDirs(cwd)
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
	cwd := t.TempDir()
	onDir := filepath.Join(cwd, ".agent-mux", "hooks", "on-event")
	if err := os.MkdirAll(onDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Script checks if HOOK_FILE_PATH starts with / (absolute)
	writeScript(t, onDir, "check_abs.sh", `#!/bin/bash
if [[ "${HOOK_FILE_PATH}" == /* ]]; then
    exit 0
else
    echo "path not absolute: ${HOOK_FILE_PATH}" >&2
    exit 1
fi
`)
	eval := NewEvaluatorFromDirs(cwd)
	// Pass a relative path -- it should be normalized to absolute
	action, reason := eval.CheckEvent(&types.HarnessEvent{FilePath: "relative/path/file.go"})
	if action != "" {
		t.Errorf("expected allow (path should be normalized to absolute), got action=%q reason=%q", action, reason)
	}
}

func TestCheckPromptSystemPrompt(t *testing.T) {
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Script blocks if HOOK_SYSTEM_PROMPT contains "secret"
	writeScript(t, preDir, "check_sys.sh", `#!/bin/bash
if [[ "${HOOK_SYSTEM_PROMPT}" == *secret* ]]; then
    echo "system prompt contains secret" >&2
    exit 1
fi
exit 0
`)
	eval := NewEvaluatorFromDirs(cwd)
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
	cwd := t.TempDir()
	onDir := filepath.Join(cwd, ".agent-mux", "hooks", "on-event")
	if err := os.MkdirAll(onDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Script checks that all env vars are set
	writeScript(t, onDir, "check_env.sh", `#!/bin/bash
if [ -z "${HOOK_PHASE}" ]; then echo "HOOK_PHASE not set" >&2; exit 1; fi
if [ -z "${HOOK_COMMAND}" ]; then echo "HOOK_COMMAND not set" >&2; exit 1; fi
exit 0
`)
	eval := NewEvaluatorFromDirs(cwd)
	action, reason := eval.CheckEvent(&types.HarnessEvent{Command: "test-cmd"})
	if action != "" {
		t.Errorf("expected allow, got action=%q reason=%q", action, reason)
	}
}

func TestNonExecutableFilesSkipped(t *testing.T) {
	cwd := t.TempDir()
	preDir := filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Non-executable file should be skipped
	if err := os.WriteFile(filepath.Join(preDir, "not-exec.sh"), []byte("#!/bin/bash\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eval := NewEvaluatorFromDirs(cwd)
	if eval.HasRules() {
		t.Error("non-executable file should not be treated as a hook script")
	}
}
