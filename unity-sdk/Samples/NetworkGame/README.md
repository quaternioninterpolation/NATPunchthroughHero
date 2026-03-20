# NAT Punchthrough Hero — Unity Sample Game

A simple multiplayer game demonstrating the NAT Punchthrough Hero Unity SDK
with Mirror networking. Players connect via join codes, move around as capsules
on a large plane, and chat with each other.

## Features

- **Host / Join** with 6-character join codes
- **Password-protected games** (optional)
- **WASD** movement on a large ground plane
- **Chat** (press Enter to type, Enter to send)
- **Options menu** (press Esc to toggle, Disconnect returns to menu)
- NAT traversal via UPnP → STUN → TURN fallback cascade

## Requirements

- Unity 2021.3+
- [Mirror](https://assetstore.unity.com/packages/tools/network/mirror-129321) networking package
- NAT Punchthrough Hero Unity SDK (this package)

## Setup

### 1. Create the Player Prefab

1. Create an empty GameObject, name it **Player**
2. Add these components:
   - **NetworkIdentity**
   - **NetworkTransform** — set **Sync Direction** to `Client To Server`
   - **CharacterController**
   - **SamplePlayer** (from `NatPunchthrough.Samples`)
3. Drag it into your Project window to create a prefab
4. Delete the instance from the scene

### 2. Set Up the Scene

1. Create a new empty scene
2. Create an empty GameObject, name it **NetworkManager**
3. Add the **SampleNetworkManager** component (NATTransport is auto-added)
4. Assign your **Player** prefab to the **Player Prefab** field

### 3. Play

1. Enter your NAT Punchthrough Hero server URL (default: `http://localhost:8080`)
2. Optionally set a game password
3. Click **Host Game** to start hosting — note the join code
4. In a second instance, enter the join code (and password if set) and click **Join Game**
4. Move with WASD, chat with Enter, disconnect with Esc → Disconnect

## Controls

| Key       | Action                    |
|-----------|---------------------------|
| W/A/S/D   | Move                      |
| Enter     | Open chat / send message  |
| Escape    | Close chat / toggle options menu |

## Architecture

```
SampleNetworkManager (NetworkManager + NATTransport)
├── Registers games via MasterServerClient REST API
├── Manages host/client lifecycle
├── Creates world environment at runtime (ground + light)
├── IMGUI overlay for menu, chat, and options
│
SamplePlayer (NetworkBehaviour)
├── Spawns capsule visual + name label at runtime
├── WASD movement via CharacterController
├── Position sync via Mirror NetworkTransform
└── Chat via [Command] / [ClientRpc]
```

## Notes

- The ground plane is created at runtime — no scene objects needed
- Player colors and names are randomly assigned and synced via SyncVars
- The host is both a server and a client (Mirror host mode)
- Heartbeats are sent every 30 seconds to keep the game registered
