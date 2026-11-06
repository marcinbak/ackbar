// Mermaid Flowchart & Diagram Rendering Module
import { escapeHtml } from './utils.js';

export const MERMAID_HEADER_REGEX = /^(?:flowchart|graph)\s+(?:TB|TD|BT|RL|LR)(?:\s|;|$)|^sequenceDiagram(?:\s|$)|^stateDiagram(?:-v2)?(?:\s|$)|^classDiagram(?:\s|$)|^erDiagram(?:\s|$)|^gantt(?:\s|$)|^pie(?:\s+(?:showData|title)\b|\s*$)|^gitGraph(?:\s|:|$)|^mindmap(?:\s|$)|^timeline(?:\s+title\b|\s*$)|^journey(?:\s|$)|^xychart-beta(?:\s|$)|^quadrantChart(?:\s|$)|^C4(?:Context|Container|Component|Dynamic|Deployment)(?:\s|$)|^sankey-beta(?:\s|$)|^block-beta(?:\s|$)|^packet-beta(?:\s|$)|^architecture-beta(?:\s|$)/;

let mermaidInitialized = false;
let mermaidRenderSeq = 0;
let mermaidRenderQueue = Promise.resolve();
export const mermaidSvgCache = new Map();

function cleanupStrayMermaidElements(renderId) {
  if (typeof document === 'undefined') return;
  const stray1 = document.getElementById(renderId);
  if (stray1) stray1.remove();
  const stray2 = document.getElementById('d' + renderId);
  if (stray2) stray2.remove();
}

export function isMermaidSource(source) {
  if (!source || typeof source !== 'string') return false;
  const lines = source.split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('%%')) continue;
    return MERMAID_HEADER_REGEX.test(trimmed);
  }
  return false;
}

export function isMermaidCodeBlock(preEl, codeEl) {
  if (!preEl && !codeEl) return false;
  const targetCode = codeEl || (preEl ? preEl.querySelector('code') : null);
  const className = targetCode ? (targetCode.className || '') : (preEl ? preEl.className || '' : '');
  const classList = className.split(/\s+/).filter(Boolean);
  if (classList.includes('language-mermaid') || classList.includes('mermaid')) {
    return true;
  }
  const hasOtherLang = classList.some(c => c.startsWith('language-') && c !== 'language-text' && c !== 'language-plaintext' && c !== 'language-mermaid');
  if (hasOtherLang) {
    return false;
  }
  const rawText = targetCode ? targetCode.textContent : (preEl ? preEl.textContent : '');
  return isMermaidSource(rawText);
}

export function normalizeMermaidForRender(source) {
  if (!source || typeof source !== 'string') return '';
  let normalized = source.replace(/\r\n/g, '\n').trim();
  normalized = normalized.replace(/"((?:(?!")[^\\]|\\.)*)"/g, (match, inner) => {
    if (!inner.includes('\\n') && !inner.includes('\n')) return match;
    return '"' + inner.replace(/\\n/g, '<br/>').replace(/\n/g, '<br/>') + '"';
  });
  return normalized;
}

export function ensureMermaidInitialized() {
  if (typeof window === 'undefined' || !window.mermaid) return false;
  if (mermaidInitialized) return true;
  try {
    window.mermaid.initialize({
      startOnLoad: false,
      suppressErrorRendering: true,
      securityLevel: 'strict',
      theme: 'base',
      fontFamily: 'JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
      themeVariables: {
        darkMode: true,
        background: '#090a0f',
        primaryColor: '#161922',
        primaryTextColor: '#e2e8f0',
        primaryBorderColor: '#06b6d4',
        lineColor: '#64748b',
        secondaryColor: '#1e2638',
        tertiaryColor: '#12151e',
        clusterBkg: '#0f131c',
        clusterBorder: '#334155',
        defaultLinkColor: '#94a3b8',
        titleColor: '#f8fafc',
        edgeLabelBackground: '#0f131c',
        nodeTextColor: '#e2e8f0'
      }
    });
    mermaidInitialized = true;
    return true;
  } catch (e) {
    console.warn('Failed to initialize mermaid:', e);
    return false;
  }
}

export function upgradePreToMermaidBlock(preEl, rawSource, svgHtml, copyFn) {
  if (!preEl) return null;
  const blockEl = document.createElement('div');
  blockEl.className = 'mermaid-diagram-block';
  blockEl.dataset.rawMermaid = rawSource;

  blockEl.innerHTML = `
    <div class="mermaid-toolbar">
      <span class="mermaid-badge">❖ Flowchart / Diagram</span>
      <div class="mermaid-toolbar-actions">
        <div class="mermaid-view-toggle" role="group" aria-label="Diagram view mode">
          <button type="button" class="btn-mermaid-toggle active" data-view="diagram" title="Show rendered diagram">Diagram</button>
          <button type="button" class="btn-mermaid-toggle" data-view="code" title="Show raw flowchart text">Code</button>
        </div>
        <button type="button" class="btn-copy-mermaid" title="Copy flowchart text" aria-label="Copy flowchart text">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
          </svg>
          <span>Copy Text</span>
        </button>
      </div>
    </div>
    <div class="mermaid-svg-container">${svgHtml}</div>
    <pre class="mermaid-code-container" style="display: none;"><code class="language-mermaid">${escapeHtml(rawSource)}</code></pre>
  `;

  const btnDiagram = blockEl.querySelector('.btn-mermaid-toggle[data-view="diagram"]');
  const btnCode = blockEl.querySelector('.btn-mermaid-toggle[data-view="code"]');
  const svgContainer = blockEl.querySelector('.mermaid-svg-container');
  const codeContainer = blockEl.querySelector('.mermaid-code-container');
  const btnCopy = blockEl.querySelector('.btn-copy-mermaid');

  if (btnDiagram && btnCode && svgContainer && codeContainer) {
    btnDiagram.addEventListener('click', (e) => {
      e.stopPropagation();
      btnDiagram.classList.add('active');
      btnCode.classList.remove('active');
      svgContainer.style.display = '';
      codeContainer.style.display = 'none';
    });

    btnCode.addEventListener('click', (e) => {
      e.stopPropagation();
      btnCode.classList.add('active');
      btnDiagram.classList.remove('active');
      svgContainer.style.display = 'none';
      codeContainer.style.display = '';
    });
  }

  if (btnCopy) {
    btnCopy.addEventListener('click', async (e) => {
      e.stopPropagation();
      e.preventDefault();
      const textToCopy = (blockEl.dataset.rawMermaid || rawSource).replace(/\r\n/g, '\n').replace(/\n+$/, '');
      let ok = false;
      if (typeof copyFn === 'function') {
        ok = await copyFn(textToCopy);
      } else if (navigator.clipboard && navigator.clipboard.writeText) {
        try {
          await navigator.clipboard.writeText(textToCopy);
          ok = true;
        } catch (_) {
          ok = false;
        }
      }
      if (ok) {
        btnCopy.classList.add('copied');
        btnCopy.innerHTML = `
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"></polyline>
          </svg>
          <span>Copied!</span>
        `;
        setTimeout(() => {
          btnCopy.classList.remove('copied');
          btnCopy.innerHTML = `
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
            </svg>
            <span>Copy Text</span>
          `;
        }, 1500);
      }
    });
  }

  if (preEl.parentNode) {
    preEl.parentNode.replaceChild(blockEl, preEl);
  }
  return blockEl;
}

export function renderMermaidDiagrams(container, copyFn, options = {}) {
  if (!container) return;
  const cacheOnly = Boolean(options && options.cacheOnly);
  const preEls = container.querySelectorAll('pre');
  preEls.forEach(pre => {
    if (pre.closest('.mermaid-diagram-block') || pre.closest('.chat-thinking') || pre.closest('.chat-tool-card')) {
      return;
    }
    const codeEl = pre.querySelector('code');
    if (!isMermaidCodeBlock(pre, codeEl)) {
      return;
    }

    let rawSource = '';
    if (codeEl) {
      rawSource = codeEl.textContent || '';
    } else {
      const clone = pre.cloneNode(true);
      const copyBtn = clone.querySelector('.btn-copy-code');
      if (copyBtn) copyBtn.remove();
      rawSource = clone.textContent || '';
    }
    rawSource = rawSource.replace(/\r\n/g, '\n').trim();
    if (!rawSource) return;

    if (mermaidSvgCache.has(rawSource)) {
      upgradePreToMermaidBlock(pre, rawSource, mermaidSvgCache.get(rawSource), copyFn);
      return;
    }

    if (cacheOnly) {
      return;
    }

    if (pre.dataset.mermaidRendering === 'true' || pre.dataset.mermaidRendering === 'failed') {
      return;
    }

    if (typeof window === 'undefined' || !window.mermaid || typeof window.mermaid.render !== 'function') {
      return;
    }

    ensureMermaidInitialized();

    pre.dataset.mermaidRendering = 'true';
    mermaidRenderSeq++;
    const renderId = 'mermaid-render-' + Date.now() + '-' + mermaidRenderSeq;
    const normalized = normalizeMermaidForRender(rawSource);

    mermaidRenderQueue = mermaidRenderQueue
      .then(() => window.mermaid.render(renderId, normalized))
      .then(({ svg }) => {
        cleanupStrayMermaidElements(renderId);

        if (mermaidSvgCache.size >= 200) {
          const firstKey = mermaidSvgCache.keys().next().value;
          mermaidSvgCache.delete(firstKey);
        }
        mermaidSvgCache.set(rawSource, svg);

        if (pre.isConnected) {
          upgradePreToMermaidBlock(pre, rawSource, svg, copyFn);
        }
      })
      .catch(err => {
        console.warn('Mermaid rendering failed:', err);
        cleanupStrayMermaidElements(renderId);
        pre.dataset.mermaidRendering = 'failed';
      });
  });
}
