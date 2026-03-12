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

## Test Client

The `test-client/` directory contains a CLI tool for manual integration testing against a running server.

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
