import { escapeHtml } from './utils.js';

export function appendEphemeralCard(tabObj, cardEl) {
  if (!tabObj || !tabObj.chatMessagesEl || !cardEl) return;
  cardEl.classList.add('chat-ephemeral-card');
  tabObj.ephemeralCards = tabObj.ephemeralCards || [];
  tabObj.ephemeralCards.push(cardEl);
  tabObj.chatMessagesEl.appendChild(cardEl);
  tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
}

export function insertSnippetIntoComposer(tabObj, snippet) {
  if (!tabObj || !tabObj.chatInputEl || !snippet) return;
  const input = tabObj.chatInputEl;
  const current = input.value;
  if (current && !current.endsWith('\n\n')) {
    input.value = current.endsWith('\n') ? `${current}\n${snippet}\n` : `${current}\n\n${snippet}\n`;
  } else {
    input.value = `${current}${snippet}\n`;
  }
  input.style.height = 'auto';
  input.style.height = `${Math.min(input.scrollHeight, 180)}px`;
  input.focus();
}

export function renderBashCard(tabObj, data) {
  const card = document.createElement('div');
  card.className = 'chat-card chat-bash-card';

  const isSuccess = data.exit_code === 0 && !data.timed_out;
  const statusBadge = isSuccess ? '<span class="bash-badge success">exit 0</span>' :
    `<span class="bash-badge fail">${data.timed_out ? 'timed out (30s)' : `exit ${data.exit_code}`}</span>`;
  const truncBadge = data.truncated ? '<span class="bash-badge trunc">truncated (128 KB)</span>' : '';

  const outputText = [data.stdout || '', data.stderr || ''].filter(Boolean).join('\n').trim();

  card.innerHTML = `
    <div class="bash-card-header">
      <div class="bash-card-title">
        <span class="bash-icon">⚡</span>
        <code class="bash-cmd">${escapeHtml(data.command || '')}</code>
      </div>
      <div class="bash-card-meta">
        <span class="bash-duration">${data.duration_ms || 0}ms</span>
        ${statusBadge}
        ${truncBadge}
      </div>
    </div>
    <div class="bash-card-body">
      ${outputText ? `<pre class="bash-output"><code>${escapeHtml(outputText)}</code></pre>` : '<div class="bash-empty">No output</div>'}
    </div>
    <div class="bash-card-actions">
      <button type="button" class="btn-bash-attach" title="Attach output to prompt composer">+ Attach to Prompt</button>
    </div>
  `;

  const attachBtn = card.querySelector('.btn-bash-attach');
  if (attachBtn) {
    attachBtn.addEventListener('click', () => {
      const snippet = `\`\`\`sh\n$ ${data.command}\n${outputText}\n\`\`\``;
      insertSnippetIntoComposer(tabObj, snippet);
    });
  }

  appendEphemeralCard(tabObj, card);
  return card;
}

export function renderDiffCard(tabObj, data) {
  const card = document.createElement('div');
  card.className = 'chat-card chat-diff-card';

  if (!data.is_git_repo) {
    card.innerHTML = `
      <div class="diff-card-header">
        <span class="diff-icon">📁</span>
        <span>Not a git repository (${escapeHtml(data.cwd || '')})</span>
      </div>
    `;
    appendEphemeralCard(tabObj, card);
    return card;
  }

  const branchLabel = data.branch ? `<span class="diff-branch">🌿 ${escapeHtml(data.branch)}</span>` : '';
  const cleanLabel = data.clean ? '<span class="diff-clean">Clean worktree</span>' : '';
  const truncLabel = data.truncated ? '<span class="diff-trunc">truncated (64 KB)</span>' : '';

  card.innerHTML = `
    <div class="diff-card-header">
      <div class="diff-title-wrap">
        <span class="diff-icon">📊</span>
        <span class="diff-title">Git Diff & Status</span>
        ${branchLabel}
      </div>
      <div class="diff-badges">
        ${cleanLabel}
        ${truncLabel}
      </div>
    </div>
    <div class="diff-card-body">
      ${data.stat ? `<div class="diff-stat-section"><div class="diff-subheading">Stat:</div><pre class="diff-stat"><code>${escapeHtml(data.stat)}</code></pre></div>` : ''}
      ${data.diff ? `<div class="diff-content-section"><div class="diff-subheading">Diff:</div><pre class="diff-code"><code>${escapeHtml(data.diff)}</code></pre></div>` : '<div class="diff-empty">No uncommitted changes</div>'}
    </div>
    ${data.diff ? `
      <div class="diff-card-actions">
        <button type="button" class="btn-diff-attach" title="Attach diff to prompt composer">+ Attach to Prompt</button>
      </div>
    ` : ''}
  `;

  const attachBtn = card.querySelector('.btn-diff-attach');
  if (attachBtn && data.diff) {
    attachBtn.addEventListener('click', () => {
      const snippet = `\`\`\`diff\n${data.diff}\n\`\`\``;
      insertSnippetIntoComposer(tabObj, snippet);
    });
  }

  appendEphemeralCard(tabObj, card);
  return card;
}

export function renderRewindCard(tabObj, rewindData, onSelectTurn) {
  const card = document.createElement('div');
  card.className = 'chat-card chat-rewind-card';

  const turns = Array.isArray(rewindData.turns) ? rewindData.turns : [];
  if (turns.length === 0) {
    card.innerHTML = `
      <div class="rewind-header"><span>⏪</span> <strong>Conversation Rewind</strong></div>
      <div class="rewind-empty">No previous conversation turns found to rewind.</div>
    `;
    appendEphemeralCard(tabObj, card);
    return card;
  }

  let turnItemsHtml = '';
  for (const t of turns) {
    turnItemsHtml += `
      <div class="rewind-turn-item">
        <div class="rewind-turn-info">
          <span class="rewind-turn-num">Turn ${t.turn}</span>
          <span class="rewind-turn-preview">${escapeHtml(t.preview || '')}</span>
        </div>
        <button type="button" class="btn-rewind-trigger" data-turn="${t.turn}">Rewind Before Turn ${t.turn}</button>
      </div>
    `;
  }

  card.innerHTML = `
    <div class="rewind-header">
      <span>⏪</span> <strong>Conversation Rewind</strong>
      <span class="rewind-subtitle">Select a turn to restore conversation state before that point</span>
    </div>
    <div class="rewind-turn-list">
      ${turnItemsHtml}
    </div>
  `;

  const buttons = card.querySelectorAll('.btn-rewind-trigger');
  buttons.forEach(btn => {
    btn.addEventListener('click', () => {
      const turnNum = parseInt(btn.dataset.turn, 10);
      if (typeof onSelectTurn === 'function') {
        onSelectTurn(turnNum);
      }
    });
  });

  appendEphemeralCard(tabObj, card);
  return card;
}

export function renderConfigNoticeCard(tabObj, title, message) {
  const card = document.createElement('div');
  card.className = 'chat-card chat-config-notice';
  card.innerHTML = `
    <div class="config-notice-header"><span>⚙️</span> <strong>${escapeHtml(title)}</strong></div>
    <div class="config-notice-body">${escapeHtml(message)}</div>
  `;
  appendEphemeralCard(tabObj, card);
  return card;
}

export function renderInspectCard(tabObj, kind, data) {
  const card = document.createElement('div');
  card.className = `chat-card chat-inspect-card chat-inspect-${kind}`;

  if (kind === 'context' || kind === 'cost') {
    card.innerHTML = `
      <div class="inspect-header"><span>📊</span> <strong>Context & Cost Breakdown (${escapeHtml(data.agent || '')})</strong></div>
      <div class="inspect-grid">
        <div><strong>Model:</strong> ${escapeHtml(data.model || 'Default')}</div>
        <div><strong>Turns:</strong> ${data.turn_count || 0}</div>
        <div><strong>Context Window:</strong> ${data.context_used_tokens || 0} / ${data.context_limit_tokens || 0} tokens (${data.context_pct || 0}%)</div>
        <div><strong>Input Tokens:</strong> ${data.input_tokens || 0}</div>
        <div><strong>Output Tokens:</strong> ${data.output_tokens || 0}</div>
        <div><strong>Cache Read / Write:</strong> ${data.cache_read_tokens || 0} / ${data.cache_write_tokens || 0}</div>
        <div><strong>Est. API Cost:</strong> <span class="cost-val">${escapeHtml(data.estimated_usd || '$0.00')}</span></div>
      </div>
      <div class="inspect-note">${escapeHtml(data.billing_note || '')}</div>
    `;
  } else if (kind === 'doctor') {
    card.innerHTML = `
      <div class="inspect-header"><span>🩺</span> <strong>Agent Diagnostic Report (${escapeHtml(data.agent || '')})</strong></div>
      <div class="inspect-body"><pre class="doctor-report"><code>${escapeHtml(data.report || 'No diagnostic output')}</code></pre></div>
    `;
  } else if (kind === 'agents') {
    const builtins = Array.isArray(data.builtins) ? data.builtins : [];
    const localRoles = Array.isArray(data.local_roles) ? data.local_roles : [];
    card.innerHTML = `
      <div class="inspect-header"><span>🤖</span> <strong>Available Agent Roles</strong></div>
      <div class="inspect-section-title">Built-in Roles:</div>
      <ul class="inspect-list">
        ${builtins.map(b => `<li><strong>${escapeHtml(b.name)}:</strong> ${escapeHtml(b.description)}</li>`).join('')}
      </ul>
      ${localRoles.length > 0 ? `
        <div class="inspect-section-title">Local Role Definitions:</div>
        <ul class="inspect-list">
          ${localRoles.map(l => `<li><strong>${escapeHtml(l.name)}:</strong> ${escapeHtml(l.description)}</li>`).join('')}
        </ul>
      ` : '<div class="inspect-empty">No custom agent roles configured</div>'}
    `;
  } else if (kind === 'plugins') {
    const installed = Array.isArray(data.installed_plugins) ? data.installed_plugins : [];
    const workspace = Array.isArray(data.workspace_plugins) ? data.workspace_plugins : [];
    card.innerHTML = `
      <div class="inspect-header"><span>🔌</span> <strong>Installed Plugins (${escapeHtml(data.agent || '')})</strong></div>
      <div class="inspect-section-title">CLI Plugins:</div>
      <pre class="plugin-raw"><code>${escapeHtml(JSON.stringify(installed, null, 2))}</code></pre>
      ${workspace.length > 0 ? `
        <div class="inspect-section-title">Workspace Plugins:</div>
        <ul class="inspect-list">
          ${workspace.map(p => `<li>${escapeHtml(p.name)} (${p.installed ? 'installed' : 'not installed'})</li>`).join('')}
        </ul>
      ` : ''}
    `;
  }

  appendEphemeralCard(tabObj, card);
  return card;
}
