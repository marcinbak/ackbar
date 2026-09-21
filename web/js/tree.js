// Sidebar Logical Tree View & Categorization
import {
  state,
  el,
  saveCollapsedGroups,
  saveCollapsedDoneGroups
} from './state.js';
import {
  escapeHtml,
  formatRelativeTime,
  getStateEmoji,
  getStateText,
  getAgentBadgeHtml,
  findHostRecord
} from './utils.js';
import {
  fetchSessions,
  setSessionDoneState,
  moveSessionToGroup
} from './api.js';
import { openSessionInTab } from './tabs.js';
import {
  showContextMenu,
  showGroupContextMenu
} from './context-menu.js';
import { showNewSessionModal } from './modals.js';

function getSessionTimestamp(s) {
  if (!s) return 0;
  const t = s.last_event_at || s.last_message_at || s.started_at;
  if (!t) return 0;
  const d = new Date(t);
  return isNaN(d.getTime()) ? 0 : d.getTime();
}

// Helper: Sort sessions descending by latest interaction (newest / active first)
function sortSessionsByInteraction(list) {
  return [...(list || [])].sort((a, b) => {
    const timeA = getSessionTimestamp(a);
    const timeB = getSessionTimestamp(b);
    if (timeB !== timeA) {
      return timeB - timeA;
    }
    return (a.id || '').localeCompare(b.id || '');
  });
}

// Settings API

function isSessionDone(sess) {
  if (!sess) return false;
  if (sess.is_done) return true;

  const settings = state.settings || {};
  if (settings.auto_done_enabled === 'false') return false;

  // Active agent states (1 = Working, 2 = Question) are never auto-done
  if (sess.state === 1 || sess.state === 2) return false;

  const hours = parseFloat(settings.auto_done_hours || '24');
  if (isNaN(hours) || hours <= 0) return false;

  const ts = getSessionTimestamp(sess);
  if (!ts) return false;

  const inactivityMs = Date.now() - ts;
  return inactivityMs > hours * 3600 * 1000;
}

// Session Auto-Archive Evaluation
function isSessionAutoArchived(sess) {
  if (!sess) return false;
  if (sess.archived) return true;

  const settings = state.settings || {};
  if (settings.auto_archive_enabled === 'false') return false;

  // Auto-archive only applies if session is Done
  if (!isSessionDone(sess)) return false;

  const days = parseFloat(settings.auto_archive_days || '7');
  if (isNaN(days) || days <= 0) return false;

  const ts = getSessionTimestamp(sess);
  if (!ts) return false;

  const inactivityMs = Date.now() - ts;
  return inactivityMs > days * 24 * 3600 * 1000;
}

// Control Session Done / Active State via Daemon API

function isDoneSectionCollapsed(groupKey) {
  const settings = state.settings || {};
  const defaultCollapsed = settings.done_collapsed_by_default !== 'false';
  if (defaultCollapsed) {
    return !state.expandedDoneGroups.has(groupKey);
  } else {
    return state.collapsedDoneGroups.has(groupKey);
  }
}

function toggleDoneSection(groupKey, sectionEl) {
  const wasCollapsed = sectionEl.classList.contains('collapsed');
  if (wasCollapsed) {
    sectionEl.classList.remove('collapsed');
    state.collapsedDoneGroups.delete(groupKey);
    state.expandedDoneGroups.add(groupKey);
  } else {
    sectionEl.classList.add('collapsed');
    state.collapsedDoneGroups.add(groupKey);
    state.expandedDoneGroups.delete(groupKey);
  }
  saveCollapsedDoneGroups();
}

function renderDoneSection(groupKey, doneSessions) {
  const sectionEl = document.createElement('div');
  sectionEl.className = 'tree-done-section';

  const isCollapsed = isDoneSectionCollapsed(groupKey);
  if (isCollapsed) {
    sectionEl.classList.add('collapsed');
  }

  const headerEl = document.createElement('div');
  headerEl.className = 'tree-done-header';
  headerEl.title = `Done sessions in this group (${doneSessions.length}). Click to toggle. Drag sessions here to mark as Done.`;

  const chevron = document.createElement('span');
  chevron.className = 'tree-done-chevron';
  chevron.textContent = '▼';

  const title = document.createElement('span');
  title.className = 'tree-done-title';
  title.innerHTML = `✓ Done <span class="tree-done-count">(${doneSessions.length})</span>`;

  headerEl.appendChild(chevron);
  headerEl.appendChild(title);

  headerEl.addEventListener('click', (e) => {
    e.stopPropagation();
    toggleDoneSection(groupKey, sectionEl);
  });

  headerEl.addEventListener('dragover', (e) => {
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = 'move';
    headerEl.classList.add('drag-over');
  });

  headerEl.addEventListener('dragleave', (e) => {
    e.stopPropagation();
    headerEl.classList.remove('drag-over');
  });

  headerEl.addEventListener('drop', async (e) => {
    e.preventDefault();
    e.stopPropagation();
    headerEl.classList.remove('drag-over');
    try {
      const data = JSON.parse(e.dataTransfer.getData('application/json'));
      if (data && data.sessionId) {
        if (groupKey !== 'Unassigned' && data.sessionPath !== groupKey) {
          await moveSessionToGroup(data.sessionId, data.sessionHost, groupKey);
        }
        await setSessionDoneState(data.sessionId, data.sessionHost, true);
      }
    } catch (err) {
      console.error('Done drop error:', err);
    }
  });

  const childrenListEl = document.createElement('div');
  childrenListEl.className = 'tree-done-children';
  doneSessions.forEach(sess => {
    childrenListEl.appendChild(createSessionRowElement(sess));
  });

  sectionEl.appendChild(headerEl);
  sectionEl.appendChild(childrenListEl);
  return sectionEl;
}

// Settings Modal Dialog

function renderTree() {
  if (!el.treeContainer) return;
  el.treeContainer.innerHTML = '';

  const query = state.searchQuery.toLowerCase().trim();

  // Pre-discover known group paths from registered tree nodes
  const knownGroupPaths = new Set();
  state.treeNodes.forEach(node => {
    if (node.path) {
      const parts = node.path.split('/');
      for (let i = 1; i <= parts.length; i++) {
        knownGroupPaths.add(parts.slice(0, i).join('/'));
      }
    }
  });

  // 1. Group sessions by exact logical node path
  const sessionsByPath = new Map();
  const unassigned = [];

  state.sessions.forEach(sess => {
    if ((sess.archived || isSessionAutoArchived(sess)) && !state.showArchived) {
      return;
    }

    if (query) {
      const match = (sess.name && sess.name.toLowerCase().includes(query)) ||
                    (sess.cwd && sess.cwd.toLowerCase().includes(query)) ||
                    (sess.host && sess.host.toLowerCase().includes(query)) ||
                    (sess.agent && sess.agent.toLowerCase().includes(query));
      if (!match) return;
    }

    let assignedPath = sess.node_path;
    if (!assignedPath && (sess.cwd || sess.git_remote)) {
      for (const n of state.treeNodes) {
        // 1. Match by Git URL across any host
        if (n.git_url && sess.git_remote && (sess.git_remote === n.git_url || normalizeGitURL(sess.git_remote) === normalizeGitURL(n.git_url))) {
          assignedPath = n.path;
          break;
        }
        // 2. Match by exact workspace prefix
        if (n.project_dir && (sess.cwd === n.project_dir || sess.cwd.startsWith(n.project_dir + '/'))) {
          assignedPath = n.path;
          break;
        }
        // 3. Cross-machine basename match (e.g. /home/dev4u/Work/ngl-android -> Modemobile/NGL/ngl-android)
        if (n.path && sess.cwd) {
          const groupLeaf = n.path.split('/').pop().toLowerCase();
          const cwdParts = sess.cwd.toLowerCase().split('/');
          if (cwdParts.includes(groupLeaf) && groupLeaf.length > 3) {
            assignedPath = n.path;
            break;
          }
        }
      }

      // 4. Fallback: match by ancestor group path component (e.g. /home/dev4u/Work/modemobile/... -> Modemobile)
      if (!assignedPath && sess.cwd) {
        const cwdParts = sess.cwd.toLowerCase().split('/').filter(Boolean);
        for (const groupPath of knownGroupPaths) {
          const topGroup = groupPath.split('/')[0];
          if (topGroup.length > 3 && cwdParts.includes(topGroup.toLowerCase())) {
            assignedPath = topGroup;
            break;
          }
        }
      }
    }

    if (assignedPath) {
      if (!sessionsByPath.has(assignedPath)) {
        sessionsByPath.set(assignedPath, []);
      }
      sessionsByPath.get(assignedPath).push(sess);
    } else {
      unassigned.push(sess);
    }
  });

  // 2. Discover all unique group paths and their parent prefixes
  const allGroupPaths = new Set();

  state.treeNodes.forEach(node => {
    if (node.path) {
      const parts = node.path.split('/');
      for (let i = 1; i <= parts.length; i++) {
        allGroupPaths.add(parts.slice(0, i).join('/'));
      }
    }
  });

  sessionsByPath.forEach((sessList, path) => {
    if (path && sessList.length > 0) {
      const parts = path.split('/');
      for (let i = 1; i <= parts.length; i++) {
        allGroupPaths.add(parts.slice(0, i).join('/'));
      }
    }
  });

  const sortedGroupPaths = Array.from(allGroupPaths).sort();

  // 3. Helper to recursively render a group node and its nested subgroups
  function renderGroupBranch(path, indentLevel) {
    const parts = path.split('/');
    const leafName = parts[parts.length - 1];
    const isCollapsed = query ? false : state.collapsedGroups.has(path);

    const groupEl = document.createElement('div');
    groupEl.className = `tree-group-node ${isCollapsed ? 'collapsed' : ''}`;

    const headerEl = document.createElement('div');
    headerEl.className = 'tree-group-header';
    headerEl.dataset.groupPath = path;
    headerEl.title = `Group: ${path} (Click to toggle, right-click for options)`;

    // Drop Target Handlers
    headerEl.addEventListener('dragover', (e) => {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      headerEl.classList.add('drag-over');
    });

    headerEl.addEventListener('dragleave', () => {
      headerEl.classList.remove('drag-over');
    });

    headerEl.addEventListener('drop', async (e) => {
      e.preventDefault();
      headerEl.classList.remove('drag-over');
      try {
        const data = JSON.parse(e.dataTransfer.getData('application/json'));
        if (data && data.sessionId) {
          await moveSessionToGroup(data.sessionId, data.sessionHost, path);
          const sess = state.sessions.find(s => s.id === data.sessionId);
          if (sess && isSessionDone(sess)) {
            await setSessionDoneState(data.sessionId, data.sessionHost, false);
          }
        }
      } catch (err) {
        console.error('Drop error:', err);
      }
    });

    headerEl.addEventListener('click', () => {
      state.lastActiveGroup = path;
      if (state.collapsedGroups.has(path)) {
        state.collapsedGroups.delete(path);
      } else {
        state.collapsedGroups.add(path);
      }
      saveCollapsedGroups();
      renderTree();
    });

    headerEl.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showGroupContextMenu(e.clientX, e.clientY, path);
    });

    const chevron = document.createElement('span');
    chevron.className = 'tree-group-chevron';
    chevron.textContent = '▼';

    const title = document.createElement('span');
    title.className = 'tree-group-title';
    title.textContent = indentLevel === 0 ? path : leafName;

    headerEl.appendChild(chevron);
    headerEl.appendChild(title);
    groupEl.appendChild(headerEl);

    // Children Container
    const childrenEl = document.createElement('div');
    childrenEl.className = 'tree-group-children';

    // 3a. Direct Sessions matching this exact path (partitioned into active and done)
    const directSessions = sortSessionsByInteraction(sessionsByPath.get(path) || []);
    const activeSessions = [];
    const doneSessions = [];

    directSessions.forEach(sess => {
      if (isSessionDone(sess)) {
        doneSessions.push(sess);
      } else {
        activeSessions.push(sess);
      }
    });

    activeSessions.forEach(sess => {
      childrenEl.appendChild(createSessionRowElement(sess));
    });

    // 3b. Done Subsection (if any sessions in Done state)
    if (doneSessions.length > 0) {
      childrenEl.appendChild(renderDoneSection(path, doneSessions));
    }

    // 3c. Direct Child Subgroups (nested recursive call)
    let childSubgroupsCount = 0;
    sortedGroupPaths.forEach(childPath => {
      if (childPath.startsWith(path + '/')) {
        const subparts = childPath.substring(path.length + 1).split('/');
        if (subparts.length === 1) { // direct descendant
          childSubgroupsCount++;
          const childEl = renderGroupBranch(childPath, indentLevel + 1);
          childrenEl.appendChild(childEl);
        }
      }
    });

    // 3d. Empty group placeholder hint
    if (activeSessions.length === 0 && doneSessions.length === 0 && childSubgroupsCount === 0) {
      const emptyHint = document.createElement('div');
      emptyHint.className = 'tree-group-empty-hint';
      emptyHint.textContent = 'Empty group (drag sessions here or right-click to spawn)';
      childrenEl.appendChild(emptyHint);
    }

    groupEl.appendChild(childrenEl);
    return groupEl;
  }

  // 4. Render Root Groups (paths without "/")
  sortedGroupPaths.forEach(path => {
    if (!path.includes('/')) {
      el.treeContainer.appendChild(renderGroupBranch(path, 0));
    }
  });

  // 5. Render Unassigned Group if non-empty (sorted by newest/active interaction first)
  if (unassigned.length > 0) {
    const unassignedPath = 'Unassigned';
    const isCollapsed = query ? false : state.collapsedGroups.has(unassignedPath);

    const groupEl = document.createElement('div');
    groupEl.className = `tree-group-node ${isCollapsed ? 'collapsed' : ''}`;

    const headerEl = document.createElement('div');
    headerEl.className = 'tree-group-header';
    headerEl.textContent = '▼ Unassigned';
    headerEl.addEventListener('click', () => {
      if (state.collapsedGroups.has(unassignedPath)) {
        state.collapsedGroups.delete(unassignedPath);
      } else {
        state.collapsedGroups.add(unassignedPath);
      }
      saveCollapsedGroups();
      renderTree();
    });

    groupEl.appendChild(headerEl);

    const childrenEl = document.createElement('div');
    childrenEl.className = 'tree-group-children';
    const sortedUnassigned = sortSessionsByInteraction(unassigned);
    const activeUnassigned = [];
    const doneUnassigned = [];

    sortedUnassigned.forEach(sess => {
      if (isSessionDone(sess)) {
        doneUnassigned.push(sess);
      } else {
        activeUnassigned.push(sess);
      }
    });

    activeUnassigned.forEach(sess => {
      childrenEl.appendChild(createSessionRowElement(sess));
    });

    if (doneUnassigned.length > 0) {
      childrenEl.appendChild(renderDoneSection('Unassigned', doneUnassigned));
    }

    groupEl.appendChild(childrenEl);
    el.treeContainer.appendChild(groupEl);
  }
}

// Create Interactive Draggable Session Card with Agent Badge & Context Menu
function createSessionRowElement(session) {
  const row = document.createElement('div');
  row.className = 'session-item';
  row.draggable = true;
  if (state.activeTabId === session.id) {
    row.classList.add('active-tab');
  }

  // Drag start event
  row.addEventListener('dragstart', (e) => {
    state.draggedSession = session;
    row.classList.add('dragging');
    e.dataTransfer.setData('application/json', JSON.stringify({
      sessionId: session.id,
      sessionHost: session.host || getSelfHostName(),
      sessionPath: session.node_path || ''
    }));
    e.dataTransfer.effectAllowed = 'move';
  });

  row.addEventListener('dragend', () => {
    row.classList.remove('dragging');
    state.draggedSession = null;
  });

  // Click: Open interactive terminal tab (connects/resumes session automatically)
  row.addEventListener('click', () => {
    openSessionInTab(session);
  });

  // Right-Click Context Menu
  row.addEventListener('contextmenu', (e) => {
    e.preventDefault();
    e.stopPropagation();
    showContextMenu(e.clientX, e.clientY, session);
  });

  const isManaged = session.managed || (session.tmux_name && session.state !== 4);
  const sessionFullName = session.name || session.agent || 'Unnamed Session';
  
  // Tooltip on hover displays the complete un-truncated session name
  row.title = sessionFullName;

  const left = document.createElement('div');
  left.className = 'session-item-left';

  const dot = document.createElement('span');
  dot.className = 'session-state-dot';
  dot.innerHTML = getStateEmoji(session);
  const stateStr = session.state === 1 ? 'Work in progress (agent is working)' : (session.state === 2 ? 'Waiting for feedback (agent is asking a question)' : (session.state === 3 ? 'Idle (completed work, not waiting)' : (session.state === 4 ? 'Ended (process terminated)' : (session.state === 5 ? 'Failed (process error)' : 'Not connected / Status unknown'))));
  dot.title = `Status: ${stateStr}\nMode: ${isManaged ? 'Live tmux session' : 'Observed process/transcript'}`;

  if (session.is_unread) {
    row.classList.add('session-unread');
    const unreadDot = document.createElement('span');
    unreadDot.className = 'unread-dot';
    unreadDot.title = 'Unread state update (click to view)';
    left.appendChild(unreadDot);
  }

  if (session.archived) {
    row.classList.add('session-archived');
  }

  if (isSessionDone(session)) {
    row.classList.add('is-done');
  }

  const name = document.createElement('span');
  name.className = 'session-name';
  name.textContent = sessionFullName;
  name.title = sessionFullName;

  left.appendChild(dot);
  left.appendChild(name);

  const right = document.createElement('div');
  right.className = 'session-item-right';

  if (session.archived) {
    const archBadge = document.createElement('span');
    archBadge.className = 'badge-archived';
    archBadge.textContent = '📦';
    archBadge.title = 'Archived Session';
    right.appendChild(archBadge);
  }

  // Agent badge with provider icon (compact icon-only with tooltip)
  const agentBadge = document.createElement('span');
  agentBadge.innerHTML = getAgentBadgeHtml(session.agent, true);
  right.appendChild(agentBadge);

  // Account badge (omitted for default profile)
  const rawAccount = session.account_id ? session.account_id.split(':').pop() : '';
  if (rawAccount && rawAccount !== 'default') {
    const accBadge = document.createElement('span');
    accBadge.className = 'badge-account';
    accBadge.textContent = rawAccount;
    accBadge.title = `Profile: ${rawAccount}`;
    right.appendChild(accBadge);
  }

  if (session.context_pct > 0) {
    const ctxBadge = document.createElement('span');
    ctxBadge.className = 'badge-ctx';
    ctxBadge.textContent = `${session.context_pct}%`;
    ctxBadge.title = `Context Window Used: ${session.context_pct}%`;

    const settings = state.settings || {};
    const handoverEnabled = settings.handover_suggestion_enabled !== 'false';
    const handoverThreshold = parseInt(settings.handover_threshold_pct || '60', 10);

    if (session.context_pct >= 80) {
      ctxBadge.classList.add('context-danger');
    } else if (session.context_pct >= handoverThreshold) {
      ctxBadge.classList.add('context-warning');
    }

    right.appendChild(ctxBadge);

    if (handoverEnabled && session.context_pct >= handoverThreshold && (session.managed || session.tmux_name) && session.state !== 'ended') {
      const btnHandover = document.createElement('button');
      btnHandover.className = 'btn-handover-quick';
      btnHandover.innerHTML = '🔄 Handover';
      btnHandover.title = `Context is at ${session.context_pct}%. Click to rotate to a clean turn with automated handover briefing.`;
      btnHandover.addEventListener('click', (e) => {
        e.stopPropagation();
        openHandoverModal(session.id);
      });
      right.appendChild(btnHandover);
    }
  }

  if (session.host && !isLocalHost(session.host)) {
    const hostBadge = document.createElement('span');
    hostBadge.className = 'badge-host';
    hostBadge.textContent = `@${formatHostLabel(session.host)}`;
    hostBadge.title = `Host: @${formatHostLabel(session.host)}`;
    right.appendChild(hostBadge);
  }

  const timeRel = formatRelativeTime(session.last_event_at || session.started_at);
  if (timeRel) {
    const timeBadge = document.createElement('span');
    timeBadge.className = 'badge-time';
    const d = new Date(session.last_event_at || session.started_at);
    const diffHour = (new Date() - d) / (1000 * 60 * 60);
    if (diffHour < 1) timeBadge.classList.add('time-recent');
    else if (diffHour < 24) timeBadge.classList.add('time-today');
    timeBadge.textContent = timeRel;
    timeBadge.title = `Last Activity: ${formatFullDateTime(session.last_event_at || session.started_at)}`;
    right.appendChild(timeBadge);
  }

  row.appendChild(left);
  row.appendChild(right);
  return row;
}

// Move Session to Group via API

export {
  getSessionTimestamp,
  sortSessionsByInteraction,
  isSessionDone,
  isSessionAutoArchived,
  isDoneSectionCollapsed,
  toggleDoneSection,
  renderDoneSection,
  renderTree,
  createSessionRowElement
};
