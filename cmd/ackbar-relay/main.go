package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"ackbar/internal/relay"
	"ackbar/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("ackbar-relay version %s\n", version.Version)
		os.Exit(0)
	}

	hostFlag := flag.String("host", "0.0.0.0", "Host address to bind relay server (default 0.0.0.0)")
	portFlag := flag.Int("port", 8888, "Port to bind relay server (default 8888)")
	secretFlag := flag.String("secret", "", "Optional secret required for daemons to register tunnels (or ACKBAR_RELAY_SECRET env var)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("ackbar-relay version %s\n", version.Version)
		os.Exit(0)
	}

	secret := *secretFlag
	if secret == "" {
		secret = os.Getenv("ACKBAR_RELAY_SECRET")
	}

	addr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)
	log.Printf("Starting Ackbar Outbound Relay Server (ackbar-relay) v%s...", version.Version)
	log.Printf("Listening on http://%s", addr)
	if secret != "" {
		log.Printf("Tunnel registration requires authentication secret")
	} else {
		log.Printf("Running in open registration mode (no secret required)")
	}

	server := relay.NewServer(secret)
	if err := http.ListenAndServe(addr, server.Mux()); err != nil {
		log.Fatalf("Relay server exited with error: %v", err)
	}
}
