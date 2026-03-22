# NAT Punchthrough Hero

**Let your players host game servers without port forwarding.**

<img src="https://github.com/user-attachments/assets/b49cbfcf-f7d8-43a7-8315-cd8f8291c60b" width="512" />

A self-hosted NAT traversal platform for **Unity/Mirror**, **Godot 4**, and **Unreal Engine** games. Players behind closed routers can host and join games — no Steam API, no manual port forwarding, no relay service fees (well, maybe some hosting fees).

> **Note:** This project is in early alpha. Expect bugs, breaking changes, and incomplete features. Contributions welcome!

## How It Works

```
Player A (Host)          Your Server            Player B (Join)
  behind NAT          (VPS / Docker)             behind NAT
      │                     │                        │
      ├── Register Game ──→ │                        │
      │                     │ ←── Browse/Join ───────┤
      │                     │                        │
      ├── WebSocket ──────→ │ ←── WebSocket ─────────┤
      │              NAT Punch Coordination          │
      │                     │                        │
      ├─────── Direct P2P Connection ────────────────┤
      │          (or TURN relay fallback)             │
```

**NAT Traversal Cascade:**
1. **UPnP** — Automatically open router port (works ~40% of the time)
2. **STUN Hole Punch** — Coordinate a UDP hole punch via signaling (~80% success)
3. **TURN Relay** — Fall back to relayed connection (100% success, adds latency)

AI gave this a **~95%+** Combined success rate! Woo!
No idea how accurate this is but it sounds good in the README.

## Quick Start

### Starting with Docker (Recommended)

```bash
git clone https://github.com/you/natpunch.git
cd natpunch
docker compose up
```

Server is running at `http://localhost:8080`. Dashboard at `http://localhost:8080/admin/`.

### Starting with Binary

```bash
cd server
go build -o natpunch-server .
./server setup    # Interactive setup wizard
./server serve    # Start the server
```

### VPS One-Liner


```bash
curl -sSL https://raw.githubusercontent.com/quaternioninterpolation/NATPunchthroughHero/refs/heads/main/deploy/deploy-vps.sh | sudo bash
```

### Generate an API Key (optional)

Don't have an API key yet? Generate one here.

```bash
bash scripts/generate-api-key.sh
```

Writes to `output/api_key.txt`. Pass it to clients via `X-API-Key` header or `?key=` query param. (see [API Reference](docs/api-reference.md) for details)

If using the Unity SDK, set `transport.apiKey` to this value. (see [Unity Integration](#unity-integration) below)

## Architecture

```
┌─────────────────────────────────────────────────┐
│               Docker Compose                     │
│                                                  │
│  ┌─────────────────────┐  ┌──────────────────┐  │
│  │   Go Server (~10MB) │  │  coturn (STUN/   │  │
│  │                     │  │   TURN)          │  │
│  │  • REST API         │  │                  │  │
│  │  • WebSocket Hub    │  │  • UDP relay     │  │
│  │  • Admin Dashboard  │  │  • Hole punching │  │
│  │  • Auto-TLS         │  │  • HMAC auth     │  │
│  └─────────────────────┘  └──────────────────┘  │
└─────────────────────────────────────────────────┘
```

- **2 containers only** — Go server + coturn
- **~10MB server image** — Multi-stage Docker build from scratch
- **Zero external dependencies** — No Redis, no nginx, no databases
- **In-memory store** — Game sessions are ephemeral (~5MB for 500 games)
- **Auto-TLS** — Built-in Let's Encrypt via Go's autocert

## Unity Integration

```csharp
// In your NetworkManager
var transport = gameObject.AddComponent<NATTransport>();
transport.masterServerUrl = "https://your-server.com";
transport.apiKey = "your-api-key";

// Host a game
transport.StartHost();

// Join a game
transport.JoinCode = "ABC123";
transport.StartClient();
```

See [docs/unity-sdk.md](docs/unity-sdk.md) for full integration guide.

## Godot Integration

```gdscript
# Add a NATClient node to your scene, then:
@onready var nat: NATClient = $NATClient

func _ready():
    nat.server_url = "https://your-server.com"
    nat.api_key = "your-api-key"
    nat.game_hosted.connect(func(id, code, token):
        print("Share this code: ", code)
    )
    nat.connection_established.connect(func(endpoint):
        print("Connected: ", endpoint)
    )

func host():
    nat.host_game({"name": "My Game", "max_players": 4})

func join(code: String):
    nat.join_game(code)
```

See [docs/godot-sdk.md](docs/godot-sdk.md) for full integration guide.

## Unreal Engine Integration

```cpp
// Add UNATClient component to any Actor
UNATClient* NATClient = CreateDefaultSubobject<UNATClient>(TEXT("NATClient"));
NATClient->ServerUrl = TEXT("https://your-server.com");
NATClient->ApiKey = TEXT("your-api-key");

// Host a game
FGameRegistration Info;
Info.Name = TEXT("My Game");
Info.MaxPlayers = 4;
NATClient->HostGame(Info);

// Join a game
NATClient->JoinGame(TEXT("ABC123"));

// Bind events
NATClient->OnGameHosted.AddDynamic(this, &AMyActor::OnHosted);
NATClient->OnConnectionEstablished.AddDynamic(this, &AMyActor::OnConnected);
```

See [docs/unreal-sdk.md](docs/unreal-sdk.md) for full integration guide.

## Documentation

| Document | Description |
|----------|-------------|
| [Quick Start](docs/quickstart.md) | Get running in 5 minutes |
| [Deployment](docs/deployment.md) | VPS, cloud, and production setup |
| [Configuration](docs/configuration.md) | All config options explained |
| [Security](docs/security.md) | Hardening, TLS, auth |
| [API Reference](docs/api-reference.md) | REST & WebSocket API |
| [Architecture](docs/architecture.md) | System design deep dive |
| [Unity SDK](docs/unity-sdk.md) | Unity/Mirror integration |
| [Godot SDK](docs/godot-sdk.md) | Godot 4.x integration |
| [Unreal SDK](docs/unreal-sdk.md) | Unreal Engine integration |
| [Testing](docs/testing.md) | Running and writing tests |
| [Troubleshooting](docs/troubleshooting.md) | Common issues & fixes |
| [Contributing](docs/contributing.md) | Development guide |
| [Changelog](docs/changelog.md) | Version history |

## API Overview

```bash
# Health check
curl http://localhost:8080/api/health

# List games
curl http://localhost:8080/api/games

# Register a game
curl -X POST http://localhost:8080/api/games \
  -H "Content-Type: application/json" \
  -H "X-API-Key: your-key" \
  -d '{"name":"My Game","max_players":4}'

# Get TURN credentials
curl http://localhost:8080/api/games/{id}/turn
```

## Security

- API key authentication for game clients
- **Password-protected games** — optional per-game passwords (SHA-256 hashed, validated at signaling layer)
- HTTP Basic Auth for admin dashboard
- HMAC-SHA1 TURN credentials (time-limited, per-session)
- Multi-layer rate limiting (global, per-IP, per-endpoint)
- IP blocklist/allowlist with CIDR support
- Automatic abuse detection with escalating blocks
- coturn locked to TURN relay only (no SSRF via private IPs)

## Requirements

- Docker & Docker Compose (for container deployment)
- OR Go 1.23+ (for binary deployment)
- A VPS with a public IP (for production)
- UDP ports 3478, 49152-50175 open (for TURN)

## License

MIT
