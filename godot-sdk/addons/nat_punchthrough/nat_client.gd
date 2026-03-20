class_name NATClient
extends Node

## High-level NAT traversal client for Godot multiplayer games.
##
## Attach this node to your scene and configure the exported properties.
## NATClient orchestrates the full NAT traversal cascade:
## UPnP -> STUN hole punch -> TURN relay fallback.
##
## It manages the MasterServerClient (REST) and NATTraversal (signaling/STUN)
## internally so you only need to call [method host_game] or [method join_game].
##
## Example:
## [codeblock]
## @onready var nat := $NATClient
##
## func _ready():
##     nat.game_hosted.connect(_on_game_hosted)
##     nat.connection_established.connect(_on_connected)
##
## func host():
##     nat.host_game({"name": "My Game", "max_players": 4})
##
## func join(code: String):
##     nat.join_game(code)
## [/codeblock]

# ─── Exported Configuration ───────────────────────────────────────────────────

@export_group("Master Server")

## URL of the NAT Punchthrough Hero server.
@export var server_url: String = "http://localhost:8080"

## API key for authentication. Leave empty if the server has no key configured.
@export var api_key: String = ""

@export_group("NAT Traversal")

## Attempt UPnP port mapping before STUN.
@export var try_upnp: bool = true

## Attempt STUN hole punch via signaling.
@export var try_stun_punch: bool = true

## Fall back to TURN relay if hole punch fails.
@export var use_turn_fallback: bool = true

## Seconds to wait for STUN punch before falling back to TURN.
@export_range(3.0, 30.0, 0.5) var punch_timeout: float = 10.0

## Game server port (for UPnP mapping and ENet).
@export var game_port: int = 7777

@export_group("Heartbeat")

## Automatically send heartbeats while hosting.
@export var auto_heartbeat: bool = true

## Heartbeat interval in seconds.
@export_range(10.0, 60.0, 5.0) var heartbeat_interval: float = 30.0


# ─── Signals ──────────────────────────────────────────────────────────────────

## Emitted when game hosting begins successfully.
## [param game_id]: The registered game ID.
## [param join_code]: The 6-character join code for players.
## [param host_token]: Secret token for heartbeat/delete.
signal game_hosted(game_id: String, join_code: String, host_token: String)

## Emitted when joining a game session.
## [param game_id]: The game ID being joined.
signal game_joining(game_id: String)

## Emitted when NAT type is detected via STUN.
## [param nat_type]: The detected NATTraversal.NATType value.
## [param nat_type_name]: Human-readable NAT type string.
signal nat_type_detected(nat_type: NATTraversal.NATType, nat_type_name: String)

## Emitted when the connection method is determined.
## [param method]: The NATTraversal.ConnectionMethod value.
## [param method_name]: Human-readable method string (e.g. "direct", "stun_punch", "turn_relay").
signal connection_method_determined(method: NATTraversal.ConnectionMethod, method_name: String)

## Emitted when a peer connection is established (host or client).
## [param peer_endpoint]: The remote endpoint string (ip:port), or "relay" for TURN.
signal connection_established(peer_endpoint: String)

## Emitted when a peer joins our hosted game (host only).
## [param peer_id]: The joining peer's signaling ID.
signal peer_joined(peer_id: String)

## Emitted when a peer leaves (host only).
## [param peer_id]: The departing peer's ID.
signal peer_left(peer_id: String)

## Emitted when TURN relay credentials are received.
## [param credentials]: Dictionary with username, password, ttl, uris.
signal turn_credentials_received(credentials: Dictionary)

## Emitted on errors at any stage of the process.
## [param message]: Human-readable error description.
signal error_occurred(message: String)

## Emitted when the game is deregistered/stopped.
signal game_stopped


# ─── Runtime State ────────────────────────────────────────────────────────────

## Current game ID (set after hosting or joining).
var game_id: String = ""

## Join code for the current game (set after hosting).
var join_code: String = ""

## Host token for the current game (set after hosting).
var host_token: String = ""

## Whether we are currently hosting.
var is_hosting: bool = false

## Whether we are currently joining/connected as client.
var is_client: bool = false

## Whether a connection is established.
var is_connected: bool = false

## The NAT traversal module (access for advanced usage).
var traversal: NATTraversal = null

## The REST client (access for advanced usage).
var master_client: MasterServerClient = null

## TURN credentials (if retrieved).
var turn_credentials: Dictionary = {}

var _heartbeat_timer: Timer = null
var _punch_timer: Timer = null
var _join_password: String = "" # Stored temporarily during join flow


# ─── Lifecycle ────────────────────────────────────────────────────────────────

func _ready() -> void:
	traversal = NATTraversal.new()
	master_client = MasterServerClient.new(server_url, api_key)

	# Wire up signaling events
	traversal.registered.connect(_on_registered)
	traversal.peer_joined.connect(_on_peer_joined)
	traversal.peer_left.connect(_on_peer_left)
	traversal.peer_candidate_received.connect(_on_peer_candidate)
	traversal.punch_signal_received.connect(_on_punch_signal)
	traversal.turn_fallback.connect(_on_turn_fallback)
	traversal.signaling_error.connect(_on_signaling_error)


func _process(_delta: float) -> void:
	if traversal != null:
		traversal.poll_signaling()


func _exit_tree() -> void:
	stop_game()


# ─── Host a Game ──────────────────────────────────────────────────────────────

## Register and host a new game session.
##
## This starts the full NAT traversal cascade for hosting:
## 1. Try UPnP port mapping (if enabled)
## 2. Run STUN discovery to detect public IP and NAT type
## 3. Register the game on the master server
## 4. Connect to signaling WebSocket for peer connections
## 5. Start heartbeat timer
##
## [param game_info]: Dictionary with game metadata. Required keys:
## - "name" (String): Game display name.
## Optional keys: "max_players", "map", "game_version", "private", "password", "data"
func host_game(game_info: Dictionary) -> void:
	if is_hosting or is_client:
		error_occurred.emit("Already in a game session")
		return

	is_hosting = true
	print("[NATClient] Starting host sequence...")

	# Stage 1: Try UPnP
	if try_upnp:
		print("[NATClient] Attempting UPnP port mapping...")
		var upnp_result := await traversal.try_upnp(game_port)
		if upnp_result.success:
			print("[NATClient] UPnP success! External IP: ", upnp_result.external_ip)
			connection_method_determined.emit(NATTraversal.ConnectionMethod.DIRECT, "direct")
		else:
			print("[NATClient] UPnP failed: ", upnp_result.error)

	# Stage 2: STUN discovery
	print("[NATClient] Running STUN discovery...")
	var stun_result := await traversal.discover_nat()
	if stun_result.success:
		var type_name := NATTraversal.NATType.keys()[traversal.nat_type].to_lower()
		print("[NATClient] NAT type: ", type_name, " | Public: ", stun_result.public_ip, ":", stun_result.public_port)
		nat_type_detected.emit(traversal.nat_type, type_name)
		game_info["nat_type"] = type_name
	else:
		print("[NATClient] STUN discovery failed: ", stun_result.error)

	# Stage 3: Register on master server
	print("[NATClient] Registering game...")
	var reg_result := await master_client.register_game(game_info)
	if not reg_result.success:
		error_occurred.emit("Failed to register game: " + reg_result.get("error", "unknown"))
		is_hosting = false
		return

	game_id = reg_result.id
	join_code = reg_result.join_code
	host_token = reg_result.host_token

	print("[NATClient] Game registered! ID: ", game_id, " | Code: ", join_code)
	game_hosted.emit(game_id, join_code, host_token)

	# Stage 4: Connect to signaling
	var ws_url := _get_signaling_url()
	var err := traversal.connect_signaling(ws_url, api_key)
	if err != OK:
		error_occurred.emit("Failed to connect to signaling server")
		# Game is still registered, just no signaling
	else:
		# Wait for connection, then register as host
		await traversal.signaling_connected
		traversal.register_host(game_id, host_token)

	# Stage 5: Start heartbeat
	if auto_heartbeat:
		_start_heartbeat()


# ─── Join a Game ──────────────────────────────────────────────────────────────

## Join an existing game session by join code or game ID.
##
## This starts the NAT traversal cascade for joining:
## 1. Look up the game (if using join code)
## 2. Get TURN credentials (for fallback)
## 3. Run STUN discovery
## 4. Connect to signaling and attempt hole punch
## 5. Fall back to TURN relay if punch fails
##
## [param target]: Either a 6-character join code or a game ID.
## [param password]: Optional password for password-protected games.
func join_game(target: String, password: String = "") -> void:
	if is_hosting or is_client:
		error_occurred.emit("Already in a game session")
		return

	is_client = true
	_join_password = password
	print("[NATClient] Starting join sequence for: ", target)

	# Resolve join code to game ID if needed
	var target_game_id := target
	if target.length() <= 8 and not target.contains("-"):
		# Looks like a join code
		print("[NATClient] Looking up join code: ", target)
		var games := await master_client.list_games(target)
		if games.is_empty():
			error_occurred.emit("Game not found with code: " + target)
			is_client = false
			return
		target_game_id = games[0].get("id", "")
		if target_game_id == "":
			error_occurred.emit("Invalid game data for code: " + target)
			is_client = false
			return

	game_id = target_game_id
	game_joining.emit(game_id)

	# Stage 1: Get TURN credentials for fallback
	print("[NATClient] Fetching TURN credentials...")
	turn_credentials = await master_client.get_turn_credentials(game_id)
	if not turn_credentials.is_empty():
		turn_credentials_received.emit(turn_credentials)
		print("[NATClient] TURN credentials received (TTL: ", turn_credentials.get("ttl", 0), "s)")

	# Stage 2: STUN discovery
	print("[NATClient] Running STUN discovery...")
	var stun_result := await traversal.discover_nat()
	if stun_result.success:
		var type_name := NATTraversal.NATType.keys()[traversal.nat_type].to_lower()
		print("[NATClient] NAT type: ", type_name)
		nat_type_detected.emit(traversal.nat_type, type_name)
	else:
		print("[NATClient] STUN discovery failed: ", stun_result.error)

	# Stage 3: Connect to signaling and request join
	if try_stun_punch:
		print("[NATClient] Connecting to signaling for hole punch...")
		var ws_url := _get_signaling_url()
		var err := traversal.connect_signaling(ws_url, api_key)
		if err != OK:
			print("[NATClient] Signaling connect failed, trying TURN...")
			_try_turn_fallback()
			return

		await traversal.signaling_connected
		traversal.request_join(game_id, _join_password)

		# Start punch timeout
		_punch_timer = Timer.new()
		_punch_timer.one_shot = true
		_punch_timer.wait_time = punch_timeout
		_punch_timer.timeout.connect(_on_punch_timeout)
		add_child(_punch_timer)
		_punch_timer.start()
	else:
		# Skip punch, go directly to TURN
		_try_turn_fallback()


# ─── Stop / Cleanup ──────────────────────────────────────────────────────────

## Stop the current game session and clean up all resources.
##
## If hosting, deregisters the game from the master server and releases
## any UPnP port mappings. Disconnects signaling WebSocket.
func stop_game() -> void:
	# Stop heartbeat
	if _heartbeat_timer != null:
		_heartbeat_timer.stop()
		_heartbeat_timer.queue_free()
		_heartbeat_timer = null

	# Stop punch timer
	if _punch_timer != null:
		_punch_timer.stop()
		_punch_timer.queue_free()
		_punch_timer = null

	# Deregister game
	if is_hosting and game_id != "" and host_token != "":
		print("[NATClient] Deregistering game...")
		await master_client.deregister_game(game_id, host_token)

	# Release UPnP
	if is_hosting and try_upnp:
		traversal.release_upnp(game_port)

	# Disconnect signaling
	if traversal != null:
		traversal.disconnect_signaling()

	game_id = ""
	join_code = ""
	host_token = ""
	_join_password = ""
	is_hosting = false
	is_client = false
	is_connected = false
	turn_credentials = {}

	game_stopped.emit()
	print("[NATClient] Game stopped.")


## Update the player count on the master server.
##
## [param count]: New current player count.
func update_player_count(count: int) -> void:
	# Player count is updated via heartbeat — the server keeps track
	# You can extend the REST API to support PATCH for this if needed
	pass


## Get the list of available games from the server.
##
## Convenience wrapper around MasterServerClient.list_games().
func get_game_list(version_filter: String = "") -> Array:
	return await master_client.list_games("", version_filter)


## Check if the server is healthy.
func check_server_health() -> Dictionary:
	return await master_client.check_health()


# ─── Internal ─────────────────────────────────────────────────────────────────

func _get_signaling_url() -> String:
	return server_url \
		.replace("http://", "ws://") \
		.replace("https://", "wss://") \
		+ "/ws/signaling"


func _start_heartbeat() -> void:
	_heartbeat_timer = Timer.new()
	_heartbeat_timer.wait_time = heartbeat_interval
	_heartbeat_timer.timeout.connect(_on_heartbeat)
	add_child(_heartbeat_timer)
	_heartbeat_timer.start()


func _on_heartbeat() -> void:
	if game_id != "" and host_token != "":
		var ok := await master_client.send_heartbeat(game_id, host_token)
		if not ok:
			print("[NATClient] Heartbeat failed — game may have expired")
			error_occurred.emit("Heartbeat failed")


func _on_registered(my_peer_id: String) -> void:
	print("[NATClient] Registered with signaling server, peer ID: ", my_peer_id)


func _on_peer_joined(remote_peer_id: String) -> void:
	print("[NATClient] Peer joined: ", remote_peer_id)
	peer_joined.emit(remote_peer_id)


func _on_peer_left(remote_peer_id: String) -> void:
	print("[NATClient] Peer left: ", remote_peer_id)
	peer_left.emit(remote_peer_id)


func _on_peer_candidate(from_peer: String, candidate: Dictionary) -> void:
	print("[NATClient] Peer candidate from ", from_peer, ": ",
		candidate.get("public_ip", "?"), ":", candidate.get("public_port", 0))


func _on_punch_signal(from_peer: String, data: Dictionary) -> void:
	var endpoint := str(data.get("peer_ip", "")) + ":" + str(data.get("peer_port", 0))
	print("[NATClient] Punch signal! Endpoint: ", endpoint)

	# Punch succeeded
	if _punch_timer != null:
		_punch_timer.stop()
		_punch_timer.queue_free()
		_punch_timer = null

	traversal.connection_method = NATTraversal.ConnectionMethod.STUN_PUNCH
	connection_method_determined.emit(NATTraversal.ConnectionMethod.STUN_PUNCH, "stun_punch")

	is_connected = true
	connection_established.emit(endpoint)

	# Notify signaling server
	traversal.send_connection_established(game_id)


func _on_turn_fallback(credentials: Dictionary) -> void:
	print("[NATClient] Received TURN fallback from signaling")
	if _punch_timer != null:
		_punch_timer.stop()
		_punch_timer.queue_free()
		_punch_timer = null

	turn_credentials = credentials
	turn_credentials_received.emit(credentials)
	_apply_turn_connection()


func _on_punch_timeout() -> void:
	print("[NATClient] Punch timeout reached")
	if _punch_timer != null:
		_punch_timer.queue_free()
		_punch_timer = null

	if use_turn_fallback:
		_try_turn_fallback()
	else:
		error_occurred.emit("NAT punch timed out and TURN fallback is disabled")


func _try_turn_fallback() -> void:
	if turn_credentials.is_empty():
		print("[NATClient] No TURN credentials available")
		error_occurred.emit("No TURN credentials — cannot establish relay connection")
		return

	_apply_turn_connection()


func _apply_turn_connection() -> void:
	print("[NATClient] Using TURN relay connection")
	traversal.connection_method = NATTraversal.ConnectionMethod.TURN_RELAY
	connection_method_determined.emit(NATTraversal.ConnectionMethod.TURN_RELAY, "turn_relay")

	is_connected = true
	connection_established.emit("relay")


func _on_signaling_error(message: String) -> void:
	print("[NATClient] Signaling error: ", message)
	error_occurred.emit("Signaling: " + message)
