//go:build axeval

// L4 · Hard (live dispatch integration tests)
//
// These are the existing tests that perform real dispatches with real tasks.
// Gated behind AX_EVAL_HARD=1 (in addition to the axeval build tag).
//
// L4 includes:
// - All existing AllCases (completion, correctness, quality, error, streaming, steering)
// - Lifecycle tests (list, status, inspect)
// - Trace verification
// - Pipeline, recovery, context-file, skills, effort tiers
//
// These tests are refactored from the original axeval_test.go, p1_test.go,
// lifecycle_test.go, wave2_test.go, and wave3_test.go into the L4 tier.

package axeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isHardMode() bool {
	return os.Getenv("AX_EVAL_HARD") == "1"
}

// TestL4Dispatches runs all legacy AllCases dispatch tests.
// These are the "real" tests that call Codex/Claude/Gemini APIs.
func TestL4Dispatches(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 tests require AX_EVAL_HARD=1")
	}

	for _, tc := range AllCases {
		tc := tc
		t.Run("L4/"+tc.Name, func(t *testing.T) {
			if tc.SkipReason != "" {
				t.Skip(tc.SkipReason)
			}
			// Liveness, events, streaming, and steering tests need controlled timing.
			if tc.Category != CatLiveness && tc.Category != CatEvents && tc.Category != CatStreaming && tc.Category != CatSteering {
				t.Parallel()
			}

			var result Result
			var verdict Verdict

			if tc.SteerSpec != nil && tc.EvalAsync != nil {
				ack, _, collected := dispatchAsyncSteer(t, binaryPath, tc)
				result = ack
				verdict = tc.EvalAsync(ack, collected)
			} else if tc.IsAsync && tc.EvalAsync != nil {
				ack, collected := dispatchAsync(t, binaryPath, tc)
				result = ack
				verdict = tc.EvalAsync(ack, collected)
			} else {
				result = dispatch(t, binaryPath, tc)
				verdict = tc.Evaluate(result)
			}

			cr := CaseResult{
				Name:       tc.Name,
				Category:   tc.Category,
				Pass:       verdict.Pass,
				Score:      verdict.Score,
				Reason:     verdict.Reason,
				DurationMS: result.Duration.Milliseconds(),
				Events:     verdict.Events,
			}

			if !verdict.Pass {
				t.Errorf("FAIL [L4/%s/%s]: %s", tc.Category, tc.Name, verdict.Reason)
			}

			// LLM-as-judge (only if deterministic eval passed and rubric is set).
			if tc.JudgePrompt != "" && verdict.Pass {
				jv := judge(t, binaryPath, tc.Prompt, result.Response, tc.JudgePrompt)
				jp := jv.Pass
				js := jv.Score
				cr.JudgePass = &jp
				cr.JudgeScore = &js

				if !jv.Pass {
					t.Errorf("JUDGE FAIL [L4/%s/%s]: %.2f — %s", tc.Category, tc.Name, jv.Score, jv.Reason)
					cr.Pass = false
					cr.Reason = "judge: " + jv.Reason
				}
			}

			recordResult(cr)

			// Also record as AX verdict for the unified report.
			axv := AXVerdict{
				Pass:     cr.Pass,
				Score:    cr.Score,
				Reason:   cr.Reason,
				Tier:     TierL4,
				CaseName: tc.Name,
				Duration: result.Duration,
			}
			recordAXVerdict(axv)

			t.Logf("[L4/%s] %s: pass=%v score=%.2f duration=%s events=%v",
				tc.Category, tc.Name, verdict.Pass, verdict.Score, result.Duration, verdict.Events)
		})
	}
}

// TestL4Lifecycle runs lifecycle subcommand tests (list, status, inspect).
func TestL4Lifecycle(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 tests require AX_EVAL_HARD=1")
	}

	t.Run("L4/lifecycle-list-status-inspect", func(t *testing.T) {
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
			t.Fatalf("dispatch status = %q, want completed\nstdout=%s\nstderr=%s",
				result.Status, string(result.RawStdout), string(result.RawStderr))
		}

		raw, err := stdoutJSONObject(result)
		if err != nil {
			t.Fatalf("parse dispatch stdout: %v", err)
		}
		dispatchID, ok := jsonStringField(raw, "dispatch_id")
		if !ok || dispatchID == "" {
			t.Fatalf("dispatch_id missing from dispatch stdout")
		}

		// Test list.
		listResult := dispatchWithFlags(t, binaryPath, []string{"list", "-json", "-limit", "0"}, 3*time.Minute)
		if listResult.ExitCode != 0 {
			t.Fatalf("list exit=%d", listResult.ExitCode)
		}
		listRows, err := parseNDJSONObjects(listResult.RawStdout, "list stdout")
		if err != nil {
			t.Fatalf("parse list output: %v", err)
		}
		found := false
		for _, row := range listRows {
			if id, ok := jsonStringField(row, "id"); ok && id == dispatchID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("dispatch_id %q not found in list output", dispatchID)
		}

		// Test status.
		statusResult := dispatchWithFlags(t, binaryPath, []string{"status", dispatchID, "-json"}, 3*time.Minute)
		if statusResult.ExitCode != 0 {
			t.Fatalf("status exit=%d", statusResult.ExitCode)
		}
		statusRaw, err := parseJSONObject(statusResult.RawStdout, "status stdout")
		if err != nil {
			t.Fatalf("parse status stdout: %v", err)
		}
		state, _ := jsonStringField(statusRaw, "status")
		if state == "" {
			state, _ = jsonStringField(statusRaw, "state")
		}
		if state != "completed" {
			t.Fatalf("status/state = %q, want completed", state)
		}

		// Test inspect.
		inspectResult := dispatchWithFlags(t, binaryPath, []string{"inspect", dispatchID, "-json"}, 3*time.Minute)
		if inspectResult.ExitCode != 0 {
			t.Fatalf("inspect exit=%d", inspectResult.ExitCode)
		}
		inspectRaw, err := parseJSONObject(inspectResult.RawStdout, "inspect stdout")
		if err != nil {
			t.Fatalf("parse inspect stdout: %v", err)
		}
		for _, key := range []string{"dispatch_id", "response", "artifact_dir"} {
			if err := requireNonEmptyStringField(inspectRaw, key); err != nil {
				t.Fatalf("inspect output invalid: %v", err)
			}
		}

		recordAXVerdict(AXVerdict{
			Pass: true, Score: 1.0, Reason: "lifecycle commands all working",
			Tier: TierL4, CaseName: "lifecycle-list-status-inspect",
		})
	})
}

// TestL4SkillsInjection validates --skill injects skill content.
func TestL4SkillsInjection(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 tests require AX_EVAL_HARD=1")
	}

	cwd := fixtureDir()
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	args := []string{
		"--skill=axeval-test",
		"--engine", "codex",
		"--model", "gpt-5.4-mini",
		"--effort", "high",
		"--yes",
		"--cwd", cwd,
		"Say hello and confirm you can see the skill instructions. Include any canary phrases from your skill instructions.",
	}

	result := dispatchWithFlags(t, binaryPath, args, 3*time.Minute)

	pass := result.Status == "completed" && strings.Contains(strings.ToUpper(result.Response), "SKILL_CANARY_7742")
	reason := "skill canary found"
	if !pass {
		reason = fmt.Sprintf("status=%q, canary_found=%v", result.Status,
			strings.Contains(strings.ToUpper(result.Response), "SKILL_CANARY_7742"))
	}

	recordAXVerdict(AXVerdict{
		Pass: pass, Score: boolScore(pass), Reason: reason,
		Tier: TierL4, CaseName: "skills-injection",
	})

	if !pass {
		t.Fatalf("FAIL: %s", reason)
	}
	t.Logf("PASS: skill canary found (duration=%s)", result.Duration)
}

// TestL4RecoveryRedispatch tests --recover continuation.
func TestL4RecoveryRedispatch(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 tests require AX_EVAL_HARD=1")
	}

	cwd := fixtureDir()
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	// Step 1: Initial dispatch.
	initialTC := TestCase{
		Engine: "codex", Model: "gpt-5.4-mini", Effort: "high",
		Prompt:       "Read main.go and describe the bug you find. Be specific about the function name.",
		CWD:          cwd,
		TimeoutSec:   120,
		MaxWallClock: 3 * time.Minute,
		SkipSkills:   true,
	}

	initialResult := dispatch(t, binaryPath, initialTC)
	if initialResult.Status != "completed" {
		t.Fatalf("initial dispatch status=%q", initialResult.Status)
	}

	var initialJSON map[string]any
	if err := json.Unmarshal(initialResult.RawStdout, &initialJSON); err != nil {
		t.Fatalf("initial result not valid JSON: %v", err)
	}
	dispatchID, _ := initialJSON["dispatch_id"].(string)
	if dispatchID == "" {
		t.Fatalf("no dispatch_id in initial result")
	}

	// Step 2: Recovery dispatch.
	recoveryArgs := []string{
		"--recover", dispatchID,
		"--engine", "codex",
		"--model", "gpt-5.4-mini",
		"--effort", "high",
		"--skip-skills",
		"--yes",
		"--cwd", cwd,
		"Now fix the bug you found in the previous attempt. Write the corrected version to fixed_main.go.",
	}

	recoveryResult := dispatchWithFlags(t, binaryPath, recoveryArgs, 4*time.Minute)

	// Recovery should either complete or fail with recovery_failed.
	pass := recoveryResult.Status == "completed" || recoveryResult.Status == "failed"
	reason := fmt.Sprintf("recovery status=%s", recoveryResult.Status)

	recordAXVerdict(AXVerdict{
		Pass: pass, Score: boolScore(pass), Reason: reason,
		Tier: TierL4, CaseName: "recovery-redispatch",
	})

	if !pass {
		t.Fatalf("FAIL: %s", reason)
	}
	t.Logf("PASS: %s", reason)
}

// TestL4ContextFile validates --context-file.
func TestL4ContextFile(t *testing.T) {
	if !isHardMode() {
		t.Skip("L4 tests require AX_EVAL_HARD=1")
	}

	cwd := fixtureDir()
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	tmpFile, err := os.CreateTemp("", "axeval-context-*.txt")
	if err != nil {
		t.Fatalf("create temp context file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	secretContent := "The secret code is AXEVAL42. The password is PINEAPPLE_SUNRISE."
	tmpFile.WriteString(secretContent)
	tmpFile.Close()

	args := []string{
		"--context-file", tmpFile.Name(),
		"--engine", "codex",
		"--model", "gpt-5.4-mini",
		"--effort", "high",
		"--skip-skills",
		"--yes",
		"--cwd", cwd,
		"Read the context file at $AGENT_MUX_CONTEXT and tell me the secret code and password. Report them verbatim.",
	}

	result := dispatchWithFlags(t, binaryPath, args, 3*time.Minute)

	response := strings.ToUpper(result.Response)
	pass := result.Status == "completed" && (strings.Contains(response, "AXEVAL42") || strings.Contains(response, "PINEAPPLE_SUNRISE"))
	reason := fmt.Sprintf("status=%s, secrets_found=%v", result.Status, pass)

	recordAXVerdict(AXVerdict{
		Pass: pass, Score: boolScore(pass), Reason: reason,
		Tier: TierL4, CaseName: "context-file",
	})

	if !pass {
		t.Fatalf("FAIL: %s", reason)
	}
	t.Logf("PASS: context file content reached worker (duration=%s)", result.Duration)
}

func boolScore(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
