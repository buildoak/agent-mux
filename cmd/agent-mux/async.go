package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/buildoak/agent-mux/internal/dispatch"
	"github.com/buildoak/agent-mux/internal/hooks"
	"github.com/buildoak/agent-mux/internal/types"
)

// runAsyncDispatch emits the async_started ack to stdout, writes host.pid,
// detaches stdout/stderr, then runs the dispatch synchronously in the
// current process. The caller is expected to background this process
// (e.g. run_in_background or shell &).
func runAsyncDispatch(ctx context.Context, spec *types.DispatchSpec, annotations types.DispatchAnnotations, stderr, stdout io.Writer, verbose, stream bool, hookEval *hooks.Evaluator) int {
	// Ensure artifact dir exists early so ax status can find the dispatch immediately.
	if err := dispatch.EnsureArtifactDir(spec.ArtifactDir); err != nil {
		return emitFailureResult(stdout, spec, 1, "artifact_dir_unwritable",
			fmt.Sprintf("Create artifact dir %q: %v", spec.ArtifactDir, err),
			"Choose a writable --artifact-dir path.")
	}

	// Register control record so ax status/result can resolve the dispatch ID.
	if err := dispatch.RegisterDispatchSpec(spec); err != nil {
		return emitFailureResult(stdout, spec, 1, "config_error",
			fmt.Sprintf("Register control path for dispatch %q: %v", spec.DispatchID, err),
			"Ensure the control path is writable.")
	}

	// Write host.pid so ax status can detect orphaned dispatches.
	// Fsync to guarantee on-disk visibility before the async ack is emitted.
	pidPath := filepath.Join(spec.ArtifactDir, "host.pid")
	if err := writeAndSync(pidPath, []byte(fmt.Sprintf("%d", os.Getpid()))); err != nil {
		return emitFailureResult(stdout, spec, 1, "artifact_dir_unwritable",
			fmt.Sprintf("Write host.pid in %q: %v", spec.ArtifactDir, err),
			"Ensure the artifact directory is writable.")
	}
	defer os.Remove(pidPath)

	// Write initial status.json so ax status returns immediately.
	// WriteStatusJSON fsyncs internally before rename.
	_ = dispatch.WriteStatusJSON(spec.ArtifactDir, dispatch.LiveStatus{
		State:        "running",
		ElapsedS:     0,
		LastActivity: "initializing",
		ToolsUsed:    0,
		FilesChanged: 0,
		DispatchID:   spec.DispatchID,
	})

	// Emit async_started ack to stdout AFTER both files are on disk.
	// The ack is the last thing written — consumers can safely read
	// host.pid and status.json immediately after receiving it.
	writeCompactJSON(stdout, map[string]any{
		"schema_version": 1,
		"kind":           "async_started",
		"dispatch_id":    spec.DispatchID,
		"artifact_dir":   spec.ArtifactDir,
	})

	// Detach stdout and stderr: redirect to /dev/null so the process
	// doesn't write to the caller's terminal after the ack.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		// Non-fatal: continue with existing stderr.
		devNull = nil
	}

	var dispatchStderr io.Writer
	if devNull != nil {
		dispatchStderr = devNull
		defer devNull.Close()
	} else {
		dispatchStderr = stderr
	}

	// Run the dispatch synchronously in this process. detached=true skips
	// the parent-death reaper: --async means the caller has committed to
	// exiting before the worker (see docs/async.md "Lifecycle"). Arming
	// the reaper would SIGKILL the worker the moment the caller exits.
	result, err := dispatchSync(ctx, spec, annotations, dispatchStderr, verbose, stream, hookEval, true)
	if err != nil {
		_ = dispatch.WriteStatusJSON(spec.ArtifactDir, dispatch.LiveStatus{
			State:        "failed",
			LastActivity: "startup_failed",
			DispatchID:   spec.DispatchID,
		})
		return 1
	}

	// Write final status.json.
	finalState := "completed"
	if result.Status == types.StatusFailed {
		finalState = "failed"
	} else if result.Status == types.StatusTimedOut {
		finalState = "timed_out"
	}
	_ = dispatch.WriteStatusJSON(spec.ArtifactDir, dispatch.LiveStatus{
		State:        finalState,
		ElapsedS:     int(result.DurationMS / 1000),
		LastActivity: "done",
		DispatchID:   spec.DispatchID,
	})

	return 0
}

// writeAndSync writes data to path with an explicit fsync to guarantee
// the content is durable on disk before the caller proceeds.
func writeAndSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
