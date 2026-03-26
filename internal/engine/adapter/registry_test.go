package adapter

import (
	"strings"
	"testing"
)

func TestRegistryGetCodex(t *testing.T) {
	r := NewRegistry(map[string][]string{})

	adp, err := r.Get("codex")
	if err != nil {
		t.Fatalf("Get(codex): %v", err)
	}
	if adp == nil {
		t.Fatal("Get(codex) returned nil adapter")
	}
}

func TestRegistryGetClaude(t *testing.T) {
	r := NewRegistry(map[string][]string{})

	adp, err := r.Get("claude")
	if err != nil {
		t.Fatalf("Get(claude): %v", err)
	}
	if adp == nil {
		t.Fatal("Get(claude) returned nil adapter")
	}
}

func TestRegistryGetGemini(t *testing.T) {
	r := NewRegistry(map[string][]string{})

	adp, err := r.Get("gemini")
	if err != nil {
		t.Fatalf("Get(gemini): %v", err)
	}
	if adp == nil {
		t.Fatal("Get(gemini) returned nil adapter")
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry(map[string][]string{})

	_, err := r.Get("unknown")
	if err == nil {
		t.Fatal("Get(unknown) error = nil, want error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "not found") {
		t.Fatalf("error = %q, want to contain %q", msg, "not found")
	}
	if !strings.Contains(strings.ToLower(msg), "valid") {
		t.Fatalf("error = %q, want to mention valid engines", msg)
	}
	for _, engine := range []string{"claude", "codex", "gemini"} {
		if !strings.Contains(msg, engine) {
			t.Fatalf("error = %q, want to contain engine %q", msg, engine)
		}
	}
}

func TestRegistryValidEngines(t *testing.T) {
	r := NewRegistry(map[string][]string{})

	got := r.ValidEngines()
	want := []string{"claude", "codex", "gemini"}
	if len(got) != len(want) {
		t.Fatalf("len(ValidEngines) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ValidEngines()[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestRegistryValidModels(t *testing.T) {
	r := NewRegistry(map[string][]string{
		"codex": {"gpt-5.4"},
	})

	got := r.ValidModels("codex")
	if len(got) != 1 || got[0] != "gpt-5.4" {
		t.Fatalf("ValidModels(codex) = %v, want [gpt-5.4]", got)
	}
}

func TestRegistryValidModelsUnknown(t *testing.T) {
	r := NewRegistry(map[string][]string{
		"codex": {"gpt-5.4"},
	})

	got := r.ValidModels("unknown")
	if got != nil {
		t.Fatalf("ValidModels(unknown) = %v, want nil", got)
	}
}

func TestRegistryCustomRegister(t *testing.T) {
	r := NewRegistry(map[string][]string{})

	r.Register("custom", &CodexAdapter{})

	adp, err := r.Get("custom")
	if err != nil {
		t.Fatalf("Get(custom): %v", err)
	}
	if adp == nil {
		t.Fatal("Get(custom) returned nil adapter")
	}
}
