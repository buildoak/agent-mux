//go:build linux

package supervisor

import (
	"syscall"
)

// WatchParentDeath uses PR_SET_PDEATHSIG to send SIGKILL to the child process
// group when the parent dies. This is a best-effort guard.
// Note: PR_SET_PDEATHSIG is set on the calling thread. This should be called
// from the goroutine that spawns the child, or via SysProcAttr.Pdeathsig.
func WatchParentDeath(pgid int) {
	// On Linux, the preferred approach is to set Pdeathsig in SysProcAttr
	// before starting the child. This function is a fallback for the calling
	// process itself.
	_ = syscall.Prctl(syscall.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0)
}
