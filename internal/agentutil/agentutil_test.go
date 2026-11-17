package agentutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFS_WriteAtomicReadHeadTailExpandTilde(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")

	if err := WriteFileAtomic(target, []byte("hello"), 0); err != nil {
		t.Fatalf("WriteFileAtomic default perm: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}

	if err := WriteFileAtomic(target, []byte("0123456789"), 0o640); err != nil {
		t.Fatalf("WriteFileAtomic overwrite: %v", err)
	}
	info, _ = os.Stat(target)
	if info.Mode().Perm() != 0o640 {
		t.Errorf("perm after overwrite = %o, want 0640", info.Mode().Perm())
	}

	head, err := ReadHead(target, 4)
	if err != nil || string(head) != "0123" {
		t.Errorf("ReadHead = (%q, %v), want 0123", head, err)
	}
	tail, err := ReadTail(target, 3)
	if err != nil || string(tail) != "789" {
		t.Errorf("ReadTail = (%q, %v), want 789", tail, err)
	}
	if zero, err := ReadTail(target, 0); err != nil || len(zero) != 0 {
		t.Errorf("ReadTail(0) = (%q, %v)", zero, err)
	}

	missingParent := filepath.Join(dir, "nope", "file.txt")
	if err := WriteFileAtomic(missingParent, []byte("x"), 0o600); err == nil {
		t.Fatal("expected error writing to missing parent dir")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file %q", e.Name())
		}
	}

	home := RealUserHome()
	if home == "" {
		t.Fatal("RealUserHome returned empty string")
	}
	if got := ExpandTilde("~/sub/dir"); got != filepath.Join(home, "sub", "dir") {
		t.Errorf("ExpandTilde = %q", got)
	}
	if got := ExpandTilde("/abs/path"); got != "/abs/path" {
		t.Errorf("ExpandTilde abs = %q", got)
	}
	if !FileExists(target) || DirExists(target) || !DirExists(dir) || FileExists(dir) {
		t.Error("FileExists/DirExists mismatch")
	}
}

func TestExec_IDsAndText(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) || IsProcessAlive(-1) {
		t.Error("IsProcessAlive mismatch")
	}
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "custom-agent-bin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBinary("custom-agent-bin", binDir)
	if err != nil || got != binPath {
		t.Errorf("ResolveBinary = (%q, %v), want %q", got, err, binPath)
	}
	if _, err := LookPathInStandardDirs("definitely-missing-ackbar-bin-xyz"); err == nil {
		t.Error("expected ErrNotFound for missing binary")
	}

	if !IsAckbarInternalProcess("ackbar-hook --agent claude") || IsAckbarInternalProcess("claude --resume 123") {
		t.Error("IsAckbarInternalProcess mismatch")
	}
	if !CheckIgnoredCLIArgs("claude mcp list", func(_, b string) bool { return b == "claude" }, map[string]bool{"mcp": true}, nil) {
		t.Error("expected subcommand 'mcp' to be ignored")
	}
	if CheckIgnoredCLIArgs("claude --resume abc", func(_, b string) bool { return b == "claude" }, map[string]bool{"mcp": true}, nil) {
		t.Error("expected --resume session not to be ignored")
	}

	uuid := "123e4567-e89b-12d3-a456-426614174000"
	if !IsUUID(uuid) || IsUUID("not-a-uuid") {
		t.Error("IsUUID mismatch")
	}
	if !IsSafeSessionID("sess_1:abc-2") || IsSafeSessionID("../etc") || IsSafeSessionID(".") {
		t.Error("IsSafeSessionID mismatch")
	}
	if !IsSafeGrokSessionID("grok-1.2:ok") || IsSafeGrokSessionID("-bad") || IsSafeGrokSessionID("a..b") {
		t.Error("IsSafeGrokSessionID mismatch")
	}
	if !IsSafeCLIArg("claude-opus-4-6") || IsSafeCLIArg("-bad") || IsSafeCLIArg("a;rm") {
		t.Error("IsSafeCLIArg mismatch")
	}
	if !IsRawSessionName(uuid) || !IsRawSessionName("ngl-android-23") || IsRawSessionName("Fix auth bug") {
		t.Error("IsRawSessionName mismatch")
	}
	if got := TruncateTitle("hi\nthere friend", 50); got != "hi there friend" {
		t.Errorf("TruncateTitle = %q", got)
	}

	cleaned := CleanCLIEnvironmentTags("<environment_context>noise</environment_context>\n\nHello")
	if cleaned != "Hello" {
		t.Errorf("CleanCLIEnvironmentTags = %q", cleaned)
	}
	if got := EnsureDoubleNewlineSeparation("hello", "world"); got != "\n\nworld" {
		t.Errorf("EnsureDoubleNewlineSeparation = %q", got)
	}
	if got := ExtractTailLines("a\nb\nc\n\n", 2); got != "b\nc" {
		t.Errorf("ExtractTailLines = %q", got)
	}
	if got := ExtractTailText([]byte("abcdef"), 3); got != "def" {
		t.Errorf("ExtractTailText = %q", got)
	}
}

func TestRateLimitParsing(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	rt, ok := ParseRateLimitResetTime("try again in 30m", now)
	if !ok || !rt.Equal(now.Add(31*time.Minute)) {
		t.Errorf("relative parse = (%v, %v)", rt, ok)
	}
	fb, ok := ParseRateLimitResetTime("unknown failure", now)
	if ok || !fb.Equal(now.Add(time.Hour)) {
		t.Errorf("fallback parse = (%v, %v)", fb, ok)
	}

	unixTS, matched := DetectDefaultRateLimit("You've hit your usage limit · resets in 10m")
	if !matched || unixTS <= time.Now().Unix() {
		t.Errorf("DetectDefaultRateLimit = (%d, %v)", unixTS, matched)
	}
	if _, matched := DetectDefaultRateLimit("all good"); matched {
		t.Error("expected unmatched rate limit")
	}
}
