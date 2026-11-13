export function getCanonicalTaskProject(task) {
  if (!task) return 'General';
  let p = (task.project_name || '').trim();
  const g = (task.group_name || '').trim();
  if (g && p.toLowerCase().startsWith(g.toLowerCase() + '/')) {
    p = p.slice(g.length + 1).trim();
  }
  const sub = (task.subproject_name || '').trim();
  if (sub) {
    const subLower = sub.toLowerCase();
    const pLower = p.toLowerCase();
    if (!pLower.endsWith('/' + subLower) && pLower !== subLower) {
      p = p ? `${p}/${sub}` : sub;
    }
  }
  return p || 'General';
}

export function getCanonicalTaskNodePath(task) {
  const group = (task?.group_name || '').trim() || 'Personal';
  const project = getCanonicalTaskProject(task) || 'General';
  return `${group}/${project}`;
}

export function matchesTaskProjectFilter(task, groupFilter, projectFilter) {
  const taskGroup = (task?.group_name || '').trim() || 'Personal';
  const gf = (groupFilter || '').trim();
  if (gf && gf !== 'all' && taskGroup.toLowerCase() !== gf.toLowerCase()) {
    return false;
  }
  const pf = (projectFilter || '').trim();
  if (pf && pf !== 'all') {
    const canonicalProj = getCanonicalTaskProject(task).toLowerCase();
    const pfLower = pf.toLowerCase();
    if (canonicalProj !== pfLower && !canonicalProj.startsWith(pfLower + '/')) {
      return false;
    }
  }
  return true;
}

export function collectSelectableNodePaths(treeNodes = [], tasks = []) {
  const paths = new Set();
  if (Array.isArray(treeNodes)) {
    treeNodes.forEach((n) => {
      const p = (n?.path || '').trim();
      if (p && !p.toLowerCase().startsWith('unassigned') && p.includes('/')) {
        paths.add(p);
      }
    });
  }
  if (Array.isArray(tasks)) {
    tasks.forEach((t) => {
      const np = getCanonicalTaskNodePath(t);
      if (np && np.includes('/')) {
        paths.add(np);
      }
    });
  }
  return Array.from(paths).sort((a, b) => a.localeCompare(b));
}

export function computeDefaultNewTaskNodePath(nodePaths = [], groupFilter = 'all', projectFilter = 'all') {
  const gf = (groupFilter || '').trim();
  const pf = (projectFilter || '').trim();

  if (gf && gf !== 'all' && pf && pf !== 'all') {
    const target1 = `${gf}/${pf}`.toLowerCase();
    const target2 = pf.toLowerCase();
    const found = (nodePaths || []).find((p) => {
      const pLower = p.toLowerCase();
      return pLower === target1 || pLower === target2;
    });
    return found || '';
  }

  if (gf && gf !== 'all' && (!pf || pf === 'all')) {
    const prefix = `${gf.toLowerCase()}/`;
    const matching = (nodePaths || []).filter((p) => p.toLowerCase().startsWith(prefix));
    if (matching.length === 1) {
      return matching[0];
    }
    return '';
  }

  if (!gf || gf === 'all') {
    return (nodePaths && nodePaths.length === 1) ? nodePaths[0] : '';
  }

  return '';
}

export function buildProjectNodeSelectorHtml({
  selectId = 'mTaskNodePath',
  customInputId = 'mTaskCustomPath',
  nodePaths = [],
  currentPath = '',
  requireExplicitChoice = false,
  escapeHtml = (s) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'),
} = {}) {
  let placeholderHtml = '';
  if (requireExplicitChoice || !currentPath) {
    const isSelected = !currentPath ? 'selected' : '';
    placeholderHtml = `<option value="" disabled ${isSelected}>Select project node…</option>`;
  }

  const optionsHtml = (nodePaths || []).map((p) => {
    const isSelected = currentPath && p.toLowerCase() === currentPath.toLowerCase() ? 'selected' : '';
    return `<option value="${escapeHtml(p)}" ${isSelected}>${escapeHtml(p)}</option>`;
  }).join('');

  return `
    <select id="${escapeHtml(selectId)}" style="width: 100%; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;">
      ${placeholderHtml}
      ${optionsHtml}
      <option value="__custom__">＋ Custom path...</option>
    </select>
    <input type="text" id="${escapeHtml(customInputId)}" placeholder="e.g. Modemobile/NGL/ngl-ios or Personal/Ackbar" style="display: none; width: 100%; margin-top: 6px; background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border-color); border-radius: 4px; padding: 7px 10px; font-size: 12px;" />
  `;
}

export function wireProjectNodeSelector(doc = (typeof document !== 'undefined' ? document : null), selectId, customInputId) {
  if (!doc) return;
  const select = doc.getElementById(selectId);
  const customInput = doc.getElementById(customInputId);
  if (!select || !customInput) return;

  select.addEventListener('change', () => {
    if (select.value === '__custom__') {
      customInput.style.display = 'block';
      customInput.focus();
    } else {
      customInput.style.display = 'none';
    }
  });
}

export function readSelectedNodePath(doc = (typeof document !== 'undefined' ? document : null), selectId, customInputId) {
  if (!doc) return '';
  const select = doc.getElementById(selectId);
  const customInput = doc.getElementById(customInputId);
  let val = select?.value || '';
  if (val === '__custom__') {
    val = customInput?.value || '';
  }
  return val.trim();
}

export function showMoveTaskModal({ task, treeNodes, boardTasks, showModal, hideModal, escapeHtml, updateTask, onMoved }) {
  if (!task || typeof showModal !== 'function') return;

  const currentPath = getCanonicalTaskNodePath(task);
  const nodePaths = collectSelectableNodePaths(treeNodes, boardTasks);
  if (currentPath && !nodePaths.some((p) => p.toLowerCase() === currentPath.toLowerCase())) {
    nodePaths.push(currentPath);
    nodePaths.sort((a, b) => a.localeCompare(b));
  }

  const selectorHtml = buildProjectNodeSelectorHtml({
    selectId: 'mMoveTaskNodePath',
    customInputId: 'mMoveTaskCustomPath',
    nodePaths,
    currentPath,
    requireExplicitChoice: false,
    escapeHtml,
  });

  const bodyHtml = `
    <div style="display: flex; flex-direction: column; gap: 12px; padding: 4px;">
      <div>
        <label style="font-size: 11px; font-weight: 600; color: var(--text-muted); display: block; margin-bottom: 4px;">TARGET PROJECT / TREE NODE</label>
        ${selectorHtml}
      </div>
      <div style="font-size: 11px; color: var(--text-muted); line-height: 1.4;">
        Moving this task updates its project tree node scope. Existing worker sessions and workstream repo paths are preserved.
      </div>
    </div>
  `;

  const footerHtml = `
    <button class="btn btn-secondary" id="mBtnCancelMove">Cancel</button>
    <button class="btn btn-primary" id="mBtnConfirmMove">Move Task</button>
  `;

  showModal(`Move Task: ${task.title}`, bodyHtml, footerHtml);

  if (typeof document !== 'undefined') {
    wireProjectNodeSelector(document, 'mMoveTaskNodePath', 'mMoveTaskCustomPath');

    document.getElementById('mBtnCancelMove')?.addEventListener('click', hideModal);
    document.getElementById('mBtnConfirmMove')?.addEventListener('click', async () => {
      const targetPath = readSelectedNodePath(document, 'mMoveTaskNodePath', 'mMoveTaskCustomPath');
      if (!targetPath) {
        alert('Please select a project node');
        return;
      }
      try {
        if (typeof updateTask === 'function') {
          await updateTask({
            id: task.id,
            project_node_path: targetPath,
            subproject_name: '',
          });
        }
        if (typeof hideModal === 'function') hideModal();
        if (typeof onMoved === 'function') onMoved();
      } catch (err) {
        alert(`Error moving task: ${err.message}`);
      }
    });
  }
}
