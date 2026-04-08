//go:build linux

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"agent/pkg/monitor"
	pb "agent/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type executionLogWriter struct {
	mem          *memory.RedisMemory
	executionID  string
	agentID      string
	functionName string
	startedAt    time.Time
	mu           sync.Mutex
	buf          []byte
}

func (w *executionLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf = append(w.buf, p...)
	snapshot := make([]byte, len(w.buf))
	copy(snapshot, w.buf)
	w.mu.Unlock()

	if err := w.mem.SaveExecution(w.executionID, w.agentID, w.functionName, "running", "", snapshot, 0, w.startedAt, nil); err != nil {
		log.Printf("[exec-writer] failed to save execution log for %s: %v", w.executionID, err)
	}
	return len(p), nil
}

type AgentServer struct {
	pb.UnimplementedAgentServiceServer
	agentID          string
	funcActiveCounts map[string]int32
	mu               sync.Mutex
	executor         *executor.Executor
	memory           *memory.RedisMemory
	monitor          *monitor.NodeMonitor

	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

func getAppsDir() string {
	u, err := user.Lookup("flux-runner")
	if err != nil {
		log.Printf("Failed to lookup flux-runner: %v, using ./apps", err)
		return "./apps"
	}
	return filepath.Join(u.HomeDir, "apps")
}

func NewAgentServer(agentID string, mem *memory.RedisMemory, mon *monitor.NodeMonitor) *AgentServer {
	return &AgentServer{
		agentID:          agentID,
		funcActiveCounts: make(map[string]int32),
		executor:         executor.NewExecutor(getAppsDir()),
		memory:           mem,
		monitor:          mon,
		cancels:          make(map[string]context.CancelFunc),
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
		Timeout:                req.TimeoutSeconds,
		Env:                    req.Env,
		MaxConcurrency:         req.MaxConcurrency,
		MaxConcurrencyBehavior: req.MaxConcurrencyBehavior,
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
	cfg, err := s.memory.GetFunction(req.FunctionName)
	if err != nil || cfg == nil {
		log.Printf("Execution failed: function %s not found", req.FunctionName)
		return &pb.ExecutionResponse{Error: "function not found"}, nil
	}

	s.mu.Lock()
	if cfg.MaxConcurrency > 0 && s.funcActiveCounts[req.FunctionName] >= cfg.MaxConcurrency {
		s.mu.Unlock()
		log.Printf("Execution rejected: function %s at capacity (%d/%d)", req.FunctionName, s.funcActiveCounts[req.FunctionName], cfg.MaxConcurrency)
		return &pb.ExecutionResponse{Error: fmt.Sprintf("function %s at capacity", req.FunctionName)}, nil
	}
	s.funcActiveCounts[req.FunctionName]++
	s.mu.Unlock()

	if req.Async {
		execCtx, cancel := context.WithCancel(context.Background())
		s.cancelMu.Lock()
		s.cancels[req.ExecutionId] = cancel
		s.cancelMu.Unlock()

		go s.runExecution(execCtx, cancel, req, cfg)
		return &pb.ExecutionResponse{}, nil
	}

	defer func() {
		s.mu.Lock()
		s.funcActiveCounts[req.FunctionName]--
		s.mu.Unlock()
	}()

	return s.runExecutionSync(ctx, req, cfg)
}

func (s *AgentServer) runExecution(ctx context.Context, cancel context.CancelFunc, req *pb.ExecutionRequest, cfg *memory.FunctionConfig) {
	defer func() {
		cancel()
		s.cancelMu.Lock()
		delete(s.cancels, req.ExecutionId)
		s.cancelMu.Unlock()
		s.mu.Lock()
		s.funcActiveCounts[req.FunctionName]--
		s.mu.Unlock()
	}()

	now := time.Now()
	s.memory.SaveExecution(req.ExecutionId, s.agentID, req.FunctionName, "running", "", nil, 0, now, nil)

	handlerPath := filepath.Join(getAppsDir(), req.FunctionName, cfg.Handler)
	logWriter := &executionLogWriter{
		mem:          s.memory,
		executionID:  req.ExecutionId,
		agentID:      s.agentID,
		functionName: req.FunctionName,
		startedAt:    now,
	}

	log.Printf("Executing async function: %s | execution_id: %s | timeout: %ds | memory: %dMB | args: %v",
		req.FunctionName, req.ExecutionId, cfg.Timeout, cfg.Resources.Memory, req.Args)

	start := time.Now()
	output, execErr := s.executor.Execute(ctx, handlerPath, req.Args, cfg.Timeout, cfg.Resources.Memory, cfg.Env, req.ExecutionId, logWriter)
	duration := time.Since(start).Milliseconds()
	statusAt := time.Now()

	status := "success"
	errMsg := ""
	if ctx.Err() != nil {
		status = "cancelled"
		errMsg = "execution cancelled"
		log.Printf("Async execution cancelled: %s | execution_id: %s", req.FunctionName, req.ExecutionId)
	} else if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
		log.Printf("Async execution failed: %s | execution_id: %s | duration: %dms | error: %s", req.FunctionName, req.ExecutionId, duration, errMsg)
	} else {
		log.Printf("Async execution completed: %s | execution_id: %s | duration: %dms", req.FunctionName, req.ExecutionId, duration)
	}

	s.memory.SaveExecution(req.ExecutionId, s.agentID, req.FunctionName, status, errMsg, output, duration, now, &statusAt)
}

func (s *AgentServer) runExecutionSync(ctx context.Context, req *pb.ExecutionRequest, cfg *memory.FunctionConfig) (*pb.ExecutionResponse, error) {
	log.Printf("Executing function: %s | timeout: %ds | memory: %dMB | args: %v",
		req.FunctionName, cfg.Timeout, cfg.Resources.Memory, req.Args)

	handlerPath := filepath.Join(getAppsDir(), req.FunctionName, cfg.Handler)

	var logWriter io.Writer

	start := time.Now()
	output, execErr := s.executor.Execute(ctx, handlerPath, req.Args, cfg.Timeout, cfg.Resources.Memory, cfg.Env, req.ExecutionId, logWriter)
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

func (s *AgentServer) CancelExecution(ctx context.Context, req *pb.CancelExecutionRequest) (*pb.CancelExecutionResponse, error) {
	s.cancelMu.Lock()
	cancel, ok := s.cancels[req.ExecutionId]
	s.cancelMu.Unlock()

	if !ok {
		return &pb.CancelExecutionResponse{Success: false, Message: "execution not found or already completed"}, nil
	}

	cancel()
	log.Printf("Execution cancelled: %s", req.ExecutionId)
	return &pb.CancelExecutionResponse{Success: true}, nil
}

func (s *AgentServer) GetExecution(ctx context.Context, req *pb.GetExecutionRequest) (*pb.GetExecutionResponse, error) {
	data, err := s.memory.GetExecution(req.ExecutionId)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return &pb.GetExecutionResponse{Found: false}, nil
	}
	return &pb.GetExecutionResponse{Found: true, Data: data}, nil
}

func (s *AgentServer) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	return &pb.HealthCheckResponse{
		Healthy: true,
		Version: "1.0.0",
	}, nil
}

func (s *AgentServer) ReportNodeStatus(ctx context.Context, req *pb.NodeStatusRequest) (*pb.NodeStatusResponse, error) {
	snap := s.monitor.Snapshot()

	s.mu.Lock()
	var activeTasks int32
	for _, count := range s.funcActiveCounts {
		activeTasks += count
	}
	s.mu.Unlock()

	return &pb.NodeStatusResponse{
		AgentId:       s.agentID,
		CpuPercent:    snap.CPUPercent,
		MemoryPercent: snap.MemPercent,
		MemoryTotalMb: snap.MemTotalMB,
		MemoryUsedMb:  snap.MemUsedMB,
		ActiveTasks:   activeTasks,
		UptimeSeconds: snap.UptimeSec,
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

// loadTLSCredentials builds mTLS server credentials from the agent's TLS config.
// The agent presents its own cert/key to Flux, and verifies Flux's cert against the CA.
func loadTLSCredentials(tlsCfg *config.TLSConfig) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load agent cert/key: %w", err)
	}

	caData, err := os.ReadFile(tlsCfg.CACert)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	return credentials.NewTLS(cfg), nil
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

	// Start node monitor (samples every 5 seconds)
	mon := monitor.NewNodeMonitor(5 * time.Second)
	log.Printf("Node monitor started (sampling every 5s)")

	agent := NewAgentServer(agentConfig.AgentID, mem, mon)

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", agentConfig.Port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	serverOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(50 * 1024 * 1024),
		grpc.MaxSendMsgSize(50 * 1024 * 1024),
	}

	if agentConfig.TLS != nil && agentConfig.TLS.Enabled {
		creds, err := loadTLSCredentials(agentConfig.TLS)
		if err != nil {
			log.Fatalf("Failed to load TLS credentials: %v", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
		log.Printf("mTLS enabled on agent gRPC server")
	} else {
		serverOpts = append(serverOpts, grpc.Creds(insecure.NewCredentials()))
	}

	grpcServer := grpc.NewServer(serverOpts...)
	pb.RegisterAgentServiceServer(grpcServer, agent)

	log.Printf("Agent %s starting on port %s...", agentConfig.AgentID, agentConfig.Port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
