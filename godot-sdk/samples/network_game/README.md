# NAT Punchthrough Hero — Godot Sample Game

A simple multiplayer game demonstrating the NAT Punchthrough Hero Godot SDK.
Players connect via join codes (or direct IP), move around as capsules on a
large plane, and chat with each other.

## Features

- **Host / Join** with 6-character join codes or direct IP:port
- **Password-protected games** (optional)
- **WASD** movement on a 200×200m ground plane
- **Chat** (press Enter to type, Enter to send)
- **Options menu** (press Esc to toggle, Disconnect returns to menu)
- NAT traversal via NATClient (UPnP → STUN → TURN cascade)

## Requirements

- Godot 4.2+
- NAT Punchthrough Hero Godot addon installed in `addons/nat_punchthrough/`

## Setup

### 1. Install the Addon

Copy the `addons/nat_punchthrough/` folder into your Godot project's
`addons/` directory. Enable the plugin in **Project → Project Settings →
Plugins**.

### 2. Copy the Sample

Copy the `samples/network_game/` folder into your project.

### 3. Set the Main Scene

Set `res://samples/network_game/network_game.tscn` as your main scene
(**Project → Project Settings → Application → Run → Main Scene**).

### 4. Run

1. Enter your NAT Punchthrough Hero server URL (default: `http://localhost:8080`)
2. Optionally set a game password
3. Click **Host** — note the join code displayed
4. Run a second instance, enter the join code (and password if set), and click **Join**
4. Move with WASD, chat with Enter, disconnect with Esc → Disconnect

For local testing without a master server, use direct connect:
enter `127.0.0.1:7777` (or a LAN IP) in the join code field instead.

## Controls

| Key       | Action                              |
|-----------|-------------------------------------|
| W/A/S/D   | Move                                |
| Enter     | Open chat / send message            |
| Escape    | Close chat / toggle options menu    |

## Architecture

```
network_game.tscn / .gd
├── NATClient node (addon) — game registration & NAT traversal
├── 3D world (ground plane, lighting, environment)
├── Players node — spawn container
├── MultiplayerSpawner — replicates player scenes to all peers
├── UI (created in code):
│   ├── Menu panel  (host / join / server config)
│   ├── Connecting panel  (status + cancel)
│   ├── Game HUD  (join code, player count, chat, controls hint)
│   └── Options overlay  (disconnect / resume)
│
player.tscn / .gd
├── CharacterBody3D with capsule mesh + collision
├── Camera3D (active only for local player)
├── Label3D (billboard name)
├── MultiplayerSynchronizer (position + rotation)
└── WASD movement (authority-gated)
```

## Notes

- The host is both server and client (Godot host mode)
- Player colors are deterministic based on peer ID (golden-ratio hue)
- Chat uses `@rpc("any_peer", "call_local", "reliable")`
- For TURN relay connections, additional WebRTC setup would be needed
  (this sample handles direct and STUN-punched connections)
