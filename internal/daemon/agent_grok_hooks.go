package daemon

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ackbar/internal/agentutil"
)

func FormatToolDisplay(name string, input interface{}) string {
	return agentutil.FormatToolDisplay(name, input)
}

func WriteGrokHooksFile(grokDir, accountName string) error {
	hooksDir := filepath.Join(grokDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0700); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}
	_ = os.Chmod(grokDir, 0700)
	targetFile := filepath.Join(hooksDir, "ackbar.json")
	hookURL := "http://127.0.0.1:7777/v1/hooks/grok"
	if accountName != "" && accountName != "default" {
		hookURL += "?account=" + url.QueryEscape(accountName)
	}

	root := make(map[string]interface{})
	if data, err := os.ReadFile(targetFile); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &root)
	}

	hooksMap, _ := root["hooks"].(map[string]interface{})
	if hooksMap == nil {
		hooksMap = make(map[string]interface{})
	}

	events := []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
		"PostToolUseFailure", "PermissionDenied", "Notification",
		"SubagentStart", "SubagentStop", "Stop", "StopCancelled",
		"StopFailure", "SessionEnd",
	}

	for _, evt := range events {
		var filteredEntries []interface{}
		if existing, ok := hooksMap[evt].([]interface{}); ok {
			for _, entry := range existing {
				entryMap, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				hooksList, ok := entryMap["hooks"].([]interface{})
				if !ok {
					filteredEntries = append(filteredEntries, entry)
					continue
				}
				var keepHooks []interface{}
				for _, h := range hooksList {
					hMap, ok := h.(map[string]interface{})
					if !ok {
						keepHooks = append(keepHooks, h)
						continue
					}
					u, _ := hMap["url"].(string)
					c, _ := hMap["command"].(string)
					if strings.Contains(u, "127.0.0.1:7777/v1/hooks/grok") ||
						strings.Contains(u, "localhost:7777/v1/hooks/grok") ||
						strings.Contains(c, "ackbar-hook") {
						continue
					}
					keepHooks = append(keepHooks, h)
				}
				if len(keepHooks) > 0 {
					entryMap["hooks"] = keepHooks
					filteredEntries = append(filteredEntries, entryMap)
				}
			}
		}
		ackEntry := map[string]interface{}{
			"hooks": []map[string]interface{}{
				{
					"type":    "http",
					"url":     hookURL,
					"timeout": 5,
				},
			},
		}
		filteredEntries = append(filteredEntries, ackEntry)
		hooksMap[evt] = filteredEntries
	}
	root["hooks"] = hooksMap

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize grok hooks: %w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(hooksDir, ".ackbar.json.tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp hooks file: %w", err)
	}
	tmpName := tmpFile.Name()
	_ = tmpFile.Chmod(0600)
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to write temp hooks file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to close temp hooks file: %w", err)
	}
	if err := os.Rename(tmpName, targetFile); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to rename temp hooks file: %w", err)
	}
	return nil
}
