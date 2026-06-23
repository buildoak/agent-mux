package config

import (
	"os"
	"testing"
)

func TestDefaultTimeoutSec(t *testing.T) {
	if DefaultTimeoutSec != 900 {
		t.Fatalf("DefaultTimeoutSec = %d, want 900", DefaultTimeoutSec)
	}
}

func TestMaxDepthDefault(t *testing.T) {
	t.Setenv("AGENT_MUX_MAX_DEPTH", "")
	if got := MaxDepth(); got != 2 {
		t.Fatalf("MaxDepth() = %d, want 2", got)
	}
}

func TestMaxDepthEnv(t *testing.T) {
	t.Setenv("AGENT_MUX_MAX_DEPTH", "5")
	if got := MaxDepth(); got != 5 {
		t.Fatalf("MaxDepth() = %d, want 5", got)
	}
}

func TestMaxDepthInvalidEnv(t *testing.T) {
	t.Setenv("AGENT_MUX_MAX_DEPTH", "not-a-number")
	if got := MaxDepth(); got != 2 {
		t.Fatalf("MaxDepth(invalid) = %d, want default 2", got)
	}
}

func TestPermissionModeDefault(t *testing.T) {
	t.Setenv("AGENT_MUX_PERMISSION_MODE", "")
	if got := PermissionMode(); got != "" {
		t.Fatalf("PermissionMode() = %q, want empty", got)
	}
}

func TestPermissionModeEnv(t *testing.T) {
	t.Setenv("AGENT_MUX_PERMISSION_MODE", "default")
	if got := PermissionMode(); got != "default" {
		t.Fatalf("PermissionMode() = %q, want %q", got, "default")
	}
}

func TestHeartbeatIntervalSecDefault(t *testing.T) {
	t.Setenv("AGENT_MUX_HEARTBEAT_INTERVAL_SEC", "")
	if got := HeartbeatIntervalSec(); got != 15 {
		t.Fatalf("HeartbeatIntervalSec() = %d, want 15", got)
	}
}

func TestHeartbeatIntervalSecEnv(t *testing.T) {
	t.Setenv("AGENT_MUX_HEARTBEAT_INTERVAL_SEC", "30")
	if got := HeartbeatIntervalSec(); got != 30 {
		t.Fatalf("HeartbeatIntervalSec() = %d, want 30", got)
	}
}

func TestDefaultModels(t *testing.T) {
	models := DefaultModels()
	if len(models["codex"]) == 0 {
		t.Fatal("DefaultModels() missing codex models")
	}
	if len(models["claude"]) == 0 {
		t.Fatal("DefaultModels() missing claude models")
	}
	if len(models["gemini"]) == 0 {
		t.Fatal("DefaultModels() missing gemini models")
	}
	agyModels := models["agy"]
	wantAgyModels := []string{
		"Gemini 3.1 Pro (High)",
		"Gemini 3.1 Pro (Low)",
		"Gemini 3.5 Flash (High)",
		"Gemini 3.5 Flash (Medium)",
		"Gemini 3.5 Flash (Low)",
		"Claude Sonnet 4.6 (Thinking)",
		"Claude Opus 4.6 (Thinking)",
		"GPT-OSS 120B (Medium)",
	}
	if len(agyModels) != len(wantAgyModels) {
		t.Fatalf("DefaultModels()[agy] = %v, want %v", agyModels, wantAgyModels)
	}
	for i, want := range wantAgyModels {
		if agyModels[i] != want {
			t.Fatalf("DefaultModels()[agy][%d] = %q, want %q", i, agyModels[i], want)
		}
	}
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{Field: "timeout", Source: "test.md", Value: -1}
	if !IsValidationError(err) {
		t.Fatal("IsValidationError should return true")
	}
	if err.Error() == "" {
		t.Fatal("ValidationError.Error() should not be empty")
	}
}

func TestDeduplicateStrings(t *testing.T) {
	got := deduplicateStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("deduplicateStrings = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("deduplicateStrings[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeduplicateStringsNil(t *testing.T) {
	got := deduplicateStrings(nil)
	if got != nil {
		t.Fatalf("deduplicateStrings(nil) = %v, want nil", got)
	}
}

func TestEnvInt(t *testing.T) {
	key := "AGENT_MUX_TEST_INT_" + t.Name()
	defer os.Unsetenv(key)

	// Unset -> default
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("envInt(unset) = %d, want 42", got)
	}

	// Valid
	os.Setenv(key, "10")
	if got := envInt(key, 42); got != 10 {
		t.Fatalf("envInt(10) = %d, want 10", got)
	}

	// Invalid -> default
	os.Setenv(key, "abc")
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("envInt(abc) = %d, want 42", got)
	}

	// Zero -> default (must be > 0)
	os.Setenv(key, "0")
	if got := envInt(key, 42); got != 42 {
		t.Fatalf("envInt(0) = %d, want 42", got)
	}
}
