//go:build axeval

package axeval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// dispatch runs agent-mux with the given TestCase and returns a parsed Result.
func dispatch(t *testing.T, binary string, tc TestCase) Result {
	t.Helper()

	artifactDir := t.TempDir()

	// Build the JSON dispatch spec for --stdin mode.
	spec := map[string]any{
		"engine":       tc.Engine,
		"model":        tc.Model,
		"effort":       tc.Effort,
		"prompt":       tc.Prompt,
		"cwd":          tc.CWD,
		"artifact_dir": artifactDir,
		"skip_skills":  tc.SkipSkills,
	}
	if tc.TimeoutSec > 0 {
		spec["timeout_sec"] = tc.TimeoutSec
	}
	if len(tc.EngineOpts) > 0 {
		opts := make(map[string]any, len(tc.EngineOpts))
		for k, v := range tc.EngineOpts {
			opts[k] = v
		}
		spec["engine_opts"] = opts
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal dispatch spec: %v", err)
	}

	// Set up context with wall-clock timeout.
	wallClock := tc.MaxWallClock
	if wallClock == 0 {
		wallClock = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), wallClock)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--stdin", "--yes")
	cmd.Stdin = bytes.NewReader(specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Context deadline or other error.
			exitCode = -1
		}
	}

	result := Result{
		ArtifactDir: artifactDir,
		Duration:    duration,
		ExitCode:    exitCode,
		RawStdout:   stdout.Bytes(),
		RawStderr:   stderr.Bytes(),
	}

	// Parse stdout as JSON to extract status, response, error fields.
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Logf("stdout not valid JSON: %s", stdout.String())
		t.Logf("stderr: %s", stderr.String())
		result.Status = "parse_error"
		result.ErrorMessage = fmt.Sprintf("failed to parse stdout as JSON: %v", err)
		return result
	}

	if s, ok := raw["status"].(string); ok {
		result.Status = s
	}
	if r, ok := raw["response"].(string); ok {
		result.Response = r
	}
	if errObj, ok := raw["error"].(map[string]any); ok {
		if code, ok := errObj["code"].(string); ok {
			result.ErrorCode = code
		}
		if msg, ok := errObj["message"].(string); ok {
			result.ErrorMessage = msg
		}
	}

	// Parse events.jsonl from artifact dir.
	result.Events = parseEvents(artifactDir)

	return result
}

// parseEvents reads events.jsonl from the artifact dir and returns parsed events.
func parseEvents(artifactDir string) []Event {
	eventsPath := filepath.Join(artifactDir, "events.jsonl")
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil // No events file is fine for error cases.
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		events = append(events, evt)
	}
	return events
}
