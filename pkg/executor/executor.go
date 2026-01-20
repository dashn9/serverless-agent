package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type Executor struct {
	workDir    string
	cgroupBase string
}

func NewExecutor(workDir string) *Executor {
	return &Executor{
		workDir:    workDir,
		cgroupBase: "/sys/fs/cgroup/agent",
	}
}

func (e *Executor) Execute(ctx context.Context, handler string, input []byte, timeoutSec int32, memoryMB int64) ([]byte, error) {
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, e.workDir+"/"+handler)

	// Apply memory limit using cgroups if specified
	var cgroupPath string
	if memoryMB > 0 {
		var err error
		cgroupPath, err = e.setupCgroup(memoryMB)
		if err != nil {
			return nil, fmt.Errorf("failed to setup cgroup: %w", err)
		}
		defer e.cleanupCgroup(cgroupPath)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start: %w", err)
	}

	// Move process to cgroup if created
	if cgroupPath != "" {
		if err := e.addProcessToCgroup(cgroupPath, cmd.Process.Pid); err != nil {
			cmd.Process.Kill()
			return nil, fmt.Errorf("failed to add to cgroup: %w", err)
		}
	}

	// Wait for completion
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	return output, nil
}

func (e *Executor) setupCgroup(memoryMB int64) (string, error) {
	cgroupName := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	cgroupPath := filepath.Join(e.cgroupBase, cgroupName)

	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return "", err
	}

	memoryBytes := memoryMB * 1024 * 1024
	memoryMaxFile := filepath.Join(cgroupPath, "memory.max")
	if err := os.WriteFile(memoryMaxFile, []byte(strconv.FormatInt(memoryBytes, 10)), 0644); err != nil {
		os.RemoveAll(cgroupPath)
		return "", err
	}

	return cgroupPath, nil
}

func (e *Executor) addProcessToCgroup(cgroupPath string, pid int) error {
	procsFile := filepath.Join(cgroupPath, "cgroup.procs")
	return os.WriteFile(procsFile, []byte(strconv.Itoa(pid)), 0644)
}

func (e *Executor) cleanupCgroup(cgroupPath string) {
	if cgroupPath != "" {
		os.RemoveAll(cgroupPath)
	}
}
