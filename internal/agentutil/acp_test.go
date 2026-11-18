package agentutil

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"ackbar/internal/agent"
)

func TestACPConn_ConcurrentCallsServerRequestsAndEOF(t *testing.T) {
	srvToCliR, srvToCliW := io.Pipe()
	cliToSrvR, cliToSrvW := io.Pipe()

	conn := NewACPConn(srvToCliR, cliToSrvW)
	var mu sync.Mutex
	var notifs []string
	conn.OnNotification = func(method string, params map[string]any) {
		mu.Lock()
		notifs = append(notifs, method)
		mu.Unlock()
	}
	conn.OnServerRequest = func(rawID json.RawMessage, reqID, method string, params map[string]any) bool {
		if method == "session/request_permission" {
			go func() { _ = conn.SendResponse(rawID, map[string]any{"outcome": "ok"}) }()
			return true
		}
		return false
	}
	conn.Start()

	var srvWriteMu sync.Mutex
	writeSrv := func(v any) {
		b, _ := json.Marshal(v)
		srvWriteMu.Lock()
		_, _ = srvToCliW.Write(append(b, '\n'))
		srvWriteMu.Unlock()
	}

	srvDone := make(chan struct{})
	var unknownErrCode int
	var permRespID any
	go func() {
		defer close(srvDone)
		sc := bufio.NewScanner(cliToSrvR)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			m, _ := msg["method"].(string)
			if m == "echo" {
				writeSrv(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": msg["params"]})
			} else if m == "" && msg["error"] != nil {
				mu.Lock()
				if e, ok := msg["error"].(map[string]any); ok {
					unknownErrCode = int(e["code"].(float64))
				}
				mu.Unlock()
			} else if m == "" && msg["result"] != nil {
				mu.Lock()
				permRespID = msg["id"]
				mu.Unlock()
			}
		}
	}()

	writeSrv(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"x": 1}})
	writeSrv(map[string]any{"jsonrpc": "2.0", "id": "srv-unknown", "method": "_x.ai/unknown", "params": map[string]any{}})
	writeSrv(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/request_permission", "params": map[string]any{"toolCall": map[string]any{"title": "bash"}}})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, rpcErr, err := conn.Call(context.Background(), "echo", map[string]any{"n": idx})
			if err != nil || rpcErr != nil || !strings.Contains(string(res), `"n":`) {
				t.Errorf("echo call %d failed: res=%s rpcErr=%v err=%v", idx, res, rpcErr, err)
			}
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := len(notifs) == 1 && unknownErrCode == -32601 && permRespID != nil
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if len(notifs) != 1 || unknownErrCode != -32601 || permRespID == nil {
		t.Fatalf("unexpected callbacks: notifs=%v unknownErrCode=%d permRespID=%v", notifs, unknownErrCode, permRespID)
	}
	mu.Unlock()

	pendingErr := make(chan error, 1)
	go func() {
		_, _, err := conn.Call(context.Background(), "hang", nil)
		pendingErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_ = srvToCliW.Close()
	if err := <-pendingErr; err == nil {
		t.Fatalf("expected pending Call to fail on EOF")
	}
	<-conn.Done()
	_ = cliToSrvW.Close()
	<-srvDone
}

func TestBootstrapACPSession_LoadFallbackAndModelSwitch(t *testing.T) {
	srvToCliR, srvToCliW := io.Pipe()
	cliToSrvR, cliToSrvW := io.Pipe()
	conn := NewACPConn(srvToCliR, cliToSrvW)
	conn.Start()

	var methods []string
	go func() {
		sc := bufio.NewScanner(cliToSrvR)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			m, _ := msg["method"].(string)
			methods = append(methods, m)
			var frame map[string]any
			switch m {
			case "initialize":
				frame = map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{
					"protocolVersion": 1,
					"authMethods":     []any{map[string]any{"id": "cached_token", "name": "Cached"}},
				}}
			case "authenticate":
				frame = map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{}}
			case "session/load":
				frame = map[string]any{"jsonrpc": "2.0", "id": msg["id"], "error": map[string]any{"code": -32004, "message": "not found"}}
			case "session/new":
				frame = map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"sessionId": "adopted-99", "currentModelId": "grok-4.7"}}
			case "session/set_model", "session/set_mode":
				frame = map[string]any{"jsonrpc": "2.0", "id": msg["id"], "error": map[string]any{"code": -32601, "message": "not found"}}
			case "session/set_config_option":
				frame = map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{}}
			}
			b, _ := json.Marshal(frame)
			_, _ = srvToCliW.Write(append(b, '\n'))
		}
	}()

	res, err := BootstrapACPSession(context.Background(), conn, ACPBootstrapInput{
		Cwd:        "/repo",
		SessionID:  "old-1",
		IsExisting: true,
		SelectAuth: func(m []agent.ACPAuthMethod, _ []string) (string, map[string]any, bool) {
			return m[0].ID, map[string]any{"headless": true}, true
		},
	})
	if err != nil {
		t.Fatalf("BootstrapACPSession: %v", err)
	}
	if !res.AdoptedNewID || res.SessionID != "adopted-99" || res.CurrentModel != "grok-4.7" {
		t.Fatalf("unexpected bootstrap result: %+v", res)
	}
	if err := SwitchACPModel(context.Background(), conn, res.SessionID, "grok-4.6", "high"); err != nil {
		t.Fatalf("SwitchACPModel fallback: %v", err)
	}
	if err := SwitchACPMode(context.Background(), conn, res.SessionID, "plan"); err != nil {
		t.Fatalf("SwitchACPMode fallback: %v", err)
	}
	_ = srvToCliW.Close()
	_ = cliToSrvW.Close()
}

func TestACPStreamAndControlParsing(t *testing.T) {
	var st ACPStreamState
	ev1, _ := st.ParseACPSessionUpdate("grok", "s1", map[string]any{"update": map[string]any{"sessionUpdate": "agent_thought_chunk", "thought": "plan"}})
	ev2, _ := st.ParseACPSessionUpdate("grok", "s1", map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "hi"}}})
	ev3, _ := st.ParseACPSessionUpdate("grok", "s1", map[string]any{"update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "mcp__linear__get", "rawInput": map[string]any{"id": "1"}}})
	ev4, _ := st.ParseACPSessionUpdate("grok", "s1", map[string]any{"update": map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "failed", "rawOutput": "401 Unauthorized"}})
	ev5, _ := st.ParseACPSessionUpdate("grok", "s1", map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": "done"}})
	if ev1[0].Type != "thought_delta" || ev2[0].Type != "text_delta" || ev3[0].Type != "tool_start" || len(ev4) != 2 || ev4[1].Type != "interactive_block" || ev5[0].Text != "\n\ndone" {
		t.Fatalf("unexpected stream sequence: %v %v %v %v %v", ev1, ev2, ev3, ev4, ev5)
	}

	_, notif := st.ParseACPSessionUpdate("grok", "s1", map[string]any{"update": map[string]any{
		"sessionUpdate":     "available_commands_update",
		"availableCommands": []any{map[string]any{"name": "goal", "description": "Set goal", "input": map[string]any{"hint": "<text>"}}},
	}})
	if len(notif.AvailableCommands) != 1 || notif.AvailableCommands[0].Name != "goal" || notif.AvailableCommands[0].ArgumentHint != "<text>" {
		t.Fatalf("commands update mismatch: %+v", notif)
	}

	prompt, ok := ParseACPPermissionRequest(json.RawMessage("7"), "7", map[string]any{
		"toolCall": map[string]any{"title": "bash", "rawInput": map[string]any{"command": "rm -rf /tmp/x"}},
		"options": []any{
			map[string]any{"optionId": "allow_once", "kind": "allow_once", "name": "Allow Once"},
			map[string]any{"optionId": "allow_always", "kind": "allow_always", "name": "Always Allow"},
			map[string]any{"optionId": "reject_once", "kind": "reject_once", "name": "Reject"},
		},
	})
	if !ok || prompt.Blocked.Kind != agent.BlockPermission || prompt.Blocked.ToolName != "bash" {
		t.Fatalf("permission parse mismatch: %+v", prompt)
	}
	b, _ := json.Marshal(BuildACPPermissionResponse(prompt, true, "Always Allow"))
	if !strings.Contains(string(b), `"optionId":"allow_always"`) {
		t.Fatalf("expected allow_always selection, got %s", b)
	}
	b2, _ := json.Marshal(BuildACPPermissionResponse(prompt, false, "cancel"))
	if !strings.Contains(string(b2), `"outcome":"cancelled"`) {
		t.Fatalf("expected cancelled outcome, got %s", b2)
	}
	bDenyMismatch, _ := json.Marshal(BuildACPPermissionResponse(prompt, false, "Allow Once"))
	if !strings.Contains(string(bDenyMismatch), `"optionId":"reject_once"`) {
		t.Fatalf("expected deny with allow label to pick reject_once, got %s", bDenyMismatch)
	}
	bAllowMismatch, _ := json.Marshal(BuildACPPermissionResponse(prompt, true, "Reject"))
	if !strings.Contains(string(bAllowMismatch), `"optionId":"allow_once"`) {
		t.Fatalf("expected allow with reject label to pick allow_once, got %s", bAllowMismatch)
	}
}
