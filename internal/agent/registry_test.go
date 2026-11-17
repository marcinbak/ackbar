package agent

import (
	"fmt"
	"sync"
	"testing"
)

type minimalPlugin struct {
	BasePlugin
	key     string
	display string
	aliases []string
	bins    []string
	caps    Capabilities
}

func (p *minimalPlugin) Key() string                { return p.key }
func (p *minimalPlugin) DisplayName() string        { return p.display }
func (p *minimalPlugin) Aliases() []string          { return p.aliases }
func (p *minimalPlugin) BinaryNames() []string      { return p.bins }
func (p *minimalPlugin) Capabilities() Capabilities { return p.caps }

type fullStubPlugin struct {
	minimalPlugin
	BaseProvisioner
	BaseAccounts
	BaseTMuxDriver
	BaseHooks
	BaseTranscripts
	BaseScanner
	BaseModelCatalog
	BaseRateLimiter
	mode HeadlessExecMode
}

func (f *fullStubPlugin) Mode() HeadlessExecMode         { return f.mode }
func (f *fullStubPlugin) ResolveBinary() (string, error) { return f.key, nil }
func (f *fullStubPlugin) BuildTurnSpec(in HeadlessTurnInput) (HeadlessTurnSpec, error) {
	return HeadlessTurnSpec{Binary: f.key, Mode: f.mode}, nil
}
func (f *fullStubPlugin) ParseStreamEvent(line []byte) ([]ChatStreamEvent, error) { return nil, nil }
func (f *fullStubPlugin) ComputeUsage(sessionID, cwd, model string) (TranscriptUsage, bool) {
	return TranscriptUsage{}, false
}

func TestRegistry_OrderAliasesAndCollisionRollback(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(nil); err == nil {
		t.Fatal("expected nil plugin error")
	}
	if err := reg.Register(&minimalPlugin{key: "   "}); err == nil {
		t.Fatal("expected empty key error")
	}

	p1 := &minimalPlugin{key: "claude-code", display: "Claude Code", aliases: []string{"claude", "CC"}}
	p2 := &minimalPlugin{key: "antigravity", display: "Antigravity", aliases: []string{"agy"}}
	if err := reg.Register(p1); err != nil {
		t.Fatalf("register p1: %v", err)
	}
	if err := reg.Register(p2); err != nil {
		t.Fatalf("register p2: %v", err)
	}

	// Duplicate primary key must fail
	if err := reg.Register(&minimalPlugin{key: "CLAUDE-CODE"}); err == nil {
		t.Fatal("expected duplicate key error")
	}

	// Alias collision on second alias must fail atomically without binding first alias
	colliding := &minimalPlugin{key: "grok", aliases: []string{"xai-grok", "AGY"}}
	if err := reg.Register(colliding); err == nil {
		t.Fatal("expected alias collision error")
	}
	if _, ok := reg.Get("grok"); ok {
		t.Fatal("colliding plugin primary key leaked into registry")
	}
	if _, ok := reg.Get("xai-grok"); ok {
		t.Fatal("colliding plugin non-colliding alias leaked into registry")
	}

	got, ok := reg.Get("  cc ")
	if !ok || got.Key() != "claude-code" {
		t.Fatalf("Get('cc') = (%v, %v), want claude-code", got, ok)
	}

	keys := reg.Keys()
	if len(keys) != 2 || keys[0] != "claude-code" || keys[1] != "antigravity" {
		t.Fatalf("Keys() = %v, want deterministic [claude-code antigravity]", keys)
	}
	list := reg.List()
	if len(list) != 2 || list[0].Key() != "claude-code" || list[1].Key() != "antigravity" {
		t.Fatalf("List() order mismatch: %+v", list)
	}

	var seqKeys []string
	for p := range reg.All() {
		seqKeys = append(seqKeys, p.Key())
		break
	}
	if len(seqKeys) != 1 || seqKeys[0] != "claude-code" {
		t.Fatalf("All() early break yielded %v", seqKeys)
	}
}

func TestRegistry_FacetAssertionsAndHeadlessGate(t *testing.T) {
	reg := NewRegistry()
	bare := &minimalPlugin{key: "bare"}
	gatedHeadless := &fullStubPlugin{
		minimalPlugin: minimalPlugin{
			key:     "tmux-only",
			aliases: []string{"to"},
			caps:    Capabilities{SupportsTmux: true, SupportsHeadless: false},
		},
		mode: HeadlessModePersistent,
	}
	full := &fullStubPlugin{
		minimalPlugin: minimalPlugin{
			key:     "full-agent",
			aliases: []string{"fa"},
			caps:    Capabilities{SupportsTmux: true, SupportsHeadless: true, HeadlessMode: HeadlessModeACP},
		},
		mode: HeadlessModeACP,
	}

	for _, p := range []Plugin{bare, gatedHeadless, full} {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Key(), err)
		}
	}

	if _, ok := reg.AsProvisioner("bare"); ok {
		t.Error("bare plugin should not satisfy Provisioner")
	}
	if _, ok := reg.AsHeadlessDriver("bare"); ok {
		t.Error("bare plugin should not satisfy HeadlessDriver")
	}
	if _, ok := reg.AsHeadlessDriver("tmux-only"); ok {
		t.Error("SupportsHeadless=false must gate AsHeadlessDriver")
	}
	if hd, ok := reg.AsHeadlessDriver("FA"); !ok || hd.Mode() != HeadlessModeACP {
		t.Fatalf("AsHeadlessDriver('FA') = (%v, %v), want ACP", hd, ok)
	}
	if _, ok := reg.AsProvisioner("fa"); !ok {
		t.Error("expected full-agent Provisioner")
	}
	if _, ok := reg.AsAccounts("fa"); !ok {
		t.Error("expected full-agent Accounts")
	}
	if _, ok := reg.AsTMuxDriver("fa"); !ok {
		t.Error("expected full-agent TMuxDriver")
	}
	if _, ok := reg.AsHooks("fa"); !ok {
		t.Error("expected full-agent Hooks")
	}
	if _, ok := reg.AsTranscript("fa"); !ok {
		t.Error("expected full-agent Transcript")
	}
	if _, ok := reg.AsScanner("fa"); !ok {
		t.Error("expected full-agent Scanner")
	}
	if _, ok := reg.AsModelCatalog("fa"); !ok {
		t.Error("expected full-agent ModelCatalog")
	}
	if _, ok := reg.AsRateLimiter("fa"); !ok {
		t.Error("expected full-agent RateLimiter")
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("agent-%d", idx)
			alias := fmt.Sprintf("ag-%d", idx)
			_ = reg.Register(&minimalPlugin{key: key, aliases: []string{alias}})
			_, _ = reg.Get(alias)
			_ = reg.List()
			_ = reg.Keys()
			for range reg.All() {
			}
		}(i)
	}
	wg.Wait()

	if len(reg.Keys()) != 20 {
		t.Fatalf("expected 20 registered plugins, got %d", len(reg.Keys()))
	}
}
