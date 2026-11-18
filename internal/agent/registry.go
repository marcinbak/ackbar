package agent

import (
	"fmt"
	"iter"
	"strings"
	"sync"
)

type Registry struct {
	mu    sync.RWMutex
	order []Plugin
	byKey map[string]Plugin
}

func NewRegistry() *Registry {
	return &Registry{
		byKey: make(map[string]Plugin),
	}
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (r *Registry) Register(p Plugin) error {
	if p == nil {
		return fmt.Errorf("plugin is nil")
	}
	primary := normalizeKey(p.Key())
	if primary == "" {
		return fmt.Errorf("plugin key is empty")
	}

	keysToBind := []string{primary}
	seenInPlugin := map[string]bool{primary: true}
	for _, rawAlias := range p.Aliases() {
		alias := normalizeKey(rawAlias)
		if alias == "" {
			continue
		}
		if seenInPlugin[alias] {
			continue
		}
		seenInPlugin[alias] = true
		keysToBind = append(keysToBind, alias)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.byKey == nil {
		r.byKey = make(map[string]Plugin)
	}

	for _, k := range keysToBind {
		if existing, exists := r.byKey[k]; exists {
			return fmt.Errorf("plugin key or alias %q collides with %q", k, existing.Key())
		}
	}

	for _, k := range keysToBind {
		r.byKey[k] = p
	}
	r.order = append(r.order, p)
	return nil
}

func (r *Registry) Get(keyOrAlias string) (Plugin, bool) {
	if r == nil {
		return nil, false
	}
	norm := normalizeKey(keyOrAlias)
	if norm == "" {
		return nil, false
	}
	r.mu.RLock()
	p, ok := r.byKey[norm]
	r.mu.RUnlock()
	return p, ok
}

func (r *Registry) List() []Plugin {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]Plugin, len(r.order))
	copy(out, r.order)
	r.mu.RUnlock()
	return out
}

func (r *Registry) Keys() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]string, len(r.order))
	for i, p := range r.order {
		out[i] = p.Key()
	}
	r.mu.RUnlock()
	return out
}

func (r *Registry) All() iter.Seq[Plugin] {
	snap := r.List()
	return func(yield func(Plugin) bool) {
		for _, p := range snap {
			if !yield(p) {
				return
			}
		}
	}
}
