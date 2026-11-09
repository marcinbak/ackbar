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
  toggle(cls, force) {
    if (force !== undefined) {
      if (force) this.add(cls);
      else this.remove(cls);
      return force;
    }
    if (this.contains(cls)) {
      this.remove(cls);
      return false;
    }
    this.add(cls);
    return true;
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
    this.scrollTop = 0;
    this.scrollHeight = 100;
    this.clientHeight = 100;
    this.value = '';
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

  get lastElementChild() {
    return this.children.length > 0 ? this.children[this.children.length - 1] : null;
  }

  get firstElementChild() {
    return this.children.length > 0 ? this.children[0] : null;
  }

  get nextElementSibling() {
    if (!this.parentNode) return null;
    const siblings = this.parentNode.children;
    const idx = siblings.indexOf(this);
    return (idx >= 0 && idx < siblings.length - 1) ? siblings[idx + 1] : null;
  }

  get previousElementSibling() {
    if (!this.parentNode) return null;
    const siblings = this.parentNode.children;
    const idx = siblings.indexOf(this);
    return idx > 0 ? siblings[idx - 1] : null;
  }

  get innerHTML() {
    return this._innerHTML;
  }
  set innerHTML(html) {
    this._innerHTML = html;
    for (const c of this.children) {
      c.parentNode = null;
    }
    this.children = [];
    if (!html) return;
    this.children = parseHtmlFragment(html, this);
  }

  get textContent() {
    if (this.children.length === 0) return this._textContent;
    return this.children.map(c => c.textContent).join(' ');
  }
  set textContent(txt) {
    this._textContent = txt;
    for (const c of this.children) {
      c.parentNode = null;
    }
    this.children = [];
  }

  appendChild(child) {
    if (!child) return child;
    if (child.parentNode) {
      child.parentNode.removeChild(child);
    }
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(newChild, refChild) {
    if (!newChild) return newChild;
    if (!refChild) return this.appendChild(newChild);
    if (newChild.parentNode) {
      newChild.parentNode.removeChild(newChild);
    }
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

  remove() {
    if (this.parentNode) {
      this.parentNode.removeChild(this);
    }
  }

  addEventListener(event, fn) {
    if (!this.listeners.has(event)) {
      this.listeners.set(event, []);
    }
    this.listeners.get(event).push(fn);
  }

  removeEventListener(event, fn) {
    const list = this.listeners.get(event);
    if (list) {
      const idx = list.indexOf(fn);
      if (idx !== -1) list.splice(idx, 1);
    }
  }

  focus() {}
  blur() {}
  scrollIntoView() {}

  closest(sel) {
    let cur = this;
    while (cur) {
      if (matchesSelector(cur, sel)) return cur;
      cur = cur.parentNode;
    }
    return null;
  }

  hasAttribute(name) {
    return Boolean(this.dataset[name] || this[name]);
  }
  getAttribute(name) {
    return this.dataset[name] || this[name] || null;
  }
  setAttribute(name, val) {
    this[name] = val;
  }
  removeAttribute(name) {
    delete this[name];
  }

  querySelector(sel) {
    const all = this.querySelectorAll(sel);
    return all.length > 0 ? all[0] : null;
  }

  querySelectorAll(sel) {
    const res = [];
    const traverse = (node) => {
      for (const child of node.children) {
        if (matchesSelector(child, sel)) {
          res.push(child);
        }
        traverse(child);
      }
    };
    traverse(this);
    return res;
  }
}

function matchesSelector(el, sel) {
  if (!el || !sel) return false;
  if (sel === '*') return true;
  const notRegex = /:not\(([^)]+)\)/g;
  let notParts = [];
  let notMatch;
  while ((notMatch = notRegex.exec(sel)) !== null) {
    notParts.push(notMatch[1]);
  }
  const baseSel = sel.replace(notRegex, '');
  for (const notPart of notParts) {
    if (matchesSelector(el, notPart)) return false;
  }
  if (!baseSel) return true;
  const parts = baseSel.split(/(?=[.#])/);
  for (const p of parts) {
    if (!p) continue;
    if (p.startsWith('.')) {
      const cls = p.slice(1);
      if (!el.classList || !el.classList.contains(cls)) return false;
    } else if (p.startsWith('#')) {
      const id = p.slice(1);
      if (el.id !== id) return false;
    } else {
      if (el.tagName !== p.toUpperCase()) return false;
    }
  }
  return true;
}

function parseHtmlFragment(html, parent) {
  const elements = [];
  const tagRegex = /<([a-zA-Z0-9-]+)([^>]*)>([\s\S]*?)<\/\1>|<([a-zA-Z0-9-]+)([^>]*)\/>/g;
  let match;
  let lastIdx = 0;
  while ((match = tagRegex.exec(html)) !== null) {
    const tagName = match[1] || match[4];
    const attrs = match[2] || match[5] || '';
    const inner = match[3] || '';
    const el = new MockElement(tagName);
    el.parentNode = parent;

    const classMatch = attrs.match(/class=["']([^"']+)["']/);
    if (classMatch) {
      el.className = classMatch[1];
    }
    const dataMatches = attrs.matchAll(/data-([a-zA-Z0-9-]+)=["']([^"']+)["']/g);
    for (const dm of dataMatches) {
      const camelKey = dm[1].replace(/-([a-z])/g, (_, l) => l.toUpperCase());
      el.dataset[camelKey] = dm[2];
    }
    if (inner && inner.includes('<')) {
      el.children = parseHtmlFragment(inner, el);
    } else if (inner) {
      el._textContent = inner.trim();
    }
    elements.push(el);
    lastIdx = tagRegex.lastIndex;
  }
  if (elements.length === 0 && html.trim() && !html.includes('<')) {
    const textNode = new MockElement('span');
    textNode.parentNode = parent;
    textNode._textContent = html.trim();
    elements.push(textNode);
  }
  return elements;
}

// Global mocks setup
const origSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = (fn, ms, ...args) => {
  const timer = origSetTimeout(fn, ms, ...args);
  if (timer && typeof timer.unref === 'function') {
    timer.unref();
  }
  return timer;
};

globalThis.window = globalThis;
globalThis.window.location = new URL('http://localhost:7777/');
globalThis.window.addEventListener = () => {};
globalThis.window.removeEventListener = () => {};
globalThis.window.requestAnimationFrame = (fn) => setTimeout(fn, 0);
globalThis.window.cancelAnimationFrame = (id) => clearTimeout(id);
globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
globalThis.document = {
  body: new MockElement('body'),
  createElement: (tag) => new MockElement(tag),
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  addEventListener: () => {},
  removeEventListener: () => {}
};

// Import chat module
const chat = await import('./chat.js');
const {
  isPromptInTranscriptTail,
  loadChatTranscript,
  sendChatPrompt,
  handleChatStreamEvent
} = chat;

test('isPromptInTranscriptTail validates tail message matching and timestamp sanity', () => {
  assert.equal(isPromptInTranscriptTail([], 'fix login', Date.now()), false);

  const t1 = [
    { role: 'user', content: 'fix login', rawPrompt: 'fix login' }
  ];
  assert.equal(isPromptInTranscriptTail(t1, 'fix login', Date.now()), true);

  const now = Date.now();
  const t2 = [
    { role: 'user', content: 'fix login', rawPrompt: 'fix login', timestamp: new Date(now - 1000).toISOString() },
    { role: 'assistant', content: 'checking files...', timestamp: new Date(now - 500).toISOString() }
  ];
  assert.equal(isPromptInTranscriptTail(t2, 'fix login', now), true);

  // Stale identical prompt from minutes ago should NOT match new prompt
  const staleTime = now - 120000;
  const t3 = [
    { role: 'user', content: 'fix login', rawPrompt: 'fix login', timestamp: new Date(staleTime).toISOString() },
    { role: 'assistant', content: 'done earlier', timestamp: new Date(staleTime + 5000).toISOString() }
  ];
  assert.equal(isPromptInTranscriptTail(t3, 'fix login', now), false);
});

test('preserves in-flight user prompt across session switch and avoids duplicate after turn_complete', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const inputEl = new MockElement('textarea');
  inputEl.value = 'Investigate memory leak';

  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const tabObj = {
    id: 'tab-test-1',
    viewMode: 'chat',
    chatMessagesEl: container,
    chatInputEl: inputEl,
    chatSendBtn: sendBtn,
    chatCancelBtn: cancelBtn,
    chatStatusBadge: statusBadge,
    pendingAttachments: [],
    session: {
      id: 'claude-code:local:sess-in-flight-test',
      agent: 'claude-code',
      state: 0
    }
  };

  let mockTranscriptMessages = [];
  globalThis.fetch = async (url, opts = {}) => {
    const urlStr = String(url);
    if (urlStr.includes('/v1/sessions/prompt')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({ status: 'started' })
      };
    }
    if (urlStr.includes('/v1/sessions/transcript')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          messages: mockTranscriptMessages
        })
      };
    }
    if (urlStr.includes('/v1/sessions/queue')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({ items: [], paused: false })
      };
    }
    if (urlStr.includes('/v1/sessions')) {
      return {
        ok: true,
        status: 200,
        json: async () => []
      };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  // 1. Send chat prompt
  await sendChatPrompt(tabObj);

  assert.ok(tabObj.inFlightPrompt, 'tabObj.inFlightPrompt should be set');
  assert.equal(tabObj.inFlightPrompt.text, 'Investigate memory leak');
  const userBubblesAfterSend = container.querySelectorAll('.chat-msg.user-msg');
  assert.equal(userBubblesAfterSend.length, 1, 'Should render optimistic user bubble');

  // 2. Receive turn_start SSE event
  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    text: 'Investigate memory leak',
    timestamp: new Date().toISOString()
  });

  assert.ok(tabObj.inFlightPrompt, 'tabObj.inFlightPrompt should remain alive after turn_start');
  assert.equal(tabObj.sending, true, 'tabObj.sending should be true');

  // 3. Switch away and switch back: call loadChatTranscript while transcript on disk only has prior messages (or empty)
  mockTranscriptMessages = [
    { role: 'user', content: 'Prior question', rawPrompt: 'Prior question', timestamp: new Date(Date.now() - 60000).toISOString() },
    { role: 'assistant', content: 'Prior answer', timestamp: new Date(Date.now() - 50000).toISOString() }
  ];

  await loadChatTranscript(tabObj);

  // Verify the sent user prompt bubble was preserved in chatMessagesEl
  const userBubblesAfterSwitch = container.querySelectorAll('.chat-msg.user-msg');
  assert.equal(userBubblesAfterSwitch.length, 2, 'Should contain prior user msg AND preserved in-flight user msg');
  assert.ok(userBubblesAfterSwitch[1].textContent.includes('Investigate memory leak'));

  // 4. Turn finishes: receive turn_complete SSE event and reload transcript with the completed turn
  mockTranscriptMessages = [
    { role: 'user', content: 'Prior question', rawPrompt: 'Prior question', timestamp: new Date(Date.now() - 60000).toISOString() },
    { role: 'assistant', content: 'Prior answer', timestamp: new Date(Date.now() - 50000).toISOString() },
    { role: 'user', content: 'Investigate memory leak', rawPrompt: 'Investigate memory leak', timestamp: new Date().toISOString() },
    { role: 'assistant', content: 'Identified leak in cache map.', timestamp: new Date().toISOString() }
  ];

  handleChatStreamEvent(tabObj, {
    type: 'turn_complete',
    text: 'Identified leak in cache map.',
    timestamp: new Date().toISOString()
  });

  // Verify tabObj.inFlightPrompt was cleared
  assert.equal(tabObj.inFlightPrompt, null, 'tabObj.inFlightPrompt should be null after turn_complete');

  // loadChatTranscript runs on turn_complete. Let us await an explicit load to ensure transcript is updated
  await loadChatTranscript(tabObj);

  const finalUserBubbles = container.querySelectorAll('.chat-msg.user-msg');
  assert.equal(finalUserBubbles.length, 2, 'Must not duplicate the in-flight user prompt bubble');
  const finalAssistantBubbles = container.querySelectorAll('.chat-msg.assistant-msg');
  assert.equal(finalAssistantBubbles.length, 2, 'Should have 2 assistant bubbles');
});
