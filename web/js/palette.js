// Command Palette & Dispatcher Launcher
import { state, el } from './state.js';
import { escapeHtml, getStateEmoji } from './utils.js';
import { openSessionInTab, activateTab } from './tabs.js';
import {
  showNewSessionModal,
  showAddHostModal,
  showSettingsModal,
  handleSpawnNewSession
} from './modals.js';

function toggleCommandPalette() {
  if (!el.cmdPaletteOverlay) return;
  const isVisible = el.cmdPaletteOverlay.style.display === 'flex';
  if (isVisible) {
    el.cmdPaletteOverlay.style.display = 'none';
    if (el.dispatchPreview) el.dispatchPreview.style.display = 'none';
    if (el.cmdPaletteResults) el.cmdPaletteResults.style.display = 'block';
  } else {
    el.cmdPaletteOverlay.style.display = 'flex';
    if (el.dispatchPreview) el.dispatchPreview.style.display = 'none';
    if (el.cmdPaletteResults) el.cmdPaletteResults.style.display = 'block';
    if (el.cmdPaletteInput) {
      el.cmdPaletteInput.value = '';
      el.cmdPaletteInput.focus();
    }
    renderCommandPaletteResults('');
  }
}

let activeDispatchResolution = null;

async function triggerDispatcherResolve(promptText) {
  if (!promptText || !promptText.trim()) return;

  if (!el.dispatchPreview) return;
  el.cmdPaletteResults.style.display = 'none';
  el.dispatchPreview.style.display = 'block';

  if (el.dispatchPreviewSource) {
    el.dispatchPreviewSource.textContent = '⚡ Resolving with Jev AI...';
  }
  if (el.btnDispatchLaunch) {
    el.btnDispatchLaunch.disabled = true;
    el.btnDispatchLaunch.textContent = '⏳ Resolving...';
  }

  try {
    const res = await fetch('/v1/meta/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt: promptText })
    });
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}`);
    }
    const data = await res.json();
    activeDispatchResolution = data;

    // 1. Source & Confidence
    if (el.dispatchPreviewSource) {
      if (data.source === 'jev') {
        const conf = Math.round((data.confidence || 0.95) * 100);
        el.dispatchPreviewSource.textContent = `via Jev AI (${conf}% confidence)`;
      } else {
        el.dispatchPreviewSource.textContent = 'via Local Heuristics';
      }
    }

    // 2. Populate Host Dropdown
    if (el.dispatchHost) {
      el.dispatchHost.innerHTML = '';
      const hosts = [{ name: 'local', label: 'local (macOS)' }];
      (state.hosts || []).forEach(h => {
        if (h.name && h.name !== 'local') {
          hosts.push({ name: h.name, label: `${h.name} (${h.url || 'remote'})` });
        }
      });
      hosts.forEach(h => {
        const opt = document.createElement('option');
        opt.value = h.name;
        opt.textContent = h.label;
        el.dispatchHost.appendChild(opt);
      });
      el.dispatchHost.value = data.host || 'local';
    }

    // 3. Populate Agent Dropdown
    if (el.dispatchAgent) {
      el.dispatchAgent.value = data.agent || 'claude-code';
    }

    // 4. Populate Group Dropdown
    if (el.dispatchGroup) {
      el.dispatchGroup.innerHTML = '';
      (state.treeNodes || []).forEach(n => {
        const opt = document.createElement('option');
        opt.value = n.path;
        opt.textContent = n.path;
        el.dispatchGroup.appendChild(opt);
      });
      if (data.node_path) {
        el.dispatchGroup.value = data.node_path;
      }
    }

    // 5. Populate Cwd & Prompt
    if (el.dispatchCwd) {
      el.dispatchCwd.value = data.cwd || '';
    }
    if (el.dispatchPrompt) {
      el.dispatchPrompt.value = data.prompt || promptText;
    }

    // 6. Matched Session Banner
    if (data.matched_session_id && el.dispatchMatchedBanner) {
      el.dispatchMatchedBanner.style.display = 'flex';
      if (el.dispatchMatchedTitle) {
        el.dispatchMatchedTitle.textContent = `Active Session: ${data.matched_session_title || data.matched_session_id}`;
      }
      if (el.dispatchMatchedSubtitle) {
        el.dispatchMatchedSubtitle.textContent = `An existing session is already working on this task.`;
      }
      if (el.btnDispatchSwitchActive) {
        el.btnDispatchSwitchActive.onclick = () => {
          el.cmdPaletteOverlay.style.display = 'none';
          const matchedSess = state.sessions.find(s => s.id === data.matched_session_id || s.native_id === data.matched_session_id);
          if (matchedSess) {
            openSessionInTab(matchedSess);
          }
        };
      }
    } else if (el.dispatchMatchedBanner) {
      el.dispatchMatchedBanner.style.display = 'none';
    }

    // 7. Enable launch button & focus
    if (el.btnDispatchLaunch) {
      el.btnDispatchLaunch.disabled = false;
      el.btnDispatchLaunch.textContent = '🚀 Launch Session (Enter)';
      el.btnDispatchLaunch.focus();
    }

  } catch (err) {
    console.error('Failed to resolve dispatcher prompt:', err);
    if (el.dispatchPreviewSource) {
      el.dispatchPreviewSource.textContent = 'Resolution fallback';
    }
    if (el.btnDispatchLaunch) {
      el.btnDispatchLaunch.disabled = false;
      el.btnDispatchLaunch.textContent = '🚀 Launch Session (Enter)';
      el.btnDispatchLaunch.focus();
    }
    if (el.dispatchPrompt) {
      el.dispatchPrompt.value = promptText;
    }
  }
}

async function executeDispatchLaunch() {
  if (!el.btnDispatchLaunch || el.btnDispatchLaunch.disabled) return;

  el.btnDispatchLaunch.disabled = true;
  el.btnDispatchLaunch.textContent = '🚀 Launching...';

  const host = el.dispatchHost ? el.dispatchHost.value : 'local';
  const agent = el.dispatchAgent ? el.dispatchAgent.value : 'claude-code';
  const node_path = el.dispatchGroup ? el.dispatchGroup.value : '';
  const cwd = el.dispatchCwd ? el.dispatchCwd.value : '';
  const prompt = el.dispatchPrompt ? el.dispatchPrompt.value : '';
  const name = activeDispatchResolution && activeDispatchResolution.name ? activeDispatchResolution.name : prompt;

  try {
    const res = await fetch('/v1/sessions/spawn', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host, agent, cwd, node_path, name, prompt })
    });
    if (!res.ok) {
      const errData = await res.json().catch(() => ({}));
      throw new Error(errData.message || errData.error || `HTTP ${res.status}`);
    }

    const spawnResult = await res.json();
    const spawnedNativeId = spawnResult.session_id || spawnResult.native_id || '';
    const spawnedSessId = spawnResult.id || `${agent}:${host}:${spawnedNativeId}`;

    el.cmdPaletteOverlay.style.display = 'none';

    // Refresh sessions
    await fetchSessions();

    const newSess = state.sessions.find(s => s.id === spawnedSessId || s.native_id === spawnedNativeId || (spawnedNativeId && s.id.includes(spawnedNativeId)));
    if (newSess) {
      openSessionInTab(newSess);
    } else {
      openTab(`session-${spawnedSessId}`, name, `
        <div class="terminal-placeholder">
          <div class="terminal-spinner"></div>
          <div>Attaching to spawned ${agent} session...</div>
        </div>
      `, true, { id: spawnedSessId, host, agent, native_id: spawnedNativeId, name });
    }
  } catch (err) {
    alert(`Failed to launch session: ${err.message}`);
    el.btnDispatchLaunch.disabled = false;
    el.btnDispatchLaunch.textContent = '🚀 Launch Session (Enter)';
  }
}

function renderCommandPaletteResults(query) {
  if (!el.cmdPaletteResults) return;
  el.cmdPaletteResults.innerHTML = '';
  state.cmdPaletteItems = [];

  const q = query.toLowerCase().trim();

  // 0. Primary Dispatcher Action if query is non-empty
  if (query.trim()) {
    state.cmdPaletteItems.push({
      title: `⚡ Dispatch Task: "${escapeHtml(query.trim())}"`,
      subtitle: '↵ Enter to Resolve with Jev AI',
      isDispatch: true,
      action: () => triggerDispatcherResolve(query.trim())
    });
  }

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
    itemEl.className = `cmd-item ${item.isDispatch ? 'dispatch-item' : ''} ${idx === 0 ? 'selected' : ''}`;
    itemEl.innerHTML = `
      <div class="cmd-item-left">${item.title}</div>
      <div class="cmd-item-right">${item.subtitle}</div>
    `;
    itemEl.addEventListener('click', () => {
      if (!item.isDispatch) {
        el.cmdPaletteOverlay.style.display = 'none';
      }
      item.action();
    });
    el.cmdPaletteResults.appendChild(itemEl);
  });
}

// Update Statusbar

export {
  toggleCommandPalette,
  triggerDispatcherResolve,
  executeDispatchLaunch,
  renderCommandPaletteResults
};
