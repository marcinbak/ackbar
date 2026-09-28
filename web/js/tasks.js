// Ackbar Work & Fleet Overview Dashboard (Kanban Board)
import { state } from './state.js';
import { fetchTasks, createTask, updateTask, sendTaskEvent } from './api.js';
import { escapeHtml } from './utils.js';
import { activateTab, openSessionInTab } from './tabs.js';
import { showModal, hideModal } from './modals.js';

let boardTasks = [];
let currentGroupFilter = 'all';
let currentProjectFilter = 'all';
let currentSearchQuery = '';
let currentMode = 'workspace';

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
  updateProjectFilterDropdown();
  renderWorkBoard();
}

function updateProjectFilterDropdown() {
  const select = document.getElementById('taskProjectFilter');
  if (!select) return;

  const previousValue = select.value || 'all';
  const projects = new Set();
  boardTasks.forEach(t => {
    if (t.project_name) projects.add(t.project_name);
  });

  let html = '<option value="all">All Projects</option>';
  Array.from(projects).sort().forEach(p => {
    const selected = p === previousValue ? 'selected' : '';
    html += `<option value="${escapeHtml(p)}" ${selected}>${escapeHtml(p)}</option>`;
  });
  select.innerHTML = html;
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
    // 1. Group Filter (e.g. Modemobile vs Personal)
    if (currentGroupFilter !== 'all') {
      const g = (t.group_name || '').toLowerCase();
      if (currentGroupFilter === 'work') {
        if (!g.includes('modemobile') && !g.includes('work') && g !== 'mode') return false;
      } else if (currentGroupFilter === 'personal') {
        if (g.includes('modemobile') || g.includes('work') || g === 'mode') return false;
      } else if (g !== currentGroupFilter.toLowerCase()) {
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

  const columns = {
    NEW: [],
    IN_PROGRESS: [],
    REVIEW: [],
    DONE: []
  };

  filtered.forEach(task => {
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
      actionButtons += `<div class="task-pr-bar">
        <a href="${escapeHtml(cleanPrUrl)}" target="_blank" rel="noopener noreferrer" class="task-pr-link">
          🐙 PR #${escapeHtml(String(task.pr_number || ''))} (${escapeHtml(task.pr_state || 'OPEN')})
        </a>
        ${task.substatus === 'approved' ? `<button class="btn btn-success btn-xs btn-merge-pr" data-pr-url="${escapeHtml(cleanPrUrl)}">⚡ Merge PR</button>` : ''}
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
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const prUrl = btn.getAttribute('data-pr-url');
      if (prUrl) window.open(prUrl, '_blank');
    });
  });
}

export function showNewTaskModal() {
  showModal('Create New Task', `
    <div style="display: flex; flex-direction: column; gap: 12px; padding: 4px;">
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">TASK TITLE</label>
        <input type="text" id="mTaskTitle" placeholder="e.g. Implement WebSocket reconnection" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 8px 10px; font-size: 13px;" />
      </div>
      <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">ORGANIZATION / GROUP</label>
          <input type="text" id="mTaskGroup" placeholder="e.g. Modemobile" value="Modemobile" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
        </div>
        <div>
          <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">PROJECT NAME</label>
          <input type="text" id="mTaskProject" placeholder="e.g. NGL" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
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
    const groupName = document.getElementById('mTaskGroup')?.value?.trim() || 'Modemobile';
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
  showModal(`Edit Task: ${escapeHtml(task.title)}`, `
    <div style="display: flex; flex-direction: column; gap: 12px; padding: 4px;">
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">TASK TITLE</label>
        <input type="text" id="mEditTitle" value="${escapeHtml(task.title)}" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 8px 10px; font-size: 13px;" />
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
    <button class="btn btn-secondary" id="mBtnCancelEdit">Cancel</button>
    <button class="btn btn-primary" id="mBtnUpdateTask">Save Changes</button>
  `);

  document.getElementById('mBtnCancelEdit')?.addEventListener('click', hideModal);
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

export function initWorkBoard() {
  // Mode switcher listeners
  document.getElementById('btnModeWorkspace')?.addEventListener('click', () => switchAppMode('workspace'));
  document.getElementById('btnModeWorkBoard')?.addEventListener('click', () => switchAppMode('workboard'));

  // Group filter buttons
  const groupBtns = document.querySelectorAll('.task-group-filter-btn');
  groupBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      groupBtns.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      currentGroupFilter = btn.getAttribute('data-group') || 'all';
      renderWorkBoard();
    });
  });

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

  // Refresh & New Task
  document.getElementById('btnRefreshWorkBoard')?.addEventListener('click', refreshWorkBoard);
  document.getElementById('btnNewTask')?.addEventListener('click', showNewTaskModal);
}
