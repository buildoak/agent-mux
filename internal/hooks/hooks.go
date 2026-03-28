package hooks

import (
	"path/filepath"
	"strings"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/types"
)

type Evaluator struct {
	deny       []pattern
	warn       []pattern
	denyAction string
}

type pattern struct {
	original string
	lower    string
}

func NewEvaluator(cfg config.HooksConfig) *Evaluator {
	normalizeList := func(items []string) []pattern {
		if len(items) == 0 {
			return nil
		}
		out := make([]pattern, 0, len(items))
		for _, item := range items {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			out = append(out, pattern{original: trimmed, lower: strings.ToLower(trimmed)})
		}
		return out
	}

	action := strings.ToLower(strings.TrimSpace(cfg.EventDenyAction))
	switch action {
	case "warn":
		action = "warn"
	case "kill", "":
		action = "deny"
	default:
		action = "deny"
	}

	return &Evaluator{
		deny:       normalizeList(cfg.Deny),
		warn:       normalizeList(cfg.Warn),
		denyAction: action,
	}
}

func (e *Evaluator) CheckPrompt(parts ...string) (denied bool, matched string) {
	if e == nil {
		return false, ""
	}
	match := matchAny(e.deny, parts)
	if match == "" {
		return false, ""
	}
	return true, match
}

func (e *Evaluator) CheckEvent(evt *types.HarnessEvent) (action string, matched string) {
	if e == nil || evt == nil {
		return "", ""
	}

	candidates := []string{evt.Text, evt.Command, evt.Tool, string(evt.Raw)}
	if evt.FilePath != "" {
		candidate := filepath.Clean(evt.FilePath)
		abs, err := filepath.Abs(candidate)
		if err == nil {
			candidate = abs
		}
		candidates = append(candidates, candidate)
	}

	if match := matchAny(e.deny, candidates); match != "" {
		return e.denyAction, match
	}
	if match := matchAny(e.warn, candidates); match != "" {
		return "warn", match
	}
	return "", ""
}

func (e *Evaluator) PromptInjection() string {
	if e == nil || (len(e.deny) == 0 && len(e.warn) == 0) {
		return ""
	}

	var b strings.Builder
	b.WriteString("Agent-mux safety rules:\n")
	if len(e.deny) > 0 {
		b.WriteString("Do NOT include or execute content matching: ")
		b.WriteString(joinPatterns(e.deny))
		b.WriteString("\n")
	}
	if len(e.warn) > 0 {
		b.WriteString("Use extra caution and avoid unless required: ")
		b.WriteString(joinPatterns(e.warn))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e *Evaluator) HasRules() bool {
	if e == nil {
		return false
	}
	return len(e.deny) > 0 || len(e.warn) > 0
}

func matchAny(patterns []pattern, candidates []string) string {
	for _, pat := range patterns {
		if pat.lower == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if strings.Contains(strings.ToLower(candidate), pat.lower) {
				return pat.original
			}
		}
	}
	return ""
}

func joinPatterns(patterns []pattern) string {
	items := make([]string, 0, len(patterns))
	for _, pat := range patterns {
		items = append(items, pat.original)
	}
	return strings.Join(items, ", ")
}
