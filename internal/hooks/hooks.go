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

type HooksConfig struct {
	PreDispatch     []string `toml:"pre_dispatch"`
	OnEvent         []string `toml:"on_event"`
	EventDenyAction string   `toml:"event_deny_action"`
}

type Evaluator struct {
	preDispatch []string
	onEvent     []string
	denyAction  string
}

func NewEvaluator(cfg HooksConfig) *Evaluator {
	action := strings.ToLower(strings.TrimSpace(cfg.EventDenyAction))
	if action != "warn" {
		action = "kill"
	}
	return &Evaluator{
		preDispatch: expandPaths(cfg.PreDispatch),
		onEvent:     expandPaths(cfg.OnEvent),
		denyAction:  action,
	}
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
				if e.denyAction == "warn" {
					return "warn", result.Reason
				}
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

func expandPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		out = append(out, p)
	}
	return out
}
