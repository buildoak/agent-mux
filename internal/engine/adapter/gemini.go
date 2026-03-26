package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/buildoak/agent-mux/internal/types"
)

type GeminiAdapter struct {
	mu           sync.Mutex
	pendingFiles map[string]string
}

type geminiEvent struct {
	Type       string          `json:"type"`
	SessionID  string          `json:"session_id"`
	Model      string          `json:"model"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolID     string          `json:"tool_id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	Output     string          `json:"output"`
	IsError    bool            `json:"is_error"`
	DurationMS int64           `json:"duration_ms"`
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Result     string          `json:"result"`
	Stats      *geminiStats    `json:"stats"`
}

type geminiStats struct {
	TotalTokens  int   `json:"total_tokens"`
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	DurationMS   int64 `json:"duration_ms"`
	ToolCalls    int   `json:"tool_calls"`
	Turns        int   `json:"turns"`
}

func (a *GeminiAdapter) Binary() string {
	return "gemini"
}

func (a *GeminiAdapter) BuildArgs(spec *types.DispatchSpec) []string {
	args := []string{"-p", spec.Prompt, "-o", "stream-json"}
	if spec.Model != "" {
		args = append(args, "-m", spec.Model)
	}
	approvalMode := "yolo"
	if mode, ok := spec.EngineOpts["permission-mode"].(string); ok && mode != "" {
		approvalMode = mode
	}
	args = append(args, "--approval-mode", approvalMode)
	return args
}

func (a *GeminiAdapter) EnvVars(spec *types.DispatchSpec) []string {
	if spec == nil || spec.SystemPrompt == "" || spec.ArtifactDir == "" {
		return nil
	}
	path := filepath.Join(spec.ArtifactDir, "system_prompt.md")
	if err := os.WriteFile(path, []byte(spec.SystemPrompt), 0644); err != nil {
		return nil
	}
	return []string{"GEMINI_SYSTEM_MD=" + path}
}

func (a *GeminiAdapter) ParseEvent(line string) (*types.HarnessEvent, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	if !strings.HasPrefix(line, "{") {
		return nil, nil
	}

	var raw geminiEvent
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, err
	}

	evt := &types.HarnessEvent{
		Timestamp: time.Now(),
		Raw:       []byte(line),
	}

	var inputFields struct {
		Path    string `json:"path"`
		Command string `json:"command"`
		Content string `json:"content"`
	}
	if len(raw.Input) > 0 {
		if err := json.Unmarshal(raw.Input, &inputFields); err != nil {
			return nil, err
		}
	}

	switch raw.Type {
	case "init":
		evt.Kind = types.EventSessionStart
		evt.SessionID = raw.SessionID
	case "message":
		if raw.Role != "assistant" {
			return nil, nil
		}
		evt.Kind = types.EventProgress
		evt.Text = raw.Content
	case "tool_use":
		return a.parseToolUse(&raw, &inputFields, evt), nil
	case "tool_result":
		return a.parseToolResult(&raw, evt), nil
	case "error":
		evt.Kind = types.EventError
		evt.ErrorCode = raw.Code
		evt.Text = raw.Message
	case "result":
		evt.Kind = types.EventResponse
		evt.SessionID = raw.SessionID
		evt.Text = raw.Result
		evt.Tokens = geminiStatsToTokens(raw.Stats)
	default:
		evt.Kind = types.EventRawPassthrough
	}

	return evt, nil
}

func (a *GeminiAdapter) parseToolUse(raw *geminiEvent, inputFields *struct {
	Path    string `json:"path"`
	Command string `json:"command"`
	Content string `json:"content"`
}, evt *types.HarnessEvent) *types.HarnessEvent {
	switch raw.Name {
	case "read_file":
		evt.Kind = types.EventFileRead
		evt.FilePath = inputFields.Path
	case "write_file":
		evt.Kind = types.EventToolStart
		evt.Tool = raw.Name
		a.mu.Lock()
		if a.pendingFiles == nil {
			a.pendingFiles = make(map[string]string)
		}
		a.pendingFiles[raw.ToolID] = inputFields.Path
		a.mu.Unlock()
	case "shell":
		evt.Kind = types.EventCommandRun
		evt.Tool = raw.Name
		evt.Command = inputFields.Command
	default:
		evt.Kind = types.EventToolStart
		evt.Tool = raw.Name
	}
	return evt
}

func (a *GeminiAdapter) parseToolResult(raw *geminiEvent, evt *types.HarnessEvent) *types.HarnessEvent {
	switch {
	case raw.Name == "write_file" && !raw.IsError:
		evt.Kind = types.EventFileWrite
		evt.Tool = raw.Name
		evt.DurationMS = raw.DurationMS
		evt.FilePath = a.takePendingFile(raw.ToolID)
	case raw.IsError:
		// Spec: tool_result with is_error=true → EventError (closest single-event mapping)
		evt.Kind = types.EventError
		evt.Tool = raw.Name
		evt.DurationMS = raw.DurationMS
		evt.ErrorCode = "tool_error"
		evt.Text = raw.Output
		if raw.Name == "write_file" {
			a.takePendingFile(raw.ToolID)
		}
	default:
		evt.Kind = types.EventToolEnd
		if raw.Name == "shell" {
			evt.SecondaryKind = types.EventCommandRun
		}
		evt.Tool = raw.Name
		evt.DurationMS = raw.DurationMS
	}
	return evt
}

func (a *GeminiAdapter) takePendingFile(toolID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingFiles == nil {
		return ""
	}
	path := a.pendingFiles[toolID]
	delete(a.pendingFiles, toolID)
	return path
}

func geminiStatsToTokens(stats *geminiStats) *types.TokenUsage {
	if stats == nil {
		return nil
	}
	return &types.TokenUsage{
		Input:  stats.InputTokens,
		Output: stats.OutputTokens,
	}
}

func (a *GeminiAdapter) SupportsResume() bool {
	return true
}

func (a *GeminiAdapter) ResumeArgs(sessionID string, message string) []string {
	return []string{"--resume", sessionID, "-p", message}
}
