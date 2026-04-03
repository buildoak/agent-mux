package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/fifo"
	"github.com/buildoak/agent-mux/internal/inbox"
	"github.com/buildoak/agent-mux/internal/sanitize"
)

const defaultNudgeMessage = "Please wrap up your current work and provide a final summary."

func runSteerCommand(args []string, stdout io.Writer) int {
	if len(args) < 2 {
		return emitSteerError(stdout, 2, "invalid_args",
			"steer requires: <dispatch_id> <action> [message/value]",
			"Actions: abort, nudge, redirect, extend, status")
	}

	ref := strings.TrimSpace(args[0])
	action := strings.TrimSpace(args[1])
	rest := args[2:]

	resolved, err := resolveDispatchReference(ref)
	if err != nil {
		if validateErr := sanitize.ValidateDispatchID(ref); validateErr != nil {
			return emitSteerError(stdout, 1, "invalid_input",
				fmt.Sprintf("invalid dispatch_id %q: %v", ref, validateErr), "")
		}
		return emitSteerError(stdout, 1, "not_found", err.Error(),
			"Use `agent-mux list` to find a valid dispatch ID.")
	}

	dispatchID := resolved.DispatchID
	if err := sanitize.ValidateDispatchID(ref); err != nil {
		return emitSteerError(stdout, 1, "invalid_input",
			fmt.Sprintf("invalid dispatch_id %q: %v", ref, err), "")
	}
	artifactDir := resolved.ArtifactDir

	switch action {
	case "abort":
		return steerAbort(dispatchID, artifactDir, stdout)
	case "nudge":
		return steerNudge(dispatchID, artifactDir, rest, stdout)
	case "redirect":
		return steerRedirect(dispatchID, artifactDir, rest, stdout)
	case "extend":
		return steerExtend(dispatchID, artifactDir, rest, stdout)
	case "status":
		return steerStatus(dispatchID, artifactDir, stdout)
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
	return deliverSteer(idPrefix, artifactDir, "nudge", message, stdout)
}

func steerRedirect(idPrefix, artifactDir string, rest []string, stdout io.Writer) int {
	if len(rest) == 0 {
		return emitSteerError(stdout, 2, "invalid_args",
			"redirect requires instructions",
			"Usage: ax steer <id> redirect \"focus on the tests, skip the refactor\"")
	}

	message := strings.Join(rest, " ")
	return deliverSteer(idPrefix, artifactDir, "redirect", message, stdout)
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

func emitSteerError(stdout io.Writer, exitCode int, code, message, suggestion string) int {
	writeCompactJSON(stdout, map[string]any{
		"kind":  "error",
		"error": dispatch.NewDispatchError(code, message, suggestion),
	})
	return exitCode
}

func deliverSteer(idPrefix, artifactDir, action, message string, stdout io.Writer) int {
	mechanism, delivered, err := tryFIFOInject(artifactDir, action, message)
	if err == nil && delivered {
		writeCompactJSON(stdout, map[string]any{
			"action":      action,
			"dispatch_id": idPrefix,
			"mechanism":   mechanism,
			"delivered":   true,
		})
		return 0
	}

	if err := writeInboxSteer(artifactDir, action, message); err != nil {
		return emitSteerError(stdout, 1, "write_failed",
			fmt.Sprintf("write inbox: %v", err), "")
	}
	writeCompactJSON(stdout, map[string]any{
		"action":      action,
		"dispatch_id": idPrefix,
		"mechanism":   "inbox",
		"delivered":   true,
	})
	return 0
}

func tryFIFOInject(artifactDir, action, message string) (mechanism string, delivered bool, err error) {
	meta, err := dispatch.ReadDispatchMeta(artifactDir)
	if err != nil || meta == nil || meta.Engine != "codex" {
		return "", false, err
	}
	status, err := dispatch.ReadStatusJSON(artifactDir)
	if err != nil || status == nil || status.State != "running" || !status.StdinPipeReady {
		return "", false, err
	}
	pid, err := dispatch.ReadHostPID(artifactDir)
	if err != nil || !dispatch.IsProcessAlive(pid) {
		return "", false, err
	}
	payload, err := adapter.EncodeSoftSteerEnvelope(action, message)
	if err != nil {
		return "", false, err
	}
	pipe, err := fifo.OpenWriteNonblock(fifo.Path(artifactDir))
	if err != nil {
		if errors.Is(err, fifo.ErrUnsupported) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENXIO) {
			return "", false, nil
		}
		return "", false, err
	}
	defer pipe.Close()
	if _, err := pipe.Write(payload); err != nil {
		if errors.Is(err, syscall.EPIPE) || errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return "stdin_fifo", true, nil
}

func writeInboxSteer(artifactDir, action, message string) error {
	switch action {
	case "redirect":
		return inbox.WriteInbox(artifactDir, "[REDIRECT] "+message)
	case "nudge":
		return inbox.WriteInbox(artifactDir, "[NUDGE] "+message)
	default:
		return inbox.WriteInbox(artifactDir, message)
	}
}
