# Testing

Guide for running and writing tests for NAT Punchthrough Hero.

## Running Server Tests

All tests live in the `server/` directory as standard Go test files.

```bash
cd server

# Run all tests
go test ./...

# Verbose output (shows each test name and result)
go test -v ./...

# With race detector (recommended for CI)
go test -race ./...

# Disable test caching
go test -count=1 ./...

# Run a specific test by name
go test -run TestAPI_Health

# Run all tests in a specific file's area (regex match)
go test -run TestRateLimiter
```

### First Time Setup

If you get errors about missing `go.sum` entries, run:

```bash
cd server
go mod tidy
```

This downloads dependencies and generates the `go.sum` file.

## Test Suites

The server has 9 test files covering all major subsystems:

| File | Tests | What It Covers |
|------|-------|----------------|
| `api_test.go` | 20 | REST API endpoints — game CRUD, health, auth |
| `config_test.go` | 21 | Configuration parsing, defaults, env overrides |
| `helpers_test.go` | 14 | IP extraction, string sanitization |
| `ipfilter_test.go` | 20 | IP blocklist/allowlist, CIDR, middleware |
| `ratelimit_test.go` | 19 | Per-IP rate limiting, burst, concurrent WS limits |
| `protection_test.go` | 17 | Auto-blocking, escalating bans, flood detection |
| `store_test.go` | 20+ | In-memory game store, queries, pagination |
| `signaling_test.go` | 15 | WebSocket signaling, host/join flow, ICE relay |
| `turn_test.go` | 7 | TURN credential generation, HMAC validation |

## TUI Test Client

The `test-client/tui/` directory contains an interactive terminal UI for end-to-end manual testing. It drives the full signaling flow — hosting, joining, NAT traversal confirmation, shared chat, and file transfer — all against a live server.

### Building

```bash
cd test-client/tui

# Run directly (no build step needed)
go run .

# Or build a binary
go build -o tui .
./tui
```

First time only — if `go.sum` is missing:

```bash
cd test-client/tui
go mod tidy
```

### Running

The TUI starts immediately and connects to `http://localhost:8080` by default. Everything is configured through the interactive menu — there are no command-line flags.

```
  ╔══════════════════════════════════════════════════╗
  ║       NAT Punchthrough Hero — Test Client        ║
  ║           Interactive Testing Console            ║
  ╚══════════════════════════════════════════════════╝

  [1] Configure Server / API Key
  [2] Health Check
  [3] List Games
  [4] Host a Game
  [5] Join a Game
  [6] NAT Punch Test
  [7] Show State
  [0] Quit
```

### Typical workflow

**Two terminals, both pointing at the same server:**

1. Terminal A — host a game:
   - Press `4` → enter a game name → the TUI registers the game and starts waiting for a joiner
2. Terminal B — join the game:
   - Press `5` → enter the join code shown in terminal A

Both clients complete the ICE/signaling exchange automatically. Once connected, the TUI prints which NAT traversal method was used:

```
  ╔══════════════════════════════════╗
  ║  NAT TRAVERSAL CONFIRMED         ║
  ║  Method: punched                 ║
  ║  Direct UDP hole-punch succeeded ║
  ╚══════════════════════════════════╝
```

Methods reported: `direct` (LAN), `punched` (UDP hole-punch), `relayed` (TURN fallback).

### Chat room

After connecting, both peers enter a shared chat room hosted by the server. Type any text and press Enter to send.

| Command | Description |
|---------|-------------|
| `/file <path>` | Offer a file to all peers |
| `/accept <id> [dir]` | Accept an incoming file offer, optionally specifying save directory (defaults to current directory) |
| `/decline <id>` | Decline an incoming file offer |
| `/help` | Show command reference |
| `/quit` | Leave the chat and return to the main menu |

### File transfer

File data is chunked (32 KB chunks), base64-encoded, and routed through the signaling server. A CRC32 integrity check runs on receipt — the TUI prints a pass/fail result and the saved file path.

Example session:

```
[14:32] You: /file ~/Downloads/demo.zip
  → File offer sent: demo.zip (2.4 MB) [id: a1b2c3]

[14:32] Peer-7f3a: incoming file offer
  demo.zip  2.4 MB  [id: a1b2c3]
  /accept a1b2c3         (save to current dir)
  /accept a1b2c3 ~/recv  (save to specific dir)

[14:32] Peer-7f3a: /accept a1b2c3 ~/recv
  ✓ demo.zip saved to /home/user/recv/demo.zip — CRC32 OK
```

### If the server requires an API key

Select option `1` from the main menu to set the server URL and API key before doing anything else.

## Test Client (CLI)

The `test-client/` directory contains a CLI tool for scripted/CI integration testing against a running server.

```bash
cd test-client

# Check server health
go run . health -server http://localhost:8080

# List games
go run . list -server http://localhost:8080

# Host a game
go run . host -name "My Game" -server http://localhost:8080

# Join a game by code
go run . join -code ABC123 -server http://localhost:8080
```

Add `-key your-api-key` if the server has an API key configured, and `-v` for verbose output.

## Writing Tests

Tests follow standard Go conventions:

- Test files are named `*_test.go` alongside the code they test
- Use `testServer()` or similar helpers defined in each test file to set up isolated server instances
- Tests run in-process with `httptest.NewServer` — no external server needed

Example:

```go
func TestMyFeature(t *testing.T) {
    srv := testServer(t) // creates a test server instance
    defer srv.Close()

    resp, err := http.Get(srv.URL + "/api/health")
    if err != nil {
        t.Fatal(err)
    }
    if resp.StatusCode != 200 {
        t.Errorf("expected 200, got %d", resp.StatusCode)
    }
}
```

## CI Notes

For continuous integration, the recommended command is:

```bash
cd server && go test -race -count=1 ./...
```

This enables the race detector and disables caching to ensure a clean run every time.
