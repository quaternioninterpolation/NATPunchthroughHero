# Unreal Engine SDK Integration Guide

Integrate NAT Punchthrough Hero with your Unreal Engine game.

## Overview

The Unreal SDK provides three components:

1. **`UNATClient`** — Actor Component that orchestrates NAT traversal automatically
2. **`UMasterServerClient`** — REST client for the game list API
3. **`UNATTraversal`** — Low-level STUN, UPnP, hole punching, and WebSocket signaling

All classes are fully exposed to Blueprints via `UPROPERTY`/`UFUNCTION` macros.

## Requirements

- Unreal Engine 5.1+
- Modules: HTTP, Json, JsonUtilities, Networking, Sockets, WebSockets (included automatically by the plugin)

## Installation

### As a Project Plugin

1. Copy the `unreal-sdk/` folder into your project's `Plugins/` directory and rename it to `NATpunchthrough`:
   ```
   YourProject/
   └── Plugins/
       └── NATpunchthrough/
           ├── NATpunchthrough.uplugin
           └── Source/
   ```
2. Regenerate project files (right-click `.uproject` → Generate Visual Studio project files).
3. Add the module to your game's `Build.cs`:
   ```csharp
   PublicDependencyModuleNames.Add("NATpunchthrough");
   ```

### As an Engine Plugin

Copy to `Engine/Plugins/Runtime/NATpunchthrough/` instead.

## Quick Setup

### 1. Add NATClient to an Actor (C++)

```cpp
#include "NATClient.h"

// In your actor's constructor:
NATClient = CreateDefaultSubobject<UNATClient>(TEXT("NATClient"));

// In BeginPlay:
NATClient->ServerUrl = TEXT("https://your-server.com");
NATClient->ApiKey = TEXT("your-api-key");

// Bind events
NATClient->OnGameHosted.AddDynamic(this, &AMyActor::OnGameHosted);
NATClient->OnConnectionEstablished.AddDynamic(this, &AMyActor::OnConnected);
NATClient->OnError.AddDynamic(this, &AMyActor::OnError);

NATClient->RegisterComponent();
```

### 2. Host a Game

```cpp
FGameRegistration Info;
Info.Name = TEXT("My Game");
Info.MaxPlayers = 4;
Info.Map = TEXT("Arena");
Info.Password = TEXT("secret123"); // Optional
Info.GameVersion = TEXT("1.0");

NATClient->HostGame(Info);
```

When hosting succeeds, `OnGameHosted` fires with the game ID, join code, and host token.

### 3. Join a Game

```cpp
// By join code (6 characters)
NATClient->JoinGame(TEXT("XK9M2P"));

// By join code with password
NATClient->JoinGame(TEXT("XK9M2P"), TEXT("secret123"));

// By game ID directly
NATClient->JoinGame(TEXT("abc123def456"));
```

### 4. Blueprint Setup

1. Add a **NATClient** component to any Actor in the editor.
2. Configure `ServerUrl`, `ApiKey`, and other settings in the Details panel.
3. Bind events in the Event Graph (e.g., `OnGameHosted`, `OnConnectionEstablished`).
4. Call `HostGame` or `JoinGame` from your Blueprint logic.

## Component Reference

### UNATClient (Actor Component)

The main entry point. Add to any Actor to enable NAT traversal.

#### Configuration Properties

| Property | Type | Default | Description |
|----------|------|---------|-------------|
| `ServerUrl` | FString | `http://localhost:8080` | Master server URL |
| `ApiKey` | FString | *(empty)* | Optional API key |
| `bTryUPnP` | bool | `true` | Attempt UPnP port mapping |
| `bTryStunPunch` | bool | `true` | Attempt STUN hole punching |
| `bUseTurnFallback` | bool | `true` | Fall back to TURN relay |
| `PunchTimeout` | float | `10.0` | Hole punch timeout (seconds) |
| `GamePort` | int32 | `7777` | Local game server port |
| `bAutoHeartbeat` | bool | `true` | Auto-send heartbeats |
| `HeartbeatInterval` | float | `30.0` | Heartbeat interval (seconds) |

#### Runtime State

| Property | Type | Description |
|----------|------|-------------|
| `GameId` | FString | Current game session ID |
| `JoinCode` | FString | 6-character join code |
| `HostToken` | FString | Host authentication token |
| `bIsHosting` | bool | Whether we are the host |
| `bIsClient` | bool | Whether we are a joining client |
| `bIsConnected` | bool | Whether a connection is established |
| `DetectedNATType` | ENATType | Discovered NAT type |
| `ActiveConnectionMethod` | EConnectionMethod | How we connected |
| `TurnCredentials` | FTurnCredentials | TURN relay credentials |
| `MasterClient` | UMasterServerClient* | REST client sub-component |
| `Traversal` | UNATTraversal* | Traversal sub-component |

#### Methods

| Method | Description |
|--------|-------------|
| `HostGame(FGameRegistration)` | Host a new game session |
| `JoinGame(FString Target, FString Password)` | Join by code or game ID |
| `StopGame()` | Stop current session |
| `RefreshGameList(FString VersionFilter)` | Fetch public game list |
| `UpdatePlayerCount(int32 Count)` | Update player count (host only) |

#### Events (Delegates)

| Event | Parameters | Description |
|-------|------------|-------------|
| `OnGameHosted` | GameId, JoinCode, HostToken | Game registered successfully |
| `OnGameJoining` | GameId | Join request initiated |
| `OnNATTypeDetected` | NATType, NATTypeName | NAT type discovered via STUN |
| `OnConnectionMethodDetermined` | Method, MethodName | Connection method selected |
| `OnConnectionEstablished` | PeerEndpoint | Peer connection ready |
| `OnPeerJoined` | PeerId | A peer connected |
| `OnPeerLeft` | PeerId | A peer disconnected |
| `OnError` | Error | Error occurred |
| `OnGameStopped` | *(none)* | Session ended |

### UMasterServerClient

REST client for the master server API. Created automatically by `UNATClient`, but can be used standalone.

#### Methods

| Method | Description |
|--------|-------------|
| `Initialize(FString Url, FString ApiKey)` | Set server URL and API key |
| `RegisterGame(FGameRegistration)` | Register a new game |
| `ListGames(Code, Version, Limit, Offset)` | List public games |
| `GetGame(FString GameId)` | Get game by ID |
| `SendHeartbeat(GameId, HostToken)` | Keep game alive |
| `DeregisterGame(GameId, HostToken)` | Remove game |
| `GetTurnCredentials(GameId)` | Get TURN relay credentials |
| `CheckHealth()` | Check server status |

#### Events

| Event | Parameters | Description |
|-------|------------|-------------|
| `OnGameRegistered` | FRegisterResult | Registration result |
| `OnGameListReceived` | TArray\<FGameInfo\> | Game list received |
| `OnGameInfoReceived` | FGameInfo | Single game info |
| `OnTurnCredentialsReceived` | FTurnCredentials | TURN credentials |
| `OnHealthCheckComplete` | FServerHealth | Health status |
| `OnHeartbeatSent` | *(none)* | Heartbeat acknowledged |
| `OnGameDeregistered` | *(none)* | Game removed |
| `OnError` | FString | Error message |

### UNATTraversal

Low-level NAT operations. Created automatically by `UNATClient`.

#### Methods

| Method | Description |
|--------|-------------|
| `DiscoverNAT(StunServer, StunPort)` | STUN binding request |
| `TryUPnP(Port, TimeoutMs)` | UPnP port mapping |
| `ReleaseUPnP(Port)` | Release UPnP mapping |
| `ConnectSignaling(Url, ApiKey)` | Connect WebSocket |
| `DisconnectSignaling()` | Disconnect WebSocket |
| `RegisterHost(GameId, HostToken)` | Register as host |
| `RequestJoin(GameId, JoinCode, Password)` | Request to join |
| `SendICECandidate(SessionId, Candidate)` | Send ICE candidate |
| `SendConnectionEstablished(SessionId, Method)` | Notify connection |
| `SendHeartbeat()` | WebSocket heartbeat |
| `AttemptPunch(PeerIP, PeerPort, LocalPort, Timeout)` | UDP hole punch |
| `StopPunch()` | Cancel punch attempt |

#### Events

| Event | Parameters | Description |
|-------|------------|-------------|
| `OnStunDiscoveryComplete` | FStunResult | STUN result |
| `OnUPnPComplete` | FUPnPResult | UPnP result |
| `OnPunchComplete` | FPunchResult | Punch result |
| `OnSignalingConnected` | *(none)* | WebSocket connected |
| `OnSignalingDisconnected` | *(none)* | WebSocket closed |
| `OnHostRegistered` | GameId | Host registered |
| `OnGatherCandidates` | SessionId, StunServers | Gather ICE candidates |
| `OnPeerCandidate` | SessionId, FICECandidate | Peer candidate received |
| `OnPunchSignal` | SessionId, PeerIP, PeerPort | Punch initiation |
| `OnTurnFallback` | FTurnCredentials | TURN fallback credentials |
| `OnPeerConnected` | PeerId, Method | Peer connection confirmed |
| `OnSignalingError` | Error | Signaling error |

## Data Types

### ENATType

```cpp
enum class ENATType : uint8
{
    Unknown,
    Open,            // No NAT
    FullCone,        // Easiest to punch
    Moderate,        // Restricted cone
    PortRestricted,  // Port-restricted cone
    Symmetric        // Hardest - usually needs TURN
};
```

### EConnectionMethod

```cpp
enum class EConnectionMethod : uint8
{
    None,       // Not connected
    Direct,     // UPnP port mapping
    StunPunch,  // UDP hole punch
    TurnRelay   // TURN server relay
};
```

### FGameRegistration

```cpp
USTRUCT(BlueprintType)
struct FGameRegistration
{
    FString Name;            // Game name (required)
    int32 MaxPlayers = 4;
    int32 CurrentPlayers = 1;
    FString NATType = "unknown";
    FString Password;        // Optional, hashed server-side
    FString Map;
    FString GameVersion;
    int32 HostPort = 7777;
    FString LocalIP;
    int32 LocalPort = 7777;
    bool bPrivate = false;
    TMap<FString, FString> Data; // Custom metadata (max 4KB)
};
```

### FRegisterResult

```cpp
USTRUCT(BlueprintType)
struct FRegisterResult
{
    bool bSuccess;
    FString GameId;
    FString JoinCode;    // 6-character join code
    FString HostToken;   // Keep secret, used for heartbeat/deregister
    FString Error;
};
```

### FGameInfo

```cpp
USTRUCT(BlueprintType)
struct FGameInfo
{
    FString Id;
    FString Name;
    FString JoinCode;
    int32 MaxPlayers;
    int32 CurrentPlayers;
    FString NATType;
    bool bHasPassword;
    bool bPrivate;
    FString CreatedAt;
    TMap<FString, FString> Data;
};
```

### FTurnCredentials

```cpp
USTRUCT(BlueprintType)
struct FTurnCredentials
{
    FString Username;
    FString Password;
    int32 TTL;
    TArray<FString> URIs;
};
```

### FStunResult / FPunchResult / FUPnPResult / FServerHealth

See [NATTypes.h](../unreal-sdk/Source/NATpunchthrough/Public/NATTypes.h) for complete struct definitions.

## Connection Flow

### Host Flow

```
1. HostGame() called
2. STUN discovery runs (determines NAT type)
3. Game registered on master server
4. WebSocket signaling connected
5. Host registered on signaling server
6. Heartbeat loop starts
7. OnGameHosted fires with join code
8. Wait for peers: gather_candidates → ice_candidate → punch_signal
9. Hole punch or TURN fallback
10. OnConnectionEstablished fires
```

### Join Flow

```
1. JoinGame("XK9M2P") called
2. Join code resolved to game ID via ListGames
3. TURN credentials fetched
4. STUN discovery runs
5. WebSocket signaling connected
6. request_join sent with optional password
7. gather_candidates received
8. ICE candidates exchanged
9. punch_signal triggers UDP hole punch
10. On success → OnConnectionEstablished
11. On failure → TURN fallback → OnConnectionEstablished("relay")
```

## Advanced Usage

### Using MasterServerClient Standalone

```cpp
UMasterServerClient* Client = NewObject<UMasterServerClient>();
Client->Initialize(TEXT("https://your-server.com"), TEXT("api-key"));

Client->OnGameListReceived.AddDynamic(this, &AMyActor::OnGamesReceived);
Client->ListGames();
```

### Custom Signaling Flow

```cpp
UNATTraversal* Traversal = NewObject<UNATTraversal>();

// STUN discovery
Traversal->OnStunDiscoveryComplete.AddDynamic(this, &AMyActor::OnStun);
Traversal->DiscoverNAT();

// Manual WebSocket signaling
Traversal->OnPunchSignal.AddDynamic(this, &AMyActor::OnPunch);
Traversal->ConnectSignaling(TEXT("ws://your-server.com/ws"));
Traversal->RegisterHost(GameId, HostToken);
```

### Integrating with Unreal's Online Subsystem

The plugin doesn't replace Unreal's built-in networking — it establishes the initial peer connection. Once `OnConnectionEstablished` fires with the peer endpoint, use it with your preferred networking solution:

```cpp
void AMyGameMode::OnNATConnected(const FString& PeerEndpoint)
{
    // Parse endpoint
    FString IP;
    int32 Port;
    if (PeerEndpoint.Split(TEXT(":"), &IP, &Port))
    {
        // Use with Unreal networking
        FString TravelURL = FString::Printf(TEXT("%s:%d"), *IP, Port);
        GetWorld()->ServerTravel(TravelURL);
    }
}
```

## Troubleshooting

### Common Issues

**"WebSocket connection error"**
- Verify `ServerUrl` is correct and the server is running
- Check firewall rules for WebSocket connections (port 8080)
- Ensure the `WebSockets` module is enabled in your project

**"Failed to resolve STUN server"**
- Check internet connectivity
- Try a different STUN server: `stun:stun1.l.google.com:19302`

**"Hole punch timed out"**
- Both peers may have Symmetric NAT — TURN fallback should activate
- Increase `PunchTimeout` if on slow connections
- Verify `bUseTurnFallback = true`

**"Game not found with that join code"**
- Ensure the host is still running and sending heartbeats
- Game sessions expire after 90 seconds without a heartbeat

**Plugin not found in editor**
- Ensure the folder is named `NATpunchthrough` and is in `Plugins/`
- Regenerate project files
- Check Output Log for module loading errors

### Debug Logging

The plugin logs to the `LogTemp` category. Filter the Output Log to see NAT-specific messages:

```
LogTemp: NATClient: Hosting game 'My Game'...
LogTemp: NATClient: STUN discovery - Public IP: 1.2.3.4:12345, NAT: moderate
LogTemp: NATClient: Game registered - ID: abc123, Code: XK9M2P
LogTemp: NATClient: Signaling connected
LogTemp: NATClient: Hole punch succeeded! Remote: 5.6.7.8:7777
```

## Sample Project

See `Samples/NetworkGame/` for a complete working example:

- **SampleNetworkGameMode** — Hosts a game on BeginPlay, displays join code on screen
- **SamplePlayerController** — Client-side join flow with `JoinByCode()`
