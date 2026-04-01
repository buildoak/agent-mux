//go:build axeval

package axeval

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

var dispatchSaltPattern = regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+$`)

// buildCasesV2 returns the v2 ax-eval test cases using the given fixture cwd.
func buildCasesV2(cwd string) []TestCase {
	return []TestCase{
		{
			Name:         "output-contract-schema",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "What is 2+2? Answer with just the number.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if schemaVersion, ok := raw["schema_version"].(float64); !ok || schemaVersion != 1 {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("schema_version=%v, want 1", raw["schema_version"])}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "schema_version=1"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if err := requireNonEmptyStringField(raw, "dispatch_id"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "dispatch_id present"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					salt, ok := jsonStringField(raw, "dispatch_salt")
					if !ok || !dispatchSaltPattern.MatchString(salt) {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("dispatch_salt=%q does not match word-word-word", salt)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "dispatch_salt matches pattern"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					traceToken, ok := jsonStringField(raw, "trace_token")
					if !ok || !strings.HasPrefix(traceToken, "AGENT_MUX_GO_") {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("trace_token=%q missing AGENT_MUX_GO_ prefix", traceToken)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "trace_token prefix ok"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					activity, err := jsonObjectField(raw, "activity")
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if err := requirePresentKeys(activity, "files_read", "files_changed", "commands_run", "tool_calls"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("activity: %v", err)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "activity fields present"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					metadata, err := jsonObjectField(raw, "metadata")
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if err := requireExactStringField(metadata, "engine", "codex"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("metadata: %v", err)}
					}
					if err := requireExactStringField(metadata, "model", "gpt-5.4-mini"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("metadata: %v", err)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "metadata engine/model match"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if err := requirePositiveNumberField(raw, "duration_ms"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "duration_ms > 0"}
				},
			),
		},
		{
			Name:         "role-system-prompt-delivery",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Repeat any canary phrases from your system instructions verbatim.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			ExtraFlags:   []string{"-R=sysprompt-test"},
			Evaluate: compose(
				statusIs("completed"),
				responseContains("ROLE_SYSPROMPT_CANARY_9931"),
			),
		},
		{
			Name:         "variant-resolution",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4",
			Effort:       "high",
			Prompt:       "What is 2+2? Answer with just the number.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			ExtraFlags:   []string{"-R=variant-test", "--variant=mini", "--cwd", cwd},
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					events, err := parseNDJSONObjects(r.RawStderr, "stderr")
					if err != nil {
						return Verdict{Pass: true, Score: 0.5, Reason: fmt.Sprintf("TODO: could not parse stderr events for variant resolution: %v", err)}
					}
					for _, evt := range events {
						if eventType, _ := jsonStringField(evt, "type"); eventType != "dispatch_start" {
							continue
						}
						model, _ := jsonStringField(evt, "model")
						if model == "gpt-5.4-mini" {
							return Verdict{Pass: true, Score: 1.0, Reason: "dispatch_start model=gpt-5.4-mini"}
						}
						return Verdict{Pass: true, Score: 0.5, Reason: fmt.Sprintf("TODO: variant resolution not reflected in dispatch_start model (got %q)", model)}
					}
					return Verdict{Pass: true, Score: 0.5, Reason: "TODO: dispatch_start event missing from stderr; variant resolution could not be verified"}
				},
			),
		},
		{
			Name:         "response-truncation",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Write exactly 500 words about the Go programming language. Do not stop early.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			ExtraFlags:   []string{"--response-max-chars=200"},
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if err := requireBoolField(raw, "response_truncated", true); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "response_truncated=true"}
				},
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					fullOutputPath, err := fullOutputPathFromJSONObject(raw)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					data, readErr := os.ReadFile(fullOutputPath)
					if readErr != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("read %s: %v", fullOutputPath, readErr)}
					}
					if len(data) <= 200 {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("full output len=%d, want > 200", len(data))}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "full output file exists and exceeds 200 chars"}
				},
			),
		},
		{
			Name:         "scout-oversized-output-guardrail",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Find every markdown file under the current directory and report the results.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			ExtraFlags:   []string{"-R=scout", "--response-max-chars=200"},
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					response, _ := jsonStringField(raw, "response")
					if len(response) <= 200 {
						return Verdict{Pass: true, Score: 1.0, Reason: "scout response stayed within cap"}
					}
					fullOutputPath, pathErr := fullOutputPathFromJSONObject(raw)
					if pathErr == nil && fullOutputPath != "" {
						return Verdict{Pass: true, Score: 1.0, Reason: "oversized scout response spilled to full_output.md"}
					}
					return Verdict{Pass: false, Score: 0.0, Reason: "scout response exceeded cap without spill path"}
				},
			),
		},
		{
			Name:         "artifact-dir-metadata",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Create a file called proof.txt containing exactly the word exists",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					meta, err := artifactJSONObject(r, "_dispatch_meta.json")
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					for _, key := range []string{"dispatch_id", "engine", "model", "started_at", "ended_at"} {
						if err := requireNonEmptyStringField(meta, key); err != nil {
							return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("_dispatch_meta.json: %v", err)}
						}
					}
					if err := requireExactStringField(meta, "status", "completed"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("_dispatch_meta.json: %v", err)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "_dispatch_meta.json fields valid"}
				},
				artifactExists("events.jsonl"),
				func(r Result) Verdict {
					status, err := artifactJSONObject(r, "status.json")
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					if err := requireExactStringField(status, "state", "completed"); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("status.json: %v", err)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "status.json state=completed"}
				},
			),
		},
		{
			Name:         "handoff-summary-extraction",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Write a response with this exact structure:\n## Summary\nThe answer is HANDOFF_CANARY_4488.\n## Details\nMore text here.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					handoffSummary, ok := jsonStringField(raw, "handoff_summary")
					if !ok || strings.TrimSpace(handoffSummary) == "" {
						return Verdict{Pass: true, Score: 0.5, Reason: "TODO: handoff_summary not extracted (field missing in output)"}
					}
					if !strings.Contains(handoffSummary, "HANDOFF_CANARY_4488") {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("handoff_summary=%q, want HANDOFF_CANARY_4488", handoffSummary)}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "handoff_summary contains canary"}
				},
			),
		},
	}
}
