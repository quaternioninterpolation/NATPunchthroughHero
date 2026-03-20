# Security Guide

Hardening NAT Punchthrough Hero for production.

## Threat Model

| Threat | Impact | Mitigation |
|--------|--------|------------|
| Unauthorized game registration | Spam/fake games | API key authentication |
| Unauthorized game joining | Unwanted players | Optional per-game passwords (SHA-256 hashed) |
| IP harvesting from game list | Player privacy | Minimal public data, no IPs exposed |
| DDoS on API | Service availability | Multi-layer rate limiting, auto-block |
| coturn as open relay | Bandwidth abuse, SSRF | denied-peer-ip rules, quotas |
| Dashboard exposure | Admin takeover | HTTP Basic Auth, localhost binding option |
| MITM on signaling | Connection hijack | TLS encryption |
| Brute-force admin login | Credential theft | Rate limiting on admin endpoints |
| Secret leakage in logs | Credential exposure | Secrets redacted in logs |

## Authentication

### API Key (Game Clients)

Game clients authenticate via the `X-API-Key` header:

```
X-API-Key: your-48-char-hex-key
```

Set in config:
```toml
game_api_key = "your-key-here"
```

If `game_api_key` is empty, API authentication is **disabled** (development mode).

### Game Passwords (Per-Game)

Games can optionally be password-protected. When hosting, set the `password` field in the registration request. The server stores a SHA-256 hash — the plaintext password is never persisted or returned in any API response.

When joining via WebSocket signaling, the client sends the password in the `request_join` message. The server validates it before initiating the NAT punch session. Returns `wrong_password` on failure — the same error whether the password is missing or incorrect, to avoid leaking which games are password-protected.

### Admin Dashboard (HTTP Basic Auth)

The admin dashboard uses HTTP Basic Authentication:
- Username: `admin`
- Password: set via `admin_password` in config

```bash
curl -u admin:yourpassword http://localhost:8080/admin/api/stats
```

### TURN Credentials (HMAC-SHA1)

TURN credentials are generated server-side using HMAC-SHA1 (RFC 5766):
- Time-limited (default: 24 hours)
- Per-session (unique username per request)
- The shared secret is **never** sent to clients

Flow:
1. Client requests `/api/games/{id}/turn`
2. Server generates HMAC credentials
3. Client uses credentials with coturn
4. coturn validates HMAC independently

## TLS / HTTPS

### Automatic (Let's Encrypt)

```toml
domain = "natpunch.example.com"
```

Requires:
- DNS A record pointing to server
- Ports 80 + 443 open
- No other service on port 80/443

### Custom Certificates

```toml
domain = "natpunch.example.com"
tls_cert = "/path/to/fullchain.pem"
tls_key = "/path/to/privkey.pem"
```

### Behind a Reverse Proxy

If using nginx/Cloudflare/Traefik in front:
1. Terminate TLS at the proxy
2. Set trusted proxy CIDRs
3. Ensure `X-Forwarded-For` headers are passed

```toml
[trusted_proxies]
cidrs = ["10.0.0.0/8", "172.16.0.0/12"]
```

## Rate Limiting

Seven independent rate limiting layers:

| Layer | Default | Scope |
|-------|---------|-------|
| Global RPS | 100/s | All requests |
| Per-IP RPM | 60/min | Per IP address |
| Per-IP Burst | 10 | Instantaneous burst per IP |
| WebSocket concurrent | 5/IP | WebSocket connections |
| WebSocket messages | 10/s | Per WebSocket connection |
| Game registration | 5/min | POST /api/games per IP |
| TURN requests | 10/min | TURN credential requests per IP |

Rate-limited clients receive `429 Too Many Requests`.

## Automatic Abuse Detection

The protection system tracks rate limit violations and auto-blocks repeat offenders:

```toml
[protection]
enabled = true
auto_block_threshold = 10    # violations before block
auto_block_window = "60s"     # violation counting window
auto_block_duration = "1h"    # first block duration
max_block_duration = "24h"    # maximum block duration
escalation_factor = 2.0       # duration multiplier per offense
```

### How It Works

1. Rate limit violation → recorded
2. 10 violations in 60 seconds → IP auto-blocked for 1 hour
3. Same IP offends again → 2 hours, then 4h, 8h, 16h, 24h (cap)
4. After 7 days clean → offense history cleared

Auto-blocked IPs appear in the admin dashboard under "Security" → "Auto-Blocked IPs".

## IP Filtering

### Blocklist Mode

Block specific IPs/CIDRs:
```toml
[ip_filter]
mode = "blocklist"
blocklist = ["1.2.3.4", "10.0.0.0/8"]
```

### Allowlist Mode

Only allow specific IPs (block everything else):
```toml
[ip_filter]
mode = "allowlist"
allowlist = ["192.168.1.0/24"]
```

### File-Based Lists

For large lists, use files:
```toml
[ip_filter]
mode = "blocklist"
blocklist_file = "/opt/natpunch/blocklist.txt"
```

File format (one per line):
```
# Comments allowed
1.2.3.4
10.0.0.0/8
192.168.1.100
```

Reload on file change:
```bash
kill -HUP $(pidof natpunch-server)
```

## coturn Security

coturn is configured with these security measures:

### Denied Peer IPs (SSRF Prevention)

All private/reserved IP ranges are blocked to prevent SSRF via TURN relay:

```
--denied-peer-ip=0.0.0.0-0.255.255.255
--denied-peer-ip=10.0.0.0-10.255.255.255
--denied-peer-ip=127.0.0.0-127.255.255.255
--denied-peer-ip=172.16.0.0-172.31.255.255
--denied-peer-ip=192.168.0.0-192.168.255.255
```

### Resource Limits

```
--max-bps=256000        # 256 Kbps per session (sufficient for game traffic)
--user-quota=5          # Max 5 relay sessions per user
--total-quota=300       # Max 300 total relay sessions
--no-tcp-relay          # UDP only (games don't need TCP relay)
--no-tls --no-dtls      # TLS handled by the Go server
```

### Authentication

coturn uses `use-auth-secret` mode:
- Time-limited HMAC credentials
- No static user/password database
- Shared secret never leaves the server

## Security Headers

The Go server sets these headers on all responses:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
X-XSS-Protection: 1; mode=block
Content-Security-Policy: default-src 'self'
Referrer-Policy: no-referrer
```

## Request Validation

- Maximum request body: 64 KB
- Game names sanitized (HTML stripped, length limited)
- JSON-only content type required for POST/PUT
- WebSocket message size limited to 4 KB

## Production Checklist

- [ ] Set a strong `game_api_key`
- [ ] Set a strong `admin_password`
- [ ] Enable TLS (domain + autocert or custom certs)
- [ ] Enable rate limiting
- [ ] Enable protection (auto-block)
- [ ] Configure trusted proxies if behind a reverse proxy
- [ ] Verify firewall rules (only needed ports open)
- [ ] Run with Docker production overlay (resource limits, read-only)
- [ ] Test with `./natpunch-server check` for diagnostic validation
- [ ] Monitor logs for suspicious activity
