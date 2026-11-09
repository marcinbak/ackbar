// Interactive Chat Interface & Streaming Transcript
import { state, el, getChatDraft, saveChatDraft, clearChatDraft } from './state.js';
import { getAuthToken } from './auth.js';
import {
  escapeHtml,
  renderMarkdown,
  formatRelativeTime,
  ensureDoubleNewlineSeparation,
  getSessionBaseUrl,
  openInVSCode,
  formatAgentChatName
} from './utils.js';
import { openSessionDetailsTab, openDocViewerTab, openTranscriptViewerTab } from './details.js';
import { openHandoverModal } from './modals.js';
import {
  getOrCreateFileContextMenu,
  hideChatFileContextMenu,
  showChatFileContextMenu,
  activeFileMenuPill,
  fileContextMenuEl
} from './context-menu.js';
import { handleTakeWheel, isAuxiliaryInputFocused, openSessionInTab } from './tabs.js';
import {
  addPendingAttachment,
  renderPendingAttachments,
  showUploadToast
} from './terminal.js';
import { fetchSessions } from './api.js';
import { renderMermaidDiagrams } from './mermaid.js';


function extractPastedAttachmentFiles(clipboardData) {
  if (!clipboardData || !clipboardData.items) return [];
  const files = [];
  for (const item of clipboardData.items) {
    if (item.kind === 'file') {
      const itemType = (item.type || '').toLowerCase();
      if (itemType.startsWith('image/') || itemType === 'application/pdf' || itemType.includes('bmp') || itemType.startsWith('text/') || itemType === 'application/json') {
        const file = item.getAsFile();
        if (file) {
          files.push(file);
        }
      }
    }
  }
  return files;
}

function insertTextIntoPromptInput(inputEl, text) {
  if (!inputEl || !text) return;
  const value = inputEl.value || '';
  const start = typeof inputEl.selectionStart === 'number' ? inputEl.selectionStart : value.length;
  const end = typeof inputEl.selectionEnd === 'number' ? inputEl.selectionEnd : value.length;
  inputEl.value = value.slice(0, start) + text + value.slice(end);
  const caretPos = start + text.length;
  if (typeof inputEl.setSelectionRange === 'function') {
    try { inputEl.setSelectionRange(caretPos, caretPos); } catch (_) {}
  }
  inputEl.style.height = 'auto';
  inputEl.style.height = Math.min(inputEl.scrollHeight, 180) + 'px';
  inputEl.dispatchEvent(new Event('input', { bubbles: true }));
  inputEl.focus();
}

// --- Phase 4: Voice Companion & 2-Tier Lazy Speech Pipeline ---

let activeSpeech = null; // { tabObj, msgEl, utterance }
let activeRecognition = null;

function isChatAutoReadEnabled() {
  if (typeof localStorage === 'undefined') return false;
  return localStorage.getItem('ackbar_voice_auto_read') === 'true';
}

function isSimpleSpeechCandidate(md) {
  if (!md || typeof md !== 'string') return true;
  // 1. Strip fenced code blocks
  const withoutCode = md.replace(/```[\s\S]*?```/g, ' ').trim();
  if (!withoutCode) return true;

  // 2. Count words
  const words = withoutCode.split(/\s+/).filter(Boolean);
  if (words.length >= 120) {
    return false;
  }

  // 3. Count table rows (lines containing | that are not separator lines)
  const lines = withoutCode.split('\n');
  let tableRows = 0;
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      if (/^\|[-:\s|]+\|$/.test(trimmed)) {
        continue;
      }
      tableRows++;
    }
  }
  if (tableRows > 3) {
    return false;
  }

  // 4. Count markdown ## / ### section headings
  let headings = 0;
  for (const line of lines) {
    if (/^#{2,3}\s+/.test(line.trim())) {
      headings++;
    }
  }
  if (headings >= 3) {
    return false;
  }

  return true;
}

function sanitizeMarkdownForSpeech(md) {
  if (!md || typeof md !== 'string') return '';
  let text = md;

  // 1. Strip fenced code blocks & diffs
  text = text.replace(/```[\s\S]*?```/g, ' ');

  // 2. Strip Mermaid blocks and ASCII box-drawing diagrams
  const lines = text.split('\n');
  const filteredLines = [];
  const boxCharsRegex = /[┌┐└┘─│├┤┬┴┼═║╔╗╚╝╠╣╦╩╬]/;

  let inTable = false;
  let tableHeaderCols = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    // Skip ASCII box drawing diagrams
    if (boxCharsRegex.test(trimmed)) {
      continue;
    }

    // Small table conversion (<= 3 data rows)
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      if (/^\|[-:\s|]+\|$/.test(trimmed)) {
        continue;
      }
      const cells = trimmed.split('|').slice(1, -1).map(c => c.trim());
      if (!inTable) {
        inTable = true;
        tableHeaderCols = cells;
      } else {
        const rowSentences = [];
        for (let c = 0; c < cells.length; c++) {
          const colName = tableHeaderCols[c] || `Column ${c + 1}`;
          const val = cells[c];
          if (val) {
            rowSentences.push(`${colName}: ${val}`);
          }
        }
        if (rowSentences.length > 0) {
          filteredLines.push(rowSentences.join(', ') + '.');
        }
      }
      continue;
    } else {
      inTable = false;
    }

    filteredLines.push(line);
  }
  text = filteredLines.join('\n');

  // 3. Shorten Markdown links with file:// or file paths to their basename
  text = text.replace(/\[([^\]]+)\]\((?:file:\/\/)?([^)]+)\)/g, (match, label, urlPath) => {
    if (urlPath.includes('/') || urlPath.startsWith('file:')) {
      const cleanPath = urlPath.replace(/^file:\/\//, '').split('#')[0].split('?')[0];
      const base = cleanPath.split('/').filter(Boolean).pop();
      if (base) {
        return base;
      }
    }
    return label;
  });

  // Shorten raw absolute file paths
  text = text.replace(/(?:^|\s)(?:\/[a-zA-Z0-9_\.\-]+)+\/([a-zA-Z0-9_\.\-]+(?:\.[a-zA-Z0-9]+)?)/g, ' $1');

  // 4. Strip or shorten UUIDs and 40-char Git SHA hashes
  text = text.replace(/\b[0-9a-f]{40}\b/gi, 'commit');
  text = text.replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, '');

  // 5. Strip residual Markdown formatting
  text = text.replace(/>\s*\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/gi, '');
  text = text.replace(/^\s*>\s?/gm, '');
  text = text.replace(/^\s*#{1,6}\s+/gm, '');
  text = text.replace(/[*_~]{1,3}([^*_~]+)[*_~]{1,3}/g, '$1');
  text = text.replace(/`([^`]+)`/g, '$1');
  text = text.replace(/^\s*[-*+]\s+/gm, '');
  text = text.replace(/^\s*\d+[\.\)]\s+/gm, '');
  text = text.replace(/<[^>]+>/g, ' ');

  // 6. Normalize whitespace
  text = text.replace(/\r\n/g, '\n');
  text = text.replace(/\n+/g, ' ');
  text = text.replace(/\s+/g, ' ').trim();

  return text;
}

// Resolve the currently connected DOM element for an assistant message bubble
function resolveLiveMessageEl(tabObj, msgEl, msgIdx) {
  if (msgEl && msgEl.isConnected) return msgEl;
  if (!tabObj || !tabObj.chatMessagesEl) return msgEl;
  const targetIdx = typeof msgIdx === 'number' && !isNaN(msgIdx) ? msgIdx : (msgEl && msgEl.dataset && msgEl.dataset.msgIndex ? parseInt(msgEl.dataset.msgIndex, 10) : NaN);
  if (!isNaN(targetIdx)) {
    const liveEl = tabObj.chatMessagesEl.querySelector(`.chat-msg.assistant-msg[data-msg-index="${targetIdx}"]`);
    if (liveEl) return liveEl;
  }
  const assistantMsgs = tabObj.chatMessagesEl.querySelectorAll('.chat-msg.assistant-msg');
  if (assistantMsgs.length > 0) {
    return assistantMsgs[assistantMsgs.length - 1];
  }
  return msgEl;
}

function stopActiveSpeech() {
  if (typeof window !== 'undefined' && window.speechSynthesis) {
    try {
      window.speechSynthesis.cancel();
    } catch (_) {}
  }
  if (activeSpeech) {
    const liveEl = resolveLiveMessageEl(activeSpeech.tabObj, activeSpeech.msgEl, activeSpeech.msgIdx);
    if (liveEl) {
      liveEl.classList.remove('speaking');
      const stopBtn = liveEl.querySelector('.btn-stop-chat-msg');
      const readBtn = liveEl.querySelector('.btn-read-chat-msg');
      const replayBtn = liveEl.querySelector('.btn-replay-chat-msg');
      if (stopBtn) stopBtn.style.display = 'none';
      if (readBtn) readBtn.style.display = 'inline-flex';
      if (replayBtn) replayBtn.style.display = 'inline-flex';
    }
    activeSpeech = null;
  }
}

function speakText(text, msgEl, tabObj, onComplete) {
  stopActiveSpeech();
  if (!text || typeof window === 'undefined' || !window.speechSynthesis) {
    if (typeof onComplete === 'function') onComplete();
    return;
  }

  const msgIdx = msgEl && msgEl.dataset && msgEl.dataset.msgIndex ? parseInt(msgEl.dataset.msgIndex, 10) : NaN;
  const liveEl = resolveLiveMessageEl(tabObj, msgEl, msgIdx);

  const utterance = new SpeechSynthesisUtterance(text);
  utterance.rate = 1.05;
  utterance.pitch = 1.0;

  activeSpeech = { tabObj, msgEl: liveEl, msgIdx, utterance };

  if (liveEl) {
    liveEl.classList.add('speaking');
    const readBtn = liveEl.querySelector('.btn-read-chat-msg');
    const stopBtn = liveEl.querySelector('.btn-stop-chat-msg');
    if (readBtn) readBtn.style.display = 'none';
    if (stopBtn) stopBtn.style.display = 'inline-flex';
  }

  utterance.onend = () => {
    const currentLiveEl = resolveLiveMessageEl(tabObj, liveEl, msgIdx);
    if (currentLiveEl) {
      currentLiveEl.classList.remove('speaking');
      const readBtn = currentLiveEl.querySelector('.btn-read-chat-msg');
      const stopBtn = currentLiveEl.querySelector('.btn-stop-chat-msg');
      const replayBtn = currentLiveEl.querySelector('.btn-replay-chat-msg');
      if (stopBtn) stopBtn.style.display = 'none';
      if (readBtn) readBtn.style.display = 'inline-flex';
      if (replayBtn) replayBtn.style.display = 'inline-flex';
    }
    activeSpeech = null;
    if (typeof onComplete === 'function') onComplete();
  };

  utterance.onerror = () => {
    const currentLiveEl = resolveLiveMessageEl(tabObj, liveEl, msgIdx);
    if (currentLiveEl) {
      currentLiveEl.classList.remove('speaking');
      const readBtn = currentLiveEl.querySelector('.btn-read-chat-msg');
      const stopBtn = currentLiveEl.querySelector('.btn-stop-chat-msg');
      const replayBtn = currentLiveEl.querySelector('.btn-replay-chat-msg');
      if (stopBtn) stopBtn.style.display = 'none';
      if (readBtn) readBtn.style.display = 'inline-flex';
      if (replayBtn) replayBtn.style.display = 'inline-flex';
    }
    activeSpeech = null;
  };

  try {
    window.speechSynthesis.speak(utterance);
  } catch (e) {
    console.warn('Speech synthesis failed:', e);
  }
}

async function handleReadChatMessage(tabObj, msgEl, forceFull = false) {
  if (!tabObj || !msgEl) return;
  const msgIdx = msgEl.dataset && msgEl.dataset.msgIndex ? parseInt(msgEl.dataset.msgIndex, 10) : 0;
  let liveEl = resolveLiveMessageEl(tabObj, msgEl, msgIdx);
  const rawContent = (liveEl && liveEl.dataset.rawContent) || msgEl.dataset.rawContent || '';
  if (!rawContent) return;

  tabObj.speechCache = tabObj.speechCache || new Map();
  tabObj.lastSpokenMsgEl = liveEl;

  if (forceFull) {
    const sanitized = sanitizeMarkdownForSpeech(rawContent);
    speakText(sanitized, liveEl, tabObj);
    return;
  }

  // 1. Check client cache
  if (tabObj.speechCache.has(msgIdx)) {
    const cached = tabObj.speechCache.get(msgIdx);
    if (cached.suggested_chips && cached.suggested_chips.length > 0) {
      renderSuggestedChips(tabObj, cached.suggested_chips);
    }
    if (cached.tier === 'structural' || cached.tier === 'haiku' || cached.tier === 'llm' || cached.tier === 'api') {
      const fullBtn = liveEl.querySelector('.btn-full-read-chat-msg');
      if (fullBtn) fullBtn.style.display = 'inline-flex';
    }
    speakText(cached.spoken_text, liveEl, tabObj);
    return;
  }

  // 2. Check if simple prose (< 120 words, <= 3 table rows, < 3 headings) -> Tier 1
  if (isSimpleSpeechCandidate(rawContent)) {
    const sanitized = sanitizeMarkdownForSpeech(rawContent);
    tabObj.speechCache.set(msgIdx, {
      spoken_text: sanitized,
      suggested_chips: [],
      tier: 'simple'
    });
    speakText(sanitized, liveEl, tabObj);
    return;
  }

  // 3. Tier 2: Call daemon POST /v1/sessions/summarize-speech
  const sessionId = tabObj.session ? tabObj.session.id : '';
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);

  const readBtn = liveEl.querySelector('.btn-read-chat-msg');
  const origReadHtml = readBtn ? readBtn.innerHTML : '';
  if (readBtn) readBtn.textContent = '⏳ ...';

  try {
    const res = await fetch(`${baseUrl}/v1/sessions/summarize-speech`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(getAuthToken() ? { 'Authorization': `Bearer ${getAuthToken()}` } : {})
      },
      body: JSON.stringify({
        session_id: sessionId,
        message_index: msgIdx,
        content: rawContent
      })
    });

    liveEl = resolveLiveMessageEl(tabObj, liveEl, msgIdx);
    const liveReadBtn = liveEl ? liveEl.querySelector('.btn-read-chat-msg') : readBtn;
    if (liveReadBtn && origReadHtml) liveReadBtn.innerHTML = origReadHtml;

    if (res.ok) {
      const summary = await res.json();
      tabObj.speechCache.set(msgIdx, summary);

      if (summary.suggested_chips && summary.suggested_chips.length > 0) {
        renderSuggestedChips(tabObj, summary.suggested_chips);
      }

      if (liveEl) {
        const fullBtn = liveEl.querySelector('.btn-full-read-chat-msg');
        if (fullBtn) fullBtn.style.display = 'inline-flex';
      }

      speakText(summary.spoken_text, liveEl, tabObj);
      return;
    }
  } catch (err) {
    console.warn('Speech summarizer request failed, falling back to local sanitizer:', err);
  }

  liveEl = resolveLiveMessageEl(tabObj, liveEl, msgIdx);
  const liveReadBtn = liveEl ? liveEl.querySelector('.btn-read-chat-msg') : readBtn;
  if (liveReadBtn && origReadHtml) liveReadBtn.innerHTML = origReadHtml;
  const fallbackSanitized = sanitizeMarkdownForSpeech(rawContent);
  speakText(fallbackSanitized, liveEl, tabObj);
}

function handleReplayChatMessage(tabObj, msgEl) {
  if (!tabObj || !msgEl) return;
  const msgIdx = parseInt(msgEl.dataset.msgIndex, 10) || 0;
  const liveEl = resolveLiveMessageEl(tabObj, msgEl, msgIdx);
  tabObj.speechCache = tabObj.speechCache || new Map();

  if (tabObj.speechCache.has(msgIdx)) {
    const cached = tabObj.speechCache.get(msgIdx);
    speakText(cached.spoken_text, liveEl, tabObj);
  } else {
    handleReadChatMessage(tabObj, liveEl, false);
  }
}

function renderSuggestedChips(tabObj, chips) {
  if (!tabObj || !tabObj.chatViewEl) return;
  const container = tabObj.chatViewEl.querySelector('.chat-suggested-chips');
  if (!container) return;

  if (!chips || !Array.isArray(chips) || chips.length === 0) {
    container.style.display = 'none';
    container.innerHTML = '';
    return;
  }

  container.innerHTML = '';
  container.style.display = 'flex';

  for (const chip of chips) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'chat-suggested-chip';
    btn.textContent = chip;

    btn.addEventListener('click', (e) => {
      e.preventDefault();
      let promptText = chip;
      const isAutoReadOn = isChatAutoReadEnabled();

      if (chip === 'Hear Open Question') {
        promptText = 'What is the open question and what are the trade-offs?';
        if (isAutoReadOn) {
          promptText += ' [Reply concisely in 2–3 spoken sentences]';
        }
      } else if (chip.startsWith('Hear ')) {
        const topic = chip.substring(5).trim();
        promptText = `Tell me about ${topic}`;
        if (isAutoReadOn) {
          promptText += ' [Reply concisely in 2–3 spoken sentences]';
        }
      } else if (chip === 'Approve Plan') {
        promptText = 'Plan looks good, please proceed with implementation.';
      } else if (isAutoReadOn && !chip.includes('[Reply concisely')) {
        promptText += ' [Reply concisely in 2–3 spoken sentences]';
      }

      if (tabObj.chatInputEl) {
        tabObj.chatInputEl.value = promptText;
        tabObj.chatInputEl.dispatchEvent(new Event('input', { bubbles: true }));
        sendChatPrompt(tabObj);
      }
      container.style.display = 'none';
      container.innerHTML = '';
    });

    container.appendChild(btn);
  }
}

function toggleVoiceMic(tabObj) {
  stopActiveSpeech();
  if (!tabObj || !tabObj.chatViewEl) return;
  const micBtn = tabObj.chatViewEl.querySelector('.btn-chat-mic');

  if (activeRecognition) {
    try {
      activeRecognition.stop();
    } catch (_) {}
    activeRecognition = null;
    if (micBtn) {
      micBtn.classList.remove('listening');
      micBtn.textContent = '🎤 Mic: OFF';
    }
    return;
  }

  const SpeechRecognition = typeof window !== 'undefined' &&
    (window.SpeechRecognition || window.webkitSpeechRecognition);

  if (!SpeechRecognition) {
    alert('Speech recognition is not supported in this browser. Please use Chrome, Edge, or Safari.');
    return;
  }

  const recognition = new SpeechRecognition();
  recognition.lang = 'en-US';
  recognition.interimResults = false;
  recognition.maxAlternatives = 1;

  recognition.onstart = () => {
    activeRecognition = recognition;
    if (micBtn) {
      micBtn.classList.add('listening');
      micBtn.textContent = '🎤 Listening...';
    }
  };

  recognition.onresult = (event) => {
    if (!event.results || event.results.length === 0) return;
    const spoken = event.results[0][0].transcript.trim();
    if (!spoken) return;

    const lower = spoken.toLowerCase().replace(/[.,!?;:]+$/, '').trim();
    if (lower === 'repeat that' || lower === 'read that again' || lower === 'repeat' || lower === 'replay') {
      if (tabObj.lastSpokenMsgEl) {
        handleReplayChatMessage(tabObj, tabObj.lastSpokenMsgEl);
      } else {
        const assistantMsgs = tabObj.chatMessagesEl ? tabObj.chatMessagesEl.querySelectorAll('.chat-msg.assistant-msg') : [];
        if (assistantMsgs.length > 0) {
          handleReadChatMessage(tabObj, assistantMsgs[assistantMsgs.length - 1], false);
        }
      }
      return;
    }

    let finalPrompt = spoken;
    if (isChatAutoReadEnabled() && !finalPrompt.includes('[Reply concisely')) {
      finalPrompt += ' [Reply concisely in 2–3 spoken sentences]';
    }

    if (tabObj.chatInputEl) {
      tabObj.chatInputEl.value = finalPrompt;
      tabObj.chatInputEl.dispatchEvent(new Event('input', { bubbles: true }));
      sendChatPrompt(tabObj);
    }
  };

  recognition.onerror = (event) => {
    console.warn('Speech recognition error:', event.error);
    if (micBtn) {
      micBtn.classList.remove('listening');
      micBtn.textContent = '🎤 Mic: OFF';
    }
    activeRecognition = null;
  };

  recognition.onend = () => {
    if (micBtn) {
      micBtn.classList.remove('listening');
      micBtn.textContent = '🎤 Mic: OFF';
    }
    activeRecognition = null;
  };

  try {
    recognition.start();
  } catch (err) {
    console.warn('Failed to start speech recognition:', err);
    if (micBtn) {
      micBtn.classList.remove('listening');
      micBtn.textContent = '🎤 Mic: OFF';
    }
    activeRecognition = null;
  }
}

// --- Antigravity Native Live Voice Bridge (JETSKI-AUDIO/1 over WebSocket) ---

let activeLiveVoiceBridge = null;

function downsampleTo16kInt16(float32Samples, inputSampleRate) {
  const targetRate = 16000;
  if (!inputSampleRate || inputSampleRate === targetRate) {
    const out = new Int16Array(float32Samples.length);
    for (let i = 0; i < float32Samples.length; i++) {
      const s = Math.max(-1, Math.min(1, float32Samples[i]));
      out[i] = s < 0 ? s * 0x8000 : s * 0x7FFF;
    }
    return out;
  }
  const ratio = inputSampleRate / targetRate;
  const newLength = Math.round(float32Samples.length / ratio);
  const out = new Int16Array(newLength);
  let offsetResult = 0;
  let offsetBuffer = 0;
  while (offsetResult < out.length) {
    const nextOffsetBuffer = Math.round((offsetResult + 1) * ratio);
    let accum = 0;
    let count = 0;
    for (let i = offsetBuffer; i < nextOffsetBuffer && i < float32Samples.length; i++) {
      accum += float32Samples[i];
      count++;
    }
    const avg = count > 0 ? accum / count : 0;
    const s = Math.max(-1, Math.min(1, avg));
    out[offsetResult] = s < 0 ? s * 0x8000 : s * 0x7FFF;
    offsetResult++;
    offsetBuffer = nextOffsetBuffer;
  }
  return out;
}

function updateLiveVoiceBtnUI(tabObj, stateLabel, isActive) {
  if (!tabObj || !tabObj.chatLiveVoiceBtn) return;
  const btn = tabObj.chatLiveVoiceBtn;
  btn.textContent = stateLabel || (isActive ? '🎙️ Live Voice: ON' : '🎙️ Live Voice: OFF');
  btn.classList.toggle('active', Boolean(isActive));
  btn.classList.toggle('listening', Boolean(isActive));
}

export function stopAntigravityLiveVoice(sendStopCmd = true) {
  if (!activeLiveVoiceBridge) return;
  const bridge = activeLiveVoiceBridge;
  activeLiveVoiceBridge = null;

  if (sendStopCmd && bridge.ws && bridge.ws.readyState === WebSocket.OPEN && bridge.sessionId) {
    try {
      bridge.ws.send(JSON.stringify({
        type: 'stop_voice',
        session_id: bridge.sessionId
      }));
    } catch (_) {}
  }

  if (bridge.processor) {
    try { bridge.processor.disconnect(); } catch (_) {}
  }
  if (bridge.micSource) {
    try { bridge.micSource.disconnect(); } catch (_) {}
  }
  if (bridge.mediaStream) {
    try {
      bridge.mediaStream.getTracks().forEach(t => t.stop());
    } catch (_) {}
  }
  if (bridge.scheduledSources) {
    for (const src of bridge.scheduledSources) {
      try { src.stop(); } catch (_) {}
    }
    bridge.scheduledSources = [];
  }
  if (bridge.audioCtx) {
    try { bridge.audioCtx.close(); } catch (_) {}
  }
  if (bridge.ws) {
    try { bridge.ws.close(); } catch (_) {}
  }
  if (bridge.tabObj) {
    updateLiveVoiceBtnUI(bridge.tabObj, '🎙️ Live Voice: OFF', false);
  }
}

export async function toggleAntigravityLiveVoice(tabObj, initialAction = 'start_live') {
  stopActiveSpeech();
  if (!tabObj || !tabObj.session) return;

  if (activeLiveVoiceBridge) {
    stopAntigravityLiveVoice(true);
    return;
  }

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    showUploadToast('Microphone access requires HTTPS or localhost in this browser.', 'error', 4000);
    return;
  }

  // If the Antigravity session is currently headless, switch it to interactive tmux first so `/voice live` can run
  if (tabObj.session.engine_type !== 'tmux') {
    updateLiveVoiceBtnUI(tabObj, '🎙️ Starting tmux...', true);
    await handleTakeWheel(tabObj.session.id);
  }

  let mediaStream;
  try {
    mediaStream = await navigator.mediaDevices.getUserMedia({
      audio: {
        channelCount: 1,
        sampleRate: 16000,
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true
      }
    });
  } catch (err) {
    console.warn('Failed to acquire microphone for Antigravity Live Voice:', err);
    showUploadToast('Microphone permission denied.', 'error', 3500);
    updateLiveVoiceBtnUI(tabObj, '🎙️ Live Voice: OFF', false);
    return;
  }

  const AudioCtx = window.AudioContext || window.webkitAudioContext;
  const audioCtx = new AudioCtx();
  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
  const wsProto = baseUrl.startsWith('https:') ? 'wss:' : 'ws:';
  const hostPart = baseUrl.replace(/^https?:\/\//, '') || window.location.host;
  const token = getAuthToken();
  const tokenParam = token ? `&token=${encodeURIComponent(token)}` : '';
  const wsUrl = `${wsProto}//${hostPart}/v1/sessions/voice-bridge?session_id=${encodeURIComponent(sessionId)}${tokenParam}`;

  const ws = new WebSocket(wsUrl);
  ws.binaryType = 'arraybuffer';

  const bridgeState = {
    tabObj,
    sessionId,
    ws,
    mediaStream,
    audioCtx,
    micSource: null,
    processor: null,
    speakerSampleRate: 24000,
    nextPlayTime: 0,
    scheduledSources: []
  };
  activeLiveVoiceBridge = bridgeState;
  updateLiveVoiceBtnUI(tabObj, '🎙️ Connecting...', true);

  const flushSpeakerQueue = () => {
    for (const src of bridgeState.scheduledSources) {
      try { src.stop(); } catch (_) {}
    }
    bridgeState.scheduledSources = [];
    bridgeState.nextPlayTime = audioCtx.currentTime;
  };

  const playSpeakerPCM = (arrayBuffer) => {
    if (!arrayBuffer || arrayBuffer.byteLength < 2) return;
    const int16 = new Int16Array(arrayBuffer);
    const float32 = new Float32Array(int16.length);
    for (let i = 0; i < int16.length; i++) {
      float32[i] = int16[i] / 32768.0;
    }
    const sampleRate = bridgeState.speakerSampleRate || 24000;
    const audioBuffer = audioCtx.createBuffer(1, float32.length, sampleRate);
    audioBuffer.getChannelData(0).set(float32);

    const source = audioCtx.createBufferSource();
    source.buffer = audioBuffer;
    source.connect(audioCtx.destination);

    const now = audioCtx.currentTime;
    const startTime = Math.max(now + 0.012, bridgeState.nextPlayTime);
    bridgeState.nextPlayTime = startTime + audioBuffer.duration;
    bridgeState.scheduledSources.push(source);

    source.onended = () => {
      const idx = bridgeState.scheduledSources.indexOf(source);
      if (idx !== -1) bridgeState.scheduledSources.splice(idx, 1);
    };
    source.start(startTime);
  };

  ws.onopen = () => {
    if (activeLiveVoiceBridge !== bridgeState) return;
    updateLiveVoiceBtnUI(tabObj, '🎙️ Live Voice: ON', true);

    // Wire microphone capture -> 16kHz S16_LE PCM -> binary WebSocket frames
    const micSource = audioCtx.createMediaStreamSource(mediaStream);
    const processor = audioCtx.createScriptProcessor(2048, 1, 1);
    bridgeState.micSource = micSource;
    bridgeState.processor = processor;

    processor.onaudioprocess = (e) => {
      if (activeLiveVoiceBridge !== bridgeState || ws.readyState !== WebSocket.OPEN) return;
      const inputData = e.inputBuffer.getChannelData(0);
      const pcm16 = downsampleTo16kInt16(inputData, audioCtx.sampleRate);
      ws.send(pcm16.buffer);
    };

    micSource.connect(processor);
    processor.connect(audioCtx.destination);

    // Trigger `/voice live` or `/voice dictate` in Antigravity tmux pane
    if (initialAction) {
      ws.send(JSON.stringify({
        type: initialAction,
        session_id: sessionId
      }));
    }
  };

  ws.onmessage = (event) => {
    if (activeLiveVoiceBridge !== bridgeState) return;
    if (event.data instanceof ArrayBuffer) {
      playSpeakerPCM(event.data);
      return;
    }
    if (typeof event.data === 'string') {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === 'clear') {
          flushSpeakerQueue();
        } else if (msg.type === 'status') {
          if (typeof msg.sample_rate === 'number' && msg.sample_rate > 0) {
            bridgeState.speakerSampleRate = msg.sample_rate;
          }
          if (msg.playing) {
            updateLiveVoiceBtnUI(tabObj, '🔊 Gemini Speaking...', true);
          } else if (msg.recording) {
            updateLiveVoiceBtnUI(tabObj, '🎙️ Live Listening...', true);
          } else {
            updateLiveVoiceBtnUI(tabObj, '🎙️ Live Voice: ON', true);
          }
        } else if (msg.type === 'error' && msg.error) {
          showUploadToast(msg.error, 'error', 4000);
        }
      } catch (_) {}
    }
  };

  ws.onerror = () => {
    if (activeLiveVoiceBridge === bridgeState) {
      stopAntigravityLiveVoice(false);
    }
  };

  ws.onclose = () => {
    if (activeLiveVoiceBridge === bridgeState) {
      stopAntigravityLiveVoice(false);
    }
  };
}

if (typeof window !== 'undefined') {
  window.sanitizeMarkdownForSpeech = sanitizeMarkdownForSpeech;
  window.isSimpleSpeechCandidate = isSimpleSpeechCandidate;
  window.stopActiveSpeech = stopActiveSpeech;
  window.speakText = speakText;
  window.handleReadChatMessage = handleReadChatMessage;
  window.toggleVoiceMic = toggleVoiceMic;
  window.toggleAntigravityLiveVoice = toggleAntigravityLiveVoice;
  window.stopAntigravityLiveVoice = stopAntigravityLiveVoice;
  window.renderSuggestedChips = renderSuggestedChips;
}

// --- Headless UX Controls: Pills, Config Popover, Autocomplete & Interactive Cards ---

const ANTIGRAVITY_DEFAULT_MODELS = [
  { id: 'gemini-4-argon-high', label: 'Gemini 4 Argon (High)' },
  { id: 'gemini-4-argon-medium', label: 'Gemini 4 Argon (Medium)' },
  { id: 'gemini-4-argon-low', label: 'Gemini 4 Argon (Low)' },
  { id: 'gemini-3.8-flash-high', label: 'Gemini 3.8 Flash (High)' },
  { id: 'gemini-3.8-flash-medium', label: 'Gemini 3.8 Flash (Medium)' },
  { id: 'gemini-3.8-flash-low', label: 'Gemini 3.8 Flash (Low)' },
  { id: 'claude-opus-5-5-high', label: 'Claude Opus 5.5 (High)' },
  { id: 'claude-opus-5-5-medium', label: 'Claude Opus 5.5 (Medium)' },
  { id: 'claude-opus-5-5-low', label: 'Claude Opus 5.5 (Low)' },
  { id: 'claude-sonnet-5-5-high', label: 'Claude Sonnet 5.5 (High)' },
  { id: 'claude-sonnet-5-5-medium', label: 'Claude Sonnet 5.5 (Medium)' },
  { id: 'claude-sonnet-5-5-low', label: 'Claude Sonnet 5.5 (Low)' },
  { id: 'gpt-oss-120b-medium', label: 'GPT-OSS 120B (Medium)' }
];

const CLAUDE_DEFAULT_MODELS = [
  { id: 'claude-fable-5-1', label: 'Claude Fable 5.1' },
  { id: 'claude-opus-5-5', label: 'Claude Opus 5.5' },
  { id: 'claude-sonnet-5-5', label: 'Claude Sonnet 5.5' },
  { id: 'claude-opus-4-6', label: 'Claude Opus 4.6' },
  { id: 'claude-sonnet-4-6', label: 'Claude Sonnet 4.6' },
  { id: 'claude-haiku-4-5', label: 'Claude Haiku 4.5' },
  { id: 'claude-3-7-sonnet-20250219', label: 'Claude 3.7 Sonnet' },
  { id: 'claude-3-5-sonnet-20241022', label: 'Claude 3.5 Sonnet' }
];

function getAgentModelOptions(agent, dynamicModels) {
  if (Array.isArray(dynamicModels) && dynamicModels.length > 0) {
    return dynamicModels;
  }
  const isAntigravity = (agent || '').toLowerCase() === 'antigravity';
  return isAntigravity ? ANTIGRAVITY_DEFAULT_MODELS : CLAUDE_DEFAULT_MODELS;
}

function formatModelLabel(model, agent, dynamicModels) {
  if (!model) {
    return 'Default Model';
  }

  const options = getAgentModelOptions(agent, dynamicModels);
  const match = options.find(m => m.id === model || model.includes(m.id));
  if (match && match.label) {
    return match.label;
  }

  const allDefaults = [...ANTIGRAVITY_DEFAULT_MODELS, ...CLAUDE_DEFAULT_MODELS];
  const defaultMatch = allDefaults.find(m => m.id === model || model.includes(m.id));
  if (defaultMatch && defaultMatch.label) {
    return defaultMatch.label;
  }

  if (model.includes('claude-fable-5-1') || model === 'claude-fable-5-1') return 'Claude Fable 5.1';
  if (model.includes('claude-opus-5-5') || model === 'claude-opus-5-5') return 'Claude Opus 5.5';
  if (model.includes('claude-sonnet-5-5') || model === 'claude-sonnet-5-5') return 'Claude Sonnet 5.5';
  if (model.includes('claude-opus-4-6') || model === 'claude-opus-4-6') return 'Claude Opus 4.6';
  if (model.includes('claude-sonnet-4-6') || model === 'claude-sonnet-4-6') return 'Claude Sonnet 4.6';
  if (model.includes('claude-haiku-4-5') || model === 'claude-haiku-4-5') return 'Claude Haiku 4.5';
  if (model.includes('claude-3-7-sonnet')) return 'Claude 3.7 Sonnet';
  if (model.includes('claude-3-5-sonnet')) return 'Claude 3.5 Sonnet';
  if (model.includes('claude-3-5-haiku')) return 'Claude 3.5 Haiku';
  if (model.includes('claude-3-opus')) return 'Claude 3 Opus';
  if (model.includes('gemini-4-argon')) return 'Gemini 4 Argon';
  if (model.includes('gemini-3.8-flash')) return 'Gemini 3.8 Flash';
  if (model.includes('gemini-2.5-pro')) return 'Gemini 2.5 Pro';
  if (model.includes('gemini-2.5-flash')) return 'Gemini 2.5 Flash';

  return model;
}

async function fetchSessionAgentModels(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const agent = tabObj.session.agent || '';
  if (tabObj.agentModels && tabObj.agentModelsAgent === agent) {
    return;
  }
  if (tabObj._fetchingAgentModels) return;
  tabObj._fetchingAgentModels = true;

  try {
    const sessionId = tabObj.session.id;
    const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
    const res = await fetch(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/suggestions?kind=models`);
    if (res.ok) {
      const data = await res.json();
      if (Array.isArray(data) && data.length > 0) {
        tabObj.agentModels = data;
        tabObj.agentModelsAgent = agent;
        updateStatusPills(tabObj);
        if (tabObj.chatConfigPopover && tabObj.chatConfigPopover.style.display !== 'none') {
          renderConfigPopoverContent(tabObj);
        }
      }
    }
  } catch (err) {
    console.warn('Failed to fetch session models:', err);
  } finally {
    tabObj._fetchingAgentModels = false;
  }
}

function updateStatusPills(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const session = tabObj.session;
  const isAntigravity = (session.agent || '').toLowerCase() === 'antigravity';

  if (tabObj.chatPillModel) {
    tabObj.chatPillModel.textContent = formatModelLabel(session.model, session.agent, tabObj.agentModels);
  }

  if (tabObj.chatPillEffort) {
    tabObj.chatPillEffort.style.display = isAntigravity ? 'none' : '';
    if (!isAntigravity) {
      const effort = session.effort || '';
      tabObj.chatPillEffort.textContent = effort ? `Effort: ${effort.charAt(0).toUpperCase() + effort.slice(1)}` : 'Effort: Default';
    }
  }

  if (tabObj.chatPillMode) {
    const isPlan = session.permission_mode === 'plan';
    if (isPlan) {
      tabObj.chatPillMode.className = 'chat-pill chat-pill-mode pill-plan-mode';
      tabObj.chatPillMode.textContent = '📝 Plan Mode';
      tabObj.chatPillMode.title = 'Plan Mode is ACTIVE (read-only planning). Click to switch to Auto Execution.';
    } else {
      tabObj.chatPillMode.className = 'chat-pill chat-pill-mode pill-auto-mode';
      tabObj.chatPillMode.textContent = '⚡ Auto Mode';
      tabObj.chatPillMode.title = 'Auto Mode is ACTIVE (actions execute automatically). Click to switch to Plan Mode.';
    }
  }

  if (tabObj.chatPillUsageText && tabObj.chatPillUsageDot) {
    const totalTokens = (typeof session.total_tokens === 'number' && session.total_tokens > 0)
      ? session.total_tokens
      : (typeof session.context_tokens === 'number' ? session.context_tokens : 0);
    const defaultMaxTokens = isAntigravity ? 1000000 : 200000;
    const maxTokens = (typeof session.max_context_tokens === 'number' && session.max_context_tokens > 0)
      ? session.max_context_tokens
      : defaultMaxTokens;

    let pct = 0;
    if (typeof session.context_pct === 'number' && session.context_pct > 0) {
      pct = Math.min(100, Math.round(session.context_pct));
    } else if (totalTokens > 0) {
      pct = Math.min(100, Math.round((totalTokens / maxTokens) * 100));
    }
    tabObj.chatPillUsageText.textContent = `${pct}% Context`;
    tabObj.chatPillUsageDot.className = 'chat-pill-dot' + (pct > 80 ? ' dot-usage-high' : (pct > 50 ? ' dot-usage-med' : ''));
  }
}

function toggleChatConfigPopover(tabObj) {
  if (!tabObj || !tabObj.chatConfigPopover) return;
  const popover = tabObj.chatConfigPopover;
  if (popover.style.display !== 'none') {
    popover.style.display = 'none';
  } else {
    fetchSessionAgentModels(tabObj);
    renderConfigPopoverContent(tabObj);
    popover.style.display = 'flex';
  }
}

function renderConfigPopoverContent(tabObj) {
  if (!tabObj || !tabObj.chatConfigPopover || !tabObj.session) return;
  const popover = tabObj.chatConfigPopover;
  const sess = tabObj.session;
  const model = sess.model || '';
  const effort = sess.effort || '';
  const isPlan = sess.permission_mode === 'plan';
  const isAntigravity = (sess.agent || '').toLowerCase() === 'antigravity';

  if (!tabObj.agentModels || tabObj.agentModelsAgent !== (sess.agent || '')) {
    fetchSessionAgentModels(tabObj);
  }

  const defaultMaxTokens = isAntigravity ? 1000000 : 200000;
  const totalTokens = (typeof sess.total_tokens === 'number' && sess.total_tokens > 0)
    ? sess.total_tokens
    : (typeof sess.context_tokens === 'number' ? sess.context_tokens : 0);
  const maxTokens = (typeof sess.max_context_tokens === 'number' && sess.max_context_tokens > 0)
    ? sess.max_context_tokens
    : defaultMaxTokens;
  let pct = 0;
  if (typeof sess.context_pct === 'number' && sess.context_pct > 0) {
    pct = Math.min(100, Math.round(sess.context_pct));
  } else if (totalTokens > 0) {
    pct = Math.min(100, Math.round((totalTokens / maxTokens) * 100));
  }
  const inputTokens = sess.input_tokens || 0;
  const outputTokens = sess.output_tokens || 0;

  const modelOptions = getAgentModelOptions(sess.agent, tabObj.agentModels);
  const isModelInOptions = modelOptions.some(m => m.id === model);
  let customOptionHtml = '';
  if (model && !isModelInOptions) {
    customOptionHtml = `<option value="${escapeHtml(model)}" selected>${escapeHtml(formatModelLabel(model, sess.agent, tabObj.agentModels))}</option>`;
  }
  const modelOptionsHtml = modelOptions.map(m => {
    const selected = (m.id === model) ? 'selected' : '';
    return `<option value="${escapeHtml(m.id)}" ${selected}>${escapeHtml(m.label)}</option>`;
  }).join('\n');

  const effortRowHtml = !isAntigravity ? `
    <div class="config-row">
      <label class="config-label">Reasoning Effort</label>
      <select class="config-select config-select-effort">
        <option value="" ${!effort ? 'selected' : ''}>Default</option>
        <option value="low" ${effort === 'low' ? 'selected' : ''}>Low</option>
        <option value="medium" ${effort === 'medium' ? 'selected' : ''}>Medium</option>
        <option value="high" ${effort === 'high' ? 'selected' : ''}>High</option>
        <option value="max" ${effort === 'max' ? 'selected' : ''}>Max</option>
      </select>
    </div>
  ` : '';

  popover.innerHTML = `
    <div class="config-popover-header">
      <div class="config-popover-title">
        <span>⚙️</span>
        <span>Session Configuration</span>
      </div>
      <button type="button" class="btn-close-popover" title="Close">✕</button>
    </div>
    <div class="config-row">
      <label class="config-label">Model</label>
      <select class="config-select config-select-model">
        <option value="" ${!model ? 'selected' : ''}>Default (Agent Default)</option>
        ${customOptionHtml}
        ${modelOptionsHtml}
      </select>
    </div>
    ${effortRowHtml}
    <div class="config-row">
      <label class="config-label">Execution Mode</label>
      <div class="config-toggle-group">
        <button type="button" class="config-toggle-btn config-btn-auto ${!isPlan ? 'active' : ''}">⚡ Auto Execution</button>
        <button type="button" class="config-toggle-btn config-btn-plan ${isPlan ? 'active' : ''}">📝 Plan Mode</button>
      </div>
    </div>
    <div class="config-row">
      <label class="config-label">Context Usage</label>
      <div class="config-usage-card">
        <div style="display: flex; justify-content: space-between; font-weight: 500;">
          <span>Context Window</span>
          <span class="config-usage-pct">${pct}%</span>
        </div>
        <div class="config-usage-bar">
          <div class="config-usage-fill" style="width: ${pct}%;"></div>
        </div>
        <div class="config-meta-details">
          <span>Input Tokens: ${inputTokens.toLocaleString()}</span>
          <span>Output Tokens: ${outputTokens.toLocaleString()}</span>
          <span>Estimated Context: ${totalTokens.toLocaleString()} / ${maxTokens.toLocaleString()}</span>
        </div>
      </div>
    </div>
  `;

  const btnClose = popover.querySelector('.btn-close-popover');
  if (btnClose) {
    btnClose.addEventListener('click', () => {
      popover.style.display = 'none';
    });
  }

  const selModel = popover.querySelector('.config-select-model');
  if (selModel) {
    selModel.addEventListener('change', () => {
      updateSessionConfig(tabObj, { model: selModel.value });
    });
  }

  const selEffort = popover.querySelector('.config-select-effort');
  if (selEffort) {
    selEffort.addEventListener('change', () => {
      updateSessionConfig(tabObj, { effort: selEffort.value });
    });
  }

  const btnAuto = popover.querySelector('.config-btn-auto');
  if (btnAuto) {
    btnAuto.addEventListener('click', () => {
      updateSessionConfig(tabObj, { permission_mode: 'bypassPermissions' });
    });
  }

  const btnPlan = popover.querySelector('.config-btn-plan');
  if (btnPlan) {
    btnPlan.addEventListener('click', () => {
      updateSessionConfig(tabObj, { permission_mode: 'plan' });
    });
  }
}

function setupChatConfigPopover(tabObj) {
  document.addEventListener('click', (e) => {
    if (!tabObj || !tabObj.chatConfigPopover) return;
    if (tabObj.chatConfigPopover.style.display === 'none') return;
    if (tabObj.chatConfigPopover.contains(e.target)) return;
    if (tabObj.chatComposerBar && tabObj.chatComposerBar.contains(e.target)) return;
    tabObj.chatConfigPopover.style.display = 'none';
  });
}

function togglePlanMode(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const current = tabObj.session.permission_mode || 'bypassPermissions';
  const next = current === 'plan' ? 'bypassPermissions' : 'plan';
  updateSessionConfig(tabObj, { permission_mode: next });
}

async function updateSessionConfig(tabObj, configUpdate) {
  if (!tabObj || !tabObj.session) return;
  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);

  tabObj.session = { ...tabObj.session, ...configUpdate };
  updateStatusPills(tabObj);
  renderConfigPopoverContent(tabObj);

  try {
    const res = await fetch(`${baseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/config`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(configUpdate)
    });
    if (!res.ok) {
      console.error('Failed to update session config:', await res.text());
    } else {
      const data = await res.json();
      const s = data && data.session ? data.session : data;
      if (s && s.id) {
        tabObj.session = { ...tabObj.session, ...s };
        updateStatusPills(tabObj);
        renderConfigPopoverContent(tabObj);
      }
    }
  } catch (err) {
    console.error('Error updating session config:', err);
  }
}

function setupChatAutocomplete(tabObj) {
  if (!tabObj || !tabObj.chatInputEl || !tabObj.chatAutocompleteMenu) return;
  const inputEl = tabObj.chatInputEl;
  const menuEl = tabObj.chatAutocompleteMenu;

  let debounceTimer = null;
  let activeKind = '';
  let activeMatch = null;

  tabObj.autocompleteItems = [];
  tabObj.autocompleteSelectedIndex = 0;

  function closeMenu() {
    menuEl.style.display = 'none';
    menuEl.innerHTML = '';
    tabObj.autocompleteItems = [];
    activeMatch = null;
  }

  function highlightActive() {
    const items = menuEl.querySelectorAll('.chat-autocomplete-item');
    items.forEach((item, idx) => {
      if (idx === tabObj.autocompleteSelectedIndex) {
        item.classList.add('active');
        item.scrollIntoView({ block: 'nearest' });
      } else {
        item.classList.remove('active');
      }
    });
  }

  inputEl.addEventListener('input', () => {
    if (debounceTimer) clearTimeout(debounceTimer);
    const text = inputEl.value || '';
    const pos = inputEl.selectionStart;
    const beforeCaret = text.slice(0, pos);

    let match = null;
    let kind = '';
    let triggerWord = '';

    const cmdMatch = beforeCaret.match(/^\s*(\/[^\s]*)$/);
    const fileMatch = beforeCaret.match(/(?:^|\s)(@[^\s@]*)$/);

    if (cmdMatch) {
      match = cmdMatch;
      triggerWord = cmdMatch[1];
      kind = 'commands';
    } else if (fileMatch) {
      match = fileMatch;
      triggerWord = fileMatch[1];
      kind = 'files';
    } else {
      closeMenu();
      return;
    }

    const query = triggerWord.slice(1);
    activeKind = kind;
    activeMatch = { matchStart: pos - triggerWord.length, matchEnd: pos, triggerWord };

    debounceTimer = setTimeout(async () => {
      if (!tabObj.session) return;
      const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
      try {
        const res = await fetch(`${baseUrl}/v1/sessions/${encodeURIComponent(tabObj.session.id)}/suggestions?kind=${encodeURIComponent(kind)}&q=${encodeURIComponent(query)}`);
        if (!res.ok) {
          closeMenu();
          return;
        }
        const items = await res.json();
        if (!Array.isArray(items) || items.length === 0) {
          closeMenu();
          return;
        }

        tabObj.autocompleteItems = items;
        tabObj.autocompleteSelectedIndex = 0;
        menuEl.innerHTML = '';

        items.forEach((item, idx) => {
          const itemEl = document.createElement('div');
          itemEl.className = 'chat-autocomplete-item' + (idx === 0 ? ' active' : '');
          if (kind === 'commands') {
            itemEl.innerHTML = `
              <div class="autocomplete-item-left">
                <span class="autocomplete-icon">⚡</span>
                <span class="autocomplete-name">${escapeHtml(item.trigger || ('/' + item.name))}</span>
                <span class="autocomplete-desc">${escapeHtml(item.description || '')}</span>
              </div>
              <span class="autocomplete-category">${escapeHtml(item.category || '')}</span>
            `;
          } else {
            itemEl.innerHTML = `
              <div class="autocomplete-item-left">
                <span class="autocomplete-icon">📄</span>
                <span class="autocomplete-name">${escapeHtml(item.name)}</span>
                <span class="autocomplete-desc">${escapeHtml(item.path)}</span>
              </div>
            `;
          }

          itemEl.addEventListener('mousedown', (e) => {
            e.preventDefault();
            selectAutocompleteItem(tabObj, item, kind, activeMatch);
          });

          menuEl.appendChild(itemEl);
        });

        menuEl.style.display = 'block';
      } catch (err) {
        console.warn('Failed to fetch suggestions:', err);
        closeMenu();
      }
    }, 100);
  });

  inputEl.addEventListener('keydown', (e) => {
    if (menuEl.style.display === 'none' || tabObj.autocompleteItems.length === 0) return;

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      tabObj.autocompleteSelectedIndex = (tabObj.autocompleteSelectedIndex + 1) % tabObj.autocompleteItems.length;
      highlightActive();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      tabObj.autocompleteSelectedIndex = (tabObj.autocompleteSelectedIndex - 1 + tabObj.autocompleteItems.length) % tabObj.autocompleteItems.length;
      highlightActive();
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault();
      e.stopPropagation();
      const selected = tabObj.autocompleteItems[tabObj.autocompleteSelectedIndex];
      if (selected) {
        selectAutocompleteItem(tabObj, selected, activeKind, activeMatch);
      }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeMenu();
    }
  });

  document.addEventListener('click', (e) => {
    if (menuEl.style.display !== 'none' && !menuEl.contains(e.target) && e.target !== inputEl) {
      closeMenu();
    }
  });
}

function selectAutocompleteItem(tabObj, item, kind, match) {
  if (!tabObj || !tabObj.chatInputEl || !item) return;
  const inputEl = tabObj.chatInputEl;
  const menuEl = tabObj.chatAutocompleteMenu;
  if (menuEl) menuEl.style.display = 'none';

  const fullText = inputEl.value || '';
  let start = 0;
  let end = fullText.length;
  if (match) {
    start = match.matchStart;
    end = match.matchEnd;
  }

  if (kind === 'commands') {
    if (item.execution_mode === 'client_action') {
      inputEl.value = fullText.slice(0, start) + fullText.slice(end);
      inputEl.style.height = 'auto';
      executeClientAction(tabObj, item.name);
      return;
    }
    const insertText = (item.trigger || ('/' + item.name)) + ' ';
    inputEl.value = fullText.slice(0, start) + insertText + fullText.slice(end);
    const newPos = start + insertText.length;
    inputEl.focus();
    inputEl.setSelectionRange(newPos, newPos);
  } else if (kind === 'files') {
    const insertText = '@' + item.path + ' ';
    inputEl.value = fullText.slice(0, start) + insertText + fullText.slice(end);
    const newPos = start + insertText.length;
    inputEl.focus();
    inputEl.setSelectionRange(newPos, newPos);
  }
}

function executeClientAction(tabObj, actionName) {
  if (!tabObj) return;
  switch (actionName) {
    case 'config':
      toggleChatConfigPopover(tabObj);
      break;
    case 'plan':
      togglePlanMode(tabObj);
      break;
    case 'clear':
      if (tabObj.chatMessagesEl) {
        tabObj.chatMessagesEl.innerHTML = '';
      }
      loadChatTranscript(tabObj);
      break;
    case 'terminal':
      if (tabObj.session) {
        handleTakeWheel(tabObj.session.id);
      }
      break;
    case 'handover':
      if (tabObj.session) {
        openHandoverModal(tabObj.session);
      }
      break;
    case 'compact':
      sendChatPrompt(tabObj, '/compact');
      break;
    case 'voice_live':
      toggleAntigravityLiveVoice(tabObj, 'start_live');
      break;
    case 'voice_dictate':
      toggleAntigravityLiveVoice(tabObj, 'start_dictate');
      break;
    case 'voice_stop':
      stopAntigravityLiveVoice(true);
      break;
    default:
      console.warn('Unknown client action:', actionName);
  }
}

function renderInteractiveBlockCard(tabObj, blocked, toolName) {
  if (!tabObj || !tabObj.chatMessagesEl || !blocked) return;

  const existing = tabObj.chatMessagesEl.querySelector('.chat-interactive-card:not(.answered)');
  if (existing) {
    existing.remove();
  }

  const isApproval = blocked.kind === 'approval' ||
    toolName === 'ExitPlanMode' ||
    (Array.isArray(blocked.options) && blocked.options.includes('Approve Plan'));

  const card = document.createElement('div');
  card.className = 'chat-interactive-card' + (isApproval ? ' card-approval' : '');

  if (isApproval) {
    const planText = blocked.reason || '';
    card.innerHTML = `
      <div class="interactive-card-header">
        <span class="interactive-card-badge">📋 Plan Approval</span>
        <span class="interactive-card-title">${escapeHtml(blocked.question || 'Approve Implementation Plan?')}</span>
      </div>
      ${planText ? `<div class="interactive-card-plan">${renderMarkdown(planText)}</div>` : ''}
      <div class="interactive-card-actions">
        <button type="button" class="btn btn-primary btn-sm btn-approve-plan">✓ Approve & Execute</button>
        <button type="button" class="btn btn-secondary btn-sm btn-reject-plan">Request Changes</button>
      </div>
      <div class="interactive-card-feedback" style="display: none;">
        <textarea class="interactive-feedback-input" rows="2" placeholder="Describe the changes you want to the plan..."></textarea>
        <div class="interactive-feedback-actions">
          <button type="button" class="btn btn-primary btn-sm btn-submit-feedback">Send Feedback</button>
          <button type="button" class="btn btn-secondary btn-sm btn-cancel-feedback">Cancel</button>
        </div>
      </div>
    `;

    attachCodeBlockCopyButtons(card);

    const btnApprove = card.querySelector('.btn-approve-plan');
    const btnReject = card.querySelector('.btn-reject-plan');
    const feedbackBox = card.querySelector('.interactive-card-feedback');
    const feedbackInput = card.querySelector('.interactive-feedback-input');
    const btnSubmitFeedback = card.querySelector('.btn-submit-feedback');
    const btnCancelFeedback = card.querySelector('.btn-cancel-feedback');

    btnApprove.addEventListener('click', async () => {
      btnApprove.disabled = true;
      btnReject.disabled = true;
      card.classList.add('answered');
      card.querySelector('.interactive-card-actions').innerHTML = `<span class="interactive-answered-badge">✓ Approved Plan • Executing...</span>`;
      feedbackBox.style.display = 'none';
      await submitSessionResponse(tabObj, 'approve', 'Approve Plan');
    });

    btnReject.addEventListener('click', () => {
      feedbackBox.style.display = 'flex';
      feedbackInput.focus();
    });

    btnCancelFeedback.addEventListener('click', () => {
      feedbackBox.style.display = 'none';
    });

    btnSubmitFeedback.addEventListener('click', async () => {
      const feedback = feedbackInput.value.trim();
      if (!feedback) {
        feedbackInput.focus();
        return;
      }
      btnSubmitFeedback.disabled = true;
      btnApprove.disabled = true;
      btnReject.disabled = true;
      card.classList.add('answered');
      card.querySelector('.interactive-card-actions').innerHTML = `<span class="interactive-answered-badge">Requested Changes: ${escapeHtml(feedback)}</span>`;
      feedbackBox.style.display = 'none';
      await submitSessionResponse(tabObj, 'reject', feedback);
    });
  } else {
    const questions = (Array.isArray(blocked.questions) && blocked.questions.length > 0)
      ? blocked.questions
      : [{
          question: blocked.question || 'Please provide input:',
          options: blocked.options || [],
          multi_select: false
        }];

    let questionsHtml = '';
    questions.forEach((q, qIdx) => {
      const inputType = q.multi_select ? 'checkbox' : 'radio';
      const opts = Array.isArray(q.options) ? q.options : [];
      questionsHtml += `
        <div class="interactive-question-group" data-q-idx="${qIdx}">
          ${q.header ? `<div class="interactive-question-header">${escapeHtml(q.header)}</div>` : ''}
          <div class="interactive-question-prompt">${escapeHtml(q.question)}</div>
          <div class="interactive-options-list">
            ${opts.map((opt, oIdx) => `
              <label class="interactive-option-label">
                <input type="${inputType}" name="q_${qIdx}" value="${escapeHtml(opt)}" ${oIdx === 0 && !q.multi_select ? 'checked' : ''} />
                <span>${escapeHtml(opt)}</span>
              </label>
            `).join('')}
            <label class="interactive-option-label">
              <input type="${inputType}" name="q_${qIdx}" value="__other__" />
              <span>Other</span>
            </label>
            <div class="interactive-other-box" style="display: none;">
              <input type="text" class="interactive-other-input" placeholder="Type custom answer..." />
            </div>
          </div>
        </div>
      `;
    });

    card.innerHTML = `
      <div class="interactive-card-header">
        <span class="interactive-card-badge">❓ Question</span>
        <span class="interactive-card-title">${escapeHtml(blocked.question || 'Input Required')}</span>
      </div>
      <div class="interactive-card-body">
        ${questionsHtml}
      </div>
      <div class="interactive-card-actions">
        <button type="button" class="btn btn-primary btn-sm btn-submit-question">Submit Answer</button>
      </div>
    `;

    card.querySelectorAll('.interactive-question-group').forEach(group => {
      const otherRadio = group.querySelector('input[value="__other__"]');
      const otherBox = group.querySelector('.interactive-other-box');
      const otherInput = group.querySelector('.interactive-other-input');
      const allInputs = group.querySelectorAll('input[type="radio"], input[type="checkbox"]');

      allInputs.forEach(inp => {
        inp.addEventListener('change', () => {
          if (otherRadio.checked) {
            otherBox.style.display = 'block';
            otherInput.focus();
          } else {
            otherBox.style.display = 'none';
          }
        });
      });
    });

    const btnSubmit = card.querySelector('.btn-submit-question');
    btnSubmit.addEventListener('click', async () => {
      const answers = [];
      card.querySelectorAll('.interactive-question-group').forEach(group => {
        const checked = group.querySelectorAll('input[type="radio"]:checked, input[type="checkbox"]:checked');
        const vals = [];
        checked.forEach(c => {
          if (c.value === '__other__') {
            const txt = group.querySelector('.interactive-other-input').value.trim();
            if (txt) vals.push(txt);
          } else {
            vals.push(c.value);
          }
        });
        if (vals.length > 0) {
          answers.push(vals.join(', '));
        }
      });

      const finalAnswer = answers.join('; ');
      if (!finalAnswer) return;

      btnSubmit.disabled = true;
      card.classList.add('answered');
      card.querySelector('.interactive-card-actions').innerHTML = `<span class="interactive-answered-badge">✓ Answered: ${escapeHtml(finalAnswer)}</span>`;
      await submitSessionResponse(tabObj, 'answer', finalAnswer);
    });
  }

  tabObj.chatMessagesEl.appendChild(card);
  tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
}

function renderTodoWriteCard(tabObj, toolInput) {
  let todos = [];
  if (toolInput) {
    if (Array.isArray(toolInput.todos)) todos = toolInput.todos;
    else if (typeof toolInput === 'string') {
      try {
        const parsed = JSON.parse(toolInput);
        if (Array.isArray(parsed.todos)) todos = parsed.todos;
      } catch (_) {}
    } else if (typeof toolInput === 'object' && Array.isArray(toolInput.tasks)) {
      todos = toolInput.tasks;
    }
  }
  if (!todos || todos.length === 0) return null;

  const completedCount = todos.filter(t => (t.status || '').toLowerCase() === 'completed').length;
  const card = document.createElement('div');
  card.className = 'chat-todo-card';
  card.innerHTML = `
    <div class="chat-todo-header">
      <span>📋</span>
      <span>Task Progress (${completedCount}/${todos.length})</span>
    </div>
    <div class="chat-todo-list">
      ${todos.map(t => {
        const rawSt = String(t.status || 'pending').toLowerCase();
        const st = ['completed', 'in_progress', 'pending'].includes(rawSt) ? rawSt : 'pending';
        let iconClass = 'todo-icon-pending';
        let iconChar = '○';
        if (st === 'completed') {
          iconClass = 'todo-icon-completed';
          iconChar = '✓';
        } else if (st === 'in_progress') {
          iconClass = 'todo-icon-inprogress';
          iconChar = '▶';
        }
        return `
          <div class="chat-todo-item ${st}">
            <span class="chat-todo-status-icon ${iconClass}">${iconChar}</span>
            <span class="chat-todo-text">${escapeHtml(t.content || t.text || t.name || '')}</span>
          </div>
        `;
      }).join('')}
    </div>
  `;
  return card;
}

async function submitSessionResponse(tabObj, action, value) {
  if (!tabObj || !tabObj.session) return;
  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);

  tabObj.session.blocked = null;
  tabObj.session.state = 1;

  appendChatMessage(tabObj, {
    role: 'user',
    content: value,
    rawPrompt: value,
    timestamp: new Date().toISOString()
  });

  setChatSending(tabObj, true);

  try {
    const res = await fetch(`${baseUrl}/v1/sessions/respond`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: sessionId,
        action: action,
        answer: value
      })
    });
    if (!res.ok) {
      console.error('Failed to submit response:', await res.text());
    }
  } catch (err) {
    console.error('Error submitting response to daemon:', err);
  }
}


function setupChatInterface(tabObj, chatViewEl, session) {
  const isTmux = session.engine_type === 'tmux';

  chatViewEl.innerHTML = `
    <div class="chat-header-bar">
      <div class="chat-header-left">
        <span class="chat-header-title">${escapeHtml(session.name || session.agent)}</span>
        <span class="chat-engine-badge ${isTmux ? 'badge-tmux' : ''}">
          ${isTmux ? '🖥️ tmux' : '💬 headless'}
        </span>
        <span class="chat-status-badge" style="font-size: 11px; color: var(--text-dim);">🟢 Ready</span>
      </div>
      <div class="chat-header-right">
        ${(session.agent || '').toLowerCase() === 'antigravity' ? `<button class="btn btn-secondary btn-sm btn-chat-live-voice" title="Start two-way Antigravity Gemini Live Voice call (/voice live)">🎙️ Live Voice: OFF</button>` : ''}
        <button class="btn btn-secondary btn-sm btn-chat-autoread" title="Toggle automatic speech playback on completion">🔊 Auto-Read: OFF</button>
        <button class="btn btn-secondary btn-sm btn-chat-mic" title="Toggle voice microphone input">🎤 Mic: OFF</button>
        <button class="btn-take-wheel" title="Spawn tmux process and attach interactive terminal">🏎️ Take the Wheel</button>
        <button class="btn btn-secondary btn-sm btn-reload-chat" title="Reload transcript history">🔄</button>
      </div>
    </div>
    <div class="chat-messages-container"></div>
    <div class="chat-composer-container">
      <div class="chat-queue-container" style="display: none;">
        <div class="chat-queue-bar">
          <div class="chat-queue-summary">
            <span class="chat-queue-icon">⏳</span>
            <span class="chat-queue-count-text">0 messages queued up</span>
            <span class="chat-queue-chevron">▾</span>
          </div>
          <div class="chat-queue-actions">
            <button class="btn-chat-queue-resume" style="display: none;" type="button" title="Resume queue execution">▶ Resume</button>
            <button class="btn-chat-queue-clear" type="button" title="Clear all queued messages">Clear all</button>
          </div>
        </div>
        <div class="chat-queue-list" style="display: none;"></div>
      </div>
      <div class="chat-subagents-container" style="display: none;">
        <div class="chat-subagents-bar">
          <div class="chat-subagents-summary">
            <span class="chat-subagents-pulse"></span>
            <span class="chat-subagents-icon">⚡</span>
            <span class="chat-subagents-count-text">0 subagents running...</span>
            <span class="chat-subagents-chevron">▾</span>
          </div>
        </div>
        <div class="chat-subagents-list" style="display: none;"></div>
      </div>
      <div class="chat-suggested-chips" style="display: none;"></div>
      <div class="chat-composer-attachments" style="display: none;"></div>
      <div class="chat-composer-bar">
        <div class="chat-composer-pills-left">
          <span class="chat-pill chat-pill-model" title="Change Model">Default Model</span>
          <span class="chat-pill chat-pill-effort" title="Reasoning Effort">Effort: Default</span>
          <span class="chat-pill chat-pill-mode pill-auto-mode" title="Click to toggle Auto / Plan Mode">⚡ Auto Mode</span>
        </div>
        <div class="chat-composer-pills-right">
          <span class="chat-pill chat-pill-usage" title="Context Usage">
            <span class="chat-pill-dot"></span>
            <span class="chat-pill-usage-text">0% Context</span>
          </span>
          <button type="button" class="chat-pill chat-pill-config" title="Session Configuration">⚙️ /config</button>
        </div>
      </div>
      <div class="chat-autocomplete-menu" style="display: none;"></div>
      <div class="chat-config-popover" style="display: none;"></div>
      <div class="chat-composer-box">
        <button class="btn-composer-attach" type="button" title="Attach file or image">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"></path>
          </svg>
        </button>
        <input type="file" class="chat-file-input" multiple style="display: none;" accept="image/*,.pdf,.txt,.md,.log,.json,.yaml,.yml,.csv" />
        <textarea class="chat-composer-textarea" rows="1" placeholder="Ask ${escapeHtml(formatAgentChatName(session.agent))} anything... (Enter to send, Shift+Enter for newline)"></textarea>
        <div class="chat-composer-actions">
          <button class="btn-composer-cancel" style="display: none;" title="Cancel turn (SIGINT)">🛑 Stop</button>
          <button class="btn-composer-send" title="Send Prompt (Enter)">➤</button>
        </div>
      </div>
      <div class="chat-composer-hints">
        <span>💡 Flat-rate OAuth active • Headless streaming • Press Shift+Enter for new line</span>
        <span class="chat-status-hint"></span>
      </div>
    </div>
  `;

  tabObj.promptQueue = tabObj.promptQueue || [];
  tabObj.isQueueExpanded = false;
  tabObj.isQueuePaused = false;
  tabObj.pendingAttachments = [];
  tabObj.runningSubagents = [];
  tabObj.isSubagentsExpanded = false;

  tabObj.chatMessagesEl = chatViewEl.querySelector('.chat-messages-container');
  tabObj.chatSubagentsContainer = chatViewEl.querySelector('.chat-subagents-container');
  tabObj.chatSubagentsBar = chatViewEl.querySelector('.chat-subagents-bar');
  tabObj.chatSubagentsCountText = chatViewEl.querySelector('.chat-subagents-count-text');
  tabObj.chatSubagentsChevron = chatViewEl.querySelector('.chat-subagents-chevron');
  tabObj.chatSubagentsList = chatViewEl.querySelector('.chat-subagents-list');
  tabObj.chatComposerAttachments = chatViewEl.querySelector('.chat-composer-attachments');
  tabObj.chatComposerBar = chatViewEl.querySelector('.chat-composer-bar');
  tabObj.chatPillModel = chatViewEl.querySelector('.chat-pill-model');
  tabObj.chatPillEffort = chatViewEl.querySelector('.chat-pill-effort');
  tabObj.chatPillMode = chatViewEl.querySelector('.chat-pill-mode');
  tabObj.chatPillUsage = chatViewEl.querySelector('.chat-pill-usage');
  tabObj.chatPillUsageText = chatViewEl.querySelector('.chat-pill-usage-text');
  tabObj.chatPillUsageDot = chatViewEl.querySelector('.chat-pill-dot');
  tabObj.chatPillConfig = chatViewEl.querySelector('.chat-pill-config');
  tabObj.chatAutocompleteMenu = chatViewEl.querySelector('.chat-autocomplete-menu');
  tabObj.chatConfigPopover = chatViewEl.querySelector('.chat-config-popover');
  tabObj.chatComposerBox = chatViewEl.querySelector('.chat-composer-box');
  tabObj.chatAttachBtn = chatViewEl.querySelector('.btn-composer-attach');
  tabObj.chatFileInput = chatViewEl.querySelector('.chat-file-input');
  tabObj.chatInputEl = chatViewEl.querySelector('.chat-composer-textarea');
  tabObj.chatSendBtn = chatViewEl.querySelector('.btn-composer-send');
  tabObj.chatCancelBtn = chatViewEl.querySelector('.btn-composer-cancel');
  tabObj.chatStatusBadge = chatViewEl.querySelector('.chat-status-badge');
  tabObj.chatEngineBadge = chatViewEl.querySelector('.chat-engine-badge');
  tabObj.chatAutoReadBtn = chatViewEl.querySelector('.btn-chat-autoread');
  tabObj.chatMicBtn = chatViewEl.querySelector('.btn-chat-mic');
  tabObj.chatLiveVoiceBtn = chatViewEl.querySelector('.btn-chat-live-voice');
  tabObj.chatSuggestedChipsEl = chatViewEl.querySelector('.chat-suggested-chips');

  if (tabObj.chatLiveVoiceBtn) {
    tabObj.chatLiveVoiceBtn.addEventListener('click', (e) => {
      e.preventDefault();
      toggleAntigravityLiveVoice(tabObj, 'start_live');
    });
  }

  const autoReadStored = isChatAutoReadEnabled();
  if (tabObj.chatAutoReadBtn) {
    if (autoReadStored) {
      tabObj.chatAutoReadBtn.textContent = '🔊 Auto-Read: ON';
      tabObj.chatAutoReadBtn.classList.add('active');
    } else {
      tabObj.chatAutoReadBtn.textContent = '🔊 Auto-Read: OFF';
      tabObj.chatAutoReadBtn.classList.remove('active');
    }

    tabObj.chatAutoReadBtn.addEventListener('click', (e) => {
      e.preventDefault();
      const current = isChatAutoReadEnabled();
      const next = !current;
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem('ackbar_voice_auto_read', next ? 'true' : 'false');
      }
      if (next) {
        tabObj.chatAutoReadBtn.textContent = '🔊 Auto-Read: ON';
        tabObj.chatAutoReadBtn.classList.add('active');
      } else {
        tabObj.chatAutoReadBtn.textContent = '🔊 Auto-Read: OFF';
        tabObj.chatAutoReadBtn.classList.remove('active');
        stopActiveSpeech();
      }
    });
  }

  if (tabObj.chatMicBtn) {
    tabObj.chatMicBtn.addEventListener('click', (e) => {
      e.preventDefault();
      toggleVoiceMic(tabObj);
    });
  }

  if (tabObj.chatSubagentsBar) {
    tabObj.chatSubagentsBar.addEventListener('click', () => {
      tabObj.isSubagentsExpanded = !tabObj.isSubagentsExpanded;
      renderSubagentsBar(tabObj);
    });
  }

  if (tabObj.chatAttachBtn && tabObj.chatFileInput) {
    tabObj.chatAttachBtn.addEventListener('click', (e) => {
      e.preventDefault();
      tabObj.chatFileInput.click();
    });

    tabObj.chatFileInput.addEventListener('change', async () => {
      const files = Array.from(tabObj.chatFileInput.files || []);
      tabObj.chatFileInput.value = '';
      for (const file of files) {
        await addPendingAttachment(tabObj, file);
      }
    });
  }

  // Drop Overlay for Drag-and-Drop file uploads (Images, PDFs, Documents, Text)
  const chatDropOverlay = document.createElement('div');
  chatDropOverlay.className = 'chat-drop-overlay';
  chatDropOverlay.innerHTML = `<div class="drop-badge">📎 Drop file to attach to prompt</div>`;
  chatViewEl.appendChild(chatDropOverlay);

  let chatDragCounter = 0;
  chatViewEl.addEventListener('dragenter', (e) => {
    if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      e.preventDefault();
      chatDragCounter++;
      chatDropOverlay.classList.add('active');
      if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.add('drag-over');
    }
  });

  chatViewEl.addEventListener('dragover', (e) => {
    e.preventDefault();
    if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      e.dataTransfer.dropEffect = 'copy';
      chatDropOverlay.classList.add('active');
      if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.add('drag-over');
    }
  });

  chatViewEl.addEventListener('dragleave', (e) => {
    chatDragCounter--;
    if (chatDragCounter <= 0 || !chatViewEl.contains(e.relatedTarget)) {
      chatDragCounter = 0;
      chatDropOverlay.classList.remove('active');
      if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
    }
  });

  chatViewEl.addEventListener('drop', async (e) => {
    e.preventDefault();
    e.stopPropagation();
    chatDragCounter = 0;
    chatDropOverlay.classList.remove('active');
    if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
    if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      for (const file of e.dataTransfer.files) {
        await addPendingAttachment(tabObj, file);
      }
    }
  });

  chatDropOverlay.addEventListener('dragover', (e) => {
    e.preventDefault();
    if (e.dataTransfer && e.dataTransfer.types && Array.from(e.dataTransfer.types).includes('Files')) {
      e.dataTransfer.dropEffect = 'copy';
    }
  });

  chatDropOverlay.addEventListener('drop', async (e) => {
    e.preventDefault();
    e.stopPropagation();
    chatDragCounter = 0;
    chatDropOverlay.classList.remove('active');
    if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
    if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      for (const file of e.dataTransfer.files) {
        await addPendingAttachment(tabObj, file);
      }
    }
  });

  tabObj.chatQueueContainer = chatViewEl.querySelector('.chat-queue-container');
  tabObj.chatQueueBar = chatViewEl.querySelector('.chat-queue-bar');
  tabObj.chatQueueCountText = chatViewEl.querySelector('.chat-queue-count-text');
  tabObj.chatQueueChevron = chatViewEl.querySelector('.chat-queue-chevron');
  tabObj.chatQueueList = chatViewEl.querySelector('.chat-queue-list');
  tabObj.chatQueueClearBtn = chatViewEl.querySelector('.btn-chat-queue-clear');
  tabObj.chatQueueResumeBtn = chatViewEl.querySelector('.btn-chat-queue-resume');

  const queueSummary = chatViewEl.querySelector('.chat-queue-summary');
  if (queueSummary) {
    queueSummary.addEventListener('click', () => {
      tabObj.isQueueExpanded = !tabObj.isQueueExpanded;
      renderChatQueue(tabObj);
    });
  }

  if (tabObj.chatQueueClearBtn) {
    tabObj.chatQueueClearBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
      try {
        await fetch(`${baseUrl}/v1/sessions/prompt/queue?session_id=${encodeURIComponent(tabObj.session.id)}`, {
          method: 'DELETE'
        });
      } catch (err) {
        console.error('Failed to clear queue on daemon:', err);
      }
      tabObj.promptQueue = [];
      tabObj.isQueuePaused = false;
      renderChatQueue(tabObj);
    });
  }

  if (tabObj.chatQueueResumeBtn) {
    tabObj.chatQueueResumeBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
      try {
        await fetch(`${baseUrl}/v1/sessions/prompt/queue/resume?session_id=${encodeURIComponent(tabObj.session.id)}`, {
          method: 'POST'
        });
      } catch (err) {
        console.error('Failed to resume queue on daemon:', err);
      }
      tabObj.isQueuePaused = false;
      renderChatQueue(tabObj);
    });
  }

  tabObj.chatTakeWheelBtn = chatViewEl.querySelector('.btn-take-wheel');
  if (tabObj.chatTakeWheelBtn) {
    if (session.engine_type === 'tmux') {
      tabObj.chatTakeWheelBtn.style.display = 'none';
    } else {
      tabObj.chatTakeWheelBtn.style.display = 'inline-flex';
      tabObj.chatTakeWheelBtn.addEventListener('click', () => handleTakeWheel(session.id));
    }
  }

  const btnReload = chatViewEl.querySelector('.btn-reload-chat');
  if (btnReload) {
    btnReload.addEventListener('click', () => {
      loadChatTranscript(tabObj);
      loadChatQueue(tabObj);
    });
  }

  if (tabObj.chatCancelBtn) {
    tabObj.chatCancelBtn.addEventListener('click', () => cancelChatTurn(tabObj));
  }

  if (tabObj.chatSendBtn) {
    tabObj.chatSendBtn.addEventListener('click', () => sendChatPrompt(tabObj));
  }

  if (tabObj.chatInputEl) {
    tabObj.chatInputEl.addEventListener('dragover', (e) => {
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    });

    tabObj.chatInputEl.addEventListener('drop', async (e) => {
      e.preventDefault();
      e.stopPropagation();
      chatDragCounter = 0;
      chatDropOverlay.classList.remove('active');
      if (tabObj.chatComposerBox) tabObj.chatComposerBox.classList.remove('drag-over');
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
        for (const file of e.dataTransfer.files) {
          await addPendingAttachment(tabObj, file);
        }
      }
    });

    tabObj.chatInputEl.addEventListener('paste', async (e) => {
      if (e._ackbarPasteHandled) return;
      if (!e.clipboardData) return;
      const files = extractPastedAttachmentFiles(e.clipboardData);
      if (files.length > 0) {
        e._ackbarPasteHandled = true;
        tabObj._lastPasteHandledAt = Date.now();
        e.preventDefault();
        e.stopPropagation();
        for (const file of files) {
          await addPendingAttachment(tabObj, file);
        }
        return;
      }
      const pastedText = e.clipboardData.getData('text/plain');
      if (pastedText) {
        e._ackbarPasteHandled = true;
        tabObj._lastPasteHandledAt = Date.now();
        e.preventDefault();
        e.stopPropagation();
        insertTextIntoPromptInput(tabObj.chatInputEl, pastedText);
      }
    });

    tabObj.chatInputEl.addEventListener('input', () => {
      tabObj._lastInputAt = Date.now();
      tabObj.chatInputEl.style.height = 'auto';
      tabObj.chatInputEl.style.height = Math.min(tabObj.chatInputEl.scrollHeight, 180) + 'px';
      saveChatDraft(tabObj.id || (tabObj.session && tabObj.session.id), tabObj.chatInputEl.value, tabObj.pendingAttachments);
    });

    tabObj.chatInputEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        if (tabObj.chatAutocompleteMenu && tabObj.chatAutocompleteMenu.style.display !== 'none' && tabObj.autocompleteItems && tabObj.autocompleteItems.length > 0) {
          return;
        }
        e.preventDefault();
        sendChatPrompt(tabObj);
      }
    });
  }

  if (tabObj.chatComposerBox) {
    tabObj.chatComposerBox.addEventListener('click', (e) => {
      if (e.target && e.target.closest && e.target.closest('button, input, a')) return;
      if (tabObj.chatInputEl) {
        tabObj.chatInputEl.focus();
      }
    });
  }

  chatViewEl.addEventListener('click', (e) => {
    if (tabObj.viewMode !== 'chat' || !tabObj.chatInputEl) return;
    if (e.target && e.target.closest && e.target.closest('button, input, textarea, select, a, summary, details, .chat-file-pill, .chat-context-menu')) {
      return;
    }
    const sel = window.getSelection ? window.getSelection() : null;
    if (sel && !sel.isCollapsed) return;
    tabObj.chatInputEl.focus();
  });

  // Auto-load older messages when user scrolls near top of chat
  if (tabObj.chatMessagesEl) {
    tabObj.chatMessagesEl.addEventListener('scroll', () => {
      if (tabObj.chatMessagesEl.scrollTop < 60) {
        loadOlderTranscriptMessages(tabObj);
      }
    }, { passive: true });
  }

  if (tabObj.chatPillModel) tabObj.chatPillModel.addEventListener('click', () => toggleChatConfigPopover(tabObj));
  if (tabObj.chatPillEffort) tabObj.chatPillEffort.addEventListener('click', () => toggleChatConfigPopover(tabObj));
  if (tabObj.chatPillUsage) tabObj.chatPillUsage.addEventListener('click', () => toggleChatConfigPopover(tabObj));
  if (tabObj.chatPillConfig) tabObj.chatPillConfig.addEventListener('click', () => toggleChatConfigPopover(tabObj));
  if (tabObj.chatPillMode) tabObj.chatPillMode.addEventListener('click', () => togglePlanMode(tabObj));

  setupChatAutocomplete(tabObj);
  setupChatConfigPopover(tabObj);
  updateStatusPills(tabObj);

  const draftId = tabObj.id || (session && session.id);
  const draft = getChatDraft(draftId);
  if (draft) {
    if (draft.text && tabObj.chatInputEl) {
      tabObj.chatInputEl.value = draft.text;
      tabObj.chatInputEl.style.height = 'auto';
      tabObj.chatInputEl.style.height = Math.min(tabObj.chatInputEl.scrollHeight, 180) + 'px';
    }
    if (Array.isArray(draft.attachments) && draft.attachments.length > 0) {
      tabObj.pendingAttachments = [...draft.attachments];
      renderPendingAttachments(tabObj);
    }
  }

  loadChatTranscript(tabObj);
  loadChatQueue(tabObj);
  if (tabObj.session && isSessionWorking(tabObj.session)) {
    setChatSending(tabObj, true);
  }
  if (tabObj.session && tabObj.session.engine_type === 'headless' && tabObj.viewMode === 'chat') {
    connectChatStream(tabObj);
  }
}

// Load prompt queue from daemon
async function loadChatQueue(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
  try {
    const res = await fetch(`${baseUrl}/v1/sessions/prompt/queue?session_id=${encodeURIComponent(tabObj.session.id)}`);
    if (res.ok) {
      const data = await res.json();
      if (Array.isArray(data.items)) {
        tabObj.promptQueue = data.items;
      }
      tabObj.isQueuePaused = !!data.paused;
      renderChatQueue(tabObj);
    }
  } catch (e) {
    console.warn('Failed to load prompt queue from daemon:', e);
  }
}

const CHAT_PAGE_SIZE = 25;

// Render or update load more banner at top of chat container

function renderLoadMoreBanner(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return;
  const container = tabObj.chatMessagesEl;
  let banner = container.querySelector('.chat-load-more-container');
  const remaining = tabObj.loadedTranscriptStartIndex || 0;

  if (remaining <= 0) {
    if (banner) banner.remove();
    return;
  }

  if (!banner) {
    banner = document.createElement('div');
    banner.className = 'chat-load-more-container';
    banner.innerHTML = `
      <button type="button" class="btn-chat-load-more">
        <span class="load-more-icon">↑</span>
        <span class="load-more-text">Load older messages (${remaining} remaining)</span>
      </button>
    `;
    const btn = banner.querySelector('.btn-chat-load-more');
    if (btn) {
      btn.addEventListener('click', (e) => {
        e.preventDefault();
        loadOlderTranscriptMessages(tabObj);
      });
    }
    container.insertBefore(banner, container.firstChild);
  } else {
    const textEl = banner.querySelector('.load-more-text');
    if (textEl) {
      textEl.textContent = `Load older messages (${remaining} remaining)`;
    }
  }
}

// Load older messages chunk from in-memory transcript cache and prepend upward
function loadOlderTranscriptMessages(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return;
  if (!tabObj.allTranscriptMessages || tabObj.loadedTranscriptStartIndex <= 0) return;
  if (tabObj.isLoadingOlderTranscript) return;

  tabObj.isLoadingOlderTranscript = true;

  const container = tabObj.chatMessagesEl;
  const prevScrollHeight = container.scrollHeight;
  const prevScrollTop = container.scrollTop;

  const nextStartIndex = Math.max(0, tabObj.loadedTranscriptStartIndex - CHAT_PAGE_SIZE);
  const olderBatch = tabObj.allTranscriptMessages.slice(nextStartIndex, tabObj.loadedTranscriptStartIndex);
  tabObj.loadedTranscriptStartIndex = nextStartIndex;

  const bannerEl = container.querySelector('.chat-load-more-container');
  const insertReference = bannerEl ? bannerEl.nextSibling : container.firstChild;

  const fragment = document.createDocumentFragment();
  for (const msg of olderBatch) {
    const msgEl = createChatMessageElement(tabObj, msg);
    if (msgEl) {
      fragment.appendChild(msgEl);
    }
  }

  if (insertReference) {
    container.insertBefore(fragment, insertReference);
  } else {
    container.appendChild(fragment);
  }

  renderLoadMoreBanner(tabObj);

  // Compensate scroll position so visual content remains locked in place without jumping
  const heightDelta = container.scrollHeight - prevScrollHeight;
  container.scrollTop = prevScrollTop + heightDelta;

  tabObj.isLoadingOlderTranscript = false;
}

// Normalize prompt text by stripping CRLF to LF and trimming whitespace
function normalizePromptText(text) {
  return (text || '').replace(/\r\n/g, '\n').trim();
}

// Coalesce consecutive assistant transcript messages into single turns and deduplicate consecutive identical user turns
function coalesceTranscriptMessages(messages) {
  if (!Array.isArray(messages)) return [];
  const coalesced = [];
  for (const msg of messages) {
    const last = coalesced[coalesced.length - 1];
    if (last && last.role === 'assistant' && msg.role === 'assistant') {
      if (msg.content) {
        if (last.content) {
          if (!last.content.includes(msg.content)) {
            last.content += '\n\n' + msg.content;
          }
        } else {
          last.content = msg.content;
        }
      }
      if (msg.thinking && !last.thinking) {
        last.thinking = msg.thinking;
      }
      if (Array.isArray(msg.tool_calls) && msg.tool_calls.length > 0) {
        last.tool_calls = (last.tool_calls || []).concat(msg.tool_calls);
      }
      if (msg.timestamp) {
        last.timestamp = msg.timestamp;
      }
    } else if (last && last.role === 'user' && msg.role === 'user') {
      const lastText = normalizePromptText(last.content || last.rawPrompt || '');
      const currText = normalizePromptText(msg.content || msg.rawPrompt || '');
      if (lastText && currText && lastText === currText) {
        if (Array.isArray(msg.attachments) && msg.attachments.length > 0) {
          last.attachments = [...(last.attachments || []), ...msg.attachments];
        }
        continue;
      }
      coalesced.push({
        ...msg,
        tool_calls: Array.isArray(msg.tool_calls) ? [...msg.tool_calls] : []
      });
    } else {
      coalesced.push({
        ...msg,
        tool_calls: Array.isArray(msg.tool_calls) ? [...msg.tool_calls] : []
      });
    }
  }
  coalesced.forEach((m, idx) => {
    m.index = idx;
  });
  return coalesced;
}

function isPromptInTranscriptTail(coalesced, promptNorm, promptTimestamp, tabObj) {
  if (!Array.isArray(coalesced) || coalesced.length === 0 || !promptNorm) {
    return false;
  }
  const matchesText = (msg) => {
    if (!msg || msg.role !== 'user') return false;
    const norm = normalizePromptText(msg.content || msg.rawPrompt || '');
    return Boolean(norm && (norm === promptNorm || norm.startsWith(promptNorm) || promptNorm.startsWith(norm)));
  };

  const last = coalesced[coalesced.length - 1];
  if (last && last.role === 'user') {
    return matchesText(last);
  }

  if (last && last.role === 'assistant' && coalesced.length >= 2) {
    const prev = coalesced[coalesced.length - 2];
    if (matchesText(prev)) {
      const parseTime = (ts) => {
        if (!ts) return NaN;
        const t = (typeof ts === 'number') ? ts : new Date(ts).getTime();
        return isNaN(t) ? NaN : t;
      };
      const msgTime = parseTime(last.timestamp) || parseTime(prev.timestamp);
      if (promptTimestamp && !isNaN(msgTime)) {
        return msgTime >= (promptTimestamp - 5000);
      }
      if (tabObj) {
        return !tabObj.sending && !isSessionWorking(tabObj.session);
      }
      return true;
    }
  }

  return false;
}

// Load past conversation messages from daemon transcript (latest first, paginated upward)
async function loadChatTranscript(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl || !tabObj.session) return;
  const sessionId = tabObj.session.id;
  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
  const reqId = (tabObj._transcriptReqId || 0) + 1;
  tabObj._transcriptReqId = reqId;
  tabObj._lastTranscriptLoadAt = Date.now();

  const container = tabObj.chatMessagesEl;
  const hadMessages = container.children.length > 0;
  const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight;
  const shouldStickToBottom = !hadMessages || distanceFromBottom < 120 || hasActiveTurnUI(tabObj);
  const prevScrollTop = container.scrollTop;

  try {
    const res = await fetch(`${baseUrl}/v1/sessions/transcript?id=${encodeURIComponent(sessionId)}&format=json`);
    if (!res.ok) return;
    const data = await res.json();
    if (tabObj._transcriptReqId !== reqId) return;

    if (data && Array.isArray(data.messages)) {
      const coalesced = coalesceTranscriptMessages(data.messages);
      tabObj.allTranscriptMessages = coalesced;
      tabObj.isLoadingOlderTranscript = false;

      const liveTurnMsgEl = (tabObj.activeTurnMsgEl && tabObj.activeTurnMsgEl.classList.contains('in-flight'))
        ? tabObj.activeTurnMsgEl
        : null;
      const liveActivityEl = tabObj.activeInStreamActivityEl || null;
      const inFlightPrompt = tabObj.inFlightPrompt || null;
      const recentTurn = (!liveTurnMsgEl && tabObj.lastCompletedTurn && (Date.now() - tabObj.lastCompletedTurn.timestamp) < 10000)
        ? tabObj.lastCompletedTurn
        : null;

      const total = coalesced.length;
      if (total === 0) {
        tabObj.loadedTranscriptStartIndex = 0;
        if (!inFlightPrompt && !liveTurnMsgEl && !liveActivityEl && !container.querySelector('.chat-msg')) {
          container.innerHTML = '';
        }
        if (inFlightPrompt && inFlightPrompt.el && !inFlightPrompt.el.isConnected) {
          if (liveTurnMsgEl && liveTurnMsgEl.parentNode === container) {
            container.insertBefore(inFlightPrompt.el, liveTurnMsgEl);
          } else if (liveActivityEl && liveActivityEl.parentNode === container) {
            container.insertBefore(inFlightPrompt.el, liveActivityEl);
          } else {
            container.appendChild(inFlightPrompt.el);
          }
        }
        if (!liveTurnMsgEl && !liveActivityEl && (tabObj.sending || isSessionWorking(tabObj.session))) {
          setChatSending(tabObj, true);
        }
        if (recentTurn && !tabObj._pendingTranscriptFollowup) {
          tabObj._pendingTranscriptFollowup = setTimeout(() => {
            tabObj._pendingTranscriptFollowup = null;
            loadChatTranscript(tabObj);
          }, 600);
        }
        return;
      }

      container.innerHTML = '';

      if (total <= CHAT_PAGE_SIZE) {
        tabObj.loadedTranscriptStartIndex = 0;
        for (const msg of coalesced) {
          appendChatMessage(tabObj, msg);
        }
      } else {
        tabObj.loadedTranscriptStartIndex = total - CHAT_PAGE_SIZE;
        renderLoadMoreBanner(tabObj);
        const initialMessages = coalesced.slice(tabObj.loadedTranscriptStartIndex);
        for (const msg of initialMessages) {
          appendChatMessage(tabObj, msg);
        }
      }

      // Re-attach optimistic in-flight user prompt if not yet present in on-disk transcript
      if (inFlightPrompt && inFlightPrompt.el) {
        if (isPromptInTranscriptTail(coalesced, inFlightPrompt.text, inFlightPrompt.timestamp, tabObj)) {
          if (tabObj.sending || isSessionWorking(tabObj.session)) {
            const userBubbles = container.querySelectorAll('.chat-msg.user-msg');
            if (userBubbles.length > 0) {
              const lastUserEl = userBubbles[userBubbles.length - 1];
              if (!inFlightPrompt.el.querySelector('.chat-msg-attachments') || lastUserEl.querySelector('.chat-msg-attachments')) {
                tabObj.inFlightPrompt.el = lastUserEl;
              }
            }
          } else {
            tabObj.inFlightPrompt = null;
          }
        } else {
          container.appendChild(inFlightPrompt.el);
        }
      }

      // Re-attach live streaming assistant bubble and activity indicator if a turn is currently in flight
      if (liveTurnMsgEl) {
        const lastMsg = coalesced[coalesced.length - 1];
        const activeNorm = normalizePromptText(tabObj.activeTurnBuffer || '');
        const lastAssistantNorm = (lastMsg && lastMsg.role === 'assistant') ? normalizePromptText(lastMsg.content || '') : '';
        if (lastMsg && lastMsg.role === 'assistant' && activeNorm && lastAssistantNorm &&
            (lastAssistantNorm.includes(activeNorm) || activeNorm.includes(lastAssistantNorm))) {
          if (container.lastElementChild && container.lastElementChild.classList.contains('assistant-msg')) {
            container.lastElementChild.remove();
          }
        }
        container.appendChild(liveTurnMsgEl);
        tabObj.activeTurnMsgEl = liveTurnMsgEl;
      } else if (recentTurn) {
        // Preserve just-completed turn bubbles if on-disk transcript hasn't flushed the final turn yet
        let missingUser = false;
        if (recentTurn.userEl && recentTurn.userText) {
          if (!isPromptInTranscriptTail(coalesced, recentTurn.userText, recentTurn.timestamp - 60000, tabObj)) {
            missingUser = true;
          }
        }
        let missingAssistant = false;
        if (recentTurn.assistantEl) {
          const lastMsg = coalesced[coalesced.length - 1];
          const lastAssistantNorm = (lastMsg && lastMsg.role === 'assistant') ? normalizePromptText(lastMsg.content || '') : '';
          if (!lastMsg || lastMsg.role !== 'assistant' ||
              (recentTurn.assistantText && (!lastAssistantNorm || (!lastAssistantNorm.includes(recentTurn.assistantText) && !recentTurn.assistantText.includes(lastAssistantNorm))))) {
            missingAssistant = true;
          }
        }
        if (missingUser && recentTurn.userEl) {
          container.appendChild(recentTurn.userEl);
        }
        if (missingAssistant && recentTurn.assistantEl) {
          container.appendChild(recentTurn.assistantEl);
        }
        if (missingUser || missingAssistant) {
          if (!tabObj._pendingTranscriptFollowup) {
            tabObj._pendingTranscriptFollowup = setTimeout(() => {
              tabObj._pendingTranscriptFollowup = null;
              loadChatTranscript(tabObj);
            }, 600);
          }
        } else {
          tabObj.lastCompletedTurn = null;
        }
      }
      if (liveActivityEl && hasActiveTurnUI(tabObj)) {
        container.appendChild(liveActivityEl);
        tabObj.activeInStreamActivityEl = liveActivityEl;
      } else if (!liveTurnMsgEl && (tabObj.sending || isSessionWorking(tabObj.session))) {
        setChatSending(tabObj, true);
      }

      if (shouldStickToBottom) {
        container.scrollTop = container.scrollHeight;
        requestAnimationFrame(() => {
          if (tabObj.chatMessagesEl) {
            tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
          }
        });
      } else {
        container.scrollTop = prevScrollTop;
      }

      if (Array.isArray(data.running_subagents)) {
        tabObj.runningSubagents = data.running_subagents;
        renderSubagentsBar(tabObj);
      } else if (Array.isArray(data.subagents)) {
        tabObj.runningSubagents = data.subagents;
        renderSubagentsBar(tabObj);
      } else {
        fetchRunningSubagents(tabObj);
      }

      if (tabObj.session && tabObj.session.blocked && (tabObj.session.state === 2 || tabObj.session.state === 'blocked')) {
        renderInteractiveBlockCard(tabObj, tabObj.session.blocked);
      }
    } else {
      fetchRunningSubagents(tabObj);
      if (tabObj.session && tabObj.session.blocked && (tabObj.session.state === 2 || tabObj.session.state === 'blocked')) {
        renderInteractiveBlockCard(tabObj, tabObj.session.blocked);
      }
    }
  } catch (e) {
    console.warn('Failed to load chat transcript:', e);
  }
}

// Copy text to clipboard with modern API and textarea fallback
async function copyTextToClipboard(text) {
  if (!text) return false;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch (e) {
    // fallback below
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    ta.style.left = '-9999px';
    ta.style.pointerEvents = 'none';
    document.body.appendChild(ta);
    ta.select();
    const success = document.execCommand('copy');
    document.body.removeChild(ta);
    return success;
  } catch (e) {
    console.warn('Failed to copy to clipboard:', e);
    return false;
  }
}

// Attach code block copy buttons to all <pre> elements inside container
function attachCodeBlockCopyButtons(container) {
  if (!container) return;
  const preEls = container.querySelectorAll('pre');
  preEls.forEach(pre => {
    if (pre.closest('.mermaid-diagram-block')) return;
    if (pre.querySelector('.btn-copy-code')) return;
    if (pre.closest('.chat-thinking') || pre.closest('.chat-tool-card')) return;

    const copyBtn = document.createElement('button');
    copyBtn.className = 'btn-copy-code';
    copyBtn.type = 'button';
    copyBtn.title = 'Copy code';
    copyBtn.setAttribute('aria-label', 'Copy code snippet');
    copyBtn.innerHTML = `
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
      </svg>
      <span>Copy</span>
    `;
    copyBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      e.preventDefault();
      const codeEl = pre.querySelector('code');
      let text = '';
      if (codeEl) {
        text = codeEl.innerText;
      } else {
        const clone = pre.cloneNode(true);
        const btn = clone.querySelector('.btn-copy-code');
        if (btn) btn.remove();
        text = clone.innerText;
      }
      text = text.replace(/\r\n/g, '\n').replace(/\n+$/, '');
      const ok = await copyTextToClipboard(text);
      if (ok) {
        copyBtn.classList.add('copied');
        copyBtn.innerHTML = `
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"></polyline>
          </svg>
          <span>Copied!</span>
        `;
        setTimeout(() => {
          copyBtn.classList.remove('copied');
          copyBtn.innerHTML = `
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
            </svg>
            <span>Copy</span>
          `;
        }, 1500);
      }
    });
    pre.appendChild(copyBtn);
  });
  renderMermaidDiagrams(container, copyTextToClipboard);
}

// Known file extensions for linkification & action pills
const KNOWN_CHAT_FILE_EXTS = new Set([
  'png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico',
  'html', 'htm', 'pdf', 'md', 'markdown', 'txt', 'csv', 'json', 'yaml', 'yml', 'xml', 'log',
  'js', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'sh', 'bash', 'zsh', 'css', 'scss',
  'sql', 'java', 'c', 'cpp', 'h', 'swift', 'kt', 'dart', 'zip', 'tar', 'gz', 'mp4', 'webm', 'mp3', 'wav'
]);

const CHAT_IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico']);
const CHAT_VIDEO_EXTS = new Set(['mp4', 'webm']);
const CHAT_AUDIO_EXTS = new Set(['mp3', 'wav']);

// Parse file paths with optional line numbers / line ranges:
// e.g. /path/to/file.go#L75-L176, /path/to/file.go#L75, /path/to/file.go:75:10, /path/to/file.go:75-176, /path/to/file.go:75
function parseLineRange(filePath) {
  let clean = (filePath || '').trim();
  if (clean.startsWith('file://')) {
    clean = clean.replace(/^file:\/\//, '');
  }
  let baseFilePath = clean;
  let lineSuffix = '';
  let startLine = null;
  let endLine = null;
  let isColonCol = false;

  const hashMatch = clean.match(/#L(\d+)(?:-L?(\d+))?$/i);
  const colonMatch = clean.match(/:(\d+)(?:([-:])(\d+))?$/);

  if (hashMatch) {
    baseFilePath = clean.slice(0, hashMatch.index);
    startLine = hashMatch[1];
    endLine = hashMatch[2];
    lineSuffix = endLine ? `:${startLine}-${endLine}` : `:${startLine}`;
  } else if (colonMatch) {
    baseFilePath = clean.slice(0, colonMatch.index);
    startLine = colonMatch[1];
    const sep = colonMatch[2];
    endLine = colonMatch[3];
    if (endLine) {
      if (sep === ':') {
        isColonCol = true;
        lineSuffix = `:${startLine}:${endLine}`;
      } else {
        lineSuffix = `:${startLine}-${endLine}`;
      }
    } else {
      lineSuffix = `:${startLine}`;
    }
  }

  baseFilePath = baseFilePath.split('?')[0];

  const vscodePath = startLine
    ? (isColonCol && endLine ? `${baseFilePath}:${startLine}:${endLine}` : `${baseFilePath}:${startLine}`)
    : baseFilePath;

  return {
    rawPath: filePath,
    baseFilePath,
    lineSuffix,
    startLine,
    endLine,
    vscodePath
  };
}

// Construct URL to stream/view file content through the daemon
function getChatFileContentUrl(filePath, session, download = false) {
  const { baseFilePath } = parseLineRange(filePath);
  let effectivePath = baseFilePath;
  if (!baseFilePath.startsWith('/') && !baseFilePath.startsWith('~') && !/^[a-zA-Z]:[\\/]/.test(baseFilePath) && session && session.cwd) {
    effectivePath = `${session.cwd.replace(/[\\/]+$/, '')}/${baseFilePath}`;
  }
  let url = `/v1/files/content?path=${encodeURIComponent(effectivePath)}`;
  if (session) {
    if (session.id) url += `&session_id=${encodeURIComponent(session.id)}`;
    if (session.host) url += `&host=${encodeURIComponent(session.host)}`;
  }
  if (download) url += '&download=1';
  const token = getAuthToken();
  if (token) url += `&token=${encodeURIComponent(token)}`;
  return url;
}

// Open file in local application (e.g. Preview, Browser, VS Code)
async function openChatFileInApp(filePath, session, app = 'default') {
  const host = session ? session.host : null;
  const sessionId = session ? session.id : null;
  const { baseFilePath, vscodePath } = parseLineRange(filePath);
  if (app === 'vscode') {
    const targetPath = (!vscodePath.startsWith('/') && session && session.cwd)
      ? `${session.cwd.replace(/\/$/, '')}/${vscodePath}`
      : vscodePath;
    return openInVSCode(targetPath, host);
  }
  try {
    const res = await fetch('/v1/files/open', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        path: baseFilePath,
        session_id: sessionId,
        host: host,
        app: app
      })
    });
    const filename = baseFilePath.split('/').pop();
    if (!res.ok) {
      const errText = await res.text().catch(() => res.statusText);
      showUploadToast(`Failed to open ${filename}: ${errText}`, 'error');
    } else {
      const data = await res.json().catch(() => ({}));
      const appLabel = app === 'preview' ? 'Preview' : (data.remote ? 'macOS App (staged from remote)' : 'App');
      showUploadToast(`Opened ${filename} in ${appLabel}`, 'success');
    }
  } catch (err) {
    showUploadToast(`Error opening file: ${err.message}`, 'error');
  }
}

// Check if string looks like a file path
function isChatFilePath(str) {
  if (!str || typeof str !== 'string') return false;
  const trimmed = str.trim();
  if (trimmed.length < 3 || trimmed.length > 250) return false;
  if (trimmed.includes('\n') || trimmed.includes('\r')) return false;
  if (trimmed.startsWith('http://') || trimmed.startsWith('https://') || trimmed.startsWith('conversation://') || trimmed.includes('@')) return false;
  if (trimmed.includes(' ') && !trimmed.startsWith('/')) return false;

  let pathWithoutQuery = trimmed.split('?')[0];
  if (pathWithoutQuery.startsWith('file://')) {
    pathWithoutQuery = pathWithoutQuery.replace(/^file:\/\//, '');
  }
  // Strip line range anchors before evaluating file extension
  pathWithoutQuery = pathWithoutQuery.replace(/#L\d+(?:-L?\d+)?$/i, '').replace(/:\d+(?:[-:]\d+)?$/, '');
  pathWithoutQuery = pathWithoutQuery.split('#')[0];

  const parts = pathWithoutQuery.split('.');
  if (parts.length < 2) return false;
  const ext = parts.pop().toLowerCase();
  if (!KNOWN_CHAT_FILE_EXTS.has(ext)) return false;

  // Filter out common code expressions like obj.property if not common file extension
  if (/^[a-zA-Z0-9_]+\.[a-zA-Z0-9_]+$/.test(pathWithoutQuery) && !['html', 'htm', 'png', 'jpg', 'jpeg', 'gif', 'svg', 'pdf', 'md', 'txt', 'csv', 'json', 'yaml', 'yml', 'css', 'js', 'ts', 'go', 'py', 'mp4', 'webm', 'mp3', 'wav'].includes(ext)) {
    return false;
  }

  return true;
}

// Authentic Application SVG Icons for File Context Menu
const APP_ICONS = {
  vscode: `<svg width="18" height="18" viewBox="0 0 256 256" fill="none" class="menu-app-svg">
    <path d="M177.6 2.3a12.8 12.8 0 0 0-11 3.5L78 88.6 36.3 56.4a12.8 12.8 0 0 0-16.7 1.7L4.7 74.2a12.8 12.8 0 0 0 1.6 17.6l38.7 34.6L6.3 161a12.8 12.8 0 0 0-1.6 17.6l14.9 16.1a12.8 12.8 0 0 0 16.7 1.7l41.7-32.2 88.6 82.8a12.8 12.8 0 0 0 20.3-9.5V11.8a12.8 12.8 0 0 0-9.3-9.5z" fill="#007ACC"/>
    <path d="M177.6 2.3a12.8 12.8 0 0 0-11 3.5L78 88.6l19.8 18.2 68.8-63.4a6.4 6.4 0 0 1 10.8 4.7v-36a12.8 12.8 0 0 0-9.8-9.8z" fill="#1F9CF0"/>
    <path d="M166.6 207.9l-68.8-63.4L78 162.7l88.6 82.8a12.8 12.8 0 0 0 20.3-9.5v-36a6.4 6.4 0 0 1-10.8 4.7l.5 3.2z" fill="#0065A9"/>
    <path d="M177.4 48.1a6.4 6.4 0 0 0-10.8-4.7L78 125.7l88.6 82.2a6.4 6.4 0 0 0 10.8-4.7V48.1z" fill="#007ACC"/>
  </svg>`,

  preview: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
    <defs>
      <linearGradient id="previewSky" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" stop-color="#38bdf8"/>
        <stop offset="50%" stop-color="#60a5fa"/>
        <stop offset="100%" stop-color="#818cf8"/>
      </linearGradient>
      <linearGradient id="previewGlass" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stop-color="#ffffff" stop-opacity="0.85"/>
        <stop offset="50%" stop-color="#e0f2fe" stop-opacity="0.3"/>
        <stop offset="100%" stop-color="#38bdf8" stop-opacity="0.1"/>
      </linearGradient>
      <linearGradient id="previewHandle" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stop-color="#475569"/>
        <stop offset="50%" stop-color="#1e293b"/>
        <stop offset="100%" stop-color="#0f172a"/>
      </linearGradient>
    </defs>
    <rect x="2.5" y="3.5" width="15" height="15" rx="2" fill="#cbd5e1" transform="rotate(-7 10 11)"/>
    <rect x="2.5" y="2" width="16" height="17" rx="2" fill="#ffffff"/>
    <rect x="3.8" y="3.2" width="13.4" height="11" rx="1.2" fill="url(#previewSky)"/>
    <circle cx="13" cy="5.8" r="1.8" fill="#fbbf24"/>
    <path d="M3.8 12.5l3.8-4.2 3 3 2.2-2 4.4 4.8v.1H3.8v-1.7z" fill="#22c55e"/>
    <path d="M6 14.2l3.4-3.2 2.8 2.2 2.8-2.6 2.2 2.4v1.2H6vz" fill="#15803d"/>
    <circle cx="14" cy="11" r="5" fill="none" stroke="#94a3b8" stroke-width="1.8"/>
    <circle cx="14" cy="11" r="4.1" fill="url(#previewGlass)"/>
    <path d="M11.8 8.8a3.2 3.2 0 0 1 3.2-0.2" stroke="#ffffff" stroke-width="1" stroke-linecap="round" fill="none"/>
    <circle cx="17.6" cy="14.6" r="1" fill="#94a3b8"/>
    <line x1="17.6" y1="14.6" x2="22.2" y2="19.2" stroke="url(#previewHandle)" stroke-width="2.6" stroke-linecap="round"/>
    <line x1="18.2" y1="15.2" x2="21.6" y2="18.6" stroke="#94a3b8" stroke-width="1" stroke-linecap="round"/>
  </svg>`,

  browser: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
    <circle cx="12" cy="12" r="10" fill="#0284c7"/>
    <circle cx="12" cy="12" r="9" fill="none" stroke="#ffffff" stroke-width="0.8" stroke-dasharray="1.2 1.5" opacity="0.6"/>
    <polygon points="12,4.5 14.2,12 12,19.5 9.8,12" fill="#ffffff" opacity="0.25"/>
    <polygon points="12,4.5 14.2,12 12,12" fill="#ef4444"/>
    <polygon points="12,4.5 9.8,12 12,12" fill="#dc2626"/>
    <polygon points="12,19.5 14.2,12 12,12" fill="#ffffff"/>
    <polygon points="12,19.5 9.8,12 12,12" fill="#e2e8f0"/>
    <circle cx="12" cy="12" r="1.3" fill="#ffffff"/>
  </svg>`,

  app: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
    <rect x="2.5" y="3" width="19" height="18" rx="4" fill="#3b82f6"/>
    <rect x="2.5" y="3" width="19" height="5.5" rx="3" fill="#1d4ed8"/>
    <circle cx="5.5" cy="5.7" r="1.1" fill="#ef4444"/>
    <circle cx="8.5" cy="5.7" r="1.1" fill="#eab308"/>
    <circle cx="11.5" cy="5.7" r="1.1" fill="#22c55e"/>
    <path d="M7 14.5l3.2 3.2 6.8-6.8" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`,

  copy: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="menu-app-svg">
    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
  </svg>`,

  markdown: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" class="menu-app-svg">
    <rect x="2.5" y="3" width="19" height="18" rx="3" fill="#6366f1"/>
    <path d="M6 15V9l3 3.2L12 9v6" stroke="#ffffff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
    <path d="M15 12l2.5 3 2.5-3M17.5 9v6" stroke="#ffffff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`
};

function createChatFilePill(filePath, session, customLabel = '') {
  const { baseFilePath, lineSuffix, vscodePath } = parseLineRange(filePath);
  const ext = baseFilePath.split('?')[0].split('.').pop().toLowerCase();
  const isMarkdown = (ext === 'md' || ext === 'markdown');
  const isImg = CHAT_IMAGE_EXTS.has(ext);
  const isVideo = CHAT_VIDEO_EXTS.has(ext);
  const isAudio = CHAT_AUDIO_EXTS.has(ext);
  const isHtml = (ext === 'html' || ext === 'htm');
  const isPdf = (ext === 'pdf');
  const filename = baseFilePath.split('/').pop();

  let icon = '📄';
  if (isImg) icon = '🖼️';
  else if (isVideo) icon = '🎬';
  else if (isAudio) icon = '🎵';
  else if (isHtml) icon = '🌐';
  else if (isPdf) icon = '📑';
  else if (['js', 'jsx', 'ts', 'tsx', 'go', 'py', 'rs', 'c', 'cpp', 'swift'].includes(ext)) icon = '💻';

  const cleanLabel = (customLabel || '').trim();
  let displayName = `${filename}${lineSuffix}`;
  if (cleanLabel && cleanLabel !== filePath && cleanLabel !== filename && cleanLabel !== baseFilePath && !cleanLabel.startsWith('file://')) {
    displayName = `${cleanLabel} (${filename}${lineSuffix})`;
  }

  const pill = document.createElement('span');
  pill.className = 'chat-file-pill';
  pill.title = isMarkdown
    ? `${vscodePath} (Click to view in Ackbar, ▾ or right-click for actions)`
    : `${vscodePath} (Click for actions)`;

  pill.innerHTML = `
    <span class="chat-file-icon">${icon}</span>
    <span class="chat-file-name">${escapeHtml(displayName)}</span>
    <span class="chat-file-chevron">▾</span>
  `;

  pill.addEventListener('click', (e) => {
    e.preventDefault();
    e.stopPropagation();
    const clickedChevron = !!e.target.closest('.chat-file-chevron');
    if (isMarkdown && !clickedChevron) {
      hideChatFileContextMenu();
      openDocViewerTab(baseFilePath, filename, (session && session.host) || 'local', session);
      return;
    }
    if (activeFileMenuPill === pill && fileContextMenuEl && fileContextMenuEl.style.display !== 'none') {
      hideChatFileContextMenu();
    } else {
      showChatFileContextMenu(pill, vscodePath, session);
    }
  });

  pill.addEventListener('contextmenu', (e) => {
    e.preventDefault();
    e.stopPropagation();
    showChatFileContextMenu(pill, vscodePath, session, e.clientX, e.clientY);
  });

  return pill;
}

// Build an inline media preview card (Video or Audio)
function createChatMediaPreviewCard(filePath, session, caption = '') {
  const { baseFilePath } = parseLineRange(filePath);
  const ext = baseFilePath.split('?')[0].split('.').pop().toLowerCase();
  const isVideo = CHAT_VIDEO_EXTS.has(ext);
  const filename = baseFilePath.split('/').pop();
  const contentUrl = getChatFileContentUrl(baseFilePath, session);

  const cleanCaption = (caption || '').trim();
  const hasCaption = cleanCaption && cleanCaption !== filename;
  const captionHtml = hasCaption ? `<div class="chat-file-preview-caption">${escapeHtml(cleanCaption)}</div>` : '';
  const icon = isVideo ? '🎬' : '🎵';

  const card = document.createElement('div');
  card.className = 'chat-file-preview-card';
  card.innerHTML = `
    <div class="chat-file-preview-header">
      <span>${icon} ${escapeHtml(filename)}</span>
      <button class="chat-file-btn" data-action="preview" title="Open in System App" style="display:inline-flex;align-items:center;gap:4px;">
        ${APP_ICONS.app} <span>Open in App</span>
      </button>
    </div>
    ${captionHtml}
    <div class="chat-file-preview-body">
      ${isVideo ? `
        <video controls preload="metadata" style="max-width:100%;border-radius:6px;max-height:400px;background:#000;">
          <source src="${escapeHtml(contentUrl)}" type="${ext === 'webm' ? 'video/webm' : 'video/mp4'}">
          Your browser does not support the video tag.
        </video>
      ` : `
        <audio controls preload="metadata" style="width:100%;">
          <source src="${escapeHtml(contentUrl)}" type="${ext === 'wav' ? 'audio/wav' : 'audio/mpeg'}">
          Your browser does not support the audio element.
        </audio>
      `}
    </div>
  `;

  const previewBtn = card.querySelector('button[data-action="preview"]');
  if (previewBtn) {
    previewBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      openChatFileInApp(baseFilePath, session, 'default');
    });
  }

  return card;
}

// Build an inline image preview card
function createChatImagePreviewCard(filePath, session, caption = '') {
  let cleanPath = filePath.trim();
  if (cleanPath.startsWith('file://')) {
    cleanPath = cleanPath.replace(/^file:\/\//, '');
  }
  const filename = cleanPath.split('/').pop();
  const contentUrl = getChatFileContentUrl(cleanPath, session);

  const cleanCaption = (caption || '').trim();
  const hasCaption = cleanCaption && cleanCaption !== filename;
  const captionHtml = hasCaption ? `<div class="chat-file-preview-caption">${escapeHtml(cleanCaption)}</div>` : '';
  const altText = cleanCaption || filename;

  const card = document.createElement('div');
  card.className = 'chat-file-preview-card';
  card.innerHTML = `
    <div class="chat-file-preview-header">
      <span>🖼️ ${escapeHtml(filename)}</span>
      <button class="chat-file-btn" data-action="preview" title="Open in macOS Preview" style="display:inline-flex;align-items:center;gap:4px;">
        ${APP_ICONS.preview} <span>Open in Preview</span>
      </button>
    </div>
    ${captionHtml}
    <div class="chat-file-preview-body">
      <img src="${escapeHtml(contentUrl)}" alt="${escapeHtml(altText)}" loading="lazy" title="Click to open full size" />
    </div>
  `;

  const img = card.querySelector('img');
  if (img) {
    img.addEventListener('click', () => {
      window.open(contentUrl, '_blank');
    });
  }

  const previewBtn = card.querySelector('button[data-action="preview"]');
  if (previewBtn) {
    previewBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      openChatFileInApp(cleanPath, session, 'preview');
    });
  }

  return card;
}

// Linkify file paths inside chat message container
function linkifyChatFiles(containerEl, session) {
  if (!containerEl) return;

  // 1. Process <a> markdown links
  containerEl.querySelectorAll('a').forEach(a => {
    if (a.dataset.fileLinkified) return;
    const href = a.getAttribute('href') || '';
    const customLabel = (a.textContent || '').trim();

    // 1a. Detect conversation:// links
    if (href.startsWith('conversation://')) {
      a.dataset.fileLinkified = 'true';
      const convId = href.replace(/^conversation:\/\//, '').split('?')[0].split('#')[0].trim();
      const displayText = customLabel && customLabel !== href ? customLabel : convId.slice(0, 8);
      const pill = document.createElement('span');
      pill.className = 'chat-subagent-pill';
      pill.dataset.conversationId = convId;
      pill.title = `Subagent conversation: ${convId} (Click to open session)`;
      pill.innerHTML = `🤖 ${escapeHtml(displayText)}`;
      pill.addEventListener('click', (e) => {
        e.preventDefault();
        e.stopPropagation();
        const matchedSess = (state.sessions || []).find(s =>
          s.native_id === convId ||
          (s.id && (s.id === convId || s.id.endsWith(convId) || s.id.endsWith(`:${convId}`)))
        );
        if (matchedSess) {
          openSessionInTab(matchedSess);
        } else {
          if (typeof openTranscriptViewerTab === 'function') {
            openTranscriptViewerTab({ id: convId, native_id: convId, title: `Subagent ${convId.slice(0, 8)}` });
          } else {
            showUploadToast(`Subagent session ${convId.slice(0, 8)} not found`, 'info');
          }
        }
      });
      a.replaceWith(pill);
      return;
    }

    let cleanPath = '';
    if (href.startsWith('file://')) {
      cleanPath = href.replace(/^file:\/\//, '');
    } else if (!href.startsWith('http://') && !href.startsWith('https://') && !href.startsWith('#') && !href.startsWith('mailto:')) {
      const { baseFilePath } = parseLineRange(href);
      const ext = baseFilePath.split('?')[0].split('.').pop().toLowerCase();
      if (KNOWN_CHAT_FILE_EXTS.has(ext)) {
        cleanPath = href;
      }
    }
    if (cleanPath && isChatFilePath(cleanPath)) {
      a.dataset.fileLinkified = 'true';
      const pill = createChatFilePill(cleanPath, session, customLabel);
      a.replaceWith(pill);
    } else if (!href.startsWith('#') && !href.startsWith('javascript:')) {
      a.setAttribute('target', '_blank');
      a.setAttribute('rel', 'noopener noreferrer');
    }
  });

  // 2. Process inline <code> elements (excluding multiline <pre><code> blocks)
  containerEl.querySelectorAll('code').forEach(code => {
    if (code.closest('pre')) return;
    if (code.dataset.fileLinkified) return;
    const text = (code.textContent || '').trim();
    if (isChatFilePath(text)) {
      code.dataset.fileLinkified = 'true';
      const pill = createChatFilePill(text, session);
      const { baseFilePath } = parseLineRange(text);
      const ext = baseFilePath.split('?')[0].split('.').pop().toLowerCase();
      if (CHAT_IMAGE_EXTS.has(ext) && !code.closest('table')) {
        const preview = createChatImagePreviewCard(text, session);
        if (code.parentNode) {
          code.parentNode.replaceChild(pill, code);
          pill.parentNode.insertBefore(preview, pill.nextSibling);
        }
      } else if ((CHAT_VIDEO_EXTS.has(ext) || CHAT_AUDIO_EXTS.has(ext)) && !code.closest('table')) {
        const preview = createChatMediaPreviewCard(text, session);
        if (code.parentNode) {
          code.parentNode.replaceChild(pill, code);
          pill.parentNode.insertBefore(preview, pill.nextSibling);
        }
      } else {
        if (code.parentNode) {
          code.parentNode.replaceChild(pill, code);
        }
      }
    }
  });

  // 3. Process tool cards (Write, Edit, Read tools)
  containerEl.querySelectorAll('.chat-tool-card').forEach(card => {
    if (card.dataset.fileBarProcessed) return;
    card.dataset.fileBarProcessed = 'true';

    const contentEl = card.querySelector('.chat-tool-content');
    const text = contentEl ? contentEl.textContent || '' : '';

    // Match path parameter in tool input (e.g. {"path": "...", "file_path": "..."})
    const pathMatches = text.match(/"(?:file_)?path"\s*:\s*"([^"]+)"/i) ||
                        text.match(/(?:created|written|saved|editing|writing|reading)\s+(?:file\s+)?([a-zA-Z0-9_\-\.\/]+)/i);

    if (pathMatches && pathMatches[1] && isChatFilePath(pathMatches[1])) {
      const filePath = pathMatches[1];
      const bar = document.createElement('div');
      bar.className = 'chat-tool-file-bar';
      bar.innerHTML = `<span style="font-size: 10.5px; color: var(--text-dim);">File:</span>`;
      bar.appendChild(createChatFilePill(filePath, session));
      if (card.querySelector('.chat-tool-content')) {
        card.insertBefore(bar, card.querySelector('.chat-tool-content'));
      } else {
        card.appendChild(bar);
      }
    }
  });

  // 4. Process local markdown <img> elements
  containerEl.querySelectorAll('img').forEach(img => {
    if (img.dataset.fileLinkified || img.closest('.chat-file-preview-card') || img.classList.contains('chat-attachment-thumb')) return;
    const rawSrc = (img.getAttribute('src') || '').trim();
    if (!rawSrc || rawSrc.startsWith('http://') || rawSrc.startsWith('https://') || rawSrc.startsWith('data:') || rawSrc.startsWith('blob:') || rawSrc.startsWith('/v1/files/content')) {
      return;
    }
    let cleanPath = rawSrc;
    if (cleanPath.startsWith('file://')) {
      cleanPath = cleanPath.replace(/^file:\/\//, '');
    }
    try {
      cleanPath = decodeURIComponent(cleanPath);
    } catch (_) {}

    if (isChatFilePath(cleanPath)) {
      img.dataset.fileLinkified = 'true';
      const altText = (img.getAttribute('alt') || '').trim();
      const { baseFilePath } = parseLineRange(cleanPath);
      const ext = baseFilePath.split('?')[0].split('.').pop().toLowerCase();
      let card;
      if (CHAT_VIDEO_EXTS.has(ext) || CHAT_AUDIO_EXTS.has(ext)) {
        card = createChatMediaPreviewCard(cleanPath, session, altText);
      } else {
        card = createChatImagePreviewCard(cleanPath, session, altText);
      }
      const parentP = (img.parentElement && img.parentElement.tagName.toLowerCase() === 'p' && img.parentElement.childNodes.length === 1)
        ? img.parentElement
        : null;
      if (parentP) {
        parentP.replaceWith(card);
      } else {
        img.replaceWith(card);
      }
    }
  });

  // 5. Initialize interactive carousel controls on .chat-carousel
  containerEl.querySelectorAll('.chat-carousel').forEach(carouselEl => {
    if (carouselEl.dataset.carouselInitialized) return;
    carouselEl.dataset.carouselInitialized = 'true';

    const slides = Array.from(carouselEl.querySelectorAll('.chat-carousel-slide'));
    if (slides.length <= 1) return;

    if (!carouselEl.dataset.carouselMode) {
      carouselEl.dataset.carouselMode = 'slide';
    }

    let currentIndex = slides.findIndex(s => s.dataset.active === 'true');
    if (currentIndex === -1) {
      currentIndex = 0;
      slides[0].dataset.active = 'true';
    }

    const headerEl = document.createElement('div');
    headerEl.className = 'chat-carousel-header';
    headerEl.innerHTML = `
      <div class="chat-carousel-title">
        <span>🖼️ Carousel</span>
        <span class="chat-carousel-counter">${currentIndex + 1} / ${slides.length}</span>
      </div>
      <div class="chat-carousel-nav">
        <button type="button" class="chat-carousel-btn chat-carousel-prev" title="Previous slide" aria-label="Previous slide">‹ Prev</button>
        <button type="button" class="chat-carousel-btn chat-carousel-next" title="Next slide" aria-label="Next slide">Next ›</button>
        <button type="button" class="chat-carousel-btn chat-carousel-mode-btn" title="Toggle Grid / Slide View" aria-label="Toggle Grid / Slide View">
          <span class="mode-icon">⊞</span> <span class="mode-label">Grid</span>
        </button>
      </div>
    `;

    carouselEl.insertBefore(headerEl, carouselEl.firstChild);

    const counterEl = headerEl.querySelector('.chat-carousel-counter');
    const prevBtn = headerEl.querySelector('.chat-carousel-prev');
    const nextBtn = headerEl.querySelector('.chat-carousel-next');
    const modeBtn = headerEl.querySelector('.chat-carousel-mode-btn');
    const modeLabel = headerEl.querySelector('.mode-label');

    function updateSlideVisibility() {
      slides.forEach((slide, idx) => {
        if (idx === currentIndex) {
          slide.dataset.active = 'true';
        } else {
          delete slide.dataset.active;
        }
      });
      if (counterEl) {
        counterEl.textContent = `${currentIndex + 1} / ${slides.length}`;
      }
    }

    prevBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      currentIndex = (currentIndex - 1 + slides.length) % slides.length;
      updateSlideVisibility();
    });

    nextBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      currentIndex = (currentIndex + 1) % slides.length;
      updateSlideVisibility();
    });

    modeBtn.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      const currentMode = carouselEl.dataset.carouselMode || 'slide';
      const newMode = currentMode === 'slide' ? 'grid' : 'slide';
      carouselEl.dataset.carouselMode = newMode;
      if (modeLabel) {
        modeLabel.textContent = newMode === 'grid' ? 'Slide' : 'Grid';
      }
      if (prevBtn) prevBtn.style.display = newMode === 'grid' ? 'none' : 'inline-flex';
      if (nextBtn) nextBtn.style.display = newMode === 'grid' ? 'none' : 'inline-flex';
      if (counterEl) counterEl.style.display = newMode === 'grid' ? 'none' : 'inline';
    });

    updateSlideVisibility();
  });
}

// Attach message-level copy and speech button handlers
function attachChatMessageListeners(msgEl, rawContent, tabObj) {
  if (!msgEl) return;
  if (rawContent) {
    msgEl.dataset.rawContent = rawContent;
  }
  const copyBtn = msgEl.querySelector('.btn-copy-chat-msg');
  if (copyBtn && !copyBtn._bound) {
    copyBtn._bound = true;
    copyBtn.addEventListener('click', async (e) => {
      e.stopPropagation();
      e.preventDefault();
      let textToCopy = msgEl.dataset.rawContent || rawContent;
      if (!textToCopy) {
        const bodyEl = msgEl.querySelector('.chat-msg-body') || msgEl.querySelector('.msg-body') || msgEl.querySelector('.markdown-body');
        if (bodyEl) {
          const clone = bodyEl.cloneNode(true);
          clone.querySelectorAll('.btn-copy-code, .chat-streaming-cursor').forEach(el => el.remove());
          textToCopy = clone.innerText || clone.textContent || '';
        }
      }
      if (!textToCopy) return;
      textToCopy = textToCopy.replace(/\r\n/g, '\n').trim();
      const ok = await copyTextToClipboard(textToCopy);
      if (ok) {
        copyBtn.classList.add('copied');
        copyBtn.title = 'Copied!';
        const origSvg = copyBtn.innerHTML;
        copyBtn.innerHTML = `
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"></polyline>
          </svg>
        `;
        setTimeout(() => {
          copyBtn.classList.remove('copied');
          copyBtn.title = 'Copy message';
          copyBtn.innerHTML = origSvg;
        }, 1500);
      }
    });
  }

  const readBtn = msgEl.querySelector('.btn-read-chat-msg');
  if (readBtn && !readBtn._bound) {
    readBtn._bound = true;
    readBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      e.preventDefault();
      handleReadChatMessage(tabObj, msgEl, false);
    });
  }

  const stopBtn = msgEl.querySelector('.btn-stop-chat-msg');
  if (stopBtn && !stopBtn._bound) {
    stopBtn._bound = true;
    stopBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      e.preventDefault();
      stopActiveSpeech();
    });
  }

  const replayBtn = msgEl.querySelector('.btn-replay-chat-msg');
  if (replayBtn && !replayBtn._bound) {
    replayBtn._bound = true;
    replayBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      e.preventDefault();
      handleReplayChatMessage(tabObj, msgEl);
    });
  }

  const fullReadBtn = msgEl.querySelector('.btn-full-read-chat-msg');
  if (fullReadBtn && !fullReadBtn._bound) {
    fullReadBtn._bound = true;
    fullReadBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      e.preventDefault();
      handleReadChatMessage(tabObj, msgEl, true);
    });
  }
}

// Parse tool call string into tool name and detail (e.g. "Bash: ls -la" -> { name: "Bash", detail: "ls -la" })
function parseToolCall(tc) {
  if (!tc || typeof tc !== 'string') return { name: 'tool', detail: '' };
  const trimmed = tc.trim();
  // Pattern 1: "ToolName: detail"
  const colonIdx = trimmed.indexOf(': ');
  if (colonIdx > 0 && colonIdx < 40) {
    return {
      name: trimmed.substring(0, colonIdx).trim(),
      detail: trimmed.substring(colonIdx + 2).trim()
    };
  }
  // Pattern 2: "tool_name (action description)"
  const parenMatch = trimmed.match(/^([a-zA-Z0-9_\-]+)\s*\((.+)\)$/);
  if (parenMatch) {
    return {
      name: parenMatch[1].trim(),
      detail: parenMatch[2].trim()
    };
  }
  return { name: trimmed, detail: '' };
}

// Generate collapsed tool group summary label (e.g. "Bash x12" or "5 tools (Bash x3, Grep x2)")
function getToolGroupSummaryLabel(toolCalls) {
  if (!toolCalls || toolCalls.length === 0) return '⚡ Tools';
  const parsed = toolCalls.map(parseToolCall);
  const counts = new Map();
  for (const p of parsed) {
    counts.set(p.name, (counts.get(p.name) || 0) + 1);
  }
  const uniqueTools = Array.from(counts.keys());
  const total = toolCalls.length;

  if (uniqueTools.length === 1) {
    const name = uniqueTools[0];
    if (total > 1) {
      return `⚡ ${name} x${total}`;
    }
    if (parsed[0].detail) {
      const shortDetail = parsed[0].detail.length > 50 ? parsed[0].detail.substring(0, 47) + '...' : parsed[0].detail;
      return `⚡ ${name}: ${shortDetail}`;
    }
    return `⚡ ${name}`;
  }

  // Multiple unique tools
  const breakdown = uniqueTools.map(name => `${name} x${counts.get(name)}`).join(', ');
  return `⚡ ${total} tools (${breakdown})`;
}

// Render a collapsed tool group card for assistant messages
function renderToolGroupHtml(toolCalls) {
  if (!toolCalls || toolCalls.length === 0) return '';
  const summaryLabel = getToolGroupSummaryLabel(toolCalls);
  const parsed = toolCalls.map(parseToolCall);
  const total = toolCalls.length;

  const itemsHtml = parsed.map((item, idx) => {
    const detailHtml = item.detail
      ? `<div class="chat-tool-call-detail"><code>${escapeHtml(item.detail)}</code></div>`
      : '';
    return `
      <div class="chat-tool-call-item">
        <div class="chat-tool-call-header">
          <span class="chat-tool-call-idx">#${idx + 1}</span>
          <span class="chat-tool-call-badge">${escapeHtml(item.name)}</span>
        </div>
        ${detailHtml}
      </div>
    `;
  }).join('');

  return `
    <details class="chat-tool-card chat-tool-group">
      <summary>
        <span class="tool-group-title">${escapeHtml(summaryLabel)}</span>
        <span class="tool-group-pill">${total} ${total === 1 ? 'action' : 'actions'}</span>
      </summary>
      <div class="chat-tool-content chat-tool-group-content">
        <div class="chat-tool-call-list">
          ${itemsHtml}
        </div>
      </div>
    </details>
  `;
}

// Create a chat message DOM element from message object
function createChatMessageElement(tabObj, msg) {
  if (!tabObj || !msg) return null;
  const msgEl = document.createElement('div');
  const timeStr = msg.timestamp ? new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';

  if (msg.role === 'user') {
    msgEl.className = 'chat-msg user-msg';
    if (msg.inFlight) {
      msgEl.dataset.inFlight = 'true';
    }
    const rawVal = msg.rawPrompt || msg.content || '';
    if (rawVal) {
      msgEl.dataset.rawPrompt = normalizePromptText(rawVal);
    }
    if (msg.timestamp) {
      msgEl.dataset.timestamp = msg.timestamp;
    }

    let displayContent = msg.content || '';
    const attachments = msg.attachments ? [...msg.attachments] : [];

    // Extract attachments embedded in the prompt format [Attached file: "..."]
    if (!msg.attachments && displayContent.includes('[Attached file: "')) {
      const regex = /\[Attached file:\s*"([^"]+)"\](?:\s*Please inspect the attached file\.)?/g;
      let match;
      while ((match = regex.exec(msg.content)) !== null) {
        const filePath = match[1];
        const fname = filePath.split('/').pop();
        const ext = fname.split('.').pop().toLowerCase();
        const isImg = ['png', 'jpg', 'jpeg', 'webp', 'gif', 'bmp', 'svg'].includes(ext);
        attachments.push({
          filename: fname,
          path: filePath,
          isImage: isImg,
          previewUrl: isImg ? getChatFileContentUrl(filePath, tabObj.session) : null
        });
      }
      displayContent = displayContent.replace(/\[Attached file:\s*"[^"]+"\](?:\s*Please inspect the attached file\.)?/g, '').trim();
    }

    let attachmentsHtml = '';
    if (attachments.length > 0) {
      const chipsHtml = attachments.map(att => {
        let thumb = '';
        if (att.previewUrl) {
          thumb = `<img class="chat-attachment-thumb" src="${att.previewUrl}" alt="${escapeHtml(att.filename)}" />`;
        } else {
          thumb = `<span class="chat-attachment-icon">${getAttachmentIcon(att.filename, att.type)}</span>`;
        }
        return `
          <div class="chat-attachment-chip" data-path="${escapeHtml(att.path || '')}" title="Click to open or preview">
            ${thumb}
            <div class="chat-attachment-info">
              <span class="chat-attachment-name" title="${escapeHtml(att.filename)}">${escapeHtml(att.filename)}</span>
              ${att.size ? `<span class="chat-attachment-size">${formatBytes(att.size)}</span>` : ''}
            </div>
          </div>
        `;
      }).join('');
      attachmentsHtml = `<div class="chat-msg-attachments">${chipsHtml}</div>`;
    }

    let bodyHtml = '';
    let hasMarkdownVisuals = false;
    if (displayContent) {
      hasMarkdownVisuals = /!\[[^\]]*\]\([^)]+\)/.test(displayContent) ||
                           /`{3,4}carousel/i.test(displayContent) ||
                           /<!--\s*slide\s*-->/i.test(displayContent) ||
                           /\$\\[a-zA-Z]+/.test(displayContent) ||
                           /\$\$[\s\S]+?\$\$/.test(displayContent) ||
                           /^\s*>\s*\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/im.test(displayContent) ||
                           /file:\/\//i.test(displayContent) ||
                           /conversation:\/\//i.test(displayContent);
      if (hasMarkdownVisuals) {
        bodyHtml = `<div class="chat-msg-body markdown-body">${renderMarkdown(displayContent)}</div>`;
      } else {
        bodyHtml = `<div class="chat-msg-body">${escapeHtml(displayContent)}</div>`;
      }
    }

    msgEl.innerHTML = `
      <div class="chat-msg-header">
        <span class="chat-msg-role">👤 You</span>
        <div class="chat-msg-actions">
          <span class="chat-msg-time">${timeStr}</span>
          <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
            </svg>
          </button>
        </div>
      </div>
      ${attachmentsHtml}
      ${bodyHtml}
    `;

    if (hasMarkdownVisuals) {
      linkifyChatFiles(msgEl, tabObj ? tabObj.session : null);
    }

    msgEl.querySelectorAll('.chat-msg-attachments .chat-attachment-chip').forEach(chip => {
      chip.addEventListener('click', (e) => {
        const path = chip.dataset.path;
        if (path) {
          showChatFileContextMenu(chip, path, tabObj.session, e.clientX, e.clientY);
        }
      });
    });
  } else if (msg.role === 'assistant') {
    msgEl.className = 'chat-msg assistant-msg';
    if (typeof msg.index === 'number') {
      msgEl.dataset.msgIndex = String(msg.index);
    }
    msgEl.dataset.rawContent = msg.content || '';
    let thinkingHtml = '';
    if (msg.thinking) {
      thinkingHtml = `
        <details class="chat-thinking">
          <summary>💭 Thinking...</summary>
          <div class="thinking-text">${escapeHtml(msg.thinking)}</div>
        </details>
      `;
    }
    let toolsHtml = '';
    if (msg.tool_calls && msg.tool_calls.length > 0) {
      toolsHtml = renderToolGroupHtml(msg.tool_calls);
    }
    const bodyHtml = renderMarkdown(msg.content || '');
    const agentName = formatAgentChatName(tabObj.session ? tabObj.session.agent : null);
    msgEl.innerHTML = `
      <div class="chat-msg-header">
        <span class="chat-msg-role">🤖 ${escapeHtml(agentName)}</span>
        <div class="chat-msg-actions">
          <span class="chat-msg-time">${timeStr}</span>
          <button class="btn-read-chat-msg" title="Read message aloud" type="button" aria-label="Read message aloud">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon>
              <path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"></path>
            </svg>
          </button>
          <button class="btn-stop-chat-msg" title="Stop speech" type="button" aria-label="Stop speech" style="display:none;">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
              <rect x="6" y="6" width="12" height="12" rx="2"></rect>
            </svg>
          </button>
          <button class="btn-replay-chat-msg" title="Replay spoken briefing" type="button" aria-label="Replay spoken briefing" style="display:none;">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="1 4 1 10 7 10"></polyline>
              <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"></path>
            </svg>
          </button>
          <button class="btn-full-read-chat-msg" title="Read full text" type="button" aria-label="Read full text" style="display:none;">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"></path>
              <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"></path>
            </svg>
          </button>
          <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
            </svg>
          </button>
        </div>
      </div>
      ${thinkingHtml}
      ${toolsHtml ? `<div class="chat-tools">${toolsHtml}</div>` : ''}
      <div class="chat-msg-body markdown-body">${bodyHtml}</div>
    `;

    const allTools = (msg.tool_calls && msg.tool_calls.length > 0) ? msg.tool_calls : (msg.tool_uses || []);
    if (Array.isArray(allTools)) {
      for (const tc of allTools) {
        const tcName = (tc.name || '').toLowerCase();
        if (tcName === 'todowrite' || tcName === 'todo_write') {
          const inputData = tc.input || tc.detail;
          const todoCard = renderTodoWriteCard(tabObj, inputData);
          if (todoCard) {
            const toolsSlot = msgEl.querySelector('.chat-tools');
            if (toolsSlot) {
              toolsSlot.appendChild(todoCard);
            } else {
              const bodyEl = msgEl.querySelector('.chat-msg-body');
              if (bodyEl) {
                msgEl.insertBefore(todoCard, bodyEl);
              } else {
                msgEl.appendChild(todoCard);
              }
            }
          }
        }
      }
    }
  } else {
    msgEl.className = 'chat-msg system-msg';
    msgEl.innerHTML = `<span class="system-tag">ℹ️ ${escapeHtml(msg.content)}</span>`;
  }

  attachChatMessageListeners(msgEl, msg.content, tabObj);
  attachCodeBlockCopyButtons(msgEl);
  if (tabObj.session) {
    linkifyChatFiles(msgEl, tabObj.session);
  }

  return msgEl;
}

// Append a message bubble into the chat messages container
function appendChatMessage(tabObj, msg) {
  if (!tabObj || !tabObj.chatMessagesEl) return null;
  const msgEl = createChatMessageElement(tabObj, msg);
  if (msgEl) {
    tabObj.chatMessagesEl.appendChild(msgEl);
  }
  return msgEl;
}

// Check whether a session is currently in Working state
function isSessionWorking(session) {
  if (!session) return false;
  return session.state === 1 || session.state === 'working';
}

// Check whether a chat tab has active or lingering turn UI indicators
function hasActiveTurnUI(tabObj) {
  if (!tabObj) return false;
  return Boolean(
    tabObj.sending ||
    tabObj.activeInStreamActivityEl ||
    tabObj.activeTurnMsgEl ||
    (tabObj.chatCancelBtn && tabObj.chatCancelBtn.style.display !== 'none') ||
    (tabObj.chatStatusBadge && tabObj.chatStatusBadge.textContent && tabObj.chatStatusBadge.textContent.includes('Working')) ||
    (tabObj.chatSendBtn && tabObj.chatSendBtn.classList.contains('is-queue'))
  );
}

// Toggle sending/working UI state on a chat tab
function setChatSending(tabObj, isSending) {
  if (!tabObj) return;
  tabObj.sending = Boolean(isSending);
  if (isSending) {
    if (tabObj.session) {
      tabObj.session.state = 1;
      if (Array.isArray(state.sessions)) {
        const match = state.sessions.find(s => s.id === tabObj.session.id || (tabObj.session.native_id && s.native_id === tabObj.session.native_id));
        if (match) match.state = 1;
      }
    }
    if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
    if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
    const hasLiveTurnMsg = Boolean(tabObj.activeTurnMsgEl && tabObj.activeTurnMsgEl.isConnected);
    const hasLiveActivity = Boolean(tabObj.activeInStreamActivityEl && tabObj.activeInStreamActivityEl.isConnected);
    if (!hasLiveTurnMsg && !hasLiveActivity) {
      showInStreamActivity(tabObj, {
        icon: '💭',
        text: 'Thinking...',
        badge: null
      });
    }
    updateComposerButtonState(tabObj);
  } else {
    resetChatComposer(tabObj);
  }
}

// Update composer send button state based on active turn
function updateComposerButtonState(tabObj) {
  if (!tabObj || !tabObj.chatSendBtn) return;
  const isTurnActive = Boolean(tabObj.sending) ||
                       (tabObj.activeTurnMsgEl !== null && tabObj.activeTurnMsgEl !== undefined) ||
                       (tabObj.activeInStreamActivityEl !== null && tabObj.activeInStreamActivityEl !== undefined) ||
                       (tabObj.chatCancelBtn && tabObj.chatCancelBtn.style.display !== 'none');
  if (isTurnActive) {
    if (tabObj.chatSendBtn.dataset.mode !== 'queue') {
      tabObj.chatSendBtn.dataset.mode = 'queue';
      tabObj.chatSendBtn.disabled = false;
      tabObj.chatSendBtn.classList.add('is-queue');
      tabObj.chatSendBtn.title = 'Queue prompt (Enter)';
      tabObj.chatSendBtn.innerHTML = `
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <line x1="12" y1="5" x2="12" y2="19"></line>
          <line x1="5" y1="12" x2="19" y2="12"></line>
        </svg>
      `;
    }
    if (tabObj.chatInputEl) {
      const nextPlaceholder = 'Queue a follow-up prompt... (Enter to queue, Shift+Enter for newline)';
      if (tabObj.chatInputEl.placeholder !== nextPlaceholder) {
        tabObj.chatInputEl.placeholder = nextPlaceholder;
      }
    }
  } else {
    if (tabObj.chatSendBtn.dataset.mode !== 'send') {
      tabObj.chatSendBtn.dataset.mode = 'send';
      tabObj.chatSendBtn.disabled = false;
      tabObj.chatSendBtn.classList.remove('is-queue');
      tabObj.chatSendBtn.title = 'Send Prompt (Enter)';
      tabObj.chatSendBtn.innerHTML = '➤';
    }
    if (tabObj.chatInputEl) {
      const nextPlaceholder = `Ask ${escapeHtml(formatAgentChatName(tabObj.session ? tabObj.session.agent : null))} anything... (Enter to send, Shift+Enter for newline)`;
      if (tabObj.chatInputEl.placeholder !== nextPlaceholder) {
        tabObj.chatInputEl.placeholder = nextPlaceholder;
      }
    }
  }
}

// Render Prompt Queue box docked above composer
function renderChatQueue(tabObj) {
  if (!tabObj || !tabObj.chatQueueContainer) return;
  const q = tabObj.promptQueue || [];

  if (q.length === 0) {
    tabObj.chatQueueContainer.style.display = 'none';
    tabObj.isQueueExpanded = false;
    tabObj.isQueuePaused = false;
    updateComposerButtonState(tabObj);
    return;
  }

  tabObj.chatQueueContainer.style.display = 'flex';
  const countText = q.length === 1 ? '1 message queued up' : `${q.length} messages queued up`;
  if (tabObj.chatQueueCountText) {
    if (tabObj.isQueuePaused) {
      tabObj.chatQueueCountText.textContent = `⏸️ Queue paused (${countText})`;
    } else {
      tabObj.chatQueueCountText.textContent = countText;
    }
  }

  if (tabObj.chatQueueResumeBtn) {
    tabObj.chatQueueResumeBtn.style.display = tabObj.isQueuePaused ? 'inline-flex' : 'none';
  }

  if (tabObj.chatQueueChevron) {
    tabObj.chatQueueChevron.textContent = tabObj.isQueueExpanded ? '▴' : '▾';
  }

  if (!tabObj.isQueueExpanded) {
    if (tabObj.chatQueueList) tabObj.chatQueueList.style.display = 'none';
  } else {
    if (tabObj.chatQueueList) {
      tabObj.chatQueueList.style.display = 'flex';
      tabObj.chatQueueList.innerHTML = '';

      q.forEach((item, idx) => {
        const itemEl = document.createElement('div');
        itemEl.className = `chat-queue-item ${item.expanded ? 'expanded' : ''}`;
        itemEl.dataset.id = item.id;

        itemEl.innerHTML = `
          <div class="chat-queue-item-header">
            <span class="chat-queue-item-index">#${idx + 1}</span>
            <div class="chat-queue-item-preview" title="Click to ${item.expanded ? 'collapse' : 'expand full message'}">
              ${escapeHtml(item.text)}
            </div>
            <div class="chat-queue-item-actions">
              <button class="btn-queue-item-toggle" type="button" title="${item.expanded ? 'Collapse' : 'Expand'}">
                ${item.expanded ? '▴' : '▾'}
              </button>
              <button class="btn-queue-item-trash" type="button" title="Remove from queue" aria-label="Remove message">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="3 6 5 6 21 6"></polyline>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>
                  <line x1="10" y1="11" x2="10" y2="17"></line>
                  <line x1="14" y1="11" x2="14" y2="17"></line>
                </svg>
              </button>
            </div>
          </div>
          ${item.expanded ? `
            <div class="chat-queue-item-body">
              <div class="chat-queue-item-full">${escapeHtml(item.text)}</div>
            </div>
          ` : ''}
        `;

        const previewEl = itemEl.querySelector('.chat-queue-item-preview');
        const toggleBtn = itemEl.querySelector('.btn-queue-item-toggle');
        const toggleFn = (e) => {
          e.stopPropagation();
          item.expanded = !item.expanded;
          renderChatQueue(tabObj);
        };
        if (previewEl) previewEl.addEventListener('click', toggleFn);
        if (toggleBtn) toggleBtn.addEventListener('click', toggleFn);

        const trashBtn = itemEl.querySelector('.btn-queue-item-trash');
        if (trashBtn) {
          trashBtn.addEventListener('click', async (e) => {
            e.stopPropagation();
            const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
            try {
              await fetch(`${baseUrl}/v1/sessions/prompt/queue?session_id=${encodeURIComponent(tabObj.session.id)}&item_id=${encodeURIComponent(item.id)}`, {
                method: 'DELETE'
              });
            } catch (err) {
              console.error('Failed to delete queue item on daemon:', err);
            }
            tabObj.promptQueue = tabObj.promptQueue.filter(p => p.id !== item.id);
            renderChatQueue(tabObj);
          });
        }

        tabObj.chatQueueList.appendChild(itemEl);
      });
    }
  }

  updateComposerButtonState(tabObj);
}

// Render running subagents box directly above prompt composer
function renderSubagentsBar(tabObj) {
  if (!tabObj || !tabObj.chatSubagentsContainer) return;
  const running = (tabObj.runningSubagents || []).filter(s => s.state === 'running');
  if (running.length === 0) {
    tabObj.chatSubagentsContainer.style.display = 'none';
    if (tabObj.chatSubagentsList) tabObj.chatSubagentsList.style.display = 'none';
    return;
  }

  tabObj.chatSubagentsContainer.style.display = 'flex';
  const countText = running.length === 1 ? '1 subagent running...' : `${running.length} subagents running...`;
  if (tabObj.chatSubagentsCountText) {
    tabObj.chatSubagentsCountText.textContent = countText;
  }

  if (tabObj.isSubagentsExpanded) {
    if (tabObj.chatSubagentsList) {
      tabObj.chatSubagentsList.style.display = 'flex';
      tabObj.chatSubagentsList.innerHTML = running.map((sub, idx) => {
        const name = sub.name || sub.role || `Subagent #${idx + 1}`;
        const role = sub.role && sub.role !== name ? sub.role : '';
        const prompt = (sub.prompt || '').trim();
        return `
          <div class="chat-subagent-item">
            <div class="chat-subagent-header">
              <div class="chat-subagent-role">
                <span class="chat-subagents-pulse"></span>
                <span class="chat-subagent-name">${escapeHtml(name)}</span>
                ${role ? `<span class="chat-subagent-badge">${escapeHtml(role)}</span>` : ''}
              </div>
              ${prompt ? `
                <button class="btn-copy-subagent-prompt" title="Copy prompt" type="button" data-prompt="${escapeHtml(prompt)}">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                  </svg>
                </button>
              ` : ''}
            </div>
            ${prompt ? `<div class="chat-subagent-prompt">${escapeHtml(prompt)}</div>` : ''}
          </div>
        `;
      }).join('');

      tabObj.chatSubagentsList.querySelectorAll('.btn-copy-subagent-prompt').forEach(btn => {
        btn.addEventListener('click', (e) => {
          e.stopPropagation();
          const p = btn.getAttribute('data-prompt') || '';
          copyToClipboard(p, btn);
        });
      });
    }
    if (tabObj.chatSubagentsChevron) {
      tabObj.chatSubagentsChevron.style.transform = 'rotate(180deg)';
    }
  } else {
    if (tabObj.chatSubagentsList) {
      tabObj.chatSubagentsList.style.display = 'none';
    }
    if (tabObj.chatSubagentsChevron) {
      tabObj.chatSubagentsChevron.style.transform = 'none';
    }
  }
}

// Fetch running subagents for tabObj
async function fetchRunningSubagents(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
  const token = getAuthToken();
  let url = `${baseUrl}/v1/sessions/subagents?id=${encodeURIComponent(tabObj.session.id)}`;
  if (token) {
    url += `&token=${encodeURIComponent(token)}`;
  }
  try {
    const res = await fetch(url);
    if (res.ok) {
      const data = await res.json();
      if (Array.isArray(data.subagents)) {
        tabObj.runningSubagents = data.subagents;
        renderSubagentsBar(tabObj);
      }
    }
  } catch (e) {
    // Background fetch ignore
  }
}

// Pop and dispatch the next queued prompt if idle
async function dispatchNextQueuedPrompt(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
  try {
    await fetch(`${baseUrl}/v1/sessions/prompt/queue/resume?session_id=${encodeURIComponent(tabObj.session.id)}`, {
      method: 'POST'
    });
  } catch (err) {
    console.error('Failed to resume queue dispatch on daemon:', err);
  }
}

// Parse tool action and parameters into human-friendly in-stream status
function formatToolActivity(toolName, toolInput) {
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

  // File reading
  if (tName === 'view_file' || tName === 'view' || tName === 'read_file') {
    icon = '📖';
    const p = params.AbsolutePath || params.file_path || params.path || params.TargetFile || '';
    const filename = p ? p.split('/').pop() : '';
    text = filename ? `Reading ${filename}` : 'Reading file...';
  }
  // File writing/editing
  else if (tName === 'replace_file_content' || tName === 'write_to_file' || tName === 'edit') {
    icon = '✏️';
    const p = params.TargetFile || params.file_path || params.path || '';
    const filename = p ? p.split('/').pop() : '';
    text = filename ? `Editing ${filename}` : 'Editing file...';
  }
  // Shell commands
  else if (tName === 'run_command' || tName === 'bash') {
    icon = '⚡';
    const cmd = (params.CommandLine || params.command || '').trim();
    if (cmd) {
      const firstLine = cmd.split('\n')[0];
      text = `Running: ${firstLine.length > 50 ? firstLine.slice(0, 47) + '...' : firstLine}`;
    } else {
      text = 'Running command...';
    }
  }
  // Searching / Grepping
  else if (tName === 'grep_search' || tName === 'grep') {
    icon = '🔍';
    const q = params.Query || params.pattern || '';
    text = q ? `Searching for "${q}"` : 'Searching codebase...';
  }
  // Finding by name / glob
  else if (tName === 'find_by_name' || tName === 'glob') {
    icon = '🔍';
    const pat = params.Pattern || params.pattern || '';
    text = pat ? `Finding "${pat}"` : 'Finding files...';
  }
  // Subagents
  else if (tName === 'invoke_subagent' || tName === 'agent' || tName === 'task') {
    icon = '🤖';
    text = 'Delegating to subagent...';
  }
  // Web fetching
  else if (tName === 'read_url_content' || tName === 'search_web') {
    icon = '🌐';
    const u = params.Url || params.query || '';
    text = u ? `Fetching ${u}` : 'Searching web...';
  }

  return { icon, text, badge };
}

// Show or update in-stream ephemeral activity indicator
function showInStreamActivity(tabObj, { icon, text, badge }) {
  if (!tabObj || !tabObj.chatMessagesEl) return;

  tabObj.activeActivityStartTime = Date.now();

  if (!tabObj.activeInStreamActivityEl || !tabObj.activeInStreamActivityEl.isConnected) {
    const el = document.createElement('div');
    el.className = 'chat-instream-activity';
    el.innerHTML = `
      <span class="chat-activity-spinner"></span>
      <span class="chat-activity-icon">${escapeHtml(icon || '⚡')}</span>
      <span class="chat-activity-text">${escapeHtml(text || 'Working...')}</span>
      <span class="chat-activity-badge" style="${badge ? '' : 'display: none;'}">${escapeHtml(badge || '')}</span>
      <span class="chat-activity-timer">(0s)</span>
    `;
    tabObj.chatMessagesEl.appendChild(el);
    tabObj.activeInStreamActivityEl = el;

    if (tabObj.activeActivityTimer) {
      clearInterval(tabObj.activeActivityTimer);
    }
    tabObj.activeActivityTimer = setInterval(() => {
      if (!tabObj.activeInStreamActivityEl || !tabObj.activeInStreamActivityEl.isConnected) {
        clearInterval(tabObj.activeActivityTimer);
        tabObj.activeActivityTimer = null;
        return;
      }
      const sec = Math.floor((Date.now() - tabObj.activeActivityStartTime) / 1000);
      const timerEl = tabObj.activeInStreamActivityEl.querySelector('.chat-activity-timer');
      if (timerEl) {
        timerEl.textContent = `(${sec}s)`;
      }
    }, 1000);
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
    if (timerEl) timerEl.textContent = '(0s)';
  }

  updateComposerButtonState(tabObj);
  tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
}

// Remove in-stream ephemeral activity indicator
function hideInStreamActivity(tabObj) {
  if (!tabObj) return;
  if (tabObj.activeActivityTimer) {
    clearInterval(tabObj.activeActivityTimer);
    tabObj.activeActivityTimer = null;
  }
  if (tabObj.activeInStreamActivityEl) {
    tabObj.activeInStreamActivityEl.remove();
    tabObj.activeInStreamActivityEl = null;
  }
  updateComposerButtonState(tabObj);
}

// Render buffered tool calls into the message tool slot
function renderBufferedToolsIntoSlot(slot, tools) {
  if (!slot || !Array.isArray(tools) || tools.length === 0) return;

  const toolStringList = tools.map(t => t.detail ? `${t.name}: ${t.detail}` : t.name);
  const summaryLabel = getToolGroupSummaryLabel(toolStringList);

  const groupCard = document.createElement('details');
  groupCard.className = 'chat-tool-card chat-tool-group';
  groupCard.open = true;
  groupCard.dataset.toolCalls = JSON.stringify(tools.map(t => ({ name: t.name, detail: t.detail })));
  groupCard.innerHTML = `
    <summary>
      <span class="tool-group-title">${escapeHtml(summaryLabel)}</span>
      <span class="tool-group-pill">${tools.length} ${tools.length === 1 ? 'action' : 'actions'}</span>
    </summary>
    <div class="chat-tool-content chat-tool-group-content">
      <div class="chat-tool-call-list">
        ${tools.map((t, idx) => `
          <div class="chat-tool-call-item ${t.status === 'running' ? 'in-progress' : ''}">
            <div class="chat-tool-call-header">
              <span class="chat-tool-call-idx">#${idx + 1}</span>
              <span class="chat-tool-call-badge">${escapeHtml(t.name)}</span>
              <span class="chat-tool-status">${escapeHtml(t.status || 'done')}</span>
            </div>
            ${t.detail ? `<div class="chat-tool-call-detail"><code>${escapeHtml(t.detail)}</code></div>` : ''}
            ${t.output ? `<div class="chat-tool-output">${escapeHtml(t.output)}</div>` : ''}
          </div>
        `).join('')}
      </div>
    </div>
  `;
  slot.appendChild(groupCard);
}

// Ensure assistant message bubble exists, creating it only when real message content arrives
function ensureActiveAssistantMessage(tabObj) {
  if (!tabObj || !tabObj.chatMessagesEl) return null;
  if (tabObj.activeInStreamActivityEl) {
    hideInStreamActivity(tabObj);
  }
  if (tabObj.activeTurnMsgEl && tabObj.activeTurnMsgEl.isConnected) return tabObj.activeTurnMsgEl;
  tabObj.activeTurnMsgEl = null;

  const assistantMsgEl = document.createElement('div');
  assistantMsgEl.className = 'chat-msg assistant-msg in-flight';
  const msgIdx = tabObj.chatMessagesEl ? tabObj.chatMessagesEl.querySelectorAll('.chat-msg').length : 0;
  assistantMsgEl.dataset.msgIndex = String(msgIdx);
  assistantMsgEl.dataset.rawContent = tabObj.activeTurnBuffer || '';
  assistantMsgEl.innerHTML = `
    <div class="chat-msg-header">
      <span class="chat-msg-role">🤖 ${escapeHtml(formatAgentChatName(tabObj.session ? tabObj.session.agent : null))}</span>
      <div class="chat-msg-actions">
        <span class="chat-msg-time">${new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
        <button class="btn-read-chat-msg" title="Read message aloud" type="button" aria-label="Read message aloud">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon>
            <path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"></path>
          </svg>
        </button>
        <button class="btn-stop-chat-msg" title="Stop speech" type="button" aria-label="Stop speech" style="display:none;">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <rect x="6" y="6" width="12" height="12" rx="2"></rect>
          </svg>
        </button>
        <button class="btn-replay-chat-msg" title="Replay spoken briefing" type="button" aria-label="Replay spoken briefing" style="display:none;">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="1 4 1 10 7 10"></polyline>
            <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"></path>
          </svg>
        </button>
        <button class="btn-full-read-chat-msg" title="Read full text" type="button" aria-label="Read full text" style="display:none;">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"></path>
            <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"></path>
          </svg>
        </button>
        <button class="btn-copy-chat-msg" title="Copy message" type="button" aria-label="Copy message">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
          </svg>
        </button>
      </div>
    </div>
    <div class="chat-thinking-slot"></div>
    <div class="chat-tools-slot"></div>
    <div class="chat-msg-body markdown-body"><span class="chat-streaming-cursor"></span></div>
  `;

  if (tabObj.activeTurnThinking) {
    const slot = assistantMsgEl.querySelector('.chat-thinking-slot');
    if (slot) {
      const thinkBlock = document.createElement('details');
      thinkBlock.className = 'chat-thinking';
      thinkBlock.open = false;
      thinkBlock.innerHTML = `<summary>💭 Thinking</summary><div class="thinking-text">${escapeHtml(tabObj.activeTurnThinking)}</div>`;
      slot.appendChild(thinkBlock);
    }
  }

  if (Array.isArray(tabObj.activeTurnTools) && tabObj.activeTurnTools.length > 0) {
    const slot = assistantMsgEl.querySelector('.chat-tools-slot');
    if (slot) {
      renderBufferedToolsIntoSlot(slot, tabObj.activeTurnTools);
    }
  }

  attachChatMessageListeners(assistantMsgEl, tabObj.activeTurnBuffer || '', tabObj);
  tabObj.chatMessagesEl.appendChild(assistantMsgEl);
  tabObj.activeTurnMsgEl = assistantMsgEl;
  updateComposerButtonState(tabObj);
  tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;

  return assistantMsgEl;
}

// Send Prompt to daemon (/v1/sessions/prompt) or queue if turn is in progress
async function sendChatPrompt(tabObj, forcedPromptText) {
  if (!tabObj || !tabObj.chatInputEl) return;
  stopActiveSpeech();
  if (tabObj.chatSuggestedChipsEl) {
    tabObj.chatSuggestedChipsEl.style.display = 'none';
    tabObj.chatSuggestedChipsEl.innerHTML = '';
  }
  let promptText = '';
  if (typeof forcedPromptText === 'string') {
    promptText = forcedPromptText.trim();
  } else {
    promptText = tabObj.chatInputEl.value.trim();
  }

  if (promptText === '/config') {
    toggleChatConfigPopover(tabObj);
    tabObj.chatInputEl.value = '';
    if (typeof forcedPromptText !== 'string') {
      clearChatDraft(tabObj.id || (tabObj.session && tabObj.session.id));
    }
    return;
  }
  if (promptText === '/plan') {
    togglePlanMode(tabObj);
    tabObj.chatInputEl.value = '';
    if (typeof forcedPromptText !== 'string') {
      clearChatDraft(tabObj.id || (tabObj.session && tabObj.session.id));
    }
    return;
  }
  if (promptText === '/clear') {
    tabObj.chatInputEl.value = '';
    if (typeof forcedPromptText !== 'string') {
      clearChatDraft(tabObj.id || (tabObj.session && tabObj.session.id));
    }
    executeClientAction(tabObj, 'clear');
    return;
  }
  if (promptText === '/terminal') {
    tabObj.chatInputEl.value = '';
    if (typeof forcedPromptText !== 'string') {
      clearChatDraft(tabObj.id || (tabObj.session && tabObj.session.id));
    }
    executeClientAction(tabObj, 'terminal');
    return;
  }
  if (promptText === '/handover') {
    tabObj.chatInputEl.value = '';
    if (typeof forcedPromptText !== 'string') {
      clearChatDraft(tabObj.id || (tabObj.session && tabObj.session.id));
    }
    executeClientAction(tabObj, 'handover');
    return;
  }

  const pending = tabObj.pendingAttachments || [];
  if (pending.some(a => a.status === 'uploading')) {
    showUploadToast('Please wait for file upload to finish...', 'info', 2500);
    return;
  }

  const failed = pending.filter(a => a.status === 'error');
  if (failed.length > 0) {
    showUploadToast(`Attachment "${failed[0].filename}" failed to upload. Please remove or retry.`, 'error', 3000);
    return;
  }

  const readyAttachments = pending.filter(a => a.status === 'ready' && a.path);

  if (!promptText && readyAttachments.length === 0) return;

  if (typeof forcedPromptText !== 'string') {
    tabObj.chatInputEl.value = '';
    clearChatDraft(tabObj.id || (tabObj.session && tabObj.session.id));
    tabObj.chatInputEl.style.height = 'auto';
    tabObj.chatInputEl.focus();
  }

  let daemonPrompt = promptText;
  if (readyAttachments.length > 0) {
    const attachInstructions = readyAttachments.map(att =>
      `[Attached file: "${att.path}"]\nPlease inspect the attached file.`
    ).join('\n\n');
    if (daemonPrompt) {
      daemonPrompt = `${daemonPrompt}\n\n${attachInstructions}`;
    } else {
      daemonPrompt = `Please inspect the attached file(s).\n\n${attachInstructions}`;
    }
  }

  const sentAttachments = [...readyAttachments];
  tabObj.pendingAttachments = [];
  renderPendingAttachments(tabObj);

  // Optimistically append user message immediately to give instant UI feedback
  const optimisticMsgEl = appendChatMessage(tabObj, {
    role: 'user',
    content: promptText || 'Please inspect the attached file(s).',
    rawPrompt: daemonPrompt,
    attachments: sentAttachments,
    inFlight: true,
    timestamp: new Date().toISOString()
  });

  tabObj.inFlightPrompt = {
    text: normalizePromptText(daemonPrompt),
    el: optimisticMsgEl,
    timestamp: Date.now()
  };

  // Temporarily disable send button during dispatch to prevent double-submitting
  if (tabObj.chatSendBtn) {
    tabObj.chatSendBtn.disabled = true;
  }
  if (typeof forcedPromptText !== 'string' && tabObj.viewMode === 'chat' && tabObj.chatInputEl) {
    tabObj.chatInputEl.focus();
  }

  connectChatStream(tabObj);
  const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
  tabObj.dispatchingPrompt = true;
  try {
    const res = await fetch(`${baseUrl}/v1/sessions/prompt`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        session_id: tabObj.session.id,
        prompt: daemonPrompt
      })
    });
    tabObj.dispatchingPrompt = false;

    if (!res.ok) {
      let errMsg = '';
      try {
        const errData = await res.json();
        errMsg = errData.message || errData.error || '';
      } catch (_) {
        errMsg = await res.text().catch(() => '');
      }
      throw new Error(errMsg || `HTTP ${res.status}`);
    }

    const data = await res.json().catch(() => ({}));
    if (data && data.status === 'queued') {
      // Prompt queued on daemon - remove optimistic bubble from active chat and update queue box.
      // Do not touch active turn buffers or stream activity so ongoing assistant streams continue undisturbed.
      if (optimisticMsgEl && optimisticMsgEl.parentNode) {
        optimisticMsgEl.parentNode.removeChild(optimisticMsgEl);
      }
      tabObj.inFlightPrompt = null;
      loadChatQueue(tabObj);
      updateComposerButtonState(tabObj);
      return;
    }

    // Prompt accepted for immediate execution (running/sent):
    // Now (and only now) reset active turn state and display stream activity
    tabObj.activeTurnMsgEl = null;
    tabObj.activeTurnBuffer = '';
    tabObj.activeTurnHadTool = false;
    tabObj.activeTurnTools = [];
    tabObj.activeTurnThinking = '';
    setChatSending(tabObj, true);

    // Retain tabObj.inFlightPrompt so turn_start can claim it.
    // Safety cleanup after 15s in case turn_start is never emitted by this agent.
    setTimeout(() => {
      if (tabObj.inFlightPrompt && tabObj.inFlightPrompt.el === optimisticMsgEl) {
        if (optimisticMsgEl) delete optimisticMsgEl.dataset.inFlight;
        if (!tabObj.sending && !isSessionWorking(tabObj.session)) {
          tabObj.inFlightPrompt = null;
        }
      }
    }, 15000);
  } catch (err) {
    tabObj.dispatchingPrompt = false;
    console.error('Failed to dispatch prompt:', err);
    if (!tabObj.activeTurnMsgEl) {
      hideInStreamActivity(tabObj);
    }
    tabObj.inFlightPrompt = null;
    if (optimisticMsgEl) {
      delete optimisticMsgEl.dataset.inFlight;
      optimisticMsgEl.classList.add('prompt-error');
    }
    const errEl = document.createElement('div');
    errEl.className = 'chat-msg assistant-msg';
    errEl.innerHTML = `
      <div class="chat-msg-header">
        <span class="chat-msg-role">🤖 ${escapeHtml(formatAgentChatName(tabObj.session ? tabObj.session.agent : null))}</span>
      </div>
      <div class="chat-msg-body markdown-body">
        <span style="color: var(--accent-red);">⚠️ Error sending prompt: ${escapeHtml(err.message)}</span>
      </div>
    `;
    tabObj.chatMessagesEl.appendChild(errEl);
    setChatSending(tabObj, false);
  }
}

// Disconnect SSE for chat turn events
function disconnectChatStream(tabObj) {
  if (!tabObj) return;
  if (tabObj.chatReconnectTimer) {
    clearTimeout(tabObj.chatReconnectTimer);
    tabObj.chatReconnectTimer = null;
  }
  if (tabObj._pendingTranscriptFollowup) {
    clearTimeout(tabObj._pendingTranscriptFollowup);
    tabObj._pendingTranscriptFollowup = null;
  }
  if (tabObj.chatEventSource) {
    try {
      tabObj.chatEventSource.close();
    } catch (e) {}
    tabObj.chatEventSource = null;
  }
  tabObj.chatStreamSessionId = null;
  hideInStreamActivity(tabObj);
}

// Connect SSE for active session turn events (headless sessions only)
function connectChatStream(tabObj) {
  if (!tabObj || !tabObj.session) return;
  // Terminal/tmux sessions stream via WebSocket PTY, not headless SSE stream
  if (tabObj.session.engine_type !== 'headless') {
    return;
  }
  // Only connect if currently in chat view mode
  if (tabObj.viewMode !== 'chat') {
    return;
  }
  // Only connect if the tab is active
  if (tabObj.tabEl && !tabObj.tabEl.classList.contains('active')) {
    return;
  }
  const sessionId = tabObj.session.id;
  if (tabObj.chatEventSource && tabObj.chatEventSource.readyState !== EventSource.CLOSED) {
    if (tabObj.chatStreamSessionId === sessionId) {
      return;
    }
    try {
      tabObj.chatEventSource.close();
    } catch (e) {}
    tabObj.chatEventSource = null;
    tabObj.chatStreamSessionId = null;
  }
  if (tabObj.chatReconnectTimer) {
    clearTimeout(tabObj.chatReconnectTimer);
    tabObj.chatReconnectTimer = null;
  }

  const baseUrl = getSessionBaseUrl(sessionId, tabObj.session);
  const token = getAuthToken();
  let sseUrl = `${baseUrl}/v1/sessions/chat/stream?session_id=${encodeURIComponent(sessionId)}`;
  if (token) {
    sseUrl += `&token=${encodeURIComponent(token)}`;
  }

  try {
    const es = new EventSource(sseUrl);
    tabObj.chatEventSource = es;
    tabObj.chatStreamSessionId = sessionId;

    es.onmessage = (event) => {
      if (!event.data) return;
      try {
        const evt = JSON.parse(event.data);
        handleChatStreamEvent(tabObj, evt);
      } catch (e) {
        console.warn('Failed to parse SSE event data:', e);
      }
    };
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED || tabObj.chatEventSource === es) {
        try { es.close(); } catch (e) {}
        if (tabObj.chatEventSource === es) {
          tabObj.chatEventSource = null;
          tabObj.chatStreamSessionId = null;
        }
      }
      if (tabObj.chatReconnectTimer) {
        clearTimeout(tabObj.chatReconnectTimer);
      }
      // If the tab is still open in state.openTabs, reconcile state and schedule a reconnect
      if (state.openTabs && state.openTabs.has(tabObj.id)) {
        tabObj.chatReconnectTimer = setTimeout(async () => {
          tabObj.chatReconnectTimer = null;
          if (!state.openTabs || !state.openTabs.has(tabObj.id)) return;
          if (!tabObj.tabEl || !tabObj.tabEl.classList.contains('active') || tabObj.viewMode !== 'chat') return;
          const sessMatch = state.sessions.find(s => s.id === tabObj.session.id || (tabObj.session.native_id && s.native_id === tabObj.session.native_id));
          const currentSess = sessMatch || tabObj.session;
          if (currentSess && !isSessionWorking(currentSess) && hasActiveTurnUI(tabObj)) {
            setChatSending(tabObj, false);
            loadChatTranscript(tabObj);
            loadChatQueue(tabObj);
          }
          if (currentSess && currentSess.engine_type === 'headless') {
            connectChatStream(tabObj);
          }
        }, 1500);
      }
    };
  } catch (e) {
    console.error('Failed to open EventSource:', e);
  }
}

// Stream Rendering Batcher (Coalesces high-frequency text deltas into requestAnimationFrame ticks)
function flushActiveTurnStreamRender(tabObj) {
  if (!tabObj) return;
  if (tabObj._streamRenderRafId) {
    if (typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(tabObj._streamRenderRafId);
    }
    tabObj._streamRenderRafId = null;
  }
  const currentMsgEl = tabObj.activeTurnMsgEl || (tabObj.chatMessagesEl ? tabObj.chatMessagesEl.querySelector('.chat-msg.assistant-msg.in-flight') : null);
  if (!currentMsgEl) return;
  const bodyEl = currentMsgEl.querySelector('.chat-msg-body');
  if (bodyEl && tabObj.activeTurnBuffer !== undefined) {
    const html = renderMarkdown(tabObj.activeTurnBuffer);
    bodyEl.innerHTML = html + '<span class="chat-streaming-cursor"></span>';
    if (tabObj.session) {
      linkifyChatFiles(bodyEl, tabObj.session);
    }
    if (tabObj.activeTurnBuffer) {
      const fenceCount = (tabObj.activeTurnBuffer.match(/```/g) || []).length;
      if (fenceCount >= 2) {
        renderMermaidDiagrams(bodyEl, copyTextToClipboard, { cacheOnly: fenceCount % 2 !== 0 });
      }
    }
  }
  if (tabObj.chatMessagesEl) {
    tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
  }
}

function scheduleActiveTurnStreamRender(tabObj) {
  if (!tabObj) return;
  if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
    if (tabObj._streamRenderRafId) return;
    tabObj._streamRenderRafId = window.requestAnimationFrame(() => {
      tabObj._streamRenderRafId = null;
      flushActiveTurnStreamRender(tabObj);
    });
  } else {
    flushActiveTurnStreamRender(tabObj);
  }
}

// Handle stream event from SSE
function handleChatStreamEvent(tabObj, evt) {
  if (!tabObj || !tabObj.chatMessagesEl) return;

  if (!tabObj.activeTurnMsgEl || !tabObj.activeTurnMsgEl.isConnected) {
    const existingInFlight = tabObj.chatMessagesEl.querySelector('.chat-msg.assistant-msg.in-flight');
    tabObj.activeTurnMsgEl = existingInFlight || null;
  }

  switch (evt.type) {
    case 'interactive_block': {
      flushActiveTurnStreamRender(tabObj);
      tabObj.sending = false;
      setChatSending(tabObj, false);
      if (evt.blocked) {
        if (!tabObj.session) tabObj.session = {};
        tabObj.session.blocked = evt.blocked;
        tabObj.session.state = 2;
        renderInteractiveBlockCard(tabObj, evt.blocked, evt.tool_name);
      }
      break;
    }

    case 'queue_update':
      if (Array.isArray(evt.queue_items)) {
        tabObj.promptQueue = evt.queue_items;
      }
      tabObj.isQueuePaused = !!evt.queue_paused;
      renderChatQueue(tabObj);
      break;

    case 'subagents_update':
      if (Array.isArray(evt.subagents)) {
        tabObj.runningSubagents = evt.subagents;
        renderSubagentsBar(tabObj);
      }
      break;

    case 'turn_start':
      tabObj.activeTurnHadTool = false;
      tabObj.lastCompletedTurn = null;
      if (evt.text) {
        const evtNorm = normalizePromptText(evt.text);
        let isAlreadyRendered = false;

        // 1. Match against currently in-flight client prompt
        if (tabObj.inFlightPrompt) {
          const inFlightNorm = tabObj.inFlightPrompt.text;
          if (inFlightNorm && (inFlightNorm === evtNorm || evtNorm.startsWith(inFlightNorm) || inFlightNorm.startsWith(evtNorm))) {
            isAlreadyRendered = true;
            if (tabObj.inFlightPrompt.el) {
              delete tabObj.inFlightPrompt.el.dataset.inFlight;
            }
            tabObj.inFlightPrompt.text = evtNorm;
          }
        }

        // 2. Check DOM for matching user message bubble (only the last user message)
        if (!isAlreadyRendered) {
          const userMsgs = tabObj.chatMessagesEl.querySelectorAll('.chat-msg.user-msg');
          if (userMsgs.length > 0) {
            const lastMsg = userMsgs[userMsgs.length - 1];
            const isUnconfirmed = lastMsg.dataset.inFlight === 'true';
            const raw = normalizePromptText(lastMsg.dataset.rawPrompt || '');
            const uBody = lastMsg.querySelector('.chat-msg-body');
            const bodyText = normalizePromptText(uBody ? uBody.textContent : '');

            let hasCompletedAssistantAfter = false;
            let sib = lastMsg.nextElementSibling;
            while (sib) {
              if (sib.classList.contains('assistant-msg') && !sib.classList.contains('in-flight')) {
                hasCompletedAssistantAfter = true;
                break;
              }
              sib = sib.nextElementSibling;
            }

            const textMatches = (raw && (raw === evtNorm || evtNorm.startsWith(raw) || raw.startsWith(evtNorm))) ||
                               (bodyText && (bodyText === evtNorm || evtNorm.startsWith(bodyText) || bodyText.startsWith(evtNorm)));

            if (isUnconfirmed) {
              if (textMatches) {
                isAlreadyRendered = true;
                delete lastMsg.dataset.inFlight;
                tabObj.inFlightPrompt = {
                  text: evtNorm,
                  el: lastMsg,
                  timestamp: tabObj.inFlightPrompt ? tabObj.inFlightPrompt.timestamp : Date.now()
                };
              }
            } else {
              if (textMatches && !hasCompletedAssistantAfter) {
                isAlreadyRendered = true;
                tabObj.inFlightPrompt = {
                  text: evtNorm,
                  el: lastMsg,
                  timestamp: tabObj.inFlightPrompt ? tabObj.inFlightPrompt.timestamp : Date.now()
                };
              } else if ((raw && raw === evtNorm) || (bodyText && bodyText === evtNorm)) {
                const msgTime = lastMsg.dataset.timestamp ? new Date(lastMsg.dataset.timestamp).getTime() : 0;
                if (!msgTime || (Date.now() - msgTime) < 5000) {
                  isAlreadyRendered = true;
                  tabObj.inFlightPrompt = {
                    text: evtNorm,
                    el: lastMsg,
                    timestamp: tabObj.inFlightPrompt ? tabObj.inFlightPrompt.timestamp : Date.now()
                  };
                }
              }
            }
          }
        }

        if (!isAlreadyRendered) {
          const newMsgEl = appendChatMessage(tabObj, {
            role: 'user',
            content: evt.text,
            rawPrompt: evt.text,
            timestamp: evt.timestamp || new Date().toISOString()
          });
          if (tabObj.activeInStreamActivityEl && tabObj.activeInStreamActivityEl.parentNode === tabObj.chatMessagesEl) {
            tabObj.chatMessagesEl.insertBefore(newMsgEl, tabObj.activeInStreamActivityEl);
          } else if (tabObj.activeTurnMsgEl && tabObj.activeTurnMsgEl.parentNode === tabObj.chatMessagesEl) {
            tabObj.chatMessagesEl.insertBefore(newMsgEl, tabObj.activeTurnMsgEl);
          }
          tabObj.inFlightPrompt = {
            text: evtNorm,
            el: newMsgEl,
            timestamp: evt.timestamp ? (new Date(evt.timestamp).getTime() || Date.now()) : Date.now()
          };
        }
      }
      setChatSending(tabObj, true);
      break;

    case 'text_delta': {
      const incoming = evt.text || '';
      if (incoming) {
        tabObj.sending = true;
        if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
        if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
        const currentMsgEl = ensureActiveAssistantMessage(tabObj);
        if (currentMsgEl) {
          if (tabObj.activeTurnHadTool && tabObj.activeTurnBuffer) {
            tabObj.activeTurnBuffer = ensureDoubleNewlineSeparation(tabObj.activeTurnBuffer, incoming);
            tabObj.activeTurnHadTool = false;
          } else {
            tabObj.activeTurnBuffer = (tabObj.activeTurnBuffer || '') + incoming;
          }
          scheduleActiveTurnStreamRender(tabObj);
        }
      }
      break;
    }

    case 'thought_delta':
      if (evt.thinking) {
        tabObj.sending = true;
        if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
        if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
        tabObj.activeTurnThinking = (tabObj.activeTurnThinking || '') + evt.thinking;
        if (tabObj.activeTurnMsgEl) {
          const slot = tabObj.activeTurnMsgEl.querySelector('.chat-thinking-slot');
          if (slot) {
            let thinkBlock = slot.querySelector('.chat-thinking');
            if (!thinkBlock) {
              thinkBlock = document.createElement('details');
              thinkBlock.className = 'chat-thinking';
              thinkBlock.open = true;
              thinkBlock.innerHTML = `<summary>💭 Thinking...</summary><div class="thinking-text"></div>`;
              slot.appendChild(thinkBlock);
            }
            const thinkText = thinkBlock.querySelector('.thinking-text');
            if (thinkText) {
              thinkText.textContent += evt.thinking;
            }
            tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
          }
        } else {
          showInStreamActivity(tabObj, {
            icon: '💭',
            text: 'Thinking...',
            badge: null
          });
        }
      }
      break;

    case 'tool_start': {
      flushActiveTurnStreamRender(tabObj);
      tabObj.sending = true;
      if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
      if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
      tabObj.activeTurnHadTool = true;
      tabObj.activeTurnTools = tabObj.activeTurnTools || [];

      const inputStr = typeof evt.tool_input === 'string'
        ? evt.tool_input
        : (evt.tool_input ? JSON.stringify(evt.tool_input, null, 2) : '');

      tabObj.activeTurnTools.push({
        name: evt.tool_name || 'tool',
        detail: inputStr,
        status: 'running',
        output: ''
      });

      if (evt.tool_name === 'TodoWrite' || evt.tool_name === 'todo_write') {
        const todoCard = renderTodoWriteCard(tabObj, evt.tool_input);
        if (todoCard && tabObj.activeTurnMsgEl) {
          const slot = tabObj.activeTurnMsgEl.querySelector('.chat-tools-slot');
          if (slot) slot.appendChild(todoCard);
        }
      }

      if (tabObj.activeTurnMsgEl && evt.tool_name) {
        const slot = tabObj.activeTurnMsgEl.querySelector('.chat-tools-slot');
        if (slot) {
          let groupCard = slot.querySelector('.chat-tool-group');
          if (!groupCard) {
            groupCard = document.createElement('details');
            groupCard.className = 'chat-tool-card chat-tool-group';
            groupCard.open = true;
            groupCard.dataset.toolCalls = JSON.stringify([]);
            groupCard.innerHTML = `
              <summary>
                <span class="tool-group-title">⚡ ${escapeHtml(evt.tool_name)}: running...</span>
                <span class="tool-group-pill">1 action</span>
              </summary>
              <div class="chat-tool-content chat-tool-group-content">
                <div class="chat-tool-call-list"></div>
              </div>
            `;
            slot.appendChild(groupCard);
          }

          let toolList = [];
          try {
            toolList = JSON.parse(groupCard.dataset.toolCalls || '[]');
          } catch (_) {}

          toolList.push({ name: evt.tool_name, detail: inputStr });
          groupCard.dataset.toolCalls = JSON.stringify(toolList);

          const summaryTitle = groupCard.querySelector('.tool-group-title');
          const summaryPill = groupCard.querySelector('.tool-group-pill');
          const label = getToolGroupSummaryLabel(toolList.map(t => t.detail ? `${t.name}: ${t.detail}` : t.name));
          if (summaryTitle) summaryTitle.textContent = `${label} (running...)`;
          if (summaryPill) summaryPill.textContent = `${toolList.length} ${toolList.length === 1 ? 'action' : 'actions'}`;

          const listEl = groupCard.querySelector('.chat-tool-call-list');
          if (listEl) {
            const itemEl = document.createElement('div');
            itemEl.className = 'chat-tool-call-item in-progress';
            itemEl.innerHTML = `
              <div class="chat-tool-call-header">
                <span class="chat-tool-call-idx">#${toolList.length}</span>
                <span class="chat-tool-call-badge">${escapeHtml(evt.tool_name)}</span>
                <span class="chat-tool-status">running...</span>
              </div>
              ${inputStr ? `<div class="chat-tool-call-detail"><code>${escapeHtml(inputStr)}</code></div>` : ''}
            `;
            listEl.appendChild(itemEl);
          }
          tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
        }
      }

      // Always show/update in-stream activity for the active tool
      const activity = formatToolActivity(evt.tool_name, evt.tool_input);
      showInStreamActivity(tabObj, activity);

      if (evt.tool_name === 'Agent' || evt.tool_name === 'Task') {
        let promptText = '';
        if (evt.tool_input && typeof evt.tool_input === 'object') {
          promptText = evt.tool_input.prompt || evt.tool_input.description || '';
        } else if (typeof evt.tool_input === 'string') {
          promptText = evt.tool_input;
        }
        const sub = {
          id: 'sub-' + Date.now(),
          name: evt.tool_name,
          prompt: promptText,
          state: 'running'
        };
        tabObj.runningSubagents = tabObj.runningSubagents || [];
        tabObj.runningSubagents.push(sub);
        renderSubagentsBar(tabObj);
      }
      break;
    }

    case 'tool_result': {
      tabObj.sending = true;
      if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'inline-flex';
      if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '⚡ Working...';
      tabObj.activeTurnHadTool = true;
      tabObj.activeTurnTools = tabObj.activeTurnTools || [];
      const lastTool = tabObj.activeTurnTools.length > 0 ? tabObj.activeTurnTools[tabObj.activeTurnTools.length - 1] : null;
      if (lastTool) {
        lastTool.status = evt.is_error ? 'error' : 'done';
        lastTool.output = evt.tool_output || '';
      }

      const toolName = evt.tool_name || (lastTool ? lastTool.name : '');
      if (toolName === 'Agent' || toolName === 'Task') {
        tabObj.runningSubagents = (tabObj.runningSubagents || []).filter(s => s.name !== toolName && s.role !== toolName);
        renderSubagentsBar(tabObj);
      }

      if (tabObj.activeTurnMsgEl) {
        const slot = tabObj.activeTurnMsgEl.querySelector('.chat-tools-slot');
        if (slot) {
          const groupCard = slot.querySelector('.chat-tool-group');
          if (groupCard) {
            const listEl = groupCard.querySelector('.chat-tool-call-list');
            const lastItem = listEl ? (listEl.querySelector('.chat-tool-call-item.in-progress:last-child') || listEl.lastElementChild) : null;
            if (lastItem) {
              lastItem.classList.remove('in-progress');
              const statusEl = lastItem.querySelector('.chat-tool-status');
              if (statusEl) statusEl.textContent = 'done';
              if (evt.tool_output) {
                const outEl = document.createElement('div');
                outEl.className = 'chat-tool-output';
                outEl.textContent = evt.tool_output;
                lastItem.appendChild(outEl);
              }
              linkifyChatFiles(lastItem, tabObj.session);
            }
          }
        }
        tabObj.chatMessagesEl.scrollTop = tabObj.chatMessagesEl.scrollHeight;
      }

      showInStreamActivity(tabObj, {
        icon: '✓',
        text: `Completed ${toolName || 'tool'} • Processing next step...`,
        badge: toolName || null
      });
      break;
    }

    case 'done':
    case 'turn_complete': {
      flushActiveTurnStreamRender(tabObj);
      tabObj.runningSubagents = [];
      renderSubagentsBar(tabObj);
      hideInStreamActivity(tabObj);

      if (!tabObj.activeTurnBuffer && evt.text) {
        tabObj.activeTurnBuffer = evt.text;
      }

      // If message bubble does not exist yet, create it if there is text or executed tools
      if (!tabObj.activeTurnMsgEl && (tabObj.activeTurnBuffer || (tabObj.activeTurnTools && tabObj.activeTurnTools.length > 0))) {
        ensureActiveAssistantMessage(tabObj);
      }

      const activeMsg = tabObj.activeTurnMsgEl;
      if (activeMsg) {
        const groupCard = activeMsg.querySelector('.chat-tool-group');
        if (groupCard) {
          groupCard.open = false; // collapse on completion so it doesn't pollute
          let toolList = [];
          try {
            toolList = JSON.parse(groupCard.dataset.toolCalls || '[]');
          } catch (_) {}
          const label = getToolGroupSummaryLabel(toolList.map(t => t.detail ? `${t.name}: ${t.detail}` : t.name));
          const summaryTitle = groupCard.querySelector('.tool-group-title');
          if (summaryTitle) summaryTitle.textContent = label;
        }

        if (tabObj.activeTurnBuffer) {
          const bodyEl = activeMsg.querySelector('.chat-msg-body');
          if (bodyEl) {
            const html = renderMarkdown(tabObj.activeTurnBuffer);
            bodyEl.innerHTML = html;
          }
        }
        activeMsg.classList.remove('in-flight');
        const cursor = activeMsg.querySelector('.chat-streaming-cursor');
        if (cursor) cursor.remove();
        attachCodeBlockCopyButtons(activeMsg);
        attachChatMessageListeners(activeMsg, tabObj.activeTurnBuffer, tabObj);
        linkifyChatFiles(activeMsg, tabObj.session);
        if (isChatAutoReadEnabled()) {
          handleReadChatMessage(tabObj, activeMsg, false);
        }
      }
      const prevCompleted = tabObj.lastCompletedTurn;
      const userMsgs = tabObj.chatMessagesEl.querySelectorAll('.chat-msg.user-msg');
      const lastUserEl = userMsgs.length > 0 ? userMsgs[userMsgs.length - 1] : null;
      const lastUserBody = lastUserEl ? lastUserEl.querySelector('.chat-msg-body') : null;
      const lastUserText = normalizePromptText(
        (lastUserEl && lastUserEl.dataset.rawPrompt) ||
        (lastUserBody && lastUserBody.textContent) ||
        ''
      );
      tabObj.lastCompletedTurn = {
        userEl: lastUserEl || (prevCompleted && prevCompleted.userEl) || null,
        assistantEl: activeMsg || (prevCompleted && prevCompleted.assistantEl) || null,
        userText: lastUserText || (prevCompleted && prevCompleted.userText) || '',
        assistantText: normalizePromptText(tabObj.activeTurnBuffer || evt.text || '') || (prevCompleted && prevCompleted.assistantText) || '',
        timestamp: Date.now()
      };
      tabObj.inFlightPrompt = null;
      setChatSending(tabObj, false);
      // Asynchronously fetch the authoritative on-disk transcript so any missed SSE chunks or tool outputs are reconciled
      loadChatTranscript(tabObj);
      loadChatQueue(tabObj);
      fetchSessions();
      break;
    }

    case 'turn_cancelled': {
      flushActiveTurnStreamRender(tabObj);
      stopActiveSpeech();
      tabObj.runningSubagents = [];
      renderSubagentsBar(tabObj);
      hideInStreamActivity(tabObj);

      if (!tabObj.activeTurnMsgEl && (tabObj.activeTurnBuffer || (tabObj.activeTurnTools && tabObj.activeTurnTools.length > 0))) {
        ensureActiveAssistantMessage(tabObj);
      }

      const activeMsg = tabObj.activeTurnMsgEl;
      if (activeMsg) {
        activeMsg.classList.remove('in-flight');
        const cursor = activeMsg.querySelector('.chat-streaming-cursor');
        if (cursor) cursor.remove();
        const bodyEl = activeMsg.querySelector('.chat-msg-body');
        if (bodyEl) {
          bodyEl.innerHTML += `<div style="margin-top: 8px; color: var(--text-dim); font-style: italic;">[Turn cancelled by user]</div>`;
        }
        attachCodeBlockCopyButtons(activeMsg);
        attachChatMessageListeners(activeMsg, tabObj.activeTurnBuffer, tabObj);
        linkifyChatFiles(activeMsg, tabObj.session);
      }
      tabObj.inFlightPrompt = null;
      setChatSending(tabObj, false);
      if (tabObj.promptQueue && tabObj.promptQueue.length > 0) {
        tabObj.isQueuePaused = true;
        renderChatQueue(tabObj);
      }
      fetchSessions();
      break;
    }

    case 'error': {
      flushActiveTurnStreamRender(tabObj);
      stopActiveSpeech();
      tabObj.runningSubagents = [];
      renderSubagentsBar(tabObj);
      hideInStreamActivity(tabObj);

      const activeMsg = ensureActiveAssistantMessage(tabObj);
      if (activeMsg) {
        activeMsg.classList.remove('in-flight');
        const cursor = activeMsg.querySelector('.chat-streaming-cursor');
        if (cursor) cursor.remove();
        const bodyEl = activeMsg.querySelector('.chat-msg-body');
        if (bodyEl) {
          bodyEl.innerHTML += `<div style="margin-top: 8px; color: var(--accent-red);">⚠️ Error: ${escapeHtml(evt.text || 'Turn error')}</div>`;
        }
        attachCodeBlockCopyButtons(activeMsg);
        attachChatMessageListeners(activeMsg, tabObj.activeTurnBuffer, tabObj);
        linkifyChatFiles(activeMsg, tabObj.session);
      }
      tabObj.inFlightPrompt = null;
      setChatSending(tabObj, false);
      if (tabObj.promptQueue && tabObj.promptQueue.length > 0) {
        tabObj.isQueuePaused = true;
        renderChatQueue(tabObj);
      }
      break;
    }
  }
}

function resetChatComposer(tabObj) {
  if (!tabObj) return;
  tabObj.inFlightPrompt = null;
  if (tabObj._streamRenderRafId) {
    if (typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(tabObj._streamRenderRafId);
    }
    tabObj._streamRenderRafId = null;
  }
  tabObj.sending = false;
  tabObj.dispatchingPrompt = false;
  if (tabObj.session && isSessionWorking(tabObj.session)) {
    tabObj.session.state = 3;
    if (Array.isArray(state.sessions)) {
      const match = state.sessions.find(s => s.id === tabObj.session.id || (tabObj.session.native_id && s.native_id === tabObj.session.native_id));
      if (match && isSessionWorking(match)) match.state = 3;
    }
  }
  hideInStreamActivity(tabObj);
  tabObj.activeTurnMsgEl = null;
  tabObj.activeTurnBuffer = '';
  tabObj.activeTurnHadTool = false;
  tabObj.activeTurnTools = [];
  tabObj.activeTurnThinking = '';
  if (tabObj.chatCancelBtn) tabObj.chatCancelBtn.style.display = 'none';
  if (tabObj.chatStatusBadge) tabObj.chatStatusBadge.textContent = '🟢 Ready';
  updateComposerButtonState(tabObj);
  if (tabObj.chatInputEl && state.activeTabId === tabObj.id && tabObj.viewMode === 'chat' && !isAuxiliaryInputFocused(tabObj.chatInputEl)) {
    tabObj.chatInputEl.focus();
  }
}

// Reconcile open chat tabs when a session update arrives via main SSE (/v1/events) or poll
function onSessionUpdated(updatedSess) {
  if (!updatedSess || !updatedSess.id || !state.openTabs) return;

  for (const [, tabObj] of state.openTabs.entries()) {
    if (!tabObj || !tabObj.session) continue;
    const isMatch = tabObj.session.id === updatedSess.id ||
      (updatedSess.old_id && tabObj.session.id === updatedSess.old_id) ||
      (tabObj.session.native_id && updatedSess.native_id && tabObj.session.native_id === updatedSess.native_id);
    if (!isMatch) continue;

    tabObj.session = {
      ...tabObj.session,
      ...updatedSess,
      id: updatedSess.id,
      native_id: updatedSess.native_id || tabObj.session.native_id,
      name: updatedSess.name || tabObj.session.name
    };

    updateStatusPills(tabObj);
    if (tabObj.session.blocked && (tabObj.session.state === 2 || tabObj.session.state === 'blocked')) {
      renderInteractiveBlockCard(tabObj, tabObj.session.blocked);
    }

    if (tabObj.chatStreamSessionId && tabObj.chatStreamSessionId !== updatedSess.id) {
      connectChatStream(tabObj);
    }

    if (typeof updatedSess.running_subagents === 'number') {
      if (updatedSess.running_subagents === 0) {
        tabObj.runningSubagents = [];
        renderSubagentsBar(tabObj);
      } else if (!tabObj.runningSubagents || tabObj.runningSubagents.length !== updatedSess.running_subagents) {
        fetchRunningSubagents(tabObj);
      }
    }

    const working = isSessionWorking(updatedSess);
    const activeTurnUI = hasActiveTurnUI(tabObj);

    if (!working && activeTurnUI && !tabObj.dispatchingPrompt) {
      setChatSending(tabObj, false);
      loadChatTranscript(tabObj);
      loadChatQueue(tabObj);
    } else if (working) {
      if (tabObj.session.engine_type === 'headless' || tabObj.viewMode === 'chat') {
        setChatSending(tabObj, true);
        if (tabObj.session.engine_type === 'headless' && tabObj.viewMode === 'chat') {
          connectChatStream(tabObj);
        }
      }
    }
  }
}

// Deterministically handle document-level paste events (including Wispr Flow voice dictation clipboard injection
// even when document.activeElement === activeTab.chatInputEl during macOS NSPanel responder transitions)
document.addEventListener('paste', async (e) => {
  if (e._ackbarPasteHandled) return;
  if (!e.clipboardData) return;
  if (!state.openTabs || !state.activeTabId) return;
  const activeTab = state.openTabs.get(state.activeTabId);
  if (!activeTab || activeTab.type !== 'terminal' || activeTab.viewMode !== 'chat' || !activeTab.chatInputEl) return;
  if (isAuxiliaryInputFocused(activeTab.chatInputEl)) return;

  const files = extractPastedAttachmentFiles(e.clipboardData);
  if (files.length > 0) {
    e._ackbarPasteHandled = true;
    activeTab._lastPasteHandledAt = Date.now();
    e.preventDefault();
    e.stopPropagation();
    activeTab.chatInputEl.focus();
    for (const file of files) {
      await addPendingAttachment(activeTab, file);
    }
    return;
  }

  const pastedText = e.clipboardData.getData('text/plain');
  if (pastedText) {
    e._ackbarPasteHandled = true;
    activeTab._lastPasteHandledAt = Date.now();
    e.preventDefault();
    e.stopPropagation();
    insertTextIntoPromptInput(activeTab.chatInputEl, pastedText);
  }
}, true);

// Fallback for synthetic Cmd+V / Ctrl+V keydowns (e.g. Wispr Flow CGEvent when macOS drops native paste event)
document.addEventListener('keydown', (e) => {
  if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return;
  if (e.key !== 'v' && e.key !== 'V') return;
  if (!state.openTabs || !state.activeTabId) return;
  const activeTab = state.openTabs.get(state.activeTabId);
  if (!activeTab || activeTab.type !== 'terminal' || activeTab.viewMode !== 'chat' || !activeTab.chatInputEl) return;
  if (isAuxiliaryInputFocused(activeTab.chatInputEl)) return;

  const keydownAt = Date.now();
  setTimeout(async () => {
    if ((activeTab._lastPasteHandledAt || 0) >= keydownAt) return;
    if ((activeTab._lastInputAt || 0) >= keydownAt) return;
    if (!navigator.clipboard || typeof navigator.clipboard.readText !== 'function') return;
    try {
      const clipText = await navigator.clipboard.readText();
      if (clipText && (activeTab._lastPasteHandledAt || 0) < keydownAt && (activeTab._lastInputAt || 0) < keydownAt) {
        activeTab._lastPasteHandledAt = Date.now();
        insertTextIntoPromptInput(activeTab.chatInputEl, clipText);
      }
    } catch (_) {}
  }, 60);
}, true);

// Reconcile transcript and reconnect headless stream when browser tab wakes or regains visibility
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState !== 'visible') return;
  if (!state.openTabs || !state.activeTabId) return;
  const activeTab = state.openTabs.get(state.activeTabId);
  if (!activeTab || !activeTab.session || activeTab.viewMode !== 'chat') return;

  const sessMatch = (state.sessions || []).find(s =>
    s.id === activeTab.session.id ||
    (activeTab.session.native_id && s.native_id === activeTab.session.native_id)
  );
  if (sessMatch) {
    onSessionUpdated(sessMatch);
  }
  loadChatTranscript(activeTab);
  loadChatQueue(activeTab);
  if (activeTab.session.engine_type === 'headless' &&
      (!activeTab.chatEventSource || activeTab.chatEventSource.readyState === EventSource.CLOSED)) {
    connectChatStream(activeTab);
  }
});

async function cancelChatTurn(tabObj) {
  if (!tabObj || !tabObj.session) return;
  const baseUrl = getSessionBaseUrl(tabObj.session.id, tabObj.session);
  try {
    await fetch(`${baseUrl}/v1/sessions/cancel`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session_id: tabObj.session.id })
    });
  } catch (e) {
    console.warn('Failed to cancel turn:', e);
  }
}

if (typeof window !== 'undefined') {
  window.AckbarChat = {
    onSessionUpdated,
    setChatSending,
    loadChatTranscript,
    connectChatStream,
    disconnectChatStream,
    insertTextIntoPromptInput
  };
}

// Open Session in Terminal/Chat Tab

export {
  CHAT_PAGE_SIZE,
  KNOWN_CHAT_FILE_EXTS,
  CHAT_IMAGE_EXTS,
  APP_ICONS,
  extractPastedAttachmentFiles,
  insertTextIntoPromptInput,
  setupChatInterface,
  loadChatQueue,
  renderLoadMoreBanner,
  loadOlderTranscriptMessages,
  normalizePromptText,
  isPromptInTranscriptTail,
  coalesceTranscriptMessages,
  loadChatTranscript,
  copyTextToClipboard,
  attachCodeBlockCopyButtons,
  renderMermaidDiagrams,
  getChatFileContentUrl,
  openChatFileInApp,
  isChatFilePath,
  parseLineRange,
  CHAT_VIDEO_EXTS,
  CHAT_AUDIO_EXTS,
  createChatFilePill,
  createChatImagePreviewCard,
  createChatMediaPreviewCard,
  linkifyChatFiles,
  attachChatMessageListeners,
  parseToolCall,
  getToolGroupSummaryLabel,
  renderToolGroupHtml,
  createChatMessageElement,
  appendChatMessage,
  isSessionWorking,
  hasActiveTurnUI,
  setChatSending,
  updateComposerButtonState,
  renderChatQueue,
  renderSubagentsBar,
  fetchRunningSubagents,
  dispatchNextQueuedPrompt,
  sendChatPrompt,
  disconnectChatStream,
  connectChatStream,
  handleChatStreamEvent,
  hideInStreamActivity,
  resetChatComposer,
  onSessionUpdated,
  cancelChatTurn,
  sanitizeMarkdownForSpeech,
  isSimpleSpeechCandidate,
  resolveLiveMessageEl,
  stopActiveSpeech,
  speakText,
  handleReadChatMessage,
  handleReplayChatMessage,
  toggleVoiceMic,
  renderSuggestedChips,
  isChatAutoReadEnabled,
  updateStatusPills,
  toggleChatConfigPopover,
  renderConfigPopoverContent,
  togglePlanMode,
  renderInteractiveBlockCard,
  renderTodoWriteCard,
  submitSessionResponse,
  executeClientAction,
  ANTIGRAVITY_DEFAULT_MODELS,
  CLAUDE_DEFAULT_MODELS,
  getAgentModelOptions,
  formatModelLabel,
  fetchSessionAgentModels
};
