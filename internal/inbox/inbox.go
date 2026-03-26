package inbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	inboxFilename = "inbox.md"
	delimiter     = "\n---\n"
)

// CreateInbox creates the inbox file at dispatch start.
// Uses O_CREATE|O_EXCL — returns nil if file already exists (idempotent for reruns).
func CreateInbox(artifactDir string) error {
	f, err := os.OpenFile(inboxPath(artifactDir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("create inbox: %w", err)
	}
	return f.Close()
}

// WriteInbox appends a message to the inbox file atomically using O_APPEND.
// Each message is terminated with the delimiter "---".
// Messages <= 4096 bytes are atomic on POSIX (PIPE_BUF guarantee).
func WriteInbox(artifactDir, message string) error {
	f, err := os.OpenFile(inboxPath(artifactDir), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open inbox for append: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock inbox: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	payload := message + delimiter
	if _, err := f.Write([]byte(payload)); err != nil {
		return fmt.Errorf("append inbox message: %w", err)
	}
	return nil
}

// ReadInbox reads all messages from the inbox and clears it atomically.
// Uses flock(LOCK_EX) to ensure exclusivity with concurrent writers.
// Returns slice of messages (split by delimiter), or nil if inbox is empty.
func ReadInbox(artifactDir string) ([]string, error) {
	f, err := os.OpenFile(inboxPath(artifactDir), os.O_RDWR, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open inbox for read: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock inbox: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read inbox: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return nil, fmt.Errorf("truncate inbox: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("rewind inbox: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	parts := strings.Split(string(data), delimiter)
	messages := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		messages = append(messages, part)
	}
	if len(messages) == 0 {
		return nil, nil
	}
	return messages, nil
}

// HasMessages checks if the inbox has any content without locking.
// Fast path: just check file size > 0.
func HasMessages(artifactDir string) bool {
	info, err := os.Stat(inboxPath(artifactDir))
	if err != nil {
		return false
	}
	return info.Size() > 0
}

func inboxPath(artifactDir string) string {
	return filepath.Join(artifactDir, inboxFilename)
}
