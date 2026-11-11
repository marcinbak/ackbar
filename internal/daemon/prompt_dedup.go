package daemon

import (
	"container/list"
	"context"
	"sync"
	"time"
)

const (
	defaultPromptDedupMaxEntries = 512
	defaultPromptDedupTTL        = 10 * time.Minute
)

type promptDedupEntry struct {
	key       string
	createdAt time.Time
	inFlight  bool
	committed bool
	status    string
	done      chan struct{}
	elem      *list.Element
}

type DedupReservation struct {
	dedup  *PromptDedup
	key    string
	entry  *promptDedupEntry
	closed bool
}

func (r *DedupReservation) Commit(status string) {
	if r == nil {
		return
	}
	r.dedup.mu.Lock()
	defer r.dedup.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.entry.inFlight = false
	r.entry.committed = true
	r.entry.status = status
	close(r.entry.done)
}

func (r *DedupReservation) Abort() {
	if r == nil {
		return
	}
	r.dedup.mu.Lock()
	defer r.dedup.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.dedup.removeEntryLocked(r.entry)
	close(r.entry.done)
}

type DedupResult struct {
	Status string
}

type PromptDedup struct {
	mu         sync.Mutex
	entries    map[string]*promptDedupEntry
	lru        *list.List
	maxEntries int
	ttl        time.Duration
	nowFn      func() time.Time
}

func newPromptDedup() *PromptDedup {
	return &PromptDedup{
		entries:    make(map[string]*promptDedupEntry),
		lru:        list.New(),
		maxEntries: defaultPromptDedupMaxEntries,
		ttl:        defaultPromptDedupTTL,
		nowFn:      time.Now,
	}
}

func (p *PromptDedup) now() time.Time {
	if p.nowFn != nil {
		return p.nowFn()
	}
	return time.Now()
}

func (p *PromptDedup) removeEntryLocked(entry *promptDedupEntry) {
	delete(p.entries, entry.key)
	if entry.elem != nil {
		p.lru.Remove(entry.elem)
		entry.elem = nil
	}
}

func (p *PromptDedup) cleanupExpiredLocked() {
	now := p.now()
	for elem := p.lru.Back(); elem != nil; {
		prev := elem.Prev()
		entry := elem.Value.(*promptDedupEntry)
		if !entry.inFlight && now.Sub(entry.createdAt) > p.ttl {
			p.removeEntryLocked(entry)
		}
		elem = prev
	}
}

func (p *PromptDedup) evictOldestIfFullLocked() {
	if len(p.entries) < p.maxEntries {
		return
	}
	for elem := p.lru.Back(); elem != nil; elem = elem.Prev() {
		entry := elem.Value.(*promptDedupEntry)
		if !entry.inFlight {
			p.removeEntryLocked(entry)
			if len(p.entries) < p.maxEntries {
				return
			}
		}
	}
}

func (p *PromptDedup) Reserve(ctx context.Context, key string) (*DedupReservation, *DedupResult, error) {
	for {
		p.mu.Lock()
		p.cleanupExpiredLocked()

		if entry, exists := p.entries[key]; exists {
			if !entry.inFlight && p.now().Sub(entry.createdAt) > p.ttl {
				p.removeEntryLocked(entry)
			} else if entry.inFlight {
				doneCh := entry.done
				p.mu.Unlock()
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-doneCh:
					continue
				}
			} else if entry.committed {
				p.lru.MoveToFront(entry.elem)
				status := entry.status
				p.mu.Unlock()
				return nil, &DedupResult{Status: status}, nil
			} else {
				p.removeEntryLocked(entry)
				p.mu.Unlock()
				continue
			}
		}

		p.evictOldestIfFullLocked()
		entry := &promptDedupEntry{
			key:       key,
			createdAt: p.now(),
			inFlight:  true,
			done:      make(chan struct{}),
		}
		entry.elem = p.lru.PushFront(entry)
		p.entries[key] = entry
		p.mu.Unlock()

		return &DedupReservation{
			dedup: p,
			key:   key,
			entry: entry,
		}, nil, nil
	}
}
