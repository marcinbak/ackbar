import test from 'node:test';
import assert from 'node:assert/strict';

class ClassList {
  constructor(el) {
    this.el = el;
    this.set = new Set();
  }
  add(...classes) {
    for (const c of classes) {
      if (c) this.set.add(c);
    }
    this._sync();
  }
  remove(...classes) {
    for (const c of classes) {
      this.set.delete(c);
    }
    this._sync();
  }
  contains(cls) {
    return this.set.has(cls);
  }
  _sync() {
    this.el._className = Array.from(this.set).join(' ');
  }
}

class MockElement {
  constructor(tagName = 'div') {
    this.tagName = tagName.toUpperCase();
    this._className = '';
    this.classList = new ClassList(this);
    this.dataset = {};
    this.style = {};
    this.children = [];
    this.parentNode = null;
    this._innerHTML = '';
    this._textContent = '';
    this.listeners = new Map();
  }

  get className() {
    return this._className;
  }
  set className(val) {
    this._className = val || '';
    this.classList.set.clear();
    for (const c of this._className.split(/\s+/)) {
      if (c) this.classList.set.add(c);
    }
  }

  get isConnected() {
    return Boolean(this.parentNode);
  }

  get innerHTML() {
    return this._innerHTML;
  }
  set innerHTML(html) {
    this._innerHTML = html;
    for (const c of this.children) c.parentNode = null;
    this.children = html ? parseHtmlFragment(html, this) : [];
  }

  get textContent() {
    if (this.children.length === 0) return this._textContent;
    return this.children.map((c) => c.textContent).join(' ');
  }
  set textContent(txt) {
    this._textContent = txt;
    for (const c of this.children) c.parentNode = null;
    this.children = [];
  }

  appendChild(child) {
    if (!child) return child;
    if (child.parentNode) child.parentNode.removeChild(child);
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(newChild, refChild) {
    if (!newChild) return newChild;
    if (!refChild) return this.appendChild(newChild);
    if (newChild.parentNode) newChild.parentNode.removeChild(newChild);
    const idx = this.children.indexOf(refChild);
    if (idx === -1) return this.appendChild(newChild);
    newChild.parentNode = this;
    this.children.splice(idx, 0, newChild);
    return newChild;
  }

  removeChild(child) {
    const idx = this.children.indexOf(child);
    if (idx !== -1) {
      this.children.splice(idx, 1);
      child.parentNode = null;
    }
    return child;
  }

  replaceWith(...nodes) {
    if (!this.parentNode) return;
    for (const node of nodes) {
      this.parentNode.insertBefore(node, this);
    }
    this.remove();
  }

  remove() {
    if (this.parentNode) this.parentNode.removeChild(this);
  }

  addEventListener() {}

  querySelector(sel) {
    const all = this.querySelectorAll(sel);
    return all.length > 0 ? all[0] : null;
  }

  querySelectorAll(sel) {
    const res = [];
    const walk = (node) => {
      for (const child of node.children) {
        if (matchesSelector(child, sel)) res.push(child);
        walk(child);
      }
    };
    walk(this);
    return res;
  }
}

function matchesSelector(el, sel) {
  if (!el || !sel) return false;
  for (const part of sel.split(/(?=[.#])/)) {
    if (!part) continue;
    if (part.startsWith('.')) {
      if (!el.classList.contains(part.slice(1))) return false;
    } else if (part.startsWith('#')) {
      if (el.id !== part.slice(1)) return false;
    } else if (el.tagName !== part.toUpperCase()) {
      return false;
    }
  }
  return true;
}

function parseHtmlFragment(html, parent) {
  const elements = [];
  let i = 0;
  while (i < html.length) {
    const openIdx = html.indexOf('<', i);
    if (openIdx === -1) {
      const text = html.slice(i).trim();
      if (text && elements.length === 0) {
        const textNode = new MockElement('span');
        textNode.parentNode = parent;
        textNode._textContent = text;
        elements.push(textNode);
      }
      break;
    }
    const closeAngle = html.indexOf('>', openIdx);
    if (closeAngle === -1) break;
    const tagContent = html.slice(openIdx + 1, closeAngle).trim();
    if (tagContent.startsWith('/') || tagContent.startsWith('!')) {
      i = closeAngle + 1;
      continue;
    }
    const spaceIdx = tagContent.search(/\s/);
    const tagName = (spaceIdx === -1 ? tagContent : tagContent.slice(0, spaceIdx)).toLowerCase();
    const attrs = spaceIdx === -1 ? '' : tagContent.slice(spaceIdx);

    const el = new MockElement(tagName);
    el.parentNode = parent;
    const classMatch = attrs.match(/class=["']([^"']+)["']/);
    if (classMatch) el.className = classMatch[1];
    const styleMatch = attrs.match(/style=["']([^"']+)["']/);
    if (styleMatch) {
      for (const part of styleMatch[1].split(';')) {
        const colon = part.indexOf(':');
        if (colon !== -1) {
          const k = part.slice(0, colon).trim();
          const v = part.slice(colon + 1).trim();
          if (k && v) el.style[k] = v;
        }
      }
    }

    let depth = 1;
    let searchPos = closeAngle + 1;
    let endTagPos = -1;
    const openRegex = new RegExp('<' + tagName + '(\\s[^>]*)?>', 'gi');
    const closeRegex = new RegExp('</' + tagName + '\\s*>', 'gi');
    while (depth > 0) {
      openRegex.lastIndex = searchPos;
      closeRegex.lastIndex = searchPos;
      const nextOpen = openRegex.exec(html);
      const nextClose = closeRegex.exec(html);
      if (!nextClose) {
        endTagPos = html.length;
        break;
      }
      if (nextOpen && nextOpen.index < nextClose.index) {
        depth++;
        searchPos = openRegex.lastIndex;
      } else {
        depth--;
        if (depth === 0) {
          endTagPos = nextClose.index;
          searchPos = closeRegex.lastIndex;
        } else {
          searchPos = closeRegex.lastIndex;
        }
      }
    }
    const inner = html.slice(closeAngle + 1, endTagPos);
    el._innerHTML = inner;
    if (inner && inner.includes('<')) {
      el.children = parseHtmlFragment(inner, el);
    } else if (inner) {
      el._textContent = inner.trim();
    }
    elements.push(el);
    i = searchPos;
  }
  return elements;
}

const origSetInterval = globalThis.setInterval;
globalThis.setInterval = (fn, ms, ...args) => {
  const t = origSetInterval(fn, ms, ...args);
  if (t && typeof t.unref === 'function') t.unref();
  return t;
};

globalThis.window = globalThis;
globalThis.window.location = new URL('http://localhost:7777/');
globalThis.window.addEventListener = () => {};
globalThis.window.removeEventListener = () => {};
globalThis.window.requestAnimationFrame = (fn) => setTimeout(fn, 0);
globalThis.window.cancelAnimationFrame = (id) => clearTimeout(id);
globalThis.window.marked = {
  use() {},
  setOptions() {},
  parse(md) { return `<p>${md || ''}</p>`; }
};
globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
globalThis.document = {
  body: new MockElement('body'),
  createElement: (tag) => new MockElement(tag),
  createDocumentFragment: () => new MockElement('div'),
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  addEventListener: () => {},
  removeEventListener: () => {}
};

const { normalizeChecklistItems, renderChecklistCard, upsertTurnChecklistCard } = await import('./chat-checklist.js');
const chat = await import('./chat.js');

test('renderChecklistCard renders all 5 statuses, active_form, progress percentage, and escapes XSS', () => {
  const card = renderChecklistCard([
    { id: '1', title: 'Read files', status: 'done' },
    { id: '2', title: 'Implement fix', active_form: 'Implementing <script>fix</script>', status: 'in_progress' },
    { id: '3', title: 'Run tests', status: 'pending' },
    { id: '4', title: 'Optional docs', status: 'skipped' },
    { id: '5', title: '<img src=x onerror=alert(1)>', status: 'failed' }
  ]);

  assert.ok(card, 'expected non-null checklist card');
  assert.equal(card.className, 'chat-todo-card');
  assert.ok(card.querySelector('.chat-todo-header').textContent.includes('Task Progress (1/5)'));
  assert.equal(card.querySelector('.chat-todo-pct').textContent.trim(), '20%');
  assert.equal(card.querySelector('.chat-todo-progress-fill').style.width, '20%');

  const rows = card.querySelectorAll('.chat-todo-item');
  assert.equal(rows.length, 5);

  const expectations = [
    { cls: 'done', iconCls: 'todo-icon-completed', icon: '✓', text: 'Read files' },
    { cls: 'in_progress', iconCls: 'todo-icon-inprogress', icon: '▶', text: 'Implementing &lt;script&gt;fix&lt;/script&gt;' },
    { cls: 'pending', iconCls: 'todo-icon-pending', icon: '○', text: 'Run tests' },
    { cls: 'skipped', iconCls: 'todo-icon-skipped', icon: '–', text: 'Optional docs' },
    { cls: 'failed', iconCls: 'todo-icon-failed', icon: '✕', text: '&lt;img src=x onerror=alert(1)&gt;' }
  ];

  expectations.forEach((exp, idx) => {
    const row = rows[idx];
    assert.ok(row.classList.contains(exp.cls), `row ${idx} missing class ${exp.cls}`);
    const icon = row.querySelector('.chat-todo-status-icon');
    assert.ok(icon.classList.contains(exp.iconCls), `row ${idx} missing icon class ${exp.iconCls}`);
    assert.equal(icon.textContent.trim(), exp.icon);
    const txt = row.querySelector('.chat-todo-text').textContent.trim();
    assert.equal(txt, exp.text);
  });

  assert.equal(card.innerHTML.includes('<script>'), false);
  assert.equal(card.innerHTML.includes('<img '), false);
  assert.equal(renderChecklistCard([]), null);
  assert.equal(normalizeChecklistItems([{ title: '   ', status: 'pending' }]).length, 0);
});

test('upsertTurnChecklistCard replaces existing card in-place instead of stacking duplicates', () => {
  const msgEl = new MockElement('div');
  msgEl.className = 'chat-msg assistant-msg in-flight';
  const slot = new MockElement('div');
  slot.className = 'chat-tools-slot';
  const body = new MockElement('div');
  body.className = 'chat-msg-body';
  msgEl.appendChild(slot);
  msgEl.appendChild(body);

  upsertTurnChecklistCard(msgEl, [
    { title: 'Step 1', active_form: 'Running Step 1', status: 'in_progress' },
    { title: 'Step 2', status: 'pending' }
  ]);
  assert.equal(msgEl.querySelectorAll('.chat-todo-card').length, 1);
  assert.equal(slot.querySelectorAll('.chat-todo-card').length, 1);
  assert.equal(slot.querySelector('.chat-todo-pct').textContent.trim(), '0%');

  upsertTurnChecklistCard(msgEl, [
    { title: 'Step 1', status: 'done' },
    { title: 'Step 2', status: 'done' }
  ]);
  assert.equal(msgEl.querySelectorAll('.chat-todo-card').length, 1);
  assert.equal(slot.querySelectorAll('.chat-todo-card').length, 1);
  assert.ok(slot.querySelector('.chat-todo-header').textContent.includes('Task Progress (2/2)'));
  assert.equal(slot.querySelector('.chat-todo-pct').textContent.trim(), '100%');
});

test('chat.js createChatMessageElement and tool_start stream events render checklist-only and live updates', () => {
  const container = new MockElement('div');
  const tabObj = {
    id: 'tab-cl',
    session: { id: 'antigravity:local:sess-cl', agent: 'antigravity' },
    chatMessagesEl: container,
    activeTurnTools: []
  };

  // Historical checklist-only assistant turn (empty content & tool_calls)
  const histEl = chat.createChatMessageElement(tabObj, {
    role: 'assistant',
    content: '',
    tool_calls: [],
    checklist: [
      { title: 'Analyze', status: 'done' },
      { title: 'Verify', status: 'pending' }
    ],
    timestamp: '2026-11-25T08:00:00Z'
  });
  assert.ok(histEl);
  assert.equal(histEl.querySelectorAll('.chat-todo-card').length, 1);
  assert.ok(histEl.querySelector('.chat-tools').querySelector('.chat-todo-card'));
  assert.ok(histEl.querySelector('.chat-todo-header').textContent.includes('Task Progress (1/2)'));

  // Live SSE tool_start updates checklist in-place across two events in one turn
  chat.handleChatStreamEvent(tabObj, {
    type: 'tool_start',
    turn_id: 't-1',
    seq: 1,
    tool_name: 'progress',
    checklist: [
      { title: 'Step A', active_form: 'Working A', status: 'in_progress' },
      { title: 'Step B', status: 'pending' }
    ]
  });
  assert.ok(tabObj.activeTurnMsgEl);
  assert.equal(tabObj.activeTurnMsgEl.querySelectorAll('.chat-todo-card').length, 1);
  assert.ok(tabObj.activeTurnMsgEl.querySelector('.chat-todo-text').textContent.includes('Working A'));

  chat.handleChatStreamEvent(tabObj, {
    type: 'tool_start',
    turn_id: 't-1',
    seq: 2,
    tool_name: 'progress',
    checklist: [
      { title: 'Step A', status: 'done' },
      { title: 'Step B', status: 'done' }
    ]
  });
  assert.equal(tabObj.activeTurnMsgEl.querySelectorAll('.chat-todo-card').length, 1);
  assert.ok(tabObj.activeTurnMsgEl.querySelector('.chat-todo-header').textContent.includes('Task Progress (2/2)'));
});
