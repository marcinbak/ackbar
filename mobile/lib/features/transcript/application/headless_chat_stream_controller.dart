import 'dart:async';
import 'dart:math';
import 'package:flutter/foundation.dart';
import '../../../core/models/chat_stream_event.dart';
import '../../../core/models/host.dart';
import '../../../core/models/session.dart';
import '../../../core/models/subagent.dart';
import '../../../core/models/transcript.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/chat_stream_client.dart';

class HeadlessChatStreamController extends ChangeNotifier {
  final ApiClient _apiClient;
  final ChatStreamClient _chatStreamClient;
  final Random _random;

  Session _session;
  HostRecord _host;
  int _sessionEpoch = 0;
  int _assistantCountAtSend = 0;
  int _messageCountAtSend = 0;

  TranscriptData? _transcriptData;
  bool _isLoading = false;
  bool _isRefreshing = false;
  bool _isSending = false;
  bool _awaitingTurnCompletion = false;
  String? _errorMessage;
  TranscriptMessage? _pendingUserMessage;
  List<PromptQueueItemModel> _queueItems = const [];
  bool _queuePaused = false;
  List<SubagentInfo> _runningSubagents = const [];

  StreamSubscription<ChatStreamEvent>? _streamSub;
  Timer? _pollingTimer;
  bool _hasStreamConnected = false;
  String _currentTurnId = '';
  bool _lastEventWasTool = false;

  HeadlessChatStreamController({
    required Session session,
    required HostRecord host,
    required ApiClient apiClient,
    ChatStreamClient? chatStreamClient,
    Random? random,
  })  : _session = session,
        _host = host,
        _apiClient = apiClient,
        _chatStreamClient =
            chatStreamClient ?? ChatStreamClient(client: apiClient.client),
        _random = random ?? Random();

  Session get session => _session;
  HostRecord get host => _host;
  int get sessionEpoch => _sessionEpoch;
  TranscriptData? get transcriptData => _transcriptData;
  bool get isLoading => _isLoading;
  bool get isSending => _isSending;
  bool get awaitingTurnCompletion => _awaitingTurnCompletion;
  String? get errorMessage => _errorMessage;
  TranscriptMessage? get pendingUserMessage => _pendingUserMessage;
  List<PromptQueueItemModel> get queueItems => _queueItems;
  bool get queuePaused => _queuePaused;
  List<SubagentInfo> get runningSubagents => _runningSubagents;

  void init() {
    _loadInitialTranscript();
    _startPolling();
    if (_session.isHeadless) _connectStream();
  }

  void updateSession(Session updated) {
    _session = updated;
    notifyListeners();
  }

  void rotateToSession(Session nextSession, HostRecord host) {
    _sessionEpoch++;
    _streamSub?.cancel();
    _streamSub = null;
    _hasStreamConnected = false;
    _currentTurnId = '';
    _lastEventWasTool = false;
    _pendingUserMessage = null;
    _awaitingTurnCompletion = false;
    _transcriptData = null;
    _queueItems = const [];
    _runningSubagents = const [];
    _session = nextSession;
    _host = host;

    notifyListeners();
    _loadInitialTranscript();
    if (_session.isHeadless) _connectStream();
  }

  void _startPolling() {
    _pollingTimer?.cancel();
    _pollingTimer = Timer.periodic(const Duration(seconds: 3), (_) {
      loadSubagents();
      if (!_session.isHeadless || !_hasStreamConnected) {
        refreshTranscriptSilently();
      }
    });
  }

  void _connectStream() {
    _streamSub?.cancel();
    _streamSub = null;
    final epoch = _sessionEpoch;
    final initialTurnId = _transcriptData?.inFlightTurn?.turnId ?? '';
    final initialAfterSeq = _transcriptData?.inFlightTurn?.lastSeq ?? 0;

    _streamSub = _chatStreamClient
        .connect(
      _host,
      _session.id,
      initialTurnId: initialTurnId,
      initialAfterSeq: initialAfterSeq,
    )
        .listen(
      (evt) {
        if (epoch != _sessionEpoch) return;
        _onStreamEvent(evt);
      },
      onError: (_) {},
    );
  }

  void _onStreamEvent(ChatStreamEvent evt) {
    _hasStreamConnected = true;
    switch (evt.type) {
      case ChatStreamEventType.status:
      case ChatStreamEventType.sessionInit:
        break;
      case ChatStreamEventType.queueUpdate:
        _queueItems = evt.queueItems;
        _queuePaused = evt.queuePaused;
        notifyListeners();
        break;
      case ChatStreamEventType.subagentsUpdate:
        _runningSubagents = evt.subagents
            .map((s) => SubagentInfo(
                  id: s['id']?.toString() ?? '',
                  name: s['name']?.toString() ?? '',
                  role: s['role']?.toString(),
                  prompt: s['prompt']?.toString() ?? '',
                  state: s['state']?.toString() ?? 'running',
                  startedAt:
                      DateTime.tryParse(s['started_at']?.toString() ?? '') ??
                          DateTime.now(),
                ))
            .where((s) => s.isRunning)
            .toList();
        notifyListeners();
        break;
      case ChatStreamEventType.turnStart:
        _currentTurnId = evt.turnId;
        _awaitingTurnCompletion = true;
        _lastEventWasTool = false;
        final msgs =
            List<TranscriptMessage>.from(_transcriptData?.messages ?? []);
        final idx =
            msgs.indexWhere((m) => m.isAssistant && m.turnId == evt.turnId);
        if (idx >= 0) {
          msgs[idx] = msgs[idx].copyWith(inFlight: true);
        } else {
          msgs.add(TranscriptMessage(
            role: 'assistant',
            content: '',
            turnId: evt.turnId,
            inFlight: true,
            timestamp: evt.timestamp,
          ));
        }
        _updateMessages(msgs);
        break;
      case ChatStreamEventType.textDelta:
        if (evt.text.isEmpty) return;
        final msgs =
            List<TranscriptMessage>.from(_transcriptData?.messages ?? []);
        final idx = _findOrCreateAssistant(msgs, evt.turnId, evt.timestamp);
        final existing = msgs[idx];
        var text = existing.content;
        if (_lastEventWasTool && text.isNotEmpty && !text.endsWith('\n')) {
          text += '\n\n';
        }
        msgs[idx] = existing.copyWith(content: text + evt.text, inFlight: true);
        _lastEventWasTool = false;
        _updateMessages(msgs);
        break;
      case ChatStreamEventType.thoughtDelta:
        if (evt.thinking.isEmpty) return;
        final msgs =
            List<TranscriptMessage>.from(_transcriptData?.messages ?? []);
        final idx = _findOrCreateAssistant(msgs, evt.turnId, evt.timestamp);
        final existing = msgs[idx];
        msgs[idx] = existing.copyWith(
          thinking: (existing.thinking ?? '') + evt.thinking,
          inFlight: true,
        );
        _updateMessages(msgs);
        break;
      case ChatStreamEventType.toolStart:
        _lastEventWasTool = true;
        final msgs =
            List<TranscriptMessage>.from(_transcriptData?.messages ?? []);
        final idx = _findOrCreateAssistant(msgs, evt.turnId, evt.timestamp);
        final existing = msgs[idx];
        final tools = List<String>.from(existing.toolCalls)
          ..add(evt.formatToolCallSummary());
        msgs[idx] = existing.copyWith(toolCalls: tools, inFlight: true);
        _updateMessages(msgs);
        break;
      case ChatStreamEventType.toolResult:
        _lastEventWasTool = true;
        break;
      case ChatStreamEventType.turnComplete:
      case ChatStreamEventType.done:
      case ChatStreamEventType.turnCancelled:
      case ChatStreamEventType.error:
        _awaitingTurnCompletion = false;
        _pendingUserMessage = null;
        _lastEventWasTool = false;
        if (_transcriptData != null) {
          final msgs = _transcriptData!.messages.map((m) {
            return m.inFlight ? m.copyWith(inFlight: false) : m;
          }).toList();
          _updateMessages(msgs);
        }
        refreshTranscriptSilently();
        break;
      default:
        break;
    }
  }

  int _findOrCreateAssistant(
      List<TranscriptMessage> msgs, String turnId, DateTime ts) {
    final tid = turnId.isNotEmpty ? turnId : _currentTurnId;
    final idx = msgs.lastIndexWhere(
      (m) =>
          m.isAssistant && (m.inFlight || (tid.isNotEmpty && m.turnId == tid)),
    );
    if (idx >= 0) return idx;
    msgs.add(TranscriptMessage(
      role: 'assistant',
      content: '',
      turnId: tid,
      inFlight: true,
      timestamp: ts,
    ));
    return msgs.length - 1;
  }

  void _updateMessages(List<TranscriptMessage> msgs) {
    if (_transcriptData == null) {
      _transcriptData = TranscriptData(
        sessionId: _session.id,
        nativeId: _session.nativeId,
        agent: _session.agent,
        title: _session.displayTitle,
        cwd: _session.cwd,
        messages: msgs,
        rawMarkdown: '',
      );
    } else {
      _transcriptData = _transcriptData!.copyWith(messages: msgs);
    }
    notifyListeners();
  }

  Future<void> _loadInitialTranscript() async {
    final epoch = _sessionEpoch;
    _isLoading = true;
    _errorMessage = null;
    notifyListeners();
    try {
      final fetched = await _fetchData();
      if (epoch != _sessionEpoch) return;
      _transcriptData = fetched;
      _isLoading = false;
      notifyListeners();
      loadSubagents();
      if (_session.isHeadless && _streamSub == null) _connectStream();
    } catch (_) {
      if (epoch != _sessionEpoch) return;
      _isLoading = false;
      _errorMessage =
          'Unable to load transcript from host. Tap Retry to try again.';
      notifyListeners();
    }
  }

  Future<void> loadTranscript() => _loadInitialTranscript();

  Future<void> refreshTranscriptSilently() async {
    final epoch = _sessionEpoch;
    if (_isRefreshing) return;
    _isRefreshing = true;
    try {
      final fetched = await _fetchData();
      if (epoch != _sessionEpoch) return;

      var merged = fetched.messages;
      final assistantCount = merged.where((m) => m.isAssistant).length;
      if (_awaitingTurnCompletion &&
          assistantCount > _assistantCountAtSend &&
          _session.state != SessionState.working) {
        _awaitingTurnCompletion = false;
        _pendingUserMessage = null;
      }
      if (_pendingUserMessage != null) {
        final newMsgs = merged.length > _messageCountAtSend
            ? merged.sublist(_messageCountAtSend)
            : <TranscriptMessage>[];
        final exists = newMsgs.any((m) =>
            m.isUser &&
            (m.content.trim() == _pendingUserMessage!.content.trim() ||
                (m.turnId != null && m.turnId == _pendingUserMessage!.turnId)));
        if (exists || assistantCount > _assistantCountAtSend) {
          _pendingUserMessage = null;
        } else {
          merged = [...merged, _pendingUserMessage!];
        }
      }
      _transcriptData = fetched.copyWith(messages: merged);
      _errorMessage = null;
      notifyListeners();
    } catch (_) {
    } finally {
      _isRefreshing = false;
    }
  }

  Future<TranscriptData> _fetchData() async {
    final structured = await _apiClient.getStructuredTranscript(
      _host.url,
      _session.id,
      authToken: _host.authToken,
    );
    if (structured != null && structured.messages.isNotEmpty) {
      return structured;
    }
    final rawMd = await _apiClient.getTranscriptOrNull(
      _host.url,
      _session.id,
      authToken: _host.authToken,
    );
    if (rawMd != null && rawMd.trim().isNotEmpty) {
      return TranscriptData.fromRawMarkdown(
        sessionId: _session.id,
        agent: _session.agent,
        title: _session.displayTitle,
        markdown: rawMd,
      );
    }
    if (structured == null && rawMd == null) {
      throw Exception(
        'Unable to load transcript from host. Tap Retry to try again.',
      );
    }
    return structured ??
        TranscriptData(
          sessionId: _session.id,
          nativeId: _session.nativeId,
          agent: _session.agent,
          title: _session.displayTitle,
          cwd: _session.cwd,
          messages: const [],
          rawMarkdown: '',
        );
  }

  Future<void> loadSubagents() async {
    final epoch = _sessionEpoch;
    try {
      final list = await _apiClient.getSubagents(
        _host.url,
        _session.id,
        authToken: _host.authToken,
      );
      if (epoch != _sessionEpoch) return;
      _runningSubagents = list.where((s) => s.isRunning).toList();
      notifyListeners();
    } catch (_) {}
  }

  Future<PromptDispatchResult> submitPrompt(String text) async {
    final trimmed = text.trim();
    if (trimmed.isEmpty || _isSending) {
      return PromptDispatchResult.failure('Empty or busy prompt');
    }

    final ms = DateTime.now().millisecondsSinceEpoch;
    final hex = _random.nextInt(0xFFFFFF).toRadixString(16).padLeft(6, '0');
    final clientTurnId = 'ct_${ms}_$hex';

    final userMsg = TranscriptMessage(
      role: 'user',
      content: trimmed,
      turnId: clientTurnId,
      inFlight: true,
      timestamp: DateTime.now(),
    );

    final prevMessages = _transcriptData?.messages ?? [];
    _assistantCountAtSend = prevMessages.where((m) => m.isAssistant).length;
    _messageCountAtSend = prevMessages.length;
    _pendingUserMessage = userMsg;
    _isSending = true;
    _awaitingTurnCompletion = true;
    _updateMessages([...prevMessages, userMsg]);

    PromptDispatchResult res = await _apiClient.sendPromptDetailed(
      _host.url,
      _session.id,
      trimmed,
      authToken: _host.authToken,
      clientTurnId: clientTurnId,
    );

    if (!res.ok) {
      res = await _apiClient.sendPromptDetailed(
        _host.url,
        _session.id,
        trimmed,
        authToken: _host.authToken,
        clientTurnId: clientTurnId,
      );
    }

    if (!res.ok) {
      _pendingUserMessage = null;
      _awaitingTurnCompletion = false;
      _isSending = false;
      _updateMessages(prevMessages);
      return res;
    }

    _isSending = false;
    notifyListeners();
    return res;
  }

  void cancelTurnLocally() {
    _awaitingTurnCompletion = false;
    _isSending = false;
    _pendingUserMessage = null;
    notifyListeners();
  }

  void onAppResumed() {
    refreshTranscriptSilently();
    loadSubagents();
    if (_session.isHeadless && _streamSub == null) _connectStream();
  }

  @override
  void dispose() {
    _streamSub?.cancel();
    _pollingTimer?.cancel();
    _chatStreamClient.dispose();
    super.dispose();
  }
}
