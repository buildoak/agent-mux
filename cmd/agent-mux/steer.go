package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/buildoak/agent-mux/internal/config"
	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/engine/adapter"
	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/steer"
)

const defaultNudgeMessage = "Please wrap up your current work and provide a final summary."

func runSteerCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		return emitSteerError(stdout, 2, "invalid_args",
			"steer requires: <dispatch_id> <action> [message/value]",
			"Actions: abort, nudge, redirect")
	}

	ref := strings.TrimSpace(args[0])
	action := strings.TrimSpace(args[1])

	// Detect reversed argument order: "steer status <id>" → swap to canonical form.
	if isSteerAction(ref) && !isSteerAction(action) {
		ref, action = action, ref
	}

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
	default:
		return emitSteerError(stdout, 2, "invalid_args",
			fmt.Sprintf("unknown steer action %q", action),
			"Actions: abort, nudge, redirect")
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

// --- control file types and I/O ---

// ControlFile is the steering control structure written to control.json
// in the artifact directory. Read by the watchdog on each tick.
type ControlFile struct {
	Abort     bool      `json:"abort,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
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

	supported, engineName, supportErr := supportsInboxSteer(artifactDir)
	if supportErr != nil {
		return emitSteerError(stdout, 1, "steer_unsupported",
			supportErr.Error(),
			"Use `agent-mux steer <id> abort` to stop it, or wait for completion and rerun with updated instructions.")
	}
	if !supported {
		return emitSteerError(stdout, 1, "steer_unsupported",
			fmt.Sprintf("engine %q has no live steering channel and does not support resume-based inbox delivery", engineName),
			"Use `agent-mux steer <id> abort` to stop it, or wait for completion and rerun with updated instructions.")
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
	pipe, err := steer.OpenWriteNonblock(steer.Path(artifactDir))
	if err != nil {
		if errors.Is(err, steer.ErrUnsupported) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENXIO) {
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

func supportsInboxSteer(artifactDir string) (supported bool, engineName string, err error) {
	meta, err := dispatch.ReadDispatchMeta(artifactDir)
	if err != nil {
		return false, "", fmt.Errorf("read dispatch metadata: %w", err)
	}
	if meta == nil || strings.TrimSpace(meta.Engine) == "" {
		return false, "", fmt.Errorf("dispatch metadata does not identify an engine")
	}
	engineName = strings.TrimSpace(meta.Engine)

	reg := adapter.NewRegistry(config.DefaultModels())
	adp, err := reg.Get(engineName)
	if err != nil {
		return false, engineName, err
	}
	return adp.SupportsResume(), engineName, nil
}

func writeInboxSteer(artifactDir, action, message string) error {
	switch action {
	case "redirect":
		return steer.WriteInbox(artifactDir, "[REDIRECT] "+message)
	case "nudge":
		return steer.WriteInbox(artifactDir, "[NUDGE] "+message)
	default:
		return steer.WriteInbox(artifactDir, message)
	}
}

// isSteerAction returns true if s is one of the valid steer action verbs.
func isSteerAction(s string) bool {
	switch s {
	case "abort", "nudge", "redirect":
		return true
	}
	return false
}
