package agentutil

import (
	"context"
	"encoding/json"
	"fmt"

	"ackbar/internal/agent"
)

type ACPBootstrapInput struct {
	Cwd        string
	SessionID  string
	IsExisting bool
	Env        []string
	SelectAuth func(methods []agent.ACPAuthMethod, env []string) (methodID string, meta map[string]any, ok bool)
}

type ACPBootstrapResult struct {
	SessionID         string
	AdoptedNewID      bool
	AuthMethods       []agent.ACPAuthMethod
	CurrentModel      string
	Models            any
	ConfigOptions     any
	Modes             any
	AvailableCommands any
	InitRaw           map[string]any
	SessionRaw        map[string]any
}

func BootstrapACPSession(ctx context.Context, conn *ACPConn, in ACPBootstrapInput) (*ACPBootstrapResult, error) {
	if conn == nil {
		return nil, fmt.Errorf("nil acp connection")
	}
	initRawBytes, rpcErr, err := conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "ackbar", "version": "1.0"},
		"clientCapabilities": map[string]any{},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if rpcErr != nil {
		return nil, fmt.Errorf("initialize: %w", rpcErr)
	}
	var initMap map[string]any
	if len(initRawBytes) > 0 {
		_ = json.Unmarshal(initRawBytes, &initMap)
	}

	authMethods := parseACPAuthMethods(initMap["authMethods"])
	if len(authMethods) > 0 && in.SelectAuth != nil {
		if methodID, meta, ok := in.SelectAuth(authMethods, in.Env); ok && methodID != "" {
			authParams := map[string]any{"methodId": methodID}
			if len(meta) > 0 {
				authParams["_meta"] = meta
			}
			_, authRPCErr, authErr := conn.Call(ctx, "authenticate", authParams)
			if authErr != nil {
				return nil, fmt.Errorf("authenticate: %w", authErr)
			}
			if authRPCErr != nil && authRPCErr.Code != -32601 {
				return nil, fmt.Errorf("authenticate: %w", authRPCErr)
			}
		}
	}

	callNew := func() (json.RawMessage, *ACPRPCError, error) {
		p := map[string]any{"cwd": in.Cwd, "mcpServers": []any{}}
		if in.SessionID != "" {
			p["sessionId"] = in.SessionID
		}
		return conn.Call(ctx, "session/new", p)
	}

	var sessRawBytes json.RawMessage
	var sessRPCErr *ACPRPCError
	var sessErr error
	if in.IsExisting && in.SessionID != "" {
		sessRawBytes, sessRPCErr, sessErr = conn.Call(ctx, "session/load", map[string]any{
			"sessionId":  in.SessionID,
			"cwd":        in.Cwd,
			"mcpServers": []any{},
		})
		if sessErr == nil && sessRPCErr != nil {
			sessRawBytes, sessRPCErr, sessErr = callNew()
		}
	} else {
		sessRawBytes, sessRPCErr, sessErr = callNew()
	}
	if sessErr != nil {
		return nil, fmt.Errorf("session setup: %w", sessErr)
	}
	if sessRPCErr != nil {
		return nil, fmt.Errorf("session setup: %w", sessRPCErr)
	}

	var sessMap map[string]any
	if len(sessRawBytes) > 0 {
		_ = json.Unmarshal(sessRawBytes, &sessMap)
	}
	resSessID := acpFirstStr(sessMap, "sessionId", "session_id")
	adopted := resSessID != "" && in.SessionID != "" && resSessID != in.SessionID
	if resSessID == "" {
		resSessID = in.SessionID
	}

	return &ACPBootstrapResult{
		SessionID:         resSessID,
		AdoptedNewID:      adopted,
		AuthMethods:       authMethods,
		CurrentModel:      extractACPModelFromMap(sessMap),
		Models:            firstNonNilMapVal(sessMap, "models", initMap),
		ConfigOptions:     sessMap["configOptions"],
		Modes:             sessMap["modes"],
		AvailableCommands: firstNonNilMapVal(sessMap, "availableCommands", initMap),
		InitRaw:           initMap,
		SessionRaw:        sessMap,
	}, nil
}

func SwitchACPModel(ctx context.Context, conn *ACPConn, sessionID, modelID, effort string) error {
	if conn == nil {
		return fmt.Errorf("nil acp connection")
	}
	params := map[string]any{"sessionId": sessionID, "modelId": modelID}
	if effort != "" {
		params["_meta"] = map[string]any{"reasoningEffort": effort}
	}
	_, rpcErr, err := conn.Call(ctx, "session/set_model", params)
	if err != nil || rpcErr == nil {
		return err
	}
	if rpcErr.Code != -32601 {
		return rpcErr
	}
	if _, optRPCErr, optErr := conn.Call(ctx, "session/set_config_option", map[string]any{
		"sessionId": sessionID, "configId": "model", "value": modelID,
	}); optErr != nil {
		return optErr
	} else if optRPCErr != nil {
		return optRPCErr
	}
	if effort != "" {
		if _, effRPCErr, effErr := conn.Call(ctx, "session/set_config_option", map[string]any{
			"sessionId": sessionID, "configId": "reasoning_effort", "value": effort,
		}); effErr != nil {
			return effErr
		} else if effRPCErr != nil {
			return effRPCErr
		}
	}
	return nil
}

func SwitchACPMode(ctx context.Context, conn *ACPConn, sessionID, modeID string) error {
	if conn == nil {
		return fmt.Errorf("nil acp connection")
	}
	_, rpcErr, err := conn.Call(ctx, "session/set_mode", map[string]any{"sessionId": sessionID, "modeId": modeID})
	if err != nil || rpcErr == nil {
		return err
	}
	if rpcErr.Code != -32601 {
		return rpcErr
	}
	_, optRPCErr, optErr := conn.Call(ctx, "session/set_config_option", map[string]any{
		"sessionId": sessionID, "configId": "mode", "value": modeID,
	})
	if optErr != nil {
		return optErr
	}
	if optRPCErr != nil {
		return optRPCErr
	}
	return nil
}
