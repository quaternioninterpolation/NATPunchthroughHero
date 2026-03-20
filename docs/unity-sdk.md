# Unity SDK Integration Guide

Integrate NAT Punchthrough Hero with your Unity/Mirror game.

## Overview

The Unity SDK provides three components:

1. **`NATTransport`** — Mirror Transport that handles NAT traversal automatically
2. **`MasterServerClient`** — REST client for the game list API
3. **`NATTraversal`** — Low-level NAT punchthrough logic

## Dependencies

Install these packages in Unity:

1. **Mirror** — [mirror-networking.gitbook.io](https://mirror-networking.gitbook.io/)
2. **LiteNetLib** (via NuGet or manual) — UDP networking with NAT punch support
3. **Open.NAT** (optional) — UPnP port mapping

### Package Installation

```
# Via Unity Package Manager (Mirror)
https://github.com/MirrorNetworking/Mirror.git

# LiteNetLib — download from NuGet or add to Packages/
# Open.NAT — download from NuGet
```

## Quick Setup

### 1. Add Transport to NetworkManager

```csharp
using UnityEngine;
using Mirror;

public class GameNetworkManager : NetworkManager
{
    [Header("NAT Punchthrough")]
    public string masterServerUrl = "http://localhost:8080";
    public string apiKey = "";

    private NATTransport natTransport;

    public override void Start()
    {
        base.Start();

        // Add or get the NAT transport
        natTransport = GetComponent<NATTransport>();
        if (natTransport == null)
            natTransport = gameObject.AddComponent<NATTransport>();

        natTransport.masterServerUrl = masterServerUrl;
        natTransport.apiKey = apiKey;

        // Set as active transport
        Transport.active = natTransport;
    }
}
```

### 2. Host a Game

```csharp
public async void HostGame(string gameName, int maxPlayers)
{
    var client = new MasterServerClient(masterServerUrl, apiKey);

    // Register on master server
    var result = await client.RegisterGame(new GameRegistration
    {
        Name = gameName,
        MaxPlayers = maxPlayers,
        NatType = natTransport.DetectedNATType,
        Data = new Dictionary<string, string>
        {
            { "map", SceneManager.GetActiveScene().name },
            { "mode", "pvp" }
        }
    });

    if (result.Success)
    {
        Debug.Log($"Game registered! Join code: {result.JoinCode}");

        // Store for later
        natTransport.GameId = result.GameId;
        natTransport.HostToken = result.HostToken;
        natTransport.JoinCode = result.JoinCode;

        // Start hosting
        NetworkManager.singleton.StartHost();

        // Begin heartbeat
        StartCoroutine(HeartbeatLoop(client, result.GameId, result.HostToken));
    }
}

private IEnumerator HeartbeatLoop(MasterServerClient client, string gameId, string token)
{
    while (NetworkServer.active)
    {
        yield return new WaitForSeconds(30f);
        client.SendHeartbeat(gameId, token);
    }
}
```

### 3. Browse & Join Games

```csharp
public async void RefreshGameList()
{
    var client = new MasterServerClient(masterServerUrl, apiKey);
    var games = await client.ListGames();

    foreach (var game in games)
    {
        Debug.Log($"{game.Name} ({game.CurrentPlayers}/{game.MaxPlayers}) - Code: {game.JoinCode}");
    }
}

public async void JoinByCode(string joinCode)
{
    var client = new MasterServerClient(masterServerUrl, apiKey);

    // Find game
    var games = await client.ListGames(code: joinCode);
    if (games.Count == 0)
    {
        Debug.LogError("Game not found!");
        return;
    }

    var game = games[0];

    // Get TURN credentials
    var turn = await client.GetTurnCredentials(game.Id);

    // Configure transport
    natTransport.GameId = game.Id;
    natTransport.TurnCredentials = turn;

    // Connect
    NetworkManager.singleton.StartClient();
}
```

## Component Reference

### NATTransport

Mirror Transport that implements the NAT traversal cascade.

```csharp
public class NATTransport : Transport
{
    [Header("Server Configuration")]
    public string masterServerUrl = "http://localhost:8080";
    public string apiKey = "";

    [Header("NAT Settings")]
    public bool tryUPnP = true;          // Try UPnP first
    public bool tryStunPunch = true;      // Try STUN hole punch
    public bool useTurnFallback = true;   // Fall back to TURN
    public float punchTimeout = 10f;      // Seconds before TURN fallback

    [Header("Runtime State")]
    public string GameId;
    public string HostToken;
    public string JoinCode;
    public string GamePassword;       // Set before StartClient() for password-protected games
    public string DetectedNATType;
    public TurnCredentials TurnCredentials;

    // Events
    public event Action<NATType> OnNATTypeDetected;
    public event Action<ConnectionMethod> OnConnectionEstablished;
}
```

#### Connection Methods

```csharp
public enum ConnectionMethod
{
    Direct,      // UPnP opened a port
    StunPunch,   // UDP hole punch succeeded
    TurnRelay    // Using TURN relay
}
```

### MasterServerClient

REST client for the master server API.

```csharp
public class MasterServerClient
{
    public MasterServerClient(string baseUrl, string apiKey = "");

    // Game management
    public Task<RegisterResult> RegisterGame(GameRegistration info);
    public Task<List<GameInfo>> ListGames(string code = null);
    public Task<GameInfo> GetGame(string gameId);
    public Task SendHeartbeat(string gameId, string hostToken);
    public Task DeregisterGame(string gameId, string hostToken);

    // TURN credentials
    public Task<TurnCredentials> GetTurnCredentials(string gameId);
}
```

### NATTraversal

Low-level NAT traversal logic.

```csharp
public class NATTraversal
{
    // UPnP
    public Task<UPnPResult> TryUPnP(int port);
    public Task ReleaseUPnP();

    // STUN
    public Task<StunResult> DiscoverNAT(string stunServer, int stunPort);

    // Hole Punch (uses LiteNetLib)
    public Task<PunchResult> AttemptPunch(
        string signalingUrl,
        string gameId,
        string peerEndpoint,
        float timeout
    );

    // TURN
    public void ConfigureTurnRelay(TurnCredentials credentials);
}
```

## NAT Traversal Flow (Detailed)

```csharp
// This happens automatically inside NATTransport.
// Shown here for understanding.

async Task<ConnectionMethod> EstablishConnection()
{
    // 1. Try UPnP
    if (tryUPnP)
    {
        var upnp = await traversal.TryUPnP(gamePort);
        if (upnp.Success)
        {
            OnConnectionEstablished?.Invoke(ConnectionMethod.Direct);
            return ConnectionMethod.Direct;
        }
    }

    // 2. Try STUN punch
    if (tryStunPunch)
    {
        // Discover our public endpoint via STUN
        var stun = await traversal.DiscoverNAT(stunServer, 3478);
        DetectedNATType = stun.NATType.ToString();
        OnNATTypeDetected?.Invoke(stun.NATType);

        // Exchange endpoints via signaling WebSocket
        // Attempt simultaneous UDP punch
        var punch = await traversal.AttemptPunch(
            signalingUrl, GameId, stun.PublicEndpoint, punchTimeout);

        if (punch.Success)
        {
            OnConnectionEstablished?.Invoke(ConnectionMethod.StunPunch);
            return ConnectionMethod.StunPunch;
        }
    }

    // 3. TURN relay fallback
    if (useTurnFallback && TurnCredentials != null)
    {
        traversal.ConfigureTurnRelay(TurnCredentials);
        OnConnectionEstablished?.Invoke(ConnectionMethod.TurnRelay);
        return ConnectionMethod.TurnRelay;
    }

    throw new Exception("All NAT traversal methods failed!");
}
```

## Data Types

```csharp
[Serializable]
public class GameRegistration
{
    public string Name;
    public int MaxPlayers;
    public int CurrentPlayers = 1;
    public string NatType = "unknown";
    public string Password;  // Optional game password (stored as SHA-256 hash on server)
    public Dictionary<string, string> Data;
}

[Serializable]
public class RegisterResult
{
    public bool Success;
    public string GameId;
    public string JoinCode;
    public string HostToken;
    public string Error;
}

[Serializable]
public class GameInfo
{
    public string Id;
    public string Name;
    public string JoinCode;
    public int MaxPlayers;
    public int CurrentPlayers;
    public string NatType;
    public bool HasPassword;  // True if a password is required to join
    public Dictionary<string, string> Data;
    public DateTime CreatedAt;
}

[Serializable]
public class TurnCredentials
{
    public string Username;
    public string Password;
    public int TTL;
    public string[] URIs;
}
```

## Example: Complete Game Lobby

```csharp
using UnityEngine;
using UnityEngine.UI;
using Mirror;
using System.Collections.Generic;

public class GameLobby : MonoBehaviour
{
    [Header("UI")]
    public InputField gameNameInput;
    public InputField joinCodeInput;
    public Text joinCodeDisplay;
    public Text connectionMethodDisplay;
    public Transform gameListParent;
    public GameObject gameListItemPrefab;

    [Header("Config")]
    public string serverUrl = "http://localhost:8080";
    public string apiKey = "";

    private MasterServerClient masterClient;
    private NATTransport natTransport;

    void Start()
    {
        masterClient = new MasterServerClient(serverUrl, apiKey);
        natTransport = FindFirstObjectByType<NATTransport>();

        natTransport.OnConnectionEstablished += method =>
        {
            connectionMethodDisplay.text = $"Connected via: {method}";
        };
    }

    public async void OnHostClicked()
    {
        string gameName = gameNameInput.text;
        if (string.IsNullOrEmpty(gameName)) gameName = "My Game";

        var result = await masterClient.RegisterGame(new GameRegistration
        {
            Name = gameName,
            MaxPlayers = 4
        });

        if (result.Success)
        {
            joinCodeDisplay.text = $"Join Code: {result.JoinCode}";
            natTransport.GameId = result.GameId;
            natTransport.HostToken = result.HostToken;
            NetworkManager.singleton.StartHost();
        }
    }

    public async void OnJoinClicked()
    {
        string code = joinCodeInput.text.ToUpper().Trim();
        var games = await masterClient.ListGames(code: code);

        if (games.Count > 0)
        {
            var turn = await masterClient.GetTurnCredentials(games[0].Id);
            natTransport.GameId = games[0].Id;
            natTransport.TurnCredentials = turn;
            NetworkManager.singleton.StartClient();
        }
    }

    public async void OnRefreshClicked()
    {
        // Clear old items
        foreach (Transform child in gameListParent)
            Destroy(child.gameObject);

        var games = await masterClient.ListGames();
        foreach (var game in games)
        {
            var item = Instantiate(gameListItemPrefab, gameListParent);
            item.GetComponentInChildren<Text>().text =
                $"{game.Name} ({game.CurrentPlayers}/{game.MaxPlayers})";
            // Add click handler to join
        }
    }
}
```

## Troubleshooting

| Issue | Solution |
|-------|----------|
| UPnP always fails | Some routers disable UPnP. This is expected. |
| STUN punch fails | Symmetric NAT. TURN fallback will handle it. |
| "Connection refused" | Check server URL and API key |
| High latency after connect | Likely using TURN relay. Check NAT type. |
| WebSocket disconnect | Network instability. Transport auto-reconnects. |

## See Also

- [API Reference](api-reference.md) — Full REST and WebSocket documentation
- [Architecture](architecture.md) — How NAT traversal works under the hood
- [Troubleshooting](troubleshooting.md) — Common issues and fixes
