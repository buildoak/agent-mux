package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDefaultStorePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := DefaultStorePath()
	want := filepath.Join(home, ".agent-mux", "data")
	if path != want {
		t.Fatalf("DefaultStorePath() = %q, want %q", path, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q should be a directory", path)
	}
	if perms := info.Mode().Perm(); perms != 0o700 {
		t.Fatalf("dir perms = %#o, want %#o", perms, 0o700)
	}
}

func TestAppendRecordAndReadBack(t *testing.T) {
	storePath := t.TempDir()
	record := testRecord("01KMT4E7BBNN1KQEC8MYJRW5H5")

	if err := AppendRecord(storePath, record); err != nil {
		t.Fatalf("AppendRecord: %v", err)
	}

	records, err := ListRecords(storePath, 10)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0] != record {
		t.Fatalf("record = %#v, want %#v", records[0], record)
	}

	indexInfo, err := os.Stat(filepath.Join(storePath, "dispatches.jsonl"))
	if err != nil {
		t.Fatalf("Stat(dispatches.jsonl): %v", err)
	}
	if perms := indexInfo.Mode().Perm(); perms != 0o600 {
		t.Fatalf("dispatches.jsonl perms = %#o, want %#o", perms, 0o600)
	}

	response := "# Result\n\nFull response text."
	if err := WriteResult(storePath, record.ID, response); err != nil {
		t.Fatalf("WriteResult: %v", err)
	}

	got, err := ReadResult(storePath, record.ID)
	if err != nil {
		t.Fatalf("ReadResult: %v", err)
	}
	if got != response {
		t.Fatalf("ReadResult() = %q, want %q", got, response)
	}

	resultInfo, err := os.Stat(filepath.Join(storePath, "results", record.ID+".md"))
	if err != nil {
		t.Fatalf("Stat(result): %v", err)
	}
	if perms := resultInfo.Mode().Perm(); perms != 0o600 {
		t.Fatalf("result perms = %#o, want %#o", perms, 0o600)
	}
}

func TestConcurrentAppends(t *testing.T) {
	storePath := t.TempDir()

	const total = 64
	var wg sync.WaitGroup
	errCh := make(chan error, total)

	for i := 0; i < total; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			record := testRecord(fmt.Sprintf("01KMT4E7BBNN1KQEC8MYJRW%03d", i))
			record.DurationMs = int64(i)
			record.ResponseChars = 1000 + i
			record.Truncated = i%2 == 0
			if err := AppendRecord(storePath, record); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("AppendRecord concurrent error: %v", err)
	}

	records, err := ListRecords(storePath, 0)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != total {
		t.Fatalf("len(records) = %d, want %d", len(records), total)
	}

	seen := make(map[string]struct{}, total)
	for _, record := range records {
		if _, ok := seen[record.ID]; ok {
			t.Fatalf("duplicate record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}

	data, err := os.ReadFile(filepath.Join(storePath, "dispatches.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(dispatches.jsonl): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != total {
		t.Fatalf("line count = %d, want %d", len(lines), total)
	}
}

func TestFindRecordByPrefix(t *testing.T) {
	storePath := t.TempDir()

	first := testRecord("01KMT4E7BBNN1KQEC8MYJRW5H5")
	second := testRecord("01KMT4E7CDDD1KQEC8MYJRW9Z9")

	if err := AppendRecord(storePath, first); err != nil {
		t.Fatalf("AppendRecord(first): %v", err)
	}
	if err := AppendRecord(storePath, second); err != nil {
		t.Fatalf("AppendRecord(second): %v", err)
	}

	match, err := FindRecord(storePath, "01KMT4E7BBN")
	if err != nil {
		t.Fatalf("FindRecord(unique): %v", err)
	}
	if match == nil {
		t.Fatal("FindRecord(unique) = nil, want match")
	}
	if match.ID != first.ID {
		t.Fatalf("FindRecord(unique).ID = %q, want %q", match.ID, first.ID)
	}

	noMatch, err := FindRecord(storePath, "does-not-match")
	if err != nil {
		t.Fatalf("FindRecord(no match): %v", err)
	}
	if noMatch != nil {
		t.Fatalf("FindRecord(no match) = %#v, want nil", noMatch)
	}

	ambiguous, err := FindRecord(storePath, "01KMT4E7")
	if err == nil {
		t.Fatalf("FindRecord(ambiguous) error = nil, got record %#v", ambiguous)
	}
}

func TestFindRecordByRefMatchesTraceToken(t *testing.T) {
	storePath := t.TempDir()

	first := testRecord("01KMT4E7BBNN1KQEC8MYJRW5H5")
	second := testRecord("01KMT4E7CDDD1KQEC8MYJRW9Z9")

	if err := AppendRecord(storePath, first); err != nil {
		t.Fatalf("AppendRecord(first): %v", err)
	}
	if err := AppendRecord(storePath, second); err != nil {
		t.Fatalf("AppendRecord(second): %v", err)
	}

	match, err := FindRecordByRef(storePath, second.TraceToken)
	if err != nil {
		t.Fatalf("FindRecordByRef(trace_token): %v", err)
	}
	if match == nil {
		t.Fatal("FindRecordByRef(trace_token) = nil, want match")
	}
	if match.ID != second.ID {
		t.Fatalf("FindRecordByRef(trace_token).ID = %q, want %q", match.ID, second.ID)
	}
}

func TestListRecordsBackCompatMissingSessionID(t *testing.T) {
	storePath := t.TempDir()
	data := []byte(`{"id":"01KMT4E7BBNN1KQEC8MYJRW5H5","salt":"quick-newt-zero","trace_token":"AGENT_MUX_GO_01KMT4E7BBNN1KQEC8MYJRW5H5","status":"completed","engine":"codex","model":"gpt-5.4","started":"2026-03-28T13:45:00Z","cwd":"/repo","truncated":false}` + "\n")
	if err := os.WriteFile(filepath.Join(storePath, "dispatches.jsonl"), data, 0o600); err != nil {
		t.Fatalf("WriteFile(dispatches.jsonl): %v", err)
	}

	records, err := ListRecords(storePath, 0)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].SessionID != "" {
		t.Fatalf("session_id = %q, want empty", records[0].SessionID)
	}
}

func TestMissingFiles(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "store")

	_, err := ReadResult(storePath, "missing")
	if err == nil {
		t.Fatal("ReadResult(missing) error = nil, want not-exist error")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("ReadResult(missing) error = %v, want os.IsNotExist", err)
	}
}

func TestEmptyStore(t *testing.T) {
	storePath := t.TempDir()

	records, err := ListRecords(storePath, 10)
	if err != nil {
		t.Fatalf("ListRecords(empty): %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("len(records) = %d, want 0", len(records))
	}

	match, err := FindRecord(storePath, "01KMT4")
	if err != nil {
		t.Fatalf("FindRecord(empty): %v", err)
	}
	if match != nil {
		t.Fatalf("FindRecord(empty) = %#v, want nil", match)
	}
}

func testRecord(id string) DispatchRecord {
	return DispatchRecord{
		ID:            id,
		Salt:          "quick-newt-zero",
		TraceToken:    "AGENT_MUX_GO_" + id,
		SessionID:     "session-" + id,
		Status:        "completed",
		Engine:        "codex",
		Model:         "gpt-5.4",
		Role:          "explorer",
		Variant:       "default",
		StartedAt:     "2026-03-28T13:45:00Z",
		EndedAt:       "2026-03-28T13:58:44Z",
		DurationMs:    824000,
		Cwd:           "/repo",
		Truncated:     true,
		ResponseChars: 3817,
		ArtifactDir:   "/tmp/agent-mux/" + id,
	}
}
