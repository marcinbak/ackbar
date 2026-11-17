package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCapabilities_ValidateConfig(t *testing.T) {
	fullCaps := Capabilities{
		SupportsTmux:             true,
		SupportsHeadless:         true,
		HeadlessMode:             HeadlessModePersistent,
		SupportsModelSelection:   true,
		SupportsEffortLevel:      true,
		SupportsYoloMode:         true,
		SupportsSystemPrompt:     true,
		SupportsExtraDirs:        true,
		SupportsMultiAccount:     true,
		SupportedPermissionModes: []string{"bypassPermissions", "auto", "acceptEdits", "dontAsk", "plan"},
	}

	tests := []struct {
		name    string
		caps    Capabilities
		in      ConfigValidationInput
		wantErr bool
	}{
		{
			name: "persistent headless valid",
			caps: fullCaps,
			in: ConfigValidationInput{
				EngineType:     EngineHeadless,
				Model:          "claude-opus-4-6",
				Effort:         "high",
				PermissionMode: "plan",
				SystemPrompt:   "be concise",
				AccountID:      "work",
				ExtraDirs:      []string{"/tmp/extra"},
			},
		},
		{
			name: "acp headless valid",
			caps: Capabilities{SupportsHeadless: true, HeadlessMode: HeadlessModeACP},
			in:   ConfigValidationInput{EngineType: EngineHeadless},
		},
		{
			name: "oneshot headless valid",
			caps: Capabilities{SupportsHeadless: true, HeadlessMode: HeadlessModeOneShot},
			in:   ConfigValidationInput{EngineType: EngineHeadless},
		},
		{
			name:    "headless supported without mode fails",
			caps:    Capabilities{SupportsHeadless: true, HeadlessMode: HeadlessModeNone},
			in:      ConfigValidationInput{EngineType: EngineHeadless},
			wantErr: true,
		},
		{
			name:    "unsupported headless engine",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{EngineType: EngineHeadless},
			wantErr: true,
		},
		{
			name:    "unsupported tmux engine",
			caps:    Capabilities{SupportsHeadless: true, HeadlessMode: HeadlessModeACP},
			in:      ConfigValidationInput{EngineType: EngineTmux},
			wantErr: true,
		},
		{
			name:    "unsupported model",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{EngineType: EngineTmux, Model: "gpt-5"},
			wantErr: true,
		},
		{
			name:    "default model allowed without model support",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{EngineType: EngineTmux, Model: "default"},
			wantErr: false,
		},
		{
			name:    "unsupported effort",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{EngineType: EngineTmux, Effort: "high"},
			wantErr: true,
		},
		{
			name:    "invalid effort value",
			caps:    fullCaps,
			in:      ConfigValidationInput{Effort: "ultra"},
			wantErr: true,
		},
		{
			name:    "unsupported yolo",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{YoloMode: true},
			wantErr: true,
		},
		{
			name:    "disallowed permission mode",
			caps:    Capabilities{SupportsTmux: true, SupportedPermissionModes: []string{"plan", "auto"}},
			in:      ConfigValidationInput{PermissionMode: "dontAsk"},
			wantErr: true,
		},
		{
			name:    "permission mode fails closed when supported list empty",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{PermissionMode: "plan"},
			wantErr: true,
		},
		{
			name:    "default permission mode allowed when supported list empty",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{PermissionMode: "default"},
			wantErr: false,
		},
		{
			name:    "unsupported system prompt",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{SystemPrompt: "override"},
			wantErr: true,
		},
		{
			name:    "unsupported multi account",
			caps:    Capabilities{SupportsTmux: true},
			in:      ConfigValidationInput{AccountID: "work"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.caps.ValidateConfig(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateConfig() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestStateAndDTORoundTrips(t *testing.T) {
	states := map[State]string{
		StateUnknown: "unknown",
		StateWorking: "working",
		StateBlocked: "blocked",
		StateIdle:    "idle",
		StateEnded:   "ended",
		StateFailed:  "failed",
		State(99):    "unknown",
	}
	for s, want := range states {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", s, got, want)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	blk := &Blocked{
		Kind:       BlockAuth,
		Reason:     "sign in required",
		Since:      now,
		AuthTarget: "mcp:github",
		AuthKind:   "mcp",
		AuthServer: "github",
		AuthURL:    "https://example.com/auth",
		AuthHint:   "run auth",
		FlowID:     "flow-1",
	}
	metaRaw := MarshalBlockedMeta(blk)
	if metaRaw == "" {
		t.Fatal("expected non-empty blocked meta")
	}
	restored := &Blocked{}
	ApplyBlockedMeta(restored, metaRaw)
	if restored.AuthTarget != blk.AuthTarget || restored.FlowID != blk.FlowID || restored.AuthURL != blk.AuthURL {
		t.Fatalf("ApplyBlockedMeta mismatch: %+v", restored)
	}

	sess := Session{
		ID:         "claude-code:local:123",
		Agent:      "claude-code",
		State:      StateWorking,
		Blocked:    blk,
		EngineType: EngineHeadless,
		StartedAt:  now,
	}
	raw, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	var gotSess Session
	if err := json.Unmarshal(raw, &gotSess); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if gotSess.ID != sess.ID || gotSess.State != StateWorking || gotSess.Blocked == nil || gotSess.Blocked.AuthServer != "github" {
		t.Fatalf("session roundtrip mismatch: %+v", gotSess)
	}

	usage := TranscriptUsage{InputTokens: 10, OutputTokens: 20, TurnCount: 2, CostUSD: 0.05}
	uRaw, _ := json.Marshal(usage)
	var uGot TranscriptUsage
	if err := json.Unmarshal(uRaw, &uGot); err != nil || uGot != usage {
		t.Fatalf("usage roundtrip mismatch: %+v err=%v", uGot, err)
	}
}
