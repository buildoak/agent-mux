//go:build axeval

package axeval

import "time"

// Category groups test cases by what they exercise.
type Category string

const (
	CatCompletion  Category = "completion"
	CatCorrectness Category = "correctness"
	CatQuality     Category = "quality"
	CatLiveness    Category = "liveness"
	CatError       Category = "error"
	CatEvents      Category = "events"
)

// TestCase defines a single ax-eval behavioral test.
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
	Evaluate     func(Result) Verdict  // deterministic check (always runs)
	JudgePrompt  string                // non-empty = run LLM-as-judge tier 2
	EngineOpts   map[string]string     // e.g. silence thresholds for liveness
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

// Verdict is the outcome of evaluating a Result.
type Verdict struct {
	Pass   bool
	Score  float64  // 0.0-1.0
	Reason string
	Events []string // event types observed
}
