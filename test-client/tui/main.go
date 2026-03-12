// Package main provides an interactive terminal UI for the NAT Punchthrough Hero test client.
// It manages server connection state, API keys, and provides a menu-driven interface
// for all test-client operations.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// ── ANSI Colors ─────────────────────────────────────────────

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[97m"
	bgBlue  = "\033[44m"
)

// ── State ───────────────────────────────────────────────────

type AppState struct {
	ServerURL string
	APIKey    string
	GameID    string
	JoinCode  string
	HostToken string
	GameName  string
	Connected bool // server reachable
	Hosting   bool // currently hosting a game
	WSConn    *websocket.Conn
	mu        sync.Mutex
}

var state = &AppState{
	ServerURL: "http://localhost:8080",
}

var reader = bufio.NewReader(os.Stdin)

// ── Main ────────────────────────────────────────────────────

func main() {
	clearScreen()
	printBanner()

	// Try to connect on startup
	checkHealth()

	for {
		printMenu()
		choice := prompt(cyan + "  ▸ " + reset)

		switch strings.TrimSpace(choice) {
		case "1":
			configureServer()
		case "2":
			healthCheck()
		case "3":
			listGames()
		case "4":
			hostGame()
		case "5":
			joinGame()
		case "6":
			punchTest()
		case "7":
			showState()
		case "0", "q", "quit", "exit":
			cleanup()
			printColored(green, "\n  Goodbye! 👋\n\n")
			os.Exit(0)
		default:
			printColored(red, "  Invalid choice.\n")
		}
	}
}

// ── Banner ──────────────────────────────────────────────────

func printBanner() {
	fmt.Println()
	fmt.Println(cyan + bold + "  ╔══════════════════════════════════════════════════╗" + reset)
	fmt.Println(cyan + bold + "  ║" + reset + white + bold + "       NAT Punchthrough Hero — Test Client       " + cyan + bold + "║" + reset)
	fmt.Println(cyan + bold + "  ║" + reset + dim + "           Interactive Testing Console             " + cyan + bold + "║" + reset)
	fmt.Println(cyan + bold + "  ╚══════════════════════════════════════════════════╝" + reset)
	fmt.Println()
}

// ── Menu ────────────────────────────────────────────────────

func printMenu() {
	fmt.Println()
	statusLine := state.ServerURL
	if state.Connected {
		statusLine += green + " ● connected" + reset
	} else {
		statusLine += red + " ● disconnected" + reset
	}
	if state.APIKey != "" {
		statusLine += yellow + " 🔑" + reset
	}
	if state.Hosting {
		statusLine += magenta + " 🎮 hosting" + reset
	}
	fmt.Println(dim + "  ─────────────────────────────────────────────────" + reset)
	fmt.Println("  " + dim + "Server:" + reset + " " + statusLine)
	if state.Hosting {
		fmt.Printf("  "+dim+"Game:"+reset+"   %s "+dim+"(code: %s)"+reset+"\n", state.GameName, state.JoinCode)
	}
	fmt.Println(dim + "  ─────────────────────────────────────────────────" + reset)
	fmt.Println()
	fmt.Println(bold + "  Configuration" + reset)
	fmt.Println("    " + cyan + "1" + reset + "  Configure server URL & API key")
	fmt.Println()
	fmt.Println(bold + "  Server" + reset)
	fmt.Println("    " + cyan + "2" + reset + "  Health check")
	fmt.Println("    " + cyan + "3" + reset + "  List games")
	fmt.Println()
	fmt.Println(bold + "  Play" + reset)
	fmt.Println("    " + cyan + "4" + reset + "  Host a game")
	fmt.Println("    " + cyan + "5" + reset + "  Join a game")
	fmt.Println("    " + cyan + "6" + reset + "  NAT punch test (WebSocket)")
	fmt.Println()
	fmt.Println(bold + "  Other" + reset)
	fmt.Println("    " + cyan + "7" + reset + "  Show current state")
	fmt.Println("    " + cyan + "0" + reset + "  Quit")
	fmt.Println()
}

// ── 1. Configure ────────────────────────────────────────────

func configureServer() {
	printHeader("Configure Connection")

	fmt.Printf("  Current server: %s\n", state.ServerURL)
	input := prompt("  New server URL (enter to keep): ")
	if input != "" {
		// Basic validation
		if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
			input = "http://" + input
		}
		state.ServerURL = strings.TrimRight(input, "/")
		printColored(green, "  ✓ Server set to "+state.ServerURL+"\n")
	}

	if state.APIKey != "" {
		fmt.Printf("  Current API key: %s...%s\n", state.APIKey[:4], state.APIKey[len(state.APIKey)-4:])
	} else {
		fmt.Println("  No API key set.")
	}
	input = prompt("  API key (enter to keep, 'clear' to remove): ")
	if input == "clear" {
		state.APIKey = ""
		printColored(yellow, "  ✓ API key cleared\n")
	} else if input != "" {
		state.APIKey = input
		printColored(green, "  ✓ API key set\n")
	}

	checkHealth()
}

// ── 2. Health Check ─────────────────────────────────────────

func healthCheck() {
	printHeader("Health Check")
	checkHealth()

	if !state.Connected {
		return
	}

	resp, err := http.Get(state.ServerURL + "/api/health")
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ Error: %v\n", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	pretty, _ := json.MarshalIndent(result, "    ", "  ")
	fmt.Println("    " + string(pretty))
}

func checkHealth() {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(state.ServerURL + "/api/health")
	if err != nil {
		state.Connected = false
		printColored(red, "  ✗ Cannot reach server\n")
		return
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		state.Connected = true
		printColored(green, "  ✓ Server is healthy\n")
	} else {
		state.Connected = false
		printColored(red, fmt.Sprintf("  ✗ Server returned %d\n", resp.StatusCode))
	}
}

// ── 3. List Games ───────────────────────────────────────────

func listGames() {
	printHeader("Game List")

	if !ensureConnected() {
		return
	}

	req, _ := http.NewRequest("GET", state.ServerURL+"/api/games", nil)
	setAuthHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ Error: %v\n", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		printColored(red, fmt.Sprintf("  ✗ Server returned %d: %s\n", resp.StatusCode, body))
		if resp.StatusCode == 401 {
			printColored(yellow, "  Hint: check your API key (option 1)\n")
		}
		return
	}

	var games []map[string]interface{}
	if err := json.Unmarshal(body, &games); err != nil {
		printColored(red, "  ✗ Invalid response\n")
		fmt.Println("    " + string(body))
		return
	}

	if len(games) == 0 {
		printColored(dim, "  No games currently hosted.\n")
		return
	}

	fmt.Printf("  Found %s%d%s game(s):\n\n", bold, len(games), reset)
	for i, g := range games {
		name, _ := g["name"].(string)
		id, _ := g["id"].(string)
		code, _ := g["join_code"].(string)
		current, _ := g["current_players"].(float64)
		max, _ := g["max_players"].(float64)
		natType, _ := g["nat_type"].(string)

		fmt.Printf("  "+cyan+bold+"[%d]"+reset+" %s\n", i+1, name)
		fmt.Printf("      "+dim+"ID:"+reset+"      %s\n", id)
		fmt.Printf("      "+dim+"Code:"+reset+"    %s%s%s\n", yellow+bold, code, reset)
		fmt.Printf("      "+dim+"Players:"+reset+" %.0f/%.0f\n", current, max)
		if natType != "" {
			fmt.Printf("      "+dim+"NAT:"+reset+"     %s\n", natType)
		}
		fmt.Println()
	}
}

// ── 4. Host Game ────────────────────────────────────────────

func hostGame() {
	printHeader("Host a Game")

	if !ensureConnected() {
		return
	}

	if state.Hosting {
		printColored(yellow, fmt.Sprintf("  Already hosting: %s (code: %s)\n", state.GameName, state.JoinCode))
		input := prompt("  Stop hosting first? (y/n): ")
		if strings.ToLower(input) == "y" {
			stopHosting()
		} else {
			return
		}
	}

	name := prompt("  Game name [Test Game]: ")
	if name == "" {
		name = "Test Game"
	}

	maxStr := prompt("  Max players [4]: ")
	maxPlayers := 4
	if maxStr != "" {
		fmt.Sscanf(maxStr, "%d", &maxPlayers)
	}

	payload := map[string]interface{}{
		"name":            name,
		"max_players":     maxPlayers,
		"current_players": 1,
		"nat_type":        "unknown",
		"data":            map[string]string{"mode": "test"},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", state.ServerURL+"/api/games", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ Failed: %v\n", err))
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		printColored(red, fmt.Sprintf("  ✗ Server returned %d: %s\n", resp.StatusCode, respBody))
		return
	}

	var hostResp struct {
		ID        string `json:"id"`
		JoinCode  string `json:"join_code"`
		HostToken string `json:"host_token"`
	}
	json.Unmarshal(respBody, &hostResp)

	state.mu.Lock()
	state.GameID = hostResp.ID
	state.JoinCode = hostResp.JoinCode
	state.HostToken = hostResp.HostToken
	state.GameName = name
	state.Hosting = true
	state.mu.Unlock()

	fmt.Println()
	printColored(green, "  ✓ Game registered!\n")
	fmt.Println()
	fmt.Printf("    "+dim+"ID:"+reset+"         %s\n", hostResp.ID)
	fmt.Printf("    "+dim+"Join Code:"+reset+"   %s%s%s\n", yellow+bold, hostResp.JoinCode, reset)
	fmt.Printf("    "+dim+"Host Token:"+reset+"  %s...%s\n", hostResp.HostToken[:8], hostResp.HostToken[len(hostResp.HostToken)-4:])
	fmt.Println()

	// Start heartbeat in background
	input := prompt("  Start heartbeat loop? (y/n) [y]: ")
	if input == "" || strings.ToLower(input) == "y" {
		go heartbeatLoop()
		printColored(green, "  ✓ Heartbeat started (15s interval)\n")
		printColored(dim, "  Game will stay alive until you stop hosting or quit.\n")
	}
}

func heartbeatLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		state.mu.Lock()
		if !state.Hosting {
			state.mu.Unlock()
			return
		}
		gameID := state.GameID
		token := state.HostToken
		state.mu.Unlock()

		hReq, _ := http.NewRequest("POST", state.ServerURL+"/api/games/"+gameID+"/heartbeat", nil)
		hReq.Header.Set("Authorization", "Bearer "+token)
		setAuthHeaders(hReq)

		hResp, err := http.DefaultClient.Do(hReq)
		if err != nil {
			printColored(red, fmt.Sprintf("\n  ✗ Heartbeat failed: %v\n", err))
			continue
		}
		hResp.Body.Close()
	}
}

func stopHosting() {
	if !state.Hosting {
		return
	}

	dReq, _ := http.NewRequest("DELETE", state.ServerURL+"/api/games/"+state.GameID, nil)
	dReq.Header.Set("Authorization", "Bearer "+state.HostToken)
	setAuthHeaders(dReq)

	dResp, err := http.DefaultClient.Do(dReq)
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ Failed to deregister: %v\n", err))
	} else {
		dResp.Body.Close()
		printColored(green, fmt.Sprintf("  ✓ Game deregistered (status %d)\n", dResp.StatusCode))
	}

	state.mu.Lock()
	state.Hosting = false
	state.GameID = ""
	state.JoinCode = ""
	state.HostToken = ""
	state.GameName = ""
	state.mu.Unlock()
}

// ── 5. Join Game ────────────────────────────────────────────

func joinGame() {
	printHeader("Join a Game")

	if !ensureConnected() {
		return
	}

	code := prompt("  Join code (or game ID): ")
	if code == "" {
		printColored(red, "  ✗ No code provided\n")
		return
	}

	var gameID string

	// If it looks like a join code (short, uppercase), look it up
	if len(code) <= 8 {
		req, _ := http.NewRequest("GET", state.ServerURL+"/api/games?code="+url.QueryEscape(strings.ToUpper(code)), nil)
		setAuthHeaders(req)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			printColored(red, fmt.Sprintf("  ✗ Error: %v\n", err))
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		var games []map[string]interface{}
		json.Unmarshal(body, &games)
		if len(games) == 0 {
			printColored(red, fmt.Sprintf("  ✗ No game found with code %s\n", code))
			return
		}
		gameID = games[0]["id"].(string)
		gameName, _ := games[0]["name"].(string)
		printColored(green, fmt.Sprintf("  ✓ Found game: %s (%s)\n", gameName, gameID))
	} else {
		gameID = code
	}

	// Get TURN credentials
	fmt.Println()
	printColored(dim, "  Fetching TURN credentials...\n")

	turnReq, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/games/%s/turn", state.ServerURL, gameID), nil)
	setAuthHeaders(turnReq)

	turnResp, err := http.DefaultClient.Do(turnReq)
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ Error: %v\n", err))
		return
	}
	defer turnResp.Body.Close()

	turnBody, _ := io.ReadAll(turnResp.Body)
	if turnResp.StatusCode != 200 {
		printColored(red, fmt.Sprintf("  ✗ Server returned %d: %s\n", turnResp.StatusCode, turnBody))
		return
	}

	var turnCreds map[string]interface{}
	json.Unmarshal(turnBody, &turnCreds)

	printColored(green, "  ✓ TURN credentials received:\n")
	fmt.Println()
	if uris, ok := turnCreds["uris"].([]interface{}); ok {
		for _, u := range uris {
			fmt.Printf("    "+dim+"URI:"+reset+"      %s\n", u)
		}
	}
	if user, ok := turnCreds["username"].(string); ok {
		fmt.Printf("    "+dim+"Username:"+reset+" %s\n", user)
	}
	if ttl, ok := turnCreds["ttl"].(float64); ok {
		fmt.Printf("    "+dim+"TTL:"+reset+"      %.0fs\n", ttl)
	}
	fmt.Println()
}

// ── 6. NAT Punch Test ──────────────────────────────────────

func punchTest() {
	printHeader("NAT Punch Test (WebSocket)")

	if !ensureConnected() {
		return
	}

	fmt.Println("    " + cyan + "1" + reset + "  Act as Host")
	fmt.Println("    " + cyan + "2" + reset + "  Act as Joiner")
	fmt.Println("    " + cyan + "0" + reset + "  Back")
	fmt.Println()

	choice := prompt("  ▸ ")

	switch choice {
	case "1":
		punchAsHost()
	case "2":
		punchAsJoiner()
	case "0":
		return
	default:
		printColored(red, "  Invalid choice.\n")
	}
}

func punchAsHost() {
	// Register game first
	payload := map[string]interface{}{
		"name":            "Punch Test",
		"max_players":     2,
		"current_players": 1,
		"nat_type":        "unknown",
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", state.ServerURL+"/api/games", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ Failed: %v\n", err))
		return
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 201 {
		printColored(red, fmt.Sprintf("  ✗ Server returned %d: %s\n", resp.StatusCode, respBody))
		return
	}

	var hostResp struct {
		ID        string `json:"id"`
		JoinCode  string `json:"join_code"`
		HostToken string `json:"host_token"`
	}
	json.Unmarshal(respBody, &hostResp)

	printColored(green, fmt.Sprintf("  ✓ Game registered: %s\n", hostResp.ID))
	fmt.Printf("    "+dim+"Join Code:"+reset+" %s%s%s\n", yellow+bold, hostResp.JoinCode, reset)
	fmt.Println()

	// Connect WebSocket
	conn := connectWebSocket()
	if conn == nil {
		return
	}
	defer conn.Close()

	// Register as host
	sendWSMsg(conn, map[string]interface{}{
		"type":       "register_host",
		"game_id":    hostResp.ID,
		"host_token": hostResp.HostToken,
	})

	printColored(green, "  ✓ Registered as host on WebSocket\n")
	fmt.Println()
	printColored(yellow, fmt.Sprintf("  Share this join code: %s\n", hostResp.JoinCode))
	printColored(dim, "  Waiting for joiners... Press Enter to disconnect.\n")
	fmt.Println()

	// Read messages in background
	done := make(chan struct{})
	go readWSMessages(conn, done)

	// Wait for Enter
	reader.ReadString('\n')
	fmt.Println()
	printColored(dim, "  Disconnecting...\n")
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	close(done)
	time.Sleep(300 * time.Millisecond)
}

func punchAsJoiner() {
	gameID := prompt("  Game ID: ")
	if gameID == "" {
		printColored(red, "  ✗ No game ID provided\n")
		return
	}

	conn := connectWebSocket()
	if conn == nil {
		return
	}
	defer conn.Close()

	sendWSMsg(conn, map[string]interface{}{
		"type":    "request_join",
		"game_id": gameID,
	})

	printColored(green, "  ✓ Join request sent\n")
	printColored(dim, "  Waiting for signaling... Press Enter to disconnect.\n")
	fmt.Println()

	done := make(chan struct{})
	go readWSMessages(conn, done)

	reader.ReadString('\n')
	fmt.Println()
	printColored(dim, "  Disconnecting...\n")
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	close(done)
	time.Sleep(300 * time.Millisecond)
}

func connectWebSocket() *websocket.Conn {
	wsURL := strings.Replace(state.ServerURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/ws/signaling"

	printColored(dim, fmt.Sprintf("  Connecting to %s...\n", wsURL))

	header := http.Header{}
	if state.APIKey != "" {
		header.Set("X-API-Key", state.APIKey)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		printColored(red, fmt.Sprintf("  ✗ WebSocket failed: %v\n", err))
		return nil
	}

	printColored(green, "  ✓ WebSocket connected\n")
	return conn
}

func readWSMessages(conn *websocket.Conn, done chan struct{}) {
	// Set up a close handler so that when done is closed we unblock ReadMessage.
	go func() {
		<-done
		conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Normal shutdown or connection closed — exit silently.
			return
		}

		var parsed map[string]interface{}
		json.Unmarshal(msg, &parsed)
		msgType, _ := parsed["type"].(string)

		// Server wraps data in "payload" — extract it
		payload, _ := parsed["payload"].(map[string]interface{})
		if payload == nil {
			payload = parsed // fallback: flat message format
		}

		switch msgType {
		case "host_registered":
			printColored(green, fmt.Sprintf("    ← host_registered (game: %s)\n", payload["game_id"]))
		case "gather_candidates":
			printColored(cyan, fmt.Sprintf("    ← gather_candidates (session: %s)\n", payload["session_id"]))
		case "peer_candidate":
			printColored(cyan, fmt.Sprintf("    ← peer_candidate: %s:%v\n", payload["public_ip"], payload["public_port"]))
		case "punch_signal":
			printColored(magenta, fmt.Sprintf("    ← punch_signal from %s\n", payload["from_peer"]))
		case "turn_fallback":
			printColored(yellow, "    ← turn_fallback (TURN relay credentials)\n")
		case "error":
			errMsg := parsed["error"]
			if errMsg == nil {
				errMsg = payload["message"]
			}
			printColored(red, fmt.Sprintf("    ← error: %s\n", errMsg))
		default:
			pretty, _ := json.MarshalIndent(parsed, "      ", "  ")
			fmt.Printf("    "+dim+"← %s:"+reset+" %s\n", msgType, pretty)
		}
	}
}

func sendWSMsg(conn *websocket.Conn, msg map[string]interface{}) {
	data, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		printColored(red, fmt.Sprintf("  ✗ Send error: %v\n", err))
	}
}

// ── 7. Show State ───────────────────────────────────────────

func showState() {
	printHeader("Current State")

	fmt.Printf("    "+dim+"Server URL:"+reset+"  %s\n", state.ServerURL)

	if state.Connected {
		fmt.Printf("    "+dim+"Status:"+reset+"      %s● connected%s\n", green, reset)
	} else {
		fmt.Printf("    "+dim+"Status:"+reset+"      %s● disconnected%s\n", red, reset)
	}

	if state.APIKey != "" {
		fmt.Printf("    "+dim+"API Key:"+reset+"     %s...%s\n", state.APIKey[:4], state.APIKey[len(state.APIKey)-4:])
	} else {
		fmt.Printf("    "+dim+"API Key:"+reset+"     %snot set%s\n", dim, reset)
	}

	fmt.Println()

	if state.Hosting {
		fmt.Printf("    "+dim+"Hosting:"+reset+"     %s✓ yes%s\n", green, reset)
		fmt.Printf("    "+dim+"Game Name:"+reset+"   %s\n", state.GameName)
		fmt.Printf("    "+dim+"Game ID:"+reset+"     %s\n", state.GameID)
		fmt.Printf("    "+dim+"Join Code:"+reset+"   %s%s%s\n", yellow+bold, state.JoinCode, reset)
		fmt.Printf("    "+dim+"Host Token:"+reset+"  %s...%s\n", state.HostToken[:8], state.HostToken[len(state.HostToken)-4:])
	} else {
		fmt.Printf("    "+dim+"Hosting:"+reset+"     no\n")
	}
	fmt.Println()
}

// ── Helpers ─────────────────────────────────────────────────

func prompt(prefix string) string {
	fmt.Print(prefix)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

func printHeader(title string) {
	fmt.Println()
	fmt.Printf("  %s%s── %s ──%s\n", bold, blue, title, reset)
	fmt.Println()
}

func printColored(color, text string) {
	fmt.Print(color + text + reset)
}

func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

func setAuthHeaders(req *http.Request) {
	if state.APIKey != "" {
		req.Header.Set("X-API-Key", state.APIKey)
	}
}

func ensureConnected() bool {
	if !state.Connected {
		printColored(yellow, "  Server not connected. Checking...\n")
		checkHealth()
	}
	if !state.Connected {
		printColored(red, "  ✗ Cannot reach server. Configure with option 1.\n")
		return false
	}
	return true
}

func cleanup() {
	if state.Hosting {
		printColored(dim, "  Deregistering game...\n")
		stopHosting()
	}
	if state.WSConn != nil {
		state.WSConn.Close()
	}
}

func init() {
	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		cleanup()
		printColored(green, "\n  Goodbye! 👋\n\n")
		os.Exit(0)
	}()
}
