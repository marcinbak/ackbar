// Daemon REST API Client
import { state, el } from './state.js';
import {
  getSelfHostName,
  getSelfDisplayName,
  isLocalHost,
  findHostRecord,
  formatHostLabel,
  escapeHtml,
  recordGroupSpawn
} from './utils.js';
import { renderTree, getSessionTimestamp, sortSessionsByInteraction } from './tree.js';
import { updateOpenTabsState } from './tabs.js';
import { showModal, hideModal, showHostSummaryModal } from './modals.js';

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
      done_collapsed_by_default: 'true',
      later_collapsed_by_default: 'false'
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

async function setSessionDoneState(sessionId, sessionHost, isDone) {
  try {
    const sess = state.sessions.find(s => s.id === sessionId);
    if (sess) {
      sess.is_done = isDone;
      if (isDone) {
        sess.is_later = false;
      }
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

async function setSessionLaterState(sessionId, sessionHost, isLater) {
  try {
    const sess = state.sessions.find(s => s.id === sessionId);
    if (sess) {
      sess.is_later = isLater;
      if (isLater) {
        sess.is_done = false;
      }
      renderTree();
    }
    const hostRec = (state.hosts || []).find(h => h.name === sessionHost);
    const baseUrl = (sess && sess.hostUrl) ? sess.hostUrl.replace(/\/$/, '') : (hostRec && hostRec.url && !isLocalHost(sessionHost) ? hostRec.url.replace(/\/$/, '') : '');
    const action = isLater ? 'later' : 'active';
    const url = `${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sessionId)}&action=${action}`;
    const res = await fetch(url, { method: 'POST' });
    if (res.ok) {
      await fetchSessions();
    }
  } catch (err) {
    console.error(`Failed to set session ${sessionId} later state:`, err);
  }
}


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

export {
  fetchProviders,
  fetchVersion,
  fetchHosts,
  renderHosts,
  fetchTreeNodes,
  lastReconnectAttempt,
  fetchSessions,
  fetchSettings,
  updateSettings,
  setSessionDoneState,
  setSessionLaterState,
  deduplicateSessions,
  isRawSessionName,
  moveSessionToGroup,
  handleReconnectHost,
  handleUpdateHost
};
