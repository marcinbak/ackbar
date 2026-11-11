package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type rewindHumanTurn struct {
	Turn      int       `json:"turn"`
	Timestamp time.Time `json:"timestamp"`
	Preview   string    `json:"preview"`
	LineIndex int       `json:"-"`
	RawPrompt string    `json:"-"`
}

func (s *Server) extractClaudeRewindTurns(home string, sess *Session) ([]rewindHumanTurn, []string, string) {
	if sess == nil {
		return nil, nil, ""
	}
	targetFile := findClaudeTranscriptPath(home, sess.NativeID, sess.Cwd)
	if targetFile == "" || !fileExists(targetFile) {
		return nil, nil, ""
	}

	f, err := os.Open(targetFile)
	if err != nil {
		return nil, nil, ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var lines []string
	var turns []rewindHumanTurn
	lineIdx := 0

	adapter := ClaudeAgentAdapter{}
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)

		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
				msgType, _ := raw["type"].(string)
				if msgType == "user" {
					var fullText string
					if msgObj, ok := raw["message"].(map[string]interface{}); ok {
						if contentStr, ok := msgObj["content"].(string); ok {
							fullText = contentStr
						} else if contentArr, ok := msgObj["content"].([]interface{}); ok {
							var parts []string
							for _, cItem := range contentArr {
								if cMap, ok := cItem.(map[string]interface{}); ok {
									if cType, ok := cMap["type"].(string); ok && cType == "text" {
										if txt, ok := cMap["text"].(string); ok && txt != "" {
											parts = append(parts, txt)
										}
									}
								} else if cStr, ok := cItem.(string); ok && cStr != "" {
									parts = append(parts, cStr)
								}
							}
							fullText = strings.Join(parts, "\n")
						}
					}
					cleanPrompt, keep := adapter.FilterUserEntry(raw, fullText)
					if keep && strings.TrimSpace(cleanPrompt) != "" {
						tsStr, _ := raw["timestamp"].(string)
						ts, _ := time.Parse(time.RFC3339, tsStr)
						if ts.IsZero() {
							ts = time.Now()
						}
						preview := strings.TrimSpace(cleanPrompt)
						if len(preview) > 120 {
							preview = preview[:120] + "..."
						}
						turns = append(turns, rewindHumanTurn{
							Turn:      len(turns) + 1,
							Timestamp: ts,
							Preview:   preview,
							LineIndex: lineIdx,
							RawPrompt: cleanPrompt,
						})
					}
				}
			}
		}
		lineIdx++
	}

	return turns, lines, targetFile
}

func (s *Server) extractAntigravityRewindTurns(home string, sess *Session) []rewindHumanTurn {
	if sess == nil {
		return nil
	}
	msgs, err := ExtractAntigravityTranscriptMessages(home, sess.NativeID)
	if err != nil || len(msgs) == 0 {
		return nil
	}

	var turns []rewindHumanTurn
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			preview := strings.TrimSpace(m.Content)
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			turns = append(turns, rewindHumanTurn{
				Turn:      len(turns) + 1,
				Timestamp: m.Timestamp,
				Preview:   preview,
				RawPrompt: m.Content,
			})
		}
	}
	return turns
}

func (s *Server) handleSessionRewind(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var bodyBytes []byte
	if r.Method == http.MethodPost {
		bodyReader := http.MaxBytesReader(w, r.Body, 8<<10)
		var err error
		bodyBytes, err = io.ReadAll(bodyReader)
		if err != nil {
			http.Error(w, "Invalid or oversized request body", http.StatusBadRequest)
			return
		}
	}

	sess, proxied := s.resolveSessionOrProxy(w, r.Method, sessionID, "application/json", bytes.NewReader(bodyBytes), func(_ *Session) string {
		return r.URL.RequestURI()
	})
	if proxied || sess == nil {
		return
	}

	home := s.effectiveHomeDir()
	var turns []rewindHumanTurn
	var rawLines []string
	var targetFile string

	if sess.Agent == "antigravity" {
		turns = s.extractAntigravityRewindTurns(home, sess)
	} else {
		turns, rawLines, targetFile = s.extractClaudeRewindTurns(home, sess)
	}

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"session_id":  sess.ID,
			"agent":       sess.Agent,
			"total_turns": len(turns),
			"turns":       turns,
		})
		return
	}

	var req struct {
		BeforeTurn int `json:"before_turn"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	total := len(turns)
	if total == 0 {
		http.Error(w, "No conversation turns available to rewind", http.StatusBadRequest)
		return
	}
	if req.BeforeTurn < 1 || req.BeforeTurn > total {
		http.Error(w, fmt.Sprintf("Invalid before_turn: must be between 1 and %d", total), http.StatusBadRequest)
		return
	}

	targetTurn := req.BeforeTurn

	if targetTurn == 1 {
		newSess, err := s.rotateHeadlessConversation(r.Context(), sess, "Rewound before Turn 1", "", true)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to rewind session: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"mode":           "empty",
			"before_turn":    1,
			"old_session_id": sess.ID,
			"session":        newSess,
		})
		return
	}

	if sess.Agent == "claude-code" && targetFile != "" && len(rawLines) > 0 {
		cutIdx := turns[targetTurn-1].LineIndex
		for cutIdx > 0 {
			prevLine := strings.TrimSpace(rawLines[cutIdx-1])
			if prevLine == "" {
				cutIdx--
				continue
			}
			var prevMap map[string]interface{}
			if err := json.Unmarshal([]byte(prevLine), &prevMap); err == nil {
				if prevType, _ := prevMap["type"].(string); prevType == "attachment" {
					cutIdx--
					continue
				}
			}
			break
		}

		newUUID := generateUUID()
		newFile := filepath.Join(filepath.Dir(targetFile), newUUID+".jsonl")

		destFile, err := os.OpenFile(newFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err == nil {
			writer := bufio.NewWriter(destFile)
			for i := 0; i < cutIdx; i++ {
				line := strings.TrimSpace(rawLines[i])
				if line == "" {
					continue
				}
				var item map[string]interface{}
				if err := json.Unmarshal([]byte(line), &item); err == nil {
					if itemType, _ := item["type"].(string); itemType == "last-prompt" {
						continue
					}
					item["sessionId"] = newUUID
					rewritten, _ := json.Marshal(item)
					_, _ = writer.Write(rewritten)
					_, _ = writer.WriteString("\n")
				}
			}
			_ = writer.Flush()
			_ = destFile.Close()

			newSess, rotErr := s.rotateHeadlessConversation(r.Context(), sess, fmt.Sprintf("Rewound before Turn %d", targetTurn), "", true, newUUID)
			if rotErr == nil {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status":         "ok",
					"mode":           "cloned_transcript",
					"before_turn":    targetTurn,
					"old_session_id": sess.ID,
					"session":        newSess,
				})
				return
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<rewound_conversation_history turns=\"1..%d\">\n", targetTurn-1))
	for i := 0; i < targetTurn-1 && i < len(turns); i++ {
		sb.WriteString(fmt.Sprintf("Turn %d (%s):\n%s\n\n", turns[i].Turn, turns[i].Timestamp.Format(time.RFC3339), turns[i].RawPrompt))
	}
	sb.WriteString("</rewound_conversation_history>\nPlease continue the conversation from this prior state.")
	seedPrompt := sb.String()
	if len(seedPrompt) > 48<<10 {
		seedPrompt = seedPrompt[:48<<10] + "\n[Truncated history]\n"
	}

	newSess, err := s.rotateHeadlessConversation(r.Context(), sess, fmt.Sprintf("Rewound before Turn %d", targetTurn), "", true)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to rewind session: %v", err), http.StatusInternalServerError)
		return
	}

	if s.headless != nil {
		newSessCopy := *newSess
		go func() {
			_ = s.headless.RunTurn(context.Background(), &newSessCopy, seedPrompt)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"mode":           "seeded_history",
		"before_turn":    targetTurn,
		"old_session_id": sess.ID,
		"session":        newSess,
	})
}
