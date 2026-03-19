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
//  7. After connection, peers can exchange chat messages and file transfers via the hub
type SignalingHub struct {
	server      *Server
	upgrader    websocket.Upgrader
	mu          sync.RWMutex
	hosts       map[string]*Peer         // game_id -> host peer
	sessions    map[string]*PunchSession // session_id -> active punch session
	peersByGame map[string][]*Peer       // game_id -> all connected peers (for chat broadcast)
	transfers   map[string]*FileTransfer // transfer_id -> active file transfer
	stopCh      chan struct{}
}

// Peer represents a connected WebSocket client (host or joiner).
type Peer struct {
	conn      *websocket.Conn
	id        string // unique peer ID (hex)
	ip        string
	gameID    string
	isHost    bool
	sendCh    chan []byte
	closeCh   chan struct{}
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

// FileTransfer tracks an in-progress file transfer between two peers.
type FileTransfer struct {
	ID        string
	GameID    string
	Sender    *Peer
	Recipient *Peer
	Filename  string
	Size      int64
	CRC32     string
	Accepted  bool
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
				if len(server.cfg.AllowedOrigins) > 0 {
					origin := r.Header.Get("Origin")
					for _, allowed := range server.cfg.AllowedOrigins {
						if origin == allowed {
							return true
						}
					}
					return false
				}
				return true
			},
		},
		hosts:       make(map[string]*Peer),
		sessions:    make(map[string]*PunchSession),
		peersByGame: make(map[string][]*Peer),
		transfers:   make(map[string]*FileTransfer),
		stopCh:      make(chan struct{}),
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

	conn.SetReadLimit(int64(h.server.cfg.Protection.MaxWSMessage))

	peer := &Peer{
		conn:    conn,
		id:      generateGameID(),
		ip:      ip,
		sendCh:  make(chan []byte, 64), // 64 slots — large enough for burst file chunks
		closeCh: make(chan struct{}),
	}

	go peer.writePump()
	h.readPump(peer)

	h.removePeer(peer)
	h.server.rateLimiter.ReleaseWebSocket(ip)
}

// readPump reads messages from a peer's WebSocket connection.
func (h *SignalingHub) readPump(peer *Peer) {
	defer peer.Close()

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

		peer.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		var msg SignalingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			peer.SendError("invalid_json", "Could not parse message")
			continue
		}

		if msg.Payload == nil {
			msg.Payload = json.RawMessage(message)
		}

		h.handleMessage(peer, &msg)
	}
}

// writePump sends messages to a peer's WebSocket connection.
func (p *Peer) writePump() {
	ticker := time.NewTicker(30 * time.Second)
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
	// NAT signaling
	case "register_host":
		h.handleRegisterHost(peer, msg.Payload)
	case "request_join":
		h.handleRequestJoin(peer, msg.Payload)
	case "ice_candidate":
		h.handleICECandidate(peer, msg.Payload)
	case "connection_established":
		h.handleConnectionEstablished(peer, msg.Payload)
	// Chat
	case "chat_message":
		h.handleChatMessage(peer, msg.Payload)
	// File transfer
	case "file_offer":
		h.handleFileOffer(peer, msg.Payload)
	case "file_accept":
		h.handleFileAccept(peer, msg.Payload)
	case "file_reject":
		h.handleFileReject(peer, msg.Payload)
	case "file_chunk":
		h.handleFileChunk(peer, msg.Payload)
	case "heartbeat":
		peer.Send("pong", nil)
	default:
		peer.SendError("unknown_type", "Unknown message type: "+msg.Type)
	}
}

// --- NAT Signaling Handlers ---

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
	h.peersByGame[p.GameID] = append(h.peersByGame[p.GameID], peer)
	h.mu.Unlock()

	log.Printf("[signaling] host registered: game=%s ip=%s peer=%s", p.GameID, peer.ip, peer.id)
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

	if !h.server.rateLimiter.AllowJoin(peer.ip) {
		peer.SendError("rate_limited", "Too many join requests")
		return
	}

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

	h.mu.RLock()
	host, ok := h.hosts[game.ID]
	h.mu.RUnlock()

	if !ok {
		peer.SendError("host_offline", "Host is not connected")
		return
	}

	peer.gameID = game.ID

	session := &PunchSession{
		ID:        generateGameID(),
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
	SessionID  string `json:"session_id"`
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

	candidateData := map[string]interface{}{
		"session_id":  p.SessionID,
		"public_ip":   p.PublicIP,
		"public_port": p.PublicPort,
		"local_ip":    p.LocalIP,
		"local_port":  p.LocalPort,
		"nat_type":    p.NATType,
	}

	if peer == session.Host {
		session.Joiner.Send("peer_candidate", candidateData)
	} else {
		session.Host.Send("peer_candidate", candidateData)
	}

	session.State = "punching"

	punchData := map[string]interface{}{
		"session_id": p.SessionID,
		"peer_ip":    p.PublicIP,
		"peer_port":  p.PublicPort,
	}

	if peer == session.Host {
		session.Joiner.Send("punch_signal", punchData)
	} else {
		session.Host.Send("punch_signal", punchData)
	}

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
	SessionID string `json:"session_id"`
	Method    string `json:"method"` // "direct", "punched", "relayed"
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

	if !ok || p.Method == "" {
		return
	}

	h.server.store.UpdateConnMethod(session.GameID, p.Method)
	log.Printf("[signaling] connection established: session=%s method=%s peer=%s", p.SessionID, p.Method, peer.id)

	// Add the peer to the game's chat room (only if not already present).
	// The host is added during register_host; joiners are added here.
	peer.gameID = session.GameID
	h.mu.Lock()
	alreadyAdded := false
	for _, existing := range h.peersByGame[session.GameID] {
		if existing == peer {
			alreadyAdded = true
			break
		}
	}
	if !alreadyAdded {
		h.peersByGame[session.GameID] = append(h.peersByGame[session.GameID], peer)
	}
	h.mu.Unlock()

	// Notify the OTHER peer in the session that connection is confirmed.
	// In standard flow the joiner sends this; the host gets notified.
	// The check handles either direction defensively.
	var notifyPeer *Peer
	if peer == session.Host {
		notifyPeer = session.Joiner
	} else {
		notifyPeer = session.Host
	}
	notifyPeer.Send("peer_connected", map[string]interface{}{
		"peer_id": peer.id,
		"method":  p.Method,
	})
}

// --- Chat Handler ---

type chatMessagePayload struct {
	GameID string `json:"game_id"`
	Text   string `json:"text"`
}

func (h *SignalingHub) handleChatMessage(peer *Peer, payload json.RawMessage) {
	var p chatMessagePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid chat_message payload")
		return
	}

	if p.Text == "" {
		return // silently ignore empty messages
	}
	// Truncate excessively long messages
	if len(p.Text) > 2000 {
		p.Text = p.Text[:2000]
	}

	gameID := p.GameID
	if gameID == "" {
		gameID = peer.gameID
	}
	if gameID == "" {
		peer.SendError("not_in_game", "Not associated with a game")
		return
	}

	msg := map[string]interface{}{
		"from": peer.id,
		"text": p.Text,
		"ts":   time.Now().UTC().Format(time.RFC3339),
	}
	h.broadcastToGame(gameID, "chat_message", msg, peer)
}

// --- File Transfer Handlers ---

type fileOfferPayload struct {
	GameID     string `json:"game_id"`
	TransferID string `json:"transfer_id"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	CRC32      string `json:"crc32"`
}

func (h *SignalingHub) handleFileOffer(peer *Peer, payload json.RawMessage) {
	var p fileOfferPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid file_offer payload")
		return
	}

	if p.TransferID == "" || p.Filename == "" {
		peer.SendError("invalid_payload", "transfer_id and filename are required")
		return
	}

	gameID := p.GameID
	if gameID == "" {
		gameID = peer.gameID
	}
	if gameID == "" {
		peer.SendError("not_in_game", "Not associated with a game")
		return
	}

	transfer := &FileTransfer{
		ID:       p.TransferID,
		GameID:   gameID,
		Sender:   peer,
		Filename: p.Filename,
		Size:     p.Size,
		CRC32:    p.CRC32,
	}

	h.mu.Lock()
	if _, exists := h.transfers[p.TransferID]; exists {
		h.mu.Unlock()
		peer.SendError("duplicate_transfer", "Transfer ID already in use")
		return
	}
	h.transfers[p.TransferID] = transfer
	h.mu.Unlock()

	log.Printf("[signaling] file_offer: id=%s name=%s size=%d from=%s", p.TransferID, p.Filename, p.Size, peer.id)

	h.broadcastToGame(gameID, "file_offer", map[string]interface{}{
		"from":        peer.id,
		"transfer_id": p.TransferID,
		"filename":    p.Filename,
		"size":        p.Size,
		"crc32":       p.CRC32,
	}, peer)
}

type fileTransferIDPayload struct {
	TransferID string `json:"transfer_id"`
}

func (h *SignalingHub) handleFileAccept(peer *Peer, payload json.RawMessage) {
	var p fileTransferIDPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid file_accept payload")
		return
	}

	h.mu.Lock()
	transfer, ok := h.transfers[p.TransferID]
	if ok && !transfer.Accepted {
		transfer.Accepted = true
		transfer.Recipient = peer
	} else {
		ok = false
	}
	h.mu.Unlock()

	if !ok {
		peer.SendError("transfer_not_found", "File transfer not found or already accepted")
		return
	}

	log.Printf("[signaling] file_accept: id=%s recipient=%s", p.TransferID, peer.id)
	transfer.Sender.Send("file_accept", map[string]string{"transfer_id": p.TransferID})
}

func (h *SignalingHub) handleFileReject(peer *Peer, payload json.RawMessage) {
	var p fileTransferIDPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid file_reject payload")
		return
	}

	h.mu.Lock()
	transfer, ok := h.transfers[p.TransferID]
	if ok {
		delete(h.transfers, p.TransferID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	transfer.Sender.Send("file_reject", map[string]string{"transfer_id": p.TransferID})
}

type fileChunkPayload struct {
	TransferID string `json:"transfer_id"`
	Index      int    `json:"index"`
	Data       string `json:"data"` // base64-encoded chunk
	IsLast     bool   `json:"is_last"`
}

func (h *SignalingHub) handleFileChunk(peer *Peer, payload json.RawMessage) {
	var p fileChunkPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		peer.SendError("invalid_payload", "Invalid file_chunk payload")
		return
	}

	h.mu.RLock()
	transfer, ok := h.transfers[p.TransferID]
	h.mu.RUnlock()

	if !ok {
		peer.SendError("transfer_not_found", "Transfer not active")
		return
	}
	if transfer.Recipient == nil {
		peer.SendError("transfer_not_accepted", "Transfer not yet accepted by a recipient")
		return
	}
	// Only the original sender may push chunks
	if transfer.Sender != peer {
		peer.SendError("unauthorized", "Only the transfer sender may send chunks")
		return
	}

	// Route chunk to recipient; attach CRC32 on the last chunk for verification
	chunk := map[string]interface{}{
		"transfer_id": p.TransferID,
		"index":       p.Index,
		"data":        p.Data,
		"is_last":     p.IsLast,
	}
	if p.IsLast {
		chunk["crc32"] = transfer.CRC32
	}
	transfer.Recipient.Send("file_chunk", chunk)

	if p.IsLast {
		log.Printf("[signaling] file transfer complete: id=%s last_chunk=%d", p.TransferID, p.Index)
		h.mu.Lock()
		delete(h.transfers, p.TransferID)
		h.mu.Unlock()
	}
}

// --- Peer lifecycle ---

// removePeer cleans up when a peer disconnects.
func (h *SignalingHub) removePeer(peer *Peer) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Remove from hosts map
	if peer.isHost && peer.gameID != "" {
		if existing, ok := h.hosts[peer.gameID]; ok && existing == peer {
			delete(h.hosts, peer.gameID)
		}
	}

	// Remove from peersByGame
	if peer.gameID != "" {
		peers := h.peersByGame[peer.gameID]
		for i, p := range peers {
			if p == peer {
				h.peersByGame[peer.gameID] = append(peers[:i], peers[i+1:]...)
				break
			}
		}
		if len(h.peersByGame[peer.gameID]) == 0 {
			delete(h.peersByGame, peer.gameID)
		}
	}

	// Cancel any file transfers where this peer is the sender
	for id, t := range h.transfers {
		if t.Sender == peer {
			if t.Recipient != nil {
				t.Recipient.Send("file_reject", map[string]string{
					"transfer_id": id,
					"reason":      "sender disconnected",
				})
			}
			delete(h.transfers, id)
		}
	}

	// Clean up signaling sessions involving this peer
	for id, session := range h.sessions {
		if session.Host == peer || session.Joiner == peer {
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

// --- Broadcast helpers ---

// broadcastToGame sends a message to all peers in a game except the excluded peer.
func (h *SignalingHub) broadcastToGame(gameID string, msgType string, payload interface{}, exclude *Peer) {
	h.mu.RLock()
	peers := make([]*Peer, len(h.peersByGame[gameID]))
	copy(peers, h.peersByGame[gameID])
	h.mu.RUnlock()

	for _, p := range peers {
		if p != exclude {
			p.Send(msgType, payload)
		}
	}
}

// --- Peer methods ---

// Send sends a typed message to the peer.
// Safe to call concurrently. Never panics even if the peer has already closed.
func (p *Peer) Send(msgType string, payload interface{}) {
	var payloadBytes json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		payloadBytes = b
	}

	msg := SignalingMessage{Type: msgType, Payload: payloadBytes}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	// Recover from the panic that occurs when sending on a closed channel.
	// This can happen when a peer disconnects exactly as another goroutine
	// tries to notify it (e.g., removePeer notifying the other party).
	defer func() { recover() }() //nolint:errcheck
	select {
	case p.sendCh <- data:
	default:
		log.Printf("[signaling] dropping message to slow peer %s", p.ip)
	}
}

// SendError sends an error message to the peer.
func (p *Peer) SendError(code, message string) {
	msg := SignalingMessage{Type: "error", Error: code + ": " + message}
	data, _ := json.Marshal(msg)
	defer func() { recover() }() //nolint:errcheck
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
