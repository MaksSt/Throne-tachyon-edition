//go:build windows

package process

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobRunner struct {
	running
	job windows.Handle
}

func (p *jobRunner) Wait() error { defer windows.CloseHandle(p.job); return p.running.Wait() }

func startManagedChild(path string, args []string, noOut bool) (running, error) {
	run, err := startChild(path, args, noOut)
	if err != nil {
		return nil, err
	}
	return attachManagedJob(run)
}

func attachManagedJob(run running) (running, error) {
	fail := func(err error) (running, error) {
		_ = run.Kill()
		_ = run.Wait()
		return nil, fmt.Errorf("cannot supervise Tachyon child: %w", err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fail(fmt.Errorf("CreateJobObject: %w", err))
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return fail(fmt.Errorf("SetInformationJobObject: %w", err))
	}
	var handle windows.Handle
	switch child := run.(type) {
	case *tokenRunner:
		handle = child.hProcess
	case *cmdRunner:
		handle, err = windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(child.cmd.Process.Pid))
		if err == nil {
			defer windows.CloseHandle(handle)
		} else {
			err = fmt.Errorf("OpenProcess: %w", err)
		}
	default:
		err = fmt.Errorf("unsupported child handle %T", run)
	}
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		if err != nil {
			err = fmt.Errorf("AssignProcessToJobObject: %w", err)
		}
	}
	if err != nil {
		_ = windows.CloseHandle(job)
		// A fast `check` can exit while the launcher is descheduled. Windows
		// refuses to assign a terminated process to a job (ERROR_ACCESS_DENIED).
		// Only a confirmed exit may bypass supervision; Wait retains its real
		// exit code. An alive child still fails closed and is terminated below.
		if managedChildExited(run, handle) {
			return run, nil
		}
		return fail(err)
	}
	return &jobRunner{running: run, job: job}, nil
}

func managedChildExited(run running, handle windows.Handle) bool {
	if handle == 0 {
		child, ok := run.(*cmdRunner)
		if !ok {
			return false
		}
		var err error
		handle, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(child.cmd.Process.Pid))
		if err != nil {
			return false
		}
		defer windows.CloseHandle(handle)
	}
	var code uint32
	const stillActive = 259
	return windows.GetExitCodeProcess(handle, &code) == nil && code != stillActive
}
