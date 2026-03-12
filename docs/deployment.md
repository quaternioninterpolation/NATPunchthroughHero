# Deployment Guide

Production deployment options for NAT Punchthrough Hero.

## Requirements

| Resource | Minimum | Recommended |
|----------|---------|-------------|
| CPU | 1 vCPU | 2 vCPU |
| RAM | 512 MB | 1 GB |
| Disk | 1 GB | 5 GB |
| OS | Any with Docker | Ubuntu 22.04+ |
| Bandwidth | 100 Mbps | 1 Gbps |

### Network Ports

| Port | Protocol | Service | Required |
|------|----------|---------|----------|
| 80 | TCP | HTTP / ACME challenge | For HTTPS only |
| 443 | TCP | HTTPS | For HTTPS only |
| 8080 | TCP | API + WebSocket | Always |
| 3478 | TCP+UDP | STUN/TURN | Always |
| 49152-50175 | UDP | TURN relay | Always |

## Option 1: VPS with Docker (Recommended)

### Automated Deployment

```bash
# SSH into your VPS and run:
curl -sSL https://raw.githubusercontent.com/you/natpunch/main/deploy/deploy-vps.sh | sudo bash
```

This script:
1. Installs Docker & Docker Compose
2. Opens firewall ports (UFW/firewalld)
3. Generates random secrets
4. Starts both services

Credentials are printed at the end. Save them!

### Manual Deployment

```bash
# Install Docker
curl -fsSL https://get.docker.com | sh
systemctl enable docker && systemctl start docker

# Clone and start
git clone https://github.com/you/natpunch.git /opt/natpunch
cd /opt/natpunch

# Edit config
cp config.example.toml config.toml
nano config.toml

# Start with production settings
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### HTTPS with Let's Encrypt

Set your domain in `config.toml`:

```toml
domain = "natpunch.yourdomain.com"
```

Then ensure:
1. DNS A record points to your server IP
2. Ports 80 and 443 are open
3. Restart the server

The Go server automatically obtains and renews TLS certificates via Let's Encrypt.

```bash
docker compose restart server
```

### HTTPS with Custom Certificates

```toml
domain = "natpunch.yourdomain.com"
tls_cert = "/app/certs/fullchain.pem"
tls_key = "/app/certs/privkey.pem"
```

Mount your certificates:
```yaml
# docker-compose.override.yml
services:
  server:
    volumes:
      - /etc/letsencrypt/live/yourdomain:/app/certs:ro
```

## Option 2: Cloud-Init (Any Provider)

When creating a VPS on DigitalOcean, Hetzner, Vultr, Linode, or AWS, paste the contents of `deploy/cloud-init.yml` into the "User Data" field.

The server will be fully configured after first boot (~2-3 minutes).

Credentials: `cat /root/natpunch-credentials.txt`

## Option 3: Bare Metal / Binary

```bash
# Build
cd server
CGO_ENABLED=0 go build -ldflags="-s -w" -o natpunch-server .

# Setup
./natpunch-server setup

# Run (with systemd, see below)
./natpunch-server serve
```

### systemd Service

```ini
# /etc/systemd/system/natpunch.service
[Unit]
Description=NAT Punchthrough Hero
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=natpunch
Group=natpunch
WorkingDirectory=/opt/natpunch
ExecStart=/opt/natpunch/natpunch-server serve
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
LimitNOFILE=65536

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/natpunch
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

```bash
sudo useradd -r -s /bin/false natpunch
sudo systemctl daemon-reload
sudo systemctl enable natpunch
sudo systemctl start natpunch
```

## Updating

### Docker

```bash
cd /opt/natpunch
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

### Binary

```bash
cd /opt/natpunch/server
git pull
go build -ldflags="-s -w" -o ../natpunch-server .
sudo systemctl restart natpunch
```

## Monitoring

### Health Check

```bash
curl http://localhost:8080/api/health
```

### Dashboard

Visit `http://your-server:8080/admin/` for real-time stats.

### Logs

```bash
# Docker
docker compose logs -f server
docker compose logs -f coturn

# systemd
journalctl -u natpunch -f
```

## Scaling Notes

A single instance handles **50-500 concurrent games** easily:
- Each game session uses ~10KB of memory
- WebSocket connections use ~4KB each
- TURN relay is the main bandwidth consumer

For larger scale:
- Increase coturn's `--total-quota` and port range
- Add more CPU/RAM for the Go server
- Consider multiple coturn instances behind DNS round-robin
