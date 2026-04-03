package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	storeDirPerm  = 0o700
	storeFilePerm = 0o600
	maxLineSize   = 1024 * 1024
)

type DispatchRecord struct {
	ID            string `json:"id"`
	Salt          string `json:"salt"`
	TraceToken    string `json:"trace_token,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Status        string `json:"status"`
	Engine        string `json:"engine"`
	Model         string `json:"model"`
	Role          string `json:"role,omitempty"`
	Variant       string `json:"variant,omitempty"`
	StartedAt     string `json:"started"`
	EndedAt       string `json:"ended,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	Cwd           string `json:"cwd"`
	Truncated     bool   `json:"truncated"`
	ResponseChars int    `json:"response_chars,omitempty"`
	ArtifactDir   string `json:"artifact_dir,omitempty"`
}

func DefaultStorePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	path := filepath.Join(homeDir, ".agent-mux", "data")
	if err := ensureDir(path); err != nil {
		return ""
	}
	return path
}

func AppendRecord(storePath string, record DispatchRecord) error {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return err
	}

	indexPath := dispatchesPath(path)
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, storeFilePerm)
	if err != nil {
		return fmt.Errorf("open dispatch index: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock dispatch index: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if err := f.Chmod(storeFilePerm); err != nil {
		return fmt.Errorf("chmod dispatch index: %w", err)
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal dispatch record: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append dispatch record: %w", err)
	}
	return nil
}

func WriteResult(storePath string, dispatchID string, response string) error {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return err
	}

	resultsDir := filepath.Join(path, "results")
	if err := ensureDir(resultsDir); err != nil {
		return fmt.Errorf("ensure results dir: %w", err)
	}

	// FM-8: Atomic write via tmp+fsync+rename so a mid-write kill cannot
	// corrupt the result file. Use os.CreateTemp for a unique temp name so
	// concurrent or retried writes never collide on the same .tmp path.
	resultPath := filepath.Join(resultsDir, dispatchID+".md")

	f, err := os.CreateTemp(resultsDir, dispatchID+".md.tmp.*")
	if err != nil {
		return fmt.Errorf("open result temp: %w", err)
	}
	tmpPath := f.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := f.Write([]byte(response)); err != nil {
		_ = f.Close()
		return fmt.Errorf("write result temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync result temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close result temp: %w", err)
	}
	// Match target permissions before rename.
	if err := os.Chmod(tmpPath, storeFilePerm); err != nil {
		return fmt.Errorf("chmod result temp: %w", err)
	}

	if err := os.Rename(tmpPath, resultPath); err != nil {
		return fmt.Errorf("rename result temp: %w", err)
	}
	renamed = true
	return nil
}

func ReadResult(storePath string, dispatchID string) (string, error) {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(path, "results", dispatchID+".md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ListRecords(storePath string, limit int) ([]DispatchRecord, error) {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return nil, err
	}

	records, err := readAllRecords(dispatchesPath(path))
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit >= len(records) {
		return records, nil
	}
	return records[len(records)-limit:], nil
}

func FindRecord(storePath string, idPrefix string) (*DispatchRecord, error) {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return nil, err
	}

	prefix := strings.TrimSpace(idPrefix)
	if prefix == "" {
		return nil, nil
	}

	records, err := readAllRecords(dispatchesPath(path))
	if err != nil {
		return nil, err
	}

	var match *DispatchRecord
	for i := range records {
		record := records[i]
		if !strings.HasPrefix(record.ID, prefix) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple dispatches match prefix %q", prefix)
		}
		recordCopy := record
		match = &recordCopy
	}

	return match, nil
}

func FindRecordByRef(storePath string, ref string) (*DispatchRecord, error) {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return nil, err
	}

	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}

	records, err := readAllRecords(dispatchesPath(path))
	if err != nil {
		return nil, err
	}

	var match *DispatchRecord
	for i := range records {
		record := records[i]
		if !strings.HasPrefix(record.ID, ref) && strings.TrimSpace(record.TraceToken) != ref {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple dispatches match reference %q", ref)
		}
		recordCopy := record
		match = &recordCopy
	}

	return match, nil
}

// RewriteRecords atomically replaces the dispatches.jsonl file with the given records.
// Used by gc to remove old entries.
func RewriteRecords(storePath string, records []DispatchRecord) error {
	path, err := ensureStorePath(storePath)
	if err != nil {
		return err
	}

	indexPath := dispatchesPath(path)
	tmpPath := indexPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, storeFilePerm)
	if err != nil {
		return fmt.Errorf("open temp dispatch index: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmpPath) // clean up on failure; no-op after rename
	}()

	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("marshal dispatch record: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write dispatch record: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temp dispatch index: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp dispatch index: %w", err)
	}
	if err := os.Rename(tmpPath, indexPath); err != nil {
		return fmt.Errorf("rename dispatch index: %w", err)
	}
	return nil
}

func ensureStorePath(storePath string) (string, error) {
	if strings.TrimSpace(storePath) == "" {
		storePath = DefaultStorePath()
		if storePath == "" {
			return "", errors.New("resolve default store path")
		}
	}
	if err := ensureDir(storePath); err != nil {
		return "", fmt.Errorf("ensure store dir: %w", err)
	}
	return storePath, nil
}

func ensureDir(path string) error {
	if err := os.MkdirAll(path, storeDirPerm); err != nil {
		return err
	}
	return os.Chmod(path, storeDirPerm)
}

func dispatchesPath(storePath string) string {
	return filepath.Join(storePath, "dispatches.jsonl")
}

func readAllRecords(path string) ([]DispatchRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open dispatch index: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	records := make([]DispatchRecord, 0)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record DispatchRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("decode dispatch record line %d: %w", lineNo, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan dispatch index: %w", err)
	}
	return records, nil
}
