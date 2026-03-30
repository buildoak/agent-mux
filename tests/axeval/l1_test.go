//go:build axeval

// L1 · Error Self-Correction
//
// Test question: "Agent, you just got this error response. Fix your invocation."
//
// Implementation:
// 1. For each error code in the ErrorCatalog: construct a realistic scenario.
// 2. Give the agent the error response (with code, message, hint, example) + the original failed command.
// 3. Prompt: "You ran this command and got this error. Write the corrected command."
// 4. LLM judge checks: is the corrected command valid? Would it avoid the original error?
//
// Key metric: first-attempt self-correction rate.
// This directly measures: do our AX-quality hint+example fields actually work?

package axeval

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// errorScenario defines a realistic error scenario for L1 testing.
type errorScenario struct {
	Name             string
	ErrorCode        string
	OriginalCommand  string
	ErrorJSON        string // pre-built error JSON the agent sees
	ChecklistItems   string // what the judge checks for in the corrected command
}

// buildErrorScenarios constructs test scenarios from the ErrorCatalog.
func buildErrorScenarios() []errorScenario {
	return []errorScenario{
		{
			Name:            "engine-not-found",
			ErrorCode:       "engine_not_found",
			OriginalCommand: `agent-mux -e openai --cwd /repo "Fix the bug in parser.go"`,
			ErrorJSON: mustMarshalError("engine_not_found", "Unknown engine name.",
				"agent-mux only supports the built-in engines codex, claude, and gemini.",
				"Retry with a valid engine. Example: agent-mux -e codex --cwd /repo \"<prompt>\".",
				true),
			ChecklistItems: `1. Does the corrected command use a valid engine (codex, claude, or gemini)?
2. Does the corrected command preserve the original prompt intent?
3. Does the corrected command include --cwd?
4. Is the corrected command syntactically valid?
5. Did the agent explain WHY the original engine was wrong?`,
		},
		{
			Name:            "model-not-found",
			ErrorCode:       "model_not_found",
			OriginalCommand: `agent-mux -e codex -m gpt-4-turbo --cwd /repo "Scan for SQL injection"`,
			ErrorJSON: mustMarshalError("model_not_found", "Unknown model for engine.",
				"The selected model is not available for the current engine.",
				"Retry with a supported model. Example: agent-mux -e codex -m gpt-5.4 --cwd /repo \"<prompt>\".",
				true),
			ChecklistItems: `1. Does the corrected command use a valid model for the codex engine (e.g., gpt-5.4, gpt-5.4-mini)?
2. Does the corrected command keep the same engine (codex)?
3. Does the corrected command preserve the original prompt?
4. Did the agent use the hint to understand the model was wrong for this engine?
5. Is the corrected command syntactically valid?`,
		},
		{
			Name:            "invalid-args-missing-cwd",
			ErrorCode:       "invalid_args",
			OriginalCommand: `agent-mux -e codex "Fix failing tests"`,
			ErrorJSON: mustMarshalError("invalid_args", "Invalid dispatch arguments.",
				"The dispatch request is missing required fields or contains invalid flag combinations.",
				"Provide a valid engine, prompt, and working directory. Example: agent-mux -e codex --cwd /repo \"Fix failing test\".",
				true),
			ChecklistItems: `1. Does the corrected command include --cwd with a directory path?
2. Does the corrected command preserve the original engine and prompt?
3. Did the agent identify that --cwd was the missing required field?
4. Is the corrected command syntactically valid?`,
		},
		{
			Name:            "frozen-killed-narrow-prompt",
			ErrorCode:       "frozen_killed",
			OriginalCommand: `agent-mux -e codex --cwd /repo "Analyze every file in this repository and write comprehensive documentation"`,
			ErrorJSON: mustMarshalError("frozen_killed",
				"Worker killed after prolonged silence.",
				"Worker was killed after prolonged silence - likely stuck in a hanging tool call. Partial work was preserved in the artifact directory.",
				"Retry with a narrower task: agent-mux -R=lifter --cwd /repo \"<narrowed prompt>\". Or extend silence timeout: add silence_kill_seconds=300 to config.",
				true),
			ChecklistItems: `1. Does the corrected command have a narrower/more specific prompt than the original?
2. Does the corrected command still target the same general goal?
3. Did the agent either narrow the prompt OR extend the timeout?
4. Did the agent recognize this was a frozen/stuck issue, not a prompt error?
5. Is the corrected command syntactically valid?`,
		},
		{
			Name:            "config-error-bad-role",
			ErrorCode:       "config_error",
			OriginalCommand: `agent-mux -R=super-worker --cwd /repo "Build the feature"`,
			ErrorJSON: mustMarshalError("config_error",
				"Configuration is invalid.",
				"agent-mux could not load or validate the referenced config, role, or control path.",
				"Fix the config file or role name, then retry. Example: agent-mux -R lifter --config /path/to/agent-mux.yaml --cwd /repo \"<prompt>\".",
				true),
			ChecklistItems: `1. Does the corrected command use a different (likely valid) role name?
2. Did the agent suggest checking available roles (e.g., agent-mux config roles)?
3. Does the corrected command preserve the original prompt intent?
4. Is the corrected command syntactically valid?`,
		},
		{
			Name:            "max-depth-exceeded",
			ErrorCode:       "max_depth_exceeded",
			OriginalCommand: `agent-mux -e codex --cwd /repo --allow-subdispatch "Recursively analyze all modules"`,
			ErrorJSON: mustMarshalError("max_depth_exceeded",
				"Max dispatch depth reached.",
				"This task tried to spawn more nested dispatches than the configured safety limit allows.",
				"Complete the work in the current agent, or raise the depth limit only if the nesting is intentional.",
				false),
			ChecklistItems: `1. Did the agent recognize this is NOT retryable (retryable=false)?
2. Did the agent suggest completing work in the current agent instead of nesting?
3. OR did the agent suggest raising the depth limit with appropriate caution?
4. Did the agent NOT simply retry the same command unchanged?`,
		},
		{
			Name:            "startup-failed",
			ErrorCode:       "startup_failed",
			OriginalCommand: `agent-mux -e gemini --cwd /repo "Research market trends"`,
			ErrorJSON: mustMarshalError("startup_failed",
				"Harness process failed to start.",
				"The harness process failed before a working session started.",
				"Check the harness install and arguments, then retry. Example: verify the engine binary runs directly from the same shell.",
				true),
			ChecklistItems: `1. Did the agent suggest verifying the engine binary (gemini) is installed and on PATH?
2. Did the agent suggest checking the installation rather than just retrying blindly?
3. Does the corrected approach include a diagnostic step?
4. Did the agent NOT just retry the exact same command?`,
		},
	}
}

// mustMarshalError builds a JSON error object matching the DispatchError schema.
func mustMarshalError(code, message, hint, example string, retryable bool) string {
	suggestion := hint + " " + example
	obj := map[string]any{
		"code":       code,
		"message":    message,
		"suggestion": suggestion,
		"hint":       hint,
		"example":    example,
		"retryable":  retryable,
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		panic("mustMarshalError: " + err.Error())
	}
	return string(data)
}

func TestL1ErrorSelfCorrection(t *testing.T) {
	scenarios := buildErrorScenarios()
	contract := outputContractDoc()

	for _, sc := range scenarios {
		sc := sc
		t.Run("L1/"+sc.Name, func(t *testing.T) {
			start := time.Now()

			// Step 1: Present the error to the agent.
			agentPrompt := fmt.Sprintf(`You ran the following agent-mux command:

%s

And got this error response:

%s

Using the error's hint and example fields, write the corrected command that would avoid this error.
Explain briefly WHY the original command failed and what you changed.

Write the corrected command on its own line starting with "CORRECTED: ".`, sc.OriginalCommand, sc.ErrorJSON)

			agentResponse := dispatchAgentUnderTest(t, binaryPath, agentPrompt, "")
			if agentResponse == "" {
				t.Fatal("agent-under-test returned empty response")
			}
			t.Logf("agent response length: %d", len(agentResponse))

			// Step 2: Judge whether the correction is valid.
			materials := &AXMaterials{
				AgentPrompt:    agentPrompt,
				AgentResponse:  agentResponse,
				ReferenceDoc:   contract,
				OriginalCommand: sc.OriginalCommand,
				ErrorPayload:   sc.ErrorJSON,
			}

			checklist := fmt.Sprintf(`Error code: %s
Original command: %s

Evaluate the agent's corrected command against these criteria:
%s

Additional criteria:
- Did the agent use information from the hint field to understand the problem?
- Did the agent use information from the example field to construct the fix?
- Is the overall self-correction response clear and actionable?

Score 1.0 if all items met. Deduct proportionally for each missed item.
A command that simply retries without changes scores 0.0 for retryable errors.`, sc.ErrorCode, sc.OriginalCommand, sc.ChecklistItems)

			verdict := axJudge(t, binaryPath, materials, checklist)
			verdict.Tier = TierL1
			verdict.CaseName = sc.Name
			verdict.Duration = time.Since(start)
			recordAXVerdict(verdict)

			if !verdict.Pass {
				t.Errorf("FAIL [L1/%s]: score=%.2f — %s", sc.Name, verdict.Score, verdict.Reason)
			}
			t.Logf("[L1] %s: pass=%v score=%.2f duration=%s",
				sc.Name, verdict.Pass, verdict.Score, verdict.Duration)
		})
	}
}
