//go:build axeval

// L2 · Skill Comprehension
//
// Test question: "Agent, here's the skill doc. Plan a 3-step dispatch pipeline."
//
// Implementation:
// 1. Fan out Codex workers with the full skill/SKILL.md
// 2. Each gets a varied complex task requiring multi-step dispatch planning.
// 3. LLM judge with checklist: correct flags, valid engines, proper patterns.
//
// This directly measures: can an agent read our skill doc and USE agent-mux correctly?

package axeval

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// skillDoc returns the content of skill/SKILL.md.
func skillDoc() string {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	data, err := os.ReadFile(filepath.Join(repoRoot, "skill", "SKILL.md"))
	if err != nil {
		return "(SKILL.md not found)"
	}
	return string(data)
}

// l2Scenario describes a complex task for the agent to plan.
type l2Scenario struct {
	Name       string
	Task       string
	Checklist  string
}

func buildL2Scenarios() []l2Scenario {
	return []l2Scenario{
		{
			Name: "audit-then-fix-then-verify",
			Task: `You are a coordinator agent with access to agent-mux. A user asks you to:
"Audit the authentication module in /home/user/webapp for security issues, fix any critical issues found, and verify the fixes pass tests."

Plan a 3-step pipeline using agent-mux. Show the exact commands you would run.
Each step should use --async, wait for completion, and check the result before proceeding.`,
			Checklist: `Evaluate the agent's dispatch plan:
1. Does it use --async for each dispatch? (required for coordinator patterns)
2. Does it use "agent-mux wait --poll <duration> <id>" to wait for completion? (NOT polling steer status in a loop)
3. Does it use "agent-mux result <id> --json" to collect results?
4. Does it use --cwd or -C= to set the working directory?
5. Does it use valid engines (codex, claude, or gemini)?
6. Does it use roles (-R=) or at minimum valid engine flags?
7. Does it NOT use invalid flags like "--sandbox none" or "--output"?
8. Does each step have a distinct, specific prompt (not vague)?
9. Does it redirect stderr (2>/dev/null) on agent-mux calls?
10. Does it check status in the result before proceeding to the next step?
11. Is the overall 3-step flow logical (audit -> fix -> verify)?

Score 1.0 if all 11 items met. Deduct ~0.09 per missed item.
Critical failures (invalid flags, polling steer status) are -0.2 each.`,
		},
		{
			Name: "parallel-research-synthesis",
			Task: `You are a coordinator agent with access to agent-mux. A user asks you to:
"Research three different approaches to implementing rate limiting: token bucket, sliding window, and leaky bucket. Then synthesize the findings into a recommendation."

Plan a multi-step pipeline using agent-mux:
- Step 1: Fan out 3 parallel research dispatches (one per approach).
- Step 2: Synthesize all three results into a recommendation.

Show the exact commands.`,
			Checklist: `Evaluate the agent's dispatch plan:
1. Does Step 1 dispatch exactly 3 parallel --async commands?
2. Does it wait for all 3 to complete before starting Step 2?
3. Does it collect results from all 3 dispatches?
4. Does Step 2 receive context from Step 1 results (via prompt or --context-file)?
5. Does it use appropriate roles/engines (research -> Claude/Codex, synthesis -> Claude)?
6. Does it use --cwd for each dispatch?
7. Does it NOT use invalid flags?
8. Does it redirect stderr (2>/dev/null)?
9. Does it handle the case where a research dispatch fails?
10. Are the prompts specific to each approach (not generic)?

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`,
		},
		{
			Name: "recovery-workflow",
			Task: `You are a coordinator agent with access to agent-mux. A user asks you to:
"Implement the payment processing module. If the worker times out, recover and continue from where it left off."

Plan a dispatch with recovery using agent-mux:
- Step 1: Dispatch the initial implementation task.
- Step 2: Check the result. If timed_out with files_changed, use --recover to continue.
- Step 3: If timed_out with empty files_changed, reframe with a narrower scope.

Show the exact commands and decision logic.`,
			Checklist: `Evaluate the agent's dispatch plan:
1. Does it dispatch the initial task with --async?
2. Does it use "wait" to wait for completion?
3. Does it check the status field in the result?
4. Does it correctly differentiate between timed_out+files_changed vs timed_out+empty?
5. For timed_out with files_changed: does it use --recover=<dispatch_id>?
6. For timed_out with empty: does it reframe with a narrower prompt?
7. Does it NOT blindly retry the same prompt on timeout?
8. Does it use valid agent-mux syntax throughout?
9. Does it mention checking activity.files_changed?
10. Is the overall recovery flow logical and matches the skill doc's failure decision tree?

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`,
		},
		{
			Name: "steer-mid-flight",
			Task: `You are a coordinator agent with access to agent-mux. A worker is running a long analysis task that you dispatched 5 minutes ago. You realize the scope needs to be narrowed.

Using agent-mux, show the exact commands to:
1. Check if the worker is still running.
2. Redirect the worker to a narrower scope.
3. Wait for the updated result.
4. If redirect fails, abort and redispatch with the new scope.

The dispatch ID is "01KMY3ABC".`,
			Checklist: `Evaluate the agent's steering commands:
1. Does it use "agent-mux steer 01KMY3ABC status" (not a loop) for the live check?
2. Does it use "agent-mux steer 01KMY3ABC redirect <message>" with a specific redirect message?
3. Does it use "agent-mux wait --poll <duration> 01KMY3ABC" to wait after redirect?
4. Does it have a fallback using "agent-mux steer 01KMY3ABC abort" if redirect fails?
5. Does the abort fallback include a redispatch with narrower scope?
6. Does it redirect stderr (2>/dev/null) on all commands?
7. Does it NOT poll "steer status" in a loop? (use wait instead)
8. Are the steer commands syntactically correct (action before message)?

Score 1.0 if all 8 items met. Deduct 0.125 per missed item.
Polling steer status in a loop is a critical anti-pattern: -0.3.`,
		},
		{
			Name: "context-and-roles",
			Task: `You are a coordinator agent with access to agent-mux. A user asks you to:
"We have a detailed specification in /tmp/spec.md (2000 lines). Have a scout quickly scan it, then have an architect plan the implementation, then have a lifter implement the first module."

Plan this 3-step pipeline using agent-mux roles and context passing.
Show the exact commands, including how context flows between steps.`,
			Checklist: `Evaluate the agent's plan:
1. Does it use -R=scout for the first step (scanning)?
2. Does it use -R=architect for the second step (planning)?
3. Does it use -R=lifter for the third step (implementation)?
4. Does it pass the spec to the first step via --context-file or including in prompt?
5. Does it pass output from step 1 to step 2 (via --context-file, prompt injection, or result piping)?
6. Does it pass output from step 2 to step 3?
7. Does it use --async + wait + result collection pattern?
8. Does it use --cwd for each dispatch?
9. Does it NOT use roles that don't exist in the skill doc?
10. Are timeouts appropriate (scout fast, architect medium, lifter longer)?

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`,
		},
	}
}

func TestL2SkillComprehension(t *testing.T) {
	skill := skillDoc()
	scenarios := buildL2Scenarios()

	for _, sc := range scenarios {
		sc := sc
		t.Run("L2/"+sc.Name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()

			// Give the agent the full SKILL.md + the task.
			agentPrompt := fmt.Sprintf(`You have access to agent-mux. Here is the complete skill documentation:

%s

---

Now complete this task:

%s`, skill, sc.Task)

			agentResponse := dispatchAgentUnderTest(t, binaryPath, agentPrompt,
				"You are a coordinator agent. You plan and dispatch agent-mux commands. Show exact commands.")
			if agentResponse == "" {
				t.Fatal("agent-under-test returned empty response")
			}
			t.Logf("agent response length: %d", len(agentResponse))

			// Judge the plan.
			materials := &AXMaterials{
				AgentPrompt:  sc.Task,
				AgentResponse: agentResponse,
				ReferenceDoc:  skill,
			}

			verdict := axJudge(t, binaryPath, materials, sc.Checklist)
			verdict.Tier = TierL2
			verdict.CaseName = sc.Name
			verdict.Duration = time.Since(start)
			recordAXVerdict(verdict)

			if !verdict.Pass {
				t.Errorf("FAIL [L2/%s]: score=%.2f — %s", sc.Name, verdict.Score, verdict.Reason)
			}
			t.Logf("[L2] %s: pass=%v score=%.2f duration=%s",
				sc.Name, verdict.Pass, verdict.Score, verdict.Duration)
		})
	}
}
