//go:build axeval

package axeval

import "time"

// ── Tiers ──────────────────────────────────────────────────────────────

// Tier groups test cases by what layer of Agent Experience they exercise.
type Tier string

const (
	TierL0 Tier = "L0" // Contract Comprehension
	TierL1 Tier = "L1" // Error Self-Correction
	TierL2 Tier = "L2" // Skill Comprehension
	TierL3 Tier = "L3" // GSD Comprehension
	TierL4 Tier = "L4" // Hard (live dispatch)
)

// ── Categories (kept for backward compat with existing tests) ──────

// Category groups test cases by what they exercise (legacy, used by L4).
type Category string

const (
	CatCompletion  Category = "completion"
	CatCorrectness Category = "correctness"
	CatQuality     Category = "quality"
	CatLiveness    Category = "liveness"
	CatError       Category = "error"
	CatEvents      Category = "events"
	CatStreaming   Category = "streaming"
	CatSteering    Category = "steering"
)

// ── AX Test Case ───────────────────────────────────────────────────────

// AXCase defines a single ax-eval test case in the tier system.
type AXCase struct {
	Name  string
	Tier  Tier
	// Prompt given to the agent-under-test (for L0-L3: what we ask the judge agent).
	Prompt string
	// Setup returns the materials for the test (e.g., a dispatch result JSON, error payload).
	// May perform live dispatches during setup.
	Setup func(ctx *AXContext) (*AXMaterials, error)
	// Checklist is the evaluation rubric given to the LLM judge.
	Checklist string
	// DeterministicCheck is an optional pre-judge gate. If it returns a non-nil verdict
	// with Pass=false, the judge is skipped. Use for structural pre-checks.
	DeterministicCheck func(materials *AXMaterials, judgeResponse string) *AXVerdict
}

// AXMaterials holds the inputs assembled during Setup for the judge to evaluate.
type AXMaterials struct {
	// AgentPrompt is what was given to the agent-under-test.
	AgentPrompt string
	// AgentResponse is what the agent-under-test produced.
	AgentResponse string
	// ReferenceDoc is the reference documentation (output-contract, SKILL.md, etc.).
	ReferenceDoc string
	// OriginalCommand is the command that produced an error (L1).
	OriginalCommand string
	// ErrorPayload is the JSON error object (L1).
	ErrorPayload string
	// Extra holds any additional context specific to the test.
	Extra map[string]string
}

// AXVerdict is the outcome of an AX tier evaluation.
type AXVerdict struct {
	Pass      bool    `json:"pass"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	Tier      Tier    `json:"tier"`
	CaseName  string  `json:"case_name"`
	Duration  time.Duration
	JudgeUsed bool `json:"judge_used"`
}

// AXContext provides test infrastructure to Setup functions.
type AXContext struct {
	BinaryPath string
	FixtureDir string
	RepoRoot   string
}

// ── Legacy types (kept for L4 backward compat) ─────────────────────

// TestCase defines a single ax-eval behavioral test (legacy, used by L4).
type TestCase struct {
	Name         string
	Category     Category
	Engine       string        // "codex"
	Model        string        // "gpt-5.4-mini"
	Effort       string        // "high"
	Prompt       string        // task for the worker
	CWD          string        // set to fixture dir (resolved to absolute)
	TimeoutSec   int           // agent-mux --timeout value
	MaxWallClock time.Duration // test-level context timeout
	SkipSkills   bool
	SkipReason   string
	ExtraFlags   []string                                   // additional CLI flags (e.g. "--stream", "--async")
	IsAsync      bool                                       // true = use dispatchAsync flow (dispatch + result collection)
	SteerSpec    *SteerSpec                                 // non-nil = dispatch async, sleep, steer, then collect
	Evaluate     func(Result) Verdict                       // deterministic check (always runs)
	EvalAsync    func(ack Result, collected Result) Verdict // async-specific evaluator
	JudgePrompt  string                                     // non-empty = run LLM-as-judge tier 2
	EngineOpts   map[string]string                          // e.g. silence thresholds for liveness
}

// Result captures everything from a single dispatch.
type Result struct {
	Status       string
	Response     string
	ErrorCode    string
	ErrorMessage string
	Events       []Event
	ArtifactDir  string
	Duration     time.Duration
	ExitCode     int
	RawStdout    []byte
	RawStderr    []byte
}

// Event is a parsed line from events.jsonl.
type Event struct {
	Type           string `json:"type"`
	Message        string `json:"message,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	SilenceSeconds int    `json:"silence_seconds,omitempty"`
	Status         string `json:"status,omitempty"`
	Timestamp      string `json:"ts,omitempty"`
}

// SteerSpec describes a mid-flight steering action to apply during an async dispatch.
type SteerSpec struct {
	DelayBeforeSteer time.Duration // how long to wait after dispatch before steering
	Action           string        // "nudge", "abort", "redirect"
	Message          string        // message argument for nudge/redirect
}

// Verdict is the outcome of evaluating a Result (legacy, used by L4).
type Verdict struct {
	Pass   bool
	Score  float64 // 0.0-1.0
	Reason string
	Events []string // event types observed
}
