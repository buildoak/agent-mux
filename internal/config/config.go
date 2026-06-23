package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
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

// DefaultModels returns the built-in model registry per engine.
func DefaultModels() map[string][]string {
	return map[string][]string{
		"codex":  {"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.2-codex"},
		"claude": {"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"},
		"gemini": {"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-flash-preview", "gemini-3.1-pro-preview"},
		"agy": {
			"Gemini 3.1 Pro (High)",
			"Gemini 3.1 Pro (Low)",
			"Gemini 3.5 Flash (High)",
			"Gemini 3.5 Flash (Medium)",
			"Gemini 3.5 Flash (Low)",
			"Claude Sonnet 4.6 (Thinking)",
			"Claude Opus 4.6 (Thinking)",
			"GPT-OSS 120B (Medium)",
		},
	}
}

// EngineCapabilities describes the user-visible behavior agent-mux can
// currently rely on for an engine. It is intentionally conservative: a false
// value means agent-mux does not expose that capability through its stable
// dispatch contract, even if the underlying provider may support it elsewhere.
type EngineCapabilities struct {
	Engine           string   `json:"engine"`
	Models           []string `json:"models"`
	ModelSource      string   `json:"model_source"`
	ModelStatus      string   `json:"model_status"`
	ModelCachePath   string   `json:"model_cache_path,omitempty"`
	SupportsResume   bool     `json:"supports_resume"`
	SteerSemantics   string   `json:"steer_semantics"`
	EventStream      bool     `json:"event_stream"`
	ActivityTracking bool     `json:"activity_tracking"`
	TokenUsage       bool     `json:"token_usage"`
	CostUsage        bool     `json:"cost_usage"`
	ArtifactScan     bool     `json:"artifact_scan"`
	MultimodalInput  bool     `json:"multimodal_input"`
	ImageGeneration  bool     `json:"image_generation"`
	Notes            string   `json:"notes"`
}

// EngineCapabilityMatrix returns capability metadata for all engines. It may
// use a valid agy model cache, but it never invokes the agy CLI.
func EngineCapabilityMatrix() []EngineCapabilities {
	return EngineCapabilityMatrixWithAgyState(CachedAgyModelState())
}

// EngineCapabilityMatrixWithAgyState returns capability metadata using the
// supplied agy model state. It is used by explicit refresh flows to report the
// just-refreshed source/status without re-reading the cache.
func EngineCapabilityMatrixWithAgyState(agyState AgyModelState) []EngineCapabilities {
	models := DefaultModels()
	if len(agyState.Models) == 0 {
		agyState = fallbackAgyModelState("fallback")
	}
	entries := []EngineCapabilities{
		{
			Engine:           "agy",
			Models:           cloneStrings(agyState.Models),
			ModelSource:      agyState.Source,
			ModelStatus:      agyState.Status,
			ModelCachePath:   agyState.CachePath,
			SupportsResume:   true,
			SteerSemantics:   "resume_inbox_or_abort",
			EventStream:      false,
			ActivityTracking: false,
			TokenUsage:       false,
			CostUsage:        false,
			ArtifactScan:     true,
			MultimodalInput:  true,
			ImageGeneration:  true,
			Notes:            "Experimental plain-stdout adapter. Resume uses agy conversation IDs discovered from agy.log; multimodal input and image generation are live-smoke verified but not exposed as structured events.",
		},
		{
			Engine:           "claude",
			Models:           cloneStrings(models["claude"]),
			ModelSource:      "built_in",
			ModelStatus:      "ok",
			SupportsResume:   true,
			SteerSemantics:   "resume_inbox_or_abort",
			EventStream:      true,
			ActivityTracking: true,
			TokenUsage:       true,
			CostUsage:        false,
			ArtifactScan:     true,
			MultimodalInput:  false,
			ImageGeneration:  false,
			Notes:            "Streams structured events and supports resume after session init.",
		},
		{
			Engine:           "codex",
			Models:           cloneStrings(models["codex"]),
			ModelSource:      "built_in",
			ModelStatus:      "ok",
			SupportsResume:   true,
			SteerSemantics:   "resume_inbox_or_abort",
			EventStream:      true,
			ActivityTracking: true,
			TokenUsage:       true,
			CostUsage:        false,
			ArtifactScan:     true,
			MultimodalInput:  false,
			ImageGeneration:  false,
			Notes:            "Streams NDJSON events and supports resume after thread start.",
		},
		{
			Engine:           "gemini",
			Models:           cloneStrings(models["gemini"]),
			ModelSource:      "built_in",
			ModelStatus:      "ok",
			SupportsResume:   true,
			SteerSemantics:   "resume_inbox_or_abort_latest_fallback",
			EventStream:      true,
			ActivityTracking: true,
			TokenUsage:       true,
			CostUsage:        false,
			ArtifactScan:     true,
			MultimodalInput:  false,
			ImageGeneration:  false,
			Notes:            "Streams structured events. UUID resume IDs fall back to Gemini CLI latest-session resume semantics.",
		},
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Engine < entries[j].Engine
	})
	return entries
}

func cloneStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	copy(out, items)
	return out
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
