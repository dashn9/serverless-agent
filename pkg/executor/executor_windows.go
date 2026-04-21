//go:build windows

package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func NewExecutor(workDir string) *Executor {
	return &Executor{workDir: workDir}
}

func (e *Executor) Execute(ctx context.Context, handler string, args []string, timeoutSec int32, memoryMB int64, env map[string]string, executionID string, logWriter io.Writer) ([]byte, error) {
	var execCtx context.Context
	var cancel context.CancelFunc
	if timeoutSec > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := exec.Command(handler, args...)
	cmd.Dir = filepath.Dir(handler)

	envList := os.Environ()
	envList = append(envList, "FLUX_EXECUTION_ID="+executionID)
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = envList

	var outputBuf bytes.Buffer
	if logWriter != nil {
		cmd.Stdout = io.MultiWriter(&outputBuf, logWriter)
		cmd.Stderr = io.MultiWriter(&outputBuf, logWriter)
	} else {
		cmd.Stdout = &outputBuf
		cmd.Stderr = &outputBuf
	}

	// Create a Job Object so the entire process tree is killed on termination.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	defer windows.CloseHandle(job)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start: %w", err)
	}

	log.Printf("[executor] started pid=%d execID=%s", cmd.Process.Pid, executionID)

	// Assign the process to the job immediately after start.
	ph, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(cmd.Process.Pid))
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("open process: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		log.Printf("[executor] warn: could not assign pid=%d to job: %v", cmd.Process.Pid, err)
	}
	windows.CloseHandle(ph)

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case waitErr := <-waitCh:
		output := outputBuf.Bytes()
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				return output, fmt.Errorf("exit code %d", exitErr.ExitCode())
			}
			return output, fmt.Errorf("process error: %w", waitErr)
		}
		return output, nil

	case <-execCtx.Done():
		reason := execCtx.Err()
		log.Printf("[executor] cancelling pid=%d execID=%s reason=%v", cmd.Process.Pid, executionID, reason)
		windows.TerminateJobObject(job, 1)

		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
			log.Printf("[executor] warn: cmd.Wait() still blocked after job termination execID=%s", executionID)
		}

		output := outputBuf.Bytes()
		if reason == context.DeadlineExceeded {
			return output, fmt.Errorf("execution timed out after %ds", timeoutSec)
		}
		return output, ctx.Err()
	}
}
