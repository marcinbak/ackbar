// Server-Sent Events Multi-Host Subscriber
import { state } from './state.js';
import { getAuthToken } from './auth.js';
import {
  fetchSessions,
  fetchHosts,
  fetchTreeNodes
} from './api.js';
import { renderTree } from './tree.js';
import { checkAndReconnectActiveTabs } from './tabs.js';

const activeEventSources = new Map();

function connectSSE() {
  const hostsToConnect = [
    { name: getSelfHostName(), url: '' },
    ...(state.hosts || []).filter(h => h.url && !isLocalHost(h.name))
  ];

  hostsToConnect.forEach((h) => {
    let sseUrl = h.url ? `${h.url.replace(/\/$/, '')}/v1/events` : '/v1/events';
    const token = getAuthToken();
    if (token) {
      sseUrl += (sseUrl.includes('?') ? '&' : '?') + `token=${encodeURIComponent(token)}`;
    }
    if (activeEventSources.has(h.name)) return;

    try {
      const sse = new EventSource(sseUrl);
      activeEventSources.set(h.name, sse);

      sse.onmessage = (event) => {
        try {
          const updatedSess = JSON.parse(event.data);
          if (!updatedSess || !updatedSess.id) return;

          if (!isLocalHost(h.name)) {
            updatedSess.host = h.name;
            updatedSess.hostUrl = h.url || '';
          } else if (!updatedSess.host || updatedSess.host === 'local') {
            updatedSess.host = getSelfHostName();
          }

          if (updatedSess.deleted || updatedSess.activity === 'Deleted') {
            const targetId = updatedSess.id;
            const targetName = updatedSess.name;
            const nativeId = updatedSess.native_id;
            state.sessions = state.sessions.filter(s => s.id !== targetId && s.name !== targetName && (!nativeId || s.native_id !== nativeId));
            closeTab(targetId);
            if (nativeId) closeTab(nativeId);
            closeTab(`details_${targetId}`);
            if (nativeId) closeTab(`details_${nativeId}`);
            for (const [tId, tabObj] of state.openTabs.entries()) {
              if (tabObj.session && (tabObj.session.id === targetId || (nativeId && tabObj.session.native_id === nativeId))) {
                closeTab(tId);
              }
            }
            renderTree();
            return;
          }

          const idx = state.sessions.findIndex(s => s.id === updatedSess.id);
          if (idx !== -1) {
            const existing = state.sessions[idx];
            const mergedName = (!isRawSessionName(updatedSess.name) || isRawSessionName(existing.name)) ? (updatedSess.name || existing.name) : existing.name;
            state.sessions[idx] = { ...existing, ...updatedSess, name: mergedName };
          } else {
            state.sessions.push(updatedSess);
          }

          state.sessions = deduplicateSessions(state.sessions);
          renderTree();
          updateOpenTabsState();

          // Sync running subagents for open chat tabs
          if (typeof updatedSess.running_subagents === 'number') {
            for (const [, tabObj] of state.openTabs.entries()) {
              if (tabObj && tabObj.session && (tabObj.session.id === updatedSess.id || tabObj.session.native_id === updatedSess.native_id)) {
                if (updatedSess.running_subagents === 0) {
                  tabObj.runningSubagents = [];
                  renderSubagentsBar(tabObj);
                } else if (!tabObj.runningSubagents || tabObj.runningSubagents.length !== updatedSess.running_subagents) {
                  fetchRunningSubagents(tabObj);
                }
              }
            }
          }
        } catch (e) {
          console.error(`[SSE ${h.name}] parse error:`, e);
        }
      };

      sse.onerror = () => {
        try { sse.close(); } catch (e) {}
        activeEventSources.delete(h.name);
      };
    } catch (e) {
      console.warn(`[SSE ${h.name}] failed to connect:`, e);
    }
  });
}

// Render Sidebar Tree Hierarchy with Recursive Group Nesting & Drag-and-Drop

export {
  activeEventSources,
  connectSSE
};
