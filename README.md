# Serverless Fabric — Agent

> The worker component of Serverless Fabric. Executes functions natively as isolated processes.

Agents receive function deployments and execution requests from [Flux](https://github.com/dashn9/serverless-flux) (the control plane) over mTLS gRPC.

## How It Works

- Functions are deployed as zip archives and extracted to the agent's `apps/{name}/` directory
- Sync executions block until the process exits and return stdout/stderr to Flux
- Async executions run in a background goroutine; the agent writes live output and final status to Redis
- Cancellation is handled directly on the agent — the process (group on Linux) is killed immediately on `CancelExecution`
- Flux registers agents by probing them at a given address via `POST /agents/register` — the agent does not need to self-register

## Platform Support

| Feature | Linux | Windows |
|---------|-------|---------|
| Function execution | Yes | Yes |
| Memory isolation (cgroups v2) | Yes | No |
| Process tree kill on cancel | Yes | Yes (`taskkill /F /T`) |
| `flux-runner` user isolation | Yes | No |

On Windows, functions run as the agent's own user with no memory cap. All other features (gRPC, Redis, async, cancel, live output) work identically.

## Install

**Linux — from .deb** (recommended — also creates the `flux-runner` user and systemd service):
```bash
sudo dpkg -i flux-agent_*.deb
```
Releases: [GitHub Releases](https://github.com/dashn9/serverless-agent/releases)

**From source (Linux):**
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o flux-agent .
sudo useradd -r -m -s /bin/bash flux-runner
sudo install -Dm755 flux-agent /usr/local/bin/flux-agent
```

**From source (Windows):**
```powershell
go build -o flux-agent.exe .
```

## Configuration

See [`example.agent.yaml`](example.agent.yaml) for a fully annotated reference. Minimal config:

| Field | Default | Description |
|-------|---------|-------------|
| `agent_id` | — | Unique ID used to identify this agent in the fleet |
| `port` | `50052` | gRPC listen port |
| `redis_addr` | `redis://localhost:6379` | Redis URL for execution state and function metadata |

When provisioned by Flux, `agent_id`, `redis_addr`, mTLS certificates, and config are written automatically during SSH bootstrap. For manually registered agents, create an `agent.yaml` and optionally set `tls.enabled: true` with `cert_file`, `key_file`, and `ca_cert` paths.

### Network

```yaml
network:
  node_private_ip: 10.0.0.5   # optional — auto-detected from interfaces
  node_public_ip: 1.2.3.4     # optional — queried from api.ipify.org
```

If omitted, the agent auto-detects both on startup and injects them into every execution as:

- `FLUX_NODE_PRIVATE_IP` — first non-loopback IPv4 from local interfaces
- `FLUX_NODE_PUBLIC_IP` — result of querying `api.ipify.org` (5 s timeout)

Override either field if auto-detection picks the wrong address.

## Run

```bash
# Via systemd (Linux, after .deb install)
sudo cp /etc/flux-agent/agent.yaml.example /etc/flux-agent/agent.yaml
# edit agent.yaml
sudo systemctl enable --now flux-agent

# Directly (Linux — must run as root for cgroups)
sudo AGENT_CONFIG=agent.yaml ./flux-agent

# Directly (Windows)
$env:AGENT_CONFIG="agent.yaml"; .\flux-agent.exe
```

## gRPC Services

| RPC | Description |
|-----|-------------|
| `RegisterFunction` | Stores function metadata (handler, limits, env, concurrency) |
| `DeployFunction` | Receives a zip archive and extracts it to the function's app directory |
| `ExecuteFunction` | Runs the function — sync blocks and returns output; async returns immediately and runs in background |
| `CancelExecution` | Cancels a running async execution by killing the process |
| `GetExecution` | Returns the current execution record (status, output, duration) for a given execution ID |
| `HealthCheck` | Liveness probe used by Flux's health poller |
| `ReportNodeStatus` | Returns CPU, memory, active tasks, and uptime; also used by Flux to identify the agent on registration |

## Related

- [serverless-flux](https://github.com/dashn9/serverless-flux) — the control plane
