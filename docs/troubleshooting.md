# Troubleshooting

Common issues and solutions for NAT Punchthrough Hero.

## Diagnostic Tool

Run the built-in diagnostic check:

```bash
./natpunch-server check
```

This verifies:
- External IP detection
- Port availability (TCP 8080, UDP 3478)
- DNS resolution (if domain configured)
- STUN binding test
- TLS certificate status

## Common Issues

### Server Won't Start

**Port already in use:**
```
listen tcp :8080: bind: address already in use
```
→ Another process is using port 8080. Find and stop it:
```bash
lsof -i :8080    # Linux/Mac
netstat -ano | findstr :8080   # Windows
```
Or change the port in `config.toml`:
```toml
http_port = 9090
```

**Permission denied on port 80/443:**
```
listen tcp :443: bind: permission denied
```
→ Ports below 1024 require root. Either:
- Run with `sudo` or as root (Docker handles this)
- Use a reverse proxy
- Use port 8080 without TLS

### Can't Connect from Unity

**Connection refused:**
- Check the server is running: `curl http://your-server:8080/api/health`
- Check firewall rules: port 8080 must be open
- If using HTTPS, ensure the domain resolves correctly

**401 Unauthorized:**
- API key mismatch. Check `game_api_key` in config matches the Unity client's `apiKey`
- If `game_api_key` is empty in config, auth is disabled

**CORS errors (browser/WebGL):**
The server sets CORS headers for relevant endpoints. If you're testing from a browser, ensure:
```
Access-Control-Allow-Origin: *
```
is in the response headers.

### NAT Punchthrough Fails

**All players using TURN relay:**
- Normal for symmetric NAT (common on mobile networks, university WiFi)
- Check if STUN is reachable: `stun-client your-server 3478`
- Ensure UDP port 3478 is open on both the server and player firewalls

**UPnP never works:**
- Many ISP routers disable UPnP by default
- Enterprise/university networks block UPnP
- This is expected — STUN/TURN will handle it

**STUN discovery fails:**
- coturn not running: `docker compose logs coturn`
- UDP 3478 blocked by firewall
- Test with: `stun your-server 3478` (install `stun-client` package)

**TURN relay fails:**
- Check coturn logs: `docker compose logs coturn`
- Verify shared secret matches between Go server and coturn
- Check UDP ports 49152-50175 are open
- Test with Trickle ICE: https://webrtc.github.io/samples/src/content/peerconnection/trickle-ice/

### coturn Issues

**coturn won't start:**
```
docker compose logs coturn
```

Common errors:
- `Cannot bind socket`: Port conflict or `network_mode: host` issue
- `Unknown option`: Check the command flags in `docker-compose.yml`

**"403 Forbidden" in TURN:**
- Time-limited credentials expired (default TTL: 24h)
- Request fresh credentials from `/api/games/{id}/turn`
- Check server and coturn clocks are synchronized

**High bandwidth usage:**
- coturn is limited to 256Kbps per session by default
- Increase `--max-bps` if your game needs more
- Monitor with: `docker stats natpunch-coturn`

### Docker Issues

**Container exits immediately:**
```bash
docker compose logs server
```

**Image won't build:**
```bash
# Clean build
docker compose build --no-cache

# Check Go build errors
docker compose build server 2>&1 | tail -50
```

**coturn can't bind to ports:**
`network_mode: host` requires the ports to be free on the host. Check:
```bash
ss -tulnp | grep -E "3478|49152"
```

### TLS / HTTPS Issues

**Let's Encrypt fails:**
- Port 80 must be open (ACME HTTP-01 challenge)
- Domain must resolve to this server's IP
- Rate limit: 50 certificates per domain per week

**Certificate expired:**
- Autocert renews automatically
- If using custom certs, update `tls_cert` and `tls_key`, then restart

**Mixed content (HTTPS server, HTTP WebSocket):**
Unity clients should use `wss://` for WebSocket when the server uses HTTPS.

### Performance Issues

**High CPU usage:**
- Check for runaway WebSocket connections: `/admin/api/stats`
- Enable rate limiting if disabled
- Consider if you're being DDoSed — check auto-blocked IPs in dashboard

**High memory usage:**
- Each game uses ~10KB, each WebSocket ~4KB
- 500 games + 1000 WebSockets ≈ 10MB
- If much higher, check for leaking connections

**Slow responses:**
- Enable `log_level = "debug"` to see timing
- Check network latency to server: `ping your-server`

### Rate Limiting / Blocking Issues

**Legitimate users getting 429:**
- Increase rate limits in `config.toml`
- Check if auto-block is too aggressive
- View blocked IPs: `curl -u admin:pass http://server:8080/admin/api/blocked`

**Unblock an IP:**
```bash
curl -X DELETE http://localhost:8080/admin/api/blocklist/1.2.3.4 \
  -u admin:yourpassword
```

**Reload IP filter files:**
```bash
kill -HUP $(pidof natpunch-server)
# or
docker compose kill -s HUP server
```

## Log Levels

Set in `config.toml`:

| Level | Shows |
|-------|-------|
| `debug` | Everything (very verbose) |
| `info` | Startup, connections, game events |
| `warn` | Rate limits, config issues |
| `error` | Failures, crashes |

## Getting Help

1. Check this troubleshooting guide
2. Run `./natpunch-server check` for diagnostics
3. Check server logs: `docker compose logs -f server`
4. Check dashboard: `http://server:8080/admin/`
5. Open an issue with:
   - Server version (`./natpunch-server version`)
   - OS and Docker version
   - Relevant log output
   - Steps to reproduce
