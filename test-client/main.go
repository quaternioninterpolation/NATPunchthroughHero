// Package main provides a CLI test client for NAT Punchthrough Hero.
//
// Usage:
//
//	go run . host -name "My Game" -server http://localhost:8080
//	go run . join -code ABC123 -server http://localhost:8080
//	go run . list -server http://localhost:8080
//	go run . health -server http://localhost:8080
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

var (
	serverURL string
	apiKey    string
	verbose   bool
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Global flags
	globalFlags := flag.NewFlagSet("global", flag.ContinueOnError)
	globalFlags.StringVar(&serverURL, "server", "http://localhost:8080", "Server base URL")
	globalFlags.StringVar(&apiKey, "key", "", "API key (optional)")
	globalFlags.BoolVar(&verbose, "v", false, "Verbose output")

	subcommand := os.Args[1]
	args := os.Args[2:]

	switch subcommand {
	case "host":
		cmdHost(args)
	case "join":
		cmdJoin(args)
	case "list":
		cmdList(args)
	case "health":
		cmdHealth(args)
	case "punch":
		cmdPunch(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`NAT Punchthrough Hero — Test Client

Usage:
  test-client <command> [flags]

Commands:
  host    Register as a game host and listen for joins
  join    Join a game by code or ID
  list    List available games
  health  Check server health
  punch   Test NAT punchthrough via signaling

Global flags:
  -server string   Server URL (default: http://localhost:8080)
  -key string      API key for authenticated endpoints
  -v               Verbose output

Examples:
  # Check server health
  test-client health -server http://localhost:8080

  # List games
  test-client list -server http://localhost:8080

  # Host a game
  test-client host -name "My Game" -max 4 -server http://localhost:8080

  # Join a game
  test-client join -code ABC123 -server http://localhost:8080

  # Test NAT punch signaling
  test-client punch -host -server http://localhost:8080
  test-client punch -join -game <game-id> -server http://localhost:8080`)
}

// ── Health Command ──────────────────────────────────────────

func cmdHealth(args []string) {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	fs.StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	fs.Parse(args)

	resp, err := http.Get(serverURL + "/api/health")
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 200 {
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		pretty, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println("✓ Server is healthy")
		fmt.Println(string(pretty))
	} else {
		fmt.Fprintf(os.Stderr, "✗ Server returned %d\n%s\n", resp.StatusCode, body)
		os.Exit(1)
	}
}

// ── List Command ────────────────────────────────────────────

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	fs.StringVar(&apiKey, "key", "", "API key")
	fs.Parse(args)

	req, _ := http.NewRequest("GET", serverURL+"/api/games", nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var games []map[string]interface{}
	if err := json.Unmarshal(body, &games); err != nil {
		fmt.Println(string(body))
		return
	}

	if len(games) == 0 {
		fmt.Println("No games currently hosted.")
		return
	}

	fmt.Printf("Found %d game(s):\n\n", len(games))
	for i, g := range games {
		fmt.Printf("  [%d] %s\n", i+1, g["name"])
		fmt.Printf("      ID:      %s\n", g["id"])
		fmt.Printf("      Code:    %s\n", g["join_code"])
		fmt.Printf("      Players: %.0f/%.0f\n", g["current_players"], g["max_players"])
		fmt.Printf("      NAT:     %s\n", g["nat_type"])
		fmt.Println()
	}
}

// ── Host Command ────────────────────────────────────────────

type HostResponse struct {
	ID        string `json:"id"`
	JoinCode  string `json:"join_code"`
	HostToken string `json:"host_token"`
}

func cmdHost(args []string) {
	fs := flag.NewFlagSet("host", flag.ExitOnError)
	fs.StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	fs.StringVar(&apiKey, "key", "", "API key")
	name := fs.String("name", "Test Game", "Game name")
	maxPlayers := fs.Int("max", 4, "Max players")
	natType := fs.String("nat", "unknown", "NAT type")
	heartbeatSec := fs.Int("heartbeat", 30, "Heartbeat interval (seconds)")
	fs.Parse(args)

	// Register the game
	payload := map[string]interface{}{
		"name":            *name,
		"max_players":     *maxPlayers,
		"current_players": 1,
		"nat_type":        *natType,
		"data":            map[string]string{"mode": "test"},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", serverURL+"/api/games", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Failed to register game: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		log.Fatalf("Failed to register: %d %s", resp.StatusCode, respBody)
	}

	var hostResp HostResponse
	json.Unmarshal(respBody, &hostResp)

	fmt.Println("✓ Game registered!")
	fmt.Printf("  ID:        %s\n", hostResp.ID)
	fmt.Printf("  Join Code: %s\n", hostResp.JoinCode)
	fmt.Printf("  Token:     %s\n", hostResp.HostToken)
	fmt.Println()
	fmt.Println("Sending heartbeats... Press Ctrl+C to stop and deregister.")
	fmt.Println()

	// Heartbeat loop
	ticker := time.NewTicker(time.Duration(*heartbeatSec) * time.Second)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	heartbeatURL := fmt.Sprintf("%s/api/games/%s/heartbeat", serverURL, hostResp.ID)

	go func() {
		for range ticker.C {
			hReq, _ := http.NewRequest("POST", heartbeatURL, nil)
			hReq.Header.Set("Authorization", "Bearer "+hostResp.HostToken)
			if apiKey != "" {
				hReq.Header.Set("X-API-Key", apiKey)
			}
			hResp, hErr := http.DefaultClient.Do(hReq)
			if hErr != nil {
				log.Printf("Heartbeat failed: %v", hErr)
				continue
			}
			hResp.Body.Close()
			if verbose {
				log.Printf("♥ Heartbeat sent (status %d)", hResp.StatusCode)
			} else {
				fmt.Print("♥ ")
			}
		}
	}()

	<-sigCh
	fmt.Println("\n\nDeregistering game...")

	// Delete the game
	dReq, _ := http.NewRequest("DELETE", serverURL+"/api/games/"+hostResp.ID, nil)
	dReq.Header.Set("Authorization", "Bearer "+hostResp.HostToken)
	if apiKey != "" {
		dReq.Header.Set("X-API-Key", apiKey)
	}
	dResp, dErr := http.DefaultClient.Do(dReq)
	if dErr != nil {
		log.Printf("Failed to deregister: %v", dErr)
		return
	}
	dResp.Body.Close()
	fmt.Printf("✓ Game deregistered (status %d)\n", dResp.StatusCode)
}

// ── Join Command ────────────────────────────────────────────

func cmdJoin(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	fs.StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	fs.StringVar(&apiKey, "key", "", "API key")
	code := fs.String("code", "", "Join code")
	gameID := fs.String("id", "", "Game ID (alternative to code)")
	fs.Parse(args)

	if *code == "" && *gameID == "" {
		log.Fatal("Provide -code or -id")
	}

	// If code provided, find the game
	if *code != "" && *gameID == "" {
		req, _ := http.NewRequest("GET", serverURL+"/api/games?code="+url.QueryEscape(*code), nil)
		if apiKey != "" {
			req.Header.Set("X-API-Key", apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("Failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		var games []map[string]interface{}
		json.Unmarshal(body, &games)
		if len(games) == 0 {
			log.Fatalf("No game found with code %s", *code)
		}
		*gameID = games[0]["id"].(string)
		fmt.Printf("✓ Found game: %s (%s)\n", games[0]["name"], *gameID)
	}

	// Request TURN credentials
	turnReq, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/games/%s/turn", serverURL, *gameID), nil)
	if apiKey != "" {
		turnReq.Header.Set("X-API-Key", apiKey)
	}
	turnResp, err := http.DefaultClient.Do(turnReq)
	if err != nil {
		log.Fatalf("Failed to get TURN credentials: %v", err)
	}
	defer turnResp.Body.Close()
	turnBody, _ := io.ReadAll(turnResp.Body)

	var turnCreds map[string]interface{}
	json.Unmarshal(turnBody, &turnCreds)

	fmt.Println("✓ TURN credentials received:")
	pretty, _ := json.MarshalIndent(turnCreds, "  ", "  ")
	fmt.Println("  " + string(pretty))
}

// ── Punch Test Command ──────────────────────────────────────

func cmdPunch(args []string) {
	fs := flag.NewFlagSet("punch", flag.ExitOnError)
	fs.StringVar(&serverURL, "server", "http://localhost:8080", "Server URL")
	fs.StringVar(&apiKey, "key", "", "API key")
	isHost := fs.Bool("host", false, "Act as host")
	isJoin := fs.Bool("join", false, "Act as joiner")
	gameID := fs.String("game", "", "Game ID (for joiner)")
	fs.BoolVar(&verbose, "v", false, "Verbose output")
	fs.Parse(args)

	if !*isHost && !*isJoin {
		log.Fatal("Specify -host or -join")
	}

	// Build WebSocket URL
	wsURL := strings.Replace(serverURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/ws/signaling"

	fmt.Printf("Connecting to %s...\n", wsURL)

	header := http.Header{}
	if apiKey != "" {
		header.Set("X-API-Key", apiKey)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		log.Fatalf("WebSocket connect failed: %v", err)
	}
	defer conn.Close()

	fmt.Println("✓ WebSocket connected")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	// Message reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					log.Printf("Read error: %v", err)
				}
				return
			}

			var parsed map[string]interface{}
			json.Unmarshal(msg, &parsed)

			msgType, _ := parsed["type"].(string)
			fmt.Printf("← %s", msgType)
			if verbose {
				pretty, _ := json.MarshalIndent(parsed, "  ", "  ")
				fmt.Printf(": %s", pretty)
			}
			fmt.Println()

			// Handle specific messages
			switch msgType {
			case "host_registered":
				fmt.Printf("  Registered as host (game: %s)\n", parsed["game_id"])

			case "gather_candidates":
				fmt.Println("  Server requests ICE candidate gathering")
				fmt.Printf("  Session: %s\n", parsed["session_id"])

			case "peer_candidate":
				fmt.Printf("  Peer candidate received: %s:%v\n", parsed["public_ip"], parsed["public_port"])

			case "punch_signal":
				fmt.Printf("  Punch signal from %s\n", parsed["from_peer"])

			case "turn_fallback":
				fmt.Println("  TURN relay credentials received")

			case "error":
				fmt.Printf("  ERROR: %s\n", parsed["message"])
			}
		}
	}()

	// Send initial message
	if *isHost {
		// First register a game via REST
		payload := map[string]interface{}{
			"name":            "Punch Test",
			"max_players":     2,
			"current_players": 1,
			"nat_type":        "unknown",
		}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", serverURL+"/api/games", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("X-API-Key", apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("Failed to register game: %v", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var hostResp HostResponse
		json.Unmarshal(respBody, &hostResp)
		fmt.Printf("✓ Game registered: %s (code: %s)\n", hostResp.ID, hostResp.JoinCode)

		// Register as host on WebSocket
		sendMsg(conn, map[string]interface{}{
			"type":       "register_host",
			"game_id":    hostResp.ID,
			"host_token": hostResp.HostToken,
		})

		fmt.Println("Waiting for joiners... (share the join code)")
		fmt.Printf("Join code: %s\n", hostResp.JoinCode)

	} else {
		// Join via WebSocket
		if *gameID == "" {
			log.Fatal("Provide -game <game-id> for join mode")
		}
		sendMsg(conn, map[string]interface{}{
			"type":    "request_join",
			"game_id": *gameID,
		})
		fmt.Println("Requesting to join game...")
	}

	// Wait for interrupt
	<-sigCh
	fmt.Println("\nDisconnecting...")
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	time.Sleep(500 * time.Millisecond)
}

func sendMsg(conn *websocket.Conn, msg map[string]interface{}) {
	data, _ := json.Marshal(msg)
	if verbose {
		fmt.Printf("→ %s\n", data)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("Send error: %v", err)
	}
}
