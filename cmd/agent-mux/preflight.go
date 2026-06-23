package main

import (
	"fmt"
	"slices"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/types"
)

func preflightDispatchSpec(spec *types.DispatchSpec) *types.DispatchError {
	if spec == nil {
		return dispatch.NewDispatchError("invalid_args", "Missing dispatch spec.", "")
	}

	reg := adapter.NewRegistry(config.ModelsWithCachedAgy())
	adp, err := reg.Get(spec.Engine)
	if err != nil {
		return dispatch.NewDispatchError("engine_not_found", fmt.Sprintf("Engine %q not found.", spec.Engine), "Valid engines: "+validEngineBracketed())
	}

	validModels := reg.ValidModels(spec.Engine)
	if spec.Model != "" && len(validModels) > 0 && !slices.Contains(validModels, spec.Model) {
		suggestion := dispatch.FuzzyMatchModel(spec.Model, validModels)
		suggestionText := fmt.Sprintf("Valid models for %s: %v", spec.Engine, validModels)
		if suggestion != "" {
			suggestionText = fmt.Sprintf("Did you mean %q? %s", suggestion, suggestionText)
		}
		return dispatch.NewDispatchError("model_not_found", fmt.Sprintf("Model %q not found for engine %s.", spec.Model, spec.Engine), suggestionText)
	}

	switch adp.(type) {
	case *adapter.CodexAdapter:
		if badVal, valid := adapter.ValidateCodexSandbox(spec); !valid {
			return dispatch.NewDispatchError(
				"invalid_args",
				fmt.Sprintf("Invalid sandbox value %q for codex engine.", badVal),
				"Valid sandbox values: danger-full-access, workspace-write, read-only. Example: agent-mux -E codex --sandbox workspace-write --cwd /repo \"<prompt>\".",
			)
		}
	case *adapter.GeminiAdapter:
		if mode, _ := spec.EngineOpts["permission-mode"].(string); mode != "" {
			if err := adapter.ValidateGeminiApprovalMode(mode); err != nil {
				return dispatch.NewDispatchError("invalid_args", err.Error(), "Use one of: default, auto_edit, yolo, plan.")
			}
		}
	case *adapter.AgyAdapter:
		if err := preflightAgySpec(spec); err != nil {
			return err
		}
	}

	return nil
}

func preflightAgySpec(spec *types.DispatchSpec) *types.DispatchError {
	for _, key := range []string{"sandbox", "permission-mode", "reasoning", "max-turns", "full_access"} {
		if !optionSourceIsExplicit(spec, key) {
			continue
		}
		return dispatch.NewDispatchError(
			"invalid_args",
			fmt.Sprintf("Option %q is not supported by agy dispatches.", key),
			"Use agy without portable sandbox, permission, reasoning, max-turns, or full-access options; agent-mux always invokes agy with the local CLI --sandbox flag.",
		)
	}
	return nil
}
