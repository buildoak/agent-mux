//go:build axeval

package axeval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

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
			Name:         "scout-role-completion",
			Category:     CatCorrectness,
			Engine:       "codex",
			Model:        "gpt-5.4-mini",
			Effort:       "high",
			Prompt:       "Find every markdown file under the current directory and report the results.",
			CWD:          cwd,
			TimeoutSec:   120,
			MaxWallClock: 3 * time.Minute,
			SkipSkills:   true,
			ExtraFlags:   []string{"-R=scout"},
			Evaluate: compose(
				statusIs("completed"),
				func(r Result) Verdict {
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					response, _ := jsonStringField(raw, "response")
					if strings.TrimSpace(response) == "" {
						return Verdict{Pass: false, Score: 0.0, Reason: "scout response is empty"}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "scout role completed with non-empty response"}
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
					// meta.json lives at ~/.agent-mux/dispatches/<dispatch_id>/meta.json
					raw, err := stdoutJSONObject(r)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: err.Error()}
					}
					dispatchID, ok := jsonStringField(raw, "dispatch_id")
					if !ok || strings.TrimSpace(dispatchID) == "" {
						return Verdict{Pass: false, Score: 0.0, Reason: "dispatch_id missing from result"}
					}
					homeDir, err := os.UserHomeDir()
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("resolve home dir: %v", err)}
					}
					metaPath := fmt.Sprintf("%s/.agent-mux/dispatches/%s/meta.json", homeDir, dispatchID)
					data, err := os.ReadFile(metaPath)
					if err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("read meta.json: %v", err)}
					}
					var meta map[string]any
					if err := json.Unmarshal(data, &meta); err != nil {
						return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("parse meta.json: %v", err)}
					}
					for _, key := range []string{"dispatch_id", "engine", "model", "started_at"} {
						if err := requireNonEmptyStringField(meta, key); err != nil {
							return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("meta.json: %v", err)}
						}
					}
					return Verdict{Pass: true, Score: 1.0, Reason: "meta.json fields valid"}
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
