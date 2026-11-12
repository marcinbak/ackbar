import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

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
import 'transcript_tool_widgets.dart';
import 'transcript_turn_bubble.dart';

class TranscriptScreen extends ConsumerStatefulWidget {
  final Session session;
  final bool showOpenSessionAction;

  const TranscriptScreen({
    super.key,
    required this.session,
    this.showOpenSessionAction = true,
  });

  static Future<void> open(
    BuildContext context,
    Session session, {
    bool showOpenSessionAction = true,
  }) {
    return Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => TranscriptScreen(
          session: session,
          showOpenSessionAction: showOpenSessionAction,
        ),
      ),
    );
  }

  @override
  ConsumerState<TranscriptScreen> createState() => _TranscriptScreenState();
}

class _TranscriptScreenState extends ConsumerState<TranscriptScreen>
    with WidgetsBindingObserver {
  late final Session _activeSession = widget.session;
  TranscriptData? _transcriptData;
  bool _isLoading = false;
  String? _errorMessage;
  bool _isSearchOpen = false;
  bool _showJumpToBottom = false;

  final TextEditingController _searchController = TextEditingController();
  final ScrollController _scrollController = ScrollController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _scrollController.addListener(_onScroll);
    _searchController.addListener(_onSearchChanged);
    _loadTranscript();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _scrollController.dispose();
    _searchController.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    super.didChangeAppLifecycleState(state);
    if (state == AppLifecycleState.resumed && mounted) {
      _loadTranscript();
    }
  }

  Session _resolveLiveSession([List<Session>? sessions]) =>
      SessionHostResolver.resolveLiveSession(
          sessions ?? ref.read(fleetSessionsProvider), _activeSession);

  void _onScroll() {
    if (!_scrollController.hasClients) return;
    final pos = _scrollController.position;
    final away = (pos.maxScrollExtent - pos.pixels) > 300;
    if (away != _showJumpToBottom) setState(() => _showJumpToBottom = away);
  }

  void _onSearchChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _loadTranscript() async {
    if (!mounted) return;
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    final current = _resolveLiveSession();
    final host = await SessionHostResolver.resolveSessionEndpoint(
      getHosts: () => ref.read(hostsListProvider),
      session: current,
      isMounted: () => mounted,
    );

    try {
      final structured = await ref
          .read(apiClientProvider)
          .getStructuredTranscript(host.url, current.id,
              authToken: host.authToken);
      if (structured != null) {
        if (mounted) {
          setState(() {
            _transcriptData = structured;
            _isLoading = false;
          });
        }
        return;
      }
      final raw = await ref
          .read(apiClientProvider)
          .getTranscriptOrNull(host.url, current.id, authToken: host.authToken);
      if (structured == null && raw == null) {
        throw Exception('Unable to load transcript');
      }
      if (mounted) {
        setState(() {
          _transcriptData = TranscriptData.fromRawMarkdown(
            sessionId: current.id,
            agent: current.agent,
            title: current.displayTitle,
            markdown: raw ?? '',
          );
          _isLoading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _errorMessage = e.toString();
          _isLoading = false;
        });
      }
    }
  }

  void _scrollToBottom({bool animate = true}) {
    if (!_scrollController.hasClients) return;
    final target = _scrollController.position.maxScrollExtent;
    animate
        ? _scrollController.animateTo(target,
            duration: const Duration(milliseconds: 250), curve: Curves.easeOut)
        : _scrollController.jumpTo(target);
  }

  Future<void> _copyFullTranscript() async {
    final raw = _transcriptData?.rawMarkdown;
    final text = (raw != null && raw.trim().isNotEmpty)
        ? raw
        : (_transcriptData?.messages ?? [])
            .map((m) => '### ${m.role.toUpperCase()}\n${m.content}')
            .join('\n\n');
    try {
      await Clipboard.setData(ClipboardData(text: text));
    } catch (_) {}
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Transcript copied to clipboard')),
      );
    }
  }

  Future<void> _speakMessage(int index, String content,
      {bool forceRefresh = false, bool forceFull = false}) async {
    final current = _resolveLiveSession();
    final host = await SessionHostResolver.resolveSessionEndpoint(
      getHosts: () => ref.read(hostsListProvider),
      session: current,
      isMounted: () => mounted,
    );
    await ref.read(audioCompanionServiceProvider.notifier).speakMessage(
        current, index, content,
        forceRefresh: forceRefresh,
        forceFull: forceFull,
        hostUrl: host.url,
        authToken: host.authToken);
  }

  void _autoReadLatestAssistantMessage() {
    final msgs = _transcriptData?.messages ?? [];
    final last = msgs
        .lastIndexWhere((m) => m.isAssistant && m.content.trim().isNotEmpty);
    if (last != -1) _speakMessage(last, msgs[last].content);
  }

  List<({TranscriptMessage message, int originalIndex})>
      _getFilteredMessages() {
    final msgs = _transcriptData?.messages ?? [];
    final q = _searchController.text.trim().toLowerCase();
    if (!_isSearchOpen || q.isEmpty) {
      return [
        for (var i = 0; i < msgs.length; i++)
          (message: msgs[i], originalIndex: i)
      ];
    }
    final result = <({TranscriptMessage message, int originalIndex})>[];
    for (var i = 0; i < msgs.length; i++) {
      final m = msgs[i];
      final matchContent = m.content.toLowerCase().contains(q);
      final matchThinking = (m.thinking ?? '').toLowerCase().contains(q);
      final matchTools = m.toolCalls.any((tc) {
        final p = ParsedToolCall.parse(tc);
        return p.name.toLowerCase().contains(q) ||
            p.detail.toLowerCase().contains(q);
      });
      if (matchContent || matchThinking || matchTools) {
        result.add((message: m, originalIndex: i));
      }
    }
    return result;
  }

  IconButton _action(IconData icon, String tip, VoidCallback press,
          [Color? color, Key? key]) =>
      IconButton(
          key: key,
          icon: Icon(icon, size: 20, color: color ?? AppColors.textPrimary),
          tooltip: tip,
          onPressed: press);

  List<Widget> _buildAppBarActions(
      Session s, AudioCompanionState audio, bool showSessionAction) {
    final notifier = ref.read(audioCompanionServiceProvider.notifier);
    final auto = audio.autoReadEnabled;
    return [
      _action(
        _isSearchOpen ? Icons.search_off_rounded : Icons.search_rounded,
        'Search Transcript',
        () {
          setState(() {
            _isSearchOpen = !_isSearchOpen;
            if (!_isSearchOpen) _searchController.clear();
          });
        },
        _isSearchOpen ? AppColors.infoCyan : AppColors.textPrimary,
        const Key('transcript_search_toggle'),
      ),
      _action(
        auto ? Icons.volume_up_rounded : Icons.volume_off_rounded,
        auto ? 'Auto-read enabled' : 'Auto-read disabled',
        notifier.toggleAutoRead,
        auto ? AppColors.infoCyan : AppColors.textMuted,
        const Key('transcript_tts_toggle'),
      ),
      _action(Icons.copy_all_rounded, 'Copy Full Transcript',
          _copyFullTranscript, null, const Key('transcript_copy_button')),
      if (showSessionAction)
        _action(
          s.isHeadless
              ? Icons.chat_bubble_outline_rounded
              : Icons.terminal_rounded,
          s.isHeadless ? 'Open Chat' : 'Open Terminal',
          () => SessionNavigator.openSession(context, ref, s),
          AppColors.infoCyan,
          const Key('transcript_open_session_button'),
        ),
      _action(Icons.refresh_rounded, 'Refresh', _loadTranscript, null,
          const Key('transcript_refresh_button')),
    ];
  }

  Widget _buildSearchBar(int matchCount) {
    return Container(
      padding:
          const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 8),
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(
            bottom: BorderSide(color: AppColors.outlineSubtle, width: 1)),
      ),
      child: Row(
        children: [
          const Icon(Icons.search_rounded,
              size: 18, color: AppColors.textSecondary),
          const SizedBox(width: 8),
          Expanded(
            child: TextField(
              key: const Key('transcript_search_input'),
              controller: _searchController,
              autofocus: true,
              style: AppTypography.bodySmall
                  .copyWith(color: AppColors.textPrimary),
              decoration: InputDecoration(
                isDense: true,
                border: InputBorder.none,
                hintText: 'Filter turns, thoughts, or tool calls...',
                hintStyle: AppTypography.bodySmall
                    .copyWith(color: AppColors.textMuted),
              ),
            ),
          ),
          Text(
            '$matchCount ${matchCount == 1 ? 'match' : 'matches'}',
            key: const Key('transcript_search_count'),
            style: AppTypography.codeXs.copyWith(color: AppColors.infoCyan),
          ),
          if (_searchController.text.isNotEmpty)
            IconButton(
              key: const Key('transcript_search_clear'),
              icon: const Icon(Icons.clear_rounded,
                  size: 18, color: AppColors.textMuted),
              onPressed: () => _searchController.clear(),
            ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<List<Session>>(fleetSessionsProvider, (prev, next) {
      final s = SessionHostResolver.findMatchingSession(next, _activeSession);
      if (s == null) return;
      final p = SessionHostResolver.findMatchingSession(prev, _activeSession) ??
          _activeSession;
      if (p.state == SessionState.working && s.state != SessionState.working) {
        _loadTranscript().then((_) {
          if (mounted &&
              ref.read(audioCompanionServiceProvider).autoReadEnabled) {
            _autoReadLatestAssistantMessage();
          }
        });
      }
    });

    final currentSession = _resolveLiveSession();
    final allMessages = _transcriptData?.messages ?? [];
    final filtered = _getFilteredMessages();
    final audio = ref.watch(audioCompanionServiceProvider);
    final canOpenSession = widget.showOpenSessionAction &&
        (currentSession.isHeadless ||
            (!currentSession.isEnded &&
                currentSession.tmuxName != '(deleted)'));

    Widget body;
    if (_isLoading && allMessages.isEmpty) {
      body = const Center(
          child: CircularProgressIndicator(color: AppColors.infoCyan));
    } else if (_errorMessage != null && allMessages.isEmpty) {
      body = Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_errorMessage!,
                textAlign: TextAlign.center,
                style: AppTypography.bodySmall
                    .copyWith(color: AppColors.statusCoral)),
            AppSpacing.gapH12,
            ElevatedButton(
                onPressed: _loadTranscript, child: const Text('Retry')),
          ],
        ),
      );
    } else if (allMessages.isEmpty) {
      body = Center(
        child: Text('No transcript available yet',
            style:
                AppTypography.bodySmall.copyWith(color: AppColors.textMuted)),
      );
    } else if (_isSearchOpen && filtered.isEmpty) {
      body = Center(
        child: Text('No matching turns found',
            style:
                AppTypography.bodySmall.copyWith(color: AppColors.textMuted)),
      );
    } else {
      body = ListView.builder(
        controller: _scrollController,
        padding:
            const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 12),
        itemCount: filtered.length,
        itemBuilder: (context, idx) {
          final item = filtered[idx];
          return TranscriptTurnBubble(
            message: item.message,
            index: item.originalIndex,
            session: currentSession,
            onSpeak: _speakMessage,
          );
        },
      );
    }

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
              const Icon(Icons.article_outlined,
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
              'TRANSCRIPT • ${currentSession.agentDisplayName} @ ${currentSession.hostTag} • ${allMessages.length} messages',
              style: AppTypography.codeXs
                  .copyWith(color: AppColors.textSecondary, fontSize: 10),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: _buildAppBarActions(currentSession, audio, canOpenSession),
      ),
      body: SafeArea(
        child: Column(
          children: [
            if (_isSearchOpen) _buildSearchBar(filtered.length),
            Expanded(
              child: Stack(
                children: [
                  body,
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
              ),
            ),
          ],
        ),
      ),
    );
  }
}
