package main

import (
	"testing"
	"time"
)

func testProtectionConfig() ProtectionConfig {
	return ProtectionConfig{
		Enabled:              true,
		AutoBlock:            true,
		AutoBlockThreshold:   5,
		AutoBlockDuration:    "1m",
		AutoBlockEscalation:  true,
		FloodConnections:     10,
		FloodWindow:          "10s",
		FloodBlockDuration:   "30s",
		LogBlocked:           false,
	}
}

func newTestProtection() (*Protection, *IPFilter) {
	filter := NewIPFilter(IPFilterConfig{Mode: "blocklist"})
	p := NewProtection(testProtectionConfig(), filter)
	return p, filter
}

// --- RecordViolation threshold ---

func TestProtection_RecordViolation_BlocksAtThreshold(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"

	// Under threshold: should not block
	for i := 0; i < 4; i++ {
		p.RecordViolation(ip)
	}
	if p.IsBlocked(ip) {
		t.Error("should not be blocked below threshold (5)")
	}

	// At threshold: should block
	p.RecordViolation(ip)
	if !p.IsBlocked(ip) {
		t.Error("should be blocked at threshold (5)")
	}
}

func TestProtection_RecordViolation_ResetAfterBlock(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	// Violations reset after block
	p.mu.Lock()
	rec := p.tracked[ip]
	if rec.Violations != 0 {
		t.Errorf("violations = %d, want 0 after block", rec.Violations)
	}
	p.mu.Unlock()
}

// --- Disabled Protection ---

func TestProtection_Disabled_NoBlocking(t *testing.T) {
	filter := NewIPFilter(IPFilterConfig{Mode: "blocklist"})
	cfg := testProtectionConfig()
	cfg.Enabled = false
	p := NewProtection(cfg, filter)
	defer p.Stop()

	for i := 0; i < 100; i++ {
		p.RecordViolation("10.0.0.1")
	}

	if p.IsBlocked("10.0.0.1") {
		t.Error("disabled protection should not block")
	}
}

func TestProtection_AutoBlockDisabled(t *testing.T) {
	filter := NewIPFilter(IPFilterConfig{Mode: "blocklist"})
	cfg := testProtectionConfig()
	cfg.AutoBlock = false
	p := NewProtection(cfg, filter)
	defer p.Stop()

	for i := 0; i < 100; i++ {
		p.RecordViolation("10.0.0.1")
	}

	if p.IsBlocked("10.0.0.1") {
		t.Error("auto-block disabled should not block")
	}
}

// --- Escalating blocks ---

func TestProtection_EscalatingBlocks(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"

	// First block: base duration (1m)
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	p.mu.Lock()
	rec1 := p.tracked[ip]
	firstBlockUntil := rec1.BlockedUntil
	firstCount := rec1.BlockCount
	p.mu.Unlock()

	if firstCount != 1 {
		t.Errorf("first block count = %d, want 1", firstCount)
	}

	// Manually expire the block
	p.mu.Lock()
	p.tracked[ip].BlockedUntil = time.Now().Add(-1 * time.Second)
	p.mu.Unlock()

	// Trigger second block
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	p.mu.Lock()
	rec2 := p.tracked[ip]
	secondBlockUntil := rec2.BlockedUntil
	secondCount := rec2.BlockCount
	p.mu.Unlock()

	if secondCount != 2 {
		t.Errorf("second block count = %d, want 2", secondCount)
	}

	// Second block should be longer than first (escalation)
	firstDuration := firstBlockUntil.Sub(time.Now())
	secondDuration := secondBlockUntil.Sub(time.Now())
	_ = firstDuration

	// With base=1m and escalation, second should be ~2m
	if secondDuration < 90*time.Second {
		t.Errorf("second block duration too short: %v (expected ~2m due to escalation)", secondDuration)
	}
}

// --- Flood detection ---

func TestProtection_FloodDetection_Blocks(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"

	// 10 connections within window is the limit
	for i := 0; i < 10; i++ {
		ok := p.RecordConnection(ip)
		if !ok {
			t.Fatalf("connection %d was rejected prematurely", i+1)
		}
	}

	// 11th should be rejected (flood)
	if p.RecordConnection(ip) {
		t.Error("11th connection should be rejected as flood")
	}

	if !p.IsBlocked(ip) {
		t.Error("IP should be blocked after flood detection")
	}
}

func TestProtection_FloodDetection_Disabled(t *testing.T) {
	filter := NewIPFilter(IPFilterConfig{Mode: "blocklist"})
	cfg := testProtectionConfig()
	cfg.Enabled = false
	p := NewProtection(cfg, filter)
	defer p.Stop()

	for i := 0; i < 100; i++ {
		if !p.RecordConnection("10.0.0.1") {
			t.Fatal("disabled protection should allow all connections")
		}
	}
}

// --- IsBlocked ---

func TestProtection_IsBlocked_NotTracked(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	if p.IsBlocked("never-seen") {
		t.Error("untracked IP should not be blocked")
	}
}

func TestProtection_IsBlocked_ExpiredBlock(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"
	// Trigger block
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}
	if !p.IsBlocked(ip) {
		t.Fatal("should be blocked after violations")
	}

	// Manually expire
	p.mu.Lock()
	p.tracked[ip].BlockedUntil = time.Now().Add(-1 * time.Second)
	p.mu.Unlock()

	if p.IsBlocked(ip) {
		t.Error("should not be blocked after expiry")
	}
}

// --- Unblock ---

func TestProtection_Unblock(t *testing.T) {
	p, filter := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	if !p.IsBlocked(ip) {
		t.Fatal("should be blocked")
	}

	p.Unblock(ip)

	if p.IsBlocked(ip) {
		t.Error("should not be blocked after Unblock")
	}

	// Should also be removed from IP filter
	if !filter.IsAllowed(ip) {
		t.Error("IP should be allowed in filter after Unblock")
	}
}

// --- GetBlocked ---

func TestProtection_GetBlocked(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	// Block two IPs
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		for i := 0; i < 5; i++ {
			p.RecordViolation(ip)
		}
	}

	blocked := p.GetBlocked()
	if len(blocked) != 2 {
		t.Fatalf("expected 2 blocked, got %d", len(blocked))
	}

	ips := map[string]bool{}
	for _, b := range blocked {
		ips[b.IP] = true
		if b.Reason != "auto-blocked" {
			t.Errorf("reason = %q, want %q", b.Reason, "auto-blocked")
		}
		if b.BlockCount != 1 {
			t.Errorf("block count = %d, want 1", b.BlockCount)
		}
	}
	if !ips["1.1.1.1"] || !ips["2.2.2.2"] {
		t.Error("missing expected blocked IPs")
	}
}

func TestProtection_GetBlocked_ExcludesExpired(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	for i := 0; i < 5; i++ {
		p.RecordViolation("1.1.1.1")
	}

	// Expire
	p.mu.Lock()
	p.tracked["1.1.1.1"].BlockedUntil = time.Now().Add(-1 * time.Second)
	p.mu.Unlock()

	blocked := p.GetBlocked()
	if len(blocked) != 0 {
		t.Errorf("expected 0 blocked (all expired), got %d", len(blocked))
	}
}

// --- Stats ---

func TestProtection_Stats(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	// 5 violations → 1 block
	for i := 0; i < 5; i++ {
		p.RecordViolation("10.0.0.1")
	}

	stats := p.Stats()
	if stats.CurrentlyBlocked != 1 {
		t.Errorf("CurrentlyBlocked = %d, want 1", stats.CurrentlyBlocked)
	}
	if stats.TotalBlocked != 1 {
		t.Errorf("TotalBlocked = %d, want 1", stats.TotalBlocked)
	}
	if stats.TotalViolations != 5 {
		t.Errorf("TotalViolations = %d, want 5", stats.TotalViolations)
	}
	if stats.TrackedIPs != 1 {
		t.Errorf("TrackedIPs = %d, want 1", stats.TrackedIPs)
	}
}

// --- IP filter integration ---

func TestProtection_AddsToIPFilter(t *testing.T) {
	p, filter := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	// Should be blocked in IP filter too (for middleware enforcement)
	if filter.IsAllowed(ip) {
		t.Error("blocked IP should be in IP filter blocklist")
	}
}

// --- Cleanup ---

func TestProtection_Cleanup_RemovesOldRecords(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	// Add a record and make it old
	p.mu.Lock()
	p.tracked["old-ip"] = &offenderRecord{
		LastViolation: time.Now().Add(-8 * 24 * time.Hour), // 8 days ago
		BlockedUntil:  time.Now().Add(-7 * 24 * time.Hour), // expired block
	}
	p.mu.Unlock()

	p.cleanup()

	p.mu.Lock()
	_, exists := p.tracked["old-ip"]
	p.mu.Unlock()

	if exists {
		t.Error("old record should be cleaned up")
	}
}

func TestProtection_Cleanup_KeepsActiveBlocks(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	p.cleanup()

	if !p.IsBlocked(ip) {
		t.Error("actively blocked IP should survive cleanup")
	}
}

// --- RecordConnection rejects blocked IP ---

func TestProtection_RecordConnection_RejectsBlocked(t *testing.T) {
	p, _ := newTestProtection()
	defer p.Stop()

	ip := "10.0.0.1"
	// Block via violations
	for i := 0; i < 5; i++ {
		p.RecordViolation(ip)
	}

	if p.RecordConnection(ip) {
		t.Error("connection from blocked IP should be rejected")
	}
}
