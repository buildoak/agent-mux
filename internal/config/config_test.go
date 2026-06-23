package config

import (
	"os"
	"path/filepath"
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

func TestModelsWithCachedAgyUsesValidCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cachePath, err := AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAgyModelCache(cachePath, []string{"Gemini Fresh 1.0", "Gemini Fresh 1.0"}); err != nil {
		t.Fatal(err)
	}

	models := ModelsWithCachedAgy()
	agyModels := models["agy"]
	if len(agyModels) != 1 || agyModels[0] != "Gemini Fresh 1.0" {
		t.Fatalf("ModelsWithCachedAgy()[agy] = %v, want cached Gemini Fresh 1.0", agyModels)
	}
}

func TestModelsWithCachedAgyFallsBackWithoutCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	models := ModelsWithCachedAgy()
	if got := models["agy"][0]; got != "Gemini 3.1 Pro (High)" {
		t.Fatalf("ModelsWithCachedAgy fallback first agy model = %q", got)
	}
}

func TestParseAgyModelNamesText(t *testing.T) {
	got := ParseAgyModelNames([]byte("Available models:\n- Gemini Fresh 1.0\n2. Claude Another 2.0\n"))
	want := []string{"Gemini Fresh 1.0", "Claude Another 2.0"}
	if len(got) != len(want) {
		t.Fatalf("ParseAgyModelNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseAgyModelNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseAgyModelNamesJSON(t *testing.T) {
	got := ParseAgyModelNames([]byte(`{"models":[{"name":"Gemini JSON 1.0"},{"display_name":"Claude Display 2.0"}]}`))
	want := []string{"Gemini JSON 1.0", "Claude Display 2.0"}
	if len(got) != len(want) {
		t.Fatalf("ParseAgyModelNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseAgyModelNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseAgyModelNamesRejectsErrorProse(t *testing.T) {
	got := ParseAgyModelNames([]byte("ERROR: authentication required\nPlease login to continue\n"))
	if len(got) != 0 {
		t.Fatalf("ParseAgyModelNames accepted error prose: %v", got)
	}
}

func TestRefreshAgyModelCacheUsesFakeAgyAndWritesCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)

	fakeAgy := filepath.Join(binDir, "agy")
	if err := os.WriteFile(fakeAgy, []byte("#!/bin/sh\nprintf '%s\\n' 'Available models:' '- Gemini Fresh 1.0' '- Claude Another 2.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	state, err := RefreshAgyModelCache()
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != "agy_models" || state.Status != "refreshed" {
		t.Fatalf("refresh state source/status = %s/%s, want agy_models/refreshed", state.Source, state.Status)
	}
	if len(state.Models) != 2 || state.Models[0] != "Gemini Fresh 1.0" {
		t.Fatalf("refresh models = %v", state.Models)
	}

	cached := CachedAgyModelState()
	if cached.Source != "cache" || cached.Status != "ok" {
		t.Fatalf("cached source/status = %s/%s, want cache/ok", cached.Source, cached.Status)
	}
	if len(cached.Models) != 2 || cached.Models[1] != "Claude Another 2.0" {
		t.Fatalf("cached models = %v", cached.Models)
	}
}

func TestRefreshAgyModelCacheRejectsErrorBanner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)

	fakeAgy := filepath.Join(binDir, "agy")
	if err := os.WriteFile(fakeAgy, []byte("#!/bin/sh\nprintf '%s\\n' 'ERROR: authentication required' 'Please login to continue'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	state, err := RefreshAgyModelCache()
	if err == nil {
		t.Fatal("RefreshAgyModelCache succeeded for error banner, want error")
	}
	if state.Source != "built_in" || state.Status != "refresh_empty" {
		t.Fatalf("state source/status = %s/%s, want built_in/refresh_empty", state.Source, state.Status)
	}
	cachePath, err := AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("error banner wrote cache unexpectedly; stat err=%v", err)
	}
}

func TestCachedAgyModelStateRejectsInvalidMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cachePath, err := AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"version":999,"source":"agy_models","status":"ok","models":["Gemini Cached 1.0"],"refreshed_at":"2026-06-23T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	state := CachedAgyModelState()
	if state.Source != "built_in" || state.Status != "fallback" {
		t.Fatalf("state source/status = %s/%s, want built_in/fallback", state.Source, state.Status)
	}
	if got := ModelsWithCachedAgy()["agy"][0]; got != "Gemini 3.1 Pro (High)" {
		t.Fatalf("ModelsWithCachedAgy first agy model = %q, want built-in fallback", got)
	}
}

func TestEngineCapabilityMatrix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	entries := EngineCapabilityMatrix()
	if len(entries) != 4 {
		t.Fatalf("EngineCapabilityMatrix() returned %d entries, want 4: %#v", len(entries), entries)
	}

	wantOrder := []string{"agy", "claude", "codex", "gemini"}
	byEngine := map[string]EngineCapabilities{}
	for i, entry := range entries {
		if entry.Engine != wantOrder[i] {
			t.Fatalf("entries[%d].Engine = %q, want %q", i, entry.Engine, wantOrder[i])
		}
		byEngine[entry.Engine] = entry
		if entry.ModelSource != "built_in" {
			t.Fatalf("%s.ModelSource = %q, want built_in", entry.Engine, entry.ModelSource)
		}
		if entry.ModelStatus == "" {
			t.Fatalf("%s.ModelStatus is empty", entry.Engine)
		}
		if len(entry.Models) == 0 {
			t.Fatalf("%s models missing", entry.Engine)
		}
	}

	agy := byEngine["agy"]
	if !agy.SupportsResume {
		t.Fatal("agy.SupportsResume = false, want true")
	}
	if agy.EventStream {
		t.Fatal("agy.EventStream = true, want false")
	}
	if agy.ActivityTracking {
		t.Fatal("agy.ActivityTracking = true, want false")
	}
	if agy.TokenUsage {
		t.Fatal("agy.TokenUsage = true, want false")
	}
	if !agy.ArtifactScan {
		t.Fatal("agy.ArtifactScan = false, want true")
	}
	if !agy.MultimodalInput {
		t.Fatal("agy.MultimodalInput = false, want true")
	}
	if !agy.ImageGeneration {
		t.Fatal("agy.ImageGeneration = false, want true")
	}
	if agy.SteerSemantics != "resume_inbox_or_abort" {
		t.Fatalf("agy.SteerSemantics = %q, want resume_inbox_or_abort", agy.SteerSemantics)
	}

	codex := byEngine["codex"]
	if !codex.SupportsResume || !codex.EventStream || !codex.ActivityTracking || !codex.TokenUsage {
		t.Fatalf("codex capabilities too weak: %#v", codex)
	}
	if codex.CostUsage {
		t.Fatal("codex.CostUsage = true, want false until pricing is implemented")
	}
}

func TestEngineCapabilityMatrixClonesModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	entries := EngineCapabilityMatrix()
	entries[0].Models[0] = "mutated"

	again := EngineCapabilityMatrix()
	if again[0].Models[0] == "mutated" {
		t.Fatal("EngineCapabilityMatrix returned shared model slice")
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
