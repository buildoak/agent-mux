package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

const (
	defaultTimeout = 2 * time.Second
	maxStderrBytes = 1024
)

type HookResult struct {
	Action string
	Reason string
}

type Evaluator struct {
	preDispatch []string
	onEvent     []string
}

// NewEvaluatorFromDirs discovers hook scripts from directory conventions:
//
//	<cwd>/.agent-mux/hooks/pre-dispatch/  (project-local)
//	<cwd>/.agent-mux/hooks/on-event/      (project-local)
//	~/.agent-mux/hooks/pre-dispatch/       (global fallback)
//	~/.agent-mux/hooks/on-event/           (global fallback)
//
// Executable files are collected in lexical order. Project hooks run before global.
// Hook contract: exit 0=allow, 1=block, 2=warn.
func NewEvaluatorFromDirs(cwd string) *Evaluator {
	homeDir := ""
	if h, err := os.UserHomeDir(); err == nil {
		homeDir = h
	}

	var preDispatchDirs, onEventDirs []string

	// Project-local hooks first.
	preDispatchDirs = append(preDispatchDirs, filepath.Join(cwd, ".agent-mux", "hooks", "pre-dispatch"))
	onEventDirs = append(onEventDirs, filepath.Join(cwd, ".agent-mux", "hooks", "on-event"))

	// Global fallback.
	if homeDir != "" {
		preDispatchDirs = append(preDispatchDirs, filepath.Join(homeDir, ".agent-mux", "hooks", "pre-dispatch"))
		onEventDirs = append(onEventDirs, filepath.Join(homeDir, ".agent-mux", "hooks", "on-event"))
	}

	return &Evaluator{
		preDispatch: discoverHookScripts(preDispatchDirs),
		onEvent:     discoverHookScripts(onEventDirs),
	}
}

// discoverHookScripts scans directories in order and returns executable file
// paths in lexical order within each directory.
func discoverHookScripts(dirs []string) []string {
	var scripts []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			// Check if executable (any execute bit set).
			if info.Mode()&0111 != 0 {
				scripts = append(scripts, path)
			}
		}
	}
	return scripts
}

func (e *Evaluator) CheckPrompt(prompt string, systemPrompt ...string) (bool, string) {
	systemPromptText := ""
	if len(systemPrompt) > 0 {
		systemPromptText = systemPrompt[0]
	}
	if e == nil {
		return false, ""
	}
	if len(e.preDispatch) > 0 {
		input := map[string]any{
			"phase":         "pre_dispatch",
			"prompt":        prompt,
			"system_prompt": systemPromptText,
		}
		env := []string{
			"HOOK_PHASE=pre_dispatch",
			fmt.Sprintf("HOOK_PROMPT=%s", prompt),
			fmt.Sprintf("HOOK_SYSTEM_PROMPT=%s", systemPromptText),
		}
		for _, script := range e.preDispatch {
			result := runHook(script, input, env)
			if result.Action == "block" {
				return true, result.Reason
			}
		}
	}
	return false, ""
}

func (e *Evaluator) CheckEvent(evt *types.HarnessEvent) (string, string) {
	if e == nil || evt == nil {
		return "", ""
	}
	normalizedPath := evt.FilePath
	if normalizedPath != "" {
		normalizedPath = filepath.Clean(normalizedPath)
		if abs, err := filepath.Abs(normalizedPath); err == nil {
			normalizedPath = abs
		}
	}
	if len(e.onEvent) > 0 {
		input := map[string]any{
			"phase":     "event",
			"text":      evt.Text,
			"command":   evt.Command,
			"tool":      evt.Tool,
			"file_path": normalizedPath,
		}
		env := []string{
			"HOOK_PHASE=event",
			fmt.Sprintf("HOOK_COMMAND=%s", evt.Command),
			fmt.Sprintf("HOOK_FILE_PATH=%s", normalizedPath),
			fmt.Sprintf("HOOK_TOOL=%s", evt.Tool),
			fmt.Sprintf("HOOK_TEXT=%s", evt.Text),
		}
		for _, script := range e.onEvent {
			result := runHook(script, input, env)
			switch result.Action {
			case "block":
				return "deny", result.Reason
			case "warn":
				return "warn", result.Reason
			}
		}
	}
	return "", ""
}

func (e *Evaluator) HasRules() bool {
	if e == nil {
		return false
	}
	return len(e.preDispatch) > 0 || len(e.onEvent) > 0
}

func (e *Evaluator) PromptInjection() string {
	return ""
}

func runHook(scriptPath string, input map[string]any, extraEnv []string) HookResult {
	data, err := json.Marshal(input)
	if err != nil {
		return HookResult{Action: "allow"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = append(os.Environ(), extraEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return HookResult{Action: "allow"}
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return HookResult{Action: "allow", Reason: err.Error()}
	}
	reason := strings.TrimSpace(stderr.String())
	if len(reason) > maxStderrBytes {
		reason = reason[:maxStderrBytes]
	}
	switch exitErr.ExitCode() {
	case 1:
		return HookResult{Action: "block", Reason: reason}
	case 2:
		return HookResult{Action: "warn", Reason: reason}
	default:
		return HookResult{Action: "allow", Reason: reason}
	}
}
