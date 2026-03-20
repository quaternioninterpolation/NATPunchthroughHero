class_name NATTraversal
extends RefCounted

## Low-level NAT traversal operations: STUN discovery, WebSocket signaling,
## and TURN relay configuration.
##
## Used internally by NATClient but can also be used directly for custom
## implementations.
##
## Note: Godot does not include a UPnP library in the base engine, but the
## UPNP class is available. STUN hole punching is handled via the signaling
## WebSocket. TURN relay is configured via WebRTC or ENet.

# ─── Enums ─────────────────────────────────────────────────────────────────────

## NAT type detected via STUN.
enum NATType {
	UNKNOWN,
	OPEN,          ## No NAT — directly reachable.
	FULL_CONE,     ## Any external host can send after first mapping.
	MODERATE,      ## Restricted cone — only same IP can reply.
	PORT_RESTRICTED, ## Same IP + port required.
	SYMMETRIC,     ## Different mapping per destination (hardest to punch).
}

## How the connection was established.
enum ConnectionMethod {
	NONE,
	DIRECT,     ## UPnP opened a port — direct connection.
	STUN_PUNCH, ## STUN hole punch succeeded — direct P2P.
	TURN_RELAY, ## Using TURN relay server — adds latency.
}


# ─── Signals ───────────────────────────────────────────────────────────────────

## Emitted when the signaling WebSocket connects successfully.
signal signaling_connected

## Emitted when the signaling WebSocket disconnects.
signal signaling_disconnected

## Emitted when registered with the signaling server.
## [param peer_id]: Our assigned peer ID.
signal registered(peer_id: String)

## Emitted when a peer joins our game session.
## [param peer_id]: The joining peer's ID.
signal peer_joined(peer_id: String)

## Emitted when a peer leaves.
## [param peer_id]: The departing peer's ID.
signal peer_left(peer_id: String)

## Emitted when a peer's ICE candidate is received.
## [param from_peer]: Peer ID that sent the candidate.
## [param candidate]: Candidate data dictionary.
signal peer_candidate_received(from_peer: String, candidate: Dictionary)

## Emitted when a punch signal is received from a peer.
## [param from_peer]: Peer ID.
## [param data]: Punch signal data.
signal punch_signal_received(from_peer: String, data: Dictionary)

## Emitted when the server tells us to fall back to TURN relay.
## [param credentials]: TURN credentials dictionary.
signal turn_fallback(credentials: Dictionary)

## Emitted on signaling errors.
## [param message]: Error description.
signal signaling_error(message: String)


# ─── State ─────────────────────────────────────────────────────────────────────

## Our public IP as discovered via STUN.
var public_ip: String = ""

## Our public port as discovered via STUN.
var public_port: int = 0

## Detected NAT type.
var nat_type: NATType = NATType.UNKNOWN

## Current connection method.
var connection_method: ConnectionMethod = ConnectionMethod.NONE

## Our peer ID assigned by the signaling server.
var peer_id: String = ""

## Whether the signaling WebSocket is connected.
var is_signaling_connected: bool = false

var _ws: WebSocketPeer = null
var _ws_url: String = ""
var _poll_timer: Timer = null


# ─── UPnP ──────────────────────────────────────────────────────────────────────

## Attempt to open a port on the local router via UPnP.
##
## [param port]: The port to map.
## [param timeout_ms]: Discovery timeout in milliseconds.
##
## Returns a Dictionary:
## - "success" (bool)
## - "external_ip" (String): Public IP (on success)
## - "error" (String): Error message (on failure)
func try_upnp(port: int, timeout_ms: int = 5000) -> Dictionary:
	var upnp := UPNP.new()
	upnp.discover_multicast_if = ""

	var discover_result := upnp.discover(timeout_ms, 2, "InternetGatewayDevice")
	if discover_result != UPNP.UPNP_RESULT_SUCCESS:
		return {"success": false, "error": "UPnP discovery failed: " + str(discover_result)}

	if upnp.get_device_count() == 0:
		return {"success": false, "error": "No UPnP devices found"}

	var map_result := upnp.add_port_mapping(port, port, "NATPunchthroughHero", "UDP")
	if map_result != UPNP.UPNP_RESULT_SUCCESS:
		return {"success": false, "error": "UPnP port mapping failed: " + str(map_result)}

	var external_ip := upnp.query_external_address()
	if external_ip == "":
		# Mapping succeeded but couldn't query external IP
		return {"success": true, "external_ip": "", "error": ""}

	public_ip = external_ip
	connection_method = ConnectionMethod.DIRECT

	return {"success": true, "external_ip": external_ip, "error": ""}


## Remove a previously created UPnP port mapping.
func release_upnp(port: int) -> void:
	var upnp := UPNP.new()
	var discover_result := upnp.discover(3000, 2, "InternetGatewayDevice")
	if discover_result == UPNP.UPNP_RESULT_SUCCESS:
		upnp.delete_port_mapping(port, "UDP")


# ─── STUN Discovery ───────────────────────────────────────────────────────────

## Discover our public IP and NAT type via a STUN binding request (RFC 5389).
##
## [param stun_server]: STUN server hostname.
## [param stun_port]: STUN server port (default: 3478).
##
## Returns a Dictionary:
## - "success" (bool)
## - "public_ip" (String): Our public IP
## - "public_port" (int): Our public port
## - "nat_type" (NATType): Detected NAT type
## - "error" (String): Error message (on failure)
func discover_nat(stun_server: String = "stun.l.google.com", stun_port: int = 19302) -> Dictionary:
	var udp := PacketPeerUDP.new()

	# Resolve STUN server hostname
	var addresses := IP.resolve_hostname(stun_server)
	if addresses == "":
		return {"success": false, "public_ip": "", "public_port": 0, "nat_type": NATType.UNKNOWN, "error": "DNS resolution failed"}

	var err := udp.connect_to_host(addresses, stun_port)
	if err != OK:
		return {"success": false, "public_ip": "", "public_port": 0, "nat_type": NATType.UNKNOWN, "error": "UDP connect failed"}

	# Build STUN Binding Request (RFC 5389)
	var request := _build_stun_request()
	udp.put_packet(request)

	# Wait for response with timeout
	var start := Time.get_ticks_msec()
	var timeout := 5000
	while Time.get_ticks_msec() - start < timeout:
		if udp.get_available_packet_count() > 0:
			var response := udp.get_packet()
			var parsed := _parse_stun_response(response)
			udp.close()

			if parsed.success:
				public_ip = parsed.public_ip
				public_port = parsed.public_port

				# Simple NAT type heuristic
				var local_port := udp.get_local_port()
				if parsed.public_ip == _get_local_ip():
					if parsed.public_port == local_port:
						nat_type = NATType.OPEN
					else:
						nat_type = NATType.PORT_RESTRICTED
				else:
					nat_type = NATType.MODERATE

				parsed["nat_type"] = nat_type

			return parsed

		# Yield to avoid blocking
		await Engine.get_main_loop().process_frame

	udp.close()
	return {"success": false, "public_ip": "", "public_port": 0, "nat_type": NATType.UNKNOWN, "error": "STUN timeout"}


# ─── WebSocket Signaling ──────────────────────────────────────────────────────

## Connect to the signaling WebSocket server.
##
## [param url]: WebSocket URL (e.g., "ws://localhost:8080/ws/signaling").
## [param api_key]: Optional API key appended as query parameter.
func connect_signaling(url: String, api_key: String = "") -> Error:
	if api_key != "":
		var separator := "&" if "?" in url else "?"
		url += separator + "key=" + api_key.uri_encode()

	_ws_url = url
	_ws = WebSocketPeer.new()
	var err := _ws.connect_to_url(url)
	if err != OK:
		signaling_error.emit("WebSocket connect failed: " + str(err))
		return err

	return OK


## Must be called every frame (or via a Timer) to process WebSocket messages.
## If using NATClient, this is handled automatically.
func poll_signaling() -> void:
	if _ws == null:
		return

	_ws.poll()
	var state := _ws.get_ready_state()

	match state:
		WebSocketPeer.STATE_OPEN:
			if not is_signaling_connected:
				is_signaling_connected = true
				signaling_connected.emit()

			while _ws.get_available_packet_count() > 0:
				var packet := _ws.get_packet()
				var text := packet.get_string_from_utf8()
				_handle_signaling_message(text)

		WebSocketPeer.STATE_CLOSING:
			pass

		WebSocketPeer.STATE_CLOSED:
			if is_signaling_connected:
				is_signaling_connected = false
				signaling_disconnected.emit()
			_ws = null


## Send a JSON message through the signaling WebSocket.
func send_signaling(msg: Dictionary) -> Error:
	if _ws == null or _ws.get_ready_state() != WebSocketPeer.STATE_OPEN:
		return ERR_CONNECTION_ERROR

	var text := JSON.stringify(msg)
	return _ws.send_text(text)


## Register as a host on the signaling server.
func register_host(game_id: String, host_token: String) -> Error:
	return send_signaling({
		"type": "register_host",
		"game_id": game_id,
		"host_token": host_token,
	})


## Request to join a game session via signaling.
## [param game_password]: Optional password for password-protected games.
func request_join(game_id: String, game_password: String = "") -> Error:
	var msg := {
		"type": "request_join",
		"game_id": game_id,
	}
	if game_password != "":
		msg["password"] = game_password
	return send_signaling(msg)


## Send an ICE candidate to a peer.
func send_ice_candidate(game_id: String, candidate: Dictionary) -> Error:
	var msg := {
		"type": "ice_candidate",
		"game_id": game_id,
	}
	msg.merge(candidate)
	return send_signaling(msg)


## Send a punch signal to a peer.
func send_punch_signal(game_id: String, target_peer: String, data: Dictionary) -> Error:
	var msg := {
		"type": "punch_signal",
		"game_id": game_id,
		"target_peer": target_peer,
	}
	msg.merge(data)
	return send_signaling(msg)


## Notify the server that connection was established.
func send_connection_established(game_id: String) -> Error:
	return send_signaling({
		"type": "connection_established",
		"game_id": game_id,
	})


## Close the signaling WebSocket connection.
func disconnect_signaling() -> void:
	if _ws != null:
		_ws.close()
		_ws = null
		is_signaling_connected = false
		signaling_disconnected.emit()


# ─── TURN Relay ────────────────────────────────────────────────────────────────

## Configure TURN relay for a WebRTC connection.
##
## Returns a Dictionary suitable for use with Godot's WebRTCPeerConnection
## ICE server configuration.
##
## [param credentials]: Credentials from MasterServerClient.get_turn_credentials()
func get_turn_ice_servers(credentials: Dictionary) -> Array:
	if credentials.is_empty():
		return []

	var servers := []
	var uris = credentials.get("uris", [])
	for uri in uris:
		servers.append({
			"urls": [uri],
			"username": credentials.get("username", ""),
			"credential": credentials.get("password", ""),
		})

	return servers


# ─── Internal ──────────────────────────────────────────────────────────────────

func _handle_signaling_message(text: String) -> void:
	var json := JSON.new()
	if json.parse(text) != OK:
		signaling_error.emit("Invalid JSON from signaling server")
		return

	var msg: Dictionary = json.data
	var type: String = msg.get("type", "")

	match type:
		"registered":
			peer_id = msg.get("peer_id", "")
			registered.emit(peer_id)

		"peer_joined":
			peer_joined.emit(msg.get("peer_id", ""))

		"peer_left":
			peer_left.emit(msg.get("peer_id", ""))

		"gather_candidates":
			# Server wants us to gather ICE candidates
			# Send our STUN-discovered endpoint
			if public_ip != "":
				send_ice_candidate(msg.get("game_id", ""), {
					"public_ip": public_ip,
					"public_port": public_port,
					"local_ip": _get_local_ip(),
					"local_port": public_port,
					"nat_type": NATType.keys()[nat_type].to_lower(),
				})

		"peer_candidate":
			peer_candidate_received.emit(
				msg.get("from_peer", ""),
				msg
			)

		"punch_signal":
			punch_signal_received.emit(
				msg.get("from_peer", ""),
				msg
			)

		"turn_fallback":
			connection_method = ConnectionMethod.TURN_RELAY
			turn_fallback.emit(msg.get("credentials", msg))

		"error":
			signaling_error.emit(msg.get("message", msg.get("error", "Unknown error")))


func _build_stun_request() -> PackedByteArray:
	var buf := PackedByteArray()
	buf.resize(20)

	# Message Type: Binding Request (0x0001)
	buf[0] = 0x00; buf[1] = 0x01
	# Message Length: 0
	buf[2] = 0x00; buf[3] = 0x00
	# Magic Cookie: 0x2112A442
	buf[4] = 0x21; buf[5] = 0x12; buf[6] = 0xA4; buf[7] = 0x42

	# Transaction ID (12 random bytes)
	for i in range(8, 20):
		buf[i] = randi() % 256

	return buf


func _parse_stun_response(data: PackedByteArray) -> Dictionary:
	if data.size() < 20:
		return {"success": false, "public_ip": "", "public_port": 0, "error": "Response too short"}

	var msg_length := (data[2] << 8) | data[3]
	var offset := 20

	while offset < 20 + msg_length and offset + 4 <= data.size():
		var attr_type := (data[offset] << 8) | data[offset + 1]
		var attr_length := (data[offset + 2] << 8) | data[offset + 3]
		offset += 4

		# XOR-MAPPED-ADDRESS (0x0020) or MAPPED-ADDRESS (0x0001)
		if attr_type == 0x0020 or attr_type == 0x0001:
			if offset + 8 > data.size():
				break
			var family := data[offset + 1]

			if family == 0x01: # IPv4
				var port: int
				var ip: String

				if attr_type == 0x0020: # XOR-MAPPED
					port = ((data[offset + 2] ^ 0x21) << 8) | (data[offset + 3] ^ 0x12)
					ip = "%d.%d.%d.%d" % [
						data[offset + 4] ^ 0x21,
						data[offset + 5] ^ 0x12,
						data[offset + 6] ^ 0xA4,
						data[offset + 7] ^ 0x42,
					]
				else: # MAPPED-ADDRESS
					port = (data[offset + 2] << 8) | data[offset + 3]
					ip = "%d.%d.%d.%d" % [
						data[offset + 4],
						data[offset + 5],
						data[offset + 6],
						data[offset + 7],
					]

				return {"success": true, "public_ip": ip, "public_port": port, "error": ""}

		# Align to 4-byte boundary
		offset += attr_length
		offset = (offset + 3) & ~3

	return {"success": false, "public_ip": "", "public_port": 0, "error": "No mapped address in STUN response"}


func _get_local_ip() -> String:
	var addresses := IP.get_local_addresses()
	for addr in addresses:
		# Return first non-loopback IPv4 address
		if addr != "127.0.0.1" and "." in addr and not addr.begins_with("169.254"):
			return addr
	return "127.0.0.1"
