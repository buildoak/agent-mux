package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/types"
)

func TestPreflightAgyModelValidationUsesCache(t *testing.T) {
	isolateHome(t)
	t.Setenv("PATH", t.TempDir())

	cachePath, err := config.AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"version":1,"source":"agy_models","status":"ok","models":["Gemini Cached 1.0"],"refreshed_at":"2026-06-23T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := preflightDispatchSpec(&types.DispatchSpec{Engine: "agy", Model: "Gemini Cached 1.0"}); err != nil {
		t.Fatalf("preflightDispatchSpec returned %v, want cached model accepted", err)
	}

	dispatchErr := preflightDispatchSpec(&types.DispatchSpec{Engine: "agy", Model: "Gemini 3.1 Pro (High)"})
	if dispatchErr == nil || dispatchErr.Code != "model_not_found" {
		t.Fatalf("preflightDispatchSpec error = %#v, want model_not_found for built-in model after cache override", dispatchErr)
	}
	if !strings.Contains(dispatchErr.Hint, "Gemini Cached 1.0") {
		t.Fatalf("hint = %q, want cached model list", dispatchErr.Hint)
	}
}

func TestDispatchAgyCachedModelDoesNotRefreshModels(t *testing.T) {
	isolateHome(t)

	cachePath, err := config.AgyModelCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"version":1,"source":"agy_models","status":"ok","models":["Gemini Cached 1.0"],"refreshed_at":"2026-06-23T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	fakeAgy := filepath.Join(binDir, "agy")
	script := "#!/bin/sh\nif [ \"$1\" = \"models\" ]; then touch \"$HOME/models-called\"; printf '%s\\n' 'Unexpected Model'; exit 0; fi\nprintf '%s\\n' 'agy dispatch ok'\n"
	if err := os.WriteFile(fakeAgy, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--yes", "--engine", "agy", "--model", "Gemini Cached 1.0", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	result := decodeResult(t, stdout.Bytes())
	if result.Error != nil {
		t.Fatalf("result error = %#v, want successful fake agy dispatch", result.Error)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "models-called")); !os.IsNotExist(err) {
		t.Fatalf("normal dispatch called `agy models`; stat err=%v", err)
	}
}
