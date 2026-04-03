package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
)

// ---------------------------------------------------------------------------
// inspect subcommand tests
// ---------------------------------------------------------------------------

func TestInspectCommandOutputsHumanSummary(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	writeStoreRecord(t, record, "inspect test response", true)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"inspect", record.ID}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"Dispatch ID:",
		record.ID,
		"Status:",
		record.Status,
		"Engine:",
		record.Engine,
		"Model:",
		record.Model,
		"Role:",
		record.Role,
		"Duration:",
		"Started:",
		record.StartedAt,
		"Cwd:",
		"--- Response ---",
		"inspect test response",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, want substring %q", out, want)
		}
	}
}

func TestInspectCommandJSONOutput(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	writeStoreRecord(t, record, "json inspect response", true)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"inspect", "--json", record.ID}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, stdout.String())
	}

	if result["dispatch_id"] != record.ID {
		t.Fatalf("dispatch_id = %v, want %q", result["dispatch_id"], record.ID)
	}
	if result["response"] != "json inspect response" {
		t.Fatalf("response = %v, want %q", result["response"], "json inspect response")
	}
	rec, ok := result["record"].(map[string]any)
	if !ok {
		t.Fatalf("record = %#v, want object", result["record"])
	}
	if rec["status"] != "completed" {
		t.Fatalf("record.status = %v, want completed", rec["status"])
	}
}

func TestInspectCommandMissingIDReturnsError(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"inspect"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["kind"] != "error" {
		t.Fatalf("kind = %v, want error", result["kind"])
	}
	errObj, _ := result["error"].(map[string]any)
	if errObj["code"] != "invalid_args" {
		t.Fatalf("error.code = %v, want invalid_args", errObj["code"])
	}
}

func TestInspectCommandInvalidIDReturnsError(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"inspect", "../bad-id"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["kind"] != "error" {
		t.Fatalf("kind = %v, want error", result["kind"])
	}
	errObj, _ := result["error"].(map[string]any)
	if errObj["code"] != "invalid_input" {
		t.Fatalf("error.code = %v, want invalid_input", errObj["code"])
	}
}

func TestInspectCommandNotFoundReturnsError(t *testing.T) {
	isolateHome(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"inspect", "NONEXISTENT1234"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["kind"] != "error" {
		t.Fatalf("kind = %v, want error", result["kind"])
	}
	errObj, _ := result["error"].(map[string]any)
	if errObj["code"] != "not_found" {
		t.Fatalf("error.code = %v, want not_found", errObj["code"])
	}
}

func TestInspectCommandPrefixMatch(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	writeStoreRecord(t, record, "prefix match response", true)

	// Use a 12-character prefix.
	prefix := record.ID[:12]

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"inspect", prefix}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}

	out := stdout.String()
	// The full ID should appear in the output, resolved from the prefix.
	if !strings.Contains(out, record.ID) {
		t.Fatalf("stdout = %q, want full dispatch ID %q", out, record.ID)
	}
	if !strings.Contains(out, "prefix match response") {
		t.Fatalf("stdout = %q, want response text", out)
	}
}

func TestWaitCommandPollsMetadataOnlyRecordUntilResultExists(t *testing.T) {
	isolateHome(t)

	record := testStoreRecord("01KMT4E7BBNN1KQEC8MYJRW5H5", "completed")
	writeStoreRecord(t, record, "", false)
	if err := dispatch.WriteStatusJSON(record.ArtifactDir, dispatch.LiveStatus{
		State:        "running",
		ElapsedS:     1,
		LastActivity: "working",
		DispatchID:   record.ID,
	}); err != nil {
		t.Fatalf("WriteStatusJSON: %v", err)
	}
	if err := os.WriteFile(filepath.Join(record.ArtifactDir, "host.pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatalf("write host.pid: %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		writeStoreRecord(t, record, "wait response", true)
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"wait", "--poll", "1s", record.ID}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stdout.String() != "wait response" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "wait response")
	}
}

func testStoreRecordWithTime(id, status string, started time.Time) dispatch.DispatchRecord {
	record := testStoreRecord(id, status)
	record.StartedAt = started.Format(time.RFC3339)
	record.EndedAt = started.Add(5 * time.Minute).Format(time.RFC3339)
	return record
}
