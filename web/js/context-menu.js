// Right-Click Context Menus
import { state, el } from './state.js';
import {
  escapeHtml,
  getSessionBaseUrl,
  isLocalHost
} from './utils.js';
import { isSessionDone, isSessionLater } from './tree.js';
import {
  openSessionInTab,
  handleTakeWheel,
  closeTab,
  closeOtherTabs,
  closeTabsToTheRight,
  closeAllTabs
} from './tabs.js';
import {
  openSessionDetailsTab,
  openTranscriptViewerTab
} from './details.js';
import {
  openHandoverModal,
  showNewSessionModal,
  showNewGroupModal,
  showProjectDocsModal,
  showModal
} from './modals.js';
import {
  fetchSessions,
  setSessionDoneState
} from './api.js';
import { showUploadToast } from './terminal.js';
import {
  openChatFileInApp,
  CHAT_IMAGE_EXTS,
  getChatFileContentUrl,
  APP_ICONS
} from './chat.js';


let activeFileMenuPill = null;
let fileContextMenuEl = null;

function getOrCreateFileContextMenu() {
  if (fileContextMenuEl) return fileContextMenuEl;
  fileContextMenuEl = document.createElement('div');
  fileContextMenuEl.className = 'chat-file-context-menu context-menu';
  fileContextMenuEl.id = 'chatFileContextMenu';
  document.body.appendChild(fileContextMenuEl);
  return fileContextMenuEl;
}

function hideChatFileContextMenu() {
  if (fileContextMenuEl) {
    fileContextMenuEl.style.display = 'none';
    fileContextMenuEl.style.visibility = 'hidden';
  }
  if (activeFileMenuPill) {
    activeFileMenuPill.classList.remove('menu-active');
    activeFileMenuPill = null;
  }
}

function showChatFileContextMenu(pill, cleanPath, session, clickX, clickY) {
  if (typeof hideContextMenu === 'function') hideContextMenu();
  if (typeof hideGroupContextMenu === 'function') hideGroupContextMenu();
  if (typeof hideTabContextMenu === 'function') hideTabContextMenu();
  hideChatFileContextMenu();

  const menu = getOrCreateFileContextMenu();
  activeFileMenuPill = pill;
  pill.classList.add('menu-active');

  const ext = cleanPath.split('?')[0].split('.').pop().toLowerCase();
  const isImg = CHAT_IMAGE_EXTS.has(ext);
  const isHtml = (ext === 'html' || ext === 'htm');
  const isPdf = (ext === 'pdf');
  const filename = cleanPath.split('/').pop();
  const contentUrl = getChatFileContentUrl(cleanPath, session);

  let icon = '📄';
  if (isImg) icon = '🖼️';
  else if (isHtml) icon = '🌐';
  else if (isPdf) icon = '📑';
  else if (['js', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'c', 'cpp', 'swift'].includes(ext)) icon = '💻';

  const previewSub = isImg || isPdf ? 'macOS Preview.app' : 'Native preview';

  menu.innerHTML = `
    <div class="chat-file-menu-header">
      <span class="chat-file-menu-icon">${icon}</span>
      <span class="chat-file-menu-name" title="${escapeHtml(cleanPath)}">${escapeHtml(filename)}</span>
    </div>
    <div class="context-menu-item" data-action="browser">
      <span class="menu-app-icon">${APP_ICONS.browser}</span>
      <div class="menu-item-text">
        <span class="menu-item-title">Open in Browser</span>
        <span class="menu-item-sub">View / render inline</span>
      </div>
    </div>
    <div class="context-menu-item" data-action="preview">
      <span class="menu-app-icon">${APP_ICONS.preview}</span>
      <div class="menu-item-text">
        <span class="menu-item-title">Open in Preview</span>
        <span class="menu-item-sub">${previewSub}</span>
      </div>
    </div>
    <div class="context-menu-item" data-action="app">
      <span class="menu-app-icon">${APP_ICONS.app}</span>
      <div class="menu-item-text">
        <span class="menu-item-title">Open in Default App</span>
        <span class="menu-item-sub">System application</span>
      </div>
    </div>
    <div class="context-menu-item" data-action="vscode">
      <span class="menu-app-icon">${APP_ICONS.vscode}</span>
      <div class="menu-item-text">
        <span class="menu-item-title">Open in VS Code</span>
        <span class="menu-item-sub">Visual Studio Code</span>
      </div>
    </div>
    <div class="context-menu-divider"></div>
    <div class="context-menu-item" data-action="copy">
      <span class="menu-app-icon">${APP_ICONS.copy}</span>
      <div class="menu-item-text">
        <span class="menu-item-title">Copy File Path</span>
        <span class="menu-item-sub">${escapeHtml(cleanPath)}</span>
      </div>
    </div>
  `;

  menu.querySelectorAll('.context-menu-item').forEach(item => {
    item.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      const action = item.dataset.action;
      hideChatFileContextMenu();

      if (action === 'browser') {
        window.open(contentUrl, '_blank');
      } else if (action === 'preview') {
        openChatFileInApp(cleanPath, session, 'preview');
      } else if (action === 'app') {
        openChatFileInApp(cleanPath, session, 'default');
      } else if (action === 'vscode') {
        openChatFileInApp(cleanPath, session, 'vscode');
      } else if (action === 'copy') {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(cleanPath).then(() => {
            showUploadToast(`Copied path: ${cleanPath}`, 'success');
          }).catch(() => {
            showUploadToast(`Copied path: ${cleanPath}`, 'info');
          });
        } else {
          const ta = document.createElement('textarea');
          ta.value = cleanPath;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand('copy');
          document.body.removeChild(ta);
          showUploadToast(`Copied path: ${cleanPath}`, 'success');
        }
      }
    });
  });

  menu.style.visibility = 'hidden';
  menu.style.display = 'block';

  const menuWidth = menu.offsetWidth || 230;
  const menuHeight = menu.offsetHeight || 260;

  let posX = 0;
  let posY = 0;

  if (typeof clickX === 'number' && typeof clickY === 'number') {
    posX = clickX;
    posY = clickY;
  } else {
    const rect = pill.getBoundingClientRect();
    posX = rect.left;
    posY = rect.bottom + 4;
    if (posY + menuHeight > window.innerHeight - 10) {
      posY = Math.max(10, rect.top - menuHeight - 4);
    }
  }

  if (posY + menuHeight > window.innerHeight - 10) {
    posY = Math.max(10, window.innerHeight - menuHeight - 10);
  }
  if (posX + menuWidth > window.innerWidth - 10) {
    posX = Math.max(10, window.innerWidth - menuWidth - 10);
  }

  menu.style.left = `${posX}px`;
  menu.style.top = `${posY}px`;
  menu.style.visibility = 'visible';
}

// Build an interactive compact file badge element

function showContextMenu(x, y, session) {
  if (!el.contextMenu) return;
  hideGroupContextMenu();
  hideTabContextMenu();
  hideChatFileContextMenu();
  state.contextMenuSession = session;

  const isLater = isSessionLater(session);
  const isDone = isSessionDone(session);

  if (el.cmItemLater) {
    el.cmItemLater.innerHTML = isLater ? '<span>↺</span> Move to Active' : '<span>⏳</span> Move to Later';
  }

  if (el.cmItemDone) {
    el.cmItemDone.innerHTML = isDone ? '<span>↺</span> Move to Active' : '<span>✓</span> Mark as Done';
  }

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
  hideTabContextMenu();
  hideChatFileContextMenu();
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
  hideChatFileContextMenu();
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

export {
  getOrCreateFileContextMenu,
  hideChatFileContextMenu,
  showChatFileContextMenu,
  showContextMenu,
  hideContextMenu,
  showGroupContextMenu,
  hideGroupContextMenu,
  showTabContextMenu,
  hideTabContextMenu
};
