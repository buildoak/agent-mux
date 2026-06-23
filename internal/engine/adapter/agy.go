package adapter

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

const defaultAgyPrintTimeoutSec = 300

type AgyAdapter struct{}

func (a *AgyAdapter) Binary() string {
	return "agy"
}

func (a *AgyAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	args := []string{"--sandbox"}

	timeoutSec := defaultAgyPrintTimeoutSec
	if spec.TimeoutSec > 0 {
		timeoutSec = spec.TimeoutSec
	}
	args = append(args, "--print-timeout", strconv.Itoa(timeoutSec)+"s")

	if spec.ArtifactDir != "" {
		args = append(args, "--log-file", filepath.Join(spec.ArtifactDir, "agy.log"))
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	for _, dir := range addDirs(spec) {
		args = append(args, "--add-dir", dir)
	}

	prompt := spec.Prompt
	if spec.SystemPrompt != "" {
		prompt = spec.SystemPrompt + "\n\n" + prompt
	}
	args = append(args, "-p", prompt)

	return args
}

func (a *AgyAdapter) EnvVars(spec *types.DispatchSpec) ([]string, error) {
	return nil, nil
}

func (a *AgyAdapter) ParseEvent(line string) (*types.HarnessEvent, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	return &types.HarnessEvent{
		Kind:      types.EventRawPassthrough,
		Timestamp: time.Now(),
		Raw:       []byte(line),
	}, nil
}

func (a *AgyAdapter) SupportsResume() bool {
	return false
}

func (a *AgyAdapter) ResumeArgs(_ *types.DispatchSpec, _ string, _ string) []string {
	return nil
}

func (a *AgyAdapter) RuntimePolicy() types.AdapterRuntimePolicy {
	return types.AdapterRuntimePolicy{
		StdinMode:               types.AdapterStdinEOF,
		OutputMode:              types.AdapterOutputPlainStdout,
		RequireNonEmptyResponse: true,
		SoftTimeoutWrapupMode:   types.AdapterSoftTimeoutNoWrapup,
		FailureContextMode:      types.AdapterFailureContextPrivateDiagnostics,
	}
}
