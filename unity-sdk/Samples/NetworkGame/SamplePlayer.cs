using UnityEngine;
using Mirror;

namespace NatPunchthrough.Samples
{
    /// <summary>
    /// Networked player for the sample game.
    /// Spawns a capsule, moves with WASD, and supports chat via Mirror RPCs.
    ///
    /// Required prefab components:
    ///   NetworkIdentity
    ///   NetworkTransform  (Sync Direction = ClientToServer)
    ///   CharacterController
    ///   SamplePlayer (this script)
    /// </summary>
    [RequireComponent(typeof(CharacterController))]
    public class SamplePlayer : NetworkBehaviour
    {
        [Header("Movement")]
        [SerializeField] private float moveSpeed = 8f;
        [SerializeField] private float gravity = -20f;

        [Header("Camera")]
        [SerializeField] private Vector3 cameraOffset = new(0, 10, -12);

        // ── Synced state ────────────────────────────────────────────────

        [SyncVar(hook = nameof(OnNameChanged))]
        private string _playerName = "";

        [SyncVar(hook = nameof(OnColorChanged))]
        private Color _playerColor = Color.white;

        public string PlayerName => _playerName;

        // ── Components ──────────────────────────────────────────────────

        private CharacterController _cc;
        private Camera _cam;
        private TextMesh _label;
        private MeshRenderer _bodyRenderer;
        private float _yVelocity;

        // ════════════════════════════════════════════════════════════════
        #region Lifecycle
        // ════════════════════════════════════════════════════════════════

        private void Awake()
        {
            _cc = GetComponent<CharacterController>();
            _cc.center = new Vector3(0, 1f, 0);
            _cc.height = 2f;
            _cc.radius = 0.5f;
        }

        public override void OnStartClient()
        {
            base.OnStartClient();
            BuildVisual();
        }

        public override void OnStartLocalPlayer()
        {
            base.OnStartLocalPlayer();

            // Camera
            _cam = new GameObject("PlayerCamera").AddComponent<Camera>();
            _cam.tag = "MainCamera";
            _cam.transform.SetParent(transform);
            _cam.transform.localPosition = cameraOffset;
            _cam.transform.LookAt(transform.position + Vector3.up * 1.5f);
            _cam.nearClipPlane = 0.3f;

            // Remove the default scene camera (tagged MainCamera) if one exists
            foreach (var cam in FindObjectsOfType<Camera>())
            {
                if (cam != _cam && cam.CompareTag("MainCamera"))
                    Destroy(cam.gameObject);
            }

            // Random spawn position on the ground plane
            transform.position = new Vector3(
                Random.Range(-30f, 30f), 0.1f, Random.Range(-30f, 30f));

            // Tell the server our identity
            string n = "Player" + Random.Range(100, 999);
            Color c = Color.HSVToRGB(Random.value, 0.55f, 0.9f);
            CmdSetIdentity(n, c);

            SampleNetworkManager.AddSystemMessage(n + " joined.");
        }

        private void BuildVisual()
        {
            // Capsule body
            var body = GameObject.CreatePrimitive(PrimitiveType.Capsule);
            body.name = "Body";
            body.transform.SetParent(transform);
            body.transform.localPosition = new Vector3(0, 1f, 0);
            Destroy(body.GetComponent<Collider>());

            _bodyRenderer = body.GetComponent<MeshRenderer>();
            var mat = new Material(SampleNetworkManager.FindLitShader())
            {
                color = _playerColor
            };
            _bodyRenderer.material = mat;

            // Floating name label
            var labelObj = new GameObject("NameLabel");
            labelObj.transform.SetParent(transform);
            labelObj.transform.localPosition = new Vector3(0, 2.6f, 0);

            _label = labelObj.AddComponent<TextMesh>();
            _label.alignment = TextAlignment.Center;
            _label.anchor = TextAnchor.MiddleCenter;
            _label.characterSize = 0.12f;
            _label.fontSize = 56;
            _label.color = Color.white;
            _label.text = string.IsNullOrEmpty(_playerName) ? "..." : _playerName;
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Movement & Camera
        // ════════════════════════════════════════════════════════════════

        private void Update()
        {
            if (!isLocalPlayer) return;

            if (SampleNetworkManager.Instance == null
                || !SampleNetworkManager.Instance.IsInputBlocked)
            {
                HandleMovement();
            }

            UpdateCamera();
        }

        private void HandleMovement()
        {
            float h = Input.GetAxisRaw("Horizontal");
            float v = Input.GetAxisRaw("Vertical");

            Vector3 dir = new Vector3(h, 0, v);
            if (dir.sqrMagnitude > 1f) dir.Normalize();

            Vector3 move = dir * moveSpeed;

            if (_cc.isGrounded)
                _yVelocity = -2f;
            else
                _yVelocity += gravity * Time.deltaTime;

            move.y = _yVelocity;
            _cc.Move(move * Time.deltaTime);
        }

        private void UpdateCamera()
        {
            if (_cam == null) return;
            _cam.transform.position = transform.position + cameraOffset;
            _cam.transform.LookAt(transform.position + Vector3.up * 1.5f);
        }

        private void LateUpdate()
        {
            // Billboard the name label toward the camera
            if (_label != null && Camera.main != null)
                _label.transform.rotation = Camera.main.transform.rotation;
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Identity Sync
        // ════════════════════════════════════════════════════════════════

        [Command]
        private void CmdSetIdentity(string playerName, Color color)
        {
            // Server-side sanitization: clamp name length, color will be validated by SyncVar
            if (playerName.Length > 20) playerName = playerName[..20];
            _playerName = playerName;
            _playerColor = color;
        }

        private void OnNameChanged(string _, string newName)
        {
            if (_label != null)
                _label.text = newName;
        }

        private void OnColorChanged(Color _, Color newColor)
        {
            if (_bodyRenderer != null)
                _bodyRenderer.material.color = newColor;
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Chat
        // ════════════════════════════════════════════════════════════════

        /// <summary>
        /// Called by the UI to send a chat message from the local player.
        /// </summary>
        public void SendChat(string message)
        {
            if (string.IsNullOrWhiteSpace(message)) return;
            CmdChat(message.Trim());
        }

        [Command]
        private void CmdChat(string message)
        {
            // Server-side validation and sanitization
            if (string.IsNullOrWhiteSpace(message)) return;
            if (message.Length > 200) message = message[..200];
            RpcChat(_playerName, message);
        }

        [ClientRpc]
        private void RpcChat(string sender, string message)
        {
            SampleNetworkManager.AddChatMessage(sender, message);
        }

        #endregion

        // ════════════════════════════════════════════════════════════════
        #region Cleanup
        // ════════════════════════════════════════════════════════════════

        public override void OnStopClient()
        {
            base.OnStopClient();
            if (!string.IsNullOrEmpty(_playerName))
                SampleNetworkManager.AddSystemMessage(_playerName + " left.");
        }

        private void OnDestroy()
        {
            if (_cam != null)
                Destroy(_cam.gameObject);
        }

        #endregion
    }
}
