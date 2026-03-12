package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func testRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:       true,
		GlobalRPS:     100,
		PerIPRPM:      60,
		PerIPBurst:    10,
		WSPerIPRPM:    30,
		WSPerIPMax:    3,
		GamesPerIPRPH: 36,
		JoinsPerIPRPM: 30,
		TurnPerIPRPH:  36,
	}
}

func disabledRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{Enabled: false}
}

// --- Disabled ---

func TestRateLimiter_Disabled_AllowsEverything(t *testing.T) {
	rl := NewRateLimiter(disabledRateLimitConfig())
	defer rl.Stop()

	for i := 0; i < 1000; i++ {
		if !rl.AllowRequest("1.2.3.4") {
			t.Fatal("disabled rate limiter should allow all requests")
		}
	}
}

func TestRateLimiter_Disabled_AllowWebSocket(t *testing.T) {
	rl := NewRateLimiter(disabledRateLimitConfig())
	defer rl.Stop()

	for i := 0; i < 100; i++ {
		if !rl.AllowWebSocket("1.2.3.4") {
			t.Fatal("disabled rate limiter should allow all WS connections")
		}
	}
}

func TestRateLimiter_Disabled_AllowGameRegistration(t *testing.T) {
	rl := NewRateLimiter(disabledRateLimitConfig())
	defer rl.Stop()

	for i := 0; i < 100; i++ {
		if !rl.AllowGameRegistration("1.2.3.4") {
			t.Fatal("disabled rate limiter should allow all game registrations")
		}
	}
}

func TestRateLimiter_Disabled_AllowJoin(t *testing.T) {
	rl := NewRateLimiter(disabledRateLimitConfig())
	defer rl.Stop()

	for i := 0; i < 100; i++ {
		if !rl.AllowJoin("1.2.3.4") {
			t.Fatal("disabled rate limiter should allow all joins")
		}
	}
}

func TestRateLimiter_Disabled_AllowTURN(t *testing.T) {
	rl := NewRateLimiter(disabledRateLimitConfig())
	defer rl.Stop()

	for i := 0; i < 100; i++ {
		if !rl.AllowTURN("1.2.3.4") {
			t.Fatal("disabled rate limiter should allow all TURN requests")
		}
	}
}

// --- AllowRequest per-IP burst ---

func TestRateLimiter_AllowRequest_BurstThenLimit(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.PerIPBurst = 5
	cfg.PerIPRPM = 60 // 1 per second
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	allowed := 0
	for i := 0; i < 20; i++ {
		if rl.AllowRequest("10.0.0.1") {
			allowed++
		}
	}

	// Should allow at most burst (5) + some from steady rate
	if allowed > 8 {
		t.Errorf("expected at most ~8 allowed, got %d", allowed)
	}
	if allowed < 3 {
		t.Errorf("expected at least 3 allowed, got %d", allowed)
	}
}

// --- AllowWebSocket concurrent limit ---

func TestRateLimiter_AllowWebSocket_ConcurrentLimit(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.WSPerIPMax = 2
	cfg.WSPerIPRPM = 600 // high RPM so rate is not the bottleneck
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	ip := "10.0.0.1"

	if !rl.AllowWebSocket(ip) {
		t.Fatal("first WS should be allowed")
	}
	if !rl.AllowWebSocket(ip) {
		t.Fatal("second WS should be allowed")
	}
	if rl.AllowWebSocket(ip) {
		t.Fatal("third WS should be denied (max=2)")
	}

	// Release one
	rl.ReleaseWebSocket(ip)

	if !rl.AllowWebSocket(ip) {
		t.Fatal("WS should be allowed after release")
	}
}

// --- ReleaseWebSocket ---

func TestReleaseWebSocket_NonexistentIP(t *testing.T) {
	rl := NewRateLimiter(testRateLimitConfig())
	defer rl.Stop()

	// Should not panic
	rl.ReleaseWebSocket("never-connected")
}

func TestReleaseWebSocket_DoesNotGoNegative(t *testing.T) {
	rl := NewRateLimiter(testRateLimitConfig())
	defer rl.Stop()

	ip := "10.0.0.1"
	rl.AllowWebSocket(ip)
	rl.ReleaseWebSocket(ip)
	rl.ReleaseWebSocket(ip) // extra release

	// Verify activeWS doesn't go below 0
	v, ok := rl.perIP.Load(ip)
	if !ok {
		t.Fatal("expected ip entry to exist")
	}
	limiter := v.(*ipLimiter)
	limiter.mu.Lock()
	if limiter.activeWS < 0 {
		t.Error("activeWS went negative")
	}
	limiter.mu.Unlock()
}

// --- OnViolation callback ---

func TestRateLimiter_OnViolation_Called(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.PerIPBurst = 1
	cfg.PerIPRPM = 1 // extremely low
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	var mu sync.Mutex
	violations := make(map[string]int)
	rl.OnViolation = func(ip string) {
		mu.Lock()
		violations[ip]++
		mu.Unlock()
	}

	// Exhaust burst
	rl.AllowRequest("bad-ip")

	// This should trigger violation
	for i := 0; i < 10; i++ {
		rl.AllowRequest("bad-ip")
	}

	mu.Lock()
	count := violations["bad-ip"]
	mu.Unlock()

	if count == 0 {
		t.Error("OnViolation was never called")
	}
}

// --- AllowGameRegistration ---

func TestRateLimiter_GameRegistration_BurstThenLimit(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.GamesPerIPRPH = 36 // 0.01 per second, burst 3
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.AllowGameRegistration("10.0.0.1") {
			allowed++
		}
	}

	// Should be limited to burst (3) + possibly 1 from rate
	if allowed > 5 {
		t.Errorf("expected at most 5 allowed, got %d", allowed)
	}
	if allowed < 1 {
		t.Errorf("expected at least 1 allowed, got %d", allowed)
	}
}

// --- AllowJoin ---

func TestRateLimiter_Join_BurstThenLimit(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.JoinsPerIPRPM = 30 // 0.5 per second, burst 5
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	allowed := 0
	for i := 0; i < 20; i++ {
		if rl.AllowJoin("10.0.0.1") {
			allowed++
		}
	}

	if allowed > 8 {
		t.Errorf("expected at most ~8 allowed, got %d", allowed)
	}
	if allowed < 3 {
		t.Errorf("expected at least 3 allowed, got %d", allowed)
	}
}

// --- AllowTURN ---

func TestRateLimiter_TURN_BurstThenLimit(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.TurnPerIPRPH = 36 // 0.01 per second, burst 2
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.AllowTURN("10.0.0.1") {
			allowed++
		}
	}

	if allowed > 4 {
		t.Errorf("expected at most 4 allowed, got %d", allowed)
	}
	if allowed < 1 {
		t.Errorf("expected at least 1 allowed, got %d", allowed)
	}
}

// --- Different IPs are independent ---

func TestRateLimiter_DifferentIPs_Independent(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.PerIPBurst = 2
	cfg.PerIPRPM = 1
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	// Exhaust IP1
	rl.AllowRequest("ip1")
	rl.AllowRequest("ip1")
	rl.AllowRequest("ip1")

	// IP2 should still be fresh
	if !rl.AllowRequest("ip2") {
		t.Error("different IPs should have independent limits")
	}
}

// --- Middleware ---

func TestRateLimiter_Middleware_Returns429(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.PerIPBurst = 1
	cfg.PerIPRPM = 1
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should pass
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request: got %d, want 200", w.Code)
	}

	// Flood until limited
	got429 := false
	for i := 0; i < 20; i++ {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}

	if !got429 {
		t.Error("expected 429 response after exceeding rate limit")
	}
}

func TestRateLimiter_Middleware_SetsRetryAfter(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.PerIPBurst = 1
	cfg.PerIPRPM = 1
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"

	// Exhaust
	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			if w.Header().Get("Retry-After") != "60" {
				t.Errorf("Retry-After = %q, want %q", w.Header().Get("Retry-After"), "60")
			}
			return
		}
	}
	t.Error("never got 429 to check Retry-After header")
}

// --- Cleanup ---

func TestRateLimiter_Cleanup_RemovesStale(t *testing.T) {
	cfg := testRateLimitConfig()
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	rl.AllowRequest("stale-ip")

	// Manually set lastSeen to the past
	v, _ := rl.perIP.Load("stale-ip")
	limiter := v.(*ipLimiter)
	limiter.mu.Lock()
	limiter.lastSeen = limiter.lastSeen.Add(-10 * time.Minute)
	limiter.mu.Unlock()

	rl.cleanup()

	if _, ok := rl.perIP.Load("stale-ip"); ok {
		t.Error("stale IP should have been cleaned up")
	}
}

func TestRateLimiter_Cleanup_KeepsActiveWS(t *testing.T) {
	cfg := testRateLimitConfig()
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	rl.AllowWebSocket("ws-ip")

	// Set lastSeen to the past but keep active WS
	v, _ := rl.perIP.Load("ws-ip")
	limiter := v.(*ipLimiter)
	limiter.mu.Lock()
	limiter.lastSeen = limiter.lastSeen.Add(-10 * time.Minute)
	limiter.mu.Unlock()

	rl.cleanup()

	if _, ok := rl.perIP.Load("ws-ip"); !ok {
		t.Error("IP with active WS should not be cleaned up even if stale")
	}
}

func TestRateLimiter_Cleanup_KeepsFresh(t *testing.T) {
	cfg := testRateLimitConfig()
	rl := NewRateLimiter(cfg)
	defer rl.Stop()

	rl.AllowRequest("fresh-ip")
	rl.cleanup()

	if _, ok := rl.perIP.Load("fresh-ip"); !ok {
		t.Error("fresh IP should not be cleaned up")
	}
}
