@tool
extends EditorPlugin

## Editor plugin that registers the NAT Punchthrough nodes in the Godot editor.

func _enter_tree() -> void:
	add_custom_type(
		"NATClient",
		"Node",
		preload("nat_client.gd"),
		preload("icon.svg") if ResourceLoader.exists("res://addons/nat_punchthrough/icon.svg") else null
	)


func _exit_tree() -> void:
	remove_custom_type("NATClient")
