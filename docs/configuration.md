# Configuration Reference

All settings for NAT Punchthrough Hero, organized by config file section.

## Config Priority

Settings are resolved in this order (highest priority first):

1. **CLI flags** — `./natpunch-server serve -http-port 9090`
2. **Environment variables** — `EXTERNAL_IP=1.2.3.4`
3. **Config file** — `config.toml`
4. **Defaults** — Built-in sensible defaults

## Config File Location

The server looks for `config.toml` in:
1. Path specified by `-config` flag
2. `./config.toml` (current directory)
3. `/app/config.toml` (Docker default)

Generate a starter config:
```bash
cp config.example.toml config.toml
```

## Top-Level Settings

| Key | Env Var | Default | Description |
|-----|---------|---------|-------------|
| `external_ip` | `EXTERNAL_IP` | `"auto"` | Public IP. `"auto"` detects via ipify/ifconfig.me |
| `domain` | `DOMAIN` | `""` | Domain for TLS. Empty = HTTP only |
| `http_port` | `HTTP_PORT` | `8080` | REST API + WebSocket port |
| `tls_cert` | `TLS_CERT` | `""` | Path to TLS certificate (overrides autocert) |
| `tls_key` | `TLS_KEY` | `""` | Path to TLS private key |
| `turn_secret` | `TURN_SECRET` | auto-generated | Shared secret for TURN HMAC auth |
| `turn_port` | `TURN_PORT` | `3478` | STUN/TURN listening port |
| `turn_ttl` | `TURN_TTL` | `86400` | TURN credential lifetime (seconds) |
| `admin_password` | `ADMIN_PASSWORD` | auto-generated | Dashboard admin password |
| `game_api_key` | `GAME_API_KEY` | `""` | API key for game client auth. Empty = no auth |
| `game_ttl` | `GAME_TTL` | `300` | Game session TTL without heartbeat (seconds) |
| `max_games` | `MAX_GAMES` | `1000` | Maximum concurrent games |
| `log_level` | `LOG_LEVEL` | `"info"` | Log level: debug, info, warn, error |

### Auto-Generated Secrets

On first run, if these are empty, the server generates cryptographically random values:
- `turn_secret` — 64-char hex string
- `admin_password` — 16-char alphanumeric
- `game_api_key` — 48-char hex string

These are logged once at startup and saved to `config.toml`.

## `[rate_limit]` Section

```toml
[rate_limit]
enabled = true
global_rps = 100
per_ip_rpm = 60
per_ip_burst = 10
ws_per_ip = 5
ws_msg_per_sec = 10
game_reg_per_min = 5
join_per_min = 20
turn_per_min = 10
```

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `true` | Enable/disable all rate limiting |
| `global_rps` | `100` | Global requests per second |
| `per_ip_rpm` | `60` | Max requests per IP per minute |
| `per_ip_burst` | `10` | Burst allowance per IP |
| `ws_per_ip` | `5` | Max WebSocket connections per IP |
| `ws_msg_per_sec` | `10` | Max WebSocket messages per second per connection |
| `game_reg_per_min` | `5` | Game registrations per IP per minute |
| `join_per_min` | `20` | Join requests per IP per minute |
| `turn_per_min` | `10` | TURN credential requests per IP per minute |

## `[ip_filter]` Section

```toml
[ip_filter]
mode = "off"
blocklist = ["192.168.1.100", "10.0.0.0/8"]
allowlist = []
blocklist_file = ""
allowlist_file = ""
```

| Key | Default | Description |
|-----|---------|-------------|
| `mode` | `"off"` | `"off"`, `"blocklist"`, or `"allowlist"` |
| `blocklist` | `[]` | IPs/CIDRs to block |
| `allowlist` | `[]` | IPs/CIDRs to allow (blocks all others) |
| `blocklist_file` | `""` | Path to file with one IP/CIDR per line |
| `allowlist_file` | `""` | Path to file with one IP/CIDR per line |

### Modes

- **`off`** — No IP filtering (default)
- **`blocklist`** — Block listed IPs, allow all others
- **`allowlist`** — Allow only listed IPs, block all others

### Runtime Changes

IPs can be added/removed at runtime via the admin API:
```bash
# Block an IP
curl -X POST http://localhost:8080/admin/api/blocklist \
  -u admin:password -d '{"ip":"1.2.3.4"}'

# Unblock
curl -X DELETE http://localhost:8080/admin/api/blocklist/1.2.3.4 \
  -u admin:password
```

### Reload from File

Send SIGHUP to reload blocklist/allowlist files:
```bash
kill -HUP $(pidof natpunch-server)
# or
docker compose kill -s HUP server
```

## `[protection]` Section

```toml
[protection]
enabled = true
auto_block_threshold = 10
auto_block_window = "60s"
auto_block_duration = "1h"
max_block_duration = "24h"
escalation_factor = 2.0
offense_retention = "168h"
```

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `true` | Enable automatic abuse detection |
| `auto_block_threshold` | `10` | Rate limit violations before auto-block |
| `auto_block_window` | `"60s"` | Window for counting violations |
| `auto_block_duration` | `"1h"` | First auto-block duration |
| `max_block_duration` | `"24h"` | Maximum escalated block duration |
| `escalation_factor` | `2.0` | Block duration multiplier per offense |
| `offense_retention` | `"168h"` | How long to remember offenses (7 days) |

### Escalation Example

For a repeat offender with default settings:
1. First offense: blocked 1 hour
2. Second offense: blocked 2 hours
3. Third offense: blocked 4 hours
4. Fourth offense: blocked 8 hours
5. Fifth+ offense: blocked 24 hours (cap)

After 7 days without offenses, the slate is wiped clean.

## `[trusted_proxies]` Section

```toml
[trusted_proxies]
cidrs = ["127.0.0.1/32", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
```

When behind a reverse proxy (nginx, Cloudflare, etc.), configure trusted proxy CIDRs so the server reads the real client IP from `X-Forwarded-For`.

**Cloudflare example:**
```toml
[trusted_proxies]
cidrs = [
  "173.245.48.0/20",
  "103.21.244.0/22",
  "103.22.200.0/22",
  "103.31.4.0/22",
  "141.101.64.0/18",
  "108.162.192.0/18",
  "190.93.240.0/20",
  "188.114.96.0/20",
  "197.234.240.0/22",
  "198.41.128.0/17",
  "162.158.0.0/15",
  "104.16.0.0/13",
  "104.24.0.0/14",
  "172.64.0.0/13",
  "131.0.72.0/22",
]
```

## Environment Variable Mapping

All config keys can be set via environment variables. The mapping uses UPPER_SNAKE_CASE:

```bash
# Top-level
export EXTERNAL_IP=auto
export DOMAIN=natpunch.example.com
export HTTP_PORT=8080
export TURN_SECRET=mysecret
export ADMIN_PASSWORD=mypassword
export GAME_API_KEY=myapikey

# Nested sections use underscore prefix
export RATE_LIMIT_ENABLED=true
export RATE_LIMIT_GLOBAL_RPS=100
export IP_FILTER_MODE=blocklist
export PROTECTION_ENABLED=true
```

## Example Configs

### Local Development

```toml
external_ip = "127.0.0.1"
http_port = 8080
game_api_key = ""  # No auth for dev
log_level = "debug"

[rate_limit]
enabled = false

[protection]
enabled = false
```

### Production (VPS)

```toml
external_ip = "auto"
domain = "natpunch.example.com"
http_port = 8080
game_api_key = "your-48-char-hex-key"
log_level = "info"

[rate_limit]
enabled = true
per_ip_rpm = 30

[ip_filter]
mode = "off"

[protection]
enabled = true
auto_block_threshold = 5
```

### High Security

```toml
external_ip = "1.2.3.4"
domain = "natpunch.example.com"
game_api_key = "required-key-here"
log_level = "warn"

[rate_limit]
enabled = true
global_rps = 50
per_ip_rpm = 20
per_ip_burst = 5

[ip_filter]
mode = "blocklist"
blocklist_file = "/opt/natpunch/blocklist.txt"

[protection]
enabled = true
auto_block_threshold = 3
auto_block_window = "30s"
```
