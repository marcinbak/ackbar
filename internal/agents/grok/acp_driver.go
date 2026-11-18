package grok

import (
	"fmt"
	"math"
	"strings"

	"ackbar/internal/agent"
	"ackbar/internal/agentutil"
)

const maxSynthesizedQueueTasks = 128

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(env[i], prefix))
		}
	}
	return ""
}

func (*Plugin) SelectACPAuth(methods []agent.ACPAuthMethod, env []string) (string, map[string]any, bool) {
	if len(methods) == 0 {
		return "", nil, false
	}
	meta := map[string]any{"headless": true}
	if envValue(env, "XAI_API_KEY") != "" {
		for _, m := range methods {
			if m.ID == "xai.api_key" || m.ID == "api_key" {
				return m.ID, meta, true
			}
		}
	}
	for _, m := range methods {
		if m.ID == "cached_token" {
			return m.ID, meta, true
		}
	}
	return methods[0].ID, meta, true
}

func (*Plugin) HandleACPNotification(homeDir string, env []string, method string, params map[string]any) agent.ACPNotificationResult {
	var res agent.ACPNotificationResult
	if params == nil {
		return res
	}
	grokDir := resolveGrokDir(homeDir, env)

	switch method {
	case "_x.ai/models/update", "x.ai/models/update":
		if models := parseACPModelsPayload(params["models"]); len(models) > 0 {
			_ = saveACPModelsCache(grokDir, models)
		}
		for _, k := range []string{"currentModelId", "modelId", "current_model_id"} {
			if m, ok := params[k].(string); ok && normalizeModelID(m) != "" {
				res.DetectedModel = normalizeModelID(m)
				break
			}
		}
		if res.DetectedModel == "" {
			if mMap, ok := params["models"].(map[string]any); ok {
				for _, k := range []string{"currentModelId", "modelId", "current_model_id"} {
					if m, ok := mMap[k].(string); ok && normalizeModelID(m) != "" {
						res.DetectedModel = normalizeModelID(m)
						break
					}
				}
			}
		}

	case "session/update", "_x.ai/session/update":
		var st agentutil.ACPStreamState
		_, parsed := st.ParseACPSessionUpdate("grok", "", params)
		if parsed != nil {
			res.DetectedModel = normalizeModelID(parsed.DetectedModel)
			if len(parsed.AvailableCommands) > 0 {
				res.AvailableCommands = parsed.AvailableCommands
				_ = saveACPCommandsCache(grokDir, parsed.AvailableCommands)
			}
		}

	case "_x.ai/queue/changed", "x.ai/queue/changed", "_x.ai/background_tasks/changed":
		if evt := normalizeGrokQueueEvent(params); evt != nil {
			res.SystemEvents = append(res.SystemEvents, evt)
		}

	case "_x.ai/task_started":
		tid := firstMapStr(params, "task_id", "taskId", "id")
		if tid == "" {
			tid = "grok_bg_task"
		}
		res.SystemEvents = append(res.SystemEvents, map[string]any{
			"subtype": "task_started", "is_backgrounded": true, "task_id": tid,
		})

	case "_x.ai/task_notification":
		res.SystemEvents = append(res.SystemEvents, map[string]any{
			"subtype": "task_notification",
			"task_id": firstMapStr(params, "task_id", "taskId", "id"),
			"status":  firstMapStr(params, "status"),
			"summary": firstMapStr(params, "summary", "message"),
		})
	}
	return res
}

func (*Plugin) ACPRewindMethods() (listMethod, execMethod, cancelMethod string, supported bool) {
	return "_x.ai/rewind/points", "_x.ai/rewind/execute", "_x.ai/cancelRewind", true
}

func normalizeGrokQueueEvent(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	if rawTasks := agentutil.ParseInterfaceSlice(params["tasks"]); rawTasks != nil {
		return map[string]any{"subtype": "background_tasks_changed", "tasks": rawTasks}
	}
	for _, key := range []string{"count", "activeCount", "backgroundCount", "size"} {
		if num, ok := asNonNegInt(params[key]); ok {
			tasks := make([]any, 0, num)
			for i := 0; i < num; i++ {
				tasks = append(tasks, map[string]any{"task_id": fmt.Sprintf("grok_bg_%d", i), "status": "running"})
			}
			return map[string]any{"subtype": "background_tasks_changed", "tasks": tasks}
		}
	}
	if items := agentutil.ParseInterfaceSlice(params["items"]); items != nil {
		return map[string]any{"subtype": "background_tasks_changed", "tasks": items}
	}
	return nil
}

func asNonNegInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return 0, false
		}
		if n > float64(maxSynthesizedQueueTasks) {
			return maxSynthesizedQueueTasks, true
		}
		return int(n), true
	case int:
		if n >= 0 {
			if n > maxSynthesizedQueueTasks {
				return maxSynthesizedQueueTasks, true
			}
			return n, true
		}
	case int64:
		if n >= 0 {
			if n > maxSynthesizedQueueTasks {
				return maxSynthesizedQueueTasks, true
			}
			return int(n), true
		}
	}
	return 0, false
}
