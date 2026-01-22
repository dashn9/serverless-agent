package executor

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/containerd/cgroups/v3/cgroup2"
)

type Executor struct {
	workDir   string
	runnerUID uint32
	runnerGID uint32
}

func NewExecutor(workDir string) *Executor {
	// Lookup flux-runner user
	u, err := user.Lookup("flux-runner")
	if err != nil {
		log.Fatalf("flux-runner user not found: %v. Please create it first.", err)
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)

	return &Executor{
		workDir:   workDir,
		runnerUID: uint32(uid),
		runnerGID: uint32(gid),
	}
}

func (e *Executor) Execute(ctx context.Context, handler string, args []string, timeoutSec int32, memoryMB int64, env map[string]string) ([]byte, error) {
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, handler, args...)

	// Set working directory to the handler's directory
	workDir := filepath.Dir(handler)
	cmd.Dir = workDir
	log.Printf("Working directory: %s | Handler: %s", workDir, handler)

	// Set environment variables
	if len(env) > 0 {
		envList := os.Environ()
		for k, v := range env {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = envList
	}

	// Capture output
	var outputBuf bytes.Buffer
	cmd.Stdout = &outputBuf
	cmd.Stderr = &outputBuf

	// Run as flux-runner user
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: e.runnerUID,
			Gid: e.runnerGID,
		},
		Setpgid: true,
	}

	// Start the process first
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start: %w", err)
	}

	// Apply memory limit using cgroups v2 if specified
	var manager *cgroup2.Manager
	if memoryMB > 0 {
		cgroupName := fmt.Sprintf("flux-exec-%d", time.Now().UnixNano())
		memoryBytes := memoryMB * 1024 * 1024
		memoryMax := int64(memoryBytes)

		resources := &cgroup2.Resources{
			Memory: &cgroup2.Memory{
				Max: &memoryMax,
			},
		}

		mgr, err := cgroup2.NewManager("/sys/fs/cgroup/flux", "/"+cgroupName, resources)
		if err != nil {
			cmd.Process.Kill()
			return nil, fmt.Errorf("failed to create cgroup: %w", err)
		}
		manager = mgr
		defer manager.Delete()

		// Add process to cgroup
		if err := manager.AddProc(uint64(cmd.Process.Pid)); err != nil {
			cmd.Process.Kill()
			manager.Delete()
			return nil, fmt.Errorf("failed to add process to cgroup: %w", err)
		}
	}

	// Wait for completion
	waitErr := cmd.Wait()
	output := outputBuf.Bytes()

	// Check if timeout occurred
	if execCtx.Err() == context.DeadlineExceeded {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return output, fmt.Errorf("execution timed out after %d seconds", timeoutSec)
	}

	// Return error for any non-zero exit
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return output, fmt.Errorf("exit code %d", exitErr.ExitCode())
		}
		return output, fmt.Errorf("process error: %w", waitErr)
	}

	return output, nil
}
