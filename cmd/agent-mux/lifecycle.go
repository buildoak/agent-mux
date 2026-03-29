package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/recovery"
	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/store"
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

	records, err := store.ListRecords("", 0)
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

	idPrefix := strings.TrimSpace(fs.Args()[0])
	if err := sanitize.ValidateDispatchID(idPrefix); err != nil {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", idPrefix, err), "")
	}

	record, err := store.FindRecord("", idPrefix)
	if err != nil {
		return emitLifecycleError(stdout, 1, "lookup_failed", fmt.Sprintf("find dispatch %q: %v", idPrefix, err), "")
	}

	// If no store record exists, try reading live status from artifact dir.
	if record == nil {
		return statusFromLiveDispatch(idPrefix, jsonOutput, stdout)
	}

	// Dispatch is in the store (completed/failed/timed_out).
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
	fmt.Fprintf(tw, "Salt:\t%s\n", dashIfEmpty(record.Salt))
	fmt.Fprintf(tw, "ArtifactDir:\t%s\n", dashIfEmpty(record.ArtifactDir))
	_ = tw.Flush()
	return 0
}

func statusFromLiveDispatch(idPrefix string, jsonOutput bool, stdout io.Writer) int {
	artifactDir, err := recovery.ResolveArtifactDir(idPrefix)
	if err != nil {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no dispatch found for prefix %q", idPrefix), "")
	}

	liveStatus, err := dispatch.ReadStatusJSON(artifactDir)
	if err != nil {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no status found for dispatch %q", idPrefix), "")
	}

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

	idPrefix := strings.TrimSpace(fs.Args()[0])
	if err := sanitize.ValidateDispatchID(idPrefix); err != nil {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", idPrefix, err), "")
	}

	record, err := store.FindRecord("", idPrefix)
	if err != nil {
		return emitLifecycleError(stdout, 1, "lookup_failed", fmt.Sprintf("find dispatch %q: %v", idPrefix, err), "")
	}

	// If no record found, dispatch may still be running.
	if record == nil {
		artifactDir, resolveErr := recovery.ResolveArtifactDir(idPrefix)
		if resolveErr != nil {
			return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for prefix %q", idPrefix), "Use `agent-mux list` to find a durable dispatch ID.")
		}
		liveStatus, statusErr := dispatch.ReadStatusJSON(artifactDir)
		if statusErr == nil && (liveStatus.State == "running" || liveStatus.State == "initializing") {
			if noWait {
				writeCompactJSON(stdout, map[string]any{
					"error":       "dispatch_running",
					"dispatch_id": idPrefix,
					"state":       liveStatus.State,
				})
				return 1
			}
			// Block: poll status.json until dispatch completes.
			return pollUntilDone(idPrefix, artifactDir, jsonOutput, showArtifacts, stdout)
		}
	}

	dispatchID := idPrefix
	if record != nil && strings.TrimSpace(record.ID) != "" {
		dispatchID = record.ID
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

	response, err := store.ReadResult("", dispatchID)
	if err != nil {
		if !os.IsNotExist(err) {
			return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", dispatchID, err), "")
		}

		fallbackPath, fallbackErr := legacyFullOutputPath(dispatchID)
		if fallbackErr != nil {
			return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", dispatchID, fallbackErr), "")
		}
		data, fallbackErr := os.ReadFile(fallbackPath)
		if fallbackErr != nil {
			if os.IsNotExist(fallbackErr) && record == nil {
				return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for prefix %q", idPrefix), "Use `agent-mux list` to find a durable dispatch ID.")
			}
			if os.IsNotExist(fallbackErr) {
				return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for dispatch %q", dispatchID), "")
			}
			return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read fallback result %q: %v", fallbackPath, fallbackErr), "")
		}
		response = string(data)
	}

	if jsonOutput {
		writeCompactJSON(stdout, map[string]any{
			"dispatch_id": dispatchID,
			"response":    response,
		})
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

func filterRecordsByStatus(records []store.DispatchRecord, status string) []store.DispatchRecord {
	if strings.TrimSpace(status) == "" {
		return append([]store.DispatchRecord(nil), records...)
	}

	filtered := make([]store.DispatchRecord, 0, len(records))
	for _, record := range records {
		if record.Status == status {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func tailRecords(records []store.DispatchRecord, limit int) []store.DispatchRecord {
	if limit <= 0 || limit >= len(records) {
		return records
	}
	return records[len(records)-limit:]
}

func isValidDispatchStatus(status string) bool {
	switch status {
	case "completed", "failed", "timed_out":
		return true
	default:
		return false
	}
}

func writeRecordTable(w io.Writer, records []store.DispatchRecord) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSALT\tSTATUS\tENGINE\tMODEL\tDURATION\tCWD")
	for _, record := range records {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortDispatchID(record.ID),
			dashIfEmpty(record.Salt),
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

func filterRecordsByEngine(records []store.DispatchRecord, engine string) []store.DispatchRecord {
	if strings.TrimSpace(engine) == "" {
		return records
	}
	filtered := make([]store.DispatchRecord, 0, len(records))
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

	idPrefix := strings.TrimSpace(fs.Args()[0])
	if err := sanitize.ValidateDispatchID(idPrefix); err != nil {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", idPrefix, err), "")
	}

	record, err := store.FindRecord("", idPrefix)
	if err != nil {
		return emitLifecycleError(stdout, 1, "lookup_failed", fmt.Sprintf("find dispatch %q: %v", idPrefix, err), "")
	}
	if record == nil {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no dispatch found for prefix %q", idPrefix), "")
	}

	dispatchID := record.ID

	// Read result text.
	response, _ := store.ReadResult("", dispatchID)

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
	fmt.Fprintf(tw, "Salt:\t%s\n", dashIfEmpty(record.Salt))
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

// --- B4: gc subcommand ---

func runGCCommand(args []string, stdout io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux gc", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	olderThan := ""
	dryRun := false
	fs.StringVar(&olderThan, "older-than", "", "Delete dispatches older than this duration (e.g., 7d, 24h)")
	fs.BoolVar(&dryRun, "dry-run", false, "List what would be deleted without actually deleting")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 0 {
		return emitLifecycleError(stdout, 2, "invalid_args", "gc does not accept positional arguments", "Use --older-than and --dry-run flags.")
	}

	olderThan = strings.TrimSpace(olderThan)
	if olderThan == "" {
		return emitLifecycleError(stdout, 2, "invalid_args", "--older-than is required", "Example: agent-mux gc --older-than 7d")
	}

	dur, err := parseDuration(olderThan)
	if err != nil {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid duration %q: %v", olderThan, err), "Supported formats: Nd (days), Nh (hours). Example: 7d, 24h")
	}

	cutoff := time.Now().UTC().Add(-dur)

	records, err := store.ListRecords("", 0)
	if err != nil {
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("list dispatch records: %v", err), "")
	}

	var keep []store.DispatchRecord
	var remove []store.DispatchRecord
	for _, record := range records {
		started, parseErr := time.Parse(time.RFC3339, record.StartedAt)
		if parseErr != nil {
			keep = append(keep, record)
			continue
		}
		if started.Before(cutoff) {
			remove = append(remove, record)
		} else {
			keep = append(keep, record)
		}
	}

	if len(remove) == 0 {
		writeCompactJSON(stdout, map[string]any{
			"kind":    "gc",
			"removed": 0,
			"message": fmt.Sprintf("No dispatches older than %s found.", olderThan),
		})
		return 0
	}

	if dryRun {
		writeCompactJSON(stdout, map[string]any{
			"kind":      "gc_dry_run",
			"would_remove": len(remove),
			"dispatches": gcRecordSummaries(remove),
		})
		return 0
	}

	// Delete result files and artifact directories for removed records.
	storePath := store.DefaultStorePath()
	for _, record := range remove {
		if strings.TrimSpace(record.ID) != "" && strings.TrimSpace(storePath) != "" {
			resultPath := filepath.Join(storePath, "results", record.ID+".md")
			_ = os.Remove(resultPath)
		}
		if strings.TrimSpace(record.ArtifactDir) != "" {
			_ = os.RemoveAll(record.ArtifactDir)
		}
	}

	// Rewrite dispatches.jsonl with only the kept records.
	if err := store.RewriteRecords("", keep); err != nil {
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("rewrite dispatch index: %v", err), "")
	}

	writeCompactJSON(stdout, map[string]any{
		"kind":    "gc",
		"removed": len(remove),
		"kept":    len(keep),
		"cutoff":  cutoff.Format(time.RFC3339),
	})
	return 0
}

func gcRecordSummaries(records []store.DispatchRecord) []map[string]string {
	out := make([]map[string]string, 0, len(records))
	for _, r := range records {
		out = append(out, map[string]string{
			"id":      shortDispatchID(r.ID),
			"started": r.StartedAt,
			"engine":  dashIfEmpty(r.Engine),
			"status":  dashIfEmpty(r.Status),
		})
	}
	return out
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", numStr)
	}
	if n <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	switch unit {
	case 'd', 'D':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'h', 'H':
		return time.Duration(n) * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported unit %q: want d (days) or h (hours)", string(unit))
	}
}

// pollUntilDone polls status.json every 1s until the dispatch reaches a
// terminal state, then reads and returns the result from the store.
func pollUntilDone(idPrefix, artifactDir string, jsonOutput, showArtifacts bool, stdout io.Writer) int {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Check if a store record appeared (dispatch finished and persisted).
		record, _ := store.FindRecord("", idPrefix)
		if record != nil {
			// Dispatch completed. Delegate to normal result display.
			return showResult(record.ID, record, jsonOutput, showArtifacts, stdout)
		}

		// Check live status.
		liveStatus, err := dispatch.ReadStatusJSON(artifactDir)
		if err != nil {
			continue
		}
		switch liveStatus.State {
		case "completed", "failed", "timed_out", "orphaned":
			// Terminal state reached. Try store once more.
			record, _ = store.FindRecord("", idPrefix)
			if record != nil {
				return showResult(record.ID, record, jsonOutput, showArtifacts, stdout)
			}
			// No store record but terminal state — return status.
			writeCompactJSON(stdout, liveStatus)
			return 0
		}
		// Still running — keep polling.
	}
	return 1
}

// showResult returns the result for a completed dispatch (shared by result and wait).
func showResult(dispatchID string, record *store.DispatchRecord, jsonOutput, showArtifacts bool, stdout io.Writer) int {
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

	response, err := store.ReadResult("", dispatchID)
	if err != nil {
		if os.IsNotExist(err) {
			return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no stored result found for dispatch %q", dispatchID), "")
		}
		return emitLifecycleError(stdout, 1, "store_error", fmt.Sprintf("read result for dispatch %q: %v", dispatchID, err), "")
	}

	if jsonOutput {
		writeCompactJSON(stdout, map[string]any{
			"dispatch_id": dispatchID,
			"response":    response,
		})
		return 0
	}

	_, _ = io.WriteString(stdout, response)
	return 0
}

// runWaitCommand blocks until a dispatch completes, emitting periodic status lines.
func runWaitCommand(args []string, stdout, stderr io.Writer) int {
	var flagOutput bytes.Buffer
	fs := flag.NewFlagSet("agent-mux wait", flag.ContinueOnError)
	fs.SetOutput(&flagOutput)

	jsonOutput := false
	pollInterval := "5s"
	fs.BoolVar(&jsonOutput, "json", false, "Emit JSON result when done")
	fs.StringVar(&pollInterval, "poll", pollInterval, "Status line interval (e.g., 5s, 10s)")

	if err := fs.Parse(normalizeArgs(args)); err != nil {
		return handleLifecycleParseError(stdout, &flagOutput, err)
	}
	if len(fs.Args()) != 1 {
		return emitLifecycleError(stdout, 2, "invalid_args", "wait requires exactly one dispatch_id argument", "Pass a full dispatch ID or unique prefix.")
	}

	idPrefix := strings.TrimSpace(fs.Args()[0])
	if err := sanitize.ValidateDispatchID(idPrefix); err != nil {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid dispatch_id %q: %v", idPrefix, err), "")
	}

	interval, err := time.ParseDuration(pollInterval)
	if err != nil {
		return emitLifecycleError(stdout, 1, "invalid_input", fmt.Sprintf("invalid poll interval %q: %v", pollInterval, err), "Use Go duration format: 5s, 10s, 1m.")
	}
	if interval < 1*time.Second {
		interval = 1 * time.Second
	}

	// Check if already done.
	record, _ := store.FindRecord("", idPrefix)
	if record != nil {
		return showResult(record.ID, record, jsonOutput, false, stdout)
	}

	artifactDir, resolveErr := recovery.ResolveArtifactDir(idPrefix)
	if resolveErr != nil {
		return emitLifecycleError(stdout, 1, "not_found", fmt.Sprintf("no dispatch found for prefix %q", idPrefix), "")
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		// Check store first.
		record, _ = store.FindRecord("", idPrefix)
		if record != nil {
			return showResult(record.ID, record, jsonOutput, false, stdout)
		}

		liveStatus, statusErr := dispatch.ReadStatusJSON(artifactDir)
		if statusErr != nil {
			fmt.Fprintf(stderr, "[?] status unknown\n")
			continue
		}

		switch liveStatus.State {
		case "completed", "failed", "timed_out":
			// Terminal. Try store one more time.
			record, _ = store.FindRecord("", idPrefix)
			if record != nil {
				return showResult(record.ID, record, jsonOutput, false, stdout)
			}
			writeCompactJSON(stdout, liveStatus)
			return 0
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

