//go:build axeval

package axeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// judge dispatches an LLM-as-judge evaluation via agent-mux.
// It sends the original task, the worker's response, and a scoring rubric
// to gpt-5.4-mini and parses a structured verdict.
func judge(t *testing.T, binary string, taskPrompt string, workerResponse string, rubric string) Verdict {
	t.Helper()

	judgeSystemPrompt := `You are an evaluation judge. You will be given:
1. The original task that was assigned to a worker
2. The worker's response
3. A scoring rubric

Evaluate the response against the rubric and return ONLY valid JSON:
{"pass": true/false, "score": 0.0-1.0, "reason": "brief explanation"}

Be strict but fair. Score 0.7+ means pass. Focus on whether the core requirement was met.`

	prompt := fmt.Sprintf(`## Original Task
%s

## Worker Response
%s

## Rubric
%s

Evaluate and return JSON verdict.`, taskPrompt, workerResponse, rubric)

	artifactDir := t.TempDir()

	spec := map[string]any{
		"engine":        "codex",
		"model":         "gpt-5.4-mini",
		"effort":        "high",
		"prompt":        prompt,
		"system_prompt": judgeSystemPrompt,
		"cwd":           artifactDir,
		"artifact_dir":  artifactDir,
		"skip_skills":   true,
		"timeout_sec":   60,
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("judge: marshal spec: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--stdin", "--yes")
	cmd.Stdin = bytes.NewReader(specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Logf("judge dispatch failed: %v\nstderr: %s", err, stderr.String())
		return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("judge dispatch failed: %v", err)}
	}

	// Parse the dispatch result to get the judge's response.
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Logf("judge: stdout not valid JSON: %s", stdout.String())
		return Verdict{Pass: false, Score: 0.0, Reason: "judge output not valid JSON"}
	}

	response, _ := raw["response"].(string)
	if response == "" {
		return Verdict{Pass: false, Score: 0.0, Reason: "judge returned empty response"}
	}

	// Extract JSON from the response (it may be wrapped in markdown code fences).
	jsonStr := extractJSON(response)

	var judgeResult struct {
		Pass   bool    `json:"pass"`
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &judgeResult); err != nil {
		t.Logf("judge: failed to parse verdict JSON from response: %s", response)
		return Verdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("judge verdict not parseable: %v", err)}
	}

	return Verdict{
		Pass:   judgeResult.Pass && judgeResult.Score >= 0.7,
		Score:  judgeResult.Score,
		Reason: judgeResult.Reason,
	}
}

// extractJSON tries to find a JSON object in text, handling markdown code fences.
func extractJSON(text string) string {
	// Try to find JSON inside code fences first.
	if start := indexOf(text, "```json"); start >= 0 {
		inner := text[start+7:]
		if end := indexOf(inner, "```"); end >= 0 {
			return trimSpace(inner[:end])
		}
	}
	if start := indexOf(text, "```"); start >= 0 {
		inner := text[start+3:]
		if end := indexOf(inner, "```"); end >= 0 {
			return trimSpace(inner[:end])
		}
	}

	// Try to find a raw JSON object.
	braceStart := indexOf(text, "{")
	if braceStart >= 0 {
		depth := 0
		for i := braceStart; i < len(text); i++ {
			switch text[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return text[braceStart : i+1]
				}
			}
		}
	}

	return text
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
