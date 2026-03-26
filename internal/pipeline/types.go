package pipeline

import "fmt"

// HandoffMode controls how step output is passed to the next step.
type HandoffMode string

const (
	HandoffSummaryAndRefs HandoffMode = "summary_and_refs"
	HandoffFullConcat     HandoffMode = "full_concat"
	HandoffRefsOnly       HandoffMode = "refs_only"
)

// WorkerStatus represents the outcome of a single worker in a pipeline step.
type WorkerStatus string

const (
	WorkerCompleted WorkerStatus = "completed"
	WorkerTimedOut  WorkerStatus = "timed_out"
	WorkerFailed    WorkerStatus = "failed"
)

// PipelineStep configures one step in a pipeline.
type PipelineStep struct {
	Name          string   `toml:"name"`
	Role          string   `toml:"role"`
	Engine        string   `toml:"engine"`
	Model         string   `toml:"model"`
	Effort        string   `toml:"effort"`
	Timeout       int      `toml:"timeout"`
	Parallel      int      `toml:"parallel"`       // 0 or 1 = sequential
	WorkerPrompts []string `toml:"worker_prompts"` // per-worker focus dirs for fan-out
	Receives      string   `toml:"receives"`       // name of prior step's pass_output_as
	PassOutputAs  string   `toml:"pass_output_as"` // name for this step's output
	HandoffMode   string   `toml:"handoff_mode"`   // "summary_and_refs" | "full_concat" | "refs_only"
}

// PipelineConfig is the full config for a named pipeline.
type PipelineConfig struct {
	MaxParallel int            `toml:"max_parallel"` // default 8
	Steps       []PipelineStep `toml:"steps"`
}

// WorkerResult is the result from one worker in a fan-out step.
type WorkerResult struct {
	WorkerIndex int          `json:"worker_index"`
	DispatchID  string       `json:"dispatch_id"`
	Status      WorkerStatus `json:"status"`
	Summary     string       `json:"summary"`
	ArtifactDir string       `json:"artifact_dir"`
	OutputFile  string       `json:"output_file,omitempty"`
	ErrorCode   string       `json:"error_code,omitempty"`
	ErrorMsg    string       `json:"error_msg,omitempty"`
	DurationMS  int64        `json:"duration_ms"`
}

// StepOutput collects all worker results from a single pipeline step.
type StepOutput struct {
	StepName    string         `json:"step_name"`
	StepIndex   int            `json:"step_index"`
	PipelineID  string         `json:"pipeline_id"`
	HandoffMode HandoffMode    `json:"handoff_mode"`
	Workers     []WorkerResult `json:"workers"`
	HandoffText string         `json:"handoff_text"`
	Succeeded   int            `json:"succeeded"`
	Failed      int            `json:"failed"`
	TotalMS     int64          `json:"total_ms"`
}

// PipelineResult is returned by ExecutePipeline.
type PipelineResult struct {
	PipelineID string       `json:"pipeline_id"`
	Status     string       `json:"status"` // "completed" | "partial" | "failed"
	Steps      []StepOutput `json:"steps"`
	FinalStep  *StepOutput  `json:"final_step,omitempty"`
	DurationMS int64        `json:"duration_ms"`
}

// ValidatePipeline checks pipeline config for correctness.
// Returns error describing the first violation found.
func ValidatePipeline(cfg PipelineConfig) error {
	if len(cfg.Steps) == 0 {
		return fmt.Errorf("steps: at least one step is required")
	}

	available := make(map[string]int, len(cfg.Steps))
	for i, step := range cfg.Steps {
		parallel := step.Parallel

		if parallel < 0 {
			return fmt.Errorf("step[%d].parallel: must be >= 1 when set", i)
		}
		if parallel == 0 {
			parallel = 1
		}

		if parallel > 1 && len(step.WorkerPrompts) > 0 && len(step.WorkerPrompts) != parallel {
			return fmt.Errorf("step[%d].worker_prompts: expected %d entries to match parallel, got %d", i, parallel, len(step.WorkerPrompts))
		}

		if step.Receives != "" {
			if _, ok := available[step.Receives]; !ok {
				return fmt.Errorf("step[%d].receives: %q not found in preceding steps' pass_output_as", i, step.Receives)
			}
		}

		if step.PassOutputAs != "" {
			if prev, exists := available[step.PassOutputAs]; exists {
				return fmt.Errorf("step[%d].pass_output_as: duplicate %q already defined by step[%d]", i, step.PassOutputAs, prev)
			}
			available[step.PassOutputAs] = i
		}
	}

	return nil
}
