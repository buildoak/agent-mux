//go:build axeval

package axeval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const fakeAgySessionID = "550e8400-e29b-41d4-a716-446655440000"

var liveAgyUUIDPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func TestAgyFakeBinaryPlainStdoutAndSessionDiscovery(t *testing.T) {
	argvLog := installFakeAgy(t)
	t.Setenv("AGY_FAKE_RESPONSE", "agy fake plain stdout success")

	tc := TestCase{
		Engine:       "agy",
		Prompt:       "return the fake response",
		CWD:          t.TempDir(),
		TimeoutSec:   5,
		MaxWallClock: 15 * time.Second,
		SkipSkills:   true,
	}
	result := dispatch(t, binaryPath, tc)
	if result.Status != "completed" {
		t.Fatalf("status=%q, want completed\nstdout=%s\nstderr=%s", result.Status, result.RawStdout, result.RawStderr)
	}
	if result.Response != "agy fake plain stdout success\n" {
		t.Fatalf("response=%q, want fake plain stdout response", result.Response)
	}

	raw, err := stdoutJSONObject(result)
	if err != nil {
		t.Fatalf("parse stdout: %v\nstdout=%s", err, result.RawStdout)
	}
	metadata, err := jsonObjectField(raw, "metadata")
	if err != nil {
		t.Fatalf("metadata: %v\nstdout=%s", err, result.RawStdout)
	}
	if err := requireExactStringField(metadata, "session_id", fakeAgySessionID); err != nil {
		t.Fatalf("metadata session_id: %v\nstdout=%s", err, result.RawStdout)
	}
	status, err := artifactJSONObject(result, "status.json")
	if err != nil {
		t.Fatalf("status.json: %v", err)
	}
	if err := requireExactStringField(status, "session_id", fakeAgySessionID); err != nil {
		t.Fatalf("status session_id: %v\nstatus=%v", err, status)
	}

	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	if !strings.Contains(string(argv), "conversation=\n") {
		t.Fatalf("initial agy invocation should not include --conversation; argv log:\n%s", string(argv))
	}
}

func TestAgyFakeBinaryEmptyStdoutFailsWithHarnessEmptyOutput(t *testing.T) {
	installFakeAgy(t)
	t.Setenv("AGY_FAKE_MODE", "empty")

	tc := TestCase{
		Engine:       "agy",
		Prompt:       "produce no stdout",
		CWD:          t.TempDir(),
		TimeoutSec:   5,
		MaxWallClock: 15 * time.Second,
		SkipSkills:   true,
	}
	result := dispatch(t, binaryPath, tc)
	if result.Status != "failed" {
		t.Fatalf("status=%q, want failed\nstdout=%s\nstderr=%s", result.Status, result.RawStdout, result.RawStderr)
	}
	if result.ErrorCode != "harness_empty_output" {
		t.Fatalf("error_code=%q, want harness_empty_output\nstdout=%s", result.ErrorCode, result.RawStdout)
	}
}

func TestAgyFakeBinaryResumeUsesDiscoveredConversation(t *testing.T) {
	argvLog := installFakeAgy(t)
	readyPath := filepath.Join(t.TempDir(), "agy-ready")
	t.Setenv("AGY_FAKE_MODE", "block")
	t.Setenv("AGY_FAKE_READY", readyPath)
	t.Setenv("AGY_FAKE_RESUME_RESPONSE", "agy resumed after nudge")

	artifactDir := t.TempDir()
	cwd := t.TempDir()
	args := []string{
		"--async",
		"--engine", "agy",
		"--skip-skills",
		"--yes",
		"--cwd", cwd,
		"--artifact-dir", artifactDir,
		"--timeout", "10",
		"wait for a coordinator nudge",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start async agy dispatch: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	processWaited := false
	t.Cleanup(func() {
		if processWaited {
			return
		}
		select {
		case <-waitDone:
		default:
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
			}
		}
	})

	reader := bufio.NewReader(stdoutPipe)
	ackLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read async ack: %v\nstderr=%s", err, stderr.String())
	}
	ackRaw, err := parseJSONObject(bytes.TrimSpace(ackLine), "agy async ack")
	if err != nil {
		t.Fatalf("parse async ack: %v\nack=%s\nstderr=%s", err, string(ackLine), stderr.String())
	}
	dispatchID, ok := jsonStringField(ackRaw, "dispatch_id")
	if !ok || dispatchID == "" {
		t.Fatalf("dispatch_id missing from ack: %s", string(ackLine))
	}
	waitForPath(t, readyPath, 5*time.Second)

	steerResult := dispatchWithFlags(t, binaryPath, []string{"steer", dispatchID, "nudge", "resume from ax fake"}, 10*time.Second)
	if steerResult.ExitCode != 0 {
		t.Fatalf("steer exit=%d\nstdout=%s\nstderr=%s", steerResult.ExitCode, steerResult.RawStdout, steerResult.RawStderr)
	}
	steerRaw, err := parseJSONObject(steerResult.RawStdout, "steer stdout")
	if err != nil {
		t.Fatalf("parse steer stdout: %v\nstdout=%s", err, steerResult.RawStdout)
	}
	if delivered, _ := steerRaw["delivered"].(bool); !delivered {
		t.Fatalf("steer delivered=%v, want true\nstdout=%s", steerRaw["delivered"], steerResult.RawStdout)
	}

	waitResult := dispatchWithFlags(t, binaryPath, []string{"wait", "--json", "--poll", "1s", dispatchID}, 20*time.Second)
	if waitResult.ExitCode != 0 {
		t.Fatalf("wait exit=%d\nstdout=%s\nstderr=%s", waitResult.ExitCode, waitResult.RawStdout, waitResult.RawStderr)
	}
	waitRaw, err := parseJSONObject(waitResult.RawStdout, "wait stdout")
	if err != nil {
		t.Fatalf("parse wait stdout: %v\nstdout=%s", err, waitResult.RawStdout)
	}
	if status, _ := jsonStringField(waitRaw, "status"); status != "completed" {
		t.Fatalf("wait status=%q, want completed\nstdout=%s", status, waitResult.RawStdout)
	}
	if response, _ := jsonStringField(waitRaw, "response"); response != "agy resumed after nudge\n" {
		t.Fatalf("wait response=%q, want resumed fake response\nstdout=%s", response, waitResult.RawStdout)
	}
	if sessionID, _ := jsonStringField(waitRaw, "session_id"); sessionID != fakeAgySessionID {
		t.Fatalf("wait session_id=%q, want %q\nstdout=%s", sessionID, fakeAgySessionID, waitResult.RawStdout)
	}
	finalStatus, err := artifactJSONObject(Result{ArtifactDir: artifactDir}, "status.json")
	if err != nil {
		t.Fatalf("final status.json: %v", err)
	}
	if err := requireExactStringField(finalStatus, "session_id", fakeAgySessionID); err != nil {
		t.Fatalf("final status session_id: %v\nstatus=%v", err, finalStatus)
	}

	select {
	case err := <-waitDone:
		processWaited = true
		if err != nil {
			t.Fatalf("async process wait: %v\nstderr=%s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async process did not exit after resumed result")
	}

	argv, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	argvText := string(argv)
	if !strings.Contains(argvText, "conversation="+fakeAgySessionID+"\n") {
		t.Fatalf("resume invocation did not include discovered --conversation; argv log:\n%s", argvText)
	}
	if !strings.Contains(argvText, "resume from ax fake") {
		t.Fatalf("resume invocation did not receive steer message; argv log:\n%s", argvText)
	}
}

func installFakeAgy(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	argvLog := filepath.Join(t.TempDir(), "agy-argv.log")
	script := `#!/usr/bin/env bash
set -euo pipefail

log_file=""
conversation=""
prompt=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --log-file)
      log_file="${2:-}"
      shift 2
      ;;
    --conversation)
      conversation="${2:-}"
      shift 2
      ;;
    -p)
      prompt="${2:-}"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -n "${AGY_FAKE_ARGV_LOG:-}" ]]; then
  {
    printf 'BEGIN\n'
    printf 'conversation=%s\n' "$conversation"
    printf 'prompt<<EOF\n%s\nEOF\n' "$prompt"
  } >>"${AGY_FAKE_ARGV_LOG}"
fi

if [[ -n "$log_file" ]]; then
  mkdir -p "$(dirname "$log_file")"
  {
    printf 'Created conversation %s\n' "${AGY_FAKE_SESSION_ID}"
    printf 'Print mode: conversation=%s\n' "${AGY_FAKE_SESSION_ID}"
  } >>"$log_file"
fi

if [[ -n "$conversation" ]]; then
  printf '%s\n' "${AGY_FAKE_RESUME_RESPONSE:-agy fake resumed}"
  exit 0
fi

case "${AGY_FAKE_MODE:-success}" in
  empty)
    exit 0
    ;;
  block)
    touch "${AGY_FAKE_READY:?AGY_FAKE_READY required for block mode}"
    trap 'exit 0' TERM INT
    while true; do sleep 0.1; done
    ;;
  *)
    printf '%s\n' "${AGY_FAKE_RESPONSE:-agy fake response}"
    ;;
esac
`
	path := filepath.Join(dir, "agy")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}
	t.Setenv("AGY_FAKE_SESSION_ID", fakeAgySessionID)
	t.Setenv("AGY_FAKE_ARGV_LOG", argvLog)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvLog
}

func waitForPath(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestAgyLiveContractRequiresExplicitOptIn(t *testing.T) {
	if os.Getenv("AX_EVAL_AGY_LIVE") != "1" {
		t.Skip("live agy provider contract tests require AX_EVAL_AGY_LIVE=1")
	}

	model := os.Getenv("AX_EVAL_AGY_MODEL")
	if strings.TrimSpace(model) == "" {
		model = "Gemini 3.5 Flash (Low)"
	}

	refreshResult := dispatchWithFlags(t, binaryPath, []string{"config", "engines", "--refresh-models", "--json"}, 45*time.Second)
	if refreshResult.ExitCode != 0 {
		t.Fatalf("refresh exit=%d\nstdout=%s\nstderr=%s", refreshResult.ExitCode, refreshResult.RawStdout, refreshResult.RawStderr)
	}
	var engines []map[string]any
	if err := json.Unmarshal(refreshResult.RawStdout, &engines); err != nil {
		t.Fatalf("parse refresh JSON: %v\nstdout=%s", err, refreshResult.RawStdout)
	}
	if !agyModelListed(engines, model) {
		t.Fatalf("agy model %q not listed after refresh: %s", model, refreshResult.RawStdout)
	}

	tc := TestCase{
		Engine:       "agy",
		Model:        model,
		Prompt:       "AX live agy identity smoke. Reply with exactly: AGY_LIVE_OK",
		CWD:          t.TempDir(),
		TimeoutSec:   90,
		MaxWallClock: 2 * time.Minute,
		SkipSkills:   true,
	}
	result := dispatch(t, binaryPath, tc)
	if result.Status != "completed" {
		t.Fatalf("status=%q, want completed\nstdout=%s\nstderr=%s", result.Status, result.RawStdout, result.RawStderr)
	}
	if strings.TrimSpace(result.Response) != "AGY_LIVE_OK" {
		t.Fatalf("response=%q, want AGY_LIVE_OK\nstdout=%s", result.Response, result.RawStdout)
	}

	raw, err := stdoutJSONObject(result)
	if err != nil {
		t.Fatalf("parse stdout: %v\nstdout=%s", err, result.RawStdout)
	}
	metadata, err := jsonObjectField(raw, "metadata")
	if err != nil {
		t.Fatalf("metadata: %v\nstdout=%s", err, result.RawStdout)
	}
	sessionID, ok := jsonStringField(metadata, "session_id")
	if !ok || strings.TrimSpace(sessionID) == "" {
		t.Fatalf("metadata session_id missing\nstdout=%s", result.RawStdout)
	}
	status, err := artifactJSONObject(result, "status.json")
	if err != nil {
		t.Fatalf("status.json: %v", err)
	}
	if err := requireExactStringField(status, "session_id", sessionID); err != nil {
		t.Fatalf("status session_id: %v\nstatus=%v", err, status)
	}
	logData, err := os.ReadFile(filepath.Join(result.ArtifactDir, "agy.log"))
	if err != nil {
		t.Fatalf("read agy.log: %v", err)
	}
	if !strings.Contains(string(logData), sessionID) {
		t.Fatalf("agy.log does not contain discovered session %q", sessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resumeLog := filepath.Join(result.ArtifactDir, "agy-live-resume.log")
	cmd := exec.CommandContext(ctx,
		"agy",
		"--sandbox",
		"--print-timeout", "90s",
		"--log-file", resumeLog,
		"--model", model,
		"--conversation", sessionID,
		"-p", "AX live agy direct resume smoke. Reply with exactly: AGY_LIVE_RESUME_OK",
	)
	var resumeStdout, resumeStderr bytes.Buffer
	cmd.Stdout = &resumeStdout
	cmd.Stderr = &resumeStderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("direct agy resume failed: %v\nstdout=%s\nstderr=%s", err, resumeStdout.String(), resumeStderr.String())
	}
	if strings.TrimSpace(resumeStdout.String()) != "AGY_LIVE_RESUME_OK" {
		t.Fatalf("direct resume stdout=%q, want AGY_LIVE_RESUME_OK\nstderr=%s", resumeStdout.String(), resumeStderr.String())
	}
}

func TestAgyLiveAsyncSteerRequiresExplicitOptIn(t *testing.T) {
	if os.Getenv("AX_EVAL_AGY_LIVE_ASYNC") != "1" {
		t.Skip("live agy async steer test requires AX_EVAL_AGY_LIVE_ASYNC=1")
	}

	model := liveAgyModel()
	artifactDir := t.TempDir()
	cwd := t.TempDir()
	args := []string{
		"--async",
		"--engine", "agy",
		"--model", model,
		"--skip-skills",
		"--yes",
		"--cwd", cwd,
		"--artifact-dir", artifactDir,
		"--timeout", "120",
		"AX live agy async steer smoke. First run a shell command that sleeps for 45 seconds before producing any final answer. During that wait, a coordinator nudge may arrive. If a nudge asks for exact text, obey it and reply with only that exact text.",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start live async agy dispatch: %v", err)
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	processWaited := false
	t.Cleanup(func() {
		if processWaited {
			return
		}
		select {
		case <-waitDone:
		default:
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-waitDone
		}
	})

	reader := bufio.NewReader(stdoutPipe)
	ackLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read live async ack: %v\nstderr=%s", err, stderr.String())
	}
	ackRaw, err := parseJSONObject(bytes.TrimSpace(ackLine), "live agy async ack")
	if err != nil {
		t.Fatalf("parse live async ack: %v\nack=%s\nstderr=%s", err, string(ackLine), stderr.String())
	}
	dispatchID, ok := jsonStringField(ackRaw, "dispatch_id")
	if !ok || dispatchID == "" {
		t.Fatalf("dispatch_id missing from ack: %s", string(ackLine))
	}

	initialSessionID := waitForAgyLogSessionID(t, filepath.Join(artifactDir, "agy.log"), 45*time.Second)
	steerResult := dispatchWithFlags(t, binaryPath, []string{"steer", dispatchID, "nudge", "Reply with exactly: AGY_LIVE_ASYNC_STEER_OK"}, 30*time.Second)
	if steerResult.ExitCode != 0 {
		t.Fatalf("steer exit=%d\nstdout=%s\nstderr=%s", steerResult.ExitCode, steerResult.RawStdout, steerResult.RawStderr)
	}
	waitResult := dispatchWithFlags(t, binaryPath, []string{"wait", "--json", "--poll", "1s", dispatchID}, 2*time.Minute)
	if waitResult.ExitCode != 0 {
		t.Fatalf("wait exit=%d\nstdout=%s\nstderr=%s", waitResult.ExitCode, waitResult.RawStdout, waitResult.RawStderr)
	}
	waitRaw, err := parseJSONObject(waitResult.RawStdout, "live agy wait stdout")
	if err != nil {
		t.Fatalf("parse wait stdout: %v\nstdout=%s", err, waitResult.RawStdout)
	}
	if status, _ := jsonStringField(waitRaw, "status"); status != "completed" {
		t.Fatalf("wait status=%q, want completed\nstdout=%s", status, waitResult.RawStdout)
	}
	if response, _ := jsonStringField(waitRaw, "response"); strings.TrimSpace(response) != "AGY_LIVE_ASYNC_STEER_OK" {
		t.Fatalf("wait response=%q, want AGY_LIVE_ASYNC_STEER_OK\nstdout=%s", response, waitResult.RawStdout)
	}
	gotSessionID, _ := jsonStringField(waitRaw, "session_id")
	if strings.TrimSpace(gotSessionID) == "" {
		t.Fatalf("wait session_id missing; initial session was %q\nstdout=%s", initialSessionID, waitResult.RawStdout)
	}
	finalStatus, err := artifactJSONObject(Result{ArtifactDir: artifactDir}, "status.json")
	if err != nil {
		t.Fatalf("final status.json: %v", err)
	}
	if err := requireExactStringField(finalStatus, "session_id", gotSessionID); err != nil {
		t.Fatalf("final status session_id: %v\nstatus=%v", err, finalStatus)
	}

	select {
	case err := <-waitDone:
		processWaited = true
		if err != nil {
			t.Fatalf("live async process wait: %v\nstderr=%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("live async process did not exit after resumed result")
	}
}

func TestAgyLiveMultimodalAndImageGenerationRequiresExplicitOptIn(t *testing.T) {
	if os.Getenv("AX_EVAL_AGY_LIVE_MULTIMODAL") != "1" {
		t.Skip("live agy multimodal/image generation test requires AX_EVAL_AGY_LIVE_MULTIMODAL=1")
	}

	model := liveAgyModel()
	cwd := t.TempDir()
	writeLivePDF(t, filepath.Join(cwd, "sample.pdf"), "PDF_AX_314")
	writeLiveRedPNG(t, filepath.Join(cwd, "red.png"))

	tc := TestCase{
		Engine:       "agy",
		Model:        model,
		Prompt:       "Read sample.pdf and inspect red.png in the current working directory. Then use the available image generation capability to create a PNG file named generated-banana.png in the current working directory. Reply with exactly: AGY_MULTIMODAL_IMAGE_OK PDF_AX_314 RED",
		CWD:          cwd,
		TimeoutSec:   180,
		MaxWallClock: 4 * time.Minute,
		SkipSkills:   true,
	}
	result := dispatch(t, binaryPath, tc)
	if result.Status != "completed" {
		t.Fatalf("status=%q, want completed\nstdout=%s\nstderr=%s", result.Status, result.RawStdout, result.RawStderr)
	}
	if strings.TrimSpace(result.Response) != "AGY_MULTIMODAL_IMAGE_OK PDF_AX_314 RED" {
		t.Fatalf("response=%q, want multimodal/image sentinel\nstdout=%s", result.Response, result.RawStdout)
	}
	generatedPath := filepath.Join(cwd, "generated-banana.png")
	info, err := os.Stat(generatedPath)
	if err != nil {
		t.Fatalf("generated image missing at %s: %v", generatedPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("generated image at %s is empty", generatedPath)
	}
}

func agyModelListed(engines []map[string]any, model string) bool {
	for _, entry := range engines {
		if entry["engine"] != "agy" {
			continue
		}
		models, _ := entry["models"].([]any)
		for _, item := range models {
			if item == model {
				return true
			}
		}
	}
	return false
}

func liveAgyModel() string {
	model := strings.TrimSpace(os.Getenv("AX_EVAL_AGY_MODEL"))
	if model == "" {
		return "Gemini 3.5 Flash (Low)"
	}
	return model
}

func waitForAgyLogSessionID(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			for _, match := range liveAgyUUIDPattern.FindAllString(string(data), -1) {
				if strings.TrimSpace(match) != "" {
					return match
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for agy conversation id in %s", path)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func writeLivePDF(t *testing.T, path string, token string) {
	t.Helper()
	pdf := "%PDF-1.4\n" +
		"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n" +
		"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj\n" +
		"4 0 obj << /Length 54 >> stream\nBT /F1 18 Tf 40 90 Td (" + token + ") Tj ET\nendstream endobj\n" +
		"5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n"
	if err := os.WriteFile(path, []byte(pdf), 0o644); err != nil {
		t.Fatalf("write PDF fixture: %v", err)
	}
}

func writeLiveRedPNG(t *testing.T, path string) {
	t.Helper()
	data := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41,
		0x54, 0x08, 0x1d, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write PNG fixture: %v", err)
	}
}
