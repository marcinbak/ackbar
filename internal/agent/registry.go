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

func (r *Registry) AsInstallationDetector(keyOrAlias string) (InstallationDetector, bool) {
	p, ok := r.Get(keyOrAlias)
	if !ok {
		return nil, false
	}
	f, ok := p.(InstallationDetector)
	return f, ok
}

func (r *Registry) AsProvisioner(key string) (Provisioner, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(Provisioner)
	return f, ok
}

func (r *Registry) AsAccounts(key string) (Accounts, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(Accounts)
	return f, ok
}

func (r *Registry) AsTMuxDriver(key string) (TMuxDriver, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(TMuxDriver)
	return f, ok
}

func (r *Registry) AsHeadlessDriver(key string) (HeadlessDriver, bool) {
	p, ok := r.Get(key)
	if !ok || !p.Capabilities().SupportsHeadless {
		return nil, false
	}
	f, ok := p.(HeadlessDriver)
	return f, ok
}

func (r *Registry) AsHooks(key string) (Hooks, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(Hooks)
	return f, ok
}

func (r *Registry) AsTranscript(key string) (Transcripts, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(Transcripts)
	return f, ok
}

func (r *Registry) AsScanner(key string) (Scanner, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(Scanner)
	return f, ok
}

func (r *Registry) AsModelCatalog(key string) (ModelCatalog, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(ModelCatalog)
	return f, ok
}

func (r *Registry) AsRateLimiter(key string) (RateLimiter, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(RateLimiter)
	return f, ok
}

func (r *Registry) AsStreamParserFactory(key string) (StreamParserFactory, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(StreamParserFactory)
	return f, ok
}

func (r *Registry) AsModelResolver(key string) (ModelResolver, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(ModelResolver)
	return f, ok
}

func (r *Registry) AsTranscriptStatter(key string) (TranscriptStatter, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(TranscriptStatter)
	return f, ok
}

func (r *Registry) AsTranscriptUsageInspector(key string) (TranscriptUsageInspector, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(TranscriptUsageInspector)
	return f, ok
}

func (r *Registry) AsCLIInspector(key string) (CLIInspector, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(CLIInspector)
	return f, ok
}

func (r *Registry) AsACPDriver(key string) (ACPDriver, bool) {
	p, ok := r.Get(key)
	if !ok || !p.Capabilities().SupportsHeadless {
		return nil, false
	}
	d, ok := p.(ACPDriver)
	if !ok || d.Mode() != HeadlessModeACP {
		return nil, false
	}
	return d, true
}

func (r *Registry) AsCommandCatalog(key string) (CommandCatalog, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(CommandCatalog)
	return f, ok
}
