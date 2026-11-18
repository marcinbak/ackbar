package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ackbar/internal/agent"
)

var errACPMethodNotFound = errors.New("acp method not found")

func isACPHeadlessAgent(reg *agent.Registry, agentName string) bool {
	_, ok := acpDriverForAgent(reg, agentName)
	return ok
}

func acpDriverForAgent(reg *agent.Registry, agentName string) (agent.ACPDriver, bool) {
	if reg == nil || strings.TrimSpace(agentName) == "" {
		return nil, false
	}
	return reg.AsACPDriver(agentName)
}

func buildPersistentOrACPArgs(reg *agent.Registry, sess *Session, home string, db *DB, isResume bool) (bin string, args []string, extraEnv []string, sysPrompt string, err error) {
	if sess == nil {
		return "", nil, nil, "", errors.New("nil session")
	}
	_ = db
	if hd, ok := acpDriverForAgent(reg, sess.Agent); ok {
		if sess.IsSupervisor {
			sysPrompt = BuildSupervisorSystemPrompt(sess.ID)
		}
		spec, bErr := hd.BuildTurnSpec(agent.HeadlessTurnInput{
			Session:      sess,
			HomeDir:      home,
			IsExisting:   isResume,
			SystemPrompt: sysPrompt,
		})
		if bErr != nil {
			return "", nil, nil, "", bErr
		}
		return spec.Binary, spec.Args, spec.Env, sysPrompt, nil
	}
	b, a, bErr := buildPersistentArgs(sess, isResume)
	return b, a, nil, "", bErr
}

func acpImmutableFingerprint(sess *Session) string {
	if sess == nil {
		return ""
	}
	ag := strings.ToLower(strings.TrimSpace(sess.Agent))
	acc := strings.TrimSpace(sess.AccountID)
	cwd := filepath.Clean(strings.TrimSpace(sess.Cwd))
	sup := strconv.FormatBool(sess.IsSupervisor)
	yoloOrBypass := strconv.FormatBool(sess.YoloMode || strings.EqualFold(strings.TrimSpace(sess.PermissionMode), "bypassPermissions"))

	var extras []string
	for _, d := range sess.ExtraDirs {
		if cleaned := filepath.Clean(strings.TrimSpace(d)); cleaned != "" && cleaned != "." {
			extras = append(extras, cleaned)
		}
	}
	sort.Strings(extras)

	h := sha256.New()
	parts := append([]string{ag, acc, cwd, sup, yoloOrBypass}, extras...)
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func tryLiveUpdateACPWarmWorker(h *HeadlessRunner, sess *Session, handle *warmWorkerHandle, newFP, newFPNoModel string) bool {
	if h == nil || sess == nil || handle == nil || !isACPHeadlessAgent(h.pluginRegistry(), sess.Agent) {
		return false
	}
	targetImmFP := acpImmutableFingerprint(sess)
	h.mu.RLock()
	sockPath := handle.SocketPath
	handleImmFP := handle.ImmutableFingerprint
	h.mu.RUnlock()
	if sockPath == "" || handleImmFP == "" || handleImmFP != targetImmFP {
		return false
	}

	home := h.effectiveHomeDir()
	isExisting := sess.FirstPrompt != "" || sess.LastPrompt != "" || hasConversationOnDiskWithRegistry(h.pluginRegistry(), sess, home, h.db)
	_, nextArgs, _, _, bErr := buildPersistentOrACPArgs(h.pluginRegistry(), sess, home, h.db, isExisting)
	if bErr != nil {
		return false
	}

	resp, err := callWorkerSocket(sockPath, workerIPCReq{
		Op:                       "set_config",
		Model:                    sess.Model,
		Effort:                   sess.Effort,
		PermissionMode:           sess.PermissionMode,
		Args:                     nextArgs,
		ConfigFingerprint:        newFP,
		ConfigFingerprintNoModel: newFPNoModel,
	}, 5*time.Second)
	if err != nil || resp == nil || !resp.OK {
		return false
	}

	h.mu.Lock()
	handle.Args = nextArgs
	handle.ConfigFingerprint = newFP
	handle.ConfigFingerprintNoModel = newFPNoModel
	handle.rotateAfterTurn = false
	if resp.ChildPID > 1 {
		handle.ChildPID = resp.ChildPID
	}
	h.mu.Unlock()
	if strings.TrimSpace(sess.Model) != "" {
		h.SetRuntimeModel(sess.ID, sess.Agent, sess.Model)
	}
	return true
}

func (h *HeadlessRunner) effectiveHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func (h *HeadlessRunner) getWarmWorkerSocket(sess *Session) string {
	if h == nil || sess == nil {
		return ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, wh := h.findWarmHandleLocked(sess); wh != nil {
		return wh.SocketPath
	}
	return ""
}

func (h *HeadlessRunner) ListACPRewindPoints(sess *Session) ([]agent.ACPRewindPoint, bool) {
	if h == nil || sess == nil || !isACPHeadlessAgent(h.pluginRegistry(), sess.Agent) {
		return nil, false
	}
	sockPath := h.getWarmWorkerSocket(sess)
	if sockPath == "" {
		return nil, false
	}
	resp, err := callWorkerSocket(sockPath, workerIPCReq{Op: "acp_rewind_list"}, 5*time.Second)
	if err != nil || resp == nil || !resp.OK {
		return nil, false
	}
	return resp.RewindPoints, true
}

func (h *HeadlessRunner) ExecuteACPRewind(ctx context.Context, sess *Session, targetID string) (json.RawMessage, bool, error) {
	if h == nil || sess == nil || targetID == "" || !isACPHeadlessAgent(h.pluginRegistry(), sess.Agent) {
		return nil, false, nil
	}
	sockPath := h.getWarmWorkerSocket(sess)
	if sockPath == "" {
		return nil, false, nil
	}
	done := make(chan struct{})
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = h.CancelACPRewind(sess)
			case <-done:
			}
		}()
	}
	resp, err := callWorkerSocket(sockPath, workerIPCReq{Op: "acp_rewind_exec", TargetID: targetID}, 10*time.Second)
	close(done)
	if ctx != nil && ctx.Err() != nil {
		return nil, true, ctx.Err()
	}
	if err != nil || resp == nil {
		return nil, false, err
	}
	if !resp.OK {
		if resp.ErrCode == "method_not_found" {
			return nil, false, errACPMethodNotFound
		}
		if resp.ErrCode == "not_idle" || resp.ErrCode == "unsupported_or_inactive" {
			return nil, false, nil
		}
		return nil, true, errors.New(resp.ErrCode)
	}
	return resp.RewindResult, true, nil
}

func (h *HeadlessRunner) CancelACPRewind(sess *Session) error {
	if h == nil || sess == nil {
		return nil
	}
	sockPath := h.getWarmWorkerSocket(sess)
	if sockPath == "" {
		return nil
	}
	_, err := callWorkerSocket(sockPath, workerIPCReq{Op: "acp_rewind_cancel"}, 2*time.Second)
	return err
}
