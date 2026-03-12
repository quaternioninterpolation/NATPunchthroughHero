package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- DefaultConfig ---

func TestDefaultConfig_Values(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.ExternalIP != "auto" {
		t.Errorf("ExternalIP = %q, want %q", cfg.ExternalIP, "auto")
	}
	if cfg.TurnPort != 3478 {
		t.Errorf("TurnPort = %d, want 3478", cfg.TurnPort)
	}
	if cfg.MaxGames != 500 {
		t.Errorf("MaxGames = %d, want 500", cfg.MaxGames)
	}
	if cfg.GameTimeout != 30 {
		t.Errorf("GameTimeout = %d, want 30", cfg.GameTimeout)
	}
	if cfg.TurnCredentialTTL != 3600 {
		t.Errorf("TurnCredentialTTL = %d, want 3600", cfg.TurnCredentialTTL)
	}
	if !cfg.RateLimit.Enabled {
		t.Error("RateLimit.Enabled should be true by default")
	}
	if cfg.RateLimit.GlobalRPS != 100 {
		t.Errorf("RateLimit.GlobalRPS = %d, want 100", cfg.RateLimit.GlobalRPS)
	}
	if !cfg.Protection.Enabled {
		t.Error("Protection.Enabled should be true by default")
	}
	if cfg.Protection.AutoBlockThreshold != 5 {
		t.Errorf("Protection.AutoBlockThreshold = %d, want 5", cfg.Protection.AutoBlockThreshold)
	}
	if cfg.IPFilter.Mode != "blocklist" {
		t.Errorf("IPFilter.Mode = %q, want %q", cfg.IPFilter.Mode, "blocklist")
	}
}

// --- ParseDuration ---

func TestParseDuration_Seconds(t *testing.T) {
	d, err := ParseDuration("10s")
	if err != nil {
		t.Fatal(err)
	}
	if d != 10*time.Second {
		t.Errorf("got %v, want 10s", d)
	}
}

func TestParseDuration_Minutes(t *testing.T) {
	d, err := ParseDuration("5m")
	if err != nil {
		t.Fatal(err)
	}
	if d != 5*time.Minute {
		t.Errorf("got %v, want 5m", d)
	}
}

func TestParseDuration_Hours(t *testing.T) {
	d, err := ParseDuration("1h")
	if err != nil {
		t.Fatal(err)
	}
	if d != 1*time.Hour {
		t.Errorf("got %v, want 1h", d)
	}
}

func TestParseDuration_Complex(t *testing.T) {
	d, err := ParseDuration("1h30m")
	if err != nil {
		t.Fatal(err)
	}
	if d != 90*time.Minute {
		t.Errorf("got %v, want 1h30m", d)
	}
}

func TestParseDuration_Invalid(t *testing.T) {
	_, err := ParseDuration("invalid")
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}

// --- generateSecret ---

func TestGenerateSecret_Length(t *testing.T) {
	s := generateSecret(16)
	// 16 bytes → 32 hex chars
	if len(s) != 32 {
		t.Errorf("len = %d, want 32 for 16-byte secret", len(s))
	}

	s = generateSecret(32)
	if len(s) != 64 {
		t.Errorf("len = %d, want 64 for 32-byte secret", len(s))
	}
}

func TestGenerateSecret_Unique(t *testing.T) {
	s1 := generateSecret(16)
	s2 := generateSecret(16)
	if s1 == s2 {
		t.Error("two secrets should not be identical")
	}
}

func TestGenerateSecret_HexEncoded(t *testing.T) {
	s := generateSecret(8)
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in secret %q", string(c), s)
			break
		}
	}
}

// --- SaveConfig / LoadConfig round-trip ---

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := DefaultConfig()
	original.Port = 9999
	original.ExternalIP = "1.2.3.4"
	original.TurnSecret = "my-test-secret"
	original.TurnHost = "1.2.3.4"
	original.TurnPort = 3479
	original.MaxGames = 200
	original.GameTimeout = 60
	original.GameAPIKey = "test-api-key"
	original.AdminPassword = "admin-pass"
	original.DashboardAccess = "local"

	if err := SaveConfig(original, path); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("config file not created")
	}

	// Override the auto-detect behavior by setting env vars
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if loaded.Port != 9999 {
		t.Errorf("Port = %d, want 9999", loaded.Port)
	}
	if loaded.ExternalIP != "1.2.3.4" {
		t.Errorf("ExternalIP = %q, want %q", loaded.ExternalIP, "1.2.3.4")
	}
	if loaded.TurnSecret != "my-test-secret" {
		t.Errorf("TurnSecret = %q, want %q", loaded.TurnSecret, "my-test-secret")
	}
	if loaded.MaxGames != 200 {
		t.Errorf("MaxGames = %d, want 200", loaded.MaxGames)
	}
}

// --- LoadConfig with missing file ---

func TestLoadConfig_MissingFile_UsesDefaults(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadConfig should not error on missing file: %v", err)
	}

	// Should be defaults
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (default)", cfg.Port)
	}
}

// --- Environment variable overrides ---

func TestApplyEnvOverrides_Port(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("PORT", "3000")
	applyEnvOverrides(cfg)

	if cfg.Port != 3000 {
		t.Errorf("Port = %d, want 3000", cfg.Port)
	}
}

func TestApplyEnvOverrides_ExternalIP(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("EXTERNAL_IP", "5.6.7.8")
	applyEnvOverrides(cfg)

	if cfg.ExternalIP != "5.6.7.8" {
		t.Errorf("ExternalIP = %q, want %q", cfg.ExternalIP, "5.6.7.8")
	}
}

func TestApplyEnvOverrides_TurnSecret(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("TURN_SECRET", "env-secret")
	applyEnvOverrides(cfg)

	if cfg.TurnSecret != "env-secret" {
		t.Errorf("TurnSecret = %q, want %q", cfg.TurnSecret, "env-secret")
	}
}

func TestApplyEnvOverrides_MaxGames(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("MAX_GAMES", "1000")
	applyEnvOverrides(cfg)

	if cfg.MaxGames != 1000 {
		t.Errorf("MaxGames = %d, want 1000", cfg.MaxGames)
	}
}

func TestApplyEnvOverrides_TrustedProxies(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("TRUSTED_PROXIES", "10.0.0.1,10.0.0.2")
	applyEnvOverrides(cfg)

	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies len = %d, want 2", len(cfg.TrustedProxies))
	}
	if cfg.TrustedProxies[0] != "10.0.0.1" {
		t.Errorf("TrustedProxies[0] = %q, want %q", cfg.TrustedProxies[0], "10.0.0.1")
	}
}

func TestApplyEnvOverrides_DomainOverride(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("DOMAIN", "example.com")
	applyEnvOverrides(cfg)

	if cfg.Domain != "example.com" {
		t.Errorf("Domain = %q, want %q", cfg.Domain, "example.com")
	}
}

func TestApplyEnvOverrides_GameAPIKey(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("GAME_API_KEY", "my-key")
	applyEnvOverrides(cfg)

	if cfg.GameAPIKey != "my-key" {
		t.Errorf("GameAPIKey = %q, want %q", cfg.GameAPIKey, "my-key")
	}
}

func TestApplyEnvOverrides_InvalidPortIgnored(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("PORT", "not-a-number")
	applyEnvOverrides(cfg)

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (should be unchanged)", cfg.Port)
	}
}

// --- LoadConfig auto-generates secrets ---

func TestLoadConfig_AutoGeneratesSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write minimal config without secrets, but with fixed ExternalIP
	content := `port = 8080
external_ip = "127.0.0.1"
turn_host = "127.0.0.1"
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.TurnSecret == "" {
		t.Error("TurnSecret should be auto-generated")
	}
	if cfg.AdminPassword == "" {
		t.Error("AdminPassword should be auto-generated")
	}
	if cfg.GameAPIKey == "" {
		t.Error("GameAPIKey should be auto-generated")
	}
}

// --- SaveConfig file permissions ---

func TestSaveConfig_FileCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-config.toml")

	cfg := DefaultConfig()
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("config file is empty")
	}
}
