package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Server holds all dependencies for the HTTP/WebSocket server.
type Server struct {
	cfg        *Config
	store      *GameStore
	turn       *TURNGenerator
	rateLimiter *RateLimiter
	ipFilter   *IPFilter
	protection *Protection
	signaling  *SignalingHub
	startTime  time.Time
}

// NewServer creates a new server instance with all subsystems.
func NewServer(cfg *Config) *Server {
	store := NewGameStore(cfg.MaxGames, cfg.GameTimeout)
	ipFilter := NewIPFilter(cfg.IPFilter)
	rateLimiter := NewRateLimiter(cfg.RateLimit)
	protection := NewProtection(cfg.Protection, ipFilter)

	// Wire rate limiter violations to protection
	rateLimiter.OnViolation = protection.RecordViolation

	// Initialize trusted proxies
	InitTrustedProxies(cfg.TrustedProxies)

	s := &Server{
		cfg:         cfg,
		store:       store,
		turn:        NewTURNGenerator(cfg),
		rateLimiter: rateLimiter,
		ipFilter:    ipFilter,
		protection:  protection,
		startTime:   time.Now(),
	}

	s.signaling = NewSignalingHub(s)

	return s
}

// Stop gracefully shuts down all subsystems.
func (s *Server) Stop() {
	s.store.Stop()
	s.rateLimiter.Stop()
	s.protection.Stop()
	s.signaling.Stop()
}

// Handler returns the root HTTP handler with all middleware and routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public API (requires game API key)
	mux.HandleFunc("POST /api/games", s.handleCreateGame)
	mux.HandleFunc("GET /api/games", s.handleListGames)
	mux.HandleFunc("GET /api/games/{id}", s.handleGetGame)
	mux.HandleFunc("DELETE /api/games/{id}", s.handleDeleteGame)
	mux.HandleFunc("POST /api/games/{id}/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("GET /api/games/{id}/turn", s.handleTURNCredentials)
	mux.HandleFunc("POST /api/turn-credentials", s.handleTURNCredentials)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// CORS preflight handler
	mux.HandleFunc("OPTIONS /", s.handleOptions)

	// WebSocket signaling (both paths for compatibility)
	mux.HandleFunc("GET /ws", s.handleWebSocket)
	mux.HandleFunc("GET /ws/signaling", s.handleWebSocket)

	// Admin API (requires admin password)
	mux.HandleFunc("GET /admin/api/stats", s.adminAuth(s.handleAdminStats))
	mux.HandleFunc("GET /admin/api/blocklist", s.adminAuth(s.handleAdminGetBlocklist))
	mux.HandleFunc("POST /admin/api/blocklist", s.adminAuth(s.handleAdminAddBlocklist))
	mux.HandleFunc("DELETE /admin/api/blocklist/{ip}", s.adminAuth(s.handleAdminRemoveBlocklist))
	mux.HandleFunc("POST /admin/api/reload", s.adminAuth(s.handleAdminReload))
	mux.HandleFunc("GET /admin/api/blocked", s.adminAuth(s.handleAdminGetBlocked))
	mux.HandleFunc("POST /admin/api/unblock/{ip}", s.adminAuth(s.handleAdminUnblock))

	// Dashboard (static files)
	mux.HandleFunc("GET /admin", s.adminAuth(s.handleDashboard))
	mux.HandleFunc("GET /admin/", s.adminAuth(s.handleDashboard))


	// Root redirect
	mux.HandleFunc("GET /", s.handleRoot)

	// Apply middleware chain: IP Filter → Rate Limiter → Request Size Limit → Handler
	var handler http.Handler = mux
	handler = s.requestSizeMiddleware(handler)
	handler = s.rateLimiter.Middleware(handler)
	handler = s.ipFilter.Middleware(handler)
	handler = s.protectionMiddleware(handler)
	handler = s.securityHeaders(handler)

	return handler
}

// --- API Key Authentication ---

// requireAPIKey validates the X-API-Key header for game client requests.
// Also allows requests that carry valid admin Basic Auth credentials,
// so the admin dashboard can call game endpoints without a separate key.
func (s *Server) requireAPIKey(r *http.Request) bool {
	if s.cfg.GameAPIKey == "" {
		return true // No key configured = open access
	}
	key := r.Header.Get("X-API-Key")
	if key == s.cfg.GameAPIKey {
		return true
	}
	// Allow admin-authenticated requests (dashboard)
	if s.cfg.AdminPassword != "" {
		if _, pass, ok := r.BasicAuth(); ok && pass == s.cfg.AdminPassword {
			return true
		}
	}
	return false
}

// extractHostToken returns the host token from the request.
// Accepts both "Authorization: Bearer <token>" and "X-Host-Token: <token>".
func extractHostToken(r *http.Request) string {
	// Check Authorization: Bearer first (preferred)
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
			return auth[len(prefix):]
		}
	}
	// Fallback to X-Host-Token header
	return r.Header.Get("X-Host-Token")
}

// --- Admin Authentication ---

// adminAuth wraps a handler with HTTP Basic Auth for admin endpoints.
func (s *Server) adminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != s.cfg.AdminPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}
}

// --- Middleware ---

// protectionMiddleware checks flood detection before processing requests.
func (s *Server) protectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !s.protection.RecordConnection(ip) {
			http.Error(w, `{"error":"blocked","message":"Too many connections"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestSizeMiddleware limits request body size.
func (s *Server) requestSizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && s.cfg.Protection.MaxRequestBody > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.Protection.MaxRequestBody))
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds hardening headers to all responses.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Del("Server")
		next.ServeHTTP(w, r)
	})
}

// --- Game API Handlers ---

// CreateGameRequest is the JSON body for game registration.
type CreateGameRequest struct {
	Name           string          `json:"name"`
	Map            string          `json:"map"`
	GameVersion    string          `json:"game_version"`
	MaxPlayers     int             `json:"max_players"`
	CurrentPlayers int             `json:"current_players"`
	Private        bool            `json:"private"`
	Password       string          `json:"password"` // Optional; plaintext in request, stored as SHA-256 hash
	HostPort       int             `json:"host_port"`
	LocalIP        string          `json:"local_ip"`
	LocalPort      int             `json:"local_port"`
	NATType        string          `json:"nat_type"`
	Data           json.RawMessage `json:"data,omitempty"`
}

func (s *Server) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}

	ip := extractIP(r)
	if !s.rateLimiter.AllowGameRegistration(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	var req CreateGameRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json", "message": err.Error()})
		return
	}

	// Validate and sanitize
	req.Name = sanitizeString(req.Name, 100)
	req.Map = sanitizeString(req.Map, 100)
	req.GameVersion = sanitizeString(req.GameVersion, 50)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name_required"})
		return
	}
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = 8
	}
	if req.MaxPlayers > 64 {
		req.MaxPlayers = 64
	}

	// Validate data size (max 4KB)
	if len(req.Data) > 4096 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data_too_large", "message": "data field max 4KB"})
		return
	}

	curPlayers := req.CurrentPlayers
	if curPlayers <= 0 {
		curPlayers = 1
	}

	// Hash password if provided (max 128 chars)
	var passwordHash string
	if req.Password != "" {
		if len(req.Password) > 128 {
			req.Password = req.Password[:128]
		}
		passwordHash = HashPassword(req.Password)
	}

	game := &Game{
		Name:        req.Name,
		Map:         req.Map,
		GameVersion: req.GameVersion,
		MaxPlayers:  req.MaxPlayers,
		Private:     req.Private,
		Password:    passwordHash,
		HostIP:      ip,
		HostPort:    req.HostPort,
		LocalIP:     req.LocalIP,
		LocalPort:   req.LocalPort,
		NATType:     req.NATType,
		CurPlayers:  curPlayers,
		Data:        req.Data,
		OwnerIP:     ip,
	}

	game, err := s.store.Register(game)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server_full", "message": err.Error()})
		return
	}

	log.Printf("[api] game registered: id=%s name=%q host=%s code=%s", game.ID, game.Name, ip, game.JoinCode)

	// Return game info INCLUDING the host token (only the host sees this)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         game.ID,
		"join_code":  game.JoinCode,
		"host_token": game.HostToken,
	})
}

func (s *Server) handleListGames(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}

	// Filter by join code if provided
	if code := r.URL.Query().Get("code"); code != "" {
		game := s.store.GetByCode(code)
		if game == nil {
			writeJSON(w, http.StatusOK, []GamePublic{})
			return
		}
		writeJSON(w, http.StatusOK, []GamePublic{game.ToPublic()})
		return
	}

	version := r.URL.Query().Get("version")
	limit := queryInt(r, "limit", 50)
	offset := queryInt(r, "offset", 0)

	games := s.store.List(version, limit, offset)
	if games == nil {
		games = []GamePublic{} // Return empty array, not null
	}

	writeJSON(w, http.StatusOK, games)
}

func (s *Server) handleGetGame(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}

	id := r.PathValue("id")
	game := s.store.Get(id)
	if game == nil {
		// Try as join code
		game = s.store.GetByCode(id)
	}
	if game == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}

	writeJSON(w, http.StatusOK, game.ToPublic())
}

func (s *Server) handleDeleteGame(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}

	id := r.PathValue("id")
	token := extractHostToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "host_token_required"})
		return
	}

	if s.store.RemoveWithToken(id, token) {
		log.Printf("[api] game removed: id=%s", id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found_or_unauthorized"})
	}
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}

	id := r.PathValue("id")
	token := extractHostToken(r)

	if s.store.Heartbeat(id, token) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	}
}

func (s *Server) handleTURNCredentials(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}

	ip := extractIP(r)
	if !s.rateLimiter.AllowTURN(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	// Use game ID as credential identifier if available, otherwise use IP
	identifier := ip
	if id := r.PathValue("id"); id != "" {
		// Verify the game exists
		if s.store.Get(id) == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "game_not_found"})
			return
		}
		identifier = id
	}

	creds := s.turn.Generate(identifier)
	writeJSON(w, http.StatusOK, creds)
}

// handleOptions responds to CORS preflight requests.
func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Host-Token, Authorization")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := s.store.Stats()
	protStats := s.protection.Stats()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "ok",
		"uptime":       time.Since(s.startTime).String(),
		"active_games": stats.ActiveGames,
		"total_players": stats.TotalPlayers,
		"blocked_ips":  protStats.CurrentlyBlocked,
		"version":      Version,
	})
}

// --- Admin API Handlers ---

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	storeStats := s.store.Stats()
	protStats := s.protection.Stats()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"store":      storeStats,
		"protection": protStats,
		"uptime":     time.Since(s.startTime).String(),
		"config": map[string]interface{}{
			"max_games":    s.cfg.MaxGames,
			"rate_limiting": s.cfg.RateLimit.Enabled,
			"protection":   s.cfg.Protection.Enabled,
			"ip_filter":    s.cfg.IPFilter.Mode,
			"domain":       s.cfg.Domain,
			"external_ip":  s.cfg.ExternalIP,
		},
	})
}

func (s *Server) handleAdminGetBlocklist(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"blocklist": s.ipFilter.GetBlocklist(),
	})
}

func (s *Server) handleAdminAddBlocklist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP string `json:"ip"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if req.IP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ip_required"})
		return
	}

	s.ipFilter.AddToBlocklist(req.IP)
	log.Printf("[admin] IP added to blocklist: %s", req.IP)
	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

func (s *Server) handleAdminRemoveBlocklist(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	s.ipFilter.RemoveFromBlocklist(ip)
	log.Printf("[admin] IP removed from blocklist: %s", ip)
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleAdminGetBlocked(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"blocked": s.protection.GetBlocked(),
	})
}

func (s *Server) handleAdminUnblock(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	s.protection.Unblock(ip)
	log.Printf("[admin] IP unblocked: %s", ip)
	writeJSON(w, http.StatusOK, map[string]string{"status": "unblocked"})
}

func (s *Server) handleAdminReload(w http.ResponseWriter, r *http.Request) {
	newCfg, err := LoadConfig("config.toml")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Reload IP filter
	s.ipFilter.Reload(newCfg.IPFilter)
	log.Printf("[admin] configuration reloaded")
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}



func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":    "NAT Punchthrough Hero",
		"version": Version,
		"docs":    "/admin (dashboard) | /api/health (status) | /api/games (game list)",
	})
}

// --- WebSocket Handler ---

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	ip := extractIP(r)

	// Check API key (via query param or header for WebSocket)
	if s.cfg.GameAPIKey != "" {
		apiKey := r.URL.Query().Get("key")
		if apiKey == "" {
			apiKey = r.Header.Get("X-API-Key")
		}
		if apiKey != s.cfg.GameAPIKey {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Rate limit WebSocket connections
	if !s.rateLimiter.AllowWebSocket(ip) {
		http.Error(w, "Too many connections", http.StatusTooManyRequests)
		return
	}

	s.signaling.HandleConnection(w, r, ip)
}

// --- JSON Helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Host-Token, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	var val int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			val = val*10 + int(c-'0')
		} else {
			return defaultVal
		}
	}
	return val
}

// Version is set at build time via ldflags.
var Version = "dev"
