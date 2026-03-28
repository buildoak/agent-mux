package inbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestCreateInbox(t *testing.T) {
	dir := t.TempDir()

	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox first call: %v", err)
	}
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox second call: %v", err)
	}
}

func TestWriteInbox(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	if err := WriteInbox(dir, "first"); err != nil {
		t.Fatalf("WriteInbox first: %v", err)
	}
	if err := WriteInbox(dir, "second"); err != nil {
		t.Fatalf("WriteInbox second: %v", err)
	}

	got, err := ReadInbox(dir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	want := []string{"first", "second"}
	if !slices.Equal(messageTexts(got), want) {
		t.Fatalf("ReadInbox messages = %v, want %v", messageTexts(got), want)
	}
	for _, msg := range got {
		if msg.Timestamp == "" {
			t.Fatalf("Timestamp is empty for message %+v", msg)
		}
		if _, err := time.Parse(time.RFC3339Nano, msg.Timestamp); err != nil {
			t.Fatalf("Timestamp %q is not RFC3339Nano: %v", msg.Timestamp, err)
		}
	}
}

func TestWriteInboxPreservesDelimiterInMessage(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	want := "first line\n---\nsecond line"
	if err := WriteInbox(dir, want); err != nil {
		t.Fatalf("WriteInbox: %v", err)
	}

	got, err := ReadInbox(dir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(got) != 1 || got[0].Message != want {
		t.Fatalf("ReadInbox messages = %+v, want [%q]", got, want)
	}
}

func TestReadInboxBackwardCompatibleLegacyDelimiterFormat(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	legacy := "first" + delimiter + "second line 1\nsecond line 2" + delimiter
	if err := os.WriteFile(filepath.Join(dir, inboxFilename), []byte(legacy), 0644); err != nil {
		t.Fatalf("WriteFile legacy inbox: %v", err)
	}

	got, err := ReadInbox(dir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	want := []string{"first", "second line 1\nsecond line 2"}
	if !slices.Equal(messageTexts(got), want) {
		t.Fatalf("ReadInbox messages = %v, want %v", messageTexts(got), want)
	}
	for _, msg := range got {
		if msg.Timestamp != "" {
			t.Fatalf("legacy message timestamp = %q, want empty", msg.Timestamp)
		}
	}
}

func TestReadInboxClearsFile(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	if err := WriteInbox(dir, "message"); err != nil {
		t.Fatalf("WriteInbox: %v", err)
	}

	if _, err := ReadInbox(dir); err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if HasMessages(dir) {
		t.Fatal("HasMessages() = true after ReadInbox, want false")
	}
}

func TestHasMessages(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}
	if HasMessages(dir) {
		t.Fatal("HasMessages() = true for empty inbox, want false")
	}
	if err := WriteInbox(dir, "message"); err != nil {
		t.Fatalf("WriteInbox: %v", err)
	}
	if !HasMessages(dir) {
		t.Fatal("HasMessages() = false after write, want true")
	}
}

func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	const writers = 10
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		msg := fmt.Sprintf("message-%d", i)
		go func() {
			defer wg.Done()
			if err := WriteInbox(dir, msg); err != nil {
				t.Errorf("WriteInbox(%q): %v", msg, err)
			}
		}()
	}
	wg.Wait()

	got, err := ReadInbox(dir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(got) != writers {
		t.Fatalf("ReadInbox returned %d messages, want %d: %v", len(got), writers, messageTexts(got))
	}
	for i := 0; i < writers; i++ {
		msg := fmt.Sprintf("message-%d", i)
		if !slices.Contains(messageTexts(got), msg) {
			t.Fatalf("ReadInbox missing %q in %v", msg, messageTexts(got))
		}
	}
}

func TestReadInboxEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	got, err := ReadInbox(dir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ReadInbox returned %v for empty inbox, want nil/empty", got)
	}
}

func TestDelimiterPoisoningInNDJSONFraming(t *testing.T) {
	dir := t.TempDir()
	if err := CreateInbox(dir); err != nil {
		t.Fatalf("CreateInbox: %v", err)
	}

	// Message text contains the legacy block delimiter \n---\n.
	// NDJSON framing should treat the whole thing as one message.
	poison := "line one\n---\nline two\n---\nline three"
	if err := WriteInbox(dir, poison); err != nil {
		t.Fatalf("WriteInbox: %v", err)
	}
	if err := WriteInbox(dir, "clean message"); err != nil {
		t.Fatalf("WriteInbox: %v", err)
	}

	got, err := ReadInbox(dir)
	if err != nil {
		t.Fatalf("ReadInbox: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReadInbox returned %d messages, want 2: %v", len(got), messageTexts(got))
	}
	if got[0].Message != poison {
		t.Fatalf("message[0] = %q, want %q", got[0].Message, poison)
	}
	if got[1].Message != "clean message" {
		t.Fatalf("message[1] = %q, want %q", got[1].Message, "clean message")
	}
}

func messageTexts(messages []InboxMessage) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Message)
	}
	return out
}
