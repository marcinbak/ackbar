package agent

import (
	"fmt"
	"io"
	"path/filepath"
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

type detectOnlyPlugin struct {
	minimalPlugin
	BaseInstallationDetector
}

func TestRegistry_FacetAssertionsAndHeadlessGate(t *testing.T) {
	reg := NewRegistry()
	bare := &minimalPlugin{key: "bare"}
	detectOnly := &detectOnlyPlugin{
		minimalPlugin: minimalPlugin{key: "detect-only", aliases: []string{"do"}},
	}
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

	for _, p := range []Plugin{bare, detectOnly, gatedHeadless, full} {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Key(), err)
		}
	}

	if _, ok := reg.AsInstallationDetector("bare"); ok {
		t.Error("bare plugin should not satisfy InstallationDetector")
	}
	if _, ok := reg.AsProvisioner("bare"); ok {
		t.Error("bare plugin should not satisfy Provisioner")
	}
	if det, ok := reg.AsInstallationDetector("do"); !ok || det.IsInstalled("/tmp") {
		t.Error("detect-only plugin should satisfy InstallationDetector")
	}
	if _, ok := reg.AsProvisioner("do"); ok {
		t.Error("detect-only plugin should not satisfy Provisioner")
	}
	if _, ok := reg.AsInstallationDetector("fa"); !ok {
		t.Error("expected full-agent InstallationDetector via Provisioner")
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
	if accts, ok := reg.AsAccounts("fa"); !ok || accts.APIKeyEnvVar() != "" {
		t.Error("expected full-agent Accounts with empty APIKeyEnvVar default")
	}
	if _, ok := reg.AsTMuxDriver("fa"); !ok {
		t.Error("expected full-agent TMuxDriver")
	}
	if _, ok := reg.AsHooks("fa"); !ok {
		t.Error("expected full-agent Hooks")
	}
	if tr, ok := reg.AsTranscript("fa"); !ok || tr.ReadSessionMetadata("", "", "") != nil || tr.ResolveSessionTitle("", "", "") != "" {
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
	if _, ok := reg.AsStreamParserFactory("fa"); ok {
		t.Error("did not expect full-agent StreamParserFactory")
	}
	if _, ok := reg.AsModelResolver("fa"); ok {
		t.Error("did not expect full-agent ModelResolver")
	}
	if _, ok := reg.AsTranscriptStatter("fa"); ok {
		t.Error("did not expect full-agent TranscriptStatter")
	}
	if _, ok := reg.AsTranscriptUsageInspector("fa"); ok {
		t.Error("did not expect full-agent TranscriptUsageInspector")
	}
	if _, ok := reg.AsCLIInspector("fa"); ok {
		t.Error("did not expect full-agent CLIInspector")
	}
}

type persistentWavePlugin struct {
	minimalPlugin
	BasePersistentHeadless
	BaseTranscripts
	BaseSessionRewinder
}

func (p *persistentWavePlugin) ResolveBinary() (string, error) { return p.key, nil }
func (p *persistentWavePlugin) BuildTurnSpec(in HeadlessTurnInput) (HeadlessTurnSpec, error) {
	return HeadlessTurnSpec{Binary: p.key, Mode: HeadlessModePersistent}, nil
}
func (p *persistentWavePlugin) ParseStreamEvent(line []byte) ([]ChatStreamEvent, error) {
	return nil, nil
}
func (p *persistentWavePlugin) ComputeUsage(sessionID, cwd, model string) (TranscriptUsage, bool) {
	return TranscriptUsage{}, false
}
func (p *persistentWavePlugin) FormatStdinTurn(in PersistentTurnInput) (string, error) {
	return in.Prompt, nil
}
func (p *persistentWavePlugin) AppendTranscript(r io.Reader, existing []TranscriptMessage) ([]TranscriptMessage, error) {
	_ = r
	return existing, nil
}

func TestRegistry_Wave45FacetsAndBaseDefaults(t *testing.T) {
	reg := NewRegistry()
	acpAgent := &fullStubPlugin{
		minimalPlugin: minimalPlugin{
			key:  "acp-agent",
			caps: Capabilities{SupportsHeadless: true, HeadlessMode: HeadlessModeACP},
		},
		mode: HeadlessModeACP,
	}
	gatedPersist := &persistentWavePlugin{
		minimalPlugin: minimalPlugin{
			key:  "gated-persist",
			caps: Capabilities{SupportsHeadless: false},
		},
	}
	persist := &persistentWavePlugin{
		minimalPlugin: minimalPlugin{
			key:     "persist-agent",
			aliases: []string{"pa"},
			caps:    Capabilities{SupportsHeadless: true, HeadlessMode: HeadlessModePersistent},
		},
	}
	for _, p := range []Plugin{acpAgent, gatedPersist, persist} {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Key(), err)
		}
	}

	if _, ok := reg.AsPersistentHeadless("acp-agent"); ok {
		t.Fatal("ACP plugin must not satisfy AsPersistentHeadless")
	}
	if _, ok := reg.AsPersistentHeadless("gated-persist"); ok {
		t.Fatal("SupportsHeadless=false must gate AsPersistentHeadless")
	}
	pd, ok := reg.AsPersistentHeadless("PA")
	if !ok || pd.Mode() != HeadlessModePersistent || pd.SupportsStdioControl() || pd.DefaultPermissionMode() != "" {
		t.Fatalf("AsPersistentHeadless('PA') mismatch: ok=%v pd=%+v", ok, pd)
	}
	if line, sup := pd.FormatSetModel("m1"); sup || line != "" {
		t.Fatalf("expected BasePersistentHeadless.FormatSetModel unsupported, got (%q, %v)", line, sup)
	}

	if _, ok := reg.AsIncrementalTranscript("acp-agent"); ok {
		t.Fatal("BaseTranscripts alone must not satisfy IncrementalTranscripts")
	}
	if _, ok := reg.AsIncrementalTranscript("pa"); !ok {
		t.Fatal("expected persist-agent to satisfy IncrementalTranscripts")
	}
	if _, ok := reg.AsSessionRewinder("acp-agent"); ok {
		t.Fatal("plugin without SessionRewinder must return false")
	}
	rw, ok := reg.AsSessionRewinder("pa")
	if !ok {
		t.Fatal("expected persist-agent to satisfy SessionRewinder")
	}
	if turns, err := rw.ListRewindTurns("/h", "/c", "n"); err != nil || turns != nil {
		t.Fatalf("BaseSessionRewinder.ListRewindTurns = (%v, %v)", turns, err)
	}
	if cloned, err := rw.CloneTranscriptBeforeTurn("/h", "/c", "n", 2, "n2"); err != nil || cloned {
		t.Fatalf("BaseSessionRewinder.CloneTranscriptBeforeTurn = (%v, %v)", cloned, err)
	}

	var mc BaseModelCatalog
	for _, blank := range []string{"", "   ", "default", "DEFAULT", "synthetic", "SYNTHETIC", "<synthetic>"} {
		if got := mc.NormalizeModelID(blank); got != "" {
			t.Errorf("NormalizeModelID(%q) = %q, want empty", blank, got)
		}
	}
	if got := mc.NormalizeModelID("  claude-opus-4-6  "); got != "claude-opus-4-6" {
		t.Errorf("NormalizeModelID valid = %q", got)
	}

	var bp BaseProvisioner
	if dirs := bp.SkillDirs("/home/u"); dirs != nil {
		t.Errorf("BaseProvisioner.SkillDirs = %v, want nil", dirs)
	}

	var ba BaseAccounts
	if dirs := ba.ProfileSkillDirs("/home/u", nil); dirs != nil {
		t.Errorf("ProfileSkillDirs(nil) = %v, want nil", dirs)
	}
	if dirs := ba.ProfileSkillDirs("/home/u", &AgentAccount{ConfigDir: "  "}); dirs != nil {
		t.Errorf("ProfileSkillDirs(blank) = %v, want nil", dirs)
	}
	tildeDirs := ba.ProfileSkillDirs("/home/u", &AgentAccount{ConfigDir: "~/profiles/work"})
	if len(tildeDirs) != 1 || tildeDirs[0] != filepath.Join("/home/u", "profiles", "work", "skills") {
		t.Errorf("ProfileSkillDirs(tilde) = %v", tildeDirs)
	}
	absDirs := ba.ProfileSkillDirs("/home/u", &AgentAccount{ConfigDir: "/opt/acct"})
	if len(absDirs) != 1 || absDirs[0] != filepath.Join("/opt/acct", "skills") {
		t.Errorf("ProfileSkillDirs(abs) = %v", absDirs)
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
