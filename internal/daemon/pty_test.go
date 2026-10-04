package daemon

import (
	"errors"
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
