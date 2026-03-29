//go:build axeval

package axeval

import (
	"os"
	"os/exec"
	"testing"
)

var binaryPath string

func TestMain(m *testing.M) {
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
	os.Exit(code)
}

func TestAxEval(t *testing.T) {
	for _, tc := range AllCases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			// Liveness and events tests need controlled timing — no parallel.
			if tc.Category != CatLiveness && tc.Category != CatEvents {
				t.Parallel()
			}

			result := dispatch(t, binaryPath, tc)

			// Tier 1: deterministic evaluation.
			verdict := tc.Evaluate(result)

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
