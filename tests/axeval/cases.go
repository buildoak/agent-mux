//go:build axeval

package axeval

import (
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// fixtureDir returns the absolute path to the fixture directory.
func fixtureDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "fixture")
}

// AllCases defines the 12 ax-eval test cases.
var AllCases = func() []TestCase {
	cwd := fixtureDir()
	// Ensure CWD is absolute.
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}

	return []TestCase{
		// ── Completion ──────────────────────────────────────────────
		{
			Name:         "complete-simple",
			Category:     CatCompletion,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Create a file called hello.txt in the current directory containing exactly the text 'hello world' (no quotes). Do not create any other files.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					path := filepath.Join(r.ArtifactDir, "hello.txt")
					if _, err := os.Stat(path); err != nil {
						// Also check CWD — worker may write there.
						cwdPath := filepath.Join(cwd, "hello.txt")
						if _, err2 := os.Stat(cwdPath); err2 != nil {
							return Verdict{Pass: false, Score: 0.0, Reason: "hello.txt not found in artifact dir or cwd"}
						}
						// Clean up fixture dir.
						defer os.Remove(cwdPath)
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "hello.txt exists"}
				},
			),
		},

		// ── Correctness ─────────────────────────────────────────────
		{
			Name:         "analyze-repo",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Read main.go and describe what it does. Identify any bugs you find. Be specific about bug locations (line numbers or function names).",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate:     statusIs("completed"),
			JudgePrompt:  "The worker should identify an off-by-one bug in the processNames function. The loop condition uses `i < len(names)-1` instead of `i < len(names)`, which skips the last element. Pass if the response mentions off-by-one, boundary error, or skipping the last element. The response should reference the processNames function or the for loop.",
		},
		{
			Name:     "count-loc",
			Category: CatCorrectness,
			Engine:   "codex",
			Model:    "gpt-5.4-mini",
			Effort:   "high",
			// main.go = 42 lines, helpers.py = 24 lines, total = 66
			Prompt:       "Count the total lines of code in main.go and helpers.py combined. Report just the number, nothing else.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				responseContains("66"),
			),
		},
		{
			Name:         "run-bash",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Run the command `wc -l main.go` in the current directory and report the exact output.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				responseContains("42"),
			),
		},

		// ── Quality ─────────────────────────────────────────────────
		{
			Name:         "write-artifact",
			Category:     CatQuality,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Read main.go and write a file called analysis.md in the current directory summarizing what the program does, its structure, and any issues you find.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					// Check both artifact dir and cwd for the file.
					for _, dir := range []string{r.ArtifactDir, cwd} {
						path := filepath.Join(dir, "analysis.md")
						if info, err := os.Stat(path); err == nil && info.Size() > 0 {
							if dir == cwd {
								defer os.Remove(path)
							}
							return Verdict{Pass: true, Score: 1.0, Reason: "analysis.md exists and is non-empty"}
						}
					}
					return Verdict{Pass: false, Score: 0.0, Reason: "analysis.md not found or empty"}
				},
			),
		},
		{
			Name:         "cross-lang-read",
			Category:     CatQuality,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Compare main.go and helpers.py. What patterns do they share? What are the key differences in approach? Be specific.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate:     statusIs("completed"),
			JudgePrompt:  "The worker should demonstrate it read both files and can compare them. It should mention: (1) both are utility/helper programs, (2) Go vs Python language differences, (3) specific functions from each file (processNames from main.go, format_table or truncate from helpers.py). Pass if at least 2 of these 3 points are addressed.",
		},

		// ── Liveness ────────────────────────────────────────────────
		{
			Name:     "freeze-watchdog",
			Category: CatLiveness,
			Engine:   "codex",
			Model:    "gpt-5.4-mini",
			Effort:   "high",
			Prompt:   "Run the command `bash scripts/freeze.sh` and wait for it to complete.",
			CWD:      cwd,
			// Short agent-mux timeout so the frozen kill triggers quickly.
			TimeoutSec:   120,
			MaxWallClock: 90 * time.Second,
			SkipSkills:   true,
			EngineOpts: map[string]string{
				"silence_warn_seconds": "10",
				"silence_kill_seconds": "20",
			},
			Evaluate: compose(
				statusIs("failed"),
				errorCodeIs("frozen_killed"),
				hasEvent("frozen_warning"),
			),
		},
		{
			Name:     "freeze-stdin-nudge",
			Category: CatEvents,
			Engine:   "codex",
			Model:    "gpt-5.4-mini",
			Effort:   "high",
			Prompt:   "Run the command `bash scripts/freeze.sh` and wait for it to complete.",
			CWD:      cwd,
			// Same thresholds as watchdog test.
			TimeoutSec:   120,
			MaxWallClock: 90 * time.Second,
			SkipSkills:   true,
			EngineOpts: map[string]string{
				"silence_warn_seconds": "10",
				"silence_kill_seconds": "20",
			},
			Evaluate: compose(
				statusIs("failed"),
				// stdin_nudge should appear between frozen_warning and frozen_killed error.
				hasEventSequence("frozen_warning", "info"),
			),
		},

		// ── Error ───────────────────────────────────────────────────
		{
			Name:         "intentional-fail",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Run `bash scripts/fail.sh` and report what happened. Include the exit code in your response.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				responseContains("42"),
			),
		},
		{
			Name:         "bad-engine",
			Category:     CatError,
			Engine:       "bogus",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "This should never reach a worker.",
			CWD:          cwd,
			TimeoutSec:   30,
			MaxWallClock: 30 * time.Second,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("failed"),
				errorCodeIs("engine_not_found"),
			),
		},
		{
			Name:         "bad-model",
			Category:     CatError,
			Engine:       "codex",
			Model:        "nonexistent-9000",
			Effort:       "high",
			Prompt:       "This should never reach a worker.",
			CWD:          cwd,
			TimeoutSec:   30,
			MaxWallClock: 30 * time.Second,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("failed"),
				errorCodeIs("model_not_found"),
			),
		},

		// ── Multi-step ──────────────────────────────────────────────
		{
			Name:         "multi-step-reason",
			Category:     CatQuality,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Read main.go, find the bug, and write a corrected version to fixed_main.go in the current directory. The fix should correct the off-by-one error in the loop.",
			CWD:          cwd,
			TimeoutSec:   180,
			MaxWallClock: 4 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					// Check both artifact dir and cwd for the file.
					for _, dir := range []string{r.ArtifactDir, cwd} {
						path := filepath.Join(dir, "fixed_main.go")
						if info, err := os.Stat(path); err == nil && info.Size() > 0 {
							if dir == cwd {
								defer os.Remove(path)
							}
							return Verdict{Pass: true, Score: 1.0, Reason: "fixed_main.go exists and is non-empty"}
						}
					}
					return Verdict{Pass: false, Score: 0.0, Reason: "fixed_main.go not found or empty"}
				},
			),
			JudgePrompt: "The worker should: (1) identify the off-by-one bug in processNames where the loop uses `i < len(names)-1` instead of `i < len(names)`, and (2) describe the fix. Pass if the response shows understanding of the bug and the fix is correct.",
		},
	}
}()
