# Godot SDK Integration Guide

NAT Punchthrough Hero includes a native Godot 4.x addon that enables P2P multiplayer without port forwarding. The SDK handles the full NAT traversal cascade — UPnP, STUN hole punch, and TURN relay fallback — through a simple node-based API.

## Requirements

- **Godot 4.2+** (GDScript)
- A running NAT Punchthrough Hero server
- Your server's API key (if authentication is enabled)

## Installation

### Option 1: Copy the addon

1. Copy the `godot-sdk/addons/nat_punchthrough/` folder into your Godot project's `addons/` directory
2. In Godot, go to **Project → Project Settings → Plugins**
3. Enable **NAT Punchthrough Hero**

### Option 2: Symlink (development)

```bash
# From your Godot project root
ln -s /path/to/NATPunchthroughHero/godot-sdk/addons/nat_punchthrough addons/nat_punchthrough
```

## Quick Start

### 1. Add the NATClient node

Add a `NATClient` node to your main scene (or as an autoload singleton).

### 2. Configure in the Inspector

| Property | Description | Default |
|----------|-------------|---------|
| `server_url` | Your server URL | `http://localhost:8080` |
| `api_key` | API key for authentication | (empty) |
| `try_upnp` | Attempt UPnP port mapping | `true` |
| `try_stun_punch` | Attempt STUN hole punch | `true` |
| `use_turn_fallback` | Fall back to TURN relay | `true` |
| `punch_timeout` | Seconds before TURN fallback | `10.0` |
| `game_port` | Game server UDP port | `7777` |
| `auto_heartbeat` | Send heartbeats automatically | `true` |
| `heartbeat_interval` | Heartbeat interval (seconds) | `30.0` |

### 3. Host a game

```gdscript
@onready var nat: NATClient = $NATClient

func _ready():
    nat.game_hosted.connect(_on_game_hosted)
    nat.nat_type_detected.connect(_on_nat_detected)
    nat.connection_method_determined.connect(_on_method)
    nat.peer_joined.connect(_on_peer_joined)
    nat.error_occurred.connect(_on_error)

func host():
    nat.host_game({
        "name": "My Awesome Game",
        "max_players": 4,
        "map": "forest",
        "game_version": "1.0",
        "password": "secret",  # Optional — omit for no password
    })

func _on_game_hosted(game_id: String, join_code: String, host_token: String):
    print("Game hosted! Share this code: ", join_code)
    # Display join_code to the player in your UI

func _on_nat_detected(nat_type: NATTraversal.NATType, name: String):
    print("NAT type: ", name)

func _on_method(method: NATTraversal.ConnectionMethod, name: String):
    print("Connection method: ", name)  # "direct", "stun_punch", or "turn_relay"

func _on_peer_joined(peer_id: String):
    print("Player joined: ", peer_id)

func _on_error(message: String):
    print("Error: ", message)
```

### 4. Join a game

```gdscript
func join(code: String, password: String = ""):
    nat.join_game(code, password)  # Pass join code + optional password

func _ready():
    nat.game_joining.connect(func(id): print("Joining game: ", id))
    nat.connection_established.connect(_on_connected)
    nat.connection_method_determined.connect(_on_method)
    nat.error_occurred.connect(_on_error)

func _on_connected(peer_endpoint: String):
    print("Connected! Endpoint: ", peer_endpoint)
    # Now set up your ENet/WebRTC peer connection to this endpoint
```

### 5. Stop hosting

```gdscript
func stop():
    nat.stop_game()
    # Deregisters from server, releases UPnP, disconnects signaling
```

## Architecture

The SDK has three layers:

```
┌─────────────────────────────────────┐
│           NATClient (Node)          │  ← High-level API, add to scene
│   Orchestrates the full cascade     │
├─────────────────────────────────────┤
│  MasterServerClient (RefCounted)    │  ← REST API client
│   register, list, heartbeat, TURN   │
├─────────────────────────────────────┤
│     NATTraversal (RefCounted)       │  ← Low-level operations
│  UPnP, STUN, WebSocket signaling   │
└─────────────────────────────────────┘
```

### NATClient

The main node you interact with. Handles the full hosting/joining flow automatically. Exported properties are configurable in the Godot Inspector.

### MasterServerClient

REST client for the master server. Can be used standalone for custom flows:

```gdscript
var client = MasterServerClient.new("http://localhost:8080", "your-key")

# Health check
var health = await client.check_health()
print(health)  # {"status": "ok", "version": "1.0.0", ...}

# List games
var games = await client.list_games()
for game in games:
    print(game.name, " - ", game.join_code)

# Register a game
var result = await client.register_game({
    "name": "Test Game",
    "max_players": 8,
})
if result.success:
    print("ID: ", result.id)
    print("Code: ", result.join_code)
    print("Token: ", result.host_token)

# Heartbeat (call every 30s while hosting)
await client.send_heartbeat(game_id, host_token)

# Deregister
await client.deregister_game(game_id, host_token)

# TURN credentials
var creds = await client.get_turn_credentials(game_id)
print(creds.username, creds.uris)
```

### NATTraversal

Low-level NAT operations. Use directly for custom traversal logic:

```gdscript
var traversal = NATTraversal.new()

# UPnP
var upnp_result = await traversal.try_upnp(7777)
if upnp_result.success:
    print("Port mapped! External IP: ", upnp_result.external_ip)

# STUN discovery
var stun_result = await traversal.discover_nat("stun.l.google.com", 19302)
if stun_result.success:
    print("Public endpoint: ", stun_result.public_ip, ":", stun_result.public_port)
    print("NAT type: ", NATTraversal.NATType.keys()[traversal.nat_type])

# WebSocket signaling
traversal.registered.connect(func(id): print("Registered: ", id))
traversal.peer_joined.connect(func(id): print("Peer: ", id))
traversal.punch_signal_received.connect(func(from, data): print("Punch: ", data))
traversal.turn_fallback.connect(func(creds): print("TURN fallback"))

traversal.connect_signaling("ws://localhost:8080/ws/signaling", "your-key")
# Call traversal.poll_signaling() every frame
```

## Signals Reference

### NATClient Signals

| Signal | Parameters | Description |
|--------|-----------|-------------|
| `game_hosted` | `game_id`, `join_code`, `host_token` | Game registered on server |
| `game_joining` | `game_id` | Started joining a game |
| `nat_type_detected` | `nat_type`, `nat_type_name` | STUN discovery complete |
| `connection_method_determined` | `method`, `method_name` | Connection method selected |
| `connection_established` | `peer_endpoint` | P2P or relay connection ready |
| `peer_joined` | `peer_id` | A peer joined (host only) |
| `peer_left` | `peer_id` | A peer left (host only) |
| `turn_credentials_received` | `credentials` | TURN creds available |
| `error_occurred` | `message` | Error at any stage |
| `game_stopped` | | Game session ended |

### NATTraversal Signals

| Signal | Parameters | Description |
|--------|-----------|-------------|
| `signaling_connected` | | WebSocket connected |
| `signaling_disconnected` | | WebSocket disconnected |
| `registered` | `peer_id` | Registered with signaling |
| `peer_joined` | `peer_id` | Peer joined session |
| `peer_left` | `peer_id` | Peer left session |
| `peer_candidate_received` | `from_peer`, `candidate` | ICE candidate from peer |
| `punch_signal_received` | `from_peer`, `data` | Hole punch signal |
| `turn_fallback` | `credentials` | Server says use TURN |
| `signaling_error` | `message` | Signaling error |

## Connecting to Godot's Multiplayer

After `connection_established` fires, you need to wire up the actual network transport. Here's how to use ENet:

### Direct / STUN Punch Connection

```gdscript
func _on_connection_established(endpoint: String):
    if endpoint == "relay":
        _setup_turn_relay()
        return

    # Parse endpoint
    var parts = endpoint.split(":")
    var ip = parts[0]
    var port = int(parts[1])

    if nat.is_hosting:
        # Host: create ENet server
        var peer = ENetMultiplayerPeer.new()
        peer.create_server(nat.game_port)
        multiplayer.multiplayer_peer = peer
    else:
        # Client: connect to host
        var peer = ENetMultiplayerPeer.new()
        peer.create_client(ip, port)
        multiplayer.multiplayer_peer = peer
```

### TURN Relay with WebRTC

For TURN relay connections, use Godot's WebRTC:

```gdscript
func _setup_turn_relay():
    var ice_servers = nat.traversal.get_turn_ice_servers(nat.turn_credentials)

    var rtc = WebRTCPeerConnection.new()
    rtc.initialize({"iceServers": ice_servers})

    # Set up data channels for game data
    var channel = rtc.create_data_channel("game", {
        "negotiated": true,
        "id": 1,
    })

    # Use the WebRTC connection for your game networking
```

## NAT Traversal Flow

```
Host                     Server                    Joiner
 │                         │                         │
 ├─ UPnP (optional) ────→ │                         │
 ├─ STUN discovery ──────→ │                         │
 ├─ POST /api/games ─────→ │                         │
 │ ←─ game_id, join_code ──┤                         │
 ├─ WS register_host ────→ │                         │
 │                         │ ←── GET /api/games ──────┤
 │                         │ ←── GET /turn ───────────┤
 │                         │ ←── STUN discovery ──────┤
 │                         │ ←── WS request_join ─────┤
 │ ←── peer_joined ────────┤──→ registered ───────────┤
 │                         │                         │
 ├─ ice_candidate ───────→ │──→ peer_candidate ──────┤
 │ ←── peer_candidate ─────┤ ←── ice_candidate ──────┤
 │                         │                         │
 ├─ punch_signal ────────→ │──→ punch_signal ────────┤
 │ ←── punch_signal ───────┤ ←── punch_signal ───────┤
 │                         │                         │
 │    ( Direct P2P established, or... )              │
 │                         │                         │
 │ ←── turn_fallback ──────┤──→ turn_fallback ───────┤
 │         ( TURN relay credentials )                 │
```

## Troubleshooting

### "HTTP request failed" errors

- Check that `server_url` is correct and the server is running
- If using HTTPS, ensure the certificate is valid
- Check firewall rules on the server

### UPnP always fails

- Many enterprise/carrier-grade NATs don't support UPnP
- This is expected — the cascade continues to STUN/TURN
- You can disable UPnP with `try_upnp = false` to skip it

### STUN discovery times out

- Default STUN server is `stun.l.google.com:19302`
- Some networks block STUN traffic
- TURN relay will still work as fallback

### Punch timeout → TURN relay

- This means the NAT type is too restrictive for direct punching
- TURN relay adds ~50-150ms latency but works through any NAT
- Consider showing connection quality indicators in your game UI

### No scene tree available

- `MasterServerClient` needs to temporarily add nodes to the scene tree for HTTP requests
- Make sure you call its methods after the scene tree is ready (in `_ready()` or later)

## Example Project

See `godot-sdk/example/` for a minimal working example with:
- Game browser UI
- Host/join flow
- Connection status display
