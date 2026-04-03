package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/recovery"
	"github.com/buildoak/agent-mux/internal/sanitize"
)

const listDefaultLimit = 20

func runListCommand(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux list", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	limit := listDefaultLimit
	statusFilter := ""
	engineFilter := ""
	jsonOutput := false

	fs.IntVar(&limit, "limit", limit, "Maximum records to print (0 = all)")
	fs.StringVar(&statusFilter, "status", "", "Filter by status: completed, failed, timed_out")
	fs.StringVar(&engineFilter, "engine", "", "Filter by engine: codex, claude, gemini")
	fs.BoolVar(&jsonOutput, "json", false, "Emit NDJSON")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 0 {
		return emitLifecycleError(stdout, 2, "invalid_args", "list does not accept positional arguments", "Use --limit, --status, or --json flags only.")
	}
	if limit < 0 {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid limit %d: must be >= 0", limit), "")
	}

	statusFilter = strings.TrimSpace(statusFilter)
	if statusFilter != "" && !isValidDispatchStatus(statusFilter) {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid status %q: want completed, failed, or timed_out", statusFilter), "")
	}
	engineFilter = strings.TrimSpace(engineFilter)
	if engineFilter != "" && !isValidEngine(engineFilter) {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid engine %q: want codex, claude, or gemini", engineFilter), "")
	}

	records, err := dispatch.ListDispatchRecords(0)
	if err != nil {
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("list dispatch records: %v", err), "")
	}

	filtered := filterRecordsByStatus(records, statusFilter)
	filtered = filterRecordsByEngine(filtered, engineFilter)
	filtered = tailRecords(filtered, limit)

	if jsonOutput {
		for _, record := range filtered {
			writeCompactJSON(stdout, record)
		}
		return 0
	}

	writeRecordTable(stdout, filtered)
	return 0
}

func runStatusCommand(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux status", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	jsonOutput := false
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 1 {
		return emitLifecycleError(stdout, 2, "invalid_args", "status requires exactly one dispatch_id argument", "Pass a full dispatch ID or unique prefix.")
	}

	ref := strings.TrimSpace(fs.Args()[0])
	resolved, err := resolveDispatchReference(ref)
	if err != nil {
		if validateErr := sanitize.ValidateDispatchID(ref); validateErr != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", ref, validateErr), "")
		}
		return emitLifecycleError(stdout, 1, "not_found", err.Error(), "")
	}
	record := resolved.Record

	if record == nil || strings.TrimSpace(record.Status) == "" {
		return statusFromLiveDispatch(resolved, jsonOutput, stdout)
	}

	if jsonOutput {
		writeCompactJSON(stdout, record)
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Status:\t%s\n", dashIfEmpty(record.Status))
	fmt.Fprintf(tw, "Engine/Model:\t%s\n", formatEngineModel(record.Engine, record.Model))
	fmt.Fprintf(tw, "Duration:\t%s\n", formatDuration(record.DurationMs))
	fmt.Fprintf(tw, "Started:\t%s\n", dashIfEmpty(record.StartedAt))
	fmt.Fprintf(tw, "Truncated:\t%t\n", record.Truncated)
	fmt.Fprintf(tw, "ArtifactDir:\t%s\n", dashIfEmpty(record.ArtifactDir))
	_ = tw.Flush()
	return 0
}

func statusFromLiveDispatch(resolved *dispatchRefResolution, jsonOutput bool, stdout io.Writer) int {
	artifactDir := resolved.ArtifactDir
	liveStatus, err := dispatch.ReadStatusJSON(artifactDir)
	if err != nil {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no status found for dispatch %q", resolved.DispatchID), "")
	}
	liveStatus.DispatchID = firstNonEmptyString(strings.TrimSpace(liveStatus.DispatchID), resolved.DispatchID)
	liveStatus.SessionID = firstNonEmptyString(strings.TrimSpace(liveStatus.SessionID), sessionIDFromArtifacts(artifactDir))

	// Check if host process is still alive.
	if liveStatus.State == "running" {
		pid, pidErr := dispatch.ReadHostPID(artifactDir)
		if pidErr == nil && !dispatch.IsProcessAlive(pid) {
			liveStatus.State = "orphaned"
		}
	}

	if jsonOutput {
		writeCompactJSON(stdout, liveStatus)
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "State:\t%s\n", liveStatus.State)
	fmt.Fprintf(tw, "Elapsed:\t%ds\n", liveStatus.ElapsedS)
	fmt.Fprintf(tw, "Last Activity:\t%s\n", dashIfEmpty(liveStatus.LastActivity))
	fmt.Fprintf(tw, "Tools Used:\t%d\n", liveStatus.ToolsUsed)
	fmt.Fprintf(tw, "Files Changed:\t%d\n", liveStatus.FilesChanged)
	fmt.Fprintf(tw, "ArtifactDir:\t%s\n", artifactDir)
	_ = tw.Flush()
	return 0
}

func runResultCommand(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux result", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	jsonOutput := false
	showArtifacts := false
	noWait := false
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON")
	fs.BoolVar(&showArtifacts, "artifacts", false, "List artifact files instead of showing result text")
	fs.BoolVar(&noWait, "no-wait", false, "Return error if dispatch is not done instead of blocking")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 1 {
		return emitLifecycleError(stdout, 2, "invalid_args", "result requires exactly one dispatch_id argument", "Pass a full dispatch ID or unique prefix.")
	}

	ref := strings.TrimSpace(fs.Args()[0])
	resolved, err := resolveDispatchReference(ref)
	if err != nil {
		if validateErr := sanitize.ValidateDispatchID(ref); validateErr != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", ref, validateErr), "")
		}
		return emitLifecycleError(stdout, 1, "not_found", err.Error(), "Use `agent-mux list` to find a durable dispatch ID.")
	}
	record := resolved.Record
	dispatchID := resolved.DispatchID
	if record != nil && strings.TrimSpace(record.ID) != "" {
		dispatchID = record.ID
	}

	persistedResult, persistedErr := dispatch.ReadPersistentResult(dispatchID)
	if persistedErr != nil && !os.IsNotExist(persistedErr) {
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", dispatchID, persistedErr), "")
	}

	if persistedResult == nil {
		artifactDir := resolved.ArtifactDir
		liveStatus, statusErr := dispatch.ReadStatusJSON(artifactDir)
		if statusErr == nil && (liveStatus.State == "running" || liveStatus.State == "initializing") {
			if noWait {
				writeCompactJSON(stdout, map[string]any{
					"error":       "dispatch_running",
					"dispatch_id": resolved.DispatchID,
					"session_id":  liveStatus.SessionID,
					"state":       liveStatus.State,
				})
				return 1
			}
			return pollUntilDone(resolved, jsonOutput, showArtifacts, stdout)
		}
	}

	if showArtifacts {
		artifactDir := ""
		if record != nil && strings.TrimSpace(record.ArtifactDir) != "" {
			artifactDir = record.ArtifactDir
		} else {
			resolved, resolveErr := recovery.ResolveArtifactDir(dispatchID)
			if resolveErr != nil {
				return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no artifact directory found for dispatch %q: %v", dispatchID, resolveErr), "")
			}
			artifactDir = resolved
		}
		artifacts := dispatch.ScanArtifacts(artifactDir)
		if jsonOutput {
			writeCompactJSON(stdout, map[string]any{
				"dispatch_id":  dispatchID,
				"artifact_dir": artifactDir,
				"artifacts":    artifacts,
			})
			return 0
		}
		fmt.Fprintf(stdout, "Artifact dir: %s\n", artifactDir)
		if len(artifacts) == 0 {
			fmt.Fprintln(stdout, "(no artifacts)")
		} else {
			for _, a := range artifacts {
				fmt.Fprintln(stdout, a)
			}
		}
		return 0
	}

	response := ""
	if persistedResult != nil {
		response = persistedResult.Response
	} else {
		fallbackPath, fallbackErr := legacyFullOutputPath(dispatchID)
		if fallbackErr != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", dispatchID, fallbackErr), "")
		}
		data, fallbackErr := os.ReadFile(fallbackPath)
		if fallbackErr != nil {
			if os.IsNotExist(fallbackErr) && record == nil {
				return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for reference %q", ref), "Use `agent-mux list` to find a durable dispatch ID.")
			}
			if os.IsNotExist(fallbackErr) {
				return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for dispatch %q", dispatchID), "")
			}
			return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read fallback result %q: %v", fallbackPath, fallbackErr), "")
		}
		response = string(data)
	}

	if jsonOutput {
		result := map[string]any{
			"dispatch_id": dispatchID,
			"response":    response,
		}
		enrichResultStatus(result, record, dispatchID)
		writeCompactJSON(stdout, result)
		return 0
	}

	_, _ = io.WriteString(stdout, response)
	return 0
}

func handleLifecycleParseError(stdout io.Writer, flagOutput *bytes.Buffer, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		emitResult(stdout, map[string]any{
			"kind":  "help",
			"usage": strings.TrimSpace(flagOutput.String()),
		})
		return 0
	}
	return emitLifecycleError(stdout, 2, "invalid_args", err.Error(), strings.TrimSpace(flagOutput.String()))
}

func emitLifecycleError(stdout io.Writer, exitCode int, code, message, suggestion string) int {
	emitResult(stdout, map[string]any{
		"kind":  "error",
		"error": dispatch.NewDispatchError(code, message, suggestion),
	})
	return exitCode
}

func filterRecordsByStatus(records []dispatch.DispatchRecord, status string) []dispatch.DispatchRecord {
	if strings.TrimSpace(status) == "" {
		return append([]dispatch.DispatchRecord(nil), records...)
	}

	filtered := make([]dispatch.DispatchRecord, 0, len(records))
	for _, record := range records {
		if record.Status == status {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func tailRecords(records []dispatch.DispatchRecord, limit int) []dispatch.DispatchRecord {
	if limit <= 0 || limit >= len(records) {
		return records
	}
	return records[:limit]
}

func isValidDispatchStatus(status string) bool {
	switch status {
	case "completed", "failed", "timed_out":
		return true
	default:
		return false
	}
}

func writeRecordTable(w io.Writer, records []dispatch.DispatchRecord) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tENGINE\tMODEL\tDURATION\tCWD")
	for _, record := range records {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			shortDispatchID(record.ID),
			dashIfEmpty(record.Status),
			dashIfEmpty(record.Engine),
			dashIfEmpty(record.Model),
			formatDuration(record.DurationMs),
			truncateMiddle(record.Cwd, 48),
		)
	}
	_ = tw.Flush()
}

func shortDispatchID(id string) string {
	runes := []rune(strings.TrimSpace(id))
	if len(runes) <= 12 {
		return string(runes)
	}
	return string(runes[:12])
}

func truncateMiddle(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return "-"
	}
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}

	headLen := (maxRunes - 3) / 2
	tailLen := maxRunes - 3 - headLen
	if headLen == 0 {
		headLen = 1
		tailLen = maxRunes - 4
	}
	return string(runes[:headLen]) + "..." + string(runes[len(runes)-tailLen:])
}

func formatDuration(durationMS int64) string {
	switch {
	case durationMS <= 0:
		return "-"
	case durationMS < 1000:
		return fmt.Sprintf("%dms", durationMS)
	default:
		return fmt.Sprintf("%ds", durationMS/1000)
	}
}

func formatEngineModel(engine, model string) string {
	engine = strings.TrimSpace(engine)
	model = strings.TrimSpace(model)
	switch {
	case engine == "" && model == "":
		return "-"
	case model == "":
		return engine
	case engine == "":
		return model
	default:
		return engine + " / " + model
	}
}

func dashIfEmpty(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func legacyFullOutputPath(dispatchID string) (string, error) {
	artifactDir, err := recovery.DefaultArtifactDir(dispatchID)
	if err != nil {
		return "", err
	}
	return filepath.Join(artifactDir, "full_output.md"), nil
}

// --- B1: engine filter ---

func filterRecordsByEngine(records []dispatch.DispatchRecord, engine string) []dispatch.DispatchRecord {
	if strings.TrimSpace(engine) == "" {
		return records
	}
	filtered := make([]dispatch.DispatchRecord, 0, len(records))
	for _, record := range records {
		if record.Engine == engine {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func isValidEngine(engine string) bool {
	switch engine {
	case "codex", "claude", "gemini":
		return true
	default:
		return false
	}
}

// --- B3: inspect subcommand ---

func runInspectCommand(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux inspect", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	jsonOutput := false
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 1 {
		return emitLifecycleError(stdout, 2, "invalid_args", "inspect requires exactly one dispatch_id argument", "Pass a full dispatch ID or unique prefix.")
	}

	ref := strings.TrimSpace(fs.Args()[0])
	resolved, err := resolveDispatchReference(ref)
	if err != nil {
		if validateErr := sanitize.ValidateDispatchID(ref); validateErr != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", ref, validateErr), "")
		}
		return emitLifecycleError(stdout, 1, "not_found", err.Error(), "")
	}
	record := resolved.Record
	if record == nil {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no dispatch found for reference %q", ref), "")
	}

	dispatchID := record.ID

	persistedResult, _ := dispatch.ReadPersistentResult(dispatchID)
	response := ""
	if persistedResult != nil {
		response = persistedResult.Response
	}

	// Resolve artifact directory and list contents.
	artifactDir := record.ArtifactDir
	if strings.TrimSpace(artifactDir) == "" {
		if resolved, resolveErr := recovery.ResolveArtifactDir(dispatchID); resolveErr == nil {
			artifactDir = resolved
		}
	}
	var artifacts []string
	if strings.TrimSpace(artifactDir) != "" {
		artifacts = dispatch.ScanArtifacts(artifactDir)
	}

	// Read dispatch meta if available.
	var meta *dispatch.DispatchMeta
	if strings.TrimSpace(artifactDir) != "" {
		if m, mErr := dispatch.ReadDispatchMeta(artifactDir); mErr == nil {
			meta = m
		}
	}

	if jsonOutput {
		result := map[string]any{
			"dispatch_id":  dispatchID,
			"session_id":   firstNonEmptyString(record.SessionID, sessionIDFromArtifacts(artifactDir)),
			"record":       record,
			"response":     response,
			"artifact_dir": artifactDir,
			"artifacts":    artifacts,
		}
		if meta != nil {
			result["meta"] = meta
		}
		writeCompactJSON(stdout, result)
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Dispatch ID:\t%s\n", dispatchID)
	fmt.Fprintf(tw, "Status:\t%s\n", dashIfEmpty(record.Status))
	fmt.Fprintf(tw, "Engine:\t%s\n", dashIfEmpty(record.Engine))
	fmt.Fprintf(tw, "Model:\t%s\n", dashIfEmpty(record.Model))
	fmt.Fprintf(tw, "Role:\t%s\n", dashIfEmpty(record.Role))
	fmt.Fprintf(tw, "Variant:\t%s\n", dashIfEmpty(record.Variant))
	fmt.Fprintf(tw, "Started:\t%s\n", dashIfEmpty(record.StartedAt))
	fmt.Fprintf(tw, "Ended:\t%s\n", dashIfEmpty(record.EndedAt))
	fmt.Fprintf(tw, "Duration:\t%s\n", formatDuration(record.DurationMs))
	fmt.Fprintf(tw, "Truncated:\t%t\n", record.Truncated)
	fmt.Fprintf(tw, "Cwd:\t%s\n", dashIfEmpty(record.Cwd))
	fmt.Fprintf(tw, "ArtifactDir:\t%s\n", dashIfEmpty(artifactDir))
	_ = tw.Flush()

	if len(artifacts) > 0 {
		fmt.Fprintln(stdout, "\nArtifacts:")
		for _, a := range artifacts {
			fmt.Fprintf(stdout, "  %s\n", a)
		}
	}

	if strings.TrimSpace(response) != "" {
		fmt.Fprintln(stdout, "\n--- Response ---")
		_, _ = io.WriteString(stdout, response)
		if !strings.HasSuffix(response, "\n") {
			fmt.Fprintln(stdout)
		}
	}

	return 0
}

// pollUntilDone polls status.json every 1s until the dispatch reaches a
// terminal state, then reads and returns the result from the dispatch dir.
func pollUntilDone(resolved *dispatchRefResolution, jsonOutput, showArtifacts bool, stdout io.Writer) int {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		record, _ := dispatch.FindDispatchRecordByRef(resolved.InputRef)
		if record != nil {
			if strings.TrimSpace(record.Status) != "" {
				return showResult(record.ID, record, jsonOutput, showArtifacts, stdout)
			}
		}

		liveStatus, err := dispatch.ReadStatusJSON(resolved.ArtifactDir)
		if err != nil {
			continue
		}
		switch liveStatus.State {
		case "completed", "failed", "timed_out", "orphaned":
			record, _ = dispatch.FindDispatchRecordByRef(resolved.InputRef)
			if record != nil && strings.TrimSpace(record.Status) != "" {
				return showResult(record.ID, record, jsonOutput, showArtifacts, stdout)
			}
			liveStatus.DispatchID = firstNonEmptyString(strings.TrimSpace(liveStatus.DispatchID), resolved.DispatchID)
			liveStatus.SessionID = firstNonEmptyString(strings.TrimSpace(liveStatus.SessionID), sessionIDFromArtifacts(resolved.ArtifactDir))
			writeCompactJSON(stdout, liveStatus)
			return 0
		}
	}
	return 1
}

func showResult(dispatchID string, record *dispatch.DispatchRecord, jsonOutput, showArtifacts bool, stdout io.Writer) int {
	if showArtifacts {
		artifactDir := ""
		if record != nil && strings.TrimSpace(record.ArtifactDir) != "" {
			artifactDir = record.ArtifactDir
		} else {
			resolved, resolveErr := recovery.ResolveArtifactDir(dispatchID)
			if resolveErr != nil {
				return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no artifact directory found for dispatch %q: %v", dispatchID, resolveErr), "")
			}
			artifactDir = resolved
		}
		artifacts := dispatch.ScanArtifacts(artifactDir)
		if jsonOutput {
			writeCompactJSON(stdout, map[string]any{
				"dispatch_id":  dispatchID,
				"session_id":   record.SessionID,
				"artifact_dir": artifactDir,
				"artifacts":    artifacts,
			})
			return 0
		}
		fmt.Fprintf(stdout, "Artifact dir: %s\n", artifactDir)
		if len(artifacts) == 0 {
			fmt.Fprintln(stdout, "(no artifacts)")
		} else {
			for _, a := range artifacts {
				fmt.Fprintln(stdout, a)
			}
		}
		return 0
	}

	response, err := dispatch.ReadResult(dispatchID)
	if err != nil {
		if os.IsNotExist(err) {
			return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for dispatch %q", dispatchID), "")
		}
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", dispatchID, err), "")
	}

	if jsonOutput {
		result := map[string]any{
			"dispatch_id": dispatchID,
			"session_id":  record.SessionID,
			"response":    response,
		}
		enrichResultStatus(result, record, dispatchID)
		writeCompactJSON(stdout, result)
		return 0
	}

	_, _ = io.WriteString(stdout, response)
	return 0
}

// runWaitCommand blocks until a dispatch completes, emitting periodic status lines.
//
// Poll interval precedence: CLI --poll flag > config.toml [async].poll_interval > 60s default.
func runWaitCommand(args []string, stdout, stderr io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux wait", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	jsonOutput := false
	pollInterval := ""
	configPath := ""
	cwd := ""
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON result when done")
	fs.StringVar(&pollInterval, "poll", "", "Status poll interval (e.g., 60s, 5m). Default: 60s or [async].poll_interval from config")
	fs.StringVar(&configPath, "config", "", "Override config path")
	fs.StringVar(&cwd, "cwd", "", "Working directory for project config discovery")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 1 {
		return emitLifecycleError(stdout, 2, "invalid_args", "wait requires exactly one dispatch_id argument", "Pass a full dispatch ID or unique prefix.")
	}

	ref := strings.TrimSpace(fs.Args()[0])
	resolved, err := resolveDispatchReference(ref)
	if err != nil {
		if validateErr := sanitize.ValidateDispatchID(ref); validateErr != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", ref, validateErr), "")
		}
		return emitLifecycleError(stdout, 1, "not_found", err.Error(), "")
	}

	// Resolve poll interval: CLI flag > config > hardcoded default.
	interval := config.DefaultAsyncPollInterval
	if strings.TrimSpace(pollInterval) == "" {
		// No CLI flag — try config.
		cfg, _ := config.LoadConfig(configPath, cwd)
		interval = config.AsyncPollInterval(cfg)
	} else {
		var err error
		interval, err = time.ParseDuration(pollInterval)
		if err != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid poll interval %q: %v", pollInterval, err), "Use Go duration format: 5s, 10s, 1m.")
		}
	}
	if interval < 1*time.Second {
		interval = 1 * time.Second
	}

	// Check if already done.
	record := resolved.Record
	if record != nil {
		if _, err := dispatch.ReadPersistentResult(record.ID); err == nil {
			return showResult(record.ID, record, jsonOutput, false, stdout)
		} else if err != nil && !os.IsNotExist(err) {
			return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", record.ID, err), "")
		}
	}

	artifactDir := resolved.ArtifactDir
	if artifactDir == "" {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no dispatch found for reference %q", ref), "")
	}

	deadline := time.Time{}
	if record != nil && record.TimeoutSec > 0 {
		if startedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(record.StartedAt)); err == nil {
			deadline = startedAt.Add(time.Duration(record.TimeoutSec) * time.Second)
		}
	}

	if result, err := dispatch.ReadPersistentResult(resolved.DispatchID); err == nil && result != nil {
		return showResult(record.ID, record, jsonOutput, false, stdout)
	} else if err != nil && !os.IsNotExist(err) {
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", resolved.DispatchID, err), "")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		// Completion is defined by result.json, not by the presence of metadata.
		record, _ = dispatch.FindDispatchRecordByRef(resolved.InputRef)
		if result, err := dispatch.ReadPersistentResult(resolved.DispatchID); err == nil && result != nil {
			if record == nil {
				record = resolved.Record
			}
			return showResult(resolved.DispatchID, record, jsonOutput, false, stdout)
		} else if err != nil && !os.IsNotExist(err) {
			return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", resolved.DispatchID, err), "")
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			return emitLifecycleError(stdout, 1, "timed_out", fmt.Sprintf("dispatch %q timed out before result.json was written", resolved.DispatchID), "")
		}

		if pid, err := dispatch.ReadHostPID(artifactDir); err == nil && !dispatch.IsProcessAlive(pid) {
			return emitLifecycleError(stdout, 1, "failed", fmt.Sprintf("dispatch %q stopped before result.json was written", resolved.DispatchID), "")
		}

		liveStatus, statusErr := dispatch.ReadStatusJSON(artifactDir)
		if statusErr != nil {
			fmt.Fprintf(stderr, "[?] status unknown\n")
			continue
		}

		switch liveStatus.State {
		case "timed_out":
			return emitLifecycleError(stdout, 1, "timed_out", fmt.Sprintf("dispatch %q timed out before result.json was written", resolved.DispatchID), "")
		case "orphaned":
			writeCompactJSON(stdout, liveStatus)
			return 1
		default:
			// Emit status line to stderr.
			fmt.Fprintf(stderr, "[%ds] %s | %d tools | %d files changed\n",
				liveStatus.ElapsedS, liveStatus.State, liveStatus.ToolsUsed, liveStatus.FilesChanged)
		}
	}
	return 1
}

// enrichResultStatus adds "status" and optionally "kill_reason" fields to a
// result --json map. Status is derived from the store record, the dispatch
// meta, or the events log (in that order of priority). This closes B-7:
// machine consumers can distinguish completed/failed/killed/timeout without
// parsing free-form logs.
func enrichResultStatus(result map[string]any, record *dispatch.DispatchRecord, dispatchID string) {
	if record != nil && strings.TrimSpace(record.Status) != "" {
		result["status"] = record.Status
		if strings.TrimSpace(record.SessionID) != "" {
			result["session_id"] = record.SessionID
		}
		// Check for kill reason from artifact events.
		if record.Status == "failed" {
			if reason := extractKillReason(record.ArtifactDir); reason != "" {
				result["kill_reason"] = reason
			}
		}
		return
	}

	// 2. Dispatch meta from artifact dir.
	artifactDir := ""
	if record != nil {
		artifactDir = record.ArtifactDir
	}
	if strings.TrimSpace(artifactDir) == "" {
		if resolved, err := recovery.ResolveArtifactDir(dispatchID); err == nil {
			artifactDir = resolved
		}
	}
	if strings.TrimSpace(artifactDir) != "" {
		if meta, err := dispatch.ReadDispatchMeta(artifactDir); err == nil && meta.Status != "" {
			result["status"] = meta.Status
			if strings.TrimSpace(meta.SessionID) != "" {
				result["session_id"] = meta.SessionID
			}
			if meta.Status == "failed" {
				if reason := extractKillReason(artifactDir); reason != "" {
					result["kill_reason"] = reason
				}
			}
			return
		}
		// 3. Fall back to status.json state.
		if live, err := dispatch.ReadStatusJSON(artifactDir); err == nil && live.State != "" {
			result["status"] = live.State
			if strings.TrimSpace(live.SessionID) != "" {
				result["session_id"] = live.SessionID
			}
			return
		}
	}
}

func sessionIDFromArtifacts(artifactDir string) string {
	if strings.TrimSpace(artifactDir) == "" {
		return ""
	}
	if meta, err := dispatch.ReadDispatchMeta(artifactDir); err == nil && meta != nil && strings.TrimSpace(meta.SessionID) != "" {
		return meta.SessionID
	}
	if live, err := dispatch.ReadStatusJSON(artifactDir); err == nil && live != nil {
		return strings.TrimSpace(live.SessionID)
	}
	return ""
}

// extractKillReason scans events.jsonl for kill-related error codes
// (frozen_killed, signal_killed, startup_failed) and returns the first found.
func extractKillReason(artifactDir string) string {
	if strings.TrimSpace(artifactDir) == "" {
		return ""
	}
	eventsPath := filepath.Join(artifactDir, "events.jsonl")
	f, err := os.Open(eventsPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	killCodes := map[string]bool{
		"frozen_killed":  true,
		"signal_killed":  true,
		"startup_failed": true,
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt struct {
			Type      string `json:"type"`
			ErrorCode string `json:"error_code"`
		}
		if json.Unmarshal(line, &evt) != nil {
			continue
		}
		if evt.Type == "error" && killCodes[evt.ErrorCode] {
			return evt.ErrorCode
		}
		// Also check event type directly (e.g. frozen_killed is emitted as a type).
		if killCodes[evt.Type] {
			return evt.Type
		}
	}
	return ""
}
