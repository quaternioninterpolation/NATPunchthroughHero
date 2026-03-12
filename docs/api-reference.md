# API Reference

Complete REST and WebSocket API for NAT Punchthrough Hero.

## Base URL

```
http://localhost:8080    # Development
https://your-domain.com  # Production
```

## Authentication

### API Key (Game Clients)

Include in all requests if `game_api_key` is configured:

```
X-API-Key: your-api-key
```

Returns `401 Unauthorized` if missing/invalid (when API key is required).

### Admin Auth (Dashboard)

HTTP Basic Authentication:
```
Authorization: Basic base64(admin:password)
```

## REST API

---

### `GET /api/health`

Health check. No authentication required.

**Response** `200 OK`:
```json
{
  "status": "ok",
  "version": "1.0.0",
  "games": 12,
  "uptime": "5h32m"
}
```

---

### `GET /api/games`

List all public games.

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `code` | string | Filter by join code (exact match) |
| `version` | string | Filter by game version |
| `limit` | int | Max results (default: 50) |
| `offset` | int | Pagination offset (default: 0) |

**Response** `200 OK`:
```json
[
  {
    "id": "abc123def456",
    "name": "My Awesome Game",
    "join_code": "XK9M2P",
    "max_players": 4,
    "current_players": 2,
    "nat_type": "moderate",
    "data": {"map": "forest", "mode": "coop"},
    "created_at": "2024-01-15T10:30:00Z"
  }
]
```

> **Note:** `host_ip` and `host_token` are never included in public responses.

---

### `POST /api/games`

Register a new game.

**Request Body:**
```json
{
  "name": "My Game",
  "max_players": 4,
  "current_players": 1,
  "nat_type": "unknown",
  "data": {
    "map": "forest",
    "mode": "pvp"
  }
}
```

| Field | Type | Required | Validation |
|-------|------|----------|------------|
| `name` | string | Yes | 1-100 chars, HTML stripped |
| `max_players` | int | Yes | 1-256 |
| `current_players` | int | No | Default: 1 |
| `nat_type` | string | No | Informational |
| `data` | object | No | Arbitrary metadata, max 4KB |

**Response** `201 Created`:
```json
{
  "id": "abc123def456",
  "join_code": "XK9M2P",
  "host_token": "secret-token-for-host-only"
}
```

> **Important:** Save `host_token` — it's needed for heartbeat and deletion.

---

### `POST /api/games/{id}/heartbeat`

Keep a game alive. Must be called before `game_ttl` expires (default: 5 min).

**Headers:**
```
Authorization: Bearer {host_token}
```

> Also accepts `X-Host-Token: {host_token}` header as an alternative.

**Response** `200 OK`:
```json
{"status": "ok"}
```

**Errors:**
- `401` — Invalid or missing host token
- `404` — Game not found (expired or deleted)

---

### `DELETE /api/games/{id}`

Remove a game listing.

**Headers:**
```
Authorization: Bearer {host_token}
```

> Also accepts `X-Host-Token: {host_token}` header as an alternative.

**Response** `200 OK`:
```json
{"status": "deleted"}
```

---

### `GET /api/games/{id}/turn`

Get time-limited TURN relay credentials.

**Response** `200 OK`:
```json
{
  "username": "1705312200:abc123",
  "password": "base64-hmac-password",
  "ttl": 86400,
  "uris": [
    "stun:your-server:3478",
    "turn:your-server:3478?transport=udp"
  ]
}
```

---

## Admin API

All admin endpoints require HTTP Basic Auth (`admin:password`).

---

### `GET /admin/api/stats`

Server statistics.

**Response** `200 OK`:
```json
{
  "games": {
    "active": 12,
    "total_created": 456,
    "max_concurrent": 50
  },
  "connections": {
    "websocket": 24,
    "total_requests": 12000
  },
  "rate_limiting": {
    "active_limiters": 150,
    "total_blocked": 45
  },
  "protection": {
    "auto_blocked_ips": 3,
    "total_violations": 120
  },
  "uptime": "5h32m",
  "version": "1.0.0"
}
```

---

### `GET /admin/api/blocked`

List auto-blocked IPs.

**Response** `200 OK`:
```json
[
  {
    "ip": "1.2.3.4",
    "blocked_at": "2024-01-15T10:30:00Z",
    "expires_at": "2024-01-15T11:30:00Z",
    "violations": 15,
    "offense_count": 2
  }
]
```

---

### `POST /admin/api/blocklist`

Add IP to permanent blocklist.

**Request Body:**
```json
{"ip": "1.2.3.4"}
```

**Response** `200 OK`:
```json
{"status": "blocked"}
```

---

### `DELETE /admin/api/blocklist/{ip}`

Remove IP from blocklist.

**Response** `200 OK`:
```json
{"status": "unblocked"}
```

---

### `POST /admin/api/reload`

Reload config and IP filter files.

**Response** `200 OK`:
```json
{"status": "reloaded"}
```

---

## WebSocket Signaling

Endpoint: `ws://server:8080/ws` or `ws://server:8080/ws/signaling` (both supported; use `wss://` for TLS)

All messages are JSON with a `type` field.

### Connection

```javascript
const ws = new WebSocket("ws://server:8080/ws/signaling");
ws.onmessage = (evt) => {
  const msg = JSON.parse(evt.data);
  console.log(msg.type, msg);
};
```

### Message Types

#### Client → Server

| Type | Fields | Description |
|------|--------|-------------|
| `register_host` | `game_id`, `host_token` | Host registers for signaling |
| `request_join` | `game_id` | Client requests to join |
| `gather_candidates` | `game_id` | Start ICE candidate gathering |
| `ice_candidate` | `game_id`, `candidate` | Send ICE candidate to peer |
| `punch_signal` | `game_id`, `target_peer`, `data` | Send NAT punch data |
| `connection_established` | `game_id` | Notify successful connection |

#### Server → Client

| Type | Fields | Description |
|------|--------|-------------|
| `registered` | `peer_id`, `game_id` | Registration confirmed |
| `peer_joined` | `peer_id`, `game_id` | A peer joined the session |
| `peer_left` | `peer_id` | A peer disconnected |
| `peer_candidate` | `from_peer`, `candidate` | ICE candidate from peer |
| `punch_signal` | `from_peer`, `data` | NAT punch data from peer |
| `turn_fallback` | `credentials` | TURN credentials for relay |
| `error` | `message` | Error description |

### Signaling Flow

```
Host                    Server                  Joiner
 │                        │                        │
 ├─ register_host ──────→ │                        │
 │ ←── registered ────────┤                        │
 │                        │ ←── request_join ──────┤
 │ ←── peer_joined ───────┤──→ registered ─────────┤
 │                        │                        │
 ├─ gather_candidates ──→ │ ←─ gather_candidates ──┤
 │                        │                        │
 ├─ ice_candidate ──────→ │──→ peer_candidate ─────┤
 │ ←── peer_candidate ────┤ ←── ice_candidate ─────┤
 │                        │                        │
 ├─ punch_signal ───────→ │──→ punch_signal ───────┤
 │ ←── punch_signal ──────┤ ←── punch_signal ──────┤
 │                        │                        │
 │    (Direct P2P established, or...)              │
 │                        │                        │
 │ ←── turn_fallback ─────┤──→ turn_fallback ──────┤
 │           (TURN relay credentials)               │
```

### Error Codes

| Error | Description |
|-------|-------------|
| `game_not_found` | Game ID doesn't exist |
| `game_full` | Game has reached max players |
| `invalid_token` | Host token is invalid |
| `rate_limited` | Too many messages |
| `invalid_message` | Malformed message |

## Rate Limit Headers

Rate-limited responses include:
```
HTTP/1.1 429 Too Many Requests
Retry-After: 60
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1705312260
```
