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

  replaceWith(...nodes) {
    if (!this.parentNode) return;
    for (const node of nodes) {
      this.parentNode.insertBefore(node, this);
    }
    this.remove();
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
    const titleMatch = attrs.match(/title=["']([^"']*)["']/);
    if (titleMatch) {
      el.title = titleMatch[1];
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
globalThis.window.marked = {
  use(ext) {
    if (ext && ext.renderer) this._renderer = ext.renderer;
  },
  setOptions(opts) {
    if (opts && opts.renderer) this._renderer = opts.renderer;
  },
  parse(md) {
    let parsed = (md || '')
      .replace(/\[(.*?)\]\((.*?)\)/g, (_, text, href) => {
        if (this._renderer && typeof this._renderer.link === 'function') {
          return this._renderer.link(href, '', text);
        }
        return `<a href="${href}">${text}</a>`;
      })
      .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
      .replace(/__(.*?)__/g, '<strong>$1</strong>');
    return `<p>${parsed}</p>`;
  }
};
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
  resumeInStreamActivity,
  formatModelLabel,
  getAgentModelOptions,
  GROK_DEFAULT_MODELS,
  updateStatusPills,
  toggleChatConfigPopover,
  createChatMessageElement
} = chat;

const chatAuth = await import('./chat-auth.js');
const {
  isSafeHTTPSUrl,
  renderAuthCard,
  mountMCPStatus,
  invalidateMCPStatusCache
} = chatAuth;

const {
  hasMarkdownSyntax,
  sanitizeRenderedMarkdownHtml,
  renderMarkdown
} = await import('./utils.js');

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

test('formatModelLabel resolves Carbon, Hydrogen XHigh, [1m], and Claude aliases', () => {
  assert.equal(formatModelLabel('gemini-4-carbon-medium', 'antigravity'), 'Gemini 4 Carbon (Medium)');
  assert.equal(formatModelLabel('gemini-4-hydrogen-xhigh', 'antigravity'), 'Gemini 4 Hydrogen (XHigh)');
  assert.equal(formatModelLabel('gemini-pro-orchestrator', 'antigravity'), 'Orchestrator (Carbon)');
  assert.equal(formatModelLabel('gemini-pro-orchestrator-argon', 'antigravity'), 'Orchestrator (Argon)');
  assert.equal(formatModelLabel('claude-opus-5-5-high', 'antigravity'), 'Claude Opus 5.5 (High)');
  assert.equal(formatModelLabel('claude-fable-5-1[1m]', 'claude-code'), 'Claude Fable 5.1');
  assert.equal(formatModelLabel('opus', 'claude-code'), 'Claude Opus');
  assert.equal(formatModelLabel('sonnet', 'claude-code'), 'Claude Sonnet');
  assert.equal(formatModelLabel('haiku', 'claude-code'), 'Claude Haiku');
  assert.equal(formatModelLabel('opusplan', 'claude-code'), 'Claude Opus (Plan)');
  assert.equal(formatModelLabel('grok-4.7', 'grok'), 'Grok 4.7');
  assert.equal(formatModelLabel('grok-4.6', 'grok'), 'Grok 4.6');
  assert.equal(formatModelLabel('grok-build', 'grok'), 'Grok Build');
  assert.deepEqual(getAgentModelOptions('grok'), GROK_DEFAULT_MODELS);
  assert.equal(formatModelLabel('', 'claude-code'), 'Default Model');
});

test('updateStatusPills displays resolved_model when model is empty and caches default', () => {
  const pillEl = document.createElement('span');
  const tabObj = {
    session: {
      model: '',
      resolved_model: 'claude-fable-5-1',
      agent: 'claude-code'
    },
    chatPillModel: pillEl
  };

  updateStatusPills(tabObj);
  assert.equal(tabObj.chatPillModel.textContent, 'Claude Fable 5.1');
  assert.equal(tabObj.lastDefaultModel, 'claude-fable-5-1');

  tabObj.session.model = 'claude-opus-5-5';
  updateStatusPills(tabObj);
  assert.equal(tabObj.chatPillModel.textContent, 'Claude Opus 5.5');
  assert.equal(tabObj.lastDefaultModel, 'claude-fable-5-1');
});

test('renderConfigPopoverContent renders Default (<Model>) and handles model reset with omitempty merge', () => {
  const popover = document.createElement('div');
  const tabObj = {
    session: {
      id: 'sess-cfg-1',
      agent: 'claude-code',
      model: '',
      resolved_model: 'claude-fable-5-1'
    },
    chatConfigPopover: popover
  };

  renderConfigPopoverContent(tabObj);
  const selectModel = popover.querySelector('.config-select-model');
  assert.ok(selectModel);
  const defaultOption = selectModel.children[0];
  assert.equal(defaultOption.value, '');
  assert.equal(defaultOption.textContent, 'Default (Claude Fable 5.1)');

  tabObj.session.model = 'claude-opus-5-5';
  tabObj.lastDefaultModel = 'claude-fable-5-1';
  renderConfigPopoverContent(tabObj);
  const selectModel2 = popover.querySelector('.config-select-model');
  const defaultOption2 = selectModel2.children[0];
  assert.equal(defaultOption2.textContent, 'Default (Claude Fable 5.1)');
});

test('interceptChatInput handles /model status and reset to default', async () => {
  const { interceptChatInput } = await import('./chat-commands.js');
  const container = document.createElement('div');
  const inputEl = document.createElement('textarea');
  const pillEl = document.createElement('span');
  const tabObj = {
    id: 'claude-code:local:model-cmd-1',
    session: {
      id: 'claude-code:local:model-cmd-1',
      agent: 'claude-code',
      model: '',
      resolved_model: 'claude-fable-5-1'
    },
    chatMessagesEl: container,
    chatInputEl: inputEl,
    chatPillModel: pillEl,
    ephemeralCards: []
  };

  updateStatusPills(tabObj);
  assert.equal(pillEl.textContent, 'Claude Fable 5.1');

  inputEl.value = '/model';
  const interceptedStatus = await interceptChatInput(tabObj, '/model');
  assert.equal(interceptedStatus, true);
  const notice = container.querySelector('.chat-config-notice');
  assert.ok(notice);
  assert.ok(notice.textContent.includes('Default (Claude Fable 5.1)'));

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => {
    return {
      ok: true,
      json: async () => ({
        id: tabObj.session.id,
        agent: 'claude-code',
        resolved_model: 'claude-fable-5-1'
      })
    };
  };

  try {
    inputEl.value = '/model default';
    const interceptedReset = await interceptChatInput(tabObj, '/model default');
    assert.equal(interceptedReset, true);
    assert.equal(tabObj.session.model, '');
    assert.equal(pillEl.textContent, 'Claude Fable 5.1');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('renderConfigPopoverContent renders compact MCP submenu button with count and toggles submenu view', async () => {
  const popover = document.createElement('div');
  popover.className = 'chat-config-popover';
  const tabObj = {
    id: 'tab-cfg-submenu',
    session: {
      id: 'sess-popover-mcp',
      agent: 'claude-code',
      model: 'claude-sonnet-4-5',
      resolved_model: 'claude-sonnet-4-5',
      permission_mode: 'bypassPermissions'
    },
    chatConfigPopover: popover
  };

  const mcpData = {
    servers: Array.from({ length: 12 }, (_, i) => ({
      name: `srv-${i + 1}`,
      target: `mcp:srv-${i + 1}`,
      transport: 'stdio',
      status: i < 2 ? 'needs_auth' : 'connected',
      auth_supported: i < 2
    })),
    plugins: [
      { name: 'plugin-auth', target: 'plugin:plugin-auth', installed: true, authenticated: true },
      { name: 'plugin-unauth', target: 'plugin:plugin-unauth', installed: true, authenticated: false },
      { name: 'plugin-uninstalled', target: 'plugin:plugin-uninstalled', installed: false, authenticated: false }
    ]
  };

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    const urlStr = String(url);
    if (urlStr.includes('/mcp-status')) {
      return {
        ok: true,
        status: 200,
        json: async () => mcpData
      };
    }
    return { ok: true, status: 200, json: async () => ({}) };
  };

  try {
    renderConfigPopoverContent(tabObj);
    const initialLabelEl = popover.querySelector('.config-mcp-submenu-label');
    assert.ok(initialLabelEl);
    assert.equal(initialLabelEl.textContent, '🔌 MCP (…)', 'Initial label must show loading state without text shift');

    await new Promise((r) => setTimeout(r, 10));

    const mainView = popover.querySelector('.config-main-view');
    const submenuView = popover.querySelector('.config-mcp-submenu-view');
    assert.ok(mainView, 'Main view container must exist');
    assert.ok(submenuView, 'Submenu view container must exist');

    assert.notEqual(mainView.style.display, 'none', 'Main view should be visible initially');
    assert.equal(submenuView.style.display, 'none', 'Submenu view should be hidden initially');
    assert.equal(tabObj.configSubmenuView, null, 'tabObj.configSubmenuView should be null initially');

    const submenuBtn = popover.querySelector('.config-mcp-submenu-btn');
    assert.ok(submenuBtn, 'Submenu button should be rendered');
    const labelEl = submenuBtn.querySelector('.config-mcp-submenu-label');
    assert.ok(labelEl);
    assert.equal(labelEl.textContent, '🔌 MCP (14)', 'Label must display total count (12 servers + 2 installed plugins)');

    const alertEl = submenuBtn.querySelector('.config-mcp-submenu-alert');
    assert.ok(alertEl);
    assert.equal(alertEl.style.display, '', 'Alert badge must be visible when auth is needed');
    assert.equal(alertEl.textContent, '3 need auth', 'Alert badge must count items needing auth (2 servers + 1 plugin)');

    submenuBtn.click();

    assert.equal(tabObj.configSubmenuView, 'mcp', 'tabObj.configSubmenuView must be "mcp" after clicking submenu button');
    assert.equal(mainView.style.display, 'none', 'Main view must be hidden in submenu view');
    assert.equal(submenuView.style.display, '', 'Submenu view must be visible');

    const headerTitleEl = popover.querySelector('.config-popover-title');
    assert.ok(headerTitleEl);
    const btnBack = headerTitleEl.querySelector('.btn-back-popover');
    assert.ok(btnBack, 'Back button must be rendered in header title');
    const headerCount = headerTitleEl.querySelector('.config-mcp-header-count');
    assert.ok(headerCount);
    assert.equal(headerCount.textContent, '14', 'Header title must show 14');

    const pillRows = submenuView.querySelectorAll('.mcp-pill-row');
    assert.equal(pillRows.length, 14, 'Submenu view must render all 14 rows');

    renderConfigPopoverContent(tabObj);
    await new Promise((r) => setTimeout(r, 10));

    const mainViewRe = popover.querySelector('.config-main-view');
    const submenuViewRe = popover.querySelector('.config-mcp-submenu-view');
    assert.equal(mainViewRe.style.display, 'none', 'Main view must stay hidden on re-render');
    assert.equal(submenuViewRe.style.display, '', 'Submenu view must stay visible on re-render');
    assert.equal(tabObj.configSubmenuView, 'mcp', 'tabObj.configSubmenuView must remain "mcp" on re-render');

    const btnBackRe = popover.querySelector('.btn-back-popover');
    assert.ok(btnBackRe, 'Back button must exist in re-rendered view');
    btnBackRe.click();

    assert.equal(tabObj.configSubmenuView, null, 'tabObj.configSubmenuView must be null after clicking back');
    assert.notEqual(mainViewRe.style.display, 'none', 'Main view must be visible after back');
    assert.equal(submenuViewRe.style.display, 'none', 'Submenu view must be hidden after back');

    const selectModel = mainViewRe.querySelector('.config-select-model');
    assert.ok(selectModel, 'Model selection in main view must be accessible');

    submenuBtn.click();
    assert.equal(tabObj.configSubmenuView, 'mcp');
    const btnClose = popover.querySelector('.btn-close-popover');
    btnClose.click();
    assert.equal(tabObj.configSubmenuView, null, 'Closing via close button must reset configSubmenuView');
    assert.equal(popover.style.display, 'none');

    tabObj.configSubmenuView = 'mcp';
    popover.style.display = 'none';
    toggleChatConfigPopover(tabObj);
    assert.equal(tabObj.configSubmenuView, null, 'toggleChatConfigPopover must reset configSubmenuView on open');
    assert.equal(popover.style.display, 'flex');

    tabObj.configSubmenuView = 'mcp';
    toggleChatConfigPopover(tabObj);
    assert.equal(tabObj.configSubmenuView, null, 'toggleChatConfigPopover must reset configSubmenuView on close');
    assert.equal(popover.style.display, 'none');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('handleChatStreamEvent keeps stream buffer intact across interleaved tool events and renders bold HTML', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-test-interleaved-tool',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: { id: 'claude-code:local:sess-tool-bold', agent: 'claude-code', state: 0 },
    activeTurnBuffer: '',
    activeTurnTools: []
  };

  handleChatStreamEvent(tabObj, {
    type: 'turn_start',
    turn_id: 'turn-1',
    text: 'do task'
  });

  handleChatStreamEvent(tabObj, {
    type: 'text_delta',
    turn_id: 'turn-1',
    seq: 1,
    text: 'Point 3 controls **what shopping, pricing, and t'
  });

  handleChatStreamEvent(tabObj, {
    type: 'tool_start',
    turn_id: 'turn-1',
    seq: 2,
    tool_name: 'inspect'
  });

  handleChatStreamEvent(tabObj, {
    type: 'tool_result',
    turn_id: 'turn-1',
    seq: 3,
    tool_name: 'inspect',
    tool_output: 'ok'
  });

  handleChatStreamEvent(tabObj, {
    type: 'text_delta',
    turn_id: 'turn-1',
    seq: 4,
    text: 'echnical build data**'
  });
  await new Promise((r) => setTimeout(r, 15));

  assert.equal(
    tabObj.activeTurnBuffer,
    'Point 3 controls **what shopping, pricing, and technical build data**',
    'Active turn buffer must not have double-newlines injected mid-word'
  );

  const assistantMsg = container.querySelector('.chat-msg.assistant-msg');
  assert.ok(assistantMsg, 'Assistant message element must exist');
  const bodyEl = assistantMsg.querySelector('.chat-msg-body');
  assert.ok(bodyEl, 'Assistant body element must exist');
  assert.ok(
    bodyEl.innerHTML.includes('<strong>what shopping, pricing, and technical build data</strong>'),
    `Rendered HTML must contain strong tag, got: ${bodyEl.innerHTML}`
  );
});

test('createChatMessageElement renders markdown-body with strong tag for bold user message', () => {
  const tabObj = {
    id: 'tab-test-user-bold',
    session: { id: 'claude-code:local:sess-user-bold', agent: 'claude-code', state: 0 }
  };

  const userMsgEl = createChatMessageElement(tabObj, {
    role: 'user',
    content: 'Here is a **bold item** to inspect'
  });

  assert.ok(userMsgEl, 'User message element must be created');
  const bodyEl = userMsgEl.querySelector('.chat-msg-body.markdown-body');
  assert.ok(bodyEl, 'User message body must have .chat-msg-body.markdown-body class');
  assert.ok(
    bodyEl.innerHTML.includes('<strong>bold item</strong>'),
    `Body innerHTML must contain strong tag, got: ${bodyEl.innerHTML}`
  );
  assert.ok(
    !bodyEl.innerHTML.includes('**bold item**'),
    `Body innerHTML must not contain literal markdown asterisks, got: ${bodyEl.innerHTML}`
  );
});

test('createChatMessageElement and renderMarkdown neutralize dangerous URI schemes with control characters to href="#"', () => {
  const tabObj = {
    id: 'tab-test-dangerous-link',
    session: { id: 'claude-code:local:sess-danger-link', agent: 'claude-code', state: 0 }
  };

  const dangerousPrompts = [
    '[Click](jav\tascript:alert(1))',
    '[Click](jav\x00ascript:alert(2))',
    '[Click](vbscript:msgbox(1))',
    '[Click](data:text/html,<script>alert(1)</script>)'
  ];

  for (const prompt of dangerousPrompts) {
    const userMsgEl = createChatMessageElement(tabObj, {
      role: 'user',
      content: prompt
    });
    assert.ok(userMsgEl, 'User message element must be created');
    const bodyEl = userMsgEl.querySelector('.chat-msg-body.markdown-body');
    assert.ok(bodyEl, 'User message body must have .chat-msg-body.markdown-body class');
    assert.ok(
      bodyEl.innerHTML.includes('href="#"'),
      `Dangerous link must be sanitized to href="#", got: ${bodyEl.innerHTML}`
    );
    assert.ok(
      !bodyEl.innerHTML.includes('javascript:') && !bodyEl.innerHTML.includes('vbscript:') && !bodyEl.innerHTML.includes('data:text/html'),
      `Executable scheme must not be present, got: ${bodyEl.innerHTML}`
    );
    assert.ok(
      !bodyEl.innerHTML.includes('target="_blank"'),
      `Sanitized anchor must not have target="_blank", got: ${bodyEl.innerHTML}`
    );
  }
});

test('sanitizeRenderedMarkdownHtml strips dangerous HTML tags and SVG SMIL animation vectors', () => {
  const xssPayloads = [
    '*notice* <base href="https://attacker.com/">',
    '*notice* <style>body{display:none}</style>',
    '*notice* <form action="javascript:evil()"><input type="submit"></form>',
    '*notice* <meta http-equiv="refresh" content="0;url=http://attacker.com">',
    '*notice* <link rel="stylesheet" href="http://attacker.com/evil.css">',
    '*svg* <svg><a id="x"><animate attributeName="href" values="javascript:alert(1)" /></a></svg>',
    '*svg* <svg><a id="y"><set attributeName="href" to="jav\tascript:alert(2)" /></a></svg>',
    '*iframe* <iframe srcdoc="<script>alert(1)</script>"></iframe>',
    '<img src="javascript:alert(1)">',
    '<img src="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">',
    '<div onclick="alert(1)">click</div>'
  ];

  for (const payload of xssPayloads) {
    const clean = sanitizeRenderedMarkdownHtml(payload);
    assert.ok(!clean.includes('<base'), `Payload must not contain <base: ${clean}`);
    assert.ok(!clean.includes('<style'), `Payload must not contain <style: ${clean}`);
    assert.ok(!clean.includes('<form'), `Payload must not contain <form: ${clean}`);
    assert.ok(!clean.includes('<meta'), `Payload must not contain <meta: ${clean}`);
    assert.ok(!clean.includes('<link'), `Payload must not contain <link: ${clean}`);
    assert.ok(!clean.includes('<animate'), `Payload must not contain <animate: ${clean}`);
    assert.ok(!clean.includes('<set'), `Payload must not contain <set: ${clean}`);
    assert.ok(!clean.includes('<iframe'), `Payload must not contain <iframe: ${clean}`);
    assert.ok(!clean.includes('srcdoc'), `Payload must not contain srcdoc: ${clean}`);
    assert.ok(!clean.includes('onclick'), `Payload must not contain onclick: ${clean}`);
    assert.ok(!clean.includes('javascript:'), `Payload must not contain javascript:: ${clean}`);
  }

  // Safe raster data URI on img src must be preserved
  const safeRaster = '<img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==" alt="dot">';
  const cleanedRaster = sanitizeRenderedMarkdownHtml(safeRaster);
  assert.ok(cleanedRaster.includes('data:image/png;base64,'), `Safe raster preview must be preserved: ${cleanedRaster}`);

  // Dangerous SVG data URI on img src must be stripped
  const dangerousSvg = '<img src="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciPjxzY3JpcHQ+YWxlcnQoMSk8L3NjcmlwdD48L3N2Zz4=" alt="svg">';
  const cleanedSvg = sanitizeRenderedMarkdownHtml(dangerousSvg);
  assert.ok(!cleanedSvg.includes('data:image/svg+xml'), `Dangerous SVG data URI on src must be stripped: ${cleanedSvg}`);
});

test('sanitizeRenderedMarkdownHtml DOM template branch strips dangerous tags and neutralizes attributes', () => {
  const origCreateElement = globalThis.document.createElement;
  try {
    let removedTags = [];
    let setAttributes = [];
    let removedAttributes = [];

    globalThis.document.createElement = (tag) => {
      if (tag === 'template') {
        return {
          content: {
            querySelectorAll(sel) {
              if (sel.includes('script')) {
                return [
                  { tagName: 'BASE', remove() { removedTags.push('base'); } },
                  { tagName: 'STYLE', remove() { removedTags.push('style'); } },
                  { tagName: 'ANIMATE', remove() { removedTags.push('animate'); } }
                ];
              }
              return [
                {
                  attributes: [
                    { name: 'href', value: 'jav\tascript:alert(1)' },
                    { name: 'srcdoc', value: '<script>alert(1)</script>' },
                    { name: 'onclick', value: 'alert(1)' },
                    { name: 'src', value: 'data:text/html;base64,123' },
                    { name: 'values', value: 'url1; jav\tascript:alert(2)' }
                  ],
                  setAttribute(name, val) { setAttributes.push({ name, val }); },
                  removeAttribute(name) { removedAttributes.push(name); }
                }
              ];
            }
          },
          get innerHTML() { return '<p>clean-dom</p>'; },
          set innerHTML(_) {}
        };
      }
      return origCreateElement(tag);
    };

    const result = sanitizeRenderedMarkdownHtml('<div>dirty</div>');
    assert.equal(result, '<p>clean-dom</p>');
    assert.deepEqual(removedTags, ['base', 'style', 'animate']);
    assert.deepEqual(removedAttributes, ['srcdoc', 'onclick', 'src', 'values']);
    assert.deepEqual(setAttributes, [{ name: 'href', val: '#' }]);
  } finally {
    globalThis.document.createElement = origCreateElement;
  }
});

test('hasMarkdownSyntax supports single-character emphasis and ignores snake_case identifiers', () => {
  // Single-character and bounded emphasis
  assert.equal(hasMarkdownSyntax('*a*'), true, '*a* must be recognized as markdown');
  assert.equal(hasMarkdownSyntax('**a**'), true, '**a** must be recognized as markdown');
  assert.equal(hasMarkdownSyntax('_a_'), true, '_a_ must be recognized as markdown');
  assert.equal(hasMarkdownSyntax('__a__'), true, '__a__ must be recognized as markdown');
  assert.equal(hasMarkdownSyntax('~~a~~'), true, '~~a~~ must be recognized as markdown');

  // Multi-word emphasis
  assert.equal(hasMarkdownSyntax('*hello world*'), true);
  assert.equal(hasMarkdownSyntax('**bold phrase**'), true);
  assert.equal(hasMarkdownSyntax('_italic phrase_'), true);
  assert.equal(hasMarkdownSyntax('__bold phrase__'), true);

  // Snake_case identifiers in user prompt must NOT trigger markdown mode
  assert.equal(hasMarkdownSyntax('foo_bar_baz'), false, 'foo_bar_baz must not be treated as italic');
  assert.equal(hasMarkdownSyntax('inspect session_token_name here'), false, 'snake_case must not be treated as italic');
  assert.equal(hasMarkdownSyntax('run test_headless_stream_parser now'), false, 'snake_case must not be treated as italic');

  // Word-boundary underscores MUST trigger markdown mode
  assert.equal(hasMarkdownSyntax('hello _italic_ world'), true);
  assert.equal(hasMarkdownSyntax('(_italic_)'), true);

  // Plain text without markdown
  assert.equal(hasMarkdownSyntax('plain text prompt without markdown'), false);

  // Bounded scan does not freeze or falsely match on large unclosed input
  const largeUnclosed = 'plain text without closing markdown '.repeat(2000) + '** unclosed';
  const start = Date.now();
  assert.equal(hasMarkdownSyntax(largeUnclosed), false);
  const elapsed = Date.now() - start;
  assert.ok(elapsed < 100, `Bounded scan took ${elapsed}ms; must be under 100ms`);
});






test('live tool_start for call_mcp_tool renders badge and updates instream activity', () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-mcp-test-1',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: { id: 'antigravity:local:sess-1', agent: 'antigravity', state: 1 }
  };

  handleChatStreamEvent(tabObj, {
    type: 'tool_start',
    tool_name: 'call_mcp_tool',
    tool_input: { ServerName: 'ackbar', ToolName: 'get_current_task', Arguments: {} },
    timestamp: new Date().toISOString()
  });

  const groupCard = container.querySelector('.chat-tool-card.chat-tool-group');
  assert.ok(groupCard, 'Must render .chat-tool-group');
  const badgeEl = groupCard.querySelector('.chat-tool-call-badge');
  assert.ok(badgeEl);
  assert.equal(badgeEl.textContent, 'mcp/ackbar/get_current_task');

  const detailEl = groupCard.querySelector('.chat-tool-call-detail');
  assert.equal(detailEl, null, 'Must omit .chat-tool-call-detail when detail is empty');

  const titleEl = groupCard.querySelector('.tool-group-title');
  assert.ok(titleEl);
  assert.ok(titleEl.textContent.includes('mcp/ackbar/get_current_task'), 'Title must contain mcp tool name');

  const activityEl = container.querySelector('.chat-instream-activity');
  assert.ok(activityEl, 'Must render .chat-instream-activity');
  const activityText = activityEl.querySelector('.chat-activity-text');
  assert.ok(activityText);
  assert.equal(activityText.textContent, 'Calling MCP get_current_task (ackbar)...');
});

test('call_mcp_tool upgrades badge and attaches detail when tool_result supplies input', () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-mcp-test-2',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: { id: 'antigravity:local:sess-2', agent: 'antigravity', state: 1 }
  };

  handleChatStreamEvent(tabObj, {
    type: 'tool_start',
    tool_name: 'call_mcp_tool',
    tool_input: null,
    timestamp: new Date().toISOString()
  });

  const groupCard = container.querySelector('.chat-tool-card.chat-tool-group');
  assert.ok(groupCard);
  let badgeEl = groupCard.querySelector('.chat-tool-call-badge');
  assert.equal(badgeEl.textContent, 'call_mcp_tool');
  assert.equal(groupCard.querySelector('.chat-tool-call-detail'), null);

  handleChatStreamEvent(tabObj, {
    type: 'tool_result',
    tool_name: 'call_mcp_tool',
    tool_input: { ServerName: 'ackbar', ToolName: 'list_tasks', Arguments: { status: 'IN_PROGRESS' } },
    tool_output: 'ok',
    timestamp: new Date().toISOString()
  });

  badgeEl = groupCard.querySelector('.chat-tool-call-badge');
  assert.equal(badgeEl.textContent, 'mcp/ackbar/list_tasks');

  const detailEl = groupCard.querySelector('.chat-tool-call-detail');
  assert.ok(detailEl, 'Must attach .chat-tool-call-detail on upgrade');
  assert.ok(detailEl.textContent.includes('IN_PROGRESS'));
});

test('mcp__linear__list_issues with empty input suppresses detail and renders badge', () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const tabObj = {
    id: 'tab-mcp-test-3',
    viewMode: 'chat',
    chatMessagesEl: container,
    session: { id: 'claude-code:local:sess-3', agent: 'claude-code', state: 1 }
  };

  handleChatStreamEvent(tabObj, {
    type: 'tool_start',
    tool_name: 'mcp__linear__list_issues',
    tool_input: {},
    timestamp: new Date().toISOString()
  });

  const groupCard = container.querySelector('.chat-tool-card.chat-tool-group');
  assert.ok(groupCard);
  const badgeEl = groupCard.querySelector('.chat-tool-call-badge');
  assert.ok(badgeEl);
  assert.equal(badgeEl.textContent, 'mcp/linear/list_issues');
  assert.equal(groupCard.querySelector('.chat-tool-call-detail'), null);
});

test('style.css resets white-space to normal on tool group content and items', async () => {
  const fs = await import('node:fs/promises');
  const cssPath = new URL('../style.css', import.meta.url);
  const css = await fs.readFile(cssPath, 'utf8');

  assert.match(css, /\.chat-tool-group-content\s*\{[^}]*white-space:\s*normal;/s);
  assert.match(css, /\.chat-tool-call-item\s*\{[^}]*white-space:\s*normal;/s);
});

test('tool_start followed by duplicate error and turn_complete collapses tool group, renders single error, and refreshes', async () => {
  const container = new MockElement('div');
  container.className = 'chat-messages-container';

  const fetchedUrls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    fetchedUrls.push(String(url));
    if (String(url).includes('/queue')) {
      return { ok: true, json: async () => ({ queue: [] }) };
    }
    if (String(url).includes('/sessions')) {
      return { ok: true, json: async () => [] };
    }
    return { ok: true, json: async () => ({}) };
  };

  try {
    const tabObj = {
      id: 'tab-test-error-resilience',
      viewMode: 'chat',
      chatMessagesEl: container,
      session: { id: 'antigravity:local:sess-err-test', agent: 'antigravity', state: 0 },
      activeTurnBuffer: '',
      activeTurnTools: [],
      inFlightPrompt: 'test prompt',
      promptQueue: [{ id: 'q1', text: 'queued item' }]
    };

    handleChatStreamEvent(tabObj, {
      type: 'turn_start',
      turn_id: 'turn-err-test',
      text: 'test prompt'
    });

    handleChatStreamEvent(tabObj, {
      type: 'text_delta',
      turn_id: 'turn-err-test',
      text: 'Starting computation...'
    });

    handleChatStreamEvent(tabObj, {
      type: 'tool_start',
      turn_id: 'turn-err-test',
      tool_name: 'test_tool',
      tool_input: {}
    });

    const toolGroup = container.querySelector('.chat-tool-group');
    assert.ok(toolGroup, 'Tool group must be rendered on tool_start');

    handleChatStreamEvent(tabObj, {
      type: 'error',
      turn_id: 'turn-err-test',
      text: 'Connection reset by peer'
    });

    handleChatStreamEvent(tabObj, {
      type: 'error',
      turn_id: 'turn-err-test',
      text: 'Connection reset by peer'
    });

    handleChatStreamEvent(tabObj, {
      type: 'turn_complete',
      turn_id: 'turn-err-test',
      text: 'Completed with partial findings'
    });

    assert.equal(toolGroup.open, false, 'Tool group must be collapsed on error');
    assert.equal(toolGroup.classList.contains('open'), false, 'open class must be removed');
    const inProgressItems = toolGroup.querySelectorAll('.chat-tool-call-item.in-progress');
    assert.equal(inProgressItems.length, 0, 'No tool item should remain in-progress');

    const assistantMsgs = container.querySelectorAll('.chat-msg.assistant-msg');
    assert.equal(assistantMsgs.length, 1, 'Exactly one assistant bubble should exist');
    const errorBanners = container.querySelectorAll('.chat-turn-error');
    assert.equal(errorBanners.length, 1, 'Error banner must be deduplicated to exactly 1');
    assert.ok(errorBanners[0].textContent.includes('Connection reset by peer'));

    const bodyEl = assistantMsgs[0].querySelector('.chat-msg-body');
    assert.ok(bodyEl, 'Message body must exist');
    assert.ok(bodyEl.textContent.includes('Starting computation'), 'Streamed markdown text must remain rendered');
    assert.ok(bodyEl.querySelector('.chat-turn-error'), 'chat-turn-error banner must remain inside bodyEl');

    assert.equal(tabObj.inFlightPrompt, null, 'inFlightPrompt must be cleared');
    assert.equal(tabObj.isQueuePaused, true, 'Queue must be paused on error');
    assert.ok(fetchedUrls.some(u => u.includes('/sessions')), 'fetchSessions must be called');
    assert.ok(fetchedUrls.some(u => u.includes('/queue')), 'loadChatQueue must be called');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('parseLineRange decodes file:// schemes, query params, line ranges, and percent-encoding', () => {
  const res1 = chat.parseLineRange('file:///Users/dev/Project/My%20Hero.png#L10');
  assert.equal(res1.baseFilePath, '/Users/dev/Project/My Hero.png');
  assert.equal(res1.startLine, '10');
  assert.equal(res1.vscodePath, '/Users/dev/Project/My Hero.png:10');

  const res2 = chat.parseLineRange('file://localhost/home/dev4u/image.avif?token=abc');
  assert.equal(res2.baseFilePath, '/home/dev4u/image.avif');

  const res3 = chat.parseLineRange('file:///C:/Projects/chart.png:42:5');
  assert.equal(res3.baseFilePath, 'C:/Projects/chart.png');
  assert.equal(res3.startLine, '42');
  assert.equal(res3.vscodePath, 'C:/Projects/chart.png:42:5');
});

test('isChatFilePath validates extensions, lengths, @ handles, spaces, and schemes', () => {
  assert.equal(chat.isChatFilePath('icon@2x.png'), true);
  assert.equal(chat.isChatFilePath('/Users/dev/@scope/img.png'), true);
  assert.equal(chat.isChatFilePath('image.avif'), true);
  assert.equal(chat.isChatFilePath('~/My Image.png'), true);
  assert.equal(chat.isChatFilePath('./out/my chart.png'), true);

  // Long paths > 250 chars up to 1024
  const longPath = '/home/user/' + 'a/'.repeat(150) + 'test.png';
  assert.ok(longPath.length > 250 && longPath.length < 1024);
  assert.equal(chat.isChatFilePath(longPath), true);

  // Rejections
  assert.equal(chat.isChatFilePath('user@example.com'), false);
  assert.equal(chat.isChatFilePath('git@github.com:org/repo.git'), false);
  assert.equal(chat.isChatFilePath('http://example.com/test.png'), false);
  assert.equal(chat.isChatFilePath('https://example.com/test.png'), false);
  assert.equal(chat.isChatFilePath('mailto:user@domain.com'), false);
  assert.equal(chat.isChatFilePath('Just a regular sentence with text.'), false);
});

test('getChatFileContentUrl and openChatFileInApp pass cwd parameter without pre-resolving base path', async () => {
  const url = chat.getChatFileContentUrl('assets/hero.png', { id: 'sess-1', host: 'remote-1', cwd: '/var/repo' });
  assert.ok(url.includes('path=assets%2Fhero.png'));
  assert.ok(url.includes('cwd=%2Fvar%2Frepo'));
  assert.ok(url.includes('session_id=sess-1'));
  assert.ok(url.includes('host=remote-1'));

  let postedBody = null;
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (endpoint, opts) => {
    if (endpoint === '/v1/files/open') {
      postedBody = JSON.parse(opts.body);
      return { ok: true, json: async () => ({ status: 'opened' }) };
    }
    return origFetch(endpoint, opts);
  };
  try {
    await chat.openChatFileInApp('assets/chart.png', { id: 'sess-1', host: 'devbox', cwd: '/home/repo' }, 'preview');
    assert.ok(postedBody);
    assert.equal(postedBody.path, 'assets/chart.png');
    assert.equal(postedBody.cwd, '/home/repo');
    assert.equal(postedBody.app, 'preview');
  } finally {
    globalThis.fetch = origFetch;
  }
});

test('linkifyChatFiles renders .chat-file-preview-card for image links', () => {
  const container = new MockElement('div');
  const p = new MockElement('p');
  const a = new MockElement('a');
  a.setAttribute('href', 'file:///workspace/assets/My%20Hero.png');
  a.textContent = 'hero.png';
  p.appendChild(a);
  container.appendChild(p);

  chat.linkifyChatFiles(container, { id: 'sess-test', cwd: '/workspace' });

  const pill = container.querySelector('.chat-file-pill');
  assert.ok(pill, 'Expected .chat-file-pill to be rendered');

  const previewCard = container.querySelector('.chat-file-preview-card');
  assert.ok(previewCard, 'Expected .chat-file-preview-card to be rendered next to pill');
});

test('/model interactive selector card switches model via PATCH and updates status pill', async () => {
  const { interceptChatInput } = await import('./chat-commands.js');
  const container = document.createElement('div');
  const inputEl = document.createElement('textarea');
  const pillEl = document.createElement('span');
  const tabObj = {
    id: 'claude-code:local:interactive-model-1',
    session: {
      id: 'claude-code:local:interactive-model-1',
      agent: 'claude-code',
      model: '',
      resolved_model: 'claude-fable-5-1'
    },
    agentModels: [
      { id: 'claude-fable-5-1', label: 'Claude Fable 5.1' },
      { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' }
    ],
    agentModelsAgent: 'claude-code',
    chatMessagesEl: container,
    chatInputEl: inputEl,
    chatPillModel: pillEl,
    ephemeralCards: []
  };

  let patchPayload = null;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    if (String(url).includes('/config') && opts.method === 'PATCH') {
      patchPayload = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({
          id: tabObj.session.id,
          agent: 'claude-code',
          model: patchPayload.model,
          resolved_model: patchPayload.model || 'claude-fable-5-1'
        })
      };
    }
    return { ok: true, json: async () => [] };
  };

  try {
    const handled = await interceptChatInput(tabObj, '/model');
    assert.equal(handled, true);
    const selectEl = container.querySelector('.chat-inline-model-select');
    assert.ok(selectEl, 'expected .chat-inline-model-select in .chat-config-notice');
    assert.ok(selectEl.children.some(o => o.value === 'claude-sonnet-4-6'));

    selectEl.value = 'claude-sonnet-4-6';
    selectEl.dispatchEvent({ type: 'change', target: selectEl });
    await new Promise(r => setTimeout(r, 15));

    assert.deepEqual(patchPayload, { model: 'claude-sonnet-4-6' });
    assert.equal(tabObj.session.model, 'claude-sonnet-4-6');
    assert.equal(pillEl.textContent, 'Claude Sonnet 4.6');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('/model interactive selector preserves empty Default selection when async fetchSessionAgentModels resolves', async () => {
  const { interceptChatInput } = await import('./chat-commands.js');
  const container = document.createElement('div');
  const inputEl = document.createElement('textarea');
  const pillEl = document.createElement('span');
  const tabObj = {
    id: 'claude-code:local:default-race-1',
    session: {
      id: 'claude-code:local:default-race-1',
      agent: 'claude-code',
      model: 'claude-sonnet-4-6',
      resolved_model: 'claude-sonnet-4-6'
    },
    lastDefaultModel: 'claude-fable-5-1',
    chatMessagesEl: container,
    chatInputEl: inputEl,
    chatPillModel: pillEl,
    ephemeralCards: []
  };

  let resolveModelsFetch;
  const modelsFetchPromise = new Promise(resolve => {
    resolveModelsFetch = resolve;
  });

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    const u = String(url);
    if (u.includes('suggestions?kind=models')) {
      return await modelsFetchPromise;
    }
    if (u.includes('/config') && opts.method === 'PATCH') {
      const payload = JSON.parse(opts.body);
      return {
        ok: true,
        json: async () => ({
          id: tabObj.session.id,
          agent: 'claude-code',
          model: payload.model,
          resolved_model: payload.model || 'claude-fable-5-1'
        })
      };
    }
    return { ok: true, json: async () => [] };
  };

  try {
    const handled = await interceptChatInput(tabObj, '/model');
    assert.equal(handled, true);

    const selectEl = container.querySelector('.chat-inline-model-select');
    assert.ok(selectEl);
    assert.equal(selectEl.value, 'claude-sonnet-4-6');

    selectEl.value = '';
    selectEl.dispatchEvent({ type: 'change', target: selectEl });
    await new Promise(r => setTimeout(r, 15));
    assert.equal(selectEl.value, '');

    resolveModelsFetch({
      ok: true,
      json: async () => [
        { id: 'claude-fable-5-1', label: 'Claude Fable 5.1' },
        { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' }
      ]
    });
    await new Promise(r => setTimeout(r, 15));

    assert.equal(selectEl.value, '', 'selecting Default ("") must not revert to initial model when models fetch resolves');
    assert.equal(selectEl.children[0].value, '');
    assert.equal(selectEl.children[0].selected, true);
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('/model <query> autocomplete fetches kind=models, prepends default, and selects via /model <id>', async () => {
  const inputEl = document.createElement('textarea');
  const menuEl = document.createElement('div');
  menuEl.style.display = 'none';
  const container = document.createElement('div');
  const pillEl = document.createElement('span');
  const tabObj = {
    id: 'claude-code:local:ac-model-1',
    session: {
      id: 'claude-code:local:ac-model-1',
      agent: 'claude-code',
      model: '',
      resolved_model: 'claude-fable-5-1'
    },
    chatInputEl: inputEl,
    chatAutocompleteMenu: menuEl,
    chatMessagesEl: container,
    chatPillModel: pillEl,
    ephemeralCards: []
  };

  chat.setupChatAutocomplete(tabObj);

  const fetchedUrls = [];
  let patchedModel = null;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts = {}) => {
    const u = String(url);
    fetchedUrls.push(u);
    if (u.includes('kind=models') && u.includes('q=son')) {
      return {
        ok: true,
        json: async () => [{ id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' }]
      };
    }
    if (u.includes('kind=models') && u.includes('q=def')) {
      return { ok: true, json: async () => [] };
    }
    if (u.includes('kind=models')) {
      return {
        ok: true,
        json: async () => [{ id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' }]
      };
    }
    if (u.includes('/config') && opts.method === 'PATCH') {
      patchedModel = JSON.parse(opts.body).model;
      return {
        ok: true,
        json: async () => ({
          id: tabObj.session.id,
          agent: 'claude-code',
          model: patchedModel,
          resolved_model: patchedModel
        })
      };
    }
    return { ok: true, json: async () => [] };
  };

  try {
    inputEl.value = '/model son';
    inputEl.selectionStart = 10;
    inputEl.dispatchEvent({ type: 'input' });
    await new Promise(r => setTimeout(r, 150));

    assert.ok(fetchedUrls.some(u => u.includes('kind=models&q=son')));
    assert.equal(menuEl.style.display, 'block');
    assert.equal(tabObj.autocompleteItems[0].id, 'claude-sonnet-4-6');

    chat.selectAutocompleteItem(
      tabObj,
      { name: 'model', trigger: '/model', execution_mode: 'slash_arg' },
      'commands',
      { matchStart: 0, matchEnd: inputEl.value.length }
    );
    assert.equal(inputEl.value, '/model ');
    await new Promise(r => setTimeout(r, 150));
    assert.ok(tabObj.autocompleteItems.some(m => m.id === 'default'));

    inputEl.value = '/model def';
    inputEl.selectionStart = 10;
    inputEl.dispatchEvent({ type: 'input' });
    await new Promise(r => setTimeout(r, 150));
    assert.equal(tabObj.autocompleteItems[0].id, 'default');

    chat.selectAutocompleteItem(
      tabObj,
      { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' },
      'models',
      { matchStart: 0, matchEnd: inputEl.value.length }
    );
    await new Promise(r => setTimeout(r, 20));
    assert.equal(patchedModel, 'claude-sonnet-4-6');
    assert.equal(inputEl.value, '');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('fetchSessionAgentModels preserves focused .config-select-model element in-place', async () => {
  const popover = document.createElement('div');
  popover.style.display = 'flex';
  const pillEl = document.createElement('span');
  const tabObj = {
    id: 'claude-code:local:focus-preserve-1',
    session: {
      id: 'claude-code:local:focus-preserve-1',
      agent: 'claude-code',
      model: 'claude-sonnet-4-6',
      resolved_model: 'claude-sonnet-4-6'
    },
    agentModels: [{ id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' }],
    agentModelsAgent: 'claude-code',
    chatConfigPopover: popover,
    chatPillModel: pillEl
  };

  renderConfigPopoverContent(tabObj);
  const originalSelect = popover.querySelector('.config-select-model');
  assert.ok(originalSelect);
  originalSelect.value = 'claude-sonnet-4-6';

  tabObj.agentModelsAgent = '';
  const prevActive = globalThis.document.activeElement;
  globalThis.document.activeElement = originalSelect;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: true,
    json: async () => [
      { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' },
      { id: 'claude-opus-4-6', label: 'Claude Opus 4.6' }
    ]
  });

  try {
    await chat.fetchSessionAgentModels(tabObj);
    const afterSelect = popover.querySelector('.config-select-model');
    assert.equal(afterSelect, originalSelect, 'focused .config-select-model must not be destroyed and recreated');
    assert.equal(afterSelect.value, 'claude-sonnet-4-6');
    assert.ok(afterSelect.children.some(o => o.value === 'claude-opus-4-6'));
  } finally {
    globalThis.fetch = prevFetch;
    globalThis.document.activeElement = prevActive;
  }
});

async function withMockClipboard(fn) {
  const origNavDesc = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  let copiedText = '';
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {
      clipboard: {
        writeText: async (txt) => { copiedText = txt; }
      }
    }
  });
  try {
    await fn(() => copiedText);
  } finally {
    if (origNavDesc) {
      Object.defineProperty(globalThis, 'navigator', origNavDesc);
    }
  }
}

test('headerless messages omit .chat-msg-header/.chat-msg-role, render bottom .chat-msg-actions with timestamp tooltip, and style.css uses flat assistant responses', async () => {
  const tsUser = '2026-11-19T10:15:00.000Z';
  const tsAsst = '2026-11-19T10:16:00.000Z';
  const tabObj = {
    id: 'tab-headerless-test',
    viewMode: 'chat',
    chatMessagesEl: new MockElement('div'),
    session: { id: 'claude-code:local:sess-headerless', agent: 'claude-code', state: 0 }
  };

  const userEl = createChatMessageElement(tabObj, {
    role: 'user',
    content: 'Inspect layout',
    timestamp: tsUser
  });
  assert.equal(userEl.querySelector('.chat-msg-header'), null);
  assert.equal(userEl.querySelector('.chat-msg-role'), null);
  const userActions = userEl.querySelector('.chat-msg-actions');
  assert.ok(userActions);
  assert.equal(userEl.lastElementChild, userActions);
  const userTime = userActions.querySelector('.chat-msg-time');
  assert.ok(userTime);
  assert.equal(userTime.getAttribute('title'), new Date(tsUser).toLocaleString());
  assert.ok(userActions.querySelector('.btn-copy-chat-msg'));

  const asstEl = createChatMessageElement(tabObj, {
    role: 'assistant',
    content: 'Flat response body',
    timestamp: tsAsst
  });
  assert.equal(asstEl.querySelector('.chat-msg-header'), null);
  assert.equal(asstEl.querySelector('.chat-msg-role'), null);
  const asstActions = asstEl.querySelector('.chat-msg-actions');
  assert.ok(asstActions);
  assert.equal(asstEl.lastElementChild, asstActions);
  const asstTime = asstActions.querySelector('.chat-msg-time');
  assert.ok(asstTime);
  assert.equal(asstTime.getAttribute('title'), new Date(tsAsst).toLocaleString());
  for (const cls of ['.btn-read-chat-msg', '.btn-stop-chat-msg', '.btn-replay-chat-msg', '.btn-full-read-chat-msg', '.btn-copy-chat-msg']) {
    assert.ok(asstActions.querySelector(cls), `expected ${cls} in assistant .chat-msg-actions`);
  }

  // Streaming in-flight assistant message also omits header and stamps dataset.timestamp + tooltip
  handleChatStreamEvent(tabObj, {
    type: 'text_delta',
    turn_id: 'turn-stream-headerless',
    text: 'Streaming text'
  });
  const streamEl = tabObj.chatMessagesEl.querySelector('.chat-msg.assistant-msg.in-flight');
  assert.ok(streamEl);
  assert.equal(streamEl.querySelector('.chat-msg-header'), null);
  assert.equal(streamEl.querySelector('.chat-msg-role'), null);
  assert.ok(streamEl.dataset.timestamp);
  const streamTime = streamEl.querySelector('.chat-msg-actions').querySelector('.chat-msg-time');
  assert.ok(streamTime && streamTime.getAttribute('title'));

  await withMockClipboard(async (getCopied) => {
    const copyBtn = asstActions.querySelector('.btn-copy-chat-msg');
    copyBtn.click();
    await new Promise(r => setTimeout(r, 10));
    assert.equal(getCopied(), 'Flat response body');
    assert.ok(copyBtn.classList.contains('copied'));
    assert.ok(asstActions.classList.contains('has-copied'));
  });

  // Prompt-dispatch error message also omits .chat-msg-header/.chat-msg-role
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: false, status: 500, json: async () => ({ error: 'boom' }) });
  try {
    const errTab = {
      id: 'tab-err-headerless',
      viewMode: 'chat',
      chatMessagesEl: new MockElement('div'),
      chatInputEl: new MockElement('textarea'),
      session: { id: 'claude-code:local:err-headerless', agent: 'claude-code', engine_type: 'tmux', state: 0 }
    };
    errTab.chatInputEl.value = 'trigger failure';
    await sendChatPrompt(errTab);
    const errAssistant = errTab.chatMessagesEl.querySelector('.chat-msg.assistant-msg');
    assert.ok(errAssistant);
    assert.equal(errAssistant.querySelector('.chat-msg-header'), null);
    assert.equal(errAssistant.querySelector('.chat-msg-role'), null);
    assert.ok(errAssistant.textContent.includes('boom'));
  } finally {
    globalThis.fetch = prevFetch;
  }

  // Verify style.css flat assistant, user bubble, centered column, and transcript hover rules
  const fs = await import('node:fs/promises');
  const css = await fs.readFile(new URL('../style.css', import.meta.url), 'utf8');
  assert.match(css, /\.chat-msg\.assistant-msg\s*\{[^}]*background:\s*transparent;[^}]*border:\s*none;[^}]*width:\s*100%;[^}]*max-width:\s*100%;/s);
  assert.match(css, /\.chat-msg\.user-msg\s*\{[^}]*max-width:\s*min\(80%,\s*40rem\);/s);
  assert.match(css, /\.chat-messages-container\s*\{[^}]*padding-inline:\s*max\(20px,\s*calc\(\(100%\s*-\s*54rem\)\s*\/\s*2\)\);/s);
  assert.match(css, /\.chat-composer-container\s*\{[^}]*padding-inline:\s*max\(20px,\s*calc\(\(100%\s*-\s*54rem\)\s*\/\s*2\)\);/s);
  assert.ok(css.includes('.transcript-msg:hover .btn-copy-chat-msg'));
  assert.ok(!css.includes('max-width: 850px'));
});

test('insertTextIntoPromptInput and replacePromptInputRange preserve native execCommand undo and provide deterministic fallback Cmd+Z / Cmd+Shift+Z', async () => {
  const { insertSnippetIntoComposer } = await import('./chat-command-cards.js');
  const { insertPathIntoPromptInput } = await import('./terminal.js');

  const prevExec = globalThis.document.execCommand;
  const prevActive = globalThis.document.activeElement;
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({}) });

  try {
    // 1. Native execCommand path: preserves native browser undo stack
    const nativeInput = new MockElement('textarea');
    nativeInput.value = 'hello ';
    nativeInput.selectionStart = 6;
    nativeInput.selectionEnd = 6;
    let focusCalled = 0;
    let selectionSet = null;
    nativeInput.focus = () => { focusCalled++; };
    nativeInput.setSelectionRange = (s, e) => {
      selectionSet = [s, e];
      nativeInput.selectionStart = s;
      nativeInput.selectionEnd = e;
    };
    let execArgs = null;
    globalThis.document.execCommand = (cmd, ui, val) => {
      execArgs = [cmd, ui, val];
      const before = nativeInput.value.slice(0, nativeInput.selectionStart);
      const after = nativeInput.value.slice(nativeInput.selectionEnd);
      nativeInput.value = before + val + after;
      return true;
    };

    chat.insertTextIntoPromptInput(nativeInput, 'world');
    assert.equal(focusCalled, 1);
    assert.deepEqual(selectionSet, [11, 11]);
    assert.deepEqual(execArgs, ['insertText', false, 'world']);
    assert.equal(nativeInput.value, 'hello world');
    assert.equal(nativeInput._ackbarNativeUndoActive, true);
    assert.equal(chat.handlePromptInputUndoKeydown(nativeInput, { metaKey: true, key: 'z' }), false);

    // 2. Fallback path when execCommand returns false: deterministic Cmd+Z / Cmd+Shift+Z
    globalThis.document.execCommand = () => false;
    const chatView = new MockElement('div');
    const tabObj = {
      id: 'tab-undo-fallback',
      viewMode: 'chat',
      session: { id: 'claude-code:local:undo-1', agent: 'claude-code', engine_type: 'tmux', state: 0 }
    };
    chat.setupChatInterface(tabObj, chatView, tabObj.session);
    const inputEl = tabObj.chatInputEl;
    inputEl.setSelectionRange = (s, e) => {
      inputEl.selectionStart = s;
      inputEl.selectionEnd = e;
    };

    // Paste text via insertTextIntoPromptInput -> falls back & enables Cmd+Z
    inputEl.value = 'prefix ';
    inputEl.selectionStart = 7;
    inputEl.selectionEnd = 7;
    chat.insertTextIntoPromptInput(inputEl, 'pasted');
    assert.equal(inputEl.value, 'prefix pasted');
    assert.equal(inputEl._ackbarNativeUndoActive, false);

    // Type additional character via keydown while fallback active
    let prevented = false;
    inputEl.dispatchEvent({ type: 'keydown', key: '!', preventDefault() { prevented = true; } });
    inputEl.value = 'prefix pasted!';
    inputEl.selectionStart = 14;
    inputEl.selectionEnd = 14;

    // Undo typing '!'
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', metaKey: true, preventDefault() { prevented = true; }, stopPropagation() {} });
    assert.equal(prevented, true);
    assert.equal(inputEl.value, 'prefix pasted');

    // Undo paste 'pasted'
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', metaKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, 'prefix ');

    // Redo paste 'pasted' via Cmd+Shift+Z and Ctrl+Y
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', metaKey: true, shiftKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, 'prefix pasted');
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', ctrlKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, 'prefix ');
    inputEl.dispatchEvent({ type: 'keydown', key: 'y', ctrlKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, 'prefix pasted');

    // Autocomplete /command & @file, bash snippet, and file path insertion are undoable
    inputEl.value = '/mod';
    chat.selectAutocompleteItem(tabObj, { name: 'model', trigger: '/model', execution_mode: 'slash_arg' }, 'commands', { matchStart: 0, matchEnd: 4 });
    assert.equal(inputEl.value, '/model ');
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', metaKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, '/mod');

    inputEl.value = '@';
    chat.selectAutocompleteItem(tabObj, { path: 'web/js/chat.js' }, 'files', { matchStart: 0, matchEnd: 1 });
    assert.equal(inputEl.value, '@web/js/chat.js ');
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', metaKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, '@');

    inputEl.value = '';
    insertSnippetIntoComposer(tabObj, '```sh\nls\n```');
    assert.ok(inputEl.value.includes('```sh\nls\n```'));
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', metaKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, '');

    globalThis.document.activeElement = inputEl;
    inputEl.selectionStart = 0;
    inputEl.selectionEnd = 0;
    insertPathIntoPromptInput(inputEl, 'docs/architecture.md');
    assert.equal(inputEl.value, '"docs/architecture.md" ');
    inputEl.dispatchEvent({ type: 'keydown', key: 'z', ctrlKey: true, preventDefault() {}, stopPropagation() {} });
    assert.equal(inputEl.value, '');

    // Sending prompt resets undo history
    inputEl.value = 'final prompt';
    await sendChatPrompt(tabObj);
    assert.equal(inputEl.value, '');
    assert.equal(inputEl._ackbarUndoStack.length, 0);
  } finally {
    globalThis.document.execCommand = prevExec;
    globalThis.document.activeElement = prevActive;
    globalThis.fetch = prevFetch;
  }
});

test('smart auto-scroll lock pauses streaming auto-scroll when scrolled up and reveals floating scroll-bottom button', async () => {
  const { showInStreamActivity } = await import('./chat-activity.js');

  const container = new MockElement('div');
  container.scrollHeight = 1000;
  container.clientHeight = 300;
  container.scrollTop = 650;
  assert.equal(chat.isChatNearBottom(container, 80), true);
  container.scrollTop = 500;
  assert.equal(chat.isChatNearBottom(container, 80), false);

  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({}) });
  try {
    const chatView = new MockElement('div');
    const tabObj = {
      id: 'tab-scroll-lock',
      viewMode: 'chat',
      session: { id: 'claude-code:local:scroll-1', agent: 'claude-code', engine_type: 'tmux', state: 0 }
    };
    chat.setupChatInterface(tabObj, chatView, tabObj.session);

    const msgContainer = tabObj.chatMessagesEl;
    const scrollBtn = tabObj.chatScrollBottomBtn;
    assert.ok(scrollBtn, 'expected .btn-chat-scroll-bottom in composer container');
    assert.equal(scrollBtn.style.display, 'none');

    msgContainer.scrollHeight = 1200;
    msgContainer.clientHeight = 300;
    msgContainer.scrollTop = 900;

    // Streaming while at bottom keeps pinned
    handleChatStreamEvent(tabObj, { type: 'text_delta', turn_id: 'turn-s1', text: 'chunk 1' });
    assert.equal(msgContainer.scrollTop, 1200);
    assert.equal(tabObj._chatUserScrolledUp, false);
    assert.equal(scrollBtn.style.display, 'none');

    // Programmatic scroll flag suppresses false scroll-up detection
    tabObj._programmaticScroll = true;
    tabObj._programmaticScrollTarget = null;
    msgContainer.scrollTop = 200;
    msgContainer.dispatchEvent({ type: 'scroll' });
    assert.equal(tabObj._programmaticScroll, false);
    assert.equal(tabObj._chatUserScrolledUp, false);

    // User scrolls up manually -> pauses auto-scroll & shows button
    msgContainer.scrollTop = 200;
    msgContainer.dispatchEvent({ type: 'scroll' });
    assert.equal(tabObj._chatUserScrolledUp, true);
    assert.equal(scrollBtn.style.display, 'inline-flex');

    // Subsequent streaming, thinking, tool events, and activity updates must NOT snap scrollTop
    msgContainer.scrollHeight = 1600;
    handleChatStreamEvent(tabObj, { type: 'text_delta', turn_id: 'turn-s1', text: ' chunk 2' });
    handleChatStreamEvent(tabObj, { type: 'thought_delta', turn_id: 'turn-s1', thinking: 'hmm' });
    handleChatStreamEvent(tabObj, { type: 'tool_start', turn_id: 'turn-s1', tool_name: 'read_file', tool_input: { path: 'a.js' } });
    handleChatStreamEvent(tabObj, { type: 'tool_result', turn_id: 'turn-s1', tool_name: 'read_file', tool_output: 'ok' });
    showInStreamActivity(tabObj, { icon: '⚡', text: 'Working...' });
    assert.equal(msgContainer.scrollTop, 200);
    assert.equal(scrollBtn.style.display, 'inline-flex');

    // Clicking floating scroll-bottom button jumps to bottom and hides button
    scrollBtn.click();
    assert.equal(tabObj._chatUserScrolledUp, false);
    assert.equal(msgContainer.scrollTop, 1600);
    assert.equal(scrollBtn.style.display, 'none');

    // Initial transcript load resets _chatUserScrolledUp and scrolls to bottom
    if (tabObj._inFlightTranscriptPromise) {
      await tabObj._inFlightTranscriptPromise;
    }
    tabObj._lastTranscriptLoadAt = 0;
    msgContainer.children = [];
    tabObj._chatUserScrolledUp = true;
    msgContainer.scrollTop = 0;
    globalThis.fetch = async (url) => {
      if (String(url).includes('/transcript')) {
        return {
          ok: true,
          headers: { get: () => 'application/json' },
          json: async () => ({ messages: [{ role: 'assistant', content: 'Loaded' }] })
        };
      }
      return { ok: true, json: async () => ({}) };
    };
    await loadChatTranscript(tabObj);
    assert.equal(tabObj._chatUserScrolledUp, false);
    assert.equal(scrollBtn.style.display, 'none');
  } finally {
    globalThis.fetch = prevFetch;
  }
});

test('code blocks render .chat-code-header with language label (skipping mermaid) and renderMarkdown wraps tables in .markdown-table-wrap', async () => {
  const container = new MockElement('div');
  const preTs = new MockElement('pre');
  const codeTs = new MockElement('code');
  codeTs.className = 'language-typescript';
  codeTs.textContent = 'const x: number = 42;';
  preTs.appendChild(codeTs);
  container.appendChild(preTs);

  const prePlain = new MockElement('pre');
  const codePlain = new MockElement('code');
  codePlain.textContent = 'echo hi';
  prePlain.appendChild(codePlain);
  container.appendChild(prePlain);

  const preMermaid = new MockElement('pre');
  const codeMermaid = new MockElement('code');
  codeMermaid.className = 'language-mermaid';
  codeMermaid.textContent = 'flowchart TD\nA --> B';
  preMermaid.appendChild(codeMermaid);
  container.appendChild(preMermaid);

  const preUnclassedMermaid = new MockElement('pre');
  const codeUnclassedMermaid = new MockElement('code');
  codeUnclassedMermaid.textContent = 'flowchart LR\nX --> Y';
  preUnclassedMermaid.appendChild(codeUnclassedMermaid);
  container.appendChild(preUnclassedMermaid);

  chat.attachCodeBlockCopyButtons(container, { skipMermaid: true });

  assert.ok(preTs.classList.contains('has-code-header'));
  const tsHeader = preTs.firstElementChild;
  assert.ok(tsHeader && tsHeader.classList.contains('chat-code-header'));
  assert.equal(tsHeader.querySelector('.chat-code-lang').textContent, 'typescript');
  assert.ok(tsHeader.querySelector('.btn-copy-code'));

  assert.ok(prePlain.classList.contains('has-code-header'));
  assert.equal(prePlain.querySelector('.chat-code-lang').textContent, 'code');

  assert.equal(preMermaid.classList.contains('has-code-header'), false);
  assert.equal(preMermaid.querySelector('.chat-code-header'), null);
  assert.equal(preUnclassedMermaid.classList.contains('has-code-header'), false);

  await withMockClipboard(async (getCopied) => {
    tsHeader.querySelector('.btn-copy-code').click();
    await new Promise(r => setTimeout(r, 10));
    assert.equal(getCopied(), 'const x: number = 42;');
  });

  // Streaming text_delta synchronously adds .chat-code-header via flushActiveTurnStreamRender
  const origParse = globalThis.window.marked.parse;
  const streamContainer = new MockElement('div');
  const streamTab = {
    id: 'tab-stream-code-header',
    viewMode: 'chat',
    chatMessagesEl: streamContainer,
    session: { id: 'claude-code:local:stream-code', agent: 'claude-code', state: 0 }
  };
  const origRaf = globalThis.window.requestAnimationFrame;
  globalThis.window.requestAnimationFrame = null;
  globalThis.window.marked.parse = () => '<pre><code class="language-go">package main</code></pre>';
  try {
    handleChatStreamEvent(streamTab, {
      type: 'text_delta',
      turn_id: 'turn-code-stream',
      text: '```go\npackage main\n```'
    });
    const streamCodeHeader = streamContainer.querySelector('.chat-code-header');
    assert.ok(streamCodeHeader);
    assert.equal(streamCodeHeader.querySelector('.chat-code-lang').textContent, 'go');
  } finally {
    globalThis.window.requestAnimationFrame = origRaf;
    globalThis.window.marked.parse = origParse;
  }

  // renderMarkdown wraps <table> without double-wrapping
  globalThis.window.marked.parse = () => '<table><thead><tr><th>Col</th></tr></thead><tbody><tr><td>Val</td></tr></tbody></table>';
  try {
    const wrappedOnce = renderMarkdown('| Col |\n| --- |\n| Val |');
    assert.equal(wrappedOnce, '<div class="markdown-table-wrap"><table><thead><tr><th>Col</th></tr></thead><tbody><tr><td>Val</td></tr></tbody></table></div>');

    globalThis.window.marked.parse = () => '<div class="markdown-table-wrap"><table><tr><td>Already</td></tr></table></div>';
    const wrappedAlready = renderMarkdown('| Already |');
    assert.equal((wrappedAlready.match(/markdown-table-wrap/g) || []).length, 1);
  } finally {
    globalThis.window.marked.parse = origParse;
  }
});

test('two-row .chat-composer-box places textarea above .chat-composer-toolbar and excludes .chat-pill from click-to-focus', async () => {
  const prevFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({}) });
  try {
    const chatView = new MockElement('div');
    const tabObj = {
      id: 'tab-composer-layout',
      viewMode: 'chat',
      session: { id: 'claude-code:local:comp-1', agent: 'claude-code', engine_type: 'tmux', state: 0 }
    };
    chat.setupChatInterface(tabObj, chatView, tabObj.session);

    const composerBox = tabObj.chatComposerBox;
    assert.ok(composerBox);
    assert.equal(composerBox.children.length, 2);
    assert.ok(composerBox.children[0].classList.contains('chat-composer-textarea'));
    assert.ok(composerBox.children[1].classList.contains('chat-composer-toolbar'));

    const toolbar = composerBox.children[1];
    const left = toolbar.querySelector('.chat-composer-toolbar-left');
    const actions = toolbar.querySelector('.chat-composer-actions');
    assert.ok(left && left.querySelector('.btn-composer-attach') && left.querySelector('.chat-composer-bar'));
    assert.ok(actions && actions.querySelector('.btn-composer-send') && actions.querySelector('.btn-composer-schedule'));

    let focusCount = 0;
    tabObj.chatInputEl.focus = () => { focusCount++; };

    // Clicking a .chat-pill inside toolbar must NOT focus textarea
    const pillModel = tabObj.chatPillModel;
    for (const fn of (composerBox.listeners.get('click') || [])) {
      fn({ target: pillModel });
    }
    assert.equal(focusCount, 0);

    // Clicking empty space in composerBox focuses textarea
    for (const fn of (composerBox.listeners.get('click') || [])) {
      fn({ target: composerBox });
    }
    assert.equal(focusCount, 1);
  } finally {
    globalThis.fetch = prevFetch;
  }
});







