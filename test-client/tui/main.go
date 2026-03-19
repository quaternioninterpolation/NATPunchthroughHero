// Package main provides an interactive terminal UI for the NAT Punchthrough Hero test client.
// It manages server connection state, API keys, and provides a menu-driven interface
// for all test-client operations including a shared chat room with file transfer.
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
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

// ── App state ────────────────────────────────────────────────

type AppState struct {
	ServerURL string
	APIKey    string
	GameID    string
	JoinCode  string
	HostToken string
	GameName  string
	Connected bool
	Hosting   bool
	WSConn    *websocket.Conn
	mu        sync.Mutex
}

var state = &AppState{
	ServerURL: "http://localhost:8080",
}

var reader = bufio.NewReader(os.Stdin)

// ── wsConn: thread-safe WebSocket writer ────────────────────
//
// gorilla/websocket allows one concurrent reader and one concurrent writer.
// The chat loop's main goroutine AND the file-send goroutine both write;
// wsConn serialises writes with a mutex so they never race.

type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newWSConn(c *websocket.Conn) *wsConn { return &wsConn{conn: c} }

// send marshals msg and writes it as a text frame. Safe for concurrent calls.
func (w *wsConn) send(msg map[string]interface{}) {
	data, _ := json.Marshal(msg)
	w.mu.Lock()
	defer w.mu.Unlock()
	// Ignore write errors here; the reader goroutine will detect the closed
	// connection and trigger the session shutdown via doneCh.
	w.conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// sendClose sends a WebSocket close frame.
func (w *wsConn) sendClose() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.WriteMessage(websocket.CloseMessage, //nolint:errcheck
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

// close closes the underlying TCP connection.
func (w *wsConn) close() { w.conn.Close() }

// read reads the next message. Only one goroutine should call this at a time.
func (w *wsConn) read() ([]byte, error) {
	_, data, err := w.conn.ReadMessage()
	return data, err
}

// ── Main ────────────────────────────────────────────────────

func main() {
	clearScreen()
	printBanner()
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
	fmt.Println("    " + cyan + "6" + reset + "  NAT punch + chat session")
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
		if strings.ToLower(prompt("  Stop hosting first? (y/n): ")) == "y" {
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
	printColored(green, "  ✓ Game registered!\n\n")
	fmt.Printf("    "+dim+"ID:"+reset+"         %s\n", hostResp.ID)
	fmt.Printf("    "+dim+"Join Code:"+reset+"   %s%s%s\n", yellow+bold, hostResp.JoinCode, reset)
	fmt.Printf("    "+dim+"Host Token:"+reset+"  %s...%s\n", hostResp.HostToken[:8], hostResp.HostToken[len(hostResp.HostToken)-4:])
	fmt.Println()

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

	printColored(green, "  ✓ TURN credentials received:\n\n")
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

// ── 6. NAT Punch + Chat Session ────────────────────────────

func punchTest() {
	printHeader("NAT Punch + Chat Session")

	if !ensureConnected() {
		return
	}

	fmt.Println("    " + cyan + "1" + reset + "  Act as Host")
	fmt.Println("    " + cyan + "2" + reset + "  Act as Joiner")
	fmt.Println("    " + cyan + "0" + reset + "  Back")
	fmt.Println()

	switch prompt("  ▸ ") {
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
	payload := map[string]interface{}{
		"name":            "Punch Test",
		"max_players":     8,
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
	fmt.Printf("    "+dim+"Join Code:"+reset+" %s%s%s\n\n", yellow+bold, hostResp.JoinCode, reset)

	ws := connectWebSocket()
	if ws == nil {
		return
	}

	ws.send(map[string]interface{}{
		"type": "register_host",
		"payload": map[string]interface{}{
			"game_id":    hostResp.ID,
			"host_token": hostResp.HostToken,
		},
	})

	printColored(yellow, fmt.Sprintf("  Share this join code: %s%s%s\n", yellow+bold, hostResp.JoinCode, reset))
	printColored(dim, "  Waiting for players to join...\n\n")

	runChatSession(ws, hostResp.ID, "host")
}

func punchAsJoiner() {
	input := prompt("  Join code or game ID: ")
	if input == "" {
		printColored(red, "  ✗ No code provided\n")
		return
	}

	var gameID string

	if len(input) <= 8 {
		req, _ := http.NewRequest("GET", state.ServerURL+"/api/games?code="+url.QueryEscape(strings.ToUpper(input)), nil)
		setAuthHeaders(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			printColored(red, fmt.Sprintf("  ✗ Lookup failed: %v\n", err))
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var games []map[string]interface{}
		json.Unmarshal(body, &games)
		if len(games) == 0 {
			printColored(red, fmt.Sprintf("  ✗ No game found with code %s\n", input))
			return
		}
		gameID = games[0]["id"].(string)
		gameName, _ := games[0]["name"].(string)
		printColored(green, fmt.Sprintf("  ✓ Found: %s (%s)\n", gameName, gameID))
	} else {
		gameID = input
	}

	ws := connectWebSocket()
	if ws == nil {
		return
	}

	ws.send(map[string]interface{}{
		"type": "request_join",
		"payload": map[string]interface{}{
			"game_id": gameID,
		},
	})

	printColored(dim, "  Join request sent — waiting for signaling...\n\n")
	runChatSession(ws, gameID, "joiner")
}

// ── Chat Session ─────────────────────────────────────────────

// wsMsg is a decoded incoming WebSocket message.
type wsMsg struct {
	Type    string
	Payload map[string]interface{}
}

// fileTransferSend tracks an outgoing file offer waiting for acceptance.
type fileTransferSend struct {
	id       string
	filePath string
}

// fileTransferRecv tracks an incoming file being assembled from chunks.
type fileTransferRecv struct {
	id       string
	filename string
	saveDir  string
	size     int64
	crc32Hex string
	chunks   map[int][]byte
	lastIdx  int // -1 until is_last chunk arrives
}

// chunkSize is the raw bytes per file transfer chunk (32 KB → ~43 KB base64).
// Combined with the 20 ms inter-chunk sleep this gives ~1.6 MB/s throughput —
// well within the server's 128 KB MaxWSMessage limit.
const chunkSize = 32 * 1024

// runChatSession is the unified handler for the signaling phase and the
// subsequent chat + file-transfer phase.  role is "host" or "joiner".
func runChatSession(ws *wsConn, gameID, role string) {
	defer ws.close()

	wsMsgCh := make(chan wsMsg, 32)
	inputCh := make(chan string, 4)
	inputDone := make(chan struct{})

	var doneOnce sync.Once
	doneCh := make(chan struct{})
	closeDone := func() { doneOnce.Do(func() { close(doneCh) }) }

	// Goroutine: read incoming WebSocket messages.
	go func() {
		defer closeDone()
		for {
			raw, err := ws.read()
			if err != nil {
				return
			}
			var parsed map[string]interface{}
			json.Unmarshal(raw, &parsed)
			msgType, _ := parsed["type"].(string)
			payload, _ := parsed["payload"].(map[string]interface{})
			if payload == nil {
				payload = parsed // flat message format fallback
			}
			select {
			case wsMsgCh <- wsMsg{Type: msgType, Payload: payload}:
			case <-doneCh:
				return
			}
		}
	}()

	// Goroutine: read stdin line by line.
	// Uses the shared global reader so there is no competing buffer when the
	// user returns to the menu after this session ends.
	go func() {
		defer close(inputDone)
		for {
			line, err := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if err != nil {
				closeDone()
				return
			}
			// Prefer doneCh to avoid the goroutine lingering after session end.
			select {
			case <-doneCh:
				return
			default:
			}
			select {
			case inputCh <- line:
			case <-doneCh:
				return
			}
		}
	}()

	// Signaling state
	natConfirmed := false
	inChat := false
	var sessionID string

	// File transfer state — only accessed from the main loop (no locking needed)
	pendingSends := make(map[string]*fileTransferSend)
	activeRecv := make(map[string]*fileTransferRecv)

	if role == "host" {
		printColored(dim, "  Waiting for the server to confirm registration...\n")
	}

mainLoop:
	for {
		select {
		case <-doneCh:
			printColored(red, "\n  Connection closed.\n")
			break mainLoop

		case msg := <-wsMsgCh:
			handleSessionMsg(ws, msg, gameID, role,
				&natConfirmed, &inChat, &sessionID,
				pendingSends, activeRecv)

		case line := <-inputCh:
			if !inChat {
				continue // signaling still in progress — ignore input
			}
			if !handleChatInput(ws, line, gameID, pendingSends, activeRecv) {
				break mainLoop
			}
		}
	}

	// Shutdown: close channel, send WS close frame, then wait for the stdin
	// goroutine to exit before returning (so the shared reader is free again).
	closeDone()
	ws.sendClose()
	fmt.Println()
	printColored(dim, "  Chat ended. Press Enter to return to menu...\n")
	<-inputDone
}

// handleSessionMsg dispatches a single incoming WebSocket message.
func handleSessionMsg(
	ws *wsConn,
	msg wsMsg,
	gameID, role string,
	natConfirmed *bool,
	inChat *bool,
	sessionID *string,
	pendingSends map[string]*fileTransferSend,
	activeRecv map[string]*fileTransferRecv,
) {
	switch msg.Type {

	// ── NAT Signaling ─────────────────────────────────────

	case "host_registered":
		printColored(green, "  ✓ Registered as host\n")
		// The host is a participant too — enter chat immediately so they can
		// send and receive messages as soon as joiners connect.
		*inChat = true
		printChatWelcome()

	case "gather_candidates":
		sid, _ := msg.Payload["session_id"].(string)
		*sessionID = sid
		printColored(dim, fmt.Sprintf("  [signaling] gathering ICE candidates (session %s)...\n", sid))
		// Send a simulated ICE candidate to drive the server's punch/TURN flow.
		ws.send(map[string]interface{}{
			"type": "ice_candidate",
			"payload": map[string]interface{}{
				"session_id":  sid,
				"public_ip":   "127.0.0.1",
				"public_port": 12345,
				"local_ip":    "127.0.0.1",
				"local_port":  12345,
				"nat_type":    "unknown",
			},
		})

	case "peer_candidate":
		ip, _ := msg.Payload["public_ip"].(string)
		port := msg.Payload["public_port"]
		printColored(dim, fmt.Sprintf("  [signaling] peer candidate: %s:%v\n", ip, port))

	case "punch_signal":
		// Joiner receives this after the host submits its ICE candidate.
		// Declare a successful hole-punch and enter chat.
		if !*natConfirmed && role == "joiner" && *sessionID != "" {
			*natConfirmed = true
			ws.send(map[string]interface{}{
				"type": "connection_established",
				"payload": map[string]interface{}{
					"session_id": *sessionID,
					"method":     "punched",
				},
			})
			printNATConfirmation("punched")
			*inChat = true
			printChatWelcome()
		}

	case "turn_fallback":
		// Joiner receives this alongside (or instead of) punch_signal.
		// If we haven't confirmed yet, fall back to the relay method.
		if !*natConfirmed && role == "joiner" && *sessionID != "" {
			*natConfirmed = true
			ws.send(map[string]interface{}{
				"type": "connection_established",
				"payload": map[string]interface{}{
					"session_id": *sessionID,
					"method":     "relayed",
				},
			})
			printNATConfirmation("relayed")
			*inChat = true
			printChatWelcome()
		}

	case "peer_connected":
		// Host receives this when a joiner sends connection_established.
		method, _ := msg.Payload["method"].(string)
		if method == "" {
			method = "punched"
		}
		// Show NAT method once, then always announce the new player.
		if !*natConfirmed {
			*natConfirmed = true
			printNATConfirmation(method)
		}
		fmt.Printf("\r  "+green+"✓ New player joined via %s"+reset+"\n", method)
		fmt.Print("  You > ")

	// ── Chat ──────────────────────────────────────────────

	case "chat_message":
		from, _ := msg.Payload["from"].(string)
		text, _ := msg.Payload["text"].(string)
		ts, _ := msg.Payload["ts"].(string)
		if len(ts) >= 16 {
			ts = ts[11:16] // extract HH:MM from RFC3339 timestamp
		} else {
			ts = time.Now().Format("15:04")
		}
		displayName := "Peer"
		if len(from) >= 4 {
			displayName = "Peer-" + from[:4]
		}
		// \r clears any partially typed "You > " line before printing
		fmt.Printf("\r  "+cyan+bold+"[%s] %s:"+reset+" %s\n", ts, displayName, text)
		fmt.Print("  You > ")

	// ── File Transfer ─────────────────────────────────────

	case "file_offer":
		from, _ := msg.Payload["from"].(string)
		tid, _ := msg.Payload["transfer_id"].(string)
		fname, _ := msg.Payload["filename"].(string)
		size, _ := msg.Payload["size"].(float64)
		crc32hex, _ := msg.Payload["crc32"].(string)

		// Sanitise filename — strip any path components the sender may have included
		fname = filepath.Base(fname)
		if fname == "." || fname == "/" {
			fname = "received_file"
		}

		senderName := "Peer"
		if len(from) >= 4 {
			senderName = "Peer-" + from[:4]
		}

		fmt.Printf("\n  "+magenta+bold+"📁 File offer"+reset+" from %s\n", senderName)
		fmt.Printf("     Name: %s  Size: %s\n", fname, formatBytes(int64(size)))
		fmt.Printf("     CRC32: %s\n", crc32hex)
		fmt.Printf("     ID: %s\n", tid)
		fmt.Printf("     "+dim+"→ /accept %s [dir]  or  /decline %s"+reset+"\n", tid, tid)
		fmt.Print("  You > ")

		activeRecv[tid] = &fileTransferRecv{
			id:       tid,
			filename: fname,
			saveDir:  ".",
			size:     int64(size),
			crc32Hex: crc32hex,
			chunks:   make(map[int][]byte),
			lastIdx:  -1,
		}

	case "file_accept":
		tid, _ := msg.Payload["transfer_id"].(string)
		if send, ok := pendingSends[tid]; ok {
			delete(pendingSends, tid)
			fmt.Printf("\n  "+green+"✓ File accepted — sending..."+reset+"\n")
			go startFileSend(ws, send.filePath, tid)
		}

	case "file_reject":
		tid, _ := msg.Payload["transfer_id"].(string)
		delete(pendingSends, tid)
		fmt.Printf("\n  "+yellow+"✗ File declined (id: %s)"+reset+"\n", tid)
		fmt.Print("  You > ")

	case "file_chunk":
		tid, _ := msg.Payload["transfer_id"].(string)
		idx, _ := msg.Payload["index"].(float64)
		data, _ := msg.Payload["data"].(string)
		isLast, _ := msg.Payload["is_last"].(bool)
		crc32hex, _ := msg.Payload["crc32"].(string)

		recv, ok := activeRecv[tid]
		if !ok {
			return
		}

		raw, err := base64.StdEncoding.DecodeString(data)
		if err == nil {
			recv.chunks[int(idx)] = raw
		}

		if isLast {
			if crc32hex != "" {
				recv.crc32Hex = crc32hex // server attaches CRC32 on last chunk
			}
			recv.lastIdx = int(idx)
			delete(activeRecv, tid) // remove before handing off to goroutine
			go func(r *fileTransferRecv) {
				if err := assembleFile(r); err != nil {
					fmt.Printf("\n  "+red+"✗ File receive failed: %v"+reset+"\n", err)
				}
				fmt.Print("  You > ")
			}(recv)
		}

	// ── Misc ──────────────────────────────────────────────

	case "peer_disconnected":
		sid, _ := msg.Payload["session_id"].(string)
		fmt.Printf("\n  "+yellow+"⚠ Peer disconnected (session %s)"+reset+"\n", sid)
		if *inChat {
			fmt.Print("  You > ")
		}

	case "error":
		// Server errors arrive as {"type":"error","error":"code: msg"}.
		// After the flat-message fallback, msg.Payload IS the full message.
		errMsg, _ := msg.Payload["error"].(string)
		if errMsg == "" {
			errMsg = fmt.Sprintf("%v", msg.Payload)
		}
		fmt.Printf("\n  "+red+"[error] %s"+reset+"\n", errMsg)
		if *inChat {
			fmt.Print("  You > ")
		}

	case "pong":
		// keep-alive response, ignore

	default:
		if msg.Type != "" {
			printColored(dim, fmt.Sprintf("  [%s]\n", msg.Type))
		}
	}
}

// handleChatInput processes one line of user input.
// Returns false when the user wants to quit the session.
func handleChatInput(
	ws *wsConn,
	line, gameID string,
	pendingSends map[string]*fileTransferSend,
	activeRecv map[string]*fileTransferRecv,
) bool {
	if line == "" {
		fmt.Print("  You > ")
		return true
	}

	switch {
	case line == "/quit" || line == "/q":
		return false

	case line == "/help":
		fmt.Println()
		fmt.Println("  " + bold + "Chat Commands:" + reset)
		fmt.Println("    /file <path>              Send a file to everyone in the room")
		fmt.Println("    /accept <id> [dir]        Accept an incoming file offer")
		fmt.Println("    /decline <id>             Decline an incoming file offer")
		fmt.Println("    /quit                     Leave the chat session")
		fmt.Println()
		fmt.Print("  You > ")

	case strings.HasPrefix(line, "/file"):
		parts := strings.Fields(strings.TrimPrefix(line, "/file"))
		if len(parts) == 0 {
			printColored(red, "  Usage: /file <path>\n")
			fmt.Print("  You > ")
			return true
		}
		filePath := strings.Join(parts, " ") // rejoin to support spaces in paths
		tid, err := sendFileOffer(ws, gameID, filePath)
		if err != nil {
			printColored(red, fmt.Sprintf("  ✗ %v\n", err))
		} else {
			pendingSends[tid] = &fileTransferSend{id: tid, filePath: filePath}
			printColored(green, fmt.Sprintf("  ✓ File offer sent (id: %s)\n", tid))
		}
		fmt.Print("  You > ")

	case strings.HasPrefix(line, "/accept"):
		parts := strings.Fields(strings.TrimPrefix(line, "/accept"))
		if len(parts) == 0 {
			printColored(red, "  Usage: /accept <transfer_id> [save_dir]\n")
			fmt.Print("  You > ")
			return true
		}
		tid := parts[0]
		saveDir := "."
		if len(parts) > 1 {
			saveDir = parts[1]
		}
		recv, ok := activeRecv[tid]
		if !ok {
			printColored(red, "  ✗ Unknown transfer ID\n")
			fmt.Print("  You > ")
			return true
		}
		recv.saveDir = saveDir
		ws.send(map[string]interface{}{
			"type":    "file_accept",
			"payload": map[string]string{"transfer_id": tid},
		})
		printColored(green, fmt.Sprintf("  ✓ Accepted — saving to: %s\n", saveDir))
		fmt.Print("  You > ")

	case strings.HasPrefix(line, "/decline"):
		parts := strings.Fields(strings.TrimPrefix(line, "/decline"))
		if len(parts) == 0 {
			printColored(red, "  Usage: /decline <transfer_id>\n")
			fmt.Print("  You > ")
			return true
		}
		tid := parts[0]
		delete(activeRecv, tid)
		ws.send(map[string]interface{}{
			"type":    "file_reject",
			"payload": map[string]string{"transfer_id": tid},
		})
		printColored(yellow, "  ✓ Declined\n")
		fmt.Print("  You > ")

	default:
		ws.send(map[string]interface{}{
			"type": "chat_message",
			"payload": map[string]interface{}{
				"game_id": gameID,
				"text":    line,
			},
		})
		ts := time.Now().Format("15:04")
		fmt.Printf("  "+bold+"[%s] You:"+reset+" %s\n", ts, line)
		fmt.Print("  You > ")
	}

	return true
}

// ── NAT Confirmation UI ──────────────────────────────────────

func printNATConfirmation(method string) {
	fmt.Println()
	fmt.Println(dim + "  ─────────────────────────────────────────────────" + reset)
	switch method {
	case "punched":
		fmt.Println("  " + green + bold + "🔓 NAT Traversal Confirmed — UDP Hole Punch" + reset)
		fmt.Println("  " + dim + "Direct P2P path established via simultaneous UDP." + reset)
	case "direct":
		fmt.Println("  " + cyan + bold + "⚡ NAT Traversal Confirmed — Direct" + reset)
		fmt.Println("  " + dim + "Direct connection (no NAT traversal required)." + reset)
	case "relayed":
		fmt.Println("  " + yellow + bold + "🔄 NAT Traversal Confirmed — TURN Relay" + reset)
		fmt.Println("  " + dim + "Hole punch failed or skipped; traffic relayed via TURN." + reset)
	default:
		fmt.Printf("  "+green+bold+"✓ NAT Traversal Confirmed — %s"+reset+"\n", method)
	}
	fmt.Println(dim + "  ─────────────────────────────────────────────────" + reset)
	fmt.Println()
}

func printChatWelcome() {
	fmt.Println("  " + bold + "Chat Room" + reset + dim + " — all peers share this room" + reset)
	fmt.Println(dim + "  Commands: /file <path>  /accept <id> [dir]  /decline <id>  /quit  /help" + reset)
	fmt.Println(dim + "  ─────────────────────────────────────────────────" + reset)
	fmt.Println()
	fmt.Print("  You > ")
}

// ── File Transfer ────────────────────────────────────────────

// sendFileOffer opens the file, computes its CRC32, and broadcasts a file_offer.
func sendFileOffer(ws *wsConn, gameID, filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("cannot stat file: %w", err)
	}

	h := crc32.NewIEEE()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("CRC32 computation failed: %w", err)
	}
	crc32Hex := fmt.Sprintf("%08x", h.Sum32())

	// Nanosecond timestamp gives a unique-enough transfer ID for demo use.
	transferID := fmt.Sprintf("%x", time.Now().UnixNano())

	ws.send(map[string]interface{}{
		"type": "file_offer",
		"payload": map[string]interface{}{
			"game_id":     gameID,
			"transfer_id": transferID,
			"filename":    filepath.Base(filePath),
			"size":        info.Size(),
			"crc32":       crc32Hex,
		},
	})

	return transferID, nil
}

// startFileSend reads the file and streams it in base64-encoded chunks.
// Runs in a goroutine; uses the thread-safe wsConn for concurrent writes.
func startFileSend(ws *wsConn, filePath, transferID string) {
	f, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("\n  "+red+"✗ File send failed: %v"+reset+"\n", err)
		fmt.Print("  You > ")
		return
	}
	defer f.Close()

	buf := make([]byte, chunkSize)
	index := 0

	for {
		n, err := f.Read(buf)
		if err != nil && err != io.EOF {
			fmt.Printf("\n  "+red+"✗ File read error: %v"+reset+"\n", err)
			fmt.Print("  You > ")
			return
		}

		isLast := err == io.EOF

		if n > 0 || (isLast && index == 0) {
			// Send the chunk; for an empty file this sends one zero-length chunk
			// so the receiver's assembly goroutine is triggered correctly.
			encoded := base64.StdEncoding.EncodeToString(buf[:n])
			ws.send(map[string]interface{}{
				"type": "file_chunk",
				"payload": map[string]interface{}{
					"transfer_id": transferID,
					"index":       index,
					"data":        encoded,
					"is_last":     isLast,
				},
			})
			index++
		}

		if isLast {
			break
		}

		// Throttle to ~1.6 MB/s, keeping the server's sendCh comfortably below capacity.
		time.Sleep(20 * time.Millisecond)
	}

	fmt.Printf("\n  "+green+"✓ File sent (%d chunk(s))"+reset+"\n", index)
	fmt.Print("  You > ")
}

// assembleFile reassembles received chunks, verifies the CRC32, and writes
// the file to disk.  Runs in a goroutine.
func assembleFile(recv *fileTransferRecv) error {
	if recv.lastIdx < 0 {
		return fmt.Errorf("incomplete transfer: last chunk never arrived")
	}

	// Assemble in chunk-index order
	var data []byte
	for i := 0; i <= recv.lastIdx; i++ {
		chunk, ok := recv.chunks[i]
		if !ok {
			return fmt.Errorf("incomplete transfer: missing chunk %d/%d", i, recv.lastIdx)
		}
		data = append(data, chunk...)
	}

	// Verify integrity
	actual := crc32.ChecksumIEEE(data)
	actualHex := fmt.Sprintf("%08x", actual)
	crcStatus := "CRC32 OK"
	if recv.crc32Hex != "" {
		if actualHex != recv.crc32Hex {
			return fmt.Errorf("CRC32 mismatch: expected %s got %s — file is corrupt",
				recv.crc32Hex, actualHex)
		}
	} else {
		crcStatus = "CRC32 not verified (sender did not provide hash)"
	}

	saveDir := recv.saveDir
	if saveDir == "" {
		saveDir = "."
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return fmt.Errorf("cannot create save directory: %w", err)
	}

	savePath := filepath.Join(saveDir, recv.filename)
	if err := os.WriteFile(savePath, data, 0644); err != nil {
		return fmt.Errorf("cannot write file: %w", err)
	}

	fmt.Printf("\n  "+green+bold+"✓ File saved: %s"+reset+" (%s, %s)\n",
		savePath, formatBytes(int64(len(data))), crcStatus)
	return nil
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int64) string {
	switch {
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ── WebSocket helpers ─────────────────────────────────────────

func connectWebSocket() *wsConn {
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
	return newWSConn(conn)
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
