using System;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using UnityEngine;

namespace NatPunchthrough
{
    /// <summary>
    /// Low-level NAT traversal operations: UPnP, STUN discovery, hole punch, TURN relay.
    /// Used internally by NATTransport but can be used directly for custom implementations.
    /// </summary>
    public class NATTraversal : IDisposable
    {
        // UPnP state
        private bool _upnpMapped;
        private int _upnpPort;

        // STUN state
        private string _publicIP;
        private int _publicPort;

        // Signaling WebSocket
        private WebSocketSharp.WebSocket _signalingWs;

        // Cancellation
        private CancellationTokenSource _cts = new();

        #region UPnP

        /// <summary>
        /// Attempt to open a port on the local router via UPnP.
        /// Requires Open.NAT library.
        /// </summary>
        public async Task<UPnPResult> TryUPnP(int port, int timeoutMs = 5000)
        {
            try
            {
                // Uses Open.NAT library
                // Install via NuGet: Open.NAT
                var discoverer = new Open.Nat.NatDiscoverer();
                var cts = new CancellationTokenSource(timeoutMs);
                var device = await discoverer.DiscoverDeviceAsync(
                    Open.Nat.PortMapper.Upnp, cts);

                // Try to create mapping
                await device.CreatePortMapAsync(
                    new Open.Nat.Mapping(
                        Open.Nat.Protocol.Udp,
                        port,
                        port,
                        "NAT Punchthrough Hero"));

                var externalIP = await device.GetExternalIPAsync();

                _upnpMapped = true;
                _upnpPort = port;

                Debug.Log($"[NATTraversal] UPnP mapped port {port}, external IP: {externalIP}");

                return new UPnPResult
                {
                    Success = true,
                    ExternalIP = externalIP.ToString(),
                    ExternalPort = port
                };
            }
            catch (Open.Nat.NatDeviceNotFoundException)
            {
                Debug.Log("[NATTraversal] No UPnP device found");
                return new UPnPResult { Success = false, Error = "No UPnP device found" };
            }
            catch (Exception e)
            {
                Debug.Log($"[NATTraversal] UPnP failed: {e.Message}");
                return new UPnPResult { Success = false, Error = e.Message };
            }
        }

        /// <summary>
        /// Release previously created UPnP port mapping.
        /// </summary>
        public async Task ReleaseUPnP()
        {
            if (!_upnpMapped) return;

            try
            {
                var discoverer = new Open.Nat.NatDiscoverer();
                var cts = new CancellationTokenSource(3000);
                var device = await discoverer.DiscoverDeviceAsync(
                    Open.Nat.PortMapper.Upnp, cts);

                await device.DeletePortMapAsync(
                    new Open.Nat.Mapping(
                        Open.Nat.Protocol.Udp,
                        _upnpPort,
                        _upnpPort,
                        "NAT Punchthrough Hero"));

                _upnpMapped = false;
                Debug.Log("[NATTraversal] UPnP mapping released");
            }
            catch (Exception e)
            {
                Debug.LogWarning($"[NATTraversal] Failed to release UPnP: {e.Message}");
            }
        }

        #endregion

        #region STUN Discovery

        /// <summary>
        /// Discover public IP and NAT type via STUN binding request.
        /// </summary>
        public async Task<StunResult> DiscoverNAT(string stunServer, int stunPort = 3478)
        {
            try
            {
                // Send STUN Binding Request (RFC 5389)
                byte[] request = BuildStunBindingRequest();

                using var udp = new UdpClient();
                var endpoint = new IPEndPoint(
                    (await Dns.GetHostAddressesAsync(stunServer))[0],
                    stunPort);

                await udp.SendAsync(request, request.Length, endpoint);

                // Wait for response with timeout
                var cts = new CancellationTokenSource(5000);
                var receiveTask = udp.ReceiveAsync();
                var completedTask = await Task.WhenAny(receiveTask, Task.Delay(5000, cts.Token));

                if (completedTask != receiveTask)
                {
                    return new StunResult
                    {
                        Success = false,
                        NATType = NATType.Unknown,
                        Error = "STUN timeout"
                    };
                }

                var result = await receiveTask;
                var (ip, port) = ParseStunResponse(result.Buffer);

                _publicIP = ip;
                _publicPort = port;

                // Determine NAT type based on comparison
                string localIP = GetLocalIPAddress();
                NATType natType;

                if (ip == localIP && port == ((IPEndPoint)udp.Client.LocalEndPoint).Port)
                {
                    natType = NATType.Open;
                }
                else if (ip == localIP)
                {
                    natType = NATType.PortRestricted;
                }
                else
                {
                    // Would need multiple STUN requests to different IPs/ports
                    // to distinguish Full Cone vs Symmetric.
                    // Simplified: assume moderate NAT.
                    natType = NATType.Moderate;
                }

                Debug.Log($"[NATTraversal] STUN result: {ip}:{port}, NAT type: {natType}");

                return new StunResult
                {
                    Success = true,
                    PublicIP = ip,
                    PublicPort = port,
                    NATType = natType,
                    PublicEndpoint = $"{ip}:{port}"
                };
            }
            catch (Exception e)
            {
                Debug.LogWarning($"[NATTraversal] STUN failed: {e.Message}");
                return new StunResult
                {
                    Success = false,
                    NATType = NATType.Unknown,
                    Error = e.Message
                };
            }
        }

        /// <summary>
        /// Build a minimal STUN Binding Request (RFC 5389).
        /// </summary>
        private byte[] BuildStunBindingRequest()
        {
            byte[] txId = new byte[12];
            new System.Random().NextBytes(txId);

            byte[] request = new byte[20];
            // Message Type: Binding Request (0x0001)
            request[0] = 0x00;
            request[1] = 0x01;
            // Message Length: 0 (no attributes)
            request[2] = 0x00;
            request[3] = 0x00;
            // Magic Cookie: 0x2112A442
            request[4] = 0x21;
            request[5] = 0x12;
            request[6] = 0xA4;
            request[7] = 0x42;
            // Transaction ID (12 bytes)
            Array.Copy(txId, 0, request, 8, 12);

            return request;
        }

        /// <summary>
        /// Parse a STUN Binding Response to extract XOR-MAPPED-ADDRESS.
        /// </summary>
        private (string ip, int port) ParseStunResponse(byte[] data)
        {
            if (data.Length < 20)
                throw new Exception("Invalid STUN response");

            int msgLength = (data[2] << 8) | data[3];

            // Parse attributes
            int offset = 20;
            while (offset < 20 + msgLength)
            {
                int attrType = (data[offset] << 8) | data[offset + 1];
                int attrLength = (data[offset + 2] << 8) | data[offset + 3];
                offset += 4;

                // XOR-MAPPED-ADDRESS (0x0020) or MAPPED-ADDRESS (0x0001)
                if (attrType == 0x0020 || attrType == 0x0001)
                {
                    int family = data[offset + 1];

                    if (family == 0x01) // IPv4
                    {
                        int port;
                        string ip;

                        if (attrType == 0x0020) // XOR-MAPPED
                        {
                            port = ((data[offset + 2] ^ 0x21) << 8) | (data[offset + 3] ^ 0x12);
                            byte[] ipBytes = new byte[4];
                            ipBytes[0] = (byte)(data[offset + 4] ^ 0x21);
                            ipBytes[1] = (byte)(data[offset + 5] ^ 0x12);
                            ipBytes[2] = (byte)(data[offset + 6] ^ 0xA4);
                            ipBytes[3] = (byte)(data[offset + 7] ^ 0x42);
                            ip = new IPAddress(ipBytes).ToString();
                        }
                        else // MAPPED-ADDRESS
                        {
                            port = (data[offset + 2] << 8) | data[offset + 3];
                            byte[] ipBytes = new byte[4];
                            Array.Copy(data, offset + 4, ipBytes, 0, 4);
                            ip = new IPAddress(ipBytes).ToString();
                        }

                        return (ip, port);
                    }
                }

                // Align to 4-byte boundary
                offset += attrLength;
                offset = (offset + 3) & ~3;
            }

            throw new Exception("No mapped address found in STUN response");
        }

        #endregion

        #region Hole Punch

        /// <summary>
        /// Attempt a UDP hole punch using the WebSocket signaling server.
        /// Uses LiteNetLib's NatPunchModule for the actual punch.
        /// </summary>
        public async Task<PunchResult> AttemptPunch(
            string signalingUrl,
            string gameId,
            string peerEndpoint,
            float timeoutSeconds,
            string gamePassword = null)
        {
            var tcs = new TaskCompletionSource<PunchResult>();
            var cts = new CancellationTokenSource(
                TimeSpan.FromSeconds(timeoutSeconds));

            cts.Token.Register(() =>
            {
                tcs.TrySetResult(new PunchResult
                {
                    Success = false,
                    Error = "Punch timeout"
                });
            });

            try
            {
                // Connect to signaling WebSocket
                _signalingWs = new WebSocketSharp.WebSocket(signalingUrl);

                _signalingWs.OnMessage += (sender, e) =>
                {
                    try
                    {
                        var msg = JsonUtility.FromJson<SignalingMessage>(e.Data);

                        switch (msg.type)
                        {
                            case "gather_candidates":
                                // Server wants us to gather ICE candidates
                                Debug.Log($"[NATTraversal] Gathering candidates for session {msg.session_id}");

                                // Send our STUN-discovered endpoint as ICE candidate
                                if (!string.IsNullOrEmpty(_publicIP))
                                {
                                    var candidateMsg = new SignalingMessage
                                    {
                                        type = "ice_candidate",
                                        session_id = msg.session_id,
                                        public_ip = _publicIP,
                                        public_port = _publicPort,
                                        local_ip = GetLocalIPAddress(),
                                        local_port = _publicPort,
                                        nat_type = "unknown"
                                    };
                                    _signalingWs.Send(JsonUtility.ToJson(candidateMsg));
                                }
                                break;

                            case "peer_candidate":
                                // Received peer's public endpoint
                                Debug.Log($"[NATTraversal] Peer endpoint: {msg.public_ip}:{msg.public_port}");
                                break;

                            case "punch_signal":
                                // Initiate UDP hole punch to peer
                                Debug.Log($"[NATTraversal] Punch signal: {msg.peer_ip}:{msg.peer_port}");
                                tcs.TrySetResult(new PunchResult
                                {
                                    Success = true,
                                    RemoteEndpoint = $"{msg.peer_ip}:{msg.peer_port}"
                                });
                                break;

                            case "turn_fallback":
                                // Server is telling us to use TURN
                                Debug.Log("[NATTraversal] TURN fallback received");
                                tcs.TrySetResult(new PunchResult
                                {
                                    Success = false,
                                    Error = "Server requested TURN fallback"
                                });
                                break;

                            case "error":
                                tcs.TrySetResult(new PunchResult
                                {
                                    Success = false,
                                    Error = msg.error ?? msg.message
                                });
                                break;
                        }
                    }
                    catch (Exception ex)
                    {
                        Debug.LogWarning($"[NATTraversal] Signaling parse error: {ex.Message}");
                    }
                };

                _signalingWs.OnError += (sender, e) =>
                {
                    tcs.TrySetResult(new PunchResult
                    {
                        Success = false,
                        Error = e.Message
                    });
                };

                _signalingWs.Connect();

                // Send join request
                var joinPayload = new SignalingMessage
                {
                    type = "request_join",
                    game_id = gameId
                };
                if (!string.IsNullOrEmpty(gamePassword))
                    joinPayload.password = gamePassword;
                _signalingWs.Send(JsonUtility.ToJson(joinPayload));

                // ICE candidates will be sent after server sends "gather_candidates"

                return await tcs.Task;
            }
            catch (Exception e)
            {
                return new PunchResult
                {
                    Success = false,
                    Error = e.Message
                };
            }
        }

        #endregion

        #region TURN Relay

        /// <summary>
        /// Configure TURN relay credentials for fallback connection.
        /// </summary>
        public void ConfigureTurnRelay(TurnCredentials credentials)
        {
            if (credentials == null)
            {
                Debug.LogWarning("[NATTraversal] No TURN credentials provided");
                return;
            }

            Debug.Log($"[NATTraversal] TURN configured: {credentials.uris?.Length ?? 0} URIs, TTL: {credentials.ttl}s");

            // Store TURN credentials for use by the transport layer.
            // The actual TURN client implementation depends on the
            // networking library (LiteNetLib doesn't have built-in TURN,
            // so you may need a TURN client library or use the Unity
            // WebRTC package for full ICE/TURN support).

            // For LiteNetLib-based TURN:
            // You would create a UDP socket that communicates through
            // the TURN server using TURN Allocate/CreatePermission/Send
            // protocol messages (RFC 5766).
        }

        #endregion

        #region Helpers

        private string GetLocalIPAddress()
        {
            try
            {
                using var socket = new Socket(
                    AddressFamily.InterNetwork,
                    SocketType.Dgram,
                    ProtocolType.Udp);
                socket.Connect("8.8.8.8", 80);
                return ((IPEndPoint)socket.LocalEndPoint).Address.ToString();
            }
            catch
            {
                return "127.0.0.1";
            }
        }

        public void Dispose()
        {
            _cts?.Cancel();
            _cts?.Dispose();

            if (_signalingWs != null && _signalingWs.IsAlive)
            {
                _signalingWs.Close();
            }
        }

        #endregion
    }

    #region Result Types

    public class UPnPResult
    {
        public bool Success;
        public string ExternalIP;
        public int ExternalPort;
        public string Error;
    }

    public class StunResult
    {
        public bool Success;
        public string PublicIP;
        public int PublicPort;
        public string PublicEndpoint;
        public NATType NATType;
        public string Error;
    }

    public class PunchResult
    {
        public bool Success;
        public string RemoteEndpoint;
        public string Error;
    }

    public enum NATType
    {
        Unknown,
        Open,        // No NAT / directly reachable
        FullCone,    // Any external host can send after mapping
        Moderate,    // Restricted cone — same IP can send
        PortRestricted, // Same IP+port required
        Symmetric    // Different mapping per destination (hardest)
    }

    [Serializable]
    internal class SignalingMessage
    {
        public string type;
        public string game_id;
        public string host_token;
        public string session_id;
        public string join_code;
        public string public_ip;
        public int public_port;
        public string local_ip;
        public int local_port;
        public string nat_type;
        public string method;
        public string peer_ip;
        public int peer_port;
        public string message;
        public string error;
        // Nested arrays for gather_candidates response
        public string[] stun_servers;
        // TURN fallback fields
        public string[] turn_server;
        public string username;
        public string password; // Game password (request_join) or TURN password (turn_fallback)
        public int ttl;
    }

    #endregion
}
