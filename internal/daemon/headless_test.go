package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
		"GEMINI_API_KEY=gemini-secret-key-123",
		"GOOGLE_API_KEY=google-api-key-456",
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
		if strings.HasPrefix(v, "GEMINI_API_KEY=") {
			t.Errorf("Expected GEMINI_API_KEY to be stripped, got %s", v)
		}
		if strings.HasPrefix(v, "GOOGLE_API_KEY=") {
			t.Errorf("Expected GOOGLE_API_KEY to be stripped, got %s", v)
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

func TestHeadlessStreamParser_Antigravity(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "antigravity:local:temp-uuid-999"
	sess := &Session{
		ID:       sessionID,
		Agent:    "antigravity",
		Host:     "local",
		NativeID: "temp-uuid-999",
	}

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"event":"init","conversation_id":"real-ag-conv-456","init":{"cwd":"/tmp","tools":["run_command"]}}
{"event":"step_update","step_update":{"conversation_id":"real-ag-conv-456","step_index":0,"state":"DONE","step_type":"user_input"}}
{"event":"step_update","step_update":{"conversation_id":"real-ag-conv-456","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"Let me check."}}
{"event":"step_update","step_update":{"conversation_id":"real-ag-conv-456","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"echo hello"}}}}
{"event":"step_update","step_update":{"conversation_id":"real-ag-conv-456","step_index":2,"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"echo hello"},"output":"hello\n"}}}
{"event":"step_update","step_update":{"conversation_id":"real-ag-conv-456","step_index":3,"state":"ACTIVE","step_type":"agent_response","text_delta":"Command returned hello."}}
{"event":"result","result":{"conversation_id":"real-ag-conv-456","status":"SUCCESS","response":"Command returned hello."}}
`

	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput), sess)
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
	if sess.NativeID != "real-ag-conv-456" {
		t.Errorf("Expected sess.NativeID to be updated to 'real-ag-conv-456', got '%s'", sess.NativeID)
	}
	if sess.ID != "antigravity:local:real-ag-conv-456" {
		t.Errorf("Expected sess.ID to be migrated to 'antigravity:local:real-ag-conv-456', got %q", sess.ID)
	}
	if sess.OldID != sessionID {
		t.Errorf("Expected sess.OldID to be %q, got %q", sessionID, sess.OldID)
	}

	if len(receivedEvents) < 5 {
		t.Fatalf("Expected at least 5 events, got %d", len(receivedEvents))
	}

	hasText1 := false
	hasToolStart := false
	hasToolResult := false
	hasText2 := false
	hasComplete := false

	for _, evt := range receivedEvents {
		switch evt.Type {
		case "text_delta":
			if evt.Text == "Let me check." {
				hasText1 = true
			}
			if evt.Text == "Command returned hello." {
				hasText2 = true
			}
		case "tool_start":
			if evt.ToolName == "run_command" {
				hasToolStart = true
				if params, ok := evt.ToolInput.(map[string]interface{}); ok {
					if cmd, _ := params["CommandLine"].(string); cmd != "echo hello" {
						t.Errorf("Unexpected tool params: %+v", params)
					}
				}
			}
		case "tool_result":
			if evt.ToolName == "run_command" && evt.ToolOutput == "hello\n" && !evt.IsError {
				hasToolResult = true
			}
		case "turn_complete":
			if evt.Text == "Command returned hello." {
				hasComplete = true
			}
		}
	}

	if !hasText1 {
		t.Errorf("Missing first text delta event")
	}
	if !hasToolStart {
		t.Errorf("Missing tool_start event")
	}
	if !hasToolResult {
		t.Errorf("Missing tool_result event")
	}
	if !hasText2 {
		t.Errorf("Missing second text delta event")
	}
	if !hasComplete {
		t.Errorf("Missing turn_complete event")
	}
}

func TestHeadlessStreamParser_AntigravityError(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "antigravity:local:err-session"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"event":"result","result":{"conversation_id":"ag-err-1","status":"ERROR","error":"API rate limit exceeded"}}
`
	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput))
	}()

	select {
	case evt := <-ch:
		if evt.Type != "error" || !evt.IsError || evt.Text != "API rate limit exceeded" {
			t.Errorf("Unexpected event received: %+v", evt)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout waiting for error event")
	}
}

func TestHeadlessRunner_AgentValidation(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)

	unsupportedSess := &Session{
		ID:    "unknown:local:123",
		Agent: "some-unknown-agent",
	}
	err := runner.RunTurn(context.Background(), unsupportedSess, "hello")
	if err != ErrAgentUnsupported {
		t.Errorf("Expected ErrAgentUnsupported for unknown agent, got: %v", err)
	}
}

func TestHeadlessRunner_ToolResultNamePropagation(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "test-session-tool-propagation"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_use_123","name":"Agent","input":{"prompt":"Do work"}}}
{"type":"tool_result","tool_use_id":"tool_use_123","content":"Finished successfully"}
{"type":"result","result":"Done."}
`
	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput))
	}()

	var receivedToolStart, receivedToolResult bool
	var resultToolName string
	for {
		select {
		case evt := <-ch:
			if evt.Type == "tool_start" {
				receivedToolStart = true
				if evt.ToolName != "Agent" {
					t.Errorf("Expected ToolName 'Agent', got %q", evt.ToolName)
				}
			} else if evt.Type == "tool_result" {
				receivedToolResult = true
				resultToolName = evt.ToolName
			} else if evt.Type == "turn_complete" {
				goto done
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for events")
		}
	}
done:
	if !receivedToolStart {
		t.Errorf("Expected tool_start event")
	}
	if !receivedToolResult {
		t.Errorf("Expected tool_result event")
	}
	if resultToolName != "Agent" {
		t.Errorf("Expected tool_result event to have ToolName 'Agent', got %q", resultToolName)
	}
}

func TestHeadlessRunner_OnTurnCompleteCallback(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	var callbackCalled bool
	var cbSessionID, cbNativeID string

	runner.SetOnTurnComplete(func(sessID, natID string) {
		callbackCalled = true
		cbSessionID = sessID
		cbNativeID = natID
	})

	runner.mu.RLock()
	cb := runner.onTurnComplete
	runner.mu.RUnlock()

	if cb == nil {
		t.Fatalf("Expected onTurnComplete callback to be registered")
	}
	cb("test-sess-id", "test-nat-id")

	if !callbackCalled || cbSessionID != "test-sess-id" || cbNativeID != "test-nat-id" {
		t.Errorf("Callback not called with expected args")
	}
}

func TestInitDB_HeadlessMigrationsAndBootCleanup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "headless_migration.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	// 1. Clean headless session with empty tmux_name and working state
	headlessClean := &Session{
		ID:          "claude-code:local:uuid-clean-headless",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-clean-headless",
		Cwd:         "/tmp",
		State:       StateWorking,
		Activity:    "Thinking...",
		PID:         999999,
		TmuxName:    "",
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(headlessClean); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 2. Corrupted headless session whose tmux_name was previously set to ackbar-<agent>-<nativeID>
	headlessCorrupted := &Session{
		ID:          "antigravity:local:uuid-corrupted-headless",
		Agent:       "antigravity",
		Host:        "local",
		NativeID:    "uuid-corrupted-headless",
		Cwd:         "/tmp",
		State:       StateIdle,
		TmuxName:    "ackbar-antigravity-uuid-corrupted-headless",
		EngineType:  EngineHeadless,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(headlessCorrupted); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 3. Tmux session with empty tmux_name (should still be populated by migration)
	tmuxSess := &Session{
		ID:          "claude-code:local:uuid-tmux-sess",
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    "uuid-tmux-sess",
		Cwd:         "/tmp",
		State:       StateIdle,
		TmuxName:    "",
		EngineType:  EngineTmux,
		StartedAt:   time.Now(),
		LastEventAt: time.Now(),
	}
	if err := db.SaveSession(tmuxSess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}
	_ = db.Close()

	// Re-open DB to run startup migrations
	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to re-init db: %v", err)
	}
	defer db2.Close()

	gotClean, err := db2.GetSession(headlessClean.ID)
	if err != nil || gotClean == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if gotClean.TmuxName != "" {
		t.Errorf("Expected headless session tmux_name to remain empty, got %q", gotClean.TmuxName)
	}
	if gotClean.State != StateIdle {
		t.Errorf("Expected stale working headless session to be reset to StateIdle on startup, got %v", gotClean.State)
	}
	if gotClean.PID != 0 {
		t.Errorf("Expected stale working headless session PID to be reset to 0, got %d", gotClean.PID)
	}

	gotCorrupted, err := db2.GetSession(headlessCorrupted.ID)
	if err != nil || gotCorrupted == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if gotCorrupted.TmuxName != "" {
		t.Errorf("Expected corrupted headless session tmux_name to be repaired to empty, got %q", gotCorrupted.TmuxName)
	}

	gotTmux, err := db2.GetSession(tmuxSess.ID)
	if err != nil || gotTmux == nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if gotTmux.TmuxName != "ackbar-claude-code-uuid-tmux-sess" {
		t.Errorf("Expected tmux session tmux_name to be migrated, got %q", gotTmux.TmuxName)
	}
}

func TestHeadlessRunner_BuildArgs_ClaudeAndAntigravity(t *testing.T) {
	tmpHome := t.TempDir()
	runner := NewHeadlessRunner(nil, nil)

	// 1. Claude Code Turn 1: should include --dangerously-skip-permissions and --session-id
	claudeSess := &Session{
		ID:         "claude-code:local:11111111-2222-3333-4444-555555555555",
		Agent:      "claude-code",
		Host:       "local",
		NativeID:   "11111111-2222-3333-4444-555555555555",
		Cwd:        "/tmp/proj",
		EngineType: EngineHeadless,
	}
	_, args1, err := runner.buildArgs(claudeSess, "Hello Claude", tmpHome)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if !slices.Contains(args1, "--dangerously-skip-permissions") {
		t.Errorf("Expected Claude args to contain --dangerously-skip-permissions, got %v", args1)
	}
	if !slices.Contains(args1, "--session-id") {
		t.Errorf("Expected Claude Turn 1 args to contain --session-id, got %v", args1)
	}

	// 2. Claude Code Turn 2: after FirstPrompt is set, should use --resume
	claudeSess.FirstPrompt = "Hello Claude"
	_, args2, err := runner.buildArgs(claudeSess, "Follow up", tmpHome)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if !slices.Contains(args2, "--dangerously-skip-permissions") {
		t.Errorf("Expected Claude Turn 2 args to contain --dangerously-skip-permissions, got %v", args2)
	}
	if !slices.Contains(args2, "--resume") {
		t.Errorf("Expected Claude Turn 2 args to contain --resume, got %v", args2)
	}

	// 3. Antigravity Turn 1: before init, should NOT pass --conversation
	agySess := &Session{
		ID:         "antigravity:local:temp-uuid-1",
		Agent:      "antigravity",
		Host:       "local",
		NativeID:   "temp-uuid-1",
		Cwd:        "/tmp/proj",
		EngineType: EngineHeadless,
	}
	_, agyArgs1, err := runner.buildArgs(agySess, "First agy prompt", tmpHome)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if slices.Contains(agyArgs1, "--conversation") {
		t.Errorf("Expected Antigravity Turn 1 args not to contain --conversation, got %v", agyArgs1)
	}

	// Simulate init event updating NativeID and persisting FirstPrompt
	runner.processStreamWithPrompt(agySess.ID, strings.NewReader(`{"event":"init","conversation_id":"real-agy-uuid-2"}`), "First agy prompt", agySess)
	if agySess.NativeID != "real-agy-uuid-2" {
		t.Errorf("Expected NativeID 'real-agy-uuid-2', got %q", agySess.NativeID)
	}
	if agySess.FirstPrompt != "First agy prompt" {
		t.Errorf("Expected FirstPrompt 'First agy prompt' after init, got %q", agySess.FirstPrompt)
	}

	_, agyArgs2, err := runner.buildArgs(agySess, "Second agy prompt", tmpHome)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if !slices.Contains(agyArgs2, "--conversation") || !slices.Contains(agyArgs2, "real-agy-uuid-2") {
		t.Errorf("Expected Antigravity Turn 2 args to contain --conversation real-agy-uuid-2, got %v", agyArgs2)
	}
}

func TestHeadlessStreamParser_ClaudeAssistantToolUseAndUserToolResult(t *testing.T) {
	runner := NewHeadlessRunner(nil, nil)
	sessionID := "claude-code:local:test-assistant-tool-blocks"

	ch, cleanup := runner.Subscribe(sessionID)
	defer cleanup()

	sampleOutput := `
{"type":"system","subtype":"init","session_id":"test-assistant-tool-blocks"}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Inspecting workspace"},{"type":"text","text":"Running ls now."},{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"README.md\ngo.mod","is_error":false}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Found README.md and go.mod."}]}}
{"type":"result","result":"Found README.md and go.mod.","is_error":false}
`

	go func() {
		runner.processStream(sessionID, strings.NewReader(sampleOutput))
	}()

	var events []ChatStreamEvent
	for {
		select {
		case evt := <-ch:
			events = append(events, evt)
			if evt.Type == "turn_complete" {
				goto done
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for stream events, got %d: %+v", len(events), events)
		}
	}
done:
	var hasThought, hasText1, hasToolStart, hasToolResult, hasText2 bool
	for _, e := range events {
		switch e.Type {
		case "thought_delta":
			if e.Thinking == "Inspecting workspace" {
				hasThought = true
			}
		case "text_delta":
			if e.Text == "Running ls now." {
				hasText1 = true
			}
			if e.Text == "\n\nFound README.md and go.mod." {
				hasText2 = true
			}
		case "tool_start":
			if e.ToolName == "Bash" {
				hasToolStart = true
			}
		case "tool_result":
			if e.ToolName == "Bash" && e.ToolOutput == "README.md\ngo.mod" {
				hasToolResult = true
			}
		}
	}
	if !hasThought || !hasText1 || !hasToolStart || !hasToolResult || !hasText2 {
		t.Errorf("Missing expected stream events: thought=%v text1=%v toolStart=%v toolResult=%v text2=%v (all=%+v)",
			hasThought, hasText1, hasToolStart, hasToolResult, hasText2, events)
	}
}

func TestHandleSpawn_HeadlessWithPromptTriggersTurn(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	mockClaude := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\necho '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Hello from headless spawn\"}]}}'\necho '{\"type\":\"result\",\"result\":\"Hello from headless spawn\"}'\n"
	if err := os.WriteFile(mockClaude, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile mock claude failed: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	db, err := InitDB(filepath.Join(tmpDir, "spawn_headless.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	payload, _ := json.Marshal(map[string]string{
		"agent":       "claude-code",
		"cwd":         tmpDir,
		"engine_type": EngineHeadless,
		"prompt":      "Initial headless prompt",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/spawn", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	server.handleSpawn(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from handleSpawn, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode spawn response: %v", err)
	}
	sessID := resp["id"]
	if sessID == "" {
		t.Fatalf("Expected non-empty session id in spawn response")
	}

	// Wait for the triggered headless turn to complete and persist FirstPrompt
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sess, _ := db.GetSession(sessID)
		if sess != nil && sess.FirstPrompt == "Initial headless prompt" && sess.State == StateIdle {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	sess, _ := db.GetSession(sessID)
	t.Fatalf("Expected headless session to complete initial turn with FirstPrompt set, got: %+v", sess)
}

func TestHeadlessLivenessAndHookStateAuthority(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	db, err := InitDB(filepath.Join(tmpDir, "liveness_headless.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	server := NewServer(db)
	mockP := &mockDynamicProvider{agentName: "claude-code"}
	server.RegisterProvider(mockP)

	nativeUUID := "11111111-2222-3333-4444-555555555999"
	sessID := "claude-code:local:" + nativeUUID
	sess := &Session{
		ID:          sessID,
		Agent:       "claude-code",
		Host:        "local",
		NativeID:    nativeUUID,
		Cwd:         tmpDir,
		Managed:     true,
		TmuxName:    "",
		EngineType:  EngineHeadless,
		State:       StateWorking,
		Activity:    "Thinking...",
		StartedAt:   time.Now().Add(-1 * time.Hour),
		LastEventAt: time.Now().Add(-1 * time.Hour),
	}
	if err := db.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// 1. verifySessionLiveness must heal working -> idle when !s.headless.IsRunning(sess.ID), and must NOT mark StateEnded
	server.verifySessionLiveness(context.Background(), []*Session{sess})
	afterHeal, _ := db.GetSession(sessID)
	if afterHeal == nil || afterHeal.State != StateIdle {
		t.Fatalf("Expected verifySessionLiveness to heal headless session to StateIdle, got %+v", afterHeal)
	}

	// 2. Late PreToolUse or SessionEnd hook when !IsRunning must NOT flip headless session to StateWorking or StateEnded
	mockP.event = &Event{
		Agent:       "claude-code",
		NativeID:    nativeUUID,
		Cwd:         tmpDir,
		EventName:   "PreToolUse",
		State:       StateWorking,
		Activity:    "Running Bash",
		LastEventAt: time.Now(),
	}
	server.processHookEvent(mockP, "PreToolUse", "local", []byte(`{}`))
	afterHook, _ := db.GetSession(sessID)
	if afterHook == nil || afterHook.State != StateIdle {
		t.Fatalf("Expected late PreToolUse hook not to flip idle headless session to StateWorking, got %+v", afterHook)
	}

	mockP.event = &Event{
		Agent:       "claude-code",
		NativeID:    nativeUUID,
		Cwd:         tmpDir,
		EventName:   "SessionEnd",
		State:       StateEnded,
		Activity:    "Session ended",
		LastEventAt: time.Now(),
	}
	server.processHookEvent(mockP, "SessionEnd", "local", []byte(`{}`))
	afterEndHook, _ := db.GetSession(sessID)
	if afterEndHook == nil || afterEndHook.State != StateIdle {
		t.Fatalf("Expected SessionEnd hook not to flip headless session to StateEnded, got %+v", afterEndHook)
	}

	// 3. Stale subagent file (> 30m old) without end_turn must be marked completed
	subDir := filepath.Join(tmpDir, ".claude", "projects", encodeClaudeProjectDir(tmpDir), nativeUUID, "subagents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("MkdirAll subDir failed: %v", err)
	}
	metaFile := filepath.Join(subDir, "agent-1.meta.json")
	jsonlFile := filepath.Join(subDir, "agent-1.jsonl")
	_ = os.WriteFile(metaFile, []byte(`{"name":"worker-1","agentType":"implementer"}`), 0644)
	_ = os.WriteFile(jsonlFile, []byte(`{"type":"assistant","message":{"stop_reason":"tool_use"}}`), 0644)
	oldTime := time.Now().Add(-45 * time.Minute)
	_ = os.Chtimes(metaFile, oldTime, oldTime)
	_ = os.Chtimes(jsonlFile, oldTime, oldTime)

	subs := loadClaudeSubagentsFromDir(tmpDir, nativeUUID, tmpDir)
	if len(subs) != 1 || subs[0].State != "completed" {
		t.Errorf("Expected stale subagent (>30m) to be marked completed, got %+v", subs)
	}
}
