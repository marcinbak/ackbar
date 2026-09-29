import { state } from './state.js';
import { fetchTasks, createTask, updateTask, deleteTask, fetchStandup, mergeTaskPR, fetchAgentStatuses, provisionAgents, fetchAllAgentStatuses, provisionAllHosts } from './api.js';
import { escapeHtml } from './utils.js';
import { activateTab, openSessionInTab } from './tabs.js';
import { showModal, hideModal } from './modals.js';

let boardTasks = [];
let currentGroupFilter = 'all';
let currentProjectFilter = 'all';
let currentSearchQuery = '';
let currentMode = 'workspace';

export function getGroupIcon(groupName) {
  if (!groupName) return '📁';
  const g = groupName.toLowerCase();
  if (g.includes('personal')) return '👤';
  if (g.includes('mode') || g.includes('work')) return '🏢';
  return '📁';
}

export function getDiscoveredTopGroups() {
  const groupMap = new Map();
  // 1. Discovered from registered tree nodes
  if (state.treeNodes && Array.isArray(state.treeNodes)) {
    state.treeNodes.forEach(n => {
      if (n.path) {
        const top = n.path.split('/')[0].trim();
        if (top && top !== 'Unassigned' && !groupMap.has(top.toLowerCase())) {
          groupMap.set(top.toLowerCase(), top);
        }
      }
    });
  }
  // 2. Discovered from current tasks on board
  boardTasks.forEach(t => {
    if (t.group_name && t.group_name.trim()) {
      const g = t.group_name.trim();
      if (!groupMap.has(g.toLowerCase())) {
        groupMap.set(g.toLowerCase(), g);
      }
    }
  });
  if (groupMap.size === 0) {
    groupMap.set('personal', 'Personal');
    groupMap.set('modemobile', 'Modemobile');
  }
  const sorted = Array.from(groupMap.values()).sort((a, b) => a.localeCompare(b));
  // If currentGroupFilter is set to a group that no longer exists, reset to 'all'
  if (currentGroupFilter !== 'all' && !sorted.some(g => g.toLowerCase() === currentGroupFilter.toLowerCase())) {
    currentGroupFilter = 'all';
  }
  return sorted;
}

export function renderGroupFilters() {
  const container = document.getElementById('taskGroupFilters');
  if (!container) return;

  const groups = getDiscoveredTopGroups();
  let html = `<button class="task-group-filter-btn ${currentGroupFilter === 'all' ? 'active' : ''}" data-group="all">All Work</button>`;

  groups.forEach(g => {
    const isActive = currentGroupFilter.toLowerCase() === g.toLowerCase();
    const icon = getGroupIcon(g);
    html += `<button class="task-group-filter-btn ${isActive ? 'active' : ''}" data-group="${escapeHtml(g)}">${icon} ${escapeHtml(g)}</button>`;
  });

  container.innerHTML = html;

  container.querySelectorAll('.task-group-filter-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      container.querySelectorAll('.task-group-filter-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      currentGroupFilter = btn.getAttribute('data-group') || 'all';
      updateProjectFilterDropdown();
      renderWorkBoard();
    });
  });
}

export function getAppMode() {
  return currentMode;
}

export function switchAppMode(mode) {
  currentMode = mode;
  const workspaceView = document.getElementById('workspaceView');
  const workBoardView = document.getElementById('workBoardView');
  const btnWorkspace = document.getElementById('btnModeWorkspace');
  const btnWorkBoard = document.getElementById('btnModeWorkBoard');

  if (mode === 'workboard') {
    if (workspaceView) workspaceView.style.display = 'none';
    if (workBoardView) workBoardView.style.display = 'flex';
    btnWorkspace?.classList.remove('active');
    btnWorkBoard?.classList.add('active');
    refreshWorkBoard();
  } else {
    if (workBoardView) workBoardView.style.display = 'none';
    if (workspaceView) workspaceView.style.display = 'flex';
    btnWorkBoard?.classList.remove('active');
    btnWorkspace?.classList.add('active');
    // Dispatch window resize event so Xterm.js fit addon re-fits terminal layout
    window.dispatchEvent(new Event('resize'));
  }
}

export async function refreshWorkBoard() {
  try {
    const tasks = await fetchTasks();
    if (Array.isArray(tasks)) {
      boardTasks = tasks;
    }
  } catch (err) {
    console.warn('Failed to refresh tasks:', err);
  }
  renderGroupFilters();
  updateProjectFilterDropdown();
  renderWorkBoard();
}

function updateProjectFilterDropdown() {
  const select = document.getElementById('taskProjectFilter');
  if (!select) return;

  const previousValue = select.value || 'all';
  const projects = new Set();
  boardTasks.forEach(t => {
    if (t.project_name) {
      if (currentGroupFilter === 'all' || (t.group_name && t.group_name.toLowerCase() === currentGroupFilter.toLowerCase())) {
        projects.add(t.project_name);
      }
    }
  });

  let html = '<option value="all">All Projects</option>';
  Array.from(projects).sort().forEach(p => {
    const selected = p === previousValue ? 'selected' : '';
    html += `<option value="${escapeHtml(p)}" ${selected}>${escapeHtml(p)}</option>`;
  });
  select.innerHTML = html;

  if (previousValue !== 'all' && !projects.has(previousValue)) {
    select.value = 'all';
  }
  currentProjectFilter = select.value;
}

export function renderWorkBoard() {
  const colNew = document.getElementById('colNewTasks');
  const colInProgress = document.getElementById('colInProgressTasks');
  const colReview = document.getElementById('colReviewTasks');
  const colDone = document.getElementById('colDoneTasks');

  const countNew = document.getElementById('countNewTasks');
  const countInProgress = document.getElementById('countInProgressTasks');
  const countReview = document.getElementById('countReviewTasks');
  const countDone = document.getElementById('countDoneTasks');

  if (!colNew || !colInProgress || !colReview || !colDone) return;

  // Filter tasks
  const filtered = boardTasks.filter(t => {
    // 1. Group Filter (strict match by group, not by path)
    if (currentGroupFilter !== 'all') {
      const g = (t.group_name || '').toLowerCase();
      if (g !== currentGroupFilter.toLowerCase()) {
        return false;
      }
    }

    // 2. Project Filter
    if (currentProjectFilter !== 'all' && t.project_name !== currentProjectFilter) {
      return false;
    }

    // 3. Search Query
    if (currentSearchQuery.trim()) {
      const q = currentSearchQuery.toLowerCase();
      const matchTitle = (t.title || '').toLowerCase().includes(q);
      const matchNotes = (t.notes || '').toLowerCase().includes(q);
      const matchBranch = (t.branch || '').toLowerCase().includes(q);
      const matchRefs = (t.external_refs || []).some(r => (r.ref_key || '').toLowerCase().includes(q));
      if (!matchTitle && !matchNotes && !matchBranch && !matchRefs) return false;
    }

    return true;
  });

  // Defensive client-side deduplication guard:
  // Suppress orphaned empty-branch tasks if an active task for the same ticket exists,
  // or redundant empty-branch tasks sharing identical title and group.
  const seenTaskKeys = new Set();
  const dedupedTasks = [];

  // Sort so branch-holding tasks take precedence over empty-branch tasks
  filtered.sort((a, b) => {
    const aHasBranch = (a.branch || '').trim() !== '';
    const bHasBranch = (b.branch || '').trim() !== '';
    if (aHasBranch !== bHasBranch) return aHasBranch ? -1 : 1;
    return (new Date(b.updated_at || 0)) - (new Date(a.updated_at || 0));
  });

  filtered.forEach(task => {
    const branch = (task.branch || '').trim();
    if (branch !== '') {
      const branchKey = `branch:${(task.project_name || '').toLowerCase()}:${branch.toLowerCase()}`;
      if (!seenTaskKeys.has(branchKey)) {
        seenTaskKeys.add(branchKey);
        (task.external_refs || []).forEach(r => {
          if (r.ref_key) seenTaskKeys.add(`ref:${r.ref_key.toLowerCase()}`);
        });
        dedupedTasks.push(task);
      }
    } else {
      let covered = false;
      const refs = (task.external_refs || []).map(r => (r.ref_key || '').toLowerCase()).filter(Boolean);
      for (const ref of refs) {
        if (seenTaskKeys.has(`ref:${ref}`)) {
          covered = true;
          break;
        }
      }
      const titleKey = `title:${(task.group_name || '').toLowerCase()}:${(task.title || '').trim().toLowerCase()}`;
      if (!covered && !seenTaskKeys.has(titleKey)) {
        seenTaskKeys.add(titleKey);
        refs.forEach(ref => seenTaskKeys.add(`ref:${ref}`));
        dedupedTasks.push(task);
      }
    }
  });

  const columns = {
    NEW: [],
    IN_PROGRESS: [],
    REVIEW: [],
    DONE: []
  };

  dedupedTasks.forEach(task => {
    const status = (task.status || 'NEW').toUpperCase();
    if (columns[status]) {
      columns[status].push(task);
    } else {
      columns.NEW.push(task);
    }
  });

  // Update counts
  if (countNew) countNew.textContent = columns.NEW.length;
  if (countInProgress) countInProgress.textContent = columns.IN_PROGRESS.length;
  if (countReview) countReview.textContent = columns.REVIEW.length;
  if (countDone) countDone.textContent = columns.DONE.length;

  // Render cards for each column
  colNew.innerHTML = renderTaskCards(columns.NEW);
  colInProgress.innerHTML = renderTaskCards(columns.IN_PROGRESS);
  colReview.innerHTML = renderTaskCards(columns.REVIEW);
  colDone.innerHTML = renderTaskCards(columns.DONE);

  // Attach card event listeners
  attachCardEventListeners();
}

function safeUrl(rawUrl) {
  if (!rawUrl || typeof rawUrl !== 'string') return '#';
  const trimmed = rawUrl.trim();
  if (trimmed.startsWith('https://') || trimmed.startsWith('http://') || trimmed.startsWith('/v1/') || trimmed.startsWith('#')) {
    return trimmed;
  }
  return '#';
}

function renderTaskCards(tasks) {
  if (tasks.length === 0) {
    return `<div class="kanban-empty-state">No tasks</div>`;
  }

  return tasks.map(task => {
    const trackerBadges = (task.external_refs || []).map(ref => {
      const icon = ref.tracker === 'jira' ? '🔷' : ref.tracker === 'linear' ? '📐' : '🔗';
      const cleanUrl = safeUrl(ref.url);
      return `<a href="${escapeHtml(cleanUrl)}" target="_blank" rel="noopener noreferrer" class="task-tracker-badge" title="${escapeHtml(ref.tracker)}: ${escapeHtml(ref.ref_key)}">${icon} ${escapeHtml(ref.ref_key)}</a>`;
    }).join(' ');

    const projectTag = task.project_name ? `<span class="task-project-tag">${escapeHtml(task.project_name)}</span>` : '';
    const substatusDot = getSubstatusBadge(task.substatus);

    const workerPills = (task.workers || []).map(w => {
      const activeClass = w.is_active ? 'active' : 'idle';
      const icon = w.agent?.includes('claude') ? '🟠' : w.agent?.includes('antigravity') ? '🟣' : '🟢';
      return `<button class="task-worker-pill ${activeClass}" data-session-id="${escapeHtml(w.session_id)}" title="Focus session: ${escapeHtml(w.session_id)}">
        ${icon} ${escapeHtml(w.agent || 'agent')} @${escapeHtml(w.host || 'local')}
      </button>`;
    }).join('');

    const deliverablesHtml = (task.deliverables || []).map(d => {
      const kindIcon = d.kind === 'html_ui' ? '🎨' : d.kind === 'video' ? '📹' : d.kind === 'plan' ? '📝' : '📦';
      const targetUrl = safeUrl(d.url || (d.file_path ? `/v1/files/content?path=${encodeURIComponent(d.file_path)}` : '#'));
      return `<a href="${escapeHtml(targetUrl)}" target="_blank" rel="noopener noreferrer" class="task-deliverable-chip" title="${escapeHtml(d.title)}">
        ${kindIcon} ${escapeHtml(d.title)}
      </a>`;
    }).join('');

    // Contextual actions
    let actionButtons = '';
    if (task.substatus === 'blocked' && task.blocker_question) {
      actionButtons += `<div class="task-blocker-callout">
        <div class="task-blocker-q">⚠️ <strong>Question:</strong> ${escapeHtml(task.blocker_question)}</div>
        <button class="btn btn-warning btn-xs btn-unblock-task" data-task-id="${escapeHtml(task.id)}">💬 Answer / Unblock</button>
      </div>`;
    }

    if (task.pr_url) {
      const cleanPrUrl = safeUrl(task.pr_url);
      const canMerge = task.status !== 'DONE' && (task.status === 'REVIEW' || task.substatus === 'approved') && task.pr_state !== 'MERGED';
      actionButtons += `<div class="task-pr-bar">
        <a href="${escapeHtml(cleanPrUrl)}" target="_blank" rel="noopener noreferrer" class="task-pr-link">
          🐙 PR #${escapeHtml(String(task.pr_number || ''))} (${escapeHtml(task.pr_state || 'OPEN')})
        </a>
        ${canMerge ? `<button class="btn btn-success btn-xs btn-merge-pr" data-task-id="${escapeHtml(task.id)}" data-pr-url="${escapeHtml(cleanPrUrl)}" title="Squash and merge pull request">⚡ Merge PR</button>` : ''}
      </div>`;
    }

    const updatedAtStr = task.updated_at ? formatRelativeTime(task.updated_at) : '';

    return `
      <div class="task-card" data-task-id="${escapeHtml(task.id)}">
        <div class="task-card-header">
          <div class="task-card-tags">
            ${trackerBadges}
            ${projectTag}
          </div>
          ${substatusDot}
        </div>
        <div class="task-card-title">${escapeHtml(task.title)}</div>
        ${task.notes ? `<div class="task-card-notes">${escapeHtml(task.notes)}</div>` : ''}
        ${actionButtons}
        ${deliverablesHtml ? `<div class="task-card-deliverables">${deliverablesHtml}</div>` : ''}
        <div class="task-card-footer">
          <div class="task-card-workers">${workerPills}</div>
          <div class="task-card-meta">
            <span class="task-card-time">${escapeHtml(updatedAtStr)}</span>
            <button class="task-card-menu-btn" data-task-id="${escapeHtml(task.id)}" title="Task Actions">⋯</button>
          </div>
        </div>
      </div>
    `;
  }).join('');
}

function getSubstatusBadge(substatus) {
  if (!substatus) return '';
  const s = substatus.toLowerCase();
  let color = 'var(--text-muted)';
  let label = substatus;

  if (s === 'active' || s === 'running') {
    color = 'var(--accent-green)';
    label = 'active';
  } else if (s === 'blocked') {
    color = 'var(--accent-red)';
    label = 'blocked';
  } else if (s === 'approved') {
    color = 'var(--accent-cyan)';
    label = 'approved';
  } else if (s === 'task_review' || s === 'in_review') {
    color = 'var(--accent-amber)';
    label = 'review';
  } else if (s === 'completed') {
    color = 'var(--accent-green)';
    label = 'completed';
  }

  return `<span class="task-substatus-badge" style="--substatus-color: ${color};">
    <span class="substatus-dot"></span>${escapeHtml(label)}
  </span>`;
}

function formatRelativeTime(isoStr) {
  try {
    if (!isoStr) return '';
    const date = new Date(isoStr);
    if (isNaN(date.getTime())) return '';
    const now = new Date();
    const diffSec = Math.floor((now - date) / 1000);
    if (diffSec < 0 || diffSec < 60) return 'just now';
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    const diffHours = Math.floor(diffMin / 60);
    if (diffHours < 24) return `${diffHours}h ago`;
    const diffDays = Math.floor(diffHours / 24);
    return `${diffDays}d ago`;
  } catch (e) {
    return '';
  }
}

function attachCardEventListeners() {
  // 1. Worker pill clicks -> switch to workspace and activate tab
  document.querySelectorAll('.task-worker-pill').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const sessionId = btn.getAttribute('data-session-id');
      if (sessionId) {
        switchAppMode('workspace');
        // Find session in state
        const session = state.sessions.find(s => s.id === sessionId);
        if (session) {
          openSessionInTab(session);
        } else {
          activateTab(sessionId);
        }
      }
    });
  });

  // 2. Task card menu / click for edit
  document.querySelectorAll('.task-card-menu-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const taskId = btn.getAttribute('data-task-id');
      const task = boardTasks.find(t => t.id === taskId);
      if (task) showEditTaskModal(task);
    });
  });

  // 3. Unblock button -> focus session chat
  document.querySelectorAll('.btn-unblock-task').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const taskId = btn.getAttribute('data-task-id');
      const task = boardTasks.find(t => t.id === taskId);
      if (task && task.workers && task.workers.length > 0) {
        switchAppMode('workspace');
        const sId = task.workers[0].session_id;
        const session = state.sessions.find(s => s.id === sId);
        if (session) openSessionInTab(session);
        else activateTab(sId);
      }
    });
  });

  // 4. Merge PR button
  document.querySelectorAll('.btn-merge-pr').forEach(btn => {
    btn.addEventListener('click', async (e) => {
      e.stopPropagation();
      const taskId = btn.getAttribute('data-task-id');
      const prUrl = btn.getAttribute('data-pr-url');
      if (!taskId) {
        if (prUrl) window.open(prUrl, '_blank');
        return;
      }
      const task = boardTasks.find(t => t.id === taskId);
      const taskTitle = task ? task.title : 'this task';
      if (!confirm(`Are you sure you want to squash and merge PR for "${taskTitle}"?`)) {
        return;
      }
      btn.disabled = true;
      btn.textContent = '⏳ Merging...';
      try {
        await mergeTaskPR(taskId, 'squash');
        refreshWorkBoard();
      } catch (err) {
        alert(`Failed to merge PR: ${err.message}`);
        btn.disabled = false;
        btn.textContent = '⚡ Merge PR';
      }
    });
  });
}

export function showNewTaskModal() {
  const topGroups = getDiscoveredTopGroups();
  const defaultGroup = currentGroupFilter !== 'all' ? currentGroupFilter : (topGroups[0] || 'Personal');
  const groupOptionsHtml = topGroups.map(g => `<option value="${escapeHtml(g)}" ${g.toLowerCase() === defaultGroup.toLowerCase() ? 'selected' : ''}>${escapeHtml(g)}</option>`).join('');

  showModal('Create New Task', `
    <div style="display: flex; flex-direction: column; gap: 12px; padding: 4px;">
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">TASK TITLE</label>
        <input type="text" id="mTaskTitle" placeholder="e.g. Implement WebSocket reconnection" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 8px 10px; font-size: 13px;" />
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">ORGANIZATION / GROUP</label>
          <select id="mTaskGroup" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;">
            ${groupOptionsHtml}
          </select>
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">PROJECT NAME</label>
          <input type="text" id="mTaskProject" placeholder="e.g. Ackbar" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">STATUS</label>
          <select id="mTaskStatus" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;">
            <option value="NEW">NEW (Backlog)</option>
            <option value="IN_PROGRESS" selected>IN_PROGRESS (Agent Executing)</option>
            <option value="REVIEW">REVIEW (Human Evaluation)</option>
            <option value="DONE">DONE (Closed / Merged)</option>
          </select>
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">SUBSTATUS</label>
          <input type="text" id="mTaskSubstatus" placeholder="e.g. active, blocked, approved" value="active" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
      </div>
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">SUMMARY & NOTES</label>
        <textarea id="mTaskNotes" rows="3" placeholder="Brief summary of requirements or current state..." style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 8px 10px; font-size: 12px; font-family: inherit; resize: vertical;"></textarea>
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">TRACKER REF (e.g. Jira/Linear)</label>
          <input type="text" id="mTaskRefKey" placeholder="e.g. NGL-409" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">GIT BRANCH</label>
          <input type="text" id="mTaskBranch" placeholder="e.g. feat/ws-reconnect" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
      </div>
    </div>
  `, `
    <button class="btn btn-secondary" id="mBtnCancelTask">Cancel</button>
    <button class="btn btn-primary" id="mBtnSaveTask">Create Task</button>
  `);

  document.getElementById('mBtnCancelTask')?.addEventListener('click', hideModal);
  document.getElementById('mBtnSaveTask')?.addEventListener('click', async () => {
    const title = document.getElementById('mTaskTitle')?.value?.trim();
    if (!title) {
      alert('Task title is required');
      return;
    }
    const groupName = document.getElementById('mTaskGroup')?.value?.trim() || 'Personal';
    const projectName = document.getElementById('mTaskProject')?.value?.trim() || 'General';
    const status = document.getElementById('mTaskStatus')?.value || 'IN_PROGRESS';
    const substatus = document.getElementById('mTaskSubstatus')?.value?.trim() || 'active';
    const notes = document.getElementById('mTaskNotes')?.value?.trim() || '';
    const refKey = document.getElementById('mTaskRefKey')?.value?.trim();
    const branch = document.getElementById('mTaskBranch')?.value?.trim() || '';

    const newTask = {
      title,
      group_name: groupName,
      project_name: projectName,
      status,
      substatus,
      notes,
      branch,
      external_refs: refKey ? [{ tracker: 'jira', ref_key: refKey }] : [],
      workers: [],
      deliverables: []
    };

    try {
      await createTask(newTask);
      hideModal();
      refreshWorkBoard();
    } catch (err) {
      alert(`Error creating task: ${err.message}`);
    }
  });
}

export function showEditTaskModal(task) {
  const topGroups = getDiscoveredTopGroups();
  const currentGroup = task.group_name || 'Personal';
  if (!topGroups.some(g => g.toLowerCase() === currentGroup.toLowerCase())) {
    topGroups.push(currentGroup);
  }
  const groupOptionsHtml = topGroups.map(g => `<option value="${escapeHtml(g)}" ${g.toLowerCase() === currentGroup.toLowerCase() ? 'selected' : ''}>${escapeHtml(g)}</option>`).join('');

  showModal(`Edit Task: ${task.title}`, `
    <div style="display: flex; flex-direction: column; gap: 12px; padding: 4px;">
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">TASK TITLE</label>
        <input type="text" id="mEditTitle" value="${escapeHtml(task.title)}" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 8px 10px; font-size: 13px;" />
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">GROUP / ORGANIZATION</label>
          <select id="mEditGroup" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;">
            ${groupOptionsHtml}
          </select>
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">PROJECT NAME</label>
          <input type="text" id="mEditProject" value="${escapeHtml(task.project_name || '')}" placeholder="e.g. Ackbar" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">STATUS</label>
          <select id="mEditStatus" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;">
            <option value="NEW" ${task.status === 'NEW' ? 'selected' : ''}>NEW (Backlog)</option>
            <option value="IN_PROGRESS" ${task.status === 'IN_PROGRESS' ? 'selected' : ''}>IN_PROGRESS (Agent Executing)</option>
            <option value="REVIEW" ${task.status === 'REVIEW' ? 'selected' : ''}>REVIEW (Human Evaluation)</option>
            <option value="DONE" ${task.status === 'DONE' ? 'selected' : ''}>DONE (Closed / Merged)</option>
          </select>
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">SUBSTATUS</label>
          <input type="text" id="mEditSubstatus" value="${escapeHtml(task.substatus || '')}" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
      </div>
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">SUMMARY & NOTES</label>
        <textarea id="mEditNotes" rows="3" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 8px 10px; font-size: 12px; font-family: inherit; resize: vertical;">${escapeHtml(task.notes || '')}</textarea>
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">GIT BRANCH</label>
          <input type="text" id="mEditBranch" value="${escapeHtml(task.branch || '')}" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">PR URL</label>
          <input type="text" id="mEditPRURL" value="${escapeHtml(task.pr_url || '')}" placeholder="https://github.com/..." style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
      </div>
    </div>
  `, `
    <div style="display: flex; justify-content: space-between; align-items: center; width: 100%;">
      <button class="btn btn-danger btn-xs" id="mBtnDeleteTask" style="background: rgba(239, 68, 68, 0.15); border: 1px solid var(--accent-red); color: var(--accent-red); padding: 6px 12px; border-radius: 4px; font-size: 12px; cursor: pointer;">🗑️ Delete Task</button>
      <div style="display: flex; gap: 8px;">
        <button class="btn btn-secondary" id="mBtnCancelEdit">Cancel</button>
        <button class="btn btn-primary" id="mBtnUpdateTask">Save Changes</button>
      </div>
    </div>
  `);

  document.getElementById('mBtnCancelEdit')?.addEventListener('click', hideModal);

  document.getElementById('mBtnDeleteTask')?.addEventListener('click', async () => {
    if (!confirm(`Are you sure you want to permanently delete task "${task.title}"?`)) {
      return;
    }
    try {
      await deleteTask(task.id);
      hideModal();
      refreshWorkBoard();
    } catch (err) {
      alert(`Error deleting task: ${err.message}`);
    }
  });

  document.getElementById('mBtnUpdateTask')?.addEventListener('click', async () => {
    const titleVal = document.getElementById('mEditTitle')?.value?.trim();
    if (!titleVal) {
      alert('Task title is required');
      return;
    }
    const substatusInput = document.getElementById('mEditSubstatus');
    const updated = {
      ...task,
      title: titleVal,
      group_name: document.getElementById('mEditGroup')?.value?.trim() || task.group_name || 'Personal',
      project_name: document.getElementById('mEditProject')?.value?.trim() || task.project_name || 'General',
      status: document.getElementById('mEditStatus')?.value || task.status,
      substatus: substatusInput ? substatusInput.value.trim() : (task.substatus || ''),
      notes: document.getElementById('mEditNotes')?.value?.trim() || '',
      branch: document.getElementById('mEditBranch')?.value?.trim() || '',
      pr_url: document.getElementById('mEditPRURL')?.value?.trim() || ''
    };

    try {
      await updateTask(updated);
      hideModal();
      refreshWorkBoard();
    } catch (err) {
      alert(`Error updating task: ${err.message}`);
    }
  });
}

export async function showStandupModal() {
  const initialGroup = currentGroupFilter !== 'all' ? currentGroupFilter : '';
  let activeDays = 1;
  let activeGroup = initialGroup;
  let currentMarkdown = '';
  let currentSpoken = '';
  let isVoicePlaying = false;
  let synth = window.speechSynthesis;
  let utterance = null;

  const topGroups = getDiscoveredTopGroups();
  const groupOptionsHtml = `<option value="" ${!activeGroup ? 'selected' : ''}>All Work</option>` +
    topGroups.map(g => {
      const icon = getGroupIcon(g);
      return `<option value="${escapeHtml(g)}" ${activeGroup.toLowerCase() === g.toLowerCase() ? 'selected' : ''}>${icon} ${escapeHtml(g)}</option>`;
    }).join('');

  const bodyHtml = `
    <div style="display: flex; flex-direction: column; gap: 14px; width: 100%;">
      <div style="display: flex; gap: 12px; align-items: center; justify-content: space-between; flex-wrap: wrap;">
        <div style="display: flex; gap: 8px; align-items: center;">
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted);">SCOPE:</label>
          <select id="mStandupGroupSelect" style="background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 6px 10px; font-size: 12px;">
            ${groupOptionsHtml}
          </select>
        </div>
        <div style="display: flex; gap: 8px; align-items: center;">
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted);">WINDOW:</label>
          <select id="mStandupDaysSelect" style="background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 6px 10px; font-size: 12px;">
            <option value="1" selected>Last 24 Hours (Daily Standup)</option>
            <option value="2">Last 48 Hours</option>
            <option value="7">Last 7 Days (Weekly Retro)</option>
          </select>
        </div>
        <div style="display: flex; gap: 6px;">
          <button class="btn btn-secondary btn-xs" id="mBtnToggleVoice" title="Listen to conversational audio briefing">🎙️ Audio Briefing</button>
        </div>
      </div>

      <!-- Spoken Audio Player Card -->
      <div id="mVoicePlayerCard" style="display: none; background: rgba(59, 130, 246, 0.08); border: 1px solid rgba(59, 130, 246, 0.25); border-radius: 6px; padding: 12px; font-size: 13px;">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
          <span style="font-weight: 600; color: var(--accent-blue); display: flex; align-items: center; gap: 6px; font-size: 12px;">
            <span>🔊</span> Conversational Voice Companion Briefing
          </span>
          <div style="display: flex; gap: 6px;">
            <button class="btn btn-primary btn-xs" id="mBtnPlayVoice">▶ Play</button>
            <button class="btn btn-secondary btn-xs" id="mBtnStopVoice" disabled>⏹ Stop</button>
          </div>
        </div>
        <div id="mVoiceText" style="color: var(--text-main); line-height: 1.5; font-size: 12px; max-height: 80px; overflow-y: auto;">
          Loading audio briefing...
        </div>
      </div>

      <!-- Markdown Output Box -->
      <div style="position: relative;">
        <div id="mStandupPreview" style="background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 6px; padding: 14px; max-height: 480px; overflow-y: auto; overflow-x: hidden; word-break: break-word; overflow-wrap: break-word; font-size: 13px; line-height: 1.6; user-select: text;">
          <div style="text-align: center; color: var(--text-muted); padding: 24px;">Generating standup report...</div>
        </div>
      </div>
    </div>
  `;

  const footerHtml = `
    <button class="btn btn-secondary" id="mBtnCloseStandup">Close</button>
    <button class="btn btn-primary" id="mBtnCopyStandup" style="background: var(--accent-blue); border-color: var(--accent-blue); color: #fff;">
      📋 Copy to Clipboard
    </button>
  `;

  showModal('📋 Daily Standup & Work Briefing', bodyHtml, footerHtml, { wide: true });

  async function loadReport() {
    const previewEl = document.getElementById('mStandupPreview');
    const voiceTextEl = document.getElementById('mVoiceText');
    if (previewEl) previewEl.innerHTML = '<div style="text-align: center; color: var(--text-muted); padding: 24px;">Generating standup report...</div>';

    try {
      const report = await fetchStandup(activeGroup, activeDays, 'json');
      currentMarkdown = report.markdown || '';
      currentSpoken = report.spoken_briefing || '';

      if (previewEl) {
        if (window.marked && typeof window.marked.parse === 'function') {
          const rawHtml = window.marked.parse(currentMarkdown);
          const parser = new DOMParser();
          const doc = parser.parseFromString(rawHtml, 'text/html');
          doc.querySelectorAll('script, iframe, object, embed, style').forEach(el => el.remove());
          doc.querySelectorAll('*').forEach(el => {
            for (const attr of Array.from(el.attributes)) {
              if (attr.name.startsWith('on') || (attr.value && attr.value.trim().toLowerCase().startsWith('javascript:'))) {
                el.removeAttribute(attr.name);
              }
            }
          });
          doc.querySelectorAll('pre, code').forEach(el => {
            el.style.whiteSpace = 'pre-wrap';
            el.style.wordBreak = 'break-word';
            el.style.overflowWrap = 'break-word';
          });
          previewEl.replaceChildren(...doc.body.childNodes);
        } else {
          previewEl.innerHTML = `<pre style="white-space: pre-wrap; word-break: break-word; overflow-x: hidden; font-family: monospace; font-size: 12px;">${escapeHtml(currentMarkdown)}</pre>`;
        }
      }
      if (voiceTextEl) {
        voiceTextEl.textContent = currentSpoken || 'No audio briefing available for this period.';
      }
    } catch (err) {
      if (previewEl) {
        previewEl.innerHTML = `<div style="color: var(--accent-red); padding: 16px;">Failed to load standup: ${escapeHtml(err.message)}</div>`;
      }
    }
  }

  // Voice player controls
  function playVoice() {
    if (!synth) {
      alert('Speech synthesis is not supported by your browser.');
      return;
    }
    if (!currentSpoken || !currentSpoken.trim()) {
      return;
    }
    synth.cancel();
    utterance = new SpeechSynthesisUtterance(currentSpoken);
    utterance.rate = 1.05;
    utterance.pitch = 1.0;

    const voices = synth.getVoices();
    const natural = voices.find(v => v.lang && v.lang.startsWith('en') && (v.name.includes('Natural') || v.name.includes('Google') || v.name.includes('Samantha') || v.name.includes('Siri')));
    if (natural) utterance.voice = natural;

    utterance.onend = () => {
      isVoicePlaying = false;
      const playBtn = document.getElementById('mBtnPlayVoice');
      const stopBtn = document.getElementById('mBtnStopVoice');
      if (playBtn) playBtn.disabled = false;
      if (stopBtn) stopBtn.disabled = true;
    };
    utterance.onerror = () => {
      isVoicePlaying = false;
      const playBtn = document.getElementById('mBtnPlayVoice');
      const stopBtn = document.getElementById('mBtnStopVoice');
      if (playBtn) playBtn.disabled = false;
      if (stopBtn) stopBtn.disabled = true;
    };

    isVoicePlaying = true;
    const playBtn = document.getElementById('mBtnPlayVoice');
    const stopBtn = document.getElementById('mBtnStopVoice');
    if (playBtn) playBtn.disabled = true;
    if (stopBtn) stopBtn.disabled = false;
    synth.speak(utterance);
  }

  function stopVoice() {
    if (synth) synth.cancel();
    isVoicePlaying = false;
    const playBtn = document.getElementById('mBtnPlayVoice');
    const stopBtn = document.getElementById('mBtnStopVoice');
    if (playBtn) playBtn.disabled = false;
    if (stopBtn) stopBtn.disabled = true;
  }

  document.getElementById('mStandupGroupSelect')?.addEventListener('change', (e) => {
    activeGroup = e.target.value;
    stopVoice();
    loadReport();
  });

  document.getElementById('mStandupDaysSelect')?.addEventListener('change', (e) => {
    activeDays = parseInt(e.target.value, 10) || 1;
    stopVoice();
    loadReport();
  });

  document.getElementById('mBtnToggleVoice')?.addEventListener('click', () => {
    const card = document.getElementById('mVoicePlayerCard');
    if (card) {
      card.style.display = card.style.display === 'none' ? 'block' : 'none';
    }
  });

  document.getElementById('mBtnPlayVoice')?.addEventListener('click', playVoice);
  document.getElementById('mBtnStopVoice')?.addEventListener('click', stopVoice);

  document.getElementById('mBtnCopyStandup')?.addEventListener('click', async () => {
    if (!currentMarkdown) return;
    try {
      await navigator.clipboard.writeText(currentMarkdown);
      const btn = document.getElementById('mBtnCopyStandup');
      if (btn) {
        const origText = btn.innerHTML;
        btn.innerHTML = '✓ Copied to Clipboard!';
        btn.style.background = 'var(--accent-green)';
        btn.style.borderColor = 'var(--accent-green)';
        setTimeout(() => {
          if (btn) {
            btn.innerHTML = origText;
            btn.style.background = 'var(--accent-blue)';
            btn.style.borderColor = 'var(--accent-blue)';
          }
        }, 2000);
      }
    } catch (err) {
      alert('Failed to copy to clipboard: ' + err.message);
    }
  });

  document.getElementById('mBtnCloseStandup')?.addEventListener('click', () => {
    stopVoice();
    hideModal();
  });

  document.getElementById('modalCloseBtn')?.addEventListener('click', stopVoice, { once: true });

  // Initial load
  loadReport();
}
export async function showAgentSetupModal() {
  let selectedHost = 'all';
  let cachedHostStatuses = [];

  const bodyHtml = `
    <div style="display: flex; flex-direction: column; gap: 14px;">
      <div style="font-size: 13px; color: var(--text-muted); line-height: 1.5;">
        Configure connected AI coding agents to natively report task status, blockers, deliverables, and discovered work to Ackbar using the versioned <code>ackbar mcp</code> server and <code>ackbar-tasks</code> skill.
      </div>
      <div id="mAgentHostTabs" style="display: none; gap: 8px; border-bottom: 1px solid var(--border-color); padding-bottom: 10px; overflow-x: auto;"></div>
      <div id="mAgentCardsContainer" style="display: flex; flex-direction: column; gap: 14px; max-height: 60vh; overflow-y: auto;">
        <div style="text-align: center; color: var(--text-muted); padding: 24px;">Detecting agent configurations across hosts...</div>
      </div>
    </div>
  `;

  const footerHtml = `
    <button class="btn btn-secondary" id="mBtnCloseAgentSetup">Close</button>
    <button class="btn btn-primary" id="mBtnAutoConfigureAgents" style="background: var(--accent-blue); border-color: var(--accent-blue); color: #fff;">
      ⚡ Auto-Configure Agents
    </button>
  `;

  showModal('⚙️ Agent Tool & Skill Setup', bodyHtml, footerHtml);

  function renderAgentCard(a, hostVersion) {
    const icon = a.key.includes('claude') ? '🟠' : a.key.includes('antigravity') ? '🟣' : '🟢';
    const detectedBadge = a.detected
      ? '<span style="background: rgba(34, 197, 94, 0.15); color: var(--accent-green); padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 600;">Detected</span>'
      : '<span style="background: rgba(156, 163, 175, 0.15); color: var(--text-muted); padding: 2px 8px; border-radius: 10px; font-size: 11px;">Not Found</span>';

    const mcpBadge = a.mcp_installed
      ? '<span style="color: var(--accent-green); font-size: 12px; font-weight: 600;">✓ Configured</span>'
      : '<span style="color: var(--accent-red); font-size: 12px; font-weight: 600;">✗ Missing</span>';

    const skillBadge = a.skill_installed
      ? `<span style="color: var(--accent-green); font-size: 12px; font-weight: 600;">✓ Installed (v${escapeHtml(a.skill_version || hostVersion || '')})</span>`
      : '<span style="color: var(--accent-red); font-size: 12px; font-weight: 600;">✗ Missing</span>';

    const driftWarning = a.drift_detected
      ? '<span style="background: rgba(239, 68, 68, 0.12); color: var(--accent-red); border: 1px solid rgba(239, 68, 68, 0.3); padding: 2px 8px; border-radius: 4px; font-size: 11px; font-weight: 600;">⚠️ Drift Detected</span>'
      : '<span style="background: rgba(34, 197, 94, 0.12); color: var(--accent-green); border: 1px solid rgba(34, 197, 94, 0.3); padding: 2px 8px; border-radius: 4px; font-size: 11px; font-weight: 600;">Up to Date</span>';

    return `
      <div style="background: var(--bg-card); border: 1px solid var(--border-color); border-radius: 6px; padding: 14px; display: flex; flex-direction: column; gap: 8px;">
        <div style="display: flex; justify-content: space-between; align-items: center;">
          <div style="display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 14px; color: var(--text-main);">
            <span>${icon}</span> ${escapeHtml(a.display_name)}
            ${detectedBadge}
          </div>
          <div>${driftWarning}</div>
        </div>

        <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-top: 4px; font-size: 12px;">
          <div style="background: var(--bg-subtle, rgba(0,0,0,0.15)); padding: 8px; border-radius: 4px; border: 1px solid var(--border-color);">
            <div style="color: var(--text-muted); font-size: 11px; margin-bottom: 2px;">MCP SERVER</div>
            <div style="margin-bottom: 4px;">${mcpBadge}</div>
            <div style="color: var(--text-muted); font-family: monospace; font-size: 10px; word-break: break-all;">${escapeHtml(a.config_file)}</div>
          </div>
          <div style="background: var(--bg-subtle, rgba(0,0,0,0.15)); padding: 8px; border-radius: 4px; border: 1px solid var(--border-color);">
            <div style="color: var(--text-muted); font-size: 11px; margin-bottom: 2px;">SKILL (SKILL.md)</div>
            <div style="margin-bottom: 4px;">${skillBadge}</div>
            <div style="color: var(--text-muted); font-family: monospace; font-size: 10px; word-break: break-all;">${escapeHtml(a.skills_dir)}/ackbar-tasks</div>
          </div>
        </div>
      </div>
    `;
  }

  function renderHostSections() {
    const container = document.getElementById('mAgentCardsContainer');
    const tabsContainer = document.getElementById('mAgentHostTabs');
    const actionBtn = document.getElementById('mBtnAutoConfigureAgents');
    if (!container) return;

    if (cachedHostStatuses.length === 0) {
      container.innerHTML = '<div style="color: var(--text-muted); padding: 16px;">No connected hosts found.</div>';
      return;
    }

    // Update Host Tabs if multiple hosts
    if (tabsContainer) {
      if (cachedHostStatuses.length > 1) {
        tabsContainer.style.display = 'flex';
        tabsContainer.innerHTML = [
          `<button class="task-group-filter-btn ${selectedHost === 'all' ? 'active' : ''}" data-host-tab="all" style="padding: 4px 12px; font-size: 12px; border-radius: 14px;">🌐 All Hosts (${cachedHostStatuses.length})</button>`,
          ...cachedHostStatuses.map(h => {
            const dot = h.online ? '🟢' : '🔴';
            const icon = h.isSelf ? '💻' : '🖥️';
            const label = h.displayName || h.host;
            return `<button class="task-group-filter-btn ${selectedHost === h.host ? 'active' : ''}" data-host-tab="${escapeHtml(h.host)}" style="padding: 4px 12px; font-size: 12px; border-radius: 14px;">${icon} ${escapeHtml(label)} ${dot}</button>`;
          })
        ].join('');

        tabsContainer.querySelectorAll('button[data-host-tab]').forEach(btn => {
          btn.addEventListener('click', () => {
            selectedHost = btn.getAttribute('data-host-tab');
            renderHostSections();
          });
        });
      } else {
        tabsContainer.style.display = 'none';
      }
    }

    // Update Action Button Text
    if (actionBtn) {
      if (selectedHost === 'all') {
        actionBtn.innerHTML = cachedHostStatuses.length > 1 ? '⚡ Auto-Configure All Hosts' : '⚡ Auto-Configure Agents';
      } else {
        const target = cachedHostStatuses.find(h => h.host === selectedHost);
        const name = target ? (target.displayName || target.host) : 'Host';
        actionBtn.innerHTML = `⚡ Configure ${escapeHtml(name)}`;
      }
    }

    // Filter hosts to render
    const hostsToRender = selectedHost === 'all'
      ? cachedHostStatuses
      : cachedHostStatuses.filter(h => h.host === selectedHost);

    if (hostsToRender.length === 0) {
      container.innerHTML = '<div style="color: var(--text-muted); padding: 16px;">Selected host not found.</div>';
      return;
    }

    container.innerHTML = hostsToRender.map(h => {
      const icon = h.isSelf ? '💻' : '🖥️';
      const label = h.displayName || h.host;
      const statusBadge = h.online
        ? '<span style="background: rgba(34, 197, 94, 0.15); color: var(--accent-green); padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 600;">ONLINE</span>'
        : '<span style="background: rgba(239, 68, 68, 0.15); color: var(--accent-red); padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 600;">OFFLINE</span>';

      const showHostHeader = cachedHostStatuses.length > 1;

      let contentHtml = '';
      if (!h.online) {
        contentHtml = `
          <div style="color: var(--accent-red); font-size: 12px; padding: 14px; background: rgba(239, 68, 68, 0.08); border-radius: 6px; border: 1px dashed rgba(239, 68, 68, 0.3);">
            ⚠️ Host is offline or unreachable: ${escapeHtml(h.error || 'Connection failed')}
          </div>
        `;
      } else if (!h.agents || h.agents.length === 0) {
        contentHtml = '<div style="color: var(--text-muted); padding: 12px;">No supported agents found on this host.</div>';
      } else {
        contentHtml = `
          <div style="display: flex; flex-direction: column; gap: 10px;">
            ${h.agents.map(a => renderAgentCard(a, h.version)).join('')}
          </div>
        `;
      }

      if (!showHostHeader) {
        return contentHtml;
      }

      return `
        <div class="agent-host-section" style="display: flex; flex-direction: column; gap: 12px; background: rgba(255,255,255,0.02); border: 1px solid var(--border-color); border-radius: 8px; padding: 14px;">
          <div style="display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid var(--border-color); padding-bottom: 10px;">
            <div style="display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 14px; color: var(--text-main);">
              <span>${icon}</span>
              <span>${escapeHtml(label)}</span>
              <span style="font-size: 11px; color: var(--text-muted); font-family: monospace;">(${h.url ? escapeHtml(h.url) : 'local'})</span>
              ${statusBadge}
            </div>
            ${h.online ? `
              <button class="btn btn-secondary btn-configure-single-host" data-host-key="${escapeHtml(h.host)}" style="padding: 3px 10px; font-size: 11px;">
                ⚡ Configure Host
              </button>
            ` : ''}
          </div>
          ${contentHtml}
        </div>
      `;
    }).join('');

    // Bind per-host configure buttons
    container.querySelectorAll('.btn-configure-single-host').forEach(btn => {
      btn.addEventListener('click', async () => {
        const hostKey = btn.getAttribute('data-host-key');
        const target = cachedHostStatuses.find(h => h.host === hostKey);
        if (!target) return;

        const origText = btn.innerHTML;
        btn.disabled = true;
        btn.innerHTML = '⚡ Configuring...';
        try {
          await provisionAgents(['all'], target.url);
          btn.innerHTML = '✓ Configured';
          btn.style.color = 'var(--accent-green)';
          await loadStatuses();
        } catch (err) {
          alert(`Failed to configure ${target.displayName || target.host}: ${err.message}`);
          btn.disabled = false;
          btn.innerHTML = origText;
        }
      });
    });
  }

  async function loadStatuses() {
    const container = document.getElementById('mAgentCardsContainer');
    if (!container) return;

    try {
      cachedHostStatuses = await fetchAllAgentStatuses();
      renderHostSections();
    } catch (err) {
      container.innerHTML = `<div style="color: var(--accent-red); padding: 16px;">Failed to load agent statuses: ${escapeHtml(err.message)}</div>`;
    }
  }

  document.getElementById('mBtnAutoConfigureAgents')?.addEventListener('click', async () => {
    const btn = document.getElementById('mBtnAutoConfigureAgents');
    if (!btn) return;
    const origHtml = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '⚡ Configuring Agents...';

    try {
      if (selectedHost === 'all') {
        const results = await provisionAllHosts(['all']);
        const successCount = results.filter(r => r.success).length;
        const totalCount = results.length;
        btn.innerHTML = `✓ Configured ${successCount}/${totalCount} Host(s)`;
      } else {
        const target = cachedHostStatuses.find(h => h.host === selectedHost);
        await provisionAgents(['all'], target ? target.url : '');
        btn.innerHTML = '✓ Configured Successfully!';
      }

      btn.style.background = 'var(--accent-green)';
      btn.style.borderColor = 'var(--accent-green)';
      await loadStatuses();
      setTimeout(() => {
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = origHtml;
          btn.style.background = 'var(--accent-blue)';
          btn.style.borderColor = 'var(--accent-blue)';
        }
      }, 2500);
    } catch (err) {
      alert('Failed to configure agents: ' + err.message);
      btn.disabled = false;
      btn.innerHTML = origHtml;
    }
  });

  document.getElementById('mBtnCloseAgentSetup')?.addEventListener('click', hideModal);

  // Initial load
  loadStatuses();
}

export function initWorkBoard() {
  // Mode switcher listeners
  document.getElementById('btnModeWorkspace')?.addEventListener('click', () => switchAppMode('workspace'));
  document.getElementById('btnModeWorkBoard')?.addEventListener('click', () => switchAppMode('workboard'));

  // Dynamic group filter buttons
  renderGroupFilters();

  // Project filter
  document.getElementById('taskProjectFilter')?.addEventListener('change', (e) => {
    currentProjectFilter = e.target.value;
    renderWorkBoard();
  });

  // Search filter
  document.getElementById('taskSearchInput')?.addEventListener('input', (e) => {
    currentSearchQuery = e.target.value;
    renderWorkBoard();
  });

  // Agent Setup, Standup, Refresh & New Task
  document.getElementById('btnAgentSetup')?.addEventListener('click', showAgentSetupModal);
  document.getElementById('btnDailyStandup')?.addEventListener('click', showStandupModal);
  document.getElementById('btnRefreshWorkBoard')?.addEventListener('click', refreshWorkBoard);
  document.getElementById('btnNewTask')?.addEventListener('click', showNewTaskModal);
}
