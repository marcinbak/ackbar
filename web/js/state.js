// Ackbar Application State & DOM Cache
function loadCollapsedGroups() {
  try {
    const saved = localStorage.getItem('ackbar_collapsed_groups');
    if (saved) {
      return new Set(JSON.parse(saved));
    }
  } catch (e) {
    console.error('Failed to load collapsed groups from localStorage:', e);
  }
  return new Set();
}

function saveCollapsedGroups() {
  try {
    localStorage.setItem('ackbar_collapsed_groups', JSON.stringify(Array.from(state.collapsedGroups)));
  } catch (e) {
    console.error('Failed to save collapsed groups to localStorage:', e);
  }
}

function loadCollapsedDoneGroups() {
  try {
    const saved = localStorage.getItem('ackbar_collapsed_done_groups');
    if (saved) {
      return new Set(JSON.parse(saved));
    }
  } catch (e) {
    console.error('Failed to load collapsed done groups from localStorage:', e);
  }
  return new Set();
}

function loadExpandedDoneGroups() {
  try {
    const saved = localStorage.getItem('ackbar_expanded_done_groups');
    if (saved) {
      return new Set(JSON.parse(saved));
    }
  } catch (e) {
    console.error('Failed to load expanded done groups from localStorage:', e);
  }
  return new Set();
}

function saveCollapsedDoneGroups() {
  try {
    localStorage.setItem('ackbar_collapsed_done_groups', JSON.stringify(Array.from(state.collapsedDoneGroups)));
    localStorage.setItem('ackbar_expanded_done_groups', JSON.stringify(Array.from(state.expandedDoneGroups)));
  } catch (e) {
    console.error('Failed to save collapsed done groups to localStorage:', e);
  }
}

function loadCollapsedLaterGroups() {
  try {
    const saved = localStorage.getItem('ackbar_collapsed_later_groups');
    if (saved) {
      return new Set(JSON.parse(saved));
    }
  } catch (e) {
    console.error('Failed to load collapsed later groups from localStorage:', e);
  }
  return new Set();
}

function loadExpandedLaterGroups() {
  try {
    const saved = localStorage.getItem('ackbar_expanded_later_groups');
    if (saved) {
      return new Set(JSON.parse(saved));
    }
  } catch (e) {
    console.error('Failed to load expanded later groups from localStorage:', e);
  }
  return new Set();
}

function saveCollapsedLaterGroups() {
  try {
    localStorage.setItem('ackbar_collapsed_later_groups', JSON.stringify(Array.from(state.collapsedLaterGroups)));
    localStorage.setItem('ackbar_expanded_later_groups', JSON.stringify(Array.from(state.expandedLaterGroups)));
  } catch (e) {
    console.error('Failed to save collapsed later groups to localStorage:', e);
  }
}


export const state = {
  version: '...',
  selfHost: { name: 'local', displayName: '', url: '', online: true, isSelf: true },
  settings: null,
  sessions: [],
  treeNodes: [],
  hosts: [],
  openTabs: new Map(), // tabId -> { type: 'terminal'|'details'|'doc', session, terminal, fitAddon, socket, containerEl, tabEl }
  activeTabId: null,
  providers: [],
  collapsedGroups: loadCollapsedGroups(),
  collapsedDoneGroups: loadCollapsedDoneGroups(),
  expandedDoneGroups: loadExpandedDoneGroups(),
  collapsedLaterGroups: loadCollapsedLaterGroups(),
  expandedLaterGroups: loadExpandedLaterGroups(),
  searchQuery: '',
  showArchived: false,
  draggedSession: null,
  contextMenuSession: null,
  contextMenuGroupPath: null,
  contextMenuTabId: null,
  lastActiveGroup: null,
  cmdPaletteSelectedIndex: 0,
  cmdPaletteItems: []
};


window.state = state;
window.__ackbarState = state;

export const el = {
  appVersion: document.getElementById('appVersion'),
  hostList: document.getElementById('hostList'),
  searchInput: document.getElementById('searchInput'),
  treeContainer: document.getElementById('treeContainer'),
  tabStrip: document.getElementById('tabStrip'),
  terminalViewport: document.getElementById('terminalViewport'),
  emptyState: document.getElementById('emptyState'),
  tabOverflowDropdown: document.getElementById('tabOverflowDropdown'),
  tabOverflowCount: document.getElementById('tabOverflowCount'),
  overflowMenuContent: document.getElementById('overflowMenuContent'),
  btnTabOverflow: document.getElementById('btnTabOverflow'),
  btnNewTab: document.getElementById('btnNewTab'),
  btnAddHost: document.getElementById('btnAddHost'),
  btnNewProject: document.getElementById('btnNewProject'),
  btnPurge: document.getElementById('btnPurge'),
  btnSettings: document.getElementById('btnSettings'),
  btnRefreshPage: document.getElementById('btnRefreshPage'),
  btnDiscovery: document.getElementById('btnDiscovery'),
  btnToggleArchived: document.getElementById('btnToggleArchived'),
  btnCollapseAll: document.getElementById('btnCollapseAll'),
  btnExpandAll: document.getElementById('btnExpandAll'),
  sidebarPanel: document.getElementById('sidebarPanel'),
  sidebarResizer: document.getElementById('sidebarResizer'),
  // Statusbar elements
  sbSessionName: document.getElementById('sbSessionName'),
  sbHostBadge: document.getElementById('sbHostBadge'),
  sbCwd: document.getElementById('sbCwd'),
  sbGitBranch: document.getElementById('sbGitBranch'),
  sbContextGauge: document.getElementById('sbContextGauge'),
  sbModelBadge: document.getElementById('sbModelBadge'),
  sbPID: document.getElementById('sbPID'),
  sbModeToggle: document.getElementById('sbModeToggle'),
  sbLastActive: document.getElementById('sbLastActive'),
  // Dual Engine View Controls
  viewModeToggle: document.getElementById('viewModeToggle'),
  btnViewChat: document.getElementById('btnViewChat'),
  btnViewTerminal: document.getElementById('btnViewTerminal'),
  newSessionEngine: document.getElementById('newSessionEngine'),
  // Modal elements
  modalOverlay: document.getElementById('modalOverlay'),
  modalTitle: document.getElementById('modalTitle'),
  modalBody: document.getElementById('modalBody'),
  modalFooter: document.getElementById('modalFooter'),
  modalCloseBtn: document.getElementById('modalCloseBtn'),
  // Command Palette & Dispatcher
  cmdPaletteOverlay: document.getElementById('cmdPaletteOverlay'),
  cmdPaletteInput: document.getElementById('cmdPaletteInput'),
  cmdPaletteResults: document.getElementById('cmdPaletteResults'),
  dispatchPreview: document.getElementById('dispatchPreview'),
  dispatchMatchedBanner: document.getElementById('dispatchMatchedBanner'),
  dispatchMatchedTitle: document.getElementById('dispatchMatchedTitle'),
  dispatchMatchedSubtitle: document.getElementById('dispatchMatchedSubtitle'),
  btnDispatchSwitchActive: document.getElementById('btnDispatchSwitchActive'),
  dispatchPreviewSource: document.getElementById('dispatchPreviewSource'),
  dispatchHost: document.getElementById('dispatchHost'),
  dispatchAgent: document.getElementById('dispatchAgent'),
  dispatchGroup: document.getElementById('dispatchGroup'),
  dispatchCwd: document.getElementById('dispatchCwd'),
  dispatchPrompt: document.getElementById('dispatchPrompt'),
  btnDispatchCancel: document.getElementById('btnDispatchCancel'),
  btnDispatchLaunch: document.getElementById('btnDispatchLaunch'),
  // Session Context Menu
  contextMenu: document.getElementById('contextMenu'),
  cmItemInfo: document.getElementById('cmItemInfo'),
  cmItemTranscript: document.getElementById('cmItemTranscript'),
  cmItemCopyName: document.getElementById('cmItemCopyName'),
  cmItemCopyPath: document.getElementById('cmItemCopyPath'),
  cmItemHandover: document.getElementById('cmItemHandover'),
  cmItemTakeWheel: document.getElementById('cmItemTakeWheel'),
  cmItemResume: document.getElementById('cmItemResume'),
  cmItemNewTab: document.getElementById('cmItemNewTab'),
  cmItemVSCode: document.getElementById('cmItemVSCode'),
  cmItemDocs: document.getElementById('cmItemDocs'),
  cmItemRestart: document.getElementById('cmItemRestart'),
  cmItemKill: document.getElementById('cmItemKill'),
  cmItemLater: document.getElementById('cmItemLater'),
  cmItemDone: document.getElementById('cmItemDone'),
  cmItemArchive: document.getElementById('cmItemArchive'),
  cmItemDelete: document.getElementById('cmItemDelete'),
  // Group Context Menu
  groupContextMenu: document.getElementById('groupContextMenu'),
  gcmItemNewSession: document.getElementById('gcmItemNewSession'),
  gcmItemNewSubgroup: document.getElementById('gcmItemNewSubgroup'),
  gcmItemVSCode: document.getElementById('gcmItemVSCode'),
  gcmItemDocs: document.getElementById('gcmItemDocs'),
  gcmItemDelete: document.getElementById('gcmItemDelete'),
  // Tab Context Menu
  tabContextMenu: document.getElementById('tabContextMenu'),
  tcmItemHandover: document.getElementById('tcmItemHandover'),
  tcmItemClose: document.getElementById('tcmItemClose'),
  tcmItemCloseOthers: document.getElementById('tcmItemCloseOthers'),
  tcmItemCloseRight: document.getElementById('tcmItemCloseRight'),
  tcmItemCloseAll: document.getElementById('tcmItemCloseAll'),
  // Handover Modal
  handoverModalOverlay: document.getElementById('handoverModalOverlay'),
  handoverModalCard: document.getElementById('handoverModalCard'),
  handoverModalTitle: document.getElementById('handoverModalTitle'),
  handoverModalAlert: document.getElementById('handoverModalAlert'),
  handoverStrategySelect: document.getElementById('handoverStrategySelect'),
  handoverCustomInstruction: document.getElementById('handoverCustomInstruction'),
  btnHandoverCancel: document.getElementById('btnHandoverCancel'),
  btnHandoverConfirm: document.getElementById('btnHandoverConfirm'),
  btnHandoverModalClose: document.getElementById('btnHandoverModalClose')
};

export {
  loadCollapsedGroups,
  saveCollapsedGroups,
  loadCollapsedDoneGroups,
  loadExpandedDoneGroups,
  saveCollapsedDoneGroups,
  loadCollapsedLaterGroups,
  loadExpandedLaterGroups,
  saveCollapsedLaterGroups
};
