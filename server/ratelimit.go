package main

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter provides multi-layer rate limiting for the server.
// It uses token bucket algorithms per IP address, with background
// cleanup of stale entries to prevent memory leaks.
//
// Layers:
//   - Global: server-wide requests per second
//   - Per-IP: requests per minute per IP
//   - Per-IP burst: max concurrent requests per IP
//   - Specialized: per-endpoint limits (WS, games, joins, TURN)
type RateLimiter struct {
	cfg     RateLimitConfig
	global  *rate.Limiter
	perIP   sync.Map // string -> *ipLimiter
	stopCh  chan struct{}

	// Callback for violation tracking (used by Protection layer)
	OnViolation func(ip string)
}

// ipLimiter holds all rate limiters for a single IP address.
type ipLimiter struct {
	general    *rate.Limiter
	websocket  *rate.Limiter
	games      *rate.Limiter
	joins      *rate.Limiter
	turn       *rate.Limiter
	activeWS   int
	lastSeen   time.Time
	mu         sync.Mutex
}

// NewRateLimiter creates a rate limiter from the given config.
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		cfg:    cfg,
		global: rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalRPS*2),
		stopCh: make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Stop halts the cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// AllowRequest checks if a general HTTP request from the given IP is allowed.
// Returns true if allowed, false if rate limited.
func (rl *RateLimiter) AllowRequest(ip string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	// Global limit
	if !rl.global.Allow() {
		return false
	}

	// Per-IP limit
	limiter := rl.getOrCreate(ip)
	limiter.mu.Lock()
	limiter.lastSeen = time.Now()
	limiter.mu.Unlock()

	if !limiter.general.Allow() {
		if rl.OnViolation != nil {
			rl.OnViolation(ip)
		}
		return false
	}

	return true
}

// AllowWebSocket checks if a new WebSocket connection from the IP is allowed.
func (rl *RateLimiter) AllowWebSocket(ip string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	limiter := rl.getOrCreate(ip)
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	limiter.lastSeen = time.Now()

	// Check concurrent WS limit
	if limiter.activeWS >= rl.cfg.WSPerIPMax {
		if rl.OnViolation != nil {
			rl.OnViolation(ip)
		}
		return false
	}

	// Check WS rate
	if !limiter.websocket.Allow() {
		if rl.OnViolation != nil {
			rl.OnViolation(ip)
		}
		return false
	}

	limiter.activeWS++
	return true
}

// ReleaseWebSocket decrements the active WS count for an IP.
func (rl *RateLimiter) ReleaseWebSocket(ip string) {
	if v, ok := rl.perIP.Load(ip); ok {
		limiter := v.(*ipLimiter)
		limiter.mu.Lock()
		if limiter.activeWS > 0 {
			limiter.activeWS--
		}
		limiter.mu.Unlock()
	}
}

// AllowGameRegistration checks if a game registration from the IP is allowed.
func (rl *RateLimiter) AllowGameRegistration(ip string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	limiter := rl.getOrCreate(ip)
	if !limiter.games.Allow() {
		if rl.OnViolation != nil {
			rl.OnViolation(ip)
		}
		return false
	}
	return true
}

// AllowJoin checks if a join request from the IP is allowed.
func (rl *RateLimiter) AllowJoin(ip string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	limiter := rl.getOrCreate(ip)
	if !limiter.joins.Allow() {
		if rl.OnViolation != nil {
			rl.OnViolation(ip)
		}
		return false
	}
	return true
}

// AllowTURN checks if a TURN credential request from the IP is allowed.
func (rl *RateLimiter) AllowTURN(ip string) bool {
	if !rl.cfg.Enabled {
		return true
	}

	limiter := rl.getOrCreate(ip)
	if !limiter.turn.Allow() {
		if rl.OnViolation != nil {
			rl.OnViolation(ip)
		}
		return false
	}
	return true
}

// Middleware returns an HTTP middleware that applies general rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !rl.AllowRequest(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate_limited","message":"Too many requests"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// getOrCreate returns or creates the rate limiters for an IP.
func (rl *RateLimiter) getOrCreate(ip string) *ipLimiter {
	if v, ok := rl.perIP.Load(ip); ok {
		return v.(*ipLimiter)
	}

	limiter := &ipLimiter{
		// Per-IP: convert RPM to per-second rate
		general:   rate.NewLimiter(rate.Limit(float64(rl.cfg.PerIPRPM)/60.0), rl.cfg.PerIPBurst),
		websocket: rate.NewLimiter(rate.Limit(float64(rl.cfg.WSPerIPRPM)/60.0), 2),
		games:     rate.NewLimiter(rate.Limit(float64(rl.cfg.GamesPerIPRPH)/3600.0), 3),
		joins:     rate.NewLimiter(rate.Limit(float64(rl.cfg.JoinsPerIPRPM)/60.0), 5),
		turn:      rate.NewLimiter(rate.Limit(float64(rl.cfg.TurnPerIPRPH)/3600.0), 2),
		lastSeen:  time.Now(),
	}

	actual, _ := rl.perIP.LoadOrStore(ip, limiter)
	return actual.(*ipLimiter)
}

// cleanupLoop removes stale IP entries every 60 seconds.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanup removes IP entries not seen in the last 5 minutes.
func (rl *RateLimiter) cleanup() {
	cutoff := time.Now().Add(-5 * time.Minute)
	rl.perIP.Range(func(key, value interface{}) bool {
		limiter := value.(*ipLimiter)
		limiter.mu.Lock()
		stale := limiter.lastSeen.Before(cutoff) && limiter.activeWS == 0
		limiter.mu.Unlock()
		if stale {
			rl.perIP.Delete(key)
		}
		return true
	})
}
