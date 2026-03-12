package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Game represents a hosted game session registered with the master server.
type Game struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Map         string          `json:"map,omitempty"`
	GameVersion string          `json:"game_version,omitempty"`
	MaxPlayers  int             `json:"max_players"`
	CurPlayers  int             `json:"current_players"`
	Data        json.RawMessage `json:"data,omitempty"` // Arbitrary game metadata
	HostToken   string          `json:"-"`              // Never exposed to API consumers
	HostIP      string          `json:"-"`              // Never exposed in game list
	HostPort    int             `json:"-"`              // Never exposed in game list
	LocalIP     string          `json:"-"`              // Host's LAN IP for local connections
	LocalPort   int             `json:"-"`              // Host's LAN port
	NATType     string          `json:"nat_type,omitempty"`
	ConnMethod  string          `json:"conn_method,omitempty"` // "direct", "punched", "relayed"
	JoinCode    string          `json:"join_code,omitempty"`
	Private     bool            `json:"private"`
	CreatedAt   time.Time       `json:"created_at"`
	LastSeen    time.Time       `json:"-"`
	OwnerIP     string          `json:"-"` // For rate limiting
}

// GamePublic is the public-facing view of a Game (no sensitive fields).
type GamePublic struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Map         string          `json:"map,omitempty"`
	GameVersion string          `json:"game_version,omitempty"`
	MaxPlayers  int             `json:"max_players"`
	CurPlayers  int             `json:"current_players"`
	Data        json.RawMessage `json:"data,omitempty"`
	NATType     string          `json:"nat_type,omitempty"`
	JoinCode    string          `json:"join_code,omitempty"`
	Private     bool            `json:"private"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ToPublic converts a Game to its public representation.
func (g *Game) ToPublic() GamePublic {
	return GamePublic{
		ID:          g.ID,
		Name:        g.Name,
		Map:         g.Map,
		GameVersion: g.GameVersion,
		MaxPlayers:  g.MaxPlayers,
		CurPlayers:  g.CurPlayers,
		Data:        g.Data,
		NATType:     g.NATType,
		JoinCode:    g.JoinCode,
		Private:     g.Private,
		CreatedAt:   g.CreatedAt,
	}
}

// GameStore provides thread-safe in-memory storage for game sessions.
// It handles automatic eviction of stale games based on heartbeat timeouts.
type GameStore struct {
	mu       sync.RWMutex
	games    map[string]*Game
	byCode   map[string]string // join_code -> game_id
	maxGames int
	timeout  time.Duration
	stopCh   chan struct{}

	// Stats
	totalRegistered int64
	totalExpired    int64
}

// NewGameStore creates a new store with the given limits.
// It starts a background goroutine for evicting stale games.
func NewGameStore(maxGames int, timeoutSec int) *GameStore {
	s := &GameStore{
		games:    make(map[string]*Game),
		byCode:   make(map[string]string),
		maxGames: maxGames,
		timeout:  time.Duration(timeoutSec) * time.Second,
		stopCh:   make(chan struct{}),
	}
	go s.evictionLoop()
	return s
}

// Stop halts the background eviction goroutine.
func (s *GameStore) Stop() {
	close(s.stopCh)
}

// Register adds a new game to the store. Returns the game (with generated ID,
// host token, and join code) or an error if the store is full.
func (s *GameStore) Register(g *Game) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.games) >= s.maxGames {
		return nil, fmt.Errorf("max games reached (%d)", s.maxGames)
	}

	g.ID = generateGameID()
	g.HostToken = generateHostToken()
	g.JoinCode = generateJoinCode()
	g.CreatedAt = time.Now()
	g.LastSeen = time.Now()

	s.games[g.ID] = g
	s.byCode[strings.ToUpper(g.JoinCode)] = g.ID
	s.totalRegistered++

	return g, nil
}

// Get returns a game by ID, or nil if not found.
func (s *GameStore) Get(id string) *Game {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.games[id]
}

// GetByCode returns a game by join code, or nil if not found.
func (s *GameStore) GetByCode(code string) *Game {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byCode[strings.ToUpper(code)]
	if !ok {
		return nil
	}
	return s.games[id]
}

// List returns all public (non-private) games, optionally filtered by version.
func (s *GameStore) List(version string, limit, offset int) []GamePublic {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var result []GamePublic
	skipped := 0

	for _, g := range s.games {
		if g.Private {
			continue
		}
		if version != "" && g.GameVersion != version {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		result = append(result, g.ToPublic())
		if len(result) >= limit {
			break
		}
	}

	return result
}

// Remove deletes a game by ID. Returns true if the game existed.
func (s *GameStore) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(id)
}

// RemoveWithToken deletes a game only if the host token matches.
// Returns true if removed, false if not found or token mismatch.
func (s *GameStore) RemoveWithToken(id, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.games[id]
	if !ok {
		return false
	}
	if g.HostToken != token {
		return false
	}
	return s.removeLocked(id)
}

// Heartbeat updates the last-seen time for a game. Returns false if not found.
func (s *GameStore) Heartbeat(id, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.games[id]
	if !ok {
		return false
	}
	if g.HostToken != token {
		return false
	}
	g.LastSeen = time.Now()
	return true
}

// UpdatePlayers updates the current player count for a game.
func (s *GameStore) UpdatePlayers(id string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if g, ok := s.games[id]; ok {
		g.CurPlayers = count
	}
}

// UpdateConnMethod records how players are connecting to a game.
func (s *GameStore) UpdateConnMethod(id, method string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if g, ok := s.games[id]; ok {
		g.ConnMethod = method
	}
}

// Count returns the number of active games.
func (s *GameStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.games)
}

// Stats returns store statistics.
func (s *GameStore) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	methods := make(map[string]int)
	natTypes := make(map[string]int)
	totalPlayers := 0

	for _, g := range s.games {
		if g.ConnMethod != "" {
			methods[g.ConnMethod]++
		}
		if g.NATType != "" {
			natTypes[g.NATType]++
		}
		totalPlayers += g.CurPlayers
	}

	return StoreStats{
		ActiveGames:     len(s.games),
		TotalPlayers:    totalPlayers,
		TotalRegistered: s.totalRegistered,
		TotalExpired:    s.totalExpired,
		ConnMethods:     methods,
		NATTypes:        natTypes,
	}
}

// StoreStats holds aggregate statistics about the game store.
type StoreStats struct {
	ActiveGames     int            `json:"active_games"`
	TotalPlayers    int            `json:"total_players"`
	TotalRegistered int64          `json:"total_registered"`
	TotalExpired    int64          `json:"total_expired"`
	ConnMethods     map[string]int `json:"conn_methods"`
	NATTypes        map[string]int `json:"nat_types"`
}

// removeLocked removes a game. Caller must hold s.mu write lock.
func (s *GameStore) removeLocked(id string) bool {
	g, ok := s.games[id]
	if !ok {
		return false
	}
	if g.JoinCode != "" {
		delete(s.byCode, strings.ToUpper(g.JoinCode))
	}
	delete(s.games, id)
	return true
}

// evictionLoop runs every 10 seconds to remove stale games.
func (s *GameStore) evictionLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.evict()
		case <-s.stopCh:
			return
		}
	}
}

// evict removes games that haven't sent a heartbeat within the timeout.
func (s *GameStore) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, g := range s.games {
		if now.Sub(g.LastSeen) > s.timeout {
			if g.JoinCode != "" {
				delete(s.byCode, strings.ToUpper(g.JoinCode))
			}
			delete(s.games, id)
			s.totalExpired++
		}
	}
}

// generateGameID creates a random game identifier (16 hex chars).
func generateGameID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// generateHostToken creates a random host authentication token (64 hex chars / 256 bits).
func generateHostToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("tk-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// generateJoinCode creates a human-friendly join code (6 alphanumeric chars).
// Excludes confusable characters: 0/O, 1/I/L.
func generateJoinCode() string {
	const chars = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "000000"
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
