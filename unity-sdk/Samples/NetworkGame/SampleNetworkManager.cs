using System;
using System.Collections;
using System.Collections.Generic;
using UnityEngine;
using Mirror;

namespace NatPunchthrough.Samples
{
    /// <summary>
    /// Sample NetworkManager demonstrating NAT Punchthrough Hero with Mirror.
    /// Provides IMGUI-based menu, chat, and options overlay — no scene setup required.
    ///
    /// Setup:
    ///   1. Create an empty scene, add a GameObject named "NetworkManager"
    ///   2. Add this component (NATTransport is auto-added)
    ///   3. Create a Player prefab: empty GameObject → add NetworkIdentity,
    ///      NetworkTransform (sync direction = ClientToServer), CharacterController, SamplePlayer
    ///   4. Drag the prefab into this component's "Player Prefab" field
    ///   5. Press Play
    /// </summary>
    [RequireComponent(typeof(NATTransport))]
    public class SampleNetworkManager : NetworkManager
    {
        [Header("Game Settings")]
        [SerializeField] private string defaultGameName = "NAT Punch Demo";
        [SerializeField] private int maxPlayers = 8;

        public static SampleNetworkManager Instance { get; private set; }

        // ── Networking state ────────────────────────────────────────────
        private NATTransport _transport;
        private MasterServerClient _masterClient;
        private string _gameId = "";
        private string _joinCode = "";
        private string _hostToken = "";
        private bool _isHost;
        private Coroutine _heartbeat;

        // ── UI state ────────────────────────────────────────────────────
        private enum UIState { Menu, Connecting, InGame }
        private UIState _uiState = UIState.Menu;

        private string _inputServerUrl = "http://localhost:8080";
        private string _inputApiKey = "";
        private string _inputGameName = "NAT Punch Demo";
        private string _inputHostPassword = "";
        private string _inputJoinCode = "";
        private string _inputJoinPassword = "";
        private string _statusText = "";
        private string _connectionMethod = "";
        private int _playerCount;

        // Chat
        private static readonly List<string> ChatLog = new();
        private string _chatInput = "";
        private bool _chatOpen;
        private Vector2 _chatScroll;

        // Options menu
        private bool _optionsOpen;

        // World objects
        private GameObject _worldRoot;

        /// <summary>True when chat or options menu is open — blocks player movement.</summary>
        public bool IsInputBlocked => _chatOpen || _optionsOpen;

        // ════════════════════════════════════════════════════════════════
        #region Lifecycle
        // ════════════════════════════════════════════════════════════════

        public override void Awake()
        {
            Instance = this;
            base.Awake();
        }

        public override void Start()
        {
            _transport = GetComponent<NATTransport>();
            _inputServerUrl = string.IsNullOrEmpty(_transport.masterServerUrl)
                ? "http://localhost:8080"
                : _transport.masterServerUrl;
            _inputApiKey = _transport.apiKey;
            _inputGameName = defaultGameName;

            _transport.OnConnectionMethodDetermined += m =>
                _connectionMethod = m.ToString();
            _transport.OnError += e =>
                _statusText = "Error: " + e;

            autoStartServerOnStart = false;
            autoConnectClientOnStart = false;

            base.Start();
        }

        private void Update()
        {
            // Cache player count once per second
            if (_uiState == UIState.InGame && Time.frameCount % 60 == 0)
                _playerCount = FindObjectsOfType<SamplePlayer>().Length;
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Networking
        // ════════════════════════════════════════════════════════════════

        private async void DoHost()
        {
            _uiState = UIState.Connecting;
            _statusText = "Registering game on master server...";

            _transport.masterServerUrl = _inputServerUrl;
            _transport.apiKey = _inputApiKey;
            _masterClient = new MasterServerClient(_inputServerUrl, _inputApiKey);

            var reg = new GameRegistration
            {
                name = _inputGameName,
                max_players = maxPlayers,
                password = _inputHostPassword,
            };

            RegisterResult result;
            try
            {
                result = await _masterClient.RegisterGame(reg);
            }
            catch (Exception e)
            {
                Debug.LogWarning("[SampleNetworkManager] RegisterGame failed: " + e.Message);
                _statusText = "Registration failed: " + e.Message;
                _uiState = UIState.Menu;
                return;
            }

            // Guard: user may have cancelled or object destroyed during await
            if (this == null || _uiState != UIState.Connecting)
                return;

            if (!result.Success)
            {
                _statusText = "Registration failed: " + result.Error;
                _uiState = UIState.Menu;
                return;
            }

            _gameId = result.GameId;
            _joinCode = result.JoinCode;
            _hostToken = result.HostToken;
            _transport.GameId = _gameId;
            _transport.JoinCode = _joinCode;
            _transport.HostToken = _hostToken;
            _isHost = true;

            _statusText = "Starting host...";
            StartHost();

            CreateWorld();
            _uiState = UIState.InGame;
            _statusText = "";

            _heartbeat = StartCoroutine(HeartbeatLoop());
            AddSystemMessage("You are hosting. Join code: " + _joinCode);
        }

        private void DoJoin()
        {
            string code = _inputJoinCode.Trim().ToUpper();
            if (string.IsNullOrWhiteSpace(code))
            {
                _statusText = "Please enter a join code.";
                return;
            }

            _uiState = UIState.Connecting;
            _statusText = "Joining game...";

            _transport.masterServerUrl = _inputServerUrl;
            _transport.apiKey = _inputApiKey;
            _transport.GamePassword = _inputJoinPassword;
            _isHost = false;

            networkAddress = "code:" + code;
            StartClient();
        }

        private void DoDisconnect()
        {
            if (_uiState == UIState.Menu) return;

            if (_heartbeat != null)
            {
                StopCoroutine(_heartbeat);
                _heartbeat = null;
            }

            // Save values needed for deregistration before cleanup clears them
            bool wasHost = _isHost;
            string gameId = _gameId;
            string hostToken = _hostToken;
            var masterClient = _masterClient;

            // Cleanup first so OnClientDisconnect (triggered by Stop*) won't double-run
            Cleanup();

            if (wasHost)
            {
                StopHost();
                if (masterClient != null && !string.IsNullOrEmpty(gameId))
                    _ = masterClient.DeregisterGame(gameId, hostToken);
            }
            else
            {
                StopClient();
            }
        }

        private void Cleanup()
        {
            if (_uiState == UIState.Menu) return; // Already cleaned up

            DestroyWorld();
            ChatLog.Clear();
            _gameId = "";
            _joinCode = "";
            _hostToken = "";
            _connectionMethod = "";
            _chatOpen = false;
            _optionsOpen = false;
            _chatInput = "";
            _isHost = false;
            _masterClient = null;
            _uiState = UIState.Menu;
            _statusText = "";
            _playerCount = 0;
        }

        private IEnumerator HeartbeatLoop()
        {
            while (true)
            {
                yield return new WaitForSeconds(30f);
                if (_masterClient != null && !string.IsNullOrEmpty(_gameId))
                    _ = _masterClient.SendHeartbeat(_gameId, _hostToken);
            }
        }

        // ── Mirror callbacks ────────────────────────────────────────────

        public override void OnClientConnect()
        {
            base.OnClientConnect();
            if (!_isHost)
            {
                CreateWorld();
                _uiState = UIState.InGame;
                _statusText = "";
                AddSystemMessage("Connected to server.");
            }
        }

        public override void OnClientDisconnect()
        {
            base.OnClientDisconnect();
            if (_uiState != UIState.Menu)
            {
                Cleanup();
                // Show reason after Cleanup so it isn't immediately cleared
                _statusText = "Disconnected from server.";
            }
        }

        public override void OnServerDisconnect(NetworkConnectionToClient conn)
        {
            base.OnServerDisconnect(conn);
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region World
        // ════════════════════════════════════════════════════════════════

        private void CreateWorld()
        {
            if (_worldRoot != null) return;
            _worldRoot = new GameObject("SampleWorld");

            // Ground plane (200×200 meters)
            var ground = GameObject.CreatePrimitive(PrimitiveType.Plane);
            ground.name = "Ground";
            ground.transform.SetParent(_worldRoot.transform);
            ground.transform.localScale = new Vector3(20, 1, 20);
            ground.transform.position = Vector3.zero;
            var groundMat = new Material(FindLitShader());
            groundMat.color = new Color(0.35f, 0.55f, 0.35f);
            ground.GetComponent<Renderer>().material = groundMat;

            // Directional light (sun)
            var lightObj = new GameObject("Sun");
            lightObj.transform.SetParent(_worldRoot.transform);
            lightObj.transform.rotation = Quaternion.Euler(50, -30, 0);
            var light = lightObj.AddComponent<Light>();
            light.type = LightType.Directional;
            light.intensity = 1.2f;
            light.shadows = LightShadows.Soft;
            light.color = new Color(1f, 0.96f, 0.88f);
        }

        private void DestroyWorld()
        {
            if (_worldRoot != null)
            {
                Destroy(_worldRoot);
                _worldRoot = null;
            }
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Chat
        // ════════════════════════════════════════════════════════════════

        /// <summary>Add a player chat message to the log.</summary>
        public static void AddChatMessage(string sender, string message)
        {
            // Sanitize to prevent rich-text tag injection
            sender = SanitizeRichText(sender);
            message = SanitizeRichText(message);
            ChatLog.Add($"<b>{sender}</b>: {message}");
            if (ChatLog.Count > 100)
                ChatLog.RemoveAt(0);
        }

        /// <summary>Add a system message to the chat log.</summary>
        public static void AddSystemMessage(string message)
        {
            ChatLog.Add($"<color=#aaa><i>{SanitizeRichText(message)}</i></color>");
            if (ChatLog.Count > 100)
                ChatLog.RemoveAt(0);
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region GUI
        // ════════════════════════════════════════════════════════════════

        private void OnGUI()
        {
            GUI.skin.label.richText = true;

            switch (_uiState)
            {
                case UIState.Menu:       DrawMenu(); break;
                case UIState.Connecting: DrawConnecting(); break;
                case UIState.InGame:     DrawInGame(); break;
            }
        }

        // ── Menu ────────────────────────────────────────────────────────

        private void DrawMenu()
        {
            float w = 380, h = 530;
            var rect = new Rect((Screen.width - w) / 2f, (Screen.height - h) / 2f, w, h);
            GUILayout.BeginArea(rect, "NAT Punchthrough Hero — Sample Game", GUI.skin.window);
            GUILayout.Space(4);

            // Server settings
            GUILayout.Label("Server URL:");
            _inputServerUrl = GUILayout.TextField(_inputServerUrl);
            GUILayout.Label("API Key (optional):");
            _inputApiKey = GUILayout.TextField(_inputApiKey);

            GUILayout.Space(10);
            DrawSeparator();

            // Host section
            GUILayout.Label("<b>Host a Game</b>");
            GUILayout.Label("Game Name:");
            _inputGameName = GUILayout.TextField(_inputGameName);
            GUILayout.Label("Password (optional):");
            _inputHostPassword = GUILayout.PasswordField(_inputHostPassword, '*');
            if (GUILayout.Button("Host Game", GUILayout.Height(30)))
                DoHost();

            GUILayout.Space(10);
            DrawSeparator();

            // Join section
            GUILayout.Label("<b>Join a Game</b>");
            GUILayout.Label("Join Code:");
            _inputJoinCode = GUILayout.TextField(_inputJoinCode, 6).ToUpper();
            GUILayout.Label("Password (if required):");
            _inputJoinPassword = GUILayout.PasswordField(_inputJoinPassword, '*');
            if (GUILayout.Button("Join Game", GUILayout.Height(30)))
                DoJoin();

            // Status
            if (!string.IsNullOrEmpty(_statusText))
            {
                GUILayout.Space(8);
                GUILayout.Label(_statusText);
            }

            GUILayout.EndArea();
        }

        // ── Connecting ──────────────────────────────────────────────────

        private void DrawConnecting()
        {
            float w = 320, h = 110;
            var rect = new Rect((Screen.width - w) / 2f, (Screen.height - h) / 2f, w, h);
            GUILayout.BeginArea(rect, "Connecting...", GUI.skin.window);
            GUILayout.Space(4);
            GUILayout.Label(_statusText);
            if (GUILayout.Button("Cancel"))
                DoDisconnect();
            GUILayout.EndArea();
        }

        // ── In-Game HUD ─────────────────────────────────────────────────

        private void DrawInGame()
        {
            HandleInGameInput();

            // Info panel — top left
            GUILayout.BeginArea(new Rect(10, 10, 260, 80));
            GUILayout.BeginVertical(GUI.skin.box);
            if (_isHost && !string.IsNullOrEmpty(_joinCode))
                GUILayout.Label($"Join Code: <b>{_joinCode}</b>");
            GUILayout.Label($"Players: {_playerCount}");
            if (!string.IsNullOrEmpty(_connectionMethod))
                GUILayout.Label($"Connection: {_connectionMethod}");
            GUILayout.EndVertical();
            GUILayout.EndArea();

            // Controls hint — top right
            GUILayout.BeginArea(new Rect(Screen.width - 280, 10, 270, 30));
            GUILayout.BeginHorizontal(GUI.skin.box);
            GUILayout.Label("WASD: Move  |  Enter: Chat  |  Esc: Menu");
            GUILayout.EndHorizontal();
            GUILayout.EndArea();

            // Chat panel — bottom left
            DrawChatPanel();

            // Options overlay — center
            if (_optionsOpen)
                DrawOptionsMenu();
        }

        private void HandleInGameInput()
        {
            if (Event.current.type != EventType.KeyDown)
                return;

            switch (Event.current.keyCode)
            {
                case KeyCode.Return:
                case KeyCode.KeypadEnter:
                    if (!_chatOpen)
                    {
                        _chatOpen = true;
                        _optionsOpen = false;
                        Event.current.Use();
                    }
                    break;
                case KeyCode.Escape:
                    if (_chatOpen)
                        _chatOpen = false;
                    else
                        _optionsOpen = !_optionsOpen;
                    Event.current.Use();
                    break;
            }
        }

        // ── Chat ────────────────────────────────────────────────────────

        private void DrawChatPanel()
        {
            var rect = new Rect(10, Screen.height - 230, 360, 220);
            GUILayout.BeginArea(rect);
            GUILayout.BeginVertical(GUI.skin.box);

            // Message log (use index loop — ChatLog can grow from RPCs mid-frame)
            _chatScroll = GUILayout.BeginScrollView(_chatScroll, GUILayout.Height(168));
            for (int i = 0; i < ChatLog.Count; i++)
                GUILayout.Label(ChatLog[i]);
            GUILayout.EndScrollView();

            // Auto-scroll
            if (ChatLog.Count > 0)
                _chatScroll.y = float.MaxValue;

            // Input row
            if (_chatOpen)
            {
                GUILayout.BeginHorizontal();
                GUI.SetNextControlName("ChatInput");
                _chatInput = GUILayout.TextField(_chatInput);
                GUI.FocusControl("ChatInput");

                bool send = GUILayout.Button("Send", GUILayout.Width(52));

                // Enter inside the text field also sends
                if (!send
                    && Event.current.type == EventType.KeyDown
                    && (Event.current.keyCode == KeyCode.Return
                        || Event.current.keyCode == KeyCode.KeypadEnter))
                {
                    send = true;
                    Event.current.Use();
                }

                if (send)
                {
                    if (!string.IsNullOrWhiteSpace(_chatInput))
                    {
                        var localPlayer = NetworkClient.localPlayer?.GetComponent<SamplePlayer>();
                        localPlayer?.SendChat(_chatInput.Trim());
                    }
                    _chatInput = "";
                    _chatOpen = false;
                }
                GUILayout.EndHorizontal();
            }
            else
            {
                GUILayout.Label("<color=#888><i>Press Enter to chat</i></color>");
            }

            GUILayout.EndVertical();
            GUILayout.EndArea();
        }

        // ── Options menu ────────────────────────────────────────────────

        private void DrawOptionsMenu()
        {
            float w = 260, h = 140;
            var rect = new Rect((Screen.width - w) / 2f, (Screen.height - h) / 2f, w, h);
            GUILayout.BeginArea(rect, "Options", GUI.skin.window);
            GUILayout.Space(4);

            if (GUILayout.Button("Disconnect", GUILayout.Height(34)))
                DoDisconnect();
            GUILayout.Space(4);
            if (GUILayout.Button("Resume", GUILayout.Height(34)))
                _optionsOpen = false;

            GUILayout.EndArea();
        }

        // ── Helpers ─────────────────────────────────────────────────────

        private static void DrawSeparator()
        {
            GUILayout.Box("", GUILayout.Height(1), GUILayout.ExpandWidth(true));
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Static Helpers
        // ════════════════════════════════════════════════════════════════

        /// <summary>Find a lit shader across Built-in, URP, and HDRP pipelines.</summary>
        internal static Shader FindLitShader()
        {
            // Built-in
            var shader = Shader.Find("Standard");
            if (shader != null) return shader;
            // URP
            shader = Shader.Find("Universal Render Pipeline/Lit");
            if (shader != null) return shader;
            // HDRP
            shader = Shader.Find("HDRP/Lit");
            if (shader != null) return shader;
            // Last resort
            shader = Shader.Find("Sprites/Default");
            return shader;
        }

        /// <summary>Strip rich-text tags from user-supplied strings to prevent injection.</summary>
        internal static string SanitizeRichText(string text)
        {
            if (string.IsNullOrEmpty(text)) return text;
            // Replace angle brackets with visually similar characters
            return text.Replace("<", "\u2039").Replace(">", "\u203A");
        }

        #endregion
    }
}
