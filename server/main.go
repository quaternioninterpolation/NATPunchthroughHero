package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

func main() {
	// Subcommands: serve (default), setup, version, health
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			configPath := "config.toml"
			if len(os.Args) > 3 && os.Args[2] == "--config" {
				configPath = os.Args[3]
			}
			RunSetupWizard(configPath)
			return
		case "version":
			fmt.Printf("NAT Punchthrough Hero %s\n", Version)
			return
		case "health":
			runHealthCheck()
			return
		case "serve":
			// Continue to normal startup
			os.Args = append(os.Args[:1], os.Args[2:]...)
		case "help", "--help", "-h":
			printHelp()
			return
		}
	}

	// Parse flags
	configPath := flag.String("config", "config.toml", "Path to config file")
	port := flag.Int("port", 0, "Override server port")
	flag.Parse()

	// Load config
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Apply CLI flag overrides
	if *port > 0 {
		cfg.Port = *port
	}

	// Create server
	srv := NewServer(cfg)
	defer srv.Stop()

	handler := srv.Handler()

	// Print startup banner
	printStartupBanner(cfg)

	// Create HTTP server
	httpSrv := &http.Server{
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Handle graceful shutdown
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	// Handle SIGHUP for config reload
	reloadCh := make(chan os.Signal, 1)
	signal.Notify(reloadCh, syscall.SIGHUP)
	go func() {
		for range reloadCh {
			log.Println("[server] SIGHUP received — reloading config")
			newCfg, err := LoadConfig(*configPath)
			if err != nil {
				log.Printf("[server] reload failed: %v", err)
				continue
			}
			srv.ipFilter.Reload(newCfg.IPFilter)
			log.Println("[server] config reloaded successfully")
		}
	}()

	// Start server
	if cfg.Domain != "" {
		// HTTPS mode with auto-TLS
		startTLSServer(httpSrv, handler, cfg, stopCh)
	} else if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		// HTTPS mode with custom certs
		httpSrv.Addr = fmt.Sprintf(":%d", cfg.Port)
		go func() {
			log.Printf("[server] starting HTTPS on :%d (custom certs)", cfg.Port)
			if err := httpSrv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != http.ErrServerClosed {
				log.Fatalf("HTTPS server error: %v", err)
			}
		}()
		<-stopCh
	} else {
		// HTTP mode
		listenAddr := fmt.Sprintf(":%d", cfg.Port)
		if cfg.DashboardAccess == "local" {
			// When dashboard is local-only, we still bind to all interfaces for API
			// but the admin routes check for localhost
			log.Printf("[server] dashboard restricted to localhost (use SSH tunnel for remote admin)")
		}
		httpSrv.Addr = listenAddr
		go func() {
			log.Printf("[server] starting HTTP on %s", listenAddr)
			if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("HTTP server error: %v", err)
			}
		}()
		<-stopCh
	}

	// Graceful shutdown
	log.Println("[server] shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	log.Println("[server] stopped")
}

// startTLSServer starts an HTTPS server with automatic Let's Encrypt certificates.
func startTLSServer(httpSrv *http.Server, handler http.Handler, cfg *Config, stopCh chan os.Signal) {
	certManager := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Domain),
		Cache:      autocert.DirCache("certs"),
	}

	httpSrv.Addr = ":443"
	httpSrv.TLSConfig = &tls.Config{
		GetCertificate: certManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	// HTTP server for ACME challenges + redirect to HTTPS
	httpRedirect := &http.Server{
		Addr:         ":80",
		Handler:      certManager.HTTPHandler(http.HandlerFunc(httpsRedirect)),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("[server] starting HTTP redirect on :80")
		if err := httpRedirect.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("[server] HTTP redirect error: %v", err)
		}
	}()

	go func() {
		log.Printf("[server] starting HTTPS on :443 (domain: %s)", cfg.Domain)
		if err := httpSrv.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			log.Fatalf("HTTPS server error: %v", err)
		}
	}()

	<-stopCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpRedirect.Shutdown(ctx)
}

// httpsRedirect redirects HTTP requests to HTTPS.
func httpsRedirect(w http.ResponseWriter, r *http.Request) {
	target := "https://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// runHealthCheck performs a health check against the local server.
// Used by Docker HEALTHCHECK to verify the container is healthy.
// Exits with code 0 if healthy, 1 if unhealthy.
func runHealthCheck() {
	port := 8080
	// Try to read port from config
	if cfg, err := LoadConfig("config.toml"); err == nil {
		port = cfg.Port
	}
	// Also check PORT env var
	if p := os.Getenv("PORT"); p != "" {
		if v, err := fmt.Sscanf(p, "%d", &port); err == nil && v == 1 {
			// port updated
		}
	}

	url := fmt.Sprintf("http://localhost:%d/api/health", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Health check failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Println("OK")
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "Health check failed: status %d\n", resp.StatusCode)
	os.Exit(1)
}

// printStartupBanner prints server configuration on startup.
func printStartupBanner(cfg *Config) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   NAT Punchthrough Hero                  ║")
	fmt.Printf("║   Version: %-29s ║\n", Version)
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	if cfg.Domain != "" {
		fmt.Printf("  API:       https://%s/api/games\n", cfg.Domain)
		fmt.Printf("  WebSocket: wss://%s/ws\n", cfg.Domain)
		fmt.Printf("  Dashboard: https://%s/admin\n", cfg.Domain)
	} else {
		fmt.Printf("  API:       http://%s:%d/api/games\n", cfg.ExternalIP, cfg.Port)
		fmt.Printf("  WebSocket: ws://%s:%d/ws\n", cfg.ExternalIP, cfg.Port)
		if cfg.DashboardAccess == "local" {
			fmt.Printf("  Dashboard: http://localhost:%d/admin (local only)\n", cfg.Port)
		} else {
			fmt.Printf("  Dashboard: http://%s:%d/admin\n", cfg.ExternalIP, cfg.Port)
		}
	}

	fmt.Printf("  TURN:      %s:%d\n", cfg.TurnHost, cfg.TurnPort)
	fmt.Printf("  Max Games: %d\n", cfg.MaxGames)
	fmt.Println()

	// Print security status
	features := []string{}
	if cfg.RateLimit.Enabled {
		features = append(features, "Rate Limiting")
	}
	if cfg.Protection.Enabled {
		features = append(features, "Auto-Protection")
	}
	if cfg.IPFilter.Mode != "off" {
		features = append(features, "IP Filter ("+cfg.IPFilter.Mode+")")
	}
	if cfg.GameAPIKey != "" {
		features = append(features, "API Key Auth")
	}
	if len(features) > 0 {
		fmt.Printf("  Security:  %s\n", strings.Join(features, " | "))
	}
	fmt.Println()
}

func printHelp() {
	fmt.Println("NAT Punchthrough Hero Server")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  server [command] [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  serve     Start the server (default)")
	fmt.Println("  setup     Run interactive setup wizard")
	fmt.Println("  health    Run health check (for Docker HEALTHCHECK)")
	fmt.Println("  version   Print version")
	fmt.Println("  help      Print this help")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --config <path>  Path to config.toml (default: config.toml)")
	fmt.Println("  --port <port>    Override server port")
	fmt.Println()
	fmt.Println("Environment variables override config.toml values.")
	fmt.Println("See config.example.toml for all options.")
}
