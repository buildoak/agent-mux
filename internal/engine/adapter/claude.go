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
	toolInputs map[string]string
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

	args = append(args, spec.Prompt)
	return args
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
	Cost      *claudeCost     `json:"cost"`
}

type claudeCost struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
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

	var inputFields struct {
		FilePath string `json:"file_path"`
		Command  string `json:"command"`
	}
	if len(raw.Input) > 0 {
		if err := json.Unmarshal(raw.Input, &inputFields); err != nil {
			return nil, err
		}
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
		return a.parseAssistantEvent(&raw, &inputFields, evt), nil

	case "result":
		switch raw.Subtype {
		case "success":
			evt.Kind = types.EventResponse
			evt.Text = raw.Result
			evt.SessionID = raw.SessionID
			evt.Tokens = claudeCostToTokens(raw.Cost)
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

func (a *ClaudeAdapter) ResumeArgs(sessionID string, message string) []string {
	return []string{"--resume", sessionID, "--continue", message}
}

func ClaudeValidModels() []string {
	return []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"}
}

func (a *ClaudeAdapter) parseAssistantEvent(raw *claudeEvent, inputFields *struct {
	FilePath string `json:"file_path"`
	Command  string `json:"command"`
}, evt *types.HarnessEvent) *types.HarnessEvent {
	switch raw.Subtype {
	case "text":
		evt.Kind = types.EventProgress
		evt.Text = raw.Text
	case "tool_use":
		switch raw.Name {
		case "Read", "Glob", "Grep":
			evt.Kind = types.EventFileRead
			evt.FilePath = inputFields.FilePath
		case "Edit", "Write":
			evt.Kind = types.EventToolStart
			evt.Tool = raw.Name
			evt.FilePath = inputFields.FilePath
			a.storeToolInput(raw.ID, inputFields.FilePath)
		case "Bash":
			evt.Kind = types.EventCommandRun
			evt.Tool = raw.Name
			evt.Command = inputFields.Command
		default:
			evt.Kind = types.EventToolStart
			evt.Tool = raw.Name
		}
	case "tool_result":
		switch {
		case (raw.Name == "Edit" || raw.Name == "Write") && !raw.IsError:
			evt.Kind = types.EventFileWrite
			evt.FilePath = a.takeToolInput(raw.ID)
		default:
			evt.Kind = types.EventToolEnd
			evt.Tool = raw.Name
			if raw.Name == "Edit" || raw.Name == "Write" {
				a.takeToolInput(raw.ID)
			}
		}
	default:
		evt.Kind = types.EventRawPassthrough
	}
	return evt
}

func (a *ClaudeAdapter) storeToolInput(id string, filePath string) {
	if id == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.toolInputs == nil {
		a.toolInputs = make(map[string]string)
	}
	a.toolInputs[id] = filePath
}

func (a *ClaudeAdapter) takeToolInput(id string) string {
	if id == "" {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.toolInputs == nil {
		return ""
	}
	filePath := a.toolInputs[id]
	delete(a.toolInputs, id)
	return filePath
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

func claudeCostToTokens(cost *claudeCost) *types.TokenUsage {
	if cost == nil {
		return nil
	}
	return &types.TokenUsage{
		Input:     cost.InputTokens,
		Output:    cost.OutputTokens,
		Reasoning: cost.CacheReadTokens + cost.CacheWriteTokens,
	}
}
