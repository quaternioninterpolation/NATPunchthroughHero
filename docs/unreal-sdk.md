# Unreal Engine SDK

NAT Punchthrough Hero plugin for Unreal Engine 5.1+.

## Installation

1. Copy `unreal-sdk/` into your project's `Plugins/` directory and rename to `NATpunchthrough`:
   ```
   YourProject/Plugins/NATpunchthrough/
   ├── NATpunchthrough.uplugin
   └── Source/
   ```
2. Regenerate project files (right-click `.uproject` > Generate Visual Studio project files).
3. Add to your game's `Build.cs`:
   ```csharp
   PublicDependencyModuleNames.Add("NATpunchthrough");
   ```

## Quick Start (C++)

```cpp
#include "NATpunchthrough.h"  // Convenience header — includes all plugin types

// In your actor's constructor:
NATClient = CreateDefaultSubobject<UNATClient>(TEXT("NATClient"));

// In BeginPlay — configure and bind events:
NATClient->ServerUrl = TEXT("https://your-server.com");
NATClient->ApiKey = TEXT("your-api-key");

NATClient->OnGameHosted.AddDynamic(this, &AMyActor::OnGameHosted);
NATClient->OnConnectionEstablished.AddDynamic(this, &AMyActor::OnConnected);
NATClient->OnError.AddDynamic(this, &AMyActor::OnError);
```

### Host

```cpp
FGameRegistration Info;
Info.Name = TEXT("My Game");
Info.MaxPlayers = 4;
NATClient->HostGame(Info);
// OnGameHosted fires with GameId, JoinCode, HostToken
```

### Join

```cpp
NATClient->JoinGame(TEXT("XK9M2P"));                       // By 6-char code
NATClient->JoinGame(TEXT("XK9M2P"), TEXT("secret123"));     // With password
NATClient->JoinGame(TEXT("abc123def456"));                  // By game ID
```

## Quick Start (Blueprint)

1. Add a **NATClient** component to any Actor.
2. Set `ServerUrl` and `ApiKey` in the Details panel.
3. Bind events in the Event Graph (`OnGameHosted`, `OnConnectionEstablished`, `OnError`).
4. Call `HostGame` or `JoinGame` from your logic.

## UNATClient Reference

The main entry point. Add to any Actor as a component.

### Config

| Property | Default | Description |
|----------|---------|-------------|
| `ServerUrl` | `http://localhost:8080` | Master server URL |
| `ApiKey` | | API key (optional) |
| `bTryUPnP` | `true` | Attempt UPnP port mapping |
| `bTryStunPunch` | `true` | Attempt STUN hole punching |
| `bUseTurnFallback` | `true` | Fall back to TURN relay |
| `PunchTimeout` | `10.0` | Punch timeout in seconds (3-30) |
| `GamePort` | `7777` | Local game port |
| `bAutoHeartbeat` | `true` | Auto-send heartbeats while hosting |
| `HeartbeatInterval` | `30.0` | Seconds between heartbeats (10-60) |

### State (read-only)

| Property | Description |
|----------|-------------|
| `GameId` | Current session ID |
| `JoinCode` | 6-character join code |
| `HostToken` | Host auth token (keep secret) |
| `bIsHosting` / `bIsClient` / `bIsConnected` | Session state flags |
| `DetectedNATType` | NAT type from STUN |
| `ActiveConnectionMethod` | How the connection was established |
| `MasterClient` | REST client (for advanced use) |
| `Traversal` | Low-level traversal (for advanced use) |

### Methods

| Method | Description |
|--------|-------------|
| `HostGame(FGameRegistration)` | Host a new session |
| `JoinGame(Target, Password)` | Join by code or game ID |
| `StopGame()` | End current session |
| `RefreshGameList(VersionFilter)` | Fetch public game list |
| `UpdatePlayerCount(Count)` | Update player count (host only) |

### Events

| Event | Params | Fires when |
|-------|--------|------------|
| `OnGameHosted` | GameId, JoinCode, HostToken | Game registered on server |
| `OnGameJoining` | GameId | Join lookup resolved |
| `OnNATTypeDetected` | NATType, NATTypeName | STUN discovery completed |
| `OnConnectionMethodDetermined` | Method, MethodName | UPnP/STUN/TURN selected |
| `OnConnectionEstablished` | PeerEndpoint | Peer connection ready |
| `OnPeerJoined` / `OnPeerLeft` | PeerId | Peer connected/disconnected |
| `OnError` | Error | Something went wrong |
| `OnGameStopped` | | Session ended |

## Data Types

### ENATType

| Value | Punchable? | Notes |
|-------|-----------|-------|
| `Open` | Always | No NAT |
| `FullCone` | Easy | Any external host can reach mapped port |
| `Moderate` | Usually | Restricted cone |
| `PortRestricted` | Sometimes | Must match port |
| `Symmetric` | Rarely | Different mapping per destination — needs TURN |

### EConnectionMethod

`None` | `Direct` (UPnP) | `StunPunch` (hole punch) | `TurnRelay` (TURN server)

### FGameRegistration

| Field | Default | Notes |
|-------|---------|-------|
| `Name` | | Required |
| `MaxPlayers` | `4` | |
| `Password` | | Optional, hashed server-side |
| `Map` | | Optional |
| `GameVersion` | | Optional, used for filtering |
| `HostPort` | `7777` | Overridden by NATClient's `GamePort` |
| `bPrivate` | `false` | Hidden from public list |
| `Data` | | Arbitrary key-value metadata (max 4KB) |

## Connection Flow

### Host
```
HostGame() → UPnP attempt → STUN discovery → Register on server
          → Connect signaling → OnGameHosted(JoinCode)
          → Wait for peers → Hole punch or TURN fallback
          → OnConnectionEstablished
```

### Join
```
JoinGame("XK9M2P") → Resolve code → Fetch TURN creds → STUN discovery
                   → Connect signaling → Exchange ICE candidates
                   → Hole punch → OnConnectionEstablished
                              └→ (timeout) TURN fallback → OnConnectionEstablished
```

## Using the Connection

The plugin establishes the peer connection — it doesn't replace Unreal's networking. Once `OnConnectionEstablished` fires, use the endpoint with your networking solution:

```cpp
void AMyGameMode::OnNATConnected(const FString& PeerEndpoint)
{
    FString IP;
    FString PortStr;
    if (PeerEndpoint.Split(TEXT(":"), &IP, &PortStr))
    {
        GetWorld()->ServerTravel(FString::Printf(TEXT("%s:%s"), *IP, *PortStr));
    }
}
```

## Debug Logging

All plugin logs use the `LogNATPunchthrough` category. Filter in the Output Log:

```
LogNATPunchthrough: NATClient: Hosting game 'My Game'...
LogNATPunchthrough: NATClient: STUN discovery - Public IP: 1.2.3.4:12345, NAT: moderate
LogNATPunchthrough: NATClient: Game registered - ID: abc123, Code: XK9M2P
LogNATPunchthrough: NATClient: Hole punch succeeded! Remote: 5.6.7.8:7777
```

## Troubleshooting

| Problem | Fix |
|---------|-----|
| WebSocket connection error | Check `ServerUrl`, firewall rules on port 8080, verify server is running |
| Failed to resolve STUN server | Check internet. Try `stun1.l.google.com:19302` |
| Hole punch timed out | Both peers likely Symmetric NAT. Ensure `bUseTurnFallback = true` |
| Game not found | Host may have stopped. Sessions expire 90s without heartbeat |
| Plugin not in editor | Ensure folder is `NATpunchthrough` inside `Plugins/`. Regenerate project files |
