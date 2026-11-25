package agent

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

func (r *Registry) AsPersistentHeadless(key string) (PersistentHeadlessDriver, bool) {
	p, ok := r.Get(key)
	if !ok || !p.Capabilities().SupportsHeadless {
		return nil, false
	}
	d, ok := p.(PersistentHeadlessDriver)
	if !ok || d.Mode() != HeadlessModePersistent {
		return nil, false
	}
	return d, true
}

func (r *Registry) AsIncrementalTranscript(key string) (IncrementalTranscripts, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(IncrementalTranscripts)
	return f, ok
}

func (r *Registry) AsSessionRewinder(key string) (SessionRewinder, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(SessionRewinder)
	return f, ok
}

func (r *Registry) AsSubagentBlockInspector(key string) (SubagentBlockInspector, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(SubagentBlockInspector)
	return f, ok
}

func (r *Registry) AsWorkspaceModelResolver(key string) (WorkspaceModelResolver, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(WorkspaceModelResolver)
	return f, ok
}

func (r *Registry) AsChecklistExtractor(key string) (ChecklistExtractor, bool) {
	p, ok := r.Get(key)
	if !ok {
		return nil, false
	}
	f, ok := p.(ChecklistExtractor)
	return f, ok
}
