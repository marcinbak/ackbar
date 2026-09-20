package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CandidateSession represents an active session that could potentially match a prompt.
type CandidateSession struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Agent     string `json:"agent"`
	Host      string `json:"host"`
	NodePath  string `json:"node_path"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"git_branch"`
	Activity  string `json:"activity"`
}

// CandidateNode represents a tree group node in Ackbar.
type CandidateNode struct {
	Path           string `json:"path"`
	ProjectDir     string `json:"project_dir"`
	PreferredAgent string `json:"preferred_agent,omitempty"`
}

// CandidateHost represents a compute host in Ackbar.
type CandidateHost struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ResolveRequest contains user input and fleet context for resolution.
type ResolveRequest struct {
	Prompt   string             `json:"prompt"`
	Hosts    []CandidateHost    `json:"hosts"`
	Nodes    []CandidateNode    `json:"nodes"`
	Sessions []CandidateSession `json:"sessions"`
	APIKey   string             `json:"api_key,omitempty"`
}

// ResolveResult represents the derived session launch configuration.
type ResolveResult struct {
	Host                string  `json:"host"`
	Agent               string  `json:"agent"`
	NodePath            string  `json:"node_path"`
	Cwd                 string  `json:"cwd"`
	Name                string  `json:"name"`
	Prompt              string  `json:"prompt"`
	MatchedSessionID    string  `json:"matched_session_id,omitempty"`
	MatchedSessionTitle string  `json:"matched_session_title,omitempty"`
	Confidence          float64 `json:"confidence"`
	Source              string  `json:"source"` // "jev" or "heuristic"
}

// JevClient handles communication with the TypeSafe AI System One API.
type JevClient struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// NewJevClient returns a new Jev client with default endpoint.
func NewJevClient(apiKey string) *JevClient {
	if apiKey == "" {
		apiKey = os.Getenv("TYPESAFE_API_KEY")
	}
	return &JevClient{
		APIKey:  apiKey,
		BaseURL: "https://api.typesafe.ai/v1/systemone",
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type jevChoiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevRequest struct {
	State     string                       `json:"state"`
	Model     string                       `json:"model"`
	Questions map[string]jevChoiceQuestion `json:"questions"`
}

type jevChoiceAnswer struct {
	Choice     string             `json:"choice"`
	Confidence float64            `json:"confidence"`
	Score      float64            `json:"score,omitempty"`
	Options    map[string]float64 `json:"options,omitempty"`
}

type jevResponse struct {
	Answers map[string]jevChoiceAnswer `json:"answers"`
	Error   string                     `json:"error,omitempty"`
}

var ticketRegex = regexp.MustCompile(`(?i)\b([A-Z]{2,10}-\d+)\b`)

// Resolve derives the launch configuration using Jev AI when an API key is available,
// or falls back to smart local heuristics.
func Resolve(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt cannot be empty")
	}

	apiKey := req.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("TYPESAFE_API_KEY")
	}

	// If API key is available, attempt Jev AI classification first
	if apiKey != "" {
		client := NewJevClient(apiKey)
		result, err := client.classify(ctx, req)
		if err == nil && result != nil {
			result.Source = "jev"
			return result, nil
		}
		// On error or timeout, log/fall back seamlessly
	}

	// Fallback heuristic classification
	result := FallbackResolve(req)
	result.Source = "heuristic"
	return result, nil
}

func (c *JevClient) classify(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	// Build host criteria
	hostCriteria := make(map[string]string)
	for _, h := range req.Hosts {
		name := h.Name
		if name == "" {
			continue
		}
		if name == "local" {
			hostCriteria["local"] = "Local development machine (default for most tasks, macOS builds, frontend, UI)."
		} else {
			hostCriteria[name] = fmt.Sprintf("Compute host %s (remote builds, GPU training, Linux compute).", name)
		}
	}
	if len(hostCriteria) == 0 {
		hostCriteria["local"] = "Local development machine."
	}

	// Build group criteria
	groupCriteria := make(map[string]string)
	for _, n := range req.Nodes {
		if n.Path != "" {
			groupCriteria[n.Path] = fmt.Sprintf("Project group path %s (directory %s).", n.Path, n.ProjectDir)
		}
	}
	if len(groupCriteria) == 0 {
		groupCriteria["Default"] = "Default root workspace."
	}

	// Build agent criteria emphasizing project-to-agent affinity
	agentCriteria := make(map[string]string)
	for _, n := range req.Nodes {
		if n.PreferredAgent != "" {
			if existing, ok := agentCriteria[n.PreferredAgent]; ok {
				agentCriteria[n.PreferredAgent] = existing + fmt.Sprintf(" Also default for %s.", n.Path)
			} else {
				agentCriteria[n.PreferredAgent] = fmt.Sprintf("Default agent for project %s.", n.Path)
			}
		}
	}
	if _, ok := agentCriteria["claude-code"]; !ok {
		agentCriteria["claude-code"] = "Anthropic Claude Code. General-purpose agent."
	}
	if _, ok := agentCriteria["antigravity"]; !ok {
		agentCriteria["antigravity"] = "Google Antigravity (agy). Multi-modal & workspace agent."
	}
	if _, ok := agentCriteria["codex"]; !ok {
		agentCriteria["codex"] = "OpenAI Codex. Scripting and automation agent."
	}

	// Build existing session criteria
	sessCriteria := map[string]string{
		"none": "No existing session matches this task. Start a fresh session.",
	}
	for _, s := range req.Sessions {
		label := s.Name
		if label == "" {
			label = s.Agent
		}
		if s.GitBranch != "" {
			label += " [branch: " + s.GitBranch + "]"
		}
		if s.NodePath != "" {
			label += " (" + s.NodePath + ")"
		}
		sessCriteria[s.ID] = label
	}

	jevPayload := jevRequest{
		State: req.Prompt,
		Model: "jev-latest",
		Questions: map[string]jevChoiceQuestion{
			"host": {
				Type:         "choice",
				Instructions: "Which compute host should run this task?",
				Criteria:     hostCriteria,
			},
			"group": {
				Type:         "choice",
				Instructions: "Which project tree group does this task belong to?",
				Criteria:     groupCriteria,
			},
			"agent": {
				Type:         "choice",
				Instructions: "Which AI coding agent should run this task? Select primarily based on the project referenced and its associated agent conventions, or explicit agent mentions in the prompt.",
				Criteria:     agentCriteria,
			},
			"existing_session": {
				Type:         "choice",
				Instructions: "Does this prompt refer to an existing active session, or is it a new task?",
				Criteria:     sessCriteria,
			},
		},
	}

	payloadBytes, err := json.Marshal(jevPayload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jev API returned status %d", resp.StatusCode)
	}

	var jevResp jevResponse
	if err := json.NewDecoder(resp.Body).Decode(&jevResp); err != nil {
		return nil, err
	}
	if jevResp.Error != "" {
		return nil, fmt.Errorf("jev API error: %s", jevResp.Error)
	}

	chosenHost := jevResp.Answers["host"].Choice
	if chosenHost == "" {
		chosenHost = "local"
	}

	chosenGroup := jevResp.Answers["group"].Choice
	if chosenGroup == "" {
		chosenGroup = "Default"
	}

	chosenAgent := jevResp.Answers["agent"].Choice
	if chosenAgent == "" {
		chosenAgent = "claude-code"
	}

	matchedSess := jevResp.Answers["existing_session"].Choice
	matchedID := ""
	matchedTitle := ""
	if matchedSess != "" && matchedSess != "none" {
		matchedID = matchedSess
		for _, s := range req.Sessions {
			if s.ID == matchedID {
				matchedTitle = s.Name
				if matchedTitle == "" {
					matchedTitle = s.Agent
				}
				break
			}
		}
	}

	// Resolve cwd from chosen group
	cwd := ""
	for _, n := range req.Nodes {
		if n.Path == chosenGroup && n.ProjectDir != "" {
			cwd = n.ProjectDir
			break
		}
	}
	if cwd == "" {
		home, _ := os.UserHomeDir()
		cwd = home
	}

	avgConfidence := (jevResp.Answers["host"].Confidence +
		jevResp.Answers["group"].Confidence +
		jevResp.Answers["agent"].Confidence) / 3.0
	if avgConfidence <= 0 {
		avgConfidence = 0.95
	}

	taskName := deriveTaskName(req.Prompt)

	return &ResolveResult{
		Host:                chosenHost,
		Agent:               chosenAgent,
		NodePath:            chosenGroup,
		Cwd:                 cwd,
		Name:                taskName,
		Prompt:              req.Prompt,
		MatchedSessionID:    matchedID,
		MatchedSessionTitle: matchedTitle,
		Confidence:          avgConfidence,
	}, nil
}

// FallbackResolve applies deterministic project-driven heuristics.
func FallbackResolve(req ResolveRequest) *ResolveResult {
	promptLower := strings.ToLower(req.Prompt)

	// 1. Check for Existing Session Match
	var matchedID, matchedTitle string
	ticketMatch := ticketRegex.FindString(req.Prompt)

	for _, s := range req.Sessions {
		nameLower := strings.ToLower(s.Name)
		branchLower := strings.ToLower(s.GitBranch)

		// Exact ticket match in session name or branch
		if ticketMatch != "" {
			if strings.Contains(nameLower, strings.ToLower(ticketMatch)) ||
				strings.Contains(branchLower, strings.ToLower(ticketMatch)) {
				matchedID = s.ID
				matchedTitle = s.Name
				break
			}
		}

		// High keyword overlap
		if len(promptLower) > 8 && strings.Contains(nameLower, promptLower) {
			matchedID = s.ID
			matchedTitle = s.Name
			break
		}
	}

	// 2. Derive Host
	chosenHost := "local"
	for _, h := range req.Hosts {
		hLower := strings.ToLower(h.Name)
		if hLower != "local" && strings.Contains(promptLower, hLower) {
			chosenHost = h.Name
			break
		}
	}
	if strings.Contains(promptLower, "gpu") || strings.Contains(promptLower, "cuda") || strings.Contains(promptLower, "linux") {
		for _, h := range req.Hosts {
			if strings.Contains(strings.ToLower(h.Name), "legion") || strings.Contains(strings.ToLower(h.Name), "gpu") {
				chosenHost = h.Name
				break
			}
		}
	}

	// 3. Derive Group & Cwd (Project Resolution)
	chosenGroup := ""
	chosenCwd := ""
	var matchedNode *CandidateNode

	// Check if prompt matches existing node path names
	for i := range req.Nodes {
		n := &req.Nodes[i]
		pLower := strings.ToLower(n.Path)
		parts := strings.Split(pLower, "/")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if len(part) > 2 && strings.Contains(promptLower, part) {
				chosenGroup = n.Path
				chosenCwd = n.ProjectDir
				matchedNode = n
				break
			}
		}
		if chosenGroup != "" {
			break
		}
	}

	// If no node matched, default to first non-empty node or home directory
	if chosenGroup == "" && len(req.Nodes) > 0 {
		chosenGroup = req.Nodes[0].Path
		chosenCwd = req.Nodes[0].ProjectDir
		matchedNode = &req.Nodes[0]
	}
	if chosenCwd == "" {
		home, _ := os.UserHomeDir()
		chosenCwd = home
	}

	// 4. Derive Agent based primarily on the Project Referenced
	chosenAgent := ""

	// Check if prompt explicitly requested an agent by name
	if strings.Contains(promptLower, "antigravity") || strings.Contains(promptLower, "agy") {
		chosenAgent = "antigravity"
	} else if strings.Contains(promptLower, "codex") {
		chosenAgent = "codex"
	} else if strings.Contains(promptLower, "claude") {
		chosenAgent = "claude-code"
	}

	// If no explicit agent in prompt, use project's preferred agent
	if chosenAgent == "" && matchedNode != nil && matchedNode.PreferredAgent != "" {
		chosenAgent = matchedNode.PreferredAgent
	}

	// If still undetermined, inspect session history for this project
	if chosenAgent == "" && chosenGroup != "" {
		agentCounts := make(map[string]int)
		for _, s := range req.Sessions {
			if s.NodePath == chosenGroup && s.Agent != "" {
				agentCounts[s.Agent]++
			}
		}
		maxCount := 0
		for ag, count := range agentCounts {
			if count > maxCount {
				maxCount = count
				chosenAgent = ag
			}
		}
	}

	// Fallback general default
	if chosenAgent == "" {
		chosenAgent = "claude-code"
	}

	taskName := deriveTaskName(req.Prompt)

	return &ResolveResult{
		Host:                chosenHost,
		Agent:               chosenAgent,
		NodePath:            chosenGroup,
		Cwd:                 chosenCwd,
		Name:                taskName,
		Prompt:              req.Prompt,
		MatchedSessionID:    matchedID,
		MatchedSessionTitle: matchedTitle,
		Confidence:          0.85,
	}
}

func deriveTaskName(prompt string) string {
	clean := strings.TrimSpace(prompt)
	ticket := ticketRegex.FindString(clean)

	// Clean up initial directive words
	clean = regexp.MustCompile(`(?i)^(start|work on|fix|implement|create|investigate|debug)\s+`).ReplaceAllString(clean, "")

	// Truncate to reasonable title length
	if len(clean) > 50 {
		clean = clean[:47] + "..."
	}

	if ticket != "" && !strings.Contains(clean, ticket) {
		return fmt.Sprintf("%s: %s", ticket, clean)
	}
	if clean == "" {
		return "New Task"
	}
	return clean
}

// Helper to expand ~ in paths
func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
