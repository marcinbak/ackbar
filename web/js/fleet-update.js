let fleetRunState = { status: 'idle', steps: [], hostStatuses: {}, completedSteps: 0, totalSteps: 0 };
let lastModalData = {};
let modalOpen = false;
let fleetTickerId = null;
let singleTickerId = null;
let doneExpiryTimerId = null;
let fleetBadgeWasVisible = false;
let uiDeps = {};

const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const pad2 = n => String(n).padStart(2, '0');

export const formatTimerClock = ms => {
  const t = Math.max(0, Math.floor((ms || 0) / 100));
  const s = Math.floor(t / 10);
  return `${Math.floor(s / 60)}:${pad2(s % 60)}.${t % 10}`;
};

export const formatBadgeElapsed = ms => {
  const s = Math.max(0, Math.floor((ms || 0) / 1000));
  return `${Math.floor(s / 60)}:${pad2(s % 60)}`;
};

export const formatStepDuration = ms => {
  const s = Math.max(0, (ms || 0) / 1000);
  return s < 60 ? `${s.toFixed(1)}s` : `${Math.floor(s / 60)}m ${pad2(Math.floor(s % 60))}s`;
};

export const formatUpdateElapsed = formatStepDuration;

export function computeCurrentStepNumber(completedSteps, totalSteps, isDone) {
  if (!totalSteps || totalSteps <= 0) return 0;
  return isDone ? totalSteps : Math.min(totalSteps, Math.max(1, (completedSteps || 0) + 1));
}

export function formatUpdateButtonLabel(runState, latestVer) {
  if (!runState || runState.status === 'idle') return `⚡ Upgrade All Outdated Hosts to v${latestVer || 'latest'}`;
  if (runState.status === 'running') {
    return runState.totalSteps > 0 ? `⏳ Updating ${computeCurrentStepNumber(runState.completedSteps, runState.totalSteps, false)}/${runState.totalSteps}...` : '⏳ Preparing Update...';
  }
  if (runState.status === 'reloading') return '✅ All Hosts Updated! Reloading...';
  if (runState.status === 'error' || (runState.failedCount || 0) > 0) return '⚠️ Retry Failed Hosts';
  if (runState.reloadPending) return `🔄 Reload UI to Apply v${runState.newVersion || latestVer || 'latest'}`;
  if (runState.localDeferred) return '⏳ Restart Scheduled After Active Turn';
  if (runState.remoteDeferred) return '⏳ Remote Daemon(s) Draining';
  return runState.totalSteps > 0 ? `✅ Fleet Updated (${runState.totalSteps}/${runState.totalSteps})` : '✅ Up to Date';
}

function getActiveStepShortLabel(runState) {
  const running = (runState?.steps || []).filter(s => s?.status === 'running');
  const active = running.find(s => s.step_id !== 'preflight') || running[running.length - 1];
  if (!active) return '';
  if (active.step_id === 'restarting_local') return 'restarting';
  if (active.host) return active.host;
  if (active.step_id?.startsWith('compile:')) return active.step_id.slice(8);
  if (active.step_id?.startsWith('download:')) return active.step_id.slice(9);
  return active.step_id === 'preflight' ? 'preflight' : '';
}

function computeRunElapsedMs(runState, nowMs) {
  if (!runState?.startedAtMs) return 0;
  const active = runState.status === 'running' || runState.status === 'reloading';
  if (!active && runState.finalElapsedMs != null) return runState.finalElapsedMs;
  return Math.max(0, (!active && runState.finishedAtMs ? runState.finishedAtMs : nowMs) - runState.startedAtMs);
}

export function formatNavbarUpdateBadge(runState, nowMs = Date.now()) {
  if (!runState || runState.status === 'idle') return { visible: false, text: '', className: '', action: '' };
  const elapsedMs = computeRunElapsedMs(runState, nowMs);
  if (runState.status === 'running' || runState.status === 'reloading') {
    const n = computeCurrentStepNumber(runState.completedSteps, runState.totalSteps, false);
    const short = getActiveStepShortLabel(runState);
    const text = runState.totalSteps > 0
      ? `⏳ Updating ${n}/${runState.totalSteps}${short ? ` · ${short}` : ''} (${formatBadgeElapsed(elapsedMs)})`
      : `⏳ Updating... (${formatBadgeElapsed(elapsedMs)})`;
    return { visible: true, text, className: 'is-updating', action: 'open_modal' };
  }
  if (runState.reloadPending) return { visible: true, text: `✅ Updated to v${runState.newVersion || 'latest'} — Click to Reload`, className: 'is-ready', action: 'reload' };
  if (runState.status === 'error' || (runState.failedCount || 0) > 0) return { visible: true, text: `⚠️ Update Failed (${runState.failedCount || 1}) — View Log`, className: 'is-error', action: 'open_modal' };
  if (runState.status === 'done' && runState.showDoneBadgeUntil && nowMs < runState.showDoneBadgeUntil) {
    return { visible: true, text: `✅ Fleet Updated (${formatStepDuration(elapsedMs)})`, className: 'is-ready', action: 'open_modal' };
  }
  return { visible: false, text: '', className: '', action: '' };
}

export function parseSSEUpdateChunk(buffer, chunk) {
  const parts = (((buffer || '') + (chunk || '')).replace(/\r\n/g, '\n')).split('\n\n');
  const rest = parts.pop() || '';
  const events = [];
  for (const frame of parts) {
    const lines = frame.split('\n').filter(l => l.startsWith('data:')).map(l => l.slice(5).trimStart());
    if (lines.length) try { events.push(JSON.parse(lines.join('\n'))); } catch (_) {}
  }
  return { events, rest };
}

export function createFleetRunState(targetVersion = '', nowMs = Date.now()) {
  return {
    status: 'running', targetVersion, newVersion: targetVersion, startedAtMs: nowMs,
    finishedAtMs: null, finalElapsedMs: null, completedSteps: 0, totalSteps: 0,
    steps: [], hostStatuses: {}, failedCount: 0, localDeferred: false,
    remoteDeferred: false, restartingLocal: false, reloadPending: false,
    showDoneBadgeUntil: 0, errorMessage: '', result: null
  };
}

export function applyUpdateProgressEvent(runState, event, nowMs = Date.now()) {
  if (!event || typeof event !== 'object') return runState;
  const next = { ...runState, steps: (runState.steps || []).map(s => ({ ...s })), hostStatuses: { ...(runState.hostStatuses || {}) } };
  if (event.total_steps > 0) next.totalSteps = event.total_steps;
  if (typeof event.completed_steps === 'number') next.completedSteps = Math.max(next.completedSteps || 0, event.completed_steps);

  if (event.type === 'step') {
    const id = event.step_id || `${event.phase || 'step'}:${event.host || ''}`;
    const idx = next.steps.findIndex(s => s.step_id === id), prev = idx >= 0 ? next.steps[idx] : null;
    const startedAtMs = prev?.startedAtMs || nowMs;
    const terminal = ['done', 'error', 'up_to_date', 'skipped_offline', 'restart_deferred'].includes(event.status);
    const detail = event.detail ? event.detail : (terminal ? '' : (prev?.detail || ''));
    const entry = {
      step_id: id, phase: event.phase || prev?.phase || '', host: event.host || prev?.host || '',
      label: event.label || prev?.label || id, detail, status: event.status || prev?.status || 'running',
      startedAtMs, elapsedMs: event.step_elapsed_ms > 0 ? event.step_elapsed_ms : Math.max(0, nowMs - startedAtMs)
    };
    if (idx >= 0) next.steps[idx] = entry; else next.steps.push(entry);
    if (id === 'local') next.hostStatuses.__local__ = { status: entry.status, detail, label: entry.label };
    else if (entry.host && entry.phase === 'updating_host') next.hostStatuses[entry.host] = { status: entry.status, detail, label: entry.label };
  } else if (event.type === 'complete') {
    const res = event.result || {}, hostResults = Array.isArray(res.host_results) ? res.host_results : [];
    for (const hr of hostResults) {
      if (hr?.host) next.hostStatuses[hr.host] = { status: hr.status === 'success' || hr.status === 'updated' ? 'done' : hr.status, detail: hr.message || '', label: hr.new_version ? `v${hr.new_version}` : '' };
    }
    const failed = hostResults.filter(h => h?.status === 'error');
    Object.assign(next, { result: res, finishedAtMs: nowMs, finalElapsedMs: event.elapsed_ms > 0 ? event.elapsed_ms : Math.max(0, nowMs - (next.startedAtMs || nowMs)), failedCount: failed.length, localDeferred: Boolean(res.restart_deferred), remoteDeferred: hostResults.some(h => h?.status === 'restart_deferred'), restartingLocal: Boolean(res.restarting_local), newVersion: res.new_version || res.version || next.targetVersion });
    if (failed.length > 0 || res.status === 'error') {
      Object.assign(next, { status: 'error', failedCount: Math.max(1, failed.length), errorMessage: res.message || `${failed.length} host(s) failed to update` });
    } else if (next.restartingLocal) {
      next.status = 'reloading';
    } else {
      Object.assign(next, { status: 'done', showDoneBadgeUntil: nowMs + 6000 });
    }
  } else if (event.type === 'error') {
    Object.assign(next, { status: 'error', errorMessage: event.message || 'Update failed', failedCount: Math.max(1, next.failedCount || 0), finishedAtMs: nowMs, finalElapsedMs: event.elapsed_ms > 0 ? event.elapsed_ms : Math.max(0, nowMs - (next.startedAtMs || nowMs)) });
  }
  return next;
}

export function configureFleetUpdateUI(deps) { if (deps && typeof deps === 'object') uiDeps = { ...uiDeps, ...deps }; }
export function isFleetUpdateBadgeActive() { return formatNavbarUpdateBadge(fleetRunState, Date.now()).visible; }
export function getFleetRunState() { return fleetRunState; }
export function resetFleetRunState() {
  stopFleetTicker();
  if (singleTickerId) { clearInterval(singleTickerId); singleTickerId = null; }
  if (doneExpiryTimerId) { clearTimeout(doneExpiryTimerId); doneExpiryTimerId = null; }
  fleetBadgeWasVisible = false; modalOpen = false; lastModalData = {};
  fleetRunState = { status: 'idle', steps: [], hostStatuses: {}, completedSteps: 0, totalSteps: 0 };
}
export function notifyUpdateModalClosed() {
  modalOpen = false;
  if (singleTickerId) { clearInterval(singleTickerId); singleTickerId = null; }
  syncNavbarUpdateBadge();
}

const isFleetModalMounted = () => typeof document !== 'undefined' && modalOpen && Boolean(document.getElementById('fleetExecRoot')?.isConnected && document.getElementById('modalOverlay')?.style.display !== 'none');

function renderStepRowsHtml(runState, nowMs) {
  const icons = { running: '<span class="fleet-step-spinner" aria-hidden="true"></span>', done: '<span class="fleet-step-icon is-done">✓</span>', up_to_date: '<span class="fleet-step-icon is-done">✓</span>', skipped_offline: '<span class="fleet-step-icon is-muted">○</span>', restart_deferred: '<span class="fleet-step-icon is-warn">⏳</span>', error: '<span class="fleet-step-icon is-error">✗</span>' };
  return (runState.steps || []).map(step => {
    const durMs = step.status === 'running' ? Math.max(0, nowMs - (step.startedAtMs || nowMs)) : (step.elapsedMs || 0);
    const dur = (step.status === 'up_to_date' || step.status === 'skipped_offline') ? '' : formatStepDuration(durMs);
    const det = step.detail ? `<span class="fleet-exec-detail">${esc(step.detail)}</span>` : '';
    return `<div class="fleet-exec-step is-${esc(step.status || 'running')}">${icons[step.status] || '<span class="fleet-step-icon">•</span>'}<div class="fleet-exec-step-text"><span class="fleet-exec-label">${esc(step.label)}</span>${det}</div><span class="fleet-exec-dur">${esc(dur)}</span></div>`;
  }).join('');
}

function renderExecLogDOM(listId, timerId, summaryId, runState, active, nowMs) {
  const listEl = document.getElementById(listId), timerEl = document.getElementById(timerId), summaryEl = document.getElementById(summaryId);
  if (timerEl) timerEl.textContent = formatTimerClock(computeRunElapsedMs(runState, nowMs));
  if (listEl) listEl.innerHTML = renderStepRowsHtml(runState, nowMs);
  if (summaryEl) {
    const msg = runState.status === 'error' ? runState.errorMessage : (runState.result?.message || '');
    summaryEl.style.display = msg && !active ? 'block' : 'none';
    if (msg && !active) summaryEl.textContent = msg;
  }
}

function renderHostBadgeHtml(hs, def) {
  if (!hs) return def;
  const map = { running: `<span class="fleet-host-running">⏳ ${esc(hs.detail || 'Updating...')}</span>`, done: `<span class="fleet-host-done">✅ ${esc(hs.label || 'Updated')}</span>`, up_to_date: '<span class="fleet-host-done">✅ Up to date</span>', skipped_offline: '<span class="fleet-host-muted">🔴 Offline (skipped)</span>', restart_deferred: '<span class="fleet-host-warn">⏳ Restart deferred</span>', error: '<span class="fleet-host-err">❌ Failed</span>' };
  return map[hs.status] || def;
}

export function syncNavbarUpdateBadge() {
  if (typeof document === 'undefined') return false;
  const badgeEl = document.getElementById('updateBadge');
  if (!badgeEl) return false;
  const info = formatNavbarUpdateBadge(fleetRunState, Date.now());
  if (!info.visible) {
    badgeEl.classList.remove('is-updating', 'is-ready', 'is-error');
    if (fleetBadgeWasVisible) { fleetBadgeWasVisible = false; uiDeps.onBadgeInactive?.(); }
    return false;
  }
  fleetBadgeWasVisible = true;
  badgeEl.style.display = 'inline-block';
  const nextClass = `brand-badge update-badge ${info.className}`.trim();
  if (badgeEl.className !== nextClass) badgeEl.className = nextClass;
  if (badgeEl.textContent !== info.text) badgeEl.textContent = info.text;
  badgeEl.title = info.action === 'reload' ? 'Click to reload UI' : 'Click to inspect fleet update progress';
  badgeEl.onclick = info.action === 'reload' ? () => globalThis.window?.location?.reload?.() : () => openFleetUpdateModal(lastModalData);
  return true;
}

function syncFleetModalDOM() {
  if (!isFleetModalMounted()) return;
  const nowMs = Date.now(), root = document.getElementById('fleetExecRoot'), logBox = document.getElementById('fleetExecLog');
  const countEl = document.getElementById('fleetExecStepCount'), execBtn = document.getElementById('btnExecuteUpdate'), dismissBtn = document.getElementById('btnDismissUpdate');
  const active = fleetRunState.status === 'running' || fleetRunState.status === 'reloading';
  const show = fleetRunState.status !== 'idle' || fleetRunState.steps.length > 0;
  if (logBox) logBox.style.display = show ? 'block' : 'none';
  renderExecLogDOM('fleetExecList', 'fleetExecTimer', 'fleetExecSummary', fleetRunState, active, nowMs);
  if (countEl) {
    countEl.textContent = fleetRunState.totalSteps > 0
      ? `Step ${computeCurrentStepNumber(fleetRunState.completedSteps, fleetRunState.totalSteps, !active && fleetRunState.status === 'done')}/${fleetRunState.totalSteps}`
      : (active ? 'Checking...' : 'Ready');
  }
  root?.querySelectorAll('[data-fleet-host-status]').forEach(span => {
    const key = span.getAttribute('data-fleet-host-status');
    const def = span.getAttribute('data-default-html') || span.innerHTML;
    if (!span.hasAttribute('data-default-html')) span.setAttribute('data-default-html', def);
    span.innerHTML = renderHostBadgeHtml(fleetRunState.hostStatuses?.[key], def);
  });
  if (execBtn) {
    execBtn.textContent = formatUpdateButtonLabel(fleetRunState, root?.getAttribute('data-latest-ver') || fleetRunState.targetVersion || 'latest');
    execBtn.disabled = active || fleetRunState.localDeferred || fleetRunState.remoteDeferred || (fleetRunState.status === 'done' && !fleetRunState.reloadPending && fleetRunState.failedCount === 0);
  }
  if (dismissBtn) {
    dismissBtn.disabled = false;
    dismissBtn.textContent = active ? 'Run in Background' : (fleetRunState.status === 'idle' ? 'Dismiss' : 'Close');
  }
}

async function fetchSSEStream(url, init, onEvent) {
  const res = await fetch(url, init);
  const ct = res.headers?.get?.('content-type') || '';
  if (!ct.includes('text/event-stream') || !res.body?.getReader) {
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.message || res.statusText || `HTTP ${res.status}`);
    return onEvent({ type: 'complete', result: data });
  }
  const reader = res.body.getReader(), decoder = new TextDecoder();
  let buf = '';
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    const parsed = parseSSEUpdateChunk(buf, decoder.decode(value, { stream: true }));
    buf = parsed.rest;
    parsed.events.forEach(onEvent);
  }
  if (buf.trim()) parseSSEUpdateChunk(`${buf}\n\n`, '').events.forEach(onEvent);
}

async function waitForDaemonReady(expectedVer) {
  const deadline = Date.now() + 10000;
  await new Promise(r => setTimeout(r, 900));
  while (Date.now() < deadline) {
    try {
      const r = await fetch('/v1/version', { cache: 'no-store' });
      if (r.ok) { const d = await r.json(); if (!expectedVer || !d.version || d.version === expectedVer) return true; }
    } catch (_) {}
    await new Promise(r => setTimeout(r, 500));
  }
  return false;
}

const stopFleetTicker = () => { if (fleetTickerId) { clearInterval(fleetTickerId); fleetTickerId = null; } };

async function startFleetUpdate(targetVer) {
  if (fleetRunState.status === 'running' || fleetRunState.status === 'reloading') return;
  if (doneExpiryTimerId) { clearTimeout(doneExpiryTimerId); doneExpiryTimerId = null; }
  fleetRunState = createFleetRunState(targetVer, Date.now());
  stopFleetTicker();
  fleetTickerId = setInterval(() => { syncFleetModalDOM(); syncNavbarUpdateBadge(); }, 100);
  syncFleetModalDOM();
  syncNavbarUpdateBadge();

  try {
    await fetchSSEStream('/v1/update?stream=true', { method: 'POST', headers: { Accept: 'text/event-stream' } }, evt => {
      fleetRunState = applyUpdateProgressEvent(fleetRunState, evt, Date.now());
      syncFleetModalDOM();
      syncNavbarUpdateBadge();
    });
    if (fleetRunState.status === 'running') {
      fleetRunState = applyUpdateProgressEvent(fleetRunState, { type: 'error', message: 'Update stream ended before completion' }, Date.now());
    }
  } catch (err) {
    if (fleetRunState.status !== 'reloading' && fleetRunState.status !== 'done') {
      fleetRunState = applyUpdateProgressEvent(fleetRunState, { type: 'error', message: err.message || 'Connection lost during update' }, Date.now());
    }
  }

  if (fleetRunState.status === 'reloading') {
    const ver = fleetRunState.newVersion || targetVer;
    fleetRunState = applyUpdateProgressEvent(fleetRunState, { type: 'step', step_id: 'restarting_local', phase: 'restarting', label: `Restarting local daemon & waiting for v${ver}...`, status: 'running' }, Date.now());
    syncFleetModalDOM(); syncNavbarUpdateBadge();
    await waitForDaemonReady(ver);
    fleetRunState = applyUpdateProgressEvent(fleetRunState, { type: 'step', step_id: 'restarting_local', phase: 'restarting', label: `Local daemon ready (v${ver})`, status: 'done' }, Date.now());
    Object.assign(fleetRunState, { finishedAtMs: Date.now(), finalElapsedMs: Math.max(0, Date.now() - fleetRunState.startedAtMs) });
    stopFleetTicker();
    if (isFleetModalMounted()) { globalThis.window?.location?.reload?.(); return; }
    Object.assign(fleetRunState, { status: 'done', reloadPending: true, newVersion: ver });
    syncNavbarUpdateBadge();
    uiDeps.showToast?.(`Update to v${ver} ready — click badge to reload UI`, 'info', 5000);
    return;
  }

  stopFleetTicker();
  if (fleetRunState.localDeferred) uiDeps.showToast?.('Update installed — daemon will restart when active turn finishes', 'info', 4000);
  syncFleetModalDOM(); syncNavbarUpdateBadge();
  uiDeps.fetchVersion?.(); uiDeps.fetchHosts?.();
  if (fleetRunState.status === 'done' && fleetRunState.showDoneBadgeUntil > Date.now()) {
    doneExpiryTimerId = setTimeout(() => syncNavbarUpdateBadge(), Math.max(50, fleetRunState.showDoneBadgeUntil - Date.now() + 50));
  }
}

export function openFleetUpdateModal(data = {}, deps = null) {
  if (deps) configureFleetUpdateUI(deps);
  if (data && Object.keys(data).length > 0) lastModalData = data;
  const snap = uiDeps.getSnapshot ? uiDeps.getSnapshot() : {};
  const curVer = snap.version || '...', latestVer = lastModalData.latest_version || snap.latestVersion || curVer || 'latest';
  const rows = [
    { key: '__local__', name: `${snap.selfHostName || 'local'} (local)`, online: true, version: curVer },
    ...(snap.hosts || []).map(h => ({ key: h.name, name: h.name, online: !!h.online, version: h.version || 'unknown' }))
  ];
  const isOutdatedRow = r => r.online && r.version && r.version !== 'online' && r.version !== latestVer;
  const hasOutdated = rows.some(isOutdatedRow);
  if (fleetRunState.status === 'done' && !fleetRunState.reloadPending && !fleetRunState.localDeferred && !fleetRunState.remoteDeferred && ((fleetRunState.newVersion && latestVer && fleetRunState.newVersion !== latestVer) || hasOutdated)) {
    fleetRunState = { status: 'idle', steps: [], hostStatuses: {}, completedSteps: 0, totalSteps: 0 };
  }
  const releaseUrl = lastModalData.release_url || snap.releaseUrl || `https://github.com/marcinbak/ackbar/releases/tag/v${latestVer}`;
  const methodStr = (lastModalData.is_homebrew ?? snap.isHomebrew) ? 'Homebrew (brew upgrade ackbar)' : 'Prebuilt Binary (~/.local/bin) + SSH Cross-Compile';
  const fleetHtml = rows.map(r => `<div class="fleet-host-row"><strong>${esc(r.name)}</strong><span class="fleet-host-status" data-fleet-host-status="${esc(r.key)}">${!r.online ? '<span class="fleet-host-muted">🔴 Offline (skipped)</span>' : isOutdatedRow(r) ? `<span class="fleet-host-warn">⚡ v${esc(r.version)} ➔ v${esc(latestVer)}</span>` : `<span class="fleet-host-done">✅ v${esc(r.version)} (Up to date)</span>`}</span></div>`).join('');

  uiDeps.showModal?.('⚡ Ackbar Fleet Update', `<div id="fleetExecRoot" data-latest-ver="${esc(latestVer)}" class="fleet-modal-root">
    <div class="fleet-banner-card"><div class="fleet-banner-left"><span class="fleet-banner-icon">⚡</span><div><div class="fleet-banner-title">Fleet Version & Update Manager</div><div class="fleet-banner-sub">Target Release: <code>v${esc(latestVer)}</code></div></div></div><div class="fleet-exec-timer" id="fleetExecTimer">0:00.0</div></div>
    <div class="fleet-host-table">${fleetHtml}</div>
    <div class="fleet-meta-line"><strong>Update Strategy:</strong> <code>${esc(methodStr)}</code></div>
    <div class="fleet-meta-line"><a href="${esc(releaseUrl)}" target="_blank" rel="noopener noreferrer" class="fleet-release-link">🔗 View Release Notes & Artifacts on GitHub</a></div>
    <div id="fleetExecLog" class="fleet-exec-log" style="display: none;"><div class="fleet-exec-header"><span>Execution Log</span><span id="fleetExecStepCount" class="fleet-exec-step-count">Ready</span></div><div id="fleetExecList" class="fleet-exec-list"></div><div id="fleetExecSummary" class="fleet-exec-summary" style="display: none;"></div></div>
  </div>`, `<button class="btn btn-secondary" id="btnDismissUpdate">Dismiss</button><button class="btn btn-primary fleet-upgrade-btn" id="btnExecuteUpdate">⚡ Upgrade All Outdated Hosts to v${esc(latestVer)}</button>`);
  modalOpen = true;
  syncFleetModalDOM();
  document.getElementById('btnDismissUpdate')?.addEventListener('click', () => uiDeps.hideModal?.());
  document.getElementById('btnExecuteUpdate')?.addEventListener('click', () => fleetRunState.reloadPending ? globalThis.window?.location?.reload?.() : startFleetUpdate(latestVer));
}

export async function runSingleHostUpdate(h, deps = null) {
  const d = { ...uiDeps, ...(deps || {}) };
  const snap = d.getSnapshot ? d.getSnapshot() : {};
  if (typeof confirm === 'function' && !confirm(`Upgrade and restart ackbard daemon & hook binaries on "${h.name}" to v${snap.version || 'latest'}?`)) return;

  let runState = createFleetRunState(snap.latestVersion || snap.version || '', Date.now());
  runState.totalSteps = 2;
  d.showModal?.(`Updating ${h.name}`, `<div id="singleHostExecRoot" class="fleet-modal-root"><div class="fleet-exec-log"><div class="fleet-exec-header"><span id="singleHostStepLabel">Updating 1/2 · ${esc(h.name)}</span><span id="singleHostTimer" class="fleet-exec-timer">0:00.0</span></div><div id="singleHostExecList" class="fleet-exec-list"></div><div id="singleHostSummary" class="fleet-exec-summary" style="display: none;"></div></div></div>`, '<button class="btn btn-secondary" id="mBtnBackToDetailsUpdate">Close</button>');
  const initBtn = document.getElementById('mBtnBackToDetailsUpdate');
  if (initBtn) initBtn.onclick = () => d.hideModal?.();

  const renderSingle = () => {
    const active = runState.status === 'running';
    renderExecLogDOM('singleHostExecList', 'singleHostTimer', 'singleHostSummary', runState, active, Date.now());
    const lbl = document.getElementById('singleHostStepLabel');
    if (lbl) {
      const n = computeCurrentStepNumber(runState.completedSteps, runState.totalSteps, runState.status === 'done');
      lbl.textContent = runState.status === 'done' ? `Updated ${h.name} (${n}/${runState.totalSteps})` : runState.status === 'error' ? `Failed updating ${h.name}` : `Updating ${n}/${runState.totalSteps} · ${h.name}`;
    }
  };
  if (singleTickerId) clearInterval(singleTickerId);
  singleTickerId = setInterval(renderSingle, 100);
  renderSingle();

  try {
    await fetchSSEStream('/v1/hosts/update?stream=true', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' }, body: JSON.stringify({ name: h.name }) }, evt => {
      runState = applyUpdateProgressEvent(runState, evt, Date.now());
      renderSingle();
    });
    if (runState.status === 'running') {
      runState = applyUpdateProgressEvent(runState, { type: 'error', message: 'Update stream ended before completion' }, Date.now());
    }
    if (runState.status !== 'error') { await d.fetchHosts?.(); await d.fetchSessions?.(); }
  } catch (err) {
    runState = applyUpdateProgressEvent(runState, { type: 'error', message: err.message }, Date.now());
  } finally {
    if (singleTickerId) { clearInterval(singleTickerId); singleTickerId = null; }
    renderSingle();
    const btn = document.getElementById('mBtnBackToDetailsUpdate');
    if (btn) { btn.textContent = '← Go Back to Details'; btn.onclick = () => d.showHostSummaryModal?.(h); }
  }
}
