package types

import (
	"encoding/json"
	"time"
)

type DispatchStatus string

const (
	StatusCompleted DispatchStatus = "completed"
	StatusTimedOut  DispatchStatus = "timed_out"
	StatusFailed    DispatchStatus = "failed"
)

type DispatchResult struct {
	SchemaVersion     int               `json:"schema_version"`
	Status            DispatchStatus    `json:"status"`
	DispatchID        string            `json:"dispatch_id"`
	Response          string            `json:"response"`
	ResponseTruncated bool              `json:"response_truncated"`
	FullOutput        *string           `json:"full_output"`
	FullOutputPath    *string           `json:"full_output_path,omitempty"`
	HandoffSummary    string            `json:"handoff_summary"`
	Artifacts         []string          `json:"artifacts"`
	Partial           bool              `json:"partial,omitempty"`
	Recoverable       bool              `json:"recoverable,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	Error             *DispatchError    `json:"error,omitempty"`
	Activity          *DispatchActivity `json:"activity"`
	Metadata          *DispatchMetadata `json:"metadata"`
	DurationMS        int64             `json:"duration_ms"`
}

type DispatchError struct {
	Code             string   `json:"code"`
	Message          string   `json:"message"`
	Hint             string   `json:"hint"`
	Example          string   `json:"example"`
	Retryable        bool     `json:"retryable"`
	PartialArtifacts []string `json:"partial_artifacts,omitempty"`
}

type DispatchActivity struct {
	FilesChanged []string `json:"files_changed"`
	FilesRead    []string `json:"files_read"`
	CommandsRun  []string `json:"commands_run"`
	ToolCalls    []string `json:"tool_calls"`
}

type DispatchMetadata struct {
	Engine    string      `json:"engine"`
	Model     string      `json:"model"`
	Role      string      `json:"role,omitempty"`
	Variant   string      `json:"variant,omitempty"`
	Profile   string      `json:"profile,omitempty"`
	Skills    []string    `json:"skills,omitempty"`
	Tokens    *TokenUsage `json:"tokens"`
	Turns     int         `json:"turns"`
	CostUSD   float64     `json:"cost_usd"`
	SessionID string      `json:"session_id,omitempty"`
}

type DispatchAnnotations struct {
	Role    string
	Variant string
	Profile string
	Skills  []string
}

type TokenUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
}

type DispatchSpec struct {
	DispatchID   string `json:"dispatch_id"`
	Engine       string `json:"engine"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort"`
	Prompt       string `json:"prompt"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	Cwd          string `json:"cwd"`
	ArtifactDir  string `json:"artifact_dir"`
	ContextFile  string `json:"context_file,omitempty"`
	TimeoutSec   int    `json:"timeout_sec,omitempty"`
	GraceSec     int    `json:"grace_sec,omitempty"`
	MaxDepth     int    `json:"max_depth"`
	Depth        int    `json:"depth"`
	// FullAccess only changes Codex sandbox resolution. Claude and Gemini use
	// EngineOpts["permission-mode"] for their permission/approval flags.
	FullAccess bool           `json:"full_access"`
	EngineOpts map[string]any `json:"engine_opts,omitempty"`
}

type dispatchSpecJSON struct {
	DispatchID   string         `json:"dispatch_id"`
	Engine       string         `json:"engine"`
	Model        string         `json:"model,omitempty"`
	Effort       string         `json:"effort"`
	Prompt       string         `json:"prompt"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	Cwd          string         `json:"cwd"`
	ContextFile  string         `json:"context_file,omitempty"`
	ArtifactDir  string         `json:"artifact_dir"`
	TimeoutSec   int            `json:"timeout_sec,omitempty"`
	GraceSec     int            `json:"grace_sec,omitempty"`
	MaxDepth     int            `json:"max_depth"`
	Depth        int            `json:"depth"`
	EngineOpts   map[string]any `json:"engine_opts,omitempty"`
	FullAccess   bool           `json:"full_access"`
}

func (s DispatchSpec) MarshalJSON() ([]byte, error) {
	wire := dispatchSpecJSON{
		DispatchID:   s.DispatchID,
		Engine:       s.Engine,
		Model:        s.Model,
		Effort:       s.Effort,
		Prompt:       s.Prompt,
		SystemPrompt: s.SystemPrompt,
		Cwd:          s.Cwd,
		ContextFile:  s.ContextFile,
		ArtifactDir:  s.ArtifactDir,
		TimeoutSec:   s.TimeoutSec,
		GraceSec:     s.GraceSec,
		MaxDepth:     s.MaxDepth,
		Depth:        s.Depth,
		EngineOpts:   s.EngineOpts,
		FullAccess:   s.FullAccess,
	}
	return json.Marshal(wire)
}

func (s *DispatchSpec) UnmarshalJSON(data []byte) error {
	var wire dispatchSpecJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*s = DispatchSpec{
		DispatchID:   wire.DispatchID,
		Engine:       wire.Engine,
		Model:        wire.Model,
		Effort:       wire.Effort,
		Prompt:       wire.Prompt,
		SystemPrompt: wire.SystemPrompt,
		Cwd:          wire.Cwd,
		ContextFile:  wire.ContextFile,
		ArtifactDir:  wire.ArtifactDir,
		TimeoutSec:   wire.TimeoutSec,
		GraceSec:     wire.GraceSec,
		MaxDepth:     wire.MaxDepth,
		Depth:        wire.Depth,
		EngineOpts:   wire.EngineOpts,
		FullAccess:   wire.FullAccess,
	}
	return nil
}

type EventKind int

const (
	EventUnknown EventKind = iota
	EventToolStart
	EventToolEnd        // Harness finished a tool call
	EventFileWrite      // Harness wrote a file
	EventFileRead       // Harness read a file
	EventCommandRun     // Harness ran a shell command
	EventProgress       // Free-form progress
	EventResponse       // Final or partial response text
	EventError          // Harness-reported error
	EventSessionStart   // Session initialized (carries session ID)
	EventTurnComplete   // Turn finished (carries token counts)
	EventTurnFailed     // Turn failed
	EventRawPassthrough // Unclassifiable line
)

type HarnessEvent struct {
	Kind          EventKind
	SecondaryKind EventKind
	Timestamp     time.Time
	Tool          string      // Set for ToolStart/ToolEnd
	FilePath      string      // Set for FileWrite/FileRead
	Command       string      // Set for CommandRun
	Text          string      // Set for Progress/Response/Error
	SessionID     string      // Set for SessionStart
	DurationMS    int64       // Set for ToolEnd/TurnComplete
	Tokens        *TokenUsage // Set for TurnComplete
	Turns         int         // Set for Response
	ErrorCode     string      // Set for Error
	Raw           []byte      // Always set (original harness line)
}

type HarnessAdapter interface {
	Binary() string
	BuildArgs(spec *DispatchSpec) []string
	EnvVars(spec *DispatchSpec) ([]string, error)
	ParseEvent(line string) (*HarnessEvent, error)
	SupportsResume() bool
	ResumeArgs(spec *DispatchSpec, sessionID string, message string) []string
	// StdinNudge returns the bytes to write to stdin as a liveness nudge,
	// or nil if the engine does not support stdin-driven recovery.
	StdinNudge() []byte
}
