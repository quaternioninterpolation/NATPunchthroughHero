package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testConfig returns a Config suitable for offline unit tests.
func testConfig() *Config {
	cfg := DefaultConfig()
	cfg.Port = 0
	cfg.ExternalIP = "127.0.0.1"
	cfg.TurnSecret = "test-turn-secret"
	cfg.TurnHost = "127.0.0.1"
	cfg.TurnPort = 3478
	cfg.AdminPassword = "admin"
	cfg.GameAPIKey = "test-api-key"
	cfg.MaxGames = 100
	cfg.GameTimeout = 300
	cfg.DashboardAccess = "local"
	cfg.RateLimit.Enabled = false // Disable rate limiting for clean tests
	cfg.Protection.Enabled = false
	cfg.IPFilter.Mode = "off"
	return cfg
}

func newTestServer() *Server {
	return NewServer(testConfig())
}

// jsonBody encodes v to JSON bytes for use as request body.
func jsonBody(v interface{}) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// doRequest runs a request against the server handler with common setup.
func doRequest(t *testing.T, s *Server, method, path string, body interface{}, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "1.2.3.4:12345"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func apiHeaders() map[string]string {
	return map[string]string{"X-API-Key": "test-api-key"}
}

func adminHeaders() map[string]string {
	return map[string]string{"Authorization": "Basic OmFkbWlu"} // :admin in base64
}

// --- Health ---

func TestAPI_Health(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/health", nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/health: got %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want %q", resp["status"], "ok")
	}
	if resp["version"] != "dev" {
		t.Errorf("version = %v, want %q", resp["version"], "dev")
	}
}

// --- Create Game ---

func TestAPI_CreateGame_Success(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	body := CreateGameRequest{
		Name:       "Test Game",
		MaxPlayers: 4,
	}

	w := doRequest(t, s, "POST", "/api/games", body, apiHeaders())
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/games: got %d, want 201; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["id"] == nil || resp["id"] == "" {
		t.Error("expected non-empty id")
	}
	if resp["join_code"] == nil || resp["join_code"] == "" {
		t.Error("expected non-empty join_code")
	}
	if resp["host_token"] == nil || resp["host_token"] == "" {
		t.Error("expected non-empty host_token")
	}
}

func TestAPI_CreateGame_MissingName(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	body := CreateGameRequest{MaxPlayers: 4}
	w := doRequest(t, s, "POST", "/api/games", body, apiHeaders())
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestAPI_CreateGame_NoAPIKey(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	body := CreateGameRequest{Name: "Test", MaxPlayers: 4}
	w := doRequest(t, s, "POST", "/api/games", body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestAPI_CreateGame_WrongAPIKey(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	body := CreateGameRequest{Name: "Test", MaxPlayers: 4}
	w := doRequest(t, s, "POST", "/api/games", body, map[string]string{"X-API-Key": "wrong"})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestAPI_CreateGame_WithData(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	raw := json.RawMessage(`{"map":"forest"}`)
	body := CreateGameRequest{Name: "Data Game", MaxPlayers: 4, Data: raw}
	w := doRequest(t, s, "POST", "/api/games", body, apiHeaders())
	if w.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201", w.Code)
	}
}

func TestAPI_CreateGame_MaxPlayersClamped(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	body := CreateGameRequest{Name: "Big Game", MaxPlayers: 200}
	w := doRequest(t, s, "POST", "/api/games", body, apiHeaders())
	if w.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201", w.Code)
	}

	// Verify it was clamped to 64
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	id := resp["id"].(string)

	w2 := doRequest(t, s, "GET", "/api/games/"+id, nil, apiHeaders())
	var game map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &game)
	if game["max_players"].(float64) != 64 {
		t.Errorf("max_players = %v, want 64 (clamped)", game["max_players"])
	}
}

// --- List Games ---

func TestAPI_ListGames_Empty(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/games", nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var games []interface{}
	json.Unmarshal(w.Body.Bytes(), &games)
	if len(games) != 0 {
		t.Errorf("expected empty list, got %d", len(games))
	}
}

func TestAPI_ListGames_ReturnsArray(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	// Create a game
	doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "G1", MaxPlayers: 4}, apiHeaders())

	w := doRequest(t, s, "GET", "/api/games", nil, apiHeaders())

	var games []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &games); err != nil {
		t.Fatalf("response is not a JSON array: %v; body: %s", err, w.Body.String())
	}
	if len(games) != 1 {
		t.Errorf("expected 1 game, got %d", len(games))
	}
}

func TestAPI_ListGames_ByJoinCode(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	// Create a game and get its join code
	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Coded", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	code := resp["join_code"].(string)

	w := doRequest(t, s, "GET", "/api/games?code="+code, nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var games []interface{}
	json.Unmarshal(w.Body.Bytes(), &games)
	if len(games) != 1 {
		t.Errorf("expected 1 game for code %s, got %d", code, len(games))
	}
}

func TestAPI_ListGames_ByVersion(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "V1", MaxPlayers: 4, GameVersion: "1.0"}, apiHeaders())
	doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "V2", MaxPlayers: 4, GameVersion: "2.0"}, apiHeaders())

	w := doRequest(t, s, "GET", "/api/games?version=1.0", nil, apiHeaders())
	var games []interface{}
	json.Unmarshal(w.Body.Bytes(), &games)
	if len(games) != 1 {
		t.Errorf("expected 1 game for version 1.0, got %d", len(games))
	}
}

func TestAPI_ListGames_ExcludesPrivate(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Public", MaxPlayers: 4}, apiHeaders())
	doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Private", MaxPlayers: 4, Private: true}, apiHeaders())

	w := doRequest(t, s, "GET", "/api/games", nil, apiHeaders())
	var games []interface{}
	json.Unmarshal(w.Body.Bytes(), &games)
	if len(games) != 1 {
		t.Errorf("expected 1 public game, got %d", len(games))
	}
}

// --- Get Game ---

func TestAPI_GetGame_Found(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Find Me", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)

	w := doRequest(t, s, "GET", "/api/games/"+id, nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var game map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &game)
	if game["name"] != "Find Me" {
		t.Errorf("name = %v, want %q", game["name"], "Find Me")
	}

	// Should NOT contain sensitive fields
	if game["host_token"] != nil {
		t.Error("response should not contain host_token")
	}
	if game["host_ip"] != nil {
		t.Error("response should not contain host_ip")
	}
	if game["owner_ip"] != nil {
		t.Error("response should not contain owner_ip")
	}
}

func TestAPI_GetGame_ByJoinCode(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "By Code", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	code := resp["join_code"].(string)

	w := doRequest(t, s, "GET", "/api/games/"+code, nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 when looking up by join code", w.Code)
	}
}

func TestAPI_GetGame_NotFound(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/games/nonexistent", nil, apiHeaders())
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

// --- Delete Game ---

func TestAPI_DeleteGame_WithBearerToken(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Delete Me", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)
	token := resp["host_token"].(string)

	headers := apiHeaders()
	headers["Authorization"] = "Bearer " + token

	w := doRequest(t, s, "DELETE", "/api/games/"+id, nil, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Verify it's gone
	w2 := doRequest(t, s, "GET", "/api/games/"+id, nil, apiHeaders())
	if w2.Code != http.StatusNotFound {
		t.Errorf("game should be gone, got %d", w2.Code)
	}
}

func TestAPI_DeleteGame_WithXHostToken(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Delete Me", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)
	token := resp["host_token"].(string)

	headers := apiHeaders()
	headers["X-Host-Token"] = token

	w := doRequest(t, s, "DELETE", "/api/games/"+id, nil, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 with X-Host-Token", w.Code)
	}
}

func TestAPI_DeleteGame_WrongToken(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Protected", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)

	headers := apiHeaders()
	headers["Authorization"] = "Bearer wrong-token"

	w := doRequest(t, s, "DELETE", "/api/games/"+id, nil, headers)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 for wrong token", w.Code)
	}
}

func TestAPI_DeleteGame_NoToken(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "No Token", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)

	w := doRequest(t, s, "DELETE", "/api/games/"+id, nil, apiHeaders())
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401 for missing token", w.Code)
	}
}

// --- Heartbeat ---

func TestAPI_Heartbeat_Success(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "Heartbeat", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)
	token := resp["host_token"].(string)

	headers := apiHeaders()
	headers["Authorization"] = "Bearer " + token

	w := doRequest(t, s, "POST", "/api/games/"+id+"/heartbeat", nil, headers)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

func TestAPI_Heartbeat_NotFound(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	headers := apiHeaders()
	headers["Authorization"] = "Bearer some-token"

	w := doRequest(t, s, "POST", "/api/games/nonexistent/heartbeat", nil, headers)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

// --- TURN Credentials ---

func TestAPI_TURNCredentials_POST(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "POST", "/api/turn-credentials", nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/turn-credentials: got %d, want 200", w.Code)
	}

	var creds TURNCredentials
	json.Unmarshal(w.Body.Bytes(), &creds)
	if creds.Username == "" {
		t.Error("expected non-empty username")
	}
	if creds.Password == "" {
		t.Error("expected non-empty password")
	}
	if len(creds.URIs) != 2 {
		t.Errorf("expected 2 URIs, got %d", len(creds.URIs))
	}
}

func TestAPI_TURNCredentials_PerGame(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	// Create a game first
	w1 := doRequest(t, s, "POST", "/api/games", CreateGameRequest{Name: "TURN Game", MaxPlayers: 4}, apiHeaders())
	var resp map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &resp)
	id := resp["id"].(string)

	w := doRequest(t, s, "GET", "/api/games/"+id+"/turn", nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/games/{id}/turn: got %d, want 200", w.Code)
	}

	var creds TURNCredentials
	json.Unmarshal(w.Body.Bytes(), &creds)
	if creds.Username == "" {
		t.Error("expected non-empty username")
	}
}

func TestAPI_TURNCredentials_GameNotFound(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/games/nonexistent/turn", nil, apiHeaders())
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 for nonexistent game", w.Code)
	}
}

// --- CORS ---

func TestAPI_CORS_Options(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "OPTIONS", "/api/games", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS: got %d, want 204", w.Code)
	}

	acao := w.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Errorf("ACAO = %q, want %q", acao, "*")
	}

	acam := w.Header().Get("Access-Control-Allow-Methods")
	if acam == "" {
		t.Error("missing Access-Control-Allow-Methods")
	}

	acah := w.Header().Get("Access-Control-Allow-Headers")
	if acah == "" {
		t.Error("missing Access-Control-Allow-Headers")
	}
}

func TestAPI_CORS_ResponseHeaders(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/health", nil, apiHeaders())
	acao := w.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Errorf("response ACAO = %q, want %q", acao, "*")
	}
}

// --- Security Headers ---

func TestAPI_SecurityHeaders(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/health", nil, apiHeaders())

	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options: DENY")
	}
	if w.Header().Get("X-XSS-Protection") != "1; mode=block" {
		t.Error("missing X-XSS-Protection header")
	}
}

// --- Content-Type ---

func TestAPI_ContentType_JSON(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/health", nil, apiHeaders())
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// --- Root ---

func TestAPI_Root(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["name"] != "NAT Punchthrough Hero" {
		t.Errorf("name = %v", resp["name"])
	}
}

func TestAPI_404(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/nonexistent/path", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

// --- Admin API ---

func TestAPI_Admin_Stats(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/admin/api/stats", nil, adminHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["store"] == nil {
		t.Error("missing store stats")
	}
	if resp["protection"] == nil {
		t.Error("missing protection stats")
	}
}

func TestAPI_Admin_NoAuth(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/admin/api/stats", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401 without admin auth", w.Code)
	}
}

func TestAPI_Admin_WrongPassword(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/admin/api/stats", nil, map[string]string{
		"Authorization": "Basic Ondyb25n", // :wrong
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401 with wrong admin password", w.Code)
	}
}

func TestAPI_Admin_Blocklist_AddAndGet(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	// Add IP
	w := doRequest(t, s, "POST", "/admin/api/blocklist", map[string]string{"ip": "6.6.6.6"}, adminHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("add blocklist: got %d, want 200", w.Code)
	}

	// Get blocklist
	w2 := doRequest(t, s, "GET", "/admin/api/blocklist", nil, adminHeaders())
	if w2.Code != http.StatusOK {
		t.Fatalf("get blocklist: got %d, want 200", w2.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp["blocklist"] == nil {
		t.Error("missing blocklist in response")
	}
}

func TestAPI_Admin_Blocklist_Remove(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	doRequest(t, s, "POST", "/admin/api/blocklist", map[string]string{"ip": "7.7.7.7"}, adminHeaders())

	w := doRequest(t, s, "DELETE", "/admin/api/blocklist/7.7.7.7", nil, adminHeaders())
	if w.Code != http.StatusOK {
		t.Errorf("remove blocklist: got %d, want 200", w.Code)
	}
}

func TestAPI_Admin_GetBlocked(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "GET", "/admin/api/blocked", nil, adminHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("get blocked: got %d, want 200", w.Code)
	}
}

func TestAPI_Admin_Unblock(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	w := doRequest(t, s, "POST", "/admin/api/unblock/1.2.3.4", nil, adminHeaders())
	if w.Code != http.StatusOK {
		t.Errorf("unblock: got %d, want 200", w.Code)
	}
}

// --- extractHostToken ---

func TestExtractHostToken_BearerHeader(t *testing.T) {
	req := httptest.NewRequest("DELETE", "/api/games/test", nil)
	req.Header.Set("Authorization", "Bearer mytoken123")

	token := extractHostToken(req)
	if token != "mytoken123" {
		t.Errorf("token = %q, want %q", token, "mytoken123")
	}
}

func TestExtractHostToken_XHostTokenHeader(t *testing.T) {
	req := httptest.NewRequest("DELETE", "/api/games/test", nil)
	req.Header.Set("X-Host-Token", "mytoken456")

	token := extractHostToken(req)
	if token != "mytoken456" {
		t.Errorf("token = %q, want %q", token, "mytoken456")
	}
}

func TestExtractHostToken_BearerPreferred(t *testing.T) {
	req := httptest.NewRequest("DELETE", "/api/games/test", nil)
	req.Header.Set("Authorization", "Bearer bearer-token")
	req.Header.Set("X-Host-Token", "xhost-token")

	token := extractHostToken(req)
	if token != "bearer-token" {
		t.Errorf("token = %q, want %q (Bearer should take priority)", token, "bearer-token")
	}
}

func TestExtractHostToken_Neither(t *testing.T) {
	req := httptest.NewRequest("DELETE", "/api/games/test", nil)

	token := extractHostToken(req)
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

// --- queryInt ---

func TestQueryInt_Present(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?limit=25", nil)
	v := queryInt(req, "limit", 50)
	if v != 25 {
		t.Errorf("got %d, want 25", v)
	}
}

func TestQueryInt_Missing(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	v := queryInt(req, "limit", 50)
	if v != 50 {
		t.Errorf("got %d, want 50 (default)", v)
	}
}

func TestQueryInt_Invalid(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?limit=abc", nil)
	v := queryInt(req, "limit", 50)
	if v != 50 {
		t.Errorf("got %d, want 50 (default for invalid)", v)
	}
}

// --- Full flow: create → list → heartbeat → delete ---

func TestAPI_FullGameLifecycle(t *testing.T) {
	s := newTestServer()
	defer s.Stop()

	// Create
	w := doRequest(t, s, "POST", "/api/games", CreateGameRequest{
		Name:       "Lifecycle",
		MaxPlayers: 8,
		GameVersion: "1.0",
	}, apiHeaders())
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	var create map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &create)
	id := create["id"].(string)
	token := create["host_token"].(string)

	// List - should include the game
	w = doRequest(t, s, "GET", "/api/games", nil, apiHeaders())
	var games []interface{}
	json.Unmarshal(w.Body.Bytes(), &games)
	if len(games) != 1 {
		t.Errorf("list: expected 1, got %d", len(games))
	}

	// Get
	w = doRequest(t, s, "GET", "/api/games/"+id, nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Errorf("get: %d", w.Code)
	}

	// Heartbeat
	headers := apiHeaders()
	headers["Authorization"] = "Bearer " + token
	w = doRequest(t, s, "POST", "/api/games/"+id+"/heartbeat", nil, headers)
	if w.Code != http.StatusOK {
		t.Errorf("heartbeat: %d", w.Code)
	}

	// TURN credentials for game
	w = doRequest(t, s, "GET", "/api/games/"+id+"/turn", nil, apiHeaders())
	if w.Code != http.StatusOK {
		t.Errorf("turn: %d", w.Code)
	}

	// Delete
	w = doRequest(t, s, "DELETE", "/api/games/"+id, nil, headers)
	if w.Code != http.StatusOK {
		t.Errorf("delete: %d", w.Code)
	}

	// Verify gone
	w = doRequest(t, s, "GET", "/api/games/"+id, nil, apiHeaders())
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete: expected 404, got %d", w.Code)
	}
}

// --- requireAPIKey with empty key (open access) ---

func TestAPI_NoAPIKeyConfigured_OpenAccess(t *testing.T) {
	cfg := testConfig()
	cfg.GameAPIKey = "" // No key configured
	s := NewServer(cfg)
	defer s.Stop()

	w := doRequest(t, s, "GET", "/api/games", nil, nil) // No API key header
	if w.Code != http.StatusOK {
		t.Errorf("open access: got %d, want 200", w.Code)
	}
}
