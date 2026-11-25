import { escapeHtml } from './utils.js';

const VALID_STATUSES = new Set(['pending', 'in_progress', 'done', 'skipped', 'failed']);

const STATUS_META = {
  done: { iconClass: 'todo-icon-completed', iconChar: '✓' },
  in_progress: { iconClass: 'todo-icon-inprogress', iconChar: '▶' },
  pending: { iconClass: 'todo-icon-pending', iconChar: '○' },
  skipped: { iconClass: 'todo-icon-skipped', iconChar: '–' },
  failed: { iconClass: 'todo-icon-failed', iconChar: '✕' }
};

function normalizeStatus(rawStatus) {
  const st = String(rawStatus || 'pending').toLowerCase().trim();
  if (VALID_STATUSES.has(st)) return st;
  if (st === 'completed') return 'done';
  if (st === 'inprogress' || st === 'running') return 'in_progress';
  if (st === 'cancelled' || st === 'canceled') return 'skipped';
  if (st === 'error') return 'failed';
  return 'pending';
}

export function normalizeChecklistItems(rawChecklist) {
  if (!Array.isArray(rawChecklist)) return [];
  const items = [];
  for (const raw of rawChecklist) {
    if (!raw || typeof raw !== 'object') continue;
    const title = String(raw.title || raw.content || raw.text || raw.name || '').trim();
    if (!title) continue;
    const activeForm = String(raw.active_form || raw.activeForm || '').trim();
    const id = (raw.id !== undefined && raw.id !== null) ? String(raw.id).trim() : '';
    items.push({
      id,
      title,
      activeForm,
      status: normalizeStatus(raw.status)
    });
  }
  return items;
}

export function renderChecklistCard(checklist) {
  const items = normalizeChecklistItems(checklist);
  if (items.length === 0) return null;

  const doneCount = items.filter((it) => it.status === 'done').length;
  const pct = Math.round((doneCount / items.length) * 100);
  const card = document.createElement('div');
  card.className = 'chat-todo-card';
  card.dataset.checklistSig = JSON.stringify(items);

  card.innerHTML = `
    <div class="chat-todo-header">
      <span>📋</span>
      <span>Task Progress (${doneCount}/${items.length})</span>
      <span class="chat-todo-pct">${pct}%</span>
    </div>
    <div class="chat-todo-progress-track">
      <div class="chat-todo-progress-fill" style="width: ${pct}%"></div>
    </div>
    <div class="chat-todo-list">
      ${items.map((item) => {
        const meta = STATUS_META[item.status] || STATUS_META.pending;
        const label = (item.status === 'in_progress' && item.activeForm) ? item.activeForm : item.title;
        return `
          <div class="chat-todo-item ${item.status}">
            <span class="chat-todo-status-icon ${meta.iconClass}">${meta.iconChar}</span>
            <span class="chat-todo-text">${escapeHtml(label)}</span>
          </div>
        `;
      }).join('')}
    </div>
  `;
  return card;
}

export function upsertTurnChecklistCard(msgEl, checklist) {
  if (!msgEl) return null;
  const existing = msgEl.querySelector('.chat-todo-card');
  const card = renderChecklistCard(checklist);
  if (!card) {
    if (existing) existing.remove();
    return null;
  }
  if (existing) {
    existing.replaceWith(card);
    return card;
  }
  const slot = msgEl.querySelector('.chat-tools-slot') || msgEl.querySelector('.chat-tools');
  if (slot) {
    slot.appendChild(card);
    return card;
  }
  const bodyEl = msgEl.querySelector('.chat-msg-body');
  if (bodyEl) {
    msgEl.insertBefore(card, bodyEl);
  } else {
    msgEl.appendChild(card);
  }
  return card;
}
