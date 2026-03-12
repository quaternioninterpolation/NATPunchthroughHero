package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsConnect dials a test WebSocket server, passing the API key in the query param.
func wsConnect(t *testing.T, ts *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + path
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	return conn
}

func readWSMessage(t *testing.T, conn *websocket.Conn) SignalingMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("WebSocket read failed: %v", err)
	}
	var sm SignalingMessage
	if err := json.Unmarshal(msg, &sm); err != nil {
		t.Fatalf("parse WS message failed: %v; raw: %s", err, string(msg))
	}
	return sm
}

func sendWSMessage(t *testing.T, conn *websocket.Conn, msgType string, payload interface{}) {
	t.Helper()
	var payloadBytes json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		payloadBytes = b
	}
	msg := SignalingMessage{Type: msgType, Payload: payloadBytes}
	data, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WebSocket write failed: %v", err)
	}
}

func signalingTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := testConfig()
	cfg.GameAPIKey = "test-key"
	s := NewServer(cfg)

	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		s.Stop()
	})
	return s, ts
}

// --- WebSocket connection ---

func TestSignaling_WSConnect(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	// Send heartbeat and expect pong
	sendWSMessage(t, conn, "heartbeat", nil)
	msg := readWSMessage(t, conn)
	if msg.Type != "pong" {
		t.Errorf("expected pong, got %q", msg.Type)
	}
}

func TestSignaling_WSConnect_AlternativePath(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws/signaling?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "heartbeat", nil)
	msg := readWSMessage(t, conn)
	if msg.Type != "pong" {
		t.Errorf("expected pong, got %q", msg.Type)
	}
}

func TestSignaling_WSConnect_NoAPIKey(t *testing.T) {
	_, ts := signalingTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected connection to fail without API key")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", resp.StatusCode)
	}
}

// --- register_host ---

func TestSignaling_RegisterHost(t *testing.T) {
	s, ts := signalingTestServer(t)

	// Create a game via API
	game, err := s.store.Register(&Game{Name: "WS Game", MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "register_host", registerHostPayload{
		GameID:    game.ID,
		HostToken: game.HostToken,
	})

	msg := readWSMessage(t, conn)
	if msg.Type != "host_registered" {
		t.Errorf("expected host_registered, got %q (payload: %s)", msg.Type, string(msg.Payload))
	}
}

func TestSignaling_RegisterHost_InvalidToken(t *testing.T) {
	s, ts := signalingTestServer(t)

	game, _ := s.store.Register(&Game{Name: "Secure Game", MaxPlayers: 4})

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "register_host", registerHostPayload{
		GameID:    game.ID,
		HostToken: "wrong-token",
	})

	msg := readWSMessage(t, conn)
	if msg.Type != "error" {
		t.Errorf("expected error, got %q", msg.Type)
	}
	if !strings.Contains(msg.Error, "unauthorized") {
		t.Errorf("error = %q, expected 'unauthorized'", msg.Error)
	}
}

func TestSignaling_RegisterHost_GameNotFound(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "register_host", registerHostPayload{
		GameID:    "nonexistent",
		HostToken: "token",
	})

	msg := readWSMessage(t, conn)
	if msg.Type != "error" {
		t.Errorf("expected error, got %q", msg.Type)
	}
	if !strings.Contains(msg.Error, "game_not_found") {
		t.Errorf("error = %q, expected 'game_not_found'", msg.Error)
	}
}

// --- request_join ---

func TestSignaling_RequestJoin_HostOffline(t *testing.T) {
	s, ts := signalingTestServer(t)

	game, _ := s.store.Register(&Game{Name: "No Host", MaxPlayers: 4})

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "request_join", requestJoinPayload{
		GameID: game.ID,
	})

	msg := readWSMessage(t, conn)
	if msg.Type != "error" {
		t.Errorf("expected error (host_offline), got %q", msg.Type)
	}
}

func TestSignaling_RequestJoin_GameNotFound(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "request_join", requestJoinPayload{
		GameID: "nonexistent",
	})

	msg := readWSMessage(t, conn)
	if msg.Type != "error" {
		t.Errorf("expected error, got %q", msg.Type)
	}
}

// --- Full signaling flow ---

func TestSignaling_FullFlow_RegisterThenJoin(t *testing.T) {
	s, ts := signalingTestServer(t)

	game, _ := s.store.Register(&Game{Name: "Full Flow", MaxPlayers: 4})

	// Host connects and registers
	hostConn := wsConnect(t, ts, "/ws?key=test-key")
	defer hostConn.Close()

	sendWSMessage(t, hostConn, "register_host", registerHostPayload{
		GameID:    game.ID,
		HostToken: game.HostToken,
	})
	hostMsg := readWSMessage(t, hostConn)
	if hostMsg.Type != "host_registered" {
		t.Fatalf("host: expected host_registered, got %q", hostMsg.Type)
	}

	// Joiner connects and requests join
	joinerConn := wsConnect(t, ts, "/ws?key=test-key")
	defer joinerConn.Close()

	sendWSMessage(t, joinerConn, "request_join", requestJoinPayload{
		GameID: game.ID,
	})

	// Both should receive gather_candidates
	joinerGather := readWSMessage(t, joinerConn)
	if joinerGather.Type != "gather_candidates" {
		t.Errorf("joiner: expected gather_candidates, got %q", joinerGather.Type)
	}

	hostGather := readWSMessage(t, hostConn)
	if hostGather.Type != "gather_candidates" {
		t.Errorf("host: expected gather_candidates, got %q", hostGather.Type)
	}

	// Parse session_id from gather payload
	var gatherPayload map[string]interface{}
	json.Unmarshal(joinerGather.Payload, &gatherPayload)
	sessionID, ok := gatherPayload["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatal("missing session_id in gather_candidates")
	}

	// Host sends ICE candidate
	sendWSMessage(t, hostConn, "ice_candidate", iceCandidatePayload{
		SessionID:  sessionID,
		PublicIP:   "1.1.1.1",
		PublicPort: 7777,
		LocalIP:    "192.168.1.1",
		LocalPort:  7777,
		NATType:    "moderate",
	})

	// Joiner should receive: peer_candidate, then punch_signal, then turn_fallback
	peerCandidate := readWSMessage(t, joinerConn)
	if peerCandidate.Type != "peer_candidate" {
		t.Errorf("joiner: expected peer_candidate, got %q", peerCandidate.Type)
	}

	punchSignal := readWSMessage(t, joinerConn)
	if punchSignal.Type != "punch_signal" {
		t.Errorf("joiner: expected punch_signal, got %q", punchSignal.Type)
	}

	turnFallback := readWSMessage(t, joinerConn)
	if turnFallback.Type != "turn_fallback" {
		t.Errorf("joiner: expected turn_fallback, got %q", turnFallback.Type)
	}

	// Joiner sends ICE candidate
	sendWSMessage(t, joinerConn, "ice_candidate", iceCandidatePayload{
		SessionID:  sessionID,
		PublicIP:   "2.2.2.2",
		PublicPort: 8888,
		LocalIP:    "192.168.1.2",
		LocalPort:  8888,
		NATType:    "symmetric",
	})

	// Host should receive peer_candidate, punch_signal, turn_fallback
	hostPeerCand := readWSMessage(t, hostConn)
	if hostPeerCand.Type != "peer_candidate" {
		t.Errorf("host: expected peer_candidate, got %q", hostPeerCand.Type)
	}

	// Confirm connection established
	sendWSMessage(t, hostConn, "connection_established", connectionEstablishedPayload{
		SessionID: sessionID,
		Method:    "punched",
	})

	// Give time for processing
	time.Sleep(50 * time.Millisecond)

	// Verify conn_method was updated
	got := s.store.Get(game.ID)
	if got != nil && got.ConnMethod != "punched" {
		t.Errorf("ConnMethod = %q, want %q", got.ConnMethod, "punched")
	}
}

// --- Unknown message type ---

func TestSignaling_UnknownType(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "totally_unknown", nil)

	msg := readWSMessage(t, conn)
	if msg.Type != "error" {
		t.Errorf("expected error for unknown type, got %q", msg.Type)
	}
}

// --- Flat message format (backward compat) ---

func TestSignaling_FlatMessageFormat(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	// Send flat format message (no payload wrapper)
	flat := `{"type":"heartbeat"}`
	conn.WriteMessage(websocket.TextMessage, []byte(flat))

	msg := readWSMessage(t, conn)
	if msg.Type != "pong" {
		t.Errorf("expected pong from flat format, got %q", msg.Type)
	}
}

// --- ICE candidate for nonexistent session ---

func TestSignaling_ICECandidate_SessionNotFound(t *testing.T) {
	_, ts := signalingTestServer(t)

	conn := wsConnect(t, ts, "/ws?key=test-key")
	defer conn.Close()

	sendWSMessage(t, conn, "ice_candidate", iceCandidatePayload{
		SessionID: "nonexistent-session",
		PublicIP:  "1.1.1.1",
		PublicPort: 7777,
	})

	msg := readWSMessage(t, conn)
	if msg.Type != "error" {
		t.Errorf("expected error (session_not_found), got %q", msg.Type)
	}
}

// --- Peer Send/SendError ---

func TestPeer_Send(t *testing.T) {
	sendCh := make(chan []byte, 16)
	closeCh := make(chan struct{})
	p := &Peer{
		sendCh:  sendCh,
		closeCh: closeCh,
		ip:      "test",
	}

	p.Send("test_type", map[string]string{"key": "value"})

	select {
	case data := <-sendCh:
		var msg SignalingMessage
		json.Unmarshal(data, &msg)
		if msg.Type != "test_type" {
			t.Errorf("type = %q, want %q", msg.Type, "test_type")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for send message")
	}
}

func TestPeer_SendError(t *testing.T) {
	sendCh := make(chan []byte, 16)
	closeCh := make(chan struct{})
	p := &Peer{
		sendCh:  sendCh,
		closeCh: closeCh,
		ip:      "test",
	}

	p.SendError("test_code", "test message")

	select {
	case data := <-sendCh:
		var msg SignalingMessage
		json.Unmarshal(data, &msg)
		if msg.Type != "error" {
			t.Errorf("type = %q, want %q", msg.Type, "error")
		}
		if !strings.Contains(msg.Error, "test_code") {
			t.Errorf("error = %q, should contain 'test_code'", msg.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error message")
	}
}

// --- itoa ---

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{3478, "3478"},
		{100, "100"},
	}
	for _, tt := range tests {
		result := itoa(tt.input)
		if result != tt.expected {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
