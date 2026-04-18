package adapter

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

type CodexAdapter struct{}

var validCodexSandboxValues = map[string]bool{
	"danger-full-access": true,
	"workspace-write":    true,
	"read-only":          true,
}

// ValidateCodexSandbox checks that the sandbox value in EngineOpts is one of the
// values accepted by the Codex CLI. Call before BuildArgs to catch invalid values
// early with a structured error instead of a silent Codex crash.
func ValidateCodexSandbox(spec *types.DispatchSpec) (string, bool) {
	permMode, _ := spec.EngineOpts["permission-mode"].(string)
	if permMode != "" {
		return "", true // permission-mode takes precedence; no sandbox to validate
	}
	sandbox := "danger-full-access"
	if opts, ok := spec.EngineOpts["sandbox"]; ok {
		if s, ok := opts.(string); ok && s != "" {
			sandbox = s
		}
	}
	if validCodexSandboxValues[sandbox] {
		return "", true
	}
	return sandbox, false
}

type CodexSoftSteerEnvelope struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

func (a *CodexAdapter) Binary() string {
	return "codex"
}

// BuildArgs builds argv for `codex exec [resume] [prompt]`. The prompt is
// still passed as a positional argv so we don't depend on stdin for input —
// necessary because codex ≥0.121 (upstream PR openai/codex#15917)
// unconditionally reads stdin to EOF before processing the prompt whenever
// stdin is non-TTY. `internal/engine/loop.go` wires the child's stdin to an
// empty reader so the drain completes immediately and codex unblocks; this
// also disables the FIFO → stdin soft-steer path for codex. A future version
// of agent-mux will wire `<artifact_dir>/inbox.md` as a proper out-of-band
// steering channel for running codex dispatches.
func (a *CodexAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	args := []string{"exec", "--json"}

	if spec.Model != "" {
		args = append(args, "-m", spec.Model)
	}
	permMode, _ := spec.EngineOpts["permission-mode"].(string)
	if permMode != "" {
		args = append(args, "-s", permMode)
	} else {
		sandbox := "danger-full-access"
		if opts, ok := spec.EngineOpts["sandbox"]; ok {
			if s, ok := opts.(string); ok && s != "" {
				sandbox = s
			}
		}
		if spec.FullAccess && sandbox == "danger-full-access" {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			args = append(args, "-s", sandbox)
		}
	}
	if spec.Cwd != "" {
		args = append(args, "-C", spec.Cwd)
	}
	if opts, ok := spec.EngineOpts["reasoning"]; ok {
		if r, ok := opts.(string); ok && r != "" {
			args = append(args, "-c", "model_reasoning_effort="+r)
		}
	}

	for _, dir := range addDirs(spec) {
		args = append(args, "--add-dir", dir)
	}
	prompt := spec.Prompt
	if spec.SystemPrompt != "" {
		prompt = spec.SystemPrompt + "\n\n" + prompt
	}

	args = append(args, prompt)

	return args
}

func (a *CodexAdapter) EnvVars(spec *types.DispatchSpec) ([]string, error) {
	return nil, nil
}

type codexEvent struct {
	Type         string      `json:"type"`
	ThreadID     string      `json:"thread_id,omitempty"`
	ItemType     string      `json:"item_type,omitempty"`
	Command      string      `json:"command,omitempty"`
	Content      string      `json:"content,omitempty"`
	ContentDelta string      `json:"content_delta,omitempty"`
	FilePath     string      `json:"file_path,omitempty"`
	ChangeType   string      `json:"change_type,omitempty"`
	ExitCode     int         `json:"exit_code,omitempty"`
	DurationMS   int64       `json:"duration_ms,omitempty"`
	Usage        *codexUsage `json:"usage,omitempty"`
	Error        *codexError `json:"error,omitempty"`
	Code         string      `json:"code,omitempty"`
	Message      string      `json:"message,omitempty"`
	Model        string      `json:"model,omitempty"`
	Item         *codexItem  `json:"item,omitempty"`
}

type codexItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
}

type codexUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type codexError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func EncodeSoftSteerEnvelope(action, message string) ([]byte, error) {
	payload := CodexSoftSteerEnvelope{
		Action:  strings.TrimSpace(action),
		Message: message,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DecodeSoftSteerEnvelope(line []byte) (CodexSoftSteerEnvelope, error) {
	var env CodexSoftSteerEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return CodexSoftSteerEnvelope{}, err
	}
	env.Action = strings.TrimSpace(env.Action)
	return env, nil
}

func FormatSoftSteerInput(action, message string) []byte {
	switch strings.TrimSpace(action) {
	case "redirect":
		return []byte("IMPORTANT: The coordinator has redirected your task. Stop your current approach and follow these new instructions instead:\n" + message + "\n")
	case "nudge":
		return []byte("Note from coordinator: " + message + "\n")
	default:
		return []byte(message + "\n")
	}
}

func (a *CodexAdapter) ParseEvent(line string) (*types.HarnessEvent, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}

	var raw codexEvent
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return &types.HarnessEvent{
			Kind:      types.EventRawPassthrough,
			Timestamp: time.Now(),
			Raw:       []byte(line),
		}, nil
	}

	evt := &types.HarnessEvent{
		Timestamp: time.Now(),
		Raw:       []byte(line),
	}

	switch raw.Type {
	case "thread.started":
		evt.Kind = types.EventSessionStart
		evt.SessionID = raw.ThreadID
	case "turn.started":
		return nil, nil
	case "item.started":
		return parseItemStarted(&raw, evt), nil
	case "item.updated":
		itemType := raw.ItemType
		if itemType == "" && raw.Item != nil {
			itemType = raw.Item.Type
		}
		if itemType != "agent_message" {
			return nil, nil
		}
		text := raw.ContentDelta
		if text == "" && raw.Item != nil {
			text = raw.Item.Text
		}
		if text == "" {
			return nil, nil
		}
		evt.Kind = types.EventProgress
		evt.Text = text
	case "item.completed":
		return parseItemCompleted(&raw, evt), nil
	case "turn.completed":
		evt.Kind = types.EventTurnComplete
		evt.DurationMS = raw.DurationMS
		evt.Tokens = usageToTokens(raw.Usage)
	case "turn.failed":
		evt.Kind = types.EventTurnFailed
		if raw.Error != nil {
			evt.ErrorCode = raw.Error.Code
			evt.Text = raw.Error.Message
		}
	case "error":
		evt.Kind = types.EventError
		evt.ErrorCode = raw.Code
		evt.Text = raw.Message
	default:
		evt.Kind = types.EventRawPassthrough
	}

	return evt, nil
}

func parseItemStarted(raw *codexEvent, evt *types.HarnessEvent) *types.HarnessEvent {
	itemType := raw.ItemType
	command := raw.Command
	filePath := raw.FilePath
	if raw.Item != nil {
		if itemType == "" {
			itemType = raw.Item.Type
		}
		if command == "" {
			command = raw.Item.Command
		}
		if filePath == "" {
			filePath = raw.Item.FilePath
		}
	}

	switch itemType {
	case "command_execution":
		evt.Kind = types.EventCommandRun
		evt.SecondaryKind = types.EventToolStart
		evt.Tool = "command_execution"
		evt.Command = command
	case "file_change":
		evt.Kind = types.EventToolStart
		evt.Tool = "file_change"
	case "web_search", "mcp_tool_call", "collab_tool_call":
		evt.Kind = types.EventToolStart
		evt.Tool = itemType
	case "agent_message":
		return nil
	default:
		evt.Kind = types.EventToolStart
		evt.Tool = itemType
	}
	return evt
}

func anySliceToStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func parseItemCompleted(raw *codexEvent, evt *types.HarnessEvent) *types.HarnessEvent {
	itemType := raw.ItemType
	text := raw.Content
	command := raw.Command
	filePath := raw.FilePath
	if raw.Item != nil {
		if itemType == "" {
			itemType = raw.Item.Type
		}
		if text == "" {
			text = raw.Item.Text
		}
		if command == "" {
			command = raw.Item.Command
		}
		if filePath == "" {
			filePath = raw.Item.FilePath
		}
	}

	switch itemType {
	case "agent_message":
		evt.Kind = types.EventResponse
		evt.Text = text
	case "command_execution":
		evt.Kind = types.EventToolEnd
		evt.Tool = "command_execution"
		evt.Command = command
		evt.DurationMS = raw.DurationMS
	case "file_change":
		evt.Kind = types.EventFileWrite
		evt.FilePath = filePath
		evt.DurationMS = raw.DurationMS
	case "reasoning":
		evt.Kind = types.EventProgress
		evt.Text = text
	default:
		evt.Kind = types.EventToolEnd
		evt.Tool = itemType
		evt.DurationMS = raw.DurationMS
	}
	return evt
}
func (a *CodexAdapter) SupportsResume() bool {
	return true
}

func (a *CodexAdapter) ResumeArgs(spec *types.DispatchSpec, sessionID string, message string) []string {
	args := []string{"exec", "resume"}
	if spec != nil && spec.Model != "" {
		args = append(args, "-m", spec.Model)
	}
	args = append(args, "--json", sessionID, message)
	return args
}

func addDirs(spec *types.DispatchSpec) []string {
	out := make([]string, 0)
	if opts, ok := spec.EngineOpts["add-dir"]; ok {
		out = append(out, anySliceToStrings(opts)...)
	}
	return out
}

func usageToTokens(usage *codexUsage) *types.TokenUsage {
	if usage == nil {
		return nil
	}
	return &types.TokenUsage{
		Input:     usage.InputTokens,
		Output:    usage.OutputTokens,
		Reasoning: usage.ReasoningTokens,
	}
}
