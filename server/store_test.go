package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Helpers ---

// newTestStore returns a GameStore with sensible test defaults.
func newTestStore() *GameStore {
	return NewGameStore(100, 300) // 100 max, 300s timeout
}

func registerTestGame(t *testing.T, s *GameStore, name string) *Game {
	t.Helper()
	g, err := s.Register(&Game{Name: name, MaxPlayers: 4, CurPlayers: 1, NATType: "moderate"})
	if err != nil {
		t.Fatalf("Register(%q) failed: %v", name, err)
	}
	return g
}

// --- Register ---

func TestRegister_Success(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Test Game")

	if g.ID == "" {
		t.Error("expected non-empty ID")
	}
	if len(g.ID) != 16 {
		t.Errorf("expected 16 char ID, got %d", len(g.ID))
	}
	if g.HostToken == "" {
		t.Error("expected non-empty HostToken")
	}
	if len(g.HostToken) != 64 {
		t.Errorf("expected 64 char HostToken, got %d", len(g.HostToken))
	}
	if g.JoinCode == "" {
		t.Error("expected non-empty JoinCode")
	}
	if len(g.JoinCode) != 6 {
		t.Errorf("expected 6 char JoinCode, got %d", len(g.JoinCode))
	}
	if g.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if g.Name != "Test Game" {
		t.Errorf("name = %q, want %q", g.Name, "Test Game")
	}
}

func TestRegister_MaxGames(t *testing.T) {
	s := NewGameStore(2, 300)
	defer s.Stop()

	registerTestGame(t, s, "Game 1")
	registerTestGame(t, s, "Game 2")

	_, err := s.Register(&Game{Name: "Game 3", MaxPlayers: 4})
	if err == nil {
		t.Error("expected error when exceeding max games")
	}
}

func TestRegister_UniqueIDs(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	ids := make(map[string]bool)
	for i := 0; i < 50; i++ {
		g := registerTestGame(t, s, "Game")
		if ids[g.ID] {
			t.Fatalf("duplicate ID on iteration %d: %s", i, g.ID)
		}
		ids[g.ID] = true
	}
}

func TestRegister_UniqueJoinCodes(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	codes := make(map[string]bool)
	for i := 0; i < 50; i++ {
		g := registerTestGame(t, s, "Game")
		if codes[g.JoinCode] {
			t.Fatalf("duplicate JoinCode on iteration %d: %s", i, g.JoinCode)
		}
		codes[g.JoinCode] = true
	}
}

// --- Get ---

func TestGet_Found(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Hello")
	got := s.Get(g.ID)
	if got == nil {
		t.Fatal("expected game, got nil")
	}
	if got.Name != "Hello" {
		t.Errorf("Name = %q, want %q", got.Name, "Hello")
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	if s.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent ID")
	}
}

// --- GetByCode ---

func TestGetByCode_Found(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Coded")
	got := s.GetByCode(g.JoinCode)
	if got == nil {
		t.Fatal("expected game, got nil")
	}
	if got.ID != g.ID {
		t.Errorf("got ID %s, want %s", got.ID, g.ID)
	}
}

func TestGetByCode_CaseInsensitive(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Coded")
	lower := strings.ToLower(g.JoinCode)
	upper := strings.ToUpper(g.JoinCode)

	if s.GetByCode(lower) == nil {
		t.Error("expected case-insensitive lookup (lower)")
	}
	if s.GetByCode(upper) == nil {
		t.Error("expected case-insensitive lookup (upper)")
	}
}

func TestGetByCode_NotFound(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	if s.GetByCode("ZZZZZZ") != nil {
		t.Error("expected nil for nonexistent join code")
	}
}

// --- List ---

func TestList_Empty(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	games := s.List("", 50, 0)
	if len(games) != 0 {
		t.Errorf("expected 0 games, got %d", len(games))
	}
}

func TestList_ExcludesPrivate(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	registerTestGame(t, s, "Public")
	priv, err := s.Register(&Game{Name: "Private", MaxPlayers: 4, Private: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = priv

	games := s.List("", 50, 0)
	if len(games) != 1 {
		t.Fatalf("expected 1 public game, got %d", len(games))
	}
	if games[0].Name != "Public" {
		t.Errorf("expected Public game, got %q", games[0].Name)
	}
}

func TestList_FilterByVersion(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	s.Register(&Game{Name: "V1", MaxPlayers: 4, GameVersion: "1.0"})
	s.Register(&Game{Name: "V2", MaxPlayers: 4, GameVersion: "2.0"})

	games := s.List("1.0", 50, 0)
	if len(games) != 1 {
		t.Fatalf("expected 1 game for version 1.0, got %d", len(games))
	}
	if games[0].GameVersion != "1.0" {
		t.Errorf("version = %q, want %q", games[0].GameVersion, "1.0")
	}
}

func TestList_Pagination(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	for i := 0; i < 10; i++ {
		registerTestGame(t, s, "Game")
	}

	page1 := s.List("", 3, 0)
	if len(page1) != 3 {
		t.Errorf("page1 len = %d, want 3", len(page1))
	}

	page2 := s.List("", 3, 3)
	if len(page2) != 3 {
		t.Errorf("page2 len = %d, want 3", len(page2))
	}
}

func TestList_LimitCap(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	for i := 0; i < 5; i++ {
		registerTestGame(t, s, "Game")
	}

	// Negative limit → defaults to 50
	games := s.List("", -1, 0)
	if len(games) != 5 {
		t.Errorf("expected 5, got %d", len(games))
	}

	// Excessive limit → capped to 200
	games = s.List("", 999, 0)
	if len(games) != 5 {
		t.Errorf("expected 5, got %d", len(games))
	}
}

// --- Remove ---

func TestRemove_Success(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Delete Me")
	if !s.Remove(g.ID) {
		t.Error("Remove returned false")
	}
	if s.Get(g.ID) != nil {
		t.Error("game still exists after Remove")
	}
	if s.GetByCode(g.JoinCode) != nil {
		t.Error("join code still resolves after Remove")
	}
}

func TestRemove_NotFound(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	if s.Remove("nonexistent") {
		t.Error("Remove should return false for nonexistent ID")
	}
}

// --- RemoveWithToken ---

func TestRemoveWithToken_ValidToken(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Delete Me")
	if !s.RemoveWithToken(g.ID, g.HostToken) {
		t.Error("RemoveWithToken returned false with valid token")
	}
	if s.Get(g.ID) != nil {
		t.Error("game still exists after removal")
	}
}

func TestRemoveWithToken_WrongToken(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Delete Me")
	if s.RemoveWithToken(g.ID, "wrong-token") {
		t.Error("RemoveWithToken should fail with wrong token")
	}
	if s.Get(g.ID) == nil {
		t.Error("game should still exist with wrong token")
	}
}

// --- Heartbeat ---

func TestHeartbeat_Success(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Heartbeat")
	time.Sleep(10 * time.Millisecond) // Ensure time advances

	before := s.Get(g.ID).LastSeen
	if !s.Heartbeat(g.ID, g.HostToken) {
		t.Error("Heartbeat returned false")
	}
	after := s.Get(g.ID).LastSeen
	if !after.After(before) {
		t.Errorf("LastSeen not updated; before=%v after=%v", before, after)
	}
}

func TestHeartbeat_WrongToken(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Heartbeat")
	if s.Heartbeat(g.ID, "bad-token") {
		t.Error("Heartbeat should fail with wrong token")
	}
}

func TestHeartbeat_NotFound(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	if s.Heartbeat("nonexistent", "any-token") {
		t.Error("Heartbeat should fail for nonexistent game")
	}
}

// --- UpdatePlayers ---

func TestUpdatePlayers(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Players")
	s.UpdatePlayers(g.ID, 3)

	got := s.Get(g.ID)
	if got.CurPlayers != 3 {
		t.Errorf("CurPlayers = %d, want 3", got.CurPlayers)
	}
}

// --- UpdateConnMethod ---

func TestUpdateConnMethod(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	g := registerTestGame(t, s, "Conn")
	s.UpdateConnMethod(g.ID, "punched")

	got := s.Get(g.ID)
	if got.ConnMethod != "punched" {
		t.Errorf("ConnMethod = %q, want %q", got.ConnMethod, "punched")
	}
}

// --- Count & Stats ---

func TestCount(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	if s.Count() != 0 {
		t.Errorf("expected 0, got %d", s.Count())
	}

	registerTestGame(t, s, "G1")
	registerTestGame(t, s, "G2")

	if s.Count() != 2 {
		t.Errorf("expected 2, got %d", s.Count())
	}
}

func TestStats(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	s.Register(&Game{Name: "G1", MaxPlayers: 4, CurPlayers: 2, NATType: "moderate", ConnMethod: "direct"})
	s.Register(&Game{Name: "G2", MaxPlayers: 8, CurPlayers: 5, NATType: "symmetric"})

	stats := s.Stats()
	if stats.ActiveGames != 2 {
		t.Errorf("ActiveGames = %d, want 2", stats.ActiveGames)
	}
	if stats.TotalPlayers != 7 {
		t.Errorf("TotalPlayers = %d, want 7", stats.TotalPlayers)
	}
	if stats.TotalRegistered != 2 {
		t.Errorf("TotalRegistered = %d, want 2", stats.TotalRegistered)
	}
	if stats.ConnMethods["direct"] != 1 {
		t.Error("expected 1 direct connection method")
	}
	if stats.NATTypes["moderate"] != 1 {
		t.Error("expected 1 moderate NAT type")
	}
}

// --- Eviction ---

func TestEviction(t *testing.T) {
	s := NewGameStore(100, 1) // 1 second timeout
	defer s.Stop()

	g := registerTestGame(t, s, "Expire Me")

	// Manually set LastSeen in the past
	s.mu.Lock()
	s.games[g.ID].LastSeen = time.Now().Add(-2 * time.Second)
	s.mu.Unlock()

	s.evict()

	if s.Get(g.ID) != nil {
		t.Error("game should have been evicted")
	}
	if s.GetByCode(g.JoinCode) != nil {
		t.Error("join code should have been cleaned up after eviction")
	}
}

func TestEviction_KeepFreshGames(t *testing.T) {
	s := NewGameStore(100, 300) // 300s timeout
	defer s.Stop()

	g := registerTestGame(t, s, "Keep Me")
	s.evict()

	if s.Get(g.ID) == nil {
		t.Error("fresh game should not be evicted")
	}
}

// --- ToPublic ---

func TestToPublic_ExcludesSensitiveFields(t *testing.T) {
	g := &Game{
		ID:         "test123",
		Name:       "Game",
		MaxPlayers: 4,
		CurPlayers: 2,
		HostToken:  "secret-token",
		HostIP:     "192.168.1.1",
		HostPort:   7777,
		OwnerIP:    "10.0.0.1",
		JoinCode:   "ABC123",
	}

	pub := g.ToPublic()

	// Marshal to JSON and check no sensitive fields
	data, err := json.Marshal(pub)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)

	if strings.Contains(s, "secret-token") {
		t.Error("JSON contains HostToken")
	}
	if strings.Contains(s, "192.168.1.1") {
		t.Error("JSON contains HostIP")
	}
	if strings.Contains(s, "10.0.0.1") {
		t.Error("JSON contains OwnerIP")
	}
	if !strings.Contains(s, "ABC123") {
		t.Error("JSON should contain JoinCode")
	}
}

// --- Concurrency ---

func TestConcurrentRegisterAndGet(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := registerTestGame(t, s, "Concurrent")
			if s.Get(g.ID) == nil {
				t.Error("concurrent Get returned nil for just-registered game")
			}
		}()
	}
	wg.Wait()

	if s.Count() != 50 {
		t.Errorf("expected 50 games, got %d", s.Count())
	}
}

// --- JoinCode Charset ---

func TestJoinCode_NoConfusableChars(t *testing.T) {
	confusable := "01OIL"
	for i := 0; i < 100; i++ {
		code := generateJoinCode()
		for _, c := range code {
			if strings.ContainsRune(confusable, c) {
				t.Errorf("JoinCode %q contains confusable char %q", code, string(c))
			}
		}
	}
}

// --- Data field ---

func TestRegister_WithData(t *testing.T) {
	s := newTestStore()
	defer s.Stop()

	data := json.RawMessage(`{"map":"forest","mode":"pvp"}`)
	g, err := s.Register(&Game{Name: "Data Game", MaxPlayers: 4, Data: data})
	if err != nil {
		t.Fatal(err)
	}

	got := s.Get(g.ID)
	pub := got.ToPublic()

	var parsed map[string]string
	if err := json.Unmarshal(pub.Data, &parsed); err != nil {
		t.Fatalf("failed to parse data: %v", err)
	}
	if parsed["map"] != "forest" {
		t.Errorf("data.map = %q, want %q", parsed["map"], "forest")
	}
}
