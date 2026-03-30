//go:build axeval

package axeval

import (
	"os"
	"os/exec"
	"testing"
)

var binaryPath string

func TestMain(m *testing.M) {
	// Set up fixture temp dir (copies testdata/fixture/ to /tmp).
	SetupFixtureDir()

	// Initialize test cases now that fixture dir is ready.
	InitCases()

	// Build agent-mux binary to a temp location.
	tmpBin, err := os.CreateTemp("", "agent-mux-test-*")
	if err != nil {
		panic("create temp binary: " + err.Error())
	}
	tmpBin.Close()
	binaryPath = tmpBin.Name()

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/agent-mux/")
	cmd.Dir = "../../" // back to repo root from tests/axeval/
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build agent-mux: " + string(out))
	}

	code := m.Run()

	// Write report if AX_EVAL_REPORT_DIR is set.
	if err := writeReport(); err != nil {
		// Log but don't fail — tests already ran.
		os.Stderr.WriteString("ax-eval: write report: " + err.Error() + "\n")
	}

	os.Remove(binaryPath)
	CleanupFixtureDir()
	os.Exit(code)
}

func TestAxEval(t *testing.T) {
	for _, tc := range AllCases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			if tc.SkipReason != "" {
				t.Skip(tc.SkipReason)
			}
			// Liveness, events, streaming, and steering tests need controlled timing — no parallel.
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
				result = ack // use ack for duration tracking
				verdict = tc.EvalAsync(ack, collected)
			} else {
				result = dispatch(t, binaryPath, tc)
				// Tier 1: deterministic evaluation.
				verdict = tc.Evaluate(result)
			}

			// Record for report.
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
				t.Errorf("FAIL [%s/%s]: %s", tc.Category, tc.Name, verdict.Reason)
			}

			// Tier 2: LLM-as-judge (only if tier 1 passed and rubric is set).
			if tc.JudgePrompt != "" && verdict.Pass {
				jv := judge(t, binaryPath, tc.Prompt, result.Response, tc.JudgePrompt)
				jp := jv.Pass
				js := jv.Score
				cr.JudgePass = &jp
				cr.JudgeScore = &js

				if !jv.Pass {
					t.Errorf("JUDGE FAIL [%s/%s]: %.2f — %s", tc.Category, tc.Name, jv.Score, jv.Reason)
					cr.Pass = false
					cr.Reason = "judge: " + jv.Reason
				}
			}

			recordResult(cr)

			t.Logf("[%s] %s: pass=%v score=%.2f duration=%s events=%v",
				tc.Category, tc.Name, verdict.Pass, verdict.Score, result.Duration, verdict.Events)
		})
	}
}
