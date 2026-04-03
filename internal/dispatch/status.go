package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// LiveStatus represents the current state of a running dispatch,
// written atomically to status.json in the artifact directory.
type LiveStatus struct {
	State          string `json:"state"`
	ElapsedS       int    `json:"elapsed_s"`
	LastActivity   string `json:"last_activity"`
	ToolsUsed      int    `json:"tools_used"`
	FilesChanged   int    `json:"files_changed"`
	StdinPipeReady bool   `json:"stdin_pipe_ready,omitempty"`
	Timestamp      string `json:"ts"`
	DispatchID     string `json:"dispatch_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
}

const statusFileName = "status.json"

// WriteStatusJSON atomically writes status.json to the artifact directory.
// The file is fsynced before rename to guarantee on-disk visibility for
// consumers that read immediately after an async ack.
func WriteStatusJSON(artifactDir string, status LiveStatus) error {
	if strings.TrimSpace(artifactDir) == "" {
		return nil
	}
	status.Timestamp = time.Now().UTC().Format(time.RFC3339)

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	path := filepath.Join(artifactDir, statusFileName)
	tmpPath := path + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open status temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write status temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync status temp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close status temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename status temp: %w", err)
	}
	return nil
}

// ReadStatusJSON reads the current status.json from the artifact directory.
func ReadStatusJSON(artifactDir string) (*LiveStatus, error) {
	path := filepath.Join(artifactDir, statusFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var status LiveStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	return &status, nil
}

// ReadHostPID reads the host.pid from the artifact directory and returns the PID.
func ReadHostPID(artifactDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(artifactDir, "host.pid"))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse host.pid: %w", err)
	}
	return pid, nil
}

// IsProcessAlive checks if a process with the given PID is running.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Use signal 0 to check liveness.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
