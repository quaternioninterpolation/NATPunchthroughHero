# Quick Start Guide

Get NAT Punchthrough Hero running in under 5 minutes.

## Prerequisites

- Docker & Docker Compose **OR** Go 1.23+
- A machine with a public IP (for production), or localhost (for testing)

## Option 1: Docker (Recommended)

```bash
# Clone the repo
git clone https://github.com/you/natpunch.git
cd natpunch

# Start both services (Go server + coturn)
docker compose up

# Or run in background
docker compose up -d
```

That's it. The server is running:
- **API**: http://localhost:8080/api/health
- **Dashboard**: http://localhost:8080/admin/ (user: `admin`, pass: check logs)
- **STUN/TURN**: localhost:3478

On first run, secrets are auto-generated. Check the server logs for your admin password and API key:

```bash
docker compose logs server | grep -E "admin|api.key"
```

## Option 2: Go Binary

```bash
cd server
go build -o natpunch-server .

# Run the interactive setup wizard
./natpunch-server setup

# Start the server
./natpunch-server serve
```

The setup wizard will:
1. Detect your external IP
2. Ask about TLS/HTTPS
3. Generate random secrets
4. Write `config.toml`

> **Note:** You still need coturn for STUN/TURN. Install it separately or use Docker for coturn only.

## Verify It's Working

```bash
# Health check
curl http://localhost:8080/api/health

# Expected response:
# {"status":"ok","version":"dev","games":0,"uptime":"5s"}

# List games (empty at first)
curl http://localhost:8080/api/games
# []
```

## Test with the CLI Client

```bash
cd test-client

# Check health
go run . health -server http://localhost:8080

# Host a test game
go run . host -name "My Test Game" -server http://localhost:8080

# In another terminal, list games
go run . list -server http://localhost:8080

# Join the game
go run . join -code <CODE_FROM_HOST> -server http://localhost:8080
```

## Next Steps

- [Deployment Guide](deployment.md) — Set up on a VPS with HTTPS
- [Configuration](configuration.md) — Customize all settings
- [Unity SDK](unity-sdk.md) — Integrate with your Unity game
- [Security](security.md) — Harden for production
