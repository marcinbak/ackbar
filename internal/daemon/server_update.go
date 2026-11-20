package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"ackbar/internal/version"
)

type UpdateProgressEvent struct {
	Type           string                 `json:"type"`
	StepID         string                 `json:"step_id,omitempty"`
	Phase          string                 `json:"phase,omitempty"`
	Host           string                 `json:"host,omitempty"`
	Label          string                 `json:"label,omitempty"`
	Detail         string                 `json:"detail,omitempty"`
	Status         string                 `json:"status,omitempty"`
	CompletedSteps int                    `json:"completed_steps"`
	TotalSteps     int                    `json:"total_steps"`
	StepElapsedMs  int64                  `json:"step_elapsed_ms,omitempty"`
	ElapsedMs      int64                  `json:"elapsed_ms"`
	Message        string                 `json:"message,omitempty"`
	Result         map[string]interface{} `json:"result,omitempty"`
}

type updateStreamWriter struct {
	w          http.ResponseWriter
	flusher    http.Flusher
	mu         sync.Mutex
	runStart   time.Time
	completed  int
	totalSteps int
}

type fleetUpdateOutcome struct {
	prevVersion, targetVersion, method, message string
	hostSummaries                               []HostUpdateSummary
	localRestartNeeded                          bool
}

func wantsUpdateStream(r *http.Request) bool {
	return r.URL.Query().Get("stream") == "true" || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

func newUpdateStreamWriter(w http.ResponseWriter, runStart time.Time) *updateStreamWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sw := &updateStreamWriter{w: w, runStart: runStart}
	if sw.flusher, _ = w.(http.Flusher); sw.flusher != nil {
		sw.flusher.Flush()
	}
	return sw
}

func (sw *updateStreamWriter) send(evt UpdateProgressEvent, inc bool) {
	if sw == nil {
		return
	}
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if inc {
		// Increment inside the stream mutex so completed_steps stays monotonic across concurrent host goroutines.
		sw.completed++
	}
	evt.CompletedSteps = sw.completed
	if evt.TotalSteps > 0 {
		sw.totalSteps = evt.TotalSteps
	} else if sw.totalSteps > 0 {
		evt.TotalSteps = sw.totalSteps
	}
	evt.ElapsedMs = time.Since(sw.runStart).Milliseconds()
	if b, err := json.Marshal(evt); err == nil {
		_, _ = fmt.Fprintf(sw.w, "data: %s\n\n", b)
		if sw.flusher != nil {
			sw.flusher.Flush()
		}
	}
}

func stepEvt(stepID, phase, host, label, detail, status string, start time.Time) UpdateProgressEvent {
	var stepMs int64
	if !start.IsZero() && (status != "running" || detail != "") {
		stepMs = time.Since(start).Milliseconds()
	}
	return UpdateProgressEvent{Type: "step", StepID: stepID, Phase: phase, Host: host, Label: label, Detail: detail, Status: status, StepElapsedMs: stepMs}
}

func writeUpdateJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	_ = json.NewEncoder(w).Encode(payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func respondUpdateError(w http.ResponseWriter, sw *updateStreamWriter, code int, msg string) {
	if sw != nil {
		sw.send(UpdateProgressEvent{Type: "error", Message: msg}, false)
		return
	}
	writeUpdateJSON(w, code, map[string]interface{}{"status": "error", "message": msg})
}

func (s *Server) runPlatformPrepareStep(ctx context.Context, sw *updateStreamWriter, targetVer, srcDir, goos, goarch string) (string, bool, func()) {
	plat := goos + "/" + goarch
	stepID, phase, runLabel, doneLabel := "download:"+plat, "downloading", fmt.Sprintf("Downloading release (%s)", plat), fmt.Sprintf("Downloaded release (%s)", plat)
	if srcDir != "" {
		stepID, phase, runLabel, doneLabel = "compile:"+plat, "compiling", fmt.Sprintf("Compiling binaries (%s)", plat), fmt.Sprintf("Compiled binaries (%s)", plat)
	}
	start := time.Now()
	sw.send(stepEvt(stepID, phase, "", runLabel, "", "running", start), false)

	platDir, tmpErr := os.MkdirTemp("", "ackbar-remote-update-*")
	prepFn := preparePlatformBinariesDefault
	if s.preparePlatformBinariesFn != nil {
		prepFn = s.preparePlatformBinariesFn
	}
	prepared, method := false, "fallback"
	if tmpErr == nil {
		prepared, method = prepFn(ctx, targetVer, srcDir, goos, goarch, platDir, func(detail string) {
			sw.send(stepEvt(stepID, phase, "", runLabel, detail, "running", start), false)
		})
	}
	label, detail := doneLabel, ""
	if prepared && method == "downloaded" {
		label = fmt.Sprintf("Downloaded release (%s)", plat)
	} else if !prepared {
		label, detail = runLabel, "using remote self-update fallback"
	}
	sw.send(stepEvt(stepID, phase, "", label, detail, "done", start), true)
	return platDir, prepared, func() { _ = os.RemoveAll(platDir) }
}

func (s *Server) runHostDeployStep(sw *updateStreamWriter, host *HostRecord, prevVer, targetVer, goos, goarch, prepDir string, prepOK bool, probeErr error) (HostUpdateSummary, map[string]interface{}, int, error) {
	start, stepID := time.Now(), "host:"+host.Name
	sw.send(stepEvt(stepID, "updating_host", host.Name, fmt.Sprintf("Updating %s", host.Name), "", "running", start), false)
	if probeErr != nil {
		sw.send(stepEvt(stepID, "updating_host", host.Name, fmt.Sprintf("Failed updating %s", host.Name), probeErr.Error(), "error", start), true)
		return HostUpdateSummary{Host: host.Name, PreviousVersion: prevVer, NewVersion: prevVer, Status: "error", Message: probeErr.Error()}, nil, statusCodeOf(probeErr, http.StatusBadGateway), probeErr
	}
	deployFn := s.deployRemoteHostDefault
	if s.deployRemoteHostFn != nil {
		deployFn = s.deployRemoteHostFn
	}
	res, statusCode, uErr := deployFn(host, targetVer, goos, goarch, prepDir, prepOK, func(detail string) {
		sw.send(stepEvt(stepID, "updating_host", host.Name, fmt.Sprintf("Updating %s", host.Name), detail, "running", start), false)
	})
	if uErr != nil {
		sw.send(stepEvt(stepID, "updating_host", host.Name, fmt.Sprintf("Failed updating %s", host.Name), uErr.Error(), "error", start), true)
		return HostUpdateSummary{Host: host.Name, PreviousVersion: prevVer, NewVersion: prevVer, Status: "error", Message: uErr.Error()}, nil, statusCode, uErr
	}
	newVer, _ := res["version"].(string)
	msg, _ := res["message"].(string)
	status, _ := res["status"].(string)
	if status == "" {
		status = "updated"
	}
	stepStatus, stepLabel := "done", fmt.Sprintf("Updated %s to v%s", host.Name, newVer)
	if status == "restart_deferred" {
		stepStatus, stepLabel = "restart_deferred", fmt.Sprintf("Updated %s to v%s (restart deferred)", host.Name, newVer)
	}
	sw.send(stepEvt(stepID, "updating_host", host.Name, stepLabel, "", stepStatus, start), true)
	return HostUpdateSummary{Host: host.Name, PreviousVersion: prevVer, NewVersion: newVer, Status: status, Message: msg}, res, http.StatusOK, nil
}

func (s *Server) runFleetUpdate(ctx context.Context, localOnly, skipLocal bool, targetOverride string, sw *updateStreamWriter) (*fleetUpdateOutcome, error) {
	preflightStart := time.Now()
	sw.send(stepEvt("preflight", "preflight", "", "Checking release & fleet hosts", "", "running", preflightStart), false)

	targetVer, localNeedsUpdate := version.Version, false
	if skipLocal && targetOverride != "" {
		targetVer = targetOverride
	} else if !skipLocal {
		if info, _ := s.checkLatestRelease(ctx, true); info != nil && version.IsNewer(info.Version, version.Version) {
			targetVer, localNeedsUpdate = info.Version, true
		}
	}

	var plan fleetProbePlan
	if !localOnly {
		plan = s.probeFleetHostsForUpdate(ctx, targetVer)
	}

	totalSteps := len(plan.uniquePlats) + len(plan.outdatedIndices)
	if localNeedsUpdate {
		totalSteps++
	}
	sw.send(UpdateProgressEvent{Type: "step", StepID: "preflight", Phase: "preflight", Label: "Checked release & fleet hosts", Detail: fmt.Sprintf("%d step(s) scheduled", totalSteps), Status: "done", TotalSteps: totalSteps, StepElapsedMs: time.Since(preflightStart).Milliseconds()}, false)
	if !skipLocal && !localNeedsUpdate {
		sw.send(stepEvt("local", "updating_local", s.HostName(), fmt.Sprintf("%s (local) already at v%s", s.HostName(), version.Version), "", "up_to_date", time.Time{}), false)
	}

	summaries := make([]HostUpdateSummary, len(plan.probes))
	for i, p := range plan.probes {
		if p.host == nil || p.status == "outdated" {
			continue
		}
		label, msg := fmt.Sprintf("%s already at v%s", p.host.Name, p.remoteVer), fmt.Sprintf("Host '%s' is already up to date (v%s)", p.host.Name, p.remoteVer)
		if p.status == "skipped_offline" {
			label, msg = fmt.Sprintf("Skipped %s (offline)", p.host.Name), fmt.Sprintf("Skipped '%s' (offline)", p.host.Name)
		}
		summaries[i] = HostUpdateSummary{Host: p.host.Name, PreviousVersion: p.remoteVer, NewVersion: p.remoteVer, Status: p.status, Message: msg}
		sw.send(stepEvt("host:"+p.host.Name, "updating_host", p.host.Name, label, "", p.status, time.Time{}), false)
	}

	outcome := &fleetUpdateOutcome{prevVersion: version.Version, targetVersion: targetVer, method: "local", message: fmt.Sprintf("Local daemon is already at v%s.", version.Version), localRestartNeeded: localNeedsUpdate}
	if localNeedsUpdate {
		localStart := time.Now()
		sw.send(stepEvt("local", "updating_local", s.HostName(), fmt.Sprintf("Updating %s (local)", s.HostName()), "installing v"+targetVer, "running", localStart), false)
		updateFn := s.updateLocalFn
		if updateFn == nil {
			updateFn = func(c context.Context) (*version.UpdateResult, error) { return version.UpdateToLatest(c, "") }
		}
		res, err := updateFn(ctx)
		if err != nil {
			sw.send(stepEvt("local", "updating_local", s.HostName(), fmt.Sprintf("Failed updating %s (local)", s.HostName()), err.Error(), "error", localStart), true)
			return nil, err
		}
		if res != nil {
			outcome.message = res.Message
			if res.PreviousVersion != "" {
				outcome.prevVersion = res.PreviousVersion
			}
			if res.NewVersion != "" && res.NewVersion != "latest" {
				targetVer, outcome.targetVersion = res.NewVersion, res.NewVersion
			}
			if res.Method != "" {
				outcome.method = res.Method
			}
		}
		sw.send(stepEvt("local", "updating_local", s.HostName(), fmt.Sprintf("Updated %s (local) to v%s", s.HostName(), targetVer), "", "done", localStart), true)
	}

	srcDir := findUpToDateSourceDir(targetVer)
	prepDirs, prepOKs := make(map[string]string, len(plan.uniquePlats)), make(map[string]bool, len(plan.uniquePlats))
	for _, platKey := range plan.uniquePlats {
		pair := plan.platSet[platKey]
		dir, ok, cleanup := s.runPlatformPrepareStep(ctx, sw, targetVer, srcDir, pair[0], pair[1])
		defer cleanup()
		prepDirs[platKey], prepOKs[platKey] = dir, ok
	}

	var deployWG sync.WaitGroup
	for _, idx := range plan.outdatedIndices {
		deployWG.Add(1)
		go func(i int, p hostProbeResult) {
			defer deployWG.Done()
			pk := p.goos + "/" + p.goarch
			summaries[i], _, _, _ = s.runHostDeployStep(sw, p.host, p.remoteVer, targetVer, p.goos, p.goarch, prepDirs[pk], prepOKs[pk], p.probeErr)
		}(idx, plan.probes[idx])
	}
	deployWG.Wait()

	var msgLines []string
	if outcome.message != "" {
		msgLines = []string{outcome.message}
	}
	for _, sum := range summaries {
		if sum.Host != "" {
			outcome.hostSummaries = append(outcome.hostSummaries, sum)
			if !localOnly && sum.Message != "" {
				msgLines = append(msgLines, fmt.Sprintf("• %s: %s", sum.Host, sum.Message))
			}
		}
	}
	if !localOnly {
		outcome.message = strings.Join(msgLines, "\n")
	}
	return outcome, nil
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		info, err := s.checkLatestRelease(r.Context(), r.URL.Query().Get("force") == "true")
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to check for updates: %v", err), http.StatusBadGateway)
			return
		}
		writeUpdateJSON(w, http.StatusOK, info)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runStart, localOnly := time.Now(), r.URL.Query().Get("local_only") == "true"
	var sw *updateStreamWriter
	if wantsUpdateStream(r) {
		sw = newUpdateStreamWriter(w, runStart)
	}
	outcome, err := s.runFleetUpdate(r.Context(), localOnly, false, "", sw)
	if err != nil {
		if sw != nil {
			sw.send(UpdateProgressEvent{Type: "error", StepID: "local", Phase: "updating_local", Label: "Local update failed", Message: fmt.Sprintf("Update failed: %v", err)}, false)
			return
		}
		http.Error(w, fmt.Sprintf("Update failed: %v", err), http.StatusInternalServerError)
		return
	}

	restartFn := s.restartDaemonFn
	if restartFn == nil {
		restartFn = restartLocalDaemonAsync
	}
	deferred, activeCount := false, 0
	if outcome.localRestartNeeded && s.headless != nil {
		// Check drain state without running restartFn immediately so the response flushes before daemon restart.
		deferred, activeCount = s.headless.DeferRestartIfActive(restartFn)
	}
	if deferred {
		outcome.message = strings.TrimSpace(outcome.message + fmt.Sprintf("\n⏳ Local daemon restart deferred until %d active headless turn(s) complete.", activeCount))
	}
	respMap := map[string]interface{}{
		"previous_version": outcome.prevVersion, "new_version": outcome.targetVersion,
		"method": outcome.method, "message": outcome.message,
		"host_results": outcome.hostSummaries, "restarting_local": outcome.localRestartNeeded && !deferred,
		"restart_deferred": deferred, "active_headless_turns": activeCount, "elapsed_ms": time.Since(runStart).Milliseconds(),
	}
	if sw != nil {
		sw.send(UpdateProgressEvent{Type: "complete", Phase: "complete", Label: "Fleet update complete", Status: "done", Result: respMap}, false)
	} else {
		writeUpdateJSON(w, http.StatusOK, respMap)
	}
	if outcome.localRestartNeeded && !deferred {
		restartFn()
	}
}

func (s *Server) handleHostUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hostName := r.URL.Query().Get("name")
	if hostName == "" {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		hostName = req["name"]
	}
	if hostName == "" {
		http.Error(w, "Missing host name", http.StatusBadRequest)
		return
	}
	var sw *updateStreamWriter
	if wantsUpdateStream(r) {
		sw = newUpdateStreamWriter(w, time.Now())
		sw.totalSteps = 2
	}
	if hostName == "local" || hostName == s.HostName() {
		respondUpdateError(w, sw, http.StatusOK, "Local host daemon is managed directly on this machine (run 'ackbar setup-hooks')")
		return
	}
	host := s.lookupUpdateHost(hostName)
	if host == nil {
		respondUpdateError(w, sw, http.StatusNotFound, fmt.Sprintf("Host '%s' not registered", hostName))
		return
	}
	targetVer := version.Version
	if info, cerr := s.checkLatestRelease(r.Context(), false); cerr == nil && info != nil && version.IsNewer(info.Version, targetVer) {
		targetVer = info.Version
	}
	goos, goarch, pErr := s.detectRemotePlatform(host)
	if pErr != nil {
		sw.send(stepEvt("host:"+host.Name, "updating_host", host.Name, fmt.Sprintf("Failed updating %s", host.Name), pErr.Error(), "error", time.Time{}), false)
		respondUpdateError(w, sw, statusCodeOf(pErr, http.StatusBadGateway), pErr.Error())
		return
	}
	prepDir, prepOK, cleanup := s.runPlatformPrepareStep(r.Context(), sw, targetVer, findUpToDateSourceDir(targetVer), goos, goarch)
	defer cleanup()
	_, res, statusCode, uErr := s.runHostDeployStep(sw, host, "", targetVer, goos, goarch, prepDir, prepOK, nil)
	if uErr != nil {
		respondUpdateError(w, sw, statusCode, uErr.Error())
		return
	}
	if sw != nil {
		sw.send(UpdateProgressEvent{Type: "complete", Phase: "complete", Host: host.Name, Label: fmt.Sprintf("Updated %s", host.Name), Status: "done", Result: res}, false)
		return
	}
	writeUpdateJSON(w, http.StatusOK, res)
}
