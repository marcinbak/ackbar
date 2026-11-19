// Ephemeral In-Stream Activity Indicator Lifecycle & Timers
import { escapeHtml } from './utils.js';
import { scrollChatToBottom } from './chat.js';

export function resolveToolDisplay(toolName, toolInput) {
  const rawName = String(toolName || '').trim();
  let params = toolInput;
  if (typeof params === 'string') {
    const trimmed = params.trim();
    if ((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
      try {
        params = JSON.parse(trimmed);
      } catch (_) {}
    }
  }
  const isObject = params !== null && typeof params === 'object' && !Array.isArray(params);
  const inputMap = isObject ? params : {};

  if (rawName.toLowerCase() === 'call_mcp_tool') {
    const server = String(inputMap.ServerName || inputMap.server_name || inputMap.server || '').trim();
    const tool = String(inputMap.ToolName || inputMap.tool_name || inputMap.tool || '').trim();
    let name = 'call_mcp_tool';
    if (server && tool) {
      name = `mcp/${server}/${tool}`;
    } else if (tool) {
      name = `mcp/${tool}`;
    } else if (server) {
      name = `mcp/${server}`;
    }

    const rawArgs = inputMap.Arguments !== undefined
      ? inputMap.Arguments
      : (inputMap.arguments !== undefined ? inputMap.arguments : inputMap.args);

    let detail = '';
    if (rawArgs !== null && rawArgs !== undefined) {
      if (typeof rawArgs === 'object' && !Array.isArray(rawArgs)) {
        if (Object.keys(rawArgs).length > 0) {
          detail = JSON.stringify(rawArgs);
        }
      } else if (typeof rawArgs === 'string') {
        const trimmedArgs = rawArgs.trim();
        if (trimmedArgs && trimmedArgs !== '{}') {
          detail = trimmedArgs;
        }
      } else {
        detail = String(rawArgs);
      }
    }
    return { name, detail, isMcp: true, mcpServer: server, mcpTool: tool };
  }

  if (rawName.startsWith('mcp__')) {
    const parts = rawName.split('__');
    if (parts.length >= 3 && parts[1] && parts[2]) {
      const server = parts[1];
      const tool = parts.slice(2).join('__');
      const name = `mcp/${server}/${tool}`;
      const detail = (isObject && Object.keys(inputMap).length > 0) ? JSON.stringify(inputMap) : '';
      return { name, detail, isMcp: true, mcpServer: server, mcpTool: tool };
    }
  }

  if (rawName.startsWith('mcp_')) {
    const parts = rawName.split('_');
    if (parts.length >= 3 && parts[1] && parts[2]) {
      const server = parts[1];
      const tool = parts.slice(2).join('_');
      const name = `mcp/${server}/${tool}`;
      const detail = (isObject && Object.keys(inputMap).length > 0) ? JSON.stringify(inputMap) : '';
      return { name, detail, isMcp: true, mcpServer: server, mcpTool: tool };
    }
  }

  const name = rawName || 'tool';
  let detail = '';
  if (typeof toolInput === 'string') {
    detail = toolInput.trim();
  } else if (isObject && Object.keys(inputMap).length > 0) {
    detail = JSON.stringify(inputMap, null, 2);
  }
  return { name, detail, isMcp: false, mcpServer: '', mcpTool: '' };
}

export function formatToolActivity(toolName, toolInput) {
  const resolved = resolveToolDisplay(toolName, toolInput);
  if (resolved.isMcp) {
    const text = resolved.mcpTool
      ? (resolved.mcpServer ? `Calling MCP ${resolved.mcpTool} (${resolved.mcpServer})...` : `Calling MCP ${resolved.mcpTool}...`)
      : `Running ${resolved.name}...`;
    return { icon: '🔌', text, badge: resolved.name };
  }

  let params = toolInput;
  if (typeof params === 'string') {
    try {
      params = JSON.parse(params);
    } catch (_) {}
  }
  params = (params && typeof params === 'object') ? params : {};

  const tName = (toolName || '').toLowerCase();
  let icon = '⚙️';
  let text = `Running ${toolName || 'tool'}...`;
  let badge = toolName || null;

  if (tName === 'view_file' || tName === 'view' || tName === 'read_file') {
    icon = '📖';
    const p = params.AbsolutePath || params.file_path || params.path || params.TargetFile || '';
    const filename = p ? p.split('/').pop() : '';
    text = filename ? `Reading ${filename}` : 'Reading file...';
  } else if (tName === 'replace_file_content' || tName === 'write_to_file' || tName === 'edit') {
    icon = '✏️';
    const p = params.TargetFile || params.file_path || params.path || '';
    const filename = p ? p.split('/').pop() : '';
    text = filename ? `Editing ${filename}` : 'Editing file...';
  } else if (tName === 'run_command' || tName === 'bash') {
    icon = '⚡';
    const cmd = (params.CommandLine || params.command || '').trim();
    if (cmd) {
      const firstLine = cmd.split('\n')[0];
      text = `Running: ${firstLine.length > 50 ? firstLine.slice(0, 47) + '...' : firstLine}`;
    } else {
      text = 'Running command...';
    }
  } else if (tName === 'grep_search' || tName === 'grep') {
    icon = '🔍';
    const q = params.Query || params.pattern || '';
    text = q ? `Searching for "${q}"` : 'Searching codebase...';
  } else if (tName === 'find_by_name' || tName === 'glob') {
    icon = '🔍';
    const pat = params.Pattern || params.pattern || '';
    text = pat ? `Finding "${pat}"` : 'Finding files...';
  } else if (tName === 'invoke_subagent' || tName === 'agent' || tName === 'task') {
    icon = '🤖';
    text = 'Delegating to subagent...';
  } else if (tName === 'read_url_content' || tName === 'search_web') {
    icon = '🌐';
    const u = params.Url || params.query || '';
    text = u ? `Fetching ${u}` : 'Searching web...';
  }

  return { icon, text, badge };
}

export function parseActivityTimestampMs(value) {
  if (value === null || value === undefined || value === '') return NaN;
  let ms = NaN;
  if (typeof value === 'number') {
    ms = Number.isFinite(value) ? value : NaN;
  } else if (typeof value === 'string') {
    const parsed = Date.parse(value);
    ms = Number.isFinite(parsed) ? parsed : NaN;
  } else if (value instanceof Date) {
    ms = value.getTime();
  }
  if (isNaN(ms) || ms <= 0) return NaN;
  if (new Date(ms).getUTCFullYear() <= 1970) return NaN;
  return Math.min(Date.now(), ms);
}

export function resolveFallbackActivityStartTime(tabObj) {
  if (!tabObj) return NaN;
  const candidates = [
    tabObj.daemonActivityStartedAt,
    tabObj.inFlightPrompt ? tabObj.inFlightPrompt.timestamp : null,
    (tabObj.session && (tabObj.session.state === 1 || tabObj.session.state === 'working')) ? tabObj.session.last_event_at : null
  ];
  for (const cand of candidates) {
    const parsed = parseActivityTimestampMs(cand);
    if (!isNaN(parsed)) return parsed;
  }
  return NaN;
}

export function deduplicateInStreamActivities(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return null;
  const all = tabObj.chatMessagesEl.querySelectorAll('.chat-instream-activity');
  if (all.length === 0) {
    if (tabObj.activeInStreamActivityEl && !tabObj.activeInStreamActivityEl.isConnected) {
      tabObj.activeInStreamActivityEl = null;
    }
    return null;
  }
  const kept = (tabObj.activeInStreamActivityEl && tabObj.activeInStreamActivityEl.isConnected)
    ? tabObj.activeInStreamActivityEl
    : all[all.length - 1];
  for (const el of all) {
    if (el !== kept) {
      el.remove();
    }
  }
  tabObj.activeInStreamActivityEl = kept;
  return kept;
}

export function startActivityInterval(tabObj) {
  if (!tabObj) return;
  if (tabObj.activeActivityTimer) {
    clearInterval(tabObj.activeActivityTimer);
    tabObj.activeActivityTimer = null;
  }
  tabObj.activeActivityTimer = setInterval(() => {
    if (!tabObj.activeInStreamActivityEl || !tabObj.activeInStreamActivityEl.isConnected) {
      if (tabObj.activeActivityTimer) {
        clearInterval(tabObj.activeActivityTimer);
        tabObj.activeActivityTimer = null;
      }
      return;
    }
    const timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');
    if (timerEl) {
      const sec = Math.max(0, Math.floor((Date.now() - (tabObj.activeActivityStartTime || Date.now())) / 1000));
      timerEl.textContent = `(${sec}s)`;
    }
  }, 1000);
}

export function showInStreamActivity(tabObj, { icon, text, badge, phaseKey, startedAt } = {}, onUpdate) {
  if (!tabObj || !tabObj.chatMessagesEl) return;

  deduplicateInStreamActivities(tabObj);

  const resolvedPhaseKey = phaseKey || `${icon || ''}:${text || ''}:${badge || ''}`;
  const explicitStart = parseActivityTimestampMs(startedAt);

  if (tabObj.activeActivityPhaseKey === resolvedPhaseKey && typeof tabObj.activeActivityStartTime === 'number' && tabObj.activeActivityStartTime > 0) {
    if (tabObj.activeActivityStartTimeSource === 'client_fallback' && !isNaN(explicitStart)) {
      tabObj.activeActivityStartTime = explicitStart;
      tabObj.activeActivityStartTimeSource = 'daemon';
    }
  } else {
    tabObj.activeActivityPhaseKey = resolvedPhaseKey;
    if (!isNaN(explicitStart)) {
      tabObj.activeActivityStartTime = explicitStart;
      tabObj.activeActivityStartTimeSource = 'daemon';
    } else if (resolvedPhaseKey === 'thinking') {
      const fallback = resolveFallbackActivityStartTime(tabObj);
      if (!isNaN(fallback)) {
        tabObj.activeActivityStartTime = fallback;
        tabObj.activeActivityStartTimeSource = 'daemon';
      } else {
        tabObj.activeActivityStartTime = Date.now();
        tabObj.activeActivityStartTimeSource = 'client_fallback';
      }
    } else {
      tabObj.activeActivityStartTime = Date.now();
      tabObj.activeActivityStartTimeSource = 'client_fallback';
    }
  }

  const initialSec = Math.max(0, Math.floor((Date.now() - tabObj.activeActivityStartTime) / 1000));

  if (!tabObj.activeInStreamActivityEl || !tabObj.activeInStreamActivityEl.isConnected) {
    const el = document.createElement('div');
    el.className = 'chat-instream-activity';
    el.innerHTML = `
      <span class="chat-activity-spinner"></span>
      <span class="chat-activity-icon">${escapeHtml(icon || '⚡')}</span>
      <span class="chat-activity-text">${escapeHtml(text || 'Working...')}</span>
      <span class="chat-activity-badge" style="${badge ? '' : 'display: none;'}">${escapeHtml(badge || '')}</span>
      <span class="chat-activity-timer">(${initialSec}s)</span>
    `;
    tabObj.chatMessagesEl.appendChild(el);
    tabObj.activeInStreamActivityEl = el;
  } else {
    const iconEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-icon');
    const textEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-text');
    const badgeEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-badge');
    const timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');

    if (iconEl) iconEl.textContent = icon || '⚡';
    if (textEl) textEl.textContent = text || 'Working...';
    if (badgeEl) {
      badgeEl.textContent = badge || '';
      badgeEl.style.display = badge ? 'inline-block' : 'none';
    }
    if (timerEl) {
      timerEl.textContent = `(${initialSec}s)`;
    }
    if (tabObj.chatMessagesEl.lastElementChild !== tabObj.activeInStreamActivityEl) {
      tabObj.chatMessagesEl.appendChild(tabObj.activeInStreamActivityEl);
    }
  }

  startActivityInterval(tabObj);

  if (typeof onUpdate === 'function') {
    onUpdate(tabObj);
  }
  scrollChatToBottom(tabObj);
}

export function pauseInStreamActivity(tabObj) {
  if (!tabObj) return;
  if (tabObj.activeActivityTimer) {
    clearInterval(tabObj.activeActivityTimer);
    tabObj.activeActivityTimer = null;
  }
}

export function resumeInStreamActivity(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return;
  deduplicateInStreamActivities(tabObj);
  if (tabObj.activeInStreamActivityEl && tabObj.activeInStreamActivityEl.isConnected && tabObj.activeActivityStartTime > 0) {
    if (tabObj.activeActivityPhaseKey === 'thinking' && tabObj.activeActivityStartTimeSource === 'client_fallback') {
      const fallback = resolveFallbackActivityStartTime(tabObj);
      if (!isNaN(fallback)) {
        tabObj.activeActivityStartTime = fallback;
        tabObj.activeActivityStartTimeSource = 'daemon';
      }
    }
    const timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');
    if (timerEl) {
      const sec = Math.max(0, Math.floor((Date.now() - tabObj.activeActivityStartTime) / 1000));
      timerEl.textContent = `(${sec}s)`;
    }
    startActivityInterval(tabObj);
  }
}

export function hideInStreamActivity(tabObj, onUpdate, { preserveTimerState = false } = {}) {
  if (!tabObj) return;
  if (tabObj.activeActivityTimer) {
    clearInterval(tabObj.activeActivityTimer);
    tabObj.activeActivityTimer = null;
  }
  if (tabObj.activeInStreamActivityEl) {
    tabObj.activeInStreamActivityEl.remove();
    tabObj.activeInStreamActivityEl = null;
  }
  if (tabObj.chatMessagesEl && typeof tabObj.chatMessagesEl.querySelectorAll === 'function') {
    const strays = tabObj.chatMessagesEl.querySelectorAll('.chat-instream-activity');
    for (const s of strays) {
      s.remove();
    }
  }
  if (!preserveTimerState) {
    tabObj.activeActivityPhaseKey = null;
    tabObj.activeActivityStartTime = 0;
    tabObj.activeActivityStartTimeSource = null;
  }
  if (typeof onUpdate === 'function') {
    onUpdate(tabObj);
  }
}
