package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type SummarizeSpeechRequest struct {
	SessionID    string `json:"session_id"`
	MessageIndex *int   `json:"message_index,omitempty"`
	Content      string `json:"content"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
}

var (
	fencedCodeRegex = regexp.MustCompile("(?s)```.*?```")
	htmlTagRegex    = regexp.MustCompile("<[^>]+>")
	boxDrawingRegex = regexp.MustCompile(`[┌┐└┘─│├┤┬┴┼═║╔╗╚╝╠╣╦╩╬]`)
	headingRegex    = regexp.MustCompile(`(?m)^#{2,3}\s+(.+)$`)
	mdLinkRegex     = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdFormatting    = regexp.MustCompile(`[*_~` + "`" + `]`)
	alertTagRegex   = regexp.MustCompile(`>\s*\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]`)
)

// BuildStructuralSpeechSummary produces a deterministic, natural spoken briefing in pure Go.
func BuildStructuralSpeechSummary(content string) (string, []string) {
	if strings.TrimSpace(content) == "" {
		return "The response is ready. Let me know if you would like me to read it.", []string{"Read Full"}
	}

	// 1. Strip code blocks first so comments like "# heading" in code aren't parsed as headings
	codeFreeContent := fencedCodeRegex.ReplaceAllString(content, " ")

	// 2. Extract markdown ## / ### section headings from code-free content
	var headings []string
	matches := headingRegex.FindAllStringSubmatch(codeFreeContent, -1)
	for _, m := range matches {
		if len(m) > 1 {
			cleanH := mdLinkRegex.ReplaceAllString(m[1], "$1")
			cleanH = mdFormatting.ReplaceAllString(cleanH, "")
			cleanH = strings.TrimSpace(cleanH)
			// Remove numbered prefix like "1. " or "1) "
			cleanH = regexp.MustCompile(`^\d+[\.\)]\s*`).ReplaceAllString(cleanH, "")
			if cleanH != "" {
				headings = append(headings, cleanH)
			}
		}
	}

	// 3. Strip box drawing diagrams, HTML tags, and alerts
	cleanText := htmlTagRegex.ReplaceAllString(codeFreeContent, " ")
	cleanText = alertTagRegex.ReplaceAllString(cleanText, " ")

	lines := strings.Split(cleanText, "\n")
	var proseLines []string
	var detectedQuestion string

	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if boxDrawingRegex.MatchString(trimmed) {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Skip table rows
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			continue
		}
		// Check for question
		if strings.Contains(trimmed, "?") && detectedQuestion == "" {
			parts := strings.Split(trimmed, "?")
			if len(parts) > 0 {
				qText := parts[0] + "?"
				qText = mdFormatting.ReplaceAllString(qText, "")
				qText = mdLinkRegex.ReplaceAllString(qText, "$1")
				qText = strings.TrimSpace(qText)
				// Limit length
				if len(qText) > 120 {
					qText = qText[:117] + "..."
				}
				detectedQuestion = qText
			}
		}

		cleanL := mdLinkRegex.ReplaceAllString(trimmed, "$1")
		cleanL = mdFormatting.ReplaceAllString(cleanL, "")
		cleanL = strings.TrimPrefix(cleanL, ">")
		cleanL = strings.TrimPrefix(cleanL, "- ")
		cleanL = strings.TrimPrefix(cleanL, "* ")
		cleanL = strings.TrimSpace(cleanL)
		if cleanL != "" {
			proseLines = append(proseLines, cleanL)
		}
	}

	// 3. Extract opening prose summary (first 1-2 sentences)
	openingSummary := ""
	if len(proseLines) > 0 {
		combined := strings.Join(proseLines[:min(len(proseLines), 3)], " ")
		// Extract first sentence or two
		sentenceParts := strings.Split(combined, ". ")
		if len(sentenceParts) > 1 {
			openingSummary = strings.TrimSpace(sentenceParts[0]) + ". " + strings.TrimSpace(sentenceParts[1])
			if !strings.HasSuffix(openingSummary, ".") && !strings.HasSuffix(openingSummary, "!") {
				openingSummary += "."
			}
		} else if len(sentenceParts) == 1 {
			openingSummary = strings.TrimSpace(sentenceParts[0])
			if !strings.HasSuffix(openingSummary, ".") && !strings.HasSuffix(openingSummary, "!") && !strings.HasSuffix(openingSummary, "?") {
				openingSummary += "."
			}
		}
	}
	if openingSummary == "" {
		openingSummary = "The response is ready."
	}

	// 4. Synthesize spoken briefing
	var spokenBuilder strings.Builder
	spokenBuilder.WriteString(openingSummary)

	var chips []string

	hasPlanKeyword := strings.Contains(strings.ToLower(content), "plan") ||
		strings.Contains(strings.ToLower(content), "proposal") ||
		strings.Contains(strings.ToLower(content), "implementation")

	if len(headings) > 0 {
		count := len(headings)
		spokenBuilder.WriteString(fmt.Sprintf(" It covers %d section", count))
		if count > 1 {
			spokenBuilder.WriteString("s: ")
		} else {
			spokenBuilder.WriteString(": ")
		}

		displayHeadings := headings
		if len(displayHeadings) > 4 {
			displayHeadings = displayHeadings[:4]
		}
		spokenBuilder.WriteString(strings.Join(displayHeadings, ", "))
		if len(headings) > len(displayHeadings) {
			spokenBuilder.WriteString(fmt.Sprintf(", and %d more", len(headings)-len(displayHeadings)))
		}
		spokenBuilder.WriteString(".")

		// Generate drill-down chips (up to 3)
		for i, h := range displayHeadings {
			if i >= 3 {
				break
			}
			chipText := "Hear " + h
			if len(chipText) > 28 {
				chipText = chipText[:25] + "..."
			}
			chips = append(chips, chipText)
		}
	}

	if detectedQuestion != "" {
		spokenBuilder.WriteString(fmt.Sprintf(" There is also an open question: %s", detectedQuestion))
		chips = append(chips, "Hear Open Question")
	}

	if len(headings) > 0 || detectedQuestion != "" {
		spokenBuilder.WriteString(" Which part would you like to hear about first?")
	}

	if hasPlanKeyword {
		chips = append(chips, "Approve Plan")
	}

	if len(chips) == 0 {
		chips = append(chips, "Hear Details", "Approve")
	}

	return strings.TrimSpace(spokenBuilder.String()), chips
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Server) handleSummarizeSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var req SummarizeSpeechRequest
	bodyBytes, err := io.ReadAll(r.Body)
	if err == nil && len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &req)
	}

	if req.SessionID == "" {
		req.SessionID = r.URL.Query().Get("session_id")
		if req.SessionID == "" {
			req.SessionID = r.URL.Query().Get("id")
		}
	}
	if req.Content == "" {
		req.Content = r.URL.Query().Get("content")
	}
	if req.MessageIndex == nil {
		if qIdx := r.URL.Query().Get("message_index"); qIdx != "" {
			if idxVal, err := strconv.Atoi(qIdx); err == nil {
				req.MessageIndex = &idxVal
			}
		}
	}

	// If content is empty but session ID is provided, look up from transcript
	if req.Content == "" && req.SessionID != "" {
		if transcript, err := s.ExtractTranscript("", req.SessionID, ""); err == nil && transcript != nil {
			if req.MessageIndex != nil && *req.MessageIndex >= 0 && *req.MessageIndex < len(transcript.Messages) {
				req.Content = transcript.Messages[*req.MessageIndex].Content
			} else if len(transcript.Messages) > 0 {
				lastIdx := len(transcript.Messages) - 1
				req.Content = transcript.Messages[lastIdx].Content
				if req.MessageIndex == nil {
					req.MessageIndex = &lastIdx
				}
			}
		}
	}

	if req.Content == "" && req.SessionID == "" {
		http.Error(w, "Missing content or session_id", http.StatusBadRequest)
		return
	}

	msgIdx := -1
	if req.MessageIndex != nil {
		msgIdx = *req.MessageIndex
	}
	cacheKey := fmt.Sprintf("%s:%d", req.SessionID, msgIdx)

	// Step 1: Check In-Memory & SQLite Cache
	if !req.ForceRefresh && req.SessionID != "" && msgIdx >= 0 {
		s.speechCacheMu.RLock()
		cached, ok := s.speechCache[cacheKey]
		s.speechCacheMu.RUnlock()

		if ok && cached != nil {
			resp := *cached
			resp.Cached = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if s.db != nil {
			if dbCached, err := s.db.GetSpeechSummary(req.SessionID, msgIdx); err == nil && dbCached != nil {
				s.speechCacheMu.Lock()
				s.speechCache[cacheKey] = dbCached
				s.speechCacheMu.Unlock()

				resp := *dbCached
				resp.Cached = true
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		}
	}

	// Step 2: Synthesis Fallback Chain
	spokenText := ""
	var suggestedChips []string
	tier := "structural"

	// 2a. Injectable Runner Override (for deterministic testing & plugins)
	if s.speechLLMRunner != nil {
		if resText, resChips, err := s.speechLLMRunner(r.Context(), req.Content); err == nil && resText != "" {
			spokenText = resText
			suggestedChips = resChips
			tier = "llm"
		}
	}

	// 2b. Primary: Flat-Rate Headless Claude CLI Haiku ($0 flat OAuth)
	if spokenText == "" && s.speechLLMRunner == nil && os.Getenv("DISABLE_EXTERNAL_CLI_TESTS") == "" {
		if claudeBin, err := resolveClaudeBinary(); err == nil && claudeBin != "" {
			if _, statErr := exec.LookPath(claudeBin); statErr == nil {
				haikuCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
				defer cancel()

				promptInput := fmt.Sprintf(
					"You are the Ackbar Voice Companion summarizing an agent turn for audio playback.\n"+
						"Follow the Outline-First Protocol:\n"+
						"1. Provide a concise 1-2 sentence spoken summary of what was completed or proposed.\n"+
						"2. Outline the major sections if present.\n"+
						"3. Highlight any open questions or required user decisions.\n"+
						"4. Output 2-4 interactive suggested reply chips (e.g. [\"Hear Section 1\", \"Hear Open Question\", \"Approve Plan\"]).\n\n"+
						"Respond strictly in JSON format with no markdown wrappers:\n"+
						"{\n  \"spoken_text\": \"Spoken briefing here...\",\n  \"suggested_chips\": [\"Chip 1\", \"Chip 2\"]\n}\n\n"+
						"Content to summarize:\n%s", req.Content)

				cmd := exec.CommandContext(haikuCtx, claudeBin, "-p", "--model", "haiku", "--no-session-persistence")
				cmd.Env = StripBilledCredentials(os.Environ())
				cmd.Stdin = bytes.NewReader([]byte(promptInput))

				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr

				if runErr := cmd.Run(); runErr == nil {
					outBytes := stdout.Bytes()
					var haikuRes struct {
						SpokenText     string   `json:"spoken_text"`
						SuggestedChips []string `json:"suggested_chips"`
					}
					// Trim potential markdown fences from output
					trimmedOut := strings.TrimSpace(string(outBytes))
					trimmedOut = strings.TrimPrefix(trimmedOut, "```json")
					trimmedOut = strings.TrimPrefix(trimmedOut, "```")
					trimmedOut = strings.TrimSuffix(trimmedOut, "```")
					trimmedOut = strings.TrimSpace(trimmedOut)

					if jsonErr := json.Unmarshal([]byte(trimmedOut), &haikuRes); jsonErr == nil && haikuRes.SpokenText != "" {
						spokenText = haikuRes.SpokenText
						suggestedChips = haikuRes.SuggestedChips
						tier = "haiku"
					}
				}
			}
		}
	}

	// 2b-API. Secondary: Optional Direct Cloud API (Anthropic Messages API)
	if spokenText == "" && s.speechLLMRunner == nil {
		apiKey := os.Getenv("ACKBAR_SPEECH_API_KEY")
		if apiKey == "" && s.db != nil {
			if k, err := s.db.GetSetting("speech_api_key"); err == nil && k != "" {
				apiKey = k
			} else if k, err := s.db.GetSetting("anthropic_api_key"); err == nil && k != "" {
				apiKey = k
			}
		}
		if apiKey != "" {
			if resText, resChips, err := tryCloudAPISpeechSummary(r.Context(), apiKey, req.Content); err == nil && resText != "" {
				spokenText = resText
				suggestedChips = resChips
				tier = "api"
			}
		}
	}

	// 2c. Pure-Go Deterministic Structural Fallback
	if spokenText == "" {
		spokenText, suggestedChips = BuildStructuralSpeechSummary(req.Content)
		tier = "structural"
	}

	summary := &SpeechSummary{
		SessionID:      req.SessionID,
		MessageIndex:   msgIdx,
		SpokenText:     spokenText,
		SuggestedChips: suggestedChips,
		Tier:           tier,
		Cached:         false,
		CreatedAt:      time.Now().UTC(),
	}

	// Step 3: Cache Store & Response
	if req.SessionID != "" && msgIdx >= 0 {
		s.speechCacheMu.Lock()
		s.speechCache[cacheKey] = summary
		s.speechCacheMu.Unlock()

		if s.db != nil {
			_ = s.db.SaveSpeechSummary(summary)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}

func tryCloudAPISpeechSummary(ctx context.Context, apiKey, content string) (string, []string, error) {
	if apiKey == "" || strings.TrimSpace(content) == "" {
		return "", nil, fmt.Errorf("empty apiKey or content")
	}

	apiCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	systemPrompt := "You are the Ackbar Voice Companion summarizing an agent turn for audio playback. Follow the Outline-First Protocol: provide a concise 1-2 sentence spoken summary, outline major sections if present, highlight any open questions, and output 2-4 interactive suggested reply chips (e.g. [\"Hear Section 1\", \"Hear Open Question\", \"Approve Plan\"]). Respond strictly in JSON format with no markdown wrappers: {\"spoken_text\": \"...\", \"suggested_chips\": [\"...\"]}"

	reqBody := map[string]any{
		"model":       "claude-3-5-haiku-latest",
		"max_tokens":  256,
		"temperature": 0.2,
		"system":      systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": content},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, err
	}

	httpReq, err := http.NewRequestWithContext(apiCtx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("anthropic api returned status %d", resp.StatusCode)
	}

	var anthropicResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return "", nil, err
	}

	if len(anthropicResp.Content) == 0 || anthropicResp.Content[0].Text == "" {
		return "", nil, fmt.Errorf("empty content in anthropic api response")
	}

	rawText := strings.TrimSpace(anthropicResp.Content[0].Text)
	rawText = strings.TrimPrefix(rawText, "```json")
	rawText = strings.TrimPrefix(rawText, "```")
	rawText = strings.TrimSuffix(rawText, "```")
	rawText = strings.TrimSpace(rawText)

	var parsed struct {
		SpokenText     string   `json:"spoken_text"`
		SuggestedChips []string `json:"suggested_chips"`
	}
	if err := json.Unmarshal([]byte(rawText), &parsed); err != nil || parsed.SpokenText == "" {
		return "", nil, fmt.Errorf("failed to parse json from anthropic response: %v", err)
	}

	return parsed.SpokenText, parsed.SuggestedChips, nil
}
