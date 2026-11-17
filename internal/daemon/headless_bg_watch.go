package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"
)

type workerWatchHub struct {
	mu     sync.Mutex
	closed bool
	subs   map[chan workerIPCResp]struct{}
}

func newWorkerWatchHub() *workerWatchHub {
	return &workerWatchHub{subs: make(map[chan workerIPCResp]struct{})}
}

func (w *workerWatchHub) subscribe(rt *workerRuntime) (workerIPCResp, chan workerIPCResp, func()) {
	ch := make(chan workerIPCResp, 64)
	rt.mu.Lock()
	snap := rt.snapshotLocked()
	w.mu.Lock()
	closed := w.closed
	if !closed {
		w.subs[ch] = struct{}{}
	}
	w.mu.Unlock()
	rt.mu.Unlock()
	if closed {
		close(ch)
	}
	return snap, ch, func() {
		w.mu.Lock()
		if _, ok := w.subs[ch]; ok {
			delete(w.subs, ch)
			close(ch)
		}
		w.mu.Unlock()
	}
}

func (w *workerWatchHub) notify(snap workerIPCResp) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	for ch := range w.subs {
		select {
		case ch <- snap:
		default:
			// Drop oldest frame so a slow reader never stalls stateMu while keeping the latest state.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
	w.mu.Unlock()
}

func (w *workerWatchHub) closeAll() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		for ch := range w.subs {
			delete(w.subs, ch)
			close(ch)
		}
	}
	w.mu.Unlock()
}

func (w *workerWatchHub) handleWatchIPC(c net.Conn, rt *workerRuntime) {
	snap, ch, unsub := w.subscribe(rt)
	defer unsub()

	writeFrame := func(frame workerIPCResp) bool {
		b, err := json.Marshal(frame)
		if err != nil {
			return true
		}
		_, err = c.Write(append(b, '\n'))
		return err == nil
	}
	if !writeFrame(snap) {
		return
	}
	for frame := range ch {
		if !writeFrame(frame) {
			return
		}
	}
}

func (h *HeadlessRunner) ensureWarmWatcher(handle *warmWorkerHandle) {
	if h == nil || handle == nil || handle.WorkerID == "" || handle.SocketPath == "" {
		return
	}
	h.mu.Lock()
	if _, exists := h.warmWatchCancels[handle.WorkerID]; exists {
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.warmWatchCancels[handle.WorkerID] = cancel
	h.watchWG.Add(1)
	sockPath, metaPath, sessID, nativeID, workerID := handle.SocketPath, handle.WorkerMetaPath, handle.SessionID, handle.NativeID, handle.WorkerID
	h.mu.Unlock()

	go h.watchWarmWorker(ctx, workerID, sockPath, metaPath, sessID, nativeID)
}

func (h *HeadlessRunner) stopWarmWatchersForSession(sessionID, nativeID string) {
	if h == nil {
		return
	}
	var cancels []context.CancelFunc
	h.mu.Lock()
	for k, wh := range h.warmWorkers {
		if wh != nil && (k == sessionID || wh.SessionID == sessionID || (nativeID != "" && (wh.NativeID == nativeID || strings.HasSuffix(k, ":"+nativeID)))) {
			if c := h.warmWatchCancels[wh.WorkerID]; c != nil {
				cancels = append(cancels, c)
				delete(h.warmWatchCancels, wh.WorkerID)
			}
		}
	}
	h.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (h *HeadlessRunner) stopAllWarmWatchers() {
	if h == nil {
		return
	}
	h.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(h.warmWatchCancels))
	for wid, c := range h.warmWatchCancels {
		if c != nil {
			cancels = append(cancels, c)
		}
		delete(h.warmWatchCancels, wid)
	}
	h.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (h *HeadlessRunner) resolveWatchSessionID(workerID, fallbackSessID, fallbackNativeID string) (string, string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for k, wh := range h.warmWorkers {
		if wh != nil && wh.WorkerID == workerID {
			sid := wh.SessionID
			if sid == "" {
				sid = k
			}
			nid := wh.NativeID
			if nid == "" {
				nid = fallbackNativeID
			}
			return sid, nid
		}
	}
	return fallbackSessID, fallbackNativeID
}

func (h *HeadlessRunner) watchWarmWorker(ctx context.Context, workerID, sockPath, workerMetaPath, initSessID, initNativeID string) {
	defer h.watchWG.Done()
	defer func() {
		h.mu.Lock()
		delete(h.warmWatchCancels, workerID)
		h.mu.Unlock()
	}()

	waitOrStop := func(d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}

	everConnected := false
	startupDeadline := time.Now().Add(3 * time.Second)
	for ctx.Err() == nil {
		var conn net.Conn
		var err error
		for {
			conn, err = net.DialTimeout("unix", sockPath, 250*time.Millisecond)
			if err == nil {
				break
			}
			if !everConnected && time.Now().Before(startupDeadline) {
				if !waitOrStop(40 * time.Millisecond) {
					return
				}
				continue
			}
			if !waitOrStop(40 * time.Millisecond) {
				return
			}
			conn, err = net.DialTimeout("unix", sockPath, 250*time.Millisecond)
			if err != nil {
				sid, nid := h.resolveWatchSessionID(workerID, initSessID, initNativeID)
				h.handleWarmWorkerExited(workerID, sid, nid)
				return
			}
			break
		}
		everConnected = true

		reqBytes, _ := json.Marshal(workerIPCReq{Op: "watch_worker"})
		if _, wErr := conn.Write(append(reqBytes, '\n')); wErr != nil {
			_ = conn.Close()
			if !waitOrStop(50 * time.Millisecond) {
				return
			}
			continue
		}

		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for sc.Scan() {
			if ctx.Err() != nil {
				_ = conn.Close()
				return
			}
			var frame workerIPCResp
			if json.Unmarshal(sc.Bytes(), &frame) != nil {
				continue
			}
			sid, nid := h.resolveWatchSessionID(workerID, initSessID, initNativeID)
			h.setBgTaskCount(sid, nid, frame.BgTaskCount)
			if frame.State == "shutting_down" || frame.State == "exited" {
				h.handleWarmWorkerExited(workerID, sid, nid)
				_ = conn.Close()
				return
			}
			if frame.TurnID != "" && (frame.State == "running_turn" || frame.State == "blocked_control") {
				h.maybeAttachObservedTurn(sid, workerMetaPath, frame)
			}
		}
		_ = conn.Close()

		if ctx.Err() != nil {
			return
		}
		resp, sErr := callWorkerSocket(sockPath, workerIPCReq{Op: "status"}, 150*time.Millisecond)
		if sErr != nil || resp == nil || !resp.OK || resp.State == "shutting_down" || resp.State == "exited" {
			sid, nid := h.resolveWatchSessionID(workerID, initSessID, initNativeID)
			h.handleWarmWorkerExited(workerID, sid, nid)
			return
		}
		if !waitOrStop(150 * time.Millisecond) {
			return
		}
	}
}
