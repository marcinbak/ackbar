package daemon

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type setsizeRecorder struct {
	calls [][2]int
	err   error
}

func (r *setsizeRecorder) setsize(cols, rows int) error {
	r.calls = append(r.calls, [2]int{cols, rows})
	return r.err
}

func TestPTYResizeTracker_SameSizeAsStartIsNoop(t *testing.T) {
	rec := &setsizeRecorder{}
	tr := newPTYResizeTracker(120, 32, rec.setsize)

	applied, err := tr.Apply(120, 32)
	if err != nil || applied {
		t.Fatalf("Apply(start size) = (%v, %v), want (false, nil)", applied, err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("setsize called %d times for unchanged size, want 0", len(rec.calls))
	}
}

func TestPTYResizeTracker_NewSizeCallsOnceThenDedupes(t *testing.T) {
	rec := &setsizeRecorder{}
	tr := newPTYResizeTracker(120, 32, rec.setsize)

	applied, err := tr.Apply(100, 40)
	if err != nil || !applied {
		t.Fatalf("Apply(new size) = (%v, %v), want (true, nil)", applied, err)
	}
	// Repeated activations re-send the same size; none may reach the PTY.
	for i := 0; i < 3; i++ {
		if applied, _ := tr.Apply(100, 40); applied {
			t.Fatalf("Apply(repeat %d) applied, want deduped", i)
		}
	}
	if len(rec.calls) != 1 || rec.calls[0] != [2]int{100, 40} {
		t.Fatalf("setsize calls = %v, want [[100 40]]", rec.calls)
	}

	// Changing only one dimension is still a change.
	if applied, _ := tr.Apply(100, 41); !applied {
		t.Fatalf("Apply(rows change) not applied")
	}
	if applied, _ := tr.Apply(101, 41); !applied {
		t.Fatalf("Apply(cols change) not applied")
	}
	if len(rec.calls) != 3 {
		t.Fatalf("setsize called %d times, want 3", len(rec.calls))
	}
}

func TestPTYResizeTracker_InvalidSizesIgnored(t *testing.T) {
	rec := &setsizeRecorder{}
	tr := newPTYResizeTracker(120, 32, rec.setsize)

	cases := [][2]int{
		{9, 32},        // too few cols
		{120, 3},       // too few rows
		{0, 0},         // zero
		{-5, 40},       // negative
		{70000, 40},    // overflows uint16
		{120, 0x10000}, // overflows uint16
	}
	for _, c := range cases {
		if applied, err := tr.Apply(c[0], c[1]); applied || err != nil {
			t.Errorf("Apply(%d, %d) = (%v, %v), want (false, nil)", c[0], c[1], applied, err)
		}
	}
	if len(rec.calls) != 0 {
		t.Fatalf("setsize called for invalid sizes: %v", rec.calls)
	}

	// Boundary minimum is accepted.
	if applied, _ := tr.Apply(ptyMinCols, ptyMinRows); !applied {
		t.Fatalf("Apply(min size) not applied")
	}
}

func TestPTYResizeTracker_FailedSetsizeIsRetried(t *testing.T) {
	rec := &setsizeRecorder{err: errors.New("boom")}
	tr := newPTYResizeTracker(120, 32, rec.setsize)

	if applied, err := tr.Apply(100, 40); !applied || err == nil {
		t.Fatalf("Apply with failing setsize = (%v, %v), want (true, error)", applied, err)
	}
	// The failed size must not be recorded as applied, so the next request retries.
	rec.err = nil
	if applied, err := tr.Apply(100, 40); !applied || err != nil {
		t.Fatalf("retry Apply = (%v, %v), want (true, nil)", applied, err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("setsize called %d times, want 2", len(rec.calls))
	}
}

func TestPerformTmuxScroll_LocalDirectionsAndAutoCancelAtBottom(t *testing.T) {
	var calls [][]string
	scrollPos := "1 12"
	runner := func(name string, args ...string) ([]byte, error) {
		cmd := append([]string{name}, args...)
		calls = append(calls, cmd)
		if len(args) >= 2 && args[0] == "display-message" {
			return []byte(scrollPos + "\n"), nil
		}
		return nil, nil
	}

	// 1. Scroll up 4 lines: enters copy-mode -e and scrolls up 4 lines
	calls = nil
	if err := performTmuxScroll("ackbar-claude-1", true, "", "up", 4, runner); err != nil {
		t.Fatalf("performTmuxScroll(up) failed: %v", err)
	}
	wantUp := [][]string{
		{"tmux", "copy-mode", "-e", "-t", "ackbar-claude-1"},
		{"tmux", "send-keys", "-t", "ackbar-claude-1", "-X", "-N", "4", "scroll-up"},
	}
	if !reflect.DeepEqual(calls, wantUp) {
		t.Fatalf("scroll up calls = %v, want %v", calls, wantUp)
	}

	// 2. Scroll down 3 lines while scroll_position > 0: does NOT cancel copy-mode
	calls = nil
	scrollPos = "1 5"
	if err := performTmuxScroll("ackbar-claude-1", true, "", "down", 3, runner); err != nil {
		t.Fatalf("performTmuxScroll(down) failed: %v", err)
	}
	wantDownMid := [][]string{
		{"tmux", "send-keys", "-t", "ackbar-claude-1", "-X", "-N", "3", "scroll-down"},
		{"tmux", "display-message", "-p", "-t", "ackbar-claude-1", "#{pane_in_mode} #{scroll_position}"},
	}
	if !reflect.DeepEqual(calls, wantDownMid) {
		t.Fatalf("scroll down mid calls = %v, want %v", calls, wantDownMid)
	}

	// 3. Scroll down when scroll_position reaches 0: cancels copy-mode via copy-mode -q
	calls = nil
	scrollPos = "1 0"
	if err := performTmuxScroll("ackbar-claude-1", true, "", "down", 2, runner); err != nil {
		t.Fatalf("performTmuxScroll(down at bottom) failed: %v", err)
	}
	wantDownBottom := [][]string{
		{"tmux", "send-keys", "-t", "ackbar-claude-1", "-X", "-N", "2", "scroll-down"},
		{"tmux", "display-message", "-p", "-t", "ackbar-claude-1", "#{pane_in_mode} #{scroll_position}"},
		{"tmux", "copy-mode", "-q", "-t", "ackbar-claude-1"},
	}
	if !reflect.DeepEqual(calls, wantDownBottom) {
		t.Fatalf("scroll down at bottom calls = %v, want %v", calls, wantDownBottom)
	}

	// 4. Page up, page down, and bottom
	calls = nil
	if err := performTmuxScroll("ackbar-claude-1", true, "", "page_up", 1, runner); err != nil {
		t.Fatalf("performTmuxScroll(page_up) failed: %v", err)
	}
	if !reflect.DeepEqual(calls, [][]string{{"tmux", "copy-mode", "-eu", "-t", "ackbar-claude-1"}}) {
		t.Fatalf("page_up calls = %v", calls)
	}

	calls = nil
	if err := performTmuxScroll("ackbar-claude-1", true, "", "bottom", 1, runner); err != nil {
		t.Fatalf("performTmuxScroll(bottom) failed: %v", err)
	}
	if !reflect.DeepEqual(calls, [][]string{{"tmux", "copy-mode", "-q", "-t", "ackbar-claude-1"}}) {
		t.Fatalf("bottom calls = %v", calls)
	}
}

func TestPerformTmuxScroll_RemoteSSHAndControlJSON(t *testing.T) {
	var calls [][]string
	runner := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}

	raw := `{"type":"scroll","direction":"up","lines":5}`
	var ctrl PTYControlMsg
	if err := json.Unmarshal([]byte(raw), &ctrl); err != nil {
		t.Fatalf("Unmarshal PTYControlMsg failed: %v", err)
	}
	if ctrl.Type != "scroll" || ctrl.Direction != "up" || ctrl.Lines != 5 {
		t.Fatalf("Unexpected parsed PTYControlMsg: %+v", ctrl)
	}

	if err := performTmuxScroll("ackbar-agy-9", false, "devbox", ctrl.Direction, ctrl.Lines, runner); err != nil {
		t.Fatalf("performTmuxScroll(remote) failed: %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "ssh" || calls[0][1] != "devbox" || !strings.Contains(calls[0][2], "scroll-up") {
		t.Fatalf("Unexpected remote SSH call: %v", calls)
	}
}
