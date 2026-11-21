package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"ackbar/internal/agents"
	"ackbar/internal/daemon"
	"ackbar/internal/provider"
	"ackbar/internal/version"
	"ackbar/web"
)

func main() {
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		log.Fatalf("Failed to initialize plugin registry: %v", err)
	}
	if len(os.Args) > 1 && os.Args[1] == "--headless-worker" {
		os.Exit(daemon.RunHeadlessWorkerWithRegistry(os.Args[2:], reg))
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("ackbard version %s\n", version.Version)
		os.Exit(0)
	}

	// Setup flags
	hostFlag := flag.String("host", "0.0.0.0", "Host address to bind daemon (default 0.0.0.0)")
	portFlag := flag.Int("port", 7777, "Port to bind daemon")
	hostNameFlag := flag.String("host-name", "", "Canonical machine identifier (or ACKBAR_HOST env var, default 'local')")
	nameFlag := flag.String("name", "", "Alias for --host-name")
	displayNameFlag := flag.String("display-name", "", "Human-friendly host display name (or ACKBAR_DISPLAY_NAME env var)")
	tokenFlag := flag.String("token", "", "Secret token for API/WebSocket authentication (or ACKBAR_TOKEN env var)")
	relayFlag := flag.String("relay", "", "Public Ackbar Relay URL e.g. wss://relay.ackbar.dev/v1/relay/tunnel (or ACKBAR_RELAY_URL env var)")
	relaySecretFlag := flag.String("relay-secret", "", "Secret required by relay server (or ACKBAR_RELAY_SECRET env var)")
	ntfyTopicFlag := flag.String("ntfy-topic", "", "ntfy.sh topic for push notifications (or ACKBAR_NTFY_TOPIC env var)")
	ntfyServerFlag := flag.String("ntfy-server", "", "ntfy.sh server URL (or ACKBAR_NTFY_SERVER env var, default https://ntfy.sh)")
	dbPathFlag := flag.String("db", "", "Path to SQLite database file (default ~/.config/ackbar/ackbard.db)")
	logDirFlag := flag.String("log-dir", "", "Path to logs directory (default ~/.config/ackbar/logs)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("ackbard version %s\n", version.Version)
		os.Exit(0)
	}

	hostName := *hostNameFlag
	if hostName == "" {
		hostName = *nameFlag
	}
	if hostName == "" {
		hostName = os.Getenv("ACKBAR_HOST")
	}

	displayName := *displayNameFlag
	if displayName == "" {
		displayName = os.Getenv("ACKBAR_DISPLAY_NAME")
	}

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("ACKBAR_TOKEN")
	}

	relayURL := *relayFlag
	if relayURL == "" {
		relayURL = os.Getenv("ACKBAR_RELAY_URL")
	}

	relaySecret := *relaySecretFlag
	if relaySecret == "" {
		relaySecret = os.Getenv("ACKBAR_RELAY_SECRET")
	}

	ntfyTopic := *ntfyTopicFlag
	if ntfyTopic == "" {
		ntfyTopic = os.Getenv("ACKBAR_NTFY_TOPIC")
	}

	ntfyServer := *ntfyServerFlag
	if ntfyServer == "" {
		ntfyServer = os.Getenv("ACKBAR_NTFY_SERVER")
	}

	// launchd and minimal service environments omit LANG, degrading tmux and child PTYs to ASCII _ sanitization.
	if os.Getenv("LANG") == "" && os.Getenv("LC_ALL") == "" && os.Getenv("LC_CTYPE") == "" {
		if runtime.GOOS == "darwin" {
			_ = os.Setenv("LANG", "en_US.UTF-8")
		} else {
			_ = os.Setenv("LANG", "C.UTF-8")
		}
	}

	// Initialize Self-Rotating Logger
	logCloser, logErr := daemon.SetupDaemonLogger(*logDirFlag)
	if logErr != nil {
		log.Printf("Warning: failed to initialize rotating file logger: %v", logErr)
	} else if logCloser != nil {
		defer logCloser.Close()
	}

	homeDir, homeErr := os.UserHomeDir()
	dbPath := *dbPathFlag
	if dbPath == "" {
		if homeErr != nil {
			log.Fatalf("Failed to retrieve user home directory: %v", homeErr)
		}
		dbPath = filepath.Join(homeDir, ".config", "ackbar", "ackbard.db")
	}
	if homeErr == nil && homeDir != "" {
		if _, isoErr := daemon.EnsureServiceProcessIsolation(homeDir); isoErr != nil {
			log.Printf("Warning: service process isolation check failed: %v", isoErr)
		}
	}

	log.Printf("Starting Ackbar Daemon (ackbard) v%s...", version.Version)
	log.Printf("Database path: %s", dbPath)
	if token != "" {
		log.Printf("API Token authentication ENABLED")
	} else {
		log.Printf("API Token authentication disabled (open local/mesh mode)")
	}

	// Bind TCP listener before opening DB to fail fast on port collision
	addr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Server exited with error: %v", err)
	}
	defer ln.Close()

	// Initialize SQLite Database
	db, err := daemon.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize HTTP Server with plugins before startup worker reattach
	server, err := daemon.NewServerWithPlugins(db, reg.List()...)
	if err != nil {
		log.Fatalf("Failed to initialize server plugins: %v", err)
	}
	if hostName == "" || strings.EqualFold(hostName, "local") {
		if dbHost, err := db.GetSetting("host_name"); err == nil && dbHost != "" && !strings.EqualFold(dbHost, "local") {
			hostName = dbHost
		} else if inferred := daemon.InferExistingLocalHostName(db); inferred != "" {
			hostName = inferred
		} else if sysHost := daemon.DefaultSystemHostName(); sysHost != "" {
			hostName = sysHost
		}
	}
	if hostName != "" || displayName != "" {
		server.SetHostIdentity(hostName, displayName)
	}
	if ntfyServer != "" || ntfyTopic != "" {
		server.SetNtfyConfig(ntfyServer, ntfyTopic)
	}
	if server.DisplayName() != "" {
		log.Printf("Host identity: %s (%q)", server.HostName(), server.DisplayName())
	} else {
		log.Printf("Host identity: %s", server.HostName())
	}
	if token != "" {
		server.SetToken(token)
	}
	server.SetWebFS(web.GetFS())

	// Register Provider Adapters
	server.RegisterProvider(provider.NewClaudeProvider())

	// Start Antigravity JETSKI-AUDIO/1 Live Voice Bridge (default 127.0.0.1:4713)
	server.EnsureVoiceBridge()

	// Start asynchronous background scanner & liveness loop
	server.StartBackgroundLoop(context.Background())

	// Start outbound relay tunnel client if configured
	if relayURL != "" {
		server.StartRelayClient(context.Background(), relayURL, relaySecret)
	}

	log.Printf("Listening on http://%s", addr)

	if err := http.Serve(ln, server.Mux()); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server exited with error: %v", err)
	}
}
