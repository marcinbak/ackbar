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

  // Application State
  const state = {
    version: '...',
    sessions: [],
    treeNodes: [],
    hosts: [],
    openTabs: new Map(), // tabId -> { type: 'terminal'|'details'|'doc', session, terminal, fitAddon, socket, containerEl, tabEl }
    activeTabId: null,
    collapsedGroups: loadCollapsedGroups(),
    searchQuery: '',
    showArchived: false,
    draggedSession: null,
    contextMenuSession: null,
    contextMenuGroupPath: null,
    contextMenuTabId: null,
    cmdPaletteSelectedIndex: 0,
    cmdPaletteItems: []
  };

  window.state = state;
  window.__ackbarState = state;

  // Format host name to server name only (e.g. "dev4u@legion" -> "legion")
  function formatHostLabel(hostName) {
    if (!hostName || hostName === 'local') return 'local';
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

  // Format full absolute timestamp
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
    const isRemote = host && host !== 'local';
    const hostLabel = host || 'local';
    const formattedPath = cwd.startsWith('/') ? cwd : '/' + cwd;
    const directUri = isRemote 
      ? `vscode://vscode-remote/ssh-remote+${hostLabel}${formattedPath}` 
      : `vscode://file${formattedPath}`;

    try {
      const res = await fetch('/v1/editor/open', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: cwd, host: host || 'local' })
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

  // State Emoji / Icon Mapping
  function getStateEmojiText(session) {
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
    cmItemResume: document.getElementById('cmItemResume'),
    cmItemNewTab: document.getElementById('cmItemNewTab'),
    cmItemVSCode: document.getElementById('cmItemVSCode'),
    cmItemDocs: document.getElementById('cmItemDocs'),
    cmItemRestart: document.getElementById('cmItemRestart'),
    cmItemKill: document.getElementById('cmItemKill'),
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
            const timer = setTimeout(() => controller.abort(), 2500);
            const vRes = await fetch(fetchUrl, { signal: controller.signal });
            clearTimeout(timer);
            if (vRes.ok) {
              const vData = await vRes.json();
              return {
                ...h,
                online: true,
                version: vData.version || 'online',
                latencyMs: Date.now() - start
              };
            }
          } catch (e) {}
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
    const localHost = state.hosts.find(h => h.name === 'local') || { name: 'local', url: '', online: true };
    const remoteHosts = state.hosts.filter(h => h.name !== 'local');
    const hostsToRender = [localHost, ...remoteHosts];

    hostsToRender.forEach(h => {
      const span = document.createElement('span');
      span.className = `host-badge ${h.online ? 'host-online' : 'host-offline'}`;
      const statusDot = h.online ? '🟢' : '🔴';
      span.textContent = `${formatHostLabel(h.name)} ${statusDot}`;
      span.title = h.online
        ? `${formatHostLabel(h.name)} is ONLINE (${h.latencyMs != null ? h.latencyMs + 'ms' : 'connected'}, v${h.version || '?'})`
        : `${formatHostLabel(h.name)} is OFFLINE (Click to reconnect SSH tunnel / inspect)`;
      span.style.cursor = 'pointer';
      span.addEventListener('click', () => showHostSummaryModal(h));
      el.hostList.appendChild(span);
    });
  }

  // Fetch Tree Nodes across all available hosts
  async function fetchTreeNodes() {
    try {
      const targetHosts = [
        { name: 'local', url: '' },
        ...(state.hosts || []).filter(h => h.url && h.name !== 'local')
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

  // Client-Side Multi-Host Session Aggregation & Deduplication
  async function fetchSessions() {
    try {
      const targetHosts = [
        { name: 'local', url: '' },
        ...state.hosts.filter(h => h.url && h.name !== 'local')
      ];

      const sessionArrays = await Promise.all(targetHosts.map(async (h) => {
        try {
          const fetchUrl = h.url ? `${h.url.replace(/\/$/, '')}/v1/sessions` : '/v1/sessions';
          const res = await fetch(fetchUrl);
          if (res.ok) {
            const list = await res.json() || [];
            return list.map(s => ({
              ...s,
              host: h.name !== 'local' ? h.name : (s.host || 'local'),
              hostUrl: h.url || ''
            }));
          }
        } catch (e) {
          console.warn(`Host ${h.name} unreachable:`, e);
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

  // Deduplication Algorithm (matches TUI mergeSessions)
  function deduplicateSessions(list) {
    const merged = new Map();

    list.forEach(sess => {
      let dedupKey = '';
      const host = sess.host || 'local';
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
      { name: 'local', url: '' },
      ...(state.hosts || []).filter(h => h.url && h.name !== 'local')
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

            if (h.name !== 'local') {
              updatedSess.host = h.name;
              updatedSess.hostUrl = h.url || '';
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

    // 1. Group sessions by exact logical node path
    const sessionsByPath = new Map();
    const unassigned = [];

    state.sessions.forEach(sess => {
      if (sess.archived && !state.showArchived) {
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
          }
        } catch (err) {
          console.error('Drop error:', err);
        }
      });

      headerEl.addEventListener('click', () => {
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

      // 3a. Direct Sessions matching this exact path (sorted by newest/active interaction first)
      const directSessions = sortSessionsByInteraction(sessionsByPath.get(path) || []);
      directSessions.forEach(sess => {
        childrenEl.appendChild(createSessionRowElement(sess));
      });

      // 3b. Direct Child Subgroups (nested recursive call)
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

      // 3c. Empty group placeholder hint
      if (directSessions.length === 0 && childSubgroupsCount === 0) {
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
      sortedUnassigned.forEach(sess => {
        childrenEl.appendChild(createSessionRowElement(sess));
      });

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
        sessionHost: session.host || 'local'
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

    const name = document.createElement('span');
    name.className = 'session-name';
    name.textContent = sessionFullName;
    name.title = sessionFullName;

    left.appendChild(dot);
    left.appendChild(name);

    const right = document.createElement('div');
    right.className = 'session-item-right';

    // Agent badge with provider icon (compact icon-only with tooltip)
    const agentBadge = document.createElement('span');
    agentBadge.innerHTML = getAgentBadgeHtml(session.agent, true);
    right.appendChild(agentBadge);

    if (session.context_pct > 0) {
      const ctxBadge = document.createElement('span');
      ctxBadge.className = 'badge-ctx';
      ctxBadge.textContent = `${session.context_pct}%`;
      ctxBadge.title = `Context Window Used: ${session.context_pct}%`;
      right.appendChild(ctxBadge);
    }

    if (session.host && session.host !== 'local') {
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
      const baseUrl = (sess && sess.hostUrl) ? sess.hostUrl.replace(/\/$/, '') : '';
      const url = `${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sessionId)}&action=move&node_path=${encodeURIComponent(targetPath)}`;
      const res = await fetch(url, { method: 'POST' });
      if (res.ok) {
        await fetchSessions();
      }
    } catch (err) {
      console.error('Failed to move session:', err);
    }
  }

  // Establish or Re-establish WebSocket Connection for a Terminal Tab
  function connectTerminalWebSocket(tab, tabId, term, fitAddon, session) {
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

    const hostParam = session.host || 'local';
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

  // Upload file (Image or PDF) to daemon and inject path into active terminal
  async function uploadAndAttachFile(file, tabObj) {
    if (!file || !tabObj) return;

    const filename = file.name || 'clipboard_image.png';
    const ext = filename.split('.').pop().toLowerCase();
    const allowed = ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg', 'pdf'];
    const type = (file.type || '').toLowerCase();
    const isAllowedMime = type.startsWith('image/') || type === 'application/pdf' || type.includes('bmp');
    const isAllowedExt = allowed.includes(ext);

    if (!isAllowedMime && !isAllowedExt) {
      showUploadToast(`Unsupported file type: ${filename} (allowed: PNG, JPG, WEBP, GIF, BMP, SVG, PDF)`, 'error', 4000);
      return;
    }

    showUploadToast(`Uploading ${filename}...`, 'info', 2000);

    const formData = new FormData();
    formData.append('file', file, filename);

    const hostParam = (tabObj.session && tabObj.session.host) ? tabObj.session.host : 'local';
    const baseUrl = (tabObj.session && tabObj.session.hostUrl && hostParam !== 'local') ? tabObj.session.hostUrl.replace(/\/$/, '') : '';
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

        // Inject quoted path with trailing space into active terminal
        const quotedPath = `"${data.path}" `;
        if (tabObj.socket && tabObj.socket.readyState === WebSocket.OPEN) {
          tabObj.socket.send(quotedPath);
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
      const baseUrl = hostRec && hostRec.url && session.host !== 'local' ? hostRec.url.replace(/\/$/, '') : '';
      await fetch(`${baseUrl}/v1/sessions/${encodeURIComponent(session.id)}?action=read`, { method: 'POST' });
    } catch (e) {
      console.warn('Failed to mark session as read on daemon:', e);
    }
  }

  // Open Session in Terminal Tab
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

    // 2. Create Terminal View Container
    const containerEl = document.createElement('div');
    containerEl.className = 'terminal-tab-view';
    containerEl.id = `termView_${tabId.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    containerEl.addEventListener('click', () => {
      const currentTab = state.openTabs.get(tabId);
      if (currentTab && (!currentTab.socket || currentTab.socket.readyState === WebSocket.CLOSED || currentTab.socket.readyState === WebSocket.CLOSING)) {
        reconnectTerminalTab(tabId);
      }
    });
    if (el.terminalViewport) el.terminalViewport.appendChild(containerEl);

    // 3. Drop Overlay for Drag-and-Drop file uploads (Images & PDFs)
    const dropOverlay = document.createElement('div');
    dropOverlay.className = 'terminal-drop-overlay';
    dropOverlay.innerHTML = `<div class="drop-badge">📎 Drop image or PDF to upload & attach</div>`;
    containerEl.appendChild(dropOverlay);

    containerEl.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
        dropOverlay.classList.add('active');
      }
    });

    containerEl.addEventListener('dragleave', (e) => {
      if (!containerEl.contains(e.relatedTarget)) {
        dropOverlay.classList.remove('active');
      }
    });

    containerEl.addEventListener('drop', (e) => {
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
    containerEl.addEventListener('paste', (e) => {
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

    term.open(containerEl);

    // Register OSC 52 Clipboard handler (receives base64-encoded clipboard from remote tmux)
    if (term.parser && term.parser.registerOscHandler) {
      term.parser.registerOscHandler(52, (data) => {
        // Format: "c;<base64-payload>" or ";<base64-payload>"
        const parts = data.split(';');
        const b64 = parts.length > 1 ? parts[1] : parts[0];
        if (b64) {
          try {
            const decoded = atob(b64);
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
      tabEl,
      pingTimer: null,
      reconnectTimer: null,
      reconnectAttempts: 0
    };
    state.openTabs.set(tabId, tabObj);

    // 4. Connect WebSocket PTY
    connectTerminalWebSocket(tabObj, tabId, term, fitAddon, session);

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

    const tabId = `doc_${(host || 'local').replace(/[^a-zA-Z0-9_-]/g, '_')}_${docPath.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    if (state.openTabs.has(tabId)) {
      activateTab(tabId);
      return;
    }

    const isRemote = host && host !== 'local';
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

    const renderedHtml = window.marked ? window.marked.parse(markdownContent) : `<pre>${markdownContent}</pre>`;

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

    const host = session.host || 'local';
    const isRemote = host && host !== 'local';
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
                <span class="msg-time">${timeStr}</span>
              </div>
              <div class="msg-body">${escapeHtml(m.content).replace(/\n/g, '<br/>')}</div>
            </div>
          `;
        } else if (m.role === 'assistant') {
          const bodyHtml = window.marked ? window.marked.parse(m.content || '') : `<pre>${escapeHtml(m.content)}</pre>`;
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
                <span class="msg-time">${timeStr}</span>
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
    const isRemote = host && host !== 'local';
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
            const h = decodeURIComponent(card.dataset.host || 'local');
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
        }
        if (tab.type === 'terminal') {
          if (!tab.socket || tab.socket.readyState === WebSocket.CLOSED || tab.socket.readyState === WebSocket.CLOSING) {
            reconnectTerminalTab(tabId);
          }
        }
        setTimeout(() => {
          if (tab.fitAddon && tab.fitAddon.fit) tab.fitAddon.fit();
          if (tab.terminal) {
            tab.terminal.focus();
            sendTerminalResize(tab.socket, tab.terminal.cols, tab.terminal.rows);
          }
        }, 30);
        updateStatusbar(tab.session);
      }
    });

    savePersistedTabs();
    renderTree();
  }

  // Close Tab
  function closeTab(tabId) {
    const tab = state.openTabs.get(tabId);
    if (!tab) return;

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
          host: tab.session ? tab.session.host : 'local'
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
    state.contextMenuSession = session;

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
    if (el.sbModelBadge) el.sbModelBadge.textContent = session.agent || 'claude';
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
    });

    window.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        hideContextMenu();
        hideGroupContextMenu();
        hideTabContextMenu();
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
          try {
            await navigator.clipboard.writeText(text);
          } catch (e) {
            const ta = document.createElement('textarea');
            ta.value = text;
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            document.body.removeChild(ta);
          }
        }
      });
    }

    if (el.cmItemCopyPath) {
      el.cmItemCopyPath.addEventListener('click', async () => {
        if (state.contextMenuSession) {
          const sess = state.contextMenuSession;
          const sessName = sess.name || sess.agent || '';
          const fullPath = sess.node_path ? `${sess.node_path}/${sessName}` : sessName;
          try {
            await navigator.clipboard.writeText(fullPath);
          } catch (e) {
            const ta = document.createElement('textarea');
            ta.value = fullPath;
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            document.body.removeChild(ta);
          }
        }
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

    if (el.cmItemArchive) {
      el.cmItemArchive.addEventListener('click', () => {
        if (state.contextMenuSession) {
          state.contextMenuSession.archived = !state.contextMenuSession.archived;
          renderTree();
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
            const host = groupSess ? groupSess.host : 'local';
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
            const host = groupSess ? groupSess.host : 'local';
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

    // 1. Direct Project Key Matching on target host
    const srcSess = (state.sessions || []).find(s => (s.host || 'local') === fromHost && s.cwd === currentPath);
    if (srcSess && srcSess.project_key) {
      const tgtSess = (state.sessions || []).find(s => (s.host || 'local') === toHost && s.project_key === srcSess.project_key && s.cwd);
      if (tgtSess && tgtSess.cwd) return tgtSess.cwd;
    }

    // 2. Project Directory Basename Matching on target host
    const cleanPath = currentPath.replace(/\/+$/, '');
    const currentBase = cleanPath.split('/').pop();
    if (currentBase) {
      const tgtMatch = (state.sessions || []).find(s => (s.host || 'local') === toHost && s.cwd && (s.cwd.endsWith('/' + currentBase) || s.cwd.split('/').pop() === currentBase));
      if (tgtMatch && tgtMatch.cwd) return tgtMatch.cwd;

      // Check target host configured remote_cwd
      const targetHostRec = (state.hosts || []).find(h => h.name === toHost);
      if (targetHostRec && targetHostRec.remote_cwd && toHost !== 'local') {
        const root = targetHostRec.remote_cwd.replace(/\/+$/, '');
        return `${root}/${currentBase}`;
      }
    }

    // 3. Standard Home Prefix Translation (macOS /Users/<user> <-> Linux /home/<user>)
    if ((fromHost === 'local' || currentPath.startsWith('/Users/')) && toHost !== 'local') {
      // macOS -> Linux
      if (currentPath.startsWith('/Users/')) {
        return currentPath.replace(/^\/Users\/([^/]+)/, '/home/$1');
      }
    } else if (fromHost !== 'local' && toHost === 'local') {
      // Linux -> macOS
      if (currentPath.startsWith('/home/')) {
        return currentPath.replace(/^\/home\/([^/]+)/, '/Users/$1');
      }
    }

    return currentPath;
  }

  // Helper: Get relevant folders for a specific host
  function getFoldersForHost(targetHost) {
    const hostSessions = (state.sessions || []).filter(s => (s.host || 'local') === targetHost && s.cwd);
    const hostCwds = new Set(hostSessions.map(s => s.cwd));

    // Also include tree node linked project dirs for this host or generic
    (state.treeNodes || []).forEach(n => {
      if (n.project_dir && (!n.host || n.host === targetHost)) {
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
      const translated = translatePathForHost(p, 'local', targetHost);
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

    // Determine initial active host
    let currentSelectedHost = 'local';
    const activeTab = state.openTabs.get(state.activeTabId);
    if (activeTab && activeTab.session && activeTab.session.host) {
      currentSelectedHost = activeTab.session.host;
    }

    // Helper: Dynamically fetch & populate available agents for the chosen host
    async function updateAgentOptions(targetHost) {
      if (!agentSelect) return;
      agentSelect.innerHTML = '<option value="">⏳ Detecting agents on host...</option>';

      try {
        const hostRec = (state.hosts || []).find(h => h.name === targetHost);
        const baseUrl = hostRec && hostRec.url && targetHost !== 'local' ? hostRec.url.replace(/\/$/, '') : '';
        const res = await fetch(`${baseUrl}/v1/agents/discovery`);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const discovery = await res.json() || [];

        const agentDisplayNames = {
          'claude-code': 'Claude Code (Anthropic)',
          'antigravity': 'Google Antigravity (agy)',
          'codex': 'OpenAI Codex'
        };

        const installed = discovery.filter(d => d.installed);
        agentSelect.innerHTML = '';

        if (installed.length > 0) {
          installed.forEach(d => {
            const opt = document.createElement('option');
            opt.value = d.agent;
            opt.textContent = `${agentDisplayNames[d.agent] || d.agent} (Installed)`;
            agentSelect.appendChild(opt);
          });
        } else {
          discovery.forEach(d => {
            const opt = document.createElement('option');
            opt.value = d.agent;
            opt.textContent = `${agentDisplayNames[d.agent] || d.agent} (Not detected)`;
            agentSelect.appendChild(opt);
          });
        }
      } catch (err) {
        console.warn('Agent discovery fallback for host:', targetHost, err);
        agentSelect.innerHTML = `
          <option value="claude-code">Claude Code (Anthropic)</option>
          <option value="antigravity">Google Antigravity (agy)</option>
          <option value="codex">OpenAI Codex</option>
        `;
      }
    }

    // Helper: Update folder suggestions datalist and adapt folderInput.value on host change
    function updateFolderSuggestions(targetHost, prevHost = null) {
      if (!folderList) return;
      const hostFolders = getFoldersForHost(targetHost);
      folderList.innerHTML = '';
      hostFolders.forEach(c => {
        const opt = document.createElement('option');
        opt.value = c;
        folderList.appendChild(opt);
      });

      // If user switched hosts, translate the current folder path intelligently
      if (prevHost && prevHost !== targetHost && folderInput) {
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

    // 1. Check if prefillGroup dictates a specific host or project directory
    if (prefillGroup) {
      const matchedNode = (state.treeNodes || []).find(n => n.path === prefillGroup);
      if (matchedNode && matchedNode.host) {
        currentSelectedHost = matchedNode.host;
      }
    }

    // 2. Populate Hosts
    if (hostSelect) {
      hostSelect.innerHTML = '<option value="local">local (This Machine)</option>';
      (state.hosts || []).forEach(h => {
        if (h.name !== 'local') {
          const opt = document.createElement('option');
          opt.value = h.name;
          opt.textContent = `${formatHostLabel(h.name)} (${h.url || 'remote'})`;
          if (h.name === currentSelectedHost) opt.selected = true;
          hostSelect.appendChild(opt);
        }
      });
      if (currentSelectedHost === 'local') hostSelect.value = 'local';

      // Update agents & folders when host selection changes
      hostSelect.onchange = () => {
        const newHost = hostSelect.value;
        updateFolderSuggestions(newHost, currentSelectedHost);
        currentSelectedHost = newHost;
        updateAgentOptions(newHost);
      };
    }

    // 3. Populate Groups & prefill linked project folder if group has one
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
    }

    // 4. Initial folder suggestions population for selected host
    updateFolderSuggestions(currentSelectedHost);

    // 5. Initial folder prefill
    if (folderInput) {
      folderInput.value = '';
      if (prefillGroup) {
        const matchedNode = (state.treeNodes || []).find(n => n.path === prefillGroup);
        if (matchedNode && matchedNode.project_dir) {
          folderInput.value = matchedNode.project_dir;
        } else {
          const childNode = (state.treeNodes || []).find(n => n.path.startsWith(prefillGroup + '/') && n.project_dir);
          if (childNode && childNode.project_dir) {
            folderInput.value = childNode.project_dir;
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

    // Initial agent discovery for selected host
    await updateAgentOptions(currentSelectedHost);
  }

  function hideNewSessionModal() {
    const modal = document.getElementById('modalNewSession');
    if (modal) modal.style.display = 'none';
  }

  async function handleSpawnNewSession() {
    const hostSelect = document.getElementById('newSessionHost');
    const agentSelect = document.getElementById('newSessionAgent');
    const folderInput = document.getElementById('newSessionFolder');
    const groupSelect = document.getElementById('newSessionGroup');
    const submitBtn = document.getElementById('btnSubmitNewSession');

    const host = hostSelect ? hostSelect.value : 'local';
    const agent = agentSelect ? agentSelect.value : 'claude-code';
    const cwd = folderInput ? folderInput.value.trim() : '';
    const targetGroup = groupSelect ? groupSelect.value : '';

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
        body: JSON.stringify({ host, agent, cwd })
      });

      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        throw new Error(errData.message || errData.error || `HTTP ${res.status}`);
      }

      const spawnResult = await res.json();
      const spawnedNativeId = spawnResult.session_id || spawnResult.native_id || '';
      const spawnedSessId = spawnResult.id || `${agent}:${host}:${spawnedNativeId}`;

      hideNewSessionModal();

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
          <option value="local">local (This Machine)</option>
          ${(state.hosts || []).filter(h => h.name !== 'local').map(h => `<option value="${h.name}">${formatHostLabel(h.name)} (${h.url || 'remote'})</option>`).join('')}
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
    showHostSummaryModal({ name: 'local', url: '' });
  }

  // Host Summary & Diagnostics Modal
  async function showHostSummaryModal(h) {
    const baseUrl = h.url ? h.url.replace(/\/$/, '') : '';
    const isLocal = !h.url || h.name === 'local';

    showModal(`Server Inspector: ${h.name}`, `
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
        ${!isLocal && !isOnline ? `<button class="btn btn-primary" id="hBtnReconnectHost" style="background: #10b981; border-color: #059669; color: #fff; font-weight: 600;">🔄 Reconnect SSH Tunnel</button>` : ''}
        ${!isLocal && isOnline ? `<button class="btn btn-primary" id="hBtnUpdateHost" style="background: #06b6d4; border-color: #0891b2; color: #090a0f; font-weight: 600;">⬆ Update ackbard</button>` : ''}
        <button class="btn btn-secondary" id="hBtnPurgeHost">🔄 Safe Purge</button>
        <button class="btn btn-secondary" onclick="document.getElementById('modalOverlay').style.display='none'">Done</button>
      `;

      showModal(`Server Inspector: ${h.name}`, body, footer);

      const reconnectBtn = document.getElementById('hBtnReconnectHost');
      if (reconnectBtn) {
        reconnectBtn.addEventListener('click', async () => {
          reconnectBtn.disabled = true;
          reconnectBtn.innerHTML = `<span>⏳</span> Reconnecting SSH tunnel...`;
          try {
            const res = await fetch('/v1/hosts/reconnect', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ name: h.name })
            });
            const data = await res.json();
            if (res.ok && data.status === 'success') {
              alert(`✅ ${data.message || 'SSH tunnel reconnected!'}`);
              await fetchHosts();
              await fetchSessions();
              await fetchTreeNodes();
              showHostSummaryModal(h);
            } else {
              alert(`❌ Reconnect failed: ${data.message || 'Unknown error'}`);
              reconnectBtn.disabled = false;
              reconnectBtn.innerHTML = `🔄 Reconnect SSH Tunnel`;
            }
          } catch (err) {
            alert(`❌ Reconnect request failed: ${err.message}`);
            reconnectBtn.disabled = false;
            reconnectBtn.innerHTML = `🔄 Reconnect SSH Tunnel`;
          }
        });
      }

      const updateBtn = document.getElementById('hBtnUpdateHost');
      if (updateBtn) {
        updateBtn.addEventListener('click', async () => {
          if (!confirm(`Upgrade and restart ackbard daemon & hook binaries on "${h.name}" to v${state.version}?`)) return;
          updateBtn.disabled = true;
          updateBtn.innerHTML = `<span>⏳</span> Upgrading ackbard on ${h.name}...`;
          try {
            const res = await fetch('/v1/hosts/update', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ name: h.name })
            });
            const data = await res.json();
            if (res.ok && data.status === 'success') {
              alert(`✅ ${data.message || 'Upgrade completed!'}`);
              await fetchHosts();
              await fetchSessions();
              showHostSummaryModal(h);
            } else {
              alert(`❌ Upgrade failed: ${data.message || 'Unknown error'}`);
              updateBtn.disabled = false;
              updateBtn.innerHTML = `⬆ Update ackbard`;
            }
          } catch (err) {
            alert(`❌ Upgrade request failed: ${err.message}`);
            updateBtn.disabled = false;
            updateBtn.innerHTML = `⬆ Update ackbard`;
          }
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
