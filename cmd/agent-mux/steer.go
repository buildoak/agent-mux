package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/inbox"
	"github.com/buildoak/agent-mux/internal/recovery"
	"github.com/buildoak/agent-mux/internal/sanitize"
)

const defaultNudgeMessage = "Please wrap up your current work and provide a final summary."

func runSteerCommand(args []string, stdout io.Writer) int {
	if len(args) < 2 {
		return emitSteerError(stdout, 2, "invalid_args",
			"steer requires: <dispatch_id> <action> [message/value]",
			"Actions: abort, nudge, redirect, extend, status")
	}

	idPrefix := strings.TrimSpace(args[0])
	action := strings.TrimSpace(args[1])
	rest := args[2:]

	if err := sanitize.ValidateDispatchID(idPrefix); err != nil {
		return emitSteerError(stdout, 1, "invalid_input",
			fmt.Sprintf("invalid dispatch_id %q: %v", idPrefix, err), "")
	}

	artifactDir, err := resolveSteerArtifactDir(idPrefix)
	if err != nil {
		return emitSteerError(stdout, 1, "not_found",
			fmt.Sprintf("no dispatch found for prefix %q", idPrefix),
			"Use `agent-mux list` to find a valid dispatch ID.")
	}

	switch action {
	case "abort":
		return steerAbort(idPrefix, artifactDir, stdout)
	case "nudge":
		return steerNudge(idPrefix, artifactDir, rest, stdout)
	case "redirect":
		return steerRedirect(idPrefix, artifactDir, rest, stdout)
	case "extend":
		return steerExtend(idPrefix, artifactDir, rest, stdout)
	case "status":
		return steerStatus(idPrefix, artifactDir, stdout)
	default:
		return emitSteerError(stdout, 2, "invalid_args",
			fmt.Sprintf("unknown steer action %q", action),
			"Actions: abort, nudge, redirect, extend, status")
	}
}

func steerAbort(idPrefix, artifactDir string, stdout io.Writer) int {
	// Try SIGTERM to host PID first (async dispatches).
	pid, pidErr := dispatch.ReadHostPID(artifactDir)
	if pidErr == nil && dispatch.IsProcessAlive(pid) {
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)
			writeCompactJSON(stdout, map[string]any{
				"action":      "abort",
				"dispatch_id": idPrefix,
				"mechanism":   "sigterm",
				"pid":         pid,
				"delivered":   true,
			})
			return 0
		}
	}

	// PID dead or no host.pid — write control.json for foreground dispatches.
	cf := ControlFile{
		Abort:     true,
		UpdatedAt: time.Now().UTC(),
	}
	if err := writeControlFile(artifactDir, &cf); err != nil {
		return emitSteerError(stdout, 1, "write_failed",
			fmt.Sprintf("write control.json: %v", err), "")
	}

	writeCompactJSON(stdout, map[string]any{
		"action":      "abort",
		"dispatch_id": idPrefix,
		"mechanism":   "control_file",
		"delivered":   true,
	})
	return 0
}

func steerNudge(idPrefix, artifactDir string, rest []string, stdout io.Writer) int {
	message := defaultNudgeMessage
	if len(rest) > 0 {
		message = strings.Join(rest, " ")
	}

	if err := inbox.WriteInbox(artifactDir, message); err != nil {
		return emitSteerError(stdout, 1, "write_failed",
			fmt.Sprintf("write inbox: %v", err), "")
	}

	writeCompactJSON(stdout, map[string]any{
		"action":      "nudge",
		"dispatch_id": idPrefix,
		"delivered":   true,
	})
	return 0
}

func steerRedirect(idPrefix, artifactDir string, rest []string, stdout io.Writer) int {
	if len(rest) == 0 {
		return emitSteerError(stdout, 2, "invalid_args",
			"redirect requires instructions",
			"Usage: ax steer <id> redirect \"focus on the tests, skip the refactor\"")
	}

	message := strings.Join(rest, " ")
	if err := inbox.WriteInbox(artifactDir, message); err != nil {
		return emitSteerError(stdout, 1, "write_failed",
			fmt.Sprintf("write inbox: %v", err), "")
	}

	writeCompactJSON(stdout, map[string]any{
		"action":      "redirect",
		"dispatch_id": idPrefix,
		"delivered":   true,
	})
	return 0
}

func steerExtend(idPrefix, artifactDir string, rest []string, stdout io.Writer) int {
	if len(rest) == 0 {
		return emitSteerError(stdout, 2, "invalid_args",
			"extend requires a seconds value",
			"Usage: ax steer <id> extend 300")
	}

	seconds, err := strconv.Atoi(strings.TrimSpace(rest[0]))
	if err != nil || seconds <= 0 {
		return emitSteerError(stdout, 1, "invalid_input",
			fmt.Sprintf("invalid seconds value %q: must be a positive integer", rest[0]), "")
	}

	cf := ControlFile{
		ExtendKillSeconds: seconds,
		UpdatedAt:         time.Now().UTC(),
	}
	if err := writeControlFile(artifactDir, &cf); err != nil {
		return emitSteerError(stdout, 1, "write_failed",
			fmt.Sprintf("write control.json: %v", err), "")
	}

	writeCompactJSON(stdout, map[string]any{
		"action":      "extend",
		"dispatch_id": idPrefix,
		"seconds":     seconds,
		"delivered":   true,
	})
	return 0
}

func steerStatus(idPrefix, artifactDir string, stdout io.Writer) int {
	liveStatus, err := dispatch.ReadStatusJSON(artifactDir)
	if err != nil {
		return emitSteerError(stdout, 1, "not_found",
			fmt.Sprintf("no status found for dispatch %q", idPrefix), "")
	}

	// Check host process liveness.
	if liveStatus.State == "running" {
		pid, pidErr := dispatch.ReadHostPID(artifactDir)
		if pidErr == nil && !dispatch.IsProcessAlive(pid) {
			liveStatus.State = "orphaned"
		}
	}

	writeCompactJSON(stdout, liveStatus)
	return 0
}

// --- control file types and I/O ---

// ControlFile is the steering control structure written to control.json
// in the artifact directory. Read by the watchdog on each tick.
type ControlFile struct {
	Abort             bool      `json:"abort,omitempty"`
	ExtendKillSeconds int       `json:"extend_kill_seconds,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

const controlFileName = "control.json"

func writeControlFile(artifactDir string, cf *ControlFile) error {
	data, err := json.Marshal(cf)
	if err != nil {
		return fmt.Errorf("marshal control file: %w", err)
	}

	path := filepath.Join(artifactDir, controlFileName)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write control temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename control temp: %w", err)
	}
	return nil
}

// ReadControlFile reads control.json from the artifact directory.
// Returns nil if the file doesn't exist or is unreadable (cheap on miss).
func ReadControlFile(artifactDir string) *ControlFile {
	data, err := os.ReadFile(filepath.Join(artifactDir, controlFileName))
	if err != nil {
		return nil
	}
	var cf ControlFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil
	}
	return &cf
}

// --- helpers ---

func resolveSteerArtifactDir(idPrefix string) (string, error) {
	// Try recovery index first (covers both completed and in-progress).
	dir, err := recovery.ResolveArtifactDir(idPrefix)
	if err == nil {
		return dir, nil
	}
	// Fall back to default artifact dir construction.
	return recovery.DefaultArtifactDir(idPrefix)
}

func emitSteerError(stdout io.Writer, exitCode int, code, message, suggestion string) int {
	writeCompactJSON(stdout, map[string]any{
		"kind":  "error",
		"error": dispatch.NewDispatchError(code, message, suggestion),
	})
	return exitCode
}
