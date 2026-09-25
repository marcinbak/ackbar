// Ackbar GUI Application Main Entrypoint (ES Module)
import { state, el, saveCollapsedGroups } from './state.js';
import './auth.js';
import {
  configureMarked,
  escapeHtml,
  getGroupPreferences,
  recordGroupSpawn,
  openInVSCode,
  isLocalHost,
  getSelfHostName,
  formatHostLabel
} from './utils.js';
import {
  fetchVersion,
  fetchHosts,
  renderHosts,
  fetchTreeNodes,
  fetchSessions,
  fetchSettings,
  fetchProviders,
  setSessionDoneState,
  setSessionLaterState
} from './api.js';
import { connectSSE } from './sse.js';
import {
  renderTree,
  getSessionTimestamp,
  sortSessionsByInteraction,
  isSessionDone,
  isSessionLater
} from './tree.js';
import {
  openSessionInTab,
  activateTab,
  closeTab,
  closeOtherTabs,
  closeTabsToTheRight,
  closeAllTabs,
  checkAndReconnectActiveTabs,
  restorePersistedTabs,
  handleTabOverflow,
  setTabViewMode,
  handleTakeWheel
} from './tabs.js';
import {
  sendTerminalResize,
  reconnectTerminalTab
} from './terminal.js';
import {
  openSessionDetailsTab,
  openTranscriptViewerTab,
  openDocViewerTab
} from './details.js';
import {
  toggleCommandPalette,
  renderCommandPaletteResults,
  executeDispatchLaunch
} from './palette.js';
import {
  showContextMenu,
  hideContextMenu,
  showGroupContextMenu,
  hideGroupContextMenu,
  showTabContextMenu,
  hideTabContextMenu,
  hideChatFileContextMenu
} from './context-menu.js';
import {
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
  showEditHostModal,
  showHooksDashboardModal
} from './modals.js';
import { updateStatusbar, resetStatusbar } from './statusbar.js';
import { copyTextToClipboard } from './chat.js';


async function init() {
  setupEventListeners();
  await fetchVersion();
  await fetchSettings();
  await fetchProviders();
  await fetchHosts();
  await fetchTreeNodes();
  await fetchSessions();
  restorePersistedTabs();
  connectSSE();
  setupResizer();
  window.addEventListener('resize', () => {
    handleTabOverflow();
    const activeTab = state.openTabs.get(state.activeTabId);
    if (activeTab && activeTab.type === 'terminal' && activeTab.fitAddon) {
      try {
        activeTab.fitAddon.fit();
        if (activeTab.terminal && activeTab.socket && activeTab.socket.readyState === WebSocket.OPEN) {
          sendTerminalResize(activeTab.socket, activeTab.terminal.cols, activeTab.terminal.rows);
        }
      } catch (e) {}
    }
  });

  // Periodic Multi-Host Live Health Probe & Auto-Discovery every 6 seconds
  setInterval(() => {
    fetchHosts();
    connectSSE();
  }, 6000);
}

function setupEventListeners() {
  // Dismiss context menus on outside click or escape
  document.addEventListener('click', () => {
    hideContextMenu();
    hideGroupContextMenu();
    hideTabContextMenu();
    hideChatFileContextMenu();
  });

  // Intercept clicks on links in chat or markdown to open in system browser / new tab
  document.addEventListener('click', (e) => {
    const link = e.target.closest('a');
    if (!link) return;

    // Ignore file pills, internal buttons, or anchor links
    if (link.dataset.fileLinkified === 'true' || link.classList.contains('chat-file-pill') || link.closest('.chat-file-pill')) {
      return;
    }

    const href = link.getAttribute('href') || '';
    if (!href || href.startsWith('#') || href.startsWith('javascript:')) {
      return;
    }

    // External web links (http://, https://, or protocol-relative //)
    if (/^https?:\/\//i.test(href) || href.startsWith('//')) {
      e.preventDefault();
      e.stopPropagation();
      window.open(href, '_blank', 'noopener,noreferrer');
    }
  }, true);

  window.addEventListener('resize', hideChatFileContextMenu);
  window.addEventListener('scroll', hideChatFileContextMenu, true);

  // Prevent browser default behavior of opening dropped files in a new tab
  window.addEventListener('dragover', (e) => {
    if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      e.preventDefault();
    }
  });

  window.addEventListener('drop', (e) => {
    if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      e.preventDefault();
    }
  });

  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      hideContextMenu();
      hideGroupContextMenu();
      hideTabContextMenu();
      if (el.cmdPaletteOverlay) {
        if (el.dispatchPreview && el.dispatchPreview.style.display === 'block') {
          el.dispatchPreview.style.display = 'none';
          if (el.cmdPaletteResults) el.cmdPaletteResults.style.display = 'block';
          if (el.cmdPaletteInput) el.cmdPaletteInput.focus();
        } else {
          el.cmdPaletteOverlay.style.display = 'none';
        }
      }
      hideModal();
    }

    // Cmd+K or Ctrl+K for Command Palette
    if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault();
      toggleCommandPalette();
    }

    // Cmd+W or Ctrl+W to Close Active Tab
    if ((e.metaKey || e.ctrlKey) && (e.key === 'w' || e.key === 'W')) {
      if (state.activeTabId && document.activeElement !== el.searchInput && document.activeElement !== el.cmdPaletteInput) {
        e.preventDefault();
        closeTab(state.activeTabId);
      }
    }

    // Cmd+1..9 to switch tabs
    if ((e.metaKey || e.ctrlKey) && e.key >= '1' && e.key <= '9') {
      const tabIndex = parseInt(e.key, 10) - 1;
      const tabIds = Array.from(state.openTabs.keys());
      if (tabIds[tabIndex]) {
        e.preventDefault();
        activateTab(tabIds[tabIndex]);
      }
    }
  });

  // Command Palette Input Events
  if (el.cmdPaletteInput) {
    el.cmdPaletteInput.addEventListener('input', (e) => {
      renderCommandPaletteResults(e.target.value);
    });

    el.cmdPaletteInput.addEventListener('keydown', (e) => {
      const items = el.cmdPaletteResults.querySelectorAll('.cmd-item');
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        state.cmdPaletteSelectedIndex = Math.min(items.length - 1, state.cmdPaletteSelectedIndex + 1);
        items.forEach((it, idx) => it.classList.toggle('selected', idx === state.cmdPaletteSelectedIndex));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        state.cmdPaletteSelectedIndex = Math.max(0, state.cmdPaletteSelectedIndex - 1);
        items.forEach((it, idx) => it.classList.toggle('selected', idx === state.cmdPaletteSelectedIndex));
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (state.cmdPaletteItems[state.cmdPaletteSelectedIndex]) {
          const it = state.cmdPaletteItems[state.cmdPaletteSelectedIndex];
          if (!it.isDispatch) {
            el.cmdPaletteOverlay.style.display = 'none';
          }
          it.action();
        }
      }
    });
  }

  // Dispatcher Preview Buttons & Keyboard Controls
  if (el.dispatchGroup) {
    el.dispatchGroup.addEventListener('change', () => {
      const val = el.dispatchGroup.value;
      const node = (state.treeNodes || []).find(n => n.path === val);
      if (node && node.project_dir && el.dispatchCwd) {
        el.dispatchCwd.value = node.project_dir;
      }
    });
  }

  if (el.btnDispatchCancel) {
    el.btnDispatchCancel.addEventListener('click', () => {
      if (el.dispatchPreview) el.dispatchPreview.style.display = 'none';
      if (el.cmdPaletteResults) el.cmdPaletteResults.style.display = 'block';
      if (el.cmdPaletteInput) el.cmdPaletteInput.focus();
    });
  }

  if (el.btnDispatchLaunch) {
    el.btnDispatchLaunch.addEventListener('click', () => {
      executeDispatchLaunch();
    });
  }

  if (el.dispatchPreview) {
    el.dispatchPreview.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.target !== el.dispatchPrompt || e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        executeDispatchLaunch();
      }
    });
  }

  // Session Context Menu actions
  if (el.cmItemInfo) {
    el.cmItemInfo.addEventListener('click', () => {
      if (state.contextMenuSession) openSessionDetailsTab(state.contextMenuSession);
    });
  }

  if (el.cmItemTranscript) {
    el.cmItemTranscript.addEventListener('click', () => {
      if (state.contextMenuSession) openTranscriptViewerTab(state.contextMenuSession);
    });
  }

  if (el.cmItemCopyName) {
    el.cmItemCopyName.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const text = sess.name || sess.agent || '';
        await copyTextToClipboard(text);
      }
    });
  }

  if (el.cmItemCopyPath) {
    el.cmItemCopyPath.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const sessName = sess.name || sess.agent || '';
        const fullPath = sess.node_path ? `${sess.node_path}/${sessName}` : sessName;
        await copyTextToClipboard(fullPath);
      }
    });
  }

  if (el.cmItemHandover) {
    el.cmItemHandover.addEventListener('click', () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        hideContextMenu();
        openHandoverModal(sess.id);
      }
    });
  }

  if (el.cmItemTakeWheel) {
    el.cmItemTakeWheel.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        hideContextMenu();
        await handleTakeWheel(sess.id);
      }
    });
  }

  if (el.btnViewChat) {
    el.btnViewChat.addEventListener('click', () => {
      if (state.activeTabId) setTabViewMode(state.activeTabId, 'chat');
    });
  }

  if (el.btnViewTerminal) {
    el.btnViewTerminal.addEventListener('click', () => {
      if (state.activeTabId) setTabViewMode(state.activeTabId, 'terminal');
    });
  }

  if (el.cmItemResume) {
    el.cmItemResume.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
        try {
          const res = await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=resume`, { method: 'POST' });
          if (res.ok) {
            const data = await res.json();
            if (data.session) Object.assign(sess, data.session);
          }
        } catch (e) {
          console.error('Failed to resume session:', e);
        }
        openSessionInTab(sess);
        await fetchSessions();
      }
    });
  }

  if (el.cmItemNewTab) {
    el.cmItemNewTab.addEventListener('click', () => {
      if (state.contextMenuSession) openSessionInTab(state.contextMenuSession);
    });
  }

  if (el.cmItemVSCode) {
    el.cmItemVSCode.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        openInVSCode(state.contextMenuSession.cwd, state.contextMenuSession.host);
      }
    });
  }

  if (el.cmItemDocs) {
    el.cmItemDocs.addEventListener('click', () => {
      if (state.contextMenuSession) {
        showProjectDocsModal(state.contextMenuSession.cwd, state.contextMenuSession.name, state.contextMenuSession.host, state.contextMenuSession);
      }
    });
  }

  if (el.cmItemRestart) {
    el.cmItemRestart.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
        await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=restart`, { method: 'POST' });
        openSessionInTab(sess);
      }
    });
  }

  if (el.cmItemKill) {
    el.cmItemKill.addEventListener('click', async () => {
      if (state.contextMenuSession && confirm(`Terminate process for "${state.contextMenuSession.name}"?`)) {
        const sess = state.contextMenuSession;
        const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
        await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=kill`, { method: 'POST' });
        await fetchSessions();
      }
    });
  }

  if (el.cmItemLater) {
    el.cmItemLater.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const isLater = isSessionLater(sess);
        hideContextMenu();
        await setSessionLaterState(sess.id, sess.host, !isLater);
      }
    });
  }

  if (el.cmItemDone) {
    el.cmItemDone.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const isDone = isSessionDone(sess);
        hideContextMenu();
        await setSessionDoneState(sess.id, sess.host, !isDone);
      }
    });
  }

  if (el.cmItemArchive) {
    el.cmItemArchive.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const targetAction = sess.archived ? 'unarchive' : 'archive';
        // Optimistically update locally
        sess.archived = !sess.archived;
        renderTree();

        try {
          const hostRec = (state.hosts || []).find(h => h.name === sess.host);
          const baseUrl = (sess && sess.hostUrl) ? sess.hostUrl.replace(/\/$/, '') : (hostRec && hostRec.url && !isLocalHost(sess.host) ? hostRec.url.replace(/\/$/, '') : '');
          const url = `${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sess.id)}&action=${targetAction}`;
          const res = await fetch(url, { method: 'POST' });
          if (res.ok) {
            await fetchSessions();
          }
        } catch (err) {
          console.error(`Failed to ${targetAction} session:`, err);
        }
      }
    });
  }

  if (el.cmItemDelete) {
    el.cmItemDelete.addEventListener('click', async () => {
      if (state.contextMenuSession) {
        const sess = state.contextMenuSession;
        const msg = `⚠️ Permanently Delete Session "${sess.name}"?\n\nThis will permanently delete the session and its transcript logs from the agent on disk (~/.claude/projects/) without the possibility for recovery.\n\nUse Archive (📦) if you only want to hide it from your active list.`;
        if (confirm(msg)) {
          const targetId = sess.id;
          const targetName = sess.name;
          const nativeId = sess.native_id;
          state.sessions = state.sessions.filter(s => s.id !== targetId && s.name !== targetName && (!nativeId || s.native_id !== nativeId));
          closeTab(targetId);
          if (nativeId) closeTab(nativeId);
          closeTab(`details_${targetId}`);
          if (nativeId) closeTab(`details_${nativeId}`);
          for (const [tId, tabObj] of state.openTabs.entries()) {
            if (tabObj.session && (tabObj.session.id === targetId || (nativeId && tabObj.session.native_id === nativeId))) {
              closeTab(tId);
            }
          }
          renderTree();
          const baseUrl = sess.hostUrl ? sess.hostUrl.replace(/\/$/, '') : '';
          await fetch(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(targetId)}&action=delete`, { method: 'POST' });
          await fetchSessions();
        }
      }
    });
  }

  // Group Folder Context Menu Actions
  if (el.gcmItemNewSession) {
    el.gcmItemNewSession.addEventListener('click', () => {
      const groupPath = state.contextMenuGroupPath;
      hideContextMenu();
      if (groupPath) {
        showNewSessionModal(groupPath);
      }
    });
  }

  if (el.gcmItemNewSubgroup) {
    el.gcmItemNewSubgroup.addEventListener('click', () => {
      if (state.contextMenuGroupPath) {
        showNewGroupModal(state.contextMenuGroupPath + '/');
      }
    });
  }

  if (el.gcmItemVSCode) {
    el.gcmItemVSCode.addEventListener('click', async () => {
      if (state.contextMenuGroupPath) {
        const node = state.treeNodes.find(n => n.path === state.contextMenuGroupPath);
        const dir = (node && node.project_dir) ? node.project_dir : '';
        if (dir) {
          const groupSess = state.sessions.find(s => s.node_path === state.contextMenuGroupPath || (s.cwd && s.cwd.startsWith(dir)));
          const host = groupSess ? groupSess.host : getSelfHostName();
          openInVSCode(dir, host);
        } else {
          alert('This category subgroup does not have a linked filesystem directory.');
        }
      }
    });
  }

  if (el.gcmItemDocs) {
    el.gcmItemDocs.addEventListener('click', () => {
      if (state.contextMenuGroupPath) {
        const node = state.treeNodes.find(n => n.path === state.contextMenuGroupPath);
        const dir = (node && node.project_dir) ? node.project_dir : '';
        if (dir) {
          const groupSess = state.sessions.find(s => s.node_path === state.contextMenuGroupPath || (s.cwd && s.cwd.startsWith(dir)));
          const host = groupSess ? groupSess.host : getSelfHostName();
          showProjectDocsModal(dir, state.contextMenuGroupPath, host, groupSess);
        } else {
          alert('This category subgroup does not have a linked filesystem directory.');
        }
      }
    });
  }

  if (el.gcmItemDelete) {
    el.gcmItemDelete.addEventListener('click', async () => {
      const groupPath = state.contextMenuGroupPath;
      if (groupPath && confirm(`Delete group folder "${groupPath}"? Sessions will be moved to Unassigned.`)) {
        state.treeNodes = state.treeNodes.filter(n => n.path !== groupPath && !n.path.startsWith(groupPath + '/'));
        state.sessions.forEach(s => {
          if (s.node_path === groupPath || (s.node_path && s.node_path.startsWith(groupPath + '/'))) {
            s.node_path = '';
          }
        });
        state.collapsedGroups.delete(groupPath);
        renderTree();

        await fetch(`/v1/nodes?path=${encodeURIComponent(groupPath)}`, { method: 'DELETE' });
        await fetchTreeNodes();
        await fetchSessions();
      }
    });
  }

  // Tab Context Menu actions
  if (el.tcmItemHandover) {
    el.tcmItemHandover.addEventListener('click', () => {
      if (state.contextMenuTabId) {
        const tabId = state.contextMenuTabId;
        hideTabContextMenu();
        openHandoverModal(tabId);
      }
    });
  }

  if (el.btnHandoverCancel) {
    el.btnHandoverCancel.addEventListener('click', () => {
      closeHandoverModal();
    });
  }

  if (el.btnHandoverModalClose) {
    el.btnHandoverModalClose.addEventListener('click', () => {
      closeHandoverModal();
    });
  }

  if (el.btnHandoverConfirm) {
    el.btnHandoverConfirm.addEventListener('click', () => {
      submitHandover();
    });
  }

  if (el.tcmItemClose) {
    el.tcmItemClose.addEventListener('click', () => {
      if (state.contextMenuTabId) {
        closeTab(state.contextMenuTabId);
        hideTabContextMenu();
      }
    });
  }

  if (el.tcmItemCloseOthers) {
    el.tcmItemCloseOthers.addEventListener('click', () => {
      if (state.contextMenuTabId) {
        closeOtherTabs(state.contextMenuTabId);
        hideTabContextMenu();
      }
    });
  }

  if (el.tcmItemCloseRight) {
    el.tcmItemCloseRight.addEventListener('click', () => {
      if (state.contextMenuTabId) {
        closeTabsToTheRight(state.contextMenuTabId);
        hideTabContextMenu();
      }
    });
  }

  if (el.tcmItemCloseAll) {
    el.tcmItemCloseAll.addEventListener('click', () => {
      closeAllTabs();
      hideTabContextMenu();
    });
  }

  if (el.searchInput) {
    el.searchInput.addEventListener('input', (e) => {
      state.searchQuery = e.target.value;
      renderTree();
    });

    window.addEventListener('keydown', (e) => {
      const activeTag = document.activeElement ? document.activeElement.tagName : '';
      const isInputFocused = activeTag === 'INPUT' || activeTag === 'TEXTAREA' || (document.activeElement && document.activeElement.isContentEditable);
      if (e.key === '/' && !isInputFocused) {
        e.preventDefault();
        el.searchInput.focus();
      }
    });
  }

  if (el.btnAddHost) {
    el.btnAddHost.addEventListener('click', showAddHostModal);
  }

  if (el.btnDiscovery) {
    el.btnDiscovery.addEventListener('click', showHooksDashboardModal);
  }

  if (el.btnToggleArchived) {
    el.btnToggleArchived.addEventListener('click', () => {
      state.showArchived = !state.showArchived;
      el.btnToggleArchived.classList.toggle('active', state.showArchived);
      renderTree();
    });
  }

  if (el.btnCollapseAll) {
    el.btnCollapseAll.addEventListener('click', () => {
      state.treeNodes.forEach(n => state.collapsedGroups.add(n.path));
      state.collapsedGroups.add('Unassigned');
      saveCollapsedGroups();
      renderTree();
    });
  }

  if (el.btnExpandAll) {
    el.btnExpandAll.addEventListener('click', () => {
      state.collapsedGroups.clear();
      saveCollapsedGroups();
      renderTree();
    });
  }

  if (el.btnPurge) {
    el.btnPurge.addEventListener('click', async () => {
      if (confirm('Safe Purge & Rescan: Refresh live sessions from disk while strictly preserving groups?')) {
        await fetch('/v1/maintenance/purge', { method: 'POST' });
        await fetchSessions();
      }
    });
  }

  if (el.btnSettings) {
    el.btnSettings.addEventListener('click', () => {
      showSettingsModal();
    });
  }

  if (el.btnRefreshPage) {
    el.btnRefreshPage.addEventListener('click', () => {
      window.location.reload();
    });
  }

  if (el.sbModeToggle) {
    el.sbModeToggle.addEventListener('click', () => {
      if (!state.activeTabId) return;
      const curTab = state.openTabs.get(state.activeTabId);
      if (curTab && curTab.socket && curTab.socket.readyState === WebSocket.OPEN) {
        curTab.socket.send(new Uint8Array([0x1b, 0x5b, 0x5a]));
        if (curTab.terminal) curTab.terminal.focus();
      }
    });
  }

  if (el.btnNewProject) {
    el.btnNewProject.addEventListener('click', () => showNewGroupModal());
  }

  if (el.btnTabOverflow) {
    el.btnTabOverflow.addEventListener('click', (e) => {
      e.stopPropagation();
      if (el.overflowMenuContent) {
        el.overflowMenuContent.classList.toggle('show');
      }
    });
  }

  if (el.modalCloseBtn) {
    el.modalCloseBtn.addEventListener('click', hideModal);
  }

  const btnNewSession = document.getElementById('btnNewSession');
  const btnSidebarNewSession = document.getElementById('btnSidebarNewSession');
  const btnCloseNewSession = document.getElementById('btnCloseNewSession');
  const btnCancelNewSession = document.getElementById('btnCancelNewSession');
  const btnSubmitNewSession = document.getElementById('btnSubmitNewSession');
  const modalNewSession = document.getElementById('modalNewSession');

  if (btnNewSession) btnNewSession.addEventListener('click', () => showNewSessionModal());
  if (btnSidebarNewSession) btnSidebarNewSession.addEventListener('click', () => showNewSessionModal());
  if (btnCloseNewSession) btnCloseNewSession.addEventListener('click', hideNewSessionModal);
  if (btnCancelNewSession) btnCancelNewSession.addEventListener('click', hideNewSessionModal);
  if (btnSubmitNewSession) btnSubmitNewSession.addEventListener('click', handleSpawnNewSession);
  if (modalNewSession) {
    modalNewSession.addEventListener('click', (e) => {
      if (e.target === modalNewSession) hideNewSessionModal();
    });
  }
}

// Helper: Intelligent Cross-Host Path Translation

function setupResizer() {
  if (!el.sidebarResizer || !el.sidebarPanel) return;
  let isResizing = false;

  el.sidebarResizer.addEventListener('mousedown', () => {
    isResizing = true;
    el.sidebarResizer.classList.add('resizing');
    document.body.style.cursor = 'col-resize';
  });

  document.addEventListener('mousemove', (e) => {
    if (!isResizing) return;
    const newWidth = Math.max(200, Math.min(600, e.clientX));
    el.sidebarPanel.style.width = `${newWidth}px`;
    const activeTab = state.openTabs.get(state.activeTabId);
    if (activeTab && activeTab.type === 'terminal' && activeTab.fitAddon) {
      try {
        activeTab.fitAddon.fit();
        if (activeTab.terminal && activeTab.socket && activeTab.socket.readyState === WebSocket.OPEN) {
          sendTerminalResize(activeTab.socket, activeTab.terminal.cols, activeTab.terminal.rows);
        }
      } catch (e) {}
    }
  });

  document.addEventListener('mouseup', () => {
    if (isResizing) {
      isResizing = false;
      el.sidebarResizer.classList.remove('resizing');
      document.body.style.cursor = '';
      state.openTabs.forEach(t => {
        if (t.fitAddon && t.fitAddon.fit) {
          try {
            t.fitAddon.fit();
            if (t.terminal && t.socket && t.socket.readyState === WebSocket.OPEN) {
              sendTerminalResize(t.socket, t.terminal.cols, t.terminal.rows);
            }
          } catch (e) {}
        }
      });
    }
  });
}



// Expose global hooks for developer console and debugging
window.state = state;
window.openSessionInTab = openSessionInTab;
window.openSessionDetailsTab = openSessionDetailsTab;
window.reconnectTerminalTab = reconnectTerminalTab;

// Launch on DOM Content Loaded
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', init);
} else {
  init();
}

export {
  init,
  setupEventListeners,
  setupResizer
};
