package dispatch

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/buildoak/agent-mux/internal/sanitize"
	"github.com/buildoak/agent-mux/internal/types"
)

const (
	dispatchesDirPerm = 0o700
	dispatchFilePerm  = 0o600
	metaFileName      = "meta.json"
	resultFileName    = "result.json"
)

type DispatchRecord struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Engine        string `json:"engine"`
	Model         string `json:"model"`
	Role          string `json:"role,omitempty"`
	StartedAt     string `json:"started"`
	EndedAt       string `json:"ended,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	Cwd           string `json:"cwd"`
	Truncated     bool   `json:"truncated"`
	ResponseChars int    `json:"response_chars,omitempty"`
	ArtifactDir   string `json:"artifact_dir,omitempty"`
	Effort        string `json:"effort,omitempty"`
	Profile       string `json:"profile,omitempty"`
	TimeoutSec    int    `json:"timeout_sec,omitempty"`
}

type PersistentDispatchMeta struct {
	DispatchID  string `json:"dispatch_id"`
	SessionID   string `json:"session_id,omitempty"`
	Engine      string `json:"engine"`
	Model       string `json:"model"`
	Effort      string `json:"effort,omitempty"`
	Role        string `json:"role,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Cwd         string `json:"cwd"`
	ArtifactDir string `json:"artifact_dir,omitempty"`
	StartedAt   string `json:"started_at"`
	TimeoutSec  int    `json:"timeout_sec,omitempty"`
	PromptHash  string `json:"prompt_hash,omitempty"`
}

type PersistentDispatchResult struct {
	types.DispatchResult
	StartedAt     string `json:"started_at,omitempty"`
	EndedAt       string `json:"ended_at,omitempty"`
	ArtifactDir   string `json:"artifact_dir,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
	Engine        string `json:"engine,omitempty"`
	Model         string `json:"model,omitempty"`
	Role          string `json:"role,omitempty"`
	Profile       string `json:"profile,omitempty"`
	Effort        string `json:"effort,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	ResponseChars int    `json:"response_chars,omitempty"`
	TimeoutSec    int    `json:"timeout_sec,omitempty"`
}

func DispatchesDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return ""
	}
	return filepath.Join(homeDir, ".agent-mux", "dispatches")
}

func DispatchDir(dispatchID string) (string, error) {
	dispatchID = strings.TrimSpace(dispatchID)
	if err := sanitize.ValidateDispatchID(dispatchID); err != nil {
		return "", fmt.Errorf("invalid dispatch ID %q: %w", dispatchID, err)
	}
	root := strings.TrimSpace(DispatchesDir())
	if root == "" {
		return "", fmt.Errorf("resolve dispatches dir: home directory unavailable")
	}
	path, err := sanitize.SafeJoinPath(root, dispatchID)
	if err != nil {
		return "", fmt.Errorf("build dispatch dir for %q: %w", dispatchID, err)
	}
	return path, nil
}

func WritePersistentMeta(spec *types.DispatchSpec, annotations types.DispatchAnnotations) error {
	if spec == nil {
		return fmt.Errorf("missing dispatch spec")
	}
	hash := sha256.Sum256([]byte(spec.Prompt))
	artifactDir := strings.TrimSpace(spec.ArtifactDir)
	if artifactDir != "" {
		artifactDirAbs, err := filepath.Abs(artifactDir)
		if err != nil {
			return fmt.Errorf("resolve artifact dir %q: %w", artifactDir, err)
		}
		artifactDir = filepath.Clean(artifactDirAbs)
	}
	dir, err := ensureDispatchDir(spec.DispatchID)
	if err != nil {
		return err
	}
	meta := PersistentDispatchMeta{
		DispatchID:  spec.DispatchID,
		Engine:      spec.Engine,
		Model:       spec.Model,
		Effort:      spec.Effort,
		Role:        annotations.Role,
		Profile:     annotations.Profile,
		Cwd:         spec.Cwd,
		ArtifactDir: artifactDir,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		TimeoutSec:  spec.TimeoutSec,
		PromptHash:  fmt.Sprintf("sha256:%x", hash[:8]),
	}
	return writeJSONFile(filepath.Join(dir, metaFileName), meta)
}

func UpdatePersistentMetaSessionID(dispatchID, sessionID string) error {
	dispatchID = strings.TrimSpace(dispatchID)
	sessionID = strings.TrimSpace(sessionID)
	if dispatchID == "" || sessionID == "" {
		return nil
	}
	meta, err := ReadPersistentMeta(dispatchID)
	if err != nil {
		return err
	}
	if meta.SessionID == sessionID {
		return nil
	}
	meta.SessionID = sessionID
	dir, err := DispatchDir(dispatchID)
	if err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, metaFileName), meta)
}

func UpdatePersistentResultStatus(dispatchID, status string, artifacts []string) error {
	dispatchID = strings.TrimSpace(dispatchID)
	if dispatchID == "" {
		return fmt.Errorf("missing dispatch ID")
	}

	result, err := ReadPersistentResult(dispatchID)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		result = &PersistentDispatchResult{}
	default:
		return err
	}

	if meta, err := ReadPersistentMeta(dispatchID); err == nil {
		result.DispatchID = firstNonEmpty(result.DispatchID, meta.DispatchID, dispatchID)
		result.SessionID = firstNonEmpty(result.SessionID, meta.SessionID)
		result.Engine = firstNonEmpty(result.Engine, meta.Engine)
		result.Model = firstNonEmpty(result.Model, meta.Model)
		result.Role = firstNonEmpty(result.Role, meta.Role)
		result.Profile = firstNonEmpty(result.Profile, meta.Profile)
		result.Cwd = firstNonEmpty(result.Cwd, meta.Cwd)
		result.ArtifactDir = firstNonEmpty(result.ArtifactDir, meta.ArtifactDir)
		result.StartedAt = firstNonEmpty(result.StartedAt, meta.StartedAt)
		result.Effort = firstNonEmpty(result.Effort, meta.Effort)
		if result.TimeoutSec == 0 {
			result.TimeoutSec = meta.TimeoutSec
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if result.DispatchID == "" {
		result.DispatchID = dispatchID
	}
	if result.SchemaVersion == 0 {
		result.SchemaVersion = 1
	}
	result.Status = types.DispatchStatus(strings.TrimSpace(status))
	result.Artifacts = artifacts
	result.EndedAt = time.Now().UTC().Format(time.RFC3339)

	dir, err := DispatchDir(dispatchID)
	if err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, resultFileName), result)
}

func ReadPersistentMeta(dispatchID string) (*PersistentDispatchMeta, error) {
	dir, err := DispatchDir(dispatchID)
	if err != nil {
		return nil, err
	}
	var meta PersistentDispatchMeta
	if err := readJSONFile(filepath.Join(dir, metaFileName), &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func WritePersistentResult(spec *types.DispatchSpec, annotations types.DispatchAnnotations, result *types.DispatchResult, responseText, startedAt, endedAt string) error {
	if spec == nil || result == nil {
		return fmt.Errorf("missing persistent dispatch result payload")
	}
	artifactDir := strings.TrimSpace(spec.ArtifactDir)
	if artifactDir != "" {
		artifactDirAbs, err := filepath.Abs(artifactDir)
		if err != nil {
			return fmt.Errorf("resolve artifact dir %q: %w", artifactDir, err)
		}
		artifactDir = filepath.Clean(artifactDirAbs)
	}
	dir, err := ensureDispatchDir(firstNonEmpty(result.DispatchID, spec.DispatchID))
	if err != nil {
		return err
	}
	copyResult := *result
	copyResult.Response = responseText
	record := PersistentDispatchResult{
		DispatchResult: copyResult,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		ArtifactDir:    artifactDir,
		Cwd:            spec.Cwd,
		Engine:         firstNonEmpty(resultMetadataEngine(result), spec.Engine),
		Model:          firstNonEmpty(resultMetadataModel(result), spec.Model),
		Role:           firstNonEmpty(resultMetadataRole(result), annotations.Role),
		Profile:        firstNonEmpty(resultMetadataProfile(result), annotations.Profile),
		Effort:         spec.Effort,
		SessionID:      resultMetadataSessionID(result),
		ResponseChars:  utf8.RuneCountInString(responseText),
		TimeoutSec:     spec.TimeoutSec,
	}
	return writeJSONFile(filepath.Join(dir, resultFileName), record)
}

func ReadPersistentResult(dispatchID string) (*PersistentDispatchResult, error) {
	dir, err := DispatchDir(dispatchID)
	if err != nil {
		return nil, err
	}
	var result PersistentDispatchResult
	if err := readJSONFile(filepath.Join(dir, resultFileName), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func ReadResult(dispatchID string) (string, error) {
	result, err := ReadPersistentResult(dispatchID)
	if err != nil {
		return "", err
	}
	return result.Response, nil
}

func ListDispatchRecords(limit int) ([]DispatchRecord, error) {
	root := strings.TrimSpace(DispatchesDir())
	if root == "" {
		return nil, fmt.Errorf("resolve dispatches dir: home directory unavailable")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []DispatchRecord{}, nil
		}
		return nil, fmt.Errorf("read dispatches dir %q: %w", root, err)
	}

	records := make([]DispatchRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dispatchID := strings.TrimSpace(entry.Name())
		meta, metaErr := ReadPersistentMeta(dispatchID)
		result, resultErr := ReadPersistentResult(dispatchID)
		if metaErr != nil && !os.IsNotExist(metaErr) {
			return nil, metaErr
		}
		if resultErr != nil && !os.IsNotExist(resultErr) {
			return nil, resultErr
		}
		record := buildDispatchRecord(dispatchID, meta, result)
		if strings.TrimSpace(record.ID) == "" {
			continue
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		ti := recordSortTime(records[i])
		tj := recordSortTime(records[j])
		if ti.Equal(tj) {
			return records[i].ID > records[j].ID
		}
		return ti.After(tj)
	})
	if limit > 0 && len(records) > limit {
		return records[:limit], nil
	}
	return records, nil
}

func FindDispatchRecordByRef(ref string) (*DispatchRecord, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	records, err := ListDispatchRecords(0)
	if err != nil {
		return nil, err
	}
	var match *DispatchRecord
	for i := range records {
		record := records[i]
		if !strings.HasPrefix(record.ID, ref) {
			continue
		}
		if match != nil && match.ID != record.ID {
			return nil, fmt.Errorf("multiple dispatches match reference %q", ref)
		}
		recordCopy := record
		match = &recordCopy
	}
	return match, nil
}

func buildDispatchRecord(dispatchID string, meta *PersistentDispatchMeta, result *PersistentDispatchResult) DispatchRecord {
	record := DispatchRecord{
		ID:          strings.TrimSpace(dispatchID),
		StartedAt:   "",
		ArtifactDir: "",
	}
	if meta != nil {
		record.ID = firstNonEmpty(meta.DispatchID, record.ID)
		record.SessionID = strings.TrimSpace(meta.SessionID)
		record.Engine = strings.TrimSpace(meta.Engine)
		record.Model = strings.TrimSpace(meta.Model)
		record.Role = strings.TrimSpace(meta.Role)
		record.StartedAt = strings.TrimSpace(meta.StartedAt)
		record.Cwd = strings.TrimSpace(meta.Cwd)
		record.ArtifactDir = strings.TrimSpace(meta.ArtifactDir)
		record.Effort = strings.TrimSpace(meta.Effort)
		record.Profile = strings.TrimSpace(meta.Profile)
		record.TimeoutSec = meta.TimeoutSec
	}
	if result != nil {
		record.ID = firstNonEmpty(result.DispatchID, record.ID)
		record.SessionID = firstNonEmpty(result.SessionID, resultMetadataSessionID(&result.DispatchResult), record.SessionID)
		record.Status = string(result.Status)
		record.Engine = firstNonEmpty(result.Engine, resultMetadataEngine(&result.DispatchResult), record.Engine)
		record.Model = firstNonEmpty(result.Model, resultMetadataModel(&result.DispatchResult), record.Model)
		record.Role = firstNonEmpty(result.Role, resultMetadataRole(&result.DispatchResult), record.Role)
		record.StartedAt = firstNonEmpty(result.StartedAt, record.StartedAt)
		record.EndedAt = strings.TrimSpace(result.EndedAt)
		record.DurationMs = result.DurationMS
		record.Cwd = firstNonEmpty(result.Cwd, record.Cwd)
		record.Truncated = result.ResponseTruncated
		record.ResponseChars = result.ResponseChars
		record.ArtifactDir = firstNonEmpty(result.ArtifactDir, record.ArtifactDir)
		record.Effort = firstNonEmpty(result.Effort, record.Effort)
		record.Profile = firstNonEmpty(result.Profile, record.Profile)
		if result.TimeoutSec > 0 {
			record.TimeoutSec = result.TimeoutSec
		}
	}
	return record
}

func recordSortTime(record DispatchRecord) time.Time {
	for _, value := range []string{record.StartedAt, record.EndedAt} {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func ensureDispatchDir(dispatchID string) (string, error) {
	root := strings.TrimSpace(DispatchesDir())
	if root == "" {
		return "", fmt.Errorf("resolve dispatches dir: home directory unavailable")
	}
	if err := os.MkdirAll(root, dispatchesDirPerm); err != nil {
		return "", fmt.Errorf("create dispatches dir %q: %w", root, err)
	}
	dir, err := DispatchDir(dispatchID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, dispatchesDirPerm); err != nil {
		return "", fmt.Errorf("create dispatch dir %q: %w", dir, err)
	}
	return dir, nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp for %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %q: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp %q: %w", path, err)
	}
	if err := tmp.Chmod(dispatchFilePerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %q: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp %q: %w", path, err)
	}
	renamed = true
	return nil
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resultMetadataEngine(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(result.Metadata.Engine)
}

func resultMetadataModel(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(result.Metadata.Model)
}

func resultMetadataRole(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(result.Metadata.Role)
}

func resultMetadataProfile(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(result.Metadata.Profile)
}

func resultMetadataSessionID(result *types.DispatchResult) string {
	if result == nil || result.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(result.Metadata.SessionID)
}
