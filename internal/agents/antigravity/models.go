package antigravity

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"ackbar/internal/agent"
)

type cachedModelCatalog struct {
	bin       string
	models    []agent.ModelSuggestion
	expiresAt time.Time
}

var (
	modelIDRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

	modelCacheMu      sync.Mutex
	modelCatalogCache cachedModelCatalog
)

func (*Plugin) DefaultModels() []agent.ModelSuggestion {
	return []agent.ModelSuggestion{
		{ID: "gemini-4-carbon-high", Label: "Gemini 4 Carbon (High)"},
		{ID: "gemini-4-carbon-medium", Label: "Gemini 4 Carbon (Medium)"},
		{ID: "gemini-4-carbon-low", Label: "Gemini 4 Carbon (Low)"},
		{ID: "gemini-4-hydrogen-xhigh", Label: "Gemini 4 Hydrogen (XHigh)"},
		{ID: "gemini-4-hydrogen-high", Label: "Gemini 4 Hydrogen (High)"},
		{ID: "gemini-4-hydrogen-medium", Label: "Gemini 4 Hydrogen (Medium)"},
		{ID: "gemini-4-hydrogen-low", Label: "Gemini 4 Hydrogen (Low)"},
		{ID: "gemini-pro-orchestrator", Label: "Orchestrator (Carbon)"},
		{ID: "gemini-pro-orchestrator-argon", Label: "Orchestrator (Argon)"},
		{ID: "gemini-4-argon-high", Label: "Gemini 4 Argon (High)"},
		{ID: "gemini-4-argon-medium", Label: "Gemini 4 Argon (Medium)"},
		{ID: "gemini-4-argon-low", Label: "Gemini 4 Argon (Low)"},
		{ID: "gemini-3.8-flash-high", Label: "Gemini 3.8 Flash (High)"},
		{ID: "gemini-3.8-flash-medium", Label: "Gemini 3.8 Flash (Medium)"},
		{ID: "gemini-3.8-flash-low", Label: "Gemini 3.8 Flash (Low)"},
		{ID: "claude-opus-5-5-high", Label: "Claude Opus 5.5 (High)"},
		{ID: "claude-opus-5-5-medium", Label: "Claude Opus 5.5 (Medium)"},
		{ID: "claude-opus-5-5-low", Label: "Claude Opus 5.5 (Low)"},
		{ID: "claude-sonnet-5-5-high", Label: "Claude Sonnet 5.5 (High)"},
		{ID: "claude-sonnet-5-5-medium", Label: "Claude Sonnet 5.5 (Medium)"},
		{ID: "claude-sonnet-5-5-low", Label: "Claude Sonnet 5.5 (Low)"},
		{ID: "gpt-oss-120b-medium", Label: "GPT-OSS 120B (Medium)"},
	}
}

func ParseModelsOutput(out string) []agent.ModelSuggestion {
	var models []agent.ModelSuggestion
	seen := make(map[string]bool)
	for _, rawLine := range strings.Split(out, "\n") {
		line := rawLine
		if idx := strings.LastIndex(line, "\r"); idx != -1 {
			line = line[idx+1:]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		id := fields[0]
		if !modelIDRegex.MatchString(id) {
			continue
		}
		lowerID := strings.ToLower(id)
		if lowerID == "usage" || lowerID == "available" || lowerID == "flags" {
			continue
		}
		label := strings.TrimSpace(line[len(id):])
		if label == "" {
			label = id
		}
		if !seen[id] {
			seen[id] = true
			models = append(models, agent.ModelSuggestion{ID: id, Label: label})
		}
	}
	return models
}

func (p *Plugin) CollectModels(_ string) []agent.ModelSuggestion {
	bin, err := p.ResolveBinary()
	if err == nil && bin != "" {
		modelCacheMu.Lock()
		if modelCatalogCache.bin == bin && len(modelCatalogCache.models) > 0 && time.Now().Before(modelCatalogCache.expiresAt) {
			cached := modelCatalogCache.models
			modelCacheMu.Unlock()
			return cached
		}
		modelCacheMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "models")
		var out bytes.Buffer
		cmd.Stdout = &out
		if cmdErr := cmd.Run(); cmdErr == nil && out.Len() > 0 {
			if parsed := ParseModelsOutput(out.String()); len(parsed) > 0 {
				modelCacheMu.Lock()
				modelCatalogCache = cachedModelCatalog{bin: bin, models: parsed, expiresAt: time.Now().Add(5 * time.Minute)}
				modelCacheMu.Unlock()
				return parsed
			}
		}
	}
	return p.DefaultModels()
}

func (*Plugin) ValidateModelID(model string) bool {
	return model != "" && len(model) <= 128 && !strings.HasPrefix(model, "-") && !strings.ContainsAny(model, "\x00\r\n \t")
}

func (p *Plugin) NormalizeModelID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "default") || strings.EqualFold(s, "synthetic") || strings.HasPrefix(s, "<") {
		return ""
	}
	for _, m := range p.DefaultModels() {
		if strings.EqualFold(s, m.ID) || strings.EqualFold(s, m.Label) {
			return m.ID
		}
	}
	return s
}
