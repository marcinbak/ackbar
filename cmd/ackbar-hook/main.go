package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"ackbar/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("ackbar-hook version %s\n", version.Version)
		os.Exit(0)
	}

	agentFlag := flag.String("agent", "", "Name of the agent (e.g. codex, antigravity)")
	portFlag := flag.Int("port", 7777, "Port of the local ackbard daemon")
	eventFlag := flag.String("event", "", "Optional event name parameter")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("ackbar-hook version %s\n", version.Version)
		os.Exit(0)
	}

	agent := *agentFlag
	event := *eventFlag

	if agent == "" && len(flag.Args()) > 0 {
		agent = flag.Arg(0)
	}
	if event == "" && len(flag.Args()) > 1 {
		event = flag.Arg(1)
	}

	if agent == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent flag is required")
		os.Exit(1)
	}

	// Read stdin payload
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	// Construct request URL
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/hooks/%s", *portFlag, agent)
	if event != "" {
		url += "?event=" + event
	}

	// Send POST request
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending hook payload: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: daemon returned status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
