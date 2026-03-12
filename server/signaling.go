package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SignalingHub manages WebSocket connections for NAT traversal signaling.
// It coordinates ICE candidate exchange between hosts and joiners, and
// orchestrates the hole-punching process.
//
// Connection flow:
//  1. Host connects via WebSocket, sends "register_host" with game_id + host_token
//  2. Joiner connects via WebSocket, sends "request_join" with game_id
//  3. Hub validates both peers, then exchanges their ICE candidates
//  4. Hub sends "punch_signal" to both peers simultaneously
//  5. If punch fails (timeout), hub provides TURN credentials as fallback
//  6. Peers confirm connection with "connection_established"
type SignalingHub struct {
	server    *Server
	upgrader  websocket.Upgrader
	mu        sync.RWMutex
	hosts     map[string]*Peer         // game_id -> host peer
	sessions  map[string]*PunchSession // session_id -> active punch session
	stopCh    chan struct{}
}

// Peer represents a connected WebSocket client (host or joiner).
type Peer struct {
	conn     *websocket.Conn
	ip       string
	gameID   string
	isHost   bool
	sendCh   chan []byte
	closeCh  chan struct{}
	closeOnce sync.Once
}

// PunchSession tracks the state of a NAT punch attempt between two peers.
type PunchSession struct {
	ID        string
	GameID    string
	Host      *Peer
	Joiner    *Peer
	State     string // "gathering", "punching", "connected", "relaying", "failed"
	CreatedAt time.Time
}

// SignalingMessage is the JSON envelope for all WebSocket messages.
type SignalingMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// NewSignalingHub creates a new signaling hub.
func NewSignalingHub(server *Server) *SignalingHub {
	return &SignalingHub{
		server: server,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				// If allowed origins are configured, enforce them
				if len(server.cfg.AllowedOrigins) > 0 {
					origin := r.Header.Get("Origin")
					for _, allowed := range server.cfg.AllowedOrigins {
						if origin == allowed {
							return true
						}
					}
					return false
				}
				return true // Allow all origins by default
			},
		},
		hosts:    make(map[string]*Peer),
		sessions: make(map[string]*PunchSession),
		stopCh:   make(chan struct{}),
	}
}

// Stop shuts down all signaling connections.
func (h *SignalingHub) Stop() {
	close(h.stopCh)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, peer := range h.hosts {
		peer.Close()
	}
}

// HandleConnection upgrades an HTTP request to a WebSocket connection
// and begins processing signaling messages.
func (h *SignalingHub) HandleConnection(w http.ResponseWriter, r *http.Request, ip string) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[signaling] upgrade error from %s: %v", ip, err)
		return
	}

	// Set message size limit
	conn.SetReadLimit(int64(h.server.cfg.Protection.MaxWSMessage))

	peer := &Peer{
		conn:    conn,
		ip:      ip,
		sendCh:  make(chan []byte, 16),
		closeCh: make(chan struct{}),
	}

	// Start send goroutine
	go peer.writePump()

	// Process messages (blocking — runs until connection closes)
	h.readPump(peer)

	// Cleanup
	h.removePeer(peer)
	h.server.rateLimiter.ReleaseWebSocket(ip)
}

// readPump reads messages from a peer's WebSocket connection.
func (h *SignalingHub) readPump(peer *Peer) {
	defer peer.Close()

	// Set read deadline — idle connections close after 60s
	peer.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	peer.conn.SetPongHandler(func(string) error {
		peer.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := peer.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[signaling] read error from %s: %v", peer.ip, err)
			}
			return
		}

		// Reset read deadline on any message
		peer.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		var msg SignalingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			peer.SendError("invalid_json", "Could not parse message")
			continue
		}

		// Support both nested payload format {"type":"x","payload":{...}}
		// and flat format {"type":"x","game_id":"y",...}
		// If no explicit payload field was provided, use the entire message as payload
		if msg.Payload == nil {
			msg.Payload = json.RawMessage(message)
		}

		h.handleMessage(peer, &msg)
	}
}

// writePump sends messages to a peer's WebSocket connection.
func (p *Peer) writePump() {
	ticker := time.NewTicker(30 * time.Second) // Ping interval
	defer func() {
		ticker.Stop()
		p.conn.Close()
	}()

	for {
		select {
		case message, ok := <-p.sendCh:
			p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				p.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := p.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := p.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-p.closeCh:
			return
		}
	}
}

// handleMessage routes a signaling message to the appropriate handler.
func (h *SignalingHub) handleMessage(peer *Peer, msg *SignalingMessage) {
	switch msg.Type {
	case "register_host":
		h.handleRegisterHost(peer, msg.Payload)
	case "request_join":
		h.handleRequestJoin(peer, msg.Payload)
	case "ice_candidate":
		h.handleICECandidate(peer, msg.Payload)
	case "connection_established":
		h.handleConnectionEstablished(peer, msg.Payload)
	case "heartbeat":
		peer.Send("pong", nil)
	default:
		peer.SendError("unknown_type", "Unknown message type: "+msg.Type)
	}
}

// --- Message Handlers ---

type registerHostPayload struct {
	GameID    string `json:"game_id"`
	HostToken string `json:"host_token"`
}

func (h *SignalingHub) handleRegisterHost(peer *Peer, payload json.RawMessage) {
	var p registerHostPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid register_host payload")
		return
	}

	// Verify game exists and token matches
	game := h.server.store.Get(p.GameID)
	if game == nil {
		peer.SendError("game_not_found", "Game not found")
		return
	}
	if game.HostToken != p.HostToken {
		peer.SendError("unauthorized", "Invalid host token")
		return
	}

	peer.gameID = p.GameID
	peer.isHost = true

	h.mu.Lock()
	h.hosts[p.GameID] = peer
	h.mu.Unlock()

	log.Printf("[signaling] host registered for game %s from %s", p.GameID, peer.ip)
	peer.Send("host_registered", map[string]string{"game_id": p.GameID})
}

type requestJoinPayload struct {
	GameID   string `json:"game_id"`
	JoinCode string `json:"join_code"`
}

func (h *SignalingHub) handleRequestJoin(peer *Peer, payload json.RawMessage) {
	var p requestJoinPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid request_join payload")
		return
	}

	// Rate limit joins
	if !h.server.rateLimiter.AllowJoin(peer.ip) {
		peer.SendError("rate_limited", "Too many join requests")
		return
	}

	// Resolve game by ID or join code
	var game *Game
	if p.GameID != "" {
		game = h.server.store.Get(p.GameID)
	} else if p.JoinCode != "" {
		game = h.server.store.GetByCode(p.JoinCode)
	}

	if game == nil {
		peer.SendError("game_not_found", "Game not found")
		return
	}

	// Check if host is connected
	h.mu.RLock()
	host, ok := h.hosts[game.ID]
	h.mu.RUnlock()

	if !ok {
		peer.SendError("host_offline", "Host is not connected")
		return
	}

	peer.gameID = game.ID

	// Create a punch session
	session := &PunchSession{
		ID:        generateGameID(), // Reuse for session IDs
		GameID:    game.ID,
		Host:      host,
		Joiner:    peer,
		State:     "gathering",
		CreatedAt: time.Now(),
	}

	h.mu.Lock()
	h.sessions[session.ID] = session
	h.mu.Unlock()

	log.Printf("[signaling] join request: session=%s game=%s joiner=%s", session.ID, game.ID, peer.ip)

	// Notify both peers to begin ICE candidate gathering
	candidateRequest := map[string]interface{}{
		"session_id": session.ID,
		"stun_servers": []string{
			"stun:" + h.server.cfg.TurnHost + ":" + itoa(h.server.cfg.TurnPort),
			"stun:stun.l.google.com:19302",
		},
	}

	host.Send("gather_candidates", candidateRequest)
	peer.Send("gather_candidates", candidateRequest)
}

type iceCandidatePayload struct {
	SessionID string `json:"session_id"`
	PublicIP   string `json:"public_ip"`
	PublicPort int    `json:"public_port"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
	NATType    string `json:"nat_type"`
}

func (h *SignalingHub) handleICECandidate(peer *Peer, payload json.RawMessage) {
	var p iceCandidatePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid ice_candidate payload")
		return
	}

	h.mu.RLock()
	session, ok := h.sessions[p.SessionID]
	h.mu.RUnlock()

	if !ok {
		peer.SendError("session_not_found", "Signaling session not found")
		return
	}

	// Forward candidate to the other peer
	candidateData := map[string]interface{}{
		"session_id": p.SessionID,
		"public_ip":  p.PublicIP,
		"public_port": p.PublicPort,
		"local_ip":   p.LocalIP,
		"local_port": p.LocalPort,
		"nat_type":   p.NATType,
	}

	if peer == session.Host {
		session.Joiner.Send("peer_candidate", candidateData)
	} else {
		session.Host.Send("peer_candidate", candidateData)
	}

	// Check if both peers have submitted candidates
	// After sending candidates to both, signal them to begin punching
	session.State = "punching"

	punchData := map[string]interface{}{
		"session_id": p.SessionID,
		"peer_ip":    p.PublicIP,
		"peer_port":  p.PublicPort,
	}

	// Send punch signal to the OTHER peer (they should punch toward this peer)
	if peer == session.Host {
		session.Joiner.Send("punch_signal", punchData)
	} else {
		session.Host.Send("punch_signal", punchData)
	}

	// Also provide TURN fallback credentials in case punch fails
	creds := h.server.turn.Generate(p.SessionID)
	turnData := map[string]interface{}{
		"session_id":  p.SessionID,
		"turn_server": creds.URIs,
		"username":    creds.Username,
		"password":    creds.Password,
		"ttl":         creds.TTL,
	}

	if peer == session.Host {
		session.Joiner.Send("turn_fallback", turnData)
	} else {
		session.Host.Send("turn_fallback", turnData)
	}
}

type connectionEstablishedPayload struct {
	SessionID  string `json:"session_id"`
	Method     string `json:"method"` // "direct", "punched", "relayed"
}

func (h *SignalingHub) handleConnectionEstablished(peer *Peer, payload json.RawMessage) {
	var p connectionEstablishedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}

	h.mu.Lock()
	session, ok := h.sessions[p.SessionID]
	if ok {
		session.State = "connected"
		delete(h.sessions, p.SessionID)
	}
	h.mu.Unlock()

	if ok && p.Method != "" {
		h.server.store.UpdateConnMethod(session.GameID, p.Method)
		log.Printf("[signaling] connection established: session=%s method=%s", p.SessionID, p.Method)
	}
}

// removePeer cleans up when a peer disconnects.
func (h *SignalingHub) removePeer(peer *Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Remove from hosts map if this was a host
	if peer.isHost && peer.gameID != "" {
		if existing, ok := h.hosts[peer.gameID]; ok && existing == peer {
			delete(h.hosts, peer.gameID)
		}
	}

	// Clean up any active sessions involving this peer
	for id, session := range h.sessions {
		if session.Host == peer || session.Joiner == peer {
			// Notify the other peer
			var other *Peer
			if session.Host == peer {
				other = session.Joiner
			} else {
				other = session.Host
			}
			if other != nil {
				other.Send("peer_disconnected", map[string]string{"session_id": id})
			}
			delete(h.sessions, id)
		}
	}
}

// --- Peer methods ---

// Send sends a typed message to the peer.
func (p *Peer) Send(msgType string, payload interface{}) {
	var payloadBytes json.RawMessage
	if payload != nil {
		var err error
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return
		}
	}

	msg := SignalingMessage{
		Type:    msgType,
		Payload: payloadBytes,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	select {
	case p.sendCh <- data:
	default:
		// Channel full — peer is slow, drop message
		log.Printf("[signaling] dropping message to slow peer %s", p.ip)
	}
}

// SendError sends an error message to the peer.
func (p *Peer) SendError(code, message string) {
	msg := SignalingMessage{
		Type:  "error",
		Error: code + ": " + message,
	}
	data, _ := json.Marshal(msg)
	select {
	case p.sendCh <- data:
	default:
	}
}

// Close closes the peer connection.
func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		close(p.closeCh)
		close(p.sendCh)
	})
}

// itoa is a simple int-to-string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
