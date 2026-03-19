# Flux Agent

The agent is the worker component of Serverless Fabric. It receives function deployments from [Flux](https://github.com/dashn9/serverless-flux) (the control plane) over gRPC, executes them natively under cgroups v2 memory limits, and reports node metrics back for autoscaling decisions.

Each agent runs as root to manage cgroups. Functions are deployed to `/home/flux-runner/apps/` and execute as the unprivileged `flux-runner` system user.

## Install from .deb

Download the latest release from [GitHub Releases](https://github.com/dashn9/serverless-agent/releases):

```bash
sudo dpkg -i flux-agent_*.deb
```

The package creates the `flux-runner` user, installs a systemd service, and places an example config at `/etc/flux-agent/agent.yaml.example`.

## Manual Setup

### Create flux-runner User

```bash
sudo useradd -r -m -s /bin/bash flux-runner
```

### Build and Install

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o flux-agent .
sudo install -Dm755 flux-agent /usr/local/bin/flux-agent
```

## Configuration

Copy `example.agent.yaml` to `agent.yaml` and edit:

```yaml
agent_id: agent-1
port: 50051
max_concurrency: 10
redis_addr: redis://localhost:6379

# mTLS (required for production)
# grpc:
#   ca_cert: /etc/flux-agent/certs/ca.pem
#   cert:    /etc/flux-agent/certs/agent.pem
#   key:     /etc/flux-agent/certs/agent.key
```

## Run

```bash
# Directly
sudo ./flux-agent

# Or via systemd (after .deb install)
sudo cp /etc/flux-agent/agent.yaml.example /etc/flux-agent/agent.yaml
# edit agent.yaml
sudo systemctl enable --now flux-agent
```

The agent self-registers with Flux by calling `POST /nodes/register` on startup.

## gRPC Services

| RPC | Description |
|-----|-------------|
| `RegisterFunction` | Stores function metadata on this agent |
| `DeployFunction` | Receives a zip archive and extracts it to the function's app directory |
| `ExecuteFunction` | Runs the function handler with cgroup isolation and returns output |
| `HealthCheck` | Liveness probe used by Flux's health poller |
| `ReportNodeStatus` | Returns CPU, memory, active tasks, and uptime for autoscaling |
