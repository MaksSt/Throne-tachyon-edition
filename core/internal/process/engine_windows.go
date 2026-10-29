//go:build windows

package process

import (
	"fmt"
	"golang.org/x/sys/windows"
	"sync"
	"unsafe"
)

// Windows closes the native engine's job if its controller crashes or is killed.
func startEngine(path string, args []string, output *engineOutput) (running, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	cmd := newCmd(path, args, false)
	cmd.Stdout = output
	cmd.Stderr = output
	run, err := startCmd(cmd)
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		windows.CloseHandle(handle)
	}
	if err != nil {
		_ = run.Kill()
		_ = run.Wait()
		windows.CloseHandle(job)
		return nil, fmt.Errorf("attach native engine to controller lifetime: %w", err)
	}
	return &engineJob{run: run, job: job}, nil
}

type engineJob struct {
	run running
	mu  sync.Mutex
	job windows.Handle
}

func (j *engineJob) Wait() error {
	err := j.run.Wait()
	j.mu.Lock()
	if j.job != 0 {
		windows.CloseHandle(j.job)
		j.job = 0
	}
	j.mu.Unlock()
	return err
}
func (j *engineJob) Kill() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(j.job, 1)
}
