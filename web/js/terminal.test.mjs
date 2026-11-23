import test from 'node:test';
import assert from 'node:assert/strict';

function createMockElement(tagName = 'div') {
  const classes = new Set();
  const node = {
    tagName: tagName.toUpperCase(),
    className: '',
    title: '',
    innerHTML: '',
    style: {},
    dataset: {},
    children: [],
    parentNode: null,
    isConnected: false,
    classList: {
      add(...cls) {
        for (const c of cls) if (c) classes.add(c);
        node.className = Array.from(classes).join(' ');
      },
      remove(...cls) {
        for (const c of cls) classes.delete(c);
        node.className = Array.from(classes).join(' ');
      },
      contains(c) {
        return classes.has(c);
      }
    },
    appendChild(child) {
      child.parentNode = node;
      child.isConnected = true;
      node.children.push(child);
      return child;
    },
    contains(target) {
      let cur = target;
      while (cur) {
        if (cur === node) return true;
        cur = cur.parentNode;
      }
      return false;
    },
    addEventListener() {}
  };
  return node;
}

globalThis.window = globalThis;
globalThis.window.location = new URL('http://localhost:7777/');
globalThis.window.addEventListener = () => {};
globalThis.window.removeEventListener = () => {};
globalThis.window.open = () => null;
globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
globalThis.document = {
  getElementById: () => null,
  querySelector: () => null,
  querySelectorAll: () => [],
  addEventListener: () => {},
  createElement: (tag) => createMockElement(tag),
  body: createMockElement('body')
};
globalThis.ResizeObserver = class {
  observe() {}
  disconnect() {}
};
globalThis.WebSocket = class {
  static OPEN = 1;
  static CLOSED = 3;
  static CLOSING = 2;
  constructor() {
    this.readyState = 1;
  }
  send() {}
  close() {}
};

const { createTerminalLinkHandler, parseSafeHttpUrl } = await import('./terminal.js');
const { mountTerminalInTab } = await import('./tabs.js');

test('parseSafeHttpUrl allows http/https and rejects unsafe or invalid URLs', () => {
  assert.equal(parseSafeHttpUrl('https://example.com/docs?q=1')?.href, 'https://example.com/docs?q=1');
  assert.equal(parseSafeHttpUrl('http://localhost:7777/path')?.href, 'http://localhost:7777/path');

  for (const bad of [
    'javascript:alert(1)',
    'JAVASCRIPT:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'file:///etc/passwd',
    'vbscript:msgbox(1)',
    'not a url',
    '',
    null,
    undefined,
    { toString: () => 'https://example.com' }
  ]) {
    assert.equal(parseSafeHttpUrl(bad), null, `expected ${String(bad)} to be rejected`);
  }
});

test('createTerminalLinkHandler activate opens valid http/https URLs with noopener,noreferrer and opener=null', () => {
  const prevOpen = globalThis.window.open;
  const openCalls = [];
  const openedWindow = { opener: 'should-be-cleared' };
  globalThis.window.open = (url, target, features) => {
    openCalls.push({ url, target, features });
    return openedWindow;
  };

  try {
    const termViewEl = createMockElement('div');
    const handler = createTerminalLinkHandler(termViewEl);

    handler.activate({}, 'https://github.com/marcinbak/ackbar');
    assert.deepEqual(openCalls, [
      {
        url: 'https://github.com/marcinbak/ackbar',
        target: '_blank',
        features: 'noopener,noreferrer'
      }
    ]);
    assert.equal(openedWindow.opener, null);

    openCalls.length = 0;
    handler.activate({}, 'http://127.0.0.1:7777');
    assert.deepEqual(openCalls, [
      {
        url: 'http://127.0.0.1:7777/',
        target: '_blank',
        features: 'noopener,noreferrer'
      }
    ]);
  } finally {
    globalThis.window.open = prevOpen;
  }
});

test('createTerminalLinkHandler activate handles null window and read-only opener defensively', () => {
  const prevOpen = globalThis.window.open;
  try {
    globalThis.window.open = () => null;
    const handler = createTerminalLinkHandler(createMockElement('div'));
    assert.doesNotThrow(() => handler.activate({}, 'https://example.com'));

    globalThis.window.open = () => Object.freeze({ opener: 'locked' });
    assert.doesNotThrow(() => handler.activate({}, 'https://example.com'));
  } finally {
    globalThis.window.open = prevOpen;
  }
});

test('createTerminalLinkHandler activate rejects javascript and invalid URLs', () => {
  const prevOpen = globalThis.window.open;
  const openCalls = [];
  globalThis.window.open = (...args) => {
    openCalls.push(args);
    return {};
  };

  try {
    const handler = createTerminalLinkHandler(createMockElement('div'));
    for (const bad of ['javascript:alert(1)', 'data:text/plain,hi', 'file:///tmp/x', 'relative/path', '']) {
      handler.activate({}, bad);
    }
    assert.equal(openCalls.length, 0);
  } finally {
    globalThis.window.open = prevOpen;
  }
});

test('createTerminalLinkHandler hover and leave update and clear tooltip safely', () => {
  const termViewEl = createMockElement('div');
  const handler = createTerminalLinkHandler(termViewEl);

  handler.hover({}, 'https://example.com/page');
  assert.equal(termViewEl.title, 'https://example.com/page');

  handler.hover({}, 'https://example.com');
  assert.equal(termViewEl.title, 'https://example.com/');

  handler.hover({}, 'https://trusted.com\t.attacker.example');
  assert.equal(termViewEl.title, 'https://trusted.com.attacker.example/');

  handler.hover({}, 'javascript:alert(1)');
  assert.equal(termViewEl.title, '');

  handler.hover({}, 'http://localhost:7777/logs');
  assert.equal(termViewEl.title, 'http://localhost:7777/logs');

  handler.leave();
  assert.equal(termViewEl.title, '');

  handler.hover({}, 'not-a-valid-url');
  assert.equal(termViewEl.title, '');
});

test('mountTerminalInTab passes linkHandler to Terminal and keeps WebLinksAddon loaded', () => {
  const prevTerminal = globalThis.Terminal;
  const prevFitAddon = globalThis.FitAddon;
  const prevWebLinksAddon = globalThis.WebLinksAddon;
  const prevOpen = globalThis.window.open;

  const constructedTerms = [];
  const loadedAddons = [];
  const openCalls = [];

  class MockFitAddon {}
  class MockWebLinksAddon {}
  class MockTerminal {
    constructor(opts) {
      this.opts = opts;
      this.cols = 80;
      this.rows = 24;
      this.parser = { registerOscHandler() {} };
      constructedTerms.push(this);
    }
    loadAddon(addon) {
      loadedAddons.push(addon);
    }
    open() {}
    onSelectionChange() {}
    attachCustomKeyEventHandler() {}
    onData() {}
    onResize() {}
  }

  globalThis.Terminal = MockTerminal;
  globalThis.FitAddon = { FitAddon: MockFitAddon };
  globalThis.WebLinksAddon = { WebLinksAddon: MockWebLinksAddon };
  globalThis.window.open = (url, target, features) => {
    openCalls.push({ url, target, features });
    return { opener: 'x' };
  };

  try {
    const tab = {
      id: 'tab-1',
      containerEl: createMockElement('div'),
      sessionContainer: createMockElement('div'),
      session: { id: 'sess-1', host: 'local' }
    };

    mountTerminalInTab(tab);

    assert.equal(constructedTerms.length, 1);
    const linkHandler = constructedTerms[0].opts.linkHandler;
    assert.equal(typeof linkHandler?.activate, 'function');
    assert.equal(typeof linkHandler?.hover, 'function');
    assert.equal(typeof linkHandler?.leave, 'function');

    assert.equal(loadedAddons.some((a) => a instanceof MockFitAddon), true);
    assert.equal(loadedAddons.some((a) => a instanceof MockWebLinksAddon), true);

    linkHandler.hover({}, 'https://example.org/osc8');
    assert.equal(tab.termViewEl.title, 'https://example.org/osc8');
    linkHandler.activate({}, 'https://example.org/osc8');
    assert.equal(openCalls.length, 1);
    assert.equal(openCalls[0].url, 'https://example.org/osc8');
    assert.equal(openCalls[0].features, 'noopener,noreferrer');
    linkHandler.leave();
    assert.equal(tab.termViewEl.title, '');
  } finally {
    globalThis.Terminal = prevTerminal;
    globalThis.FitAddon = prevFitAddon;
    globalThis.WebLinksAddon = prevWebLinksAddon;
    globalThis.window.open = prevOpen;
  }
});
