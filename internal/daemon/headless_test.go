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
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "test-session-parser"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"type":"message_start","message":{"id":"msg_123","role":"assistant"}}
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I will "}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"inspect files."}}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la"}}}
{"type":"tool_result","content":"file1.go\nfile2.go"}
{"type":"result","result":"Done."}
`

	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput))
	}()

	var receivedEvents []ChatStreamEvent
	for {
		select {
		case evt := <-ch:
			receivedEvents = append(receivedEvents, evt)
			if evt.Type == "turn_complete" {
				goto done
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for events, received %d so far", len(receivedEvents))
		}
	}
done:

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

func TestHeadlessStreamParser_PostToolTextSpacing(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "test-session-post-tool"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Checking now."}}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Bash","input":{}}}
{"type":"tool_result","content":"ok"}
{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"All files look good."}}
{"type":"result","result":"Done."}
`

	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput))
	}()

	var textDeltas []string
	for {
		select {
		case evt := <-ch:
			if evt.Type == "text_delta" {
				textDeltas = append(textDeltas, evt.Text)
			} else if evt.Type == "turn_complete" {
				goto done
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for events")
		}
	}
done:

	if len(textDeltas) != 2 {
		t.Fatalf("Expected 2 text deltas, got %d: %v", len(textDeltas), textDeltas)
	}
	if textDeltas[0] != "Checking now." {
		t.Errorf("Expected first delta 'Checking now.', got %q", textDeltas[0])
	}
	if textDeltas[1] != "\n\nAll files look good." {
		t.Errorf("Expected post-tool delta to start with double newline, got %q", textDeltas[1])
	}
}

func TestHeadlessStreamParser_ConsecutiveAssistantMessages(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "test-session-consecutive-assistant"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"type":"assistant","message":{"content":"First message."}}
{"type":"assistant","message":{"content":"Second message."}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Third part A."},{"type":"text","text":"Third part B."}]}}
{"type":"result","result":"Done."}
`

	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput))
	}()

	var textDeltas []string
	for {
		select {
		case evt := <-ch:
			if evt.Type == "text_delta" {
				textDeltas = append(textDeltas, evt.Text)
			} else if evt.Type == "turn_complete" {
				goto done
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for events")
		}
	}
done:

	if len(textDeltas) != 3 {
		t.Fatalf("Expected 3 text deltas, got %d: %v", len(textDeltas), textDeltas)
	}
	if textDeltas[0] != "First message." {
		t.Errorf("Expected 'First message.', got %q", textDeltas[0])
	}
	if textDeltas[1] != "\n\nSecond message." {
		t.Errorf("Expected consecutive message to have double newline prefix, got %q", textDeltas[1])
	}
	if textDeltas[2] != "\n\nThird part A.\n\nThird part B." {
		t.Errorf("Expected consecutive multipart message to separate with double newlines, got %q", textDeltas[2])
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

func TestHeadlessRunner_PromptQueue(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "claude-code:local:test-queue-sess"

	// Initial state should be empty
	items, paused := runner.GetPromptQueue(sessionID)
	if len(items) != 0 {
		t.Fatalf("Expected empty queue, got %d items", len(items))
	}
	if paused {
		t.Fatalf("Expected queue not paused initially")
	}

	// Subscribe to events
	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	// Enqueue 3 items
	q1 := runner.EnqueuePrompt(sessionID, "Prompt 1")
	q2 := runner.EnqueuePrompt(sessionID, "Prompt 2")
	q3 := runner.EnqueuePrompt(sessionID, "Prompt 3")

	items, _ = runner.GetPromptQueue(sessionID)
	if len(items) != 3 {
		t.Fatalf("Expected 3 queued items, got %d", len(items))
	}
	if items[0].Text != "Prompt 1" || items[1].Text != "Prompt 2" || items[2].Text != "Prompt 3" {
		t.Errorf("Queue order mismatch: %+v", items)
	}

	// Delete item 2
	deleted := runner.DeletePromptQueueItem(sessionID, q2.ID)
	if !deleted {
		t.Errorf("Expected q2 to be deleted")
	}
	items, _ = runner.GetPromptQueue(sessionID)
	if len(items) != 2 {
		t.Fatalf("Expected 2 queued items after deletion, got %d", len(items))
	}
	if items[0].ID != q1.ID || items[1].ID != q3.ID {
		t.Errorf("Unexpected items after deletion: %+v", items)
	}

	// Pause and Emit update
	runner.SetQueuePaused(sessionID, true)
	if !runner.IsQueuePaused(sessionID) {
		t.Errorf("Expected queue to be paused")
	}
	runner.EmitQueueUpdate(sessionID)

	select {
	case evt := <-ch:
		if evt.Type != "queue_update" {
			t.Errorf("Expected queue_update event, got %s", evt.Type)
		}
		if len(evt.QueueItems) != 2 || !evt.QueuePaused {
			t.Errorf("Unexpected queue_update payload: %+v", evt)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout waiting for queue_update event")
	}

	// Dequeue prompt (FIFO)
	next := runner.DequeuePrompt(sessionID)
	if next == nil || next.ID != q1.ID {
		t.Errorf("Expected q1 dequeued, got %+v", next)
	}

	// Clear queue
	runner.ClearPromptQueue(sessionID)
	items, paused = runner.GetPromptQueue(sessionID)
	if len(items) != 0 || paused {
		t.Errorf("Expected empty unpaused queue after ClearPromptQueue, got len=%d paused=%v", len(items), paused)
	}
}
