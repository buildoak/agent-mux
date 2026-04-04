package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// --- Hardcoded defaults ---

// DefaultTimeoutSec is the fallback timeout when no timeout is specified
// via CLI flag or frontmatter. Effort level does not affect timeout.
const DefaultTimeoutSec = 900

const defaultMaxDepth = 2
const defaultPermissionMode = ""

// Liveness defaults.
const defaultHeartbeatIntervalSec = 15
const defaultSilenceWarnSeconds = 90
const defaultSilenceKillSeconds = 180

// DefaultAsyncPollInterval is the hardcoded default when neither CLI flag
// nor env provides a value.
const DefaultAsyncPollInterval = 60 * time.Second

// --- Validation ---

type ValidationError struct {
	Field  string
	Source string
	Value  int
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Source != "" {
		return fmt.Sprintf("invalid %s in %q: must be > 0 (got %d)", e.Field, e.Source, e.Value)
	}
	return fmt.Sprintf("invalid %s: must be > 0 (got %d)", e.Field, e.Value)
}

func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

func validatePositiveInt(field, source string, value int) error {
	if value > 0 {
		return nil
	}
	return &ValidationError{
		Field:  field,
		Source: source,
		Value:  value,
	}
}

// --- Public API (replaces Config struct) ---

// MaxDepth returns the max recursion depth from env or hardcoded default.
func MaxDepth() int {
	return envInt("AGENT_MUX_MAX_DEPTH", defaultMaxDepth)
}

// PermissionMode returns the permission mode from env or hardcoded default.
func PermissionMode() string {
	if v := os.Getenv("AGENT_MUX_PERMISSION_MODE"); v != "" {
		return v
	}
	return defaultPermissionMode
}

// HeartbeatIntervalSec returns the heartbeat interval from env or hardcoded default.
func HeartbeatIntervalSec() int {
	return envInt("AGENT_MUX_HEARTBEAT_INTERVAL_SEC", defaultHeartbeatIntervalSec)
}

// SilenceWarnSeconds returns the silence warn threshold from env or hardcoded default.
func SilenceWarnSeconds() int {
	return envInt("AGENT_MUX_SILENCE_WARN_SECONDS", defaultSilenceWarnSeconds)
}

// SilenceKillSeconds returns the silence kill threshold from env or hardcoded default.
func SilenceKillSeconds() int {
	return envInt("AGENT_MUX_SILENCE_KILL_SECONDS", defaultSilenceKillSeconds)
}

// DefaultModels returns the built-in model registry per engine.
func DefaultModels() map[string][]string {
	return map[string][]string{
		"codex":  {"gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"},
		"claude": {"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"},
		"gemini": {"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview", "gemini-3.1-pro-preview"},
	}
}

// envInt reads an integer from the named env var, returning defaultVal if unset or unparseable.
func envInt(key string, defaultVal int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultVal
	}
	return v
}

// deduplicateStrings returns a new slice with duplicate entries removed,
// preserving the order of first occurrence.
func deduplicateStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
