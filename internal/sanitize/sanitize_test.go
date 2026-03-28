package sanitize

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestValidateDispatchIDAcceptsNormalIDs(t *testing.T) {
	valid := []string{
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"fixed-dispatch-123",
		strings.Repeat("界", 42),
	}

	for _, id := range valid {
		t.Run(id, func(t *testing.T) {
			if err := ValidateDispatchID(id); err != nil {
				t.Fatalf("ValidateDispatchID(%q) error = %v, want nil", id, err)
			}
		})
	}
}

func TestValidateDispatchIDRejectsInvalidValues(t *testing.T) {
	tooLongASCII := strings.Repeat("a", MaxDispatchIDLen+1)
	tooLongUnicode := strings.Repeat("界", 43)

	cases := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "whitespace", id: "   \t\n"},
		{name: "dot", id: "."},
		{name: "dotdot", id: ".."},
		{name: "parent_dir", id: "../"},
		{name: "parent_file", id: "../x"},
		{name: "absolute_path", id: "/etc/passwd"},
		{name: "forward_slash", id: "a/b"},
		{name: "backslash", id: `a\b`},
		{name: "too_long_ascii", id: tooLongASCII},
		{name: "too_long_unicode_bytes", id: tooLongUnicode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateDispatchID(tc.id); err == nil {
				t.Fatalf("ValidateDispatchID(%q) error = nil, want error", tc.id)
			}
		})
	}
}

func TestValidateBasenameAcceptsNormalNames(t *testing.T) {
	valid := []string{
		"explorer",
		"profile-01",
		"名前",
		strings.Repeat("界", 42),
	}

	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateBasename(name); err != nil {
				t.Fatalf("ValidateBasename(%q) error = %v, want nil", name, err)
			}
		})
	}
}

func TestValidateBasenameRejectsInvalidValues(t *testing.T) {
	tooLongASCII := strings.Repeat("b", MaxBasenameLen+1)
	tooLongUnicode := strings.Repeat("界", 43)

	cases := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "whitespace", value: "   "},
		{name: "dot", value: "."},
		{name: "dotdot", value: ".."},
		{name: "parent_dir", value: "../"},
		{name: "parent_file", value: "../skill"},
		{name: "absolute_path", value: "/etc/passwd"},
		{name: "forward_slash", value: "skills/review"},
		{name: "backslash", value: `skills\review`},
		{name: "too_long_ascii", value: tooLongASCII},
		{name: "too_long_unicode_bytes", value: tooLongUnicode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateBasename(tc.value); err == nil {
				t.Fatalf("ValidateBasename(%q) error = nil, want error", tc.value)
			}
		})
	}
}

func TestSafeJoinPathAcceptsPathWithinRoot(t *testing.T) {
	root := t.TempDir()

	got, err := SafeJoinPath(root, filepath.Join("prompts", "lifter.md"))
	if err != nil {
		t.Fatalf("SafeJoinPath: %v", err)
	}

	want := filepath.Join(root, "prompts", "lifter.md")
	if got != want {
		t.Fatalf("SafeJoinPath() = %q, want %q", got, want)
	}
}

func TestSafeJoinPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	absoluteChild := filepath.Join(string(os.PathSeparator), "etc", "passwd")

	cases := []struct {
		name  string
		child string
	}{
		{name: "dotdot", child: ".."},
		{name: "parent_dir", child: "../"},
		{name: "parent_file", child: "../x"},
		{name: "grandparent_file", child: "../../x"},
		{name: "absolute", child: absoluteChild},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SafeJoinPath(root, tc.child); err == nil {
				t.Fatalf("SafeJoinPath(%q, %q) error = nil, want error", root, tc.child)
			}
		})
	}
}

func TestSecureArtifactRootUsesXDGRuntimeDir(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("TMPDIR", t.TempDir())

	got := SecureArtifactRoot()
	want := filepath.Join(runtimeDir, "agent-mux")
	if got != want {
		t.Fatalf("SecureArtifactRoot() = %q, want %q", got, want)
	}

	assertPrivateDir(t, got)
}

func TestSecureArtifactRootFallsBackToPerUIDTempDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", t.TempDir())

	got := SecureArtifactRoot()
	want := filepath.Join("/tmp", fmt.Sprintf("agent-mux-%d", os.Getuid()))
	if got != want {
		t.Fatalf("SecureArtifactRoot() = %q, want %q", got, want)
	}

	assertPrivateDir(t, got)
}

func TestCheckPathChainNoSymlinksRejectsSymlinkComponent(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatalf("Mkdir(realDir): %v", err)
	}

	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("Symlink(linkDir): %v", err)
	}

	err := checkPathChainNoSymlinks(filepath.Join(linkDir, "child.txt"))
	if err == nil {
		t.Fatal("checkPathChainNoSymlinks error = nil, want error")
	}
}

func TestOpenFileNoFollowRejectsLeafSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile(target): %v", err)
	}

	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(link): %v", err)
	}

	file, err := openFileNoFollow(link, unix.O_RDONLY, 0o600)
	if err == nil {
		_ = file.Close()
		t.Fatal("openFileNoFollow error = nil, want error")
	}
}

func TestSecureArtifactRootCreatesDirectoryWith0700(t *testing.T) {
	runtimeDir := t.TempDir()
	// Resolve symlinks so the display path matches the resolved creation path.
	resolvedRuntime, err := filepath.EvalSymlinks(runtimeDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", resolvedRuntime)

	got := SecureArtifactRoot()
	want := filepath.Join(resolvedRuntime, "agent-mux")
	if got != want {
		t.Fatalf("SecureArtifactRoot() = %q, want %q", got, want)
	}

	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("Stat(%q): %v", got, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", got)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("%q mode = %o, want 0700", got, info.Mode().Perm())
	}
}

func TestEnsurePrivateDirCorrectsBadPermissions(t *testing.T) {
	// t.TempDir() may use symlinked paths (macOS /var -> /private/var).
	// Resolve symlinks first to satisfy checkPathChainNoSymlinks.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	badDir := filepath.Join(root, "world-readable")
	if err := os.Mkdir(badDir, 0o777); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// ensurePrivateDir should chmod it to 0700, not reject.
	if err := ensurePrivateDir(badDir, 0o700); err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}
	info, err := os.Stat(badDir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 0700 after ensurePrivateDir", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirRejectsSymlinkDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	ensureErr := ensurePrivateDir(linkDir, 0o700)
	if ensureErr == nil {
		t.Fatal("ensurePrivateDir(symlink) error = nil, want error")
	}
	if !strings.Contains(ensureErr.Error(), "symlink") {
		t.Fatalf("error = %q, want symlink rejection", ensureErr.Error())
	}
}

func assertPrivateDir(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", path)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("%q mode = %o, want 700", path, info.Mode().Perm())
	}
}
