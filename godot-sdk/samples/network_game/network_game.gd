extends Node3D
## Main controller for the NAT Punchthrough Hero sample game.
##
## Manages the menu UI, networking (NATClient + ENet), player spawning,
## chat, and the in-game options overlay. All UI is created in code so
## the .tscn stays lean.

@export var player_scene: PackedScene

# ─── Node references ─────────────────────────────────────────────────────────

@onready var nat_client: Node = $NATClient  # NATClient typed via class_name
@onready var players_node: Node3D = $Players

# ─── State ───────────────────────────────────────────────────────────────────

enum State { MENU, CONNECTING, IN_GAME }
var _state: State = State.MENU
var _is_host: bool = false
var _join_code: String = ""
var _host_endpoint: String = ""

# ─── UI references (built in _ready) ────────────────────────────────────────

var _canvas: CanvasLayer

# Menu
var _menu_panel: PanelContainer
var _server_url_input: LineEdit
var _api_key_input: LineEdit
var _game_name_input: LineEdit
var _host_password_input: LineEdit
var _join_code_input: LineEdit
var _join_password_input: LineEdit
var _menu_status: Label

# Connecting
var _connecting_panel: PanelContainer
var _connecting_status: Label

# HUD
var _hud: Control
var _info_label: Label
var _controls_label: Label

# Chat
var _chat_panel: PanelContainer
var _chat_log: RichTextLabel
var _chat_input: LineEdit
var _chat_open: bool = false

# Options
var _options_panel: PanelContainer
var _options_visible: bool = false

# ─── Chat messages ───────────────────────────────────────────────────────────

var _chat_messages: PackedStringArray = PackedStringArray()


# ═════════════════════════════════════════════════════════════════════════════
# Lifecycle
# ═════════════════════════════════════════════════════════════════════════════

func _ready() -> void:
	_build_ui()
	_switch_state(State.MENU)

	# NATClient signals
	nat_client.game_hosted.connect(_on_game_hosted)
	nat_client.connection_established.connect(_on_connection_established)
	nat_client.error_occurred.connect(_on_nat_error)

	# Multiplayer signals
	multiplayer.peer_connected.connect(_on_peer_connected)
	multiplayer.peer_disconnected.connect(_on_peer_disconnected)
	multiplayer.connected_to_server.connect(_on_connected_to_server)
	multiplayer.server_disconnected.connect(_on_server_disconnected)


func _unhandled_input(event: InputEvent) -> void:
	if _state != State.IN_GAME:
		return
	if not (event is InputEventKey) or not event.pressed or event.echo:
		return

	match event.keycode:
		KEY_ENTER, KEY_KP_ENTER:
			if not _chat_open:
				_open_chat()
				get_viewport().set_input_as_handled()
		KEY_ESCAPE:
			if _chat_open:
				_close_chat()
			else:
				_options_visible = not _options_visible
				_options_panel.visible = _options_visible
			get_viewport().set_input_as_handled()


# ═════════════════════════════════════════════════════════════════════════════
# Networking — Host
# ═════════════════════════════════════════════════════════════════════════════

func _host_game() -> void:
	_switch_state(State.CONNECTING)
	_connecting_status.text = "Starting server..."

	# Apply server URL / API key to NATClient
	nat_client.server_url = _server_url_input.text.strip_edges()
	nat_client.api_key = _api_key_input.text.strip_edges()

	# Create ENet server first so UPnP maps the correct port
	var peer := ENetMultiplayerPeer.new()
	var err := peer.create_server(nat_client.game_port)
	if err != OK:
		_connecting_status.text = "Failed to create server: " + error_string(err)
		_switch_state(State.MENU)
		return
	multiplayer.multiplayer_peer = peer
	_is_host = true

	# Register on master server (also does UPnP / STUN)
	_connecting_status.text = "Registering game..."
	var game_name := _game_name_input.text.strip_edges()
	if game_name == "":
		game_name = "Sample Game"
	var game_info := {"name": game_name, "max_players": 8}
	var pw := _host_password_input.text.strip_edges()
	if pw != "":
		game_info["password"] = pw
	nat_client.host_game(game_info)

	# Spawn host player immediately
	_spawn_player(1)
	_switch_state(State.IN_GAME)
	_add_system_message("You are hosting. Waiting for join code...")


func _on_game_hosted(game_id: String, join_code: String, _host_token: String) -> void:
	_join_code = join_code
	_update_info_label()
	_add_system_message("Join code: " + join_code)


# ═════════════════════════════════════════════════════════════════════════════
# Networking — Join
# ═════════════════════════════════════════════════════════════════════════════

func _join_game() -> void:
	var target := _join_code_input.text.strip_edges()
	if target == "":
		_menu_status.text = "Enter a join code or IP:port."
		return

	_switch_state(State.CONNECTING)
	_is_host = false

	nat_client.server_url = _server_url_input.text.strip_edges()
	nat_client.api_key = _api_key_input.text.strip_edges()

	# Direct IP:port — bypass NATClient
	if ":" in target and target.count(":") == 1 and target.split(":")[1].is_valid_int():
		var parts := target.split(":")
		_connecting_status.text = "Connecting to " + target + "..."
		_connect_enet_client(parts[0], int(parts[1]))
		return

	# Join via NATClient (NAT traversal)
	_connecting_status.text = "Looking up game..."
	var pw := _join_password_input.text.strip_edges()
	nat_client.join_game(target.to_upper(), pw)


func _on_connection_established(endpoint: String) -> void:
	if _is_host:
		return  # Host doesn't need to connect ENet here

	if endpoint == "relay":
		_connecting_status.text = "TURN relay requires WebRTC (not in this sample)."
		_add_system_message("TURN relay not supported in sample — use direct or STUN.")
		_disconnect()
		return

	if ":" in endpoint:
		var parts := endpoint.split(":")
		_connecting_status.text = "Connecting to " + endpoint + "..."
		_connect_enet_client(parts[0], int(parts[1]))
	else:
		_connecting_status.text = "Invalid endpoint: " + endpoint
		_disconnect()


func _connect_enet_client(host: String, port: int) -> void:
	var peer := ENetMultiplayerPeer.new()
	var err := peer.create_client(host, port)
	if err != OK:
		_connecting_status.text = "ENet connect failed: " + error_string(err)
		_switch_state(State.MENU)
		return
	multiplayer.multiplayer_peer = peer


func _on_connected_to_server() -> void:
	_switch_state(State.IN_GAME)
	_add_system_message("Connected to server.")


func _on_server_disconnected() -> void:
	_add_system_message("Server disconnected.")
	_disconnect()


# ═════════════════════════════════════════════════════════════════════════════
# Networking — Common
# ═════════════════════════════════════════════════════════════════════════════

func _on_peer_connected(id: int) -> void:
	if multiplayer.is_server():
		_spawn_player(id)
	_update_info_label()


func _on_peer_disconnected(id: int) -> void:
	if players_node.has_node(str(id)):
		players_node.get_node(str(id)).queue_free()
	_update_info_label()


func _spawn_player(id: int) -> void:
	var player := player_scene.instantiate()
	player.name = str(id)
	players_node.add_child(player, true)


func _disconnect() -> void:
	if _state == State.MENU:
		return  # Already disconnected / prevent re-entry

	# Immediately switch state so callbacks (server_disconnected) won't re-enter
	_switch_state(State.MENU)

	# Clean up players
	for child in players_node.get_children():
		child.queue_free()

	# Close and release the networking peer
	if multiplayer.multiplayer_peer:
		multiplayer.multiplayer_peer.close()
		multiplayer.multiplayer_peer = null

	# Deregister from master server (fire-and-forget, TTL will expire anyway)
	nat_client.stop_game()

	# Reset state
	_join_code = ""
	_is_host = false
	_chat_open = false
	_options_visible = false
	_chat_messages = PackedStringArray()
	if _chat_log:
		_chat_log.text = ""
	if _chat_input:
		_chat_input.visible = false
	_menu_status.text = ""


func _on_nat_error(message: String) -> void:
	if _state == State.CONNECTING:
		_connecting_status.text = "Error: " + message
	else:
		_add_system_message("NAT error: " + message)


# ═════════════════════════════════════════════════════════════════════════════
# Chat
# ═════════════════════════════════════════════════════════════════════════════

func _open_chat() -> void:
	_chat_open = true
	_chat_input.visible = true
	_chat_input.text = ""
	_chat_input.grab_focus()


func _close_chat() -> void:
	_chat_open = false
	_chat_input.visible = false
	_chat_input.release_focus()


func _on_chat_submitted(text: String) -> void:
	var msg := text.strip_edges()
	if msg != "":
		_send_chat(msg)
	_close_chat()


func _send_chat(message: String) -> void:
	if not multiplayer.has_multiplayer_peer() or multiplayer.multiplayer_peer.get_connection_status() == MultiplayerPeer.CONNECTION_DISCONNECTED:
		return
	if message.length() > 200:
		message = message.substr(0, 200)
	var my_name := _get_local_player_name()
	_broadcast_chat.rpc(my_name, message)


@rpc("any_peer", "call_local", "reliable")
func _broadcast_chat(sender_name: String, message: String) -> void:
	_add_chat_message(sender_name, message)


func _add_chat_message(sender: String, message: String) -> void:
	# Sanitize to prevent BBCode injection via player names or messages
	sender = _sanitize_bbcode(sender)
	message = _sanitize_bbcode(message)
	var line := "[b]" + sender + "[/b]: " + message
	_chat_messages.append(line)
	if _chat_messages.size() > 100:
		_chat_messages = _chat_messages.slice(1)
	_refresh_chat_log()


func _add_system_message(message: String) -> void:
	var line := "[i][color=#aaaaaa]" + _sanitize_bbcode(message) + "[/color][/i]"
	_chat_messages.append(line)
	if _chat_messages.size() > 100:
		_chat_messages = _chat_messages.slice(1)
	_refresh_chat_log()


func _refresh_chat_log() -> void:
	if _chat_log == null:
		return
	_chat_log.text = "\n".join(_chat_messages)
	# Defer scroll so the RichTextLabel updates its line count first
	_chat_log.scroll_to_line.call_deferred(_chat_log.get_line_count())


func _get_local_player_name() -> String:
	var id := str(multiplayer.get_unique_id())
	var player_node = players_node.get_node_or_null(id)
	if player_node and "player_name" in player_node:
		return player_node.player_name
	return "Player" + id.substr(0, 4)


# ═════════════════════════════════════════════════════════════════════════════
# UI — State switching
# ═════════════════════════════════════════════════════════════════════════════

func _switch_state(new_state: State) -> void:
	_state = new_state
	_menu_panel.visible = (new_state == State.MENU)
	_connecting_panel.visible = (new_state == State.CONNECTING)
	_hud.visible = (new_state == State.IN_GAME)
	_chat_panel.visible = (new_state == State.IN_GAME)
	_options_panel.visible = false
	_options_visible = false

	if new_state == State.IN_GAME:
		_update_info_label()


func _update_info_label() -> void:
	var lines := PackedStringArray()
	if _is_host and _join_code != "":
		lines.append("Join Code: " + _join_code)
	var count := players_node.get_child_count()
	lines.append("Players: " + str(count))
	if _is_host:
		lines.append("Role: Host")
	else:
		lines.append("Role: Client")
	_info_label.text = "\n".join(lines)


# ═════════════════════════════════════════════════════════════════════════════
# UI — Build (called once in _ready)
# ═════════════════════════════════════════════════════════════════════════════

func _build_ui() -> void:
	_canvas = CanvasLayer.new()
	_canvas.name = "UI"
	add_child(_canvas)

	_build_menu()
	_build_connecting()
	_build_hud()
	_build_chat()
	_build_options()


# ── Menu panel ───────────────────────────────────────────────────────────────

func _build_menu() -> void:
	var center := CenterContainer.new()
	center.set_anchors_preset(Control.PRESET_FULL_RECT)
	center.mouse_filter = Control.MOUSE_FILTER_IGNORE
	_canvas.add_child(center)

	_menu_panel = PanelContainer.new()
	_menu_panel.custom_minimum_size = Vector2(380, 0)
	center.add_child(_menu_panel)

	var margin := MarginContainer.new()
	margin.add_theme_constant_override("margin_left", 16)
	margin.add_theme_constant_override("margin_right", 16)
	margin.add_theme_constant_override("margin_top", 16)
	margin.add_theme_constant_override("margin_bottom", 16)
	_menu_panel.add_child(margin)

	var vbox := VBoxContainer.new()
	vbox.add_theme_constant_override("separation", 6)
	margin.add_child(vbox)

	# Title
	var title := Label.new()
	title.text = "NAT Punchthrough Hero — Sample Game"
	title.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	title.add_theme_font_size_override("font_size", 18)
	vbox.add_child(title)

	vbox.add_child(HSeparator.new())

	# Server URL
	vbox.add_child(_label("Server URL:"))
	_server_url_input = LineEdit.new()
	_server_url_input.text = "http://localhost:8080"
	_server_url_input.placeholder_text = "http://your-server:8080"
	vbox.add_child(_server_url_input)

	# API Key
	vbox.add_child(_label("API Key (optional):"))
	_api_key_input = LineEdit.new()
	_api_key_input.placeholder_text = "Leave empty if not set"
	vbox.add_child(_api_key_input)

	vbox.add_child(HSeparator.new())

	# Host section
	var host_lbl := Label.new()
	host_lbl.text = "Host a Game"
	host_lbl.add_theme_font_size_override("font_size", 16)
	vbox.add_child(host_lbl)

	vbox.add_child(_label("Game Name:"))
	_game_name_input = LineEdit.new()
	_game_name_input.text = "NAT Punch Demo"
	vbox.add_child(_game_name_input)

	vbox.add_child(_label("Password (optional):"))
	_host_password_input = LineEdit.new()
	_host_password_input.placeholder_text = "Leave empty for no password"
	_host_password_input.secret = true
	vbox.add_child(_host_password_input)

	var host_btn := Button.new()
	host_btn.text = "Host Game"
	host_btn.custom_minimum_size.y = 34
	host_btn.pressed.connect(_host_game)
	vbox.add_child(host_btn)

	vbox.add_child(HSeparator.new())

	# Join section
	var join_lbl := Label.new()
	join_lbl.text = "Join a Game"
	join_lbl.add_theme_font_size_override("font_size", 16)
	vbox.add_child(join_lbl)

	vbox.add_child(_label("Join Code (or IP:port):"))
	_join_code_input = LineEdit.new()
	_join_code_input.placeholder_text = "ABC123 or 127.0.0.1:7777"
	_join_code_input.max_length = 21
	vbox.add_child(_join_code_input)

	vbox.add_child(_label("Password (if required):"))
	_join_password_input = LineEdit.new()
	_join_password_input.placeholder_text = "Leave empty if none"
	_join_password_input.secret = true
	vbox.add_child(_join_password_input)

	var join_btn := Button.new()
	join_btn.text = "Join Game"
	join_btn.custom_minimum_size.y = 34
	join_btn.pressed.connect(_join_game)
	vbox.add_child(join_btn)

	# Status
	_menu_status = Label.new()
	_menu_status.add_theme_color_override("font_color", Color(1, 0.4, 0.4))
	vbox.add_child(_menu_status)


# ── Connecting panel ─────────────────────────────────────────────────────────

func _build_connecting() -> void:
	var center := CenterContainer.new()
	center.set_anchors_preset(Control.PRESET_FULL_RECT)
	center.mouse_filter = Control.MOUSE_FILTER_IGNORE
	_canvas.add_child(center)

	_connecting_panel = PanelContainer.new()
	_connecting_panel.custom_minimum_size = Vector2(320, 0)
	center.add_child(_connecting_panel)

	var margin := MarginContainer.new()
	margin.add_theme_constant_override("margin_left", 16)
	margin.add_theme_constant_override("margin_right", 16)
	margin.add_theme_constant_override("margin_top", 16)
	margin.add_theme_constant_override("margin_bottom", 16)
	_connecting_panel.add_child(margin)

	var vbox := VBoxContainer.new()
	vbox.add_theme_constant_override("separation", 10)
	margin.add_child(vbox)

	var title := Label.new()
	title.text = "Connecting..."
	title.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	title.add_theme_font_size_override("font_size", 18)
	vbox.add_child(title)

	_connecting_status = Label.new()
	_connecting_status.text = "Please wait..."
	_connecting_status.autowrap_mode = TextServer.AUTOWRAP_WORD
	vbox.add_child(_connecting_status)

	var cancel_btn := Button.new()
	cancel_btn.text = "Cancel"
	cancel_btn.custom_minimum_size.y = 30
	cancel_btn.pressed.connect(_disconnect)
	vbox.add_child(cancel_btn)


# ── HUD (in-game info) ──────────────────────────────────────────────────────

func _build_hud() -> void:
	_hud = Control.new()
	_hud.set_anchors_preset(Control.PRESET_FULL_RECT)
	_hud.mouse_filter = Control.MOUSE_FILTER_IGNORE
	_canvas.add_child(_hud)

	# Info label — top left
	var info_panel := PanelContainer.new()
	info_panel.position = Vector2(10, 10)
	info_panel.custom_minimum_size = Vector2(200, 0)
	_hud.add_child(info_panel)

	_info_label = Label.new()
	_info_label.text = ""
	info_panel.add_child(_info_label)

	# Controls hint — top right
	var hint_panel := PanelContainer.new()
	hint_panel.set_anchors_preset(Control.PRESET_TOP_RIGHT)
	hint_panel.position = Vector2(-290, 10)
	hint_panel.custom_minimum_size = Vector2(280, 0)
	_hud.add_child(hint_panel)

	_controls_label = Label.new()
	_controls_label.text = "WASD: Move | Enter: Chat | Esc: Menu"
	_controls_label.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	hint_panel.add_child(_controls_label)


# ── Chat panel ───────────────────────────────────────────────────────────────

func _build_chat() -> void:
	_chat_panel = PanelContainer.new()
	_chat_panel.set_anchors_preset(Control.PRESET_BOTTOM_LEFT)
	_chat_panel.anchor_top = 0.6
	_chat_panel.anchor_right = 0.35
	_chat_panel.offset_left = 10
	_chat_panel.offset_bottom = -10
	_chat_panel.offset_right = 0
	_chat_panel.offset_top = 0
	_canvas.add_child(_chat_panel)

	var vbox := VBoxContainer.new()
	vbox.add_theme_constant_override("separation", 4)
	_chat_panel.add_child(vbox)

	# Message log
	_chat_log = RichTextLabel.new()
	_chat_log.bbcode_enabled = true
	_chat_log.scroll_following = true
	_chat_log.size_flags_vertical = Control.SIZE_EXPAND_FILL
	_chat_log.custom_minimum_size.y = 120
	vbox.add_child(_chat_log)

	# Input field
	_chat_input = LineEdit.new()
	_chat_input.placeholder_text = "Type a message..."
	_chat_input.max_length = 200
	_chat_input.visible = false
	_chat_input.text_submitted.connect(_on_chat_submitted)
	vbox.add_child(_chat_input)

	# Hint when chat is closed
	var hint := Label.new()
	hint.text = "Press Enter to chat"
	hint.add_theme_color_override("font_color", Color(0.6, 0.6, 0.6))
	hint.add_theme_font_size_override("font_size", 12)
	vbox.add_child(hint)


# ── Options overlay ──────────────────────────────────────────────────────────

func _build_options() -> void:
	var center := CenterContainer.new()
	center.set_anchors_preset(Control.PRESET_FULL_RECT)
	center.mouse_filter = Control.MOUSE_FILTER_IGNORE
	_canvas.add_child(center)

	_options_panel = PanelContainer.new()
	_options_panel.custom_minimum_size = Vector2(260, 0)
	_options_panel.visible = false
	center.add_child(_options_panel)

	var margin := MarginContainer.new()
	margin.add_theme_constant_override("margin_left", 16)
	margin.add_theme_constant_override("margin_right", 16)
	margin.add_theme_constant_override("margin_top", 16)
	margin.add_theme_constant_override("margin_bottom", 16)
	_options_panel.add_child(margin)

	var vbox := VBoxContainer.new()
	vbox.add_theme_constant_override("separation", 8)
	margin.add_child(vbox)

	var title := Label.new()
	title.text = "Options"
	title.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	title.add_theme_font_size_override("font_size", 18)
	vbox.add_child(title)

	var disconnect_btn := Button.new()
	disconnect_btn.text = "Disconnect"
	disconnect_btn.custom_minimum_size.y = 34
	disconnect_btn.pressed.connect(_disconnect)
	vbox.add_child(disconnect_btn)

	var resume_btn := Button.new()
	resume_btn.text = "Resume"
	resume_btn.custom_minimum_size.y = 34
	resume_btn.pressed.connect(func():
		_options_visible = false
		_options_panel.visible = false
	)
	vbox.add_child(resume_btn)


# ── Helpers ──────────────────────────────────────────────────────────────────

func _label(text: String) -> Label:
	var lbl := Label.new()
	lbl.text = text
	return lbl


## Strip BBCode bracket tags from user-supplied strings to prevent injection.
static func _sanitize_bbcode(text: String) -> String:
	return text.replace("[", "(").replace("]", ")")
