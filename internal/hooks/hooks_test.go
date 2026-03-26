package hooks

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/types"
)

func TestCheckPromptDeny(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"rm -rf"}})
	denied, matched := eval.CheckPrompt("please run rm -rf /")
	if !denied {
		t.Fatalf("denied = false, want true")
	}
	if matched != "rm -rf" {
		t.Fatalf("matched = %q, want %q", matched, "rm -rf")
	}
}

func TestCheckPromptAllow(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"drop database"}})
	denied, matched := eval.CheckPrompt("hello world")
	if denied {
		t.Fatalf("denied = true, want false")
	}
	if matched != "" {
		t.Fatalf("matched = %q, want empty", matched)
	}
}

func TestCheckEventDeny(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"DELETE"}, EventDenyAction: "kill"})
	evt := &types.HarnessEvent{Kind: types.EventCommandRun, Command: "DELETE FROM users"}
	action, matched := eval.CheckEvent(evt)
	if action != "deny" {
		t.Fatalf("action = %q, want deny", action)
	}
	if matched != "DELETE" {
		t.Fatalf("matched = %q, want DELETE", matched)
	}
}

func TestCheckEventWarn(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Warn: []string{"secrets"}})
	evt := &types.HarnessEvent{Kind: types.EventProgress, Text: "found secrets"}
	action, matched := eval.CheckEvent(evt)
	if action != "warn" {
		t.Fatalf("action = %q, want warn", action)
	}
	if matched != "secrets" {
		t.Fatalf("matched = %q, want secrets", matched)
	}

	eval = NewEvaluator(config.HooksConfig{Deny: []string{"password"}, EventDenyAction: "warn"})
	evt = &types.HarnessEvent{Kind: types.EventProgress, Text: "password found"}
	action, matched = eval.CheckEvent(evt)
	if action != "warn" {
		t.Fatalf("action = %q, want warn", action)
	}
	if matched != "password" {
		t.Fatalf("matched = %q, want password", matched)
	}
}

func TestCheckEventAllow(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"blocked"}, Warn: []string{"warn"}})
	evt := &types.HarnessEvent{Kind: types.EventProgress, Text: "all good"}
	action, matched := eval.CheckEvent(evt)
	if action != "" || matched != "" {
		t.Fatalf("action/matched = %q/%q, want empty", action, matched)
	}
}

func TestCaseInsensitive(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"SeCrEt"}})
	denied, matched := eval.CheckPrompt("do not leak secret")
	if !denied || matched != "SeCrEt" {
		t.Fatalf("denied/matched = %v/%q, want true/SeCrEt", denied, matched)
	}
	evt := &types.HarnessEvent{Kind: types.EventProgress, Text: "SECRET"}
	action, matched := eval.CheckEvent(evt)
	if action != "deny" || matched != "SeCrEt" {
		t.Fatalf("action/matched = %q/%q, want deny/SeCrEt", action, matched)
	}
}

func TestPromptInjection(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"rm -rf"}, Warn: []string{"secrets"}})
	inj := eval.PromptInjection()
	if inj == "" {
		t.Fatal("prompt injection empty, want content")
	}
	if !strings.Contains(inj, "rm -rf") || !strings.Contains(inj, "secrets") {
		t.Fatalf("prompt injection missing patterns: %q", inj)
	}

	empty := NewEvaluator(config.HooksConfig{})
	if got := empty.PromptInjection(); got != "" {
		t.Fatalf("empty prompt injection = %q, want empty", got)
	}
}

func TestCheckEventNil(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"blocked"}})
	action, matched := eval.CheckEvent(nil)
	if action != "" || matched != "" {
		t.Fatalf("action/matched = %q/%q, want empty", action, matched)
	}
}

func TestDenyPrecedenceOverWarn(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"token"}, Warn: []string{"token"}})
	evt := &types.HarnessEvent{Kind: types.EventProgress, Text: "token leak"}
	action, matched := eval.CheckEvent(evt)
	if action != "deny" {
		t.Fatalf("action = %q, want deny", action)
	}
	if matched != "token" {
		t.Fatalf("matched = %q, want token", matched)
	}
}

func TestAbsolutePathMatch(t *testing.T) {
	relPath := filepath.Join("tmp", "file.txt")
	absPath, err := filepath.Abs(relPath)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	eval := NewEvaluator(config.HooksConfig{Deny: []string{absPath}})
	evt := &types.HarnessEvent{Kind: types.EventFileWrite, FilePath: relPath}
	action, matched := eval.CheckEvent(evt)
	if action != "deny" {
		t.Fatalf("action = %q, want deny", action)
	}
	if matched != absPath {
		t.Fatalf("matched = %q, want %q", matched, absPath)
	}
}

func TestEmptyPatternsIgnored(t *testing.T) {
	eval := NewEvaluator(config.HooksConfig{Deny: []string{"", "  "}, Warn: []string{"\t"}})
	if eval.HasRules() {
		t.Fatal("HasRules = true, want false")
	}
	denied, matched := eval.CheckPrompt("anything")
	if denied || matched != "" {
		t.Fatalf("denied/matched = %v/%q, want false/empty", denied, matched)
	}
}
