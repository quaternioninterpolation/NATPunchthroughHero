# Changelog

All notable changes to NAT Punchthrough Hero.

Format: [Keep a Changelog](https://keepachangelog.com/)
Versioning: [Semantic Versioning](https://semver.org/)

## [0.1.0] — 2026-03-12

### Added
- Initial release
- Go master server with REST API and WebSocket signaling
- In-memory game store with TTL eviction and join codes
- TURN credential generation (HMAC-SHA1, RFC 5766)
- Multi-layer rate limiting (7 independent layers)
- IP blocklist/allowlist with CIDR support and file loading
- Automatic abuse detection with escalating blocks
- Embedded admin dashboard (dark theme, real-time stats)
- Interactive CLI setup wizard (`./server setup`)
- Built-in diagnostic checks (`./server check`)
- Auto-TLS via Let's Encrypt (or custom certificates)
- SIGHUP config reload support
- Graceful shutdown
- Docker support with multi-stage build (~10MB image)
- Docker Compose with coturn STUN/TURN server
- Production overlay (resource limits, log rotation, read-only root)
- VPS deploy script (Ubuntu/Debian/Fedora/RHEL)
- Cloud-init template for any VPS provider
- Go CLI test client (host, join, list, health, punch)
- Unity SDK stubs (NATTransport, MasterServerClient, NATTraversal)
- Comprehensive documentation (10 guides)
- Security hardening (denied-peer-ip, quotas, trusted proxies)
- TOML configuration with env var and CLI flag overrides
