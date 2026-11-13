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
  get nextSibling() {
    return this.nextElementSibling;
  }

  get previousElementSibling() {
    if (!this.parentNode) return null;
    const siblings = this.parentNode.children;
    const idx = siblings.indexOf(this);
    return idx > 0 ? siblings[idx - 1] : null;
  }
  get previousSibling() {
    return this.previousElementSibling;
  }

  get firstChild() {
    return this.firstElementChild;
  }

  get lastChild() {
    return this.lastElementChild;
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
    if (child.isFragment) {
      const fragmentChildren = [...child.children];
      for (const fc of fragmentChildren) {
        this.appendChild(fc);
      }
      return child;
    }
    if (child.parentNode) {
      child.parentNode.removeChild(child);
    }
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  insertBefore(newChild, refChild) {
    if (!newChild) return newChild;
    if (newChild.isFragment) {
      const fragmentChildren = [...newChild.children];
      for (const fc of fragmentChildren) {
        this.insertBefore(fc, refChild);
      }
      return newChild;
    }
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

  replaceChild(newChild, oldChild) {
    const idx = this.children.indexOf(oldChild);
    if (idx === -1) return oldChild;
    if (newChild.parentNode) {
      newChild.parentNode.removeChild(newChild);
    }
    newChild.parentNode = this;
    oldChild.parentNode = null;
    this.children.splice(idx, 1, newChild);
    return oldChild;
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

  after(...nodes) {
    if (!this.parentNode) return;
    const siblings = this.parentNode.children;
    const idx = siblings.indexOf(this);
    if (idx === -1) return;
    let nextIdx = idx + 1;
    for (const node of nodes) {
      if (!node) continue;
      if (node.parentNode) node.parentNode.removeChild(node);
      node.parentNode = this.parentNode;
      siblings.splice(nextIdx, 0, node);
      nextIdx++;
    }
  }

  click() {
    const list = this.listeners.get('click');
    if (list) {
      for (const fn of [...list]) {
        fn({ target: this, preventDefault() {}, stopPropagation() {} });
      }
    }
  }

  dispatchEvent(evt) {
    const type = evt?.type || 'click';
    const list = this.listeners.get(type);
    if (list) {
      for (const fn of [...list]) {
        fn(evt);
      }
    }
  }

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
  if (sel.includes(',')) {
    return sel.split(',').some((sub) => matchesSelector(el, sub.trim()));
  }
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

    const isSelfClosing = tagContent.endsWith('/') || /^(input|img|br|hr)\b/i.test(tagContent);
    const spaceIdx = tagContent.search(/\s/);
    const tagName = (spaceIdx === -1
      ? (isSelfClosing && tagContent.endsWith('/') ? tagContent.slice(0, -1) : tagContent)
      : tagContent.slice(0, spaceIdx)).toLowerCase();
    const attrs = spaceIdx === -1 ? '' : tagContent.slice(spaceIdx, isSelfClosing && tagContent.endsWith('/') ? -1 : undefined);

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
    const styleMatch = attrs.match(/style=["']([^"']+)["']/);
    if (styleMatch) {
      for (const part of styleMatch[1].split(';')) {
        const colon = part.indexOf(':');
        if (colon !== -1) {
          const k = part.slice(0, colon).trim().replace(/-([a-z])/g, (_, l) => l.toUpperCase());
          const v = part.slice(colon + 1).trim();
          if (k && v) el.style[k] = v;
        }
      }
    }

    if (isSelfClosing) {
      elements.push(el);
      i = closeAngle + 1;
      continue;
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

// Global mocks setup
const origSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = (fn, ms, ...args) => {
  const timer = origSetTimeout(fn, ms, ...args);
  if (timer && typeof timer.unref === 'function') {
    timer.unref();
  }
  return timer;
};

const origSetInterval = globalThis.setInterval;
globalThis.setInterval = (fn, ms, ...args) => {
  const timer = origSetInterval(fn, ms, ...args);
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
globalThis.window.open = () => null;
globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
globalThis.document = {
  body: new MockElement('body'),
  createElement: (tag) => new MockElement(tag),
  createDocumentFragment: () => {
    const f = new MockElement('#document-fragment');
    f.isFragment = true;
    return f;
  },
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
  loadOlderTranscriptMessages,
  sendChatPrompt,
  connectChatStream,
  disconnectChatStream,
  handleChatStreamEvent,
  renderInteractiveBlockCard,
  renderConfigPopoverContent,
  setChatSending,
  hideInStreamActivity,
  pauseInStreamActivity,
  resumeInStreamActivity
} = chat;

const chatAuth = await import('./chat-auth.js');
const {
  isSafeHTTPSUrl,
  renderAuthCard,
  mountMCPStatus,
  invalidateMCPStatusCache
} = chatAuth;

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

  // Short prompt prefix in previous turn should NOT match new longer prompt starting with same prefix
  const t4 = [
    { role: 'user', content: 'run', rawPrompt: 'run', timestamp: new Date(now - 2000).toISOString() },
    { role: 'assistant', content: 'done', timestamp: new Date(now - 1000).toISOString() }
  ];
  assert.equal(isPromptInTranscriptTail(t4, 'run all benchmarks', now), false);
});

test('handleChatStreamEvent turn_start does not drop new user prompt when preceded by identical prompt in completed turn', () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const userMsgEl = new MockElement('div');
  userMsgEl.className = 'chat-msg user-msg';
  userMsgEl.dataset.rawPrompt = 'continue';
  userMsgEl.innerHTML = '<div class="chat-msg-body">continue</div>';
  container.appendChild(userMsgEl);

  const assistantMsgEl = new MockElement('div');
  assistantMsgEl.className = 'chat-msg assistant-msg';
  assistantMsgEl.innerHTML = '<div class="chat-msg-body">Working on it</div>';
  container.appendChild(assistantMsgEl);

  const tabObj = {
    id: 'tab-test-identical-turn',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: { id: 'claude-code:local:sess-identical', agent: 'claude-code', state: 0 }
  };

  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    text: 'continue',
    timestamp: new Date().toISOString()
  });

  const userBubbles = container.querySelectorAll('.chat-msg.user-msg');
  assert.equal(userBubbles.length, 2, 'Must append a new user bubble for Turn 2 rather than dropping it');
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

test('avoids duplicate assistant bubbles and DOM wipe when reconciling multi-block response and duplicate turn_complete', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const inputEl = new MockElement('textarea');
  inputEl.value = 'Check status';

  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const tabObj = {
    id: 'tab-test-no-blink-dup',
    viewMode: 'chat',
    chatMessagesEl: container,
    chatInputEl: inputEl,
    chatSendBtn: sendBtn,
    chatCancelBtn: cancelBtn,
    chatStatusBadge: statusBadge,
    pendingAttachments: [],
    session: {
      id: 'claude-code:local:sess-no-blink-dup',
      agent: 'claude-code',
      engine_type: 'headless',
      state: 0
    }
  };

  let mockTranscriptMessages = [];
  globalThis.fetch = async (url) => {
    const urlStr = String(url);
    if (urlStr.includes('/v1/sessions/prompt')) {
      return { ok: true, status: 200, json: async () => ({ status: 'running' }) };
    }
    if (urlStr.includes('/v1/sessions/transcript')) {
      return { ok: true, status: 200, json: async () => ({ messages: mockTranscriptMessages }) };
    }
    if (urlStr.includes('/v1/sessions/queue')) {
      return { ok: true, status: 200, json: async () => ({ items: [], paused: false }) };
    }
    if (urlStr.includes('/v1/sessions')) {
      return { ok: true, status: 200, json: async () => [] };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  await sendChatPrompt(tabObj);

  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    text: 'Check status',
    timestamp: new Date().toISOString()
  });

  handleChatStreamEvent(tabObj, {
    type: 'text_delta',
    text: 'Part 1.\n\nPart 2.'
  });

  // On-disk transcript uses single newline between text blocks
  mockTranscriptMessages = [
    { role: 'user', content: 'Check status', rawPrompt: 'Check status', timestamp: new Date().toISOString() },
    { role: 'assistant', content: 'Part 1.\nPart 2.', timestamp: new Date().toISOString() }
  ];

  handleChatStreamEvent(tabObj, {
    type: 'turn_complete',
    text: 'Part 1.\n\nPart 2.',
    timestamp: new Date().toISOString()
  });

  // Duplicate turn_complete must not create a second assistant bubble
  handleChatStreamEvent(tabObj, {
    type: 'turn_complete',
    text: 'Part 1.\n\nPart 2.',
    timestamp: new Date().toISOString()
  });

  const existingAssistantEl = container.querySelectorAll('.chat-msg.assistant-msg')[0];
  assert.ok(existingAssistantEl, 'Assistant bubble should exist');

  await loadChatTranscript(tabObj);

  const assistantBubbles = container.querySelectorAll('.chat-msg.assistant-msg');
  assert.equal(assistantBubbles.length, 1, 'Must have exactly 1 assistant bubble without duplication');
  assert.equal(assistantBubbles[0], existingAssistantEl, 'Should reuse existing assistant DOM node without wiping innerHTML');
  assert.ok(!tabObj._pendingTranscriptFollowup, 'Should not schedule 600ms follow-up reload loop when text matches');
});

test('does not duplicate in-flight user prompt when transcript has partial assistant output before turn_start SSE or with attachments after >5s', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const inputEl = new MockElement('textarea');
  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const turnStartIso = new Date(Date.now() - 25000).toISOString();
  const partialAssistantIso = new Date(Date.now() - 20000).toISOString();

  // Part A: Newly opened / switched tab loads transcript with [user, assistant(partial)] and active_prompt BEFORE turn_start SSE arrives
  const tabObj = {
    id: 'tab-test-partial-assistant-switch',
    viewMode: 'chat',
    chatMessagesEl: container,
    chatInputEl: inputEl,
    chatSendBtn: sendBtn,
    chatCancelBtn: cancelBtn,
    chatStatusBadge: statusBadge,
    pendingAttachments: [],
    session: {
      id: 'antigravity:local:sess-partial-switch',
      agent: 'antigravity',
      engine_type: 'headless',
      state: 1
    }
  };

  let mockTranscriptMessages = [
    { role: 'user', content: 'Fix duplicate prompt', rawPrompt: 'Fix duplicate prompt', timestamp: turnStartIso },
    { role: 'assistant', content: '', thinking: 'Analyzing...', tool_calls: ['Read (web/js/chat.js)'], timestamp: partialAssistantIso }
  ];
  let mockActivePrompt = {
    text: 'Fix duplicate prompt',
    timestamp: turnStartIso
  };

  globalThis.fetch = async (url) => {
    const urlStr = String(url);
    if (urlStr.includes('/v1/sessions/transcript')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          messages: mockTranscriptMessages,
          active_prompt: mockActivePrompt
        })
      };
    }
    if (urlStr.includes('/v1/sessions/queue')) {
      return { ok: true, status: 200, json: async () => ({ items: [], paused: false }) };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  await loadChatTranscript(tabObj);
  assert.equal(container.querySelectorAll('.chat-msg.user-msg').length, 1, 'Should render 1 user bubble from transcript');
  assert.ok(tabObj.inFlightPrompt, 'Should bind tabObj.inFlightPrompt from active_prompt metadata');

  // Initial turn_start SSE arrives after loadChatTranscript already rendered the partial assistant message
  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    text: 'Fix duplicate prompt',
    timestamp: turnStartIso
  });

  assert.equal(
    container.querySelectorAll('.chat-msg.user-msg').length,
    1,
    'Must not append a second user bubble when turn_start arrives after partial assistant output is on disk'
  );

  // Subsequent loadChatTranscript >25s into the turn with attachment prompt must also not duplicate the user prompt
  const attachPromptDaemon = 'Inspect screenshot\n\n[Attached file: "/tmp/screen.png"]\nPlease inspect the attached file.';
  const attachPromptDisk = 'Inspect screenshot\n\n[Attached file: "/tmp/screen.png"]';
  tabObj.inFlightPrompt = null;
  mockActivePrompt = {
    text: attachPromptDaemon,
    timestamp: turnStartIso
  };
  mockTranscriptMessages = [
    { role: 'user', content: attachPromptDisk, rawPrompt: attachPromptDisk, timestamp: turnStartIso },
    { role: 'assistant', content: '', thinking: 'Checking screenshot...', tool_calls: ['Read (/tmp/screen.png)'], timestamp: partialAssistantIso }
  ];

  // Simulate turn_start arriving BEFORE loadChatTranscript with Date.now() vs turnStartIso
  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    text: attachPromptDaemon,
    timestamp: turnStartIso
  });

  await loadChatTranscript(tabObj);

  assert.equal(
    container.querySelectorAll('.chat-msg.user-msg').length,
    1,
    'Must not duplicate attachment user prompt when reloading transcript mid-turn after >20s tool execution'
  );
});

test('restores in_flight_turn from spool on tab switch and continues streaming from last_seq without gaps or duplicates', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabEl = new MockElement('div');
  tabEl.className = 'tab active';
  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const openedUrls = [];
  class MockEventSource {
    static OPEN = 1;
    static CLOSED = 2;
    constructor(url) {
      this.url = url;
      this.readyState = MockEventSource.OPEN;
      openedUrls.push(url);
    }
    close() {
      this.readyState = MockEventSource.CLOSED;
    }
  }
  const prevEventSource = globalThis.EventSource;
  globalThis.EventSource = MockEventSource;

  try {
    const turnStartIso = new Date().toISOString();
    const tabObj = {
      id: 'tab-spool-restore',
      viewMode: 'chat',
      tabEl,
      chatMessagesEl: container,
      chatSendBtn: sendBtn,
      chatCancelBtn: cancelBtn,
      chatStatusBadge: statusBadge,
      session: {
        id: 'claude-code:local:sess-spool-restore',
        agent: 'claude-code',
        engine_type: 'headless',
        state: 1
      }
    };

    globalThis.fetch = async (url) => {
      const urlStr = String(url);
      if (urlStr.includes('/v1/sessions/transcript')) {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            messages: [
              {
                role: 'user',
                content: 'Explain worker reattachment',
                timestamp: turnStartIso,
                turn_id: 'turn-42',
                in_flight: true
              },
              {
                role: 'assistant',
                content: 'First chunk. ',
                thinking: 'Checking spool...',
                tool_calls: ['Read: internal/daemon/headless_worker.go'],
                timestamp: turnStartIso,
                turn_id: 'turn-42',
                in_flight: true
              }
            ],
            in_flight_turn: {
              turn_id: 'turn-42',
              last_seq: 4,
              active: true,
              prompt: 'Explain worker reattachment',
              assistant_text: 'First chunk. ',
              thinking: 'Checking spool...',
              tool_calls: ['Read: internal/daemon/headless_worker.go'],
              timestamp: turnStartIso
            },
            active_prompt: {
              text: 'Explain worker reattachment',
              timestamp: turnStartIso,
              turn_id: 'turn-42'
            }
          })
        };
      }
      return { ok: true, status: 200, json: async () => ({ items: [], paused: false }) };
    };

    await loadChatTranscript(tabObj);
    assert.equal(tabObj.activeTurnId, 'turn-42');
    assert.equal(tabObj.lastStreamSeq, 4);
    assert.equal(tabObj.sending, true);
    assert.equal(container.querySelectorAll('.chat-msg.user-msg').length, 1);
    assert.equal(container.querySelectorAll('.chat-msg.assistant-msg.in-flight').length, 1);

    connectChatStream(tabObj);
    assert.equal(openedUrls.length, 1);
    assert.ok(openedUrls[0].includes('turn_id=turn-42'), 'SSE URL must carry turn_id');
    assert.ok(openedUrls[0].includes('after_seq=4'), 'SSE URL must carry after_seq=4');

    handleChatStreamEvent(tabObj, {
      turn_id: 'turn-42',
      seq: 4,
      type: 'text_delta',
      text: 'First chunk. '
    });
    assert.equal(tabObj.activeTurnBuffer, 'First chunk. ');

    handleChatStreamEvent(tabObj, {
      turn_id: 'turn-42',
      seq: 5,
      type: 'text_delta',
      text: 'Second chunk.'
    });
    await new Promise((r) => setTimeout(r, 15));

    assert.equal(tabObj.lastStreamSeq, 5);
    assert.equal(tabObj.activeTurnBuffer, 'First chunk. Second chunk.');
    assert.equal(container.querySelectorAll('.chat-msg.assistant-msg').length, 1);
    assert.ok(container.querySelectorAll('.chat-msg.assistant-msg')[0].textContent.includes('First chunk. Second chunk.'));
  } finally {
    globalThis.EventSource = prevEventSource;
  }
});

test('connectChatStream reconnects with updated after_seq and deduplicates mid-turn and post-turn jsonl flushes by turn_id', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabEl = new MockElement('div');
  tabEl.className = 'tab active';
  const inputEl = new MockElement('textarea');
  inputEl.value = 'Run diagnostics';
  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const openedSources = [];
  class MockEventSource {
    static OPEN = 1;
    static CLOSED = 2;
    constructor(url) {
      this.url = url;
      this.readyState = MockEventSource.OPEN;
      openedSources.push(this);
    }
    close() {
      this.readyState = MockEventSource.CLOSED;
    }
  }
  const prevEventSource = globalThis.EventSource;
  globalThis.EventSource = MockEventSource;

  try {
    const turnIso = new Date().toISOString();
    const tabObj = {
      id: 'tab-sse-reconnect-dedup',
      viewMode: 'chat',
      tabEl,
      chatMessagesEl: container,
      chatInputEl: inputEl,
      chatSendBtn: sendBtn,
      chatCancelBtn: cancelBtn,
      chatStatusBadge: statusBadge,
      pendingAttachments: [],
      session: {
        id: 'claude-code:local:sess-reconnect-dedup',
        agent: 'claude-code',
        engine_type: 'headless',
        state: 0
      }
    };

    let transcriptPayload = { messages: [] };
    globalThis.fetch = async (url) => {
      const urlStr = String(url);
      if (urlStr.includes('/v1/sessions/prompt')) {
        return { ok: true, status: 200, json: async () => ({ status: 'running' }) };
      }
      if (urlStr.includes('/v1/sessions/transcript')) {
        return { ok: true, status: 200, json: async () => transcriptPayload };
      }
      if (urlStr.includes('/v1/sessions/queue')) {
        return { ok: true, status: 200, json: async () => ({ items: [], paused: false }) };
      }
      if (urlStr.includes('/v1/sessions')) {
        return { ok: true, status: 200, json: async () => [] };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    await sendChatPrompt(tabObj);
    assert.equal(openedSources.length, 1);

    handleChatStreamEvent(tabObj, {
      turn_id: 'turn-99',
      seq: 1,
      type: 'turn_start',
      text: 'Run diagnostics',
      timestamp: turnIso
    });
    handleChatStreamEvent(tabObj, {
      turn_id: 'turn-99',
      seq: 2,
      type: 'text_delta',
      text: 'Diagnostics: '
    });
    await new Promise((r) => setTimeout(r, 15));

    openedSources[0].close();
    connectChatStream(tabObj);
    assert.equal(openedSources.length, 2);
    assert.ok(openedSources[1].url.includes('turn_id=turn-99'));
    assert.ok(openedSources[1].url.includes('after_seq=2'));

    transcriptPayload = {
      messages: [
        { role: 'user', content: 'Run diagnostics', timestamp: turnIso, turn_id: 'turn-99', in_flight: true },
        { role: 'assistant', content: 'Diagnostics: ', timestamp: turnIso, turn_id: 'turn-99', in_flight: true }
      ],
      in_flight_turn: {
        turn_id: 'turn-99',
        last_seq: 2,
        active: true,
        prompt: 'Run diagnostics',
        assistant_text: 'Diagnostics: ',
        timestamp: turnIso
      }
    };
    await loadChatTranscript(tabObj);
    assert.equal(container.querySelectorAll('.chat-msg.user-msg').length, 1, 'Zero duplicate user bubbles on mid-turn flush');
    assert.equal(container.querySelectorAll('.chat-msg.assistant-msg').length, 1, 'Zero duplicate assistant bubbles on mid-turn flush');

    handleChatStreamEvent(tabObj, {
      turn_id: 'turn-99',
      seq: 3,
      type: 'text_delta',
      text: 'all green.'
    });
    transcriptPayload = {
      messages: [
        { role: 'user', content: 'Run diagnostics', timestamp: turnIso, turn_id: 'turn-99' },
        { role: 'assistant', content: 'Diagnostics: all green.', timestamp: turnIso, turn_id: 'turn-99' }
      ]
    };
    handleChatStreamEvent(tabObj, {
      turn_id: 'turn-99',
      seq: 4,
      type: 'turn_complete',
      text: 'Diagnostics: all green.',
      timestamp: turnIso
    });
    await loadChatTranscript(tabObj);

    assert.equal(container.querySelectorAll('.chat-msg.user-msg').length, 1, 'Zero duplicate user bubbles after post-turn flush');
    assert.equal(container.querySelectorAll('.chat-msg.assistant-msg').length, 1, 'Zero duplicate assistant bubbles after post-turn flush');
    assert.equal(tabObj.lastStreamSeq, 4);
  } finally {
    globalThis.EventSource = prevEventSource;
  }
});

test('isSafeHTTPSUrl blocks insecure schemes, credentials, whitespace, and control chars', () => {
  assert.equal(isSafeHTTPSUrl('https://accounts.google.com/o/oauth2/v2/auth?client_id=123'), true);
  assert.equal(isSafeHTTPSUrl('https://claude.ai/login?code=abc'), true);

  // Insecure schemes
  assert.equal(isSafeHTTPSUrl('http://localhost:8080/oauth'), false);
  assert.equal(isSafeHTTPSUrl('javascript:alert(1)'), false);
  assert.equal(isSafeHTTPSUrl('file:///etc/passwd'), false);
  assert.equal(isSafeHTTPSUrl('data:text/html,<script>alert(1)</script>'), false);

  // Credentials embedded in URL
  assert.equal(isSafeHTTPSUrl('https://user:pass@accounts.google.com/oauth'), false);
  assert.equal(isSafeHTTPSUrl('https://user@accounts.google.com/oauth'), false);

  // Whitespace and control characters
  assert.equal(isSafeHTTPSUrl('https://accounts.google.com/oauth\n'), false);
  assert.equal(isSafeHTTPSUrl('https://accounts.google.com/oauth\t'), false);
  assert.equal(isSafeHTTPSUrl('https://accounts.google.com/oauth\x00'), false);
  assert.equal(isSafeHTTPSUrl('https://accounts.google.com/oauth?q=foo bar'), false);

  // Non-string, empty, overly long
  assert.equal(isSafeHTTPSUrl(''), false);
  assert.equal(isSafeHTTPSUrl(null), false);
  assert.equal(isSafeHTTPSUrl(undefined), false);
  assert.equal(isSafeHTTPSUrl(12345), false);
  assert.equal(isSafeHTTPSUrl('https://example.com/' + 'a'.repeat(4100)), false);
});

test('handleChatStreamEvent renders live .card-auth on interactive_block event', () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-auth-block',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: {
      id: 'claude-code:local:sess-auth',
      agent: 'claude-code',
      state: 1
    }
  };

  handleChatStreamEvent(tabObj, {
    type: 'interactive_block',
    tool_name: 'run_command',
    blocked: {
      kind: 'auth',
      auth_kind: 'plugin',
      auth_target: 'plugin:gslides',
      auth_server: 'gslides',
      reason: "Google Workspace plugin 'gslides' requires sign-in.",
      question: 'Sign in to gslides to continue?',
      options: ['Sign in', 'Retry', 'Dismiss']
    }
  });

  const authCard = container.querySelector('.chat-interactive-card.card-auth');
  assert.ok(authCard, 'Must render .chat-interactive-card.card-auth');
  assert.equal(tabObj.session.state, 2, 'Session state must become Blocked (2)');
  assert.equal(tabObj.session.blocked?.auth_server, 'gslides');

  const serverBadge = authCard.querySelector('.interactive-card-badge-server');
  assert.ok(serverBadge);
  assert.equal(serverBadge.textContent, 'gslides');

  const title = authCard.querySelector('.interactive-card-title');
  assert.ok(title);
  assert.equal(title.textContent, 'Sign in to gslides to continue?');

  const btnLaunch = authCard.querySelector('.btn-auth-launch');
  assert.ok(btnLaunch, 'Must render launch button');
  assert.ok(btnLaunch.textContent.includes('Sign in to gslides'));

  const btnRetry = authCard.querySelector('.btn-auth-retry');
  assert.ok(btnRetry, 'Must render retry button');

  const btnTerminal = authCard.querySelector('.btn-auth-terminal');
  assert.ok(btnTerminal, 'Must render terminal button');

  const btnDismiss = authCard.querySelector('.btn-auth-dismiss');
  assert.ok(btnDismiss, 'Must render dismiss button');
});

test('renderInteractiveBlockCard renders pre-expanded auth card when flow_id and auth_url exist on session reload', () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-auth-reload',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: {
      id: 'antigravity:local:sess-auth-reload',
      agent: 'antigravity',
      state: 2,
      blocked: {
        kind: 'auth',
        auth_kind: 'plugin',
        auth_target: 'plugin:gdrive',
        auth_server: 'gdrive',
        auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?flow_id=123',
        flow_id: 'flow-auth-456',
        reason: 'Google Workspace plugin gdrive requires authentication.',
        question: 'Sign in to gdrive to continue?'
      }
    }
  };

  renderInteractiveBlockCard(tabObj, tabObj.session.blocked);

  const authCard = container.querySelector('.chat-interactive-card.card-auth');
  assert.ok(authCard, 'Must render card-auth on reload');

  const btnLaunch = authCard.querySelector('.btn-auth-launch');
  assert.ok(btnLaunch.textContent.includes('Open Sign-In Link Again'), 'Launch button indicates existing flow');

  const callbackContainer = authCard.querySelector('.auth-callback-container');
  assert.ok(callbackContainer, 'Callback container must exist');
  assert.notEqual(callbackContainer.style.display, 'none', 'Callback container must be pre-expanded when flow exists');

  const callbackInput = authCard.querySelector('.auth-callback-input');
  assert.ok(callbackInput, 'Must have callback input for pasting redirect URL or code');
});

test('remote callback submission posts to /v1/sessions/auth-callback and marks card answered', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-auth-cb',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: {
      id: 'antigravity:local:sess-cb',
      agent: 'antigravity',
      state: 2,
      blocked: {
        kind: 'auth',
        auth_kind: 'plugin',
        auth_target: 'plugin:gsheets',
        auth_server: 'gsheets',
        auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?client_id=gsheets',
        flow_id: 'flow-sheets-789'
      }
    }
  };

  renderInteractiveBlockCard(tabObj, tabObj.session.blocked);
  const authCard = container.querySelector('.card-auth');

  const callbackInput = authCard.querySelector('.auth-callback-input');
  callbackInput.value = 'http://127.0.0.1:8085/?code=4/0Abc123&state=xyz';

  let postedUrl = '';
  let postedBody = null;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    postedUrl = String(url);
    postedBody = opts.body ? JSON.parse(opts.body) : null;
    return {
      ok: true,
      status: 200,
      json: async () => ({ completed: true, message: 'Authentication successful' })
    };
  };

  try {
    const btnComplete = authCard.querySelector('.btn-auth-complete');
    btnComplete.click();
    await new Promise((r) => setTimeout(r, 10));

    assert.ok(postedUrl.includes('/v1/sessions/auth-callback'), 'Must fetch /v1/sessions/auth-callback');
    assert.equal(postedBody?.session_id, 'antigravity:local:sess-cb');
    assert.equal(postedBody?.flow_id, 'flow-sheets-789');
    assert.equal(postedBody?.callback_url, 'http://127.0.0.1:8085/?code=4/0Abc123&state=xyz');

    assert.ok(authCard.classList.contains('answered'), 'Card must be marked answered upon completion');
    const actionsEl = authCard.querySelector('.interactive-card-actions');
    assert.ok(actionsEl.textContent.includes('Authentication Complete'));
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('auth card terminal, retry, and dismiss action triggers execute appropriate flows', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  let respondCalls = [];
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    const urlStr = String(url);
    if (urlStr.includes('/v1/sessions/respond')) {
      respondCalls.push(opts.body ? JSON.parse(opts.body) : {});
      return { ok: true, status: 200, json: async () => ({ status: 'resumed' }) };
    }
    if (urlStr.includes('/v1/sessions/take-wheel')) {
      respondCalls.push({ action: 'take-wheel' });
      return { ok: true, status: 200, json: async () => ({ status: 'detached' }) };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  try {
    // 1. Dismiss action
    const tabDismiss = {
      id: 'tab-dismiss',
      chatMessagesEl: container,
      session: { id: 'claude-code:local:sess-dismiss', agent: 'claude-code', state: 2 }
    };
    renderInteractiveBlockCard(tabDismiss, {
      kind: 'auth',
      auth_target: 'plugin:gslides',
      auth_server: 'gslides'
    });
    let card = container.querySelector('.card-auth');
    const btnDismiss = card.querySelector('.btn-auth-dismiss');
    btnDismiss.click();
    await new Promise((r) => setTimeout(r, 10));

    assert.equal(respondCalls.length, 1);
    assert.equal(respondCalls[0].action, 'dismiss');
    assert.equal(tabDismiss.session.state, 3, 'Dismiss updates session state to Running/Detached (3)');
    assert.equal(tabDismiss.session.blocked, null);
    assert.ok(card.classList.contains('answered'));

    // 2. Retry action
    respondCalls = [];
    const tabRetry = {
      id: 'tab-retry',
      chatMessagesEl: container,
      session: { id: 'claude-code:local:sess-retry', agent: 'claude-code', state: 2 }
    };
    renderInteractiveBlockCard(tabRetry, {
      kind: 'auth',
      auth_target: 'plugin:gdocs',
      auth_server: 'gdocs'
    });
    card = container.querySelector('.card-auth:not(.answered)');
    const btnRetry = card.querySelector('.btn-auth-retry');
    btnRetry.click();
    await new Promise((r) => setTimeout(r, 10));

    assert.equal(respondCalls.length, 1);
    assert.equal(respondCalls[0].action, 'retry');
    assert.equal(tabRetry.session.state, 1, 'Retry updates session state to Active (1)');
    assert.equal(tabRetry.session.blocked, null);
    assert.ok(card.classList.contains('answered'));

    // 3. Terminal action
    respondCalls = [];
    const tabTerminal = {
      id: 'tab-terminal',
      chatMessagesEl: container,
      session: { id: 'claude-code:local:sess-terminal', agent: 'claude-code', state: 2 }
    };
    renderInteractiveBlockCard(tabTerminal, {
      kind: 'auth',
      auth_target: 'agent:agy',
      auth_server: 'antigravity'
    });
    card = container.querySelector('.card-auth:not(.answered)');
    const btnTerminal = card.querySelector('.btn-auth-terminal');
    btnTerminal.click();
    await new Promise((r) => setTimeout(r, 10));

    assert.equal(respondCalls.length, 1);
    assert.equal(respondCalls[0].action, 'take-wheel');
    assert.equal(container.querySelector('.card-auth:not(.answered)'), null, 'Card removed when taking terminal wheel');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('mountMCPStatus renders server and plugin health pills with sign-in and inline callback flow', async () => {
  const container = new MockElement('div');
  const sessionId = 'antigravity:local:sess-popover';
  const tabObj = {
    id: 'tab-mcp-status',
    session: { id: sessionId, agent: 'antigravity' }
  };

  let fetchedUrls = [];
  let postCalls = [];
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    const urlStr = String(url);
    fetchedUrls.push(urlStr);
    if (urlStr.includes('/mcp-status')) {
      return {
        ok: true,
        status: 200,
        json: async () => ({
          servers: [
            { name: 'stripe', target: 'mcp:stripe', transport: 'stdio', status: 'needs_auth', auth_supported: true }
          ],
          plugins: [
            { name: 'gslides', target: 'plugin:gslides', installed: true, authenticated: false },
            { name: 'gdocs', target: 'plugin:gdocs', installed: true, authenticated: true }
          ]
        })
      };
    }
    if (urlStr.includes('/auth-tool')) {
      postCalls.push({ url: urlStr, body: JSON.parse(opts.body) });
      return {
        ok: true,
        status: 200,
        json: async () => ({
          flow_id: 'flow-slides-999',
          auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?scope=slides'
        })
      };
    }
    if (urlStr.includes('/auth-callback')) {
      postCalls.push({ url: urlStr, body: JSON.parse(opts.body) });
      return {
        ok: true,
        status: 200,
        json: async () => ({ completed: true })
      };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  try {
    await mountMCPStatus(container, sessionId, tabObj);

    assert.ok(fetchedUrls.some((u) => u.includes('/mcp-status')));
    const rows = container.querySelectorAll('.mcp-pill-row');
    assert.equal(rows.length, 3, 'Must render 1 MCP server + 2 plugin rows');

    const gdocsRow = rows.find((r) => r.dataset.target === 'plugin:gdocs');
    assert.ok(gdocsRow);
    const docsStatus = gdocsRow.querySelector('.status-ok');
    assert.ok(docsStatus);
    assert.ok(docsStatus.textContent.includes('Signed in'));

    const gslidesRow = rows.find((r) => r.dataset.target === 'plugin:gslides');
    assert.ok(gslidesRow);
    const slidesStatus = gslidesRow.querySelector('.status-warn');
    assert.ok(slidesStatus);
    assert.ok(slidesStatus.textContent.includes('Needs auth'));

    const btnPluginSignin = gslidesRow.querySelector('.btn-plugin-signin');
    assert.ok(btnPluginSignin, 'Unauthenticated plugin has sign in button');

    btnPluginSignin.click();
    await new Promise((r) => setTimeout(r, 10));

    assert.equal(postCalls.length, 1);
    assert.equal(postCalls[0].body.target, 'plugin:gslides');

    const inlineRow = container.querySelector('.mcp-inline-callback-row');
    assert.ok(inlineRow, 'Must inject inline callback row upon starting auth flow');

    const inInput = inlineRow.querySelector('.mcp-inline-input');
    inInput.value = 'http://127.0.0.1:8085/?code=4/authcode123';
    const subBtn = inlineRow.querySelector('.btn-mcp-submit-cb');
    subBtn.click();
    await new Promise((r) => setTimeout(r, 10));

    assert.equal(postCalls.length, 2);
    assert.equal(postCalls[1].body.flow_id, 'flow-slides-999');
    assert.equal(postCalls[1].body.callback_url, 'http://127.0.0.1:8085/?code=4/authcode123');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('updateShellModeHint toggles .chat-input-shell-mode badge when input begins with !', async () => {
  const { updateShellModeHint } = await import('./chat-commands.js');

  const composerBox = document.createElement('div');
  composerBox.className = 'chat-composer-box';
  const textarea = document.createElement('textarea');
  textarea.className = 'chat-composer-textarea';
  composerBox.appendChild(textarea);

  const tabObj = {
    chatComposerBox: composerBox,
    chatInputEl: textarea
  };

  // 1. Initial state: normal input -> no badge
  textarea.value = 'hello world';
  updateShellModeHint(tabObj);
  let badge = composerBox.querySelector('.chat-input-shell-mode');
  assert.ok(!badge || badge.style.display === 'none');

  // 2. Typing !ls -la -> shows shell mode badge
  textarea.value = '!ls -la';
  updateShellModeHint(tabObj);
  badge = composerBox.querySelector('.chat-input-shell-mode');
  assert.ok(badge, 'Badge element should be created');
  assert.equal(badge.style.display, 'inline-flex');
  assert.ok(badge.textContent.includes('SHELL'));

  // 3. Clear ! -> hides badge
  textarea.value = 'ls -la';
  updateShellModeHint(tabObj);
  assert.equal(badge.style.display, 'none');
});

test('interceptChatInput executes shell command and renders .chat-bash-card with attach action', async () => {
  const { interceptChatInput } = await import('./chat-commands.js');

  const container = document.createElement('div');
  const inputEl = document.createElement('textarea');
  const tabObj = {
    id: 'claude-code:local:shell-test-1',
    session: { id: 'claude-code:local:shell-test-1', agent: 'claude-code' },
    chatMessagesEl: container,
    chatInputEl: inputEl,
    ephemeralCards: []
  };

  const prevFetch = globalThis.fetch;
  let shellCalled = false;
  globalThis.fetch = async (url, opts) => {
    if (String(url).includes('/shell')) {
      shellCalled = true;
      const body = JSON.parse(opts.body);
      assert.equal(body.command, 'git status -s');
      return {
        ok: true,
        status: 200,
        json: async () => ({
          command: 'git status -s',
          cwd: '/repo',
          stdout: 'M web/js/chat.js',
          stderr: '',
          exit_code: 0,
          duration_ms: 12,
          timed_out: false,
          truncated: false
        })
      };
    }
    return { ok: true, json: async () => ({}) };
  };

  try {
    inputEl.value = '! git status -s';
    const intercepted = await interceptChatInput(tabObj, '! git status -s');
    assert.equal(intercepted, true, 'Shell command should be intercepted');
    assert.equal(shellCalled, true, 'Shell API must be invoked');
    assert.equal(inputEl.value, '', 'Input should be cleared');

    const bashCard = container.querySelector('.chat-bash-card');
    assert.ok(bashCard, 'Should render .chat-bash-card');
    assert.ok(bashCard.textContent.includes('M web/js/chat.js'));

    const attachBtn = bashCard.querySelector('.btn-bash-attach');
    assert.ok(attachBtn, 'Must render [+ Attach to Prompt] button');
    attachBtn.click();

    assert.ok(inputEl.value.includes('```sh\n$ git status -s\nM web/js/chat.js\n```'));
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('interceptChatInput handles /effort on antigravity without PATCHing config', async () => {
  const { interceptChatInput } = await import('./chat-commands.js');

  const container = document.createElement('div');
  const inputEl = document.createElement('textarea');
  const tabObj = {
    id: 'antigravity:local:agy-1',
    session: { id: 'antigravity:local:agy-1', agent: 'antigravity' },
    chatMessagesEl: container,
    chatInputEl: inputEl,
    ephemeralCards: []
  };

  let configPatchCalled = false;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes('/config')) {
      configPatchCalled = true;
    }
    return { ok: true, json: async () => ({}) };
  };

  try {
    inputEl.value = '/effort high';
    const intercepted = await interceptChatInput(tabObj, '/effort high');
    assert.equal(intercepted, true);
    assert.equal(configPatchCalled, false, 'Must not patch config on antigravity for effort');
    const notice = container.querySelector('.chat-config-notice');
    assert.ok(notice);
    assert.ok(notice.textContent.includes('Antigravity models encode reasoning effort in model tiers'));
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('interceptChatInput handles /mcp mounting status card', async () => {
  const { interceptChatInput } = await import('./chat-commands.js');

  const container = document.createElement('div');
  const inputEl = document.createElement('textarea');
  const tabObj = {
    id: 'claude-code:local:mcp-test-1',
    session: { id: 'claude-code:local:mcp-test-1', agent: 'claude-code' },
    chatMessagesEl: container,
    chatInputEl: inputEl,
    ephemeralCards: []
  };

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => {
    return {
      ok: true,
      json: async () => ({
        configured_count: 1,
        active_count: 1,
        servers: [
          { name: 'sqlite', status: 'connected', scope: 'project', transport: 'stdio' }
        ]
      })
    };
  };

  try {
    inputEl.value = '/mcp';
    const intercepted = await interceptChatInput(tabObj, '/mcp');
    assert.equal(intercepted, true);
    const mcpCard = container.querySelector('.chat-inspect-mcp');
    assert.ok(mcpCard, 'Must append .chat-inspect-mcp ephemeral card');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('loadChatTranscript renders reverse-streamed NDJSON progressively with bottom-up order and load-more banner', async () => {
  const container = document.createElement('div');
  const tabObj = {
    id: 'claude-code:local:ndjson-sess',
    session: { id: 'claude-code:local:ndjson-sess', agent: 'claude-code' },
    chatMessagesEl: container,
    ephemeralCards: []
  };

  const total = 30;
  const encoder = new TextEncoder();
  const chunks = [];

  // Line 1: meta
  chunks.push(`{"type":"meta","total":${total},"session_id":"claude-code:local:ndjson-sess"}\n`);

  // Lines in reverse order from 29 down to 0
  for (let i = total - 1; i >= 0; i--) {
    const role = (i % 2 === 0) ? 'user' : 'assistant';
    const msg = { role, content: `Message ${i}` };
    chunks.push(`{"type":"msg","index":${i},"message":${JSON.stringify(msg)}}\n`);
  }

  const stream = new ReadableStream({
    start(controller) {
      for (const c of chunks) {
        controller.enqueue(encoder.encode(c));
      }
      controller.close();
    }
  });

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes('/v1/sessions/transcript')) {
      return {
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/x-ndjson; charset=utf-8' }),
        body: stream
      };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  try {
    await loadChatTranscript(tabObj);

    assert.equal(tabObj.allTranscriptMessages.length, 30, 'All 30 messages must be stored in tabObj.allTranscriptMessages');
    assert.equal(tabObj.loadedTranscriptStartIndex, 5, 'loadedTranscriptStartIndex must be 5 (30 - 25)');

    const domMsgs = container.querySelectorAll('.chat-msg');
    assert.equal(domMsgs.length, 25, 'Container must render exactly CHAT_PAGE_SIZE (25) visible messages');

    // First visible message in DOM must be message 5
    assert.equal(domMsgs[0].dataset.msgIndex, '5', 'First visible DOM node dataset.msgIndex must be 5');
    assert.equal(domMsgs[0].dataset.rawContent, 'Message 5', 'First visible DOM node dataset.rawContent must be Message 5');

    // Message 6 (user message) text content contains Message 6
    assert.ok(domMsgs[1].textContent.includes('Message 6'), 'Second visible message in DOM must be Message 6');
    assert.equal(domMsgs[1].dataset.msgIndex, '6');

    // Last visible message in DOM must be message 29
    assert.equal(domMsgs[domMsgs.length - 1].dataset.msgIndex, '29', 'Last visible DOM node dataset.msgIndex must be 29');
    assert.equal(domMsgs[domMsgs.length - 1].dataset.rawContent, 'Message 29', 'Last visible DOM node dataset.rawContent must be Message 29');

    // Load-more banner must be present with remaining count
    const banner = container.querySelector('.chat-load-more-container');
    assert.ok(banner, 'Must render .chat-load-more-container when total > CHAT_PAGE_SIZE');
    assert.ok(banner.textContent.includes('5 remaining'), 'Banner must indicate 5 remaining older messages');

    // Older messages 0-4 are stored in tabObj.allTranscriptMessages
    assert.equal(tabObj.allTranscriptMessages[0].content, 'Message 0');
    assert.equal(tabObj.allTranscriptMessages[4].content, 'Message 4');

    // Call loadOlderTranscriptMessages to page in messages 0-4
    loadOlderTranscriptMessages(tabObj);
    assert.equal(tabObj.loadedTranscriptStartIndex, 0, 'loadedTranscriptStartIndex must be 0 after loading older batch');
    assert.equal(container.querySelectorAll('.chat-msg').length, 30, 'Container must now have all 30 messages rendered');
    assert.equal(container.querySelector('.chat-load-more-container'), null, 'Load more banner must be removed when all messages are visible');

    // Subsequent loadChatTranscript call should reuse DOM and preserve loadedTranscriptStartIndex === 0
    const stream2 = new ReadableStream({
      start(controller) {
        for (const c of chunks) {
          controller.enqueue(encoder.encode(c));
        }
        controller.close();
      }
    });
    globalThis.fetch = async (url) => {
      if (String(url).includes('/v1/sessions/transcript')) {
        return {
          ok: true,
          status: 200,
          headers: new Headers({ 'content-type': 'application/x-ndjson; charset=utf-8' }),
          body: stream2
        };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    };

    await loadChatTranscript(tabObj);
    assert.equal(tabObj.loadedTranscriptStartIndex, 0, 'Calling loadChatTranscript again must preserve loadedTranscriptStartIndex = 0 on DOM reuse');
    assert.equal(container.querySelectorAll('.chat-msg').length, 30, 'Container must still have exactly 30 messages without duplication');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('loadChatTranscript coalesces in-flight requests (<50ms coalesces; >50ms schedules reload)', async () => {
  const container = document.createElement('div');
  const tabObj = {
    id: 'claude-code:local:coalesce-sess',
    session: { id: 'claude-code:local:coalesce-sess', agent: 'claude-code' },
    chatMessagesEl: container,
    ephemeralCards: []
  };

  let fetchCount = 0;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes('/v1/sessions/transcript')) {
      fetchCount++;
      // Simulate network latency (e.g., 80ms)
      await new Promise(r => setTimeout(r, 80));
      return {
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: async () => ({ messages: [], total: 0 })
      };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  try {
    const p1 = loadChatTranscript(tabObj);
    assert.equal(fetchCount, 1, 'First call should immediately trigger fetch');

    // Call <50ms should coalesce into p1 without extra fetch
    const p2 = loadChatTranscript(tabObj);
    assert.equal(fetchCount, 1, 'Call <50ms should coalesce into same in-flight fetch');

    // Wait 60ms (>50ms after first call, but before p1 finishes at 80ms)
    await new Promise(r => setTimeout(r, 60));
    assert.equal(fetchCount, 1, 'Fetch should still be in flight at 60ms');
    const p3 = loadChatTranscript(tabObj);
    assert.equal(tabObj._pendingTranscriptReload, true, 'Call >50ms while fetch is pending should mark _pendingTranscriptReload');

    // Await all
    await Promise.all([p1, p2, p3]);

    assert.equal(fetchCount, 2, 'Follow-up fetch should have executed after first completed');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('async NDJSON stream race yields mid-load while SSE events trigger activity; strictly 1 activity bubble remains', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';
  const tabEl = new MockElement('div');
  tabEl.className = 'tab active';
  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const tabObj = {
    id: 'tab-ndjson-race',
    viewMode: 'chat',
    tabEl,
    chatMessagesEl: container,
    chatSendBtn: sendBtn,
    chatCancelBtn: cancelBtn,
    chatStatusBadge: statusBadge,
    session: {
      id: 'claude-code:local:sess-race',
      agent: 'claude-code',
      engine_type: 'headless',
      state: 1
    }
  };

  const metaLine = JSON.stringify({
    type: 'meta',
    total: 1,
    session_id: 'claude-code:local:sess-race',
    in_flight_turn: {
      turn_id: 'turn-race',
      active: true,
      prompt: 'Refactor auth',
      timestamp: new Date().toISOString()
    }
  }) + '\n';

  const msgLine = JSON.stringify({
    type: 'msg',
    index: 0,
    message: {
      role: 'user',
      content: 'Refactor auth',
      turn_id: 'turn-race',
      timestamp: new Date().toISOString()
    }
  }) + '\n';

  const encoder = new TextEncoder();
  const stream = new ReadableStream({
    async start(controller) {
      controller.enqueue(encoder.encode(metaLine));
      // Simulate mid-stream asynchronous delay
      await new Promise(r => setTimeout(r, 20));
      controller.enqueue(encoder.encode(msgLine));
      controller.close();
    }
  });

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    if (String(url).includes('/v1/sessions/transcript')) {
      return {
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/x-ndjson; charset=utf-8' }),
        body: stream
      };
    }
    return { ok: true, status: 200, json: async () => ({ items: [], paused: false }) };
  };

  try {
    const transcriptPromise = loadChatTranscript(tabObj);

    // During the mid-stream delay, SSE turn_start and thought_delta arrive
    await new Promise(r => setTimeout(r, 5));
    handleChatStreamEvent(tabObj, {
      type: 'turn_start',
      turn_id: 'turn-race',
      text: 'Refactor auth',
      seq: 1
    });
    handleChatStreamEvent(tabObj, {
      type: 'thought_delta',
      turn_id: 'turn-race',
      thinking: 'Analyzing module boundaries...',
      seq: 2
    });

    await transcriptPromise;

    const activityEls = container.querySelectorAll('.chat-instream-activity');
    assert.equal(activityEls.length, 1, 'Container must have strictly 1 .chat-instream-activity after NDJSON stream race');
    assert.ok(tabObj.activeInStreamActivityEl && tabObj.activeInStreamActivityEl.isConnected, 'Active activity element must remain connected');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('no dual Thinking bubbles: in_flight_turn + setChatSending + thought_delta preserves only thinking details and zero instream activity', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';
  const tabEl = new MockElement('div');
  tabEl.className = 'tab active';
  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const tabObj = {
    id: 'tab-no-dual-thinking',
    viewMode: 'chat',
    tabEl,
    chatMessagesEl: container,
    chatSendBtn: sendBtn,
    chatCancelBtn: cancelBtn,
    chatStatusBadge: statusBadge,
    session: {
      id: 'claude-code:local:sess-no-dual',
      agent: 'claude-code',
      engine_type: 'headless',
      state: 1
    }
  };

  const prevFetch = globalThis.fetch;
  const turnTs = new Date().toISOString();
  globalThis.fetch = async (url) => {
    if (String(url).includes('/v1/sessions/transcript')) {
      return {
        ok: true,
        status: 200,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: async () => ({
          messages: [
            {
              role: 'user',
              content: 'Check system health',
              timestamp: turnTs,
              turn_id: 'turn-health',
              in_flight: true
            },
            {
              role: 'assistant',
              content: '',
              thinking: 'Checking spool...',
              timestamp: turnTs,
              turn_id: 'turn-health',
              in_flight: true
            }
          ],
          in_flight_turn: {
            turn_id: 'turn-health',
            last_seq: 2,
            active: true,
            prompt: 'Check system health',
            assistant_text: '',
            thinking: 'Checking spool...',
            timestamp: turnTs
          }
        })
      };
    }
    return { ok: true, status: 200, json: async () => ({ items: [], paused: false }) };
  };

  try {
    // 1. Initial prompt submission puts UI in sending mode (shows initial thinking bubble)
    setChatSending(tabObj, true);
    assert.equal(container.querySelectorAll('.chat-instream-activity').length, 1, 'Initial setChatSending creates thinking bubble');

    // 2. Transcript reloads and restores in_flight_turn with thinking details
    await loadChatTranscript(tabObj);

    // 3. Subsequent thought_delta arrives
    handleChatStreamEvent(tabObj, {
      type: 'thought_delta',
      turn_id: 'turn-health',
      thinking: 'Spool verified healthy.',
      seq: 3
    });

    const thinkingDetails = container.querySelectorAll('.chat-thinking');
    assert.equal(thinkingDetails.length, 1, 'Must have strictly 1 .chat-thinking details element');
    const activityEls = container.querySelectorAll('.chat-instream-activity');
    assert.equal(activityEls.length, 0, 'Must have 0 .chat-instream-activity bubbles when assistant thinking details exist');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('activity timer preservation across tab switches and thought_delta chunks', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';
  const tabEl = new MockElement('div');
  tabEl.className = 'tab active';
  const sendBtn = new MockElement('button');
  const cancelBtn = new MockElement('button');
  cancelBtn.style.display = 'none';
  const statusBadge = new MockElement('span');

  const tabObj = {
    id: 'tab-timer-preserve',
    viewMode: 'chat',
    tabEl,
    chatMessagesEl: container,
    chatSendBtn: sendBtn,
    chatCancelBtn: cancelBtn,
    chatStatusBadge: statusBadge,
    session: {
      id: 'claude-code:local:sess-timer',
      agent: 'claude-code',
      engine_type: 'headless',
      state: 1
    }
  };

  const started45sAgo = new Date(Date.now() - 45000).toISOString();

  // 1. Turn starts with a 45s-old timestamp
  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    turn_id: 'turn-timer',
    text: 'Long running task',
    timestamp: started45sAgo,
    seq: 1
  });

  assert.ok(tabObj.activeInStreamActivityEl, 'Activity element must be present');
  let timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');
  assert.ok(timerEl, 'Timer element must exist');
  let match = timerEl.textContent.match(/\((\d+)s\)/);
  assert.ok(match, 'Timer format must match (Ns)');
  assert.ok(parseInt(match[1], 10) >= 45, `Timer must display >= 45s, got: ${timerEl.textContent}`);

  // 2. Multiple thought_delta chunks arrive; timer must NOT reset to (0s)
  handleChatStreamEvent(tabObj, {
    type: 'thought_delta',
    turn_id: 'turn-timer',
    thinking: 'Step 1...',
    timestamp: new Date(Date.now() - 43000).toISOString(),
    seq: 2
  });
  handleChatStreamEvent(tabObj, {
    type: 'thought_delta',
    turn_id: 'turn-timer',
    thinking: 'Step 2...',
    timestamp: new Date(Date.now() - 40000).toISOString(),
    seq: 3
  });

  timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');
  match = timerEl.textContent.match(/\((\d+)s\)/);
  assert.ok(match && parseInt(match[1], 10) >= 45, `Timer must remain >= 45s after thought deltas, got: ${timerEl.textContent}`);

  // 3. Tab switch away: disconnectChatStream pauses the timer without wiping start time
  disconnectChatStream(tabObj);
  assert.equal(tabObj.activeActivityTimer, null, 'Timer interval must be cleared on disconnect');

  // 4. Tab switch back: setChatSending and resumeInStreamActivity resume the timer
  setChatSending(tabObj, true);
  resumeInStreamActivity(tabObj);

  timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');
  match = timerEl.textContent.match(/\((\d+)s\)/);
  assert.ok(match && parseInt(match[1], 10) >= 45, `Timer must resume with >= 45s, got: ${timerEl.textContent}`);

  // 5. Fresh tab object simulation (opening session with daemonActivityStartedAt)
  const container2 = new MockElement('div');
  container2.className = 'chat-messages-container';
  const tabObj2 = {
    id: 'tab-timer-fresh',
    viewMode: 'chat',
    chatMessagesEl: container2,
    chatSendBtn: new MockElement('button'),
    chatCancelBtn: new MockElement('button'),
    chatStatusBadge: new MockElement('span'),
    daemonActivityStartedAt: started45sAgo,
    session: {
      id: 'claude-code:local:sess-timer',
      agent: 'claude-code',
      engine_type: 'headless',
      state: 1
    }
  };

  setChatSending(tabObj2, true);
  assert.ok(tabObj2.activeInStreamActivityEl, 'Fresh tab activity element must be created');
  const timerEl2 = tabObj2.activeInStreamActivityEl.querySelector('.chat-activity-timer');
  const match2 = timerEl2.textContent.match(/\((\d+)s\)/);
  assert.ok(match2 && parseInt(match2[1], 10) >= 45, `Fresh tab timer must display >= 45s from daemon timestamp, got: ${timerEl2.textContent}`);
});





