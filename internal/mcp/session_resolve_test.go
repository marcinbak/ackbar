package mcp

import (
	"testing"
)

func TestSessionResolve_MultiPassRanking(t *testing.T) {
	uuidStr := "11111111-2222-3333-4444-555555555555"
	sessions := []map[string]any{
		{
			"id":        "claude-code:hostA:" + uuidStr,
			"native_id": "worker-A",
		},
		{
			"id":        "claude-code:hostB:" + uuidStr,
			"native_id": "worker-B",
		},
		{
			"id":        "worker-alpha",
			"native_id": "1",
		},
	}

	// 1. Exact match on hostB beats earlier suffix match on hostA
	match := resolveSessionFromList(sessions, "claude-code:hostB:"+uuidStr)
	if match == nil || getString(match, "id") != "claude-code:hostB:"+uuidStr {
		t.Fatalf("expected exact match on hostB, got: %v", match)
	}

	// 2. "worker-alpha-1" does NOT match native_id == "1"
	noMatch := resolveSessionFromList(sessions, "worker-alpha-1")
	if noMatch != nil {
		t.Fatalf("expected nil for worker-alpha-1, got match: %v", noMatch)
	}

	// 3. Suffix match on bare UUID resolves to first session matching suffix
	uuidMatch := resolveSessionFromList(sessions, uuidStr)
	if uuidMatch == nil || getString(uuidMatch, "id") != "claude-code:hostA:"+uuidStr {
		t.Fatalf("expected bare UUID match to resolve hostA, got: %v", uuidMatch)
	}
}
