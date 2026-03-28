//go:build !darwin && !linux

package supervisor

// WatchParentDeath is a no-op on unsupported platforms.
func WatchParentDeath(pgid int) {}
