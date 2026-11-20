package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ackbar/internal/version"
)

func parseSSEUpdateEvents(t *testing.T, body string) []UpdateProgressEvent {
	t.Helper()
	var events []UpdateProgressEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" {
			continue
		}
		var evt UpdateProgressEvent
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			t.Fatalf("failed to unmarshal SSE event %q: %v", raw, err)
		}
		events = append(events, evt)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	return events
}

func newUpdateTestServer(t *testing.T) (*Server, *DB) {
	t.Helper()
	db, err := InitDB(filepath.Join(t.TempDir(), "update_test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := NewServer(db)
	srv.SetHostIdentity("macbook", "MacBook Pro")
	return srv, db
}

func TestHandleUpdate_StreamDeduplicatesPlatformsAndCompletesBeforeRestart(t *testing.T) {
	srv, db := newUpdateTestServer(t)
	_ = db.SaveHost(&HostRecord{Name: "legion", URL: "http://127.0.0.1:59981"})
	_ = db.SaveHost(&HostRecord{Name: "worker2", URL: "http://127.0.0.1:59982"})
	_ = db.SaveHost(&HostRecord{Name: "macmini", URL: "http://127.0.0.1:59983"})
	_ = db.SaveHost(&HostRecord{Name: "uptodate-box", URL: "http://127.0.0.1:59984"})
	_ = db.SaveHost(&HostRecord{Name: "offline-box", URL: "http://127.0.0.1:59985"})

	srv.updateHostHealth("legion", true, "20261101.01", "Legion", 5)
	srv.updateHostHealth("worker2", true, "20261101.02", "Worker 2", 8)
	srv.updateHostHealth("macmini", true, "20261101.03", "Mac Mini", 10)
	srv.updateHostHealth("uptodate-box", true, "20991120.01", "Up To Date", 4)
	srv.updateHostHealth("offline-box", false, "20261101.01", "Offline", 0)

	srv.checkLatestReleaseFn = func(ctx context.Context, force bool) (*version.ReleaseInfo, error) {
		return &version.ReleaseInfo{Version: "20991120.01", IsNewer: true}, nil
	}
	srv.updateLocalFn = func(ctx context.Context) (*version.UpdateResult, error) {
		return &version.UpdateResult{
			PreviousVersion: version.Version,
			NewVersion:      "20991120.01",
			Method:          "standalone",
			Message:         "Local upgraded to v20991120.01",
		}, nil
	}
	srv.detectRemotePlatformFn = func(host *HostRecord) (string, string, error) {
		if host.Name == "macmini" {
			return "darwin", "arm64", nil
		}
		return "linux", "amd64", nil
	}

	var prepMu sync.Mutex
	prepCalls := map[string]int{}
	srv.preparePlatformBinariesFn = func(ctx context.Context, targetVer, srcDir, goos, goarch, destDir string, onDetail func(string)) (bool, string) {
		prepMu.Lock()
		prepCalls[goos+"/"+goarch]++
		prepMu.Unlock()
		if onDetail != nil {
			onDetail("compiling ackbard")
		}
		return true, "compiled"
	}
	srv.deployRemoteHostFn = func(host *HostRecord, targetVer, goos, goarch, preparedDir string, binariesPrepared bool, onDetail func(string)) (map[string]interface{}, int, error) {
		if onDetail != nil {
			onDetail("uploading binaries")
			onDetail("restarting ackbard")
			onDetail("verifying v" + targetVer)
		}
		return map[string]interface{}{
			"status":  "success",
			"host":    host.Name,
			"version": targetVer,
			"message": fmt.Sprintf("Upgraded %s to v%s", host.Name, targetVer),
		}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/update?stream=true", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()

	var bodyAtRestart string
	var restartCalled atomic.Bool
	srv.restartDaemonFn = func() {
		bodyAtRestart = rec.Body.String()
		restartCalled.Store(true)
	}

	srv.handleUpdate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream Content-Type, got %q", ct)
	}

	if prepCalls["linux/amd64"] != 1 {
		t.Errorf("expected linux/amd64 prepare called once (deduplicated across 2 hosts), got %d", prepCalls["linux/amd64"])
	}
	if prepCalls["darwin/arm64"] != 1 {
		t.Errorf("expected darwin/arm64 prepare called once, got %d", prepCalls["darwin/arm64"])
	}

	events := parseSSEUpdateEvents(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("expected non-empty SSE event stream")
	}

	// 1 local + 2 platform builds (linux/amd64, darwin/arm64) + 3 outdated hosts (legion, worker2, macmini) = 6
	const wantTotal = 6
	prevCompleted := 0
	var sawComplete bool
	var sawOffline, sawUpToDate bool
	for i, evt := range events {
		if evt.CompletedSteps < prevCompleted {
			t.Fatalf("event %d completed_steps decreased: %d < %d", i, evt.CompletedSteps, prevCompleted)
		}
		prevCompleted = evt.CompletedSteps
		if evt.StepID != "preflight" && evt.TotalSteps != wantTotal {
			t.Errorf("event %d (%s/%s) expected total_steps=%d, got %d", i, evt.Type, evt.StepID, wantTotal, evt.TotalSteps)
		}
		if evt.Status == "skipped_offline" && evt.Host == "offline-box" {
			sawOffline = true
		}
		if evt.Status == "up_to_date" && evt.Host == "uptodate-box" {
			sawUpToDate = true
		}
		if evt.Type == "complete" {
			sawComplete = true
			if evt.CompletedSteps != wantTotal {
				t.Errorf("expected final completed_steps=%d, got %d", wantTotal, evt.CompletedSteps)
			}
			if gotVer, _ := evt.Result["new_version"].(string); gotVer != "20991120.01" {
				t.Errorf("expected complete result new_version=20991120.01, got %v", evt.Result["new_version"])
			}
		}
	}
	if !sawComplete || !sawOffline || !sawUpToDate {
		t.Fatalf("missing expected events: complete=%v offline=%v uptodate=%v", sawComplete, sawOffline, sawUpToDate)
	}
	if !restartCalled.Load() {
		t.Fatal("expected restartDaemonFn to be invoked")
	}
	restartEvents := parseSSEUpdateEvents(t, bodyAtRestart)
	if len(restartEvents) == 0 || restartEvents[len(restartEvents)-1].Type != "complete" {
		t.Fatalf("expected 'complete' SSE event flushed BEFORE restartDaemonFn executed; saw %d events at restart", len(restartEvents))
	}
}

func TestHandleUpdate_LegacyJSONBackwardCompatibility(t *testing.T) {
	srv, db := newUpdateTestServer(t)
	_ = db.SaveHost(&HostRecord{Name: "macmini", URL: "http://127.0.0.1:59986"})
	srv.updateHostHealth("macmini", true, "20261101.01", "Mac Mini", 5)

	srv.checkLatestReleaseFn = func(ctx context.Context, force bool) (*version.ReleaseInfo, error) {
		return &version.ReleaseInfo{Version: "20991120.02", IsNewer: true}, nil
	}
	srv.updateLocalFn = func(ctx context.Context) (*version.UpdateResult, error) {
		return &version.UpdateResult{PreviousVersion: version.Version, NewVersion: "20991120.02", Method: "standalone", Message: "Updated local"}, nil
	}
	srv.detectRemotePlatformFn = func(host *HostRecord) (string, string, error) {
		return "darwin", "arm64", nil
	}
	srv.preparePlatformBinariesFn = func(ctx context.Context, targetVer, srcDir, goos, goarch, destDir string, onDetail func(string)) (bool, string) {
		return true, "downloaded"
	}
	srv.deployRemoteHostFn = func(host *HostRecord, targetVer, goos, goarch, preparedDir string, binariesPrepared bool, onDetail func(string)) (map[string]interface{}, int, error) {
		return map[string]interface{}{"status": "success", "host": host.Name, "version": targetVer, "message": "ok"}, http.StatusOK, nil
	}
	var restarted atomic.Bool
	srv.restartDaemonFn = func() { restarted.Store(true) }

	req := httptest.NewRequest(http.MethodPost, "/v1/update", nil)
	rec := httptest.NewRecorder()
	srv.handleUpdate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected application/json Content-Type, got %q", ct)
	}
	var payload struct {
		NewVersion      string              `json:"new_version"`
		RestartingLocal bool                `json:"restarting_local"`
		HostResults     []HostUpdateSummary `json:"host_results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if payload.NewVersion != "20991120.02" || !payload.RestartingLocal || len(payload.HostResults) != 1 || payload.HostResults[0].Host != "macmini" {
		t.Fatalf("unexpected legacy update response: %+v", payload)
	}
	if !restarted.Load() {
		t.Error("expected restartDaemonFn to be called")
	}
}

func TestHandleUpdate_StreamDeferredRestartWhileTurnActive(t *testing.T) {
	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nsleep 0.3\necho '{\"type\":\"result\",\"result\":\"done\",\"is_error\":false}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	srv, db := newUpdateTestServer(t)
	sess := &Session{ID: "claude-code:local:u1", Agent: "claude-code", Host: "local", NativeID: "u1", Cwd: tmpDir, EngineType: EngineHeadless, State: StateIdle}
	_ = db.SaveSession(sess)
	if err := srv.headless.RunTurn(context.Background(), sess, "Hold restart"); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	srv.checkLatestReleaseFn = func(ctx context.Context, force bool) (*version.ReleaseInfo, error) {
		return &version.ReleaseInfo{Version: "20991120.03", IsNewer: true}, nil
	}
	srv.updateLocalFn = func(ctx context.Context) (*version.UpdateResult, error) {
		return &version.UpdateResult{PreviousVersion: version.Version, NewVersion: "20991120.03", Method: "standalone", Message: "ok"}, nil
	}
	restarted := make(chan struct{}, 1)
	srv.restartDaemonFn = func() { restarted <- struct{}{} }

	req := httptest.NewRequest(http.MethodPost, "/v1/update?local_only=true&stream=true", nil)
	rec := httptest.NewRecorder()
	srv.handleUpdate(rec, req)

	select {
	case <-restarted:
		t.Fatal("restartDaemonFn must not fire while headless turn is still active")
	default:
	}
	events := parseSSEUpdateEvents(t, rec.Body.String())
	last := events[len(events)-1]
	if last.Type != "complete" || last.Result["restart_deferred"] != true || last.Result["restarting_local"] != false {
		t.Fatalf("expected deferred complete event, got %+v", last)
	}
	<-restarted
}

func TestHandleHostUpdate_StreamAndLegacy(t *testing.T) {
	srv, db := newUpdateTestServer(t)
	_ = db.SaveHost(&HostRecord{Name: "macmini", URL: "http://127.0.0.1:59987"})
	srv.checkLatestReleaseFn = func(ctx context.Context, force bool) (*version.ReleaseInfo, error) {
		return &version.ReleaseInfo{Version: "20991120.04", IsNewer: true}, nil
	}
	srv.detectRemotePlatformFn = func(host *HostRecord) (string, string, error) {
		return "darwin", "arm64", nil
	}
	srv.preparePlatformBinariesFn = func(ctx context.Context, targetVer, srcDir, goos, goarch, destDir string, onDetail func(string)) (bool, string) {
		if onDetail != nil {
			onDetail("downloading release tarball")
		}
		return true, "downloaded"
	}
	srv.deployRemoteHostFn = func(host *HostRecord, targetVer, goos, goarch, preparedDir string, binariesPrepared bool, onDetail func(string)) (map[string]interface{}, int, error) {
		if onDetail != nil {
			onDetail("uploading binaries")
			onDetail("restarting ackbard")
		}
		return map[string]interface{}{"status": "success", "host": host.Name, "version": targetVer, "message": "upgraded"}, http.StatusOK, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/hosts/update?stream=true", bytes.NewBufferString(`{"name":"macmini"}`))
	rec := httptest.NewRecorder()
	srv.handleHostUpdate(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected 200 SSE response, got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	events := parseSSEUpdateEvents(t, rec.Body.String())
	last := events[len(events)-1]
	if last.Type != "complete" || last.CompletedSteps != 2 || last.TotalSteps != 2 {
		t.Fatalf("expected final complete event with 2/2 steps, got %+v", last)
	}
	if last.Result["status"] != "success" || last.Result["version"] != "20991120.04" {
		t.Fatalf("unexpected single-host complete result: %+v", last.Result)
	}

	// Non-streaming JSON mode
	reqJSON := httptest.NewRequest(http.MethodPost, "/v1/hosts/update", bytes.NewBufferString(`{"name":"macmini"}`))
	recJSON := httptest.NewRecorder()
	srv.handleHostUpdate(recJSON, reqJSON)
	if recJSON.Code != http.StatusOK || !strings.Contains(recJSON.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected 200 JSON response, got %d %q", recJSON.Code, recJSON.Header().Get("Content-Type"))
	}
}
