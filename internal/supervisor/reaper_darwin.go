//go:build darwin

package supervisor

import (
	"os"
	"syscall"
)

// WatchParentDeath monitors the parent process via kqueue EVFILT_PROC+NOTE_EXIT.
// When the parent dies, the child process group (pgid) is killed.
// This is a best-effort guard — errors are silently ignored.
func WatchParentDeath(pgid int) {
	ppid := os.Getppid()
	if ppid <= 1 {
		// Already orphaned or init-parented; nothing to watch.
		return
	}

	kq, err := syscall.Kqueue()
	if err != nil {
		return
	}

	event := syscall.Kevent_t{
		Ident:  uint64(ppid),
		Filter: syscall.EVFILT_PROC,
		Flags:  syscall.EV_ADD | syscall.EV_ONESHOT,
		Fflags: syscall.NOTE_EXIT,
	}

	events := make([]syscall.Kevent_t, 1)
	n, err := syscall.Kevent(kq, []syscall.Kevent_t{event}, events, nil)
	_ = syscall.Close(kq)
	if err != nil || n < 1 {
		return
	}

	// Parent exited — kill child process group.
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
