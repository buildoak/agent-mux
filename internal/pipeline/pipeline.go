package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
	"github.com/oklog/ulid/v2"
)

// DispatchFunc dispatches a single worker spec.
type DispatchFunc func(context.Context, *types.DispatchSpec) *types.DispatchResult

// ExecutePipeline runs all steps in a pipeline sequentially.
// Fan-out steps run workers concurrently, capped by cfg.MaxParallel.
// Completed step outputs are preserved even when later steps fail.
func ExecutePipeline(
	ctx context.Context,
	cfg PipelineConfig,
	baseSpec *types.DispatchSpec,
	pipelineArtifactDir string,
	dispatch DispatchFunc,
) (*PipelineResult, error) {
	start := time.Now()
	pipelineID := ulid.Make().String()

	maxParallel := cfg.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 8
	}
	sem := make(chan struct{}, maxParallel)

	stepOutputs := make(map[string]*StepOutput)
	allSteps := make([]StepOutput, 0, len(cfg.Steps))
	pipelineStatus := "completed"

	for i, step := range cfg.Steps {
		stepStart := time.Now()
		parallel := step.Parallel
		if parallel <= 0 {
			parallel = 1
		}

		prompt := buildStepPrompt(baseSpec.Prompt, step, stepOutputs, i)
		stepArtifactDir := filepath.Join(pipelineArtifactDir, fmt.Sprintf("step-%d", i))
		if err := os.MkdirAll(stepArtifactDir, 0o755); err != nil {
			return partialResult(pipelineID, allSteps, start, "failed"), fmt.Errorf("create step artifact dir: %w", err)
		}

		workerSpecs := make([]*types.DispatchSpec, parallel)
		for w := 0; w < parallel; w++ {
			workerArtifactDir := filepath.Join(stepArtifactDir, fmt.Sprintf("worker-%d", w))
			if err := os.MkdirAll(workerArtifactDir, 0o755); err != nil {
				return partialResult(pipelineID, allSteps, start, "failed"), fmt.Errorf("create worker artifact dir: %w", err)
			}

			spec := buildWorkerSpec(baseSpec, step, pipelineID, i, w, workerArtifactDir, prompt)
			if parallel > 1 && w < len(step.WorkerPrompts) && strings.TrimSpace(step.WorkerPrompts[w]) != "" {
				spec.Prompt += "\n\n" + step.WorkerPrompts[w]
			}
			workerSpecs[w] = spec
		}

		var workers []WorkerResult
		if parallel == 1 {
			result := dispatch(ctx, workerSpecs[0])
			workers = []WorkerResult{resultToWorkerResult(result, 0, workerSpecs[0])}
		} else {
			workers = fanOut(ctx, workerSpecs, dispatch, sem)
		}

		handoffMode := HandoffMode(step.HandoffMode)
		if handoffMode == "" {
			handoffMode = HandoffSummaryAndRefs
		}

		succeeded := 0
		failed := 0
		for _, worker := range workers {
			if worker.Status == WorkerCompleted || worker.Status == WorkerTimedOut {
				succeeded++
			} else {
				failed++
			}
		}

		stepOut := StepOutput{
			StepName:    step.Name,
			StepIndex:   i,
			PipelineID:  pipelineID,
			HandoffMode: handoffMode,
			Workers:     workers,
			Succeeded:   succeeded,
			Failed:      failed,
			TotalMS:     time.Since(stepStart).Milliseconds(),
		}
		stepOut.HandoffText = renderHandoff(stepOut)

		allSteps = append(allSteps, stepOut)
		if step.PassOutputAs != "" {
			stepOutputs[step.PassOutputAs] = &allSteps[len(allSteps)-1]
		}

		if succeeded == 0 && failed > 0 {
			pipelineStatus = "failed"
			break
		}
		if failed > 0 {
			pipelineStatus = "partial"
		}
	}

	return &PipelineResult{
		PipelineID: pipelineID,
		Status:     pipelineStatus,
		Steps:      allSteps,
		FinalStep:  lastStep(allSteps),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func fanOut(ctx context.Context, specs []*types.DispatchSpec, dispatch DispatchFunc, sem chan struct{}) []WorkerResult {
	results := make([]WorkerResult, len(specs))
	var wg sync.WaitGroup

	for i, spec := range specs {
		wg.Add(1)
		go func(idx int, s *types.DispatchSpec) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[idx] = WorkerResult{
					WorkerIndex: idx,
					Status:      WorkerFailed,
					ArtifactDir: s.ArtifactDir,
					ErrorCode:   "interrupted",
					ErrorMsg:    "context cancelled",
				}
				return
			}
			defer func() { <-sem }()

			result := dispatch(ctx, s)
			results[idx] = resultToWorkerResult(result, idx, s)
		}(i, spec)
	}

	wg.Wait()
	return results
}

func buildWorkerSpec(base *types.DispatchSpec, step PipelineStep, pipelineID string, stepIdx, workerIdx int, artifactDir, prompt string) *types.DispatchSpec {
	spec := *base
	spec.DispatchID = ulid.Make().String()
	spec.Salt = ""
	spec.TraceToken = ""
	spec.ArtifactDir = artifactDir
	spec.PipelineID = pipelineID
	spec.PipelineStep = stepIdx
	spec.ParentDispatchID = base.DispatchID
	spec.Prompt = prompt
	spec.Receives = step.Receives
	spec.PassOutputAs = step.PassOutputAs
	spec.Parallel = step.Parallel
	spec.HandoffMode = step.HandoffMode
	if step.Role != "" {
		spec.Role = step.Role
	}
	if step.Variant != "" {
		spec.Variant = step.Variant
	}
	if step.Engine != "" {
		spec.Engine = step.Engine
	}
	if step.Model != "" {
		spec.Model = step.Model
	}
	if step.Effort != "" {
		spec.Effort = step.Effort
	}
	if step.Timeout > 0 {
		spec.TimeoutSec = step.Timeout
	}
	if len(step.ResolvedSkills) > 0 {
		spec.Skills = append([]string(nil), step.ResolvedSkills...)
	}
	if step.ResolvedSystemPrompt != "" {
		spec.SystemPrompt = step.ResolvedSystemPrompt
	}
	if base.EngineOpts != nil {
		spec.EngineOpts = make(map[string]any, len(base.EngineOpts))
		for k, v := range base.EngineOpts {
			spec.EngineOpts[k] = v
		}
	}
	_ = workerIdx
	return &spec
}

func resultToWorkerResult(r *types.DispatchResult, idx int, spec *types.DispatchSpec) WorkerResult {
	if r == nil {
		return WorkerResult{
			WorkerIndex: idx,
			Status:      WorkerFailed,
			ArtifactDir: spec.ArtifactDir,
			ErrorCode:   "dispatch_nil",
			ErrorMsg:    "dispatch returned nil result",
		}
	}

	wr := WorkerResult{
		WorkerIndex: idx,
		DispatchID:  r.DispatchID,
		ArtifactDir: spec.ArtifactDir,
		DurationMS:  r.DurationMS,
	}

	switch r.Status {
	case types.StatusCompleted:
		wr.Status = WorkerCompleted
		wr.Summary = truncateSummary(r.HandoffSummary)
	case types.StatusTimedOut:
		wr.Status = WorkerTimedOut
		wr.Summary = truncateSummary(r.HandoffSummary)
	default:
		wr.Status = WorkerFailed
		if r.Error != nil {
			wr.ErrorCode = r.Error.Code
			wr.ErrorMsg = r.Error.Message
		}
	}

	if wr.Status != WorkerFailed && r.Response != "" {
		outputFile := filepath.Join(spec.ArtifactDir, "output.md")
		if err := os.WriteFile(outputFile, []byte(r.Response), 0o644); err == nil {
			wr.OutputFile = outputFile
		}
	}

	return wr
}

func renderHandoff(step StepOutput) string {
	if len(step.Workers) == 0 {
		return ""
	}

	if len(step.Workers) == 1 {
		return renderSequentialHandoff(step, step.Workers[0])
	}
	return renderFanOutHandoff(step)
}

func renderSequentialHandoff(step StepOutput, worker WorkerResult) string {
	header := fmt.Sprintf("=== Output from step %q (%s, %dms) ===", step.StepName, worker.Status, step.TotalMS)

	switch step.HandoffMode {
	case HandoffFullConcat:
		body := renderWorkerFull(worker)
		if body == "" {
			body = renderWorkerFailure(worker)
		}
		return strings.TrimSpace(header + "\n\n" + body)
	case HandoffRefsOnly:
		return strings.TrimSpace(header + "\n\n" + renderWorkerRefs(worker))
	default:
		if worker.Status == WorkerFailed {
			return strings.TrimSpace(header + "\n\n" + renderWorkerFailure(worker))
		}
		return strings.TrimSpace(header + "\n\nSummary:\n" + worker.Summary + "\n\nFull output: " + worker.OutputFile + "\nArtifact directory: " + worker.ArtifactDir)
	}
}

func renderFanOutHandoff(step StepOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== Output from step %q (%d succeeded, %d failed, %dms) ===\n\n", step.StepName, step.Succeeded, step.Failed, step.TotalMS)

	for i, worker := range step.Workers {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "--- Worker %d (%s, %dms) ---\n", worker.WorkerIndex, worker.Status, worker.DurationMS)

		switch step.HandoffMode {
		case HandoffFullConcat:
			body := renderWorkerFull(worker)
			if body == "" {
				body = renderWorkerFailure(worker)
			}
			b.WriteString(body)
		case HandoffRefsOnly:
			b.WriteString(renderWorkerRefs(worker))
		default:
			if worker.Status == WorkerFailed {
				b.WriteString(renderWorkerFailure(worker))
			} else if worker.Status == WorkerTimedOut {
				b.WriteString("Summary: ")
				b.WriteString(worker.Summary)
				b.WriteString("\n")
				b.WriteString("Full output: ")
				b.WriteString(worker.OutputFile)
				b.WriteString("\n")
				b.WriteString("Artifact directory: ")
				b.WriteString(worker.ArtifactDir)
			} else {
				b.WriteString("Summary: ")
				b.WriteString(worker.Summary)
				b.WriteString("\n")
				b.WriteString("Full output: ")
				b.WriteString(worker.OutputFile)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func renderWorkerFull(worker WorkerResult) string {
	if worker.Status == WorkerFailed {
		return renderWorkerFailure(worker)
	}
	if worker.OutputFile == "" {
		return renderWorkerRefs(worker)
	}
	data, err := os.ReadFile(worker.OutputFile)
	if err != nil {
		return renderWorkerRefs(worker)
	}
	return string(data)
}

func renderWorkerRefs(worker WorkerResult) string {
	parts := make([]string, 0, 2)
	if worker.OutputFile != "" {
		parts = append(parts, "Full output: "+worker.OutputFile)
	}
	parts = append(parts, "Artifact directory: "+worker.ArtifactDir)
	return strings.Join(parts, "\n")
}

func renderWorkerFailure(worker WorkerResult) string {
	code := worker.ErrorCode
	msg := worker.ErrorMsg
	if code == "" {
		code = "worker_failed"
	}
	if msg == "" {
		msg = "worker failed"
	}
	return fmt.Sprintf("Error: %s — %s\nPartial artifacts: %s", code, msg, worker.ArtifactDir)
}

func buildStepPrompt(userPrompt string, step PipelineStep, stepOutputs map[string]*StepOutput, stepIdx int) string {
	var parts []string
	if step.Receives != "" {
		if prior, ok := stepOutputs[step.Receives]; ok {
			parts = append(parts, prior.HandoffText)
		}
	}
	parts = append(parts, userPrompt)
	if step.PassOutputAs != "" {
		parts = append(parts, fmt.Sprintf("Please write your output so it can be identified as %q for downstream pipeline steps.", step.PassOutputAs))
	}
	_ = stepIdx
	return strings.Join(parts, "\n\n")
}

func truncateSummary(summary string) string {
	const maxChars = 2000

	summary = strings.TrimSpace(summary)
	if len(summary) <= maxChars {
		return summary
	}

	truncated := summary[:maxChars]
	if idx := strings.LastIndexAny(truncated, ".!?\n"); idx > maxChars/2 {
		return strings.TrimSpace(truncated[:idx+1])
	}
	return strings.TrimSpace(truncated)
}

func lastStep(steps []StepOutput) *StepOutput {
	if len(steps) == 0 {
		return nil
	}
	return &steps[len(steps)-1]
}

func partialResult(pipelineID string, steps []StepOutput, start time.Time, status string) *PipelineResult {
	return &PipelineResult{
		PipelineID: pipelineID,
		Status:     status,
		Steps:      steps,
		FinalStep:  lastStep(steps),
		DurationMS: time.Since(start).Milliseconds(),
	}
}
