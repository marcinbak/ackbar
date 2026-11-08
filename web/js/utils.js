// Markdown, Formatting & Host Utilities
import { state, el } from './state.js';
import { sortSessionsByInteraction } from './tree.js';
import { showUploadToast } from './terminal.js';

let markedConfigured = false;
function configureMarked() {
  if (!window.marked || markedConfigured) return;

  const customRenderer = {
    link(href, title, text) {
      let linkHref = href;
      let linkTitle = title;
      let linkText = text;
      if (typeof href === 'object' && href !== null) {
        linkHref = href.href;
        linkTitle = href.title;
        linkText = href.text;
      }
      linkHref = linkHref || '';
      linkText = linkText || linkHref;

      const isAnchor = linkHref.startsWith('#');
      const isFile = linkHref.startsWith('file://');
      const targetAttr = (!isAnchor && !isFile) ? ' target="_blank" rel="noopener noreferrer"' : '';
      const titleAttr = linkTitle ? ` title="${escapeHtml(linkTitle)}"` : '';

      return `<a href="${escapeHtml(linkHref)}"${titleAttr}${targetAttr}>${linkText}</a>`;
    }
  };

  if (typeof window.marked.use === 'function') {
    window.marked.use({
      breaks: true,
      gfm: true,
      renderer: customRenderer
    });
    markedConfigured = true;
  } else if (typeof window.marked.setOptions === 'function') {
    const renderer = window.marked.Renderer ? new window.marked.Renderer() : {};
    renderer.link = customRenderer.link;
    window.marked.setOptions({
      breaks: true,
      gfm: true,
      renderer: renderer
    });
    markedConfigured = true;
  }
}
configureMarked();

function ensureExternalLinksTargetBlank(html) {
  if (!html || typeof html !== 'string') return html || '';
  return html.replace(/<a\b([^>]*)>/gi, (match, attrs) => {
    const hrefMatch = attrs.match(/href\s*=\s*["']([^"']*)["']/i);
    const href = hrefMatch ? hrefMatch[1] : '';
    if (!href || href.startsWith('#') || href.startsWith('file://') || href.startsWith('javascript:')) {
      return match;
    }
    let newAttrs = attrs;
    if (/target\s*=/i.test(newAttrs)) {
      newAttrs = newAttrs.replace(/target\s*=\s*["'][^"']*["']/i, 'target="_blank"');
    } else {
      newAttrs += ' target="_blank"';
    }
    if (/rel\s*=/i.test(newAttrs)) {
      newAttrs = newAttrs.replace(/rel\s*=\s*["'][^"']*["']/i, 'rel="noopener noreferrer"');
    } else {
      newAttrs += ' rel="noopener noreferrer"';
    }
    return `<a${newAttrs}>`;
  });
}

function sanitizeRenderedMarkdownHtml(html) {
  if (!html || typeof html !== 'string') return '';
  if (typeof document !== 'undefined' && typeof document.createElement === 'function') {
    const tpl = document.createElement('template');
    if (tpl && tpl.content && typeof tpl.content.querySelectorAll === 'function') {
      tpl.innerHTML = html;
      tpl.content.querySelectorAll('script, iframe, object, embed').forEach(node => node.remove());
      tpl.content.querySelectorAll('*').forEach(node => {
        for (const attr of Array.from(node.attributes)) {
          const name = attr.name.toLowerCase();
          const val = (attr.value || '').trim();
          if (name.startsWith('on')) {
            node.removeAttribute(attr.name);
          } else if ((name === 'href' || name === 'src' || name === 'xlink:href' || name === 'action' || name === 'formaction') && /^javascript:/i.test(val)) {
            node.setAttribute(attr.name, '#');
          }
        }
      });
      return tpl.innerHTML;
    }
  }
  return html
    .replace(/<(script|iframe|object)\b[\s\S]*?<\/\1>/gi, '')
    .replace(/<(script|iframe|object|embed)\b[^>]*\/?>/gi, '');
}

function renderSlideContent(slideMd) {
  if (!slideMd) return '';
  if (typeof window !== 'undefined' && window.marked && typeof window.marked.parse === 'function') {
    configureMarked();

    // Mask code blocks and inline code spans before LaTeX math processing
    const slideTokens = [];
    let masked = slideMd.replace(/(^|\r?\n)(`{3,}|~{3,})(?![`~])[^\n]*\r?\n[\s\S]*?\r?\n\2(?=\r?\n|$)/gi, (m, prefix) => {
      const idx = slideTokens.length;
      slideTokens.push(m.slice(prefix.length));
      return prefix + `%%SLIDE_CODE_TOKEN_${idx}%%`;
    });
    masked = masked.replace(/(`+)(?:[^\r\n`]|(?!\1)`)+\1/g, (m) => {
      const idx = slideTokens.length;
      slideTokens.push(m);
      return `%%SLIDE_CODE_TOKEN_${idx}%%`;
    });

    if (masked.includes('$')) {
      masked = preprocessLatexMath(masked);
    }

    // Unmask code blocks before feeding into marked.parse
    if (slideTokens.length > 0) {
      masked = masked.replace(/%%SLIDE_CODE_TOKEN_(\d+)%%/g, (m, id) => {
        const val = slideTokens[parseInt(id, 10)];
        return val !== undefined ? val : m;
      });
    }

    const raw = window.marked.parse(masked);
    const linked = ensureExternalLinksTargetBlank(raw);
    const alerted = processMarkdownAlerts(linked);
    return sanitizeRenderedMarkdownHtml(alerted);
  }
  return `<p>${escapeHtml(slideMd)}</p>`;
}

function buildCarouselHtml(inner, unmaskFn = (s) => s) {
  const parts = inner.split(/<!--\s*slide\s*-->/i);
  const slides = parts.map(s => s.trim()).filter(Boolean);
  if (slides.length === 0) return '';
  const slidesHtml = slides.map((slideMd, idx) => {
    const activeAttr = idx === 0 ? ' data-active="true"' : '';
    const unmasked = typeof unmaskFn === 'function' ? unmaskFn(slideMd) : slideMd;
    const slideParsed = renderSlideContent(unmasked);
    return `<div class="chat-carousel-slide" data-slide-index="${idx}"${activeAttr}>${slideParsed}</div>`;
  }).join('');
  return `<div class="chat-carousel" data-carousel-mode="slide"><div class="chat-carousel-slides">${slidesHtml}</div></div>`;
}

function processUnfencedSlides(text, unmaskFn = (s) => s) {
  if (!/<!--\s*slide\s*-->/i.test(text)) return typeof unmaskFn === 'function' ? unmaskFn(text) : text;

  const normalizedText = text.replace(/\r\n/g, '\n');
  const rawParts = normalizedText.split(/<!--\s*slide\s*-->/i);
  if (rawParts.length <= 1) return typeof unmaskFn === 'function' ? unmaskFn(normalizedText) : normalizedText;

  let prefix = '';
  let firstSlide = rawParts[0];

  const trimmedFirst = firstSlide.trimEnd();
  const lastDoubleNewline = trimmedFirst.lastIndexOf('\n\n');
  if (lastDoubleNewline !== -1) {
    const beforeDbl = trimmedFirst.slice(0, lastDoubleNewline).trim();
    const afterDbl = trimmedFirst.slice(lastDoubleNewline + 2).trim();
    const lastParaOfBefore = beforeDbl.includes('\n\n')
      ? beforeDbl.slice(beforeDbl.lastIndexOf('\n\n') + 2).trim()
      : beforeDbl;
    if (!lastParaOfBefore.startsWith('![') && !lastParaOfBefore.startsWith('<img') &&
        (afterDbl.startsWith('![') || afterDbl.startsWith('<img') || (afterDbl.startsWith('#') && !lastParaOfBefore.startsWith('#')))) {
      prefix = beforeDbl + '\n\n';
      firstSlide = afterDbl;
    }
  }

  let suffix = '';
  let lastSlide = rawParts[rawParts.length - 1];
  const trimmedLast = lastSlide.trimStart();
  const firstDoubleNewline = trimmedLast.indexOf('\n\n');
  if (firstDoubleNewline !== -1) {
    const beforeDbl = trimmedLast.slice(0, firstDoubleNewline).trim();
    const afterDbl = trimmedLast.slice(firstDoubleNewline + 2).trim();
    if (!firstSlide.trim().includes('\n\n') &&
        (beforeDbl.startsWith('![') || beforeDbl.startsWith('<img')) &&
        !afterDbl.startsWith('![') && !afterDbl.startsWith('<img')) {
      lastSlide = beforeDbl;
      suffix = '\n\n' + afterDbl;
    }
  }

  const slideTexts = [firstSlide, ...rawParts.slice(1, -1), lastSlide]
    .map(s => s.trim())
    .filter(Boolean);

  if (slideTexts.length === 0) return typeof unmaskFn === 'function' ? unmaskFn(text) : text;

  const slidesHtml = slideTexts.map((slideMd, idx) => {
    const activeAttr = idx === 0 ? ' data-active="true"' : '';
    const unmaskedSlideMd = typeof unmaskFn === 'function' ? unmaskFn(slideMd) : slideMd;
    const slideParsed = renderSlideContent(unmaskedSlideMd);
    return `<div class="chat-carousel-slide" data-slide-index="${idx}"${activeAttr}>${slideParsed}</div>`;
  }).join('');

  const carouselHtml = `<div class="chat-carousel" data-carousel-mode="slide"><div class="chat-carousel-slides">${slidesHtml}</div></div>`;

  const finalPrefix = typeof unmaskFn === 'function' ? unmaskFn(prefix) : prefix;
  const finalSuffix = typeof unmaskFn === 'function' ? unmaskFn(suffix) : suffix;

  return finalPrefix + carouselHtml + finalSuffix;
}

function preprocessCarouselBlocks(content, unmaskFn = (s) => s) {
  if (!content || typeof content !== 'string') return content || '';
  let result = content;

  // 1. Process 3- or 4-backtick ````carousel ... ```` blocks
  result = result.replace(/(^|\r?\n)(`{3,4})carousel[ \t]*\r?\n([\s\S]*?)\r?\n\2(?=\r?\n|$)/gi, (match, prefix, fence, inner) => {
    return prefix + buildCarouselHtml(inner, unmaskFn);
  });

  // 2. Process unfenced <!-- slide --> sequences outside fenced code blocks
  if (/<!--\s*slide\s*-->/i.test(result)) {
    const codeBlocks = [];
    const masked = result.replace(/(^|\r?\n)(`{3,}|~{3,})[^\n]*\r?\n[\s\S]*?\r?\n\2(?=\r?\n|$)/g, (m) => {
      const idx = codeBlocks.length;
      codeBlocks.push(m);
      return `\n%%CAROUSEL_CODE_BLOCK_${idx}%%\n`;
    });

    if (/<!--\s*slide\s*-->/i.test(masked)) {
      const combinedUnmask = (str) => {
        if (!str || typeof str !== 'string') return str || '';
        let unmasked = str.replace(/%%CAROUSEL_CODE_BLOCK_(\d+)%%/g, (m, id) => codeBlocks[parseInt(id, 10)] || m);
        if (typeof unmaskFn === 'function') {
          unmasked = unmaskFn(unmasked);
        }
        return unmasked;
      };
      result = processUnfencedSlides(masked, combinedUnmask);
    }
  }

  return result;
}

// LaTeX Symbol & Macro Mapping Table (ordered from longest commands to shortest prefixes)
const LATEX_SYMBOL_REPLACEMENTS = [
  // Multi-character arrows & symbols
  [/\\longleftrightarrow(?![a-zA-Z])/g, '⟷'],
  [/\\Longleftrightarrow(?![a-zA-Z])/g, '⟺'],
  [/\\leftrightarrow(?![a-zA-Z])/g, '↔'],
  [/\\Leftrightarrow(?![a-zA-Z])/g, '⇔'],
  [/\\longrightarrow(?![a-zA-Z])/g, '⟶'],
  [/\\Longrightarrow(?![a-zA-Z])/g, '⟹'],
  [/\\rightarrow(?![a-zA-Z])/g, '→'],
  [/\\to(?![a-zA-Z])/g, '→'],
  [/\\Rightarrow(?![a-zA-Z])/g, '⇒'],
  [/\\implies(?![a-zA-Z])/g, '⇒'],
  [/\\longleftarrow(?![a-zA-Z])/g, '⟵'],
  [/\\Longleftarrow(?![a-zA-Z])/g, '⟸'],
  [/\\leftarrow(?![a-zA-Z])/g, '←'],
  [/\\gets(?![a-zA-Z])/g, '←'],
  [/\\Leftarrow(?![a-zA-Z])/g, '⇐'],
  [/\\mapsto(?![a-zA-Z])/g, '↦'],
  [/\\uparrow(?![a-zA-Z])/g, '↑'],
  [/\\downarrow(?![a-zA-Z])/g, '↓'],

  // Operators & Relations
  [/\\times(?![a-zA-Z])/g, '×'],
  [/\\div(?![a-zA-Z])/g, '÷'],
  [/\\pm(?![a-zA-Z])/g, '±'],
  [/\\mp(?![a-zA-Z])/g, '∓'],
  [/\\cdot(?![a-zA-Z])/g, '·'],
  [/\\ast(?![a-zA-Z])/g, '∗'],
  [/\\star(?![a-zA-Z])/g, '⋆'],
  [/\\circ(?![a-zA-Z])/g, '∘'],
  [/\\bullet(?![a-zA-Z])/g, '•'],
  [/\\oplus(?![a-zA-Z])/g, '⊕'],
  [/\\otimes(?![a-zA-Z])/g, '⊗'],
  [/\\approx(?![a-zA-Z])/g, '≈'],
  [/\\neq(?![a-zA-Z])/g, '≠'],
  [/\\ne(?![a-zA-Z])/g, '≠'],
  [/\\leq(?![a-zA-Z])/g, '≤'],
  [/\\le(?![a-zA-Z])/g, '≤'],
  [/\\geq(?![a-zA-Z])/g, '≥'],
  [/\\ge(?![a-zA-Z])/g, '≥'],
  [/\\ll(?![a-zA-Z])/g, '≪'],
  [/\\gg(?![a-zA-Z])/g, '≫'],
  [/\\equiv(?![a-zA-Z])/g, '≡'],
  [/\\simeq(?![a-zA-Z])/g, '≃'],
  [/\\sim(?![a-zA-Z])/g, '∼'],
  [/\\cong(?![a-zA-Z])/g, '≅'],
  [/\\propto(?![a-zA-Z])/g, '∝'],
  [/\\infty(?![a-zA-Z])/g, '∞'],
  [/\\notin(?![a-zA-Z])/g, '∉'],
  [/\\in(?![a-zA-Z])/g, '∈'],
  [/\\subseteq(?![a-zA-Z])/g, '⊆'],
  [/\\subset(?![a-zA-Z])/g, '⊂'],
  [/\\supseteq(?![a-zA-Z])/g, '⊇'],
  [/\\supset(?![a-zA-Z])/g, '⊃'],
  [/\\cup(?![a-zA-Z])/g, '∪'],
  [/\\cap(?![a-zA-Z])/g, '∩'],
  [/\\varnothing(?![a-zA-Z])/g, '∅'],
  [/\\emptyset(?![a-zA-Z])/g, '∅'],
  [/\\forall(?![a-zA-Z])/g, '∀'],
  [/\\exists(?![a-zA-Z])/g, '∃'],
  [/\\neg(?![a-zA-Z])/g, '¬'],
  [/\\lnot(?![a-zA-Z])/g, '¬'],
  [/\\wedge(?![a-zA-Z])/g, '∧'],
  [/\\land(?![a-zA-Z])/g, '∧'],
  [/\\vee(?![a-zA-Z])/g, '∨'],
  [/\\lor(?![a-zA-Z])/g, '∨'],
  [/\\nabla(?![a-zA-Z])/g, '∇'],
  [/\\partial(?![a-zA-Z])/g, '∂'],
  [/\\sum(?![a-zA-Z])/g, '∑'],
  [/\\prod(?![a-zA-Z])/g, '∏'],
  [/\\int(?![a-zA-Z])/g, '∫'],
  [/\\checkmark(?![a-zA-Z])/g, '✓'],
  [/\\cdots(?![a-zA-Z])/g, '…'],
  [/\\ldots(?![a-zA-Z])/g, '…'],
  [/\\dots(?![a-zA-Z])/g, '…'],

  // Greek letters
  [/\\alpha(?![a-zA-Z])/g, 'α'],
  [/\\beta(?![a-zA-Z])/g, 'β'],
  [/\\gamma(?![a-zA-Z])/g, 'γ'],
  [/\\delta(?![a-zA-Z])/g, 'δ'],
  [/\\varepsilon(?![a-zA-Z])/g, 'ε'],
  [/\\epsilon(?![a-zA-Z])/g, 'ε'],
  [/\\zeta(?![a-zA-Z])/g, 'ζ'],
  [/\\eta(?![a-zA-Z])/g, 'η'],
  [/\\theta(?![a-zA-Z])/g, 'θ'],
  [/\\iota(?![a-zA-Z])/g, 'ι'],
  [/\\kappa(?![a-zA-Z])/g, 'κ'],
  [/\\lambda(?![a-zA-Z])/g, 'λ'],
  [/\\mu(?![a-zA-Z])/g, 'μ'],
  [/\\nu(?![a-zA-Z])/g, 'ν'],
  [/\\xi(?![a-zA-Z])/g, 'ξ'],
  [/\\pi(?![a-zA-Z])/g, 'π'],
  [/\\rho(?![a-zA-Z])/g, 'ρ'],
  [/\\sigma(?![a-zA-Z])/g, 'σ'],
  [/\\tau(?![a-zA-Z])/g, 'τ'],
  [/\\upsilon(?![a-zA-Z])/g, 'υ'],
  [/\\varphi(?![a-zA-Z])/g, 'φ'],
  [/\\phi(?![a-zA-Z])/g, 'φ'],
  [/\\chi(?![a-zA-Z])/g, 'χ'],
  [/\\psi(?![a-zA-Z])/g, 'ψ'],
  [/\\omega(?![a-zA-Z])/g, 'ω'],
  [/\\Gamma(?![a-zA-Z])/g, 'Γ'],
  [/\\Delta(?![a-zA-Z])/g, 'Δ'],
  [/\\Theta(?![a-zA-Z])/g, 'Θ'],
  [/\\Lambda(?![a-zA-Z])/g, 'Λ'],
  [/\\Xi(?![a-zA-Z])/g, 'Ξ'],
  [/\\Pi(?![a-zA-Z])/g, 'Π'],
  [/\\Sigma(?![a-zA-Z])/g, 'Σ'],
  [/\\Phi(?![a-zA-Z])/g, 'Φ'],
  [/\\Psi(?![a-zA-Z])/g, 'Ψ'],
  [/\\Omega(?![a-zA-Z])/g, 'Ω'],

  // Functions
  [/\\log(?![a-zA-Z])/g, 'log'],
  [/\\ln(?![a-zA-Z])/g, 'ln'],
  [/\\sin(?![a-zA-Z])/g, 'sin'],
  [/\\cos(?![a-zA-Z])/g, 'cos'],
  [/\\tan(?![a-zA-Z])/g, 'tan'],
  [/\\min(?![a-zA-Z])/g, 'min'],
  [/\\max(?![a-zA-Z])/g, 'max'],
  [/\\exp(?![a-zA-Z])/g, 'exp'],
  [/\\lim(?![a-zA-Z])/g, 'lim']
];

function convertLatexToHtml(tex) {
  if (!tex || typeof tex !== 'string') return '';
  let res = escapeHtml(tex.trim());

  // Formatting & text functions: \text{...}, \mathrm{...}, \mathbf{...}
  res = res.replace(/\\(?:text|mathrm|mathbf)\{([^}]*)\}/g, '$1');

  // Fractions: \frac{a}{b} -> <span class="math-frac"><sup>a</sup>⁄<sub>b</sub></span>
  while (/\\frac\{([^{}]+)\}\{([^{}]+)\}/.test(res)) {
    res = res.replace(/\\frac\{([^{}]+)\}\{([^{}]+)\}/g, '<span class="math-frac"><sup>$1</sup>⁄<sub>$2</sub></span>');
  }

  // Square roots: \sqrt{a} -> √(a)
  while (/\\sqrt\{([^{}]+)\}/.test(res)) {
    res = res.replace(/\\sqrt\{([^{}]+)\}/g, '√($1)');
  }

  // Symbol and macro replacements
  for (const [re, repl] of LATEX_SYMBOL_REPLACEMENTS) {
    res = res.replace(re, repl);
  }

  // Superscripts & subscripts
  res = res.replace(/\^{([^}]+)}/g, '<sup>$1</sup>');
  res = res.replace(/\^([a-zA-Z0-9+-])/g, '<sup>$1</sup>');
  res = res.replace(/_{([^}]+)}/g, '<sub>$1</sub>');
  res = res.replace(/_([a-zA-Z0-9+-])/g, '<sub>$1</sub>');

  // Delimiter and spacing cleanup
  res = res.replace(/\\([{}])/g, '$1');
  res = res.replace(/\\([,;:!])/g, ' ');
  res = res.replace(/\\\s/g, ' ');
  res = res.replace(/\\\$/g, '$');

  return res;
}

function isLikelyLatexMath(inner) {
  if (!inner || typeof inner !== 'string') return false;
  // If starts or ends with whitespace, not genuine math
  if (inner.startsWith(' ') || inner.endsWith(' ') || inner.startsWith('\t') || inner.endsWith('\t')) {
    return false;
  }
  // If looks like currency: e.g. $50, $50.00, $50k, $50 USD
  if (/^\d+(?:[.,]\d+)*(?:\s*(?:k|m|b|usd|eur|gbp|dollars?|cents?))?$/i.test(inner)) {
    return false;
  }
  // Indicators of genuine LaTeX math:
  // 1. Contains LaTeX command like \rightarrow, \times, \frac, \alpha, etc.
  if (/\\[a-zA-Z]+/.test(inner)) return true;
  // 2. Contains superscript or subscript syntax
  if (/[\^_][{a-zA-Z0-9+-]/.test(inner)) return true;
  // 3. Contains Big-O notation e.g. O(n log n), O(1), O(n^2), O(V + E)
  if (/^O\([^)]+\)$/.test(inner)) return true;
  // 4. Common math relations or operators
  if (/[=<>≤≥±≠≈×÷∈∉⊂⊆∪∩∑∏∫√]/.test(inner)) return true;

  return false;
}

function preprocessLatexMath(text) {
  if (!text || typeof text !== 'string' || !text.includes('$')) return text || '';

  // 1. Convert display math blocks: $$([\s\S]+?)$$
  let result = text.replace(/\$\$([\s\S]+?)\$\$/g, (match, inner) => {
    return `<div class="math-block">${convertLatexToHtml(inner)}</div>`;
  });

  // 2. Convert inline math spans: $((?:\\\$|[^$\r\n])+?)$
  result = result.replace(/(^|[^\\])\$((?:\\\$|[^$\r\n])+?)\$/g, (match, prefix, inner) => {
    if (isLikelyLatexMath(inner)) {
      return `${prefix}<span class="math-inline">${convertLatexToHtml(inner)}</span>`;
    }
    return match;
  });

  return result;
}

const ALERT_CONFIG = {
  NOTE: { title: 'Note', icon: 'ℹ️', className: 'markdown-alert-note' },
  TIP: { title: 'Tip', icon: '💡', className: 'markdown-alert-tip' },
  IMPORTANT: { title: 'Important', icon: '⚡', className: 'markdown-alert-important' },
  WARNING: { title: 'Warning', icon: '⚠️', className: 'markdown-alert-warning' },
  CAUTION: { title: 'Caution', icon: '🛑', className: 'markdown-alert-caution' }
};

function processMarkdownAlerts(html) {
  if (!html || typeof html !== 'string' || !/\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/i.test(html)) {
    return html || '';
  }
  return html.replace(/<blockquote\b[^>]*>([\s\S]*?)<\/blockquote>/gi, (match, inner) => {
    const alertMatch = inner.match(/^\s*<p>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\](?:\s*<br\s*\/?>)?(?:[ \t]*\r?\n|[ \t]+)?([\s\S]*)/i);
    if (!alertMatch) return match;
    const alertType = alertMatch[1].toUpperCase();
    const config = ALERT_CONFIG[alertType];
    if (!config) return match;
    let restOfFirstPara = alertMatch[2] || '';
    let body = restOfFirstPara;
    if (body.trim().startsWith('</p>')) {
      body = body.trim().slice(4).trim();
    } else {
      body = '<p>' + body;
    }
    body = body.replace(/<p>\s*<\/p>/g, '').trim();

    return `<div class="markdown-alert ${config.className}">\n  <div class="markdown-alert-title"><span class="markdown-alert-icon">${config.icon}</span> ${config.title}</div>\n  <div class="markdown-alert-body">${body}</div>\n</div>`;
  });
}

function preprocessMarkdownExtensions(content) {
  if (!content || typeof content !== 'string') return '';
  // Fast bailout: if no carousel, slide, dollar (math), or alert indicator, return content immediately
  if (!/`{3,4}carousel|<!--\s*slide|\$|\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/i.test(content)) {
    return content;
  }

  // Mask non-carousel code blocks and inline code spans in a single pass
  const codeTokens = [];
  // 1. Mask non-carousel fenced code blocks
  let masked = content.replace(/(^|\r?\n)(`{3,}|~{3,})(?![`~]|carousel\b)[^\n]*\r?\n[\s\S]*?\r?\n\2(?=\r?\n|$)/gi, (m, prefix) => {
    const idx = codeTokens.length;
    codeTokens.push(m.slice(prefix.length));
    return prefix + `%%MD_CODE_TOKEN_${idx}%%`;
  });

  // 2. Mask inline code spans `...`
  masked = masked.replace(/(`+)(?:[^\r\n`]|(?!\1)`)+\1/g, (m) => {
    const idx = codeTokens.length;
    codeTokens.push(m);
    return `%%MD_CODE_TOKEN_${idx}%%`;
  });

  // 1. Process LaTeX Math
  if (masked.includes('$')) {
    masked = preprocessLatexMath(masked);
  }

  // 2. Process Carousel & Unfenced <!-- slide --> blocks
  if (/`{3,4}carousel|<!--\s*slide/i.test(masked)) {
    const unmaskFn = (str) => {
      if (!str || typeof str !== 'string' || codeTokens.length === 0) return str || '';
      return str.replace(/%%MD_CODE_TOKEN_(\d+)%%/g, (m, id) => {
        const val = codeTokens[parseInt(id, 10)];
        return val !== undefined ? val : m;
      });
    };
    masked = preprocessCarouselBlocks(masked, unmaskFn);
  }

  // Unmask remaining code tokens
  if (codeTokens.length > 0) {
    masked = masked.replace(/%%MD_CODE_TOKEN_(\d+)%%/g, (m, id) => {
      const val = codeTokens[parseInt(id, 10)];
      return val !== undefined ? val : m;
    });
  }

  return masked;
}

function renderMarkdown(content) {
  if (!content || typeof content !== 'string') return '';
  if (!window.marked) {
    return `<pre>${escapeHtml(content || '')}</pre>`;
  }
  configureMarked();
  const preprocessed = preprocessMarkdownExtensions(content);
  const raw = window.marked.parse(preprocessed || '');
  const linked = ensureExternalLinksTargetBlank(raw);
  const alerted = processMarkdownAlerts(linked);
  return sanitizeRenderedMarkdownHtml(alerted);
}

function ensureDoubleNewlineSeparation(prev, next) {
  if (!prev) return next || '';
  if (!next) return prev || '';
  const prevEndsWithNL = prev.endsWith('\n\n') ? 2 : (prev.endsWith('\n') ? 1 : 0);
  const nextStartsWithNL = next.startsWith('\n\n') ? 2 : (next.startsWith('\n') ? 1 : 0);
  const totalNL = prevEndsWithNL + nextStartsWithNL;
  if (totalNL >= 2) {
    return prev + next;
  }
  if (totalNL === 1) {
    return prev + '\n' + next;
  }
  return prev + '\n\n' + next;
}

function isLocalHost(hostName) {
  if (!hostName || hostName === 'local') return true;
  if (state.selfHost && (hostName === state.selfHost.name || hostName === state.selfHost.displayName)) return true;
  return false;
}

function findHostRecord(hostName) {
  if (!hostName || isLocalHost(hostName)) return null;
  return (state.hosts || []).find(h =>
    h.name === hostName ||
    h.name.endsWith('@' + hostName) ||
    (h.displayName && h.displayName.toLowerCase() === hostName.toLowerCase())
  ) || null;
}

function sessionBelongsToHost(sess, hostRecord) {
  if (!sess || !hostRecord) return true;
  const sHost = (sess.host || '').trim();
  if (!sHost || sHost.toLowerCase() === 'local') return true;
  if (isLocalHost(hostRecord.name)) {
    return isLocalHost(sHost);
  }
  if (isLocalHost(sHost)) {
    return false;
  }
  const sLower = sHost.toLowerCase();
  const candidates = [
    hostRecord.name,
    hostRecord.reportedHost,
    hostRecord.displayName,
    hostRecord.display_name,
    hostRecord.ssh_target
  ];
  for (const cand of candidates) {
    const cLower = (cand || '').trim().toLowerCase();
    if (!cLower || cLower === 'local') continue;
    if (cLower === sLower || cLower.endsWith('@' + sLower) || sLower.endsWith('@' + cLower)) {
      return true;
    }
  }
  const matched = findHostRecord(sHost);
  if (matched) {
    return matched.name === hostRecord.name;
  }
  return !hostRecord.reportedHost;
}

function getSessionBaseUrl(sessionId, sessionObj) {
  const sess = sessionObj || (state.sessions && state.sessions.find(s => s.id === sessionId));
  if (!sess) return '';
  if (sess.hostUrl) return sess.hostUrl.replace(/\/$/, '');
  const sessionHost = sess.host || '';
  if (isLocalHost(sessionHost)) return '';
  const hostRec = findHostRecord(sessionHost);
  if (hostRec && hostRec.url && !isLocalHost(hostRec.name)) {
    return hostRec.url.replace(/\/$/, '');
  }
  return '';
}

function getSelfHostName() {
  return (state.selfHost && state.selfHost.name) ? state.selfHost.name : 'local';
}

function getSelfDisplayName() {
  return (state.selfHost && state.selfHost.displayName) ? state.selfHost.displayName : getSelfHostName();
}

// Format host name to server name or display name
function formatHostLabel(hostName) {
  if (!hostName || isLocalHost(hostName)) return getSelfDisplayName();
  const hostRec = findHostRecord(hostName);
  if (hostRec && hostRec.displayName) return hostRec.displayName;
  const parts = hostName.split('@');
  return parts[parts.length - 1] || hostName;
}

// Format relative timestamp (e.g. "2m", "1h", "yesterday", "Aug 18")
function formatRelativeTime(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  if (isNaN(d.getTime()) || d.getTime() === 0) return '';
  const now = new Date();
  const diffSec = Math.floor((now - d) / 1000);
  if (diffSec < 0) return 'just now';
  if (diffSec < 60) return `${diffSec}s`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour}h`;
  const diffDays = Math.floor(diffHour / 24);
  if (diffDays === 1) return 'yesterday';
  if (diffDays < 7) return `${diffDays}d`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// Format absolute date and time (e.g. "Aug 18, 2026, 10:45 AM")
function formatFullDateTime(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  if (isNaN(d.getTime()) || d.getTime() === 0) return '';
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  });
}

// Open Path in VS Code (Local or Remote)
async function openInVSCode(cwd, host = 'local') {
  if (!cwd) {
    alert('Workspace directory is empty.');
    return;
  }
  const isRemote = !isLocalHost(host);
  const hostLabel = isRemote ? (host || 'local') : getSelfHostName();
  let cleanPath = cwd;
  let lineSuffix = '';
  const hashMatch = cwd.match(/#L(\d+)(?:-L?(\d+))?$/i);
  const colonMatch = cwd.match(/:(\d+)(?:([-:])(\d+))?$/);
  if (hashMatch) {
    cleanPath = cwd.slice(0, hashMatch.index);
    lineSuffix = `:${hashMatch[1]}`;
  } else if (colonMatch) {
    cleanPath = cwd.slice(0, colonMatch.index);
    if (colonMatch[2] === ':') {
      lineSuffix = `:${colonMatch[1]}:${colonMatch[3]}`;
    } else {
      lineSuffix = `:${colonMatch[1]}`;
    }
  }
  const formattedPath = cleanPath.startsWith('/') ? cleanPath : '/' + cleanPath;
  const directUri = isRemote 
    ? `vscode://vscode-remote/ssh-remote+${hostLabel}${formattedPath}${lineSuffix}` 
    : `vscode://file${formattedPath}${lineSuffix}`;

  try {
    const res = await fetch('/v1/editor/open', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: cwd, host: isRemote ? host : getSelfHostName() })
    });
    if (res.ok) {
      const data = await res.json().catch(() => ({}));
      const targetUri = data.uri || directUri;
      // Direct anchor click to ensure browser triggers URL handler
      const a = document.createElement('a');
      a.href = targetUri;
      a.style.display = 'none';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
    } else if (res.status === 404) {
      const errText = await res.text().catch(() => 'File or directory not found');
      showUploadToast(`Cannot open in VS Code: ${errText}`, 'error');
    } else {
      window.location.href = directUri;
    }
  } catch (err) {
    window.location.href = directUri;
  }
}


// Session State Helpers
function getStateText(session) {
  if (!session) return 'Unknown';
  switch (session.state) {
    case 1: return 'Working';
    case 2: return 'Blocked';
    case 3: return 'Idle';
    case 4: return 'Ended';
    case 5: return 'Failed';
    default: return session.managed ? 'Idle' : 'Standby';
  }
}

function getStateRawEmoji(session) {
  if (!session) return '◌';
  switch (session.state) {
    case 1: return '⚙️';
    case 2: return '❓';
    case 3: return '✅';
    case 4: return '⏹️';
    case 5: return '🛑';
    default: return session.managed ? '✅' : '◌';
  }
}

function getStateEmoji(session) {
  if (!session) return '<span title="Standby / Unknown">◌</span>';
  switch (session.state) {
    case 1: // StateWorking (work in progress - agent is working)
      return '<span class="state-working-spinner" title="Working (generating / executing tools)">⚙️</span>';
    case 2: // StateBlocked (waiting for feedback - agent is asking a question and waiting for response)
      return '<span title="Blocked (waiting for feedback / authorization)">❓</span>';
    case 3: // StateIdle (idle - ready for next prompt)
      return '<span title="Idle (ready for prompt)">✅</span>';
    case 4: // StateEnded (session terminated / exited)
      return '<span title="Ended (process terminated)">⏹️</span>';
    case 5: // StateFailed (process crashed or error)
      return '<span title="Failed (process error / crashed)">🛑</span>';
    default:
      return session.managed ? '<span title="Idle">✅</span>' : '<span title="Standby / Unknown">◌</span>';
  }
}

// Agent Provider Badge Helper
function getAgentBadgeHtml(agent, iconOnly = false) {
  const a = (agent || 'claude-code').toLowerCase();

  // 1. Dynamic lookup from registered providers
  if (state.providers && state.providers.length > 0) {
    const p = state.providers.find(prov => prov.agent.toLowerCase() === a || a.includes(prov.agent.toLowerCase()));
    if (p) {
      const svg = p.icon_svg || `<svg class="agent-logo-svg" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="16" rx="2"/></svg>`;
      return `<span class="agent-icon-badge ${p.agent}" title="${p.display_name || p.agent}">${svg}${iconOnly ? '' : ' ' + (p.display_name || p.agent)}</span>`;
    }
  }

  // 2. Built-in Fallbacks
  if (a.includes('claude')) {
    const svg = `<svg class="agent-logo-svg claude-logo" viewBox="0 0 24 24" width="12" height="12" fill="currentColor"><path clip-rule="evenodd" fill-rule="evenodd" d="M20.998 10.949H24v3.102h-3v3.028h-1.487V20H18v-2.921h-1.487V20H15v-2.921H9V20H7.488v-2.921H6V20H4.487v-2.921H3V14.05H0V10.95h3V5h17.998v5.949zM6 10.949h1.488V8.102H6v2.847zm10.51 0H18V8.102h-1.49v2.847z"/></svg>`;
    return `<span class="agent-icon-badge claude-code" title="Claude Code (Anthropic)">${svg}${iconOnly ? '' : ' claude'}</span>`;
  } else if (a.includes('antigravity') || a.includes('agy') || a.includes('gemini')) {
    const svg = `<svg class="agent-logo-svg antigravity-logo" viewBox="0 0 24 24" width="12" height="12"><defs><linearGradient id="agGrad" x1="0%" y1="0%" x2="100%" y2="100%"><stop offset="0%" stop-color="#EA4335"/><stop offset="35%" stop-color="#FBBC04"/><stop offset="70%" stop-color="#34A853"/><stop offset="100%" stop-color="#4285F4"/></linearGradient></defs><path fill="url(#agGrad)" d="M12 0.8 C8.2 0.8 5.6 3.2 4.1 7.8 C2.6 12.5 1.2 18.2 0.3 22.4 C-0.1 23.6 0.8 24.2 1.6 23.6 C4.8 21.2 7.8 14.2 12 12.4 C16.2 14.2 19.2 21.2 22.4 23.6 C23.2 24.2 24.1 23.6 23.7 22.4 C22.8 18.2 21.4 12.5 19.9 7.8 C18.4 3.2 15.8 0.8 12 0.8 Z"/></svg>`;
    return `<span class="agent-icon-badge antigravity" title="Google Antigravity">${svg}${iconOnly ? '' : ' antigravity'}</span>`;
  } else if (a.includes('codex') || a.includes('openai')) {
    const svg = `<svg class="agent-logo-svg codex-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a4.5 4.5 0 0 1 4.2 2.9l.4.9a4.5 4.5 0 0 1 1.8 4.7l-.2 1a4.5 4.5 0 0 1-2.4 4.1l-.8.5a4.5 4.5 0 0 1-4.7 1.8l-1-.2a4.5 4.5 0 0 1-4.1-2.4l-.5-.8a4.5 4.5 0 0 1-1.8-4.7l.2-1a4.5 4.5 0 0 1 2.4-4.1l.8-.5A4.5 4.5 0 0 1 12 2z"/><path d="M12 6.5l3.5 2v4l-3.5 2l-3.5-2v-4z"/><path d="M8.5 8.5L5 10.5M15.5 8.5L19 10.5M12 14.5V18.5"/></svg>`;
    return `<span class="agent-icon-badge codex" title="OpenAI Codex">${svg}${iconOnly ? '' : ' codex'}</span>`;
  }
  const svg = `<svg class="agent-logo-svg generic-logo" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><line x1="7" y1="8" x2="10" y2="8"/><line x1="7" y1="12" x2="17" y2="12"/></svg>`;
  return `<span class="agent-icon-badge" title="Agent: ${agent}">${svg}${iconOnly ? '' : ' ' + agent}</span>`;
}

function formatAgentChatName(agent) {
  const a = (agent || 'claude-code').toLowerCase();
  if (a.includes('claude')) return 'Claude Code';
  if (a.includes('antigravity') || a.includes('agy') || a.includes('gemini')) return 'Google Antigravity';
  if (a.includes('codex') || a.includes('openai')) return 'OpenAI Codex';
  return agent || 'Claude Code';
}

// DOM Elements

function escapeHtml(str) {
  if (!str) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

// Show Project Documents Auto-Discovery Modal with Real-Time Search and Categorization

function translatePathForHost(currentPath, fromHost, toHost) {
  if (!currentPath || fromHost === toHost) return currentPath;
  const effFrom = isLocalHost(fromHost) ? getSelfHostName() : fromHost;
  const effTo = isLocalHost(toHost) ? getSelfHostName() : toHost;
  if (effFrom === effTo) return currentPath;

  // 1. Direct Project Key Matching on target host
  const srcSess = (state.sessions || []).find(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effFrom && s.cwd === currentPath);
  if (srcSess && srcSess.project_key) {
    const tgtSess = (state.sessions || []).find(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effTo && s.project_key === srcSess.project_key && s.cwd);
    if (tgtSess && tgtSess.cwd) return tgtSess.cwd;
  }

  // 2. Project Directory Basename Matching on target host
  const cleanPath = currentPath.replace(/\/+$/, '');
  const currentBase = cleanPath.split('/').pop();
  if (currentBase) {
    const tgtMatch = (state.sessions || []).find(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effTo && s.cwd && (s.cwd.endsWith('/' + currentBase) || s.cwd.split('/').pop() === currentBase));
    if (tgtMatch && tgtMatch.cwd) return tgtMatch.cwd;

    // Check target host configured remote_cwd
    const targetHostRec = (state.hosts || []).find(h => h.name === toHost);
    if (targetHostRec && targetHostRec.remote_cwd && !isLocalHost(toHost)) {
      const root = targetHostRec.remote_cwd.replace(/\/+$/, '');
      return `${root}/${currentBase}`;
    }
  }

  // 3. Standard Home Prefix Translation (macOS /Users/<user> <-> Linux /home/<user>)
  if ((isLocalHost(fromHost) || currentPath.startsWith('/Users/')) && !isLocalHost(toHost)) {
    // macOS -> Linux
    if (currentPath.startsWith('/Users/')) {
      return currentPath.replace(/^\/Users\/([^/]+)/, '/home/$1');
    }
  } else if (!isLocalHost(fromHost) && isLocalHost(toHost)) {
    // Linux -> macOS
    if (currentPath.startsWith('/home/')) {
      return currentPath.replace(/^\/home\/([^/]+)/, '/Users/$1');
    }
  }

  return currentPath;
}

// Group Memory Storage Key
const GROUP_PREFERENCES_KEY = 'ackbar_group_preferences_v1';

function getStoredGroupPreferences() {
  try {
    const raw = localStorage.getItem(GROUP_PREFERENCES_KEY);
    return raw ? JSON.parse(raw) : {};
  } catch (e) {
    console.warn('Failed to parse group preferences:', e);
    return {};
  }
}

function saveStoredGroupPreferences(prefs) {
  try {
    localStorage.setItem(GROUP_PREFERENCES_KEY, JSON.stringify(prefs));
  } catch (e) {
    console.warn('Failed to save group preferences:', e);
  }
}

// Record a spawn in group memory
function recordGroupSpawn({ group, host, agent, cwd, account }) {
  if (!host) host = getSelfHostName();
  if (!agent) agent = 'claude-code';
  if (!cwd) return;

  const groupKey = group || '__default__';
  const allPrefs = getStoredGroupPreferences();
  if (!allPrefs[groupKey]) {
    allPrefs[groupKey] = {
      preferred_host: host,
      by_host: {}
    };
  }

  const g = allPrefs[groupKey];
  g.preferred_host = host;
  if (!g.by_host) g.by_host = {};
  if (!g.by_host[host]) {
    g.by_host[host] = {
      agent: agent,
      recent_paths: [],
      counts: {}
    };
  }

  const hostEntry = g.by_host[host];
  hostEntry.agent = agent;
  if (account !== undefined) {
    hostEntry.account = account;
  }
  hostEntry.last_used_at = Date.now();

  const filteredPaths = (hostEntry.recent_paths || []).filter(p => p !== cwd);
  hostEntry.recent_paths = [cwd, ...filteredPaths].slice(0, 3);

  if (!hostEntry.counts) hostEntry.counts = {};
  hostEntry.counts[cwd] = (hostEntry.counts[cwd] || 0) + 1;

  saveStoredGroupPreferences(allPrefs);
}

// Get preferences for a given group and target host
function getGroupPreferences(groupPath, targetHost = null) {
  const groupKey = groupPath || '__default__';
  const allPrefs = getStoredGroupPreferences();
  const storedGroup = allPrefs[groupKey];

  // Filter matching sessions from state.sessions
  let groupSessions = (state.sessions || []).filter(s => s.node_path === groupPath);
  if (groupSessions.length === 0 && groupPath) {
    groupSessions = (state.sessions || []).filter(s => s.node_path && s.node_path.startsWith(groupPath));
  }
  const sortedGroupSessions = sortSessionsByInteraction(groupSessions);

  // 1. Determine Preferred Host
  let preferredHost = '';
  if (storedGroup && storedGroup.preferred_host) {
    preferredHost = storedGroup.preferred_host;
  } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].host) {
    preferredHost = sortedGroupSessions[0].host;
  } else {
    const node = (state.treeNodes || []).find(n => n.path === groupPath);
    if (node && node.host) {
      preferredHost = node.host;
    } else {
      preferredHost = targetHost || getSelfHostName();
    }
  }

  const effectiveHost = targetHost || preferredHost || getSelfHostName();
  const hostSessions = sortedGroupSessions.filter(s => (isLocalHost(s.host) ? getSelfHostName() : s.host) === effectiveHost);

  // 2. Determine Preferred Agent
  let preferredAgent = '';
  if (storedGroup && storedGroup.by_host && storedGroup.by_host[effectiveHost] && storedGroup.by_host[effectiveHost].agent) {
    preferredAgent = storedGroup.by_host[effectiveHost].agent;
  } else if (hostSessions.length > 0 && hostSessions[0].agent) {
    preferredAgent = hostSessions[0].agent;
  } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].agent) {
    preferredAgent = sortedGroupSessions[0].agent;
  } else {
    preferredAgent = 'claude-code';
  }

  // 3. Determine Preferred Account
  let preferredAccount = '';
  if (storedGroup && storedGroup.by_host && storedGroup.by_host[effectiveHost] && storedGroup.by_host[effectiveHost].account) {
    preferredAccount = storedGroup.by_host[effectiveHost].account;
  } else if (hostSessions.length > 0 && hostSessions[0].account_id) {
    preferredAccount = hostSessions[0].account_id;
  } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].account_id) {
    preferredAccount = sortedGroupSessions[0].account_id;
  }

  // 4. Determine Recent Paths and Preferred Path
  let recentPaths = [];
  if (storedGroup && storedGroup.by_host && storedGroup.by_host[effectiveHost] && Array.isArray(storedGroup.by_host[effectiveHost].recent_paths)) {
    recentPaths = [...storedGroup.by_host[effectiveHost].recent_paths];
  }
  hostSessions.forEach(s => {
    if (s.cwd && !recentPaths.includes(s.cwd)) {
      recentPaths.push(s.cwd);
    }
  });
  recentPaths = recentPaths.slice(0, 3);

  let preferredPath = recentPaths.length > 0 ? recentPaths[0] : '';
  if (!preferredPath && groupPath) {
    const node = (state.treeNodes || []).find(n => n.path === groupPath && n.project_dir);
    if (node && node.project_dir) {
      preferredPath = translatePathForHost(node.project_dir, node.host || 'local', effectiveHost);
    } else {
      const childNode = (state.treeNodes || []).find(n => n.path.startsWith(groupPath + '/') && n.project_dir);
      if (childNode && childNode.project_dir) {
        preferredPath = translatePathForHost(childNode.project_dir, childNode.host || 'local', effectiveHost);
      } else if (sortedGroupSessions.length > 0 && sortedGroupSessions[0].cwd) {
        preferredPath = translatePathForHost(sortedGroupSessions[0].cwd, sortedGroupSessions[0].host || 'local', effectiveHost);
      }
    }
  }

  return {
    preferredHost,
    preferredAgent,
    preferredAccount,
    preferredPath,
    recentPaths
  };
}

// Helper: Get relevant folders for a specific host
function getFoldersForHost(targetHost) {
  const isTargetLocal = isLocalHost(targetHost);
  const hostSessions = (state.sessions || []).filter(s => (isTargetLocal ? isLocalHost(s.host) : s.host === targetHost) && s.cwd);
  const hostCwds = new Set(hostSessions.map(s => s.cwd));

  // Also include tree node linked project dirs for this host or generic
  (state.treeNodes || []).forEach(n => {
    if (n.project_dir && (!n.host || (isTargetLocal ? isLocalHost(n.host) : n.host === targetHost))) {
      hostCwds.add(n.project_dir);
    }
  });

  // Check host configured remote_cwd
  const targetHostRec = (state.hosts || []).find(h => h.name === targetHost);
  if (targetHostRec && targetHostRec.remote_cwd) {
    hostCwds.add(targetHostRec.remote_cwd);
  }

  // If target host has few or no session history yet, translate known paths from other hosts
  const allUnique = Array.from(new Set((state.sessions || []).map(s => s.cwd).filter(Boolean)));
  allUnique.forEach(p => {
    const translated = translatePathForHost(p, getSelfHostName(), targetHost);
    if (translated) hostCwds.add(translated);
  });

  return Array.from(hostCwds).sort();
}

// New Session Modal Launcher

export {
  markedConfigured,
  openInVSCode,
  configureMarked,
  ensureExternalLinksTargetBlank,
  sanitizeRenderedMarkdownHtml,
  preprocessCarouselBlocks,
  preprocessLatexMath,
  convertLatexToHtml,
  preprocessMarkdownExtensions,
  processMarkdownAlerts,
  renderMarkdown,
  ensureDoubleNewlineSeparation,
  isLocalHost,
  findHostRecord,
  sessionBelongsToHost,
  getSessionBaseUrl,
  getSelfHostName,
  getSelfDisplayName,
  formatHostLabel,
  formatRelativeTime,
  formatFullDateTime,
  getStateText,
  getStateRawEmoji,
  getStateEmoji,
  getAgentBadgeHtml,
  formatAgentChatName,
  escapeHtml,
  formatBytes,
  translatePathForHost,
  GROUP_PREFERENCES_KEY,
  getStoredGroupPreferences,
  saveStoredGroupPreferences,
  recordGroupSpawn,
  getGroupPreferences,
  getFoldersForHost
};
