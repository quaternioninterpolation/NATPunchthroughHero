package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds all server configuration. Loaded from config.toml,
// overridable by environment variables, overridable by CLI flags.
type Config struct {
	// Server
	Port       int    `toml:"port"`
	ExternalIP string `toml:"external_ip"`

	// Domain & TLS
	Domain      string `toml:"domain"`
	TLSCertFile string `toml:"tls_cert_file"`
	TLSKeyFile  string `toml:"tls_key_file"`

	// TURN Relay
	TurnSecret       string `toml:"turn_secret"`
	TurnHost         string `toml:"turn_host"`
	TurnPort         int    `toml:"turn_port"`
	TurnRelayMin     int    `toml:"turn_relay_min"`
	TurnRelayMax     int    `toml:"turn_relay_max"`
	TurnQuota        int    `toml:"turn_quota"`
	TurnCredentialTTL int   `toml:"turn_credential_ttl"`

	// Dashboard
	AdminPassword   string `toml:"admin_password"`
	DashboardAccess string `toml:"dashboard_access"`

	// Security
	GameAPIKey string `toml:"game_api_key"`

	// Rate Limits
	RateLimit RateLimitConfig `toml:"rate_limit"`

	// IP Filter
	IPFilter IPFilterConfig `toml:"ip_filter"`

	// Protection
	Protection ProtectionConfig `toml:"protection"`

	// Limits
	MaxGames    int `toml:"max_games"`
	GameTimeout int `toml:"game_timeout"`

	// Network
	TrustedProxies []string `toml:"trusted_proxies"`
	AllowedOrigins []string `toml:"allowed_origins"`
}

// RateLimitConfig controls per-IP and global rate limits.
type RateLimitConfig struct {
	Enabled        bool `toml:"enabled"`
	GlobalRPS      int  `toml:"global_rps"`
	PerIPRPM       int  `toml:"per_ip_rpm"`
	PerIPBurst     int  `toml:"per_ip_burst"`
	WSPerIPRPM     int  `toml:"ws_per_ip_rpm"`
	WSPerIPMax     int  `toml:"ws_per_ip_max"`
	GamesPerIPRPH  int  `toml:"games_per_ip_rph"`
	JoinsPerIPRPM  int  `toml:"joins_per_ip_rpm"`
	TurnPerIPRPH   int  `toml:"turn_per_ip_rph"`
}

// IPFilterConfig controls IP allowlisting and blocklisting.
type IPFilterConfig struct {
	Mode      string   `toml:"mode"`
	Blocklist []string `toml:"blocklist"`
	Allowlist []string `toml:"allowlist"`
}

// ProtectionConfig controls automatic abuse detection and blocking.
type ProtectionConfig struct {
	Enabled             bool   `toml:"enabled"`
	AutoBlock           bool   `toml:"auto_block"`
	AutoBlockThreshold  int    `toml:"auto_block_threshold"`
	AutoBlockDuration   string `toml:"auto_block_duration"`
	AutoBlockEscalation bool   `toml:"auto_block_escalation"`

	FloodConnections   int    `toml:"flood_connections"`
	FloodWindow        string `toml:"flood_window"`
	FloodBlockDuration string `toml:"flood_block_duration"`

	SlowRequestTimeout string `toml:"slow_request_timeout"`
	SlowRequestMax     int    `toml:"slow_request_max"`

	MaxRequestBody int `toml:"max_request_body"`
	MaxWSMessage   int `toml:"max_ws_message"`

	LogBlocked bool `toml:"log_blocked"`
}

// DefaultConfig returns a Config with sane defaults. Everything works
// out of the box for local development with no config file.
func DefaultConfig() *Config {
	return &Config{
		Port:              8080,
		ExternalIP:        "auto",
		TurnHost:          "auto",
		TurnPort:          3478,
		TurnRelayMin:      49152,
		TurnRelayMax:      50175,
		TurnQuota:         300,
		TurnCredentialTTL: 3600,
		DashboardAccess:   "auto",
		MaxGames:          500,
		GameTimeout:       90,
		RateLimit: RateLimitConfig{
			Enabled:       true,
			GlobalRPS:     100,
			PerIPRPM:      60,
			PerIPBurst:    10,
			WSPerIPRPM:    5,
			WSPerIPMax:    3,
			GamesPerIPRPH: 10,
			JoinsPerIPRPM: 20,
			TurnPerIPRPH:  10,
		},
		IPFilter: IPFilterConfig{
			Mode: "blocklist",
		},
		Protection: ProtectionConfig{
			Enabled:             true,
			AutoBlock:           true,
			AutoBlockThreshold:  5,
			AutoBlockDuration:   "1h",
			AutoBlockEscalation: true,
			FloodConnections:    50,
			FloodWindow:         "10s",
			FloodBlockDuration:  "1h",
			SlowRequestTimeout:  "10s",
			SlowRequestMax:      5,
			MaxRequestBody:      16384,  // 16KB
			MaxWSMessage:        131072, // 128KB (increased to support chat and file transfer chunks)
			LogBlocked:          true,
		},
	}
}

// LoadConfig loads configuration from config.toml (if present),
// then applies environment variable overrides.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	// Try to read config file
	if path == "" {
		path = "config.toml"
	}
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", path, err)
		}
	}

	// Environment variable overrides (highest priority)
	applyEnvOverrides(cfg)

	// Auto-generate secrets if empty
	if cfg.TurnSecret == "" {
		cfg.TurnSecret = generateSecret(32)
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = generateSecret(8)
	}
	if cfg.GameAPIKey == "" {
		cfg.GameAPIKey = generateSecret(16)
	}

	// Resolve "auto" external IP
	if cfg.ExternalIP == "auto" {
		ip, err := detectExternalIP()
		if err != nil {
			cfg.ExternalIP = "127.0.0.1"
		} else {
			cfg.ExternalIP = ip
		}
	}

	// Resolve "auto" TURN host
	if cfg.TurnHost == "auto" {
		cfg.TurnHost = cfg.ExternalIP
	}

	// Resolve "auto" dashboard access
	if cfg.DashboardAccess == "auto" {
		if cfg.Domain != "" {
			cfg.DashboardAccess = "public"
		} else {
			cfg.DashboardAccess = "local"
		}
	}

	return cfg, nil
}

// SaveConfig writes the current config to a TOML file.
func SaveConfig(cfg *Config, path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return nil
}

// applyEnvOverrides reads environment variables and overrides config values.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("EXTERNAL_IP"); v != "" {
		cfg.ExternalIP = v
	}
	if v := os.Getenv("DOMAIN"); v != "" {
		cfg.Domain = v
	}
	if v := os.Getenv("TLS_CERT_FILE"); v != "" {
		cfg.TLSCertFile = v
	}
	if v := os.Getenv("TLS_KEY_FILE"); v != "" {
		cfg.TLSKeyFile = v
	}
	if v := os.Getenv("TURN_SECRET"); v != "" {
		cfg.TurnSecret = v
	}
	if v := os.Getenv("TURN_HOST"); v != "" {
		cfg.TurnHost = v
	}
	if v := os.Getenv("TURN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.TurnPort = n
		}
	}
	if v := os.Getenv("ADMIN_PASSWORD"); v != "" {
		cfg.AdminPassword = v
	}
	if v := os.Getenv("GAME_API_KEY"); v != "" {
		cfg.GameAPIKey = v
	}
	if v := os.Getenv("MAX_GAMES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxGames = n
		}
	}
	if v := os.Getenv("GAME_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.GameTimeout = n
		}
	}
	if v := os.Getenv("DASHBOARD_ACCESS"); v != "" {
		cfg.DashboardAccess = v
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		cfg.TrustedProxies = strings.Split(v, ",")
	}
}

// detectExternalIP discovers the server's public IP address.
func detectExternalIP() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	// Try multiple services for reliability
	services := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	for _, url := range services {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		if err != nil {
			continue
		}

		ip := strings.TrimSpace(string(body))
		if ip != "" {
			return ip, nil
		}
	}

	return "", fmt.Errorf("could not detect external IP from any service")
}

// generateSecret generates a random hex-encoded secret of the given byte length.
func generateSecret(byteLen int) string {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		// Fallback: this should never happen
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ParseDuration parses a duration string like "1h", "30m", "10s".
// Wraps time.ParseDuration but accepts our config format.
func ParseDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}
