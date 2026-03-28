package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/buildoak/agent-mux/internal/types"
)

func TestExecutePipeline_Sequential(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)

	var (
		mu      sync.Mutex
		prompts []string
	)
	dispatch := func(_ context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		mu.Lock()
		prompts = append(prompts, spec.Prompt)
		mu.Unlock()

		response := fmt.Sprintf("response for step %d", spec.PipelineStep)
		if spec.PipelineStep == 0 {
			response = "plan output"
		}

		return &types.DispatchResult{
			Status:         types.StatusCompleted,
			DispatchID:     spec.DispatchID,
			Response:       response,
			HandoffSummary: response,
			DurationMS:     5,
		}
	}

	result, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "plan", PassOutputAs: "plan"},
			{Name: "execute", Receives: "plan"},
		},
	}, baseSpec, filepath.Join(tmp, "pipeline"), dispatch)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if got := len(result.Steps); got != 2 {
		t.Fatalf("steps = %d, want 2", got)
	}
	if result.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if got := len(prompts); got != 2 {
		t.Fatalf("prompts = %d, want 2", got)
	}
	if !strings.Contains(prompts[1], "=== Output from step \"plan\"") {
		t.Fatalf("step 2 prompt missing prior step handoff: %q", prompts[1])
	}
	if !strings.Contains(prompts[1], "Summary:\nplan output") {
		t.Fatalf("step 2 prompt missing prior step summary: %q", prompts[1])
	}
}

func TestExecutePipeline_FanOut(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)

	dispatch := func(_ context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		return &types.DispatchResult{
			Status:         types.StatusCompleted,
			DispatchID:     spec.DispatchID,
			Response:       "worker output",
			HandoffSummary: "worker summary",
			DurationMS:     5,
		}
	}

	result, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "fanout", Parallel: 3},
		},
	}, baseSpec, filepath.Join(tmp, "pipeline"), dispatch)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if got := len(result.Steps); got != 1 {
		t.Fatalf("steps = %d, want 1", got)
	}
	if got := len(result.Steps[0].Workers); got != 3 {
		t.Fatalf("workers = %d, want 3", got)
	}
}

func TestBuildWorkerSpecResetsPerDispatchTraceability(t *testing.T) {
	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)
	baseSpec.Salt = "shared-salt"
	baseSpec.TraceToken = "AGENT_MUX_GO_base-dispatch"

	spec := buildWorkerSpec(baseSpec, PipelineStep{Name: "plan"}, "pipeline-123", 0, filepath.Join(tmp, "worker-0"), "worker prompt")

	if spec.DispatchID == baseSpec.DispatchID {
		t.Fatalf("dispatch_id = %q, want a new worker dispatch ID", spec.DispatchID)
	}
	if spec.Salt != "" {
		t.Fatalf("salt = %q, want empty so worker dispatch derives its own salt", spec.Salt)
	}
	if spec.TraceToken != "" {
		t.Fatalf("trace_token = %q, want empty so worker dispatch derives its own trace token", spec.TraceToken)
	}
}

func TestBuildWorkerSpecAppliesResolvedRoleFields(t *testing.T) {
	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)
	baseSpec.SystemPrompt = "base prompt"
	baseSpec.Skills = []string{"cli-skill"}

	spec := buildWorkerSpec(baseSpec, PipelineStep{
		Name:                 "plan",
		Role:                 "lifter",
		Variant:              "spark",
		ResolvedSkills:       []string{"cli-skill", "role-skill"},
		ResolvedSystemPrompt: "role prompt\n\nbase prompt",
	}, "pipeline-123", 0, filepath.Join(tmp, "worker-0"), "worker prompt")

	if spec.Role != "lifter" {
		t.Fatalf("role = %q, want %q", spec.Role, "lifter")
	}
	if spec.Variant != "spark" {
		t.Fatalf("variant = %q, want %q", spec.Variant, "spark")
	}
	if got := spec.Skills; len(got) != 2 || got[0] != "cli-skill" || got[1] != "role-skill" {
		t.Fatalf("skills = %#v, want merged skills", got)
	}
	if spec.SystemPrompt != "role prompt\n\nbase prompt" {
		t.Fatalf("system_prompt = %q, want resolved layered prompt", spec.SystemPrompt)
	}
}

func TestExecutePipeline_PartialFailure(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)

	var mu sync.Mutex
	stepsSeen := map[int]int{}
	dispatch := func(_ context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		mu.Lock()
		stepsSeen[spec.PipelineStep]++
		mu.Unlock()

		if spec.PipelineStep == 0 && strings.HasSuffix(spec.ArtifactDir, "worker-1") {
			return &types.DispatchResult{
				Status:     types.StatusFailed,
				DispatchID: spec.DispatchID,
				Error: &types.DispatchError{
					Code:    "boom",
					Message: "worker failed",
				},
				DurationMS: 5,
			}
		}

		return &types.DispatchResult{
			Status:         types.StatusCompleted,
			DispatchID:     spec.DispatchID,
			Response:       "ok",
			HandoffSummary: "ok",
			DurationMS:     5,
		}
	}

	result, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "fanout", Parallel: 3, PassOutputAs: "fan"},
			{Name: "next", Receives: "fan"},
		},
	}, baseSpec, filepath.Join(tmp, "pipeline"), dispatch)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if result.Status != "partial" {
		t.Fatalf("status = %q, want partial", result.Status)
	}
	if result.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if result.Steps[0].Succeeded != 2 {
		t.Fatalf("step 0 succeeded = %d, want 2", result.Steps[0].Succeeded)
	}
	if result.Steps[0].Failed != 1 {
		t.Fatalf("step 0 failed = %d, want 1", result.Steps[0].Failed)
	}
	if got := len(result.Steps); got != 2 {
		t.Fatalf("steps = %d, want 2", got)
	}
	if stepsSeen[1] == 0 {
		t.Fatal("pipeline did not continue after partial failure")
	}
}

func TestExecutePipeline_AllFail(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)

	var mu sync.Mutex
	stepsSeen := map[int]int{}
	dispatch := func(_ context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		mu.Lock()
		stepsSeen[spec.PipelineStep]++
		mu.Unlock()

		return &types.DispatchResult{
			Status:     types.StatusFailed,
			DispatchID: spec.DispatchID,
			Error: &types.DispatchError{
				Code:    "boom",
				Message: "worker failed",
			},
			DurationMS: 5,
		}
	}

	result, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "fanout", Parallel: 3, PassOutputAs: "fan"},
			{Name: "next", Receives: "fan"},
		},
	}, baseSpec, filepath.Join(tmp, "pipeline"), dispatch)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if got := len(result.Steps); got != 1 {
		t.Fatalf("steps = %d, want 1", got)
	}
	if stepsSeen[1] != 0 {
		t.Fatalf("step 1 dispatches = %d, want 0", stepsSeen[1])
	}
}

func TestHandoffTemplates_SummaryAndRefs(t *testing.T) {
	t.Parallel()

	step := StepOutput{
		StepName:    "plan",
		HandoffMode: HandoffSummaryAndRefs,
		Workers: []WorkerResult{
			{
				WorkerIndex: 0,
				Status:      WorkerCompleted,
				Summary:     "brief summary",
				ArtifactDir: "/tmp/artifacts/worker-0",
				OutputFile:  "/tmp/artifacts/worker-0/output.md",
			},
		},
		TotalMS: 42,
	}

	got := renderHandoff(step)
	wantParts := []string{
		"=== Output from step \"plan\" (completed, 42ms) ===",
		"Summary:\nbrief summary",
		"Full output: /tmp/artifacts/worker-0/output.md",
		"Artifact directory: /tmp/artifacts/worker-0",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("handoff missing %q:\n%s", want, got)
		}
	}
}

func TestHandoffTemplates_RefsOnly(t *testing.T) {
	t.Parallel()

	step := StepOutput{
		StepName:    "plan",
		HandoffMode: HandoffRefsOnly,
		Workers: []WorkerResult{
			{
				WorkerIndex: 0,
				Status:      WorkerCompleted,
				Summary:     "brief summary",
				ArtifactDir: "/tmp/artifacts/worker-0",
				OutputFile:  "/tmp/artifacts/worker-0/output.md",
			},
		},
		TotalMS: 42,
	}

	got := renderHandoff(step)
	if strings.Contains(got, "Summary:") {
		t.Fatalf("refs_only handoff should not include summary:\n%s", got)
	}
	if !strings.Contains(got, "Full output: /tmp/artifacts/worker-0/output.md") {
		t.Fatalf("handoff missing output file ref:\n%s", got)
	}
	if !strings.Contains(got, "Artifact directory: /tmp/artifacts/worker-0") {
		t.Fatalf("handoff missing artifact dir ref:\n%s", got)
	}
}

func TestPipelineIDShared(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)

	var (
		mu          sync.Mutex
		pipelineIDs []string
	)
	dispatch := func(_ context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		mu.Lock()
		pipelineIDs = append(pipelineIDs, spec.PipelineID)
		mu.Unlock()
		return &types.DispatchResult{
			Status:         types.StatusCompleted,
			DispatchID:     spec.DispatchID,
			Response:       "ok",
			HandoffSummary: "ok",
			DurationMS:     5,
		}
	}

	result, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "fanout", Parallel: 2},
			{Name: "next"},
		},
	}, baseSpec, filepath.Join(tmp, "pipeline"), dispatch)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	if result.PipelineID == "" {
		t.Fatal("pipeline id should be set")
	}
	for i, id := range pipelineIDs {
		if id != result.PipelineID {
			t.Fatalf("pipelineIDs[%d] = %q, want %q", i, id, result.PipelineID)
		}
	}
	for i, step := range result.Steps {
		if step.PipelineID != result.PipelineID {
			t.Fatalf("steps[%d].pipeline_id = %q, want %q", i, step.PipelineID, result.PipelineID)
		}
	}
}

func TestArtifactDirs(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)

	var (
		mu   sync.Mutex
		dirs []string
	)
	dispatch := func(_ context.Context, spec *types.DispatchSpec) *types.DispatchResult {
		mu.Lock()
		dirs = append(dirs, spec.ArtifactDir)
		mu.Unlock()
		return &types.DispatchResult{
			Status:         types.StatusCompleted,
			DispatchID:     spec.DispatchID,
			Response:       "ok",
			HandoffSummary: "ok",
			DurationMS:     5,
		}
	}

	pipelineDir := filepath.Join(tmp, "pipeline")
	_, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "fanout", Parallel: 2},
			{Name: "next"},
		},
	}, baseSpec, pipelineDir, dispatch)
	if err != nil {
		t.Fatalf("ExecutePipeline: %v", err)
	}

	want := []string{
		filepath.Join(pipelineDir, "step-0", "worker-0"),
		filepath.Join(pipelineDir, "step-0", "worker-1"),
		filepath.Join(pipelineDir, "step-1", "worker-0"),
	}
	if len(dirs) != len(want) {
		t.Fatalf("artifact dir count = %d, want %d (%v)", len(dirs), len(want), dirs)
	}
	gotSet := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		gotSet[dir] = true
	}
	for _, dir := range want {
		if !gotSet[dir] {
			t.Fatalf("missing expected artifact dir %q in %v", dir, dirs)
		}
	}
}

func TestExecutePipeline_ArtifactDirFailureUsesPipelineEnvelope(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	baseSpec := testBaseSpec(tmp)
	pipelineDir := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(pipelineDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := ExecutePipeline(context.Background(), PipelineConfig{
		Steps: []PipelineStep{
			{Name: "review"},
		},
	}, baseSpec, pipelineDir, func(_ context.Context, _ *types.DispatchSpec) *types.DispatchResult {
		t.Fatal("dispatch should not run when pipeline artifact directory setup fails")
		return nil
	})
	if err == nil {
		t.Fatal("ExecutePipeline error = nil, want setup failure")
	}
	if result == nil {
		t.Fatal("result = nil, want failed pipeline envelope")
	}
	if result.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil {
		t.Fatal("error = nil, want pipeline setup error")
	}
	if result.Error.Code != "artifact_dir_unwritable" {
		t.Fatalf("error.code = %q, want artifact_dir_unwritable", result.Error.Code)
	}
	if len(result.Steps) != 0 {
		t.Fatalf("len(steps) = %d, want 0", len(result.Steps))
	}
}

func testBaseSpec(tmp string) *types.DispatchSpec {
	return &types.DispatchSpec{
		DispatchID:       "base-dispatch",
		Engine:           "codex",
		Model:            "gpt-5.4",
		Effort:           "high",
		Prompt:           "user prompt",
		Cwd:              tmp,
		ArtifactDir:      filepath.Join(tmp, "base"),
		ResponseMaxChars: 2000,
		HandoffMode:      string(HandoffSummaryAndRefs),
	}
}
