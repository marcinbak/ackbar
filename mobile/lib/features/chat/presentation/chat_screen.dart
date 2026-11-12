import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/headless_models.dart';
import '../../../core/models/session.dart';
import '../../../core/models/transcript.dart';
import '../../../core/navigation/session_navigator.dart';
import '../../../core/network/api_client.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/services/audio_companion_service.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/utils/session_host_resolver.dart';
import '../../headless/presentation/headless_auth_card.dart';
import '../../headless/presentation/headless_config_sheet.dart';
import '../../terminal/presentation/terminal_screen.dart';
import '../../transcript/application/headless_chat_stream_controller.dart';
import '../../transcript/presentation/headless_autocomplete_strip.dart';
import '../../transcript/presentation/headless_command_dispatcher.dart';
import '../../transcript/presentation/subagents_dock.dart';
import 'chat_composer_bar.dart';

class ChatScreen extends ConsumerStatefulWidget {
  final Session session;
  const ChatScreen({super.key, required this.session});

  static Future<void> open(BuildContext context, Session session) {
    return Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => ChatScreen(session: session)),
    );
  }

  @override
  ConsumerState<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends ConsumerState<ChatScreen>
    with WidgetsBindingObserver {
  late Session _activeSession = widget.session;
  final List<HeadlessEphemeralCardEntry> _ephemeralCards = [];
  final ScrollController _scrollController = ScrollController();
  final TextEditingController _promptController = TextEditingController();
  final FocusNode _promptFocusNode = FocusNode();
  bool _showJumpToBottom = false;
  late final HeadlessChatStreamController _streamController;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _scrollController.addListener(_onScroll);
    final host = SessionHostResolver.resolveHostRecord(
        ref.read(hostsListProvider), _activeSession.host);
    _streamController = HeadlessChatStreamController(
      session: _activeSession,
      host: host,
      apiClient: ref.read(apiClientProvider),
    );
    _streamController.addListener(_onStreamUpdated);
    _streamController.init();
  }

  void _onStreamUpdated() {
    if (!mounted) return;
    setState(() {});
    if (!_showJumpToBottom) _scrollToBottom();
  }

  void _handleSessionRotated(Session nextSession, String? notice) {
    if (!mounted) return;
    setState(() {
      _activeSession = nextSession;
      _ephemeralCards.add(HeadlessEphemeralCardEntry(
        id: 'rot_${DateTime.now().millisecondsSinceEpoch}',
        kind: 'status',
        createdAt: DateTime.now(),
        statusTitle: 'Conversation Rotated',
        statusBody: notice ?? 'Rotated to ${nextSession.displayTitle}',
      ));
    });
    ref.read(fleetSessionsProvider.notifier).upsertSession(nextSession);
    final h = SessionHostResolver.resolveHostRecord(
        ref.read(hostsListProvider), nextSession.host);
    _streamController.rotateToSession(nextSession, h);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _streamController.removeListener(_onStreamUpdated);
    _streamController.dispose();
    _scrollController.dispose();
    _promptController.dispose();
    _promptFocusNode.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    super.didChangeAppLifecycleState(state);
    if (state == AppLifecycleState.resumed && mounted) {
      _streamController.onAppResumed();
      if (ref.read(hostsListProvider).isNotEmpty) {
        ref.read(fleetSessionsProvider.notifier).refreshSessions();
      }
    }
  }

  Session _resolveLiveSession([List<Session>? sessions]) =>
      SessionHostResolver.resolveLiveSession(
          sessions ?? ref.read(fleetSessionsProvider), _activeSession);

  bool get _isSessionWorking =>
      _streamController.isSending ||
      _streamController.awaitingTurnCompletion ||
      _resolveLiveSession().state == SessionState.working;

  void _onScroll() {
    if (!_scrollController.hasClients) return;
    final pos = _scrollController.position;
    final away = (pos.maxScrollExtent - pos.pixels) > 300;
    if (away != _showJumpToBottom) setState(() => _showJumpToBottom = away);
  }

  Future<ResolvedSessionHost> _resolveHost() =>
      SessionHostResolver.resolveSessionEndpoint(
        getHosts: () => ref.read(hostsListProvider),
        session: _resolveLiveSession(),
        isMounted: () => mounted,
      );

  Future<({String url, String? authToken})> _resolveHostRecord() async {
    final res = await _resolveHost();
    return (url: res.url, authToken: res.authToken);
  }

  void _scrollToBottom({bool animate = true}) {
    if (!_scrollController.hasClients) return;
    final target = _scrollController.position.maxScrollExtent;
    animate
        ? _scrollController.animateTo(target,
            duration: const Duration(milliseconds: 250), curve: Curves.easeOut)
        : _scrollController.jumpTo(target);
  }

  Future<void> _sendPrompt() async {
    final text = _promptController.text.trim();
    if (text.isEmpty || _streamController.isSending) return;
    final currentSession = _resolveLiveSession();
    final host = SessionHostResolver.resolveHostRecord(
        ref.read(hostsListProvider), currentSession.host);
    final dispatcher = HeadlessCommandDispatcher(
      apiClient: ref.read(apiClientProvider),
      host: host,
      onOpenConfig: () => HeadlessConfigSheet.show(context, currentSession),
      onTakeWheel: () => _takeTheWheel(currentSession),
      onRotateSession: (newSession, {notice}) =>
          _handleSessionRotated(newSession, notice),
      onSessionUpdated: (updated) {
        ref.read(fleetSessionsProvider.notifier).upsertSession(updated);
        _streamController.updateSession(updated);
      },
    );
    final res = await dispatcher.dispatch(currentSession, text);
    if (!mounted) return;
    if (res.intercepted) {
      if (res.card != null) {
        setState(() => _ephemeralCards.add(res.card!));
      }
      _promptController.clear();
      _scrollToBottom();
      return;
    }
    _promptController.clear();
    _streamController.updateSession(currentSession);
    final submitRes = await _streamController.submitPrompt(text);
    if (!mounted) return;
    if (!submitRes.ok) {
      _promptController.text = text;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Failed to dispatch prompt to agent'),
          backgroundColor: AppColors.statusCoral,
        ),
      );
    }
    _scrollToBottom();
  }

  Future<void> _cancelTurn() async {
    HapticFeedback.mediumImpact();
    try {
      final host = await _resolveHost();
      if (!mounted) return;
      await ref.read(apiClientProvider).cancelTurn(
          host.url, _resolveLiveSession().id,
          authToken: host.authToken);
      if (mounted) {
        _streamController.cancelTurnLocally();
        _streamController.refreshTranscriptSilently();
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('Turn cancelled'),
            backgroundColor: AppColors.surfaceHighlight));
      }
    } catch (_) {}
  }

  Future<void> _takeTheWheel(Session s) async {
    HapticFeedback.mediumImpact();
    final host = await _resolveHost();
    if (!mounted) return;
    try {
      final res = await ref
          .read(apiClientProvider)
          .takeWheel(host.url, s.id, authToken: host.authToken);
      if (!mounted) return;
      final tmux = res?['tmux_name']?.toString() ??
          res?['tmux_session_name']?.toString() ??
          s.tmuxName;
      final updated = s.copyWith(
          engineType: 'tmux',
          managed: true,
          tmuxName: tmux.isNotEmpty ? tmux : 'ackbar-${s.id}');
      ref.read(fleetSessionsProvider.notifier).upsertSession(updated);
      TerminalScreen.openReplacement(context, updated);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text('Failed to take wheel: $e'),
            backgroundColor: AppColors.statusCoral));
      }
    }
  }

  Future<void> _speakMessage(int index, String content,
      {bool forceRefresh = false, bool forceFull = false}) async {
    final host = await _resolveHost();
    if (!mounted) return;
    await ref.read(audioCompanionServiceProvider.notifier).speakMessage(
        _resolveLiveSession(), index, content,
        forceRefresh: forceRefresh,
        forceFull: forceFull,
        hostUrl: host.url,
        authToken: host.authToken);
  }

  void _autoReadLatestAssistantMessage() {
    final msgs = _streamController.transcriptData?.messages ?? [];
    final last = msgs
        .lastIndexWhere((m) => m.isAssistant && m.content.trim().isNotEmpty);
    if (last != -1) _speakMessage(last, msgs[last].content);
  }

  void _handleRecognizedSpeech(String text, bool isFinal) {
    if (!mounted || text.trim().isEmpty) return;
    final t = text.trim();
    if (!isFinal) {
      _promptController.text = t;
      _promptController.selection =
          TextSelection.fromPosition(TextPosition(offset: t.length));
      return;
    }
    final norm = t.toLowerCase().replaceAll(RegExp(r'[.,!?;:]+$'), '').trim();
    if (const {'repeat that', 'read that again', 'repeat', 'replay'}
        .contains(norm)) {
      _promptController.clear();
      _autoReadLatestAssistantMessage();
      return;
    }
    final auto = ref.read(audioCompanionServiceProvider).autoReadEnabled;
    _promptController.text = (auto && !t.contains('[Reply concisely'))
        ? '$t [Reply concisely in 2–3 spoken sentences]'
        : t;
    _sendPrompt();
  }

  Future<void> _toggleAntigravityLiveVoice(Session s) async {
    HapticFeedback.mediumImpact();
    final host = await _resolveHost();
    if (!mounted) return;
    if (!ref.read(audioCompanionServiceProvider).liveVoiceConnected &&
        s.isHeadless) {
      await ref
          .read(apiClientProvider)
          .takeWheel(host.url, s.id, authToken: host.authToken);
    }
    if (mounted) {
      await ref
          .read(audioCompanionServiceProvider.notifier)
          .toggleAntigravityLiveVoice(s,
              hostUrl: host.url, authToken: host.authToken);
    }
  }

  Widget _buildBodyContent(
      Session s, List<TranscriptMessage> msgs, TranscriptMessage? pending) {
    return Stack(
      children: [
        ChatTurnListView(
          scrollController: _scrollController,
          messages: msgs,
          ephemeralCards: _ephemeralCards,
          pendingUserMessage: pending,
          session: s,
          isLoading: _streamController.isLoading,
          errorMessage: _streamController.errorMessage,
          onRetry: _streamController.loadTranscript,
          onSpeak: _speakMessage,
          onDismissCard: (idx) => setState(() => _ephemeralCards.removeAt(idx)),
          onAttachToPrompt: (text) =>
              _promptController.text = '${_promptController.text} $text'.trim(),
        ),
        if (_showJumpToBottom)
          Positioned(
            right: 16,
            bottom: 16,
            child: FloatingActionButton.small(
              backgroundColor: AppColors.surfaceHighlight,
              onPressed: _scrollToBottom,
              child: const Icon(Icons.keyboard_arrow_down_rounded,
                  color: AppColors.infoCyan),
            ),
          ),
      ],
    );
  }

  IconButton _action(IconData icon, String tip, VoidCallback press,
          [Color? color, Key? key]) =>
      IconButton(
          key: key,
          icon: Icon(icon, size: 20, color: color),
          tooltip: tip,
          onPressed: press);

  List<Widget> _buildAppBarActions(Session s, AudioCompanionState audio) {
    final notifier = ref.read(audioCompanionServiceProvider.notifier);
    final auto = audio.autoReadEnabled;
    final live = audio.liveVoiceConnected;
    return [
      if (s.agent == 'antigravity')
        _action(
            Icons.record_voice_over_rounded,
            live ? 'Live Voice: Connected' : 'Live Voice: Connect',
            () => _toggleAntigravityLiveVoice(s),
            live ? AppColors.statusCoral : AppColors.textMuted),
      _action(
          auto ? Icons.volume_up_rounded : Icons.volume_off_rounded,
          auto ? 'Auto-read enabled' : 'Auto-read disabled',
          notifier.toggleAutoRead,
          auto ? AppColors.infoCyan : AppColors.textMuted),
      _action(
          audio.micEnabled ? Icons.mic_rounded : Icons.mic_off_rounded,
          audio.micEnabled ? 'Microphone active' : 'Microphone off',
          () => notifier.toggleMic(onResult: _handleRecognizedSpeech),
          audio.micEnabled ? AppColors.statusCoral : AppColors.textMuted),
      _action(
          Icons.sports_motorsports_rounded,
          'Take the Wheel',
          () => _takeTheWheel(s),
          AppColors.statusEmerald,
          const Key('chat_take_wheel_button')),
      _action(
          Icons.article_outlined,
          'View Transcript',
          () => SessionNavigator.openTranscript(context, s,
              showOpenSessionAction: false),
          AppColors.infoCyan,
          const Key('chat_open_transcript_button')),
      _action(
          Icons.refresh_rounded, 'Refresh', _streamController.loadTranscript),
    ];
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<List<Session>>(fleetSessionsProvider, (prev, next) {
      final s = SessionHostResolver.findMatchingSession(next, _activeSession);
      if (s == null) return;
      _streamController.updateSession(s);
      final p = SessionHostResolver.findMatchingSession(prev, _activeSession) ??
          _activeSession;
      if (p.state == SessionState.working && s.state != SessionState.working) {
        _streamController.refreshTranscriptSilently().then((_) {
          if (mounted &&
              ref.read(audioCompanionServiceProvider).autoReadEnabled) {
            _autoReadLatestAssistantMessage();
          }
        });
        _streamController.loadSubagents();
      }
    });

    final sessions = ref.watch(fleetSessionsProvider);
    final currentSession = _resolveLiveSession(sessions);
    _streamController.updateSession(currentSession, notify: false);
    final isWorking = _isSessionWorking;
    final messages = _streamController.transcriptData?.messages ?? [];
    final audio = ref.watch(audioCompanionServiceProvider);
    final pending = _streamController.pendingUserMessage;

    return Scaffold(
      backgroundColor: AppColors.surface,
      appBar: AppBar(
        backgroundColor: AppColors.surface,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_rounded,
              color: AppColors.textPrimary),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(children: [
              const Icon(Icons.chat_bubble_outline_rounded,
                  size: 16, color: AppColors.infoCyan),
              const SizedBox(width: 6),
              Expanded(
                child: Text(currentSession.displayTitle,
                    style: AppTypography.titleMedium.copyWith(
                        color: AppColors.textPrimary,
                        fontWeight: FontWeight.w700),
                    overflow: TextOverflow.ellipsis),
              ),
            ]),
            Text(
              '${currentSession.agentDisplayName} @ ${currentSession.hostTag} • ${messages.length} messages',
              style: AppTypography.codeXs
                  .copyWith(color: AppColors.textSecondary, fontSize: 10),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: _buildAppBarActions(currentSession, audio),
      ),
      body: SafeArea(
        child: Column(
          children: [
            HeadlessConfigPillBar(session: currentSession),
            Expanded(
              child: _buildBodyContent(currentSession, messages, pending),
            ),
            SubagentsDock(runningSubagents: _streamController.runningSubagents),
            if (currentSession.blocked?.kind == BlockKind.auth)
              HeadlessAuthCard(
                session: currentSession,
                surfacePrefix: 'transcript',
                onResolved: _streamController.loadTranscript,
              ),
            if (currentSession.isHeadless)
              HeadlessAutocompleteStrip(
                controller: _promptController,
                session: currentSession,
                resolveHost: _resolveHostRecord,
              ),
            ChatComposerBar(
              session: currentSession,
              isWorking: isWorking,
              promptController: _promptController,
              promptFocusNode: _promptFocusNode,
              onSend: _sendPrompt,
              onCancel: _cancelTurn,
            ),
          ],
        ),
      ),
    );
  }
}
