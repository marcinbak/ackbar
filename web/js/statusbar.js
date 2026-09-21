// Status Bar Controller
import { el } from './state.js';
import {
  getStateEmoji,
  getStateText,
  formatRelativeTime
} from './utils.js';

function updateStatusbar(session) {
  if (!session) return;
  if (el.sbSessionName) el.sbSessionName.textContent = session.name || session.agent;
  if (el.sbHostBadge) el.sbHostBadge.textContent = `@${formatHostLabel(session.host || 'local')}`;
  if (el.sbCwd) el.sbCwd.textContent = session.cwd || '~/';
  if (el.sbContextGauge) {
    el.sbContextGauge.className = 'statusbar-item';
    if (session.context_pct) {
      el.sbContextGauge.textContent = `ctx: ${session.context_pct}%`;
      const settings = state.settings || {};
      const handoverThreshold = parseInt(settings.handover_threshold_pct || '60', 10);
      if (session.context_pct >= 80) {
        el.sbContextGauge.classList.add('context-danger');
      } else if (session.context_pct >= handoverThreshold) {
        el.sbContextGauge.classList.add('context-warning');
      }
    } else {
      el.sbContextGauge.textContent = 'ctx: —';
    }
  }
  if (el.sbModelBadge) {
    const engineLabel = session.engine_type === 'headless' ? 'headless' : 'tmux';
    el.sbModelBadge.textContent = `${session.agent || 'claude'} (${engineLabel})`;
  }
  if (el.sbPID) el.sbPID.textContent = session.pid ? `PID ${session.pid}` : 'PID —';
  if (el.sbLastActive) {
    const timeRel = formatRelativeTime(session.last_event_at || session.started_at);
    const timeFull = formatFullDateTime(session.last_event_at || session.started_at);
    el.sbLastActive.textContent = timeRel ? `🕒 ${timeRel}` : '🕒 —';
    el.sbLastActive.title = timeFull ? `Last activity: ${timeFull}` : 'Last activity unknown';
  }
}

function resetStatusbar() {
  if (el.sbSessionName) el.sbSessionName.textContent = '—';
  if (el.sbHostBadge) el.sbHostBadge.textContent = '@local';
  if (el.sbCwd) el.sbCwd.textContent = '~/';
  if (el.sbGitBranch) el.sbGitBranch.textContent = '⎇ —';
  if (el.sbContextGauge) {
    el.sbContextGauge.className = 'statusbar-item';
    el.sbContextGauge.textContent = 'ctx: —';
  }
  if (el.sbModelBadge) el.sbModelBadge.textContent = 'claude';
  if (el.sbPID) el.sbPID.textContent = 'PID —';
  if (el.sbLastActive) {
    el.sbLastActive.textContent = '🕒 —';
    el.sbLastActive.title = 'No active session';
  }
}

// Setup Event Listeners & Global Shortcuts

export {
  updateStatusbar,
  resetStatusbar
};
