package main

import (
	"log"
	"sync"
	"time"
)

// Protection provides automatic abuse detection and IP blocking.
// It tracks rate limit violations per IP, detects connection floods,
// and automatically blocks repeat offenders with escalating durations.
type Protection struct {
	cfg      ProtectionConfig
	filter   *IPFilter
	mu       sync.Mutex
	tracked  map[string]*offenderRecord
	stopCh   chan struct{}

	// Stats
	TotalBlocked   int64
	TotalViolations int64
}

// offenderRecord tracks abuse history for a single IP.
type offenderRecord struct {
	Violations    int
	LastViolation time.Time
	BlockedUntil  time.Time
	BlockCount    int       // Number of times this IP has been blocked
	FirstSeen     time.Time
	Connections   []time.Time // Sliding window for flood detection
}

// NewProtection creates a protection system linked to the given IP filter.
func NewProtection(cfg ProtectionConfig, filter *IPFilter) *Protection {
	p := &Protection{
		cfg:     cfg,
		filter:  filter,
		tracked: make(map[string]*offenderRecord),
		stopCh:  make(chan struct{}),
	}
	go p.cleanupLoop()
	return p
}

// Stop halts the background cleanup goroutine.
func (p *Protection) Stop() {
	close(p.stopCh)
}

// RecordViolation records a rate limit violation from the given IP.
// If the IP exceeds the auto-block threshold, it is automatically blocked.
func (p *Protection) RecordViolation(ip string) {
	if !p.cfg.Enabled || !p.cfg.AutoBlock {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.TotalViolations++

	rec := p.getOrCreate(ip)
	rec.Violations++
	rec.LastViolation = time.Now()

	if rec.Violations >= p.cfg.AutoBlockThreshold {
		p.blockIP(ip, rec)
	}
}

// RecordConnection records a new connection from an IP for flood detection.
// Returns false if the connection should be rejected (flood detected).
func (p *Protection) RecordConnection(ip string) bool {
	if !p.cfg.Enabled {
		return true
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	rec := p.getOrCreate(ip)

	// Check if currently blocked
	if time.Now().Before(rec.BlockedUntil) {
		return false
	}

	// Add connection timestamp
	now := time.Now()
	rec.Connections = append(rec.Connections, now)

	// Parse flood window
	window, err := ParseDuration(p.cfg.FloodWindow)
	if err != nil {
		window = 10 * time.Second
	}

	// Clean old entries outside the window
	cutoff := now.Add(-window)
	start := 0
	for start < len(rec.Connections) && rec.Connections[start].Before(cutoff) {
		start++
	}
	rec.Connections = rec.Connections[start:]

	// Check flood threshold
	if len(rec.Connections) > p.cfg.FloodConnections {
		if p.cfg.LogBlocked {
			log.Printf("[protection] flood detected from %s (%d connections in %s)",
				ip, len(rec.Connections), p.cfg.FloodWindow)
		}
		p.blockIPWithDuration(ip, rec, p.cfg.FloodBlockDuration)
		return false
	}

	return true
}

// IsBlocked checks if an IP is currently auto-blocked.
func (p *Protection) IsBlocked(ip string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	rec, ok := p.tracked[ip]
	if !ok {
		return false
	}
	return time.Now().Before(rec.BlockedUntil)
}

// Unblock manually removes an auto-block for an IP.
func (p *Protection) Unblock(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if rec, ok := p.tracked[ip]; ok {
		rec.BlockedUntil = time.Time{}
		rec.Violations = 0
	}
	p.filter.RemoveFromBlocklist(ip)
}

// GetBlocked returns a list of currently auto-blocked IPs with their unblock times.
func (p *Protection) GetBlocked() []BlockedEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var result []BlockedEntry
	for ip, rec := range p.tracked {
		if now.Before(rec.BlockedUntil) {
			result = append(result, BlockedEntry{
				IP:           ip,
				BlockedUntil: rec.BlockedUntil,
				Violations:   rec.Violations,
				BlockCount:   rec.BlockCount,
				Reason:       "auto-blocked",
			})
		}
	}
	return result
}

// Stats returns protection statistics.
func (p *Protection) Stats() ProtectionStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	blocked := 0
	now := time.Now()
	for _, rec := range p.tracked {
		if now.Before(rec.BlockedUntil) {
			blocked++
		}
	}

	return ProtectionStats{
		CurrentlyBlocked: blocked,
		TotalBlocked:     p.TotalBlocked,
		TotalViolations:  p.TotalViolations,
		TrackedIPs:       len(p.tracked),
	}
}

// BlockedEntry represents a single auto-blocked IP.
type BlockedEntry struct {
	IP           string    `json:"ip"`
	BlockedUntil time.Time `json:"blocked_until"`
	Violations   int       `json:"violations"`
	BlockCount   int       `json:"block_count"`
	Reason       string    `json:"reason"`
}

// ProtectionStats holds aggregate protection statistics.
type ProtectionStats struct {
	CurrentlyBlocked int   `json:"currently_blocked"`
	TotalBlocked     int64 `json:"total_blocked"`
	TotalViolations  int64 `json:"total_violations"`
	TrackedIPs       int   `json:"tracked_ips"`
}

// blockIP blocks an IP with escalating duration.
func (p *Protection) blockIP(ip string, rec *offenderRecord) {
	baseDuration, err := ParseDuration(p.cfg.AutoBlockDuration)
	if err != nil {
		baseDuration = 1 * time.Hour
	}

	duration := baseDuration
	if p.cfg.AutoBlockEscalation && rec.BlockCount > 0 {
		// Double duration for each previous block, cap at 24h
		for i := 0; i < rec.BlockCount; i++ {
			duration *= 2
			if duration > 24*time.Hour {
				duration = 24 * time.Hour
				break
			}
		}
	}

	rec.BlockedUntil = time.Now().Add(duration)
	rec.BlockCount++
	rec.Violations = 0 // Reset violations after block
	p.TotalBlocked++

	// Add to IP filter for immediate enforcement at the middleware layer
	p.filter.AddToBlocklist(ip)

	if p.cfg.LogBlocked {
		log.Printf("[protection] auto-blocked %s for %s (offense #%d, %d violations)",
			ip, duration, rec.BlockCount, rec.Violations)
	}
}

// blockIPWithDuration blocks an IP for a specific duration string.
func (p *Protection) blockIPWithDuration(ip string, rec *offenderRecord, durationStr string) {
	duration, err := ParseDuration(durationStr)
	if err != nil {
		duration = 1 * time.Hour
	}

	rec.BlockedUntil = time.Now().Add(duration)
	rec.BlockCount++
	p.TotalBlocked++

	p.filter.AddToBlocklist(ip)

	if p.cfg.LogBlocked {
		log.Printf("[protection] blocked %s for %s (flood detection)", ip, duration)
	}
}

// getOrCreate returns or creates an offender record. Caller must hold p.mu.
func (p *Protection) getOrCreate(ip string) *offenderRecord {
	if rec, ok := p.tracked[ip]; ok {
		return rec
	}
	rec := &offenderRecord{
		FirstSeen: time.Now(),
	}
	p.tracked[ip] = rec
	return rec
}

// cleanupLoop removes expired tracking records every 5 minutes.
func (p *Protection) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.cleanup()
		case <-p.stopCh:
			return
		}
	}
}

// cleanup removes records for IPs that haven't been seen in 7 days
// and whose blocks have expired.
func (p *Protection) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	now := time.Now()

	for ip, rec := range p.tracked {
		// Only clean up if block has expired and record is old
		if now.After(rec.BlockedUntil) && rec.LastViolation.Before(cutoff) {
			// Remove from IP filter if it was auto-blocked
			p.filter.RemoveFromBlocklist(ip)
			delete(p.tracked, ip)
		}
	}
}
