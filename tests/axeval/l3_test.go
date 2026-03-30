//go:build axeval

// L3 · GSD Comprehension
//
// Test question: "GSD agents, read the cookbook, plan a real dispatch."
//
// Implementation:
// 1. Give GSD-Heavy and GSD-Light prompts to separate test agents.
// 2. Prompt: "You are GSD-Heavy/Light. A user asks you to dispatch a Codex worker
//    to analyze a repo. Show your exact dispatch plan."
// 3. LLM judge checks alignment with the agent-mux cookbook sections in the GSD prompts.
//
// This directly measures: can GSD agents use agent-mux correctly given their prompt?

package axeval

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// gsdHeavyPrompt returns the GSD-Heavy agent definition.
func gsdHeavyPrompt() string {
	// Try the coordinator's GSD-Heavy definition.
	paths := []string{
		"/Users/otonashi/thinking/pratchett-os/coordinator/.claude/agents/gsd-heavy.md",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return "(gsd-heavy.md not found)"
}

// gsdLightPrompt returns the GSD-Light agent definition.
func gsdLightPrompt() string {
	paths := []string{
		"/Users/otonashi/thinking/pratchett-os/coordinator/.claude/agents/gsd-light.md",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return "(gsd-light.md not found)"
}

// l3Scenario defines a task for GSD agents.
type l3Scenario struct {
	Name         string
	GSDVariant   string // "heavy" or "light"
	GSDPrompt    string // the GSD agent's system prompt
	Task         string // what the user asks the GSD agent
	Checklist    string // evaluation rubric
}

func buildL3Scenarios() []l3Scenario {
	heavy := gsdHeavyPrompt()
	light := gsdLightPrompt()
	skill := skillDoc()

	return []l3Scenario{
		{
			Name:       "heavy-novel-problem",
			GSDVariant: "heavy",
			GSDPrompt:  heavy,
			Task: `A user says: "I need to reverse-engineer an undocumented API by analyzing network traffic logs, then generate a client SDK from the findings."

You are GSD-Heavy. This is a novel problem with no clear pipeline.
Plan your approach using agent-mux. Show:
1. How you would explore the problem (which engine/role for what).
2. Your dispatch commands with exact flags.
3. Your verification gates.

You have access to agent-mux. Here is the agent-mux skill documentation:
` + skill,
			Checklist: fmt.Sprintf(`Evaluate as if this came from a GSD-Heavy coordinator:
1. Does the plan show strategic thinking (not just mechanical execution)?
2. Does it use appropriate roles (e.g., researcher/explorer for analysis, lifter for implementation)?
3. Does it use --async + wait + result collection pattern?
4. Does it define verification gates (how the agent knows it's done)?
5. Does it use valid agent-mux syntax and flags?
6. Does it use --cwd for dispatches?
7. Does it show awareness of engine cognitive styles (Claude for exploration, Codex for precision)?
8. Does it have a clear multi-step structure with decision points?
9. Does it handle potential failures between steps?
10. Does it NOT use invalid flags (--sandbox none, --output, etc.)?

The GSD-Heavy agent definition begins with:
%s

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`, truncateStr(heavy, 500)),
		},
		{
			Name:       "light-known-pipeline",
			GSDVariant: "light",
			GSDPrompt:  light,
			Task: `A user says: "Run the standard build pipeline: have an architect plan the changes to add retry logic to the HTTP client, then have a lifter implement it, then have an auditor verify it."

You are GSD-Light. This is a known pipeline (plan -> implement -> verify).
Execute the plan using agent-mux. Show the exact commands.

You have access to agent-mux. Here is the agent-mux skill documentation:
` + skill,
			Checklist: fmt.Sprintf(`Evaluate as if this came from a GSD-Light executor:
1. Does it follow a clear sequential pipeline (plan -> implement -> verify)?
2. Does it use appropriate roles (-R=architect, -R=lifter, -R=auditor)?
3. Does it use --async + wait + result collection for each step?
4. Does it pass context from one step to the next (via prompt or --context-file)?
5. Does it use valid agent-mux syntax throughout?
6. Does it use --cwd for each dispatch?
7. Does it check result status before proceeding to next step?
8. Does it redirect stderr (2>/dev/null)?
9. Does the plan show mechanical execution, not strategic pivoting?
10. Does it NOT over-engineer or add unnecessary steps?

The GSD-Light agent definition begins with:
%s

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`, truncateStr(light, 500)),
		},
		{
			Name:       "heavy-dispatch-with-recovery",
			GSDVariant: "heavy",
			GSDPrompt:  heavy,
			Task: `A user says: "Migrate the database schema from v2 to v3. The migration involves 15 tables and typically takes the worker 20+ minutes, so timeouts are expected."

You are GSD-Heavy. Plan how you would handle this with agent-mux, knowing workers will likely time out.
Show your dispatch plan with recovery strategy.

You have access to agent-mux. Here is the agent-mux skill documentation:
` + skill,
			Checklist: `Evaluate the GSD-Heavy recovery plan:
1. Does it anticipate timeouts and plan for them upfront?
2. Does it break the work into smaller chunks (e.g., groups of tables)?
3. Or does it use --recover for continuation after timeout?
4. Does it check activity.files_changed to decide whether to recover or reframe?
5. Does it use appropriate effort tiers (high or xhigh for long tasks)?
6. Does it set appropriate timeouts or use roles with long timeouts?
7. Does it use valid agent-mux syntax?
8. Does it have a clear escalation path (what to do if recovery also fails)?
9. Does the plan demonstrate strategic depth, not just mechanical retry?
10. Does it use --async + wait + result collection pattern?

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`,
		},
		{
			Name:       "light-parallel-fanout",
			GSDVariant: "light",
			GSDPrompt:  light,
			Task: `A user says: "Scan these 4 microservices for deprecated API usage: auth-service, user-service, billing-service, notification-service. All are in /home/user/services/<name>/."

You are GSD-Light. Execute parallel scans using agent-mux.
Show the exact commands for parallel dispatch and result collection.

You have access to agent-mux. Here is the agent-mux skill documentation:
` + skill,
			Checklist: `Evaluate the GSD-Light parallel execution:
1. Does it dispatch exactly 4 parallel --async commands?
2. Does each dispatch target a different service directory (--cwd)?
3. Does it use an appropriate role (-R=scout for scanning)?
4. Does it wait for all 4 to complete?
5. Does it collect results from all 4 dispatches?
6. Does it aggregate/summarize the findings?
7. Does it use valid agent-mux syntax?
8. Does it redirect stderr (2>/dev/null)?
9. Does it handle the case where one scan fails?
10. Are the prompts specific to each service?

Score 1.0 if all 10 items met. Deduct 0.1 per missed item.`,
		},
	}
}

func TestL3GSDComprehension(t *testing.T) {
	scenarios := buildL3Scenarios()

	// Check if GSD prompt files are available.
	if gsdHeavyPrompt() == "(gsd-heavy.md not found)" {
		t.Log("WARNING: gsd-heavy.md not found, L3 heavy tests will run with placeholder")
	}
	if gsdLightPrompt() == "(gsd-light.md not found)" {
		t.Log("WARNING: gsd-light.md not found, L3 light tests will run with placeholder")
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run("L3/"+sc.Name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()

			// Use the GSD prompt as system context.
			systemPrompt := fmt.Sprintf("You are GSD-%s, a coordinator agent. Here is your full agent definition:\n\n%s",
				sc.GSDVariant, truncateStr(sc.GSDPrompt, 8000))

			agentResponse := dispatchAgentUnderTest(t, binaryPath, sc.Task, systemPrompt)
			if agentResponse == "" {
				t.Fatal("agent-under-test returned empty response")
			}
			t.Logf("agent response length: %d", len(agentResponse))

			// Judge.
			materials := &AXMaterials{
				AgentPrompt:  sc.Task,
				AgentResponse: agentResponse,
				ReferenceDoc:  skillDoc(),
				Extra: map[string]string{
					"GSD Variant": sc.GSDVariant,
				},
			}

			verdict := axJudge(t, binaryPath, materials, sc.Checklist)
			verdict.Tier = TierL3
			verdict.CaseName = sc.Name
			verdict.Duration = time.Since(start)
			recordAXVerdict(verdict)

			if !verdict.Pass {
				t.Errorf("FAIL [L3/%s]: score=%.2f — %s", sc.Name, verdict.Score, verdict.Reason)
			}
			t.Logf("[L3] %s: pass=%v score=%.2f duration=%s",
				sc.Name, verdict.Pass, verdict.Score, verdict.Duration)
		})
	}
}

// Ensure no unused import issues.
var _ = filepath.Join
var _ = os.ReadFile
