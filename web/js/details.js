// Session Details, Documentation & Transcript Tabs
import { state, el } from './state.js';
import {
  escapeHtml,
  renderMarkdown,
  formatBytes,
  formatRelativeTime,
  formatFullDateTime,
  getStateEmoji,
  getStateText,
  getAgentBadgeHtml,
  getSessionBaseUrl,
  isLocalHost
} from './utils.js';
import { fetchSessions, updateSettings } from './api.js';
import { openHandoverModal } from './modals.js';
import {
  activateTab,
  closeTab,
  openSessionInTab
} from './tabs.js';

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


export {
  openSessionDetailsTab,
  openDocViewerTab,
  openTranscriptViewerTab
};
