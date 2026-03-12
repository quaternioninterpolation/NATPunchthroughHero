package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RunSetupWizard runs the interactive CLI setup wizard.
// It detects the environment, asks minimal questions, generates
// secrets, writes config.toml, and optionally starts the server.
//
// Usage: ./server setup [--config path/to/config.toml]
func RunSetupWizard(configPath string) {
	reader := bufio.NewReader(os.Stdin)

	printBanner()

	fmt.Println("Detecting your environment...")

	// Detect external IP
	ip, err := detectExternalIP()
	if err != nil {
		ip = "127.0.0.1"
		printCheck("warn", "Public IP", "Could not detect (using 127.0.0.1)")
	} else {
		printCheck("ok", "Public IP", ip)
	}

	// Check if common ports are available
	apiResult := checkPort("API", "tcp", 8080)
	printCheckResult(apiResult)

	turnResult := checkPort("STUN/TURN", "udp", 3478)
	printCheckResult(turnResult)

	fmt.Println()

	// Step 1: Mode
	fmt.Println("── Step 1: Mode ──────────────────────────")
	fmt.Println("How will you run this server?")
	fmt.Println("  [1] Local / LAN only (no domain, HTTP)")
	fmt.Println("  [2] Public server with domain + HTTPS")
	fmt.Println("  [3] Public server, no domain (HTTP only)")
	fmt.Println()

	mode := promptChoice(reader, "> ", 1, 3, 1)

	cfg := DefaultConfig()
	cfg.ExternalIP = ip

	switch mode {
	case 1:
		cfg.ExternalIP = "127.0.0.1"
		cfg.DashboardAccess = "public" // Safe on localhost
		fmt.Println()
		printCheck("ok", "Mode", "Local/LAN — no domain needed")

	case 2:
		fmt.Println()
		fmt.Println("── Step 2: Domain ────────────────────────")
		domain := promptString(reader, "Enter your domain: ")
		cfg.Domain = domain
		cfg.DashboardAccess = "public"

		// Check DNS
		fmt.Println()
		fmt.Printf("Checking DNS for %s...\n", domain)
		dnsResult := checkDNS(cfg)
		printCheckResult(dnsResult)

		if dnsResult.Status == "fail" {
			fmt.Println()
			fmt.Println("  → Go to your DNS provider and create an A record:")
			fmt.Printf("    %s → %s\n", domain, ip)
			fmt.Println("  → Then re-run this setup.")
			fmt.Println()
			if !promptYesNo(reader, "Continue anyway? [y/N] ", false) {
				fmt.Println("Setup cancelled.")
				return
			}
		}

		// Check TLS ports
		fmt.Println()
		fmt.Println("── Step 3: Firewall ──────────────────────")
		fmt.Println("Checking required ports...")
		httpResult := checkHTTPPorts(cfg)
		printCheckResult(httpResult)

		if httpResult.Status != "ok" {
			fmt.Println()
			fmt.Println("  → Port 80 is needed for Let's Encrypt certificate issuance.")
			fmt.Println("  → Port 443 is needed for HTTPS.")
			fmt.Println("  → You may need to run: sudo ufw allow 80/tcp && sudo ufw allow 443/tcp")
		}

	case 3:
		cfg.DashboardAccess = "local"
		fmt.Println()
		printCheck("ok", "Mode", "Public HTTP — dashboard local-only for security")
	}

	fmt.Println()

	// Step: Port customization
	fmt.Println("── Port Configuration ────────────────────")
	fmt.Printf("API port [%d]: ", cfg.Port)
	if p := promptIntOptional(reader, cfg.Port); p > 0 {
		cfg.Port = p
	}
	fmt.Printf("TURN port [%d]: ", cfg.TurnPort)
	if p := promptIntOptional(reader, cfg.TurnPort); p > 0 {
		cfg.TurnPort = p
	}
	fmt.Println()

	// Generate secrets
	fmt.Println("── Secrets ───────────────────────────────")
	cfg.TurnSecret = generateSecret(32)
	cfg.AdminPassword = generateSecret(8)
	cfg.GameAPIKey = generateSecret(16)

	printCheck("ok", "TURN Secret", "Generated (stored in config file)")
	printCheck("ok", "Admin Password", cfg.AdminPassword+" ← save this!")
	printCheck("ok", "Game API Key", cfg.GameAPIKey[:8]+"... (full key in config file)")

	fmt.Println()

	// Summary
	fmt.Println("── Summary ───────────────────────────────")
	if cfg.Domain != "" {
		fmt.Printf("  Mode:           HTTPS (Let's Encrypt)\n")
		fmt.Printf("  Domain:         %s\n", cfg.Domain)
		fmt.Printf("  Dashboard:      https://%s/admin\n", cfg.Domain)
		fmt.Printf("  API:            https://%s/api/games\n", cfg.Domain)
	} else if cfg.ExternalIP == "127.0.0.1" {
		fmt.Printf("  Mode:           Local/LAN\n")
		fmt.Printf("  Dashboard:      http://localhost:%d/admin\n", cfg.Port)
		fmt.Printf("  API:            http://localhost:%d/api/games\n", cfg.Port)
	} else {
		fmt.Printf("  Mode:           Public HTTP\n")
		fmt.Printf("  Dashboard:      http://%s:%d/admin (local access only)\n", cfg.ExternalIP, cfg.Port)
		fmt.Printf("  API:            http://%s:%d/api/games\n", cfg.ExternalIP, cfg.Port)
	}
	fmt.Printf("  Public IP:      %s\n", cfg.ExternalIP)
	fmt.Printf("  API Port:       %d\n", cfg.Port)
	fmt.Printf("  TURN Port:      %d\n", cfg.TurnPort)
	fmt.Printf("  Admin Password: %s\n", cfg.AdminPassword)
	fmt.Printf("  Config file:    %s\n", configPath)

	fmt.Println()

	if !promptYesNo(reader, "Write config and continue? [Y/n] ", true) {
		fmt.Println("Setup cancelled.")
		return
	}

	// Write config
	if err := SaveConfig(cfg, configPath); err != nil {
		fmt.Printf("Error writing config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n  ✓ Config written to %s\n", configPath)
	fmt.Println()

	if cfg.Domain != "" {
		fmt.Println("To start the server:")
		fmt.Println("  docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d")
	} else {
		fmt.Println("To start the server:")
		fmt.Println("  docker compose up -d")
	}
	fmt.Println()
	fmt.Println("Or without Docker:")
	fmt.Printf("  ./server serve --config %s\n", configPath)
	fmt.Println()

	// Unity client configuration
	fmt.Println("── Unity Client Configuration ────────────")
	if cfg.Domain != "" {
		fmt.Printf("  Server URL: https://%s\n", cfg.Domain)
	} else if cfg.ExternalIP != "127.0.0.1" {
		fmt.Printf("  Server URL: http://%s:%d\n", cfg.ExternalIP, cfg.Port)
	} else {
		fmt.Printf("  Server URL: http://localhost:%d\n", cfg.Port)
	}
	fmt.Printf("  API Key:    %s\n", cfg.GameAPIKey)
	fmt.Println()
}

// --- Prompt Helpers ---

func printBanner() {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   NAT Punchthrough Hero — Setup          ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
}

func printCheck(status, name, message string) {
	icon := "✓"
	switch status {
	case "warn":
		icon = "!"
	case "fail":
		icon = "✗"
	}
	fmt.Printf("  %s %s: %s\n", icon, name, message)
}

func printCheckResult(r CheckResult) {
	printCheck(r.Status, r.Name, r.Message)
}

func promptString(reader *bufio.Reader, prompt string) string {
	fmt.Print(prompt)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptChoice(reader *bufio.Reader, prompt string, min, max, defaultVal int) int {
	for {
		fmt.Print(prompt)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultVal
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < min || n > max {
			fmt.Printf("  Please enter a number between %d and %d.\n", min, max)
			continue
		}
		return n
	}
}

func promptIntOptional(reader *bufio.Reader, defaultVal int) int {
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		return defaultVal
	}
	return n
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	fmt.Print(prompt)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}
