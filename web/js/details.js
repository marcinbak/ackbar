// Session Details, Documentation & Transcript Inspector Pane
import { state, el } from './state.js';
import {
  escapeHtml,
  renderMarkdown,
  formatBytes,
  formatRelativeTime,
  formatFullDateTime,
  formatHostLabel,
  getStateEmoji,
  getStateText,
  getAgentBadgeHtml,
  getSessionBaseUrl,
  getSelfHostName,
  isLocalHost,
  openInVSCode
} from './utils.js';
import { fetchSessions, updateSettings } from './api.js';
import { openHandoverModal, showProjectDocsModal } from './modals.js';
import {
  closeTab,
  openSessionInTab
} from './tabs.js';
import { sendTerminalResize } from './terminal.js';
import { renderTree } from './tree.js';
import {
  attachChatMessageListeners,
  linkifyChatFiles,
  attachCodeBlockCopyButtons
} from './chat.js';


function refitActiveTerminal() {
  const activeTab = state.activeTabId ? state.openTabs.get(state.activeTabId) : null;
  if (!activeTab) return;
  setTimeout(() => {
    if (activeTab.fitAddon && typeof activeTab.fitAddon.fit === 'function') {
      try {
        activeTab.fitAddon.fit();
        if (activeTab.terminal && activeTab.socket && activeTab.socket.readyState === WebSocket.OPEN) {
          sendTerminalResize(activeTab.socket, activeTab.terminal.cols, activeTab.terminal.rows);
        }
      } catch (_) {}
    }
  }, 20);
}

function openInspectorPane({ titleHtml, subtitleHtml = '', actionsEl = null, bodyEl, meta = null }) {
  if (el.inspectorHeaderLeft) {
    const plainSub = subtitleHtml ? subtitleHtml.replace(/<[^>]*>/g, '') : '';
    el.inspectorHeaderLeft.innerHTML = `
      <div class="inspector-title" id="inspectorTitle">${titleHtml || 'Inspector'}</div>
      ${subtitleHtml ? `<div class="inspector-subtitle" title="${escapeHtml(plainSub)}">${subtitleHtml}</div>` : ''}
    `;
  }

  if (el.inspectorActions) {
    el.inspectorActions.innerHTML = '';
    if (actionsEl) {
      el.inspectorActions.appendChild(actionsEl);
    }
  }

  if (el.inspectorBody) {
    el.inspectorBody.innerHTML = '';
    if (bodyEl) {
      el.inspectorBody.appendChild(bodyEl);
    }
  }

  if (el.inspectorResizer) {
    el.inspectorResizer.style.display = 'block';
  }

  if (el.inspectorPanel) {
    try {
      const savedWidth = localStorage.getItem('ackbar_inspector_width');
      if (savedWidth) {
        const parsed = parseInt(savedWidth, 10);
        if (!isNaN(parsed) && parsed >= 300) {
          el.inspectorPanel.style.width = `${parsed}px`;
        }
      }
    } catch (_) {}
    el.inspectorPanel.style.display = 'flex';
  }

  state.inspectorState = meta;
  refitActiveTerminal();
}

function closeInspectorPane() {
  if (el.inspectorResizer) {
    el.inspectorResizer.style.display = 'none';
  }
  if (el.inspectorPanel) {
    el.inspectorPanel.style.display = 'none';
  }
  if (el.inspectorBody) {
    el.inspectorBody.innerHTML = '';
  }
  if (el.inspectorActions) {
    el.inspectorActions.innerHTML = '';
  }
  state.inspectorState = null;
  refitActiveTerminal();
}


function openSessionDetailsTab(session) {
  if (!session) return;

  const titleHtml = `ℹ ${escapeHtml(session.name || session.agent)}`;
  const subtitleHtml = `@${escapeHtml(formatHostLabel(session.host || 'local'))}${session.cwd ? ` • ${escapeHtml(session.cwd)}` : ''}`;

  const detailsContainer = document.createElement('div');
  detailsContainer.className = 'session-details-view session-details-container';
  detailsContainer.id = `detailsView_${session.id.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
  detailsContainer.innerHTML = `
    <div class="details-header">
      <div class="details-title-area">
        <div class="details-title-row">
          <span class="details-title">${escapeHtml(session.name || session.agent)}</span>
          ${getAgentBadgeHtml(session.agent)}
          <span class="host-badge">@${escapeHtml(formatHostLabel(session.host || 'local'))}</span>
          ${session.git_branch ? `<span class="badge-branch">⎇ ${escapeHtml(session.git_branch)}</span>` : ''}
          <span class="host-badge">${getStateEmoji(session)} ${session.managed ? 'Managed' : 'Observed'}</span>
        </div>
        <span class="details-subtitle">${escapeHtml(session.id)}</span>
      </div>
      <div class="details-actions">
        <button class="btn btn-primary btn-sm" id="detBtnResume">▶ Attach / Open Terminal</button>
        <button class="btn btn-secondary btn-sm" id="detBtnTranscript">📜 Transcript</button>
        <button class="btn btn-secondary btn-sm" id="detBtnHandover">🔄 Handover</button>
        <button class="btn btn-secondary btn-sm" id="detBtnShell">🐚 Shell</button>
        <button class="btn btn-secondary btn-sm" id="detBtnDocs">📄 View Project Docs</button>
        <button class="btn btn-secondary btn-sm" id="detBtnVSCode">📂 Open in VS Code</button>
        <button class="btn btn-secondary btn-sm" id="detBtnRestart">↻ Restart</button>
        <button class="btn btn-secondary btn-sm" id="detBtnArchive">📦 ${session.archived ? 'Unarchive' : 'Archive'}</button>
        <button class="btn btn-secondary btn-sm danger" id="detBtnKill">⛔ Kill Process</button>
        <button class="btn btn-secondary btn-sm danger" id="detBtnDelete">🗑 Delete Session</button>
      </div>
    </div>

    <div class="details-grid">
      <div class="details-card">
        <span class="details-card-label">Directory (CWD)</span>
        <span class="details-card-value">${escapeHtml(session.cwd || '—')}</span>
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
        <span class="details-card-value" style="color: var(--accent-cyan);">${escapeHtml(session.custom_title)}</span>
      </div>` : ''}
      ${session.ai_title ? `
      <div class="details-card">
        <span class="details-card-label">AI Generated Title</span>
        <span class="details-card-value" style="color: var(--accent-orange);">${escapeHtml(session.ai_title)}</span>
      </div>` : ''}
    </div>

    ${session.ai_description ? `
    <div class="details-section-title">AI Task Description & Summary</div>
    <div class="details-code-box" style="color: var(--text-main); font-size: 13px;">
      ${escapeHtml(session.ai_description)}
    </div>` : ''}

    ${session.first_prompt ? `
    <div class="details-section-title">First User Prompt (Initiating Task)</div>
    <div class="details-code-box" style="white-space: pre-wrap; font-family: var(--font-mono); color: #93c5fd;">
${escapeHtml(session.first_prompt)}
    </div>` : ''}

    ${session.last_prompt && session.last_prompt !== session.first_prompt ? `
    <div class="details-section-title">Latest User Prompt</div>
    <div class="details-code-box" style="white-space: pre-wrap; font-family: var(--font-mono); color: #86efac;">
${escapeHtml(session.last_prompt)}
    </div>` : ''}

    <div class="details-section-title">Recorded Activity</div>
    <div class="details-code-box">
      ${escapeHtml(session.activity || 'No recorded live activity. Session is stored from historical transcripts.')}
    </div>
  `;

  detailsContainer.querySelector('#detBtnResume').addEventListener('click', async () => {
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

  detailsContainer.querySelector('#detBtnTranscript').addEventListener('click', () => {
    openTranscriptViewerTab(session);
  });

  detailsContainer.querySelector('#detBtnHandover').addEventListener('click', () => {
    openHandoverModal(session.id);
  });

  detailsContainer.querySelector('#detBtnShell').addEventListener('click', () => {
    openSessionInTab({
      ...session,
      id: `shell_${session.id}`,
      name: `Shell (${session.name || 'Terminal'})`,
      managed: true
    });
  });

  detailsContainer.querySelector('#detBtnDocs').addEventListener('click', () => {
    showProjectDocsModal(session.cwd, session.name, session.host, session);
  });

  detailsContainer.querySelector('#detBtnVSCode').addEventListener('click', async () => {
    openInVSCode(session.cwd, session.host);
  });

  detailsContainer.querySelector('#detBtnRestart').addEventListener('click', async () => {
    const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
    await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(session.id)}&action=restart`, { method: 'POST' });
    openSessionInTab(session);
    await fetchSessions();
  });

  const archiveBtn = detailsContainer.querySelector('#detBtnArchive');
  if (archiveBtn) {
    archiveBtn.addEventListener('click', async () => {
      const targetAction = session.archived ? 'unarchive' : 'archive';
      session.archived = !session.archived;
      archiveBtn.textContent = `📦 ${session.archived ? 'Unarchive' : 'Archive'}`;
      renderTree();
      try {
        const hostRec = (state.hosts || []).find(h => h.name === session.host);
        const baseUrl = (session && session.hostUrl) ? session.hostUrl.replace(/\/$/, '') : (hostRec && hostRec.url && !isLocalHost(session.host) ? hostRec.url.replace(/\/$/, '') : '');
        const res = await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(session.id)}&action=${targetAction}`, { method: 'POST' });
        if (res.ok) {
          await fetchSessions();
        }
      } catch (err) {
        console.error(`Failed to ${targetAction} session:`, err);
      }
    });
  }

  detailsContainer.querySelector('#detBtnKill').addEventListener('click', async () => {
    if (confirm(`Terminate process for "${session.name}"?`)) {
      const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
      await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(session.id)}&action=kill`, { method: 'POST' });
      await fetchSessions();
    }
  });

  detailsContainer.querySelector('#detBtnDelete').addEventListener('click', async () => {
    const msg = `⚠️ Permanently Delete Session "${session.name}"?\n\nThis will permanently delete the session and its transcript logs from the agent on disk (~/.claude/projects/) without the possibility for recovery.\n\nUse Archive (📦) if you only want to hide it from your active list.`;
    if (confirm(msg)) {
      const targetId = session.id;
      const targetName = session.name;
      const nativeId = session.native_id;
      state.sessions = state.sessions.filter(s => s.id !== targetId && s.name !== targetName && (!nativeId || s.native_id !== nativeId));
      closeTab(targetId);
      if (nativeId) closeTab(nativeId);
      for (const [tId, tabObj] of state.openTabs.entries()) {
        if (tabObj.session && (tabObj.session.id === targetId || (nativeId && tabObj.session.native_id === nativeId))) {
          closeTab(tId);
        }
      }
      closeInspectorPane();
      renderTree();
      const baseUrl = session.hostUrl ? session.hostUrl.replace(/\/$/, '') : '';
      await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(targetId)}&action=delete`, { method: 'POST' });
      await fetchSessions();
    }
  });

  openInspectorPane({
    titleHtml,
    subtitleHtml,
    bodyEl: detailsContainer,
    meta: {
      type: 'details',
      sessionId: session.id,
      host: session.host || 'local'
    }
  });
}

// Open In-UI Markdown Document Viewer in Right Inspector Pane
async function openDocViewerTab(docPath, docTitle, host = 'local') {
  if (!docPath) return;

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

  const displayTitle = docTitle || docPath.split('/').pop();
  const titleHtml = `📄 ${escapeHtml(displayTitle)} ${isRemote ? `<span class="badge-host">@${escapeHtml(formatHostLabel(host))}</span>` : ''}`;
  const subtitleHtml = escapeHtml(docPath);

  const actionsEl = document.createElement('div');
  actionsEl.className = 'inspector-header-actions';
  const vsCodeBtn = document.createElement('button');
  vsCodeBtn.className = 'btn btn-secondary btn-sm';
  vsCodeBtn.id = 'docBtnVSCode';
  vsCodeBtn.textContent = '📂 VS Code';
  vsCodeBtn.title = 'Open in VS Code';
  vsCodeBtn.addEventListener('click', async () => {
    openInVSCode(docPath, host);
  });
  actionsEl.appendChild(vsCodeBtn);

  const docContainer = document.createElement('div');
  docContainer.className = 'doc-viewer-container';

  const renderedHtml = renderMarkdown(markdownContent);
  docContainer.innerHTML = `
    <div class="markdown-body">
      ${renderedHtml}
    </div>
  `;

  docContainer.querySelectorAll('.markdown-body a').forEach(link => {
    const href = link.getAttribute('href') || '';
    if (/^https?:\/\//i.test(href) || href.startsWith('//')) {
      link.setAttribute('target', '_blank');
      link.setAttribute('rel', 'noopener noreferrer');
    }
  });
  attachCodeBlockCopyButtons(docContainer);

  openInspectorPane({
    titleHtml,
    subtitleHtml,
    actionsEl,
    bodyEl: docContainer,
    meta: {
      type: 'doc',
      docPath,
      docTitle: displayTitle,
      host
    }
  });
}

// Open In-UI Rich Transcript Viewer in Right Inspector Pane
async function openTranscriptViewerTab(session) {
  if (!session) return;

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

  const transContainer = document.createElement('div');
  transContainer.className = 'transcript-viewer-container';

  let messagesHtml = '';
  if (transcriptData && transcriptData.messages && transcriptData.messages.length > 0) {
    messagesHtml = transcriptData.messages.map((m) => {
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
    <div class="transcript-body">
      ${messagesHtml}
    </div>
  `;

  transContainer.querySelectorAll('.transcript-msg').forEach(msgEl => {
    attachChatMessageListeners(msgEl);
    linkifyChatFiles(msgEl, session);
  });
  attachCodeBlockCopyButtons(transContainer);

  const actionsEl = document.createElement('div');
  actionsEl.className = 'inspector-header-actions';

  const btnResume = document.createElement('button');
  btnResume.className = 'btn btn-primary btn-sm';
  btnResume.id = 'transBtnResume';
  btnResume.textContent = '▶ Terminal';
  btnResume.title = 'Attach in Terminal';
  btnResume.addEventListener('click', () => {
    openSessionInTab(session);
  });

  const btnDocs = document.createElement('button');
  btnDocs.className = 'btn btn-secondary btn-sm';
  btnDocs.id = 'transBtnDocs';
  btnDocs.textContent = '📄 Docs';
  btnDocs.title = 'View Project Docs';
  btnDocs.addEventListener('click', () => {
    showProjectDocsModal(session.cwd, session.name, session.host, session);
  });

  const btnVSCode = document.createElement('button');
  btnVSCode.className = 'btn btn-secondary btn-sm';
  btnVSCode.id = 'transBtnVSCode';
  btnVSCode.textContent = '📂 VS Code';
  btnVSCode.title = 'Open in VS Code';
  btnVSCode.addEventListener('click', async () => {
    openInVSCode(session.cwd, session.host);
  });

  actionsEl.appendChild(btnResume);
  actionsEl.appendChild(btnDocs);
  actionsEl.appendChild(btnVSCode);

  const stepCount = transcriptData && transcriptData.messages ? transcriptData.messages.length : 0;
  const titleHtml = `📜 ${escapeHtml(session.name || session.agent)} ${getAgentBadgeHtml(session.agent)}`;
  const subtitleHtml = `${escapeHtml(session.cwd || '')} • ${stepCount} steps`;

  openInspectorPane({
    titleHtml,
    subtitleHtml,
    actionsEl,
    bodyEl: transContainer,
    meta: {
      type: 'transcript',
      sessionId: session.id,
      host
    }
  });
}


export {
  refitActiveTerminal,
  openInspectorPane,
  closeInspectorPane,
  openSessionDetailsTab,
  openDocViewerTab,
  openTranscriptViewerTab
};
