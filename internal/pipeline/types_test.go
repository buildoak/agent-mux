package pipeline

import (
	"strings"
	"testing"
)

func TestValidatePipeline_Valid(t *testing.T) {
	cfg := PipelineConfig{
		Steps: []PipelineStep{
			{Role: "architect", PassOutputAs: "plan"},
			{Role: "heavy_lifter", Receives: "plan", PassOutputAs: "implementation"},
			{Role: "auditor", Receives: "implementation", PassOutputAs: "audit"},
		},
	}

	if err := ValidatePipeline(cfg); err != nil {
		t.Fatalf("ValidatePipeline() error = %v, want nil", err)
	}
}

func TestValidatePipeline_EmptySteps(t *testing.T) {
	err := ValidatePipeline(PipelineConfig{})
	if err == nil {
		t.Fatal("ValidatePipeline() error = nil, want error")
	}
}

func TestValidatePipeline_InvalidReceives(t *testing.T) {
	cfg := PipelineConfig{
		Steps: []PipelineStep{
			{Role: "architect", PassOutputAs: "plan"},
			{Role: "heavy_lifter", Receives: "missing", PassOutputAs: "implementation"},
		},
	}

	err := ValidatePipeline(cfg)
	if err == nil {
		t.Fatal("ValidatePipeline() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ValidatePipeline() error = %q, want contains %q", err.Error(), "not found")
	}
}

func TestValidatePipeline_DuplicatePassOutputAs(t *testing.T) {
	cfg := PipelineConfig{
		Steps: []PipelineStep{
			{Role: "architect", PassOutputAs: "plan"},
			{Role: "heavy_lifter", PassOutputAs: "plan"},
		},
	}

	err := ValidatePipeline(cfg)
	if err == nil {
		t.Fatal("ValidatePipeline() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ValidatePipeline() error = %q, want contains %q", err.Error(), "duplicate")
	}
}

func TestValidatePipeline_WorkerPromptsMismatch(t *testing.T) {
	cfg := PipelineConfig{
		Steps: []PipelineStep{
			{
				Role:          "heavy_lifter",
				Parallel:      3,
				WorkerPrompts: []string{"focus on auth", "focus on data"},
			},
		},
	}

	err := ValidatePipeline(cfg)
	if err == nil {
		t.Fatal("ValidatePipeline() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "worker_prompts") {
		t.Fatalf("ValidatePipeline() error = %q, want contains %q", err.Error(), "worker_prompts")
	}
}

func TestValidatePipeline_ForwardReceives(t *testing.T) {
	cfg := PipelineConfig{
		Steps: []PipelineStep{
			{Role: "architect", Receives: "implementation", PassOutputAs: "plan"},
			{Role: "heavy_lifter", PassOutputAs: "implementation"},
		},
	}

	err := ValidatePipeline(cfg)
	if err == nil {
		t.Fatal("ValidatePipeline() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ValidatePipeline() error = %q, want contains %q", err.Error(), "not found")
	}
}
