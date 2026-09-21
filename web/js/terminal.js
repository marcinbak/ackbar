// xterm.js Terminal Tab & WebSocket PTY Connection
import { state, el } from './state.js';
import { getAuthToken } from './auth.js';
import {
  getSessionBaseUrl,
  escapeHtml,
  formatBytes,
  isLocalHost,
  getSelfHostName
} from './utils.js';
import { fetchSessions } from './api.js';
import { closeTab } from './tabs.js';
import { renderTree } from './tree.js';


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

function sendTerminalResize(socket, cols, rows) {
  if (socket && socket.readyState === WebSocket.OPEN && cols >= 10 && rows >= 4) {
    socket.send(JSON.stringify({ type: 'resize', cols, rows }));
  }
}

// Activate Tab

export {
  connectTerminalWebSocket,
  showUploadToast,
  insertPathIntoPromptInput,
  getAttachmentIcon,
  removePendingAttachment,
  renderPendingAttachments,
  addPendingAttachment,
  uploadAndAttachFile,
  reconnectTerminalTab,
  markSessionAsRead,
  sendTerminalResize
};
