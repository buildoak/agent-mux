package inbox

import (
	"fmt"
	"slices"
	"sync"
	"testing"
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
	if !slices.Equal(got, want) {
		t.Fatalf("ReadInbox messages = %v, want %v", got, want)
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
		t.Fatalf("ReadInbox returned %d messages, want %d: %v", len(got), writers, got)
	}
	for i := 0; i < writers; i++ {
		msg := fmt.Sprintf("message-%d", i)
		if !slices.Contains(got, msg) {
			t.Fatalf("ReadInbox missing %q in %v", msg, got)
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
