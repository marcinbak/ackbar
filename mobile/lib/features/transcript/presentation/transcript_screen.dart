import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/headless_models.dart';
import '../../../core/models/host.dart';
import '../application/headless_chat_stream_controller.dart';
import '../../../core/models/session.dart';
import '../../../core/models/subagent.dart';
import '../../../core/models/transcript.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/headless_api_client.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/services/audio_companion_service.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/utils/session_host_resolver.dart';
import '../../chat/presentation/chat_composer_bar.dart';
import '../../headless/presentation/headless_auth_card.dart';
import '../../headless/presentation/headless_config_sheet.dart';
import '../../terminal/presentation/terminal_screen.dart';
import 'headless_autocomplete_strip.dart';
import 'headless_command_cards.dart';
import 'headless_command_dispatcher.dart';
import 'subagents_dock.dart';
import 'transcript_turn_bubble.dart';

/// Fullscreen interactive Chat-Style Transcript Screen displaying user and agent turns
/// with right-aligned user bubbles, left-aligned agent responses, collapsible tool calls,
/// thinking accordions, and jump-to-bottom controls.
class TranscriptScreen extends ConsumerStatefulWidget {
  final Session session;

  const TranscriptScreen({super.key, required this.session});

  static Future<void> open(BuildContext context, Session session) {
    return Navigator.of(context).push(
      MaterialPageRoute(
        builder: (context) => TranscriptScreen(session: session),
      ),
    );
  }

  static Future<void> showModal(
    BuildContext context,
    WidgetRef ref,
    Session session,
  ) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => SizedBox(
        height: MediaQuery.of(context).size.height * 0.90,
        child: ClipRRect(
          borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
          child: TranscriptScreen(session: session),
        ),
      ),
    );
  }

  @override
  ConsumerState<TranscriptScreen> createState() => _TranscriptScreenState();
}

class _TranscriptScreenState extends ConsumerState<TranscriptScreen>
    with WidgetsBindingObserver {
  late Session _activeSession = widget.session;
  final List<HeadlessEphemeralCardEntry> _ephemeralCards = [];

  final ScrollController _scrollController = ScrollController();
  final TextEditingController _promptController = TextEditingController();
  final FocusNode _promptFocusNode = FocusNode();
  bool _showJumpToBottom = false;
  bool _hasPromptText = false;
  late final HeadlessChatStreamController _streamController;

  TranscriptData? get _transcriptData => _streamController.transcriptData;
  bool get _isLoading => _streamController.isLoading;
  bool get _isSending => _streamController.isSending;
  bool get _awaitingTurnCompletion => _streamController.awaitingTurnCompletion;
  String? get _errorMessage => _streamController.errorMessage;
  TranscriptMessage? get _pendingUserMessage =>
      _streamController.pendingUserMessage;
  List<SubagentInfo> get _runningSubagents =>
      _streamController.runningSubagents;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _promptController.addListener(_onPromptChanged);
    _scrollController.addListener(_onScroll);

    final host = _resolveHostRecord(_activeSession.host);
    _streamController = HeadlessChatStreamController(
      session: _activeSession,
      host: host,
      apiClient: ref.read(apiClientProvider),
    );
    _streamController.addListener(_onStreamControllerUpdated);
    _streamController.init();
  }

  void _onStreamControllerUpdated() {
    if (!mounted) return;
    setState(() {});
  }

  void _rotateToSession(Session nextSession, {String? notice}) {
    setState(() {
      _activeSession = nextSession;
      _ephemeralCards.clear();
      _ephemeralCards.add(
        HeadlessEphemeralCardEntry(
          id: 'card_rot_${DateTime.now().millisecondsSinceEpoch}',
          kind: 'status',
          createdAt: DateTime.now(),
          statusTitle: 'Conversation Rotated',
          statusBody: notice ??
              'Rotated to new conversation (${nextSession.displayTitle})',
        ),
      );
    });
    ref.read(fleetSessionsProvider.notifier).upsertSession(nextSession);
    _streamController.rotateToSession(
      nextSession,
      _resolveHostRecord(nextSession.host),
    );
  }

  HostRecord _resolveHostRecord(String hostName) {
    return SessionHostResolver.resolveHostRecord(
      ref.read(hostsListProvider),
      hostName,
    );
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _streamController.removeListener(_onStreamControllerUpdated);
    _streamController.dispose();
    _promptController.removeListener(_onPromptChanged);
    _scrollController.removeListener(_onScroll);
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

  void _onPromptChanged() {
    final hasText = _promptController.text.trim().isNotEmpty;
    if (hasText != _hasPromptText && mounted) {
      setState(() {
        _hasPromptText = hasText;
      });
    }
  }

  Session? _findMatchingSession(List<Session>? sessions) {
    return SessionHostResolver.findMatchingSession(sessions, _activeSession);
  }

  Session _resolveLiveSession([List<Session>? sessions]) {
    final list = sessions ?? ref.read(fleetSessionsProvider);
    return _findMatchingSession(list) ?? _activeSession;
  }

  bool get _isSessionWorking {
    final liveSession = _resolveLiveSession();
    return _isSending ||
        _awaitingTurnCompletion ||
        liveSession.state == SessionState.working;
  }

  void _onScroll() {
    if (!_scrollController.hasClients) return;
    final maxScroll = _scrollController.position.maxScrollExtent;
    final currentScroll = _scrollController.position.pixels;
    final isAwayFromBottom = (maxScroll - currentScroll) > 300;

    if (isAwayFromBottom != _showJumpToBottom) {
      setState(() {
        _showJumpToBottom = isAwayFromBottom;
      });
    }
  }

  Future<({String url, String? authToken})> _resolveHost() async {
    final res = await SessionHostResolver.resolveSessionEndpoint(
      getHosts: () => ref.read(hostsListProvider),
      session: _resolveLiveSession(),
      isMounted: () => mounted,
    );
    return (url: res.url, authToken: res.authToken);
  }

  Future<void> _loadTranscript() => _streamController.loadTranscript();
  Future<void> _refreshTranscriptSilently() =>
      _streamController.refreshTranscriptSilently();
  Future<void> _loadSubagents() => _streamController.loadSubagents();

  void _scrollToBottom({bool animate = true}) {
    if (!_scrollController.hasClients) return;
    final target = _scrollController.position.maxScrollExtent;
    if (animate) {
      _scrollController.animateTo(
        target,
        duration: const Duration(milliseconds: 300),
        curve: Curves.easeOutCubic,
      );
    } else {
      _scrollController.jumpTo(target);
    }
  }

  Future<void> _sendPrompt() async {
    final text = _promptController.text.trim();
    if (text.isEmpty || _isSending) return;

    final currentSession = _resolveLiveSession();
    _streamController.updateSession(currentSession);
    if (currentSession.isHeadless) {
      final hostRec = _resolveHostRecord(currentSession.host);
      final dispatcher = HeadlessCommandDispatcher(
        apiClient: ref.read(apiClientProvider),
        host: hostRec,
        onOpenConfig: () => HeadlessConfigSheet.show(context, currentSession),
        onTakeWheel: _takeWheel,
        onRotateSession: (newSession, {notice}) =>
            _rotateToSession(newSession, notice: notice),
        onSessionUpdated: (updated) =>
            ref.read(fleetSessionsProvider.notifier).upsertSession(updated),
      );

      final result = await dispatcher.dispatch(currentSession, text);
      if (result.intercepted) {
        _promptController.clear();
        if (result.card != null && mounted) {
          setState(() {
            _ephemeralCards.add(result.card!);
          });
          WidgetsBinding.instance
              .addPostFrameCallback((_) => _scrollToBottom());
        }
        return;
      }
    }

    _promptController.clear();
    HapticFeedback.lightImpact();
    WidgetsBinding.instance.addPostFrameCallback((_) => _scrollToBottom());

    final res = await _streamController.submitPrompt(text);
    if (!res.ok && mounted) {
      _promptController.text = text;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Failed to dispatch prompt to agent'),
          backgroundColor: AppColors.statusCoral,
        ),
      );
    }
  }

  Future<void> _takeWheel() async {
    HapticFeedback.mediumImpact();
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('Taking the wheel: spawning live tmux session...'),
        backgroundColor: AppColors.infoCyan,
        duration: Duration(seconds: 2),
      ),
    );

    try {
      final host = await _resolveHost();
      final activeSessionId = _resolveLiveSession().id;
      final res = await ref
          .read(apiClientProvider)
          .takeWheel(host.url, activeSessionId, authToken: host.authToken);
      if (res != null && mounted) {
        final tmuxName = res['tmux_name']?.toString() ?? '';
        final updatedSession = _resolveLiveSession().copyWith(
          tmuxName: tmuxName,
          engineType: 'tmux',
          managed: true,
        );
        TerminalScreen.open(context, updatedSession);
      } else if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text(
              'Failed to take wheel. Ensure no headless turn is in progress.',
            ),
            backgroundColor: AppColors.statusCoral,
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Error taking wheel: $e'),
            backgroundColor: AppColors.statusCoral,
          ),
        );
      }
    }
  }

  Future<void> _cancelTurn() async {
    HapticFeedback.mediumImpact();
    try {
      final host = await _resolveHost();
      final activeSessionId = _resolveLiveSession().id;
      await ref
          .read(apiClientProvider)
          .cancelTurn(host.url, activeSessionId, authToken: host.authToken);
      if (mounted) {
        _streamController.cancelTurnLocally();
        _streamController.refreshTranscriptSilently();
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Turn cancelled'),
            backgroundColor: AppColors.surfaceHighlight,
          ),
        );
      }
    } catch (_) {}
  }

  Future<void> _speakMessage(
    int index,
    String content, {
    bool forceRefresh = false,
    bool forceFull = false,
  }) async {
    final host = await _resolveHost();
    final liveSession = _resolveLiveSession();
    await ref.read(audioCompanionServiceProvider.notifier).speakMessage(
          liveSession,
          index,
          content,
          forceRefresh: forceRefresh,
          forceFull: forceFull,
          hostUrl: host.url,
          authToken: host.authToken,
        );
  }

  void _autoReadLatestAssistantMessage() {
    final msgs = _transcriptData?.messages ?? [];
    for (var i = msgs.length - 1; i >= 0; i--) {
      if (msgs[i].isAssistant && msgs[i].content.trim().isNotEmpty) {
        _speakMessage(i, msgs[i].content);
        break;
      }
    }
  }

  void _handleRecognizedSpeech(String text, bool isFinal) {
    if (!mounted) return;
    final trimmed = text.trim();
    if (trimmed.isEmpty) return;

    if (!isFinal) {
      _promptController.text = trimmed;
      _promptController.selection = TextSelection.fromPosition(
        TextPosition(offset: _promptController.text.length),
      );
      return;
    }

    final normalized =
        trimmed.toLowerCase().replaceAll(RegExp(r'[.,!?;:]+$'), '').trim();
    if (normalized == 'repeat that' ||
        normalized == 'read that again' ||
        normalized == 'repeat' ||
        normalized == 'replay') {
      _promptController.clear();
      _autoReadLatestAssistantMessage();
      return;
    }

    var finalPrompt = trimmed;
    final autoRead = ref.read(audioCompanionServiceProvider).autoReadEnabled;
    if (autoRead && !finalPrompt.contains('[Reply concisely')) {
      finalPrompt += ' [Reply concisely in 2–3 spoken sentences]';
    }
    _promptController.text = finalPrompt;
    _sendPrompt();
  }

  Future<void> _toggleAntigravityLiveVoice(Session currentSession) async {
    HapticFeedback.mediumImpact();
    final host = await _resolveHost();
    if (!mounted) return;

    final notifier = ref.read(audioCompanionServiceProvider.notifier);
    if (!ref.read(audioCompanionServiceProvider).liveVoiceConnected &&
        currentSession.isHeadless) {
      await ref
          .read(apiClientProvider)
          .takeWheel(host.url, currentSession.id, authToken: host.authToken);
    }
    if (!mounted) return;
    await notifier.toggleAntigravityLiveVoice(
      currentSession,
      hostUrl: host.url,
      authToken: host.authToken,
    );
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<List<Session>>(fleetSessionsProvider, (previous, next) {
      final prevSession = _findMatchingSession(previous) ?? _activeSession;
      final nextSession = _findMatchingSession(next);
      if (nextSession == null) return;

      _streamController.updateSession(nextSession);

      if (prevSession.state == SessionState.working &&
          nextSession.state != SessionState.working) {
        _refreshTranscriptSilently().then((_) {
          if (mounted &&
              ref.read(audioCompanionServiceProvider).autoReadEnabled) {
            _autoReadLatestAssistantMessage();
          }
        });
        _loadSubagents();
      }
    });

    final liveSession = ref.watch(
      fleetSessionsProvider.select(_findMatchingSession),
    );
    final currentSession = liveSession ?? _activeSession;
    if (currentSession.id != _streamController.session.id) {
      _streamController.updateSession(currentSession, notify: false);
    }
    final isWorking = _isSending ||
        _awaitingTurnCompletion ||
        currentSession.state == SessionState.working;
    final messages = _transcriptData?.messages ?? [];
    final audioCompanion = ref.watch(audioCompanionServiceProvider);

    return Scaffold(
      backgroundColor: AppColors.terminalBlack,
      appBar: AppBar(
        backgroundColor: AppColors.surface,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(
            Icons.arrow_back_ios_new_rounded,
            size: 18,
            color: AppColors.textPrimary,
          ),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(
                  Icons.forum_outlined,
                  size: 16,
                  color: AppColors.infoCyan,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    currentSession.displayTitle,
                    style: AppTypography.codeSm.copyWith(
                      color: AppColors.textPrimary,
                      fontWeight: FontWeight.w700,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            Text(
              '${currentSession.agentDisplayName} @ ${currentSession.hostTag} • ${messages.length} messages',
              style: AppTypography.codeXs.copyWith(
                color: AppColors.textSecondary,
              ),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: [
          // Antigravity Native Gemini Live Voice Toggle (JETSKI-AUDIO/1 Bridge)
          if (currentSession.agent.toLowerCase() == 'antigravity')
            IconButton(
              icon: Icon(
                Icons.record_voice_over_rounded,
                size: 20,
                color: audioCompanion.liveVoiceConnected
                    ? AppColors.statusCoral
                    : AppColors.textMuted,
              ),
              tooltip: audioCompanion.liveVoiceConnected
                  ? 'Live Voice: On'
                  : 'Live Voice: Off',
              onPressed: () => _toggleAntigravityLiveVoice(currentSession),
            ),

          // Auto-Read Audio Companion Toggle
          IconButton(
            icon: Icon(
              audioCompanion.autoReadEnabled
                  ? Icons.volume_up_rounded
                  : Icons.volume_off_rounded,
              size: 20,
              color: audioCompanion.autoReadEnabled
                  ? AppColors.infoCyan
                  : AppColors.textMuted,
            ),
            tooltip: audioCompanion.autoReadEnabled
                ? 'Auto-Read: On'
                : 'Auto-Read: Off',
            onPressed: () {
              ref.read(audioCompanionServiceProvider.notifier).toggleAutoRead();
            },
          ),

          // Voice / Mic Companion Toggle
          IconButton(
            icon: Icon(
              audioCompanion.micEnabled
                  ? Icons.mic_rounded
                  : Icons.mic_off_rounded,
              size: 20,
              color: audioCompanion.micEnabled
                  ? AppColors.statusCoral
                  : AppColors.textMuted,
            ),
            tooltip: audioCompanion.micEnabled ? 'Mic Active' : 'Mic Off',
            onPressed: () {
              ref
                  .read(audioCompanionServiceProvider.notifier)
                  .toggleMic(onResult: _handleRecognizedSpeech);
            },
          ),

          // Take the Wheel in Terminal (Headless sessions)
          if (currentSession.isHeadless)
            IconButton(
              icon: const Icon(
                Icons.sports_motorsports_rounded,
                size: 20,
                color: AppColors.infoCyan,
              ),
              tooltip: 'Take the Wheel (Interactive Terminal)',
              onPressed: _takeWheel,
            ),

          // Live Terminal Quick Jump
          IconButton(
            icon: const Icon(
              Icons.terminal_rounded,
              size: 20,
              color: AppColors.statusEmerald,
            ),
            tooltip: 'Open Live Terminal',
            onPressed: () => TerminalScreen.open(context, currentSession),
          ),

          // Copy Full Transcript
          if (_transcriptData != null &&
              _transcriptData!.rawMarkdown.isNotEmpty)
            IconButton(
              icon: const Icon(
                Icons.copy_all_rounded,
                size: 20,
                color: AppColors.textMuted,
              ),
              tooltip: 'Copy Full Transcript',
              onPressed: () {
                Clipboard.setData(
                  ClipboardData(text: _transcriptData!.rawMarkdown),
                );
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text('Full transcript copied to clipboard'),
                    backgroundColor: AppColors.surfaceHighlight,
                  ),
                );
              },
            ),

          // Refresh
          IconButton(
            icon: const Icon(
              Icons.refresh_rounded,
              size: 20,
              color: AppColors.textMuted,
            ),
            tooltip: 'Reload Conversation',
            onPressed: () {
              _loadTranscript();
              _loadSubagents();
            },
          ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            if (currentSession.isHeadless)
              HeadlessConfigPillBar(session: currentSession),
            Expanded(
              child: Stack(
                children: [
                  if (_isLoading)
                    Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const CircularProgressIndicator(
                            color: AppColors.infoCyan,
                          ),
                          AppSpacing.gapH12,
                          Text(
                            'Loading conversation stream...',
                            style: AppTypography.bodySmall.copyWith(
                              color: AppColors.textSecondary,
                            ),
                          ),
                        ],
                      ),
                    )
                  else if (_errorMessage != null)
                    Center(
                      child: Padding(
                        padding: AppSpacing.paddingScreen,
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            const Icon(
                              Icons.error_outline_rounded,
                              size: 36,
                              color: AppColors.statusCoral,
                            ),
                            AppSpacing.gapH12,
                            Text(
                              _errorMessage!,
                              textAlign: TextAlign.center,
                              style: AppTypography.bodyMedium.copyWith(
                                color: AppColors.statusCoral,
                              ),
                            ),
                            AppSpacing.gapH16,
                            ElevatedButton.icon(
                              onPressed: () {
                                _loadTranscript();
                                _loadSubagents();
                              },
                              icon: const Icon(Icons.refresh_rounded, size: 16),
                              label: const Text('Retry'),
                            ),
                          ],
                        ),
                      ),
                    )
                  else if (messages.isEmpty && _ephemeralCards.isEmpty)
                    Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(
                            Icons.chat_bubble_outline_rounded,
                            size: 40,
                            color: AppColors.textMuted,
                          ),
                          AppSpacing.gapH12,
                          Text(
                            'No conversation messages recorded yet',
                            style: AppTypography.bodyMedium.copyWith(
                              color: AppColors.textMuted,
                            ),
                          ),
                        ],
                      ),
                    )
                  else
                    ListView.builder(
                      controller: _scrollController,
                      padding: const EdgeInsets.fromLTRB(
                        AppSpacing.md,
                        AppSpacing.md,
                        AppSpacing.md,
                        20,
                      ),
                      physics: const BouncingScrollPhysics(),
                      itemCount: messages.length + _ephemeralCards.length,
                      itemBuilder: (context, index) {
                        if (index < messages.length) {
                          final message = messages[index];
                          return _buildMessageTurn(message, index);
                        }
                        final cardIndex = index - messages.length;
                        final cardEntry = _ephemeralCards[cardIndex];
                        return HeadlessCommandCardView(
                          entry: cardEntry,
                          session: _resolveLiveSession(),
                          resolveHost: _resolveHost,
                          onDismiss: () {
                            setState(() {
                              _ephemeralCards.removeWhere(
                                (c) => c.id == cardEntry.id,
                              );
                            });
                          },
                          onAttachToPrompt: (fenced) {
                            _promptController.text =
                                '${_promptController.text}$fenced';
                            _promptFocusNode.requestFocus();
                          },
                          onRewindToTurn: (turn) async {
                            final host = await _resolveHost();
                            final api = ref.read(apiClientProvider);
                            try {
                              final res = await api.rewindSession(
                                host.url,
                                _resolveLiveSession().id,
                                turn,
                                authToken: host.authToken,
                              );
                              if (!mounted) return;
                              if (res.session != null) {
                                _rotateToSession(
                                  res.session!,
                                  notice: 'Rewound to Turn $turn',
                                );
                              }
                            } catch (e) {
                              if (!mounted) return;
                              final msg = e is ApiHttpException
                                  ? e.message
                                  : e.toString();
                              ScaffoldMessenger.of(context).showSnackBar(
                                SnackBar(
                                  content: Text('Rewind failed: $msg'),
                                  backgroundColor: AppColors.statusCoral,
                                ),
                              );
                            }
                          },
                        );
                      },
                    ),

                  // Floating "Jump to Latest" Button
                  if (_showJumpToBottom)
                    Positioned(
                      bottom: 12,
                      right: 16,
                      child: FloatingActionButton.extended(
                        onPressed: () {
                          HapticFeedback.selectionClick();
                          _scrollToBottom();
                        },
                        backgroundColor: AppColors.surfaceHighlight,
                        foregroundColor: AppColors.infoCyan,
                        icon: const Icon(
                          Icons.arrow_downward_rounded,
                          size: 16,
                        ),
                        label: Text(
                          'Latest',
                          style: AppTypography.codeXs.copyWith(
                            fontWeight: FontWeight.w700,
                            color: AppColors.infoCyan,
                          ),
                        ),
                      ),
                    ),
                ],
              ),
            ),
            _buildSubagentsDock(),
            if (currentSession.blocked?.kind == BlockKind.auth)
              HeadlessAuthCard(
                session: currentSession,
                surfacePrefix: 'transcript',
                onResolved: () => _loadTranscript(),
              ),
            if (currentSession.isHeadless)
              HeadlessAutocompleteStrip(
                controller: _promptController,
                session: currentSession,
                resolveHost: _resolveHost,
              ),
            _buildBottomComposer(currentSession, isWorking),
          ],
        ),
      ),
    );
  }

  Widget _buildBottomComposer(Session currentSession, bool isWorking) {
    return ChatComposerBar(
      session: currentSession,
      isWorking: isWorking,
      promptController: _promptController,
      promptFocusNode: _promptFocusNode,
      onSend: _sendPrompt,
      onCancel: _cancelTurn,
    );
  }

  Widget _buildMessageTurn(TranscriptMessage message, int index) {
    return TranscriptTurnBubble(
      message: message,
      index: index,
      session: _resolveLiveSession(),
      onSpeak: _speakMessage,
    );
  }

  Widget _buildSubagentsDock() {
    return SubagentsDock(runningSubagents: _runningSubagents);
  }
}
