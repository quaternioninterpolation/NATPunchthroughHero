#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════
# NAT Punchthrough Hero — VPS Deploy Script
# ══════════════════════════════════════════════════════════════
#
# One-liner to deploy on a fresh VPS (Ubuntu/Debian/Fedora/RHEL):
#   curl -sSL https://raw.githubusercontent.com/you/natpunch/main/deploy/deploy-vps.sh | bash
#
# Or run manually:
#   chmod +x deploy/deploy-vps.sh && ./deploy/deploy-vps.sh
#
# What this does:
#   1. Installs Docker + Docker Compose (if missing)
#   2. Opens firewall ports (UFW or firewalld)
#   3. Clones or updates the repo
#   4. Runs the setup wizard
#   5. Starts services
#
set -euo pipefail

# ── Colors ───────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log()  { echo -e "${GREEN}[✓]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[✗]${NC} $*" >&2; }

# ── Check root ───────────────────────────────────────────────
if [[ $EUID -ne 0 ]]; then
    err "Please run as root or with sudo"
    exit 1
fi

echo -e "${CYAN}"
echo "╔═══════════════════════════════════════╗"
echo "║   NAT Punchthrough Hero — Deployer    ║"
echo "╚═══════════════════════════════════════╝"
echo -e "${NC}"

# ── Detect package manager ───────────────────────────────────
if command -v apt-get &>/dev/null; then
    PKG_MGR="apt"
elif command -v dnf &>/dev/null; then
    PKG_MGR="dnf"
elif command -v yum &>/dev/null; then
    PKG_MGR="yum"
else
    err "Unsupported package manager. Install Docker manually."
    exit 1
fi

log "Detected package manager: $PKG_MGR"

# ── Install Docker ───────────────────────────────────────────
install_docker() {
    if command -v docker &>/dev/null; then
        log "Docker already installed: $(docker --version)"
        return
    fi

    warn "Installing Docker..."

    case $PKG_MGR in
        apt)
            apt-get update -qq
            apt-get install -y -qq ca-certificates curl gnupg lsb-release
            install -m 0755 -d /etc/apt/keyrings
            curl -fsSL https://download.docker.com/linux/$(. /etc/os-release && echo "$ID")/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
            chmod a+r /etc/apt/keyrings/docker.gpg
            echo \
              "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$(. /etc/os-release && echo "$ID") \
              $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
            apt-get update -qq
            apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin
            ;;
        dnf|yum)
            $PKG_MGR install -y -q dnf-plugins-core 2>/dev/null || true
            $PKG_MGR config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo 2>/dev/null || true
            $PKG_MGR install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin
            ;;
    esac

    systemctl enable docker
    systemctl start docker
    log "Docker installed successfully"
}

# ── Install Docker Compose (standalone, fallback) ───────────
install_compose() {
    if docker compose version &>/dev/null; then
        log "Docker Compose plugin available"
        COMPOSE_CMD="docker compose"
        return
    fi

    if command -v docker-compose &>/dev/null; then
        log "Docker Compose standalone available"
        COMPOSE_CMD="docker-compose"
        return
    fi

    warn "Installing Docker Compose..."
    COMPOSE_VERSION=$(curl -s https://api.github.com/repos/docker/compose/releases/latest | grep '"tag_name"' | cut -d'"' -f4)
    curl -SL "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
    chmod +x /usr/local/bin/docker-compose
    COMPOSE_CMD="docker-compose"
    log "Docker Compose installed: $(docker-compose --version)"
}

# ── Open firewall ports ──────────────────────────────────────
configure_firewall() {
    log "Configuring firewall..."

    PORTS_TCP="80 443 8080 3478"
    PORTS_UDP="3478 49152:50175"

    if command -v ufw &>/dev/null; then
        for port in $PORTS_TCP; do
            ufw allow "$port/tcp" 2>/dev/null || true
        done
        for port in $PORTS_UDP; do
            ufw allow "$port/udp" 2>/dev/null || true
        done
        log "UFW rules applied"

    elif command -v firewall-cmd &>/dev/null; then
        for port in $PORTS_TCP; do
            firewall-cmd --permanent --add-port="$port/tcp" 2>/dev/null || true
        done
        for port in $PORTS_UDP; do
            firewall-cmd --permanent --add-port="$port/udp" 2>/dev/null || true
        done
        firewall-cmd --reload 2>/dev/null || true
        log "firewalld rules applied"

    else
        warn "No firewall detected. Make sure these ports are open:"
        warn "  TCP: $PORTS_TCP"
        warn "  UDP: $PORTS_UDP"
    fi
}

# ── Deploy the application ───────────────────────────────────
INSTALL_DIR="/opt/natpunch"

deploy_app() {
    mkdir -p "$INSTALL_DIR"

    # If we're running from the repo already, copy files
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    REPO_ROOT="$(dirname "$SCRIPT_DIR")"

    if [[ -f "$REPO_ROOT/docker-compose.yml" ]]; then
        log "Copying from local repo..."
        cp -r "$REPO_ROOT"/* "$INSTALL_DIR/"
    elif [[ -d "$INSTALL_DIR/.git" ]]; then
        log "Updating existing installation..."
        cd "$INSTALL_DIR"
        git pull --ff-only
    else
        warn "Please clone the repository to $INSTALL_DIR first, or run this"
        warn "script from within the cloned repo."
        warn ""
        warn "  git clone https://github.com/you/natpunch.git $INSTALL_DIR"
        warn "  cd $INSTALL_DIR && ./deploy/deploy-vps.sh"
        exit 1
    fi

    cd "$INSTALL_DIR"
}

# ── Generate config ──────────────────────────────────────────
generate_config() {
    if [[ -f "$INSTALL_DIR/config.toml" ]]; then
        log "Config already exists at $INSTALL_DIR/config.toml"
        return
    fi

    log "Generating configuration..."

    # Detect external IP
    EXTERNAL_IP=$(curl -4 -s --connect-timeout 5 https://api.ipify.org || echo "")
    if [[ -z "$EXTERNAL_IP" ]]; then
        EXTERNAL_IP=$(curl -4 -s --connect-timeout 5 https://ifconfig.me || echo "unknown")
    fi

    # Generate secrets
    TURN_SECRET=$(openssl rand -hex 32)
    ADMIN_PASS=$(openssl rand -base64 16 | tr -d '=/+' | head -c 16)
    API_KEY=$(openssl rand -hex 24)

    # Write config
    cat > "$INSTALL_DIR/config.toml" <<EOF
# NAT Punchthrough Hero — Generated by deploy script
# $(date -u +"%Y-%m-%d %H:%M:%S UTC")

external_ip = "$EXTERNAL_IP"
http_port = 8080
turn_secret = "$TURN_SECRET"
admin_password = "$ADMIN_PASS"
game_api_key = "$API_KEY"

# Set your domain for automatic HTTPS:
# domain = "natpunch.example.com"

[rate_limit]
enabled = true

[ip_filter]
mode = "off"

[protection]
enabled = true
EOF

    log "Config written to $INSTALL_DIR/config.toml"
    echo ""
    echo -e "${CYAN}╔═══════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║     SAVE THESE CREDENTIALS NOW!       ║${NC}"
    echo -e "${CYAN}╠═══════════════════════════════════════╣${NC}"
    echo -e "${CYAN}║${NC} External IP  : ${GREEN}$EXTERNAL_IP${NC}"
    echo -e "${CYAN}║${NC} Admin User   : ${GREEN}admin${NC}"
    echo -e "${CYAN}║${NC} Admin Pass   : ${GREEN}$ADMIN_PASS${NC}"
    echo -e "${CYAN}║${NC} API Key      : ${GREEN}$API_KEY${NC}"
    echo -e "${CYAN}║${NC}"
    echo -e "${CYAN}║${NC} Dashboard    : ${GREEN}http://$EXTERNAL_IP:8080/admin/${NC}"
    echo -e "${CYAN}║${NC} API          : ${GREEN}http://$EXTERNAL_IP:8080/api/${NC}"
    echo -e "${CYAN}╚═══════════════════════════════════════╝${NC}"
    echo ""
}

# ── Start services ───────────────────────────────────────────
start_services() {
    cd "$INSTALL_DIR"

    log "Building and starting services..."
    $COMPOSE_CMD -f docker-compose.yml -f docker-compose.prod.yml up -d --build

    echo ""
    log "Services started! Checking health..."
    sleep 3

    if curl -sf http://localhost:8080/api/health > /dev/null 2>&1; then
        log "Server is healthy ✓"
    else
        warn "Server may still be starting. Check: $COMPOSE_CMD logs -f server"
    fi

    echo ""
    log "Deployment complete!"
    echo ""
    echo "Useful commands:"
    echo "  cd $INSTALL_DIR"
    echo "  $COMPOSE_CMD logs -f          # View logs"
    echo "  $COMPOSE_CMD restart           # Restart services"
    echo "  $COMPOSE_CMD down              # Stop services"
    echo "  $COMPOSE_CMD up -d --build     # Rebuild & restart"
    echo ""
}

# ── Main ─────────────────────────────────────────────────────
main() {
    install_docker
    install_compose
    configure_firewall
    deploy_app
    generate_config
    start_services
}

main "$@"
