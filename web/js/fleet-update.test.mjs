import test from 'node:test';
import assert from 'node:assert/strict';
import {
  formatTimerClock,
  formatBadgeElapsed,
  formatStepDuration,
  formatUpdateElapsed,
  computeCurrentStepNumber,
  formatUpdateButtonLabel,
  formatNavbarUpdateBadge,
  parseSSEUpdateChunk,
  createFleetRunState,
  applyUpdateProgressEvent,
  configureFleetUpdateUI,
  openFleetUpdateModal,
  runSingleHostUpdate,
  notifyUpdateModalClosed,
  isFleetUpdateBadgeActive,
  getFleetRunState,
  resetFleetRunState
} from './fleet-update.js';

test('time formatters and step number clamping', () => {
  assert.equal(formatTimerClock(0), '0:00.0');
  assert.equal(formatTimerClock(7450), '0:07.4');
  assert.equal(formatTimerClock(65200), '1:05.2');
  assert.equal(formatBadgeElapsed(7450), '0:07');
  assert.equal(formatBadgeElapsed(65200), '1:05');
  assert.equal(formatStepDuration(3240), '3.2s');
  assert.equal(formatUpdateElapsed(65200), '1m 05s');

  assert.equal(computeCurrentStepNumber(0, 0, false), 0);
  assert.equal(computeCurrentStepNumber(0, 4, false), 1);
  assert.equal(computeCurrentStepNumber(1, 4, false), 2);
  assert.equal(computeCurrentStepNumber(3, 4, false), 4);
  assert.equal(computeCurrentStepNumber(4, 4, false), 4);
  assert.equal(computeCurrentStepNumber(2, 4, true), 4);
});

test('parseSSEUpdateChunk handles arbitrary TCP chunk splits', () => {
  const part1 = 'data: {"type":"step","step_id":"preflight"';
  const part2 = ',"completed_steps":0,"total_steps":0}\n\ndata: {"type":"step","step_id":"local"';
  const part3 = ',"completed_steps":1,"total_steps":4}\n\n';

  const r1 = parseSSEUpdateChunk('', part1);
  assert.equal(r1.events.length, 0);
  assert.equal(r1.rest, part1);

  const r2 = parseSSEUpdateChunk(r1.rest, part2);
  assert.equal(r2.events.length, 1);
  assert.equal(r2.events[0].step_id, 'preflight');

  const r3 = parseSSEUpdateChunk(r2.rest, part3);
  assert.equal(r3.events.length, 1);
  assert.equal(r3.events[0].step_id, 'local');
  assert.equal(r3.rest, '');
});

test('formatUpdateButtonLabel reflects running steps and completion states', () => {
  assert.equal(formatUpdateButtonLabel({ status: 'idle' }, '20261120.02'), '⚡ Upgrade All Outdated Hosts to v20261120.02');
  assert.equal(formatUpdateButtonLabel({ status: 'running', totalSteps: 0, completedSteps: 0 }, '20261120.02'), '⏳ Preparing Update...');
  assert.equal(formatUpdateButtonLabel({ status: 'running', totalSteps: 4, completedSteps: 0 }, '20261120.02'), '⏳ Updating 1/4...');
  assert.equal(formatUpdateButtonLabel({ status: 'running', totalSteps: 4, completedSteps: 1 }, '20261120.02'), '⏳ Updating 2/4...');
  assert.equal(formatUpdateButtonLabel({ status: 'running', totalSteps: 4, completedSteps: 2 }, '20261120.02'), '⏳ Updating 3/4...');
  assert.equal(formatUpdateButtonLabel({ status: 'running', totalSteps: 4, completedSteps: 3 }, '20261120.02'), '⏳ Updating 4/4...');
  assert.equal(formatUpdateButtonLabel({ status: 'error', failedCount: 1 }, '20261120.02'), '⚠️ Retry Failed Hosts');
  assert.equal(formatUpdateButtonLabel({ status: 'done', localDeferred: true }, '20261120.02'), '⏳ Restart Scheduled After Active Turn');
  assert.equal(formatUpdateButtonLabel({ status: 'done', remoteDeferred: true }, '20261120.02'), '⏳ Remote Daemon(s) Draining');
  assert.equal(formatUpdateButtonLabel({ status: 'reloading' }, '20261120.02'), '✅ All Hosts Updated! Reloading...');
  assert.equal(formatUpdateButtonLabel({ status: 'done', reloadPending: true, newVersion: '20261120.02' }, '20261120.02'), '🔄 Reload UI to Apply v20261120.02');
  assert.equal(formatUpdateButtonLabel({ status: 'done', totalSteps: 4 }, '20261120.02'), '✅ Fleet Updated (4/4)');
});

test('applyUpdateProgressEvent orders steps, updates live detail, and tracks hostStatuses', () => {
  let st = createFleetRunState('20261120.02', 1000);
  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'preflight', phase: 'preflight', label: 'Checking release & fleet hosts', status: 'running', completed_steps: 0, total_steps: 0 }, 1100);
  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'preflight', phase: 'preflight', label: 'Checked release & fleet hosts', status: 'done', completed_steps: 0, total_steps: 4, step_elapsed_ms: 220 }, 1320);
  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'compile:darwin/arm64', phase: 'compiling', label: 'Compiling binaries (darwin/arm64)', detail: 'compiling ackbard', status: 'running', completed_steps: 0, total_steps: 4 }, 1500);
  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'compile:darwin/arm64', phase: 'compiling', label: 'Compiled binaries (darwin/arm64)', status: 'done', completed_steps: 1, total_steps: 4, step_elapsed_ms: 2100 }, 3600);
  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'host:macmini', phase: 'updating_host', host: 'macmini', label: 'Updating macmini', detail: 'uploading binaries', status: 'running', completed_steps: 1, total_steps: 4 }, 4000);

  assert.equal(st.steps.length, 3);
  assert.deepEqual(st.steps.map(s => s.step_id), ['preflight', 'compile:darwin/arm64', 'host:macmini']);
  assert.equal(st.steps[1].detail, '');
  assert.equal(st.steps[1].elapsedMs, 2100);
  assert.equal(st.steps[2].detail, 'uploading binaries');
  assert.equal(st.hostStatuses.macmini.status, 'running');
  assert.equal(st.hostStatuses.macmini.detail, 'uploading binaries');

  const badge = formatNavbarUpdateBadge(st, 8000);
  assert.equal(badge.visible, true);
  assert.equal(badge.text, '⏳ Updating 2/4 · macmini (0:07)');
  assert.equal(badge.className, 'is-updating');

  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'host:macmini', phase: 'updating_host', host: 'macmini', label: 'Updating macmini', detail: 'restarting ackbard', status: 'running', completed_steps: 1, total_steps: 4 }, 5500);
  assert.equal(st.hostStatuses.macmini.detail, 'restarting ackbard');

  st = applyUpdateProgressEvent(st, { type: 'step', step_id: 'host:macmini', phase: 'updating_host', host: 'macmini', label: 'Updated macmini to v20261120.02', status: 'done', completed_steps: 2, total_steps: 4, step_elapsed_ms: 3200 }, 7200);
  assert.equal(st.hostStatuses.macmini.status, 'done');
  assert.equal(st.steps[2].elapsedMs, 3200);
});

function createFakeDOM() {
  const elements = new Map();
  const makeEl = (id) => {
    const attrs = new Map();
    const listeners = {};
    let _text = '';
    let _html = '';
    const node = {
      id,
      isConnected: true,
      style: { display: 'none' },
      className: '',
      disabled: false,
      title: '',
      onclick: null,
      classList: {
        add: (...c) => { node.className = Array.from(new Set([...node.className.split(/\s+/).filter(Boolean), ...c])).join(' '); },
        remove: (...c) => { node.className = node.className.split(/\s+/).filter(x => x && !c.includes(x)).join(' '); }
      },
      setAttribute: (k, v) => attrs.set(k, String(v)),
      getAttribute: (k) => attrs.get(k) ?? null,
      hasAttribute: (k) => attrs.has(k),
      addEventListener: (ev, fn) => { (listeners[ev] ||= []).push(fn); },
      click: () => { node.onclick?.(); (listeners.click || []).forEach(fn => fn()); },
      querySelectorAll: () => [],
      set textContent(v) { _text = String(v); },
      get textContent() { return _text; },
      set innerHTML(v) {
        _html = String(v);
        for (const m of _html.matchAll(/id="([^"]+)"/g)) {
          elements.set(m[1], makeEl(m[1]));
        }
        const verMatch = _html.match(/data-latest-ver="([^"]+)"/);
        if (verMatch && elements.has('fleetExecRoot')) {
          elements.get('fleetExecRoot').setAttribute('data-latest-ver', verMatch[1]);
        }
      },
      get innerHTML() { return _html; }
    };
    return node;
  };
  for (const id of ['modalOverlay', 'modalBody', 'modalFooter', 'updateBadge']) {
    elements.set(id, makeEl(id));
  }
  return {
    elements,
    document: { getElementById: (id) => elements.get(id) || null }
  };
}

test('background dismiss keeps update running, sets reloadPending without auto-reloading, and re-attaches on re-open', async () => {
  resetFleetRunState();
  const { elements, document } = createFakeDOM();
  let reloadCount = 0;
  const toasts = [];
  globalThis.document = document;
  globalThis.window = { location: { reload: () => { reloadCount++; } } };

  let releaseStream;
  const streamClosed = new Promise(r => { releaseStream = r; });

  globalThis.fetch = async (url) => {
    if (url.startsWith('/v1/update')) {
      const encoder = new TextEncoder();
      let sentFirst = false;
      let sentSecond = false;
      return {
        ok: true,
        headers: { get: () => 'text/event-stream' },
        body: {
          getReader() {
            return {
              async read() {
                if (!sentFirst) {
                  sentFirst = true;
                  const chunk =
                    'data: {"type":"step","step_id":"preflight","phase":"preflight","label":"Checked","status":"done","completed_steps":0,"total_steps":2}\n\n' +
                    'data: {"type":"step","step_id":"host:macmini","phase":"updating_host","host":"macmini","label":"Updating macmini","status":"running","completed_steps":1,"total_steps":2}\n\n';
                  return { done: false, value: encoder.encode(chunk) };
                }
                await streamClosed;
                if (sentSecond) return { done: true };
                sentSecond = true;
                const finalChunk =
                  'data: {"type":"step","step_id":"host:macmini","phase":"updating_host","host":"macmini","label":"Updated macmini","status":"done","completed_steps":2,"total_steps":2}\n\n' +
                  'data: {"type":"complete","phase":"complete","status":"done","completed_steps":2,"total_steps":2,"result":{"new_version":"20261120.99","restarting_local":true,"host_results":[{"host":"macmini","status":"success","new_version":"20261120.99"}]}}\n\n';
                return { done: false, value: encoder.encode(finalChunk) };
              }
            };
          }
        }
      };
    }
    return { ok: true, json: async () => ({ version: '20261120.99' }) };
  };

  const modalOverlay = elements.get('modalOverlay');
  const modalBody = elements.get('modalBody');
  const modalFooter = elements.get('modalFooter');
  const deps = {
    showModal: (_t, body, footer) => {
      modalBody.innerHTML = body;
      modalFooter.innerHTML = footer;
      modalOverlay.style.display = 'flex';
    },
    hideModal: () => {
      modalOverlay.style.display = 'none';
      notifyUpdateModalClosed();
    },
    showToast: (msg) => toasts.push(msg),
    fetchVersion: async () => {},
    fetchHosts: async () => {},
    getSnapshot: () => ({ version: '20261120.01', latestVersion: '20261120.99', selfHostName: 'legion', hosts: [{ name: 'macmini', online: true, version: '20261120.01' }] })
  };

  configureFleetUpdateUI(deps);
  openFleetUpdateModal({ latest_version: '20261120.99' }, deps);

  elements.get('btnExecuteUpdate').click();
  await new Promise(r => setTimeout(r, 30));

  assert.equal(elements.get('btnDismissUpdate').textContent, 'Run in Background');
  assert.equal(elements.get('btnExecuteUpdate').textContent, '⏳ Updating 2/2...');

  // Dismiss modal while update is running in background
  deps.hideModal();
  assert.equal(isFleetUpdateBadgeActive(), true);
  assert.match(elements.get('updateBadge').textContent, /⏳ Updating 2\/2 · macmini/);

  // Complete stream + daemon readiness while modal stays dismissed
  releaseStream();
  await new Promise(r => setTimeout(r, 1100));

  assert.equal(reloadCount, 0, 'background update must NOT force location.reload() while modal is dismissed');
  assert.equal(getFleetRunState().reloadPending, true);
  assert.equal(elements.get('updateBadge').textContent, '✅ Updated to v20261120.99 — Click to Reload');
  assert.equal(toasts.length, 1);

  // Re-open modal and verify log & reload button state attach immediately
  openFleetUpdateModal({}, deps);
  assert.equal(elements.get('btnExecuteUpdate').textContent, '🔄 Reload UI to Apply v20261120.99');
  assert.match(elements.get('fleetExecList').innerHTML, /Local daemon ready \(v20261120.99\)/);

  elements.get('btnExecuteUpdate').click();
  assert.equal(reloadCount, 1);

  delete globalThis.document;
  delete globalThis.window;
  delete globalThis.fetch;
});

function makeSSEBody(chunks) {
  const encoder = new TextEncoder();
  let idx = 0;
  return {
    getReader() {
      return {
        async read() {
          if (idx >= chunks.length) return { done: true };
          return { done: false, value: encoder.encode(chunks[idx++]) };
        }
      };
    }
  };
}

test('premature SSE EOF transitions fleetRunState to error instead of remaining stuck in running', async () => {
  resetFleetRunState();
  const { elements, document } = createFakeDOM();
  globalThis.document = document;
  globalThis.window = { location: { reload: () => {} } };
  globalThis.fetch = async () => ({
    ok: true,
    headers: { get: () => 'text/event-stream' },
    body: makeSSEBody([
      'data: {"type":"step","step_id":"preflight","phase":"preflight","label":"Checked","status":"done","completed_steps":0,"total_steps":2}\n\n'
    ])
  });

  try {
    const modalOverlay = elements.get('modalOverlay');
    const modalBody = elements.get('modalBody');
    const modalFooter = elements.get('modalFooter');
    const deps = {
      showModal: (_t, body, footer) => { modalBody.innerHTML = body; modalFooter.innerHTML = footer; modalOverlay.style.display = 'flex'; },
      hideModal: () => { modalOverlay.style.display = 'none'; notifyUpdateModalClosed(); },
      showToast: () => {},
      fetchVersion: async () => {},
      fetchHosts: async () => {},
      getSnapshot: () => ({ version: '20261120.01', latestVersion: '20261120.02', selfHostName: 'legion', hosts: [] })
    };
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    elements.get('btnExecuteUpdate').click();
    await new Promise(r => setTimeout(r, 30));

    assert.equal(getFleetRunState().status, 'error');
    assert.equal(getFleetRunState().errorMessage, 'Update stream ended before completion');
    assert.equal(elements.get('btnExecuteUpdate').textContent, '⚠️ Retry Failed Hosts');
    assert.equal(elements.get('btnExecuteUpdate').disabled, false);
  } finally {
    delete globalThis.document;
    delete globalThis.window;
    delete globalThis.fetch;
  }
});

test('runSingleHostUpdate preserves singleton uiDeps, handles EOF, and replaces close handler without calling hideModal', async () => {
  resetFleetRunState();
  const { elements, document } = createFakeDOM();
  globalThis.document = document;
  globalThis.window = { location: { reload: () => {} } };
  globalThis.confirm = () => true;

  let hideCalls = 0;
  let summaryCalls = 0;
  let fetchHostsCalls = 0;
  const fleetHosts = [{ name: 'macmini', online: true, version: '20261120.01' }];
  const modalOverlay = elements.get('modalOverlay');
  const modalBody = elements.get('modalBody');
  const modalFooter = elements.get('modalFooter');

  configureFleetUpdateUI({
    showModal: (_t, body, footer) => { modalBody.innerHTML = body; modalFooter.innerHTML = footer; modalOverlay.style.display = 'flex'; },
    hideModal: () => { hideCalls++; modalOverlay.style.display = 'none'; notifyUpdateModalClosed(); },
    showToast: () => {},
    fetchVersion: async () => {},
    fetchHosts: async () => {},
    getSnapshot: () => ({ version: '20261120.02', latestVersion: '20261120.02', selfHostName: 'legion', hosts: fleetHosts })
  });

  try {
    // 1. Success path: verify mBtnBackToDetailsUpdate only calls showHostSummaryModal (not hideModal) and preserves singleton hosts
    globalThis.fetch = async () => ({
      ok: true,
      headers: { get: () => 'text/event-stream' },
      body: makeSSEBody([
        'data: {"type":"step","step_id":"host:macmini","phase":"updating_host","host":"macmini","label":"Updating macmini","status":"done","completed_steps":2,"total_steps":2}\n\n',
        'data: {"type":"complete","phase":"complete","status":"done","completed_steps":2,"total_steps":2,"result":{"status":"success","version":"20261120.02","message":"Upgraded"}}\n\n'
      ])
    });

    await runSingleHostUpdate({ name: 'macmini' }, {
      showModal: (_t, body, footer) => { modalBody.innerHTML = body; modalFooter.innerHTML = footer; modalOverlay.style.display = 'flex'; },
      hideModal: () => { hideCalls++; modalOverlay.style.display = 'none'; notifyUpdateModalClosed(); },
      fetchHosts: async () => { fetchHostsCalls++; },
      fetchSessions: async () => {},
      showHostSummaryModal: () => { summaryCalls++; },
      getSnapshot: () => ({ version: '20261120.02', latestVersion: '20261120.02' })
    });

    assert.equal(fetchHostsCalls, 1);
    assert.equal(elements.get('mBtnBackToDetailsUpdate').textContent, '← Go Back to Details');
    elements.get('mBtnBackToDetailsUpdate').click();
    assert.equal(summaryCalls, 1);
    assert.equal(hideCalls, 0, 'Go Back to Details must not also invoke hideModal');

    // Re-opening fleet modal must still use singleton getSnapshot (with hosts)
    openFleetUpdateModal({ latest_version: '20261120.02' });
    assert.match(modalBody.innerHTML, /macmini/);

    // 2. Premature SSE EOF in runSingleHostUpdate must transition to error and skip fetchHosts
    globalThis.fetch = async () => ({
      ok: true,
      headers: { get: () => 'text/event-stream' },
      body: makeSSEBody([
        'data: {"type":"step","step_id":"compile:darwin/arm64","phase":"compiling","label":"Compiling","status":"running","completed_steps":0,"total_steps":2}\n\n'
      ])
    });

    await runSingleHostUpdate({ name: 'macmini' }, {
      showModal: (_t, body, footer) => { modalBody.innerHTML = body; modalFooter.innerHTML = footer; modalOverlay.style.display = 'flex'; },
      hideModal: () => { hideCalls++; },
      fetchHosts: async () => { fetchHostsCalls++; },
      fetchSessions: async () => {},
      showHostSummaryModal: () => {},
      getSnapshot: () => ({ version: '20261120.02', latestVersion: '20261120.02' })
    });

    assert.equal(fetchHostsCalls, 1, 'fetchHosts must not run when single-host SSE stream ends prematurely');
    assert.equal(elements.get('singleHostSummary').textContent, 'Update stream ended before completion');
    assert.equal(elements.get('singleHostStepLabel').textContent, 'Failed updating macmini');
  } finally {
    delete globalThis.document;
    delete globalThis.window;
    delete globalThis.fetch;
    delete globalThis.confirm;
  }
});

test('remoteDeferred disables upgrade button and newly online outdated host resets stale done state', async () => {
  resetFleetRunState();
  const { elements, document } = createFakeDOM();
  globalThis.document = document;
  globalThis.window = { location: { reload: () => {} } };

  let remoteHosts = [{ name: 'macmini', online: false, version: '20261120.01' }];
  const modalOverlay = elements.get('modalOverlay');
  const modalBody = elements.get('modalBody');
  const modalFooter = elements.get('modalFooter');
  const deps = {
    showModal: (_t, body, footer) => { modalBody.innerHTML = body; modalFooter.innerHTML = footer; modalOverlay.style.display = 'flex'; },
    hideModal: () => { modalOverlay.style.display = 'none'; notifyUpdateModalClosed(); },
    showToast: () => {},
    fetchVersion: async () => {},
    fetchHosts: async () => {},
    getSnapshot: () => ({ version: '20261120.02', latestVersion: '20261120.02', selfHostName: 'legion', hosts: remoteHosts })
  };

  try {
    // 1. remoteDeferred disables #btnExecuteUpdate
    globalThis.fetch = async () => ({
      ok: true,
      headers: { get: () => 'text/event-stream' },
      body: makeSSEBody([
        'data: {"type":"complete","phase":"complete","status":"done","completed_steps":2,"total_steps":2,"result":{"new_version":"20261120.02","restarting_local":false,"host_results":[{"host":"macmini","status":"restart_deferred","new_version":"20261120.02"}]}}\n\n'
      ])
    });
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    elements.get('btnExecuteUpdate').click();
    await new Promise(r => setTimeout(r, 30));
    assert.equal(elements.get('btnExecuteUpdate').textContent, '⏳ Remote Daemon(s) Draining');
    assert.equal(elements.get('btnExecuteUpdate').disabled, true);

    // 2. Clean done run with offline host, then re-open after offline host reconnects outdated for same latestVer
    globalThis.fetch = async () => ({
      ok: true,
      headers: { get: () => 'text/event-stream' },
      body: makeSSEBody([
        'data: {"type":"complete","phase":"complete","status":"done","completed_steps":1,"total_steps":1,"result":{"new_version":"20261120.02","restarting_local":false,"host_results":[{"host":"macmini","status":"skipped_offline"}]}}\n\n'
      ])
    });
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    elements.get('btnExecuteUpdate').click();
    await new Promise(r => setTimeout(r, 30));
    assert.equal(getFleetRunState().status, 'done');
    assert.equal(elements.get('btnExecuteUpdate').disabled, true);

    // Host comes back online still running old version
    remoteHosts = [{ name: 'macmini', online: true, version: '20261120.01' }];
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    assert.equal(getFleetRunState().status, 'idle');
    assert.equal(elements.get('btnExecuteUpdate').disabled, false);
    assert.equal(elements.get('btnExecuteUpdate').textContent, '⚡ Upgrade All Outdated Hosts to v20261120.02');
  } finally {
    delete globalThis.document;
    delete globalThis.window;
    delete globalThis.fetch;
  }
});

test('mixed error+remoteDeferred keeps Retry enabled, reloadPending+remoteDeferred keeps Reload enabled, and newerRelease resets deferred runs', async () => {
  resetFleetRunState();
  const { elements, document } = createFakeDOM();
  globalThis.document = document;
  globalThis.window = { location: { reload: () => {} } };

  const modalOverlay = elements.get('modalOverlay');
  const modalBody = elements.get('modalBody');
  const modalFooter = elements.get('modalFooter');
  const deps = {
    showModal: (_t, body, footer) => { modalBody.innerHTML = body; modalFooter.innerHTML = footer; modalOverlay.style.display = 'flex'; },
    hideModal: () => { modalOverlay.style.display = 'none'; notifyUpdateModalClosed(); },
    showToast: () => {},
    fetchVersion: async () => {},
    fetchHosts: async () => {},
    getSnapshot: () => ({ version: '20261120.02', latestVersion: '20261120.02', selfHostName: 'legion', hosts: [{ name: 'macmini', online: true, version: '20261120.02' }] })
  };

  try {
    // 1. Mixed host_results (error + restart_deferred) -> Retry Failed Hosts must remain enabled
    globalThis.fetch = async () => ({
      ok: true,
      headers: { get: () => 'text/event-stream' },
      body: makeSSEBody([
        'data: {"type":"complete","phase":"complete","status":"done","completed_steps":2,"total_steps":2,"result":{"new_version":"20261120.02","restarting_local":false,"host_results":[{"host":"gpu1","status":"error","message":"ssh timeout"},{"host":"macmini","status":"restart_deferred","new_version":"20261120.02"}]}}\n\n'
      ])
    });
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    elements.get('btnExecuteUpdate').click();
    await new Promise(r => setTimeout(r, 30));
    assert.equal(getFleetRunState().remoteDeferred, true);
    assert.equal(getFleetRunState().status, 'error');
    assert.equal(elements.get('btnExecuteUpdate').textContent, '⚠️ Retry Failed Hosts');
    assert.equal(elements.get('btnExecuteUpdate').disabled, false);

    // 2. Background restarting_local + remote restart_deferred -> Reload UI must remain enabled
    resetFleetRunState();
    globalThis.fetch = async (url) => {
      if (url.startsWith('/v1/update')) {
        return {
          ok: true,
          headers: { get: () => 'text/event-stream' },
          body: makeSSEBody([
            'data: {"type":"complete","phase":"complete","status":"done","completed_steps":2,"total_steps":2,"result":{"new_version":"20261120.02","restarting_local":true,"host_results":[{"host":"macmini","status":"restart_deferred","new_version":"20261120.02"}]}}\n\n'
          ])
        };
      }
      return { ok: true, json: async () => ({ version: '20261120.02' }) };
    };
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    elements.get('btnExecuteUpdate').click();
    await new Promise(r => setTimeout(r, 20));
    deps.hideModal();
    await new Promise(r => setTimeout(r, 1050));
    assert.equal(getFleetRunState().reloadPending, true);
    assert.equal(getFleetRunState().remoteDeferred, true);
    openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
    assert.equal(elements.get('btnExecuteUpdate').textContent, '🔄 Reload UI to Apply v20261120.02');
    assert.equal(elements.get('btnExecuteUpdate').disabled, false);

    // 3. Opening modal with a newer release (20261120.03) resets remoteDeferred & localDeferred done runs to idle
    for (const payload of [
      { new_version: '20261120.02', restarting_local: false, host_results: [{ host: 'macmini', status: 'restart_deferred' }] },
      { new_version: '20261120.02', restarting_local: false, restart_deferred: true, host_results: [] }
    ]) {
      resetFleetRunState();
      globalThis.fetch = async () => ({
        ok: true,
        headers: { get: () => 'text/event-stream' },
        body: makeSSEBody([`data: ${JSON.stringify({ type: 'complete', phase: 'complete', status: 'done', completed_steps: 1, total_steps: 1, result: payload })}\n\n`])
      });
      openFleetUpdateModal({ latest_version: '20261120.02' }, deps);
      elements.get('btnExecuteUpdate').click();
      await new Promise(r => setTimeout(r, 30));
      assert.equal(getFleetRunState().status, 'done');
      assert.equal(elements.get('btnExecuteUpdate').disabled, true);

      openFleetUpdateModal({ latest_version: '20261120.03' }, deps);
      assert.equal(getFleetRunState().status, 'idle');
      assert.equal(elements.get('btnExecuteUpdate').disabled, false);
      assert.equal(elements.get('btnExecuteUpdate').textContent, '⚡ Upgrade All Outdated Hosts to v20261120.03');
    }
  } finally {
    delete globalThis.document;
    delete globalThis.window;
    delete globalThis.fetch;
  }
});
