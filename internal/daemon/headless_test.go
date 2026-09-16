package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStripBilledCredentials(t *testing.T) {
	inputEnv := []string{
		"PATH=/usr/bin:/bin",
		"ANTHROPIC_API_KEY=sk-ant-api03-secretkey",
		"USER=dev4u",
		"ANTHROPIC_AUTH_TOKEN=auth_token_xyz",
		"HOME=/Users/dev4u",
		"CLAUDE_CONFIG_DIR=/Users/dev4u/.claude",
	}

	cleaned := StripBilledCredentials(inputEnv)

	for _, v := range cleaned {
		if strings.HasPrefix(v, "ANTHROPIC_API_KEY=") {
			t.Errorf("Expected ANTHROPIC_API_KEY to be stripped, got %s", v)
		}
		if strings.HasPrefix(v, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("Expected ANTHROPIC_AUTH_TOKEN to be stripped, got %s", v)
		}
	}

	if len(cleaned) != 4 {
		t.Errorf("Expected 4 env vars remaining, got %d: %v", len(cleaned), cleaned)
	}
}

func TestHeadlessRunner_SubscribeAndEmit(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "test-session-123"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	if runner.IsRunning(sessionID) {
		t.Errorf("Expected session not running")
	}

	evt := ChatStreamEvent{
		SessionID: sessionID,
		Type:      "text_delta",
		Text:      "Hello world",
	}

	runner.Emit(sessionID, evt)

	select {
	case received := <-ch:
		if received.Text != "Hello world" || received.Type != "text_delta" {
			t.Errorf("Unexpected event received: %+v", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Timed out waiting for event")
	}

	cleanup()
	runner.Emit(sessionID, evt)
	// Channel should be closed or unsubscribed, no deadlock
}

func TestHeadlessStreamParser(t *testing.T) {
	sampleOutput := `
{"type":"message_start","message":{"id":"msg_123","role":"assistant"}}
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I will "}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"inspect files."}}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la"}}}
{"type":"tool_result","content":"file1.go\nfile2.go"}
{"type":"result","result":"Done."}
`

	var receivedEvents []ChatStreamEvent
	lines := strings.Split(strings.TrimSpace(sampleOutput), "\n")

	for _, line := range lines {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("Failed to parse JSON line: %v", err)
		}

		evtType, _ := raw["type"].(string)
		switch evtType {
		case "content_block_delta":
			if delta, ok := raw["delta"].(map[string]interface{}); ok {
				deltaType, _ := delta["type"].(string)
				if deltaType == "text_delta" {
					text, _ := delta["text"].(string)
					receivedEvents = append(receivedEvents, ChatStreamEvent{
						Type: "text_delta",
						Text: text,
					})
				}
			}
		case "content_block_start":
			if cb, ok := raw["content_block"].(map[string]interface{}); ok {
				if cb["type"] == "tool_use" {
					toolName, _ := cb["name"].(string)
					receivedEvents = append(receivedEvents, ChatStreamEvent{
						Type:     "tool_start",
						ToolName: toolName,
					})
				}
			}
		case "tool_result":
			out, _ := raw["content"].(string)
			receivedEvents = append(receivedEvents, ChatStreamEvent{
				Type:       "tool_result",
				ToolOutput: out,
			})
		case "result":
			res, _ := raw["result"].(string)
			receivedEvents = append(receivedEvents, ChatStreamEvent{
				Type: "turn_complete",
				Text: res,
			})
		}
	}

	if len(receivedEvents) != 5 {
		t.Fatalf("Expected 5 parsed events, got %d", len(receivedEvents))
	}

	if receivedEvents[0].Text != "I will " || receivedEvents[1].Text != "inspect files." {
		t.Errorf("Unexpected text deltas: %+v", receivedEvents[0:2])
	}
	if receivedEvents[2].Type != "tool_start" || receivedEvents[2].ToolName != "Bash" {
		t.Errorf("Unexpected tool_start event: %+v", receivedEvents[2])
	}
	if receivedEvents[3].Type != "tool_result" || receivedEvents[3].ToolOutput != "file1.go\nfile2.go" {
		t.Errorf("Unexpected tool_result event: %+v", receivedEvents[3])
	}
	if receivedEvents[4].Type != "turn_complete" || receivedEvents[4].Text != "Done." {
		t.Errorf("Unexpected turn_complete event: %+v", receivedEvents[4])
	}
}

func TestHeadlessRunner_DatabasePersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	sess := &Session{
		ID:          "claude-code:local:uuid-headless-1",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-headless-1",
		Cwd:         "/tmp",
		State:       StateIdle,
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}

	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	loaded, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if loaded == nil || loaded.EngineType != EngineHeadless {
		t.Fatalf("Expected EngineType == 'headless', got: %+v", loaded)
	}
}

func TestHeadlessStreamParser_CommandAndAssistantBlocks(t *testing.T) {
	sampleOutput := `
{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"Total cost: $0.05\nUsage: 1000 input"}],"role":"assistant"},"local_command_source":"<local-command-stdout>Total cost: $0.05</local-command-stdout>","local_command_run":{"command":"usage","args":""}}
{"type":"result","result":"Total cost: $0.05","local_command":"cost"}
{"type":"result","is_error":true,"result":"Failed to authenticate"}
`

	var receivedEvents []ChatStreamEvent
	lines := strings.Split(strings.TrimSpace(sampleOutput), "\n")

	for _, line := range lines {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("Failed to parse JSON line: %v", err)
		}

		evtType, _ := raw["type"].(string)
		switch evtType {
		case "assistant":
			if msg, ok := raw["message"].(map[string]interface{}); ok {
				if contentStr, ok := msg["content"].(string); ok && contentStr != "" {
					receivedEvents = append(receivedEvents, ChatStreamEvent{
						Type: "text_delta",
						Text: contentStr,
					})
				} else if contentArr, ok := msg["content"].([]interface{}); ok {
					for _, item := range contentArr {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if itemMap["type"] == "text" {
								if txt, ok := itemMap["text"].(string); ok && txt != "" {
									receivedEvents = append(receivedEvents, ChatStreamEvent{
										Type: "text_delta",
										Text: txt,
									})
								}
							}
						}
					}
				}
			}
		case "result":
			resText, _ := raw["result"].(string)
			isErr, _ := raw["is_error"].(bool)
			if isErr && resText != "" {
				receivedEvents = append(receivedEvents, ChatStreamEvent{
					Type:    "error",
					Text:    resText,
					IsError: true,
				})
			} else {
				receivedEvents = append(receivedEvents, ChatStreamEvent{
					Type: "turn_complete",
					Text: resText,
				})
			}
		}
	}

	if len(receivedEvents) != 3 {
		t.Fatalf("Expected 3 parsed events, got %d", len(receivedEvents))
	}
	if receivedEvents[0].Type != "text_delta" || !strings.Contains(receivedEvents[0].Text, "Total cost: $0.05") {
		t.Errorf("Unexpected assistant block event: %+v", receivedEvents[0])
	}
	if receivedEvents[1].Type != "turn_complete" || receivedEvents[1].Text != "Total cost: $0.05" {
		t.Errorf("Unexpected turn_complete event: %+v", receivedEvents[1])
	}
	if receivedEvents[2].Type != "error" || receivedEvents[2].Text != "Failed to authenticate" {
		t.Errorf("Unexpected error event: %+v", receivedEvents[2])
	}
}
