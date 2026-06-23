package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

const defaultAgyPrintTimeoutSec = 300
const agyPrintTimeoutBackstopGraceSec = 5

const uuidPatternText = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

var agyConversationPattern = regexp.MustCompile(`\b(?:Created conversation |Print mode: conversation=|Streaming conversation )(` + uuidPatternText + `)\b`)

type AgyAdapter struct{}

func (a *AgyAdapter) Binary() string {
	return "agy"
}

func (a *AgyAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	prompt := spec.Prompt
	if spec.SystemPrompt != "" {
		prompt = spec.SystemPrompt + "\n\n" + prompt
	}
	return a.buildPrintArgs(spec, "", prompt)
}

func (a *AgyAdapter) buildPrintArgs(spec *types.DispatchSpec, conversationID string, prompt string) []string {
	args := []string{"--sandbox"}

	timeoutSec := defaultAgyPrintTimeoutSec
	if spec.TimeoutSec > 0 {
		timeoutSec = spec.TimeoutSec
		if spec.GraceSec > 0 {
			timeoutSec += spec.GraceSec
		}
		timeoutSec += agyPrintTimeoutBackstopGraceSec
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
	if conversationID != "" {
		args = append(args, "--conversation", conversationID)
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
	return true
}

func (a *AgyAdapter) ResumeArgs(spec *types.DispatchSpec, sessionID string, message string) []string {
	return a.buildPrintArgs(spec, sessionID, message)
}

func (a *AgyAdapter) DiscoverSessionID(spec *types.DispatchSpec) (string, error) {
	if spec == nil || spec.ArtifactDir == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(spec.ArtifactDir, "agy.log"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return extractAgyConversationID(string(data)), nil
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

func extractAgyConversationID(logText string) string {
	sessionID := ""
	for _, match := range agyConversationPattern.FindAllStringSubmatch(logText, -1) {
		if len(match) > 1 {
			sessionID = match[1]
		}
	}
	return sessionID
}
