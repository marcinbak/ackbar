package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"ackbar/internal/daemon"
	"ackbar/internal/provider"
	"ackbar/internal/version"
	"ackbar/internal/web"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("ackbard version %s\n", version.Version)
		os.Exit(0)
	}

	// Setup flags
	hostFlag := flag.String("host", "0.0.0.0", "Host address to bind daemon (default 0.0.0.0)")
	portFlag := flag.Int("port", 7777, "Port to bind daemon")
	tokenFlag := flag.String("token", "", "Secret token for API/WebSocket authentication (or ACKBAR_TOKEN env var)")
	relayFlag := flag.String("relay", "", "Public Ackbar Relay URL e.g. wss://relay.ackbar.dev/v1/relay/tunnel (or ACKBAR_RELAY_URL env var)")
	relaySecretFlag := flag.String("relay-secret", "", "Secret required by relay server (or ACKBAR_RELAY_SECRET env var)")
	dbPathFlag := flag.String("db", "", "Path to SQLite database file (default ~/.config/ackbar/ackbard.db)")
	logDirFlag := flag.String("log-dir", "", "Path to logs directory (default ~/.config/ackbar/logs)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("ackbard version %s\n", version.Version)
		os.Exit(0)
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

	// Initialize Self-Rotating Logger
	logCloser, logErr := daemon.SetupDaemonLogger(*logDirFlag)
	if logErr != nil {
		log.Printf("Warning: failed to initialize rotating file logger: %v", logErr)
	} else if logCloser != nil {
		defer logCloser.Close()
	}

	dbPath := *dbPathFlag
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to retrieve user home directory: %v", err)
		}
		dbPath = filepath.Join(home, ".config", "ackbar", "ackbard.db")
	}

	log.Printf("Starting Ackbar Daemon (ackbard) v%s...", version.Version)
	log.Printf("Database path: %s", dbPath)
	if token != "" {
		log.Printf("API Token authentication ENABLED")
	} else {
		log.Printf("API Token authentication disabled (open local/mesh mode)")
	}

	// Initialize SQLite Database
	db, err := daemon.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize HTTP Server
	server := daemon.NewServer(db)
	if token != "" {
		server.SetToken(token)
	}
	server.SetWebFS(web.GetFS())

	// Register Provider Adapters
	server.RegisterProvider(provider.NewClaudeProvider())
	server.RegisterProvider(provider.NewCodexProvider())
	server.RegisterProvider(provider.NewAntigravityProvider())

	// Start asynchronous background scanner & liveness loop
	server.StartBackgroundLoop(context.Background())

	// Start outbound relay tunnel client if configured
	if relayURL != "" {
		server.StartRelayClient(context.Background(), relayURL, relaySecret)
	}

	// Listen on configured host address (default 127.0.0.1 loopback)
	addr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)
	log.Printf("Listening on http://%s", addr)

	if err := http.ListenAndServe(addr, server.Mux()); err != nil {
		log.Fatalf("Server exited with error: %v", err)
	}
}
