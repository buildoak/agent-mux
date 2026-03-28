package adapter

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

type ClaudeAdapter struct {
	mu         sync.Mutex
	toolInputs map[string]claudeToolMeta
}

func (a *ClaudeAdapter) Binary() string {
	return "claude"
}

func (a *ClaudeAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}

	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if maxTurns := claudeMaxTurns(spec); maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	}
	if mode, ok := spec.EngineOpts["permission-mode"].(string); ok && mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	if spec.SystemPrompt != "" {
		args = append(args, "--system-prompt", spec.SystemPrompt)
	}
	for _, dir := range addDirs(spec) {
		args = append(args, "--add-dir", dir)
	}

	args = append(args, spec.Prompt)
	return args
}

func (a *ClaudeAdapter) EnvVars(spec *types.DispatchSpec) ([]string, error) {
	return nil, nil
}

type claudeEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Output    string          `json:"output"`
	IsError   bool            `json:"is_error"`
	Result    string          `json:"result"`
	Error     string          `json:"error"`
	Usage     *claudeUsage    `json:"usage"`
	NumTurns  int             `json:"num_turns"`
	Message   claudeMessage   `json:"message"`
}

type claudeMessage struct {
	Content []claudeContent `json:"content"`
}

type claudeContent struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	Content   string          `json:"content"`
	IsError   bool            `json:"is_error"`
}

type claudeUsage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_input_tokens"`
	CacheWriteTokens int `json:"cache_creation_input_tokens"`
}

type claudeToolMeta struct {
	Name     string
	FilePath string
}

func (a *ClaudeAdapter) ParseEvent(line string) (*types.HarnessEvent, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}

	var raw claudeEvent
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
	case "system":
		if raw.Subtype != "init" {
			evt.Kind = types.EventRawPassthrough
			return evt, nil
		}
		evt.Kind = types.EventSessionStart
		evt.SessionID = raw.SessionID
		return evt, nil

	case "assistant":
		return a.parseAssistantEvent(&raw, evt), nil

	case "result":
		switch raw.Subtype {
		case "success":
			evt.Kind = types.EventResponse
			evt.Text = raw.Result
			evt.SessionID = raw.SessionID
			evt.Tokens = claudeUsageToTokens(raw.Usage)
			evt.Turns = raw.NumTurns
		case "error":
			evt.Kind = types.EventTurnFailed
			evt.ErrorCode = "result_error"
			evt.Text = raw.Error
			evt.SessionID = raw.SessionID
		default:
			evt.Kind = types.EventRawPassthrough
		}
		return evt, nil

	default:
		evt.Kind = types.EventRawPassthrough
		return evt, nil
	}
}

func (a *ClaudeAdapter) SupportsResume() bool {
	return true
}

func (a *ClaudeAdapter) ResumeArgs(_ *types.DispatchSpec, sessionID string, message string) []string {
	return []string{"--resume", sessionID, "--continue", message}
}

func (a *ClaudeAdapter) parseAssistantEvent(raw *claudeEvent, evt *types.HarnessEvent) *types.HarnessEvent {
	if len(raw.Message.Content) == 0 {
		evt.Kind = types.EventRawPassthrough
		return evt
	}

	var textParts []string
	var firstToolEvent *types.HarnessEvent

	for _, item := range raw.Message.Content {
		switch item.Type {
		case "text":
			if item.Text != "" {
				textParts = append(textParts, item.Text)
			}
		case "tool_use":
			if firstToolEvent != nil {
				continue
			}
			ev := *evt
			var inputFields struct {
				FilePath string `json:"file_path"`
				Command  string `json:"command"`
			}
			if len(item.Input) > 0 {
				_ = json.Unmarshal(item.Input, &inputFields)
			}
			a.storeToolInput(item.ID, item.Name, inputFields.FilePath)
			switch item.Name {
			case "Read", "Glob", "Grep":
				ev.Kind = types.EventFileRead
				if item.Name == "Read" {
					ev.SecondaryKind = types.EventToolStart
					ev.Tool = item.Name
				}
				ev.FilePath = inputFields.FilePath
			case "Edit", "Write":
				ev.Kind = types.EventToolStart
				ev.Tool = item.Name
				ev.FilePath = inputFields.FilePath
			case "Bash":
				ev.Kind = types.EventCommandRun
				ev.Tool = item.Name
				ev.Command = inputFields.Command
			default:
				ev.Kind = types.EventToolStart
				ev.Tool = item.Name
			}
			firstToolEvent = &ev
		case "tool_result":
			if firstToolEvent != nil {
				continue
			}
			ev := *evt
			toolName := item.Name
			toolID := item.ToolUseID
			if toolID == "" {
				toolID = item.ID
			}
			meta := a.takeToolInput(toolID)
			if toolName == "" {
				toolName = meta.Name
			}
			switch {
			case (toolName == "Edit" || toolName == "Write") && !item.IsError:
				ev.Kind = types.EventFileWrite
				ev.FilePath = meta.FilePath
			default:
				ev.Kind = types.EventToolEnd
				ev.Tool = toolName
			}
			firstToolEvent = &ev
		}
	}

	if firstToolEvent != nil {
		return firstToolEvent
	}
	if len(textParts) > 0 {
		evt.Kind = types.EventProgress
		evt.Text = strings.Join(textParts, "")
		return evt
	}
	evt.Kind = types.EventRawPassthrough
	return evt
}

func (a *ClaudeAdapter) storeToolInput(id string, name string, filePath string) {
	if id == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.toolInputs == nil {
		a.toolInputs = make(map[string]claudeToolMeta)
	}
	a.toolInputs[id] = claudeToolMeta{Name: name, FilePath: filePath}
}

func (a *ClaudeAdapter) takeToolInput(id string) claudeToolMeta {
	if id == "" {
		return claudeToolMeta{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.toolInputs == nil {
		return claudeToolMeta{}
	}
	meta := a.toolInputs[id]
	delete(a.toolInputs, id)
	return meta
}

func claudeMaxTurns(spec *types.DispatchSpec) int {
	if spec == nil || spec.EngineOpts == nil {
		return 0
	}
	switch v := spec.EngineOpts["max-turns"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func claudeUsageToTokens(usage *claudeUsage) *types.TokenUsage {
	if usage == nil {
		return nil
	}
	return &types.TokenUsage{
		Input:      usage.InputTokens,
		Output:     usage.OutputTokens,
		CacheRead:  usage.CacheReadTokens,
		CacheWrite: usage.CacheWriteTokens,
	}
}
