package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ackbar/internal/client"
	"ackbar/internal/version"

	"github.com/charmbracelet/bubbletea"
)

type Config struct {
	Hosts       []client.HostConfig `json:"hosts"`
	ProjectsDir string              `json:"projects_dir,omitempty"`
	Groups      map[string][]string `json:"groups"`
}

func main() {
	var argsForFlag []string
	shouldSetup := false
	isVersionCmd := false

	noTUI := false
	isGUICmd := false
	for _, arg := range os.Args[1:] {
		if arg == "setup-hooks" || arg == "--setup-hooks" {
			shouldSetup = true
		} else if arg == "--no-tui" || arg == "-no-tui" {
			noTUI = true
		} else if arg == "version" || arg == "--version" || arg == "-v" {
			isVersionCmd = true
		} else if arg == "gui" || arg == "--gui" || arg == "web" || arg == "--web" {
			isGUICmd = true
		} else {
			argsForFlag = append(argsForFlag, arg)
		}
	}

	configFlag := flag.String("config", "", "Path to client config file (default ~/.config/ackbar/client.json)")
	setupFlag := flag.Bool("setup-hooks", false, "Auto-configure agent hooks, install ackbar-hook binary, and start TUI")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	setProjectsDirFlag := flag.String("set-projects-dir", "", "Permanently set default projects directory in config")
	saveFlag := flag.Bool("save", false, "Save --projects-dir permanently to config file")
	var projectsDirOverride string
	flag.StringVar(&projectsDirOverride, "projects-dir", "", "Override default projects directory for new projects")
	flag.StringVar(&projectsDirOverride, "p", "", "Override default projects directory for new projects (shorthand)")
	_ = flag.CommandLine.Parse(argsForFlag)

	if isVersionCmd || *versionFlag {
		fmt.Printf("ackbar version %s\n", version.Version)
		os.Exit(0)
	}

	if isGUICmd {
		ensureDaemonRunning()
		fmt.Printf("⚓ Ackbar GUI running at http://127.0.0.1:7777\n")
		fmt.Println("Opening browser...")
		if err := exec.Command("open", "http://127.0.0.1:7777").Start(); err != nil {
			_ = exec.Command("xdg-open", "http://127.0.0.1:7777").Start()
		}
		os.Exit(0)
	}

	if shouldSetup || *setupFlag {
		if err := runSetupHooks(); err != nil {
			fmt.Printf("Error setting up hooks: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🚀 Hook setup complete (v%s)!\n", version.Version)
		if noTUI {
			os.Exit(0)
		}
		fmt.Println("Launching Ackbar TUI...")
		time.Sleep(1 * time.Second)
	}

	configFile := *configFlag
	if configFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Failed to retrieve user home directory: %v", err)
		}
		configFile = filepath.Join(home, ".config", "ackbar", "client.json")
	}

	// Ensure local daemon is running before loading config
	ensureDaemonRunning()

	// Read or create default configuration
	hosts, projectsDir, groups, firstRun, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	if *setProjectsDirFlag != "" {
		if err := client.SaveProjectsDirConfig(configFile, *setProjectsDirFlag); err != nil {
			log.Fatalf("Failed to save projects directory: %v", err)
		}
		fmt.Printf("✅ Permanently set default projects directory to: %s\n", *setProjectsDirFlag)
		if projectsDirOverride == "" {
			os.Exit(0)
		}
		projectsDir = *setProjectsDirFlag
		firstRun = false
	}

	if projectsDirOverride != "" {
		projectsDir = projectsDirOverride
		firstRun = false
		if *saveFlag {
			_ = client.SaveProjectsDirConfig(configFile, projectsDir)
			fmt.Printf("✅ Permanently saved default projects directory to: %s\n", projectsDir)
		}
	}



	// Create Bubble Tea Model and Program
	model := client.NewModel(hosts, projectsDir, groups, firstRun, configFile)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("TUI program exited with error: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig(path string) ([]client.HostConfig, string, map[string][]string, bool, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", nil, false, fmt.Errorf("failed to create config directory: %w", err)
	}

	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		// Create default client configuration pointing to local daemon
		defaultHosts := []client.HostConfig{
			{Name: "local", URL: "http://127.0.0.1:7777"},
		}
		defaultGroups := make(map[string][]string)
		cfg := Config{Hosts: defaultHosts, ProjectsDir: "~/Projects", Groups: defaultGroups}
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, "", nil, false, fmt.Errorf("failed to marshal default config: %w", err)
		}

		if err := os.WriteFile(path, data, 0644); err != nil {
			return nil, "", nil, false, fmt.Errorf("failed to write default config file: %w", err)
		}
		return defaultHosts, "~/Projects", defaultGroups, true, nil
	} else if err != nil {
		return nil, "", nil, false, err
	}

	// Read existing config
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil, false, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, "", nil, false, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	if len(cfg.Hosts) == 0 {
		cfg.Hosts = []client.HostConfig{{Name: "local", URL: "http://127.0.0.1:7777"}}
	}
	if cfg.ProjectsDir == "" {
		cfg.ProjectsDir = "~/Projects"
	}
	if cfg.Groups == nil {
		cfg.Groups = make(map[string][]string)
	} else {
		for g := range cfg.Groups {
			if strings.HasPrefix(g, "Project Y") || strings.HasPrefix(g, "ProjectY") {
				delete(cfg.Groups, g)
			}
		}
	}

	// Query local daemon for persisted hosts
	httpClient := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := httpClient.Get("http://127.0.0.1:7777/v1/hosts")
	if err == nil && resp.StatusCode == http.StatusOK {
		var hostRecs []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&hostRecs); err == nil {
			existing := make(map[string]bool)
			for _, h := range cfg.Hosts {
				existing[h.Name] = true
			}
			for _, rec := range hostRecs {
				if !existing[rec.Name] && rec.Name != "" && rec.URL != "" {
					cfg.Hosts = append(cfg.Hosts, client.HostConfig{Name: rec.Name, URL: rec.URL})
				}
			}
		}
		resp.Body.Close()
	}

	return cfg.Hosts, cfg.ProjectsDir, cfg.Groups, false, nil
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

func runSetupHooks() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home: %w", err)
	}

	// 1. Build & install ackbar-hook and ackbard binaries to ~/.local/bin
	localBin := filepath.Join(home, ".local", "bin")
	_ = os.MkdirAll(localBin, 0755)

	hookBinTarget := filepath.Join(localBin, "ackbar-hook")
	cmd := exec.Command("go", "build", "-o", hookBinTarget, "./cmd/ackbar-hook")
	if err := cmd.Run(); err != nil {
		cmd2 := exec.Command("go", "install", "ackbar/cmd/ackbar-hook@latest")
		_ = cmd2.Run()
	}
	fmt.Printf("✅ Installed ackbar-hook (v%s) to %s\n", version.Version, hookBinTarget)

	ackbardBinTarget := filepath.Join(localBin, "ackbard")
	cmdBard := exec.Command("go", "build", "-o", ackbardBinTarget, "./cmd/ackbard")
	if err := cmdBard.Run(); err != nil {
		cmdBard2 := exec.Command("go", "install", "ackbar/cmd/ackbard@latest")
		_ = cmdBard2.Run()
	}
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
							for _, h := range innerHooks {
								if hm, ok := h.(map[string]interface{}); ok {
									if hm["url"] == "http://127.0.0.1:7777/v1/hooks/claude-code" {
										hasHook = true
									}
								}
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

	// 3. Configure Antigravity hooks (~/.gemini/config/hooks.json)
	geminiHooksDir := filepath.Join(home, ".gemini", "config")
	_ = os.MkdirAll(geminiHooksDir, 0755)
	geminiHooksFile := filepath.Join(geminiHooksDir, "hooks.json")
	geminiHookData := fmt.Sprintf(`{
  "hooks": [
    {
      "command": "%s --agent=antigravity"
    }
  ]
}`, hookBinTarget)
	_ = os.WriteFile(geminiHooksFile, []byte(geminiHookData), 0644)
	fmt.Printf("✅ Configured Antigravity hook in %s\n", geminiHooksFile)

	return nil
}
