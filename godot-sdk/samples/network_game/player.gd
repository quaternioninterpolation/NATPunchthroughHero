extends CharacterBody3D
## Networked player capsule with WASD movement.
##
## Authority is set based on the node's name (which equals the peer ID).
## The MultiplayerSynchronizer in the scene handles position/rotation replication.

const MOVE_SPEED := 8.0
const GRAVITY := -20.0

var player_name: String = "Player"
var player_color: Color = Color.WHITE

@onready var body: MeshInstance3D = $Body
@onready var name_label: Label3D = $NameLabel
@onready var camera: Camera3D = $Camera3D


func _enter_tree() -> void:
	# Convention: the node name is the peer ID (set by the server when spawning).
	var peer_id := name.to_int()
	if peer_id > 0:
		set_multiplayer_authority(peer_id)


func _ready() -> void:
	var peer_id := get_multiplayer_authority()

	# Activate camera only for the local player
	camera.current = (multiplayer.get_unique_id() == peer_id)

	# Deterministic color from peer ID (golden-ratio hue spread)
	player_color = Color.from_hsv(fmod(float(peer_id) * 0.618033, 1.0), 0.55, 0.9)
	var mat := StandardMaterial3D.new()
	mat.albedo_color = player_color
	body.material_override = mat

	# Name
	player_name = "Player" + str(peer_id).substr(0, 4)
	name_label.text = player_name

	# Random spawn position (only the authority sets this)
	if is_multiplayer_authority():
		position = Vector3(randf_range(-30, 30), 0.1, randf_range(-30, 30))


func _physics_process(delta: float) -> void:
	if not is_multiplayer_authority():
		return

	# Gravity
	if not is_on_floor():
		velocity.y += GRAVITY * delta
	else:
		velocity.y = -0.5  # small downward force to stay grounded

	# Skip movement when UI has focus (chat input, buttons, etc.)
	var focused := get_viewport().gui_get_focus_owner()
	if focused == null:
		var dir := Vector3.ZERO
		if Input.is_key_pressed(KEY_W) or Input.is_key_pressed(KEY_UP):
			dir.z -= 1
		if Input.is_key_pressed(KEY_S) or Input.is_key_pressed(KEY_DOWN):
			dir.z += 1
		if Input.is_key_pressed(KEY_A) or Input.is_key_pressed(KEY_LEFT):
			dir.x -= 1
		if Input.is_key_pressed(KEY_D) or Input.is_key_pressed(KEY_RIGHT):
			dir.x += 1
		dir = dir.normalized()
		velocity.x = dir.x * MOVE_SPEED
		velocity.z = dir.z * MOVE_SPEED
	else:
		velocity.x = move_toward(velocity.x, 0.0, MOVE_SPEED)
		velocity.z = move_toward(velocity.z, 0.0, MOVE_SPEED)

	move_and_slide()
