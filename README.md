# Flux Agent Setup

## ⚠️ SECURITY WARNING

**Agent gRPC communication is currently INSECURE**

- No TLS encryption (all traffic in plaintext)
- No authentication (anyone can connect and execute functions)
- Functions receive deployment code and secrets unencrypted

**PRIORITY**: Implement mTLS before production use.

## Create flux-runner User

```bash
sudo useradd -r -m -s /bin/bash flux-runner
```

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

Agent runs as root to manage cgroups. Functions deploy to `/home/flux-runner/apps/` and execute as flux-runner user.
