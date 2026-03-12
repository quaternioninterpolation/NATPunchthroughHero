package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// CheckResult describes the result of a single diagnostic check.
type CheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "fail"
	Message string `json:"message"`
}

// RunAllChecks performs all diagnostic checks and returns results.
func RunAllChecks(cfg *Config) []CheckResult {
	var results []CheckResult

	results = append(results, checkExternalIP(cfg))
	results = append(results, checkPort("API", "tcp", cfg.Port))
	results = append(results, checkPort("STUN/TURN", "udp", cfg.TurnPort))
	results = append(results, checkDNS(cfg))
	results = append(results, checkHTTPPorts(cfg))

	return results
}

// checkExternalIP verifies the external IP can be detected.
func checkExternalIP(cfg *Config) CheckResult {
	if cfg.ExternalIP != "" && cfg.ExternalIP != "auto" && cfg.ExternalIP != "127.0.0.1" {
		return CheckResult{
			Name:    "External IP",
			Status:  "ok",
			Message: cfg.ExternalIP,
		}
	}

	ip, err := detectExternalIP()
	if err != nil {
		return CheckResult{
			Name:    "External IP",
			Status:  "warn",
			Message: "Could not detect: " + err.Error(),
		}
	}

	return CheckResult{
		Name:    "External IP",
		Status:  "ok",
		Message: ip,
	}
}

// checkPort tests if a port is available for binding.
func checkPort(name, proto string, port int) CheckResult {
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	switch proto {
	case "tcp":
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return CheckResult{
				Name:    name + " Port",
				Status:  "warn",
				Message: fmt.Sprintf("Port %d/tcp may be in use: %v", port, err),
			}
		}
		ln.Close()
	case "udp":
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			return CheckResult{
				Name:    name + " Port",
				Status:  "warn",
				Message: fmt.Sprintf("Port %d/udp may be in use: %v", port, err),
			}
		}
		conn.Close()
	}

	return CheckResult{
		Name:    name + " Port",
		Status:  "ok",
		Message: fmt.Sprintf("Port %d/%s available", port, proto),
	}
}

// checkDNS verifies that a domain points to the server's external IP.
func checkDNS(cfg *Config) CheckResult {
	if cfg.Domain == "" {
		return CheckResult{
			Name:    "DNS",
			Status:  "ok",
			Message: "No domain configured (HTTP mode)",
		}
	}

	ips, err := net.LookupHost(cfg.Domain)
	if err != nil {
		return CheckResult{
			Name:    "DNS",
			Status:  "fail",
			Message: fmt.Sprintf("DNS lookup failed for %s: %v", cfg.Domain, err),
		}
	}

	for _, ip := range ips {
		if ip == cfg.ExternalIP {
			return CheckResult{
				Name:    "DNS",
				Status:  "ok",
				Message: fmt.Sprintf("%s → %s ✓", cfg.Domain, cfg.ExternalIP),
			}
		}
	}

	return CheckResult{
		Name:    "DNS",
		Status:  "fail",
		Message: fmt.Sprintf("%s points to %s, expected %s", cfg.Domain, strings.Join(ips, ", "), cfg.ExternalIP),
	}
}

// checkHTTPPorts verifies ports 80 and 443 are available when domain is set.
func checkHTTPPorts(cfg *Config) CheckResult {
	if cfg.Domain == "" {
		return CheckResult{
			Name:    "TLS Ports",
			Status:  "ok",
			Message: "Not needed (no domain)",
		}
	}

	var issues []string

	ln80, err := net.Listen("tcp", ":80")
	if err != nil {
		issues = append(issues, fmt.Sprintf("Port 80: %v", err))
	} else {
		ln80.Close()
	}

	ln443, err := net.Listen("tcp", ":443")
	if err != nil {
		issues = append(issues, fmt.Sprintf("Port 443: %v", err))
	} else {
		ln443.Close()
	}

	if len(issues) > 0 {
		return CheckResult{
			Name:    "TLS Ports",
			Status:  "warn",
			Message: strings.Join(issues, "; "),
		}
	}

	return CheckResult{
		Name:    "TLS Ports",
		Status:  "ok",
		Message: "Ports 80 and 443 available",
	}
}

// checkCoturnReachable tests if coturn is responding to STUN binding requests.
func checkCoturnReachable(host string, port int) CheckResult {
	addr := fmt.Sprintf("%s:%d", host, port)

	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return CheckResult{
			Name:    "coturn",
			Status:  "fail",
			Message: fmt.Sprintf("Cannot reach coturn at %s: %v", addr, err),
		}
	}
	defer conn.Close()

	// Send a minimal STUN binding request (RFC 5389)
	// Message Type: Binding Request (0x0001)
	// Message Length: 0
	// Magic Cookie: 0x2112A442
	// Transaction ID: 12 random bytes
	stunReq := []byte{
		0x00, 0x01, // Type: Binding Request
		0x00, 0x00, // Length: 0
		0x21, 0x12, 0xA4, 0x42, // Magic Cookie
		0x01, 0x02, 0x03, 0x04, // Transaction ID (12 bytes)
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C,
	}

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write(stunReq)
	if err != nil {
		return CheckResult{
			Name:    "coturn",
			Status:  "fail",
			Message: fmt.Sprintf("Failed to send STUN request: %v", err),
		}
	}

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		return CheckResult{
			Name:    "coturn",
			Status:  "warn",
			Message: fmt.Sprintf("coturn at %s did not respond (may not be running yet)", addr),
		}
	}

	// Check if response is a STUN binding response (0x0101)
	if n >= 2 && buf[0] == 0x01 && buf[1] == 0x01 {
		return CheckResult{
			Name:    "coturn",
			Status:  "ok",
			Message: fmt.Sprintf("coturn responding at %s", addr),
		}
	}

	return CheckResult{
		Name:    "coturn",
		Status:  "warn",
		Message: fmt.Sprintf("Unexpected response from %s", addr),
	}
}

// CheckCoturnFromHTTP performs a coturn reachability check (for health endpoint).
func CheckCoturnFromHTTP(cfg *Config) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	addr := fmt.Sprintf("%s:%d", cfg.TurnHost, cfg.TurnPort)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// isPrivateIP checks if an IP is in a private/reserved range.
func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"fc00::/7",
		"::1/128",
	}

	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// checkFirewallPort attempts an external reachability check.
// This is a best-effort check using an HTTP callback.
func checkFirewallPort(externalIP string, port int) CheckResult {
	// Try to make an HTTP request to ourselves from outside
	url := fmt.Sprintf("http://%s:%d/api/health", externalIP, port)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return CheckResult{
			Name:    "Firewall",
			Status:  "warn",
			Message: fmt.Sprintf("Port %d may not be reachable externally (could not self-check)", port),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return CheckResult{
			Name:    "Firewall",
			Status:  "ok",
			Message: fmt.Sprintf("Port %d reachable from external IP", port),
		}
	}

	return CheckResult{
		Name:    "Firewall",
		Status:  "warn",
		Message: fmt.Sprintf("Got status %d checking external reachability", resp.StatusCode),
	}
}
