export const VALID_TASK_STATUSES = ['NEW', 'IN_PROGRESS', 'REVIEW', 'DONE'];

let activeDragState = null;

export function isInteractiveCardElement(target) {
  if (!target || typeof target.closest !== 'function') return false;
  return Boolean(target.closest('button, a, input, select, textarea, [role="button"], [data-move-task-id]'));
}

export function computeStatusTransitionPatch(task, targetStatus, nowIso = new Date().toISOString()) {
  const currentStatus = (task?.status || 'NEW').toUpperCase();
  const nextStatus = String(targetStatus || '').toUpperCase();

  if (!task?.id || !VALID_TASK_STATUSES.includes(nextStatus) || currentStatus === nextStatus) {
    return null;
  }

  let substatus = 'active';
  let completedAt = null;
  let blockerQuestion = task.blocker_question || '';

  if (nextStatus === 'NEW') {
    substatus = 'ready';
    completedAt = null;
    blockerQuestion = '';
  } else if (nextStatus === 'IN_PROGRESS') {
    substatus = 'active';
    completedAt = null;
    blockerQuestion = '';
  } else if (nextStatus === 'REVIEW') {
    substatus = task.pr_url ? 'pr_review' : (task.task_kind === 'design' ? 'design_review' : 'in_review');
    completedAt = null;
  } else if (nextStatus === 'DONE') {
    substatus = 'completed';
    completedAt = nowIso;
    blockerQuestion = '';
  }

  const optimisticTask = {
    ...task,
    status: nextStatus,
    substatus,
    updated_at: nowIso,
    completed_at: completedAt,
    blocker_question: blockerQuestion,
  };

  if (nextStatus === 'DONE' && Array.isArray(task.workers)) {
    optimisticTask.workers = task.workers.map((w) => ({ ...w, is_active: false }));
  }

  if (currentStatus === 'DONE' && nextStatus !== 'DONE' && Array.isArray(task.workstreams)) {
    optimisticTask.workstreams = task.workstreams.map((ws) => {
      if (ws.status === 'DONE') {
        return { ...ws, status: nextStatus, updated_at: nowIso };
      }
      return { ...ws };
    });
  }

  return {
    patch: { id: task.id, status: nextStatus, substatus },
    optimisticTask,
  };
}

export function isTaskDragging() {
  return activeDragState !== null;
}

export function initKanbanDragAndDrop(gridEl, { onDropTask, onDragEnd } = {}) {
  if (!gridEl || gridEl.dataset.dndBound === 'true') return;
  gridEl.dataset.dndBound = 'true';

  let pointerStartedOnControl = false;

  gridEl.addEventListener('mousedown', (e) => {
    pointerStartedOnControl = isInteractiveCardElement(e.target);
  });
  gridEl.addEventListener('mouseup', () => {
    pointerStartedOnControl = false;
  });

  gridEl.addEventListener('dragstart', (e) => {
    const card = e.target?.closest?.('.task-card[data-task-id]');
    if (!card) return;
    if (pointerStartedOnControl || isInteractiveCardElement(e.target)) {
      e.preventDefault();
      return;
    }

    const taskId = card.getAttribute('data-task-id');
    const sourceStatus = (card.getAttribute('data-task-status') || 'NEW').toUpperCase();
    activeDragState = { taskId, sourceStatus };

    card.classList.add('dragging');
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('application/x-ackbar-task-id', taskId);
      e.dataTransfer.setData('text/plain', taskId);
    }
  });

  gridEl.addEventListener('dragover', (e) => {
    const col = e.target?.closest?.('.kanban-column[data-status]');
    if (!col) return;

    const types = e.dataTransfer?.types ? Array.from(e.dataTransfer.types) : [];
    if (!activeDragState && !types.includes('application/x-ackbar-task-id')) return;

    e.preventDefault();
    const targetStatus = (col.getAttribute('data-status') || '').toUpperCase();

    if (!activeDragState || activeDragState.sourceStatus !== targetStatus) {
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
      gridEl.querySelectorAll('.kanban-column.drag-over').forEach((c) => {
        if (c !== col) c.classList.remove('drag-over');
      });
      col.classList.add('drag-over');
    } else {
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'none';
      col.classList.remove('drag-over');
    }
  });

  gridEl.addEventListener('dragleave', (e) => {
    const col = e.target?.closest?.('.kanban-column[data-status]');
    if (col && (!e.relatedTarget || !col.contains(e.relatedTarget))) {
      col.classList.remove('drag-over');
    }
  });

  gridEl.addEventListener('drop', (e) => {
    const col = e.target?.closest?.('.kanban-column[data-status]');
    gridEl.querySelectorAll('.kanban-column.drag-over').forEach((c) => c.classList.remove('drag-over'));
    gridEl.querySelectorAll('.task-card.dragging').forEach((c) => c.classList.remove('dragging'));

    if (!col) {
      activeDragState = null;
      pointerStartedOnControl = false;
      return;
    }

    const taskId = e.dataTransfer?.getData?.('application/x-ackbar-task-id') || activeDragState?.taskId || '';
    e.preventDefault();
    const targetStatus = (col.getAttribute('data-status') || '').toUpperCase();
    activeDragState = null;
    pointerStartedOnControl = false;

    if (taskId) {
      onDropTask?.(taskId, targetStatus);
    }
  });

  gridEl.addEventListener('dragend', () => {
    gridEl.querySelectorAll('.kanban-column.drag-over').forEach((c) => c.classList.remove('drag-over'));
    gridEl.querySelectorAll('.task-card.dragging').forEach((c) => c.classList.remove('dragging'));
    activeDragState = null;
    pointerStartedOnControl = false;
    onDragEnd?.();
  });
}
