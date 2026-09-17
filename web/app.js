// Ackbar GUI Application Client
(function() {
  'use strict';

  function getAuthToken() {
    const urlParams = new URLSearchParams(window.location.search);
    const urlToken = urlParams.get('token');
    if (urlToken) {
      localStorage.setItem('ackbar_token', urlToken);
      return urlToken;
    }
    return localStorage.getItem('ackbar_token') || '';
  }

  function setAuthToken(token) {
    if (token) {
      localStorage.setItem('ackbar_token', token);
    } else {
      localStorage.removeItem('ackbar_token');
    }
  }

  // Intercept window.fetch to automatically inject auth headers and handle 401
  const originalFetch = window.fetch;
  window.fetch = function(input, init = {}) {
    const token = getAuthToken();
    if (token) {
      init.headers = init.headers || {};
      if (init.headers instanceof Headers) {
        if (!init.headers.has('Authorization')) init.headers.set('Authorization', `Bearer ${token}`);
        if (!init.headers.has('X-Ackbar-Token')) init.headers.set('X-Ackbar-Token', token);
      } else if (Array.isArray(init.headers)) {
        init.headers.push(['Authorization', `Bearer ${token}`]);
        init.headers.push(['X-Ackbar-Token', token]);
      } else {
        init.headers['Authorization'] = init.headers['Authorization'] || `Bearer ${token}`;
        init.headers['X-Ackbar-Token'] = init.headers['X-Ackbar-Token'] || token;
      }
    }
    return originalFetch(input, init).then(res => {
      if (res.status === 401 && !window.__promptingToken) {
        window.__promptingToken = true;
        const promptToken = prompt('Ackbar Daemon requires an API Authentication Token:');
        window.__promptingToken = false;
        if (promptToken) {
          setAuthToken(promptToken.trim());
          location.reload();
        }
      }
      return res;
    });
  };

  function configureMarked() {
    if (window.marked) {
      if (typeof window.marked.use === 'function') {
        window.marked.use({ breaks: true, gfm: true });
      } else if (typeof window.marked.setOptions === 'function') {
        window.marked.setOptions({ breaks: true, gfm: true });
      }
    }
  }
  configureMarked();

  function renderMarkdown(content) {
    if (!window.marked) {
      return `<pre>${escapeHtml(content || '')}</pre>`;
    }
    configureMarked();
    return window.marked.parse(content || '');
  }

  function ensureDoubleNewlineSeparation(prev, next) {
    if (!prev) return next || '';
    if (!next) return prev || '';
    const prevEndsWithNL = prev.endsWith('\n\n') ? 2 : (prev.endsWith('\n') ? 1 : 0);
    const nextStartsWithNL = next.startsWith('\n\n') ? 2 : (next.startsWith('\n') ? 1 : 0);
    const totalNL = prevEndsWithNL + nextStartsWithNL;
    if (totalNL >= 2) {
      return prev + next;
    }
    if (totalNL === 1) {
      return prev + '\n' + next;
    }
    return prev + '\n\n' + next;
  }

  function loadCollapsedGroups() {
    try {
      const saved = localStorage.getItem('ackbar_collapsed_groups');
      if (saved) {
        return new Set(JSON.parse(saved));
      }
    } catch (e) {
      console.error('Failed to load collapsed groups from localStorage:', e);
    }
    return new Set();
  }

  function saveCollapsedGroups() {
    try {
      localStorage.setItem('ackbar_collapsed_groups', JSON.stringify(Array.from(state.collapsedGroups)));
    } catch (e) {
      console.error('Failed to save collapsed groups to localStorage:', e);
    }
  }

  function loadCollapsedDoneGroups() {
    try {
      const saved = localStorage.getItem('ackbar_collapsed_done_groups');
      if (saved) {
        return new Set(JSON.parse(saved));
      }
    } catch (e) {
      console.error('Failed to load collapsed done groups from localStorage:', e);
    }
    return new Set();
  }

  function loadExpandedDoneGroups() {
    try {
      const saved = localStorage.getItem('ackbar_expanded_done_groups');
      if (saved) {
        return new Set(JSON.parse(saved));
      }
    } catch (e) {
      console.error('Failed to load expanded done groups from localStorage:', e);
    }
    return new Set();
  }

  function saveCollapsedDoneGroups() {
    try {
      localStorage.setItem('ackbar_collapsed_done_groups', JSON.stringify(Array.from(state.collapsedDoneGroups)));
      localStorage.setItem('ackbar_expanded_done_groups', JSON.stringify(Array.from(state.expandedDoneGroups)));
    } catch (e) {
      console.error('Failed to save collapsed done groups to localStorage:', e);
    }
  }

  // Application State
  const state = {
    version: '...',
    selfHost: { name: 'local', displayName: '', url: '', online: true, isSelf: true },
    settings: null,
    sessions: [],
    treeNodes: [],
    hosts: [],
    openTabs: new Map(), // tabId -> { type: 'terminal'|'details'|'doc', session, terminal, fitAddon, socket, containerEl, tabEl }
    activeTabId: null,
    providers: [],
    collapsedGroups: loadCollapsedGroups(),
    collapsedDoneGroups: loadCollapsedDoneGroups(),
    expandedDoneGroups: loadExpandedDoneGroups(),
    searchQuery: '',
    showArchived: false,
    draggedSession: null,
    contextMenuSession: null,
    contextMenuGroupPath: null,
    contextMenuTabId: null,
    lastActiveGroup: null,
    cmdPaletteSelectedIndex: 0,
    cmdPaletteItems: []
  };

  window.state = state;
  window.__ackbarState = state;

  function isLocalHost(hostName) {
    if (!hostName || hostName === 'local') return true;
    if (state.selfHost && (hostName === state.selfHost.name || hostName === state.selfHost.displayName)) return true;
    return false;
  }

  function findHostRecord(hostName) {
    if (!hostName || isLocalHost(hostName)) return null;
    return (state.hosts || []).find(h =>
      h.name === hostName ||
      h.name.endsWith('@' + hostName) ||
      (h.displayName && h.displayName.toLowerCase() === hostName.toLowerCase())
    ) || null;
  }

  function getSessionBaseUrl(sessionId, sessionObj) {
    const sess = sessionObj || (state.sessions && state.sessions.find(s => s.id === sessionId));
    if (!sess) return '';
    if (sess.hostUrl) return sess.hostUrl.replace(/\/$/, '');
    const sessionHost = sess.host || '';
    if (isLocalHost(sessionHost)) return '';
    const hostRec = findHostRecord(sessionHost);
    if (hostRec && hostRec.url && !isLocalHost(hostRec.name)) {
      return hostRec.url.replace(/\/$/, '');
    }
    return '';
  }

  function getSelfHostName() {
    return (state.selfHost && state.selfHost.name) ? state.selfHost.name : 'local';
  }

  function getSelfDisplayName() {
    return (state.selfHost && state.selfHost.displayName) ? state.selfHost.displayName : getSelfHostName();
  }

  // Format host name to server name or display name
  function formatHostLabel(hostName) {
    if (!hostName || isLocalHost(hostName)) return getSelfDisplayName();
    const hostRec = findHostRecord(hostName);
    if (hostRec && hostRec.displayName) return hostRec.displayName;
    const parts = hostName.split('@');
    return parts[parts.length - 1] || hostName;
  }

  // Format relative timestamp (e.g. "2m", "1h", "yesterday", "Aug 18")
  function formatRelativeTime(dateStr) {
    if (!dateStr) return '';
    const d = new Date(dateStr);
    if (isNaN(d.getTime()) || d.getTime() === 0) return '';
    const now = new Date();
    const diffSec = Math.floor((now - d) / 1000);
    if (diffSec < 0) return 'just now';
    if (diffSec < 60) return `${diffSec}s`;
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m`;
    const diffHour = Math.floor(diffMin / 60);
    if (diffHour < 24) return `${diffHour}h`;
    const diffDays = Math.floor(diffHour / 24);
    if (diffDays === 1) return 'yesterday';
    if (diffDays < 7) return `${diffDays}d`;
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  }

  // Format absolute date and time (e.g. "Aug 18, 2026, 10:45 AM")
  function formatFullDateTime(dateStr) {
    if (!dateStr) return '';
    const d = new Date(dateStr);
    if (isNaN(d.getTime()) || d.getTime() === 0) return '';
    return d.toLocaleString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    });
  }

  // Open Path in VS Code (Local or Remote)
  async function openInVSCode(cwd, host = 'local') {
    if (!cwd) {
      alert('Workspace directory is empty.');
      return;
    }
    const isRemote = !isLocalHost(host);
    const hostLabel = isRemote ? (host || 'local') : getSelfHostName();
    const formattedPath = cwd.startsWith('/') ? cwd : '/' + cwd;
    const directUri = isRemote 
      ? `vscode://vscode-remote/ssh-remote+${hostLabel}${formattedPath}` 
      : `vscode://file${formattedPath}`;

    try {
      const res = await fetch('/v1/editor/open', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: cwd, host: isRemote ? host : getSelfHostName() })
      });
      if (res.ok) {
        const data = await res.json().catch(() => ({}));
        const targetUri = data.uri || directUri;
        // Direct anchor click to ensure browser triggers URL handler
        const a = document.createElement('a');
        a.href = targetUri;
        a.style.display = 'none';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      } else {
        window.location.href = directUri;
      }
    } catch (err) {
      window.location.href = directUri;
    }
  }

  // Fetch Providers
  async function fetchProviders() {
    try {
      const res = await fetch('/v1/providers');
      if (res.ok) {
        state.providers = await res.json() || [];
      }
    } catch (e) {
      console.warn('Failed to fetch providers:', e);
    }
  }

  // Session State Helpers
  function getStateText(session) {
    if (!session) return 'Unknown';
    switch (session.state) {
      case 1: return 'Working';
      case 2: return 'Blocked';
      case 3: return 'Idle';
      case 4: return 'Ended';
      case 5: return 'Failed';
      default: return session.managed ? 'Idle' : 'Standby';
    }
  }

  function getStateRawEmoji(session) {
    if (!session) return '◌';
    switch (session.state) {
      case 1: return '⚙️';
      case 2: return '❓';
      case 3: return '✅';
      case 4: return '⏹️';
      case 5: return '🛑';
      default: return session.managed ? '✅' : '◌';
    }
  }

  function getStateEmoji(session) {
    if (!session) return '<span title="Standby / Unknown">◌</span>';
    switch (session.state) {
      case 1: // StateWorking (work in progress - agent is working)
        return '<span class="state-working-spinner" title="Working (generating / executing tools)">⚙️</span>';
      case 2: // StateBlocked (waiting for feedback - agent is asking a question and waiting for response)
        return '<span title="Blocked (waiting for feedback / authorization)">❓</span>';
      case 3: // StateIdle (idle - completed work and not waiting for anything)
        return '<span title="Idle (turn completed, awaiting next prompt)">✅</span>';
      case 4: // StateEnded (session terminated / exited)
        return '<span title="Ended (process terminated)">⏹️</span>';
      case 5: // StateFailed (process crashed or error)
        return '<span title="Failed (process error / crashed)">🛑</span>';
      default:
        return session.managed ? '<span title="Idle">✅</span>' : '<span title="Standby / Unknown">◌</span>';
    }
  }

  // Agent Provider Badge Helper
  function getAgentBadgeHtml(agent, iconOnly = false) {
    const a = (agent || 'claude-code').toLowerCase();

    // 1. Dynamic lookup from registered providers
    if (state.providers && state.providers.length > 0) {
      const p = state.providers.find(prov => prov.agent.toLowerCase() === a || a.includes(prov.agent.toLowerCase()));
      if (p) {
        const svg = p.icon_svg || `<svg class="agent-logo-svg" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="16" rx="2"/></svg>`;
        return `<span class="agent-icon-badge ${p.agent}" title="${p.display_name || p.agent}">${svg}${iconOnly ? '' : ' ' + (p.display_name || p.agent)}</span>`;
      }
    }

    // 2. Built-in Fallbacks
    if (a.includes('claude')) {
      const svg = `<svg class="agent-logo-svg claude-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path clip-rule="evenodd" fill-rule="evenodd" d="M20.998 10.949H24v3.102h-3v3.028h-1.487V20H18v-2.921h-1.487V20H15v-2.921H9V20H7.488v-2.921H6V20H4.487v-2.921H3V14.05H0V10.95h3V5h17.998v5.949zM6 10.949h1.488V8.102H6v2.847zm10.51 0H18V8.102h-1.49v2.847z"/></svg>`;
      return `<span class="agent-icon-badge claude-code" title="Claude Code (Anthropic)">${svg}${iconOnly ? '' : ' claude'}</span>`;
    } else if (a.includes('antigravity') || a.includes('agy') || a.includes('gemini')) {
      const svg = `<svg class="agent-logo-svg antigravity-logo" viewBox="0 0 24 24" width="12" height="12"><defs><linearGradient id="agGrad" x1="0%" y1="0%" x2="100%" y2="100%"><stop offset="0%" stop-color="#EA4335"/><stop offset="35%" stop-color="#FBBC04"/><stop offset="70%" stop-color="#34A853"/><stop offset="100%" stop-color="#4285F4"/></linearGradient></defs><path fill="url(#agGrad)" d="M12 0.8 C8.2 0.8 5.6 3.2 4.1 7.8 C2.6 12.5 1.2 18.2 0.3 22.4 C-0.1 23.6 0.8 24.2 1.6 23.6 C4.8 21.2 7.8 14.2 12 12.4 C16.2 14.2 19.2 21.2 22.4 23.6 C23.2 24.2 24.1 23.6 23.7 22.4 C22.8 18.2 21.4 12.5 19.9 7.8 C18.4 3.2 15.8 0.8 12 0.8 Z"/></svg>`;
      return `<span class="agent-icon-badge antigravity" title="Google Antigravity">${svg}${iconOnly ? '' : ' antigravity'}</span>`;
    } else if (a.includes('codex') || a.includes('openai')) {
      const svg = `<svg class="agent-logo-svg codex-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a4.5 4.5 0 0 1 4.2 2.9l.4.9a4.5 4.5 0 0 1 1.8 4.7l-.2 1a4.5 4.5 0 0 1-2.4 4.1l-.8.5a4.5 4.5 0 0 1-4.7 1.8l-1-.2a4.5 4.5 0 0 1-4.1-2.4l-.5-.8a4.5 4.5 0 0 1-1.8-4.7l.2-1a4.5 4.5 0 0 1 2.4-4.1l.8-.5A4.5 4.5 0 0 1 12 2z"/><path d="M12 6.5l3.5 2v4l-3.5 2l-3.5-2v-4z"/><path d="M8.5 8.5L5 10.5M15.5 8.5L19 10.5M12 14.5V18.5"/></svg>`;
      return `<span class="agent-icon-badge codex" title="OpenAI Codex">${svg}${iconOnly ? '' : ' codex'}</span>`;
    }
    const svg = `<svg class="agent-logo-svg generic-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><line x1="7" y1="8" x2="10" y2="8"/><line x1="7" y1="12" x2="17" y2="12"/></svg>`;
    return `<span class="agent-icon-badge" title="Agent: ${agent}">${svg}${iconOnly ? '' : ' ' + agent}</span>`;
  }

  // DOM Elements
  const el = {
    appVersion: document.getElementById('appVersion'),
    hostList: document.getElementById('hostList'),
    searchInput: document.getElementById('searchInput'),
    treeContainer: document.getElementById('treeContainer'),
    tabStrip: document.getElementById('tabStrip'),
    terminalViewport: document.getElementById('terminalViewport'),
    emptyState: document.getElementById('emptyState'),
    tabOverflowDropdown: document.getElementById('tabOverflowDropdown'),
    tabOverflowCount: document.getElementById('tabOverflowCount'),
    overflowMenuContent: document.getElementById('overflowMenuContent'),
    btnTabOverflow: document.getElementById('btnTabOverflow'),
    btnNewTab: document.getElementById('btnNewTab'),
    btnAddHost: document.getElementById('btnAddHost'),
    btnNewProject: document.getElementById('btnNewProject'),
    btnPurge: document.getElementById('btnPurge'),
    btnSettings: document.getElementById('btnSettings'),
    btnRefreshPage: document.getElementById('btnRefreshPage'),
    btnDiscovery: document.getElementById('btnDiscovery'),
    btnToggleArchived: document.getElementById('btnToggleArchived'),
    btnCollapseAll: document.getElementById('btnCollapseAll'),
    btnExpandAll: document.getElementById('btnExpandAll'),
    sidebarPanel: document.getElementById('sidebarPanel'),
    sidebarResizer: document.getElementById('sidebarResizer'),
    // Statusbar elements
    sbSessionName: document.getElementById('sbSessionName'),
    sbHostBadge: document.getElementById('sbHostBadge'),
    sbCwd: document.getElementById('sbCwd'),
    sbGitBranch: document.getElementById('sbGitBranch'),
    sbContextGauge: document.getElementById('sbContextGauge'),
    sbModelBadge: document.getElementById('sbModelBadge'),
    sbPID: document.getElementById('sbPID'),
    sbModeToggle: document.getElementById('sbModeToggle'),
    sbLastActive: document.getElementById('sbLastActive'),
    // Dual Engine View Controls
    viewModeToggle: document.getElementById('viewModeToggle'),
    btnViewChat: document.getElementById('btnViewChat'),
    btnViewTerminal: document.getElementById('btnViewTerminal'),
    newSessionEngine: document.getElementById('newSessionEngine'),
    // Modal elements
    modalOverlay: document.getElementById('modalOverlay'),
    modalTitle: document.getElementById('modalTitle'),
    modalBody: document.getElementById('modalBody'),
    modalFooter: document.getElementById('modalFooter'),
    modalCloseBtn: document.getElementById('modalCloseBtn'),
    // Command Palette
    cmdPaletteOverlay: document.getElementById('cmdPaletteOverlay'),
    cmdPaletteInput: document.getElementById('cmdPaletteInput'),
    cmdPaletteResults: document.getElementById('cmdPaletteResults'),
    // Session Context Menu
    contextMenu: document.getElementById('contextMenu'),
    cmItemInfo: document.getElementById('cmItemInfo'),
    cmItemTranscript: document.getElementById('cmItemTranscript'),
    cmItemCopyName: document.getElementById('cmItemCopyName'),
    cmItemCopyPath: document.getElementById('cmItemCopyPath'),
    cmItemTakeWheel: document.getElementById('cmItemTakeWheel'),
    cmItemResume: document.getElementById('cmItemResume'),
    cmItemNewTab: document.getElementById('cmItemNewTab'),
    cmItemVSCode: document.getElementById('cmItemVSCode'),
    cmItemDocs: document.getElementById('cmItemDocs'),
    cmItemRestart: document.getElementById('cmItemRestart'),
    cmItemKill: document.getElementById('cmItemKill'),
    cmItemDone: document.getElementById('cmItemDone'),
    cmItemArchive: document.getElementById('cmItemArchive'),
    cmItemDelete: document.getElementById('cmItemDelete'),
    // Group Context Menu
    groupContextMenu: document.getElementById('groupContextMenu'),
    gcmItemNewSession: document.getElementById('gcmItemNewSession'),
    gcmItemNewSubgroup: document.getElementById('gcmItemNewSubgroup'),
    gcmItemVSCode: document.getElementById('gcmItemVSCode'),
    gcmItemDocs: document.getElementById('gcmItemDocs'),
    gcmItemDelete: document.getElementById('gcmItemDelete'),
    // Tab Context Menu
    tabContextMenu: document.getElementById('tabContextMenu'),
    tcmItemClose: document.getElementById('tcmItemClose'),
    tcmItemCloseOthers: document.getElementById('tcmItemCloseOthers'),
    tcmItemCloseRight: document.getElementById('tcmItemCloseRight'),
    tcmItemCloseAll: document.getElementById('tcmItemCloseAll')
  };

  // Initialize Application
  async function init() {
    setupEventListeners();
    await fetchVersion();
    await fetchSettings();
    await fetchProviders();
    await fetchHosts();
    await fetchTreeNodes();
    await fetchSessions();
    restorePersistedTabs();
    connectSSE();
    setupResizer();
    window.addEventListener('resize', () => {
      handleTabOverflow();
      const activeTab = state.openTabs.get(state.activeTabId);
      if (activeTab && activeTab.type === 'terminal' && activeTab.fitAddon) {
        try {
          activeTab.fitAddon.fit();
          if (activeTab.terminal && activeTab.socket && activeTab.socket.readyState === WebSocket.OPEN) {
            sendTerminalResize(activeTab.socket, activeTab.terminal.cols, activeTab.terminal.rows);
          }
        } catch (e) {}
      }
    });

    // Periodic Multi-Host Live Health Probe & Auto-Discovery every 6 seconds
    setInterval(() => {
      fetchHosts();
      connectSSE();
    }, 6000);
  }

  // Fetch Version
  async function fetchVersion() {
    try {
      const res = await fetch('/v1/version');
      if (res.ok) {
        const data = await res.json();
        state.version = data.version || 'unknown';
        if (el.appVersion) el.appVersion.textContent = `v${state.version}`;
        if (data.host) {
          state.selfHost = {
            name: data.host,
            displayName: data.display_name || '',
            url: '',
            online: true,
            isSelf: true
          };
          renderHosts();
        }
      }
    } catch (err) {
      console.warn('Failed to fetch version:', err);
    }
  }

  // Fetch and Live-Probe Hosts
  async function fetchHosts() {
    try {
      const res = await fetch('/v1/hosts');
      if (res.ok) {
        const rawHosts = await res.json() || [];
        const prevHosts = state.hosts || [];

        // Probe each host's live health & latency
        const probedHosts = await Promise.all(rawHosts.map(async (h) => {
          const fetchUrl = h.url ? `${h.url.replace(/\/$/, '')}/v1/version` : '/v1/version';
          const start = Date.now();
          try {
            const controller = new AbortController();
            const timer = setTimeout(() => controller.abort(), 6000);
            const vRes = await fetch(fetchUrl, { signal: controller.signal });
            clearTimeout(timer);
            if (vRes.ok) {
              const vData = await vRes.json();
              return {
                ...h,
                online: true,
                version: vData.version || 'online',
                displayName: vData.display_name || h.display_name || '',
                latencyMs: Date.now() - start
              };
            }
          } catch (e) {}
          // Fall back to daemon-reported live health if direct browser fetch timed out or failed
          if (h.online !== undefined) {
            return {
              ...h,
              online: !!h.online,
              version: h.version || 'online',
              displayName: h.display_name || h.displayName || '',
              latencyMs: h.latency_ms != null ? h.latency_ms : (h.latencyMs != null ? h.latencyMs : null)
            };
          }
          return {
            ...h,
            online: false,
            latencyMs: null
          };
        }));

        let hadHostStateChange = false;
        probedHosts.forEach(ph => {
          const prev = prevHosts.find(p => p.name === ph.name);
          if (prev && prev.online !== ph.online) {
            hadHostStateChange = true;
          }
        });

        state.hosts = probedHosts;
        renderHosts();

        if (hadHostStateChange) {
          fetchSessions();
          fetchTreeNodes();
        }
      }
    } catch (err) {
      console.warn('Failed to fetch hosts:', err);
    }
  }

  function renderHosts() {
    if (!el.hostList) return;
    el.hostList.innerHTML = '';
    const selfName = getSelfHostName();
    const selfDisp = getSelfDisplayName();
    const localHost = {
      name: selfName,
      displayName: selfDisp,
      url: '',
      online: true,
      isSelf: true
    };
    const remoteHosts = (state.hosts || []).filter(h => !isLocalHost(h.name));
    const hostsToRender = [localHost, ...remoteHosts];

    hostsToRender.forEach(h => {
      const span = document.createElement('span');
      span.className = `host-badge ${h.online ? 'host-online' : 'host-offline'}`;
      const statusDot = h.online ? '🟢' : '🔴';
      const label = h.displayName || formatHostLabel(h.name);
      span.textContent = `${label} ${statusDot}`;
      span.title = h.online
        ? `${label} is ONLINE (${h.latencyMs != null ? h.latencyMs + 'ms' : 'connected'}, v${h.version || '?'})`
        : `${label} is OFFLINE (Click to reconnect SSH tunnel / inspect)`;
      span.style.cursor = 'pointer';
      span.addEventListener('click', () => showHostSummaryModal(h));
      el.hostList.appendChild(span);
    });
  }

  // Fetch Tree Nodes across all available hosts
  async function fetchTreeNodes() {
    try {
      const targetHosts = [
        { name: getSelfHostName(), url: '' },
        ...(state.hosts || []).filter(h => h.url && !isLocalHost(h.name))
      ];

      const nodeArrays = await Promise.all(targetHosts.map(async (h) => {
        try {
          const fetchUrl = h.url ? `${h.url.replace(/\/$/, '')}/v1/nodes` : '/v1/nodes';
          const res = await fetch(fetchUrl);
          if (res.ok) {
            return await res.json() || [];
          }
        } catch (e) {}
        return [];
      }));

      const rawNodes = nodeArrays.flat();
      const nodeMap = new Map();
      rawNodes.forEach(n => {
        if (n && n.path) {
          if (!nodeMap.has(n.path) || (!nodeMap.get(n.path).project_dir && n.project_dir)) {
            nodeMap.set(n.path, n);
          }
        }
      });
      state.treeNodes = Array.from(nodeMap.values());
    } catch (err) {
      console.warn('Failed to fetch tree nodes:', err);
    }
  }

  const lastReconnectAttempt = {};

  // Client-Side Multi-Host Session Aggregation & Deduplication
  async function fetchSessions() {
    try {
      const targetHosts = [
        { name: getSelfHostName(), url: '' },
        ...state.hosts.filter(h => h.url && !isLocalHost(h.name))
      ];

      const sessionArrays = await Promise.all(targetHosts.map(async (h) => {
        try {
          const fetchUrl = h.url ? `${h.url.replace(/\/$/, '')}/v1/sessions` : '/v1/sessions';
          const res = await fetch(fetchUrl);
          if (res.ok) {
            const list = await res.json() || [];
            return list.map(s => ({
              ...s,
              host: !isLocalHost(h.name) ? h.name : (s.host || getSelfHostName()),
              hostUrl: h.url || ''
            }));
          }
        } catch (e) {
          console.warn(`Host ${h.name} unreachable:`, e);
          const now = Date.now();
          const lastAttempt = lastReconnectAttempt[h.name] || 0;
          if (now - lastAttempt > 15000 && h.url && (h.url.includes('127.0.0.1') || h.url.includes('localhost'))) {
            lastReconnectAttempt[h.name] = now;
            fetch('/v1/hosts/reconnect', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ name: h.name })
            }).catch(() => {});
          }
        }
        return [];
      }));

      const rawSessions = sessionArrays.flat();
      state.sessions = deduplicateSessions(rawSessions);
      renderTree();
      updateOpenTabsState();
    } catch (err) {
      console.error('Failed to fetch sessions:', err);
    }
  }

  // Helper: Get timestamp (ms) for latest interaction
  function getSessionTimestamp(s) {
    if (!s) return 0;
    const t = s.last_event_at || s.last_message_at || s.started_at;
    if (!t) return 0;
    const d = new Date(t);
    return isNaN(d.getTime()) ? 0 : d.getTime();
  }

  // Helper: Sort sessions descending by latest interaction (newest / active first)
  function sortSessionsByInteraction(list) {
    return [...(list || [])].sort((a, b) => {
      const timeA = getSessionTimestamp(a);
      const timeB = getSessionTimestamp(b);
      if (timeB !== timeA) {
        return timeB - timeA;
      }
      return (a.id || '').localeCompare(b.id || '');
    });
  }

  // Settings API
  async function fetchSettings() {
    try {
      const res = await fetch('/v1/settings');
      if (res.ok) {
        state.settings = await res.json();
      }
    } catch (err) {
      console.warn('Failed to fetch settings:', err);
      state.settings = {
        auto_done_enabled: 'true',
        auto_done_hours: '24',
        auto_archive_enabled: 'true',
        auto_archive_days: '7',
        done_collapsed_by_default: 'true'
      };
    }
  }

  async function updateSettings(newSettings) {
    try {
      const res = await fetch('/v1/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newSettings)
      });
      if (res.ok) {
        state.settings = await res.json();
        if (state.settings.host_name) state.selfHost.name = state.settings.host_name;
        if (state.settings.display_name !== undefined) state.selfHost.displayName = state.settings.display_name;
        renderHosts();
        renderTree();
        return true;
      }
    } catch (err) {
      console.error('Failed to save settings:', err);
    }
    return false;
  }

  // Session Done Evaluation (Dynamic Virtual Attribute)
  function isSessionDone(sess) {
    if (!sess) return false;
    if (sess.is_done) return true;

    const settings = state.settings || {};
    if (settings.auto_done_enabled === 'false') return false;

    // Active agent states (1 = Working, 2 = Question) are never auto-done
    if (sess.state === 1 || sess.state === 2) return false;

    const hours = parseFloat(settings.auto_done_hours || '24');
    if (isNaN(hours) || hours <= 0) return false;

    const ts = getSessionTimestamp(sess);
    if (!ts) return false;

    const inactivityMs = Date.now() - ts;
    return inactivityMs > hours * 3600 * 1000;
  }

  // Session Auto-Archive Evaluation
  function isSessionAutoArchived(sess) {
    if (!sess) return false;
    if (sess.archived) return true;

    const settings = state.settings || {};
    if (settings.auto_archive_enabled === 'false') return false;

    // Auto-archive only applies if session is Done
    if (!isSessionDone(sess)) return false;

    const days = parseFloat(settings.auto_archive_days || '7');
    if (isNaN(days) || days <= 0) return false;

    const ts = getSessionTimestamp(sess);
    if (!ts) return false;

    const inactivityMs = Date.now() - ts;
    return inactivityMs > days * 24 * 3600 * 1000;
  }

  // Control Session Done / Active State via Daemon API
  async function setSessionDoneState(sessionId, sessionHost, isDone) {
    try {
      const sess = state.sessions.find(s => s.id === sessionId);
      if (sess) {
        sess.is_done = isDone;
        renderTree();
      }
      const hostRec = (state.hosts || []).find(h => h.name === sessionHost);
      const baseUrl = (sess && sess.hostUrl) ? sess.hostUrl.replace(/\/$/, '') : (hostRec && hostRec.url && !isLocalHost(sessionHost) ? hostRec.url.replace(/\/$/, '') : '');
      const action = isDone ? 'done' : 'active';
      const url = `${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sessionId)}&action=${action}`;
      const res = await fetch(url, { method: 'POST' });
      if (res.ok) {
        await fetchSessions();
      }
    } catch (err) {
      console.error(`Failed to set session ${sessionId} done state:`, err);
    }
  }

  function isDoneSectionCollapsed(groupKey) {
    const settings = state.settings || {};
    const defaultCollapsed = settings.done_collapsed_by_default !== 'false';
    if (defaultCollapsed) {
      return !state.expandedDoneGroups.has(groupKey);
    } else {
      return state.collapsedDoneGroups.has(groupKey);
    }
  }

  function toggleDoneSection(groupKey, sectionEl) {
    const wasCollapsed = sectionEl.classList.contains('collapsed');
    if (wasCollapsed) {
      sectionEl.classList.remove('collapsed');
      state.collapsedDoneGroups.delete(groupKey);
      state.expandedDoneGroups.add(groupKey);
    } else {
      sectionEl.classList.add('collapsed');
      state.collapsedDoneGroups.add(groupKey);
      state.expandedDoneGroups.delete(groupKey);
    }
    saveCollapsedDoneGroups();
  }

  function renderDoneSection(groupKey, doneSessions) {
    const sectionEl = document.createElement('div');
    sectionEl.className = 'tree-done-section';

    const isCollapsed = isDoneSectionCollapsed(groupKey);
    if (isCollapsed) {
      sectionEl.classList.add('collapsed');
    }

    const headerEl = document.createElement('div');
    headerEl.className = 'tree-done-header';
    headerEl.title = `Done sessions in this group (${doneSessions.length}). Click to toggle. Drag sessions here to mark as Done.`;

    const chevron = document.createElement('span');
    chevron.className = 'tree-done-chevron';
    chevron.textContent = '▼';

    const title = document.createElement('span');
    title.className = 'tree-done-title';
    title.innerHTML = `✓ Done <span class="tree-done-count">(${doneSessions.length})</span>`;

    headerEl.appendChild(chevron);
    headerEl.appendChild(title);

    headerEl.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleDoneSection(groupKey, sectionEl);
    });

    headerEl.addEventListener('dragover', (e) => {
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = 'move';
      headerEl.classList.add('drag-over');
    });

    headerEl.addEventListener('dragleave', (e) => {
      e.stopPropagation();
      headerEl.classList.remove('drag-over');
    });

    headerEl.addEventListener('drop', async (e) => {
      e.preventDefault();
      e.stopPropagation();
      headerEl.classList.remove('drag-over');
      try {
        const data = JSON.parse(e.dataTransfer.getData('application/json'));
        if (data && data.sessionId) {
          if (groupKey !== 'Unassigned' && data.sessionPath !== groupKey) {
            await moveSessionToGroup(data.sessionId, data.sessionHost, groupKey);
          }
          await setSessionDoneState(data.sessionId, data.sessionHost, true);
        }
      } catch (err) {
        console.error('Done drop error:', err);
      }
    });

    const childrenListEl = document.createElement('div');
    childrenListEl.className = 'tree-done-children';
    doneSessions.forEach(sess => {
      childrenListEl.appendChild(createSessionRowElement(sess));
    });

    sectionEl.appendChild(headerEl);
    sectionEl.appendChild(childrenListEl);
    return sectionEl;
  }

  // Settings Modal Dialog
  function showSettingsModal() {
    const s = state.settings || {
      auto_done_enabled: 'true',
      auto_done_hours: '24',
      auto_archive_enabled: 'true',
      auto_archive_days: '7',
      done_collapsed_by_default: 'true'
    };

    const autoDoneEnabled = s.auto_done_enabled !== 'false';
    const autoDoneHours = s.auto_done_hours || '24';
    const autoArchiveEnabled = s.auto_archive_enabled !== 'false';
    const autoArchiveDays = s.auto_archive_days || '7';
    const doneCollapsed = s.done_collapsed_by_default !== 'false';

    const bodyHtml = `
      <div class="settings-modal-content">
        <div class="settings-section">
          <div class="settings-section-title"><span>🖥️</span> Host Identity & Display Name</div>
          <div class="settings-section-desc">Configure the canonical machine identifier and human-friendly display name for this machine across the Ackbar control plane.</div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingHostName">Host Identifier</label>
            <div class="settings-input-group" style="flex: 1; max-width: 250px;">
              <input type="text" id="settingHostName" class="form-input" value="${s.host_name || getSelfHostName()}" placeholder="e.g. macbook" />
            </div>
          </div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingDisplayName">Display Name (Optional Alias)</label>
            <div class="settings-input-group" style="flex: 1; max-width: 250px;">
              <input type="text" id="settingDisplayName" class="form-input" value="${s.display_name !== undefined ? s.display_name : (state.selfHost ? state.selfHost.displayName : '')}" placeholder="e.g. MacBook Air" />
            </div>
          </div>
        </div>

        <div class="settings-section">
          <div class="settings-section-title"><span>✓</span> Auto-Move to Done</div>
          <div class="settings-section-desc">Automatically move inactive sessions from the Active list into the per-group Done section. Submitting a prompt or active agent behavior automatically revives them back to Active.</div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingAutoDoneEnabled">Enable Auto-Move to Done</label>
            <input type="checkbox" id="settingAutoDoneEnabled" ${autoDoneEnabled ? 'checked' : ''} />
          </div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingAutoDoneHours">Inactivity Threshold</label>
            <div class="settings-input-group">
              <input type="number" id="settingAutoDoneHours" class="settings-number-input" min="1" max="720" value="${autoDoneHours}" />
              <span style="font-size: 12px; color: var(--text-muted);">hours</span>
            </div>
          </div>
        </div>

        <div class="settings-section">
          <div class="settings-section-title"><span>📦</span> Auto-Archive from Done</div>
          <div class="settings-section-desc">Automatically hide sessions that have remained in Done for an extended period. Archived sessions can still be viewed by toggling "Show Archived".</div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingAutoArchiveEnabled">Enable Auto-Archive</label>
            <input type="checkbox" id="settingAutoArchiveEnabled" ${autoArchiveEnabled ? 'checked' : ''} />
          </div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingAutoArchiveDays">Archive Inactivity Threshold</label>
            <div class="settings-input-group">
              <input type="number" id="settingAutoArchiveDays" class="settings-number-input" min="1" max="365" value="${autoArchiveDays}" />
              <span style="font-size: 12px; color: var(--text-muted);">days</span>
            </div>
          </div>
        </div>

        <div class="settings-section">
          <div class="settings-section-title"><span>📂</span> Done Section Presentation</div>
          <div class="settings-section-desc">Configure the initial display state of the Done section in the session sidebar tree.</div>
          <div class="settings-row">
            <label class="settings-row-label" for="settingDoneCollapsed">Start Done Sections Collapsed</label>
            <input type="checkbox" id="settingDoneCollapsed" ${doneCollapsed ? 'checked' : ''} />
          </div>
        </div>

        <div class="settings-section">
          <div class="settings-section-title"><span>👤</span> Agent Accounts & Profiles</div>
          <div class="settings-section-desc">Manage multi-account profiles for Claude Code and Antigravity across local and fleet machines.</div>
          <div id="settingsAccountsList" style="margin-top: 10px; font-size: 13px;">
            <em style="color: var(--text-muted);">Loading accounts...</em>
          </div>
          
          <div style="margin-top: 14px; padding-top: 10px; border-top: 1px dashed rgba(255,255,255,0.1);">
            <div style="font-size: 12px; font-weight: 600; margin-bottom: 8px; color: var(--text-dim);">Add New Account Profile</div>
            <div style="display: flex; gap: 8px; margin-bottom: 8px; flex-wrap: wrap;">
              <select id="newAccAgent" class="form-input" style="flex: 1; min-width: 120px;">
                <option value="claude-code">Claude Code</option>
                <option value="antigravity">Antigravity</option>
              </select>
              <input type="text" id="newAccName" class="form-input" placeholder="profile-name (e.g. work)" style="flex: 1; min-width: 140px;" />
              <input type="text" id="newAccDisplayName" class="form-input" placeholder="Display Name (optional)" style="flex: 1; min-width: 160px;" />
            </div>
            <div style="display: flex; gap: 8px; margin-bottom: 8px;">
              <input type="password" id="newAccApiKey" class="form-input" placeholder="Optional API Key (e.g. ANTHROPIC_API_KEY)" style="flex: 1;" />
            </div>
            <div style="display: flex; gap: 16px; align-items: center; margin-bottom: 10px; font-size: 12px; color: var(--text-dim);">
              <label style="display: flex; align-items: center; gap: 6px; cursor: pointer;">
                <input type="checkbox" id="newAccDefault" /> Set as Default
              </label>
              <label style="display: flex; align-items: center; gap: 6px; cursor: pointer;">
                <input type="checkbox" id="newAccAllHosts" /> Propagate to all connected fleet hosts
              </label>
            </div>
            <div style="display: flex; align-items: center; gap: 10px;">
              <button class="btn btn-secondary btn-sm" id="btnAddNewAccount">＋ Create Profile</button>
              <span id="accountAddStatus" style="font-size: 11px; color: var(--text-muted);"></span>
            </div>
          </div>
        </div>
      </div>
    `;

    const footerHtml = `
      <button class="btn btn-secondary" onclick="document.getElementById('modalOverlay').style.display='none'">Cancel</button>
      <button class="btn btn-primary" id="btnSaveSettings">Save Settings</button>
    `;

    showModal('⚙️ Workspace & Lifecycle Settings', bodyHtml, footerHtml);

    // Render Accounts List
    async function renderSettingsAccountsList() {
      const container = document.getElementById('settingsAccountsList');
      if (!container) return;
      try {
        const res = await fetch('/v1/accounts');
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const accounts = await res.json() || [];
        if (accounts.length === 0) {
          container.innerHTML = '<div style="color: var(--text-muted);">No accounts configured.</div>';
          return;
        }
        let html = '<div style="display: flex; flex-direction: column; gap: 6px;">';
        accounts.forEach(a => {
          const isDef = a.is_default;
          const logStatus = a.is_logged_in ? '<span style="color: var(--accent-green); font-size: 11px;">🟢 Logged In</span>' : '<span style="color: var(--text-muted); font-size: 11px;">⚪ Not Logged In</span>';
          const defBadge = isDef ? '<span style="background: rgba(34,197,94,0.15); color: var(--accent-green); font-size: 10px; padding: 1px 5px; border-radius: 3px; margin-left: 4px;">DEFAULT</span>' : '';
          html += `
            <div style="display: flex; align-items: center; justify-content: space-between; padding: 6px 10px; background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.06); border-radius: 4px;">
              <div>
                <span style="font-weight: 600; color: var(--text-bright);">${a.display_name || a.name}</span>
                <span style="color: var(--text-muted); font-size: 11px; margin-left: 6px;">(${a.agent}:${a.name})</span>
                ${defBadge}
                <div style="margin-top: 2px;">${logStatus}</div>
              </div>
              <div style="display: flex; gap: 6px;">
                ${!isDef ? `<button class="btn btn-secondary btn-sm btn-set-default" data-id="${a.id}" style="padding: 2px 7px; font-size: 11px;">Make Default</button>` : ''}
                ${a.name !== 'default' ? `<button class="btn btn-secondary btn-sm btn-del-acc" data-id="${a.id}" style="padding: 2px 7px; font-size: 11px; color: #ff5555;">Delete</button>` : ''}
              </div>
            </div>
          `;
        });
        html += '</div>';
        container.innerHTML = html;

        container.querySelectorAll('.btn-set-default').forEach(btn => {
          btn.addEventListener('click', async () => {
            const id = btn.getAttribute('data-id');
            await fetch(`/v1/accounts/${encodeURIComponent(id)}/default`, { method: 'POST' });
            renderSettingsAccountsList();
          });
        });

        container.querySelectorAll('.btn-del-acc').forEach(btn => {
          btn.addEventListener('click', async () => {
            const id = btn.getAttribute('data-id');
            if (confirm(`Delete account profile '${id}'?`)) {
              await fetch(`/v1/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' });
              renderSettingsAccountsList();
            }
          });
        });
      } catch (err) {
        container.innerHTML = `<div style="color: #ff5555;">Error loading accounts: ${err.message}</div>`;
      }
    }
    renderSettingsAccountsList();

    const btnAddAcc = document.getElementById('btnAddNewAccount');
    if (btnAddAcc) {
      btnAddAcc.addEventListener('click', async () => {
        const agent = document.getElementById('newAccAgent')?.value;
        const name = document.getElementById('newAccName')?.value?.trim();
        const displayName = document.getElementById('newAccDisplayName')?.value?.trim();
        const apiKey = document.getElementById('newAccApiKey')?.value?.trim();
        const isDefault = document.getElementById('newAccDefault')?.checked || false;
        const propagateAll = document.getElementById('newAccAllHosts')?.checked || false;
        const statusEl = document.getElementById('accountAddStatus');

        if (!name) {
          alert('Please enter a profile name (e.g. work).');
          return;
        }

        const env = {};
        if (apiKey) {
          if (agent === 'claude-code') env['ANTHROPIC_API_KEY'] = apiKey;
          else if (agent === 'antigravity') env['GEMINI_API_KEY'] = apiKey;
        }

        btnAddAcc.disabled = true;
        if (statusEl) statusEl.textContent = 'Creating...';

        try {
          const res = await fetch('/v1/accounts', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              agent,
              name,
              display_name: displayName,
              env,
              is_default: isDefault,
              propagate_all: propagateAll
            })
          });
          if (!res.ok) {
            const errBody = await res.text();
            throw new Error(errBody || `HTTP ${res.status}`);
          }
          if (statusEl) {
            statusEl.style.color = 'var(--accent-green)';
            statusEl.textContent = '✅ Created!';
          }
          document.getElementById('newAccName').value = '';
          document.getElementById('newAccDisplayName').value = '';
          document.getElementById('newAccApiKey').value = '';
          renderSettingsAccountsList();
        } catch (e) {
          if (statusEl) {
            statusEl.style.color = '#ff5555';
            statusEl.textContent = `❌ ${e.message}`;
          }
        } finally {
          btnAddAcc.disabled = false;
        }
      });
    }

    const btnSave = document.getElementById('btnSaveSettings');
    if (btnSave) {
      btnSave.addEventListener('click', async () => {
        btnSave.disabled = true;
        btnSave.textContent = 'Saving...';
        const newSettings = {
          host_name: document.getElementById('settingHostName')?.value?.trim() || getSelfHostName(),
          display_name: document.getElementById('settingDisplayName')?.value?.trim() || '',
          auto_done_enabled: document.getElementById('settingAutoDoneEnabled')?.checked ? 'true' : 'false',
          auto_done_hours: document.getElementById('settingAutoDoneHours')?.value?.trim() || '24',
          auto_archive_enabled: document.getElementById('settingAutoArchiveEnabled')?.checked ? 'true' : 'false',
          auto_archive_days: document.getElementById('settingAutoArchiveDays')?.value?.trim() || '7',
          done_collapsed_by_default: document.getElementById('settingDoneCollapsed')?.checked ? 'true' : 'false'
        };

        const ok = await updateSettings(newSettings);
        if (ok) {
          await fetchSessions();
          hideModal();
        } else {
          btnSave.disabled = false;
          btnSave.textContent = 'Save Failed - Retry';
        }
      });
    }
  }

  // Deduplication Algorithm (matches TUI mergeSessions)
  function deduplicateSessions(list) {
    const merged = new Map();

    list.forEach(sess => {
      let dedupKey = '';
      const host = isLocalHost(sess.host) ? getSelfHostName() : sess.host;
      const nativeId = sess.native_id || '';

      if (nativeId && !nativeId.startsWith('proc-')) {
        dedupKey = `${host}::native::${nativeId}`;
      } else if (sess.pid && sess.pid > 0) {
        dedupKey = `${host}::pid::${sess.pid}`;
      } else if (sess.tmux_name && sess.tmux_name.startsWith('ackbar-')) {
        const parts = sess.tmux_name.split('-');
        if (parts.length >= 3) {
          const suffix = parts.slice(2).join('-');
          dedupKey = `${host}::native::${suffix}`;
        } else {
          dedupKey = `${host}::tmux::${sess.tmux_name}`;
        }
      } else {
        dedupKey = `${host}::id::${sess.id || (sess.host + ':' + sess.native_id)}`;
      }

      if (!merged.has(dedupKey)) {
        merged.set(dedupKey, { ...sess });
      } else {
        const existing = merged.get(dedupKey);
        if (sess.name && !isRawSessionName(sess.name)) {
          existing.name = sess.name;
        } else if (!existing.name || isRawSessionName(existing.name)) {
          if (sess.name) existing.name = sess.name;
        }

        if (sess.managed || (sess.state && sess.state !== 4)) {
          existing.id = sess.id;
          existing.managed = true;
          existing.state = sess.state || existing.state;
          existing.pid = sess.pid || existing.pid;
          existing.tmux_name = sess.tmux_name || existing.tmux_name;
          existing.context_pct = sess.context_pct || existing.context_pct;
          existing.started_at = sess.started_at || existing.started_at;

          const sessTime = getSessionTimestamp(sess);
          const existingTime = getSessionTimestamp(existing);
          if (sessTime > existingTime) {
            existing.last_event_at = sess.last_event_at || existing.last_event_at;
          }

          existing.first_prompt = sess.first_prompt || existing.first_prompt;
          existing.last_prompt = sess.last_prompt || existing.last_prompt;
        }
      }
    });

    return sortSessionsByInteraction(Array.from(merged.values()));
  }

  function isRawSessionName(n) {
    if (!n) return true;
    let s = String(n).trim();
    if (s.endsWith(':')) s = s.slice(0, -1).trim();
    const lower = s.toLowerCase();
    if (lower === 'antigravity' || lower === 'claude-code' || lower === 'codex' || lower === 'cli' || lower === 'mock-agent') return true;
    if (lower.startsWith('ackbar-') || lower.startsWith('proc-')) return true;
    if (lower.startsWith('antigravity (') || lower.startsWith('claude-code (') || lower.startsWith('codex (') || lower.startsWith('claude code (')) return true;
    if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s)) return true;
    return false;
  }

  // Server-Sent Events (SSE) Multi-Host Stream for Live Updates
  const activeEventSources = new Map();

  function connectSSE() {
    const hostsToConnect = [
      { name: getSelfHostName(), url: '' },
      ...(state.hosts || []).filter(h => h.url && !isLocalHost(h.name))
    ];

    hostsToConnect.forEach((h) => {
      let sseUrl = h.url ? `${h.url.replace(/\/$/, '')}/v1/events` : '/v1/events';
      const token = getAuthToken();
      if (token) {
        sseUrl += (sseUrl.includes('?') ? '&' : '?') + `token=${encodeURIComponent(token)}`;
      }
      if (activeEventSources.has(h.name)) return;

      try {
        const sse = new EventSource(sseUrl);
        activeEventSources.set(h.name, sse);

        sse.onmessage = (event) => {
          try {
            const updatedSess = JSON.parse(event.data);
            if (!updatedSess || !updatedSess.id) return;

            if (!isLocalHost(h.name)) {
              updatedSess.host = h.name;
              updatedSess.hostUrl = h.url || '';
            } else if (!updatedSess.host || updatedSess.host === 'local') {
              updatedSess.host = getSelfHostName();
            }

            if (updatedSess.deleted || updatedSess.activity === 'Deleted') {
              const targetId = updatedSess.id;
              const targetName = updatedSess.name;
              const nativeId = updatedSess.native_id;
              state.sessions = state.sessions.filter(s => s.id !== targetId && s.name !== targetName && (!nativeId || s.native_id !== nativeId));
              closeTab(targetId);
              if (nativeId) closeTab(nativeId);
              closeTab(`details_${targetId}`);
              if (nativeId) closeTab(`details_${nativeId}`);
              for (const [tId, tabObj] of state.openTabs.entries()) {
                if (tabObj.session && (tabObj.session.id === targetId || (nativeId && tabObj.session.native_id === nativeId))) {
                  closeTab(tId);
                }
              }
              renderTree();
              return;
            }

            const idx = state.sessions.findIndex(s => s.id === updatedSess.id);
            if (idx !== -1) {
              const existing = state.sessions[idx];
              const mergedName = (!isRawSessionName(updatedSess.name) || isRawSessionName(existing.name)) ? (updatedSess.name || existing.name) : existing.name;
              state.sessions[idx] = { ...existing, ...updatedSess, name: mergedName };
            } else {
              state.sessions.push(updatedSess);
            }

            state.sessions = deduplicateSessions(state.sessions);
            renderTree();
            updateOpenTabsState();
          } catch (e) {
            console.error(`[SSE ${h.name}] parse error:`, e);
          }
        };

        sse.onerror = () => {
          try { sse.close(); } catch (e) {}
          activeEventSources.delete(h.name);
        };
      } catch (e) {
        console.warn(`[SSE ${h.name}] failed to connect:`, e);
      }
    });
  }

  // Render Sidebar Tree Hierarchy with Recursive Group Nesting & Drag-and-Drop
  function renderTree() {
    if (!el.treeContainer) return;
    el.treeContainer.innerHTML = '';

    const query = state.searchQuery.toLowerCase().trim();

    // Pre-discover known group paths from registered tree nodes
    const knownGroupPaths = new Set();
    state.treeNodes.forEach(node => {
      if (node.path) {
        const parts = node.path.split('/');
        for (let i = 1; i <= parts.length; i++) {
          knownGroupPaths.add(parts.slice(0, i).join('/'));
        }
      }
    });

    // 1. Group sessions by exact logical node path
    const sessionsByPath = new Map();
    const unassigned = [];

    state.sessions.forEach(sess => {
      if ((sess.archived || isSessionAutoArchived(sess)) && !state.showArchived) {
        return;
      }

      if (query) {
        const match = (sess.name && sess.name.toLowerCase().includes(query)) ||
                      (sess.cwd && sess.cwd.toLowerCase().includes(query)) ||
                      (sess.host && sess.host.toLowerCase().includes(query)) ||
                      (sess.agent && sess.agent.toLowerCase().includes(query));
        if (!match) return;
      }

      let assignedPath = sess.node_path;
      if (!assignedPath && (sess.cwd || sess.git_remote)) {
        for (const n of state.treeNodes) {
          // 1. Match by Git URL across any host
          if (n.git_url && sess.git_remote && (sess.git_remote === n.git_url || normalizeGitURL(sess.git_remote) === normalizeGitURL(n.git_url))) {
            assignedPath = n.path;
            break;
          }
          // 2. Match by exact workspace prefix
          if (n.project_dir && (sess.cwd === n.project_dir || sess.cwd.startsWith(n.project_dir + '/'))) {
            assignedPath = n.path;
            break;
          }
          // 3. Cross-machine basename match (e.g. /home/dev4u/Work/ngl-android -> Modemobile/NGL/ngl-android)
          if (n.path && sess.cwd) {
            const groupLeaf = n.path.split('/').pop().toLowerCase();
            const cwdParts = sess.cwd.toLowerCase().split('/');
            if (cwdParts.includes(groupLeaf) && groupLeaf.length > 3) {
              assignedPath = n.path;
              break;
            }
          }
        }

        // 4. Fallback: match by ancestor group path component (e.g. /home/dev4u/Work/modemobile/... -> Modemobile)
        if (!assignedPath && sess.cwd) {
          const cwdParts = sess.cwd.toLowerCase().split('/').filter(Boolean);
          for (const groupPath of knownGroupPaths) {
            const topGroup = groupPath.split('/')[0];
            if (topGroup.length > 3 && cwdParts.includes(topGroup.toLowerCase())) {
              assignedPath = topGroup;
              break;
            }
          }
        }
      }

      if (assignedPath) {
        if (!sessionsByPath.has(assignedPath)) {
          sessionsByPath.set(assignedPath, []);
        }
        sessionsByPath.get(assignedPath).push(sess);
      } else {
        unassigned.push(sess);
      }
    });

    // 2. Discover all unique group paths and their parent prefixes
    const allGroupPaths = new Set();

    state.treeNodes.forEach(node => {
      if (node.path) {
        const parts = node.path.split('/');
        for (let i = 1; i <= parts.length; i++) {
          allGroupPaths.add(parts.slice(0, i).join('/'));
        }
      }
    });

    sessionsByPath.forEach((sessList, path) => {
      if (path && sessList.length > 0) {
        const parts = path.split('/');
        for (let i = 1; i <= parts.length; i++) {
          allGroupPaths.add(parts.slice(0, i).join('/'));
        }
      }
    });

    const sortedGroupPaths = Array.from(allGroupPaths).sort();

    // 3. Helper to recursively render a group node and its nested subgroups
    function renderGroupBranch(path, indentLevel) {
      const parts = path.split('/');
      const leafName = parts[parts.length - 1];
      const isCollapsed = query ? false : state.collapsedGroups.has(path);

      const groupEl = document.createElement('div');
      groupEl.className = `tree-group-node ${isCollapsed ? 'collapsed' : ''}`;

      const headerEl = document.createElement('div');
      headerEl.className = 'tree-group-header';
      headerEl.dataset.groupPath = path;
      headerEl.title = `Group: ${path} (Click to toggle, right-click for options)`;

      // Drop Target Handlers
      headerEl.addEventListener('dragover', (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        headerEl.classList.add('drag-over');
      });

      headerEl.addEventListener('dragleave', () => {
        headerEl.classList.remove('drag-over');
      });

      headerEl.addEventListener('drop', async (e) => {
        e.preventDefault();
        headerEl.classList.remove('drag-over');
        try {
          const data = JSON.parse(e.dataTransfer.getData('application/json'));
          if (data && data.sessionId) {
            await moveSessionToGroup(data.sessionId, data.sessionHost, path);
            const sess = state.sessions.find(s => s.id === data.sessionId);
            if (sess && isSessionDone(sess)) {
              await setSessionDoneState(data.sessionId, data.sessionHost, false);
            }
          }
        } catch (err) {
          console.error('Drop error:', err);
        }
      });

      headerEl.addEventListener('click', () => {
        state.lastActiveGroup = path;
        if (state.collapsedGroups.has(path)) {
          state.collapsedGroups.delete(path);
        } else {
          state.collapsedGroups.add(path);
        }
        saveCollapsedGroups();
        renderTree();
      });

      headerEl.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        e.stopPropagation();
        showGroupContextMenu(e.clientX, e.clientY, path);
      });

      const chevron = document.createElement('span');
      chevron.className = 'tree-group-chevron';
      chevron.textContent = '▼';

      const title = document.createElement('span');
      title.className = 'tree-group-title';
      title.textContent = indentLevel === 0 ? path : leafName;

      headerEl.appendChild(chevron);
      headerEl.appendChild(title);
      groupEl.appendChild(headerEl);

      // Children Container
      const childrenEl = document.createElement('div');
      childrenEl.className = 'tree-group-children';

      // 3a. Direct Sessions matching this exact path (partitioned into active and done)
      const directSessions = sortSessionsByInteraction(sessionsByPath.get(path) || []);
      const activeSessions = [];
      const doneSessions = [];

      directSessions.forEach(sess => {
        if (isSessionDone(sess)) {
          doneSessions.push(sess);
        } else {
          activeSessions.push(sess);
        }
      });

      activeSessions.forEach(sess => {
        childrenEl.appendChild(createSessionRowElement(sess));
      });

      // 3b. Done Subsection (if any sessions in Done state)
      if (doneSessions.length > 0) {
        childrenEl.appendChild(renderDoneSection(path, doneSessions));
      }

      // 3c. Direct Child Subgroups (nested recursive call)
      let childSubgroupsCount = 0;
      sortedGroupPaths.forEach(childPath => {
        if (childPath.startsWith(path + '/')) {
          const subparts = childPath.substring(path.length + 1).split('/');
          if (subparts.length === 1) { // direct descendant
            childSubgroupsCount++;
            const childEl = renderGroupBranch(childPath, indentLevel + 1);
            childrenEl.appendChild(childEl);
          }
        }
      });

      // 3d. Empty group placeholder hint
      if (activeSessions.length === 0 && doneSessions.length === 0 && childSubgroupsCount === 0) {
        const emptyHint = document.createElement('div');
        emptyHint.className = 'tree-group-empty-hint';
        emptyHint.textContent = 'Empty group (drag sessions here or right-click to spawn)';
        childrenEl.appendChild(emptyHint);
      }

      groupEl.appendChild(childrenEl);
      return groupEl;
    }

    // 4. Render Root Groups (paths without "/")
    sortedGroupPaths.forEach(path => {
      if (!path.includes('/')) {
        el.treeContainer.appendChild(renderGroupBranch(path, 0));
      }
    });

    // 5. Render Unassigned Group if non-empty (sorted by newest/active interaction first)
    if (unassigned.length > 0) {
      const unassignedPath = 'Unassigned';
      const isCollapsed = query ? false : state.collapsedGroups.has(unassignedPath);

      const groupEl = document.createElement('div');
      groupEl.className = `tree-group-node ${isCollapsed ? 'collapsed' : ''}`;

      const headerEl = document.createElement('div');
      headerEl.className = 'tree-group-header';
      headerEl.textContent = '▼ Unassigned';
      headerEl.addEventListener('click', () => {
        if (state.collapsedGroups.has(unassignedPath)) {
          state.collapsedGroups.delete(unassignedPath);
        } else {
          state.collapsedGroups.add(unassignedPath);
        }
        saveCollapsedGroups();
        renderTree();
      });

      groupEl.appendChild(headerEl);

      const childrenEl = document.createElement('div');
      childrenEl.className = 'tree-group-children';
      const sortedUnassigned = sortSessionsByInteraction(unassigned);
      const activeUnassigned = [];
      const doneUnassigned = [];

      sortedUnassigned.forEach(sess => {
        if (isSessionDone(sess)) {
          doneUnassigned.push(sess);
        } else {
          activeUnassigned.push(sess);
        }
      });

      activeUnassigned.forEach(sess => {
        childrenEl.appendChild(createSessionRowElement(sess));
      });

      if (doneUnassigned.length > 0) {
        childrenEl.appendChild(renderDoneSection('Unassigned', doneUnassigned));
      }

      groupEl.appendChild(childrenEl);
      el.treeContainer.appendChild(groupEl);
    }
  }

  // Create Interactive Draggable Session Card with Agent Badge & Context Menu
  function createSessionRowElement(session) {
    const row = document.createElement('div');
    row.className = 'session-item';
    row.draggable = true;
    if (state.activeTabId === session.id) {
      row.classList.add('active-tab');
    }

    // Drag start event
    row.addEventListener('dragstart', (e) => {
      state.draggedSession = session;
      row.classList.add('dragging');
      e.dataTransfer.setData('application/json', JSON.stringify({
        sessionId: session.id,
        sessionHost: session.host || getSelfHostName(),
        sessionPath: session.node_path || ''
      }));
      e.dataTransfer.effectAllowed = 'move';
    });

    row.addEventListener('dragend', () => {
      row.classList.remove('dragging');
      state.draggedSession = null;
    });

    // Click: Open interactive terminal tab (connects/resumes session automatically)
    row.addEventListener('click', () => {
      openSessionInTab(session);
    });

    // Right-Click Context Menu
    row.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showContextMenu(e.clientX, e.clientY, session);
    });

    const isManaged = session.managed || (session.tmux_name && session.state !== 4);
    const sessionFullName = session.name || session.agent || 'Unnamed Session';
    
    // Tooltip on hover displays the complete un-truncated session name
    row.title = sessionFullName;

    const left = document.createElement('div');
    left.className = 'session-item-left';

    const dot = document.createElement('span');
    dot.className = 'session-state-dot';
    dot.innerHTML = getStateEmoji(session);
    const stateStr = session.state === 1 ? 'Work in progress (agent is working)' : (session.state === 2 ? 'Waiting for feedback (agent is asking a question)' : (session.state === 3 ? 'Idle (completed work, not waiting)' : (session.state === 4 ? 'Ended (process terminated)' : (session.state === 5 ? 'Failed (process error)' : 'Not connected / Status unknown'))));
    dot.title = `Status: ${stateStr}\nMode: ${isManaged ? 'Live tmux session' : 'Observed process/transcript'}`;

    if (session.is_unread) {
      row.classList.add('session-unread');
      const unreadDot = document.createElement('span');
      unreadDot.className = 'unread-dot';
      unreadDot.title = 'Unread state update (click to view)';
      left.appendChild(unreadDot);
    }

    if (session.archived) {
      row.classList.add('session-archived');
    }

    if (isSessionDone(session)) {
      row.classList.add('is-done');
    }

    const name = document.createElement('span');
    name.className = 'session-name';
    name.textContent = sessionFullName;
    name.title = sessionFullName;

    left.appendChild(dot);
    left.appendChild(name);

    const right = document.createElement('div');
    right.className = 'session-item-right';

    if (session.archived) {
      const archBadge = document.createElement('span');
      archBadge.className = 'badge-archived';
      archBadge.textContent = '📦';
      archBadge.title = 'Archived Session';
      right.appendChild(archBadge);
    }

    // Agent badge with provider icon (compact icon-only with tooltip)
    const agentBadge = document.createElement('span');
    agentBadge.innerHTML = getAgentBadgeHtml(session.agent, true);
    right.appendChild(agentBadge);

    // Account badge (omitted for default profile)
    const rawAccount = session.account_id ? session.account_id.split(':').pop() : '';
    if (rawAccount && rawAccount !== 'default') {
      const accBadge = document.createElement('span');
      accBadge.className = 'badge-account';
      accBadge.textContent = rawAccount;
      accBadge.title = `Profile: ${rawAccount}`;
      right.appendChild(accBadge);
    }

    if (session.context_pct > 0) {
      const ctxBadge = document.createElement('span');
      ctxBadge.className = 'badge-ctx';
      ctxBadge.textContent = `${session.context_pct}%`;
      ctxBadge.title = `Context Window Used: ${session.context_pct}%`;
      right.appendChild(ctxBadge);
    }

    if (session.host && !isLocalHost(session.host)) {
      const hostBadge = document.createElement('span');
      hostBadge.className = 'badge-host';
      hostBadge.textContent = `@${formatHostLabel(session.host)}`;
      hostBadge.title = `Host: @${formatHostLabel(session.host)}`;
      right.appendChild(hostBadge);
    }

    const timeRel = formatRelativeTime(session.last_event_at || session.started_at);
    if (timeRel) {
      const timeBadge = document.createElement('span');
      timeBadge.className = 'badge-time';
      const d = new Date(session.last_event_at || session.started_at);
      const diffHour = (new Date() - d) / (1000 * 60 * 60);
      if (diffHour < 1) timeBadge.classList.add('time-recent');
      else if (diffHour < 24) timeBadge.classList.add('time-today');
      timeBadge.textContent = timeRel;
      timeBadge.title = `Last Activity: ${formatFullDateTime(session.last_event_at || session.started_at)}`;
      right.appendChild(timeBadge);
    }

    row.appendChild(left);
    row.appendChild(right);
    return row;
  }

  // Move Session to Group via API
  async function moveSessionToGroup(sessionId, sessionHost, targetPath) {
    try {
      const sess = state.sessions.find(s => s.id === sessionId);
      const hostRec = (state.hosts || []).find(h => h.name === sessionHost);
      const baseUrl = (sess && sess.hostUrl) ? sess.hostUrl.replace(/\/$/, '') : (hostRec && hostRec.url && !isLocalHost(sessionHost) ? hostRec.url.replace(/\/$/, '') : '');
      const url = `${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sessionId)}&action=move&node_path=${encodeURIComponent(targetPath)}`;
      const res = await fetch(url, { method: 'POST' });
      if (res.ok) {
        if (sess && targetPath) {
          recordGroupSpawn({ group: targetPath, host: sess.host || sessionHost, agent: sess.agent, cwd: sess.cwd });
        }
        await fetchSessions();
      }
    } catch (err) {
      console.error('Failed to move session:', err);
    }
  }

  // Establish or Re-establish WebSocket Connection for a Terminal Tab
  function connectTerminalWebSocket(tab, tabId, term, fitAddon, session) {
    if (session && session.engine_type === 'headless' && !session.tmux_name) {
      return;
    }
    if (tab.pingTimer) {
      clearInterval(tab.pingTimer);
      tab.pingTimer = null;
    }
    if (tab.reconnectTimer) {
      clearTimeout(tab.reconnectTimer);
      tab.reconnectTimer = null;
    }
    if (tab.socket) {
      try {
        tab.socket.onopen = null;
        tab.socket.onclose = null;
        tab.socket.onerror = null;
        tab.socket.onmessage = null;
        tab.socket.close();
      } catch (e) {}
      tab.socket = null;
    }

    const hostParam = isLocalHost(session.host) ? getSelfHostName() : session.host;
    const wsBase = session.hostUrl ? session.hostUrl.replace(/^http/, 'ws').replace(/\/$/, '') : `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}`;
    let wsUrl = `${wsBase}/v1/sessions/pty?id=${encodeURIComponent(session.id)}&host=${encodeURIComponent(hostParam)}&cols=${term.cols}&rows=${term.rows}`;
    const token = getAuthToken();
    if (token) {
      wsUrl += `&token=${encodeURIComponent(token)}`;
    }

    try {
      const socket = new WebSocket(wsUrl);
      socket.binaryType = 'arraybuffer';
      tab.socket = socket;

      socket.onopen = () => {
        if (tab.reconnectTimer) {
          clearTimeout(tab.reconnectTimer);
          tab.reconnectTimer = null;
        }
        tab.reconnectAttempts = 0;
        fitAddon.fit();
        sendTerminalResize(socket, term.cols, term.rows);

        // Keepalive Ping from client every 15s to prevent NAT/proxy/sleep timeout
        tab.pingTimer = setInterval(() => {
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: 'ping' }));
          }
        }, 15000);
      };

      socket.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          const u8 = new Uint8Array(event.data);
          if (u8.length === 0) return;
          if (u8[0] === 123) {
            try {
              const text = new TextDecoder().decode(u8);
              if (text.startsWith('{"type":')) return;
            } catch (e) {}
          }
          const textPreview = new TextDecoder().decode(u8.slice(0, 100));
          if (textPreview.includes('[Session was deleted]')) {
            closeTab(tabId);
            return;
          }
          term.write(u8);
        } else if (typeof event.data === 'string') {
          if (!event.data || event.data.startsWith('{"type":')) return;
          if (event.data.includes('[Session was deleted]')) {
            closeTab(tabId);
            return;
          }
          term.write(event.data);
        } else {
          term.write(event.data);
        }
      };

      socket.onclose = () => {
        if (tab.pingTimer) {
          clearInterval(tab.pingTimer);
          tab.pingTimer = null;
        }

        // If session was deleted from state, close tab immediately without reconnecting
        const sessMatch = state.sessions.find(s => s.id === tabId || (tab.session && s.id === tab.session.id));
        if (!sessMatch || sessMatch.deleted) {
          closeTab(tabId);
          return;
        }

        // Only auto-reconnect if tab is still open
        if (state.openTabs.has(tabId)) {
          if (document.visibilityState === 'visible') {
            if (!tab.reconnectAttempts || tab.reconnectAttempts === 0) {
              term.write('\r\n\x1b[90m[Connection interrupted — Reconnecting...]\x1b[0m\r\n');
            }
            tab.reconnectAttempts = (tab.reconnectAttempts || 0) + 1;
            const delay = Math.min(10000, 1000 * Math.min(tab.reconnectAttempts, 5));
            tab.reconnectTimer = setTimeout(() => {
              if (state.openTabs.has(tabId)) {
                connectTerminalWebSocket(tab, tabId, term, fitAddon, session);
              }
            }, delay);
          } else {
            term.write('\r\n\x1b[90m[Session paused — Will resume when focused]\x1b[0m\r\n');
          }
        }
      };

      socket.onerror = (err) => {
        console.error(`[PTY] Tab ${tabId} socket error:`, err);
      };
    } catch (err) {
      console.error(`[PTY] Failed to create socket for ${tabId}:`, err);
    }
  }

  // Helper to show a floating upload toast notification
  function showUploadToast(message, type = 'info', duration = 3000) {
    const toast = document.createElement('div');
    toast.className = `upload-toast ${type}`;
    let icon = '📎';
    if (type === 'success') icon = '✅';
    else if (type === 'error') icon = '⚠️';
    toast.innerHTML = `<span>${icon}</span><span>${escapeHtml(message)}</span>`;
    document.body.appendChild(toast);
    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transform = 'translateY(12px)';
      setTimeout(() => toast.remove(), 200);
    }, duration);
    return toast;
  }

  // Insert quoted file path into prompt textarea at cursor position or append with clean spacing
  function insertPathIntoPromptInput(inputEl, filePath) {
    if (!inputEl) return;
    const quotedPath = `"${filePath}" `;
    const val = inputEl.value || '';
    const start = inputEl.selectionStart;
    const end = inputEl.selectionEnd;

    if (typeof start === 'number' && typeof end === 'number' && document.activeElement === inputEl) {
      const before = val.substring(0, start);
      const after = val.substring(end);
      const spaceBefore = (before.length > 0 && !before.endsWith(' ') && !before.endsWith('\n')) ? ' ' : '';
      inputEl.value = before + spaceBefore + quotedPath + after;
      const newPos = start + spaceBefore.length + quotedPath.length;
      inputEl.selectionStart = newPos;
      inputEl.selectionEnd = newPos;
    } else {
      const spaceBefore = (val.length > 0 && !val.endsWith(' ') && !val.endsWith('\n')) ? ' ' : '';
      inputEl.value = val + spaceBefore + quotedPath;
      inputEl.selectionStart = inputEl.value.length;
      inputEl.selectionEnd = inputEl.value.length;
    }

    inputEl.style.height = 'auto';
    inputEl.style.height = Math.min(inputEl.scrollHeight, 180) + 'px';
    inputEl.dispatchEvent(new Event('input', { bubbles: true }));
    inputEl.focus();
  }

  function getAttachmentIcon(filename, type) {
    const ext = (filename || '').split('.').pop().toLowerCase();
    if ((type && type.startsWith('image/')) || ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg'].includes(ext)) {
      return '🖼️';
    }
    if (ext === 'pdf') return '📑';
    if (['json', 'yaml', 'yml', 'csv'].includes(ext)) return '📊';
    if (['txt', 'log'].includes(ext)) return '📝';
    if (ext === 'md') return '📋';
    return '📄';
  }

  function removePendingAttachment(tabObj, id) {
    if (!tabObj || !tabObj.pendingAttachments) return;
    const idx = tabObj.pendingAttachments.findIndex(a => a.id === id);
    if (idx !== -1) {
      const att = tabObj.pendingAttachments[idx];
      if (att.previewUrl && typeof URL !== 'undefined' && URL.revokeObjectURL) {
        try { URL.revokeObjectURL(att.previewUrl); } catch (_) {}
      }
      tabObj.pendingAttachments.splice(idx, 1);
      renderPendingAttachments(tabObj);
    }
  }

  function renderPendingAttachments(tabObj) {
    if (!tabObj || !tabObj.chatComposerAttachments) return;
    const items = tabObj.pendingAttachments || [];

    if (items.length === 0) {
      tabObj.chatComposerAttachments.style.display = 'none';
      tabObj.chatComposerAttachments.innerHTML = '';
      return;
    }

    tabObj.chatComposerAttachments.style.display = 'flex';
    tabObj.chatComposerAttachments.innerHTML = '';

    items.forEach(att => {
      const chip = document.createElement('div');
      chip.className = `chat-attachment-chip ${att.status}`;
      chip.dataset.id = att.id;

      let thumbHtml = '';
      if (att.previewUrl) {
        thumbHtml = `<img class="chat-attachment-thumb" src="${att.previewUrl}" alt="${escapeHtml(att.filename)}" />`;
      } else {
        thumbHtml = `<span class="chat-attachment-icon">${getAttachmentIcon(att.filename, att.type)}</span>`;
      }

      let statusHtml = '';
      if (att.status === 'uploading') {
        statusHtml = `<span class="chat-attachment-spinner" title="Uploading...">⏳</span>`;
      } else if (att.status === 'error') {
        statusHtml = `<span class="chat-attachment-error-badge" title="${escapeHtml(att.error || 'Upload failed')}">⚠️</span>`;
      }

      chip.innerHTML = `
        ${thumbHtml}
        <div class="chat-attachment-info">
          <span class="chat-attachment-name" title="${escapeHtml(att.filename)}">${escapeHtml(att.filename)}</span>
          <span class="chat-attachment-size">${formatBytes(att.size)}</span>
        </div>
        ${statusHtml}
        <button class="chat-attachment-remove" type="button" title="Remove attachment" aria-label="Remove">&times;</button>
      `;

      const removeBtn = chip.querySelector('.chat-attachment-remove');
      if (removeBtn) {
        removeBtn.addEventListener('click', (e) => {
          e.stopPropagation();
          removePendingAttachment(tabObj, att.id);
        });
      }

      tabObj.chatComposerAttachments.appendChild(chip);
    });
  }

  async function addPendingAttachment(tabObj, file) {
    if (!file || !tabObj) return;

    tabObj.pendingAttachments = tabObj.pendingAttachments || [];

    const filename = file.name || (file.type && file.type.startsWith('image/') ? 'pasted_image.png' : 'attachment');
    const ext = filename.split('.').pop().toLowerCase();
    const allowed = ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg', 'pdf', 'txt', 'md', 'log', 'json', 'yaml', 'yml', 'csv'];
    const type = (file.type || '').toLowerCase();
    const isAllowedMime = type.startsWith('image/') || type === 'application/pdf' || type.includes('bmp') || type.startsWith('text/') || type === 'application/json';
    const isAllowedExt = allowed.includes(ext);

    if (!isAllowedMime && !isAllowedExt) {
      showUploadToast(`Unsupported file type: ${filename} (allowed: images, PDF, TXT, MD, LOG, JSON, YAML, CSV)`, 'error', 4000);
      return;
    }

    const attId = 'att_' + Date.now() + '_' + Math.random().toString(36).substring(2, 8);
    const isImg = type.startsWith('image/') || ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg'].includes(ext);
    let previewUrl = null;
    if (isImg && typeof URL !== 'undefined' && URL.createObjectURL) {
      try {
        previewUrl = URL.createObjectURL(file);
      } catch (_) {}
    }

    const item = {
      id: attId,
      file,
      filename,
      size: file.size,
      type: file.type,
      isImage: isImg,
      previewUrl,
      status: 'uploading',
      path: null,
      error: null
    };

    tabObj.pendingAttachments.push(item);
    renderPendingAttachments(tabObj);

    const formData = new FormData();
    formData.append('file', file, filename);

    const hostParam = (tabObj.session && tabObj.session.host) ? tabObj.session.host : getSelfHostName();
    const baseUrl = getSessionBaseUrl(tabObj.session && tabObj.session.id, tabObj.session);
    const uploadUrl = `${baseUrl}/v1/uploads?host=${encodeURIComponent(hostParam)}&session_id=${encodeURIComponent((tabObj.session && tabObj.session.id) || '')}`;

    const headers = {};
    const token = getAuthToken();
    if (token) {
      headers['X-Ackbar-Token'] = token;
      headers['Authorization'] = `Bearer ${token}`;
    }

    try {
      const res = await fetch(uploadUrl, {
        method: 'POST',
        headers,
        body: formData
      });

      if (!res.ok) {
        const errText = await res.text();
        throw new Error(errText || `Upload failed (HTTP ${res.status})`);
      }

      const data = await res.json();
      if (data.status === 'ok' && data.path) {
        item.status = 'ready';
        item.path = data.path;
        if (data.filename) item.filename = data.filename;
        renderPendingAttachments(tabObj);
      } else {
        throw new Error('Upload succeeded but no destination path returned');
      }
    } catch (err) {
      console.error('Failed to upload attachment:', err);
      item.status = 'error';
      item.error = err.message || 'Upload error';
      showUploadToast(`Upload failed: ${err.message}`, 'error', 4000);
      renderPendingAttachments(tabObj);
    }
  }

  // Upload file (Image, PDF, Document, or Text) to daemon and inject path into prompt input or active terminal
  async function uploadAndAttachFile(file, tabObj, target = 'auto') {
    if (!file || !tabObj) return;

    const isChat = target === 'chat' || (target === 'auto' && tabObj.viewMode === 'chat') || (tabObj.chatInputEl && (!tabObj.socket || tabObj.socket.readyState !== WebSocket.OPEN));
    if (isChat && tabObj.chatComposerAttachments) {
      return addPendingAttachment(tabObj, file);
    }

    const filename = file.name || 'clipboard_image.png';
    const ext = filename.split('.').pop().toLowerCase();
    const allowed = ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg', 'pdf', 'txt', 'md', 'log', 'json', 'yaml', 'yml', 'csv'];
    const type = (file.type || '').toLowerCase();
    const isAllowedMime = type.startsWith('image/') || type === 'application/pdf' || type.includes('bmp') || type.startsWith('text/') || type === 'application/json';
    const isAllowedExt = allowed.includes(ext);

    if (!isAllowedMime && !isAllowedExt) {
      showUploadToast(`Unsupported file type: ${filename} (allowed: images, PDF, TXT, MD, LOG, JSON, YAML, CSV)`, 'error', 4000);
      return;
    }

    showUploadToast(`Uploading ${filename}...`, 'info', 2000);

    const formData = new FormData();
    formData.append('file', file, filename);

    const hostParam = (tabObj.session && tabObj.session.host) ? tabObj.session.host : getSelfHostName();
    const baseUrl = getSessionBaseUrl(tabObj.session && tabObj.session.id, tabObj.session);
    const uploadUrl = `${baseUrl}/v1/uploads?host=${encodeURIComponent(hostParam)}&session_id=${encodeURIComponent((tabObj.session && tabObj.session.id) || '')}`;

    const headers = {};
    const token = getAuthToken();
    if (token) {
      headers['X-Ackbar-Token'] = token;
      headers['Authorization'] = `Bearer ${token}`;
    }

    try {
      const res = await fetch(uploadUrl, {
        method: 'POST',
        headers,
        body: formData
      });

      if (!res.ok) {
        const errText = await res.text();
        showUploadToast(`Upload failed: ${errText || res.statusText}`, 'error', 4000);
        return;
      }

      const data = await res.json();
      if (data.status === 'ok' && data.path) {
        showUploadToast(`Attached: ${data.filename || filename}`, 'success', 3000);

        if (tabObj.socket && tabObj.socket.readyState === WebSocket.OPEN) {
          const quotedPath = `"${data.path}" `;
          tabObj.socket.send(quotedPath);
        } else if (tabObj.chatInputEl) {
          insertPathIntoPromptInput(tabObj.chatInputEl, data.path);
        }
      }
    } catch (err) {
      showUploadToast(`Upload error: ${err.message}`, 'error', 4000);
    }
  }

  // Reconnect Terminal Tab Socket
  function reconnectTerminalTab(tabId) {
    const tab = state.openTabs.get(tabId);
    if (!tab || tab.type !== 'terminal') return;
    connectTerminalWebSocket(tab, tabId, tab.terminal, tab.fitAddon, tab.session);
  }

  // Mark session state as read
  async function markSessionAsRead(session) {
    if (!session || !session.is_unread) return;
    session.is_unread = false;
    const targetInState = (state.sessions || []).find(s => s.id === session.id);
    if (targetInState) targetInState.is_unread = false;
    renderTree();

    try {
      const hostRec = (state.hosts || []).find(h => h.name === session.host);
      const baseUrl = hostRec && hostRec.url && !isLocalHost(session.host) ? hostRec.url.replace(/\/$/, '') : '';
      await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(session.id)}&action=read`, { method: 'POST' });
    } catch (e) {
      console.warn('Failed to mark session as read on daemon:', e);
    }
  }

  // ==========================================================================
  // Dual-Engine Chat Interface & View Mode Handlers
  // ==========================================================================

  // Set View Mode for a session tab ('chat' | 'terminal')
  function setTabViewMode(tabId, mode) {
    const tab = state.openTabs.get(tabId);
    if (!tab || !tab.containerEl) return;
    const targetMode = mode === 'terminal' ? 'terminal' : 'chat';
    tab.viewMode = targetMode;

    tab.containerEl.classList.remove('view-mode-chat', 'view-mode-terminal', 'view-mode-split');
    tab.containerEl.classList.add(`view-mode-${targetMode}`);

    updateViewModeButtons(targetMode);

    if (targetMode === 'terminal') {
      disconnectChatStream(tab);
      // Ensure WebSocket PTY is connected for tmux sessions
      if ((tab.session.engine_type !== 'headless' || tab.session.tmux_name) && (!tab.socket || tab.socket.readyState !== WebSocket.OPEN)) {
        connectTerminalWebSocket(tab, tabId, tab.terminal, tab.fitAddon, tab.session);
      }
      setTimeout(() => {
        if (tab.fitAddon && tab.fitAddon.fit) {
          try { tab.fitAddon.fit(); } catch (_) {}
        }
        if (tab.terminal) {
          tab.terminal.focus();
          if (tab.socket && tab.socket.readyState === WebSocket.OPEN) {
            sendTerminalResize(tab.socket, tab.terminal.cols, tab.terminal.rows);
            // Send Ctrl+L (form feed / redraw) so tmux re-renders current screen buffer
            tab.socket.send(new Uint8Array([0x0c]));
          }
        }
      }, 50);
    } else if (targetMode === 'chat') {
      if (tab.session && tab.session.engine_type === 'headless') {
        connectChatStream(tab);
      }
      // Reload transcript so any activity that occurred in the terminal appears in chat
      loadChatTranscript(tab);
      if (tab.chatInputEl) {
        tab.chatInputEl.focus();
      }
    }
  }

  function updateViewModeButtons(mode) {
    if (el.btnViewChat) el.btnViewChat.classList.toggle('active', mode === 'chat');
    if (el.btnViewTerminal) el.btnViewTerminal.classList.toggle('active', mode === 'terminal');
  }

  // Handle "Take the Wheel in Terminal": resume headless session into live tmux
  async function handleTakeWheel(sessionId) {
    if (!sessionId) return;
    try {
      const sess = state.sessions.find(s => s.id === sessionId);
      const tab = state.openTabs.get(sessionId);
      const sessionObj = sess || (tab ? tab.session : null);
      const baseUrl = getSessionBaseUrl(sessionId, sessionObj);

      const res = await fetch(`${baseUrl}/v1/sessions/take-wheel`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionId })
      });

      if (!res.ok) {
        let errMsg = '';
        try {
          const errData = await res.json();
          errMsg = errData.message || errData.error || '';
        } catch (_) {
          errMsg = await res.text().catch(() => '');
        }
        showUploadToast(`Take the wheel: ${errMsg || res.statusText}`, 'error', 4000);
        return;
      }

      const data = await res.json();
      const tmuxName = data.tmux_name;
      if (sess) {
        sess.engine_type = 'tmux';
        sess.tmux_name = tmuxName;
        sess.managed = true;
      }

      if (tab) {
        tab.session.engine_type = 'tmux';
        tab.session.tmux_name = tmuxName;
        tab.session.managed = true;

        if (tab.chatEngineBadge) {
          tab.chatEngineBadge.textContent = '🖥️ tmux';
          tab.chatEngineBadge.classList.add('badge-tmux');
        }

        if (tab.chatTakeWheelBtn) {
          tab.chatTakeWheelBtn.style.display = 'none';
        }

        const notice = tab.termViewEl ? tab.termViewEl.querySelector('.headless-term-notice') : null;
        if (notice) notice.remove();

        connectTerminalWebSocket(tab, sessionId, tab.terminal, tab.fitAddon, tab.session);
        setTabViewMode(sessionId, 'terminal');
      }

      showUploadToast('Attached interactive terminal', 'success', 2500);
      await fetchSessions();
    } catch (err) {
      console.error('Failed to take wheel:', err);
      showUploadToast(`Error taking wheel: ${err.message}`, 'error', 4000);
    }
  }

  // Setup rich Chat Interface inside tab
  function setupChatInterface(tabObj, chatViewEl, session) {
    const isTmux = session.engine_type === 'tmux';

    chatViewEl.innerHTML = `
      <div class="chat-header-bar">
        <div class="chat-header-left">
          <span class="chat-header-title">${escapeHtml(session.name || session.agent)}</span>
          <span class="chat-engine-badge ${isTmux ? 'badge-tmux' : ''}">
            ${isTmux ? '🖥️ tmux' : '💬 headless'}
          </span>
          <span class="chat-status-badge" style="font-size: 11px; color: var(--text-dim);">🟢 Ready</span>
        </div>
        <div class="chat-header-right">
          <button class="btn-take-wheel" title="Spawn tmux process and attach interactive terminal">🏎️ Take the Wheel</button>
          <button class="btn btn-secondary btn-sm btn-reload-chat" title="Reload transcript history">🔄</button>
        </div>
      </div>
      <div class="chat-messages-container"></div>
      <div class="chat-composer-container">
        <div class="chat-queue-container" style="display: none;">
          <div class="chat-queue-bar">
            <div class="chat-queue-summary">
              <span class="chat-queue-icon">⏳</span>
              <span class="chat-queue-count-text">0 messages queued up</span>
              <span class="chat-queue-chevron">▾</span>
            </div>
            <div class="chat-queue-actions">
              <button class="btn-chat-queue-resume" style="display: none;" type="button" title="Resume queue execution">▶ Resume</button>
              <button class="btn-chat-queue-clear" type="button" title="Clear all queued messages">Clear all</button>
            </div>
          </div>
          <div class="chat-queue-list" style="display: none;"></div>
        </div>
        <div class="chat-composer-attachments" style="display: none;"></div>
        <div class="chat-composer-box">
          <button class="btn-composer-attach" type="button" title="Attach file or image">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
            </svg>
          </button>
          <input type="file" class="chat-file-input" multiple style="display: none;" accept="image/*,.pdf,.txt,.md,.log,.json,.yaml,.yml,.csv" />
          <textarea class="chat-composer-textarea" rows="1" placeholder="Ask ${escapeHtml(session.agent || 'Claude Code')} anything... (Enter to send, Shift+Enter for newline)"></textarea>
          <div class="chat-composer-actions">
            <button class="btn-composer-cancel" style="display: none;" title="Cancel turn (SIGINT)">🛑 Stop</button>
            <button class="btn-composer-send" title="Send Prompt (Enter)">➤</button>
          </div>
        </div>
        <div class="chat-composer-hints">
          <span>💡 Flat-rate OAuth active • Headless streaming • Press Shift+Enter for new line</span>
          <span class="chat-status-hint"></span>
        </div>
      </div>
    `;

    tabObj.promptQueue = tabObj.promptQueue || [];
    tabObj.isQueueExpanded = false;
    tabObj.isQueuePaused = false;
    tabObj.pendingAttachments = [];

    tabObj.chatMessagesEl = chatViewEl.querySelector('.chat-messages-container');
    tabObj.chatComposerAttachments = chatViewEl.querySelector('.chat-composer-attachments');
    tabObj.chatComposerBox = chatViewEl.querySelector('.chat-composer-box');
    tabObj.chatAttachBtn = chatViewEl.querySelector('.btn-composer-attach');
    tabObj.chatFileInput = chatViewEl.querySelector('.chat-file-input');
    tabObj.chatInputEl = chatViewEl.querySelector('.chat-composer-textarea');
    tabObj.chatSendBtn = chatViewEl.querySelector('.btn-composer-send');
    tabObj.chatCancelBtn = chatViewEl.querySelector('.btn-composer-cancel');
    tabObj.chatStatusBadge = chatViewEl.querySelector('.chat-status-badge');
    tabObj.chatEngineBadge = chatViewEl.querySelector('.chat-engine-badge');

    if (tabObj.chatAttachBtn && tabObj.chatFileInput) {
      tabObj.chatAttachBtn.addEventListener('click', (e) => {
        e.preventDefault();
        tabObj.chatFileInput.click();
      });

      tabObj.chatFileInput.addEventListener('change', async () => {
        const files = Array.from(tabObj.chatFileInput.files || []);
        tabObj.chatFileInput.value = '';
        for (const file of files) {
          await addPendingAttachment(tabObj, file);
        }
      });
    }

    // Drop Overlay for Drag-and-Drop file uploads (Images, PDFs, Documents, Text)
    const chatDropOverlay = document.createElement('div');
    chatDropOverlay.className = 'chat-drop-overlay';
    chatDropOverlay.innerHTML = `<div class="drop-badge">📎 Drop file to attach to prompt</div>`;
    chatViewEl.appendChild(chatDropOverlay);

    let chatDragCounter = 0;
    chatViewEl.addEventListener('dragenter', (e) => {
      if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        e.preventDefault();
        chatDragCounter++;
        chatDropOverlay.classList.add('active');
        if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.add('drag-over');
      }
    });

    chatViewEl.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        e.dataTransfer.dropEffect = 'copy';
        chatDropOverlay.classList.add('active');
        if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.add('drag-over');
      }
    });

    chatViewEl.addEventListener('dragleave', (e) => {
      chatDragCounter--;
      if (chatDragCounter <= 0 || !chatViewEl.contains(e.relatedTarget)) {
        chatDragCounter = 0;
        chatDropOverlay.classList.remove('active');
        if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
      }
    });

    chatViewEl.addEventListener('drop', async (e) => {
      e.preventDefault();
      e.stopPropagation();
      chatDragCounter = 0;
      chatDropOverlay.classList.remove('active');
      if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
      const currentTab = state.openTabs.get(tabId) || tabObj;
      if (currentTab && e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        for (const file of e.dataTransfer.files) {
          await addPendingAttachment(currentTab, file);
        }
      }
    });

    tabObj.chatQueueContainer = chatViewEl.querySelector('.chat-queue-container');
    tabObj.chatQueueBar = chatViewEl.querySelector('.chat-queue-bar');
    tabObj.chatQueueCountText = chatViewEl.querySelector('.chat-queue-count-text');
    tabObj.chatQueueChevron = chatViewEl.querySelector('.chat-queue-chevron');
    tabObj.chatQueueList = chatViewEl.querySelector('.chat-queue-list');
    tabObj.chatQueueClearBtn = chatViewEl.querySelector('.btn-chat-queue-clear');
    tabObj.chatQueueResumeBtn = chatViewEl.querySelector('.btn-chat-queue-resume');

    const queueSummary = chatViewEl.querySelector('.chat-queue-summary');
    if (queueSummary) {
      queueSummary.addEventListener('click', () => {
        tabObj.isQueueExpanded = !tabObj.isQueueExpanded;
        renderChatQueue(tabObj);
      });
    }

    if (tabObj.chatQueueClearBtn) {
      tabObj.chatQueueClearBtn.addEventListener('click', async (e) => {
        e.stopPropagation();
        const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
        try {
          await fetch(`${baseUrl}/v1/sessions/prompt/queue?session_id=${encodeURIComponent(tabObj.session.id)}`, {
            method: 'DELETE'
          });
        } catch (err) {
          console.error('Failed to clear queue on daemon:', err);
        }
        tabObj.promptQueue = [];
        tabObj.isQueuePaused = false;
        renderChatQueue(tabObj);
      });
    }

    if (tabObj.chatQueueResumeBtn) {
      tabObj.chatQueueResumeBtn.addEventListener('click', async (e) => {
        e.stopPropagation();
        const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
        try {
          await fetch(`${baseUrl}/v1/sessions/prompt/queue/resume?session_id=${encodeURIComponent(tabObj.session.id)}`, {
            method: 'POST'
          });
        } catch (err) {
          console.error('Failed to resume queue on daemon:', err);
        }
        tabObj.isQueuePaused = false;
        renderChatQueue(tabObj);
      });
    }

    tabObj.chatTakeWheelBtn = chatViewEl.querySelector('.btn-take-wheel');
    if (tabObj.chatTakeWheelBtn) {
      if (session.engine_type === 'tmux') {
        tabObj.chatTakeWheelBtn.style.display = 'none';
      } else {
        tabObj.chatTakeWheelBtn.style.display = 'inline-flex';
        tabObj.chatTakeWheelBtn.addEventListener('click', () => handleTakeWheel(session.id));
      }
    }

    const btnReload = chatViewEl.querySelector('.btn-reload-chat');
    if (btnReload) {
      btnReload.addEventListener('click', () => {
        loadChatTranscript(tabObj);
        loadChatQueue(tabObj);
      });
    }

    if (tabObj.chatCancelBtn) {
      tabObj.chatCancelBtn.addEventListener('click', () => cancelChatTurn(tabObj));
    }

    if (tabObj.chatSendBtn) {
      tabObj.chatSendBtn.addEventListener('click', () => sendChatPrompt(tabObj));
    }

    if (tabObj.chatInputEl) {
      tabObj.chatInputEl.addEventListener('dragover', (e) => {
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
      });

      tabObj.chatInputEl.addEventListener('drop', async (e) => {
        e.preventDefault();
        e.stopPropagation();
        chatDragCounter = 0;
        chatDropOverlay.classList.remove('active');
        if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
        const currentTab = state.openTabs.get(tabId) || tabObj;
        if (currentTab && e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
          for (const file of e.dataTransfer.files) {
            await addPendingAttachment(currentTab, file);
          }
        }
      });

      tabObj.chatInputEl.addEventListener('paste', async (e) => {
        if (!e.clipboardData) return;
        const items = e.clipboardData.items || [];
        let handled = false;
        const currentTab = state.openTabs.get(tabId) || tabObj;
        for (const item of items) {
          if (item.kind === 'file') {
            const itemType = (item.type || '').toLowerCase();
            if (itemType.startsWith('image/') || itemType === 'application/pdf' || itemType.includes('bmp') || itemType.startsWith('text/') || itemType === 'application/json') {
              const file = item.getAsFile();
              if (file) {
                handled = true;
                await addPendingAttachment(currentTab, file);
              }
            }
          }
        }
        if (handled) {
          e.preventDefault();
          e.stopPropagation();
        }
      });

      tabObj.chatInputEl.addEventListener('input', () => {
        tabObj.chatInputEl.style.height = 'auto';
        tabObj.chatInputEl.style.height = Math.min(tabObj.chatInputEl.scrollHeight, 180) + 'px';
      });

      tabObj.chatInputEl.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          sendChatPrompt(tabObj);
        }
      });
    }

    loadChatTranscript(tabObj);
    loadChatQueue(tabObj);
    if (tabObj.session && tabObj.session.engine_type === 'headless' && tabObj.viewMode === 'chat') {
      connectChatStream(tabObj);
    }
  }

  // Load prompt queue from daemon
  async function loadChatQueue(tabObj) {
    if (!tabObj || !tabObj.session) return;
    const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
    try {
      const res = await fetch(`${baseUrl}/v1/sessions/prompt/queue?session_id=${encodeURIComponent(tabObj.session.id)}`);
      if (res.ok) {
        const data = await res.json();
        if (Array.isArray(data.items)) {
          tabObj.promptQueue = data.items;
        }
        tabObj.isQueuePaused = !!data.paused;
        renderChatQueue(tabObj);
      }
    } catch (e) {
      console.warn('Failed to load prompt queue from daemon:', e);
    }
  }

  // Load past conversation messages from daemon transcript
  async function loadChatTranscript(tabObj) {
    if (!tabObj || !tabObj.chatMessagesEl) return;
    const sessionId = tabObj.session.id;
    const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
    try {
      const res = await fetch(`${baseUrl}/v1/sessions/transcript?id=${encodeURIComponent(sessionId)}&format=json`);
      if (!res.ok) return;
      const data = await res.json();
      if (data && data.messages && data.messages.length > 0) {
        tabObj.chatMessagesEl.innerHTML = '';
        for (const msg of data.messages) {
          appendChatMessage(tabObj, msg);
        }
        tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
      }
    } catch (e) {
      console.warn('Failed to load chat transcript:', e);
    }
  }

  // Copy text to clipboard with modern API and textarea fallback
  async function copyTextToClipboard(text) {
    if (!text) return false;
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch (e) {
      // fallback below
    }
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      ta.style.left = '-9999px';
      ta.style.pointerEvents = 'none';
      document.body.appendChild(ta);
      ta.select();
      const success = document.execCommand('copy');
      document.body.removeChild(ta);
      return success;
    } catch (e) {
      console.warn('Failed to copy to clipboard:', e);
      return false;
    }
  }

  // Attach code block copy buttons to all <pre> elements inside container
  function attachCodeBlockCopyButtons(container) {
    if (!container) return;
    const preEls = container.querySelectorAll('pre');
    preEls.forEach(pre => {
      if (pre.querySelector('.btn-copy-code')) return;
      if (pre.closest('.chat-thinking') || pre.closest('.chat-tool-card')) return;

      const copyBtn = document.createElement('button');
      copyBtn.className = 'btn-copy-code';
      copyBtn.type = 'button';
      copyBtn.title = 'Copy code';
      copyBtn.setAttribute('aria-label', 'Copy code snippet');
      copyBtn.innerHTML = `
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
        </svg>
        <span>Copy</span>
      `;
      copyBtn.addEventListener('click', async (e) => {
        e.stopPropagation();
        e.preventDefault();
        const codeEl = pre.querySelector('code');
        let text = '';
        if (codeEl) {
          text = codeEl.innerText;
        } else {
          const clone = pre.cloneNode(true);
          const btn = clone.querySelector('.btn-copy-code');
          if (btn) btn.remove();
          text = clone.innerText;
        }
        text = text.replace(/\r\n/g, '\n').replace(/\n+$/, '');
        const ok = await copyTextToClipboard(text);
        if (ok) {
          copyBtn.classList.add('copied');
          copyBtn.innerHTML = `
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="20 6 9 17 4 12"></polyline>
            </svg>
            <span>Copied!</span>
          `;
          setTimeout(() => {
            copyBtn.classList.remove('copied');
            copyBtn.innerHTML = `
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
              </svg>
              <span>Copy</span>
            `;
          }, 1500);
        }
      });
      pre.appendChild(copyBtn);
    });
  }

  // Known file extensions for linkification & action pills
  const KNOWN_CHAT_FILE_EXTS = new Set([
    'png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico',
    'html', 'htm', 'pdf', 'md', 'markdown', 'txt', 'csv', 'json', 'yaml', 'yml', 'xml', 'log',
    'js', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'sh', 'bash', 'zsh', 'css', 'scss',
    'sql', 'java', 'c', 'cpp', 'h', 'swift', 'kt', 'dart', 'zip', 'tar', 'gz', 'mp4', 'webm', 'mp3', 'wav'
  ]);

  const CHAT_IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico']);

  // Construct URL to stream/view file content through the daemon
  function getChatFileContentUrl(filePath, session, download = false) {
    let url = `/v1/files/content?path=${encodeURIComponent(filePath)}`;
    if (session) {
      if (session.id) url += `&session_id=${encodeURIComponent(session.id)}`;
      if (session.host) url += `&host=${encodeURIComponent(session.host)}`;
    }
    if (download) url += '&download=1';
    const token = getAuthToken();
    if (token) url += `&token=${encodeURIComponent(token)}`;
    return url;
  }

  // Open file in local application (e.g. Preview, Browser, VS Code)
  async function openChatFileInApp(filePath, session, app = 'default') {
    const host = session ? session.host : null;
    const sessionId = session ? session.id : null;
    if (app === 'vscode') {
      return openInVSCode(filePath, host);
    }
    try {
      const res = await fetch('/v1/files/open', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          path: filePath,
          session_id: sessionId,
          host: host,
          app: app
        })
      });
      const filename = filePath.split('/').pop();
      if (!res.ok) {
        const errText = await res.text().catch(() => res.statusText);
        showUploadToast(`Failed to open ${filename}: ${errText}`, 'error');
      } else {
        const data = await res.json().catch(() => ({}));
        const appLabel = app === 'preview' ? 'Preview' : (data.remote ? 'macOS App (staged from remote)' : 'App');
        showUploadToast(`Opened ${filename} in ${appLabel}`, 'success');
      }
    } catch (err) {
      showUploadToast(`Error opening file: ${err.message}`, 'error');
    }
  }

  // Check if string looks like a file path
  function isChatFilePath(str) {
    if (!str || typeof str !== 'string') return false;
    const trimmed = str.trim();
    if (trimmed.length < 3 || trimmed.length > 250) return false;
    if (trimmed.includes('\n') || trimmed.includes('\r')) return false;
    if (trimmed.startsWith('http://') || trimmed.startsWith('https://') || trimmed.includes('@')) return false;
    if (trimmed.includes(' ') && !trimmed.startsWith('/')) return false;

    let pathWithoutQuery = trimmed.split('?')[0].split('#')[0];
    if (pathWithoutQuery.startsWith('file://')) {
      pathWithoutQuery = pathWithoutQuery.replace(/^file:\/\//, '');
    }

    const parts = pathWithoutQuery.split('.');
    if (parts.length < 2) return false;
    const ext = parts.pop().toLowerCase();
    if (!KNOWN_CHAT_FILE_EXTS.has(ext)) return false;

    // Filter out common code expressions like obj.property if not common file extension
    if (/^[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+$/.test(pathWithoutQuery) && !['html', 'htm', 'png', 'jpg', 'jpeg', 'gif', 'svg', 'pdf', 'md', 'txt', 'csv', 'json', 'yaml', 'yml', 'css', 'js', 'ts', 'go', 'py'].includes(ext)) {
      return false;
    }

    return true;
  }

  // Authentic Application SVG Icons for File Context Menu
  const APP_ICONS = {
    vscode: `<svg width="18" height="18" viewBox="0 0 256 256" fill="none" class="menu-app-svg">
      <path d="M177.6 2.3a12.8 12.8 0 0 0-11 3.5L78 88.6 36.3 56.4a12.8 12.8 0 0 0-16.7 1.7L4.7 74.2a12.8 12.8 0 0 0 1.6 17.6l38.7 34.6L6.3 161a12.8 12.8 0 0 0-1.6 17.6l14.9 16.1a12.8 12.8 0 0 0 16.7 1.7l41.7-32.2 88.6 82.8a12.8 12.8 0 0 0 20.3-9.5V11.8a12.8 12.8 0 0 0-9.3-9.5z" fill="#007ACC"/>
      <path d="M177.6 2.3a12.8 12.8 0 0 0-11 3.5L78 88.6l19.8 18.2 68.8-63.4a6.4 6.4 0 0 1 10.8 4.7v-36a12.8 12.8 0 0 0-9.8-9.8z" fill="#1F9CF0"/>
      <path d="M166.6 207.9l-68.8-63.4L78 162.7l88.6 82.8a12.8 12.8 0 0 0 20.3-9.5v-36a6.4 6.4 0 0 1-10.8 4.7l.5 3.2z" fill="#0065A9"/>
      <path d="M177.4 48.1a6.4 6.4 0 0 0-10.8-4.7L78 125.7l88.6 82.2a6.4 6.4 0 0 0 10.8-4.7V48.1z" fill="#007ACC"/>
    </svg>`,

    preview: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
      <defs>
        <linearGradient id="previewSky" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#38bdf8"/>
          <stop offset="50%" stop-color="#60a5fa"/>
          <stop offset="100%" stop-color="#818cf8"/>
        </linearGradient>
        <linearGradient id="previewGlass" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stop-color="#ffffff" stop-opacity="0.85"/>
          <stop offset="50%" stop-color="#e0f2fe" stop-opacity="0.3"/>
          <stop offset="100%" stop-color="#38bdf8" stop-opacity="0.1"/>
        </linearGradient>
        <linearGradient id="previewHandle" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stop-color="#475569"/>
          <stop offset="50%" stop-color="#1e293b"/>
          <stop offset="100%" stop-color="#0f172a"/>
        </linearGradient>
      </defs>
      <rect x="2.5" y="3.5" width="15" height="15" rx="2" fill="#cbd5e1" transform="rotate(-7 10 11)"/>
      <rect x="2.5" y="2" width="16" height="17" rx="2" fill="#ffffff"/>
      <rect x="3.8" y="3.2" width="13.4" height="11" rx="1.2" fill="url(#previewSky)"/>
      <circle cx="13" cy="5.8" r="1.8" fill="#fbbf24"/>
      <path d="M3.8 12.5l3.8-4.2 3 3 2.2-2 4.4 4.8v.1H3.8v-1.7z" fill="#22c55e"/>
      <path d="M6 14.2l3.4-3.2 2.8 2.2 2.8-2.6 2.2 2.4v1.2H6vz" fill="#15803d"/>
      <circle cx="14" cy="11" r="5" fill="none" stroke="#94a3b8" stroke-width="1.8"/>
      <circle cx="14" cy="11" r="4.1" fill="url(#previewGlass)"/>
      <path d="M11.8 8.8a3.2 3.2 0 0 1 3.2-0.2" stroke="#ffffff" stroke-width="1" stroke-linecap="round" fill="none"/>
      <circle cx="17.6" cy="14.6" r="1" fill="#94a3b8"/>
      <line x1="17.6" y1="14.6" x2="22.2" y2="19.2" stroke="url(#previewHandle)" stroke-width="2.6" stroke-linecap="round"/>
      <line x1="18.2" y1="15.2" x2="21.6" y2="18.6" stroke="#94a3b8" stroke-width="1" stroke-linecap="round"/>
    </svg>`,

    browser: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
      <circle cx="12" cy="12" r="10" fill="#0284c7"/>
      <circle cx="12" cy="12" r="9" fill="none" stroke="#ffffff" stroke-width="0.8" stroke-dasharray="1.2 1.5" opacity="0.6"/>
      <polygon points="12,4.5 14.2,12 12,19.5 9.8,12" fill="#ffffff" opacity="0.25"/>
      <polygon points="12,4.5 14.2,12 12,12" fill="#ef4444"/>
      <polygon points="12,4.5 9.8,12 12,12" fill="#dc2626"/>
      <polygon points="12,19.5 14.2,12 12,12" fill="#ffffff"/>
      <polygon points="12,19.5 9.8,12 12,12" fill="#e2e8f0"/>
      <circle cx="12" cy="12" r="1.3" fill="#ffffff"/>
    </svg>`,

    app: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
      <rect x="2.5" y="3" width="19" height="18" rx="4" fill="#3b82f6"/>
      <rect x="2.5" y="3" width="19" height="5.5" rx="3" fill="#1d4ed8"/>
      <circle cx="5.5" cy="5.7" r="1.1" fill="#ef4444"/>
      <circle cx="8.5" cy="5.7" r="1.1" fill="#eab308"/>
      <circle cx="11.5" cy="5.7" r="1.1" fill="#22c55e"/>
      <path d="M7 14.5l3.2 3.2 6.8-6.8" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>`,

    copy: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="menu-app-svg">
      <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
    </svg>`
  };

  let activeFileMenuPill = null;
  let fileContextMenuEl = null;

  function getOrCreateFileContextMenu() {
    if (fileContextMenuEl) return fileContextMenuEl;
    fileContextMenuEl = document.createElement('div');
    fileContextMenuEl.className = 'chat-file-context-menu context-menu';
    fileContextMenuEl.id = 'chatFileContextMenu';
    document.body.appendChild(fileContextMenuEl);
    return fileContextMenuEl;
  }

  function hideChatFileContextMenu() {
    if (fileContextMenuEl) {
      fileContextMenuEl.style.display = 'none';
      fileContextMenuEl.style.visibility = 'hidden';
    }
    if (activeFileMenuPill) {
      activeFileMenuPill.classList.remove('menu-active');
      activeFileMenuPill = null;
    }
  }

  function showChatFileContextMenu(pill, cleanPath, session, clickX, clickY) {
    if (typeof hideContextMenu === 'function') hideContextMenu();
    if (typeof hideGroupContextMenu === 'function') hideGroupContextMenu();
    if (typeof hideTabContextMenu === 'function') hideTabContextMenu();
    hideChatFileContextMenu();

    const menu = getOrCreateFileContextMenu();
    activeFileMenuPill = pill;
    pill.classList.add('menu-active');

    const ext = cleanPath.split('?')[0].split('.').pop().toLowerCase();
    const isImg = CHAT_IMAGE_EXTS.has(ext);
    const isHtml = (ext === 'html' || ext === 'htm');
    const isPdf = (ext === 'pdf');
    const filename = cleanPath.split('/').pop();
    const contentUrl = getChatFileContentUrl(cleanPath, session);

    let icon = '📄';
    if (isImg) icon = '🖼️';
    else if (isHtml) icon = '🌐';
    else if (isPdf) icon = '📑';
    else if (['js', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'c', 'cpp', 'swift'].includes(ext)) icon = '💻';

    const previewSub = isImg || isPdf ? 'macOS Preview.app' : 'Native preview';

    menu.innerHTML = `
      <div class="chat-file-menu-header">
        <span class="chat-file-menu-icon">${icon}</span>
        <span class="chat-file-menu-name" title="${escapeHtml(cleanPath)}">${escapeHtml(filename)}</span>
      </div>
      <div class="context-menu-item" data-action="browser">
        <span class="menu-app-icon">${APP_ICONS.browser}</span>
        <div class="menu-item-text">
          <span class="menu-item-title">Open in Browser</span>
          <span class="menu-item-sub">View / render inline</span>
        </div>
      </div>
      <div class="context-menu-item" data-action="preview">
        <span class="menu-app-icon">${APP_ICONS.preview}</span>
        <div class="menu-item-text">
          <span class="menu-item-title">Open in Preview</span>
          <span class="menu-item-sub">${previewSub}</span>
        </div>
      </div>
      <div class="context-menu-item" data-action="app">
        <span class="menu-app-icon">${APP_ICONS.app}</span>
        <div class="menu-item-text">
          <span class="menu-item-title">Open in Default App</span>
          <span class="menu-item-sub">System application</span>
        </div>
      </div>
      <div class="context-menu-item" data-action="vscode">
        <span class="menu-app-icon">${APP_ICONS.vscode}</span>
        <div class="menu-item-text">
          <span class="menu-item-title">Open in VS Code</span>
          <span class="menu-item-sub">Visual Studio Code</span>
        </div>
      </div>
      <div class="context-menu-divider"></div>
      <div class="context-menu-item" data-action="copy">
        <span class="menu-app-icon">${APP_ICONS.copy}</span>
        <div class="menu-item-text">
          <span class="menu-item-title">Copy File Path</span>
          <span class="menu-item-sub">${escapeHtml(cleanPath)}</span>
        </div>
      </div>
    `;

    menu.querySelectorAll('.context-menu-item').forEach(item => {
      item.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const action = item.dataset.action;
        hideChatFileContextMenu();

        if (action === 'browser') {
          window.open(contentUrl, '_blank');
        } else if (action === 'preview') {
          openChatFileInApp(cleanPath, session, 'preview');
        } else if (action === 'app') {
          openChatFileInApp(cleanPath, session, 'default');
        } else if (action === 'vscode') {
          openChatFileInApp(cleanPath, session, 'vscode');
        } else if (action === 'copy') {
          if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(cleanPath).then(() => {
              showUploadToast(`Copied path: ${cleanPath}`, 'success');
            }).catch(() => {
              showUploadToast(`Copied path: ${cleanPath}`, 'info');
            });
          } else {
            const ta = document.createElement('textarea');
            ta.value = cleanPath;
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            document.body.removeChild(ta);
            showUploadToast(`Copied path: ${cleanPath}`, 'success');
          }
        }
      });
    });

    menu.style.visibility = 'hidden';
    menu.style.display = 'block';

    const menuWidth = menu.offsetWidth || 230;
    const menuHeight = menu.offsetHeight || 260;

    let posX = 0;
    let posY = 0;

    if (typeof clickX === 'number' && typeof clickY === 'number') {
      posX = clickX;
      posY = clickY;
    } else {
      const rect = pill.getBoundingClientRect();
      posX = rect.left;
      posY = rect.bottom + 4;
      if (posY + menuHeight > window.innerHeight - 10) {
        posY = Math.max(10, rect.top - menuHeight - 4);
      }
    }

    if (posY + menuHeight > window.innerHeight - 10) {
      posY = Math.max(10, window.innerHeight - menuHeight - 10);
    }
    if (posX + menuWidth > window.innerWidth - 10) {
      posX = Math.max(10, window.innerWidth - menuWidth - 10);
    }

    menu.style.left = `${posX}px`;
    menu.style.top = `${posY}px`;
    menu.style.visibility = 'visible';
  }

  // Build an interactive compact file badge element
  function createChatFilePill(filePath, session) {
    let cleanPath = filePath.trim();
    if (cleanPath.startsWith('file://')) {
      cleanPath = cleanPath.replace(/^file:\/\//, '');
    }
    const ext = cleanPath.split('?')[0].split('.').pop().toLowerCase();
    const isImg = CHAT_IMAGE_EXTS.has(ext);
    const isHtml = (ext === 'html' || ext === 'htm');
    const isPdf = (ext === 'pdf');
    const filename = cleanPath.split('/').pop();

    let icon = '📄';
    if (isImg) icon = '🖼️';
    else if (isHtml) icon = '🌐';
    else if (isPdf) icon = '📑';
    else if (['js', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'c', 'cpp', 'swift'].includes(ext)) icon = '💻';

    const pill = document.createElement('span');
    pill.className = 'chat-file-pill';
    pill.title = `${cleanPath} (Click for actions)`;

    pill.innerHTML = `
      <span class="chat-file-icon">${icon}</span>
      <span class="chat-file-name">${escapeHtml(filename)}</span>
      <span class="chat-file-chevron">▾</span>
    `;

    pill.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      if (activeFileMenuPill === pill && fileContextMenuEl && fileContextMenuEl.style.display !== 'none') {
        hideChatFileContextMenu();
      } else {
        showChatFileContextMenu(pill, cleanPath, session);
      }
    });

    pill.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showChatFileContextMenu(pill, cleanPath, session, e.clientX, e.clientY);
    });

    return pill;
  }

  // Build an inline image preview card
  function createChatImagePreviewCard(filePath, session) {
    let cleanPath = filePath.trim();
    if (cleanPath.startsWith('file://')) {
      cleanPath = cleanPath.replace(/^file:\/\//, '');
    }
    const filename = cleanPath.split('/').pop();
    const contentUrl = getChatFileContentUrl(cleanPath, session);

    const card = document.createElement('div');
    card.className = 'chat-file-preview-card';
    card.innerHTML = `
      <div class="chat-file-preview-header">
        <span>🖼️ ${escapeHtml(filename)}</span>
        <button class="chat-file-btn" data-action="preview" title="Open in macOS Preview" style="display:inline-flex;align-items:center;gap:4px;">
          ${APP_ICONS.preview} <span>Open in Preview</span>
        </button>
      </div>
      <div class="chat-file-preview-body">
        <img src="${escapeHtml(contentUrl)}" alt="${escapeHtml(filename)}" loading="lazy" title="Click to open full size" />
      </div>
    `;

    const img = card.querySelector('img');
    if (img) {
      img.addEventListener('click', () => {
        window.open(contentUrl, '_blank');
      });
    }

    const previewBtn = card.querySelector('button[data-action="preview"]');
    if (previewBtn) {
      previewBtn.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        openChatFileInApp(cleanPath, session, 'preview');
      });
    }

    return card;
  }

  // Linkify file paths inside chat message container
  function linkifyChatFiles(containerEl, session) {
    if (!containerEl) return;

    // 1. Process <a> markdown links
    containerEl.querySelectorAll('a').forEach(a => {
      if (a.dataset.fileLinkified) return;
      const href = a.getAttribute('href') || '';
      let cleanPath = '';
      if (href.startsWith('file://')) {
        cleanPath = href.replace(/^file:\/\//, '');
      } else if (!href.startsWith('http://') && !href.startsWith('https://') && !href.startsWith('#') && !href.startsWith('mailto:')) {
        const ext = href.split('?')[0].split('.').pop().toLowerCase();
        if (KNOWN_CHAT_FILE_EXTS.has(ext)) {
          cleanPath = href;
        }
      }
      if (cleanPath && isChatFilePath(cleanPath)) {
        a.dataset.fileLinkified = 'true';
        const pill = createChatFilePill(cleanPath, session);
        a.replaceWith(pill);
      }
    });

    // 2. Process inline <code> elements (excluding multiline <pre><code> blocks)
    containerEl.querySelectorAll('code').forEach(code => {
      if (code.closest('pre')) return;
      if (code.dataset.fileLinkified) return;
      const text = (code.textContent || '').trim();
      if (isChatFilePath(text)) {
        code.dataset.fileLinkified = 'true';
        const pill = createChatFilePill(text, session);
        const ext = text.split('?')[0].split('.').pop().toLowerCase();
        if (CHAT_IMAGE_EXTS.has(ext) && !code.closest('table')) {
          const preview = createChatImagePreviewCard(text, session);
          if (code.parentNode) {
            code.parentNode.replaceChild(pill, code);
            pill.parentNode.insertBefore(preview, pill.nextSibling);
          }
        } else {
          if (code.parentNode) {
            code.parentNode.replaceChild(pill, code);
          }
        }
      }
    });

    // 3. Process tool cards (Write, Edit, Read tools)
    containerEl.querySelectorAll('.chat-tool-card').forEach(card => {
      if (card.dataset.fileBarProcessed) return;
      card.dataset.fileBarProcessed = 'true';

      const contentEl = card.querySelector('.chat-tool-content');
      const text = contentEl ? contentEl.textContent || '' : '';

      // Match path parameter in tool input (e.g. {"path": "...", "file_path": "..."})
      const pathMatches = text.match(/"(?:file_)?path"\s*:\s*"([^"]+)"/i) ||
                          text.match(/(?:created|written|saved|editing|writing|reading)\s+(?:file\s+)?([a-zA-Z0-9_\-\.\/]+)/i);

      if (pathMatches && pathMatches[1] && isChatFilePath(pathMatches[1])) {
        const filePath = pathMatches[1];
        const bar = document.createElement('div');
        bar.className = 'chat-tool-file-bar';
        bar.innerHTML = `<span style="font-size: 10.5px; color: var(--text-dim);">File:</span>`;
        bar.appendChild(createChatFilePill(filePath, session));
        if (card.querySelector('.chat-tool-content')) {
          card.insertBefore(bar, card.querySelector('.chat-tool-content'));
        } else {
          card.appendChild(bar);
        }
      }
    });
  }

  // Attach message-level copy button handler
  function attachChatMessageListeners(msgEl, rawContent) {
    if (!msgEl) return;
    const copyBtn = msgEl.querySelector('.btn-copy-chat-msg');
    if (!copyBtn) return;
    copyBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      e.preventDefault();
      let textToCopy = rawContent;
      if (!textToCopy) {
        const bodyEl = msgEl.querySelector('.chat-msg-body') || msgEl.querySelector('.msg-body') || msgEl.querySelector('.markdown-body');
        if (bodyEl) {
          const clone = bodyEl.cloneNode(true);
          clone.querySelectorAll('.btn-copy-code, .chat-streaming-cursor').forEach(el => el.remove());
          textToCopy = clone.innerText || clone.textContent || '';
        }
      }
      if (!textToCopy) return;
      textToCopy = textToCopy.replace(/\r\n/g, '\n').trim();
      const ok = await copyTextToClipboard(textToCopy);
      if (ok) {
        copyBtn.classList.add('copied');
        copyBtn.title = 'Copied!';
        const origSvg = copyBtn.innerHTML;
        copyBtn.innerHTML = `
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"></polyline>
          </svg>
        `;
        setTimeout(() => {
          copyBtn.classList.remove('copied');
          copyBtn.title = 'Copy message';
          copyBtn.innerHTML = origSvg;
        }, 1500);
      }
    });
  }

  // Append a message bubble into the chat messages container
  function appendChatMessage(tabObj, msg) {
    if (!tabObj || !tabObj.chatMessagesEl) return;
    const msgEl = document.createElement('div');
    const timeStr = msg.timestamp ? new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';

    if (msg.role === 'user') {
      msgEl.className = 'chat-msg user-msg';

      let displayContent = msg.content || '';
      const attachments = msg.attachments ? [...msg.attachments] : [];

      // Extract attachments embedded in the prompt format [Attached file: "..."]
      if (!msg.attachments && displayContent.includes('[Attached file: "')) {
        const regex = /\[Attached file:\s*"([^"]+)"\](?:\s*Please inspect the attached file\.)?/g;
        let match;
        while ((match = regex.exec(msg.content)) !== null) {
          const filePath = match[1];
          const fname = filePath.split('/').pop();
          const ext = fname.split('.').pop().toLowerCase();
          const isImg = ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg'].includes(ext);
          attachments.push({
            filename: fname,
            path: filePath,
            isImage: isImg,
            previewUrl: isImg ? getChatFileContentUrl(filePath, tabObj.session) : null
          });
        }
        displayContent = displayContent.replace(/\[Attached file:\s*"[^"]+"\](?:\s*Please inspect the attached file\.)?/g, '').trim();
      }

      let attachmentsHtml = '';
      if (attachments.length > 0) {
        const chipsHtml = attachments.map(att => {
          let thumb = '';
          if (att.previewUrl) {
            thumb = `<img class="chat-attachment-thumb" src="${att.previewUrl}" alt="${escapeHtml(att.filename)}" />`;
          } else {
            thumb = `<span class="chat-attachment-icon">${getAttachmentIcon(att.filename, att.type)}</span>`;
          }
          return `
            <div class="chat-attachment-chip" data-path="${escapeHtml(att.path || '')}" title="Click to open or preview">
              ${thumb}
              <div class="chat-attachment-info">
                <span class="chat-attachment-name" title="${escapeHtml(att.filename)}">${escapeHtml(att.filename)}</span>
                ${att.size ? `<span class="chat-attachment-size">${formatBytes(att.size)}</span>` : ''}
              </div>
            </div>
          `;
        }).join('');
        attachmentsHtml = `<div class="chat-msg-attachments">${chipsHtml}</div>`;
      }

      const bodyHtml = displayContent ? `<div class="chat-msg-body">${escapeHtml(displayContent)}</div>` : '';

      msgEl.innerHTML = `
        <div class="chat-msg-header">
          <span class="chat-msg-role">👤 You</span>
          <div class="chat-msg-actions">
            <span class="chat-msg-time">${timeStr}</span>
            <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
              </svg>
            </button>
          </div>
        </div>
        ${attachmentsHtml}
        ${bodyHtml}
      `;

      msgEl.querySelectorAll('.chat-msg-attachments .chat-attachment-chip').forEach(chip => {
        chip.addEventListener('click', (e) => {
          const path = chip.dataset.path;
          if (path) {
            showChatFileContextMenu(chip, path, tabObj.session, e.clientX, e.clientY);
          }
        });
      });
    } else if (msg.role === 'assistant') {
      msgEl.className = 'chat-msg assistant-msg';
      let thinkingHtml = '';
      if (msg.thinking) {
        thinkingHtml = `
          <details class="chat-thinking">
            <summary>💭 Thinking...</summary>
            <div class="thinking-text">${escapeHtml(msg.thinking)}</div>
          </details>
        `;
      }
      let toolsHtml = '';
      if (msg.tool_calls && msg.tool_calls.length > 0) {
        toolsHtml = msg.tool_calls.map(tc => `
          <details class="chat-tool-card">
            <summary>⚡ ${escapeHtml(tc)}</summary>
            <div class="chat-tool-content">${escapeHtml(tc)}</div>
          </details>
        `).join('');
      }
      const bodyHtml = renderMarkdown(msg.content || '');
      msgEl.innerHTML = `
        <div class="chat-msg-header">
          <span class="chat-msg-role">🤖 ${escapeHtml(tabObj.session.agent || 'Claude Code')}</span>
          <div class="chat-msg-actions">
            <span class="chat-msg-time">${timeStr}</span>
            <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
              </svg>
            </button>
          </div>
        </div>
        ${thinkingHtml}
        ${toolsHtml ? `<div class="chat-tools">${toolsHtml}</div>` : ''}
        <div class="chat-msg-body markdown-body">${bodyHtml}</div>
      `;
    } else {
      msgEl.className = 'chat-msg system-msg';
      msgEl.innerHTML = `<span class="system-tag">ℹ️ ${escapeHtml(msg.content)}</span>`;
    }

    attachChatMessageListeners(msgEl, msg.content);
    attachCodeBlockCopyButtons(msgEl);
    linkifyChatFiles(msgEl, tabObj.session);

    tabObj.chatMessagesEl.appendChild(msgEl);
    return msgEl;
  }

  // Update composer send button state based on active turn
  function updateComposerButtonState(tabObj) {
    if (!tabObj || !tabObj.chatSendBtn) return;
    const isTurnActive = (tabObj.activeTurnMsgEl !== null && tabObj.activeTurnMsgEl !== undefined) ||
                         (tabObj.chatCancelBtn && tabObj.chatCancelBtn.style.display !== 'none');
    if (isTurnActive) {
      tabObj.chatSendBtn.disabled = false;
      tabObj.chatSendBtn.classList.add('is-queue');
      tabObj.chatSendBtn.title = 'Queue prompt (Enter)';
      tabObj.chatSendBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <line x1="12" y1="5" x2="12" y2="19"></line>
          <line x1="5" y1="12" x2="19" y2="12"></line>
        </svg>
      `;
      if (tabObj.chatInputEl) {
        tabObj.chatInputEl.placeholder = 'Queue a follow-up prompt... (Enter to queue, Shift+Enter for newline)';
      }
    } else {
      tabObj.chatSendBtn.disabled = false;
      tabObj.chatSendBtn.classList.remove('is-queue');
      tabObj.chatSendBtn.title = 'Send Prompt (Enter)';
      tabObj.chatSendBtn.innerHTML = '➤';
      if (tabObj.chatInputEl) {
        tabObj.chatInputEl.placeholder = `Ask ${escapeHtml(tabObj.session && tabObj.session.agent ? tabObj.session.agent : 'Claude Code')} anything... (Enter to send, Shift+Enter for newline)`;
      }
    }
  }

  // Render Prompt Queue box docked above composer
  function renderChatQueue(tabObj) {
    if (!tabObj || !tabObj.chatQueueContainer) return;
    const q = tabObj.promptQueue || [];

    if (q.length === 0) {
      tabObj.chatQueueContainer.style.display = 'none';
      tabObj.isQueueExpanded = false;
      tabObj.isQueuePaused = false;
      updateComposerButtonState(tabObj);
      return;
    }

    tabObj.chatQueueContainer.style.display = 'flex';
    const countText = q.length === 1 ? '1 message queued up' : `${q.length} messages queued up`;
    if (tabObj.chatQueueCountText) {
      if (tabObj.isQueuePaused) {
        tabObj.chatQueueCountText.textContent = `⏸️ Queue paused (${countText})`;
      } else {
        tabObj.chatQueueCountText.textContent = countText;
      }
    }

    if (tabObj.chatQueueResumeBtn) {
      tabObj.chatQueueResumeBtn.style.display = tabObj.isQueuePaused ? 'inline-flex' : 'none';
    }

    if (tabObj.chatQueueChevron) {
      tabObj.chatQueueChevron.textContent = tabObj.isQueueExpanded ? '▴' : '▾';
    }

    if (!tabObj.isQueueExpanded) {
      if (tabObj.chatQueueList) tabObj.chatQueueList.style.display = 'none';
    } else {
      if (tabObj.chatQueueList) {
        tabObj.chatQueueList.style.display = 'flex';
        tabObj.chatQueueList.innerHTML = '';

        q.forEach((item, idx) => {
          const itemEl = document.createElement('div');
          itemEl.className = `chat-queue-item ${item.expanded ? 'expanded' : ''}`;
          itemEl.dataset.id = item.id;

          itemEl.innerHTML = `
            <div class="chat-queue-item-header">
              <span class="chat-queue-item-index">#${idx + 1}</span>
              <div class="chat-queue-item-preview" title="Click to ${item.expanded ? 'collapse' : 'expand full message'}">
                ${escapeHtml(item.text)}
              </div>
              <div class="chat-queue-item-actions">
                <button class="btn-queue-item-toggle" type="button" title="${item.expanded ? 'Collapse' : 'Expand'}">
                  ${item.expanded ? '▴' : '▾'}
                </button>
                <button class="btn-queue-item-trash" type="button" title="Remove from queue" aria-label="Remove message">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="3 6 5 6 21 6"></polyline>
                    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                    <line x1="10" y1="11" x2="10" y2="17"></line>
                    <line x1="14" y1="11" x2="14" y2="17"></line>
                  </svg>
                </button>
              </div>
            </div>
            ${item.expanded ? `
              <div class="chat-queue-item-body">
                <div class="chat-queue-item-full">${escapeHtml(item.text)}</div>
              </div>
            ` : ''}
          `;

          const previewEl = itemEl.querySelector('.chat-queue-item-preview');
          const toggleBtn = itemEl.querySelector('.btn-queue-item-toggle');
          const toggleFn = (e) => {
            e.stopPropagation();
            item.expanded = !item.expanded;
            renderChatQueue(tabObj);
          };
          if (previewEl) previewEl.addEventListener('click', toggleFn);
          if (toggleBtn) toggleBtn.addEventListener('click', toggleFn);

          const trashBtn = itemEl.querySelector('.btn-queue-item-trash');
          if (trashBtn) {
            trashBtn.addEventListener('click', async (e) => {
              e.stopPropagation();
              const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
              try {
                await fetch(`${baseUrl}/v1/sessions/prompt/queue?session_id=${encodeURIComponent(tabObj.session.id)}&item_id=${encodeURIComponent(item.id)}`, {
                  method: 'DELETE'
                });
              } catch (err) {
                console.error('Failed to delete queue item on daemon:', err);
              }
              tabObj.promptQueue = tabObj.promptQueue.filter(p => p.id !== item.id);
              renderChatQueue(tabObj);
            });
          }

          tabObj.chatQueueList.appendChild(itemEl);
        });
      }
    }

    updateComposerButtonState(tabObj);
  }

  // Pop and dispatch the next queued prompt if idle
  async function dispatchNextQueuedPrompt(tabObj) {
    if (!tabObj || !tabObj.session) return;
    const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
    try {
      await fetch(`${baseUrl}/v1/sessions/prompt/queue/resume?session_id=${encodeURIComponent(tabObj.session.id)}`, {
        method: 'POST'
      });
    } catch (err) {
      console.error('Failed to resume queue dispatch on daemon:', err);
    }
  }

  // Send Prompt to daemon (/v1/sessions/prompt) or queue if turn is in progress
  async function sendChatPrompt(tabObj, forcedPromptText) {
    if (!tabObj || !tabObj.chatInputEl) return;
    let promptText = '';
    if (typeof forcedPromptText === 'string') {
      promptText = forcedPromptText.trim();
    } else {
      promptText = tabObj.chatInputEl.value.trim();
    }

    const pending = tabObj.pendingAttachments || [];
    if (pending.some(a => a.status === 'uploading')) {
      showUploadToast('Please wait for file upload to finish...', 'info', 2500);
      return;
    }

    const failed = pending.filter(a => a.status === 'error');
    if (failed.length > 0) {
      showUploadToast(`Attachment "${failed[0].filename}" failed to upload. Please remove or retry.`, 'error', 3000);
      return;
    }

    const readyAttachments = pending.filter(a => a.status === 'ready' && a.path);

    if (!promptText && readyAttachments.length === 0) return;

    if (typeof forcedPromptText !== 'string') {
      tabObj.chatInputEl.value = '';
      tabObj.chatInputEl.style.height = 'auto';
    }

    let daemonPrompt = promptText;
    if (readyAttachments.length > 0) {
      const attachInstructions = readyAttachments.map(att =>
        `[Attached file: "${att.path}"]\nPlease inspect the attached file.`
      ).join('\n\n');
      if (daemonPrompt) {
        daemonPrompt = `${daemonPrompt}\n\n${attachInstructions}`;
      } else {
        daemonPrompt = `Please inspect the attached file(s).\n\n${attachInstructions}`;
      }
    }

    const sentAttachments = [...readyAttachments];
    tabObj.pendingAttachments = [];
    renderPendingAttachments(tabObj);

    connectChatStream(tabObj);
    const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
    try {
      const res = await fetch(`${baseUrl}/v1/sessions/prompt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          session_id: tabObj.session.id,
          prompt: daemonPrompt
        })
      });

      if (!res.ok) {
        let errMsg = '';
        try {
          const errData = await res.json();
          errMsg = errData.message || errData.error || '';
        } catch (_) {
          errMsg = await res.text().catch(() => '');
        }
        throw new Error(errMsg || `HTTP ${res.status}`);
      }

      const data = await res.json().catch(() => ({}));
      if (data && data.status === 'queued') {
        // Prompt queued on daemon
        loadChatQueue(tabObj);
        return;
      }

      appendChatMessage(tabObj, {
        role: 'user',
        content: promptText,
        attachments: sentAttachments,
        timestamp: new Date().toISOString()
      });

      const assistantMsgEl = document.createElement('div');
      assistantMsgEl.className = 'chat-msg assistant-msg in-flight';
      assistantMsgEl.innerHTML = `
        <div class="chat-msg-header">
          <span class="chat-msg-role">🤖 ${escapeHtml(tabObj.session.agent || 'Claude Code')}</span>
          <div class="chat-msg-actions">
            <span class="chat-msg-time">${new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
            <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
              </svg>
            </button>
          </div>
        </div>
        <div class="chat-thinking-slot"></div>
        <div class="chat-tools-slot"></div>
        <div class="chat-msg-body markdown-body"><span class="chat-streaming-cursor"></span></div>
      `;
      attachChatMessageListeners(assistantMsgEl);
      tabObj.chatMessagesEl.appendChild(assistantMsgEl);
      tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;

      tabObj.activeTurnMsgEl = assistantMsgEl;
      tabObj.activeTurnBuffer = '';
      tabObj.activeTurnHadTool = false;

      if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
      if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
      updateComposerButtonState(tabObj);
    } catch (err) {
      console.error('Failed to dispatch prompt:', err);
      appendChatMessage(tabObj, {
        role: 'user',
        content: promptText,
        attachments: sentAttachments,
        timestamp: new Date().toISOString()
      });
      const errEl = document.createElement('div');
      errEl.className = 'chat-msg assistant-msg';
      errEl.innerHTML = `
        <div class="chat-msg-header">
          <span class="chat-msg-role">🤖 ${escapeHtml(tabObj.session.agent || 'Claude Code')}</span>
        </div>
        <div class="chat-msg-body markdown-body">
          <span style="color: var(--accent-red);">⚠️ Error sending prompt: ${escapeHtml(err.message)}</span>
        </div>
      `;
      tabObj.chatMessagesEl.appendChild(errEl);
      resetChatComposer(tabObj);
      tabObj.activeTurnMsgEl = null;
      tabObj.activeTurnBuffer = '';
      tabObj.activeTurnHadTool = false;
    }
  }

  // Disconnect SSE for chat turn events
  function disconnectChatStream(tabObj) {
    if (tabObj && tabObj.chatEventSource) {
      try {
        tabObj.chatEventSource.close();
      } catch (e) {}
      tabObj.chatEventSource = null;
    }
  }

  // Connect SSE for active session turn events (headless sessions only)
  function connectChatStream(tabObj) {
    if (!tabObj || !tabObj.session) return;
    // Terminal/tmux sessions stream via WebSocket PTY, not headless SSE stream
    if (tabObj.session.engine_type !== 'headless') {
      return;
    }
    // Only connect if currently in chat view mode
    if (tabObj.viewMode !== 'chat') {
      return;
    }
    // Only connect if the tab is active
    if (tabObj.tabEl && !tabObj.tabEl.classList.contains('active')) {
      return;
    }
    if (tabObj.chatEventSource && tabObj.chatEventSource.readyState !== EventSource.CLOSED) {
      return;
    }

    const sessionId = tabObj.session.id;
    const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
    const token = getAuthToken();
    let sseUrl = `${baseUrl}/v1/sessions/chat/stream?session_id=${encodeURIComponent(sessionId)}`;
    if (token) {
      sseUrl += `&token=${encodeURIComponent(token)}`;
    }

    try {
      const es = new EventSource(sseUrl);
      tabObj.chatEventSource = es;

      es.onmessage = (event) => {
        if (!event.data) return;
        try {
          const evt = JSON.parse(event.data);
          handleChatStreamEvent(tabObj, evt);
        } catch (e) {
          console.warn('Failed to parse SSE event data:', e);
        }
      };
      es.onerror = () => {
        disconnectChatStream(tabObj);
      };
    } catch (e) {
      console.error('Failed to open EventSource:', e);
    }
  }

  // Handle stream event from SSE
  function handleChatStreamEvent(tabObj, evt) {
    if (!tabObj || !tabObj.chatMessagesEl) return;

    if (!tabObj.activeTurnMsgEl && evt.type !== 'status' && evt.type !== 'queue_update') {
      let inFlight = tabObj.chatMessagesEl.querySelector('.chat-msg.assistant-msg.in-flight');
      if (!inFlight && evt.type !== 'turn_complete' && evt.type !== 'turn_cancelled') {
        inFlight = document.createElement('div');
        inFlight.className = 'chat-msg assistant-msg in-flight';
        inFlight.innerHTML = `
          <div class="chat-msg-header">
            <span class="chat-msg-role">🤖 ${escapeHtml(tabObj.session.agent || 'Claude Code')}</span>
            <div class="chat-msg-actions">
              <span class="chat-msg-time">${new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
              <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                </svg>
              </button>
            </div>
          </div>
          <div class="chat-thinking-slot"></div>
          <div class="chat-tools-slot"></div>
          <div class="chat-msg-body markdown-body"><span class="chat-streaming-cursor"></span></div>
        `;
        attachChatMessageListeners(inFlight);
        tabObj.chatMessagesEl.appendChild(inFlight);
      }
      tabObj.activeTurnMsgEl = inFlight;
      if (!tabObj.activeTurnBuffer) tabObj.activeTurnBuffer = '';
    }

    const msgEl = tabObj.activeTurnMsgEl;

    switch (evt.type) {
      case 'queue_update':
        if (Array.isArray(evt.queue_items)) {
          tabObj.promptQueue = evt.queue_items;
        }
        tabObj.isQueuePaused = !!evt.queue_paused;
        renderChatQueue(tabObj);
        break;

      case 'turn_start':
        tabObj.activeTurnHadTool = false;
        if (evt.text) {
          const lastMsg = tabObj.chatMessagesEl.lastElementChild;
          const userBody = lastMsg ? lastMsg.querySelector('.chat-msg-body') : null;
          const isAlreadyRendered = lastMsg && lastMsg.classList.contains('user-msg') && 
            (lastMsg.textContent.includes(evt.text) || (userBody && userBody.textContent.trim() === evt.text.trim()));
          if (!isAlreadyRendered) {
            appendChatMessage(tabObj, {
              role: 'user',
              content: evt.text,
              timestamp: evt.timestamp || new Date().toISOString()
            });
          }
        }
        if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
        if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
        updateComposerButtonState(tabObj);
        break;

      case 'text_delta':
        if (msgEl) {
          const incoming = evt.text || '';
          if (incoming) {
            if (tabObj.activeTurnHadTool && tabObj.activeTurnBuffer) {
              tabObj.activeTurnBuffer = ensureDoubleNewlineSeparation(tabObj.activeTurnBuffer, incoming);
              tabObj.activeTurnHadTool = false;
            } else {
              tabObj.activeTurnBuffer = (tabObj.activeTurnBuffer || '') + incoming;
            }
            const bodyEl = msgEl.querySelector('.chat-msg-body');
            if (bodyEl) {
              const html = renderMarkdown(tabObj.activeTurnBuffer);
              bodyEl.innerHTML = html + '<span class="chat-streaming-cursor"></span>';
            }
            tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
          }
        }
        break;

      case 'thought_delta':
        if (msgEl && evt.thinking) {
          const slot = msgEl.querySelector('.chat-thinking-slot');
          if (slot) {
            let thinkBlock = slot.querySelector('.chat-thinking');
            if (!thinkBlock) {
              thinkBlock = document.createElement('details');
              thinkBlock.className = 'chat-thinking';
              thinkBlock.open = true;
              thinkBlock.innerHTML = `<summary>💭 Thinking...</summary><div class="thinking-text"></div>`;
              slot.appendChild(thinkBlock);
            }
            const thinkText = thinkBlock.querySelector('.thinking-text');
            if (thinkText) {
              thinkText.textContent += evt.thinking;
            }
            tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
          }
        }
        break;

      case 'tool_start':
        tabObj.activeTurnHadTool = true;
        if (msgEl && evt.tool_name) {
          const slot = msgEl.querySelector('.chat-tools-slot');
          if (slot) {
            const toolCard = document.createElement('details');
            toolCard.className = 'chat-tool-card';
            toolCard.dataset.toolName = evt.tool_name;
            toolCard.open = true;
            const inputStr = typeof evt.tool_input === 'string' ? evt.tool_input : JSON.stringify(evt.tool_input, null, 2);
            toolCard.innerHTML = `
              <summary>⚡ ${escapeHtml(evt.tool_name)}: running...</summary>
              <div class="chat-tool-content">${escapeHtml(inputStr || '')}</div>
            `;
            slot.appendChild(toolCard);
            tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
          }
        }
        break;

      case 'tool_result':
        tabObj.activeTurnHadTool = true;
        if (msgEl) {
          const slot = msgEl.querySelector('.chat-tools-slot');
          if (slot) {
            const lastCard = slot.querySelector('.chat-tool-card:last-child');
            if (lastCard) {
              const summaryEl = lastCard.querySelector('summary');
              if (summaryEl && summaryEl.textContent.includes('running...')) {
                summaryEl.textContent = `✅ ${lastCard.dataset.toolName || 'tool'}: done`;
              }
              if (evt.tool_output) {
                const outEl = document.createElement('div');
                outEl.className = 'chat-tool-output';
                outEl.textContent = evt.tool_output;
                lastCard.appendChild(outEl);
              }
              linkifyChatFiles(lastCard, tabObj.session);
            }
          }
          tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
        }
        break;

      case 'turn_complete':
        if (msgEl) {
          if (!tabObj.activeTurnBuffer && evt.text) {
            tabObj.activeTurnBuffer = evt.text;
          }
          if (tabObj.activeTurnBuffer) {
            const bodyEl = msgEl.querySelector('.chat-msg-body');
            if (bodyEl) {
              const html = renderMarkdown(tabObj.activeTurnBuffer);
              bodyEl.innerHTML = html;
            }
          }
          msgEl.classList.remove('in-flight');
          const cursor = msgEl.querySelector('.chat-streaming-cursor');
          if (cursor) cursor.remove();
          attachCodeBlockCopyButtons(msgEl);
          attachChatMessageListeners(msgEl, tabObj.activeTurnBuffer);
          linkifyChatFiles(msgEl, tabObj.session);
        }
        resetChatComposer(tabObj);
        tabObj.activeTurnMsgEl = null;
        tabObj.activeTurnBuffer = '';
        tabObj.activeTurnHadTool = false;
        fetchSessions();
        break;

      case 'turn_cancelled':
        if (msgEl) {
          msgEl.classList.remove('in-flight');
          const cursor = msgEl.querySelector('.chat-streaming-cursor');
          if (cursor) cursor.remove();
          const bodyEl = msgEl.querySelector('.chat-msg-body');
          if (bodyEl) {
            bodyEl.innerHTML += `<div style="margin-top: 8px; color: var(--text-dim); font-style: italic;">[Turn cancelled by user]</div>`;
          }
          attachCodeBlockCopyButtons(msgEl);
          attachChatMessageListeners(msgEl, tabObj.activeTurnBuffer);
          linkifyChatFiles(msgEl, tabObj.session);
        }
        resetChatComposer(tabObj);
        tabObj.activeTurnMsgEl = null;
        tabObj.activeTurnBuffer = '';
        tabObj.activeTurnHadTool = false;
        if (tabObj.promptQueue && tabObj.promptQueue.length > 0) {
          tabObj.isQueuePaused = true;
          renderChatQueue(tabObj);
        }
        fetchSessions();
        break;

      case 'error':
        if (msgEl) {
          msgEl.classList.remove('in-flight');
          const cursor = msgEl.querySelector('.chat-streaming-cursor');
          if (cursor) cursor.remove();
          const bodyEl = msgEl.querySelector('.chat-msg-body');
          if (bodyEl) {
            bodyEl.innerHTML += `<div style="margin-top: 8px; color: var(--accent-red);">⚠️ ${escapeHtml(evt.text || 'Turn error')}</div>`;
          }
        }
        resetChatComposer(tabObj);
        tabObj.activeTurnMsgEl = null;
        tabObj.activeTurnBuffer = '';
        tabObj.activeTurnHadTool = false;
        if (tabObj.promptQueue && tabObj.promptQueue.length > 0) {
          tabObj.isQueuePaused = true;
          renderChatQueue(tabObj);
        }
        break;
    }
  }

  function resetChatComposer(tabObj) {
    if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'none';
    if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '🟢 Ready';
    updateComposerButtonState(tabObj);
    tabObj.activeTurnHadTool = false;
    if (tabObj.chatInputEl) tabObj.chatInputEl.focus();
  }

  async function cancelChatTurn(tabObj) {
    if (!tabObj || !tabObj.session) return;
    const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
    try {
      await fetch(`${baseUrl}/v1/sessions/cancel`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: tabObj.session.id })
      });
    } catch (e) {
      console.warn('Failed to cancel turn:', e);
    }
  }

  // Open Session in Terminal/Chat Tab
  function openSessionInTab(session) {
    if (!session) return;
    const tabId = session.id;

    if (state.openTabs.has(tabId)) {
      const existingTab = state.openTabs.get(tabId);
      if (existingTab && existingTab.type === 'terminal') {
        if (!existingTab.socket || existingTab.socket.readyState === WebSocket.CLOSED || existingTab.socket.readyState === WebSocket.CLOSING) {
          reconnectTerminalTab(tabId);
        }
        activateTab(tabId);
        markSessionAsRead(session);
        return;
      }
    }

    markSessionAsRead(session);

    session.managed = true;
    if (session.state === 4 || !session.state) {
      session.state = 3;
    }

    if (el.emptyState) el.emptyState.style.display = 'none';

    // 1. Create Tab DOM Element
    const tabEl = document.createElement('div');
    tabEl.className = 'terminal-tab';
    tabEl.dataset.tabId = tabId;

    const titleWrap = document.createElement('div');
    titleWrap.className = 'tab-title-wrap';

    const emojiSpan = document.createElement('span');
    emojiSpan.className = 'tab-emoji';
    emojiSpan.innerHTML = getStateEmoji(session);

    const unreadDot = document.createElement('span');
    unreadDot.className = 'tab-unread-dot unread-dot';
    unreadDot.style.display = (session.is_unread && state.activeTabId !== tabId) ? 'inline-block' : 'none';

    const titleSpan = document.createElement('span');
    titleSpan.className = 'tab-title';
    titleSpan.textContent = session.name || session.agent;
    titleSpan.title = session.name || session.agent;

    const timeFull = formatFullDateTime(session.last_event_at || session.started_at);
    const timeRel = formatRelativeTime(session.last_event_at || session.started_at);
    tabEl.title = `${session.name || session.agent}\nHost: @${formatHostLabel(session.host || 'local')}${timeRel ? `\nLast Active: ${timeRel} (${timeFull})` : ''}`;

    titleWrap.appendChild(emojiSpan);
    titleWrap.appendChild(unreadDot);
    titleWrap.appendChild(titleSpan);

    const closeBtn = document.createElement('button');
    closeBtn.className = 'tab-close-btn';
    closeBtn.textContent = '✕';
    closeBtn.title = 'Close Tab';
    closeBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      closeTab(tabId);
    });

    tabEl.appendChild(titleWrap);
    tabEl.appendChild(closeBtn);

    // Left click to activate tab
    tabEl.addEventListener('click', (e) => {
      if (e.button === 0) activateTab(tabId);
    });

    // Middle mouse click to close tab
    tabEl.addEventListener('auxclick', (e) => {
      if (e.button === 1) {
        e.preventDefault();
        e.stopPropagation();
        closeTab(tabId);
      }
    });

    // Right-click for Tab Context Menu
    tabEl.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showTabContextMenu(e.clientX, e.clientY, tabId);
    });

    tabEl.addEventListener('mousedown', (e) => {
      if (e.button === 1) e.preventDefault();
    });

    if (el.tabStrip) el.tabStrip.appendChild(tabEl);

    // 2. Create Terminal / Chat Container
    const initialViewMode = session.engine_type === 'headless' ? 'chat' : 'terminal';
    const containerEl = document.createElement('div');
    containerEl.className = `terminal-tab-view has-session-container view-mode-${initialViewMode}`;
    containerEl.id = `termView_${tabId.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    containerEl.addEventListener('click', () => {
      const currentTab = state.openTabs.get(tabId);
      if (currentTab && (!currentTab.socket || currentTab.socket.readyState === WebSocket.CLOSED || currentTab.socket.readyState === WebSocket.CLOSING)) {
        reconnectTerminalTab(tabId);
      }
    });
    if (el.terminalViewport) el.terminalViewport.appendChild(containerEl);

    // Sub-containers for dual-engine layout
    const sessionContainer = document.createElement('div');
    sessionContainer.className = 'session-view-container';
    containerEl.appendChild(sessionContainer);

    const chatViewEl = document.createElement('div');
    chatViewEl.className = 'session-chat-view';
    sessionContainer.appendChild(chatViewEl);

    const termViewEl = document.createElement('div');
    termViewEl.className = 'session-term-view';
    sessionContainer.appendChild(termViewEl);

    // If headless and not yet resumed into tmux, show friendly takeover card in terminal view
    if (session.engine_type === 'headless' && !session.tmux_name) {
      const notice = document.createElement('div');
      notice.className = 'headless-term-notice';
      notice.innerHTML = `
        <div class="headless-term-notice-card">
          <div style="font-size: 32px;">🏎️</div>
          <h3>Headless Execution Mode</h3>
          <p>This session is running in turn-by-turn headless streaming mode to protect flat-rate OAuth subscriptions. An interactive tmux terminal has not been spawned yet.</p>
          <button class="btn btn-primary btn-take-wheel-prompt">🏎️ Take the Wheel in Terminal</button>
        </div>
      `;
      const takeWheelBtn = notice.querySelector('.btn-take-wheel-prompt');
      if (takeWheelBtn) {
        takeWheelBtn.addEventListener('click', () => handleTakeWheel(tabId));
      }
      termViewEl.appendChild(notice);
    }

    // 3. Drop Overlay for Drag-and-Drop file uploads (Images & PDFs)
    const dropOverlay = document.createElement('div');
    dropOverlay.className = 'terminal-drop-overlay';
    dropOverlay.innerHTML = `<div class="drop-badge">📎 Drop image or PDF to upload & attach</div>`;
    termViewEl.appendChild(dropOverlay);

    termViewEl.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        dropOverlay.classList.add('active');
      }
    });

    termViewEl.addEventListener('dragleave', (e) => {
      if (!termViewEl.contains(e.relatedTarget)) {
        dropOverlay.classList.remove('active');
      }
    });

    termViewEl.addEventListener('drop', (e) => {
      e.preventDefault();
      dropOverlay.classList.remove('active');
      const currentTab = state.openTabs.get(tabId);
      if (currentTab && e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        for (const file of e.dataTransfer.files) {
          uploadAndAttachFile(file, currentTab);
        }
      }
    });

    // 4. Clipboard paste listener for images / PDFs
    termViewEl.addEventListener('paste', (e) => {
      if (!e.clipboardData) return;
      const items = e.clipboardData.items || [];
      let handled = false;
      const currentTab = state.openTabs.get(tabId);
      if (!currentTab) return;

      for (const item of items) {
        if (item.kind === 'file') {
          const itemType = (item.type || '').toLowerCase();
          if (itemType.startsWith('image/') || itemType === 'application/pdf' || itemType.includes('bmp')) {
            const file = item.getAsFile();
            if (file) {
              handled = true;
              uploadAndAttachFile(file, currentTab);
            }
          }
        }
      }
      if (handled) {
        e.preventDefault();
        e.stopPropagation();
      }
    });

    // 5. Initialize xterm.js & FitAddon
    const term = new Terminal({
      cursorBlink: true,
      cursorStyle: 'block',
      fontSize: 13,
      lineHeight: 1.2,
      fontFamily: 'ui-monospace, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
      theme: {
        background: '#090a0f',
        foreground: '#e2e8f0',
        cursor: '#3b82f6',
        selectionBackground: 'rgba(59, 130, 246, 0.3)'
      },
      allowTransparency: true
    });

    const fitAddon = new FitAddon.FitAddon();
    term.loadAddon(fitAddon);
    if (window.WebLinksAddon) {
      term.loadAddon(new WebLinksAddon.WebLinksAddon());
    }

    term.open(termViewEl);

    // Helper: Safely decode base64 UTF-8 text (handles multi-byte unicode such as box drawings, emoji, diacritics)
    function decodeBase64Utf8(b64) {
      if (!b64) return '';
      try {
        const cleanB64 = b64.trim();
        const binaryString = atob(cleanB64);
        const bytes = new Uint8Array(binaryString.length);
        for (let i = 0; i < binaryString.length; i++) {
          bytes[i] = binaryString.charCodeAt(i);
        }
        return new TextDecoder('utf-8').decode(bytes);
      } catch (e) {
        try {
          return atob(b64.trim());
        } catch (e2) {
          return '';
        }
      }
    }

    // Register OSC 52 Clipboard handler (receives base64-encoded clipboard from remote tmux)
    if (term.parser && term.parser.registerOscHandler) {
      term.parser.registerOscHandler(52, (data) => {
        // Format: "c;<base64-payload>" or ";<base64-payload>"
        const firstSemi = data.indexOf(';');
        const b64 = firstSemi !== -1 ? data.slice(firstSemi + 1) : data;
        if (b64) {
          try {
            const decoded = decodeBase64Utf8(b64);
            if (decoded && navigator.clipboard && navigator.clipboard.writeText) {
              navigator.clipboard.writeText(decoded).catch(() => {});
            }
          } catch (e) {}
        }
        return true;
      });
    }

    // Auto-copy highlighted text to clipboard immediately on mouse selection
    term.onSelectionChange(() => {
      if (term.hasSelection()) {
        const text = term.getSelection();
        if (text && text.length > 0 && navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).catch(() => {});
        }
      }
    });

    term.attachCustomKeyEventHandler((event) => {
      // 1. Allow Cmd+R / Ctrl+R / F5 to reload webpage
      if ((event.metaKey || event.ctrlKey) && (event.key === 'r' || event.key === 'R')) {
        if (event.type === 'keydown') {
          window.location.reload();
        }
        return false;
      }
      if (event.key === 'F5') {
        if (event.type === 'keydown') {
          window.location.reload();
        }
        return false;
      }
      // 2. Allow Cmd+W / Ctrl+W to close active tab
      if ((event.metaKey || event.ctrlKey) && (event.key === 'w' || event.key === 'W')) {
        if (event.type === 'keydown') {
          closeTab(tabId);
        }
        return false;
      }
      // 3. Allow Cmd+K / Ctrl+K for command palette
      if ((event.metaKey || event.ctrlKey) && (event.key === 'k' || event.key === 'K')) {
        if (event.type === 'keydown') {
          toggleCommandPalette();
        }
        return false;
      }
      // 4. Cmd+C / Ctrl+C with active selection copies to clipboard without sending SIGINT
      if ((event.metaKey || event.ctrlKey) && (event.key === 'c' || event.key === 'C')) {
        if (term.hasSelection()) {
          const text = term.getSelection();
          if (text && navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(text).catch(() => {});
          }
          return false;
        }
      }
      // 5. Allow Cmd+1..9 to switch tabs
      if ((event.metaKey || event.ctrlKey) && event.key >= '1' && event.key <= '9') {
        if (event.type === 'keydown') {
          const tabIndex = parseInt(event.key, 10) - 1;
          const tabIds = Array.from(state.openTabs.keys());
          if (tabIds[tabIndex]) {
            activateTab(tabIds[tabIndex]);
          }
        }
        return false;
      }
      // 7. Shift+Tab to cycle Claude Code modes (send raw ANSI backtab [0x1b, 0x5b, 0x5a] and prevent browser focus shift)
      if (event.key === 'Tab' && event.shiftKey) {
        if (event.type === 'keydown') {
          event.preventDefault();
          event.stopPropagation();
          const currentTab = state.openTabs.get(tabId);
          if (currentTab && currentTab.socket && currentTab.socket.readyState === WebSocket.OPEN) {
            currentTab.socket.send(new Uint8Array([0x1b, 0x5b, 0x5a]));
          }
        }
        return false;
      }
      return true;
    });

    // Save in State
    const tabObj = {
      type: 'terminal',
      session,
      terminal: term,
      fitAddon,
      socket: null,
      containerEl,
      sessionContainer,
      chatViewEl,
      termViewEl,
      viewMode: initialViewMode,
      tabEl,
      chatEventSource: null,
      chatMessagesEl: null,
      chatInputEl: null,
      chatSendBtn: null,
      chatCancelBtn: null,
      chatStatusBadge: null,
      chatEngineBadge: null,
      activeTurnBuffer: '',
      activeTurnHadTool: false,
      activeTurnMsgEl: null,
      promptQueue: [],
      isQueueExpanded: false,
      isQueuePaused: false,
      chatQueueContainer: null,
      chatQueueBar: null,
      chatQueueCountText: null,
      chatQueueChevron: null,
      chatQueueList: null,
      chatQueueClearBtn: null,
      chatQueueResumeBtn: null,
      pingTimer: null,
      reconnectTimer: null,
      reconnectAttempts: 0
    };
    state.openTabs.set(tabId, tabObj);

    // Setup interactive chat UI & load history
    setupChatInterface(tabObj, chatViewEl, session);

    // 4. Connect WebSocket PTY (if tmux session is active)
    if (session.engine_type !== 'headless' || session.tmux_name) {
      connectTerminalWebSocket(tabObj, tabId, term, fitAddon, session);
    }

    // 5. Automatic Viewport Re-flow & ResizeObserver
    let resizeTimeout = null;
    const resizeObserver = new ResizeObserver(() => {
      if (containerEl.classList.contains('active')) {
        if (resizeTimeout) clearTimeout(resizeTimeout);
        resizeTimeout = setTimeout(() => {
          if (tabObj.fitAddon && tabObj.fitAddon.fit) {
            try {
              tabObj.fitAddon.fit();
              if (tabObj.terminal && tabObj.socket && tabObj.socket.readyState === WebSocket.OPEN) {
                sendTerminalResize(tabObj.socket, tabObj.terminal.cols, tabObj.terminal.rows);
              }
            } catch (e) {}
          }
        }, 15);
      }
    });
    resizeObserver.observe(containerEl);
    if (termViewEl) resizeObserver.observe(termViewEl);
    tabObj.resizeObserver = resizeObserver;

    term.onData((data) => {
      const currentTab = state.openTabs.get(tabId);
      if (currentTab && currentTab.socket && currentTab.socket.readyState === WebSocket.OPEN) {
        currentTab.socket.send(data);
        // If user submitted input (e.g. Enter key), bump last_event_at and re-sort
        if (currentTab.session && (data.includes('\r') || data.includes('\n'))) {
          currentTab.session.last_event_at = new Date().toISOString();
          const targetInState = state.sessions.find(s => s.id === currentTab.session.id);
          if (targetInState) {
            targetInState.last_event_at = currentTab.session.last_event_at;
          }
          renderTree();
        }
      } else if (!currentTab || !currentTab.socket || currentTab.socket.readyState === WebSocket.CLOSED || currentTab.socket.readyState === WebSocket.CLOSING) {
        reconnectTerminalTab(tabId);
      }
    });

    term.onResize((size) => {
      const currentTab = state.openTabs.get(tabId);
      if (currentTab && currentTab.socket) {
        sendTerminalResize(currentTab.socket, size.cols, size.rows);
      }
    });

    activateTab(tabId);
    handleTabOverflow();
    savePersistedTabs();
  }

  // Open Rich Session Details Tab (For Observed & Ended Sessions)
  function openSessionDetailsTab(session) {
    if (!session) return;
    const tabId = `details_${session.id}`;

    if (state.openTabs.has(tabId)) {
      activateTab(tabId);
      return;
    }

    if (el.emptyState) el.emptyState.style.display = 'none';

    // 1. Create Tab Element
    const tabEl = document.createElement('div');
    tabEl.className = 'terminal-tab';
    tabEl.dataset.tabId = tabId;

    const titleWrap = document.createElement('div');
    titleWrap.className = 'tab-title-wrap';

    const emojiSpan = document.createElement('span');
    emojiSpan.className = 'tab-emoji';
    emojiSpan.textContent = 'ℹ';

    const unreadDot = document.createElement('span');
    unreadDot.className = 'tab-unread-dot unread-dot';
    unreadDot.style.display = (session.is_unread && state.activeTabId !== tabId) ? 'inline-block' : 'none';

    const titleSpan = document.createElement('span');
    titleSpan.className = 'tab-title';
    titleSpan.textContent = `${session.name || session.agent}`;
    titleSpan.title = session.name || session.agent;

    titleWrap.appendChild(emojiSpan);
    titleWrap.appendChild(unreadDot);
    titleWrap.appendChild(titleSpan);

    const closeBtn = document.createElement('button');
    closeBtn.className = 'tab-close-btn';
    closeBtn.textContent = '✕';
    closeBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      closeTab(tabId);
    });

    tabEl.appendChild(titleWrap);
    tabEl.appendChild(closeBtn);

    tabEl.addEventListener('click', (e) => {
      if (e.button === 0) activateTab(tabId);
    });

    tabEl.addEventListener('auxclick', (e) => {
      if (e.button === 1) {
        e.preventDefault();
        e.stopPropagation();
        closeTab(tabId);
      }
    });

    // Right-click for Tab Context Menu
    tabEl.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showTabContextMenu(e.clientX, e.clientY, tabId);
    });

    if (el.tabStrip) el.tabStrip.appendChild(tabEl);

    // 2. Create Details View DOM
    const containerEl = document.createElement('div');
    containerEl.className = 'terminal-tab-view';
    containerEl.id = `detailsView_${session.id.replace(/[^a-zA-Z0-9_-]/g, '_')}`;

    const detailsView = document.createElement('div');
    detailsView.className = 'session-details-view';
    detailsView.innerHTML = `
      <div class="details-header">
        <div class="details-title-area">
          <div class="details-title-row">
            <span class="details-title">${session.name || session.agent}</span>
            ${getAgentBadgeHtml(session.agent)}
            <span class="host-badge">@${formatHostLabel(session.host || 'local')}</span>
            ${session.git_branch ? `<span class="badge-branch">⎇ ${session.git_branch}</span>` : ''}
            <span class="host-badge">${getStateEmoji(session)} ${session.managed ? 'Managed' : 'Observed'}</span>
          </div>
          <span class="details-subtitle">${session.id}</span>
        </div>
        <div class="details-actions">
          <button class="btn btn-primary" id="detBtnResume">▶ Resume in Tmux</button>
          <button class="btn btn-secondary" id="detBtnTranscript">📜 Transcript</button>
          <button class="btn btn-secondary" id="detBtnShell">🐚 Shell</button>
          <button class="btn btn-secondary" id="detBtnDocs">📄 Docs</button>
          <button class="btn btn-secondary" id="detBtnVSCode">📂 VS Code</button>
          <button class="btn btn-secondary danger" id="detBtnKill">⛔ Terminate</button>
          <button class="btn btn-secondary danger" id="detBtnDelete">🗑 Delete</button>
        </div>
      </div>

      <div class="details-grid">
        <div class="details-card">
          <span class="details-card-label">Directory (CWD)</span>
          <span class="details-card-value">${session.cwd || '—'}</span>
        </div>
        <div class="details-card">
          <span class="details-card-label">Context Usage</span>
          <span class="details-card-value">${session.context_pct ? session.context_pct + '%' : '—'}</span>
        </div>
        <div class="details-card">
          <span class="details-card-label">Process ID (PID)</span>
          <span class="details-card-value">${session.pid ? session.pid : 'Exited'}</span>
        </div>
        <div class="details-card">
          <span class="details-card-label">Started At</span>
          <span class="details-card-value">${session.started_at ? new Date(session.started_at).toLocaleString() : '—'}</span>
        </div>
        ${session.custom_title ? `
        <div class="details-card">
          <span class="details-card-label">Custom Title</span>
          <span class="details-card-value" style="color: var(--accent-cyan);">${session.custom_title}</span>
        </div>` : ''}
        ${session.ai_title ? `
        <div class="details-card">
          <span class="details-card-label">AI Generated Title</span>
          <span class="details-card-value" style="color: var(--accent-orange);">${session.ai_title}</span>
        </div>` : ''}
      </div>

      ${session.ai_description ? `
      <div class="details-section-title">AI Task Description & Summary</div>
      <div class="details-code-box" style="color: var(--text-main); font-size: 13px;">
        ${session.ai_description}
      </div>` : ''}

      ${session.first_prompt ? `
      <div class="details-section-title">First User Prompt (Initiating Task)</div>
      <div class="details-code-box" style="white-space: pre-wrap; font-family: var(--font-mono); color: #93c5fd;">
${session.first_prompt}
      </div>` : ''}

      ${session.last_prompt && session.last_prompt !== session.first_prompt ? `
      <div class="details-section-title">Latest User Prompt</div>
      <div class="details-code-box" style="white-space: pre-wrap; font-family: var(--font-mono); color: #86efac;">
${session.last_prompt}
      </div>` : ''}

      <div class="details-section-title">Recorded Activity</div>
      <div class="details-code-box">
        ${session.activity || 'No recorded live activity. Session is stored from historical transcripts.'}
      </div>
    `;

    containerEl.appendChild(detailsView);
    if (el.terminalViewport) el.terminalViewport.appendChild(containerEl);

    // Event listeners for action buttons
    detailsView.querySelector('#detBtnResume').addEventListener('click', async () => {
      closeTab(tabId);
      const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
      try {
        const res = await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(session.id)}&action=resume`, { method: 'POST' });
        if (res.ok) {
          const data = await res.json();
          if (data.session) Object.assign(session, data.session);
        }
      } catch (e) {
        console.error('Failed to resume session:', e);
      }
      openSessionInTab(session);
      await fetchSessions();
    });

    detailsView.querySelector('#detBtnTranscript').addEventListener('click', () => {
      openTranscriptViewerTab(session);
    });

    detailsView.querySelector('#detBtnShell').addEventListener('click', () => {
      openSessionInTab({
        ...session,
        id: `shell_${session.id}`,
        name: `Shell (${session.name || 'Terminal'})`,
        managed: true
      });
    });

    detailsView.querySelector('#detBtnDocs').addEventListener('click', () => {
      showProjectDocsModal(session.cwd, session.name, session.host, session);
    });

    detailsView.querySelector('#detBtnVSCode').addEventListener('click', async () => {
      openInVSCode(session.cwd, session.host);
    });

    detailsView.querySelector('#detBtnKill').addEventListener('click', async () => {
      if (confirm(`Terminate process for "${session.name}"?`)) {
        const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
        await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(session.id)}&action=kill`, { method: 'POST' });
        await fetchSessions();
      }
    });

    detailsView.querySelector('#detBtnDelete').addEventListener('click', async () => {
      const msg = `⚠️ Permanently Delete Session "${session.name}"?\n\nThis will permanently delete the session and its transcript logs from the agent on disk (~/.claude/projects/) without the possibility for recovery.\n\nUse Archive (📦) if you only want to hide it from your active list.`;
      if (confirm(msg)) {
        const targetId = session.id;
        const targetName = session.name;
        const nativeId = session.native_id;
        state.sessions = state.sessions.filter(s => s.id !== targetId && s.name !== targetName && (!nativeId || s.native_id !== nativeId));
        closeTab(tabId);
        closeTab(targetId);
        if (nativeId) closeTab(nativeId);
        closeTab(`details_${targetId}`);
        for (const [tId, tabObj] of state.openTabs.entries()) {
          if (tabObj.session && (tabObj.session.id === targetId || (nativeId && tabObj.session.native_id === nativeId))) {
            closeTab(tId);
          }
        }
        renderTree();
        const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
        await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(targetId)}&action=delete`, { method: 'POST' });
        await fetchSessions();
      }
    });

    state.openTabs.set(tabId, {
      type: 'details',
      session,
      containerEl,
      tabEl,
      fitAddon: { fit: () => {} }
    });

    activateTab(tabId);
    handleTabOverflow();
  }

  // Open In-UI Markdown Document Viewer Tab
  async function openDocViewerTab(docPath, docTitle, host = 'local') {
    if (!docPath) return;

    const hostKey = isLocalHost(host) ? getSelfHostName() : host;
    const tabId = `doc_${hostKey.replace(/[^a-zA-Z0-9_-]/g, '_')}_${docPath.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    if (state.openTabs.has(tabId)) {
      activateTab(tabId);
      return;
    }

    const isRemote = !isLocalHost(host);
    const targetHost = state.hosts.find(h => h.name === host);
    const baseUrl = (isRemote && targetHost && targetHost.url) ? targetHost.url.replace(/\/$/, '') : '';

    let markdownContent = '# Loading document...';
    try {
      const res = await fetch(`${baseUrl}/v1/documents/content?path=${encodeURIComponent(docPath)}`);
      if (res.ok) {
        const data = await res.json();
        markdownContent = data.content || '*(Document is empty)*';
      } else {
        markdownContent = `⚠️ Failed to load document: ${res.statusText}`;
      }
    } catch (e) {
      markdownContent = `⚠️ Error loading document: ${e.message}`;
    }

    if (el.emptyState) el.emptyState.style.display = 'none';

    // 1. Tab DOM Element
    const tabEl = document.createElement('div');
    tabEl.className = 'terminal-tab';
    tabEl.dataset.tabId = tabId;

    const titleWrap = document.createElement('div');
    titleWrap.className = 'tab-title-wrap';

    const emojiSpan = document.createElement('span');
    emojiSpan.className = 'tab-emoji';
    emojiSpan.textContent = '📄';

    const titleSpan = document.createElement('span');
    titleSpan.className = 'tab-title';
    titleSpan.textContent = docTitle || docPath.split('/').pop();

    titleWrap.appendChild(emojiSpan);
    titleWrap.appendChild(titleSpan);

    if (isRemote) {
      const hostSpan = document.createElement('span');
      hostSpan.className = 'badge-host';
      hostSpan.textContent = `@${formatHostLabel(host)}`;
      titleWrap.appendChild(hostSpan);
    }

    const closeBtn = document.createElement('button');
    closeBtn.className = 'tab-close-btn';
    closeBtn.textContent = '✕';
    closeBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      closeTab(tabId);
    });

    tabEl.appendChild(titleWrap);
    tabEl.appendChild(closeBtn);

    tabEl.addEventListener('click', (e) => {
      if (e.button === 0) activateTab(tabId);
    });

    tabEl.addEventListener('auxclick', (e) => {
      if (e.button === 1) {
        e.preventDefault();
        e.stopPropagation();
        closeTab(tabId);
      }
    });

    // Right-click for Tab Context Menu
    tabEl.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showTabContextMenu(e.clientX, e.clientY, tabId);
    });

    if (el.tabStrip) el.tabStrip.appendChild(tabEl);

    // 2. Document Viewer View DOM
    const containerEl = document.createElement('div');
    containerEl.className = 'terminal-tab-view';
    containerEl.id = `docView_${tabId}`;

    const docContainer = document.createElement('div');
    docContainer.className = 'doc-viewer-container';

    const renderedHtml = renderMarkdown(markdownContent);

    docContainer.innerHTML = `
      <div class="doc-viewer-header">
        <div>
          <div class="doc-viewer-title">📄 ${docTitle || docPath.split('/').pop()} ${isRemote ? `<span class="badge-host">@${formatHostLabel(host)}</span>` : ''}</div>
          <div class="doc-viewer-path">${docPath}</div>
        </div>
        <div>
          <button class="btn btn-secondary" id="docBtnVSCode">📂 Open in VS Code</button>
        </div>
      </div>
      <div class="markdown-body">
        ${renderedHtml}
      </div>
    `;

    containerEl.appendChild(docContainer);
    if (el.terminalViewport) el.terminalViewport.appendChild(containerEl);

    docContainer.querySelector('#docBtnVSCode').addEventListener('click', async () => {
      openInVSCode(docPath, host);
    });

    state.openTabs.set(tabId, {
      type: 'doc',
      session: { name: docTitle, cwd: docPath, host: host },
      containerEl,
      tabEl,
      fitAddon: { fit: () => {} }
    });

    activateTab(tabId);
    handleTabOverflow();
  }

  // Open In-UI Rich Transcript Viewer Tab
  async function openTranscriptViewerTab(session) {
    if (!session) return;

    const tabId = `transcript_${session.id.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    if (state.openTabs.has(tabId)) {
      activateTab(tabId);
      return;
    }

    const host = session.host || getSelfHostName();
    const isRemote = !isLocalHost(host);
    const targetHost = state.hosts.find(h => h.name === host);
    const baseUrl = (isRemote && targetHost && targetHost.url) ? targetHost.url.replace(/\/$/, '') : (session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '');

    let transcriptData = null;
    let errorMsg = '';
    try {
      const res = await fetch(`${baseUrl}/v1/sessions/transcript?id=${encodeURIComponent(session.id)}&format=json`);
      if (res.ok) {
        transcriptData = await res.json();
      } else {
        errorMsg = `Failed to fetch transcript: ${res.statusText}`;
      }
    } catch (e) {
      errorMsg = `Error loading transcript: ${e.message}`;
    }

    if (el.emptyState) el.emptyState.style.display = 'none';

    // 1. Tab DOM Element
    const tabEl = document.createElement('div');
    tabEl.className = 'terminal-tab';
    tabEl.dataset.tabId = tabId;

    const titleWrap = document.createElement('div');
    titleWrap.className = 'tab-title-wrap';

    const emojiSpan = document.createElement('span');
    emojiSpan.className = 'tab-emoji';
    emojiSpan.textContent = '📜';

    const titleSpan = document.createElement('span');
    titleSpan.className = 'tab-title';
    titleSpan.textContent = `Transcript: ${session.name || session.agent}`;

    titleWrap.appendChild(emojiSpan);
    titleWrap.appendChild(titleSpan);

    if (isRemote) {
      const hostSpan = document.createElement('span');
      hostSpan.className = 'badge-host';
      hostSpan.textContent = `@${formatHostLabel(host)}`;
      titleWrap.appendChild(hostSpan);
    }

    const closeBtn = document.createElement('button');
    closeBtn.className = 'tab-close-btn';
    closeBtn.textContent = '✕';
    closeBtn.title = 'Close Tab';
    closeBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      closeTab(tabId);
    });

    tabEl.appendChild(titleWrap);
    tabEl.appendChild(closeBtn);

    tabEl.addEventListener('click', (e) => {
      if (e.button === 0) activateTab(tabId);
    });

    tabEl.addEventListener('auxclick', (e) => {
      if (e.button === 1) {
        e.preventDefault();
        e.stopPropagation();
        closeTab(tabId);
      }
    });

    // Right-click for Tab Context Menu
    tabEl.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showTabContextMenu(e.clientX, e.clientY, tabId);
    });

    if (el.tabStrip) el.tabStrip.appendChild(tabEl);

    // 2. Transcript Container
    const containerEl = document.createElement('div');
    containerEl.className = 'terminal-tab-view';
    containerEl.id = `transcriptView_${tabId}`;

    const transContainer = document.createElement('div');
    transContainer.className = 'transcript-viewer-container';

    let messagesHtml = '';
    if (transcriptData && transcriptData.messages && transcriptData.messages.length > 0) {
      messagesHtml = transcriptData.messages.map((m, idx) => {
        const timeStr = m.timestamp ? new Date(m.timestamp).toLocaleTimeString() : '';
        if (m.role === 'user') {
          return `
            <div class="transcript-msg user-msg">
              <div class="msg-header">
                <span class="msg-role">👤 User</span>
                <div class="chat-msg-actions">
                  <span class="msg-time">${timeStr}</span>
                  <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                    </svg>
                  </button>
                </div>
              </div>
              <div class="msg-body">${escapeHtml(m.content || '')}</div>
            </div>
          `;
        } else if (m.role === 'assistant') {
          const bodyHtml = renderMarkdown(m.content || '');
          const toolsHtml = m.tool_calls && m.tool_calls.length > 0 ? `
            <div class="msg-tools">
              ${m.tool_calls.map(tc => `<span class="tool-tag">⚡ ${escapeHtml(tc)}</span>`).join(' ')}
            </div>
          ` : '';
          const thinkingHtml = m.thinking ? `
            <details class="msg-thinking">
              <summary>💭 Thought Process</summary>
              <div class="thinking-content">${escapeHtml(m.thinking).replace(/\n/g, '<br/>')}</div>
            </details>
          ` : '';
          return `
            <div class="transcript-msg assistant-msg">
              <div class="msg-header">
                <span class="msg-role">🤖 Assistant</span>
                <div class="chat-msg-actions">
                  <span class="msg-time">${timeStr}</span>
                  <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                    </svg>
                  </button>
                </div>
              </div>
              ${toolsHtml}
              ${thinkingHtml}
              <div class="markdown-body">${bodyHtml}</div>
            </div>
          `;
        } else {
          return `
            <div class="transcript-msg system-msg">
              <span class="system-tag">ℹ️ System [${timeStr}]:</span> ${escapeHtml(m.content)}
            </div>
          `;
        }
      }).join('');
    } else {
      messagesHtml = `
        <div class="transcript-empty">
          <p>${errorMsg || 'No transcript messages found for this session on disk.'}</p>
        </div>
      `;
    }

    transContainer.innerHTML = `
      <div class="transcript-header">
        <div class="transcript-header-info">
          <div class="transcript-title">📜 ${escapeHtml(session.name || session.agent)}</div>
          <div class="transcript-subtitle">${escapeHtml(session.cwd || '')} • ${getAgentBadgeHtml(session.agent)} • ${transcriptData && transcriptData.messages ? transcriptData.messages.length : 0} steps</div>
        </div>
        <div class="transcript-header-actions">
          <button class="btn btn-primary" id="transBtnResume">▶ Attach in Terminal</button>
          <button class="btn btn-secondary" id="transBtnDocs">📄 Docs</button>
          <button class="btn btn-secondary" id="transBtnVSCode">📂 VS Code</button>
        </div>
      </div>
      <div class="transcript-body">
        ${messagesHtml}
      </div>
    `;

    containerEl.appendChild(transContainer);
    if (el.terminalViewport) el.terminalViewport.appendChild(containerEl);

    transContainer.querySelectorAll('.transcript-msg').forEach(msgEl => {
      attachChatMessageListeners(msgEl);
      linkifyChatFiles(msgEl, session);
    });
    attachCodeBlockCopyButtons(transContainer);

    transContainer.querySelector('#transBtnResume').addEventListener('click', () => {
      openSessionInTab(session);
    });

    transContainer.querySelector('#transBtnDocs').addEventListener('click', () => {
      showProjectDocsModal(session.cwd, session.name, session.host, session);
    });

    transContainer.querySelector('#transBtnVSCode').addEventListener('click', async () => {
      openInVSCode(session.cwd, session.host);
    });

    state.openTabs.set(tabId, {
      type: 'transcript',
      session,
      containerEl,
      tabEl,
      fitAddon: { fit: () => {} }
    });

    activateTab(tabId);
    handleTabOverflow();
  }

  function escapeHtml(str) {
    if (!str) return '';
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#039;');
  }

  function formatBytes(bytes) {
    if (!bytes || bytes === 0) return '';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  // Show Project Documents Auto-Discovery Modal with Real-Time Search and Categorization
  async function showProjectDocsModal(cwd, title, host = 'local', session = null) {
    const isRemote = !isLocalHost(host);
    const targetHost = state.hosts.find(h => h.name === host);
    const baseUrl = (isRemote && targetHost && targetHost.url) ? targetHost.url.replace(/\/$/, '') : '';

    const agent = session?.agent || '';
    const nativeId = session?.native_id || '';
    const sessionId = session?.id || '';

    try {
      const qParams = new URLSearchParams({
        cwd: cwd || '',
        agent: agent,
        native_id: nativeId,
        session_id: sessionId
      });
      const res = await fetch(`${baseUrl}/v1/documents?${qParams.toString()}`);
      const docs = await res.json() || [];

      if (docs.length === 0) {
        alert(`No markdown documents (task.md, AGENTS.md, README.md, docs/, etc.) found for ${title || cwd}.`);
        return;
      }

      const renderDocCards = (items) => {
        if (items.length === 0) {
          return `<div style="text-align: center; padding: 24px 0; color: var(--text-dim); font-size: 13px;">No documents match your search.</div>`;
        }
        return items.map((d, index) => {
          const categoryClass = d.category || 'other';
          const categoryBadge = d.category_label ? `<span class="doc-badge-tag ${categoryClass}">${d.category_label}</span>` : '';
          const hostBadge = isRemote ? `<span class="badge-host">@${formatHostLabel(host)}</span>` : '';
          
          return `
            <div class="doc-card-item ${index === 0 ? 'selected' : ''}" data-path="${encodeURIComponent(d.path)}" data-title="${encodeURIComponent(d.title)}" data-host="${encodeURIComponent(host)}">
              <div class="doc-card-info">
                <span style="font-size: 18px; flex-shrink: 0;">📄</span>
                <div class="doc-card-details">
                  <div class="doc-card-title-row">
                    <span class="doc-card-title">${escapeHtml(d.title)}</span>
                    ${categoryBadge}
                    ${hostBadge}
                  </div>
                  <div class="doc-card-path" title="${escapeHtml(d.path)}">${escapeHtml(d.rel_path || d.path)}</div>
                </div>
              </div>
              <div class="doc-card-meta">
                ${d.size ? `<span style="font-size: 10px; color: var(--text-dim); font-family: var(--font-mono);">${formatBytes(d.size)}</span>` : ''}
              </div>
            </div>
          `;
        }).join('');
      };

      const bodyHtml = `
        <div class="doc-search-box">
          <input type="text" id="docSearchInput" class="doc-search-input" placeholder="🔍 Search documents by name, category, or path..." autocomplete="off">
        </div>
        <div class="doc-list-scrollable" id="docListContainer">
          ${renderDocCards(docs)}
        </div>
      `;

      const footerHtml = `
        <span style="font-size: 11px; color: var(--text-dim); margin-right: auto;" id="docCountLabel">${docs.length} document${docs.length === 1 ? '' : 's'} found</span>
        <button class="btn btn-secondary" onclick="document.getElementById('modalOverlay').style.display='none'">Close</button>
      `;

      showModal(`Project Documents — ${title || cwd}`, bodyHtml, footerHtml);

      const searchInput = document.getElementById('docSearchInput');
      const listContainer = document.getElementById('docListContainer');
      const countLabel = document.getElementById('docCountLabel');

      let currentFiltered = [...docs];

      function attachItemClicks() {
        if (!listContainer) return;
        listContainer.querySelectorAll('.doc-card-item').forEach(card => {
          card.addEventListener('click', () => {
            const p = decodeURIComponent(card.dataset.path);
            const t = decodeURIComponent(card.dataset.title);
            const h = decodeURIComponent(card.dataset.host || getSelfHostName());
            hideModal();
            openDocViewerTab(p, t, h);
          });
        });
      }

      attachItemClicks();

      if (searchInput) {
        setTimeout(() => searchInput.focus(), 60);
        searchInput.addEventListener('input', (e) => {
          const query = e.target.value.toLowerCase().trim();
          if (!query) {
            currentFiltered = [...docs];
          } else {
            currentFiltered = docs.filter(d => {
              const t = (d.title || '').toLowerCase();
              const p = (d.path || '').toLowerCase();
              const r = (d.rel_path || '').toLowerCase();
              const c = (d.category_label || '').toLowerCase();
              return t.includes(query) || p.includes(query) || r.includes(query) || c.includes(query);
            });
          }
          if (listContainer) {
            listContainer.innerHTML = renderDocCards(currentFiltered);
            attachItemClicks();
          }
          if (countLabel) {
            countLabel.textContent = `${currentFiltered.length} of ${docs.length} documents`;
          }
        });

        searchInput.addEventListener('keydown', (e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            if (currentFiltered.length > 0) {
              hideModal();
              openDocViewerTab(currentFiltered[0].path, currentFiltered[0].title, host);
            }
          }
        });
      }
    } catch (err) {
      alert('Error fetching documents: ' + err.message);
    }
  }

  function sendTerminalResize(socket, cols, rows) {
    if (socket && socket.readyState === WebSocket.OPEN && cols >= 10 && rows >= 4) {
      socket.send(JSON.stringify({ type: 'resize', cols, rows }));
    }
  }

  // Activate Tab
  function activateTab(tabId) {
    if (state.activeTabId && state.activeTabId !== tabId) {
      const prevTab = state.openTabs.get(state.activeTabId);
      if (prevTab && prevTab.session && prevTab.session.is_unread) {
        markSessionAsRead(prevTab.session);
      }
    }
    state.activeTabId = tabId;

    state.openTabs.forEach((tab, id) => {
      const isActive = id === tabId;
      tab.tabEl.classList.toggle('active', isActive);
      tab.containerEl.classList.toggle('active', isActive);

      if (isActive) {
        const unreadDot = tab.tabEl.querySelector('.tab-unread-dot');
        if (unreadDot) unreadDot.style.display = 'none';
        if (tab.session) {
          markSessionAsRead(tab.session);
          if (tab.session.node_path) {
            state.lastActiveGroup = tab.session.node_path;
          }
        }
        if (el.viewModeToggle) {
          if (tab.session) {
            el.viewModeToggle.style.display = 'inline-flex';
            const mode = tab.viewMode || (tab.session.engine_type === 'headless' ? 'chat' : 'terminal');
            updateViewModeButtons(mode);
          } else {
            el.viewModeToggle.style.display = 'none';
          }
        }
        if (tab.type === 'terminal') {
          if (tab.viewMode === 'chat' && tab.chatInputEl) {
            tab.chatInputEl.focus();
          } else if (!tab.socket || tab.socket.readyState === WebSocket.CLOSED || tab.socket.readyState === WebSocket.CLOSING) {
            reconnectTerminalTab(tabId);
          }
        }
        setTimeout(() => {
          if (tab.fitAddon && tab.fitAddon.fit) tab.fitAddon.fit();
          if (tab.terminal && tab.viewMode !== 'chat') {
            tab.terminal.focus();
            sendTerminalResize(tab.socket, tab.terminal.cols, tab.terminal.rows);
          }
        }, 30);
        setTimeout(() => {
          if (tab.fitAddon && tab.fitAddon.fit) {
            try {
              tab.fitAddon.fit();
              if (tab.terminal && tab.viewMode !== 'chat') {
                sendTerminalResize(tab.socket, tab.terminal.cols, tab.terminal.rows);
              }
            } catch (e) {}
          }
        }, 120);
        updateStatusbar(tab.session);
        if (tab.session && tab.session.engine_type === 'headless' && tab.viewMode === 'chat') {
          connectChatStream(tab);
        }
      } else {
        disconnectChatStream(tab);
      }
    });

    savePersistedTabs();
    renderTree();
  }

  // Close Tab
  function closeTab(tabId) {
    const tab = state.openTabs.get(tabId);
    if (!tab) return;

    if (tab.chatEventSource) {
      try { tab.chatEventSource.close(); } catch (e) {}
      tab.chatEventSource = null;
    }
    if (tab.pingTimer) {
      clearInterval(tab.pingTimer);
      tab.pingTimer = null;
    }
    if (tab.reconnectTimer) {
      clearTimeout(tab.reconnectTimer);
      tab.reconnectTimer = null;
    }

    if (tab.resizeObserver) {
      try { tab.resizeObserver.disconnect(); } catch (e) {}
    }
    if (tab.socket) {
      try {
        tab.socket.onopen = null;
        tab.socket.onclose = null;
        tab.socket.onerror = null;
        tab.socket.onmessage = null;
        tab.socket.close();
      } catch (e) {}
    }
    if (tab.terminal) {
      try { tab.terminal.dispose(); } catch (e) {}
    }
    if (tab.tabEl && tab.tabEl.parentNode) {
      tab.tabEl.parentNode.removeChild(tab.tabEl);
    }
    if (tab.containerEl && tab.containerEl.parentNode) {
      tab.containerEl.parentNode.removeChild(tab.containerEl);
    }

    state.openTabs.delete(tabId);

    if (state.activeTabId === tabId) {
      const remainingTabIds = Array.from(state.openTabs.keys());
      if (remainingTabIds.length > 0) {
        activateTab(remainingTabIds[remainingTabIds.length - 1]);
      } else {
        state.activeTabId = null;
        if (el.emptyState) el.emptyState.style.display = 'flex';
        resetStatusbar();
      }
    }

    savePersistedTabs();
    handleTabOverflow();
    renderTree();
  }

  // Close All Other Tabs Except KeepTabId
  function closeOtherTabs(keepTabId) {
    const tabIds = Array.from(state.openTabs.keys());
    tabIds.forEach(id => {
      if (id !== keepTabId) {
        closeTab(id);
      }
    });
    if (state.openTabs.has(keepTabId)) {
      activateTab(keepTabId);
    }
  }

  // Close All Tabs to the Right of TargetTabId
  function closeTabsToTheRight(targetTabId) {
    const tabIds = Array.from(state.openTabs.keys());
    const idx = tabIds.indexOf(targetTabId);
    if (idx !== -1) {
      for (let i = idx + 1; i < tabIds.length; i++) {
        closeTab(tabIds[i]);
      }
    }
  }

  // Close All Open Tabs
  function closeAllTabs() {
    const tabIds = Array.from(state.openTabs.keys());
    tabIds.forEach(id => closeTab(id));
  }

  // Auto-reconnect active tabs on Window Focus, Visibility Change, and Network Online
  function checkAndReconnectActiveTabs() {
    state.openTabs.forEach((tab, tabId) => {
      if (tab.type === 'terminal') {
        if (!tab.socket || tab.socket.readyState === WebSocket.CLOSED || tab.socket.readyState === WebSocket.CLOSING) {
          reconnectTerminalTab(tabId);
        }
      }
    });
  }

  window.addEventListener('focus', checkAndReconnectActiveTabs);
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      checkAndReconnectActiveTabs();
      fetchSessions();
    }
  });
  window.addEventListener('online', () => {
    checkAndReconnectActiveTabs();
    fetchSessions();
  });

  // Persist Open Tabs to localStorage
  function savePersistedTabs() {
    try {
      const list = [];
      state.openTabs.forEach((tab, tabId) => {
        list.push({
          tabId,
          type: tab.type,
          sessionId: tab.session ? tab.session.id : null,
          host: tab.session ? (tab.session.host || getSelfHostName()) : getSelfHostName()
        });
      });
      localStorage.setItem('ackbar_persisted_tabs', JSON.stringify(list));
      if (state.activeTabId) {
        localStorage.setItem('ackbar_active_tab_id', state.activeTabId);
      }
    } catch (e) {}
  }

  // Restore Open Tabs from localStorage on Load / Refresh
  function restorePersistedTabs() {
    try {
      const savedStr = localStorage.getItem('ackbar_persisted_tabs');
      if (!savedStr) return;
      const list = JSON.parse(savedStr);
      if (!Array.isArray(list) || list.length === 0) return;

      list.forEach(item => {
        if (item.sessionId) {
          const sess = state.sessions.find(s => s.id === item.sessionId);
          if (sess) {
            if (item.type === 'terminal') {
              openSessionInTab(sess);
            } else if (item.type === 'details') {
              openSessionDetailsTab(sess);
            }
          }
        }
      });

      const activeId = localStorage.getItem('ackbar_active_tab_id');
      if (activeId && state.openTabs.has(activeId)) {
        activateTab(activeId);
      }
    } catch (e) {}
  }

  // Update Tab States (Emojis & Titles) when Session Events arrive
  function updateOpenTabsState() {
    state.openTabs.forEach((tab, id) => {
      const updatedSess = state.sessions.find(s => s.id === id || `details_${s.id}` === id);
      if (updatedSess) {
        tab.session = updatedSess;
        const emojiEl = tab.tabEl.querySelector('.tab-emoji');
        const titleEl = tab.tabEl.querySelector('.tab-title');
        let unreadDot = tab.tabEl.querySelector('.tab-unread-dot');
        if (!unreadDot) {
          unreadDot = document.createElement('span');
          unreadDot.className = 'tab-unread-dot unread-dot';
          const titleWrap = tab.tabEl.querySelector('.tab-title-wrap');
          if (titleWrap && titleEl) {
            titleWrap.insertBefore(unreadDot, titleEl);
          }
        }
        if (unreadDot) {
          unreadDot.style.display = (updatedSess.is_unread && state.activeTabId !== id) ? 'inline-block' : 'none';
        }
        if (emojiEl && tab.type === 'terminal') emojiEl.innerHTML = getStateEmoji(updatedSess);
        if (titleEl && tab.type !== 'doc') titleEl.textContent = updatedSess.name || updatedSess.agent;

        const timeFull = formatFullDateTime(updatedSess.last_event_at || updatedSess.started_at);
        const timeRel = formatRelativeTime(updatedSess.last_event_at || updatedSess.started_at);
        tab.tabEl.title = `${updatedSess.name || updatedSess.agent}\nHost: @${formatHostLabel(updatedSess.host || 'local')}${timeRel ? `\nLast Active: ${timeRel} (${timeFull})` : ''}`;

        if (state.activeTabId === id) {
          updateStatusbar(updatedSess);
        }
      }
    });
  }

  // Handle Tab Overflow Dropdown on Window Resize
  function handleTabOverflow() {
    if (!el.tabStrip || !el.tabOverflowDropdown) return;

    const availableWidth = el.tabStrip.clientWidth;
    const scrollWidth = el.tabStrip.scrollWidth;

    if (scrollWidth > availableWidth + 5 && state.openTabs.size > 2) {
      el.tabOverflowDropdown.style.display = 'block';
      const hiddenCount = Math.max(1, Math.floor((scrollWidth - availableWidth) / 140));
      if (el.tabOverflowCount) el.tabOverflowCount.textContent = `▾ ${hiddenCount} tabs`;
      renderOverflowMenu();
    } else {
      el.tabOverflowDropdown.style.display = 'none';
      if (el.overflowMenuContent) el.overflowMenuContent.classList.remove('show');
    }
  }

  function renderOverflowMenu() {
    if (!el.overflowMenuContent) return;
    el.overflowMenuContent.innerHTML = '';

    state.openTabs.forEach((tab, tabId) => {
      const item = document.createElement('div');
      item.className = 'overflow-item';
      const icon = tab.type === 'doc' ? '📄' : (tab.type === 'details' ? 'ℹ' : getStateEmoji(tab.session));
      item.innerHTML = `<span>${icon} ${tab.session.name || tab.session.agent}</span>`;
      item.addEventListener('click', (e) => {
        if (e.button === 0) {
          activateTab(tabId);
          if (el.overflowMenuContent) el.overflowMenuContent.classList.remove('show');
        }
      });
      item.addEventListener('auxclick', (e) => {
        if (e.button === 1) {
          e.preventDefault();
          e.stopPropagation();
          closeTab(tabId);
        }
      });
      el.overflowMenuContent.appendChild(item);
    });
  }

  // Right-Click Session Context Menu
  function showContextMenu(x, y, session) {
    if (!el.contextMenu) return;
    hideGroupContextMenu();
    hideTabContextMenu();
    hideChatFileContextMenu();
    state.contextMenuSession = session;

    if (el.cmItemDone) {
      const isDone = isSessionDone(session);
      el.cmItemDone.innerHTML = isDone ? '<span>↺</span> Move to Active' : '<span>✓</span> Mark as Done';
    }

    if (el.cmItemArchive) {
      el.cmItemArchive.innerHTML = session.archived ? '<span>📦</span> Unarchive Session' : '<span>📦</span> Archive Session';
    }

    el.contextMenu.style.visibility = 'hidden';
    el.contextMenu.style.display = 'block';

    const menuWidth = el.contextMenu.offsetWidth || 220;
    const menuHeight = el.contextMenu.offsetHeight || 440;

    let posX = x;
    let posY = y;

    // Reposition upward if too close to bottom of screen
    if (posY + menuHeight > window.innerHeight - 10) {
      posY = Math.max(10, window.innerHeight - menuHeight - 10);
    }

    // Reposition leftward if too close to right edge of screen
    if (posX + menuWidth > window.innerWidth - 10) {
      posX = Math.max(10, window.innerWidth - menuWidth - 10);
    }

    el.contextMenu.style.left = `${posX}px`;
    el.contextMenu.style.top = `${posY}px`;
    el.contextMenu.style.visibility = 'visible';
  }

  function hideContextMenu() {
    if (el.contextMenu) el.contextMenu.style.display = 'none';
    state.contextMenuSession = null;
  }

  // Right-Click Group Folder Context Menu
  function showGroupContextMenu(x, y, groupPath) {
    if (!el.groupContextMenu) return;
    hideContextMenu();
    hideTabContextMenu();
    hideChatFileContextMenu();
    state.contextMenuGroupPath = groupPath;

    el.groupContextMenu.style.visibility = 'hidden';
    el.groupContextMenu.style.display = 'block';

    const menuWidth = el.groupContextMenu.offsetWidth || 220;
    const menuHeight = el.groupContextMenu.offsetHeight || 220;

    let posX = x;
    let posY = y;

    if (posY + menuHeight > window.innerHeight - 10) {
      posY = Math.max(10, window.innerHeight - menuHeight - 10);
    }

    if (posX + menuWidth > window.innerWidth - 10) {
      posX = Math.max(10, window.innerWidth - menuWidth - 10);
    }

    el.groupContextMenu.style.left = `${posX}px`;
    el.groupContextMenu.style.top = `${posY}px`;
    el.groupContextMenu.style.visibility = 'visible';
  }

  function hideGroupContextMenu() {
    if (el.groupContextMenu) el.groupContextMenu.style.display = 'none';
    state.contextMenuGroupPath = null;
  }

  // Right-Click Tab Context Menu
  function showTabContextMenu(x, y, tabId) {
    if (!el.tabContextMenu) return;
    hideContextMenu();
    hideGroupContextMenu();
    hideChatFileContextMenu();
    state.contextMenuTabId = tabId;

    const tabIds = Array.from(state.openTabs.keys());
    const tabIdx = tabIds.indexOf(tabId);

    if (el.tcmItemCloseOthers) {
      el.tcmItemCloseOthers.style.display = tabIds.length > 1 ? 'flex' : 'none';
    }
    if (el.tcmItemCloseRight) {
      el.tcmItemCloseRight.style.display = (tabIdx !== -1 && tabIdx < tabIds.length - 1) ? 'flex' : 'none';
    }

    el.tabContextMenu.style.visibility = 'hidden';
    el.tabContextMenu.style.display = 'block';

    const menuWidth = el.tabContextMenu.offsetWidth || 200;
    const menuHeight = el.tabContextMenu.offsetHeight || 160;

    let posX = x;
    let posY = y;

    if (posY + menuHeight > window.innerHeight - 10) {
      posY = Math.max(10, window.innerHeight - menuHeight - 10);
    }

    if (posX + menuWidth > window.innerWidth - 10) {
      posX = Math.max(10, window.innerWidth - menuWidth - 10);
    }

    el.tabContextMenu.style.left = `${posX}px`;
    el.tabContextMenu.style.top = `${posY}px`;
    el.tabContextMenu.style.visibility = 'visible';
  }

  function hideTabContextMenu() {
    if (el.tabContextMenu) el.tabContextMenu.style.display = 'none';
    state.contextMenuTabId = null;
  }

  // Command Palette (`Cmd+K` / `Ctrl+K`)
  function toggleCommandPalette() {
    if (!el.cmdPaletteOverlay) return;
    const isVisible = el.cmdPaletteOverlay.style.display === 'flex';
    if (isVisible) {
      el.cmdPaletteOverlay.style.display = 'none';
    } else {
      el.cmdPaletteOverlay.style.display = 'flex';
      if (el.cmdPaletteInput) {
        el.cmdPaletteInput.value = '';
        el.cmdPaletteInput.focus();
      }
      renderCommandPaletteResults('');
    }
  }

  function renderCommandPaletteResults(query) {
    if (!el.cmdPaletteResults) return;
    el.cmdPaletteResults.innerHTML = '';
    state.cmdPaletteItems = [];

    const q = query.toLowerCase().trim();

    // 1. Actions
    const actions = [
      { title: '＋ Create New Project / Subgroup', subtitle: 'Action', action: showNewGroupModal },
      { title: '＋ Register Remote SSH Host', subtitle: 'Action', action: showAddHostModal },
      { title: '⚡ Agent Hooks & Discovery', subtitle: 'Action', action: showHooksDashboardModal },
      { title: '⚙️ Workspace & Lifecycle Settings', subtitle: 'Action', action: showSettingsModal },
      { title: '↻ Database Maintenance & Rescan', subtitle: 'Action', action: () => el.btnPurge.click() },
      { title: '📦 Toggle Show/Hide Archived Sessions', subtitle: 'Action', action: () => el.btnToggleArchived.click() }
    ];

    actions.forEach(a => {
      if (!q || a.title.toLowerCase().includes(q)) {
        state.cmdPaletteItems.push(a);
      }
    });

    // 2. Open Sessions
    state.sessions.forEach(sess => {
      const match = !q || (sess.name && sess.name.toLowerCase().includes(q)) ||
                          (sess.git_branch && sess.git_branch.toLowerCase().includes(q)) ||
                          (sess.cwd && sess.cwd.toLowerCase().includes(q)) ||
                          (sess.agent && sess.agent.toLowerCase().includes(q));
      if (match) {
        state.cmdPaletteItems.push({
          title: `${getStateEmojiText(sess)} ${sess.name || sess.agent}`,
          subtitle: `${sess.host ? '@' + formatHostLabel(sess.host) : '@local'}${sess.git_branch ? ' • ⎇ ' + sess.git_branch : ''} • ${sess.cwd || ''}`,
          action: () => openSessionInTab(sess)
        });
      }
    });

    // 3. Tree Groups
    state.treeNodes.forEach(n => {
      if (!q || n.path.toLowerCase().includes(q)) {
        state.cmdPaletteItems.push({
          title: `📁 ${n.path}`,
          subtitle: `Group folder • ${n.project_dir || ''}`,
          action: () => {
            state.searchQuery = n.path;
            if (el.searchInput) el.searchInput.value = n.path;
            renderTree();
          }
        });
      }
    });

    state.cmdPaletteSelectedIndex = 0;
    state.cmdPaletteItems.slice(0, 15).forEach((item, idx) => {
      const itemEl = document.createElement('div');
      itemEl.className = `cmd-item ${idx === 0 ? 'selected' : ''}`;
      itemEl.innerHTML = `
        <div class="cmd-item-left">${item.title}</div>
        <div class="cmd-item-right">${item.subtitle}</div>
      `;
      itemEl.addEventListener('click', () => {
        el.cmdPaletteOverlay.style.display = 'none';
        item.action();
      });
      el.cmdPaletteResults.appendChild(itemEl);
    });
  }

  // Update Statusbar
  function updateStatusbar(session) {
    if (!session) return;
    if (el.sbSessionName) el.sbSessionName.textContent = session.name || session.agent;
    if (el.sbHostBadge) el.sbHostBadge.textContent = `@${formatHostLabel(session.host || 'local')}`;
    if (el.sbCwd) el.sbCwd.textContent = session.cwd || '~/';
    if (el.sbGitBranch) el.sbGitBranch.textContent = session.git_branch ? `⎇ ${session.git_branch}` : '⎇ —';
    if (el.sbContextGauge) el.sbContextGauge.textContent = session.context_pct ? `ctx: ${session.context_pct}%` : 'ctx: —';
    if (el.sbModelBadge) {
      const engineLabel = session.engine_type === 'headless' ? 'headless' : 'tmux';
      el.sbModelBadge.textContent = `${session.agent || 'claude'} (${engineLabel})`;
    }
    if (el.sbPID) el.sbPID.textContent = session.pid ? `PID ${session.pid}` : 'PID —';
    if (el.sbLastActive) {
      const timeRel = formatRelativeTime(session.last_event_at || session.started_at);
      const timeFull = formatFullDateTime(session.last_event_at || session.started_at);
      el.sbLastActive.textContent = timeRel ? `🕒 ${timeRel}` : '🕒 —';
      el.sbLastActive.title = timeFull ? `Last activity: ${timeFull}` : 'Last activity unknown';
    }
  }

  function resetStatusbar() {
    if (el.sbSessionName) el.sbSessionName.textContent = '—';
    if (el.sbHostBadge) el.sbHostBadge.textContent = '@local';
    if (el.sbCwd) el.sbCwd.textContent = '~/';
    if (el.sbGitBranch) el.sbGitBranch.textContent = '⎇ —';
    if (el.sbContextGauge) el.sbContextGauge.textContent = 'ctx: —';
    if (el.sbModelBadge) el.sbModelBadge.textContent = 'claude';
    if (el.sbPID) el.sbPID.textContent = 'PID —';
    if (el.sbLastActive) {
      el.sbLastActive.textContent = '🕒 —';
      el.sbLastActive.title = 'No active session';
    }
  }

  // Setup Event Listeners & Global Shortcuts
  function setupEventListeners() {
    // Dismiss context menus on outside click or escape
    document.addEventListener('click', () => {
      hideContextMenu();
      hideGroupContextMenu();
      hideTabContextMenu();
      hideChatFileContextMenu();
    });

    window.addEventListener('resize', hideChatFileContextMenu);
    window.addEventListener('scroll', hideChatFileContextMenu, true);

    // Prevent browser default behavior of opening dropped files in a new tab
    window.addEventListener('dragover', (e) => {
      if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        e.preventDefault();
      }
    });

    window.addEventListener('drop', (e) => {
      if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        e.preventDefault();
      }
    });

    window.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        hideContextMenu();
        hideGroupContextMenu();
        hideTabContextMenu();
        hideChatFileContextMenu();
        if (el.cmdPaletteOverlay) el.cmdPaletteOverlay.style.display = 'none';
        hideModal();
      }

      // Cmd+K or Ctrl+K for Command Palette
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault();
        toggleCommandPalette();
      }

      // Cmd+W or Ctrl+W to Close Active Tab
      if ((e.metaKey || e.ctrlKey) && (e.key === 'w' || e.key === 'W')) {
        if (state.activeTabId && document.activeElement !== el.searchInput && document.activeElement !== el.cmdPaletteInput) {
          e.preventDefault();
          closeTab(state.activeTabId);
        }
      }

      // Cmd+1..9 to switch tabs
      if ((e.metaKey || e.ctrlKey) && e.key >= '1' && e.key <= '9') {
        const tabIndex = parseInt(e.key, 10) - 1;
        const tabIds = Array.from(state.openTabs.keys());
        if (tabIds[tabIndex]) {
          e.preventDefault();
          activateTab(tabIds[tabIndex]);
        }
      }
    });

    // Command Palette Input Events
    if (el.cmdPaletteInput) {
      el.cmdPaletteInput.addEventListener('input', (e) => {
        renderCommandPaletteResults(e.target.value);
      });

      el.cmdPaletteInput.addEventListener('keydown', (e) => {
        const items = el.cmdPaletteResults.querySelectorAll('.cmd-item');
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          state.cmdPaletteSelectedIndex = Math.min(items.length - 1, state.cmdPaletteSelectedIndex + 1);
          items.forEach((it, idx) => it.classList.toggle('selected', idx === state.cmdPaletteSelectedIndex));
        } else if (e.key === 'ArrowUp') {
          e.preventDefault();
          state.cmdPaletteSelectedIndex = Math.max(0, state.cmdPaletteSelectedIndex - 1);
          items.forEach((it, idx) => it.classList.toggle('selected', idx === state.cmdPaletteSelectedIndex));
        } else if (e.key === 'Enter') {
          e.preventDefault();
          if (state.cmdPaletteItems[state.cmdPaletteSelectedIndex]) {
            el.cmdPaletteOverlay.style.display = 'none';
            state.cmdPaletteItems[state.cmdPaletteSelectedIndex].action();
          }
        }
      });
    }

    // Session Context Menu actions
    if (el.cmItemInfo) {
      el.cmItemInfo.addEventListener('click', () => {
        if (state.contextMenuSession) openSessionDetailsTab(state.contextMenuSession);
      });
    }

    if (el.cmItemTranscript) {
      el.cmItemTranscript.addEventListener('click', () => {
        if (state.contextMenuSession) openTranscriptViewerTab(state.contextMenuSession);
      });
    }

    if (el.cmItemCopyName) {
      el.cmItemCopyName.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const text = sess.name || sess.agent || '';
          await copyTextToClipboard(text);
        }
      });
    }

    if (el.cmItemCopyPath) {
      el.cmItemCopyPath.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const sessName = sess.name || sess.agent || '';
          const fullPath = sess.node_path ? `${sess.node_path}/${sessName}` : sessName;
          await copyTextToClipboard(fullPath);
        }
      });
    }

    if (el.cmItemTakeWheel) {
      el.cmItemTakeWheel.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          hideContextMenu();
          await handleTakeWheel(sess.id);
        }
      });
    }

    if (el.btnViewChat) {
      el.btnViewChat.addEventListener('click', () => {
        if (state.activeTabId) setTabViewMode(state.activeTabId, 'chat');
      });
    }

    if (el.btnViewTerminal) {
      el.btnViewTerminal.addEventListener('click', () => {
        if (state.activeTabId) setTabViewMode(state.activeTabId, 'terminal');
      });
    }

    if (el.cmItemResume) {
      el.cmItemResume.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
          try {
            const res = await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=resume`, { method: 'POST' });
            if (res.ok) {
              const data = await res.json();
              if (data.session) Object.assign(sess, data.session);
            }
          } catch (e) {
            console.error('Failed to resume session:', e);
          }
          openSessionInTab(sess);
          await fetchSessions();
        }
      });
    }

    if (el.cmItemNewTab) {
      el.cmItemNewTab.addEventListener('click', () => {
        if (state.contextMenuSession) openSessionInTab(state.contextMenuSession);
      });
    }

    if (el.cmItemVSCode) {
      el.cmItemVSCode.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          openInVSCode(state.contextMenuSession.cwd, state.contextMenuSession.host);
        }
      });
    }

    if (el.cmItemDocs) {
      el.cmItemDocs.addEventListener('click', () => {
        if (state.contextMenuSession) {
          showProjectDocsModal(state.contextMenuSession.cwd, state.contextMenuSession.name, state.contextMenuSession.host, state.contextMenuSession);
        }
      });
    }

    if (el.cmItemRestart) {
      el.cmItemRestart.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
          await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=restart`, { method: 'POST' });
          openSessionInTab(sess);
        }
      });
    }

    if (el.cmItemKill) {
      el.cmItemKill.addEventListener('click', async () => {
        if (state.contextMenuSession && confirm(`Terminate process for "${state.contextMenuSession.name}"?`)) {
          const sess = state.contextMenuSession;
          const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
          await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=kill`, { method: 'POST' });
          await fetchSessions();
        }
      });
    }

    if (el.cmItemDone) {
      el.cmItemDone.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const isDone = isSessionDone(sess);
          hideContextMenu();
          await setSessionDoneState(sess.id, sess.host, !isDone);
        }
      });
    }

    if (el.cmItemArchive) {
      el.cmItemArchive.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const targetAction = sess.archived ? 'unarchive' : 'archive';
          // Optimistically update locally
          sess.archived = !sess.archived;
          renderTree();

          try {
            const hostRec = (state.hosts || []).find(h => h.name === sess.host);
            const baseUrl = (sess && sess.hostUrl) ? sess.hostUrl.replace(/\/$/, '') : (hostRec && hostRec.url && !isLocalHost(sess.host) ? hostRec.url.replace(/\/$/, '') : '');
            const url = `${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=${targetAction}`;
            const res = await fetch(url, { method: 'POST' });
            if (res.ok) {
              await fetchSessions();
            }
          } catch (err) {
            console.error(`Failed to ${targetAction} session:`, err);
          }
        }
      });
    }

    if (el.cmItemDelete) {
      el.cmItemDelete.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const msg = `⚠️ Permanently Delete Session "${sess.name}"?\n\nThis will permanently delete the session and its transcript logs from the agent on disk (~/.claude/projects/) without the possibility for recovery.\n\nUse Archive (📦) if you only want to hide it from your active list.`;
          if (confirm(msg)) {
            const targetId = sess.id;
            const targetName = sess.name;
            const nativeId = sess.native_id;
            state.sessions = state.sessions.filter(s => s.id !== targetId && s.name !== targetName && (!nativeId || s.native_id !== nativeId));
            closeTab(targetId);
            if (nativeId) closeTab(nativeId);
            closeTab(`details_${targetId}`);
            if (nativeId) closeTab(`details_${nativeId}`);
            for (const [tId, tabObj] of state.openTabs.entries()) {
              if (tabObj.session && (tabObj.session.id === targetId || (nativeId && tabObj.session.native_id === nativeId))) {
                closeTab(tId);
              }
            }
            renderTree();
            const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
            await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(targetId)}&action=delete`, { method: 'POST' });
            await fetchSessions();
          }
        }
      });
    }

    // Group Folder Context Menu Actions
    if (el.gcmItemNewSession) {
      el.gcmItemNewSession.addEventListener('click', () => {
        const groupPath = state.contextMenuGroupPath;
        hideContextMenu();
        if (groupPath) {
          showNewSessionModal(groupPath);
        }
      });
    }

    if (el.gcmItemNewSubgroup) {
      el.gcmItemNewSubgroup.addEventListener('click', () => {
        if (state.contextMenuGroupPath) {
          showNewGroupModal(state.contextMenuGroupPath + '/');
        }
      });
    }

    if (el.gcmItemVSCode) {
      el.gcmItemVSCode.addEventListener('click', async () => {
        if (state.contextMenuGroupPath) {
          const node = state.treeNodes.find(n => n.path === state.contextMenuGroupPath);
          const dir = (node && node.project_dir) ? node.project_dir : '';
          if (dir) {
            const groupSess = state.sessions.find(s => s.node_path === state.contextMenuGroupPath || (s.cwd && s.cwd.startsWith(dir)));
            const host = groupSess ? groupSess.host : getSelfHostName();
            openInVSCode(dir, host);
          } else {
            alert('This category subgroup does not have a linked filesystem directory.');
          }
        }
      });
    }

    if (el.gcmItemDocs) {
      el.gcmItemDocs.addEventListener('click', () => {
        if (state.contextMenuGroupPath) {
          const node = state.treeNodes.find(n => n.path === state.contextMenuGroupPath);
          const dir = (node && node.project_dir) ? node.project_dir : '';
          if (dir) {
            const groupSess = state.sessions.find(s => s.node_path === state.contextMenuGroupPath || (s.cwd && s.cwd.startsWith(dir)));
            const host = groupSess ? groupSess.host : getSelfHostName();
            showProjectDocsModal(dir, state.contextMenuGroupPath, host, groupSess);
          } else {
            alert('This category subgroup does not have a linked filesystem directory.');
          }
        }
      });
    }

    if (el.gcmItemDelete) {
      el.gcmItemDelete.addEventListener('click', async () => {
        const groupPath = state.contextMenuGroupPath;
        if (groupPath && confirm(`Delete group folder "${groupPath}"? Sessions will be moved to Unassigned.`)) {
          state.treeNodes = state.treeNodes.filter(n => n.path !== groupPath && !n.path.startsWith(groupPath + '/'));
          state.sessions.forEach(s => {
            if (s.node_path === groupPath || (s.node_path && s.node_path.startsWith(groupPath + '/'))) {
              s.node_path = '';
            }
          });
          state.collapsedGroups.delete(groupPath);
          renderTree();

          await fetch(`/v1/nodes?path=${encodeURIComponent(groupPath)}`, { method: 'DELETE' });
          await fetchTreeNodes();
          await fetchSessions();
        }
      });
    }

    // Tab Context Menu actions
    if (el.tcmItemClose) {
      el.tcmItemClose.addEventListener('click', () => {
        if (state.contextMenuTabId) {
          closeTab(state.contextMenuTabId);
          hideTabContextMenu();
        }
      });
    }

    if (el.tcmItemCloseOthers) {
      el.tcmItemCloseOthers.addEventListener('click', () => {
        if (state.contextMenuTabId) {
          closeOtherTabs(state.contextMenuTabId);
          hideTabContextMenu();
        }
      });
    }

    if (el.tcmItemCloseRight) {
      el.tcmItemCloseRight.addEventListener('click', () => {
        if (state.contextMenuTabId) {
          closeTabsToTheRight(state.contextMenuTabId);
          hideTabContextMenu();
        }
      });
    }

    if (el.tcmItemCloseAll) {
      el.tcmItemCloseAll.addEventListener('click', () => {
        closeAllTabs();
        hideTabContextMenu();
      });
    }

    if (el.searchInput) {
      el.searchInput.addEventListener('input', (e) => {
        state.searchQuery = e.target.value;
        renderTree();
      });

      window.addEventListener('keydown', (e) => {
        const activeTag = document.activeElement ? document.activeElement.tagName : '';
        const isInputFocused = activeTag === 'INPUT' || activeTag === 'TEXTAREA' || (document.activeElement && document.activeElement.isContentEditable);
        if (e.key === '/' && !isInputFocused) {
          e.preventDefault();
          el.searchInput.focus();
        }
      });
    }

    if (el.btnAddHost) {
      el.btnAddHost.addEventListener('click', showAddHostModal);
    }

    if (el.btnDiscovery) {
      el.btnDiscovery.addEventListener('click', showHooksDashboardModal);
    }

    if (el.btnToggleArchived) {
      el.btnToggleArchived.addEventListener('click', () => {
        state.showArchived = !state.showArchived;
        el.btnToggleArchived.classList.toggle('active', state.showArchived);
        renderTree();
      });
    }

    if (el.btnCollapseAll) {
      el.btnCollapseAll.addEventListener('click', () => {
        state.treeNodes.forEach(n => state.collapsedGroups.add(n.path));
        state.collapsedGroups.add('Unassigned');
        saveCollapsedGroups();
        renderTree();
      });
    }

    if (el.btnExpandAll) {
      el.btnExpandAll.addEventListener('click', () => {
        state.collapsedGroups.clear();
        saveCollapsedGroups();
        renderTree();
      });
    }

    if (el.btnPurge) {
      el.btnPurge.addEventListener('click', async () => {
        if (confirm('Safe Purge & Rescan: Refresh live sessions from disk while strictly preserving groups?')) {
          await fetch('/v1/maintenance/purge', { method: 'POST' });
          await fetchSessions();
        }
      });
    }

    if (el.btnSettings) {
      el.btnSettings.addEventListener('click', () => {
        showSettingsModal();
      });
    }

    if (el.btnRefreshPage) {
      el.btnRefreshPage.addEventListener('click', () => {
        window.location.reload();
      });
    }

    if (el.sbModeToggle) {
      el.sbModeToggle.addEventListener('click', () => {
        if (!state.activeTabId) return;
        const curTab = state.openTabs.get(state.activeTabId);
        if (curTab && curTab.socket && curTab.socket.readyState === WebSocket.OPEN) {
          curTab.socket.send(new Uint8Array([0x1b, 0x5b, 0x5a]));
          if (curTab.terminal) curTab.terminal.focus();
        }
      });
    }

    if (el.btnNewProject) {
      el.btnNewProject.addEventListener('click', () => showNewGroupModal());
    }

    if (el.btnTabOverflow) {
      el.btnTabOverflow.addEventListener('click', (e) => {
        e.stopPropagation();
        if (el.overflowMenuContent) {
          el.overflowMenuContent.classList.toggle('show');
        }
      });
    }

    if (el.modalCloseBtn) {
      el.modalCloseBtn.addEventListener('click', hideModal);
    }

    const btnNewSession = document.getElementById('btnNewSession');
    const btnSidebarNewSession = document.getElementById('btnSidebarNewSession');
    const btnCloseNewSession = document.getElementById('btnCloseNewSession');
    const btnCancelNewSession = document.getElementById('btnCancelNewSession');
    const btnSubmitNewSession = document.getElementById('btnSubmitNewSession');
    const modalNewSession = document.getElementById('modalNewSession');

    if (btnNewSession) btnNewSession.addEventListener('click', () => showNewSessionModal());
    if (btnSidebarNewSession) btnSidebarNewSession.addEventListener('click', () => showNewSessionModal());
    if (btnCloseNewSession) btnCloseNewSession.addEventListener('click', hideNewSessionModal);
    if (btnCancelNewSession) btnCancelNewSession.addEventListener('click', hideNewSessionModal);
    if (btnSubmitNewSession) btnSubmitNewSession.addEventListener('click', handleSpawnNewSession);
    if (modalNewSession) {
      modalNewSession.addEventListener('click', (e) => {
        if (e.target === modalNewSession) hideNewSessionModal();
      });
    }
  }

  // Helper: Intelligent Cross-Host Path Translation
  function translatePathForHost(currentPath, fromHost, toHost) {
    if (!currentPath || fromHost === toHost) return currentPath;
    const effFrom = isLocalHost(fromHost) ? getSelfHostName() : fromHost;
    const effTo = isLocalHost(toHost) ? getSelfHostName() : toHost;
    if (effFrom === effTo) return currentPath;

    // 1. Direct Project Key Matching on target host
    const srcSess = (state.sessions || []).find(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effFrom && s.cwd === currentPath);
    if (srcSess && srcSess.project_key) {
      const tgtSess = (state.sessions || []).find(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effTo && s.project_key === srcSess.project_key && s.cwd);
      if (tgtSess && tgtSess.cwd) return tgtSess.cwd;
    }

    // 2. Project Directory Basename Matching on target host
    const cleanPath = currentPath.replace(/\/+$/, '');
    const currentBase = cleanPath.split('/').pop();
    if (currentBase) {
      const tgtMatch = (state.sessions || []).find(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effTo && s.cwd && (s.cwd.endsWith('/' + currentBase) || s.cwd.split('/').pop() === currentBase));
      if (tgtMatch && tgtMatch.cwd) return tgtMatch.cwd;

      // Check target host configured remote_cwd
      const targetHostRec = (state.hosts || []).find(h => h.name === toHost);
      if (targetHostRec && targetHostRec.remote_cwd && !isLocalHost(toHost)) {
        const root = targetHostRec.remote_cwd.replace(/\/+$/, '');
        return `${root}/${currentBase}`;
      }
    }

    // 3. Standard Home Prefix Translation (macOS /Users/<user> <-> Linux /home/<user>)
    if ((isLocalHost(fromHost) || currentPath.startsWith('/Users/')) && !isLocalHost(toHost)) {
      // macOS -> Linux
      if (currentPath.startsWith('/Users/')) {
        return currentPath.replace(/^\/Users\/([^/]+)/, '/home/$1');
      }
    } else if (!isLocalHost(fromHost) && isLocalHost(toHost)) {
      // Linux -> macOS
      if (currentPath.startsWith('/home/')) {
        return currentPath.replace(/^\/home\/([^/]+)/, '/Users/$1');
      }
    }

    return currentPath;
  }

  // Group Memory Storage Key
  const GROUP_PREFERENCES_KEY = 'ackbar_group_preferences_v1';

  function getStoredGroupPreferences() {
    try {
      const raw = localStorage.getItem(GROUP_PREFERENCES_KEY);
      return raw ? JSON.parse(raw) : {};
    } catch (e) {
      console.warn('Failed to parse group preferences:', e);
      return {};
    }
  }

  function saveStoredGroupPreferences(prefs) {
    try {
      localStorage.setItem(GROUP_PREFERENCES_KEY, JSON.stringify(prefs));
    } catch (e) {
      console.warn('Failed to save group preferences:', e);
    }
  }

  // Record a spawn in group memory
  function recordGroupSpawn({ group, host, agent, cwd, account }) {
    if (!host) host = getSelfHostName();
    if (!agent) agent = 'claude-code';
    if (!cwd) return;

    const groupKey = group || '__default__';
    const allPrefs = getStoredGroupPreferences();
    if (!allPrefs[groupKey]) {
      allPrefs[groupKey] = {
        preferred_host: host,
        by_host: {}
      };
    }

    const g = allPrefs[groupKey];
    g.preferred_host = host;
    if (!g.by_host) g.by_host = {};
    if (!g.by_host[host]) {
      g.by_host[host] = {
        agent: agent,
        recent_paths: [],
        counts: {}
      };
    }

    const hostEntry = g.by_host[host];
    hostEntry.agent = agent;
    if (account !== undefined) {
      hostEntry.account = account;
    }
    hostEntry.last_used_at = Date.now();

    const filteredPaths = (hostEntry.recent_paths || []).filter(p => p !== cwd);
    hostEntry.recent_paths = [cwd, ...filteredPaths].slice(0, 3);

    if (!hostEntry.counts) hostEntry.counts = {};
    hostEntry.counts[cwd] = (hostEntry.counts[cwd] || 0) + 1;

    saveStoredGroupPreferences(allPrefs);
  }

  // Get preferences for a given group and target host
  function getGroupPreferences(groupPath, targetHost = null) {
    const groupKey = groupPath || '__default__';
    const allPrefs = getStoredGroupPreferences();
    const storedGroup = allPrefs[groupKey];

    // Filter matching sessions from state.sessions
    let groupSessions = (state.sessions || []).filter(s => s.node_path === groupPath);
    if (groupSessions.length === 0 && groupPath) {
      groupSessions = (state.sessions || []).filter(s => s.node_path && s.node_path.startsWith(groupPath));
    }
    const sortedGroupSessions = sortSessionsByInteraction(groupSessions);

    // 1. Determine Preferred Host
    let preferredHost = '';
    if (storedGroup && storedGroup.preferred_host) {
      preferredHost = storedGroup.preferred_host;
    } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].host) {
      preferredHost = sortedGroupSessions[0].host;
    } else {
      const node = (state.treeNodes || []).find(n => n.path === groupPath);
      if (node && node.host) {
        preferredHost = node.host;
      } else {
        preferredHost = targetHost || getSelfHostName();
      }
    }

    const effectiveHost = targetHost || preferredHost || getSelfHostName();
    const hostSessions = sortedGroupSessions.filter(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effectiveHost);

    // 2. Determine Preferred Agent
    let preferredAgent = '';
    if (storedGroup && storedGroup.by_host && storedGroup.by_host[effectiveHost] && storedGroup.by_host[effectiveHost].agent) {
      preferredAgent = storedGroup.by_host[effectiveHost].agent;
    } else if (hostSessions.length > 0 && hostSessions[0].agent) {
      preferredAgent = hostSessions[0].agent;
    } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].agent) {
      preferredAgent = sortedGroupSessions[0].agent;
    } else {
      preferredAgent = 'claude-code';
    }

    // 3. Determine Preferred Account
    let preferredAccount = '';
    if (storedGroup && storedGroup.by_host && storedGroup.by_host[effectiveHost] && storedGroup.by_host[effectiveHost].account) {
      preferredAccount = storedGroup.by_host[effectiveHost].account;
    } else if (hostSessions.length > 0 && hostSessions[0].account_id) {
      preferredAccount = hostSessions[0].account_id;
    } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].account_id) {
      preferredAccount = sortedGroupSessions[0].account_id;
    }

    // 4. Determine Recent Paths and Preferred Path
    let recentPaths = [];
    if (storedGroup && storedGroup.by_host && storedGroup.by_host[effectiveHost] && Array.isArray(storedGroup.by_host[effectiveHost].recent_paths)) {
      recentPaths = [...storedGroup.by_host[effectiveHost].recent_paths];
    }
    hostSessions.forEach(s => {
      if (s.cwd && !recentPaths.includes(s.cwd)) {
        recentPaths.push(s.cwd);
      }
    });
    recentPaths = recentPaths.slice(0, 3);

    let preferredPath = recentPaths.length > 0 ? recentPaths[0] : '';
    if (!preferredPath && groupPath) {
      const node = (state.treeNodes || []).find(n => n.path === groupPath && n.project_dir);
      if (node && node.project_dir) {
        preferredPath = translatePathForHost(node.project_dir, node.host || 'local', effectiveHost);
      } else {
        const childNode = (state.treeNodes || []).find(n => n.path.startsWith(groupPath + '/') && n.project_dir);
        if (childNode && childNode.project_dir) {
          preferredPath = translatePathForHost(childNode.project_dir, childNode.host || 'local', effectiveHost);
        } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].cwd) {
          preferredPath = translatePathForHost(sortedGroupSessions[0].cwd, sortedGroupSessions[0].host || 'local', effectiveHost);
        }
      }
    }

    return {
      preferredHost,
      preferredAgent,
      preferredAccount,
      preferredPath,
      recentPaths
    };
  }

  // Helper: Get relevant folders for a specific host
  function getFoldersForHost(targetHost) {
    const isTargetLocal = isLocalHost(targetHost);
    const hostSessions = (state.sessions || []).filter(s => (isTargetLocal ? isLocalHost(s.host) : s.host === targetHost) && s.cwd);
    const hostCwds = new Set(hostSessions.map(s => s.cwd));

    // Also include tree node linked project dirs for this host or generic
    (state.treeNodes || []).forEach(n => {
      if (n.project_dir && (!n.host || (isTargetLocal ? isLocalHost(n.host) : n.host === targetHost))) {
        hostCwds.add(n.project_dir);
      }
    });

    // Check host configured remote_cwd
    const targetHostRec = (state.hosts || []).find(h => h.name === targetHost);
    if (targetHostRec && targetHostRec.remote_cwd) {
      hostCwds.add(targetHostRec.remote_cwd);
    }

    // If target host has few or no session history yet, translate known paths from other hosts
    const allUnique = Array.from(new Set((state.sessions || []).map(s => s.cwd).filter(Boolean)));
    allUnique.forEach(p => {
      const translated = translatePathForHost(p, getSelfHostName(), targetHost);
      if (translated) hostCwds.add(translated);
    });

    return Array.from(hostCwds).sort();
  }

  // New Session Modal Launcher
  async function showNewSessionModal(prefillGroup = '') {
    const modal = document.getElementById('modalNewSession');
    if (!modal) return;

    const hostSelect = document.getElementById('newSessionHost');
    const agentSelect = document.getElementById('newSessionAgent');
    const folderInput = document.getElementById('newSessionFolder');
    const folderList = document.getElementById('folderSuggestions');
    const groupSelect = document.getElementById('newSessionGroup');

    // If no group explicitly passed, check active tab's session group or last active group
    const activeTab = state.openTabs.get(state.activeTabId);
    if (!prefillGroup && activeTab && activeTab.session && activeTab.session.node_path) {
      prefillGroup = activeTab.session.node_path;
    } else if (!prefillGroup && state.lastActiveGroup) {
      prefillGroup = state.lastActiveGroup;
    }

    // Helper: Dynamically fetch & populate available agents for the chosen host
    async function updateAgentOptions(targetHost, preferredAgent = null) {
      if (!agentSelect) return;
      agentSelect.innerHTML = '<option value="">⏳ Detecting agents on host...</option>';

      try {
        const hostRec = (state.hosts || []).find(h => h.name === targetHost);
        const baseUrl = hostRec && hostRec.url && !isLocalHost(targetHost) ? hostRec.url.replace(/\/$/, '') : '';
        const controller = new AbortController();
        const timeoutId = setTimeout(() => controller.abort(), 5000);
        let res;
        try {
          res = await fetch(`${baseUrl}/v1/agents/discovery`, { signal: controller.signal });
        } finally {
          clearTimeout(timeoutId);
        }
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const discovery = await res.json() || [];

        const agentDisplayNames = {
          'claude-code': 'Claude Code (Anthropic)',
          'antigravity': 'Google Antigravity (agy)',
          'codex': 'OpenAI Codex'
        };

        const installed = discovery.filter(d => d.installed);
        agentSelect.innerHTML = '';

        const listToRender = installed.length > 0 ? installed : discovery;
        const availableAgents = listToRender.map(d => d.agent);

        // Determine which agent should be selected
        // Priority: preferredAgent (if available) -> 'claude-code' (if available) -> first available
        let selectedAgent = preferredAgent && availableAgents.includes(preferredAgent) ? preferredAgent : null;
        if (!selectedAgent && availableAgents.includes('claude-code')) {
          selectedAgent = 'claude-code';
        }
        if (!selectedAgent && availableAgents.length > 0) {
          selectedAgent = availableAgents[0];
        }

        listToRender.forEach(d => {
          const opt = document.createElement('option');
          opt.value = d.agent;
          const statusLabel = d.installed ? '(Installed)' : '(Not detected)';
          opt.textContent = `${d.display_name || agentDisplayNames[d.agent] || d.agent} ${statusLabel}`;
          if (d.agent === selectedAgent) {
            opt.selected = true;
          }
          agentSelect.appendChild(opt);
        });

        if (selectedAgent) {
          agentSelect.value = selectedAgent;
        }
      } catch (err) {
        console.warn('Agent discovery fallback for host:', targetHost, err);
        agentSelect.innerHTML = `
          <option value="claude-code">Claude Code (Anthropic)</option>
          <option value="antigravity">Google Antigravity (agy)</option>
          <option value="codex">OpenAI Codex</option>
        `;
        agentSelect.value = preferredAgent || 'claude-code';
      }
    }

    // Helper: Update folder suggestions datalist and adapt folderInput.value on host change
    function updateFolderSuggestions(targetHost, prevHost = null, targetGroup = '') {
      if (!folderList) return;
      const hostFolders = getFoldersForHost(targetHost);
      const groupPrefs = getGroupPreferences(targetGroup, targetHost);
      const recentGroupPaths = (groupPrefs && groupPrefs.recentPaths) ? groupPrefs.recentPaths : [];

      // Combine suggestions: group's recent paths first, then hostFolders (deduplicated)
      const combined = Array.from(new Set([...recentGroupPaths, ...hostFolders]));

      folderList.innerHTML = '';
      combined.forEach(c => {
        const opt = document.createElement('option');
        opt.value = c;
        if (recentGroupPaths.includes(c)) {
          opt.label = 'Recent in group';
        }
        folderList.appendChild(opt);
      });

      // If user switched hosts, translate the current folder path or apply group preference
      if (prevHost && prevHost !== targetHost && folderInput) {
        if (targetGroup && groupPrefs && groupPrefs.preferredPath) {
          folderInput.value = groupPrefs.preferredPath;
        } else {
          const oldVal = folderInput.value.trim();
          if (oldVal) {
            const translated = translatePathForHost(oldVal, prevHost, targetHost);
            if (translated && translated !== oldVal) {
              folderInput.value = translated;
            } else if (hostFolders.length > 0 && !hostFolders.includes(oldVal)) {
              // Check if any host folder matches the basename
              const base = oldVal.replace(/\/+$/, '').split('/').pop();
              const matched = hostFolders.find(f => f.endsWith('/' + base) || f.split('/').pop() === base);
              if (matched) {
                folderInput.value = matched;
              } else {
                folderInput.value = hostFolders[0];
              }
            }
          } else if (hostFolders.length > 0) {
            folderInput.value = hostFolders[0];
          }
        }
      }
    }

    // 1. Determine initial active host based on group preference or active tab
    let currentSelectedHost = getSelfHostName();
    if (prefillGroup) {
      const groupPrefs = getGroupPreferences(prefillGroup, getSelfHostName());
      if (groupPrefs.preferredHost) {
        currentSelectedHost = groupPrefs.preferredHost;
      } else {
        const matchedNode = (state.treeNodes || []).find(n => n.path === prefillGroup);
        if (matchedNode && matchedNode.host) {
          currentSelectedHost = matchedNode.host;
        }
      }
    } else if (activeTab && activeTab.session && activeTab.session.host) {
      currentSelectedHost = isLocalHost(activeTab.session.host) ? getSelfHostName() : activeTab.session.host;
    }

    // 2. Populate Hosts
    if (hostSelect) {
      hostSelect.innerHTML = `<option value="${getSelfHostName()}">${getSelfDisplayName()} (This Machine)</option>`;
      (state.hosts || []).forEach(h => {
        if (!isLocalHost(h.name)) {
          const opt = document.createElement('option');
          opt.value = h.name;
          opt.textContent = `${h.displayName || formatHostLabel(h.name)} (${h.url || 'remote'})`;
          if (h.name === currentSelectedHost) opt.selected = true;
          hostSelect.appendChild(opt);
        }
      });
      hostSelect.value = currentSelectedHost;

      // Update agents & folders when host selection changes
      hostSelect.onchange = () => {
        const newHost = hostSelect.value;
        const selectedGroup = groupSelect ? groupSelect.value : '';
        const prevHost = currentSelectedHost;
        currentSelectedHost = newHost;
        updateFolderSuggestions(newHost, prevHost, selectedGroup);
        const groupPrefs = getGroupPreferences(selectedGroup, newHost);
        if (selectedGroup && groupPrefs.preferredPath) {
          folderInput.value = groupPrefs.preferredPath;
        }
        updateAgentOptions(newHost, groupPrefs.preferredAgent).then(() => {
          updateAccountOptions(newHost, agentSelect ? agentSelect.value : 'claude-code', groupPrefs.preferredAccount);
        });
      };
    }

    // Helper: Update account profile options for chosen host and agent
    async function updateAccountOptions(targetHost, targetAgent, preferredAccount = '') {
      const accountGroup = document.getElementById('newSessionAccountGroup');
      const accountSelect = document.getElementById('newSessionAccount');
      if (!accountGroup || !accountSelect) return;

      if (!targetAgent) {
        accountGroup.style.display = 'none';
        return;
      }

      try {
        const hostRec = (state.hosts || []).find(h => h.name === targetHost);
        const baseUrl = hostRec && hostRec.url && !isLocalHost(targetHost) ? hostRec.url.replace(/\/$/, '') : '';
        const res = await fetch(`${baseUrl}/v1/accounts`);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const allAccounts = await res.json() || [];

        const agentAccounts = allAccounts.filter(a => a.agent === targetAgent);

        // If 0 or 1 accounts (e.g. only default), hide selector to keep UI clean
        if (agentAccounts.length <= 1) {
          accountGroup.style.display = 'none';
          accountSelect.innerHTML = '<option value="default">Default</option>';
          accountSelect.value = 'default';
          return;
        }

        accountGroup.style.display = 'block';
        accountSelect.innerHTML = '';

        let matched = false;
        agentAccounts.forEach(a => {
          const opt = document.createElement('option');
          opt.value = a.name;
          const statusIcon = a.is_logged_in ? '🟢' : '⚪';
          const defaultLabel = a.is_default ? ' (Default)' : '';
          opt.textContent = `${statusIcon} ${a.display_name || a.name}${defaultLabel}`;
          if (preferredAccount && (a.name === preferredAccount || a.id === preferredAccount)) {
            opt.selected = true;
            matched = true;
          }
          accountSelect.appendChild(opt);
        });

        if (!matched) {
          const defAcc = agentAccounts.find(a => a.is_default);
          if (defAcc) {
            accountSelect.value = defAcc.name;
          } else if (agentAccounts.length > 0) {
            accountSelect.value = agentAccounts[0].name;
          }
        }
      } catch (err) {
        console.warn('Account options detection fallback for host:', targetHost, err);
        accountGroup.style.display = 'none';
      }
    }

    if (agentSelect) {
      agentSelect.onchange = () => {
        const selAgent = agentSelect.value;
        const curGroup = groupSelect ? groupSelect.value : '';
        const prefs = getGroupPreferences(curGroup, currentSelectedHost);
        updateAccountOptions(currentSelectedHost, selAgent, prefs.preferredAccount);
      };
    }

    // 3. Populate Groups & wire onchange
    if (groupSelect) {
      groupSelect.innerHTML = '<option value="">(Default / Unassigned)</option>';
      const allPaths = new Set();
      (state.treeNodes || []).forEach(n => {
        if (n.path) {
          const parts = n.path.split('/');
          let acc = '';
          parts.forEach(p => {
            acc = acc ? acc + '/' + p : p;
            allPaths.add(acc);
          });
        }
      });
      (state.sessions || []).forEach(s => {
        if (s.node_path) {
          const parts = s.node_path.split('/');
          let acc = '';
          parts.forEach(p => {
            acc = acc ? acc + '/' + p : p;
            allPaths.add(acc);
          });
        }
      });

      const sortedPaths = Array.from(allPaths).sort();
      sortedPaths.forEach(p => {
        const depth = (p.match(/\//g) || []).length;
        const prefix = '  ↳ '.repeat(depth);
        const name = p.split('/').pop();
        const opt = document.createElement('option');
        opt.value = p;
        opt.textContent = prefix + name + (depth > 0 ? ` (${p})` : '');
        if (p === prefillGroup) opt.selected = true;
        groupSelect.appendChild(opt);
      });
      if (prefillGroup) groupSelect.value = prefillGroup;

      groupSelect.onchange = () => {
        const selectedGroup = groupSelect.value;
        const groupPrefs = getGroupPreferences(selectedGroup, currentSelectedHost);

        // If group has a preferred host, switch host
        if (groupPrefs.preferredHost && groupPrefs.preferredHost !== currentSelectedHost) {
          currentSelectedHost = groupPrefs.preferredHost;
          if (hostSelect) hostSelect.value = currentSelectedHost;
        }

        // Update folder suggestions and folder input
        updateFolderSuggestions(currentSelectedHost, null, selectedGroup);
        if (folderInput) {
          if (groupPrefs.preferredPath) {
            folderInput.value = groupPrefs.preferredPath;
          } else {
            const hostFolders = getFoldersForHost(currentSelectedHost);
            folderInput.value = hostFolders.length > 0 ? hostFolders[0] : '';
          }
        }

        // Update agent and account options with group's preferences
        updateAgentOptions(currentSelectedHost, groupPrefs.preferredAgent).then(() => {
          updateAccountOptions(currentSelectedHost, agentSelect ? agentSelect.value : 'claude-code', groupPrefs.preferredAccount);
        });
      };
    }

    // 4. Initial folder suggestions population for selected host and group
    const initialGroupPrefs = getGroupPreferences(prefillGroup, currentSelectedHost);
    updateFolderSuggestions(currentSelectedHost, null, prefillGroup);

    // 5. Initial folder prefill
    if (folderInput) {
      folderInput.value = '';
      if (initialGroupPrefs && initialGroupPrefs.preferredPath) {
        folderInput.value = initialGroupPrefs.preferredPath;
      } else if (prefillGroup) {
        const matchedNode = (state.treeNodes || []).find(n => n.path === prefillGroup);
        if (matchedNode && matchedNode.project_dir) {
          folderInput.value = translatePathForHost(matchedNode.project_dir, matchedNode.host || 'local', currentSelectedHost);
        } else {
          const childNode = (state.treeNodes || []).find(n => n.path.startsWith(prefillGroup + '/') && n.project_dir);
          if (childNode && childNode.project_dir) {
            folderInput.value = translatePathForHost(childNode.project_dir, childNode.host || 'local', currentSelectedHost);
          }
        }
      }

      if (!folderInput.value) {
        if (activeTab && activeTab.session && activeTab.session.cwd) {
          folderInput.value = translatePathForHost(activeTab.session.cwd, activeTab.session.host || 'local', currentSelectedHost);
        } else {
          const hostFolders = getFoldersForHost(currentSelectedHost);
          if (hostFolders.length > 0) {
            folderInput.value = hostFolders[0];
          }
        }
      }
    }

    modal.style.display = 'flex';
    setTimeout(() => {
      if (folderInput) folderInput.focus();
    }, 50);

    // Initial agent discovery & account options for selected host
    await updateAgentOptions(currentSelectedHost, initialGroupPrefs.preferredAgent);
    await updateAccountOptions(currentSelectedHost, agentSelect ? agentSelect.value : 'claude-code', initialGroupPrefs.preferredAccount);
  }

  function hideNewSessionModal() {
    const modal = document.getElementById('modalNewSession');
    if (modal) modal.style.display = 'none';
  }

  async function handleSpawnNewSession() {
    const hostSelect = document.getElementById('newSessionHost');
    const agentSelect = document.getElementById('newSessionAgent');
    const accountGroup = document.getElementById('newSessionAccountGroup');
    const accountSelect = document.getElementById('newSessionAccount');
    const folderInput = document.getElementById('newSessionFolder');
    const groupSelect = document.getElementById('newSessionGroup');
    const engineSelect = document.getElementById('newSessionEngine');
    const submitBtn = document.getElementById('btnSubmitNewSession');

    const host = hostSelect ? hostSelect.value : getSelfHostName();
    const agent = agentSelect ? agentSelect.value : 'claude-code';
    const account_id = (accountGroup && accountGroup.style.display !== 'none' && accountSelect) ? accountSelect.value : '';
    const cwd = folderInput ? folderInput.value.trim() : '';
    const targetGroup = groupSelect ? groupSelect.value : '';
    const engine_type = engineSelect ? engineSelect.value : 'headless';

    if (!cwd) {
      alert('Please provide a working directory or project path.');
      if (folderInput) folderInput.focus();
      return;
    }

    if (submitBtn) {
      submitBtn.disabled = true;
      submitBtn.textContent = '🚀 Launching...';
    }

    try {
      const res = await fetch('/v1/sessions/spawn', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ host, agent, cwd, node_path: targetGroup, account_id, engine_type })
      });

      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        throw new Error(errData.message || errData.error || `HTTP ${res.status}`);
      }

      const spawnResult = await res.json();
      const spawnedNativeId = spawnResult.session_id || spawnResult.native_id || '';
      const spawnedSessId = spawnResult.id || `${agent}:${host}:${spawnedNativeId}`;

      hideNewSessionModal();

      // Record spawn in group preferences
      recordGroupSpawn({ group: targetGroup, host, agent, cwd, account: account_id });

      // If user selected a group, assign it immediately
      if (targetGroup && spawnedSessId) {
        await moveSessionToGroup(spawnedSessId, host, targetGroup);
      }

      // Refresh sessions from daemon
      await fetchSessions();

      // Find the session and open in terminal tab
      const newSess = state.sessions.find(s => s.id === spawnedSessId || s.native_id === spawnedNativeId || (spawnedNativeId && s.id.includes(spawnedNativeId)));
      if (newSess) {
        openSessionInTab(newSess);
      } else {
        openSessionInTab({
          id: spawnedSessId,
          native_id: spawnedNativeId,
          name: sessionTitle || agent,
          agent: agent,
          host: host,
          cwd: cwd,
          managed: true,
          engine_type: engine_type,
          state: 3
        });
      }

      // Scroll to new session row and apply pulse glow highlight
      setTimeout(() => {
        const targetId = newSess ? newSess.id : spawnedSessId;
        const rowEl = document.querySelector(`[data-session-id="${targetId}"]`) ||
                      document.querySelector(`[data-session-id="${spawnedSessId}"]`);
        if (rowEl) {
          // Expand parent groups if collapsed
          let parent = rowEl.parentElement;
          while (parent && parent !== el.treeContainer) {
            if (parent.classList.contains('group-children')) {
              parent.classList.remove('hidden');
              const groupHeader = parent.previousElementSibling;
              if (groupHeader && groupHeader.querySelector('.group-arrow')) {
                groupHeader.querySelector('.group-arrow').textContent = '▼';
              }
            }
            parent = parent.parentElement;
          }

          rowEl.scrollIntoView({ behavior: 'smooth', block: 'center' });
          rowEl.classList.remove('highlight-new');
          void rowEl.offsetWidth; // trigger reflow
          rowEl.classList.add('highlight-new');
        }
      }, 350);

    } catch (err) {
      alert('Failed to launch session: ' + err.message);
    } finally {
      if (submitBtn) {
        submitBtn.disabled = false;
        submitBtn.textContent = '🚀 Launch Session';
      }
    }
  }

  // Modals
  function showModal(title, bodyHtml, footerHtml) {
    if (!el.modalOverlay) return;
    el.modalTitle.textContent = title;
    el.modalBody.innerHTML = bodyHtml;
    el.modalFooter.innerHTML = footerHtml;
    el.modalOverlay.style.display = 'flex';
  }

  function hideModal() {
    if (el.modalOverlay) el.modalOverlay.style.display = 'none';
  }

  // New Group Modal
  function showNewGroupModal(prefillPath = '') {
    const isSubgroup = prefillPath.length > 0;
    const body = `
      <div class="form-group">
        <label>Logical Group Path</label>
        <input type="text" id="mGroupPath" placeholder="e.g. Modemobile or Modemobile/NGL" value="${prefillPath}" />
        <div style="font-size: 11px; color: var(--text-muted); margin-top: 5px;">
          💡 <strong>Top-Level Group:</strong> Enter a single name without slashes (e.g. <code>Modemobile</code>, <code>Work</code>, <code>Personal</code>).<br>
          📁 <strong>Nested Subgroup:</strong> Use forward slashes (e.g. <code>Modemobile/NGL</code>, <code>Modemobile/ngl-android</code>).
        </div>
      </div>
      <div class="form-group">
        <label>Target Host / Machine</label>
        <select id="mHost">
          <option value="${getSelfHostName()}">${getSelfDisplayName()} (This Machine)</option>
          ${(state.hosts || []).filter(h => !isLocalHost(h.name)).map(h => `<option value="${h.name}">${formatHostLabel(h.name)} (${h.url || 'remote'})</option>`).join('')}
        </select>
      </div>
      <div class="form-group">
        <label>Linked Workspace Directory (Optional)</label>
        <input type="text" id="mProjDir" placeholder="/Users/dev4u/Work/... or ~/Work/..." />
        <div style="font-size: 11px; color: var(--text-dim); margin-top: 3px;">
          Leave empty for a logical category group folder.
        </div>
      </div>
      <div class="form-group">
        <label>Git Remote URL (Optional)</label>
        <input type="text" id="mGitUrl" placeholder="git@github.com:... or https://github.com/..." />
      </div>
      <div class="form-group" style="margin-top: 8px;">
        <label style="display: flex; align-items: center; gap: 8px; cursor: pointer; font-weight: normal; color: var(--text-normal); user-select: none;">
          <input type="checkbox" id="mCloneRepo" checked style="cursor: pointer; width: 15px; height: 15px;" />
          <span>Clone repository into workspace directory if not already present</span>
        </label>
      </div>
    `;

    const footer = `
      <button class="btn btn-secondary" id="mBtnCancel">Cancel</button>
      <button class="btn btn-primary" id="mBtnSubmit">${isSubgroup ? 'Create Subgroup' : 'Create Top-Level Group'}</button>
    `;

    showModal(isSubgroup ? '＋ New Nested Subgroup' : '＋ New Top-Level Group / Project', body, footer);

    document.getElementById('mBtnCancel').addEventListener('click', hideModal);
    document.getElementById('mBtnSubmit').addEventListener('click', async () => {
      const path = document.getElementById('mGroupPath').value.trim();
      const host = document.getElementById('mHost').value;
      const dir = document.getElementById('mProjDir').value.trim();
      const git = document.getElementById('mGitUrl').value.trim();
      const cloneRepo = document.getElementById('mCloneRepo').checked;
      if (!path) return;

      const submitBtn = document.getElementById('mBtnSubmit');
      submitBtn.disabled = true;
      submitBtn.textContent = (cloneRepo && git) ? 'Cloning repository...' : 'Creating...';

      try {
        const res = await fetch('/v1/projects/create', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path,
            host,
            project_dir: dir,
            git_url: git,
            clone_repo: cloneRepo
          })
        });
        if (!res.ok) {
          const errData = await res.json().catch(() => ({}));
          alert('Failed to create group: ' + (errData.message || errData.error || `HTTP ${res.status}`));
          submitBtn.disabled = false;
          submitBtn.textContent = isSubgroup ? 'Create Subgroup' : 'Create Top-Level Group';
          return;
        }

        // Uncollapse the newly created path and all parent segments
        const parts = path.split('/');
        for (let i = 1; i <= parts.length; i++) {
          const prefix = parts.slice(0, i).join('/');
          state.collapsedGroups.delete(prefix);
        }
        saveCollapsedGroups();

        await fetchTreeNodes();
        await fetchSessions();
        hideModal();

        // Scroll to and highlight the newly created group header
        setTimeout(() => {
          const headerEl = document.querySelector(`[data-group-path="${path}"]`);
          if (headerEl) {
            headerEl.scrollIntoView({ behavior: 'smooth', block: 'center' });
            headerEl.classList.add('highlight-new');
            setTimeout(() => headerEl.classList.remove('highlight-new'), 2500);
          }
        }, 100);
      } catch (err) {
        alert('Failed to create group: ' + err.message);
        submitBtn.disabled = false;
        submitBtn.textContent = isSubgroup ? 'Create Subgroup' : 'Create Top-Level Group';
      }
    });
  }

  // Register Remote SSH Host Modal
  function showAddHostModal() {
    const body = `
      <div class="form-group">
        <label>Host Alias (e.g. legion, gpu-server)</label>
        <input type="text" id="hHostName" placeholder="legion" />
      </div>
      <div class="form-group">
        <label>SSH Target (user@hostname or SSH config host)</label>
        <input type="text" id="hSshTarget" placeholder="dev4u@legion" />
      </div>
      <div class="form-group">
        <label>Local Forwarded URL (default http://127.0.0.1:7778)</label>
        <input type="text" id="hHostUrl" placeholder="http://127.0.0.1:7778" value="http://127.0.0.1:7778" />
      </div>
      <div style="background: #090a0f; border: 1px solid var(--border-color); border-radius: 6px; padding: 10px; font-size: 11px; font-family: var(--font-mono); color: var(--text-muted); margin-top: 6px;">
        💡 Tunnel Command:<br>
        <span style="color: var(--accent-cyan);">ssh -f -N -o ExitOnForwardFailure=yes -L 7778:127.0.0.1:7777 dev4u@legion</span>
      </div>
    `;

    const footer = `
      <button class="btn btn-secondary" id="hBtnCancel">Cancel</button>
      <button class="btn btn-primary" id="hBtnSubmit">Register Host</button>
    `;

    showModal('Register Remote SSH Host', body, footer);

    document.getElementById('hBtnCancel').addEventListener('click', hideModal);
    document.getElementById('hBtnSubmit').addEventListener('click', async () => {
      const name = document.getElementById('hHostName').value.trim();
      const ssh = document.getElementById('hSshTarget').value.trim();
      const url = document.getElementById('hHostUrl').value.trim();
      if (!name || !url) return;

      try {
        await fetch('/v1/hosts', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name, ssh_target: ssh, url })
        });
        await fetchHosts();
        await fetchSessions();
        hideModal();
      } catch (err) {
        alert('Failed to register host: ' + err.message);
      }
    });
  }

  // Agent Hook Diagnostics / Host Inspector Modal
  async function showHooksDashboardModal() {
    showHostSummaryModal(state.selfHost || { name: getSelfHostName(), displayName: getSelfDisplayName(), url: '' });
  }

  // Smooth Reconnection Flow with Progress, Auto-Dismiss on Success, and Retry on Failure
  async function handleReconnectHost(h) {
    showModal(`Reconnecting ${h.name}`, `
      <div style="padding: 32px 20px; text-align: center;">
        <div class="modal-loading-spinner"></div>
        <div style="font-size: 15px; font-weight: 600; color: #fff; margin-bottom: 6px;">Reconnecting SSH tunnel to ${h.name}...</div>
        <div style="font-size: 12px; color: var(--text-muted); font-family: var(--font-mono);">${h.url || 'remote host'}</div>
      </div>
    `, '');

    try {
      const res = await fetch('/v1/hosts/reconnect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: h.name })
      });
      const data = await res.json().catch(() => ({}));

      if (res.ok && data.status === 'success') {
        showModal(`Reconnecting ${h.name}`, `
          <div style="padding: 32px 20px; text-align: center;">
            <div style="font-size: 38px; margin-bottom: 12px; color: var(--accent-green);">✓</div>
            <div style="font-size: 16px; font-weight: 600; color: var(--accent-green); margin-bottom: 6px;">Connection Succeeded</div>
            <div style="font-size: 12px; color: var(--text-muted);">${data.message || 'SSH tunnel reconnected successfully.'}</div>
          </div>
        `, '');

        await fetchHosts();
        await fetchSessions();
        await fetchTreeNodes();

        setTimeout(() => {
          hideModal();
        }, 1000);
      } else {
        const errMsg = data.message || (data.error ? (data.error.message || data.error) : 'Unknown connection error');
        showModal(`Reconnection Failed: ${h.name}`, `
          <div style="padding: 24px 20px; text-align: center;">
            <div style="font-size: 38px; margin-bottom: 12px; color: var(--accent-red);">✗</div>
            <div style="font-size: 16px; font-weight: 600; color: var(--accent-red); margin-bottom: 8px;">Connection Failed</div>
            <div style="font-size: 12px; color: var(--text-muted); margin-bottom: 16px; line-height: 1.5; max-width: 440px; margin-left: auto; margin-right: auto; word-break: break-word;">
              ${escapeHtml(errMsg)}
            </div>
          </div>
        `, `
          <button class="btn btn-secondary" id="mBtnBackToDetails">← Go Back to Details</button>
          <button class="btn btn-primary" id="mBtnRetryReconnect" style="background: #10b981; border-color: #059669; color: #fff; font-weight: 600;">🔄 Retry</button>
        `);

        document.getElementById('mBtnBackToDetails')?.addEventListener('click', () => {
          showHostSummaryModal(h);
        });
        document.getElementById('mBtnRetryReconnect')?.addEventListener('click', () => {
          handleReconnectHost(h);
        });
      }
    } catch (err) {
      showModal(`Reconnection Failed: ${h.name}`, `
        <div style="padding: 24px 20px; text-align: center;">
          <div style="font-size: 38px; margin-bottom: 12px; color: var(--accent-red);">✗</div>
          <div style="font-size: 16px; font-weight: 600; color: var(--accent-red); margin-bottom: 8px;">Connection Failed</div>
          <div style="font-size: 12px; color: var(--text-muted); margin-bottom: 16px; line-height: 1.5; max-width: 440px; margin-left: auto; margin-right: auto; word-break: break-word;">
            ${escapeHtml(err.message)}
          </div>
        </div>
      `, `
        <button class="btn btn-secondary" id="mBtnBackToDetails">← Go Back to Details</button>
        <button class="btn btn-primary" id="mBtnRetryReconnect" style="background: #10b981; border-color: #059669; color: #fff; font-weight: 600;">🔄 Retry</button>
      `);

      document.getElementById('mBtnBackToDetails')?.addEventListener('click', () => {
        showHostSummaryModal(h);
      });
      document.getElementById('mBtnRetryReconnect')?.addEventListener('click', () => {
        handleReconnectHost(h);
      });
    }
  }

  // Smooth Host Binary Upgrade Flow
  async function handleUpdateHost(h) {
    if (!confirm(`Upgrade and restart ackbard daemon & hook binaries on "${h.name}" to v${state.version}?`)) return;

    showModal(`Updating ${h.name}`, `
      <div style="padding: 32px 20px; text-align: center;">
        <div class="modal-loading-spinner"></div>
        <div style="font-size: 15px; font-weight: 600; color: #fff; margin-bottom: 6px;">Upgrading ackbard on ${h.name}...</div>
        <div style="font-size: 12px; color: var(--text-muted);">Compiling and deploying v${state.version}</div>
      </div>
    `, '');

    try {
      const res = await fetch('/v1/hosts/update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: h.name })
      });
      const data = await res.json().catch(() => ({}));

      if (res.ok && data.status === 'success') {
        showModal(`Update Complete: ${h.name}`, `
          <div style="padding: 32px 20px; text-align: center;">
            <div style="font-size: 38px; margin-bottom: 12px; color: var(--accent-green);">✓</div>
            <div style="font-size: 16px; font-weight: 600; color: var(--accent-green); margin-bottom: 6px;">Update Succeeded</div>
            <div style="font-size: 12px; color: var(--text-muted);">${data.message || 'Upgrade completed successfully!'}</div>
          </div>
        `, '');

        await fetchHosts();
        await fetchSessions();
        setTimeout(() => {
          showHostSummaryModal(h);
        }, 1000);
      } else {
        const errMsg = data.message || (data.error ? (data.error.message || data.error) : 'Unknown update error');
        showModal(`Update Failed: ${h.name}`, `
          <div style="padding: 24px 20px; text-align: center;">
            <div style="font-size: 38px; margin-bottom: 12px; color: var(--accent-red);">✗</div>
            <div style="font-size: 16px; font-weight: 600; color: var(--accent-red); margin-bottom: 8px;">Update Failed</div>
            <div style="font-size: 12px; color: var(--text-muted); margin-bottom: 16px; line-height: 1.5; max-width: 440px; margin-left: auto; margin-right: auto; word-break: break-word;">
              ${escapeHtml(errMsg)}
            </div>
          </div>
        `, `
          <button class="btn btn-secondary" id="mBtnBackToDetailsUpdate">← Go Back to Details</button>
        `);

        document.getElementById('mBtnBackToDetailsUpdate')?.addEventListener('click', () => {
          showHostSummaryModal(h);
        });
      }
    } catch (err) {
      showModal(`Update Failed: ${h.name}`, `
        <div style="padding: 24px 20px; text-align: center;">
          <div style="font-size: 38px; margin-bottom: 12px; color: var(--accent-red);">✗</div>
          <div style="font-size: 16px; font-weight: 600; color: var(--accent-red); margin-bottom: 8px;">Update Failed</div>
          <div style="font-size: 12px; color: var(--text-muted); margin-bottom: 16px; line-height: 1.5; max-width: 440px; margin-left: auto; margin-right: auto; word-break: break-word;">
            ${escapeHtml(err.message)}
          </div>
        </div>
      `, `
        <button class="btn btn-secondary" id="mBtnBackToDetailsUpdate">← Go Back to Details</button>
      `);

      document.getElementById('mBtnBackToDetailsUpdate')?.addEventListener('click', () => {
        showHostSummaryModal(h);
      });
    }
  }

  // Edit Remote Host Modal
  function showEditHostModal(h) {
    const body = `
      <div class="form-group">
        <label>Host Alias (identifier)</label>
        <input type="text" id="editHostName" value="${escapeHtml(h.name)}" readonly style="opacity: 0.7; cursor: not-allowed;" />
      </div>
      <div class="form-group">
        <label>SSH Target (user@hostname or SSH config host)</label>
        <input type="text" id="editSshTarget" placeholder="e.g. dev4u@legion" value="${escapeHtml(h.ssh_target || '')}" />
      </div>
      <div class="form-group">
        <label>Daemon Endpoint URL</label>
        <input type="text" id="editHostUrl" placeholder="http://127.0.0.1:7778 or http://legion:7777" value="${escapeHtml(h.url || '')}" />
        <div style="font-size: 11px; color: var(--text-muted); margin-top: 4px;">
          Use <code>http://127.0.0.1:7778</code> for SSH tunnel, or direct Tailscale URL like <code>http://legion:7777</code>.
        </div>
      </div>
    `;

    const footer = `
      <button class="btn btn-secondary" id="editBtnBack">← Back</button>
      <button class="btn btn-primary" id="editBtnSave">Save Changes</button>
    `;

    showModal(`Edit Host: ${h.name}`, body, footer);

    document.getElementById('editBtnBack')?.addEventListener('click', () => {
      showHostSummaryModal(h);
    });

    document.getElementById('editBtnSave')?.addEventListener('click', async () => {
      const name = document.getElementById('editHostName').value.trim();
      const ssh = document.getElementById('editSshTarget').value.trim();
      const url = document.getElementById('editHostUrl').value.trim();
      if (!name || !url) {
        alert('Host name and URL are required');
        return;
      }

      try {
        const res = await fetch('/v1/hosts', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name, ssh_target: ssh, url, remote_cwd: h.remote_cwd || '' })
        });
        if (!res.ok) {
          const errText = await res.text();
          throw new Error(errText || 'Failed to save host');
        }
        await fetchHosts();
        await fetchSessions();
        const updated = (state.hosts || []).find(x => x.name === name) || { ...h, name, ssh_target: ssh, url };
        showHostSummaryModal(updated);
      } catch (err) {
        alert('Failed to update host: ' + err.message);
      }
    });
  }

  // Host Summary & Diagnostics Modal
  async function showHostSummaryModal(h) {
    const baseUrl = h.url ? h.url.replace(/\/$/, '') : '';
    const isLocal = !h.url || isLocalHost(h.name);
    const hostLabel = formatHostLabel(h.name || getSelfHostName());

    showModal(`Server Inspector: ${hostLabel}`, `
      <div style="padding: 20px; text-align: center; color: var(--text-muted);">
        <div style="margin-bottom: 8px;">⏳ Connecting to daemon at <code>${h.url || '127.0.0.1:7777'}</code>...</div>
      </div>
    `, `<button class="btn btn-secondary" onclick="document.getElementById('modalOverlay').style.display='none'">Close</button>`);

    try {
      let daemonVersion = 'unknown';
      let isOnline = false;
      try {
        const vRes = await fetch(`${baseUrl}/v1/version`);
        if (vRes.ok) {
          const vData = await vRes.json();
          daemonVersion = vData.version || 'online';
          isOnline = true;
        }
      } catch (e) {
        console.warn('Version check failed:', e);
      }

      let discovery = [];
      try {
        const dRes = await fetch(`${baseUrl}/v1/agents/discovery`);
        if (dRes.ok) {
          discovery = await dRes.json() || [];
        }
      } catch (e) {
        console.warn('Discovery check failed:', e);
      }

      let rowsHtml = '';
      discovery.forEach(d => {
        const instBadge = d.installed ? '<span style="color: var(--accent-green);">● Installed</span>' : '<span style="color: var(--text-dim);">○ Not Detected</span>';
        const hookBadge = d.hook_configured ? '<span style="color: var(--accent-green);">🟢 Active</span>' : '<span style="color: var(--accent-yellow);">⚠️ Missing</span>';
        rowsHtml += `
          <tr>
            <td><strong>${d.agent}</strong></td>
            <td>${instBadge}</td>
            <td>${hookBadge}</td>
            <td><code>${d.setup_cmd || '—'}</code></td>
          </tr>
        `;
      });

      const setupCmd = isLocal ? 'ackbar setup-hooks' : `ssh ${h.ssh_target || h.name} "ackbar setup-hooks"`;

      const isOutdated = !isLocal && isOnline && daemonVersion !== '...' && daemonVersion !== state.version;
      const updateBanner = isOutdated ? `
        <div style="margin-top: 10px; padding: 10px 12px; background: rgba(245, 158, 11, 0.15); border: 1px solid rgba(245, 158, 11, 0.4); border-radius: 6px; font-size: 11px; display: flex; align-items: center; justify-content: space-between; color: #fbbf24;">
          <div>⚠️ <strong>Update Available:</strong> Remote host is running <code>v${daemonVersion}</code>, control plane is <code>v${state.version}</code>.</div>
        </div>
      ` : '';

      const body = `
        <div style="display: flex; gap: 12px; margin-bottom: 12px; align-items: center; background: var(--bg-card); padding: 12px; border-radius: 8px; border: 1px solid var(--border-color);">
          <div style="font-size: 24px;">🖥️</div>
          <div style="flex: 1;">
            <div style="font-weight: 600; font-size: 14px; color: #fff;">${h.name} ${isOnline ? '<span style="color: var(--accent-green); font-size: 12px;">🟢 Online</span>' : '<span style="color: var(--accent-red); font-size: 12px;">🔴 Offline</span>'}</div>
            <div style="font-size: 11px; color: var(--text-muted); font-family: var(--font-mono); margin-top: 2px;">
              Endpoint: ${h.url || 'http://127.0.0.1:7777'} &nbsp;|&nbsp; ackbard v${daemonVersion} ${h.ssh_target ? `&nbsp;|&nbsp; SSH: ${h.ssh_target}` : ''}
            </div>
          </div>
        </div>
        ${updateBanner}

        <h4 style="font-size: 12px; color: var(--text-muted); text-transform: uppercase; margin: 14px 0 8px 0; letter-spacing: 0.5px;">Agent Hook Configuration on ${h.name}</h4>
        <table class="hook-table">
          <thead>
            <tr>
              <th>Agent</th>
              <th>CLI Status</th>
              <th>Hook Status</th>
              <th>Configuration</th>
            </tr>
          </thead>
          <tbody>
            ${rowsHtml || '<tr><td colspan="4" style="text-align: center; color: var(--text-dim);">No agent diagnostics returned</td></tr>'}
          </tbody>
        </table>

        <div style="background: #090a0f; border: 1px solid var(--border-color); border-radius: 6px; padding: 10px; margin-top: 14px; font-size: 11px; font-family: var(--font-mono);">
          <span style="color: var(--accent-cyan); font-weight: 600;">⚡ 1-Click CLI Setup Command:</span><br>
          <span style="color: #fff;">${setupCmd}</span>
        </div>
      `;

      const footer = `
        ${!isLocal ? `<button class="btn btn-danger" id="hBtnDeleteHost" style="margin-right: auto;">🗑 Remove</button>` : ''}
        ${!isLocal ? `<button class="btn btn-secondary" id="hBtnEditHost">✏️ Edit Host</button>` : ''}
        ${!isLocal && !isOnline ? `<button class="btn btn-primary" id="hBtnReconnectHost" style="background: #10b981; border-color: #059669; color: #fff; font-weight: 600;">🔄 Reconnect SSH Tunnel</button>` : ''}
        ${!isLocal && isOnline ? `<button class="btn btn-primary" id="hBtnUpdateHost" style="background: #06b6d4; border-color: #0891b2; color: #090a0f; font-weight: 600;">⬆ Update ackbard</button>` : ''}
        <button class="btn btn-secondary" id="hBtnPurgeHost">🔄 Safe Purge</button>
        <button class="btn btn-secondary" onclick="document.getElementById('modalOverlay').style.display='none'">Done</button>
      `;

      showModal(`Server Inspector: ${h.name}`, body, footer);

      const editBtn = document.getElementById('hBtnEditHost');
      if (editBtn) {
        editBtn.addEventListener('click', () => {
          showEditHostModal(h);
        });
      }

      const reconnectBtn = document.getElementById('hBtnReconnectHost');
      if (reconnectBtn) {
        reconnectBtn.addEventListener('click', () => {
          handleReconnectHost(h);
        });
      }

      const updateBtn = document.getElementById('hBtnUpdateHost');
      if (updateBtn) {
        updateBtn.addEventListener('click', () => {
          handleUpdateHost(h);
        });
      }

      const purgeBtn = document.getElementById('hBtnPurgeHost');
      if (purgeBtn) {
        purgeBtn.addEventListener('click', async () => {
          if (confirm(`Safe Purge & Rescan on ${h.name}? Refresh live sessions from disk while strictly preserving groups.`)) {
            await fetch(`${baseUrl}/v1/maintenance/purge`, { method: 'POST' });
            await fetchSessions();
            showHostSummaryModal(h);
          }
        });
      }

      const delBtn = document.getElementById('hBtnDeleteHost');
      if (delBtn) {
        delBtn.addEventListener('click', async () => {
          if (confirm(`Remove remote host registration "${h.name}"?`)) {
            await fetch(`/v1/hosts?name=${encodeURIComponent(h.name)}`, { method: 'DELETE' });
            await fetchHosts();
            await fetchSessions();
            hideModal();
          }
        });
      }
    } catch (err) {
      showModal(`Server Inspector: ${h.name}`, `<div style="color: var(--accent-red); padding: 12px;">Error connecting to host: ${err.message}</div>`, `<button class="btn btn-secondary" onclick="document.getElementById('modalOverlay').style.display='none'">Close</button>`);
    }
  }

  // Sidebar Drag Resizer
  function setupResizer() {
    if (!el.sidebarResizer || !el.sidebarPanel) return;
    let isResizing = false;

    el.sidebarResizer.addEventListener('mousedown', () => {
      isResizing = true;
      el.sidebarResizer.classList.add('resizing');
      document.body.style.cursor = 'col-resize';
    });

    document.addEventListener('mousemove', (e) => {
      if (!isResizing) return;
      const newWidth = Math.max(200, Math.min(600, e.clientX));
      el.sidebarPanel.style.width = `${newWidth}px`;
      const activeTab = state.openTabs.get(state.activeTabId);
      if (activeTab && activeTab.type === 'terminal' && activeTab.fitAddon) {
        try {
          activeTab.fitAddon.fit();
          if (activeTab.terminal && activeTab.socket && activeTab.socket.readyState === WebSocket.OPEN) {
            sendTerminalResize(activeTab.socket, activeTab.terminal.cols, activeTab.terminal.rows);
          }
        } catch (e) {}
      }
    });

    document.addEventListener('mouseup', () => {
      if (isResizing) {
        isResizing = false;
        el.sidebarResizer.classList.remove('resizing');
        document.body.style.cursor = '';
        state.openTabs.forEach(t => {
          if (t.fitAddon && t.fitAddon.fit) {
            try {
              t.fitAddon.fit();
              if (t.terminal && t.socket && t.socket.readyState === WebSocket.OPEN) {
                sendTerminalResize(t.socket, t.terminal.cols, t.terminal.rows);
              }
            } catch (e) {}
          }
        });
      }
    });
  }

  // Expose global hooks for developer console and debugging
  window.state = state;
  window.openSessionInTab = openSessionInTab;
  window.openSessionDetailsTab = openSessionDetailsTab;
  window.reconnectTerminalTab = reconnectTerminalTab;

  // Launch on DOM Content Loaded
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
