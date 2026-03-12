# Architecture

Deep dive into the system design of NAT Punchthrough Hero.

## Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                         Internet                                  │
│                                                                   │
│  ┌─────────────┐           ┌─────────────┐                       │
│  │  Game Host   │           │  Game Client │                      │
│  │  (Unity)     │           │  (Unity)     │                      │
│  │  behind NAT  │           │  behind NAT  │                      │
│  └──────┬───────┘           └──────┬───────┘                      │
│         │                          │                              │
│         │    REST + WebSocket      │                              │
│         ▼                          ▼                              │
│  ┌─────────────────────────────────────────┐                      │
│  │         Go Master Server                 │                     │
│  │                                          │                     │
│  │  ┌──────────┐  ┌───────────┐  ┌───────┐ │                     │
│  │  │ REST API │  │ Signaling │  │ Admin │ │                     │
│  │  │          │  │   Hub     │  │ Dash  │ │                     │
│  │  └──────────┘  └───────────┘  └───────┘ │                     │
│  │  ┌──────────┐  ┌───────────┐  ┌───────┐ │                     │
│  │  │  Store   │  │   Rate    │  │  IP   │ │                     │
│  │  │ (memory) │  │  Limiter  │  │Filter │ │                     │
│  │  └──────────┘  └───────────┘  └───────┘ │                     │
│  └─────────────────────────────────────────┘                      │
│                        │                                          │
│                        │ HMAC credentials                         │
│                        ▼                                          │
│  ┌─────────────────────────────────────────┐                      │
│  │         coturn (STUN/TURN)               │                     │
│  │                                          │                     │
│  │  • STUN binding (NAT discovery)          │                     │
│  │  • TURN relay (fallback)                 │                     │
│  │  • HMAC-SHA1 auth (shared secret)        │                     │
│  └─────────────────────────────────────────┘                      │
│                                                                   │
│         ◄────── Direct P2P (UDP) ──────►                          │
│             (after successful punch)                              │
└──────────────────────────────────────────────────────────────────┘
```

## NAT Traversal Cascade

The system attempts three connection methods in order:

### Stage 1: UPnP (Client-Side)

```
Unity Client → Router (UPnP) → Port mapped
```

- Uses Open.NAT library in Unity
- Attempts to open a port on the local router via UPnP/NAT-PMP
- Works ~40% of the time (some routers disable UPnP)
- Zero server involvement
- If successful, the host's public IP:port is directly reachable

### Stage 2: STUN Hole Punch

```
Host ──→ STUN ──→ discovers public IP:port
Client ──→ STUN ──→ discovers public IP:port

Host ←── Signaling ──→ Client
         (exchange public endpoints)

Host ←─── UDP hole punch ───→ Client
```

- Both peers send STUN binding requests to coturn
- STUN response reveals their public IP:port (server reflexive candidate)
- Signaling hub exchanges these candidates between peers
- Both peers simultaneously send UDP packets to each other's public endpoint
- NAT tables update, allowing bidirectional communication
- Works ~80% of the time (fails with symmetric NAT)

### Stage 3: TURN Relay

```
Host ←──→ TURN Server ←──→ Client
           (relayed)
```

- Last resort when direct connection fails
- Traffic is relayed through the coturn TURN server
- Adds 10-50ms latency depending on server location
- 100% success rate
- HMAC-SHA1 credentials with time-limited access

## Go Server Components

### Server Struct (`api.go`)

The central `Server` struct wires all components together:

```go
type Server struct {
    config     *Config
    store      *GameStore
    turn       *TURNGenerator
    limiter    *RateLimiter
    filter     *IPFilter
    protection *Protection
    hub        *SignalingHub
    mux        *http.ServeMux
}
```

### Request Flow

```
Incoming Request
      │
      ▼
┌─────────────┐
│  Security   │  ← X-Content-Type-Options, X-Frame-Options, etc.
│  Headers    │
└──────┬──────┘
       ▼
┌─────────────┐
│ Protection  │  ← Check if IP is auto-blocked
│  Check      │
└──────┬──────┘
       ▼
┌─────────────┐
│  IP Filter  │  ← Blocklist/allowlist check
│             │
└──────┬──────┘
       ▼
┌─────────────┐
│ Rate Limiter│  ← Token bucket per IP + global
│             │
└──────┬──────┘
       ▼
┌─────────────┐
│ Request Size│  ← 64KB max body
│  Limit      │
└──────┬──────┘
       ▼
┌─────────────┐
│  Handler    │  ← Actual endpoint logic
│             │
└─────────────┘
```

### In-Memory Store (`store.go`)

```
sync.Map
  ├── game-id-1 → Game{Name, Host, Players, TTL, ...}
  ├── game-id-2 → Game{...}
  └── game-id-3 → Game{...}

sync.Map (join codes)
  ├── "XK9M2P" → "game-id-1"
  ├── "B4H7TQ" → "game-id-2"
  └── "R8N3WJ" → "game-id-3"
```

- Thread-safe via `sync.Map` (no mutex contention)
- TTL eviction runs every 10 seconds
- Join codes use non-confusable characters (no 0/O, 1/I/L)
- Game IDs are 12-char random hex
- Host tokens are 32-char random hex (never exposed publicly)

### Signaling Hub (`signaling.go`)

```
WebSocket connections organized by game:

games map[string]*PunchSession
  ├── "game-id-1" → PunchSession{
  │     Host: &Peer{conn, id},
  │     Clients: []*Peer{...},
  │   }
  └── "game-id-2" → PunchSession{...}
```

- One WebSocket per peer
- Messages routed by `game_id`
- 60-second idle timeout with 30-second ping keep-alive
- Peer disconnect triggers cleanup and notification

### Rate Limiter (`ratelimit.go`)

```
Seven independent token bucket layers:

Global:  ┌──────────────┐
         │ 100 req/sec  │
         └──────────────┘

Per-IP:  ┌──────────────┐  ┌──────────────┐
         │ 60 req/min   │  │ burst: 10    │
         └──────────────┘  └──────────────┘

Per-Endpoint (per IP):
  WebSocket:  ┌───────┐  Game Reg:  ┌───────┐  TURN:  ┌───────┐
              │ 5 conn│             │ 5/min │         │ 10/min│
              └───────┘             └───────┘         └───────┘
```

Stale entries (no activity in 5 min) are cleaned up every 60 seconds.

### Protection (`protection.go`)

```
Violation tracking (per IP, sliding window):

offenders map[string]*OffenseRecord
  ├── "1.2.3.4" → {
  │     violations: [timestamp, timestamp, ...],
  │     offense_count: 2,
  │     blocked_until: time.Time,
  │   }
  └── "5.6.7.8" → {...}
```

Escalation: violation threshold → block → doubled on repeat → capped at 24h.

## coturn Configuration

coturn runs as a separate container with `network_mode: host` to avoid Docker NAT issues with UDP:

```
┌─────────────────────────────────────┐
│  coturn                              │
│                                      │
│  Listening: 0.0.0.0:3478 (TCP+UDP)  │
│  Relay ports: 49152-50175 (UDP)      │
│                                      │
│  Auth: HMAC-SHA1 (use-auth-secret)   │
│  Secret: shared with Go server       │
│                                      │
│  Denied: all private IPs (SSRF)      │
│  Limits: 256Kbps/session, 5/user     │
└─────────────────────────────────────┘
```

The Go server generates TURN credentials:
```
username = "expiry_timestamp:random_id"
password = HMAC-SHA1(username, shared_secret) → base64
```

coturn validates these independently using the same shared secret.

## Data Flow Examples

### Host Registers a Game

```
1. Unity Host → POST /api/games
   Body: {name, max_players, nat_type, data}

2. Server validates, generates:
   - game_id (12 hex chars)
   - join_code (6 alphanumeric)
   - host_token (32 hex chars)

3. Stores in memory with TTL

4. Returns: {id, join_code, host_token}

5. Host begins heartbeat loop (every 30s):
   POST /api/games/{id}/heartbeat
   Authorization: Bearer {host_token}
```

### Client Joins via Code

```
1. Unity Client → GET /api/games?code=XK9M2P
   Returns game list (filtered)

2. Client → GET /api/games/{id}/turn
   Returns STUN/TURN credentials

3. Client → WebSocket /ws/signaling
   Sends: {type: "request_join", game_id: "..."}

4. Server notifies host: {type: "peer_joined"}

5. Both exchange ICE candidates via signaling

6. Both attempt UDP hole punch

7. If punch fails → server sends TURN credentials
   Both connect via coturn relay
```

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Go over Node/Rust | Single binary, great concurrency, fast builds, tiny image |
| In-memory over Redis | Game sessions are ephemeral; sync.Map is zero-config |
| sync.Map over mutex | Better for read-heavy workloads (game listing) |
| coturn over custom | Battle-tested, RFC-compliant, maintained |
| network_mode: host | Avoids Docker NAT for UDP relay (critical for TURN) |
| TOML over YAML | Comment-friendly, human-readable, unambiguous |
| Embedded dashboard | Single binary deployment, no static file management |
| scratch over alpine | Smallest possible image, no shell = no exploit surface |
| Auto-TLS over nginx | One fewer container, simpler deployment |
| HMAC-SHA1 TURN auth | Standard RFC 5766, no user database needed |
