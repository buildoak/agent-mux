//go:build axeval

package axeval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TraceVerdict captures behavioral analysis of an agent's execution trace.
type TraceVerdict struct {
	Case         string   `json:"case"`
	DispatchID   string   `json:"dispatch_id"`
	TraceSession string   `json:"trace_session"` // gaal session ID if found
	Pass         bool     `json:"pass"`
	Flags        []string `json:"flags"`      // behavioral flags
	Reasoning    string   `json:"reasoning"`
	CostUSD      float64  `json:"cost_usd"`
	TurnsUsed    int      `json:"turns_used"`
	ToolCalls    int      `json:"tool_calls"`
	ErrorCount   int      `json:"error_count"`
	FirstAction  string   `json:"first_action"` // what did agent try first
}

// TraceReport is the JSON structure written to trace-report.json.
type TraceReport struct {
	RunID     string         `json:"run_id"`
	Timestamp string         `json:"timestamp"`
	Verdicts  []TraceVerdict `json:"verdicts"`
	Summary   TraceSummary   `json:"summary"`
}

// TraceSummary provides aggregate stats across all trace verdicts.
type TraceSummary struct {
	Total       int      `json:"total"`
	Passed      int      `json:"passed"`
	Failed      int      `json:"failed"`
	Skipped     int      `json:"skipped"`
	CommonFlags []string `json:"common_flags"`
}

// traceEvent is a richer event parse for trace analysis (superset of Event).
type traceEvent struct {
	Type           string `json:"type"`
	Message        string `json:"message,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	SilenceSeconds int    `json:"silence_seconds,omitempty"`
	Status         string `json:"status,omitempty"`
	Timestamp      string `json:"ts,omitempty"`
	Tool           string `json:"tool,omitempty"`
	Args           string `json:"args,omitempty"`
	Path           string `json:"path,omitempty"`
	Command        string `json:"command,omitempty"`
	DurationMS     int    `json:"duration_ms,omitempty"`
	Engine         string `json:"engine,omitempty"`
	Model          string `json:"model,omitempty"`
}

// gaalSession holds structured data from gaal inspect --json.
type gaalSession struct {
	ID       string `json:"id"`
	Tokens   any    `json:"tokens,omitempty"`
	CostUSD  any    `json:"cost_usd,omitempty"`
	Duration any    `json:"duration,omitempty"`
}

// RunTraceVerification analyzes an agent's behavior trace after a test case completes.
func RunTraceVerification(binary string, caseName string, casePrompt string, result *Result) (*TraceVerdict, error) {
	verdict := &TraceVerdict{
		Case:  caseName,
		Flags: []string{},
	}

	// 1. Extract dispatch_id from the result stdout JSON.
	var raw map[string]any
	if err := json.Unmarshal(result.RawStdout, &raw); err != nil {
		return nil, fmt.Errorf("parse result stdout: %w", err)
	}
	dispatchID, _ := raw["dispatch_id"].(string)
	verdict.DispatchID = dispatchID

	// Extract metadata if available.
	if meta, ok := raw["metadata"].(map[string]any); ok {
		if tokens, ok := meta["tokens"].(map[string]any); ok {
			_ = tokens // available for future cost calculation
		}
		if turns, ok := meta["turns"].(float64); ok {
			verdict.TurnsUsed = int(turns)
		}
		if cost, ok := meta["cost_usd"].(float64); ok {
			verdict.CostUSD = cost
		}
	}

	// Extract activity if available.
	if activity, ok := raw["activity"].(map[string]any); ok {
		if toolCalls, ok := activity["tool_calls"].([]any); ok {
			verdict.ToolCalls = len(toolCalls)
		}
	}

	// 2. Parse events.jsonl from artifact dir for rich timeline.
	events := parseTraceEvents(result.ArtifactDir)

	// Count errors and identify first action from events.
	for _, evt := range events {
		if evt.Type == "error" {
			verdict.ErrorCount++
		}
	}
	verdict.FirstAction = identifyFirstAction(events)

	// 3. Try gaal search for the session (optional enrichment).
	if dispatchID != "" {
		traceToken := "AGENT_MUX_GO_" + dispatchID
		sessionID := searchGaalSession(traceToken)
		if sessionID != "" {
			verdict.TraceSession = sessionID
			// Try to get structured inspect data.
			enrichFromGaal(verdict, sessionID)
		}
	}

	// 4. Build the event timeline summary for the analyzer.
	timelineSummary := buildTimelineSummary(events)

	// 5. Dispatch Codex trace analyzer via agent-mux.
	analyzerResult, err := dispatchTraceAnalyzer(binary, caseName, casePrompt, timelineSummary, verdict)
	if err != nil {
		// Analyzer failed — still return what we have from deterministic analysis.
		verdict.Reasoning = fmt.Sprintf("analyzer dispatch failed: %v (deterministic analysis only)", err)
		verdict.Pass = verdict.ErrorCount == 0
		applyDeterministicFlags(verdict, events)
		return verdict, nil
	}

	// 6. Parse analyzer response into verdict.
	mergeAnalyzerResult(verdict, analyzerResult)

	return verdict, nil
}

// parseTraceEvents reads events.jsonl with richer field extraction.
func parseTraceEvents(artifactDir string) []traceEvent {
	eventsPath := filepath.Join(artifactDir, "events.jsonl")
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []traceEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt traceEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		events = append(events, evt)
	}
	return events
}

// identifyFirstAction finds the first meaningful action in the event timeline.
func identifyFirstAction(events []traceEvent) string {
	for _, evt := range events {
		switch evt.Type {
		case "tool_start":
			if evt.Tool != "" {
				return fmt.Sprintf("tool:%s", evt.Tool)
			}
			return "tool_call"
		case "command_run":
			if evt.Command != "" {
				return fmt.Sprintf("command:%s", evt.Command)
			}
			return "command"
		case "file_read":
			if evt.Path != "" {
				return fmt.Sprintf("read:%s", filepath.Base(evt.Path))
			}
			return "file_read"
		case "file_write":
			if evt.Path != "" {
				return fmt.Sprintf("write:%s", filepath.Base(evt.Path))
			}
			return "file_write"
		}
	}
	return "unknown"
}

// searchGaalSession tries to find a gaal session by trace token.
func searchGaalSession(traceToken string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gaal", "search", traceToken, "--limit", "1")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return ""
	}

	// gaal search returns a single JSON envelope: {"results": [...]}, not NDJSON.
	var envelope struct {
		Results []struct {
			SessionID string `json:"session_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return ""
	}
	if len(envelope.Results) > 0 {
		return envelope.Results[0].SessionID
	}
	return ""
}

// enrichFromGaal adds gaal inspect data to the verdict.
func enrichFromGaal(verdict *TraceVerdict, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gaal", "inspect", sessionID, "--tokens")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return
	}

	var session map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		return
	}

	// Extract cost if available from gaal.
	if cost, ok := session["cost_usd"].(float64); ok && cost > 0 {
		verdict.CostUSD = cost
	}
}

// buildTimelineSummary formats events into a readable timeline for the analyzer.
func buildTimelineSummary(events []traceEvent) string {
	if len(events) == 0 {
		return "(no events captured)"
	}

	var sb strings.Builder
	for i, evt := range events {
		if i >= 50 {
			sb.WriteString(fmt.Sprintf("... (%d more events truncated)\n", len(events)-50))
			break
		}

		ts := evt.Timestamp
		if ts == "" {
			ts = "?"
		}

		switch evt.Type {
		case "dispatch_start":
			sb.WriteString(fmt.Sprintf("[%s] START engine=%s model=%s\n", ts, evt.Engine, evt.Model))
		case "dispatch_end":
			sb.WriteString(fmt.Sprintf("[%s] END status=%s duration=%dms\n", ts, evt.Status, evt.DurationMS))
		case "tool_start":
			sb.WriteString(fmt.Sprintf("[%s] TOOL_START %s args=%s\n", ts, evt.Tool, truncateStr(evt.Args, 100)))
		case "tool_end":
			sb.WriteString(fmt.Sprintf("[%s] TOOL_END %s duration=%dms\n", ts, evt.Tool, evt.DurationMS))
		case "file_read":
			sb.WriteString(fmt.Sprintf("[%s] READ %s\n", ts, evt.Path))
		case "file_write":
			sb.WriteString(fmt.Sprintf("[%s] WRITE %s\n", ts, evt.Path))
		case "command_run":
			sb.WriteString(fmt.Sprintf("[%s] COMMAND %s\n", ts, truncateStr(evt.Command, 100)))
		case "error":
			sb.WriteString(fmt.Sprintf("[%s] ERROR code=%s msg=%s\n", ts, evt.ErrorCode, truncateStr(evt.Message, 100)))
		case "frozen_warning":
			sb.WriteString(fmt.Sprintf("[%s] FROZEN_WARNING silence=%ds\n", ts, evt.SilenceSeconds))
		case "heartbeat":
			// Skip heartbeats to keep summary compact.
		case "progress":
			sb.WriteString(fmt.Sprintf("[%s] PROGRESS %s\n", ts, truncateStr(evt.Message, 80)))
		default:
			sb.WriteString(fmt.Sprintf("[%s] %s %s\n", ts, evt.Type, truncateStr(evt.Message, 80)))
		}
	}
	return sb.String()
}

// dispatchTraceAnalyzer sends the trace to a Codex analyzer via agent-mux.
func dispatchTraceAnalyzer(binary string, caseName string, casePrompt string, timeline string, preVerdictData *TraceVerdict) (map[string]any, error) {
	analyzerPrompt := fmt.Sprintf(`You are evaluating an AI agent's behavior trace. The agent was given this task:
%s

Test case name: %s

Pre-extracted metrics:
- Turns used: %d
- Tool calls: %d
- Error count: %d
- First action: %s
- Duration: recorded by test harness

Event timeline (from events.jsonl):
%s

Evaluate:
1. First-attempt pattern: What did the agent try first? Was it the right approach?
2. Error handling: When errors occurred, did the agent correct course or repeat mistakes?
3. Completion quality: Did the agent accomplish the semantic intent?
4. Efficiency: How many turns/tools relative to task complexity?

Respond with ONLY valid JSON:
{"pass": true/false, "flags": ["list", "of", "behavioral", "flags"], "reasoning": "1-2 sentence explanation", "first_action": "what agent did first"}

Possible flags: "efficient", "wasteful", "good_first_attempt", "poor_first_attempt", "recovered_from_error", "error_spiral", "over_engineered", "under_delivered", "clean_completion"`,
		casePrompt, caseName,
		preVerdictData.TurnsUsed, preVerdictData.ToolCalls,
		preVerdictData.ErrorCount, preVerdictData.FirstAction,
		timeline)

	artifactDir, err := os.MkdirTemp("", "axeval-trace-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(artifactDir)

	spec := map[string]any{
		"engine":       "codex",
		"model":        "gpt-5.4-mini",
		"effort":       "high",
		"prompt":       analyzerPrompt,
		"cwd":          artifactDir,
		"artifact_dir": artifactDir,
		"skip_skills":  true,
		"timeout_sec":  90,
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "--stdin", "--yes")
	cmd.Stdin = bytes.NewReader(specJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("analyzer dispatch: %w (stderr: %s)", err, stderr.String())
	}

	// Parse dispatch result.
	var dispatchResult map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &dispatchResult); err != nil {
		return nil, fmt.Errorf("parse analyzer output: %w", err)
	}

	response, _ := dispatchResult["response"].(string)
	if response == "" {
		return nil, fmt.Errorf("analyzer returned empty response")
	}

	// Extract JSON from response (may be in code fences).
	jsonStr := extractJSON(response)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("parse analyzer verdict: %w (raw: %s)", err, truncateStr(response, 200))
	}

	return parsed, nil
}

// mergeAnalyzerResult merges the Codex analyzer's response into the verdict.
func mergeAnalyzerResult(verdict *TraceVerdict, analyzerResult map[string]any) {
	if pass, ok := analyzerResult["pass"].(bool); ok {
		verdict.Pass = pass
	}

	if flags, ok := analyzerResult["flags"].([]any); ok {
		for _, f := range flags {
			if s, ok := f.(string); ok {
				verdict.Flags = append(verdict.Flags, s)
			}
		}
	}

	if reasoning, ok := analyzerResult["reasoning"].(string); ok {
		verdict.Reasoning = reasoning
	}

	if firstAction, ok := analyzerResult["first_action"].(string); ok && verdict.FirstAction == "unknown" {
		verdict.FirstAction = firstAction
	}
}

// applyDeterministicFlags sets flags based on deterministic event analysis when the analyzer fails.
func applyDeterministicFlags(verdict *TraceVerdict, events []traceEvent) {
	toolCount := 0
	errorCount := 0
	for _, evt := range events {
		if evt.Type == "tool_start" || evt.Type == "tool_end" {
			toolCount++
		}
		if evt.Type == "error" {
			errorCount++
		}
	}

	if errorCount == 0 {
		verdict.Flags = append(verdict.Flags, "clean_completion")
	}
	if errorCount > 3 {
		verdict.Flags = append(verdict.Flags, "error_spiral")
	}
	if toolCount <= 6 {
		verdict.Flags = append(verdict.Flags, "efficient")
	}
	if toolCount > 20 {
		verdict.Flags = append(verdict.Flags, "wasteful")
	}
}

// truncateStr truncates a string to maxLen with ellipsis.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// writeTraceReport writes trace verdicts to trace-report.json.
func writeTraceReport(verdicts []TraceVerdict) error {
	dir := writeTraceReportDir()

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	summary := TraceSummary{
		Total: len(verdicts),
	}
	flagCounts := make(map[string]int)
	for _, v := range verdicts {
		if v.Pass {
			summary.Passed++
		} else {
			summary.Failed++
		}
		for _, f := range v.Flags {
			flagCounts[f]++
		}
	}
	// Common flags = appear in >50% of verdicts.
	for flag, count := range flagCounts {
		if count*2 >= len(verdicts) {
			summary.CommonFlags = append(summary.CommonFlags, flag)
		}
	}

	report := TraceReport{
		RunID:     fmt.Sprintf("trace-%d", time.Now().Unix()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Verdicts:  verdicts,
		Summary:   summary,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal trace report: %w", err)
	}

	path := filepath.Join(dir, "trace-report.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write trace report: %w", err)
	}

	fmt.Fprintf(os.Stderr, "ax-eval: trace report written to %s\n", path)
	return nil
}

// writeTraceReportDir returns the directory for trace reports.
func writeTraceReportDir() string {
	dir := os.Getenv("AX_EVAL_REPORT_DIR")
	if dir == "" {
		_, thisFile, _, ok := runtime.Caller(0)
		if ok {
			dir = filepath.Dir(thisFile)
		} else {
			dir = "."
		}
	}
	return dir
}
