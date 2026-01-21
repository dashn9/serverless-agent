# Flux Agent Setup

## Install Agent

```bash
chmod +x agent-linux
```

## Configure Agent

Create `agent.yaml`:
```yaml
agent_id: agent-1
port: 50051
max_concurrency: 10
redis_addr: redis://localhost:6379
```

## Run Agent

```bash
sudo ./agent-linux
```

Agent runs as root to manage cgroups. Functions deploy to `~/apps/` and execute as root.
