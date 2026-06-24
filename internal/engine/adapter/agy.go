package adapter

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

const defaultAgyPrintTimeoutSec = 300
const agyPrintTimeoutBackstopGraceSec = 5

const uuidPatternText = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`
const agyPrivateDiagnosticMaxBytes int64 = 1024 * 1024
const agyProviderOverloadedText = "model api is currently overloaded"

var agyConversationPattern = regexp.MustCompile(`\b(?:Created conversation |Print mode: conversation=|Streaming conversation )(` + uuidPatternText + `)\b`)
var agyConversationIDExactPattern = regexp.MustCompile(`^` + uuidPatternText + `$`)

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

func (a *AgyAdapter) DiagnoseFailure(ctx types.AdapterFailureDiagnosticContext) *types.AdapterFailureDiagnosis {
	if ctx.Spec == nil {
		return nil
	}
	conversationID, err := a.DiscoverSessionID(ctx.Spec)
	if err != nil || conversationID == "" {
		return nil
	}
	for _, path := range agyPrivateDiagnosticPaths(conversationID) {
		if agyDiagnosticFileHasProviderRateLimit(path) {
			return &types.AdapterFailureDiagnosis{Code: "provider_rate_limited"}
		}
	}
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

func extractAgyConversationID(logText string) string {
	sessionID := ""
	for _, match := range agyConversationPattern.FindAllStringSubmatch(logText, -1) {
		if len(match) > 1 {
			sessionID = match[1]
		}
	}
	return sessionID
}

func agyPrivateDiagnosticPaths(conversationID string) []string {
	if !agyConversationIDExactPattern.MatchString(conversationID) {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}

	logsDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", conversationID, ".system_generated", "logs")
	paths := []string{filepath.Join(logsDir, "transcript_full.jsonl")}

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return paths
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "transcript_full.jsonl" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "transcript") ||
			strings.Contains(lower, "error") ||
			strings.Contains(lower, "diagnostic") ||
			strings.HasSuffix(lower, ".jsonl") ||
			strings.HasSuffix(lower, ".log") {
			paths = append(paths, filepath.Join(logsDir, name))
		}
	}
	sort.Strings(paths[1:])
	return paths
}

func agyDiagnosticFileHasProviderRateLimit(path string) bool {
	data, err := readTail(path, agyPrivateDiagnosticMaxBytes)
	if err != nil || len(data) == 0 {
		return false
	}
	text := string(data)
	for _, line := range strings.Split(text, "\n") {
		if agyDiagnosticLineHasProviderRateLimit(line) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(text), agyProviderOverloadedText)
}

func agyDiagnosticLineHasProviderRateLimit(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return false
	}
	if !strings.EqualFold(stringField(obj, "source"), "SYSTEM") || !strings.EqualFold(stringField(obj, "type"), "ERROR_MESSAGE") {
		return false
	}
	if jsonValueIs429(obj["error_code"]) || jsonValueIs429(obj["status_code"]) {
		return true
	}
	if nested, ok := obj["error"].(map[string]any); ok {
		return jsonValueIs429(nested["error_code"]) || jsonValueIs429(nested["status_code"])
	}
	return false
}

func stringField(obj map[string]any, key string) string {
	if value, ok := obj[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func jsonValueIs429(value any) bool {
	switch v := value.(type) {
	case float64:
		return v == 429
	case string:
		return strings.TrimSpace(v) == "429"
	case json.Number:
		return strings.TrimSpace(v.String()) == "429"
	default:
		return false
	}
}

func readTail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset := int64(0)
	if size := stat.Size(); size > maxBytes {
		offset = size - maxBytes
	}
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(file, maxBytes))
}
