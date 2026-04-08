# Serverless Fabric — Agent

> The worker component of Serverless Fabric. Executes functions natively on Linux using cgroups v2 for memory isolation — no Docker required.

Agents receive function deployments and execution requests from [Flux](https://github.com/dashn9/serverless-flux) (the control plane) over mTLS gRPC. Each execution runs as an isolated process under the unprivileged `flux-runner` user with a cgroup memory limit.

## How It Works

- Functions are deployed as zip archives and extracted to `/home/flux-runner/apps/{name}/`
- Sync executions block until the process exits and return stdout/stderr to Flux
- Async executions run in a background goroutine; the agent writes live output and status to Redis for Flux to surface via `GET /executions/{id}`
- Cancellation is handled directly on the agent — the process group is killed immediately on `CancelExecution`
- The agent self-registers with Flux on boot via `POST /nodes/register`

## Install

**From .deb** (recommended — also creates the `flux-runner` user and systemd service):
```bash
sudo dpkg -i flux-agent_*.deb
```
Releases: [GitHub Releases](https://github.com/dashn9/serverless-agent/releases)

**From source:**
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o flux-agent .
sudo useradd -r -m -s /bin/bash flux-runner
sudo install -Dm755 flux-agent /usr/local/bin/flux-agent
```

## Configuration

See [`example.agent.yaml`](example.agent.yaml) for a fully annotated reference. Minimal config:

| Field | Default | Description |
|-------|---------|-------------|
| `agent_id` | — | Unique ID used to register with Flux |
| `port` | `50052` | gRPC listen port |
| `max_concurrency` | `10` | Max concurrent executions across all functions |
| `redis_addr` | `redis://localhost:6379` | Must point to the same Redis instance as Flux |

When provisioned by Flux, mTLS certificates are generated and uploaded automatically during SSH bootstrap. For manually configured agents, set `tls.enabled: true` and provide `cert_file`, `key_file`, and `ca_cert` paths.

## Run

```bash
# Via systemd (after .deb install)
sudo cp /etc/flux-agent/agent.yaml.example /etc/flux-agent/agent.yaml
# edit agent.yaml
sudo systemctl enable --now flux-agent

# Directly
sudo AGENT_CONFIG=agent.yaml ./flux-agent
```

The agent must run as root to manage cgroups.

## gRPC Services

| RPC | Description |
|-----|-------------|
| `RegisterFunction` | Stores function metadata (handler, limits, env, concurrency) |
| `DeployFunction` | Receives a zip archive and extracts it to the function's app directory |
| `ExecuteFunction` | Runs the function handler — sync blocks and returns output; async returns immediately and runs in background |
| `CancelExecution` | Cancels a running async execution by killing the process group |
| `HealthCheck` | Liveness probe used by Flux's health poller |
| `ReportNodeStatus` | Returns CPU, memory, active tasks, and uptime for autoscaling |

## Related

- [serverless-flux](https://github.com/dashn9/serverless-flux) — the control plane
