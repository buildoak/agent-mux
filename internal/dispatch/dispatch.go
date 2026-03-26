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
func EnsureArtifactDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

type DispatchMeta struct {
	DispatchID          string   `json:"dispatch_id"`
	DispatchSalt        string   `json:"dispatch_salt"`
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
	hash := sha256.Sum256([]byte(spec.Prompt))
	meta := DispatchMeta{
		DispatchID:   spec.DispatchID,
		DispatchSalt: spec.Salt,
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
func TruncateResponse(response string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(response) <= maxChars {
		return response, false
	}

	return truncateAtBoundary(response, maxChars), true
}
func WriteFullOutput(artifactDir, response string) (string, error) {
	path := filepath.Join(artifactDir, "full_output.md")
	if err := os.WriteFile(path, []byte(response), 0644); err != nil {
		return "", fmt.Errorf("write full output: %w", err)
	}
	return path, nil
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
			if len(section) > maxChars {
				section = section[:maxChars]
			}
			return section
		}
	}
	if len(response) <= maxChars {
		return response
	}

	return truncateAtBoundary(response, maxChars)
}

type ErrorInfo struct {
	Message    string
	Suggestion string
	Retryable  bool
}

var ErrorCatalog = map[string]ErrorInfo{
	"model_not_found":         {Retryable: true},
	"engine_not_found":        {Message: "Unknown engine name.", Suggestion: "Valid engines: codex, claude, gemini", Retryable: true},
	"binary_not_found":        {Suggestion: "Install the requested harness binary and verify it is on PATH before retrying.", Retryable: false},
	"api_key_missing":         {Suggestion: "Set the provider API key in the environment expected by the harness, then retry.", Retryable: false},
	"api_overloaded":          {Message: "Provider overloaded (429/529).", Suggestion: "Retry in 30s or try --engine with a different provider.", Retryable: true},
	"api_error":               {Suggestion: "Retry once. If it repeats, switch model or provider and include the provider error details.", Retryable: true},
	"frozen_tool_call":        {Suggestion: "Worker may be stuck in a hanging command or tool call. Retry with a narrower task or longer timeout. Partial work was preserved.", Retryable: true},
	"invalid_args":            {Suggestion: "Fix the dispatch arguments and retry. Check required fields such as engine, prompt, and working directory.", Retryable: true},
	"config_error":            {Suggestion: "Fix the referenced config or role definition, then retry the dispatch.", Retryable: true},
	"process_killed":          {Suggestion: "Check harness stderr and recent events, then retry once the underlying process issue is resolved.", Retryable: true},
	"recovery_failed":         {Message: "No artifacts found for the given dispatch ID.", Suggestion: "Previous dispatch may not have written artifacts. Check the artifact directory.", Retryable: false},
	"output_parse_error":      {Suggestion: "The harness emitted output agent-mux could not parse cleanly. Retry once; if it repeats, inspect raw harness output.", Retryable: false},
	"skill_not_found":         {Retryable: true},
	"coordinator_not_found":   {Retryable: true},
	"prompt_file_missing":     {Retryable: true},
	"artifact_dir_unwritable": {Retryable: false},
	"interrupted":             {Suggestion: "Caller cancellation stopped the dispatch. Resume manually if you still need the work completed.", Retryable: false},
	"max_depth_exceeded":      {Message: "Max dispatch depth reached.", Suggestion: "Complete work directly in the current dispatch instead of spawning another one.", Retryable: false},
}

func NewDispatchError(code string, message string, suggestion string) *types.DispatchError {
	info, ok := ErrorCatalog[code]
	if !ok {
		info = ErrorInfo{Retryable: false}
	}

	if message == "" {
		message = info.Message
	}
	if suggestion == "" {
		suggestion = info.Suggestion
	}

	return &types.DispatchError{
		Code:       code,
		Message:    message,
		Suggestion: suggestion,
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
func BuildCompletedResult(spec *types.DispatchSpec, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64, responseMaxChars int) *types.DispatchResult {
	result := baseResult(spec, types.StatusCompleted, response, activity, metadata, durationMS)

	if responseMaxChars > 0 {
		truncated, wasTruncated := TruncateResponse(response, responseMaxChars)
		if wasTruncated {
			result.Response = truncated
			result.ResponseTruncated = true
			fullPath, err := WriteFullOutput(spec.ArtifactDir, response)
			if err == nil {
				result.FullOutput = &fullPath
			}
		}
	}

	result.Artifacts = ScanArtifacts(spec.ArtifactDir)
	return result
}
func BuildTimedOutResult(spec *types.DispatchSpec, response, reason string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	result := baseResult(spec, types.StatusTimedOut, response, activity, metadata, durationMS)
	result.Partial = true
	result.Recoverable = true
	result.Reason = reason
	result.Artifacts = ScanArtifacts(spec.ArtifactDir)
	return result
}
func BuildFailedResult(spec *types.DispatchSpec, dispatchErr *types.DispatchError, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	result := baseResult(spec, types.StatusFailed, "", activity, metadata, durationMS)
	result.HandoffSummary = dispatchErr.Message
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
		if name == "_dispatch_meta.json" || name == "events.jsonl" || name == "inbox.md" {
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
	return os.WriteFile(path, data, 0644)
}

func truncateAtBoundary(s string, maxChars int) string {
	truncated := s[:maxChars]
	lastBoundary := strings.LastIndexAny(truncated, ".!?\n")
	if lastBoundary > maxChars/2 {
		return truncated[:lastBoundary+1]
	}
	return truncated
}

func baseResult(spec *types.DispatchSpec, status types.DispatchStatus, response string, activity *types.DispatchActivity, metadata *types.DispatchMetadata, durationMS int64) *types.DispatchResult {
	return &types.DispatchResult{
		SchemaVersion:  1,
		Status:         status,
		DispatchID:     spec.DispatchID,
		DispatchSalt:   spec.Salt,
		Response:       response,
		HandoffSummary: ExtractHandoffSummary(response, 2000),
		Activity:       activity,
		Metadata:       metadata,
		DurationMS:     durationMS,
	}
}
