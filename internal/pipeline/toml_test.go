package pipeline

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestPipelineConfigTOML(t *testing.T) {
	var cfg struct {
		Pipelines map[string]PipelineConfig `toml:"pipelines"`
	}

	input := `
[pipelines.plan-execute]
max_parallel = 4

[[pipelines.plan-execute.steps]]
role = "architect"
pass_output_as = "plan"

[[pipelines.plan-execute.steps]]
role = "heavy_lifter"
receives = "plan"
parallel = 2
worker_prompts = ["focus on auth", "focus on data layer"]
`

	if _, err := toml.Decode(input, &cfg); err != nil {
		t.Fatalf("toml.Decode() error = %v", err)
	}

	pipelineCfg, ok := cfg.Pipelines["plan-execute"]
	if !ok {
		t.Fatal("Pipelines[plan-execute] missing")
	}
	if pipelineCfg.MaxParallel != 4 {
		t.Fatalf("MaxParallel = %d, want %d", pipelineCfg.MaxParallel, 4)
	}
	if len(pipelineCfg.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want %d", len(pipelineCfg.Steps), 2)
	}
	if pipelineCfg.Steps[0].Role != "architect" {
		t.Fatalf("Steps[0].Role = %q, want %q", pipelineCfg.Steps[0].Role, "architect")
	}
	if pipelineCfg.Steps[1].Receives != "plan" {
		t.Fatalf("Steps[1].Receives = %q, want %q", pipelineCfg.Steps[1].Receives, "plan")
	}
	if pipelineCfg.Steps[1].Parallel != 2 {
		t.Fatalf("Steps[1].Parallel = %d, want %d", pipelineCfg.Steps[1].Parallel, 2)
	}
	if len(pipelineCfg.Steps[1].WorkerPrompts) != 2 {
		t.Fatalf("len(Steps[1].WorkerPrompts) = %d, want %d", len(pipelineCfg.Steps[1].WorkerPrompts), 2)
	}
}
