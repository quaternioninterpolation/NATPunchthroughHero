package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestTURN() *TURNGenerator {
	return &TURNGenerator{
		Secret: "test-secret-key",
		Host:   "turn.example.com",
		Port:   3478,
		TTL:    86400,
	}
}

func TestTURNGenerate_UsernameFormat(t *testing.T) {
	gen := newTestTURN()
	creds := gen.Generate("player42")

	parts := strings.SplitN(creds.Username, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("expected username format 'expiry:userID', got %q", creds.Username)
	}

	if parts[1] != "player42" {
		t.Errorf("userID part = %q, want %q", parts[1], "player42")
	}

	// Verify expiry is in the future
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("failed to parse expiry: %v", err)
	}
	now := time.Now().Unix()
	if expiry <= now {
		t.Errorf("expiry %d should be > now %d", expiry, now)
	}
	if expiry > now+86400+5 { // Allow 5s tolerance
		t.Errorf("expiry %d too far in future (now=%d, TTL=%d)", expiry, now, 86400)
	}
}

func TestTURNGenerate_PasswordIsValidHMAC(t *testing.T) {
	secret := "my-secret"
	gen := &TURNGenerator{
		Secret: secret,
		Host:   "turn.example.com",
		Port:   3478,
		TTL:    3600,
	}

	creds := gen.Generate("user1")

	// Manually verify the HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(creds.Username))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if creds.Password != expected {
		t.Errorf("password = %q, want %q", creds.Password, expected)
	}
}

func TestTURNGenerate_TTL(t *testing.T) {
	gen := newTestTURN()
	gen.TTL = 7200

	creds := gen.Generate("u1")
	if creds.TTL != 7200 {
		t.Errorf("TTL = %d, want 7200", creds.TTL)
	}
}

func TestTURNGenerate_URIs(t *testing.T) {
	gen := &TURNGenerator{
		Secret: "s",
		Host:   "relay.myserver.com",
		Port:   3479,
		TTL:    3600,
	}

	creds := gen.Generate("x")

	expectedTURN := "turn:relay.myserver.com:3479?transport=udp"
	expectedSTUN := "stun:relay.myserver.com:3479"

	if len(creds.URIs) != 2 {
		t.Fatalf("expected 2 URIs, got %d", len(creds.URIs))
	}
	if creds.URIs[0] != expectedTURN {
		t.Errorf("URI[0] = %q, want %q", creds.URIs[0], expectedTURN)
	}
	if creds.URIs[1] != expectedSTUN {
		t.Errorf("URI[1] = %q, want %q", creds.URIs[1], expectedSTUN)
	}
}

func TestTURNGenerate_DifferentUsersDifferentCreds(t *testing.T) {
	gen := newTestTURN()
	c1 := gen.Generate("userA")
	c2 := gen.Generate("userB")

	if c1.Username == c2.Username {
		t.Error("different users got same username")
	}
	if c1.Password == c2.Password {
		t.Error("different users got same password")
	}
}

func TestTURNGenerate_DifferentSecretsProduceDifferentPasswords(t *testing.T) {
	g1 := &TURNGenerator{Secret: "secret1", Host: "h", Port: 3478, TTL: 3600}
	g2 := &TURNGenerator{Secret: "secret2", Host: "h", Port: 3478, TTL: 3600}

	c1 := g1.Generate("testuser")
	c2 := g2.Generate("testuser")

	// Passwords should differ because secrets differ
	if c1.Password == c2.Password {
		t.Error("different secrets produced same password")
	}
}

func TestTURNGenerate_PasswordBase64Decodable(t *testing.T) {
	gen := newTestTURN()
	creds := gen.Generate("player1")

	decoded, err := base64.StdEncoding.DecodeString(creds.Password)
	if err != nil {
		t.Fatalf("password not valid base64: %v", err)
	}
	// SHA1 produces 20 bytes
	if len(decoded) != 20 {
		t.Errorf("decoded password length = %d, want 20 (SHA1)", len(decoded))
	}
}
