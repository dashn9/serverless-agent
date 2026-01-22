package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"agent/pkg/config"
	"agent/pkg/executor"
	"agent/pkg/memory"
	pb "agent/proto"

	"google.golang.org/grpc"
)

type AgentServer struct {
	pb.UnimplementedAgentServiceServer
	agentID       string
	maxConcurrent int32
	activeCount   int32
	mu            sync.Mutex
	executor      *executor.Executor
	memory        *memory.RedisMemory
}

func getAppsDir() string {
	u, err := user.Lookup("flux-runner")
	if err != nil {
		log.Printf("Failed to lookup flux-runner: %v, using ./apps", err)
		return "./apps"
	}
	return filepath.Join(u.HomeDir, "apps")
}

func NewAgentServer(agentID string, maxConcurrent int32, mem *memory.RedisMemory) *AgentServer {
	return &AgentServer{
		agentID:       agentID,
		maxConcurrent: maxConcurrent,
		activeCount:   0,
		executor:      executor.NewExecutor(getAppsDir()),
		memory:        mem,
	}
}

func (s *AgentServer) RegisterFunction(ctx context.Context, req *pb.FunctionConfig) (*pb.FunctionAck, error) {
	log.Printf("Registering function: %s", req.Name)

	config := &memory.FunctionConfig{
		Name:    req.Name,
		Handler: req.Handler,
		Resources: memory.ResourceLimits{
			CPU:    req.CpuMillicores,
			Memory: req.MemoryMb,
		},
		Timeout: req.TimeoutSeconds,
		Env:     req.Env,
	}

	if err := s.memory.SaveFunction(config); err != nil {
		return &pb.FunctionAck{Success: false, Message: err.Error()}, nil
	}

	log.Printf("Successfully registered function: %s", req.Name)
	return &pb.FunctionAck{Success: true, Message: "Function registered"}, nil
}

func (s *AgentServer) DeployFunction(ctx context.Context, req *pb.DeploymentPackage) (*pb.DeploymentAck, error) {
	log.Printf("Receiving deployment for function: %s", req.FunctionName)

	// Check if function is registered
	config, err := s.memory.GetFunction(req.FunctionName)
	if err != nil || config == nil {
		return &pb.DeploymentAck{Success: false, Message: "function not registered"}, nil
	}

	// Extract code archive
	deployPath := filepath.Join(getAppsDir(), req.FunctionName)
	if err := os.MkdirAll(deployPath, 0755); err != nil {
		return &pb.DeploymentAck{Success: false, Message: err.Error()}, nil
	}

	// Extract zip (overwrites existing code)
	if err := extractZip(req.CodeArchive, deployPath); err != nil {
		return &pb.DeploymentAck{Success: false, Message: err.Error()}, nil
	}

	// Change ownership to flux-runner
	if err := chownToFluxRunner(deployPath); err != nil {
		return &pb.DeploymentAck{Success: false, Message: fmt.Sprintf("failed to change ownership: %v", err)}, nil
	}

	// Make all files executable
	if err := makeExecutableRecursive(deployPath); err != nil {
		return &pb.DeploymentAck{Success: false, Message: fmt.Sprintf("failed to make files executable: %v", err)}, nil
	}

	log.Printf("Successfully deployed code for function: %s", req.FunctionName)
	return &pb.DeploymentAck{Success: true, Message: "Deployed successfully"}, nil
}

func (s *AgentServer) ExecuteFunction(ctx context.Context, req *pb.ExecutionRequest) (*pb.ExecutionResponse, error) {
	s.mu.Lock()
	if s.activeCount >= s.maxConcurrent {
		s.mu.Unlock()
		log.Printf("Execution rejected: agent at capacity (%d/%d)", s.activeCount, s.maxConcurrent)
		return &pb.ExecutionResponse{Error: "agent at capacity"}, nil
	}
	s.activeCount++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.activeCount--
		s.mu.Unlock()
	}()

	config, err := s.memory.GetFunction(req.FunctionName)
	if err != nil || config == nil {
		log.Printf("Execution failed: function %s not found", req.FunctionName)
		return &pb.ExecutionResponse{Error: "function not found"}, nil
	}

	log.Printf("Executing function: %s | timeout: %ds | memory: %dMB | args: %v",
		req.FunctionName, config.Timeout, config.Resources.Memory, req.Args)

	start := time.Now()
	handlerPath := filepath.Join(getAppsDir(), req.FunctionName, config.Handler)

	output, execErr := s.executor.Execute(ctx, handlerPath, req.Args, config.Timeout, config.Resources.Memory, config.Env)
	duration := time.Since(start).Milliseconds()

	response := &pb.ExecutionResponse{
		Output:     output,
		DurationMs: duration,
	}

	if execErr != nil {
		response.Error = execErr.Error()
		log.Printf("Execution failed: %s | duration: %dms | error: %s", req.FunctionName, duration, execErr.Error())
	} else {
		log.Printf("Execution completed: %s | duration: %dms", req.FunctionName, duration)
	}

	return response, nil
}

func (s *AgentServer) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{
		Healthy: true,
		Version: "1.0.0",
	}, nil
}

func chownToFluxRunner(path string) error {
	u, err := user.Lookup("flux-runner")
	if err != nil {
		return err
	}
	uid, _ := strconv.ParseInt(u.Uid, 10, 32)
	gid, _ := strconv.ParseInt(u.Gid, 10, 32)

	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(name, int(uid), int(gid))
	})
}

func makeExecutableRecursive(path string) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return os.Chmod(name, 0755)
		}
		return nil
	})
}

func extractZip(data []byte, destDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}

	for _, file := range reader.File {
		path := filepath.Join(destDir, file.Name)

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, file.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}

		rc, err := file.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

func main() {
	configPath := os.Getenv("AGENT_CONFIG")
	if configPath == "" {
		configPath = "agent.yaml"
	}

	// Load agent configuration
	agentConfig, err := config.LoadAgentConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load agent config: %v", err)
	}

	// Initialize Redis memory
	mem := memory.NewRedisMemory(agentConfig.RedisAddr)
	defer mem.Close()
	log.Printf("Connected to Redis at %s", agentConfig.RedisAddr)

	agent := NewAgentServer(agentConfig.AgentID, agentConfig.MaxConcurrency, mem)

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", agentConfig.Port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(50*1024*1024), // 50MB
		grpc.MaxSendMsgSize(50*1024*1024), // 50MB
	)
	pb.RegisterAgentServiceServer(grpcServer, agent)

	log.Printf("Agent %s starting on port %s (max concurrency: %d)...", agentConfig.AgentID, agentConfig.Port, agentConfig.MaxConcurrency)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
