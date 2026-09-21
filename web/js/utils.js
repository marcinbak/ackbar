// Markdown, Formatting & Host Utilities
import { state, el } from './state.js';
import { sortSessionsByInteraction } from './tree.js';
import { showUploadToast } from './terminal.js';

let markedConfigured = false;
function configureMarked() {
  if (!window.marked || markedConfigured) return;

  const customRenderer = {
    link(href, title, text) {
      let linkHref = href;
      let linkTitle = title;
      let linkText = text;
      if (typeof href === 'object' && href !== null) {
        linkHref = href.href;
        linkTitle = href.title;
        linkText = href.text;
      }
      linkHref = linkHref || '';
      linkText = linkText || linkHref;

      const isAnchor = linkHref.startsWith('#');
      const isFile = linkHref.startsWith('file://');
      const targetAttr = (!isAnchor && !isFile) ? ' target="_blank" rel="noopener noreferrer"' : '';
      const titleAttr = linkTitle ? ` title="${escapeHtml(linkTitle)}"` : '';

      return `<a href="${escapeHtml(linkHref)}"${titleAttr}${targetAttr}>${linkText}</a>`;
    }
  };

  if (typeof window.marked.use === 'function') {
    window.marked.use({
      breaks: true,
      gfm: true,
      renderer: customRenderer
    });
    markedConfigured = true;
  } else if (typeof window.marked.setOptions === 'function') {
    const renderer = window.marked.Renderer ? new window.marked.Renderer() : {};
    renderer.link = customRenderer.link;
    window.marked.setOptions({
      breaks: true,
      gfm: true,
      renderer: renderer
    });
    markedConfigured = true;
  }
}
configureMarked();

function ensureExternalLinksTargetBlank(html) {
  if (!html || typeof html !== 'string') return html || '';
  return html.replace(/<a\b([^>]*)>/gi, (match, attrs) => {
    const hrefMatch = attrs.match(/href\s*=\s*["']([^"']*)["']/i);
    const href = hrefMatch ? hrefMatch[1] : '';
    if (!href || href.startsWith('#') || href.startsWith('file://') || href.startsWith('javascript:')) {
      return match;
    }
    let newAttrs = attrs;
    if (/target\s*=/i.test(newAttrs)) {
      newAttrs = newAttrs.replace(/target\s*=\s*["'][^"']*["']/i, 'target="_blank"');
    } else {
      newAttrs += ' target="_blank"';
    }
    if (/rel\s*=/i.test(newAttrs)) {
      newAttrs = newAttrs.replace(/rel\s*=\s*["'][^"']*["']/i, 'rel="noopener noreferrer"');
    } else {
      newAttrs += ' rel="noopener noreferrer"';
    }
    return `<a${newAttrs}>`;
  });
}

function renderMarkdown(content) {
  if (!window.marked) {
    return `<pre>${escapeHtml(content || '')}</pre>`;
  }
  configureMarked();
  const raw = window.marked.parse(content || '');
  return ensureExternalLinksTargetBlank(raw);
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
    } else if (res.status === 404) {
      const errText = await res.text().catch(() => 'File or directory not found');
      showUploadToast(`Cannot open in VS Code: ${errText}`, 'error');
    } else {
      window.location.href = directUri;
    }
  } catch (err) {
    window.location.href = directUri;
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

function formatAgentChatName(agent) {
  const a = (agent || 'claude-code').toLowerCase();
  if (a.includes('claude')) return 'Claude Code';
  if (a.includes('antigravity') || a.includes('agy') || a.includes('gemini')) return 'Google Antigravity';
  if (a.includes('codex') || a.includes('openai')) return 'OpenAI Codex';
  return agent || 'Claude Code';
}

// DOM Elements

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

export {
  markedConfigured,
  openInVSCode,
  configureMarked,
  ensureExternalLinksTargetBlank,
  renderMarkdown,
  ensureDoubleNewlineSeparation,
  isLocalHost,
  findHostRecord,
  getSessionBaseUrl,
  getSelfHostName,
  getSelfDisplayName,
  formatHostLabel,
  formatRelativeTime,
  formatFullDateTime,
  getStateText,
  getStateRawEmoji,
  getStateEmoji,
  getAgentBadgeHtml,
  formatAgentChatName,
  escapeHtml,
  formatBytes,
  translatePathForHost,
  GROUP_PREFERENCES_KEY,
  getStoredGroupPreferences,
  saveStoredGroupPreferences,
  recordGroupSpawn,
  getGroupPreferences,
  getFoldersForHost
};
