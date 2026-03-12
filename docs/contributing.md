# Contributing

Guide for developing and contributing to NAT Punchthrough Hero.

## Development Setup

### Prerequisites

- Go 1.23+
- Docker & Docker Compose
- Git

### Clone & Build

```bash
git clone https://github.com/you/natpunch.git
cd natpunch

# Build the server
cd server
go build -o natpunch-server .

# Run locally
./natpunch-server serve
```

### Run with Docker (Development)

```bash
docker compose up --build
```

### Run Tests

```bash
cd server
go test ./...

# With race detector
go test -race ./...

# Verbose
go test -v ./...
```

## Project Structure

```
natpunch/
├── server/                  # Go master server
│   ├── main.go             # Entrypoint, subcommands
│   ├── config.go           # Configuration system (TOML + env + flags)
│   ├── store.go            # In-memory game store
│   ├── api.go              # REST API + middleware
│   ├── signaling.go        # WebSocket signaling hub
│   ├── turn.go             # TURN credential generation
│   ├── ratelimit.go        # Multi-layer rate limiter
│   ├── ipfilter.go         # IP blocklist/allowlist
│   ├── protection.go       # Automatic abuse detection
│   ├── helpers.go          # IP extraction, sanitization
│   ├── checks.go           # Diagnostic checks
│   ├── setup.go            # Interactive setup wizard
│   ├── embed.go            # Dashboard embed directive
│   ├── doc.go              # Package documentation
│   └── dashboard/
│       └── index.html      # Admin dashboard (embedded)
├── test-client/            # Go CLI test client
│   └── main.go
├── unity-sdk/              # Unity C# components
│   └── Runtime/
│       ├── NATTransport.cs
│       ├── MasterServerClient.cs
│       └── NATTraversal.cs
├── docs/                   # Documentation
│   ├── quickstart.md
│   ├── deployment.md
│   ├── configuration.md
│   ├── security.md
│   ├── api-reference.md
│   ├── architecture.md
│   ├── unity-sdk.md
│   ├── troubleshooting.md
│   ├── contributing.md
│   └── changelog.md
├── deploy/                 # Deployment scripts
│   ├── deploy-vps.sh
│   └── cloud-init.yml
├── Dockerfile
├── docker-compose.yml
├── docker-compose.prod.yml
├── config.example.toml
└── README.md
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `golangci-lint` for linting:
  ```bash
  golangci-lint run
  ```
- Error messages: lowercase, no punctuation
- Comments: full sentences with period
- Exported types/functions: always have doc comments

## Adding a New API Endpoint

1. Add the route in `api.go` → `setupRoutes()`
2. Implement the handler method on `*Server`
3. Add tests
4. Update `docs/api-reference.md`

Example:
```go
// In setupRoutes:
mux.HandleFunc("GET /api/games/{id}/players", s.handleGetPlayers)

// Handler:
func (s *Server) handleGetPlayers(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    // ...
    writeJSON(w, http.StatusOK, players)
}
```

## Adding a New Config Option

1. Add field to `Config` struct in `config.go`
2. Set default in `DefaultConfig()`
3. Add env var mapping in `LoadConfig()`
4. Add CLI flag in `main.go` if appropriate
5. Update `config.example.toml`
6. Update `docs/configuration.md`

## Branching Strategy

- `main` — stable, deployable
- `develop` — integration branch
- `feature/*` — new features
- `fix/*` — bug fixes

## Pull Request Process

1. Fork the repository
2. Create a feature branch
3. Make changes with tests
4. Ensure `go test ./...` passes
5. Ensure `go vet ./...` clean
6. Update documentation if needed
7. Submit PR against `develop`

## Release Process

1. Update version in `CHANGELOG.md`
2. Tag: `git tag v1.2.3`
3. Push: `git push origin v1.2.3`
4. Docker image built automatically:
   ```bash
   docker build --build-arg VERSION=1.2.3 -t natpunch-server:1.2.3 .
   ```
