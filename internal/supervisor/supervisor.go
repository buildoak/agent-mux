package supervisor

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Process struct {
	cmd      *exec.Cmd
	pgid     int
	started  bool
	waitDone chan struct{}
	waitErr  error
	waitOnce sync.Once
}

func NewProcess(name string, args []string, dir string, env []string) *Process {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &Process{cmd: cmd, waitDone: make(chan struct{})}
}

func (p *Process) Cmd() *exec.Cmd { return p.cmd }

func (p *Process) Start() error {
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	p.started = true
	if p.cmd.Process == nil {
		return nil
	}
	if pgid, err := syscall.Getpgid(p.cmd.Process.Pid); err == nil {
		p.pgid = pgid
	} else {
		p.pgid = p.cmd.Process.Pid
	}
	return nil
}

func (p *Process) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

func (p *Process) GracefulStop(graceSec int) error {
	if !p.started || p.cmd.Process == nil {
		return nil
	}
	if err := p.signalGroup(syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = p.Wait(); close(done) }()
	select {
	case <-done:
		return p.waitErr
	case <-time.After(time.Duration(graceSec) * time.Second):
		_ = p.signalGroup(syscall.SIGKILL)
		<-done
		return p.waitErr
	}
}

func (p *Process) Kill() error {
	if !p.started || p.cmd.Process == nil {
		return nil
	}
	return p.signalGroup(syscall.SIGKILL)
}

func (p *Process) signalGroup(sig syscall.Signal) error {
	send := func() error {
		if p.pgid > 0 {
			return syscall.Kill(-p.pgid, sig)
		}
		if p.cmd.Process != nil {
			return p.cmd.Process.Signal(sig)
		}
		return nil
	}
	if err := send(); err != nil && !ignoreSignalErr(err) {
		return err
	}
	return nil
}

func ignoreSignalErr(err error) bool {
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "process already") || strings.Contains(errText, "already exited") || strings.Contains(errText, "already finished")
}

func (p *Process) Pid() int {
	if p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

func (p *Process) ExitCode() int {
	if p.cmd.ProcessState != nil {
		return p.cmd.ProcessState.ExitCode()
	}
	return -1
}
