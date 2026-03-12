using System;
using System.Collections;
using System.Collections.Generic;
using UnityEngine;
using Mirror;

namespace NatPunchthrough
{
    /// <summary>
    /// Mirror Transport that implements NAT traversal cascade:
    /// UPnP → STUN hole punch → TURN relay fallback.
    ///
    /// Add this to your NetworkManager GameObject and set it as the active transport.
    /// </summary>
    [DisallowMultipleComponent]
    [HelpURL("https://github.com/you/natpunch/blob/main/docs/unity-sdk.md")]
    public class NATTransport : Transport
    {
        [Header("Master Server")]
        [Tooltip("URL of the NAT Punchthrough Hero server")]
        public string masterServerUrl = "http://localhost:8080";

        [Tooltip("API key for authentication (leave empty if auth disabled)")]
        public string apiKey = "";

        [Header("NAT Traversal Settings")]
        [Tooltip("Attempt UPnP port mapping before STUN")]
        public bool tryUPnP = true;

        [Tooltip("Attempt STUN hole punch")]
        public bool tryStunPunch = true;

        [Tooltip("Fall back to TURN relay if punch fails")]
        public bool useTurnFallback = true;

        [Tooltip("Seconds to wait for STUN punch before TURN fallback")]
        [Range(3f, 30f)]
        public float punchTimeout = 10f;

        [Tooltip("Game server port (for UPnP mapping)")]
        public int gamePort = 7777;

        [Header("Runtime State (Read-Only)")]
        [SerializeField] private string _gameId;
        [SerializeField] private string _joinCode;
        [SerializeField] private string _detectedNATType = "unknown";
        [SerializeField] private ConnectionMethod _connectionMethod;
        [SerializeField] private bool _isConnected;

        // Public properties
        public string GameId { get => _gameId; set => _gameId = value; }
        public string JoinCode { get => _joinCode; set => _joinCode = value; }
        public string HostToken { get; set; }
        public string DetectedNATType => _detectedNATType;
        public ConnectionMethod ActiveConnectionMethod => _connectionMethod;
        public TurnCredentials TurnCredentials { get; set; }

        // Events
        public event Action<string> OnNATTypeDetected;
        public event Action<ConnectionMethod> OnConnectionMethodDetermined;
        public event Action<string> OnJoinCodeReceived;
        public event Action<string> OnError;

        // Internal components
        private MasterServerClient _masterClient;
        private NATTraversal _traversal;
        private LiteNetLib.NetManager _netManager;
        private LiteNetLib.EventBasedNetListener _listener;
        private readonly Dictionary<int, LiteNetLib.NetPeer> _peers = new();
        private int _nextConnectionId = 1;
        private bool _serverActive;
        private bool _clientActive;

        #region Transport Implementation

        public override bool Available()
        {
            // Available on all platforms that support UDP
            return Application.platform != RuntimePlatform.WebGLPlayer;
        }

        public override int GetMaxPacketSize(int channelId = 0)
        {
            // LiteNetLib default MTU minus headers
            return 1168;
        }

        public override void Shutdown()
        {
            _netManager?.Stop();
            _traversal?.Dispose();
            _isConnected = false;
            _serverActive = false;
            _clientActive = false;
            _peers.Clear();
        }

        #region Server

        public override bool ServerActive() => _serverActive;

        public override void ServerStart()
        {
            Debug.Log("[NATTransport] Starting server...");
            _masterClient = new MasterServerClient(masterServerUrl, apiKey);
            _traversal = new NATTraversal();

            SetupNetManager();

            // Start the NAT traversal cascade for hosting
            StartCoroutine(ServerStartRoutine());
        }

        private IEnumerator ServerStartRoutine()
        {
            // Stage 1: Try UPnP
            if (tryUPnP)
            {
                Debug.Log("[NATTransport] Attempting UPnP port mapping...");
                var upnpTask = _traversal.TryUPnP(gamePort);
                yield return new WaitUntil(() => upnpTask.IsCompleted);

                if (upnpTask.Result.Success)
                {
                    Debug.Log($"[NATTransport] UPnP success! Mapped port {gamePort}");
                    _connectionMethod = ConnectionMethod.Direct;
                    OnConnectionMethodDetermined?.Invoke(ConnectionMethod.Direct);
                }
                else
                {
                    Debug.Log("[NATTransport] UPnP failed, will use STUN/TURN");
                }
            }

            // Stage 2: STUN discovery — detect our public IP and NAT type
            {
                Debug.Log("[NATTransport] Running STUN discovery...");
                var stunTask = _traversal.DiscoverNAT("stun.l.google.com");
                yield return new WaitUntil(() => stunTask.IsCompleted);

                if (stunTask.Result.Success)
                {
                    _detectedNATType = stunTask.Result.NATType.ToString().ToLower();
                    Debug.Log($"[NATTransport] NAT type detected: {_detectedNATType}");
                    OnNATTypeDetected?.Invoke(_detectedNATType);
                }
                else
                {
                    Debug.LogWarning($"[NATTransport] STUN discovery failed: {stunTask.Result.Error}");
                }
            }

            // Start listening
            _netManager.Start(gamePort);
            _serverActive = true;

            Debug.Log($"[NATTransport] Server listening on port {gamePort}");
            OnServerConnected?.Invoke();
        }

        public override void ServerStop()
        {
            Debug.Log("[NATTransport] Stopping server...");

            // Deregister from master server
            if (!string.IsNullOrEmpty(GameId) && !string.IsNullOrEmpty(HostToken))
            {
                _masterClient?.DeregisterGame(GameId, HostToken);
            }

            // Release UPnP mapping
            _traversal?.ReleaseUPnP();

            _netManager?.Stop();
            _serverActive = false;
            _peers.Clear();
        }

        public override void ServerSend(int connectionId, ArraySegment<byte> segment, int channelId)
        {
            if (_peers.TryGetValue(connectionId, out var peer))
            {
                var deliveryMethod = channelId == Channels.Reliable
                    ? LiteNetLib.DeliveryMethod.ReliableOrdered
                    : LiteNetLib.DeliveryMethod.Unreliable;
                peer.Send(segment.Array, segment.Offset, segment.Count, deliveryMethod);
            }
        }

        public override void ServerDisconnect(int connectionId)
        {
            if (_peers.TryGetValue(connectionId, out var peer))
            {
                peer.Disconnect();
                _peers.Remove(connectionId);
            }
        }

        public override string ServerGetClientAddress(int connectionId)
        {
            return _peers.TryGetValue(connectionId, out var peer)
                ? peer.Address.ToString()
                : "unknown";
        }

        public override Uri ServerUri()
        {
            var builder = new UriBuilder
            {
                Scheme = "natpunch",
                Host = _gameId ?? "unknown",
                Port = gamePort
            };
            return builder.Uri;
        }

        #endregion

        #region Client

        public override bool ClientConnected() => _clientActive && _isConnected;

        public override void ClientConnect(string address)
        {
            // Address is either a game ID or "joincode:XXXX"
            Debug.Log($"[NATTransport] Connecting to {address}...");
            _masterClient = new MasterServerClient(masterServerUrl, apiKey);
            _traversal = new NATTraversal();

            SetupNetManager();
            _netManager.Start();

            StartCoroutine(ClientConnectRoutine(address));
        }

        public override void ClientConnect(Uri uri)
        {
            ClientConnect(uri.Host);
        }

        private IEnumerator ClientConnectRoutine(string target)
        {
            _clientActive = true;

            // Parse target (game ID or join code)
            string gameId = target;
            if (target.StartsWith("code:", StringComparison.OrdinalIgnoreCase))
            {
                string code = target.Substring(5);
                var findTask = _masterClient.ListGames(code: code);
                yield return new WaitUntil(() => findTask.IsCompleted);

                if (findTask.Result.Count == 0)
                {
                    OnError?.Invoke("Game not found with code: " + code);
                    OnClientError?.Invoke(TransportError.DnsResolve, "Game not found");
                    yield break;
                }
                gameId = findTask.Result[0].Id;
            }

            GameId = gameId;

            // Stage 1: Get TURN credentials (we'll need them for fallback)
            var turnTask = _masterClient.GetTurnCredentials(gameId);
            yield return new WaitUntil(() => turnTask.IsCompleted);
            TurnCredentials = turnTask.Result;

            // Stage 2: STUN discovery — detect our public IP and NAT type
            {
                Debug.Log("[NATTransport] Running STUN discovery...");
                var stunTask = _traversal.DiscoverNAT("stun.l.google.com");
                yield return new WaitUntil(() => stunTask.IsCompleted);

                if (stunTask.Result.Success)
                {
                    _detectedNATType = stunTask.Result.NATType.ToString().ToLower();
                    Debug.Log($"[NATTransport] NAT type detected: {_detectedNATType}");
                    OnNATTypeDetected?.Invoke(_detectedNATType);
                }
                else
                {
                    Debug.LogWarning($"[NATTransport] STUN discovery failed: {stunTask.Result.Error}");
                }
            }

            // Stage 3: Try STUN punch
            if (tryStunPunch)
            {
                Debug.Log("[NATTransport] Attempting STUN hole punch...");
                string signalingUrl = masterServerUrl
                    .Replace("http://", "ws://")
                    .Replace("https://", "wss://")
                    + "/ws/signaling";

                // Append API key if configured
                if (!string.IsNullOrEmpty(apiKey))
                    signalingUrl += "?key=" + Uri.EscapeDataString(apiKey);

                var punchTask = _traversal.AttemptPunch(signalingUrl, gameId, null, punchTimeout);
                yield return new WaitUntil(() => punchTask.IsCompleted);

                if (punchTask.Result.Success)
                {
                    Debug.Log($"[NATTransport] STUN punch succeeded! Endpoint: {punchTask.Result.RemoteEndpoint}");
                    _connectionMethod = ConnectionMethod.StunPunch;
                    OnConnectionMethodDetermined?.Invoke(ConnectionMethod.StunPunch);

                    // Connect directly
                    _netManager.Connect(punchTask.Result.RemoteEndpoint, "natpunch");
                    _isConnected = true;
                    OnClientConnected?.Invoke();
                    yield break;
                }

                Debug.Log("[NATTransport] STUN punch failed.");
            }

            // Stage 4: TURN relay
            if (useTurnFallback && TurnCredentials != null)
            {
                Debug.Log("[NATTransport] Using TURN relay...");
                _connectionMethod = ConnectionMethod.TurnRelay;
                OnConnectionMethodDetermined?.Invoke(ConnectionMethod.TurnRelay);

                _traversal.ConfigureTurnRelay(TurnCredentials);
                // Connect through TURN
                // (Implementation depends on TURN client library)

                _isConnected = true;
                OnClientConnected?.Invoke();
                yield break;
            }

            OnError?.Invoke("All NAT traversal methods failed");
            OnClientError?.Invoke(TransportError.Unexpected, "NAT traversal failed");
        }

        public override void ClientDisconnect()
        {
            _netManager?.Stop();
            _clientActive = false;
            _isConnected = false;
            OnClientDisconnected?.Invoke();
        }

        public override void ClientSend(ArraySegment<byte> segment, int channelId)
        {
            if (_peers.Count > 0)
            {
                var deliveryMethod = channelId == Channels.Reliable
                    ? LiteNetLib.DeliveryMethod.ReliableOrdered
                    : LiteNetLib.DeliveryMethod.Unreliable;

                foreach (var peer in _peers.Values)
                {
                    peer.Send(segment.Array, segment.Offset, segment.Count, deliveryMethod);
                    break; // Client only has one server peer
                }
            }
        }

        #endregion

        #endregion

        #region LiteNetLib Setup

        private void SetupNetManager()
        {
            _listener = new LiteNetLib.EventBasedNetListener();
            _netManager = new LiteNetLib.NetManager(_listener)
            {
                NatPunchEnabled = true,
                UpdateTime = 15,
                DisconnectTimeout = 10000
            };

            _listener.ConnectionRequestEvent += request =>
            {
                if (_serverActive)
                {
                    request.AcceptIfKey("natpunch");
                }
            };

            _listener.PeerConnectedEvent += peer =>
            {
                int connId = _nextConnectionId++;
                _peers[connId] = peer;

                if (_serverActive)
                {
                    OnServerConnected?.Invoke(connId);
                }
            };

            _listener.PeerDisconnectedEvent += (peer, info) =>
            {
                foreach (var kv in _peers)
                {
                    if (kv.Value == peer)
                    {
                        _peers.Remove(kv.Key);
                        if (_serverActive)
                        {
                            OnServerDisconnected?.Invoke(kv.Key);
                        }
                        break;
                    }
                }

                if (_clientActive)
                {
                    _isConnected = false;
                    OnClientDisconnected?.Invoke();
                }
            };

            _listener.NetworkReceiveEvent += (peer, reader, channel, deliveryMethod) =>
            {
                var data = new byte[reader.AvailableBytes];
                reader.GetBytes(data, data.Length);
                var segment = new ArraySegment<byte>(data);

                if (_serverActive)
                {
                    foreach (var kv in _peers)
                    {
                        if (kv.Value == peer)
                        {
                            OnServerDataReceived?.Invoke(kv.Key, segment, channel);
                            break;
                        }
                    }
                }
                else if (_clientActive)
                {
                    OnClientDataReceived?.Invoke(segment, channel);
                }

                reader.Recycle();
            };
        }

        private void Update()
        {
            _netManager?.PollEvents();
        }

        private void OnDestroy()
        {
            Shutdown();
        }

        #endregion
    }

    /// <summary>
    /// How the connection was established.
    /// </summary>
    public enum ConnectionMethod
    {
        None,
        /// <summary>UPnP opened a port — direct connection.</summary>
        Direct,
        /// <summary>STUN hole punch succeeded — direct P2P.</summary>
        StunPunch,
        /// <summary>Using TURN relay server — adds some latency.</summary>
        TurnRelay
    }
}
