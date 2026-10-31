package web_test

import (
	"io"
	"io/fs"
	"strings"
	"testing"

	"ackbar/web"
)

func TestGetFS(t *testing.T) {
	embeddedFS := web.GetFS()
	if embeddedFS == nil {
		t.Fatal("expected non-nil fs.FS from web.GetFS()")
	}

	requiredFiles := []string{
		"index.html",
		"style.css",
		"manifest.json",
		"app.js",
		"js/api.js",
		"js/app.js",
		"js/auth.js",
		"js/chat.js",
		"js/context-menu.js",
		"js/details.js",
		"js/modals.js",
		"js/palette.js",
		"js/sse.js",
		"js/state.js",
		"js/statusbar.js",
		"js/tabs.js",
		"js/tasks.js",
		"js/terminal.js",
		"js/tree.js",
		"js/utils.js",
		"icons/favicon-32.png",
		"icons/icon-192.png",
		"icons/icon-512.png",
		"icons/apple-touch-icon.png",
	}

	for _, path := range requiredFiles {
		t.Run(path, func(t *testing.T) {
			f, err := embeddedFS.Open(path)
			if err != nil {
				t.Fatalf("failed to open embedded file %s: %v", path, err)
			}
			defer f.Close()

			stat, err := f.Stat()
			if err != nil {
				t.Fatalf("failed to stat embedded file %s: %v", path, err)
			}
			if stat.IsDir() {
				t.Fatalf("expected %s to be a file, got directory", path)
			}
			if stat.Size() == 0 {
				t.Fatalf("embedded file %s is empty", path)
			}

			content, err := io.ReadAll(f)
			if err != nil {
				t.Fatalf("failed to read embedded file %s: %v", path, err)
			}
			if len(content) == 0 {
				t.Fatalf("read zero bytes from embedded file %s", path)
			}
		})
	}

	// Verify walking embedded FS
	entriesCount := 0
	err := fs.WalkDir(embeddedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			entriesCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk embeddedFS: %v", err)
	}

	if entriesCount < len(requiredFiles) {
		t.Fatalf("expected at least %d files, walked %d", len(requiredFiles), entriesCount)
	}
}

func TestChatDeduplication_JS(t *testing.T) {
	embeddedFS := web.GetFS()
	f, err := embeddedFS.Open("js/chat.js")
	if err != nil {
		t.Fatalf("failed to open js/chat.js: %v", err)
	}
	defer f.Close()

	contentBytes, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read js/chat.js: %v", err)
	}
	content := string(contentBytes)

	// Verify required functions and invariants are present
	if !strings.Contains(content, "normalizePromptText") {
		t.Errorf("expected js/chat.js to contain normalizePromptText")
	}
	if !strings.Contains(content, "tabObj.inFlightPrompt") {
		t.Errorf("expected js/chat.js to track inFlightPrompt on tabObj")
	}
	if !strings.Contains(content, "dataset.inFlight") {
		t.Errorf("expected js/chat.js to mark inFlight on optimistic element")
	}
	if !strings.Contains(content, "data.status === 'queued'") {
		t.Errorf("expected js/chat.js to handle queued prompt status")
	}
	if strings.Contains(content, "fullNorm.includes(evtNorm)") {
		t.Errorf("js/chat.js must not perform broad fullNorm.includes(evtNorm) substring matching across past messages")
	}
}

func TestMultiHostAgentSetup_JS(t *testing.T) {
	embeddedFS := web.GetFS()

	// 1. Verify js/api.js multi-host functions
	fAPI, err := embeddedFS.Open("js/api.js")
	if err != nil {
		t.Fatalf("failed to open js/api.js: %v", err)
	}
	defer fAPI.Close()

	apiBytes, err := io.ReadAll(fAPI)
	if err != nil {
		t.Fatalf("failed to read js/api.js: %v", err)
	}
	apiContent := string(apiBytes)

	for _, expected := range []string{
		"function getTargetHosts()",
		"async function fetchAllAgentStatuses()",
		"async function provisionAllHosts(",
		"fetchAllAgentStatuses,",
		"provisionAllHosts",
	} {
		if !strings.Contains(apiContent, expected) {
			t.Errorf("expected js/api.js to contain %q", expected)
		}
	}

	// 2. Verify js/tasks.js multi-host setup modal
	fTasks, err := embeddedFS.Open("js/tasks.js")
	if err != nil {
		t.Fatalf("failed to open js/tasks.js: %v", err)
	}
	defer fTasks.Close()

	tasksBytes, err := io.ReadAll(fTasks)
	if err != nil {
		t.Fatalf("failed to read js/tasks.js: %v", err)
	}
	tasksContent := string(tasksBytes)

	for _, expected := range []string{
		"fetchAllAgentStatuses",
		"provisionAllHosts",
		"mAgentHostTabs",
		"renderHostSections",
		"btn-configure-single-host",
	} {
		if !strings.Contains(tasksContent, expected) {
			t.Errorf("expected js/tasks.js to contain %q", expected)
		}
	}
}

func TestTabDeduplicationAndSafeMigration_JS(t *testing.T) {
	embeddedFS := web.GetFS()
	f, err := embeddedFS.Open("js/tabs.js")
	if err != nil {
		t.Fatalf("failed to open js/tabs.js: %v", err)
	}
	defer f.Close()

	contentBytes, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read js/tabs.js: %v", err)
	}
	content := string(contentBytes)

	for _, expected := range []string{
		"function isSameUnderlyingSession(",
		"function destroyTabResources(",
		"existingNewTab && existingNewTab !== tab",
		"destroyTabResources(existingNewTab)",
		"isSameUnderlyingSession(session, existingTab.session)",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("expected js/tabs.js to contain %q", expected)
		}
	}
}

func TestHeadlessThinkingBubblePersistence_JS(t *testing.T) {
	embeddedFS := web.GetFS()

	fChat, err := embeddedFS.Open("js/chat.js")
	if err != nil {
		t.Fatalf("failed to open js/chat.js: %v", err)
	}
	defer fChat.Close()

	chatBytes, err := io.ReadAll(fChat)
	if err != nil {
		t.Fatalf("failed to read js/chat.js: %v", err)
	}
	chatContent := string(chatBytes)

	for _, expected := range []string{
		"const hasLiveTurnMsg = Boolean(tabObj.activeTurnMsgEl && tabObj.activeTurnMsgEl.isConnected);",
		"const hasLiveActivity = Boolean(tabObj.activeInStreamActivityEl && tabObj.activeInStreamActivityEl.isConnected);",
		"if (!hasLiveTurnMsg && !hasLiveActivity) {",
		"} else if (working) {",
		"if (!liveTurnMsgEl && !liveActivityEl && (tabObj.sending || isSessionWorking(tabObj.session))) {",
		"} else if (!liveTurnMsgEl && (tabObj.sending || isSessionWorking(tabObj.session))) {",
	} {
		if !strings.Contains(chatContent, expected) {
			t.Errorf("expected js/chat.js to contain %q", expected)
		}
	}

	fTabs, err := embeddedFS.Open("js/tabs.js")
	if err != nil {
		t.Fatalf("failed to open js/tabs.js: %v", err)
	}
	defer fTabs.Close()

	tabsBytes, err := io.ReadAll(fTabs)
	if err != nil {
		t.Fatalf("failed to read js/tabs.js: %v", err)
	}
	tabsContent := string(tabsBytes)

	for _, expected := range []string{
		"setChatSending,",
		"isSessionWorking,",
		"if (tab.sending || isSessionWorking(currentSess)) {",
		"setChatSending(tab, true);",
		"loadChatTranscript(tab);",
	} {
		if !strings.Contains(tabsContent, expected) {
			t.Errorf("expected js/tabs.js to contain %q", expected)
		}
	}
}
