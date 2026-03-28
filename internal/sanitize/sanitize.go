package sanitize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	MaxDispatchIDLen = 128
	MaxBasenameLen   = 128
)

// ValidateDispatchID rejects empty IDs, path separators, and traversal-like names.
func ValidateDispatchID(id string) error {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return errors.New("dispatch ID is empty")
	case len(id) > MaxDispatchIDLen:
		return fmt.Errorf("dispatch ID exceeds %d bytes", MaxDispatchIDLen)
	case id == "." || id == "..":
		return errors.New("dispatch ID must not be . or ..")
	case strings.ContainsAny(id, `/\`):
		return errors.New("dispatch ID must not contain path separators")
	}
	return nil
}

// ValidateBasename rejects empty names, path separators, and traversal-like names.
func ValidateBasename(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("name is empty")
	case len(name) > MaxBasenameLen:
		return fmt.Errorf("name exceeds %d bytes", MaxBasenameLen)
	case name == "." || name == "..":
		return errors.New("name must not be . or ..")
	case strings.ContainsAny(name, `/\`):
		return errors.New("name must not contain path separators")
	}
	return nil
}

// SafeJoinPath joins child beneath root and rejects paths that escape the cleaned root.
func SafeJoinPath(root, child string) (string, error) {
	root = strings.TrimSpace(root)
	child = strings.TrimSpace(child)
	if root == "" {
		return "", errors.New("root is empty")
	}
	if child == "" {
		return "", errors.New("child is empty")
	}
	if filepath.IsAbs(child) {
		return "", fmt.Errorf("absolute path %q is not allowed", child)
	}

	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("abs root: %w", err)
	}
	joinedAbs, err := filepath.Abs(filepath.Join(rootAbs, child))
	if err != nil {
		return "", fmt.Errorf("abs joined path: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, joinedAbs)
	if err != nil {
		return "", fmt.Errorf("rel path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes root %q", child, rootAbs)
	}
	return joinedAbs, nil
}

// SecureArtifactRoot returns a private per-user runtime directory for agent-mux artifacts.
func SecureArtifactRoot() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); xdg != "" && filepath.IsAbs(xdg) {
		displayPath := filepath.Join(xdg, "agent-mux")
		createPath := filepath.Join(resolveExistingPath(xdg), "agent-mux")
		if err := ensurePrivateDir(createPath, 0o700); err == nil {
			return displayPath
		}
	}

	const tmpDir = "/tmp"
	displayPath := filepath.Join(tmpDir, fmt.Sprintf("agent-mux-%d", os.Getuid()))
	createPath := filepath.Join(resolveExistingPath(tmpDir), fmt.Sprintf("agent-mux-%d", os.Getuid()))
	_ = ensurePrivateDir(createPath, 0o700)
	return displayPath
}

func ensurePrivateDir(path string, perm os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is empty")
	}
	path = filepath.Clean(path)

	if err := checkPathChainNoSymlinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, perm.Perm()); err != nil {
		return fmt.Errorf("mkdir %q: %w", path, err)
	}
	if err := checkPathChainNoSymlinks(path); err != nil {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("directory %q must not be a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	if err := checkOwnedByCurrentUser(info); err != nil {
		return fmt.Errorf("directory %q: %w", path, err)
	}
	if err := os.Chmod(path, perm.Perm()); err != nil {
		return fmt.Errorf("chmod %q: %w", path, err)
	}

	return nil
}

func checkPathChainNoSymlinks(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is empty")
	}

	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("abs path %q: %w", path, err)
	}

	var chain []string
	for current := absPath; ; current = filepath.Dir(current) {
		chain = append(chain, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}

	for i := len(chain) - 1; i >= 0; i-- {
		current := chain[i]
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("path %q contains symlink component %q", absPath, current)
			}
		case os.IsNotExist(err):
			continue
		default:
			return fmt.Errorf("lstat %q: %w", current, err)
		}
	}

	return nil
}

func checkOwnedByCurrentUser(info os.FileInfo) error {
	if info == nil {
		return errors.New("file info is nil")
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fmt.Errorf("unsupported file info type %T", info.Sys())
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("owned by uid %d, want %d", stat.Uid, os.Getuid())
	}

	return nil
}

func openFileNoFollow(path string, flags int, perm os.FileMode) (*os.File, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("path is empty")
	}

	cleanPath := filepath.Clean(path)
	if err := checkPathChainNoSymlinks(cleanPath); err != nil {
		return nil, err
	}

	fd, err := unix.Open(cleanPath, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm.Perm()))
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", cleanPath, err)
	}

	file := os.NewFile(uintptr(fd), cleanPath)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open %q: unable to wrap file descriptor", cleanPath)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat %q: %w", cleanPath, err)
	}
	if err := checkOwnedByCurrentUser(info); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("open %q: %w", cleanPath, err)
	}

	return file, nil
}

func resolveExistingPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(resolved) {
		return path
	}
	return resolved
}
