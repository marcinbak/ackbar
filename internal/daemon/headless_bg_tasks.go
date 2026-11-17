package daemon

import (
	"os"
	"strconv"
	"time"
)

const (
	BgAutoPokePrompt         = "Background task notification received. Review the completed background task output and continue if action is needed."
	defaultBgPokeQuietWindow = 600 * time.Millisecond
	defaultBgPokeMaxCoalesce = 3 * time.Second
	maxBgTaskIdleHold        = 6 * time.Hour
)

type bgNotificationMeta struct {
	TaskID  string
	Status  string
	Summary string
}

type workerBgController struct {
	rt             *workerRuntime
	tasks          map[string]struct{}
	idleSince      time.Time
	pokeTimer      *time.Timer
	pokeGen        uint64
	pendingNotifs  []bgNotificationMeta
	firstPendingAt time.Time
	baseQuiet      time.Duration
	maxCoalesce    time.Duration
}

func newWorkerBgController(rt *workerRuntime) *workerBgController {
	quiet := defaultBgPokeQuietWindow
	if raw := os.Getenv("ACKBAR_BG_POKE_DEBOUNCE"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			quiet = d
		}
	}
	maxCoalesce := defaultBgPokeMaxCoalesce
	if raw := os.Getenv("ACKBAR_BG_POKE_MAX_COALESCE"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			maxCoalesce = d
		}
	}
	return &workerBgController{
		rt:          rt,
		tasks:       make(map[string]struct{}),
		baseQuiet:   quiet,
		maxCoalesce: maxCoalesce,
	}
}

func computeBgTaskIdleHold(bgCount int, idleTTL time.Duration, idleSince, now time.Time) (time.Duration, bool) {
	if bgCount <= 0 {
		return 0, false
	}
	if idleSince.IsZero() {
		idleSince = now
	}
	maxDeadline := idleSince.Add(maxBgTaskIdleHold)
	if !now.Before(maxDeadline) {
		return 0, false
	}
	wait := idleTTL
	if wait <= 0 {
		wait = DefaultHeadlessIdleTTL
	}
	if rem := maxDeadline.Sub(now); wait > rem {
		wait = rem
	}
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return wait, true
}

func (c *workerBgController) markIdleLocked(now time.Time) {
	if c != nil {
		c.idleSince = now
	}
}

func (c *workerBgController) cancelAutoPokeLocked() {
	if c == nil {
		return
	}
	c.pokeGen++
	if c.pokeTimer != nil {
		c.pokeTimer.Stop()
	}
	c.pendingNotifs = nil
	c.firstPendingAt = time.Time{}
}

func (c *workerBgController) resetOnChildChangeLocked() {
	if c == nil {
		return
	}
	c.cancelAutoPokeLocked()
	clear(c.tasks)
	if c.rt != nil && c.rt.workerMeta != nil {
		c.rt.workerMeta.BgTaskCount = 0
	}
}

func (c *workerBgController) schedulePokeLocked(delay time.Duration) {
	c.pokeGen++
	gen := c.pokeGen
	if c.pokeTimer != nil {
		c.pokeTimer.Stop()
	}
	c.pokeTimer = time.AfterFunc(delay, func() { c.onPokeTimerFired(gen) })
}

func (c *workerBgController) armAutoPokeLocked(notif bgNotificationMeta) {
	now := time.Now()
	c.pendingNotifs = append(c.pendingNotifs, notif)
	if c.firstPendingAt.IsZero() {
		c.firstPendingAt = now
	}
	delay := c.baseQuiet
	if len(c.tasks) > 0 {
		if rem := c.firstPendingAt.Add(c.maxCoalesce).Sub(now); rem < time.Millisecond {
			delay = time.Millisecond
		} else if delay > rem {
			delay = rem
		}
	}
	c.schedulePokeLocked(delay)
}

func (c *workerBgController) handleSystemEvent(gen *workerChildGen, raw map[string]interface{}) {
	if c == nil || c.rt == nil || raw == nil {
		return
	}
	c.rt.mu.Lock()
	defer c.rt.mu.Unlock()
	if *c.rt.curChild != gen || gen.replaced || *c.rt.state == "shutting_down" || *c.rt.state == "exited" {
		return
	}
	subtype, _ := raw["subtype"].(string)
	prevCount := len(c.tasks)

	switch subtype {
	case "background_tasks_changed":
		next := make(map[string]struct{})
		for idx, item := range parseInterfaceSlice(raw["tasks"]) {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if boolField(m, "ambient") || boolField(m, "off_host") || isTerminalBgTaskStatus(stringField(m, "status")) {
				continue
			}
			tid := stringField(m, "task_id", "id")
			if tid == "" {
				tid = "anon_" + strconv.Itoa(idx)
			}
			next[tid] = struct{}{}
		}
		c.tasks = next
		if prevCount > 0 && len(c.tasks) == 0 && *c.rt.state == "idle_warm" {
			if *c.rt.idleTimer != nil && c.rt.idleTTL > 0 {
				(*c.rt.idleTimer).Reset(c.rt.idleTTL)
			}
			if len(c.pendingNotifs) > 0 {
				c.schedulePokeLocked(c.baseQuiet)
			}
		}

	case "task_started":
		if boolField(raw, "is_backgrounded") && !boolField(raw, "ambient") && !boolField(raw, "off_host") {
			if tid := stringField(raw, "task_id"); tid != "" {
				c.tasks[tid] = struct{}{}
			}
		}

	case "task_notification":
		tid := stringField(raw, "task_id", "id")
		if tid != "" {
			delete(c.tasks, tid)
		}
		if prevCount > 0 && len(c.tasks) == 0 && *c.rt.state == "idle_warm" && *c.rt.idleTimer != nil && c.rt.idleTTL > 0 {
			(*c.rt.idleTimer).Reset(c.rt.idleTTL)
		}
		if *c.rt.state == "idle_warm" && *c.rt.activeTurn == nil && !boolField(raw, "ambient") && stringField(raw, "reason") != "worker_restart" {
			c.armAutoPokeLocked(bgNotificationMeta{
				TaskID:  tid,
				Status:  stringField(raw, "status"),
				Summary: stringField(raw, "summary"),
			})
		}
	}

	if len(c.tasks) != prevCount {
		c.rt.workerMeta.BgTaskCount = len(c.tasks)
		c.rt.workerMeta.UpdatedAt = time.Now().UTC()
		c.rt.saveMeta()
		c.rt.notifyWatchersLocked()
	}
}

func (c *workerBgController) handleTurnContent(gen *workerChildGen) {
	if c == nil || c.rt == nil {
		return
	}
	c.rt.mu.Lock()
	defer c.rt.mu.Unlock()
	if *c.rt.curChild != gen || gen.replaced || *c.rt.state != "idle_warm" || *c.rt.activeTurn != nil {
		return
	}
	c.cancelAutoPokeLocked()
	_ = c.openSyntheticBgTurnLocked("")
}

func (c *workerBgController) onPokeTimerFired(myGen uint64) {
	if c == nil || c.rt == nil {
		return
	}
	c.rt.mu.Lock()
	if myGen != c.pokeGen || len(c.pendingNotifs) == 0 || *c.rt.state != "idle_warm" || *c.rt.activeTurn != nil {
		c.rt.mu.Unlock()
		return
	}
	g := *c.rt.curChild
	if g == nil || g.stdinPipe == nil {
		c.cancelAutoPokeLocked()
		c.rt.mu.Unlock()
		return
	}
	now := time.Now()
	if len(c.tasks) > 0 && !c.firstPendingAt.IsZero() && now.Sub(c.firstPendingAt) < c.maxCoalesce {
		delay := c.baseQuiet
		if rem := c.firstPendingAt.Add(c.maxCoalesce).Sub(now); delay > rem {
			delay = rem
		}
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		c.schedulePokeLocked(delay)
		c.rt.mu.Unlock()
		return
	}
	c.cancelAutoPokeLocked()
	stdinLine, fErr := formatPersistentStdinTurn(c.rt.workerMeta.Agent, c.rt.workerMeta.SessionID, c.rt.workerMeta.IsSupervisor, false, BgAutoPokePrompt)
	if fErr != nil {
		c.rt.mu.Unlock()
		return
	}
	if err := c.openSyntheticBgTurnLocked(BgAutoPokePrompt); err != nil {
		c.rt.mu.Unlock()
		return
	}
	c.rt.mu.Unlock()
	g.writeLine(stdinLine)
}
