import test from 'node:test';
import assert from 'node:assert/strict';
import {
  VALID_TASK_STATUSES,
  isInteractiveCardElement,
  computeStatusTransitionPatch,
  isTaskDragging,
  initKanbanDragAndDrop,
} from './tasks-dnd.js';

class ClassList {
  constructor() {
    this._set = new Set();
  }
  add(...classes) {
    for (const c of classes) {
      if (c) this._set.add(c);
    }
  }
  remove(...classes) {
    for (const c of classes) {
      this._set.delete(c);
    }
  }
  contains(cls) {
    return this._set.has(cls);
  }
  has(cls) {
    return this._set.has(cls);
  }
}

class MockElement {
  constructor(tagName = 'div', attrs = {}) {
    this.tagName = tagName.toUpperCase();
    this.attrs = { ...attrs };
    this.dataset = {};
    this.classList = new ClassList();
    this.children = [];
    this.parentNode = null;
    this.listeners = new Map();
  }

  getAttribute(name) {
    return this.attrs[name] ?? null;
  }

  setAttribute(name, value) {
    this.attrs[name] = String(value);
  }

  removeAttribute(name) {
    delete this.attrs[name];
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  contains(target) {
    let cur = target;
    while (cur) {
      if (cur === this) return true;
      cur = cur.parentNode;
    }
    return false;
  }

  closest(selector) {
    let cur = this;
    while (cur) {
      if (cur._matches(selector)) return cur;
      cur = cur.parentNode;
    }
    return null;
  }

  querySelectorAll(selector) {
    const res = [];
    const walk = (node) => {
      for (const child of node.children) {
        if (child._matches(selector)) res.push(child);
        walk(child);
      }
    };
    walk(this);
    return res;
  }

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) {
      this.listeners.set(type, []);
    }
    this.listeners.get(type).push(listener);
  }

  dispatchEvent(event) {
    const list = this.listeners.get(event.type) || [];
    for (const listener of list) {
      listener(event);
    }
  }

  _matches(selector) {
    if (!selector) return false;
    if (selector.includes(',')) {
      return selector.split(',').some((s) => this._matches(s.trim()));
    }
    let s = selector;
    const tagMatch = s.match(/^[a-zA-Z0-9]+/);
    if (tagMatch) {
      if (this.tagName !== tagMatch[0].toUpperCase()) return false;
      s = s.slice(tagMatch[0].length);
    }
    const classMatches = s.match(/\.[a-zA-Z0-9_-]+/g);
    if (classMatches) {
      for (const cls of classMatches) {
        if (!this.classList.has(cls.slice(1))) return false;
      }
      s = s.replace(/\.[a-zA-Z0-9_-]+/g, '');
    }
    const attrMatches = s.match(/\[([a-zA-Z0-9_-]+)(?:="([^"]*)")?\]/g);
    if (attrMatches) {
      for (const attr of attrMatches) {
        const m = attr.match(/\[([a-zA-Z0-9_-]+)(?:="([^"]*)")?\]/);
        const name = m[1];
        const val = m[2];
        if (val === undefined) {
          if (this.attrs[name] === undefined && this.dataset[name.replace(/^data-/, '')] === undefined) return false;
        } else {
          const actual = this.attrs[name] ?? this.dataset[name.replace(/^data-/, '')];
          if (actual !== val) return false;
        }
      }
    }
    return true;
  }
}

class MockDataTransfer {
  constructor(types = []) {
    this.types = types;
    this.data = new Map();
    this.effectAllowed = 'none';
    this.dropEffect = 'none';
  }
  setData(format, data) {
    this.data.set(format, data);
    if (!this.types.includes(format)) this.types.push(format);
  }
  getData(format) {
    return this.data.get(format) || '';
  }
}

class MockDragEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.target = init.target || null;
    this.relatedTarget = init.relatedTarget || null;
    this.dataTransfer = init.dataTransfer || null;
    this.defaultPrevented = false;
  }
  preventDefault() {
    this.defaultPrevented = true;
  }
}

test('VALID_TASK_STATUSES contains all 4 pillars', () => {
  assert.deepEqual(VALID_TASK_STATUSES, ['NEW', 'IN_PROGRESS', 'REVIEW', 'DONE']);
});

test('isInteractiveCardElement correctly identifies interactive elements', () => {
  const card = new MockElement('div', { class: 'task-card' });
  const btn = new MockElement('button');
  const spanInBtn = new MockElement('span');
  btn.appendChild(spanInBtn);
  const link = new MockElement('a');
  const input = new MockElement('input');
  const select = new MockElement('select');
  const textarea = new MockElement('textarea');
  const roleBtn = new MockElement('div', { role: 'button' });
  const plainDiv = new MockElement('div');
  card.appendChild(btn);
  card.appendChild(link);
  card.appendChild(plainDiv);

  assert.equal(isInteractiveCardElement(btn), true);
  assert.equal(isInteractiveCardElement(spanInBtn), true);
  assert.equal(isInteractiveCardElement(link), true);
  assert.equal(isInteractiveCardElement(input), true);
  assert.equal(isInteractiveCardElement(select), true);
  assert.equal(isInteractiveCardElement(textarea), true);
  assert.equal(isInteractiveCardElement(roleBtn), true);
  assert.equal(isInteractiveCardElement(plainDiv), false);
  assert.equal(isInteractiveCardElement(card), false);
  assert.equal(isInteractiveCardElement(null), false);
});

test('computeStatusTransitionPatch transitions NEW to IN_PROGRESS', () => {
  const task = { id: 't1', status: 'NEW', substatus: 'ready', blocker_question: 'old q' };
  const res = computeStatusTransitionPatch(task, 'IN_PROGRESS', '2026-11-13T00:00:00Z');
  assert.ok(res);
  assert.deepEqual(res.patch, { id: 't1', status: 'IN_PROGRESS', substatus: 'active' });
  assert.equal(res.optimisticTask.status, 'IN_PROGRESS');
  assert.equal(res.optimisticTask.substatus, 'active');
  assert.equal(res.optimisticTask.completed_at, null);
  assert.equal(res.optimisticTask.blocker_question, '');
});

test('computeStatusTransitionPatch transitions IN_PROGRESS to REVIEW across kinds and PRs', () => {
  const codeTask = { id: 't2', status: 'IN_PROGRESS', task_kind: 'code' };
  const codeRes = computeStatusTransitionPatch(codeTask, 'REVIEW');
  assert.equal(codeRes.patch.substatus, 'in_review');
  assert.equal(codeRes.optimisticTask.completed_at, null);

  const prTask = { id: 't3', status: 'IN_PROGRESS', pr_url: 'https://github.com/org/repo/pull/1' };
  const prRes = computeStatusTransitionPatch(prTask, 'REVIEW');
  assert.equal(prRes.patch.substatus, 'pr_review');

  const designTask = { id: 't4', status: 'IN_PROGRESS', task_kind: 'design' };
  const designRes = computeStatusTransitionPatch(designTask, 'REVIEW');
  assert.equal(designRes.patch.substatus, 'design_review');
});

test('computeStatusTransitionPatch transitions REVIEW to DONE', () => {
  const task = {
    id: 't5',
    status: 'REVIEW',
    substatus: 'pr_review',
    blocker_question: 'need answer',
    workers: [{ session_id: 's1', is_active: true }, { session_id: 's2', is_active: false }],
  };
  const now = '2026-11-13T01:00:00Z';
  const res = computeStatusTransitionPatch(task, 'DONE', now);
  assert.ok(res);
  assert.deepEqual(res.patch, { id: 't5', status: 'DONE', substatus: 'completed' });
  assert.equal(res.optimisticTask.completed_at, now);
  assert.equal(res.optimisticTask.blocker_question, '');
  assert.deepEqual(res.optimisticTask.workers, [
    { session_id: 's1', is_active: false },
    { session_id: 's2', is_active: false },
  ]);
});

test('computeStatusTransitionPatch reopens DONE task to IN_PROGRESS or NEW', () => {
  const task = {
    id: 't6',
    status: 'DONE',
    substatus: 'completed',
    completed_at: '2026-11-12T12:00:00Z',
    workstreams: [
      { repo_node_path: 'backend', status: 'DONE' },
      { repo_node_path: 'frontend', status: 'DONE' },
    ],
  };
  const now = '2026-11-13T02:00:00Z';
  const resProg = computeStatusTransitionPatch(task, 'IN_PROGRESS', now);
  assert.ok(resProg);
  assert.equal(resProg.optimisticTask.completed_at, null);
  assert.equal(resProg.optimisticTask.workstreams[0].status, 'IN_PROGRESS');
  assert.equal(resProg.optimisticTask.workstreams[1].status, 'IN_PROGRESS');
  assert.equal(resProg.optimisticTask.workstreams[0].updated_at, now);

  const resNew = computeStatusTransitionPatch(task, 'NEW', now);
  assert.ok(resNew);
  assert.equal(resNew.optimisticTask.substatus, 'ready');
  assert.equal(resNew.optimisticTask.completed_at, null);
  assert.equal(resNew.optimisticTask.workstreams[0].status, 'NEW');
});

test('computeStatusTransitionPatch returns null for identical status or invalid input', () => {
  const task = { id: 't7', status: 'IN_PROGRESS' };
  assert.equal(computeStatusTransitionPatch(task, 'IN_PROGRESS'), null);
  assert.equal(computeStatusTransitionPatch(task, 'in_progress'), null);
  assert.equal(computeStatusTransitionPatch(task, 'UNKNOWN_STATUS'), null);
  assert.equal(computeStatusTransitionPatch({ status: 'NEW' }, 'DONE'), null);
  assert.equal(computeStatusTransitionPatch(null, 'DONE'), null);
});

function setupBoardDOM() {
  const grid = new MockElement('div', { class: 'kanban-grid' });
  const colNew = new MockElement('div', { 'data-status': 'NEW' });
  colNew.classList.add('kanban-column');
  const colProg = new MockElement('div', { 'data-status': 'IN_PROGRESS' });
  colProg.classList.add('kanban-column');
  const card = new MockElement('div', { 'data-task-id': 'task-100', 'data-task-status': 'NEW' });
  card.classList.add('task-card');
  const cardBtn = new MockElement('button');
  card.appendChild(cardBtn);
  colNew.appendChild(card);
  grid.appendChild(colNew);
  grid.appendChild(colProg);
  return { grid, colNew, colProg, card, cardBtn };
}

test('initKanbanDragAndDrop handles full drag, hover, drop and dragend lifecycle', () => {
  const { grid, colNew, colProg, card, cardBtn } = setupBoardDOM();

  let droppedTask = null;
  let droppedStatus = null;
  let dragEnded = false;

  initKanbanDragAndDrop(grid, {
    onDropTask: (taskId, status) => {
      droppedTask = taskId;
      droppedStatus = status;
    },
    onDragEnd: () => {
      dragEnded = true;
    },
  });

  // 1. Mousedown on interactive child blocks drag
  grid.dispatchEvent(new MockDragEvent('mousedown', { target: cardBtn }));
  const blockedDragStart = new MockDragEvent('dragstart', {
    target: cardBtn,
    dataTransfer: new MockDataTransfer(),
  });
  grid.dispatchEvent(blockedDragStart);
  assert.equal(blockedDragStart.defaultPrevented, true);
  assert.equal(isTaskDragging(), false);
  assert.equal(card.classList.contains('dragging'), false);

  // Mouseup resets control flag
  grid.dispatchEvent(new MockDragEvent('mouseup', { target: cardBtn }));

  // 2. Dragstart on card succeeds
  const dt = new MockDataTransfer();
  const validDragStart = new MockDragEvent('dragstart', { target: card, dataTransfer: dt });
  grid.dispatchEvent(validDragStart);
  assert.equal(validDragStart.defaultPrevented, false);
  assert.equal(isTaskDragging(), true);
  assert.equal(card.classList.contains('dragging'), true);
  assert.equal(dt.getData('application/x-ackbar-task-id'), 'task-100');

  // 3. Dragover on same column sets dropEffect = none
  const dragOverSame = new MockDragEvent('dragover', { target: colNew, dataTransfer: dt });
  grid.dispatchEvent(dragOverSame);
  assert.equal(dragOverSame.defaultPrevented, true);
  assert.equal(dt.dropEffect, 'none');
  assert.equal(colNew.classList.contains('drag-over'), false);

  // 4. Dragover on target column sets dropEffect = move and highlights
  const dragOverTarget = new MockDragEvent('dragover', { target: colProg, dataTransfer: dt });
  grid.dispatchEvent(dragOverTarget);
  assert.equal(dragOverTarget.defaultPrevented, true);
  assert.equal(dt.dropEffect, 'move');
  assert.equal(colProg.classList.contains('drag-over'), true);

  // 5. Dragleave to internal child does not clear highlight
  const internalChild = new MockElement('div');
  colProg.appendChild(internalChild);
  grid.dispatchEvent(new MockDragEvent('dragleave', { target: colProg, relatedTarget: internalChild }));
  assert.equal(colProg.classList.contains('drag-over'), true);

  // 6. Drop triggers onDropTask and resets dragging state
  const dropEvent = new MockDragEvent('drop', { target: colProg, dataTransfer: dt });
  grid.dispatchEvent(dropEvent);
  assert.equal(dropEvent.defaultPrevented, true);
  assert.equal(droppedTask, 'task-100');
  assert.equal(droppedStatus, 'IN_PROGRESS');
  assert.equal(isTaskDragging(), false);
  assert.equal(colProg.classList.contains('drag-over'), false);
  assert.equal(card.classList.contains('dragging'), false);

  // 7. Dragend calls onDragEnd
  grid.dispatchEvent(new MockDragEvent('dragend'));
  assert.equal(dragEnded, true);
});

test('initKanbanDragAndDrop ignores external text drop without task id', () => {
  const { grid, colProg } = setupBoardDOM();
  let dropped = false;
  initKanbanDragAndDrop(grid, {
    onDropTask: () => {
      dropped = true;
    },
  });

  const foreignDt = new MockDataTransfer(['text/plain']);
  foreignDt.setData('text/plain', 'random external text');

  const dragOverForeign = new MockDragEvent('dragover', { target: colProg, dataTransfer: foreignDt });
  grid.dispatchEvent(dragOverForeign);
  assert.equal(dragOverForeign.defaultPrevented, false);
  assert.equal(colProg.classList.contains('drag-over'), false);

  const dropForeign = new MockDragEvent('drop', { target: colProg, dataTransfer: foreignDt });
  grid.dispatchEvent(dropForeign);
  assert.equal(dropped, false);
});
