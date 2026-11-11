// Headless MCP & Plugin Authentication Broker UI
import { escapeHtml, getSessionBaseUrl } from './utils.js';
import { handleTakeWheel } from './tabs.js';
import { getAuthToken } from './auth.js';

export function isSafeHTTPSUrl(raw) {
  if (!raw || typeof raw !== 'string') return false;
  if (/[\s\x00-\x1f\x7f]/.test(raw)) return false;
  if (raw.length > 4096) return false;
  try {
    const u = new URL(raw);
    return u.protocol === 'https:' && !!u.host && !u.username && !u.password;
  } catch {
    return false;
  }
}

async function submitSessionAuthResponse(tabObj, action, value) {
  if (!tabObj || !tabObj.session) return;
  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
  const token = getAuthToken ? getAuthToken() : '';
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers['Authorization'] = `Bearer ${token}`;
  tabObj.session.blocked = null;
  if (action === 'dismiss') {
    tabObj.session.state = 3;
  } else {
    tabObj.session.state = 1;
  }
  return fetch(`${baseUrl}/v1/sessions/respond`, {
    method: 'POST',
    headers,
    body: JSON.stringify({
      sessionId,
      action,
      value
    })
  });
}

export function renderAuthCard(tabObj, blocked, toolName) {
  if (!tabObj || !tabObj.chatMessagesEl || !blocked) return;

  const existing = tabObj.chatMessagesEl.querySelector('.chat-interactive-card:not(.answered)');
  if (existing) {
    existing.remove();
  }

  const card = document.createElement('div');
  card.className = 'chat-interactive-card card-auth';

  const serverName = blocked.auth_server || blocked.auth_target || 'Service';
  const authTarget = blocked.auth_target || '';
  const isAgentAgy = authTarget === 'agent:agy';
  const isPlugin = authTarget.startsWith('plugin:') || blocked.auth_kind === 'plugin';
  const isMCP = authTarget.startsWith('mcp:') || blocked.auth_kind === 'mcp';
  const isBrokerSupported = isPlugin || isAgentAgy || (isMCP && tabObj.session?.agent === 'claude-code');

  const reason = blocked.reason || `Authentication required for ${serverName}`;
  const hint = blocked.auth_hint || '';
  const initialUrl = blocked.auth_url || '';
  const initialFlowId = blocked.flow_id || '';
  const hasExistingFlow = !!(initialUrl && initialFlowId);

  card.innerHTML = `
    <div class="interactive-card-header">
      <span class="interactive-card-badge">🔑 Authentication Required</span>
      <span class="interactive-card-badge-server">${escapeHtml(serverName)}</span>
      <span class="interactive-card-title">${escapeHtml(blocked.question || reason)}</span>
    </div>
    <div class="interactive-card-body">
      <p class="auth-card-desc">${escapeHtml(reason)}</p>
      ${hint ? `<p class="auth-card-hint">${escapeHtml(hint)}</p>` : ''}
      <div class="auth-flow-section" style="${isBrokerSupported ? '' : 'display: none;'}">
        <div class="auth-launch-row">
          <button type="button" class="btn btn-primary btn-sm btn-auth-launch">
            ${hasExistingFlow ? '🔗 Open Sign-In Link Again' : `🔑 Sign in to ${escapeHtml(serverName)}`}
          </button>
          <span class="auth-launch-status" style="display: none;"></span>
        </div>
        <div class="auth-callback-container" style="${hasExistingFlow ? '' : 'display: none;'}">
          <label class="auth-callback-label">Paste redirect URL or authorization code:</label>
          <div class="auth-callback-row">
            <input type="text" class="auth-callback-input" placeholder="Paste http://127.0.0.1:... or code here" />
            <button type="button" class="btn btn-secondary btn-sm btn-auth-complete">Complete Sign-In</button>
          </div>
          <div class="auth-callback-status" style="display: none;"></div>
        </div>
      </div>
    </div>
    <div class="interactive-card-actions">
      <button type="button" class="btn btn-secondary btn-sm btn-auth-retry">↻ Retry</button>
      <button type="button" class="btn btn-secondary btn-sm btn-auth-terminal">🏎️ Authenticate in Terminal</button>
      <button type="button" class="btn btn-secondary btn-sm btn-auth-dismiss">Dismiss</button>
    </div>
  `;

  const btnLaunch = card.querySelector('.btn-auth-launch');
  const launchStatus = card.querySelector('.auth-launch-status');
  const callbackContainer = card.querySelector('.auth-callback-container');
  const callbackInput = card.querySelector('.auth-callback-input');
  const btnComplete = card.querySelector('.btn-auth-complete');
  const callbackStatus = card.querySelector('.auth-callback-status');

  let activeFlowId = initialFlowId;
  let activeAuthUrl = initialUrl;

  if (btnLaunch) {
    btnLaunch.addEventListener('click', async () => {
      btnLaunch.disabled = true;
      if (launchStatus) {
        launchStatus.style.display = 'inline';
        launchStatus.textContent = 'Starting authentication...';
      }
      try {
        if (activeAuthUrl && isSafeHTTPSUrl(activeAuthUrl)) {
          window.open(activeAuthUrl, '_blank', 'noopener,noreferrer');
          if (launchStatus) launchStatus.textContent = 'Sign-in page opened in new tab.';
          if (callbackContainer) callbackContainer.style.display = 'block';
          btnLaunch.disabled = false;
          return;
        }

        const sessionId = tabObj.session?.id || '';
        const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
        const token = getAuthToken ? getAuthToken() : '';
        const headers = { 'Content-Type': 'application/json' };
        if (token) headers['Authorization'] = `Bearer ${token}`;

        const res = await fetch(`${baseUrl}/v1/sessions/auth-tool`, {
          method: 'POST',
          headers,
          body: JSON.stringify({
            session_id: sessionId,
            target: authTarget
          })
        });

        if (!res.ok) {
          const errText = await res.text();
          throw new Error(errText || 'Failed to start auth process');
        }

        const data = await res.json();
        activeFlowId = data.flow_id;
        activeAuthUrl = data.auth_url;

        if (isSafeHTTPSUrl(activeAuthUrl)) {
          window.open(activeAuthUrl, '_blank', 'noopener,noreferrer');
          if (launchStatus) launchStatus.textContent = 'Sign-in page opened in new tab.';
        } else {
          if (launchStatus) launchStatus.textContent = 'Invalid auth URL returned.';
        }

        btnLaunch.textContent = '🔗 Open Sign-In Link Again';
        btnLaunch.disabled = false;
        if (callbackContainer) callbackContainer.style.display = 'block';
      } catch (err) {
        btnLaunch.disabled = false;
        if (launchStatus) {
          launchStatus.textContent = `Error: ${err.message}`;
          launchStatus.style.color = 'var(--text-danger, #ef4444)';
        }
      }
    });
  }

  if (btnComplete) {
    btnComplete.addEventListener('click', async () => {
      const codeOrUrl = (callbackInput?.value || '').trim();
      if (!codeOrUrl) return;
      btnComplete.disabled = true;
      if (callbackStatus) {
        callbackStatus.style.display = 'block';
        callbackStatus.textContent = 'Verifying sign-in...';
      }
      try {
        const sessionId = tabObj.session?.id || '';
        const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
        const token = getAuthToken ? getAuthToken() : '';
        const headers = { 'Content-Type': 'application/json' };
        if (token) headers['Authorization'] = `Bearer ${token}`;

        const res = await fetch(`${baseUrl}/v1/sessions/auth-callback`, {
          method: 'POST',
          headers,
          body: JSON.stringify({
            session_id: sessionId,
            flow_id: activeFlowId,
            callback_url: codeOrUrl
          })
        });

        if (!res.ok) {
          const errText = await res.text();
          throw new Error(errText || 'Verification failed');
        }

        const data = await res.json();
        if (data.completed) {
          card.classList.add('answered');
          const actionsEl = card.querySelector('.interactive-card-actions');
          if (actionsEl) {
            actionsEl.innerHTML = `<span class="interactive-answered-badge">✓ Authentication Complete</span>`;
          }
          if (callbackContainer) callbackContainer.style.display = 'none';
        } else {
          if (callbackStatus) callbackStatus.textContent = 'Authentication pending, finishing token exchange...';
        }
      } catch (err) {
        btnComplete.disabled = false;
        if (callbackStatus) {
          callbackStatus.textContent = `Error: ${err.message}`;
          callbackStatus.style.color = 'var(--text-danger, #ef4444)';
        }
      }
    });
  }

  const btnRetry = card.querySelector('.btn-auth-retry');
  if (btnRetry) {
    btnRetry.addEventListener('click', async () => {
      btnRetry.disabled = true;
      card.classList.add('answered');
      const actionsEl = card.querySelector('.interactive-card-actions');
      if (actionsEl) {
        actionsEl.innerHTML = `<span class="interactive-answered-badge">↻ Retrying operation...</span>`;
      }
      await submitSessionAuthResponse(tabObj, 'retry', 'retry');
    });
  }

  const btnTerminal = card.querySelector('.btn-auth-terminal');
  if (btnTerminal) {
    btnTerminal.addEventListener('click', () => {
      card.remove();
      const sessionId = tabObj.session?.id || '';
      if (sessionId) {
        handleTakeWheel(sessionId);
      }
    });
  }

  const btnDismiss = card.querySelector('.btn-auth-dismiss');
  if (btnDismiss) {
    btnDismiss.addEventListener('click', async () => {
      btnDismiss.disabled = true;
      card.classList.add('answered');
      const actionsEl = card.querySelector('.interactive-card-actions');
      if (actionsEl) {
        actionsEl.innerHTML = `<span class="interactive-answered-badge">Dismissed</span>`;
      }
      await submitSessionAuthResponse(tabObj, 'dismiss', 'dismiss');
    });
  }

  tabObj.chatMessagesEl.appendChild(card);
  tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
}

export async function mountMCPStatus(container, sessionId, tabObj) {
  if (!container || !sessionId) return;
  container.innerHTML = `
    <div class="mcp-status-header">
      <span class="mcp-status-title">🔌 MCP & Plugins</span>
      <button type="button" class="btn-refresh-mcp-status" title="Refresh">↻</button>
    </div>
    <div class="mcp-status-body">
      <div class="mcp-status-loading">Loading status...</div>
    </div>
  `;

  const bodyEl = container.querySelector('.mcp-status-body');
  const btnRefresh = container.querySelector('.btn-refresh-mcp-status');

  const loadStatus = async (bypassCache = false) => {
    try {
      const baseUrl = getSessionBaseUrl(sessionId, tabObj?.session);
      const token = getAuthToken ? getAuthToken() : '';
      const headers = {};
      if (token) headers['Authorization'] = `Bearer ${token}`;
      const url = `${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/mcp-status${bypassCache ? '?refresh=1' : ''}`;
      const res = await fetch(url, { headers });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      renderMCPStatusContent(bodyEl, data, sessionId, tabObj, loadStatus);
    } catch (err) {
      if (bodyEl) {
        bodyEl.innerHTML = `<div class="mcp-status-error">Failed to load MCP status: ${escapeHtml(err.message)}</div>`;
      }
    }
  };

  if (btnRefresh) {
    btnRefresh.addEventListener('click', () => loadStatus(true));
  }
  return loadStatus(false);
}

function renderMCPStatusContent(container, data, sessionId, tabObj, reloadFn) {
  if (!container) return;
  const servers = Array.isArray(data?.servers) ? data.servers : [];
  const plugins = Array.isArray(data?.plugins) ? data.plugins : [];

  if (servers.length === 0 && plugins.length === 0) {
    container.innerHTML = `<div class="mcp-status-empty">No MCP servers or plugins configured.</div>`;
    return;
  }

  let html = '';
  if (servers.length > 0) {
    html += `<div class="mcp-status-group"><div class="mcp-group-label">MCP Servers</div>`;
    for (const s of servers) {
      const isNeedsAuth = s.status === 'needs_auth';
      html += `
        <div class="mcp-pill-row" data-target="${escapeHtml(s.target || '')}">
          <span class="mcp-pill-name">${escapeHtml(s.name)}</span>
          <span class="mcp-pill-badge transport-${escapeHtml(s.transport)}">${escapeHtml(s.transport)}</span>
          <span class="mcp-pill-status ${isNeedsAuth ? 'status-warn' : 'status-ok'}">${isNeedsAuth ? 'Needs Auth' : 'Configured'}</span>
          ${s.auth_supported ? `<button type="button" class="btn btn-xs btn-mcp-signin">Sign in</button>` : ''}
        </div>
      `;
    }
    html += `</div>`;
  }

  if (plugins.length > 0) {
    html += `<div class="mcp-status-group"><div class="mcp-group-label">Workspace Plugins</div>`;
    for (const p of plugins) {
      if (!p.installed) continue;
      const isAuth = !!p.authenticated;
      html += `
        <div class="mcp-pill-row" data-target="${escapeHtml(p.target || '')}">
          <span class="mcp-pill-name">${escapeHtml(p.name)}</span>
          <span class="mcp-pill-status ${isAuth ? 'status-ok' : 'status-warn'}">${isAuth ? '✓ Signed in' : 'Needs auth'}</span>
          ${!isAuth ? `<button type="button" class="btn btn-xs btn-plugin-signin">Sign in</button>` : ''}
        </div>
      `;
    }
    html += `</div>`;
  }

  container.innerHTML = html;

  const signinButtons = container.querySelectorAll('.btn-mcp-signin, .btn-plugin-signin');
  for (const btn of signinButtons) {
    btn.addEventListener('click', async () => {
      const row = btn.closest('.mcp-pill-row');
      const target = row?.dataset?.target;
      if (!target) return;
      btn.disabled = true;
      btn.textContent = 'Starting...';

      try {
        const baseUrl = getSessionBaseUrl(sessionId, tabObj?.session);
        const token = getAuthToken ? getAuthToken() : '';
        const headers = { 'Content-Type': 'application/json' };
        if (token) headers['Authorization'] = `Bearer ${token}`;

        const res = await fetch(`${baseUrl}/v1/sessions/auth-tool`, {
          method: 'POST',
          headers,
          body: JSON.stringify({ session_id: sessionId, target })
        });
        if (!res.ok) throw new Error(await res.text());
        const flowData = await res.json();

        if (isSafeHTTPSUrl(flowData.auth_url)) {
          window.open(flowData.auth_url, '_blank', 'noopener,noreferrer');
        }

        let inlineRow = row.nextElementSibling;
        if (!inlineRow || !inlineRow.classList.contains('mcp-inline-callback-row')) {
          inlineRow = document.createElement('div');
          inlineRow.className = 'mcp-inline-callback-row';
          row.after(inlineRow);
        }

        inlineRow.innerHTML = `
          <input type="text" class="mcp-inline-input" placeholder="Paste callback URL or code..." />
          <button type="button" class="btn btn-xs btn-mcp-submit-cb">Submit</button>
        `;

        const subBtn = inlineRow.querySelector('.btn-mcp-submit-cb');
        const inInput = inlineRow.querySelector('.mcp-inline-input');

        subBtn?.addEventListener('click', async () => {
          const val = (inInput?.value || '').trim();
          if (!val) return;
          subBtn.disabled = true;
          subBtn.textContent = '...';
          try {
            const cbRes = await fetch(`${baseUrl}/v1/sessions/auth-callback`, {
              method: 'POST',
              headers,
              body: JSON.stringify({
                session_id: sessionId,
                flow_id: flowData.flow_id,
                callback_url: val
              })
            });
            if (cbRes.ok) {
              inlineRow.remove();
              btn.textContent = '✓ Done';
              if (reloadFn) reloadFn(true);
            } else {
              subBtn.disabled = false;
              subBtn.textContent = 'Retry';
            }
          } catch {
            subBtn.disabled = false;
            subBtn.textContent = 'Retry';
          }
        });

        btn.textContent = 'Re-open';
        btn.disabled = false;
      } catch (err) {
        btn.textContent = 'Failed';
        btn.disabled = false;
      }
    });
  }
}
