//go:build axeval

// L0 · Contract Comprehension
//
// Test question: "Agent, here's a real dispatch result + our output-contract doc.
// Is this aligned? Parse it. Tell me what each field means."
//
// Implementation:
// 1. Dispatch a real agent-mux command to generate a real result JSON.
// 2. Give the result JSON + output-contract.md to a fresh agent (Codex).
// 3. Prompt: "Parse every field. Explain what each means. Flag misalignments."
// 4. LLM judge checks: did the agent parse all fields correctly?
//
// This directly measures: can an agent UNDERSTAND agent-mux output?

package axeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// outputContractDoc returns the content of references/output-contract.md.
func outputContractDoc() string {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	data, err := os.ReadFile(filepath.Join(repoRoot, "references", "output-contract.md"))
	if err != nil {
		// Fall back to skill copy.
		data, err = os.ReadFile(filepath.Join(repoRoot, "skill", "references", "output-contract.md"))
		if err != nil {
			return "(output-contract.md not found)"
		}
	}
	return string(data)
}

// generateRealDispatchResult dispatches a real command and returns the raw stdout JSON.
func generateRealDispatchResult(t *testing.T, binary string, cwd string) ([]byte, error) {
	t.Helper()

	artifactDir := t.TempDir()
	spec := map[string]any{
		"engine":       "codex",
		"model":        "gpt-5.4-mini",
		"effort":       "high",
		"prompt":       "What is 2+2? Answer with just the number.",
		"cwd":          cwd,
		"artifact_dir": artifactDir,
		"skip_skills":  true,
		"timeout_sec":  120,
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := buildCommand(ctx, binary, []string{"--stdin", "--yes"}, specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("dispatch failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// generateFailedDispatchResult dispatches a command designed to fail and returns stdout.
func generateFailedDispatchResult(t *testing.T, binary string, cwd string) ([]byte, error) {
	t.Helper()

	artifactDir := t.TempDir()
	spec := map[string]any{
		"engine":       "bogus_engine",
		"model":        "gpt-5.4-mini",
		"effort":       "high",
		"prompt":       "This should fail.",
		"cwd":          cwd,
		"artifact_dir": artifactDir,
		"skip_skills":  true,
		"timeout_sec":  30,
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	cmd := buildCommand(ctx, binary, []string{"--stdin", "--yes"}, specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// We expect a non-zero exit here — that's the point.
	_ = cmd.Run()

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("no stdout from failed dispatch; stderr: %s", stderr.String())
	}

	return stdout.Bytes(), nil
}

func TestL0ContractComprehension(t *testing.T) {
	cwd := fixtureDir()
	contract := outputContractDoc()

	t.Run("L0/completed-result-parse", func(t *testing.T) {
		start := time.Now()

		// Step 1: Generate a real completed dispatch result.
		resultJSON, err := generateRealDispatchResult(t, binaryPath, cwd)
		if err != nil {
			t.Fatalf("generate real dispatch result: %v", err)
		}
		t.Logf("generated real dispatch result (%d bytes)", len(resultJSON))

		// Pretty-print for the agent.
		var prettyResult bytes.Buffer
		if err := json.Indent(&prettyResult, resultJSON, "", "  "); err != nil {
			prettyResult.Write(resultJSON) // fallback to raw
		}

		// Step 2: Ask the agent to parse the result against the contract.
		agentPrompt := fmt.Sprintf(`You are given the output-contract documentation for agent-mux and a real dispatch result JSON.

Your task:
1. Parse every top-level field in the result JSON.
2. For each field, explain what it means according to the documentation.
3. Flag any field that does not match the documentation or is confusing.
4. Confirm the status value is valid per the contract.
5. Confirm metadata.engine and metadata.model are present and valid.
6. Confirm activity has all four required arrays (files_changed, files_read, commands_run, tool_calls).
7. State whether schema_version is correct.

Be precise. Name every field you see.

## Output Contract Documentation
%s

## Real Dispatch Result
%s`, contract, prettyResult.String())

		agentResponse := dispatchAgentUnderTest(t, binaryPath, agentPrompt, "")
		if agentResponse == "" {
			t.Fatal("agent-under-test returned empty response")
		}
		t.Logf("agent response length: %d", len(agentResponse))

		// Step 3: Judge the agent's response.
		materials := &AXMaterials{
			AgentPrompt:   agentPrompt,
			AgentResponse: agentResponse,
			ReferenceDoc:  contract,
			Extra: map[string]string{
				"Real Result JSON": prettyResult.String(),
			},
		}

		checklist := `Check each criterion:
1. Did the agent identify and explain schema_version (should be 1)?
2. Did the agent identify and explain status (should be "completed")?
3. Did the agent identify dispatch_id?
4. Did the agent identify and explain the response field?
5. Did the agent identify the activity object with its four arrays?
6. Did the agent identify the metadata object (engine, model, tokens, turns)?
7. Did the agent identify duration_ms?
8. Did the agent correctly note that error is null for completed dispatches?
9. Did the agent flag any genuine misalignments (real or false-positive)?
10. Did the agent NOT hallucinate field meanings that contradict the documentation?

Score 1.0 if all 10 met. Deduct 0.1 per missed item. Hallucinated meanings count as -0.2.`

		verdict := axJudge(t, binaryPath, materials, checklist)
		verdict.Tier = TierL0
		verdict.CaseName = "completed-result-parse"
		verdict.Duration = time.Since(start)
		recordAXVerdict(verdict)

		if !verdict.Pass {
			t.Errorf("FAIL [L0/completed-result-parse]: score=%.2f — %s", verdict.Score, verdict.Reason)
		}
		t.Logf("[L0] completed-result-parse: pass=%v score=%.2f duration=%s", verdict.Pass, verdict.Score, verdict.Duration)
	})

	t.Run("L0/failed-result-parse", func(t *testing.T) {
		start := time.Now()

		// Step 1: Generate a real failed dispatch result.
		resultJSON, err := generateFailedDispatchResult(t, binaryPath, cwd)
		if err != nil {
			t.Fatalf("generate failed dispatch result: %v", err)
		}
		t.Logf("generated failed dispatch result (%d bytes)", len(resultJSON))

		var prettyResult bytes.Buffer
		if err := json.Indent(&prettyResult, resultJSON, "", "  "); err != nil {
			prettyResult.Write(resultJSON)
		}

		// Step 2: Ask the agent to parse the failed result.
		agentPrompt := fmt.Sprintf(`You are given the output-contract documentation for agent-mux and a real dispatch result that FAILED.

Your task:
1. Parse every top-level field in the result JSON.
2. Identify that the status is "failed" and explain what that means.
3. Parse the error object: identify code, message, hint, example, retryable fields.
4. Based on the error.retryable field, state whether this error can be retried.
5. Based on error.hint and error.example, suggest what the caller should do next.
6. Confirm the metadata and activity objects are present even on failure.

Be precise. Name every field you see.

## Output Contract Documentation
%s

## Real Failed Dispatch Result
%s`, contract, prettyResult.String())

		agentResponse := dispatchAgentUnderTest(t, binaryPath, agentPrompt, "")
		if agentResponse == "" {
			t.Fatal("agent-under-test returned empty response")
		}

		// Step 3: Judge.
		materials := &AXMaterials{
			AgentPrompt:   agentPrompt,
			AgentResponse: agentResponse,
			ReferenceDoc:  contract,
			Extra: map[string]string{
				"Failed Result JSON": prettyResult.String(),
			},
		}

		checklist := `Check each criterion:
1. Did the agent identify that status is "failed"?
2. Did the agent parse the error object and identify the error code?
3. Did the agent explain what the error code means?
4. Did the agent identify hint and example fields in the error?
5. Did the agent state whether the error is retryable based on the retryable field?
6. Did the agent suggest a corrective action based on hint/example?
7. Did the agent NOT hallucinate error codes or field meanings?
8. Did the agent note that metadata and activity are still present on failed results?

Score 1.0 if all 8 met. Deduct 0.125 per missed item.`

		verdict := axJudge(t, binaryPath, materials, checklist)
		verdict.Tier = TierL0
		verdict.CaseName = "failed-result-parse"
		verdict.Duration = time.Since(start)
		recordAXVerdict(verdict)

		if !verdict.Pass {
			t.Errorf("FAIL [L0/failed-result-parse]: score=%.2f — %s", verdict.Score, verdict.Reason)
		}
		t.Logf("[L0] failed-result-parse: pass=%v score=%.2f duration=%s", verdict.Pass, verdict.Score, verdict.Duration)
	})

	t.Run("L0/field-meaning-accuracy", func(t *testing.T) {
		start := time.Now()

		// This test checks if the agent can distinguish between similar fields
		// and doesn't conflate them (e.g., response vs handoff_summary).
		agentPrompt := fmt.Sprintf(`You are given the output-contract documentation for agent-mux.

Answer these specific questions precisely:

1. What is the difference between "response" and "handoff_summary"?
2. When is "full_output_path" set vs null?
3. What three values can "status" take and what does each mean?
4. What is the difference between "partial" and "recoverable"?
5. In the error object, what is the difference between "hint" and "example"?

## Output Contract Documentation
%s`, contract)

		agentResponse := dispatchAgentUnderTest(t, binaryPath, agentPrompt, "")
		if agentResponse == "" {
			t.Fatal("agent-under-test returned empty response")
		}

		materials := &AXMaterials{
			AgentPrompt:   agentPrompt,
			AgentResponse: agentResponse,
			ReferenceDoc:  contract,
		}

		checklist := `Check each question was answered correctly:
1. response is the full worker response text; handoff_summary is extracted from ## Summary/## Handoff headers or shortened. They are NOT the same.
2. full_output_path is set when response was truncated due to response_max_chars; null otherwise.
3. status can be: completed (clean exit), timed_out (timeout), failed (validation/startup/adapter error).
4. partial=true and recoverable=true appear on timed_out runs. partial means work is incomplete, recoverable means it can be resumed.
5. hint is guidance text; example is the corrective command example.

Score 1.0 if all 5 answered correctly. Deduct 0.2 per wrong/missing answer. Hallucinations count as -0.2.`

		verdict := axJudge(t, binaryPath, materials, checklist)
		verdict.Tier = TierL0
		verdict.CaseName = "field-meaning-accuracy"
		verdict.Duration = time.Since(start)
		recordAXVerdict(verdict)

		if !verdict.Pass {
			t.Errorf("FAIL [L0/field-meaning-accuracy]: score=%.2f — %s", verdict.Score, verdict.Reason)
		}
		t.Logf("[L0] field-meaning-accuracy: pass=%v score=%.2f duration=%s", verdict.Pass, verdict.Score, verdict.Duration)
	})
}

// Ensure unused imports don't cause build errors.
var _ = strings.Contains
