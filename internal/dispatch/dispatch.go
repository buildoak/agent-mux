package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/buildoak/agent-mux/internal/types"
)

var (
	adjectives = []string{
		"amber", "blue", "coral", "dark", "eager", "fair", "gold",
		"hot", "icy", "jade", "keen", "lime", "mint", "navy",
		"oak", "pale", "quick", "red", "sage", "teal",
	}
	nouns = []string{
		"ant", "bear", "cat", "deer", "elk", "fox", "goat",
		"hawk", "ibis", "jay", "koi", "lark", "moth", "newt",
		"owl", "pike", "quail", "ray", "swan", "toad",
	}
	digits = []string{
		"one", "two", "three", "four", "five",
		"six", "seven", "eight", "nine", "zero",
	}
	saltRand = rand.New(rand.NewSource(time.Now().UnixNano()))
	saltMu   sync.Mutex
)

func GenerateSalt() string {
	saltMu.Lock()
	defer saltMu.Unlock()
	return fmt.Sprintf("%s-%s-%s",
		adjectives[saltRand.Intn(len(adjectives))],
		nouns[saltRand.Intn(len(nouns))],
		digits[saltRand.Intn(len(digits))],
	)
}

func DefaultTraceToken(dispatchID string) string {
	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return ""
	}
	return "AGENT_MUX_GO_" + dispatchID
}

func EnsureTraceability(spec *types.DispatchSpec) {
	if spec == nil {
		return
	}
	spec.Salt = strings.TrimSpace(spec.Salt)
	if spec.Salt == "" {
		spec.Salt = GenerateSalt()
	}
	spec.TraceToken = strings.TrimSpace(spec.TraceToken)
	if spec.TraceToken == "" {
		spec.TraceToken = DefaultTraceToken(spec.DispatchID)
	}
}

func EnsureArtifactDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

type DispatchMeta struct {
	DispatchID          string   `json:"dispatch_id"`
	DispatchSalt        string   `json:"dispatch_salt"`
	TraceToken          string   `json:"trace_token,omitempty"`
	SessionID           string   `json:"session_id,omitempty"`
	StartedAt           string   `json:"started_at"`
	Engine              string   `json:"engine"`
	Model               string   `json:"model"`
	Role                string   `json:"role,omitempty"`
	PromptHash          string   `json:"prompt_hash"`
	Cwd                 string   `json:"cwd"`
	ContinuesDispatchID *string  `json:"continues_dispatch_id"`
	EndedAt             string   `json:"ended_at,omitempty"`
	Status              string   `json:"status,omitempty"`
	Artifacts           []string `json:"artifacts,omitempty"`
}

func WriteDispatchMeta(artifactDir string, spec *types.DispatchSpec) error {
	EnsureTraceability(spec)
	hash := sha256.Sum256([]byte(spec.Prompt))
	meta := DispatchMeta{
		DispatchID:   spec.DispatchID,
		DispatchSalt: spec.Salt,
		TraceToken:   spec.TraceToken,
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		Engine:       spec.Engine,
		Model:        spec.Model,
		Role:         spec.Role,
		PromptHash:   fmt.Sprintf("sha256:%x", hash[:8]),
		Cwd:          spec.Cwd,
	}
	if spec.ContinuesDispatchID != "" {
		meta.ContinuesDispatchID = &spec.ContinuesDispatchID
	}

	return writeMetaFile(filepath.Join(artifactDir, "_dispatch_meta.json"), &meta)
}
func UpdateDispatchMeta(artifactDir string, status string, artifacts []string) error {
	path := filepath.Join(artifactDir, "_dispatch_meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}

	var meta DispatchMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("unmarshal meta: %w", err)
	}

	meta.EndedAt = time.Now().UTC().Format(time.RFC3339)
	meta.Status = status
	meta.Artifacts = artifacts

	return writeMetaFile(path, &meta)
}

func UpdateDispatchSessionID(artifactDir string, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}

	path := filepath.Join(artifactDir, "_dispatch_meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}

	var meta DispatchMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("unmarshal meta: %w", err)
	}
	if meta.SessionID == sessionID {
		return nil
	}

	meta.SessionID = sessionID
	return writeMetaFile(path, &meta)
}

func ReadDispatchMeta(artifactDir string) (*DispatchMeta, error) {
	data, err := os.ReadFile(filepath.Join(artifactDir, "_dispatch_meta.json"))
	if err != nil {
		return nil, fmt.Errorf("read meta: %w", err)
	}

	var meta DispatchMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal meta: %w", err)
	}
	return &meta, nil
}

func WriteFullOutput(artifactDir, response string) (string, error) {
	path := filepath.Join(artifactDir, "full_output.md")
	if err := os.WriteFile(path, []byte(response), 0644); err != nil {
		return "", fmt.Errorf("write full output: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve full output path: %w", err)
	}
	return absPath, nil
}

type terminalResponseShape struct {
	Response          string
	HandoffSummary    string
	ResponseTruncated bool
	FullOutputPath    *string
}

func ExtractHandoffSummary(response string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 2000
	}
	for _, header := range []string{"## Summary", "## Handoff"} {
		idx := strings.Index(response, header)
		if idx >= 0 {
			section := response[idx+len(header):]
			nextHeader := strings.Index(section, "\n## ")
			if nextHeader >= 0 {
				section = section[:nextHeader]
			}
			section = strings.TrimSpace(section)
			if utf8.RuneCountInString(section) > maxChars {
				section = truncateAtBoundary(section, maxChars)
			}
			return section
		}
	}
	if utf8.RuneCountInString(response) <= maxChars {
		return response
	}

	return truncateAtBoundary(response, maxChars)
}

func PromptPreamble(spec *types.DispatchSpec) []string {
	if spec == nil {
		return nil
	}
	lines := make([]string, 0, 3)
	if spec.TraceToken != "" {
		lines = append(lines, "Trace token: "+spec.TraceToken)
	}
	if spec.DispatchID != "" {
		lines = append(lines, "Dispatch ID: "+spec.DispatchID)
	}
	if spec.ArtifactDir != "" {
		lines = append(lines, "Write intermediate artifacts to $AGENT_MUX_ARTIFACT_DIR.")
	}
	return lines
}

func WithPromptPreamble(prompt string, spec *types.DispatchSpec) string {
	lines := PromptPreamble(spec)
	if len(lines) == 0 {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines, "\n") + "\n\n" + prompt
}

type ErrorInfo struct {
	Message   string
	Hint      string
	Example   string
	Retryable bool
}

var ErrorCatalog = map[string]ErrorInfo{
	"model_not_found": {
		Message:   "Unknown model for engine.",
		Hint:      "The selected model is not available for the current engine.",
		Example:   "Retry with a supported model. Example: agent-mux -e codex -m gpt-5.4 --cwd /repo \"<prompt>\".",
		Retryable: true,
	},
	"engine_not_found": {
		Message:   "Unknown engine name.",
		Hint:      "agent-mux only supports the built-in engines codex, claude, and gemini.",
		Example:   "Retry with a valid engine. Example: agent-mux -e codex --cwd /repo \"<prompt>\".",
		Retryable: true,
	},
	"binary_not_found": {
		Hint:      "The requested harness binary is not installed or is not on PATH for this shell.",
		Example:   "Install the harness, confirm `codex`, `claude`, or `gemini` resolves on PATH, then retry the same agent-mux command.",
		Retryable: false,
	},
	"frozen_tool_call": {
		Message:   "Worker appears stuck in a tool call.",
		Hint:      "Worker stopped producing harness events while likely blocked in a hanging tool or shell command. Partial work was preserved in the artifact directory.",
		Example:   "Retry with a narrower task: agent-mux -R=lifter --cwd /repo \"<narrowed prompt>\". If long commands are expected, raise `silence_kill_seconds` in config.",
		Retryable: true,
	},
	"invalid_args": {
		Message:   "Invalid dispatch arguments.",
		Hint:      "The dispatch request is missing required fields or contains invalid flag combinations.",
		Example:   "Provide a valid engine, prompt, and working directory. Example: agent-mux -e codex --cwd /repo \"Fix failing test\".",
		Retryable: true,
	},
	"invalid_input": {
		Message:   "Input validation failed.",
		Hint:      "One of the provided values failed validation, usually a dispatch ID, basename, path fragment, or duration.",
		Example:   "Remove path separators or traversal segments and retry. Example: `--signal 01ABC...`, not `../01ABC`.",
		Retryable: true,
	},
	"config_error": {
		Message:   "Configuration is invalid.",
		Hint:      "agent-mux could not load or validate the referenced config, role, or control path.",
		Example:   "Fix the config file or role name, then retry. Example: agent-mux -R lifter --config /path/to/agent-mux.yaml --cwd /repo \"<prompt>\".",
		Retryable: true,
	},
	"parse_error": {
		Message:   "Malformed final harness output.",
		Hint:      "The final harness output was malformed enough that no trustworthy terminal result could be built.",
		Example:   "Retry once. If it repeats, inspect the raw harness output in the artifact directory before retrying.",
		Retryable: false,
	},
	"startup_failed": {
		Message:   "Harness process failed to start.",
		Hint:      "The harness process failed before a working session started.",
		Example:   "Check the harness install and arguments, then retry. Example: verify the engine binary runs directly from the same shell.",
		Retryable: true,
	},
	"frozen_killed": {
		Message:   "Worker killed after prolonged silence.",
		Hint:      "Worker was killed after prolonged silence - likely stuck in a hanging tool call. Partial work was preserved in the artifact directory.",
		Example:   "Retry with a narrower task: agent-mux -R=lifter --cwd /repo \"<narrowed prompt>\". Or extend silence timeout: add silence_kill_seconds=300 to config.",
		Retryable: true,
	},
	"signal_killed": {
		Message:   "Harness terminated by OS signal.",
		Hint:      "The harness process was terminated by the OS or another external actor, commonly SIGKILL, SIGTERM, or an OOM kill.",
		Example:   "Check exit status, system logs, and memory pressure, then retry once the host is stable.",
		Retryable: true,
	},
	"process_killed": {
		Message:   "Harness process exited unexpectedly.",
		Hint:      "The harness process exited unexpectedly and agent-mux could not classify the kill more precisely.",
		Example:   "Inspect stderr and recent events in the artifact directory, fix the underlying process issue, then retry.",
		Retryable: true,
	},
	"recovery_failed": {
		Message:   "No artifacts found for the given dispatch ID.",
		Hint:      "agent-mux could not find the prior dispatch state needed for this recovery or signal operation.",
		Example:   "Verify the dispatch ID and artifact directory, then retry. Example: agent-mux ps --root /artifacts.",
		Retryable: false,
	},
	"output_parse_error": {
		Message:   "Failed to parse streaming harness output.",
		Hint:      "The streaming harness event output could not be parsed cleanly.",
		Example:   "Retry once. If it repeats, inspect `events.jsonl` or raw harness output in the artifact directory.",
		Retryable: false,
	},
	"artifact_dir_unwritable": {
		Message:   "Artifact directory is not writable.",
		Hint:      "agent-mux could not create or write files in the artifact directory.",
		Example:   "Choose a writable path or fix permissions. Example: agent-mux --artifact-dir /tmp/agent-mux --cwd /repo \"<prompt>\".",
		Retryable: false,
	},
	"interrupted": {
		Message:   "Dispatch interrupted by caller cancellation.",
		Hint:      "The dispatch stopped because the caller context ended or an external cancellation signal arrived.",
		Example:   "Retry the dispatch if the work is still needed, or resume manually from preserved artifacts.",
		Retryable: false,
	},
	"abort_requested": {
		Message:   "Abort requested via steer or control file.",
		Hint:      "A steer or control-file abort explicitly requested that this dispatch stop early.",
		Example:   "If you still want the work, rerun the dispatch with a narrower prompt or without issuing `ax steer abort`.",
		Retryable: false,
	},
	"max_depth_exceeded": {
		Message:   "Max dispatch depth reached.",
		Hint:      "This task tried to spawn more nested dispatches than the configured safety limit allows.",
		Example:   "Complete the work in the current agent, or raise the depth limit only if the nesting is intentional.",
		Retryable: false,
	},
	"cancelled": {
		Message:   "Dispatch cancelled before launch.",
		Hint:      "The run was cancelled at the confirmation step, so no harness work started.",
		Example:   "Rerun with confirmation accepted, or skip the prompt entirely: agent-mux --yes --cwd /repo \"<prompt>\".",
		Retryable: false,
	},
	"prompt_denied": {
		Message:   "Prompt blocked by hooks policy.",
		Hint:      "A hooks policy blocked the prompt before the harness was allowed to start.",
		Example:   "Remove the matched content from the prompt or adjust the hook policy, then retry the same agent-mux command.",
		Retryable: false,
	},
	"event_denied": {
		Message:   "Event blocked by hooks policy.",
		Hint:      "A hooks policy blocked a harness event during execution, so the dispatch was stopped.",
		Example:   "Inspect the matched rule in the hook configuration, adjust the policy or task, then rerun the dispatch.",
		Retryable: false,
	},
	"internal_error": {
		Message:   "agent-mux hit an internal error.",
		Hint:      "agent-mux hit an internal invariant failure while building the result or command response.",
		Example:   "Retry once. If it repeats, capture the full JSON result and artifact directory for debugging.",
		Retryable: false,
	},
	"resume_unsupported": {
		Message:   "Resume unsupported by harness adapter.",
		Hint:      "The selected harness adapter does not support resuming an existing session, so inbox steering cannot restart it.",
		Example:   "Use an engine or adapter with resume support, or rerun the task without mid-flight coordinator injection.",
		Retryable: false,
	},
	"resume_session_missing": {
		Message:   "No resumable session ID available.",
		Hint:      "A resume was requested before the harness reported a resumable session or thread ID.",
		Example:   "Wait until the run emits a session-start event before steering, then retry the injection.",
		Retryable: false,
	},
	"resume_start_failed": {
		Message:   "Failed to start resumed harness process.",
		Hint:      "agent-mux attempted to resume the harness session, but the restart command failed.",
		Example:   "Check the adapter resume arguments and harness installation, then retry from the preserved artifact directory.",
		Retryable: false,
	},
}

func NewDispatchError(code string, message string, suggestion string) *types.DispatchError {
	info, ok := ErrorCatalog[code]
	if !ok {
		info = ErrorInfo{Retryable: false}
	}

	if message == "" {
		message = info.Message
	}

	hint := info.Hint
	example := info.Example
	if suggestion != "" {
		hint = suggestion
		example = ""
	}
	suggestionText := strings.TrimSpace(hint + " " + example)

	return &types.DispatchError{
		Code:       code,
		Message:    message,
		Suggestion: suggestionText,
		Hint:       hint,
		Example:    example,
		Retryable:  info.Retryable,
	}
}
func FuzzyMatchModel(input string, validModels []string) string {
	if len(validModels) == 0 {
		return ""
	}

	bestMatch := ""
	bestDist := len(input) + 10

	for _, model := range validModels {
		d := levenshtein(strings.ToLower(input), strings.ToLower(model))
		if d < bestDist {
			bestDist = d
			bestMatch = model
		}
	}
	if bestDist <= 3 {
		return bestMatch
	}
	return ""
}
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}
func BuildCompletedResult(spec *types.DispatchSpec, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	result := baseResult(spec, types.StatusCompleted, shapeTerminalResponse(spec, response), activity, metadata, durationMS)

	result.Artifacts = ScanArtifacts(spec.ArtifactDir)
	return result
}
func BuildTimedOutResult(spec *types.DispatchSpec, response, reason string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	result := baseResult(spec, types.StatusTimedOut, shapeTerminalResponse(spec, response), activity, metadata, durationMS)
	result.Partial = true
	result.Recoverable = true
	result.Reason = reason
	result.Artifacts = ScanArtifacts(spec.ArtifactDir)
	return result
}
func BuildFailedResult(spec *types.DispatchSpec, response string, dispatchErr *types.DispatchError, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	// FM-9: Include accumulated partial response so callers can see what the
	// worker accomplished before failure.
	result := baseResult(spec, types.StatusFailed, shapeTerminalResponse(spec, response), activity, metadata, durationMS)
	if response == "" {
		result.HandoffSummary = dispatchErr.Message
	}
	result.Error = dispatchErr
	result.Artifacts = ScanArtifacts(spec.ArtifactDir)
	return result
}
func ScanArtifacts(dir string) []string {
	empty := []string{}
	if dir == "" {
		return empty
	}

	var artifacts []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "_dispatch_meta.json" || name == "events.jsonl" || name == "inbox.md" || name == "status.json" || name == "host.pid" || name == "control.json" {
			return nil
		}
		artifacts = append(artifacts, path)
		return nil
	})
	if err != nil {
		return empty
	}
	if len(artifacts) == 0 {
		return empty
	}
	return artifacts
}

func writeMetaFile(path string, meta *DispatchMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write meta temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename meta temp file: %w", err)
	}
	return nil
}

func truncateAtBoundary(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	truncated := string(runes[:maxChars])
	lastBoundary := strings.LastIndexAny(truncated, ".!?\n")
	if lastBoundary > maxChars/2 {
		return truncated[:lastBoundary+1]
	}
	return truncated
}

func shapeTerminalResponse(spec *types.DispatchSpec, response string) terminalResponseShape {
	shape := terminalResponseShape{
		Response:       response,
		HandoffSummary: ExtractHandoffSummary(response, 2000),
	}

	if spec == nil || spec.ResponseMaxChars <= 0 {
		return shape
	}
	if utf8.RuneCountInString(response) <= spec.ResponseMaxChars {
		return shape
	}

	fullOutputPath, err := WriteFullOutput(spec.ArtifactDir, response)
	if err != nil {
		return shape
	}

	shape.Response = truncateAtBoundary(response, spec.ResponseMaxChars)
	shape.ResponseTruncated = true
	shape.FullOutputPath = &fullOutputPath
	return shape
}

func baseResult(spec *types.DispatchSpec, status types.DispatchStatus, shaped terminalResponseShape, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	EnsureTraceability(spec)
	return &types.DispatchResult{
		SchemaVersion:     1,
		Status:            status,
		DispatchID:        spec.DispatchID,
		DispatchSalt:      spec.Salt,
		TraceToken:        spec.TraceToken,
		Response:          shaped.Response,
		ResponseTruncated: shaped.ResponseTruncated,
		FullOutputPath:    shaped.FullOutputPath,
		HandoffSummary:    shaped.HandoffSummary,
		Activity:          activity,
		Metadata:          metadata,
		DurationMS:        durationMS,
	}
}
