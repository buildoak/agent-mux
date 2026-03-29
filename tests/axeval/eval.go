//go:build axeval

package axeval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// statusIs checks that Result.Status matches expected.
func statusIs(expected string) func(Result) Verdict {
	return func(r Result) Verdict {
		if r.Status == expected {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("status=%s", r.Status)}
		}
		return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("status=%q, want %q", r.Status, expected)}
	}
}

// statusIsOneOf checks that Result.Status matches any of the expected values.
func statusIsOneOf(expected ...string) func(Result) Verdict {
	return func(r Result) Verdict {
		for _, e := range expected {
			if r.Status == e {
				return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("status=%s (accepted)", r.Status)}
			}
		}
		return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("status=%q, want one of %v", r.Status, expected)}
	}
}

// responseContains checks that Result.Response contains substr (case-insensitive).
func responseContains(substr string) func(Result) Verdict {
	return func(r Result) Verdict {
		if strings.Contains(strings.ToLower(r.Response), strings.ToLower(substr)) {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("response contains %q", substr)}
		}
		return Verdict{
			Pass:   false,
			Score:  0.0,
			Reason: fmt.Sprintf("response missing %q (len=%d)", substr, len(r.Response)),
		}
	}
}

// artifactExists checks that a file exists in the artifact dir.
func artifactExists(filename string) func(Result) Verdict {
	return func(r Result) Verdict {
		path := filepath.Join(r.ArtifactDir, filename)
		if _, err := os.Stat(path); err == nil {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("artifact %q exists", filename)}
		}
		return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("artifact %q not found in %s", filename, r.ArtifactDir)}
	}
}

// errorCodeIs checks that Result.ErrorCode matches expected.
func errorCodeIs(code string) func(Result) Verdict {
	return func(r Result) Verdict {
		if r.ErrorCode == code {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("error_code=%s", r.ErrorCode)}
		}
		return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("error_code=%q, want %q", r.ErrorCode, code)}
	}
}

// hasEvent checks that at least one event matches the given type.
func hasEvent(eventType string) func(Result) Verdict {
	return func(r Result) Verdict {
		for _, e := range r.Events {
			if e.Type == eventType {
				return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("event %q found", eventType), Events: eventTypes(r.Events)}
			}
		}
		return Verdict{
			Pass:   false,
			Score:  0.0,
			Reason: fmt.Sprintf("event %q not found; got types: %v", eventType, eventTypes(r.Events)),
			Events: eventTypes(r.Events),
		}
	}
}

// hasEventSequence checks that the given event types appear in order in the events list.
func hasEventSequence(types ...string) func(Result) Verdict {
	return func(r Result) Verdict {
		idx := 0
		for _, e := range r.Events {
			if idx < len(types) && e.Type == types[idx] {
				idx++
			}
		}
		if idx == len(types) {
			return Verdict{
				Pass:   true,
				Score:  1.0,
				Reason: fmt.Sprintf("event sequence %v found in order", types),
				Events: eventTypes(r.Events),
			}
		}
		return Verdict{
			Pass:   false,
			Score:  0.0,
			Reason: fmt.Sprintf("event sequence %v not found (matched %d/%d); got types: %v", types, idx, len(types), eventTypes(r.Events)),
			Events: eventTypes(r.Events),
		}
	}
}

// stderrContains checks that Result.RawStderr contains substr.
func stderrContains(substr string) func(Result) Verdict {
	return func(r Result) Verdict {
		if strings.Contains(string(r.RawStderr), substr) {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("stderr contains %q", substr)}
		}
		return Verdict{
			Pass:   false,
			Score:  0.0,
			Reason: fmt.Sprintf("stderr missing %q (len=%d)", substr, len(r.RawStderr)),
		}
	}
}

// stderrNotContains checks that Result.RawStderr does NOT contain substr.
func stderrNotContains(substr string) func(Result) Verdict {
	return func(r Result) Verdict {
		if !strings.Contains(string(r.RawStderr), substr) {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("stderr does not contain %q", substr)}
		}
		return Verdict{
			Pass:   false,
			Score:  0.0,
			Reason: fmt.Sprintf("stderr unexpectedly contains %q", substr),
		}
	}
}

// eventLogContains checks that events.jsonl has an event of the given type.
func eventLogContains(eventType string) func(Result) Verdict {
	return hasEvent(eventType)
}

// stdoutContains checks that Result.RawStdout contains substr.
func stdoutContains(substr string) func(Result) Verdict {
	return func(r Result) Verdict {
		if strings.Contains(string(r.RawStdout), substr) {
			return Verdict{Pass: true, Score: 1.0, Reason: fmt.Sprintf("stdout contains %q", substr)}
		}
		return Verdict{
			Pass:   false,
			Score:  0.0,
			Reason: fmt.Sprintf("stdout missing %q (len=%d)", substr, len(r.RawStdout)),
		}
	}
}

// compose ANDs multiple check functions. All must pass.
func compose(checks ...func(Result) Verdict) func(Result) Verdict {
	return func(r Result) Verdict {
		var allEvents []string
		minScore := 1.0

		for _, check := range checks {
			v := check(r)
			if !v.Pass {
				return Verdict{
					Pass:   false,
					Score:  v.Score,
					Reason: v.Reason,
					Events: v.Events,
				}
			}
			if v.Score < minScore {
				minScore = v.Score
			}
			allEvents = append(allEvents, v.Events...)
		}

		return Verdict{
			Pass:   true,
			Score:  minScore,
			Reason: fmt.Sprintf("all %d checks passed", len(checks)),
			Events: dedup(allEvents),
		}
	}
}

// eventTypes extracts the Type field from a slice of Events.
func eventTypes(events []Event) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// dedup removes duplicate strings preserving order.
func dedup(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
