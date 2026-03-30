//go:build axeval

package axeval

import (
	"testing"
	"time"
)

func TestLifecycleListStatusInspect(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 test — requires AX_EVAL_HARD=1")
	}
	tc := TestCase{
		Engine:       "codex",
		Model:        "gpt-5.4-mini",
		Effort:       "high",
		Prompt:       "What is 2+2?",
		CWD:          fixtureDir(),
		TimeoutSec:   120,
		MaxWallClock: 3 * time.Minute,
		SkipSkills:   true,
	}

	result := dispatch(t, binaryPath, tc)
	if result.Status != "completed" {
		t.Fatalf("dispatch status = %q, want completed\nstdout=%s\nstderr=%s", result.Status, string(result.RawStdout), string(result.RawStderr))
	}

	raw, err := stdoutJSONObject(result)
	if err != nil {
		t.Fatalf("parse dispatch stdout: %v\nstdout=%s", err, string(result.RawStdout))
	}
	dispatchID, ok := jsonStringField(raw, "dispatch_id")
	if !ok || dispatchID == "" {
		t.Fatalf("dispatch_id missing from dispatch stdout: %s", string(result.RawStdout))
	}

	listResult := dispatchWithFlags(t, binaryPath, []string{"list", "-json", "-limit", "0"}, 3*time.Minute)
	if listResult.ExitCode != 0 {
		t.Fatalf("list exit=%d\nstdout=%s\nstderr=%s", listResult.ExitCode, string(listResult.RawStdout), string(listResult.RawStderr))
	}
	listRows, err := parseNDJSONObjects(listResult.RawStdout, "list stdout")
	if err != nil {
		t.Fatalf("parse list output: %v\nstdout=%s", err, string(listResult.RawStdout))
	}
	found := false
	for _, row := range listRows {
		if id, ok := jsonStringField(row, "id"); ok && id == dispatchID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("dispatch_id %q not found in list output\nstdout=%s", dispatchID, string(listResult.RawStdout))
	}

	statusResult := dispatchWithFlags(t, binaryPath, []string{"status", dispatchID, "-json"}, 3*time.Minute)
	if statusResult.ExitCode != 0 {
		t.Fatalf("status exit=%d\nstdout=%s\nstderr=%s", statusResult.ExitCode, string(statusResult.RawStdout), string(statusResult.RawStderr))
	}
	statusRaw, err := parseJSONObject(statusResult.RawStdout, "status stdout")
	if err != nil {
		t.Fatalf("parse status stdout: %v\nstdout=%s", err, string(statusResult.RawStdout))
	}
	state, _ := jsonStringField(statusRaw, "status")
	if state == "" {
		state, _ = jsonStringField(statusRaw, "state")
	}
	if state != "completed" {
		t.Fatalf("status/state = %q, want completed\nstdout=%s", state, string(statusResult.RawStdout))
	}

	inspectResult := dispatchWithFlags(t, binaryPath, []string{"inspect", dispatchID, "-json"}, 3*time.Minute)
	if inspectResult.ExitCode != 0 {
		t.Fatalf("inspect exit=%d\nstdout=%s\nstderr=%s", inspectResult.ExitCode, string(inspectResult.RawStdout), string(inspectResult.RawStderr))
	}
	inspectRaw, err := parseJSONObject(inspectResult.RawStdout, "inspect stdout")
	if err != nil {
		t.Fatalf("parse inspect stdout: %v\nstdout=%s", err, string(inspectResult.RawStdout))
	}
	for _, key := range []string{"dispatch_id", "response", "artifact_dir"} {
		if err := requireNonEmptyStringField(inspectRaw, key); err != nil {
			t.Fatalf("inspect output invalid: %v\nstdout=%s", err, string(inspectResult.RawStdout))
		}
	}
}
