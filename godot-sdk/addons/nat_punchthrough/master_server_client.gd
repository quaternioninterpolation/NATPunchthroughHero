class_name MasterServerClient
extends RefCounted

## REST client for the NAT Punchthrough Hero master server.
##
## Handles game registration, listing, heartbeats, and TURN credential
## retrieval. All methods are async and return results via signals or
## awaitable coroutines.
##
## Usage:
## [codeblock]
## var client = MasterServerClient.new("http://localhost:8080", "your-api-key")
## var result = await client.register_game({"name": "My Game", "max_players": 4})
## if result.success:
##     print("Registered! Join code: ", result.join_code)
## [/codeblock]

## The base URL of the NAT Punchthrough Hero server (no trailing slash).
var base_url: String

## API key for authentication. Leave empty if the server has no key configured.
var api_key: String


func _init(server_url: String = "http://localhost:8080", key: String = "") -> void:
	base_url = server_url.rstrip("/")
	api_key = key


# ─── Game Management ───────────────────────────────────────────────────────────

## Register a new game on the master server.
##
## [param info] should be a Dictionary with the following keys:
## - "name" (String, required): Display name for the game session.
## - "max_players" (int, optional): Maximum player count (default: 8).
## - "current_players" (int, optional): Current player count (default: 1).
## - "map" (String, optional): Current map name.
## - "game_version" (String, optional): Game version string.
## - "nat_type" (String, optional): Detected NAT type.
## - "private" (bool, optional): Whether the game is private.
## - "password" (String, optional): Game password. Sent as plaintext; server stores SHA-256 hash.
## - "data" (Dictionary, optional): Arbitrary metadata (max 4KB JSON).
##
## Returns a Dictionary:
## - "success" (bool)
## - "id" (String): Game ID (on success)
## - "join_code" (String): 6-character join code (on success)
## - "host_token" (String): Secret token for heartbeat/delete (on success)
## - "error" (String): Error message (on failure)
func register_game(info: Dictionary) -> Dictionary:
	var body := JSON.stringify(info)
	var result := await _request("POST", "/api/games", body)

	if result.error != "":
		return {"success": false, "error": result.error}

	if result.status != 201:
		var err := _parse_error(result.body)
		return {"success": false, "error": err}

	var parsed := _parse_json(result.body)
	if parsed == null:
		return {"success": false, "error": "Invalid JSON response"}

	return {
		"success": true,
		"id": parsed.get("id", ""),
		"join_code": parsed.get("join_code", ""),
		"host_token": parsed.get("host_token", ""),
	}


## List available games from the server.
##
## [param code]: Filter by join code (exact match). Empty string = no filter.
## [param version]: Filter by game version. Empty string = no filter.
## [param limit]: Maximum number of results (default: 50).
## [param offset]: Pagination offset (default: 0).
##
## Returns an Array of Dictionaries, each containing:
## - "id", "name", "join_code", "max_players", "current_players",
##   "nat_type", "map", "game_version", "data", "created_at"
func list_games(code: String = "", version: String = "", limit: int = 50, offset: int = 0) -> Array:
	var query := "?limit=%d&offset=%d" % [limit, offset]
	if code != "":
		query += "&code=" + code.uri_encode()
	if version != "":
		query += "&version=" + version.uri_encode()

	var result := await _request("GET", "/api/games" + query)

	if result.error != "" or result.status != 200:
		return []

	var parsed = _parse_json(result.body)
	if parsed is Array:
		return parsed
	if parsed is Dictionary and parsed.has("games"):
		return parsed["games"]
	return []


## Get details of a specific game by ID.
##
## Returns a Dictionary with game info, or an empty Dictionary on failure.
func get_game(game_id: String) -> Dictionary:
	var result := await _request("GET", "/api/games/" + game_id.uri_encode())

	if result.error != "" or result.status != 200:
		return {}

	var parsed := _parse_json(result.body)
	return parsed if parsed is Dictionary else {}


## Send a heartbeat to keep a game session alive.
##
## Must be called before the server's game TTL expires (default: 90 seconds).
## [param game_id]: The game ID returned from register_game().
## [param host_token]: The host token returned from register_game().
##
## Returns true if the heartbeat was accepted.
func send_heartbeat(game_id: String, host_token: String) -> bool:
	var result := await _request(
		"POST",
		"/api/games/" + game_id.uri_encode() + "/heartbeat",
		"",
		{"Authorization": "Bearer " + host_token}
	)
	return result.status == 200


## Remove a game from the master server.
##
## [param game_id]: The game ID to remove.
## [param host_token]: The host token for authorization.
##
## Returns true if the game was deleted.
func deregister_game(game_id: String, host_token: String) -> bool:
	var result := await _request(
		"DELETE",
		"/api/games/" + game_id.uri_encode(),
		"",
		{"Authorization": "Bearer " + host_token}
	)
	return result.status == 200


# ─── TURN Credentials ─────────────────────────────────────────────────────────

## Get time-limited TURN relay credentials for a game session.
##
## [param game_id]: The game ID to get credentials for.
##
## Returns a Dictionary:
## - "username" (String): TURN username
## - "password" (String): TURN password
## - "ttl" (int): Time-to-live in seconds
## - "uris" (Array[String]): TURN server URIs
##
## Returns an empty Dictionary on failure.
func get_turn_credentials(game_id: String) -> Dictionary:
	var result := await _request("GET", "/api/games/" + game_id.uri_encode() + "/turn")

	if result.error != "" or result.status != 200:
		return {}

	var parsed := _parse_json(result.body)
	return parsed if parsed is Dictionary else {}


# ─── Health ────────────────────────────────────────────────────────────────────

## Check if the master server is healthy and reachable.
##
## Returns a Dictionary with server info on success, or empty Dict on failure:
## - "status" (String): "ok"
## - "version" (String): Server version
## - "uptime" (String): Server uptime
## - "active_games" (int): Number of active games
func check_health() -> Dictionary:
	var result := await _request("GET", "/api/health")

	if result.error != "" or result.status != 200:
		return {}

	var parsed := _parse_json(result.body)
	return parsed if parsed is Dictionary else {}


# ─── Internal HTTP ─────────────────────────────────────────────────────────────

## Perform an HTTP request and return the result.
##
## Returns a Dictionary with "status" (int), "body" (String), "error" (String).
func _request(method: String, path: String, body: String = "", extra_headers: Dictionary = {}) -> Dictionary:
	var http := HTTPRequest.new()

	# We need to add it to the scene tree temporarily
	var tree := Engine.get_main_loop() as SceneTree
	if tree == null or tree.root == null:
		return {"status": 0, "body": "", "error": "No scene tree available"}

	tree.root.add_child(http)
	http.timeout = 10.0

	var headers: PackedStringArray = ["Content-Type: application/json"]
	if api_key != "":
		headers.append("X-API-Key: " + api_key)
	for key in extra_headers:
		headers.append(key + ": " + extra_headers[key])

	var http_method: int
	match method:
		"GET":    http_method = HTTPClient.METHOD_GET
		"POST":   http_method = HTTPClient.METHOD_POST
		"DELETE": http_method = HTTPClient.METHOD_DELETE
		"PUT":    http_method = HTTPClient.METHOD_PUT
		_:        http_method = HTTPClient.METHOD_GET

	var url := base_url + path
	var err := http.request(url, headers, http_method, body)

	if err != OK:
		http.queue_free()
		return {"status": 0, "body": "", "error": "HTTP request failed: " + str(err)}

	var response: Array = await http.request_completed
	http.queue_free()

	# response = [result, response_code, headers, body]
	var result_code: int = response[0]
	var status_code: int = response[1]
	var response_body: String = response[3].get_string_from_utf8()

	if result_code != HTTPRequest.RESULT_SUCCESS:
		return {"status": 0, "body": "", "error": "HTTP error: " + str(result_code)}

	return {"status": status_code, "body": response_body, "error": ""}


func _parse_json(text: String):
	var json := JSON.new()
	var err := json.parse(text)
	if err != OK:
		return null
	return json.data


func _parse_error(body: String) -> String:
	var parsed = _parse_json(body)
	if parsed is Dictionary:
		if parsed.has("message"):
			return str(parsed["message"])
		if parsed.has("error"):
			return str(parsed["error"])
	return "Request failed"
