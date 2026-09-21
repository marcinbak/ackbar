// Interactive Chat Interface & Streaming Transcript
import { state, el } from './state.js';
import { getAuthToken } from './auth.js';
import {
  escapeHtml,
  renderMarkdown,
  formatRelativeTime,
  ensureDoubleNewlineSeparation,
  getSessionBaseUrl
} from './utils.js';
import { openSessionDetailsTab } from './details.js';
import { openHandoverModal } from './modals.js';
import {
  getOrCreateFileContextMenu,
  hideChatFileContextMenu,
  showChatFileContextMenu
} from './context-menu.js';

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
      <div class="chat-subagents-container" style="display: none;">
        <div class="chat-subagents-bar">
          <div class="chat-subagents-summary">
            <span class="chat-subagents-pulse"></span>
            <span class="chat-subagents-icon">⚡</span>
            <span class="chat-subagents-count-text">0 subagents running...</span>
            <span class="chat-subagents-chevron">▾</span>
          </div>
        </div>
        <div class="chat-subagents-list" style="display: none;"></div>
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
  tabObj.runningSubagents = [];
  tabObj.isSubagentsExpanded = false;

  tabObj.chatMessagesEl = chatViewEl.querySelector('.chat-messages-container');
  tabObj.chatSubagentsContainer = chatViewEl.querySelector('.chat-subagents-container');
  tabObj.chatSubagentsBar = chatViewEl.querySelector('.chat-subagents-bar');
  tabObj.chatSubagentsCountText = chatViewEl.querySelector('.chat-subagents-count-text');
  tabObj.chatSubagentsChevron = chatViewEl.querySelector('.chat-subagents-chevron');
  tabObj.chatSubagentsList = chatViewEl.querySelector('.chat-subagents-list');
  tabObj.chatComposerAttachments = chatViewEl.querySelector('.chat-composer-attachments');
  tabObj.chatComposerBox = chatViewEl.querySelector('.chat-composer-box');
  tabObj.chatAttachBtn = chatViewEl.querySelector('.btn-composer-attach');
  tabObj.chatFileInput = chatViewEl.querySelector('.chat-file-input');
  tabObj.chatInputEl = chatViewEl.querySelector('.chat-composer-textarea');
  tabObj.chatSendBtn = chatViewEl.querySelector('.btn-composer-send');
  tabObj.chatCancelBtn = chatViewEl.querySelector('.btn-composer-cancel');
  tabObj.chatStatusBadge = chatViewEl.querySelector('.chat-status-badge');
  tabObj.chatEngineBadge = chatViewEl.querySelector('.chat-engine-badge');

  if (tabObj.chatSubagentsBar) {
    tabObj.chatSubagentsBar.addEventListener('click', () => {
      tabObj.isSubagentsExpanded = !tabObj.isSubagentsExpanded;
      renderSubagentsBar(tabObj);
    });
  }

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
    if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      for (const file of e.dataTransfer.files) {
        await addPendingAttachment(tabObj, file);
      }
    }
  });

  chatDropOverlay.addEventListener('dragover', (e) => {
    e.preventDefault();
    if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      e.dataTransfer.dropEffect = 'copy';
    }
  });

  chatDropOverlay.addEventListener('drop', async (e) => {
    e.preventDefault();
    e.stopPropagation();
    chatDragCounter = 0;
    chatDropOverlay.classList.remove('active');
    if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
    if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      for (const file of e.dataTransfer.files) {
        await addPendingAttachment(tabObj, file);
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
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        for (const file of e.dataTransfer.files) {
          await addPendingAttachment(tabObj, file);
        }
      }
    });

    tabObj.chatInputEl.addEventListener('paste', async (e) => {
      if (!e.clipboardData) return;
      const items = e.clipboardData.items || [];
      let handled = false;
      for (const item of items) {
        if (item.kind === 'file') {
          const itemType = (item.type || '').toLowerCase();
          if (itemType.startsWith('image/') || itemType === 'application/pdf' || itemType.includes('bmp') || itemType.startsWith('text/') || itemType === 'application/json') {
            const file = item.getAsFile();
            if (file) {
              handled = true;
              await addPendingAttachment(tabObj, file);
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

  // Auto-load older messages when user scrolls near top of chat
  if (tabObj.chatMessagesEl) {
    tabObj.chatMessagesEl.addEventListener('scroll', () => {
      if (tabObj.chatMessagesEl.scrollTop < 60) {
        loadOlderTranscriptMessages(tabObj);
      }
    }, { passive: true });
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

const CHAT_PAGE_SIZE = 25;

// Render or update load more banner at top of chat container

function renderLoadMoreBanner(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return;
  const container = tabObj.chatMessagesEl;
  let banner = container.querySelector('.chat-load-more-container');
  const remaining = tabObj.loadedTranscriptStartIndex || 0;

  if (remaining <= 0) {
    if (banner) banner.remove();
    return;
  }

  if (!banner) {
    banner = document.createElement('div');
    banner.className = 'chat-load-more-container';
    banner.innerHTML = `
      <button type="button" class="btn-chat-load-more">
        <span class="load-more-icon">↑</span>
        <span class="load-more-text">Load older messages (${remaining} remaining)</span>
      </button>
    `;
    const btn = banner.querySelector('.btn-chat-load-more');
    if (btn) {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        loadOlderTranscriptMessages(tabObj);
      });
    }
    container.insertBefore(banner, container.firstChild);
  } else {
    const textEl = banner.querySelector('.load-more-text');
    if (textEl) {
      textEl.textContent = `Load older messages (${remaining} remaining)`;
    }
  }
}

// Load older messages chunk from in-memory transcript cache and prepend upward
function loadOlderTranscriptMessages(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return;
  if (!tabObj.allTranscriptMessages || tabObj.loadedTranscriptStartIndex <= 0) return;
  if (tabObj.isLoadingOlderTranscript) return;

  tabObj.isLoadingOlderTranscript = true;

  const container = tabObj.chatMessagesEl;
  const prevScrollHeight = container.scrollHeight;
  const prevScrollTop = container.scrollTop;

  const nextStartIndex = Math.max(0, tabObj.loadedTranscriptStartIndex - CHAT_PAGE_SIZE);
  const olderBatch = tabObj.allTranscriptMessages.slice(nextStartIndex, tabObj.loadedTranscriptStartIndex);
  tabObj.loadedTranscriptStartIndex = nextStartIndex;

  const bannerEl = container.querySelector('.chat-load-more-container');
  const insertReference = bannerEl ? bannerEl.nextSibling : container.firstChild;

  const fragment = document.createDocumentFragment();
  for (const msg of olderBatch) {
    const msgEl = createChatMessageElement(tabObj, msg);
    if (msgEl) {
      fragment.appendChild(msgEl);
    }
  }

  if (insertReference) {
    container.insertBefore(fragment, insertReference);
  } else {
    container.appendChild(fragment);
  }

  renderLoadMoreBanner(tabObj);

  // Compensate scroll position so visual content remains locked in place without jumping
  const heightDelta = container.scrollHeight - prevScrollHeight;
  container.scrollTop = prevScrollTop + heightDelta;

  tabObj.isLoadingOlderTranscript = false;
}

// Coalesce consecutive assistant transcript messages into single turns
function coalesceTranscriptMessages(messages) {
  if (!Array.isArray(messages)) return [];
  const coalesced = [];
  for (const msg of messages) {
    const last = coalesced[coalesced.length - 1];
    if (last && last.role === 'assistant' && msg.role === 'assistant') {
      if (msg.content) {
        if (last.content) {
          if (!last.content.includes(msg.content)) {
            last.content += '\n\n' + msg.content;
          }
        } else {
          last.content = msg.content;
        }
      }
      if (msg.thinking && !last.thinking) {
        last.thinking = msg.thinking;
      }
      if (Array.isArray(msg.tool_calls) && msg.tool_calls.length > 0) {
        last.tool_calls = (last.tool_calls || []).concat(msg.tool_calls);
      }
      if (msg.timestamp) {
        last.timestamp = msg.timestamp;
      }
    } else {
      coalesced.push({
        ...msg,
        tool_calls: Array.isArray(msg.tool_calls) ? [...msg.tool_calls] : []
      });
    }
  }
  return coalesced;
}

// Load past conversation messages from daemon transcript (latest first, paginated upward)
async function loadChatTranscript(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return;
  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
  try {
    const res = await fetch(`${baseUrl}/v1/sessions/transcript?id=${encodeURIComponent(sessionId)}&format=json`);
    if (!res.ok) return;
    const data = await res.json();
    if (data && Array.isArray(data.messages)) {
      const coalesced = coalesceTranscriptMessages(data.messages);
      tabObj.allTranscriptMessages = coalesced;
      tabObj.isLoadingOlderTranscript = false;
      tabObj.chatMessagesEl.innerHTML = '';

      const total = coalesced.length;
      if (total === 0) {
        tabObj.loadedTranscriptStartIndex = 0;
        return;
      }

      if (total <= CHAT_PAGE_SIZE) {
        tabObj.loadedTranscriptStartIndex = 0;
        for (const msg of coalesced) {
          appendChatMessage(tabObj, msg);
        }
      } else {
        tabObj.loadedTranscriptStartIndex = total - CHAT_PAGE_SIZE;
        renderLoadMoreBanner(tabObj);
        const initialMessages = coalesced.slice(tabObj.loadedTranscriptStartIndex);
        for (const msg of initialMessages) {
          appendChatMessage(tabObj, msg);
        }
      }
      tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
      requestAnimationFrame(() => {
        if (tabObj.chatMessagesEl) {
          tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
        }
      });

      if (Array.isArray(data.running_subagents)) {
        tabObj.runningSubagents = data.running_subagents;
        renderSubagentsBar(tabObj);
      } else if (Array.isArray(data.subagents)) {
        tabObj.runningSubagents = data.subagents;
        renderSubagentsBar(tabObj);
      } else {
        fetchRunningSubagents(tabObj);
      }
    } else {
      fetchRunningSubagents(tabObj);
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
    const targetPath = (!filePath.startsWith('/') && session && session.cwd)
      ? `${session.cwd.replace(/\/$/, '')}/${filePath}`
      : filePath;
    return openInVSCode(targetPath, host);
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
    } else if (!href.startsWith('#') && !href.startsWith('javascript:')) {
      a.setAttribute('target', '_blank');
      a.setAttribute('rel', 'noopener noreferrer');
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

// Parse tool call string into tool name and detail (e.g. "Bash: ls -la" -> { name: "Bash", detail: "ls -la" })
function parseToolCall(tc) {
  if (!tc || typeof tc !== 'string') return { name: 'tool', detail: '' };
  const trimmed = tc.trim();
  // Pattern 1: "ToolName: detail"
  const colonIdx = trimmed.indexOf(': ');
  if (colonIdx > 0 && colonIdx < 40) {
    return {
      name: trimmed.substring(0, colonIdx).trim(),
      detail: trimmed.substring(colonIdx + 2).trim()
    };
  }
  // Pattern 2: "tool_name (action description)"
  const parenMatch = trimmed.match(/^([a-zA-Z0-9_\-]+)\s*\((.+)\)$/);
  if (parenMatch) {
    return {
      name: parenMatch[1].trim(),
      detail: parenMatch[2].trim()
    };
  }
  return { name: trimmed, detail: '' };
}

// Generate collapsed tool group summary label (e.g. "Bash x12" or "5 tools (Bash x3, Grep x2)")
function getToolGroupSummaryLabel(toolCalls) {
  if (!toolCalls || toolCalls.length === 0) return '⚡ Tools';
  const parsed = toolCalls.map(parseToolCall);
  const counts = new Map();
  for (const p of parsed) {
    counts.set(p.name, (counts.get(p.name) || 0) + 1);
  }
  const uniqueTools = Array.from(counts.keys());
  const total = toolCalls.length;

  if (uniqueTools.length === 1) {
    const name = uniqueTools[0];
    if (total > 1) {
      return `⚡ ${name} x${total}`;
    }
    if (parsed[0].detail) {
      const shortDetail = parsed[0].detail.length > 50 ? parsed[0].detail.substring(0, 47) + '...' : parsed[0].detail;
      return `⚡ ${name}: ${shortDetail}`;
    }
    return `⚡ ${name}`;
  }

  // Multiple unique tools
  const breakdown = uniqueTools.map(name => `${name} x${counts.get(name)}`).join(', ');
  return `⚡ ${total} tools (${breakdown})`;
}

// Render a collapsed tool group card for assistant messages
function renderToolGroupHtml(toolCalls) {
  if (!toolCalls || toolCalls.length === 0) return '';
  const summaryLabel = getToolGroupSummaryLabel(toolCalls);
  const parsed = toolCalls.map(parseToolCall);
  const total = toolCalls.length;

  const itemsHtml = parsed.map((item, idx) => {
    const detailHtml = item.detail
      ? `<div class="chat-tool-call-detail"><code>${escapeHtml(item.detail)}</code></div>`
      : '';
    return `
      <div class="chat-tool-call-item">
        <div class="chat-tool-call-header">
          <span class="chat-tool-call-idx">#${idx + 1}</span>
          <span class="chat-tool-call-badge">${escapeHtml(item.name)}</span>
        </div>
        ${detailHtml}
      </div>
    `;
  }).join('');

  return `
    <details class="chat-tool-card chat-tool-group">
      <summary>
        <span class="tool-group-title">${escapeHtml(summaryLabel)}</span>
        <span class="tool-group-pill">${total} ${total === 1 ? 'action' : 'actions'}</span>
      </summary>
      <div class="chat-tool-content chat-tool-group-content">
        <div class="chat-tool-call-list">
          ${itemsHtml}
        </div>
      </div>
    </details>
  `;
}

// Create a chat message DOM element from message object
function createChatMessageElement(tabObj, msg) {
  if (!tabObj || !msg) return null;
  const msgEl = document.createElement('div');
  const timeStr = msg.timestamp ? new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';

  if (msg.role === 'user') {
    msgEl.className = 'chat-msg user-msg';
    if (msg.rawPrompt) {
      msgEl.dataset.rawPrompt = msg.rawPrompt;
    } else if (msg.content) {
      msgEl.dataset.rawPrompt = msg.content;
    }

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
      toolsHtml = renderToolGroupHtml(msg.tool_calls);
    }
    const bodyHtml = renderMarkdown(msg.content || '');
    const agentName = tabObj.session ? (tabObj.session.agent || 'Claude Code') : 'Claude Code';
    msgEl.innerHTML = `
      <div class="chat-msg-header">
        <span class="chat-msg-role">🤖 ${escapeHtml(agentName)}</span>
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
  if (tabObj.session) {
    linkifyChatFiles(msgEl, tabObj.session);
  }

  return msgEl;
}

// Append a message bubble into the chat messages container
function appendChatMessage(tabObj, msg) {
  if (!tabObj || !tabObj.chatMessagesEl) return null;
  const msgEl = createChatMessageElement(tabObj, msg);
  if (msgEl) {
    tabObj.chatMessagesEl.appendChild(msgEl);
  }
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

// Render running subagents box directly above prompt composer
function renderSubagentsBar(tabObj) {
  if (!tabObj || !tabObj.chatSubagentsContainer) return;
  const running = (tabObj.runningSubagents || []).filter(s => s.state === 'running');
  if (running.length === 0) {
    tabObj.chatSubagentsContainer.style.display = 'none';
    if (tabObj.chatSubagentsList) tabObj.chatSubagentsList.style.display = 'none';
    return;
  }

  tabObj.chatSubagentsContainer.style.display = 'flex';
  const countText = running.length === 1 ? '1 subagent running...' : `${running.length} subagents running...`;
  if (tabObj.chatSubagentsCountText) {
    tabObj.chatSubagentsCountText.textContent = countText;
  }

  if (tabObj.isSubagentsExpanded) {
    if (tabObj.chatSubagentsList) {
      tabObj.chatSubagentsList.style.display = 'flex';
      tabObj.chatSubagentsList.innerHTML = running.map((sub, idx) => {
        const name = sub.name || sub.role || `Subagent #${idx + 1}`;
        const role = sub.role && sub.role !== name ? sub.role : '';
        const prompt = (sub.prompt || '').trim();
        return `
          <div class="chat-subagent-item">
            <div class="chat-subagent-header">
              <div class="chat-subagent-role">
                <span class="chat-subagents-pulse"></span>
                <span class="chat-subagent-name">${escapeHtml(name)}</span>
                ${role ? `<span class="chat-subagent-badge">${escapeHtml(role)}</span>` : ''}
              </div>
              ${prompt ? `
                <button class="btn-copy-subagent-prompt" title="Copy prompt" type="button" data-prompt="${escapeHtml(prompt)}">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                  </svg>
                </button>
              ` : ''}
            </div>
            ${prompt ? `<div class="chat-subagent-prompt">${escapeHtml(prompt)}</div>` : ''}
          </div>
        `;
      }).join('');

      tabObj.chatSubagentsList.querySelectorAll('.btn-copy-subagent-prompt').forEach(btn => {
        btn.addEventListener('click', (e) => {
          e.stopPropagation();
          const p = btn.getAttribute('data-prompt') || '';
          copyToClipboard(p, btn);
        });
      });
    }
    if (tabObj.chatSubagentsChevron) {
      tabObj.chatSubagentsChevron.style.transform = 'rotate(180deg)';
    }
  } else {
    if (tabObj.chatSubagentsList) {
      tabObj.chatSubagentsList.style.display = 'none';
    }
    if (tabObj.chatSubagentsChevron) {
      tabObj.chatSubagentsChevron.style.transform = 'none';
    }
  }
}

// Fetch running subagents for tabObj
async function fetchRunningSubagents(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
  const token = getAuthToken();
  let url = `${baseUrl}/v1/sessions/subagents?id=${encodeURIComponent(tabObj.session.id)}`;
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  try {
    const res = await fetch(url);
    if (res.ok) {
      const data = await res.json();
      if (Array.isArray(data.subagents)) {
        tabObj.runningSubagents = data.subagents;
        renderSubagentsBar(tabObj);
      }
    }
  } catch (e) {
    // Background fetch ignore
  }
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
      content: promptText || 'Please inspect the attached file(s).',
      rawPrompt: daemonPrompt,
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
      content: promptText || 'Please inspect the attached file(s).',
      rawPrompt: daemonPrompt,
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

    case 'subagents_update':
      if (Array.isArray(evt.subagents)) {
        tabObj.runningSubagents = evt.subagents;
        renderSubagentsBar(tabObj);
      }
      break;

    case 'turn_start':
      tabObj.activeTurnHadTool = false;
      if (evt.text) {
        const evtText = (evt.text || '').trim();
        const userMsgs = tabObj.chatMessagesEl.querySelectorAll('.chat-msg.user-msg');
        let isAlreadyRendered = false;
        for (let i = userMsgs.length - 1; i >= Math.max(0, userMsgs.length - 3); i--) {
          const uMsg = userMsgs[i];
          const raw = uMsg.dataset.rawPrompt ? uMsg.dataset.rawPrompt.trim() : '';
          const uBody = uMsg.querySelector('.chat-msg-body');
          const bodyText = uBody ? uBody.textContent.trim() : '';
          if (raw && (raw === evtText || evtText.startsWith(raw))) {
            isAlreadyRendered = true;
            break;
          }
          if (bodyText && (evtText === bodyText || evtText.startsWith(bodyText) || bodyText.startsWith(evtText))) {
            isAlreadyRendered = true;
            break;
          }
          if (uMsg.textContent.includes(evtText)) {
            isAlreadyRendered = true;
            break;
          }
        }
        if (!isAlreadyRendered) {
          const newMsgEl = appendChatMessage(tabObj, {
            role: 'user',
            content: evt.text,
            rawPrompt: evt.text,
            timestamp: evt.timestamp || new Date().toISOString()
          });
          if (tabObj.activeTurnMsgEl && tabObj.activeTurnMsgEl.parentNode === tabObj.chatMessagesEl) {
            tabObj.chatMessagesEl.insertBefore(newMsgEl, tabObj.activeTurnMsgEl);
          }
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
          let groupCard = slot.querySelector('.chat-tool-group');
          if (!groupCard) {
            groupCard = document.createElement('details');
            groupCard.className = 'chat-tool-card chat-tool-group';
            groupCard.open = true;
            groupCard.dataset.toolCalls = JSON.stringify([]);
            groupCard.innerHTML = `
              <summary>
                <span class="tool-group-title">⚡ ${escapeHtml(evt.tool_name)}: running...</span>
                <span class="tool-group-pill">1 action</span>
              </summary>
              <div class="chat-tool-content chat-tool-group-content">
                <div class="chat-tool-call-list"></div>
              </div>
            `;
            slot.appendChild(groupCard);
          }

          let toolList = [];
          try {
            toolList = JSON.parse(groupCard.dataset.toolCalls || '[]');
          } catch (_) {}

          const inputStr = typeof evt.tool_input === 'string'
            ? evt.tool_input
            : (evt.tool_input ? JSON.stringify(evt.tool_input, null, 2) : '');

          toolList.push({ name: evt.tool_name, detail: inputStr });
          groupCard.dataset.toolCalls = JSON.stringify(toolList);

          const summaryTitle = groupCard.querySelector('.tool-group-title');
          const summaryPill = groupCard.querySelector('.tool-group-pill');
          const label = getToolGroupSummaryLabel(toolList.map(t => t.detail ? `${t.name}: ${t.detail}` : t.name));
          if (summaryTitle) summaryTitle.textContent = `${label} (running...)`;
          if (summaryPill) summaryPill.textContent = `${toolList.length} ${toolList.length === 1 ? 'action' : 'actions'}`;

          const listEl = groupCard.querySelector('.chat-tool-call-list');
          if (listEl) {
            const itemEl = document.createElement('div');
            itemEl.className = 'chat-tool-call-item in-progress';
            itemEl.innerHTML = `
              <div class="chat-tool-call-header">
                <span class="chat-tool-call-idx">#${toolList.length}</span>
                <span class="chat-tool-call-badge">${escapeHtml(evt.tool_name)}</span>
                <span class="chat-tool-status">running...</span>
              </div>
              ${inputStr ? `<div class="chat-tool-call-detail"><code>${escapeHtml(inputStr)}</code></div>` : ''}
            `;
            listEl.appendChild(itemEl);
          }
          tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
        }
      }
      if (evt.tool_name === 'Agent' || evt.tool_name === 'Task') {
        let promptText = '';
        if (evt.tool_input && typeof evt.tool_input === 'object') {
          promptText = evt.tool_input.prompt || evt.tool_input.description || '';
        } else if (typeof evt.tool_input === 'string') {
          promptText = evt.tool_input;
        }
        const sub = {
          id: 'sub-' + Date.now(),
          name: evt.tool_name,
          prompt: promptText,
          state: 'running'
        };
        tabObj.runningSubagents = tabObj.runningSubagents || [];
        tabObj.runningSubagents.push(sub);
        renderSubagentsBar(tabObj);
      }
      break;

    case 'tool_result':
      tabObj.activeTurnHadTool = true;
      if (evt.tool_name === 'Agent' || evt.tool_name === 'Task') {
        tabObj.runningSubagents = (tabObj.runningSubagents || []).filter(s => s.name !== evt.tool_name);
        renderSubagentsBar(tabObj);
      }
      if (msgEl) {
        const slot = msgEl.querySelector('.chat-tools-slot');
        if (slot) {
          const groupCard = slot.querySelector('.chat-tool-group');
          if (groupCard) {
            const listEl = groupCard.querySelector('.chat-tool-call-list');
            const lastItem = listEl ? (listEl.querySelector('.chat-tool-call-item.in-progress:last-child') || listEl.lastElementChild) : null;
            if (lastItem) {
              lastItem.classList.remove('in-progress');
              const statusEl = lastItem.querySelector('.chat-tool-status');
              if (statusEl) statusEl.textContent = 'done';
              if (evt.tool_output) {
                const outEl = document.createElement('div');
                outEl.className = 'chat-tool-output';
                outEl.textContent = evt.tool_output;
                lastItem.appendChild(outEl);
              }
              linkifyChatFiles(lastItem, tabObj.session);
            }
          }
        }
        tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
      }
      break;

    case 'turn_complete':
      tabObj.runningSubagents = [];
      renderSubagentsBar(tabObj);
      if (msgEl) {
        const groupCard = msgEl.querySelector('.chat-tool-group');
        if (groupCard) {
          groupCard.open = false; // collapse on completion so it doesn't pollute
          let toolList = [];
          try {
            toolList = JSON.parse(groupCard.dataset.toolCalls || '[]');
          } catch (_) {}
          const label = getToolGroupSummaryLabel(toolList.map(t => t.detail ? `${t.name}: ${t.detail}` : t.name));
          const summaryTitle = groupCard.querySelector('.tool-group-title');
          if (summaryTitle) summaryTitle.textContent = label;
        }

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
      tabObj.runningSubagents = [];
      renderSubagentsBar(tabObj);
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

export {
  CHAT_PAGE_SIZE,
  KNOWN_CHAT_FILE_EXTS,
  CHAT_IMAGE_EXTS,
  APP_ICONS,
  setupChatInterface,
  loadChatQueue,
  renderLoadMoreBanner,
  loadOlderTranscriptMessages,
  coalesceTranscriptMessages,
  loadChatTranscript,
  copyTextToClipboard,
  attachCodeBlockCopyButtons,
  getChatFileContentUrl,
  openChatFileInApp,
  isChatFilePath,
  createChatFilePill,
  createChatImagePreviewCard,
  linkifyChatFiles,
  attachChatMessageListeners,
  parseToolCall,
  getToolGroupSummaryLabel,
  renderToolGroupHtml,
  createChatMessageElement,
  appendChatMessage,
  updateComposerButtonState,
  renderChatQueue,
  renderSubagentsBar,
  fetchRunningSubagents,
  dispatchNextQueuedPrompt,
  sendChatPrompt,
  disconnectChatStream,
  connectChatStream,
  handleChatStreamEvent,
  resetChatComposer,
  cancelChatTurn
};
