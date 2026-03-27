package types

import (
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
	DispatchSalt      string            `json:"dispatch_salt"`
	TraceToken        string            `json:"trace_token"`
	Response          string            `json:"response"`
	ResponseTruncated bool              `json:"response_truncated"`
	FullOutput        *string           `json:"full_output"`
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
	Suggestion       string   `json:"suggestion"`
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
	Engine           string      `json:"engine"`
	Model            string      `json:"model"`
	Role             string      `json:"role,omitempty"`
	Tokens           *TokenUsage `json:"tokens"`
	Turns            int         `json:"turns"`
	CostUSD          float64     `json:"cost_usd"`
	SessionID        string      `json:"session_id,omitempty"`
	PipelineID       string      `json:"pipeline_id,omitempty"`
	ParentDispatchID string      `json:"parent_dispatch_id,omitempty"`
}

type TokenUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
}

type DispatchSpec struct {
	DispatchID          string         `json:"dispatch_id"`
	Salt                string         `json:"salt,omitempty"`
	TraceToken          string         `json:"trace_token,omitempty"`
	Engine              string         `json:"engine"`
	Model               string         `json:"model,omitempty"`
	Effort              string         `json:"effort"`
	Prompt              string         `json:"prompt"`
	SystemPrompt        string         `json:"system_prompt,omitempty"`
	Cwd                 string         `json:"cwd"`
	Skills              []string       `json:"skills,omitempty"`
	Coordinator         string         `json:"coordinator,omitempty"`
	Pipeline            string         `json:"pipeline,omitempty"`
	ContextFile         string         `json:"context_file,omitempty"`
	ArtifactDir         string         `json:"artifact_dir"`
	TimeoutSec          int            `json:"timeout_sec,omitempty"`
	GraceSec            int            `json:"grace_sec,omitempty"`
	Role                string         `json:"role,omitempty"`
	MaxDepth            int            `json:"max_depth"`
	AllowSubdispatch    bool           `json:"allow_subdispatch"`
	Depth               int            `json:"depth"`
	ParentDispatchID    string         `json:"parent_dispatch_id,omitempty"`
	PipelineID          string         `json:"pipeline_id,omitempty"`
	PipelineStep        int            `json:"pipeline_step"`
	ContinuesDispatchID string         `json:"continues_dispatch_id,omitempty"`
	Receives            string         `json:"receives,omitempty"`
	PassOutputAs        string         `json:"pass_output_as,omitempty"`
	Parallel            int            `json:"parallel,omitempty"`
	HandoffMode         string         `json:"handoff_mode,omitempty"`
	ResponseMaxChars    int            `json:"response_max_chars,omitempty"`
	EngineOpts          map[string]any `json:"engine_opts,omitempty"`
	FullAccess          bool           `json:"full_access"`
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
	EnvVars(spec *DispatchSpec) []string
	ParseEvent(line string) (*HarnessEvent, error)
	SupportsResume() bool
	ResumeArgs(spec *DispatchSpec, sessionID string, message string) []string
}
