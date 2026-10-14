//go:build linux

package process

import (
	"fmt"
	"runtime"
	"syscall"
)

func startManagedChild(path string, args []string, noOut bool) (running, error) {
	cmd := newCmd(path, args, noOut)
	if err := applyPrivilegeDrop(cmd); err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	// PDEATHSIG tracks the creating thread. Keep that thread alive until the
	// child exits; Go installs the signal after dropping child credentials.
	type result struct {
		run running
		err error
	}
	started := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		run, err := startCmd(cmd)
		if err != nil {
			started <- result{err: fmt.Errorf("Tachyon child: %w", err)}
			return
		}
		child := &threadRunner{running: run, done: make(chan error, 1)}
		started <- result{run: child}
		child.done <- run.Wait()
	}()
	res := <-started
	return res.run, res.err
}

type threadRunner struct {
	running
	done chan error
}

func (p *threadRunner) Wait() error { return <-p.done }
