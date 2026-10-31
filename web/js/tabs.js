// Tab Strip Lifecycle, Persistence & Multiplexing
import { state, el } from './state.js';
import {
  escapeHtml,
  getStateEmoji,
  getAgentBadgeHtml,
  formatFullDateTime,
  formatRelativeTime,
  formatHostLabel,
  getSessionBaseUrl,
  getSelfHostName
} from './utils.js';
import {
  connectTerminalWebSocket,
  sendTerminalResize,
  reconnectTerminalTab,
  markSessionAsRead,
  showUploadToast,
  uploadAndAttachFile
} from './terminal.js';
import {
  setupChatInterface,
  disconnectChatStream,
  connectChatStream,
  loadChatTranscript,
  hideInStreamActivity,
  resetChatComposer,
  setChatSending,
  isSessionWorking,
  onSessionUpdated
} from './chat.js';
import { openSessionDetailsTab } from './details.js';
import { updateStatusbar, resetStatusbar } from './statusbar.js';
import { showTabContextMenu } from './context-menu.js';
import { fetchSessions } from './api.js';
import { toggleCommandPalette } from './palette.js';
import { renderTree } from './tree.js';


function isAuxiliaryInputFocused(chatInputEl) {
  const workspaceView = el.workspaceView || document.getElementById('workspaceView');
  if (workspaceView && workspaceView.style.display === 'none') return true;
  const workBoardView = el.workBoardView || document.getElementById('workBoardView');
  if (workBoardView && workBoardView.style.display === 'flex') return true;
  if (el.cmdPaletteOverlay && el.cmdPaletteOverlay.style.display === 'flex') return true;
  if (el.modalOverlay && el.modalOverlay.style.display === 'flex') return true;
  const modalNewSession = document.getElementById('modalNewSession');
  if (modalNewSession && modalNewSession.style.display === 'flex') return true;
  const handoverModal = el.handoverModalOverlay || document.getElementById('handoverModalOverlay');
  if (handoverModal && handoverModal.style.display === 'flex') return true;
  const agentSetupModal = document.getElementById('agentSetupModalOverlay');
  if (agentSetupModal && agentSetupModal.style.display === 'flex') return true;

  const active = document.activeElement;
  if (!active || active === document.body || active === document.documentElement || active === chatInputEl) {
    return false;
  }
  if (active.classList && (active.classList.contains('xterm-helper-textarea') || active.classList.contains('chat-composer-textarea'))) {
    return false;
  }
  const tag = (active.tagName || '').toUpperCase();
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || active.isContentEditable) {
    return true;
  }
  return false;
}

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
        }
        // Repaint the local xterm viewport (it may have been laid out while
        // hidden). Never send Ctrl+L to the PTY for this: it is a keystroke to
        // the agent, which makes it clear and redraw (or replay) its screen.
        tab.terminal.refresh(0, tab.terminal.rows - 1);
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
      setTimeout(() => {
        if (tab.viewMode === 'chat' && tab.chatInputEl && !isAuxiliaryInputFocused(tab.chatInputEl)) {
          tab.chatInputEl.focus();
        }
      }, 30);
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

// Helper: Check if two session objects represent the same underlying terminal session
function isSameUnderlyingSession(sessA, sessB) {
  if (!sessA || !sessB) return false;
  if (sessA.id && sessB.id && sessA.id === sessB.id) return true;
  if (sessA.old_id && (sessA.old_id === sessB.id || (sessB.old_id && sessA.old_id === sessB.old_id))) return true;
  if (sessB.old_id && sessB.old_id === sessA.id) return true;

  const hostA = sessA.host || 'local';
  const hostB = sessB.host || 'local';
  if (hostA === hostB) {
    if (sessA.tmux_name && sessB.tmux_name && sessA.tmux_name === sessB.tmux_name) {
      return true;
    }
    const agent = sessA.agent || sessB.agent;
    if (agent) {
      if (sessA.tmux_name && sessB.native_id && sessA.tmux_name === `ackbar-${agent}-${sessB.native_id}`) {
        return true;
      }
      if (sessB.tmux_name && sessA.native_id && sessB.tmux_name === `ackbar-${agent}-${sessA.native_id}`) {
        return true;
      }
    }
  }
  return false;
}

// Helper: Cleanly tear down a tab object's connections, observers, terminal, and DOM elements
function destroyTabResources(tab) {
  if (!tab) return;
  if (tab.chatReconnectTimer) {
    clearTimeout(tab.chatReconnectTimer);
    tab.chatReconnectTimer = null;
  }
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
    tab.resizeObserver = null;
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
  if (tab.terminal) {
    try { tab.terminal.dispose(); } catch (e) {}
    tab.terminal = null;
  }
  if (tab.tabEl && tab.tabEl.parentNode) {
    tab.tabEl.parentNode.removeChild(tab.tabEl);
  }
  if (tab.containerEl && tab.containerEl.parentNode) {
    tab.containerEl.parentNode.removeChild(tab.containerEl);
  }
}

// Setup rich Chat Interface inside tab

function openSessionInTab(session) {
  if (!session) return;
  const tabId = session.id;

  if (!state.openTabs.has(tabId)) {
    for (const [existingId, existingTab] of state.openTabs.entries()) {
      if (existingTab && existingTab.type === 'terminal' && existingTab.session && isSameUnderlyingSession(session, existingTab.session)) {
        migrateTabId(existingId, tabId, session);
        break;
      }
    }
  }

  if (state.openTabs.has(tabId)) {
    const existingTab = state.openTabs.get(tabId);
    if (existingTab && existingTab.type === 'terminal') {
      existingTab.session = session;
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
    closeTab(tabEl.dataset.tabId || tabId);
  });

  tabEl.appendChild(titleWrap);
  tabEl.appendChild(closeBtn);

  // Left click to activate tab
  tabEl.addEventListener('click', (e) => {
    if (e.button === 0) activateTab(tabEl.dataset.tabId || tabId);
  });

  // Middle mouse click to close tab
  tabEl.addEventListener('auxclick', (e) => {
    if (e.button === 1) {
      e.preventDefault();
      e.stopPropagation();
      closeTab(tabEl.dataset.tabId || tabId);
    }
  });

  // Right-click for Tab Context Menu
  tabEl.addEventListener('contextmenu', (e) => {
    e.preventDefault();
    e.stopPropagation();
    showTabContextMenu(e.clientX, e.clientY, tabEl.dataset.tabId || tabId);
  });

  tabEl.addEventListener('mousedown', (e) => {
    if (e.button === 1) e.preventDefault();
  });

  if (el.tabStrip) el.tabStrip.appendChild(tabEl);

  const getActiveTabId = () => tabEl.dataset.tabId || tabId;
  let tabObj = null;
  const getCurrentTab = () => (state.openTabs ? state.openTabs.get(getActiveTabId()) : null) || tabObj;

  // 2. Create Terminal / Chat Container
  const initialViewMode = session.engine_type === 'headless' ? 'chat' : 'terminal';
  const containerEl = document.createElement('div');
  containerEl.className = `terminal-tab-view has-session-container view-mode-${initialViewMode}`;
  containerEl.id = `termView_${tabId.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
  containerEl.addEventListener('click', () => {
    const currentTab = getCurrentTab();
    if (currentTab && (!currentTab.socket || currentTab.socket.readyState === WebSocket.CLOSED || currentTab.socket.readyState === WebSocket.CLOSING)) {
      reconnectTerminalTab(getActiveTabId());
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
      takeWheelBtn.addEventListener('click', () => handleTakeWheel(getActiveTabId()));
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
    const currentTab = getCurrentTab();
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
    const currentTab = getCurrentTab();
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
        closeTab(getActiveTabId());
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
        const currentTab = getCurrentTab();
        if (currentTab && currentTab.socket && currentTab.socket.readyState === WebSocket.OPEN) {
          currentTab.socket.send(new Uint8Array([0x1b, 0x5b, 0x5a]));
        }
      }
      return false;
    }
    return true;
  });

  // Save in State
  tabObj = {
    id: tabId,
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
    const currentTab = getCurrentTab();
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
      reconnectTerminalTab(getActiveTabId());
    }
  });

  term.onResize((size) => {
    const currentTab = getCurrentTab();
    if (currentTab && currentTab.socket) {
      sendTerminalResize(currentTab.socket, size.cols, size.rows);
    }
  });

  activateTab(tabId);
  handleTabOverflow();
  savePersistedTabs();
}

// Open Rich Session Details Tab (For Observed & Ended Sessions)

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
        if (tab.viewMode === 'chat' && tab.chatInputEl) {
          if (!isAuxiliaryInputFocused(tab.chatInputEl)) {
            tab.chatInputEl.focus();
          }
        } else if (tab.terminal && tab.viewMode !== 'chat') {
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
      if (tab.session && tab.viewMode === 'chat') {
        const sessMatch = state.sessions.find(s => s.id === tab.session.id || (tab.session.native_id && s.native_id === tab.session.native_id));
        const currentSess = sessMatch || tab.session;
        const prevReqId = tab._transcriptReqId || 0;
        if (currentSess) {
          onSessionUpdated(currentSess);
        }
        if (isSessionWorking(currentSess) || (tab.sending && !currentSess)) {
          setChatSending(tab, true);
        }
        if ((tab._transcriptReqId || 0) === prevReqId && (Date.now() - (tab._lastTranscriptLoadAt || 0)) > 200) {
          loadChatTranscript(tab);
        }
        if (currentSess && currentSess.engine_type === 'headless') {
          connectChatStream(tab);
        }
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
  if (!tabId) return;
  const tab = state.openTabs.get(tabId);
  if (tab) {
    destroyTabResources(tab);
    state.openTabs.delete(tabId);
  }

  // Always clean up any orphaned DOM tab/container elements matching tabId
  if (el.tabStrip) {
    const matchingTabs = el.tabStrip.querySelectorAll('.terminal-tab');
    matchingTabs.forEach(node => {
      if (node.dataset.tabId === tabId) {
        node.remove();
      }
    });
  }
  if (el.terminalViewport && !state.openTabs.has(tabId)) {
    const rawId = tabId.startsWith('details_') ? tabId.slice('details_'.length) : tabId;
    const sanitized = rawId.replace(/[^a-zA-Z0-9_-]/g, '_');
    const viewPrefix = tabId.startsWith('details_') ? 'detailsView_' : 'termView_';
    const orphanView = document.getElementById(`${viewPrefix}${sanitized}`);
    if (orphanView && orphanView.parentNode === el.terminalViewport) {
      orphanView.remove();
    }
  }

  if (state.activeTabId === tabId || (state.activeTabId && !state.openTabs.has(state.activeTabId))) {
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

// Migrate tab ID smoothly when a placeholder session adopts its real native conversation ID
function migrateTabId(oldId, newId, session) {
  if (!oldId || !newId || oldId === newId) return;

  const tab = state.openTabs.get(oldId);
  if (tab) {
    const existingNewTab = state.openTabs.get(newId);
    if (existingNewTab && existingNewTab !== tab) {
      destroyTabResources(existingNewTab);
      state.openTabs.delete(newId);
    }

    state.openTabs.delete(oldId);
    tab.id = newId;
    tab.session = session;
    if (tab.tabEl) {
      tab.tabEl.dataset.tabId = newId;
      const titleSpan = tab.tabEl.querySelector('.tab-title');
      if (titleSpan) {
        titleSpan.textContent = session.name || session.agent;
        titleSpan.title = session.name || session.agent;
      }
    }
    if (tab.containerEl) {
      tab.containerEl.id = `termView_${newId.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    }
    state.openTabs.set(newId, tab);

    if (state.activeTabId === oldId || state.activeTabId === newId) {
      state.activeTabId = newId;
      if (tab.tabEl) tab.tabEl.classList.add('active');
      if (tab.containerEl) tab.containerEl.classList.add('active');
    }
    if (tab.chatEventSource) {
      connectChatStream(tab);
    }
  }

  // Deduplicate any orphaned DOM tab elements for oldId / newId
  if (el.tabStrip) {
    const canonicalTab = state.openTabs.get(newId);
    el.tabStrip.querySelectorAll('.terminal-tab').forEach(node => {
      if (node.dataset.tabId === oldId || (node.dataset.tabId === newId && canonicalTab && node !== canonicalTab.tabEl)) {
        node.remove();
      }
    });
  }

  const detailsOldId = `details_${oldId}`;
  const detailsNewId = `details_${newId}`;
  const detailsTab = state.openTabs.get(detailsOldId);
  if (detailsTab) {
    const existingDetailsNew = state.openTabs.get(detailsNewId);
    if (existingDetailsNew && existingDetailsNew !== detailsTab) {
      destroyTabResources(existingDetailsNew);
      state.openTabs.delete(detailsNewId);
    }

    state.openTabs.delete(detailsOldId);
    detailsTab.id = detailsNewId;
    detailsTab.session = session;
    if (detailsTab.tabEl) {
      detailsTab.tabEl.dataset.tabId = detailsNewId;
      const titleSpan = detailsTab.tabEl.querySelector('.tab-title');
      if (titleSpan) {
        titleSpan.textContent = session.name || session.agent;
        titleSpan.title = session.name || session.agent;
      }
    }
    if (detailsTab.containerEl) {
      detailsTab.containerEl.id = `detailsView_${newId.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
    }
    state.openTabs.set(detailsNewId, detailsTab);
    if (state.activeTabId === detailsOldId || state.activeTabId === detailsNewId) {
      state.activeTabId = detailsNewId;
      if (detailsTab.tabEl) detailsTab.tabEl.classList.add('active');
      if (detailsTab.containerEl) detailsTab.containerEl.classList.add('active');
    }
  }

  savePersistedTabs();
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
      if (tab.viewMode === 'chat') {
        const sessMatch = state.sessions.find(s => s.id === (tab.session && tab.session.id) || (tab.session && tab.session.native_id && s.native_id === tab.session.native_id));
        const currentSess = sessMatch || tab.session;
        if (currentSess) {
          onSessionUpdated(currentSess);
        }
        if (state.activeTabId === tabId) {
          if (currentSess && currentSess.engine_type === 'headless') {
            connectChatStream(tab);
          }
          if (tab.chatInputEl && !isAuxiliaryInputFocused(tab.chatInputEl) && document.activeElement !== tab.chatInputEl) {
            tab.chatInputEl.focus();
          }
        }
      } else if (!tab.socket || tab.socket.readyState === WebSocket.CLOSED || tab.socket.readyState === WebSocket.CLOSING) {
        reconnectTerminalTab(tabId);
      }
    }
  });
}

window.addEventListener('focus', checkAndReconnectActiveTabs);
document.addEventListener('visibilitychange', async () => {
  if (document.visibilityState === 'visible') {
    await fetchSessions();
    checkAndReconnectActiveTabs();
  }
});
window.addEventListener('online', async () => {
  await fetchSessions();
  checkAndReconnectActiveTabs();
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

    const seenTabKeys = new Set();
    const seenTmuxKeys = new Set();

    list.forEach(item => {
      if (item.sessionId) {
        const sess = state.sessions.find(s => s.id === item.sessionId || s.old_id === item.sessionId);
        if (sess) {
          const tabKey = `${item.type || 'terminal'}:${sess.id}`;
          if (seenTabKeys.has(tabKey)) return;
          seenTabKeys.add(tabKey);

          if (item.type === 'terminal') {
            if (sess.tmux_name) {
              const tmuxKey = `${sess.host || 'local'}:${sess.tmux_name}`;
              if (seenTmuxKeys.has(tmuxKey)) return;
              seenTmuxKeys.add(tmuxKey);
            }
            openSessionInTab(sess);
          } else if (item.type === 'details') {
            openSessionDetailsTab(sess);
          }
        }
      }
    });

    const activeId = localStorage.getItem('ackbar_active_tab_id');
    if (activeId) {
      if (state.openTabs.has(activeId)) {
        activateTab(activeId);
      } else {
        const migratedSess = state.sessions.find(s => s.old_id === activeId);
        if (migratedSess && state.openTabs.has(migratedSess.id)) {
          activateTab(migratedSess.id);
        }
      }
    }
  } catch (e) {}
}

// Update Tab States (Emojis & Titles) when Session Events arrive
function updateOpenTabsState() {
  // First reconcile any open tabs whose underlying session ID migrated
  const pendingMigrations = [];
  state.openTabs.forEach((tab, id) => {
    if (tab && tab.type === 'terminal' && tab.session) {
      const exactMatch = state.sessions.find(s => s.id === id);
      if (!exactMatch) {
        const migratedSess = state.sessions.find(s => isSameUnderlyingSession(s, tab.session));
        if (migratedSess && migratedSess.id !== id) {
          pendingMigrations.push({ oldId: id, newId: migratedSess.id, sess: migratedSess });
        }
      }
    }
  });
  pendingMigrations.forEach(({ oldId, newId, sess }) => {
    migrateTabId(oldId, newId, sess);
  });

  // Remove any orphaned DOM tab nodes in #tabStrip that are no longer canonical in state.openTabs
  if (el.tabStrip) {
    el.tabStrip.querySelectorAll('.terminal-tab').forEach(node => {
      const tId = node.dataset.tabId;
      const canonical = tId ? state.openTabs.get(tId) : null;
      if (!canonical || canonical.tabEl !== node) {
        node.remove();
      }
    });
  }

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

export {
  isAuxiliaryInputFocused,
  setTabViewMode,
  updateViewModeButtons,
  handleTakeWheel,
  openSessionInTab,
  activateTab,
  closeTab,
  closeOtherTabs,
  closeTabsToTheRight,
  closeAllTabs,
  checkAndReconnectActiveTabs,
  savePersistedTabs,
  restorePersistedTabs,
  updateOpenTabsState,
  handleTabOverflow,
  renderOverflowMenu,
  migrateTabId
};
