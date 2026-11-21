package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"ackbar/internal/agent"
	"ackbar/internal/agents"
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

	agentName := *agentFlag
	event := *eventFlag

	if agentName == "" && len(flag.Args()) > 0 {
		agentName = flag.Arg(0)
	}
	if event == "" && len(flag.Args()) > 1 {
		event = flag.Arg(1)
	}

	if agentName == "" {
		fmt.Fprintln(os.Stderr, "Error: --agent flag is required")
		os.Exit(1)
	}

	reg, _ := agents.NewDefaultRegistry()
	if reg != nil {
		if p, ok := reg.Get(agentName); ok {
			agentName = p.Key()
		}
	}

	// Read stdin payload
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	// Construct request URL
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/hooks/%s", *portFlag, agentName)
	if event != "" {
		url += "?event=" + event
	}

	// Send POST request
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending hook payload: %v\n", err)
		outputHookSuccess(reg, agentName, event)
		os.Exit(0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: daemon returned status %d\n", resp.StatusCode)
		outputHookSuccess(reg, agentName, event)
		os.Exit(0)
	}

	outputHookSuccess(reg, agentName, event)
}

func outputHookSuccess(reg *agent.Registry, agentName, event string) {
	if h, ok := reg.AsHooks(agentName); ok {
		fmt.Println(string(h.HookAckPayload(event)))
		return
	}
	fmt.Println("{}")
}
