//go:build axeval

package axeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// axJudge dispatches an LLM-as-judge evaluation via agent-mux.
// It sends the assembled materials and a checklist rubric to gpt-5.4-mini
// and returns a structured verdict.
//
// The judge is itself an agent-mux dispatch — we use our own tool to evaluate.
func axJudge(t *testing.T, binary string, materials *AXMaterials, checklist string) AXVerdict {
	t.Helper()

	judgeSystemPrompt := `You are an AX (Agent Experience) evaluation judge.

You will be given:
1. Materials from a test scenario (agent prompt, agent response, reference docs, etc.)
2. A checklist of criteria to evaluate against.

Evaluate the agent's response against every checklist item. Be strict but fair.

Return ONLY valid JSON in this exact format:
{"pass": true, "score": 0.85, "reason": "Brief explanation of what passed and what failed."}

Scoring:
- 1.0: All checklist items met perfectly.
- 0.7-0.99: All critical items met, minor issues.
- 0.5-0.69: Some items met but gaps remain (marginal).
- 0.0-0.49: Critical items failed.

A score >= 0.7 means pass. Below 0.7 is fail.`

	prompt := buildJudgePrompt(materials, checklist)

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
		"timeout_sec":   90,
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("ax_judge: marshal spec: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := buildCommand(ctx, binary, []string{"--stdin", "--yes"}, specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Logf("ax_judge dispatch failed: %v\nstderr: %s", err, stderr.String())
		return AXVerdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("judge dispatch failed: %v", err)}
	}

	// Parse the dispatch result to get the judge's response.
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Logf("ax_judge: stdout not valid JSON: %s", stdout.String())
		return AXVerdict{Pass: false, Score: 0.0, Reason: "judge output not valid JSON"}
	}

	response, _ := raw["response"].(string)
	if response == "" {
		return AXVerdict{Pass: false, Score: 0.0, Reason: "judge returned empty response"}
	}

	// Extract JSON from the response (it may be wrapped in markdown code fences).
	jsonStr := extractJSON(response)

	var judgeResult struct {
		Pass   bool    `json:"pass"`
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &judgeResult); err != nil {
		t.Logf("ax_judge: failed to parse verdict JSON from response: %s", response)
		return AXVerdict{Pass: false, Score: 0.0, Reason: fmt.Sprintf("judge verdict not parseable: %v", err)}
	}

	return AXVerdict{
		Pass:      judgeResult.Pass && judgeResult.Score >= 0.7,
		Score:     judgeResult.Score,
		Reason:    judgeResult.Reason,
		JudgeUsed: true,
	}
}

// buildJudgePrompt assembles the full prompt for the LLM judge from materials.
func buildJudgePrompt(materials *AXMaterials, checklist string) string {
	var b bytes.Buffer

	if materials.ReferenceDoc != "" {
		b.WriteString("## Reference Documentation\n")
		b.WriteString(materials.ReferenceDoc)
		b.WriteString("\n\n")
	}

	if materials.AgentPrompt != "" {
		b.WriteString("## Agent Prompt (what was given to the agent)\n")
		b.WriteString(materials.AgentPrompt)
		b.WriteString("\n\n")
	}

	if materials.OriginalCommand != "" {
		b.WriteString("## Original Command\n")
		b.WriteString(materials.OriginalCommand)
		b.WriteString("\n\n")
	}

	if materials.ErrorPayload != "" {
		b.WriteString("## Error Response\n")
		b.WriteString(materials.ErrorPayload)
		b.WriteString("\n\n")
	}

	if materials.AgentResponse != "" {
		b.WriteString("## Agent Response (what the agent produced)\n")
		b.WriteString(materials.AgentResponse)
		b.WriteString("\n\n")
	}

	for k, v := range materials.Extra {
		b.WriteString(fmt.Sprintf("## %s\n%s\n\n", k, v))
	}

	b.WriteString("## Evaluation Checklist\n")
	b.WriteString(checklist)
	b.WriteString("\n\nEvaluate and return JSON verdict.\n")

	return b.String()
}

// dispatchAgentUnderTest dispatches a prompt to an agent via agent-mux and
// returns the agent's response text. Used to get agent responses for L0-L3.
func dispatchAgentUnderTest(t *testing.T, binary string, agentPrompt string, systemPrompt string) string {
	t.Helper()

	artifactDir := t.TempDir()

	spec := map[string]any{
		"engine":       "codex",
		"model":        "gpt-5.4-mini",
		"effort":       "high",
		"prompt":       agentPrompt,
		"cwd":          artifactDir,
		"artifact_dir": artifactDir,
		"skip_skills":  true,
		"timeout_sec":  120,
	}
	if systemPrompt != "" {
		spec["system_prompt"] = systemPrompt
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("dispatchAgentUnderTest: marshal spec: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := buildCommand(ctx, binary, []string{"--stdin", "--yes"}, specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Logf("dispatchAgentUnderTest failed: %v\nstderr: %s", err, stderr.String())
		return ""
	}

	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Logf("dispatchAgentUnderTest: stdout not valid JSON: %s", stdout.String())
		return ""
	}

	response, _ := raw["response"].(string)
	return response
}
