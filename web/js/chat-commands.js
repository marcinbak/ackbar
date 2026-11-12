import { state, clearChatDraft } from './state.js';
import { getAuthToken } from './auth.js';
import { getSessionBaseUrl } from './utils.js';
import { migrateTabId } from './tabs.js';
import { renderTree } from './tree.js';
import { connectChatStream, disconnectChatStream, loadChatTranscript } from './chat.js';
import { mountMCPStatus } from './chat-auth.js';
import {
  appendEphemeralCard,
  renderBashCard,
  renderDiffCard,
  renderRewindCard,
  renderConfigNoticeCard,
  renderInspectCard
} from './chat-command-cards.js';

async function fetchSessionJSON(url, options = {}) {
  const token = getAuthToken();
  const headers = Object.assign({}, options.headers || {});
  if (token && !headers['Authorization']) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  const res = await fetch(url, Object.assign({}, options, { headers }));
  if (!res.ok) {
    const errText = (await res.text()).trim();
    throw new Error(errText || `HTTP ${res.status}`);
  }
  return await res.json();
}

export function updateShellModeHint(tabObj) {
  if (!tabObj || !tabObj.chatComposerBox) return;
  const isShell = tabObj.chatInputEl && tabObj.chatInputEl.value.trim().startsWith('!');
  let hintEl = tabObj.chatComposerBox.querySelector('.chat-input-shell-mode');
  if (isShell) {
    if (!hintEl) {
      hintEl = document.createElement('div');
      hintEl.className = 'chat-input-shell-mode';
      hintEl.textContent = 'SHELL · 0 tokens';
      tabObj.chatComposerBox.insertBefore(hintEl, tabObj.chatComposerBox.firstChild);
    }
    hintEl.style.display = 'inline-flex';
  } else if (hintEl) {
    hintEl.style.display = 'none';
  }
}

export async function rotateSessionTabInPlace(tabObj, oldId, newSess, noticeText) {
  if (!tabObj || !newSess) return;

  // Closing active SSE prevents reconnect attempts on obsolete session ID during tab migration.
  if (tabObj.chatEventSource) {
    try {
      tabObj.chatEventSource.close();
    } catch (_) {}
    tabObj.chatEventSource = null;
    tabObj.chatStreamSessionId = null;
  }
  disconnectChatStream(tabObj);

  if (tabObj.session && tabObj.session.hostUrl) {
    newSess.hostUrl = tabObj.session.hostUrl;
  }
  if (tabObj.session && tabObj.session.host && !newSess.host) {
    newSess.host = tabObj.session.host;
  }

  if (Array.isArray(state.sessions)) {
    const oldIdx = state.sessions.findIndex(s => s.id === oldId || s.native_id === oldId);
    if (oldIdx !== -1) {
      state.sessions[oldIdx].is_done = true;
      state.sessions[oldIdx].state = 'ended';
    }
    const newIdx = state.sessions.findIndex(s => s.id === newSess.id || s.native_id === newSess.id);
    if (newIdx !== -1) {
      state.sessions[newIdx] = newSess;
    } else {
      state.sessions.unshift(newSess);
    }
  }

  tabObj.ephemeralCards = [];
  if (tabObj.chatMessagesEl) {
    tabObj.chatMessagesEl.innerHTML = '';
  }

  migrateTabId(oldId, newSess.id, newSess);
  connectChatStream(tabObj);
  await loadChatTranscript(tabObj);

  try {
    renderTree();
  } catch (_) {}

  if (noticeText) {
    renderConfigNoticeCard(tabObj, 'Conversation Rotated', noticeText);
  }
}

export async function interceptChatInput(tabObj, rawText) {
  if (!tabObj || !tabObj.session) return false;
  const text = (rawText || '').trim();
  if (!text) return false;

  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);

  if (text.startsWith('!')) {
    const cmd = text.slice(1).trim();
    if (!cmd) return false;

    tabObj.chatInputEl.value = '';
    clearChatDraft(tabObj.id || sessionId);
    tabObj.chatInputEl.style.height = 'auto';
    updateShellModeHint(tabObj);

    try {
      const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/shell`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd })
      });
      renderBashCard(tabObj, data);
    } catch (err) {
      renderConfigNoticeCard(tabObj, 'Shell Execution Failed', err.message);
    }
    return true;
  }

  if (text.startsWith('/')) {
    const parts = text.split(/\s+/);
    const cmdName = parts[0].slice(1).toLowerCase();
    const args = text.slice(parts[0].length).trim();

    const recognizedCommands = new Set([
      'clear', 'reset', 'restart', 'compact', 'handover', 'model', 'effort',
      'permissions', 'auto', 'add-dir', 'mcp', 'plugins', 'agents',
      'doctor', 'context', 'cost', 'diff', 'rewind'
    ]);

    if (!recognizedCommands.has(cmdName)) {
      return false; // Workspace / custom skills pass through to prompt turn
    }

    tabObj.chatInputEl.value = '';
    clearChatDraft(tabObj.id || sessionId);
    tabObj.chatInputEl.style.height = 'auto';

    switch (cmdName) {
      case 'clear':
      case 'reset':
        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/clear`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
          });
          if (data && data.session) {
            await rotateSessionTabInPlace(tabObj, sessionId, data.session, 'Cleared conversation context into fresh turn.');
          }
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Clear Failed', err.message);
        }
        return true;

      case 'restart':
        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/control?id=${encodeURIComponent(sessionId)}&action=restart`, {
            method: 'POST'
          });
          if (data && data.session) {
            if (tabObj.session && tabObj.session.hostUrl && !data.session.hostUrl) {
              data.session.hostUrl = tabObj.session.hostUrl;
            }
            tabObj.session = Object.assign(tabObj.session || {}, data.session);
            if (Array.isArray(state.sessions)) {
              const idx = state.sessions.findIndex(s => s.id === sessionId);
              if (idx !== -1) {
                state.sessions[idx] = Object.assign(state.sessions[idx], data.session);
              }
            }
            try { renderTree(); } catch (_) {}
          }
          renderConfigNoticeCard(
            tabObj,
            'Session Restarted',
            'CLI session restarted in-place; MCP connectors and configuration reloaded.'
          );
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Restart Failed', err.message);
        }
        return true;

      case 'compact':
        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/compact`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ instructions: args })
          });
          if (data && data.mode === 'rotated_session' && data.session) {
            await rotateSessionTabInPlace(tabObj, sessionId, data.session, 'Compacted context into fresh conversation.');
          } else if (data && data.mode === 'native_stream') {
            connectChatStream(tabObj);
          }
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Compact Failed', err.message);
        }
        return true;

      case 'handover':
        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/handover`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: sessionId, strategy: 'in_place', custom_instruction: args })
          });
          if (data && data.session) {
            await rotateSessionTabInPlace(tabObj, sessionId, data.session, 'Handover briefing seeded into fresh conversation.');
          }
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Handover Failed', err.message);
        }
        return true;

      case 'model':
        if (!args) {
          renderConfigNoticeCard(tabObj, 'Current Model', tabObj.session.model || 'Default');
          return true;
        }
        try {
          const updated = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/config`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ model: args })
          });
          tabObj.session.model = updated.model;
          renderConfigNoticeCard(tabObj, 'Model Updated', `Model set to: ${updated.model || 'Default'}`);
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Model Update Failed', err.message);
        }
        return true;

      case 'effort':
        if ((tabObj.session.agent || '').toLowerCase() === 'antigravity') {
          renderConfigNoticeCard(tabObj, 'Reasoning Effort', 'Antigravity models encode reasoning effort in model tiers. Use /model to switch tiers.');
          return true;
        }
        if (!args) {
          renderConfigNoticeCard(tabObj, 'Current Reasoning Effort', tabObj.session.effort || 'Default');
          return true;
        }
        try {
          const updated = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/config`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ effort: args })
          });
          tabObj.session.effort = updated.effort;
          renderConfigNoticeCard(tabObj, 'Effort Updated', `Reasoning effort set to: ${updated.effort || 'Default'}`);
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Effort Update Failed', err.message);
        }
        return true;

      case 'permissions':
      case 'auto':
        {
          const mode = (cmdName === 'auto') ? 'auto' : (args || '');
          if (!mode) {
            renderConfigNoticeCard(tabObj, 'Current Permission Mode', tabObj.session.permission_mode || 'bypassPermissions');
            return true;
          }
          try {
            const updated = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/config`, {
              method: 'PATCH',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ permission_mode: mode })
            });
            tabObj.session.permission_mode = updated.permission_mode;
            renderConfigNoticeCard(tabObj, 'Permission Mode Updated', `Permission mode set to: ${updated.permission_mode}`);
          } catch (err) {
            renderConfigNoticeCard(tabObj, 'Permission Update Failed', err.message);
          }
          return true;
        }

      case 'add-dir':
        if (!args) {
          const dirs = Array.isArray(tabObj.session.extra_dirs) ? tabObj.session.extra_dirs : [];
          renderConfigNoticeCard(tabObj, 'Extra Directories', dirs.length > 0 ? dirs.join('\n') : 'No extra directories allowed.');
          return true;
        }
        try {
          let payload = {};
          if (args === '--clear') {
            payload = { extra_dirs: [] };
          } else if (args.startsWith('--remove ')) {
            payload = { remove_dirs: [args.slice(9).trim()] };
          } else {
            payload = { add_dirs: [args] };
          }
          const updated = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/config`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
          });
          tabObj.session.extra_dirs = updated.extra_dirs;
          const currentDirs = Array.isArray(updated.extra_dirs) ? updated.extra_dirs : [];
          renderConfigNoticeCard(tabObj, 'Extra Directories Updated', currentDirs.length > 0 ? currentDirs.join('\n') : 'Extra directories cleared.');
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Add-Dir Failed', err.message);
        }
        return true;

      case 'mcp': {
        const card = document.createElement('div');
        card.className = 'chat-card chat-inspect-card chat-inspect-mcp';
        appendEphemeralCard(tabObj, card);
        await mountMCPStatus(card, sessionId, tabObj);
        return true;
      }

      case 'plugins':
      case 'agents':
      case 'doctor':
      case 'context':
      case 'cost':
        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/inspect?kind=${encodeURIComponent(cmdName)}`);
          renderInspectCard(tabObj, cmdName, data);
        } catch (err) {
          renderConfigNoticeCard(tabObj, `${cmdName} Failed`, err.message);
        }
        return true;

      case 'diff':
        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/diff`);
          renderDiffCard(tabObj, data);
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Diff Failed', err.message);
        }
        return true;

      case 'rewind':
        if (args && /^\d+$/.test(args)) {
          const turnNum = parseInt(args, 10);
          try {
            const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/rewind`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ before_turn: turnNum })
            });
            if (data && data.session) {
              await rotateSessionTabInPlace(tabObj, sessionId, data.session, `Rewound conversation before Turn ${turnNum}.`);
            }
          } catch (err) {
            renderConfigNoticeCard(tabObj, 'Rewind Failed', err.message);
          }
          return true;
        }

        try {
          const data = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/rewind`);
          renderRewindCard(tabObj, data, async (selectedTurn) => {
            try {
              const postData = await fetchSessionJSON(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/rewind`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ before_turn: selectedTurn })
              });
              if (postData && postData.session) {
                await rotateSessionTabInPlace(tabObj, sessionId, postData.session, `Rewound conversation before Turn ${selectedTurn}.`);
              }
            } catch (postErr) {
              renderConfigNoticeCard(tabObj, 'Rewind Failed', postErr.message);
            }
          });
        } catch (err) {
          renderConfigNoticeCard(tabObj, 'Rewind Failed', err.message);
        }
        return true;
    }
  }

  return false;
}
