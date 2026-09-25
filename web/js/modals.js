// Modal Dialogs (New Session, Hosts, Groups, Settings, Handover)
import { state, el, saveCollapsedGroups } from './state.js';
import {
  escapeHtml,
  formatBytes,
  formatHostLabel,
  isLocalHost,
  translatePathForHost,
  getFoldersForHost,
  getGroupPreferences,
  recordGroupSpawn,
  renderMarkdown,
  ensureExternalLinksTargetBlank,
  getSelfHostName,
  getSelfDisplayName
} from './utils.js';
import {
  fetchHosts,
  fetchSessions,
  fetchTreeNodes,
  fetchSettings,
  updateSettings,
  handleReconnectHost,
  handleUpdateHost,
  lastReconnectAttempt,
  moveSessionToGroup
} from './api.js';
import { renderTree } from './tree.js';
import { openSessionInTab, setTabViewMode } from './tabs.js';
import { openDocViewerTab } from './details.js';


function showSettingsModal() {
  const s = state.settings || {
    auto_done_enabled: 'true',
    auto_done_hours: '24',
    auto_archive_enabled: 'true',
    auto_archive_days: '7',
    done_collapsed_by_default: 'true',
    later_collapsed_by_default: 'false'
  };

  const autoDoneEnabled = s.auto_done_enabled !== 'false';
  const autoDoneHours = s.auto_done_hours || '24';
  const autoArchiveEnabled = s.auto_archive_enabled !== 'false';
  const autoArchiveDays = s.auto_archive_days || '7';
  const doneCollapsed = s.done_collapsed_by_default !== 'false';
  const laterCollapsed = s.later_collapsed_by_default === 'true';
  const handoverSuggestionEnabled = s.handover_suggestion_enabled !== 'false';
  const handoverThresholdPct = s.handover_threshold_pct || '60';

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
        <div class="settings-section-title"><span>⚡</span> TypeSafe AI (Jev) Integration</div>
        <div class="settings-section-desc">Configure your TypeSafe API Key for ultra-fast (70–200ms) System-1 dispatch routing and decision making. If omitted, Ackbar automatically uses local project heuristics.</div>
        <div class="settings-row">
          <label class="settings-row-label" for="settingTypesafeApiKey">TypeSafe API Key</label>
          <div class="settings-input-group" style="flex: 1; max-width: 320px;">
            <input type="password" id="settingTypesafeApiKey" class="form-input" value="${s.typesafe_api_key || ''}" placeholder="Enter API key..." autocomplete="off" />
          </div>
        </div>
      </div>

      <div class="settings-section">
        <div class="settings-section-title"><span>🔄</span> Context Handover & Rotation</div>
        <div class="settings-section-desc">Configure automated context rotation suggestions when agent token usage reaches high thresholds.</div>
        <div class="settings-row">
          <label class="settings-row-label" for="settingHandoverSuggestionEnabled">Enable Handover Suggestions</label>
          <input type="checkbox" id="settingHandoverSuggestionEnabled" ${handoverSuggestionEnabled ? 'checked' : ''} />
        </div>
        <div class="settings-row">
          <label class="settings-row-label" for="settingHandoverThresholdPct">Suggestion Threshold</label>
          <div class="settings-input-group">
            <input type="number" id="settingHandoverThresholdPct" class="settings-number-input" min="10" max="95" value="${handoverThresholdPct}" />
            <span style="font-size: 12px; color: var(--text-muted);">% context</span>
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
        <div class="settings-section-title"><span>⏳</span> Later Section Presentation</div>
        <div class="settings-section-desc">Configure the initial display state of the Later section in the session sidebar tree.</div>
        <div class="settings-row">
          <label class="settings-row-label" for="settingLaterCollapsed">Start Later Sections Collapsed</label>
          <input type="checkbox" id="settingLaterCollapsed" ${laterCollapsed ? 'checked' : ''} />
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
        done_collapsed_by_default: document.getElementById('settingDoneCollapsed')?.checked ? 'true' : 'false',
        later_collapsed_by_default: document.getElementById('settingLaterCollapsed')?.checked ? 'true' : 'false',
        typesafe_api_key: document.getElementById('settingTypesafeApiKey')?.value?.trim() || '',
        handover_suggestion_enabled: document.getElementById('settingHandoverSuggestionEnabled')?.checked ? 'true' : 'false',
        handover_threshold_pct: document.getElementById('settingHandoverThresholdPct')?.value?.trim() || '60'
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
        name: agent,
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

// Handover Modal Dialog

function openHandoverModal(sessionId) {
  const session = state.sessions.find(s => s.id === sessionId || s.native_id === sessionId);
  if (!session) return;

  if (session.state === 'working') {
    alert('Cannot perform handover while session is actively running. Please wait for the current turn to complete or cancel it first.');
    return;
  }

  if (!el.handoverModalOverlay) return;

  state.handoverTargetSessionId = session.id;

  if (el.handoverModalTitle) {
    el.handoverModalTitle.innerHTML = `<span>🔄</span> Context Handover — ${escapeHtml(session.name || session.agent)}`;
  }

  if (el.handoverModalAlert) {
    const pct = session.context_pct || 0;
    el.handoverModalAlert.innerHTML = `Context window is at <strong>${pct}%</strong>. Rotating will generate an automated handover briefing, execute context reset, and seed the fresh turn with full task continuity.`;
  }

  if (el.handoverStrategySelect) {
    el.handoverStrategySelect.value = 'in_place';
  }

  if (el.handoverCustomInstruction) {
    el.handoverCustomInstruction.value = '';
  }

  if (el.btnHandoverConfirm) {
    el.btnHandoverConfirm.disabled = false;
    el.btnHandoverConfirm.textContent = 'Confirm Handover';
  }

  el.handoverModalOverlay.style.display = 'flex';
}

function closeHandoverModal() {
  if (el.handoverModalOverlay) {
    el.handoverModalOverlay.style.display = 'none';
  }
  state.handoverTargetSessionId = null;
}

async function submitHandover() {
  if (!state.handoverTargetSessionId) return;
  const sessionId = state.handoverTargetSessionId;
  const session = state.sessions.find(s => s.id === sessionId || s.native_id === sessionId);
  if (!session) return;

  const strategy = el.handoverStrategySelect ? el.handoverStrategySelect.value : 'in_place';
  const customInstruction = el.handoverCustomInstruction ? el.handoverCustomInstruction.value.trim() : '';

  if (el.btnHandoverConfirm) {
    el.btnHandoverConfirm.disabled = true;
    el.btnHandoverConfirm.textContent = 'Initiating...';
  }

  try {
    const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
    const res = await fetch(`${baseUrl}/v1/sessions/handover`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: session.id,
        strategy: strategy,
        custom_instruction: customInstruction
      })
    });

    if (!res.ok) {
      const errText = await res.text();
      throw new Error(errText || `HTTP ${res.status}`);
    }

    closeHandoverModal();
    await fetchSessions();
  } catch (err) {
    alert(`Handover failed: ${err.message}`);
    if (el.btnHandoverConfirm) {
      el.btnHandoverConfirm.disabled = false;
      el.btnHandoverConfirm.textContent = 'Confirm Handover';
    }
  }
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

export {
  showModal,
  hideModal,
  showProjectDocsModal,
  showSettingsModal,
  showNewSessionModal,
  hideNewSessionModal,
  handleSpawnNewSession,
  openHandoverModal,
  closeHandoverModal,
  submitHandover,
  showNewGroupModal,
  showAddHostModal,
  showHooksDashboardModal,
  showEditHostModal,
  showHostSummaryModal
};
