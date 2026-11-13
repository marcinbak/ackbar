package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"ackbar/internal/daemon"
	"ackbar/internal/mcp"
	"ackbar/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "update" || os.Args[1] == "upgrade") {
		runUpdateCmd(os.Args[2:])
		os.Exit(0)
	}

	if len(os.Args) > 1 && os.Args[1] == "check-update" {
		runCheckUpdateCmd(os.Args[2:])
		os.Exit(0)
	}

	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCPCmd(os.Args[2:])
		os.Exit(0)
	}

	if len(os.Args) > 1 && (os.Args[1] == "agent" || os.Args[1] == "agents") {
		runAgentCmd(os.Args[2:])
		os.Exit(0)
	}

	if len(os.Args) > 1 && (os.Args[1] == "account" || os.Args[1] == "accounts") {
		runAccountCmd(os.Args[2:])
		os.Exit(0)
	}

	shouldSetup := false
	isGUICmd := false
	noTUI := false

	for _, arg := range os.Args[1:] {
		switch arg {
		case "help", "--help", "-h":
			printUsage()
			os.Exit(0)
		case "version", "--version", "-v":
			fmt.Printf("ackbar version %s\n", version.Version)
			os.Exit(0)
		case "setup-hooks", "--setup-hooks":
			shouldSetup = true
		case "gui", "--gui", "web", "--web":
			isGUICmd = true
		case "--no-tui", "-no-tui":
			noTUI = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown command or flag: %s\nRun 'ackbar help' for usage.\n", arg)
			os.Exit(1)
		}
	}

	if shouldSetup {
		if err := runSetupHooks(); err != nil {
			fmt.Printf("Error setting up hooks: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🚀 Hook setup complete (v%s)!\n", version.Version)
		if !isGUICmd {
			os.Exit(0)
		}
	}

	// Default invocation (no args, or web/gui)
	ensureDaemonRunning()
	if noTUI {
		os.Exit(0)
	}

	fmt.Printf("⚓ Ackbar Web GUI running at http://127.0.0.1:7777\n")
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", "http://127.0.0.1:7777").Start()
	} else {
		_ = exec.Command("xdg-open", "http://127.0.0.1:7777").Start()
	}
}

func printUsage() {
	fmt.Println(`Ackbar - Supervisor & control plane for agentic programming sessions

Usage:
  ackbar [command] [flags]

Available Commands:
  web, gui          Launch the Web GUI in your browser (default)
  setup-hooks       Auto-configure agent hooks and install binaries
  agent, agents     Manage agent providers, MCP servers, and skills
  account, accounts Manage agent profiles and authentication accounts
  mcp               Run the Ackbar MCP server over stdio
  update, upgrade   Update Ackbar to the latest release
  check-update      Check GitHub for newer Ackbar releases
  version           Print version information and exit
  help              Show this help message

Flags:
  -v, --version     Print version information and exit
  -h, --help        Show this help message
  --no-tui          Headless mode (ensure daemon is running without opening browser)`)
}

func ensureDaemonRunning() {
	httpClient := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := httpClient.Get("http://127.0.0.1:7777/v1/version")
	if err == nil {
		var verRes map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&verRes); err == nil {
			resp.Body.Close()
			runningVer := verRes["version"]
			if runningVer == version.Version {
				return
			}
			fmt.Printf("🔄 Outdated daemon detected (running v%s, client v%s). Restarting daemon...\n", runningVer, version.Version)
			_, _ = httpClient.Post("http://127.0.0.1:7777/v1/shutdown", "application/json", nil)
			time.Sleep(300 * time.Millisecond)
		} else {
			resp.Body.Close()
		}
	}

	fmt.Println("🚀 Local ackbard daemon starting in background...")

	home, _ := os.UserHomeDir()
	ackbardBin := "ackbard"
	if home != "" {
		localBin := filepath.Join(home, ".local", "bin", "ackbard")
		if _, err := os.Stat(localBin); err == nil {
			ackbardBin = localBin
		}
	}

	cmd := exec.Command(ackbardBin)
	if err := cmd.Start(); err != nil {
		cmdRun := exec.Command("go", "run", "./cmd/ackbard")
		_ = cmdRun.Start()
	}

	// Poll up to 3 seconds for daemon port 7777 to become ready
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := httpClient.Get("http://127.0.0.1:7777/v1/version")
		if err == nil {
			var verRes map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&verRes); err == nil {
				resp.Body.Close()
				if verRes["version"] == version.Version {
					return
				}
			} else {
				resp.Body.Close()
			}
		}
	}
}

func openLocalDaemonDB(home string) *daemon.DB {
	candidates := []string{
		filepath.Join(home, ".config", "ackbar", "ackbard.db"),
		filepath.Join(home, ".ackbard.db"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			if db, err := daemon.InitDB(p); err == nil {
				return db
			}
		}
	}
	if db, err := daemon.InitDB(filepath.Join(home, ".config", "ackbar", "ackbard.db")); err == nil {
		return db
	}
	db, _ := daemon.InitDB(filepath.Join(home, ".ackbard.db"))
	return db
}

func runSetupHooks() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home: %w", err)
	}

	// 1. Build & install ackbar-hook and ackbard binaries to ~/.local/bin
	localBin := filepath.Join(home, ".local", "bin")
	_ = os.MkdirAll(localBin, 0755)

	installBinary := func(pkg, target string) {
		tmpTarget := target + ".new"
		_ = os.Remove(tmpTarget)
		cmd := exec.Command("go", "build", "-o", tmpTarget, pkg)
		if err := cmd.Run(); err != nil {
			_ = os.Remove(tmpTarget)
			cmd2 := exec.Command("go", "install", "ackbar/"+pkg+"@latest")
			_ = cmd2.Run()
			return
		}
		if err := os.Rename(tmpTarget, target); err != nil {
			_ = os.Remove(tmpTarget)
		}
	}

	hookBinTarget := filepath.Join(localBin, "ackbar-hook")
	installBinary("./cmd/ackbar-hook", hookBinTarget)
	fmt.Printf("✅ Installed ackbar-hook (v%s) to %s\n", version.Version, hookBinTarget)

	ackbardBinTarget := filepath.Join(localBin, "ackbard")
	installBinary("./cmd/ackbard", ackbardBinTarget)
	fmt.Printf("✅ Installed ackbard daemon (v%s) to %s\n", version.Version, ackbardBinTarget)

	// 2. Configure Claude Code settings (~/.claude/settings.json)
	claudeSettingsPath := filepath.Join(home, ".claude", "settings.json")
	if data, err := os.ReadFile(claudeSettingsPath); err == nil {
		content := string(data)
		content = strings.ReplaceAll(content, "Write(~/.memories/log/**)", "Edit(~/.memories/log/**)")
		content = strings.ReplaceAll(content, fmt.Sprintf("Write(%s/.memories/log/**)", home), fmt.Sprintf("Edit(%s/.memories/log/**)", home))

		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(content), &settings); err == nil {
			hooks, _ := settings["hooks"].(map[string]interface{})
			if hooks == nil {
				hooks = make(map[string]interface{})
			}
			// Correct Claude Code schema: matcher + hooks array
			correctHookEntry := map[string]interface{}{
				"matcher": "",
				"hooks": []map[string]string{
					{
						"type": "http",
						"url":  "http://127.0.0.1:7777/v1/hooks/claude-code",
					},
				},
			}

			eventKeys := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Notification", "Stop"}
			for _, evtKey := range eventKeys {
				existingList, _ := hooks[evtKey].([]interface{})
				var validList []interface{}
				hasHook := false

				for _, item := range existingList {
					if m, ok := item.(map[string]interface{}); ok {
						if innerHooks, ok := m["hooks"].([]interface{}); ok {
							isAckbarHook := false
							for _, h := range innerHooks {
								if hm, ok := h.(map[string]interface{}); ok {
									u, _ := hm["url"].(string)
									cmdStr, _ := hm["command"].(string)
									if strings.Contains(u, "/v1/hooks/claude-code") || strings.Contains(cmdStr, "ackbar-hook") {
										isAckbarHook = true
									}
								}
							}
							if isAckbarHook {
								if hasHook {
									continue
								}
								hasHook = true
							}
							validList = append(validList, item)
						}
					}
				}

				if !hasHook {
					validList = append(validList, correctHookEntry)
				}
				hooks[evtKey] = validList
			}
			settings["hooks"] = hooks

			newData, _ := json.MarshalIndent(settings, "", "  ")
			_ = os.WriteFile(claudeSettingsPath, newData, 0644)
			fmt.Printf("✅ Configured Claude Code HTTP hook and fixed permission rules in %s\n", claudeSettingsPath)
		}
	}

	// Clean up legacy hooks in ~/.claude.json if present
	legacyClaudePath := filepath.Join(home, ".claude.json")
	if data, err := os.ReadFile(legacyClaudePath); err == nil {
		var legMap map[string]interface{}
		if err := json.Unmarshal(data, &legMap); err == nil {
			if _, exists := legMap["hooks"]; exists {
				delete(legMap, "hooks")
				if updated, err := json.MarshalIndent(legMap, "", "  "); err == nil {
					_ = os.WriteFile(legacyClaudePath, updated, 0644)
					fmt.Printf("🧹 Cleaned legacy hooks from %s\n", legacyClaudePath)
				}
			}
		}
	}

	// 3. Configure Antigravity hooks (~/.gemini/config/hooks.json)
	geminiHooksDir := filepath.Join(home, ".gemini", "config")
	_ = os.MkdirAll(geminiHooksDir, 0755)
	geminiHooksFile := filepath.Join(geminiHooksDir, "hooks.json")

	var existingHooks map[string]interface{}
	if data, err := os.ReadFile(geminiHooksFile); err == nil {
		_ = json.Unmarshal(data, &existingHooks)
	}
	if existingHooks == nil {
		existingHooks = make(map[string]interface{})
	}
	delete(existingHooks, "hooks") // remove legacy flat array if present

	hookCmd := fmt.Sprintf("%s --agent=antigravity", hookBinTarget)
	existingHooks["ackbar"] = map[string]interface{}{
		"PreInvocation": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=PreInvocation",
			},
		},
		"PostInvocation": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=PostInvocation",
			},
		},
		"Stop": []map[string]interface{}{
			{
				"type":    "command",
				"command": hookCmd + " --event=Stop",
			},
		},
		"PreToolUse": []map[string]interface{}{
			{
				"matcher": "*",
				"hooks": []map[string]interface{}{
					{
						"type":    "command",
						"command": hookCmd + " --event=PreToolUse",
					},
				},
			},
		},
		"PostToolUse": []map[string]interface{}{
			{
				"matcher": "*",
				"hooks": []map[string]interface{}{
					{
						"type":    "command",
						"command": hookCmd + " --event=PostToolUse",
					},
				},
			},
		},
	}

	if updated, err := json.MarshalIndent(existingHooks, "", "  "); err == nil {
		_ = os.WriteFile(geminiHooksFile, updated, 0644)
		fmt.Printf("✅ Configured Antigravity lifecycle hooks in %s\n", geminiHooksFile)
	}

	// 4. Provision agent MCP servers and ackbar-tasks skills
	fmt.Printf("⚡ Configuring agent MCP servers and ackbar-tasks skills...\n")
	if _, err := daemon.ProvisionAgents(home, []string{"all"}); err != nil {
		fmt.Printf("⚠️ Warning: Agent provisioning encountered an error: %v\n", err)
	} else {
		fmt.Printf("✅ Provisioned agent MCP servers and skills\n")
	}

	// 5. Discover and sync external skills repository if present
	db := openLocalDaemonDB(home)
	if db != nil {
		defer db.Close()
	}
	repoPath, repoURL := daemon.DiscoverSkillsRepo(home, db)
	if repoPath != "" || repoURL != "" {
		fmt.Printf("🔄 Syncing external skills repository...\n")
		syncStatus, err := daemon.SyncSkillsRepo(home, db, daemon.SyncSkillsRepoOptions{})
		if err != nil {
			fmt.Printf("⚠️ Note: Skills repository sync: %v\n", err)
		} else if syncStatus != nil && syncStatus.Cloned {
			fmt.Printf("✅ Synced external skills repository (%s @ %s)\n", syncStatus.RepoPath, syncStatus.LocalSHA)
		}
	}

	return nil
}

// fetchAccounts queries an Ackbar daemon for configured accounts
func fetchAccounts(hostURL string) ([]*daemon.AgentAccount, error) {
	reqURL := fmt.Sprintf("%s/v1/accounts", strings.TrimSuffix(hostURL, "/"))
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch accounts failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var accounts []*daemon.AgentAccount
	if err := json.NewDecoder(resp.Body).Decode(&accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// saveAccount registers or updates an agent account on an Ackbar daemon
func saveAccount(hostURL string, payload interface{}) (*daemon.AgentAccount, error) {
	reqURL := fmt.Sprintf("%s/v1/accounts", strings.TrimSuffix(hostURL, "/"))
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(reqURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("save account failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var acc daemon.AgentAccount
	if err := json.NewDecoder(resp.Body).Decode(&acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

// deleteAccount removes a non-default account
func deleteAccount(hostURL, accountID string) error {
	reqURL := fmt.Sprintf("%s/v1/accounts/%s", strings.TrimSuffix(hostURL, "/"), url.PathEscape(accountID))
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete account failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// setDefaultAccount sets an account as default for its agent
func setDefaultAccount(hostURL, accountID string) error {
	reqURL := fmt.Sprintf("%s/v1/accounts/%s/default", strings.TrimSuffix(hostURL, "/"), url.PathEscape(accountID))
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(reqURL, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set default account failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// loginAccount requests the daemon to spawn an interactive login session in tmux
func loginAccount(hostURL, accountID string) (string, error) {
	reqURL := fmt.Sprintf("%s/v1/accounts/%s/login", strings.TrimSuffix(hostURL, "/"), url.PathEscape(accountID))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(reqURL, "application/json", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login account failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var res struct {
		Status      string `json:"status"`
		TmuxName    string `json:"tmux_name"`
		TmuxSession string `json:"tmux_session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("failed to decode login response: %w", err)
	}
	sessionName := res.TmuxName
	if sessionName == "" {
		sessionName = res.TmuxSession
	}
	if sessionName == "" {
		return "", fmt.Errorf("daemon returned empty tmux session name")
	}
	return sessionName, nil
}

func runAccountCmd(args []string) {
	ensureDaemonRunning()
	hostURL := "http://127.0.0.1:7777"

	if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
		accounts, err := fetchAccounts(hostURL)
		if err != nil {
			fmt.Printf("❌ Failed to fetch accounts: %v\n", err)
			os.Exit(1)
		}
		if len(accounts) == 0 {
			fmt.Println("No accounts configured.")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tAGENT\tNAME\tDISPLAY NAME\tDEFAULT\tLOGGED IN\tCONFIG DIR")
		for _, a := range accounts {
			defStr := "no"
			if a.IsDefault {
				defStr = "yes"
			}
			logStr := "no"
			if a.IsLoggedIn {
				logStr = "yes"
			}
			cfgDir := a.ConfigDir
			if cfgDir == "" {
				cfgDir = "(native default)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Agent, a.Name, a.DisplayName, defStr, logStr, cfgDir)
		}
		_ = w.Flush()
		return
	}

	subcmd := args[0]
	switch subcmd {
	case "add":
		fs := flag.NewFlagSet("account add", flag.ExitOnError)
		displayName := fs.String("display-name", "", "Display name for UI")
		apiKey := fs.String("api-key", "", "API key to set in profile environment")
		configDir := fs.String("config-dir", "", "Custom configuration directory")
		isDefault := fs.Bool("default", false, "Set this account as default for the agent")
		allHosts := fs.Bool("all-hosts", false, "Propagate account creation across all connected fleet hosts")
		shouldLogin := fs.Bool("login", false, "Launch interactive login session in tmux immediately")
		_ = fs.Parse(args[1:])

		tail := fs.Args()
		if len(tail) < 2 {
			fmt.Println("Usage: ackbar account add <agent> <name> [flags]")
			fmt.Println("  Agents: claude-code, antigravity")
			fmt.Println("  Flags:")
			fmt.Println("    --display-name <name>  Human-readable name (e.g. \"Work Anthropic\")")
			fmt.Println("    --api-key <key>        API key (e.g. ANTHROPIC_API_KEY)")
			fmt.Println("    --config-dir <path>    Custom config directory")
			fmt.Println("    --default              Set as default profile")
			fmt.Println("    --all-hosts            Propagate to connected fleet hosts")
			fmt.Println("    --login                Immediately launch tmux login session")
			os.Exit(1)
		}
		agent := strings.TrimSpace(tail[0])
		name := strings.TrimSpace(tail[1])

		envMap := make(map[string]string)
		if *apiKey != "" {
			if agent == "claude-code" {
				envMap["ANTHROPIC_API_KEY"] = *apiKey
			} else if agent == "antigravity" {
				envMap["GEMINI_API_KEY"] = *apiKey
			}
		}

		payload := map[string]interface{}{
			"agent":         agent,
			"name":          name,
			"display_name":  *displayName,
			"config_dir":    *configDir,
			"is_default":    *isDefault,
			"propagate_all": *allHosts,
			"env":           envMap,
		}

		acc, err := saveAccount(hostURL, payload)
		if err != nil {
			fmt.Printf("❌ Failed to create account: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Account '%s' created successfully!\n", acc.ID)
		if *allHosts {
			fmt.Println("🌐 Fleet propagation initiated in background.")
		}

		if *shouldLogin {
			runLoginCmd(hostURL, acc.ID)
		}

	case "remove", "rm", "del", "delete":
		if len(args) < 2 {
			fmt.Println("Usage: ackbar account remove <id>")
			os.Exit(1)
		}
		id := args[1]
		if err := deleteAccount(hostURL, id); err != nil {
			fmt.Printf("❌ Failed to delete account: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Account '%s' removed successfully.\n", id)

	case "default":
		if len(args) < 2 {
			fmt.Println("Usage: ackbar account default <id>")
			os.Exit(1)
		}
		id := args[1]
		if err := setDefaultAccount(hostURL, id); err != nil {
			fmt.Printf("❌ Failed to set default account: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Account '%s' is now default.\n", id)

	case "login":
		if len(args) < 2 {
			fmt.Println("Usage: ackbar account login <id>")
			os.Exit(1)
		}
		id := args[1]
		runLoginCmd(hostURL, id)

	case "help", "--help", "-h":
		fmt.Println("Usage: ackbar account <subcommand> [flags]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  list, ls                 List all configured accounts")
		fmt.Println("  add <agent> <name>       Add a new account profile")
		fmt.Println("  remove, rm <id>          Remove an account (e.g. claude-code:work)")
		fmt.Println("  default <id>             Set account as default for its agent")
		fmt.Println("  login <id>               Launch interactive login session in tmux")
		return

	default:
		fmt.Printf("Unknown subcommand: %s\nRun 'ackbar account help' for usage.\n", subcmd)
		os.Exit(1)
	}
}

func runLoginCmd(hostURL, accountID string) {
	fmt.Printf("🚀 Spawning login session for account '%s'...\n", accountID)
	tmuxSession, err := loginAccount(hostURL, accountID)
	if err != nil {
		fmt.Printf("❌ Failed to spawn login session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Attaching to tmux session '%s' (press Ctrl+B then D to detach)...\n", tmuxSession)
	cmd := exec.Command("tmux", "attach", "-t", tmuxSession)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	fmt.Println("✅ Detached from login session.")
}

func runMCPCmd(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcp.NewServer(nil, os.Stdin, os.Stdout)
	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server exited: %v\n", err)
		os.Exit(1)
	}
}

func runAgentCmd(args []string) {
	if len(args) == 0 || args[0] == "status" || args[0] == "list" || args[0] == "ls" {
		statuses, err := daemon.GetAgentStatuses("")
		if err != nil {
			fmt.Printf("❌ Failed to query agent statuses: %v\n", err)
			os.Exit(1)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "AGENT\tDETECTED\tMCP CONFIGURED\tSKILL INSTALLED\tVERSION\tSTATUS")
		for _, s := range statuses {
			detStr := "no"
			if s.Detected {
				detStr = "yes"
			}
			mcpStr := "no"
			if s.MCPInstalled {
				mcpStr = "yes"
			}
			skillStr := "no"
			if s.SkillInstalled {
				skillStr = "yes"
			}
			statusStr := "UP TO DATE"
			if !s.Detected {
				statusStr = "NOT DETECTED"
			} else if s.DriftDetected {
				statusStr = "DRIFT DETECTED"
			}
			vStr := s.SkillVersion
			if vStr == "" {
				vStr = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.DisplayName, detStr, mcpStr, skillStr, vStr, statusStr)
		}
		_ = w.Flush()

		home, _ := os.UserHomeDir()
		db := openLocalDaemonDB(home)
		if db != nil {
			defer db.Close()
		}
		repoStatus := daemon.GetSkillsRepoStatus(home, db, false)
		fmt.Println("\nSKILLS REPOSITORY:")
		if !repoStatus.Configured && !repoStatus.Cloned {
			fmt.Println("  Status: Not configured")
			if repoStatus.SuggestedPath != "" {
				fmt.Printf("  Suggested Path: %s\n", repoStatus.SuggestedPath)
			}
		} else {
			if repoStatus.RepoPath != "" {
				fmt.Printf("  Path:    %s\n", repoStatus.RepoPath)
			}
			if repoStatus.RepoURL != "" {
				fmt.Printf("  URL:     %s\n", repoStatus.RepoURL)
			}
			if repoStatus.Cloned {
				fmt.Printf("  Branch:  %s\n", repoStatus.Branch)
				fmt.Printf("  Commit:  %s\n", repoStatus.LocalSHA)
				if repoStatus.RemoteSHA != "" {
					fmt.Printf("  Remote:  %s\n", repoStatus.RemoteSHA)
				}
				syncStatus := "UP TO DATE"
				if repoStatus.Dirty {
					syncStatus = "DIRTY WORKING TREE"
				} else if repoStatus.BehindCount > 0 {
					syncStatus = fmt.Sprintf("BEHIND BY %d COMMITS", repoStatus.BehindCount)
				}
				fmt.Printf("  Status:  %s\n", syncStatus)
				if repoStatus.LastSyncedAt != "" {
					fmt.Printf("  Synced:  %s\n", repoStatus.LastSyncedAt)
				}
			} else {
				fmt.Println("  Status:  CONFIGURED BUT NOT CLONED LOCALLY")
				if repoStatus.SuggestedPath != "" {
					fmt.Printf("  Suggested Path: %s\n", repoStatus.SuggestedPath)
				}
			}
		}
		return
	}

	subcmd := args[0]
	switch subcmd {
	case "setup", "configure", "install", "sync":
		fs := flag.NewFlagSet("agent "+subcmd, flag.ExitOnError)
		agentFlag := fs.String("agent", "all", "Target agent (claude-code, antigravity, codex, or all)")
		repoURLFlag := fs.String("skills-repo-url", "", "Git repository URL for shared skills")
		repoPathFlag := fs.String("skills-repo-path", "", "Local destination directory for skills repo")
		cloneFlag := fs.Bool("clone", false, "Automatically clone without confirmation prompt if missing")
		_ = fs.Parse(args[1:])
		if fs.NArg() > 0 && *agentFlag == "all" && !strings.HasPrefix(fs.Arg(0), "-") {
			*agentFlag = fs.Arg(0)
		}

		home, _ := os.UserHomeDir()
		db := openLocalDaemonDB(home)
		if db != nil {
			defer db.Close()
		}

		// 1. If setup/configure/install, configure agents
		if subcmd != "sync" {
			targets := []string{*agentFlag}
			fmt.Printf("⚡ Configuring Ackbar Tasks MCP server and Skill for: %s...\n", *agentFlag)
			updated, err := daemon.ProvisionAgents(home, targets)
			if err != nil {
				fmt.Printf("❌ Provisioning failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✅ Successfully configured agents:")
			for _, s := range updated {
				if s.Detected {
					fmt.Printf("  • %s: MCP server registered (%s), Skill installed (%s)\n", s.DisplayName, s.ConfigFile, s.SkillVersion)
				}
			}
		}

		// 2. Skills repo handling
		repoStatus := daemon.GetSkillsRepoStatus(home, db, false)
		repoURL := *repoURLFlag
		if repoURL == "" {
			repoURL = repoStatus.RepoURL
		}
		repoPath := *repoPathFlag
		if repoPath == "" {
			repoPath = repoStatus.RepoPath
		}
		suggestedPath := repoStatus.SuggestedPath
		if suggestedPath == "" && repoURL != "" {
			suggestedPath = daemon.GetSkillsRepoStatus(home, db, false).SuggestedPath
		}

		shouldClone := *cloneFlag
		declinedClone := false
		// If URL is configured/provided but not cloned locally:
		if repoURL != "" && (repoPath == "" || !repoStatus.Cloned) {
			if !shouldClone {
				targetSuggest := repoPath
				if targetSuggest == "" {
					targetSuggest = suggestedPath
				}
				fmt.Printf("\n📦 Clone skills repo %s to %s? [Y/n/custom path]: ", repoURL, targetSuggest)
				var input string
				_, _ = fmt.Scanln(&input)
				input = strings.TrimSpace(input)
				if input == "" || strings.EqualFold(input, "y") || strings.EqualFold(input, "yes") {
					shouldClone = true
					if repoPath == "" {
						repoPath = targetSuggest
					}
				} else if strings.EqualFold(input, "n") || strings.EqualFold(input, "no") {
					declinedClone = true
					fmt.Println("Skipping skills repository clone.")
				} else {
					repoPath = input
					shouldClone = true
				}
			}
		}

		if !declinedClone && (repoURL != "" || repoPath != "" || repoStatus.Cloned) {
			fmt.Println("\n🔄 Syncing skills repository...")
			opts := daemon.SyncSkillsRepoOptions{
				RepoPath:       repoPath,
				RepoURL:        repoURL,
				CloneIfMissing: shouldClone,
			}
			syncStatus, err := daemon.SyncSkillsRepo(home, db, opts)
			if err != nil {
				fmt.Printf("❌ Skills repo sync failed: %v\n", err)
			} else if syncStatus != nil && syncStatus.Cloned {
				fmt.Printf("✅ Skills repository synced successfully at %s (branch: %s, commit: %s)\n", syncStatus.RepoPath, syncStatus.Branch, syncStatus.LocalSHA)
			}
		}
	default:
		fmt.Printf("Unknown agent command: %s\n", subcmd)
		fmt.Println("Usage: ackbar agent [status|setup|sync]")
		os.Exit(1)
	}
}

func runCheckUpdateCmd(args []string) {
	fmt.Printf("⚓ Checking GitHub for latest Ackbar release...\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := version.CheckLatestRelease(ctx, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to check for updates: %v\n", err)
		os.Exit(1)
	}

	installType, exePath := version.DetectInstallType()

	fmt.Printf("\n📦 Ackbar Version Status:\n")
	fmt.Printf("  • Installed Version: v%s\n", version.Version)
	fmt.Printf("  • Latest Release:    v%s\n", info.Version)
	fmt.Printf("  • Install Method:    %s (%s)\n", strings.Title(installType), exePath)
	if !info.PublishedAt.IsZero() {
		fmt.Printf("  • Published:         %s\n", info.PublishedAt.Format("2006-01-02 15:04:05 MST"))
	}
	if info.ReleaseURL != "" {
		fmt.Printf("  • Release Notes:     %s\n", info.ReleaseURL)
	}

	fmt.Println()
	if info.IsNewer {
		fmt.Printf("⚡ A newer version of Ackbar is available (v%s -> v%s)!\n", version.Version, info.Version)
		if installType == "homebrew" {
			fmt.Printf("👉 Run 'brew upgrade ackbar' or 'ackbar update' to upgrade.\n")
		} else {
			fmt.Printf("👉 Run 'ackbar update' to download and install the prebuilt binaries.\n")
		}
		os.Exit(0)
	}

	fmt.Printf("✅ You are running the latest version of Ackbar!\n")
}

func runUpdateCmd(args []string) {
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		}
	}

	fmt.Printf("🔍 Checking for Ackbar updates...\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	info, err := version.CheckLatestRelease(ctx, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to query GitHub releases: %v\n", err)
		os.Exit(1)
	}

	installType, exePath := version.DetectInstallType()

	if !info.IsNewer && !force {
		fmt.Printf("✅ Ackbar is already up to date (v%s).\n", version.Version)
		fmt.Printf("Use 'ackbar update --force' to re-install the latest release.\n")
		return
	}

	if info.IsNewer {
		fmt.Printf("⚡ New version available: v%s (current: v%s)\n", info.Version, version.Version)
	} else {
		fmt.Printf("⚡ Re-installing latest release: v%s\n", info.Version)
	}

	var res *version.UpdateResult
	if installType == "homebrew" {
		fmt.Printf("🍺 Detected Homebrew installation (%s). Upgrading via Homebrew...\n\n", exePath)
		res, err = version.UpdateToLatest(ctx, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Homebrew upgrade failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(res.Message)
		fmt.Printf("\n✅ Homebrew upgrade complete!\n")
	} else {
		fmt.Printf("📦 Standalone install detected. Downloading prebuilt release for %s/%s...\n", runtime.GOOS, runtime.GOARCH)
		if info.AssetURL != "" {
			fmt.Printf("⬇️  Asset: %s\n", info.AssetName)
		}

		targetDir := ""
		if exePath != "" {
			targetDir = filepath.Dir(exePath)
		}
		res, err = version.UpdateToLatest(ctx, targetDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Update installation failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ %s\n", res.Message)
		if len(res.UpdatedBinaries) > 0 {
			fmt.Printf("🚀 Updated binaries: %s\n", strings.Join(res.UpdatedBinaries, ", "))
		}
	}

	// If local ackbard is currently running, restart it with the newly installed binary
	token := os.Getenv("ACKBAR_TOKEN")
	httpClient := &http.Client{Timeout: 5 * time.Second}
	vReq, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:7777/v1/version", nil)
	if token != "" {
		vReq.Header.Set("Authorization", "Bearer "+token)
	}
	if resp, err := httpClient.Do(vReq); err == nil {
		_ = resp.Body.Close()
		fmt.Printf("🔄 Restarting local ackbard daemon...\n")
		sReq, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:7777/v1/shutdown?drain=true", nil)
		sReq.Header.Set("Content-Type", "application/json")
		if token != "" {
			sReq.Header.Set("Authorization", "Bearer "+token)
		}
		shutdownResp, sErr := httpClient.Do(sReq)
		if sErr == nil && shutdownResp != nil {
			if shutdownResp.StatusCode == http.StatusUnauthorized || shutdownResp.StatusCode == http.StatusForbidden {
				_ = shutdownResp.Body.Close()
				fmt.Printf("⚠️ Local ackbard requires ACKBAR_TOKEN to restart automatically; restart ackbard manually.\n")
				return
			}
			restartDeferred := false
			if shutdownResp.StatusCode == http.StatusOK {
				var sData struct {
					RestartDeferred bool `json:"restart_deferred"`
				}
				if err := json.NewDecoder(shutdownResp.Body).Decode(&sData); err == nil {
					restartDeferred = sData.RestartDeferred
				}
			}
			_ = shutdownResp.Body.Close()

			if restartDeferred {
				fmt.Printf("⏳ Active headless session turn(s) in progress — ackbard will restart automatically once the current turn finishes.\n")
				return
			}
		}

		time.Sleep(400 * time.Millisecond)
		ackbardBin := ""
		if res != nil && res.TargetDir != "" {
			ackbardBin = filepath.Join(res.TargetDir, "ackbard")
		}
		if ackbardBin == "" {
			ackbardBin = "ackbard"
		} else if _, err := os.Stat(ackbardBin); err != nil {
			ackbardBin = "ackbard"
		}
		_ = exec.Command(ackbardBin).Start()
	}

	fmt.Printf("🎉 Successfully upgraded Ackbar to v%s!\n", info.Version)
}
