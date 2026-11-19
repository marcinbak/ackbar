package antigravity

import (
	"encoding/json"
	"strings"

	"ackbar/internal/agent"
)

type streamParser struct {
	sessionID               string
	agStepText              map[int]string
	agHadToolSinceLastText  bool
	lastTextBuilder         strings.Builder
	hasEmittedAssistantText bool
	errTracker              turnErrorTracker
	toolInputs              map[string]map[string]any
	emittedAuthBlock        bool
}

func (*Plugin) NewStreamParser(sessionID string) agent.StreamParser {
	return &streamParser{
		sessionID:  sessionID,
		agStepText: make(map[int]string),
		toolInputs: make(map[string]map[string]any),
	}
}

func (p *Plugin) ParseStreamEvent(line []byte) ([]agent.ChatStreamEvent, error) {
	evts, _, err := p.NewStreamParser("").ParseStreamLine(line)
	return evts, err
}

func (p *streamParser) resetTurnState() {
	p.lastTextBuilder.Reset()
	p.errTracker.clear()
	p.emittedAuthBlock = false
	clear(p.agStepText)
	p.agHadToolSinceLastText = false
	p.hasEmittedAssistantText = false
	clear(p.toolInputs)
}

func (p *streamParser) ParseStreamLine(line []byte) ([]agent.ChatStreamEvent, bool, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil || raw == nil {
		return nil, false, nil
	}
	agyEvt, _ := raw["event"].(string)
	if agyEvt == "" {
		if t, _ := raw["type"].(string); t == "init" {
			convID, _ := raw["conversation_id"].(string)
			model, _ := raw["model"].(string)
			return []agent.ChatStreamEvent{{SessionID: p.sessionID, Type: "session_init", NativeID: convID, Model: model}}, true, nil
		}
		return nil, false, nil
	}

	switch agyEvt {
	case "init":
		convID, _ := raw["conversation_id"].(string)
		model, _ := raw["model"].(string)
		if model == "" {
			if initObj, ok := raw["init"].(map[string]interface{}); ok {
				model, _ = initObj["model"].(string)
			}
		}
		if convID != "" {
			return []agent.ChatStreamEvent{{SessionID: p.sessionID, Type: "session_init", NativeID: convID, Model: model}}, true, nil
		}
		if model != "" {
			return []agent.ChatStreamEvent{{SessionID: p.sessionID, Type: "model_detected", Model: model}}, true, nil
		}
		return nil, true, nil

	case "step_update":
		su, ok := raw["step_update"].(map[string]interface{})
		if !ok {
			return nil, true, nil
		}
		return p.parseStepUpdate(su), true, nil

	case "result":
		res, ok := raw["result"].(map[string]interface{})
		if !ok {
			p.resetTurnState()
			return nil, true, nil
		}
		status, _ := res["status"].(string)
		respText, _ := res["response"].(string)
		errVal, _ := res["error"].(string)
		if strings.EqualFold(status, "ERROR") || strings.TrimSpace(errVal) != "" || p.errTracker.hasPending() {
			rawErr := strings.TrimSpace(errVal)
			if rawErr == "" && strings.EqualFold(status, "ERROR") {
				rawErr = strings.TrimSpace(respText)
			}
			errText := p.errTracker.resolveFinal(rawErr)
			p.resetTurnState()
			return []agent.ChatStreamEvent{
				{SessionID: p.sessionID, Type: "error", Text: errText, IsError: true},
				{SessionID: p.sessionID, Type: "turn_complete", Text: errText, IsError: true},
			}, true, nil
		}
		p.resetTurnState()
		return []agent.ChatStreamEvent{{SessionID: p.sessionID, Type: "turn_complete", Text: respText}}, true, nil
	}
	return nil, false, nil
}
