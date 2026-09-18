import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/models/subagent.dart';
import '../../../core/models/transcript.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';
import '../../terminal/presentation/terminal_screen.dart';

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

  static Future<void> showModal(BuildContext context, WidgetRef ref, Session session) {
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

class _TranscriptScreenState extends ConsumerState<TranscriptScreen> {
  final ScrollController _scrollController = ScrollController();
  final TextEditingController _promptController = TextEditingController();
  final FocusNode _promptFocusNode = FocusNode();
  bool _showJumpToBottom = false;
  bool _isLoading = true;
  bool _isSending = false;
  String? _errorMessage;
  TranscriptData? _transcriptData;
  List<SubagentInfo> _runningSubagents = [];
  bool _isSubagentsExpanded = false;
  Timer? _subagentPollingTimer;

  @override
  void initState() {
    super.initState();
    _loadTranscript();
    _loadSubagents();
    _scrollController.addListener(_onScroll);
    _subagentPollingTimer = Timer.periodic(const Duration(seconds: 3), (_) {
      if (mounted) {
        _loadSubagents();
      }
    });
  }

  @override
  void dispose() {
    _subagentPollingTimer?.cancel();
    _scrollController.removeListener(_onScroll);
    _scrollController.dispose();
    _promptController.dispose();
    _promptFocusNode.dispose();
    super.dispose();
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

  Future<String> _resolveHostUrl() async {
    var hosts = ref.read(hostsListProvider);
    if (hosts.isEmpty) {
      for (var i = 0; i < 6 && hosts.isEmpty; i++) {
        await Future.delayed(const Duration(milliseconds: 80));
        if (!mounted) return 'http://127.0.0.1:7777';
        hosts = ref.read(hostsListProvider);
      }
    }

    // 1. Direct name or URL match
    final match = hosts.where((h) => h.name == widget.session.host || h.url.contains(widget.session.host));
    if (match.isNotEmpty) return match.first.url;

    // 2. Any online host
    final online = hosts.where((h) => h.online);
    if (online.isNotEmpty) return online.first.url;

    // 3. Any configured host
    if (hosts.isNotEmpty) return hosts.first.url;

    return 'http://127.0.0.1:7777';
  }

  Future<void> _loadTranscript() async {
    if (!mounted) return;
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    try {
      final hostUrl = await _resolveHostUrl();
      final structured = await ref.read(apiClientProvider).getStructuredTranscript(hostUrl, widget.session.id);
      
      if (structured != null && structured.messages.isNotEmpty) {
        if (mounted) {
          setState(() {
            _transcriptData = structured;
            _isLoading = false;
          });
        }
      } else {
        // Fallback to markdown parser
        final rawMd = await ref.read(apiClientProvider).getTranscript(hostUrl, widget.session.id);
        if (rawMd.trim().isNotEmpty) {
          final parsed = TranscriptData.fromRawMarkdown(
            sessionId: widget.session.id,
            agent: widget.session.agent,
            title: widget.session.displayTitle,
            markdown: rawMd,
          );
          if (mounted) {
            setState(() {
              _transcriptData = parsed;
              _isLoading = false;
            });
          }
        } else {
          if (mounted) {
            setState(() {
              _transcriptData = structured ?? TranscriptData(
                sessionId: widget.session.id,
                nativeId: widget.session.nativeId,
                agent: widget.session.agent,
                title: widget.session.displayTitle,
                cwd: widget.session.cwd,
                messages: const [],
                rawMarkdown: '',
              );
              _isLoading = false;
            });
          }
        }
      }

      WidgetsBinding.instance.addPostFrameCallback((_) {
        _scrollToBottom(animate: false);
      });
      _loadSubagents();
    } catch (e) {
      if (mounted) {
        setState(() {
          _errorMessage = 'Failed to load transcript: $e';
          _isLoading = false;
        });
      }
    }
  }

  Future<void> _loadSubagents() async {
    try {
      final hostUrl = await _resolveHostUrl();
      final list = await ref.read(apiClientProvider).getSubagents(hostUrl, widget.session.id);
      if (mounted) {
        final running = list.where((s) => s.isRunning).toList();
        if (!_areSubagentListsEqual(running, _runningSubagents)) {
          setState(() {
            _runningSubagents = running;
          });
        }
      }
    } catch (_) {}
  }

  bool _areSubagentListsEqual(List<SubagentInfo> a, List<SubagentInfo> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i].id != b[i].id || a[i].state != b[i].state || a[i].name != b[i].name || a[i].prompt != b[i].prompt) {
        return false;
      }
    }
    return true;
  }

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

    _promptController.clear();
    HapticFeedback.lightImpact();

    final userMsg = TranscriptMessage(
      role: 'user',
      content: text,
      timestamp: DateTime.now(),
    );

    setState(() {
      _isSending = true;
      if (_transcriptData != null) {
        _transcriptData = _transcriptData!.copyWith(
          messages: [..._transcriptData!.messages, userMsg],
        );
      }
    });

    WidgetsBinding.instance.addPostFrameCallback((_) {
      _scrollToBottom();
    });

    try {
      final hostUrl = await _resolveHostUrl();
      final success = await ref.read(apiClientProvider).sendPrompt(hostUrl, widget.session.id, text);
      if (!success && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Failed to dispatch prompt to agent'),
            backgroundColor: AppColors.statusCoral,
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Error sending prompt: $e'),
            backgroundColor: AppColors.statusCoral,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          _isSending = false;
        });
      }
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
      final hostUrl = await _resolveHostUrl();
      final res = await ref.read(apiClientProvider).takeWheel(hostUrl, widget.session.id);
      if (res != null && mounted) {
        final tmuxName = res['tmux_name']?.toString() ?? '';
        final updatedSession = widget.session.copyWith(
          tmuxName: tmuxName,
          engineType: 'tmux',
          managed: true,
        );
        TerminalScreen.open(context, updatedSession);
      } else if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Failed to take wheel. Ensure no headless turn is in progress.'),
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
      final hostUrl = await _resolveHostUrl();
      await ref.read(apiClientProvider).cancelTurn(hostUrl, widget.session.id);
      if (mounted) {
        setState(() {
          _isSending = false;
        });
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Turn cancelled'),
            backgroundColor: AppColors.surfaceHighlight,
          ),
        );
      }
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) {
    final messages = _transcriptData?.messages ?? [];

    return Scaffold(
      backgroundColor: AppColors.terminalBlack,
      appBar: AppBar(
        backgroundColor: AppColors.surface,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded, size: 18, color: AppColors.textPrimary),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.forum_outlined, size: 16, color: AppColors.infoCyan),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    widget.session.displayTitle,
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
              '${widget.session.agentDisplayName} @ ${widget.session.hostTag} • ${messages.length} messages',
              style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: [
          // Take the Wheel in Terminal (Headless sessions)
          if (widget.session.isHeadless)
            IconButton(
              icon: const Icon(Icons.sports_motorsports_rounded, size: 20, color: AppColors.infoCyan),
              tooltip: 'Take the Wheel (Interactive Terminal)',
              onPressed: _takeWheel,
            ),

          // Live Terminal Quick Jump
          IconButton(
            icon: const Icon(Icons.terminal_rounded, size: 20, color: AppColors.statusEmerald),
            tooltip: 'Open Live Terminal',
            onPressed: () => TerminalScreen.open(context, widget.session),
          ),

          // Copy Full Transcript
          if (_transcriptData != null && _transcriptData!.rawMarkdown.isNotEmpty)
            IconButton(
              icon: const Icon(Icons.copy_all_rounded, size: 20, color: AppColors.textMuted),
              tooltip: 'Copy Full Transcript',
              onPressed: () {
                Clipboard.setData(ClipboardData(text: _transcriptData!.rawMarkdown));
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
            icon: const Icon(Icons.refresh_rounded, size: 20, color: AppColors.textMuted),
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
            Expanded(
              child: Stack(
                children: [
                  if (_isLoading)
                    Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const CircularProgressIndicator(color: AppColors.infoCyan),
                          AppSpacing.gapH12,
                          Text(
                            'Loading conversation stream...',
                            style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
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
                            const Icon(Icons.error_outline_rounded, size: 36, color: AppColors.statusCoral),
                            AppSpacing.gapH12,
                            Text(
                              _errorMessage!,
                              textAlign: TextAlign.center,
                              style: AppTypography.bodyMedium.copyWith(color: AppColors.statusCoral),
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
                  else if (messages.isEmpty)
                    Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.chat_bubble_outline_rounded, size: 40, color: AppColors.textMuted),
                          AppSpacing.gapH12,
                          Text(
                            'No conversation messages recorded yet',
                            style: AppTypography.bodyMedium.copyWith(color: AppColors.textMuted),
                          ),
                        ],
                      ),
                    )
                  else
                    ListView.builder(
                      controller: _scrollController,
                      padding: const EdgeInsets.fromLTRB(AppSpacing.md, AppSpacing.md, AppSpacing.md, 20),
                      physics: const BouncingScrollPhysics(),
                      itemCount: messages.length,
                      itemBuilder: (context, index) {
                        final message = messages[index];
                        return _buildMessageTurn(message, index);
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
                        icon: const Icon(Icons.arrow_downward_rounded, size: 16),
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
            _buildBottomComposer(),
          ],
        ),
      ),
    );
  }

  Widget _buildBottomComposer() {
    return Container(
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(
          top: BorderSide(color: AppColors.outlineSubtle, width: 1),
        ),
      ),
      padding: const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: Container(
                  decoration: BoxDecoration(
                    color: AppColors.terminalBlack,
                    borderRadius: BorderRadius.circular(10),
                    border: Border.all(
                      color: _isSending ? AppColors.infoCyan : AppColors.outlineSubtle,
                      width: 1,
                    ),
                  ),
                  padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                  child: TextField(
                    controller: _promptController,
                    focusNode: _promptFocusNode,
                    minLines: 1,
                    maxLines: 5,
                    style: AppTypography.bodySmall.copyWith(color: AppColors.textPrimary),
                    textInputAction: TextInputAction.send,
                    onSubmitted: (_) => _sendPrompt(),
                    decoration: InputDecoration(
                      isDense: true,
                      contentPadding: EdgeInsets.zero,
                      border: InputBorder.none,
                      hintText: 'Ask ${widget.session.agentDisplayName}...',
                      hintStyle: AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              if (_isSending)
                IconButton(
                  icon: const Icon(Icons.stop_circle_rounded, size: 28, color: AppColors.statusCoral),
                  tooltip: 'Cancel turn',
                  onPressed: _cancelTurn,
                )
              else
                IconButton(
                  icon: const Icon(Icons.arrow_upward_rounded, size: 22, color: AppColors.infoCyan),
                  style: IconButton.styleFrom(
                    backgroundColor: AppColors.surfaceHighlight,
                    shape: const CircleBorder(),
                  ),
                  tooltip: 'Send prompt',
                  onPressed: _sendPrompt,
                ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.only(top: 4, left: 2, right: 2),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  widget.session.isHeadless
                      ? '💬 Headless turn-by-turn • OAuth Flat-rate'
                      : '🖥️ Tmux process • Interactive shell',
                  style: AppTypography.codeXs.copyWith(color: AppColors.textMuted, fontSize: 10),
                ),
                if (_isSending)
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const SizedBox(
                        width: 10,
                        height: 10,
                        child: CircularProgressIndicator(strokeWidth: 1.5, color: AppColors.infoCyan),
                      ),
                      const SizedBox(width: 4),
                      Text(
                        'Working...',
                        style: AppTypography.codeXs.copyWith(color: AppColors.infoCyan, fontSize: 10),
                      ),
                    ],
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildMessageTurn(TranscriptMessage message, int index) {
    if (message.isSystem) {
      return _buildSystemEventBubble(message);
    } else if (message.isUser) {
      return _buildUserMessageBubble(message);
    } else {
      return _buildAssistantMessageBubble(message);
    }
  }

  // --- 1. Right-Aligned User Message Bubble ---

  Widget _buildUserMessageBubble(TranscriptMessage message) {
    final timeStr = _formatTimestamp(message.timestamp);

    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        margin: const EdgeInsets.only(bottom: 14, left: 40),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: const Color(0xFF1E2638), // Elevated Slate/Indigo user bubble
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(16),
            bottomRight: Radius.circular(3),
          ),
          border: Border.all(color: const Color(0xFF2C374E), width: 1),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withOpacity(0.2),
              blurRadius: 4,
              offset: const Offset(0, 2),
            ),
          ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          mainAxisSize: MainAxisSize.min,
          children: [
            // User Header Row
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  timeStr,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textMuted,
                    fontSize: 10,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  '👤 You',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w700,
                    fontSize: 11,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 6),

            // User Prompt Text
            SelectableText(
              message.content,
              style: AppTypography.bodyMedium.copyWith(
                color: AppColors.textPrimary,
                height: 1.35,
                fontSize: 13.5,
              ),
            ),
          ],
        ),
      ),
    );
  }

  // --- 2. Left-Aligned Agent Response Card ---

  Widget _buildAssistantMessageBubble(TranscriptMessage message) {
    final timeStr = _formatTimestamp(message.timestamp);

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.only(bottom: 16, right: 16),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.surface, // Dark terminal surface
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(3),
            bottomRight: Radius.circular(16),
          ),
          border: Border.all(color: AppColors.outlineSubtle, width: 0.8),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            // Agent Header Row
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(
                    color: widget.session.agentColor.withOpacity(0.15),
                    borderRadius: AppSpacing.roundedSm,
                    border: Border.all(color: widget.session.agentColor.withOpacity(0.4), width: 0.8),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      AgentLogo(agent: widget.session.agent, size: 11, color: widget.session.agentColor),
                      const SizedBox(width: 4),
                      Text(
                        widget.session.agentDisplayName,
                        style: AppTypography.codeXs.copyWith(
                          color: widget.session.agentColor,
                          fontWeight: FontWeight.w700,
                          fontSize: 11,
                        ),
                      ),
                    ],
                  ),
                ),
                const Spacer(),
                Text(
                  timeStr,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textMuted,
                    fontSize: 10,
                  ),
                ),
                const SizedBox(width: 4),
                IconButton(
                  icon: const Icon(Icons.copy_rounded, size: 14, color: AppColors.textMuted),
                  padding: EdgeInsets.zero,
                  constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
                  tooltip: 'Copy message',
                  onPressed: () {
                    Clipboard.setData(ClipboardData(text: message.content));
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text('Message copied to clipboard'),
                        backgroundColor: AppColors.surfaceHighlight,
                      ),
                    );
                  },
                ),
              ],
            ),

            // Tool Invocations Group Accordion
            if (message.toolCalls.isNotEmpty) ...[
              const SizedBox(height: 8),
              _buildToolCallsGroup(message.toolCalls),
            ],

            // Thought Process Accordion
            if (message.thinking != null && message.thinking!.trim().isNotEmpty) ...[
              const SizedBox(height: 8),
              _buildThinkingAccordion(message.thinking!),
            ],

            // Main Markdown Response Body
            if (message.content.trim().isNotEmpty) ...[
              const SizedBox(height: 10),
              MarkdownBody(
                data: message.content,
                selectable: true,
                styleSheet: MarkdownStyleSheet(
                  p: AppTypography.bodyMedium.copyWith(
                    color: AppColors.textPrimary,
                    fontSize: 13.5,
                    height: 1.4,
                  ),
                  code: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    backgroundColor: AppColors.terminalBlack,
                    fontSize: 12,
                  ),
                  codeblockDecoration: BoxDecoration(
                    color: AppColors.terminalBlack,
                    borderRadius: AppSpacing.roundedSm,
                    border: Border.all(color: AppColors.outlineSubtle, width: 0.8),
                  ),
                  h1: AppTypography.titleLarge.copyWith(color: AppColors.textPrimary),
                  h2: AppTypography.titleMedium.copyWith(color: AppColors.textPrimary),
                  h3: AppTypography.titleSmall.copyWith(color: AppColors.textPrimary),
                  blockquote: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
                  blockquoteDecoration: BoxDecoration(
                    border: const Border(left: BorderSide(color: AppColors.infoCyan, width: 3)),
                    color: AppColors.surfaceHighlight.withOpacity(0.3),
                  ),
                  listBullet: AppTypography.bodyMedium.copyWith(color: AppColors.infoCyan),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  // --- 3. Collapsible Tool Invocations Group Accordion ---

  String _getToolGroupSummaryLabel(List<String> toolCalls) {
    if (toolCalls.isEmpty) return '⚡ Tools';
    final parsed = toolCalls.map(_ParsedToolCall.parse).toList();
    final counts = <String, int>{};
    for (final p in parsed) {
      counts[p.name] = (counts[p.name] ?? 0) + 1;
    }
    final uniqueTools = counts.keys.toList();
    final total = toolCalls.length;

    if (uniqueTools.length == 1) {
      final name = uniqueTools[0];
      if (total > 1) {
        return '⚡ $name x$total';
      }
      if (parsed[0].detail.isNotEmpty) {
        final shortDetail = parsed[0].detail.length > 35
            ? '${parsed[0].detail.substring(0, 32)}...'
            : parsed[0].detail;
        return '⚡ $name: $shortDetail';
      }
      return '⚡ $name';
    }

    // Multiple unique tools
    final breakdown = uniqueTools.map((name) => '$name x${counts[name]}').join(', ');
    return '⚡ $total tools ($breakdown)';
  }

  Widget _buildToolCallsGroup(List<String> tools) {
    final summaryLabel = _getToolGroupSummaryLabel(tools);
    final parsed = tools.map(_ParsedToolCall.parse).toList();
    final total = tools.length;

    return Container(
      decoration: BoxDecoration(
        color: AppColors.terminalBlack.withOpacity(0.6),
        borderRadius: AppSpacing.roundedSm,
        border: Border.all(color: AppColors.outlineSubtle.withOpacity(0.7), width: 0.6),
      ),
      child: Theme(
        data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          tilePadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
          childrenPadding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
          dense: true,
          leading: const Icon(Icons.bolt_rounded, size: 16, color: AppColors.statusAmberLight),
          title: Row(
            children: [
              Expanded(
                child: Text(
                  summaryLabel,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.statusAmberLight,
                    fontWeight: FontWeight.w700,
                    fontSize: 11,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1.5),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                ),
                child: Text(
                  '$total ${total == 1 ? 'action' : 'actions'}',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textMuted,
                    fontSize: 9.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          children: [
            ListView.separated(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              itemCount: parsed.length,
              separatorBuilder: (_, __) => const SizedBox(height: 6),
              itemBuilder: (context, idx) {
                final item = parsed[idx];
                return Container(
                  padding: const EdgeInsets.all(8),
                  decoration: BoxDecoration(
                    color: AppColors.surfaceHighlight.withOpacity(0.5),
                    borderRadius: BorderRadius.circular(6),
                    border: Border.all(color: AppColors.outlineSubtle.withOpacity(0.5), width: 0.5),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Text(
                            '#${idx + 1}',
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.textMuted,
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                          const SizedBox(width: 6),
                          Container(
                            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                            decoration: BoxDecoration(
                              color: AppColors.surface,
                              borderRadius: BorderRadius.circular(4),
                              border: Border.all(color: AppColors.statusAmberLight.withOpacity(0.3), width: 0.5),
                            ),
                            child: Text(
                              item.name,
                              style: AppTypography.codeXs.copyWith(
                                color: AppColors.statusAmberLight,
                                fontSize: 10.5,
                                fontWeight: FontWeight.w700,
                              ),
                            ),
                          ),
                          const Spacer(),
                          if (item.detail.isNotEmpty)
                            InkWell(
                              onTap: () {
                                Clipboard.setData(ClipboardData(text: item.detail));
                                HapticFeedback.lightImpact();
                                ScaffoldMessenger.of(context).showSnackBar(
                                  const SnackBar(
                                    content: Text('Command copied to clipboard'),
                                    duration: Duration(seconds: 1),
                                    backgroundColor: AppColors.surfaceHighlight,
                                  ),
                                );
                              },
                              child: const Padding(
                                padding: EdgeInsets.all(2),
                                child: Icon(Icons.copy_rounded, size: 13, color: AppColors.textMuted),
                              ),
                            ),
                        ],
                      ),
                      if (item.detail.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        Container(
                          width: double.infinity,
                          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
                          decoration: BoxDecoration(
                            color: AppColors.terminalBlack,
                            borderRadius: BorderRadius.circular(4),
                            border: Border.all(color: AppColors.outlineSubtle.withOpacity(0.4), width: 0.5),
                          ),
                          child: SelectableText(
                            item.detail,
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.infoCyan,
                              fontSize: 10.5,
                              height: 1.3,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }

  // --- Subagents Running Dock ---

  Widget _buildSubagentsDock() {
    if (_runningSubagents.isEmpty) {
      return const SizedBox.shrink();
    }

    final count = _runningSubagents.length;
    final countText = count == 1 ? '1 subagent running...' : '$count subagents running...';

    return Container(
      decoration: const BoxDecoration(
        color: Color(0xFF141720),
        border: Border(
          top: BorderSide(color: Color(0xFF2C3245), width: 1),
          bottom: BorderSide(color: AppColors.outlineSubtle, width: 0.5),
        ),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Collapsible Header Bar
          InkWell(
            onTap: () {
              HapticFeedback.selectionClick();
              setState(() {
                _isSubagentsExpanded = !_isSubagentsExpanded;
              });
            },
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 7),
              child: Row(
                children: [
                  // Pulsing Amber Dot
                  Container(
                    width: 7,
                    height: 7,
                    decoration: const BoxDecoration(
                      color: AppColors.statusAmber,
                      shape: BoxShape.circle,
                      boxShadow: [
                        BoxShadow(
                          color: AppColors.statusAmber,
                          blurRadius: 4,
                          spreadRadius: 1,
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  const Text(
                    '⚡',
                    style: TextStyle(fontSize: 11),
                  ),
                  const SizedBox(width: 4),
                  Expanded(
                    child: Text(
                      countText,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.statusAmberLight,
                        fontWeight: FontWeight.w700,
                        fontSize: 11,
                      ),
                    ),
                  ),
                  Icon(
                    _isSubagentsExpanded ? Icons.keyboard_arrow_up_rounded : Icons.keyboard_arrow_down_rounded,
                    size: 18,
                    color: AppColors.textMuted,
                  ),
                ],
              ),
            ),
          ),

          // Expanded Subagent Cards List
          if (_isSubagentsExpanded)
            Container(
              constraints: const BoxConstraints(maxHeight: 200),
              padding: const EdgeInsets.fromLTRB(AppSpacing.md, 0, AppSpacing.md, 8),
              child: ListView.separated(
                shrinkWrap: true,
                itemCount: _runningSubagents.length,
                separatorBuilder: (_, __) => const SizedBox(height: 6),
                itemBuilder: (context, idx) {
                  final sub = _runningSubagents[idx];
                  final prompt = sub.prompt.trim();

                  return Container(
                    padding: const EdgeInsets.all(8),
                    decoration: BoxDecoration(
                      color: AppColors.surfaceHighlight.withOpacity(0.6),
                      borderRadius: BorderRadius.circular(6),
                      border: Border.all(color: AppColors.outlineSubtle.withOpacity(0.6), width: 0.6),
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Container(
                              width: 6,
                              height: 6,
                              decoration: const BoxDecoration(
                                color: AppColors.statusAmber,
                                shape: BoxShape.circle,
                              ),
                            ),
                            const SizedBox(width: 6),
                            Expanded(
                              child: Text(
                                sub.displayName,
                                style: AppTypography.codeXs.copyWith(
                                  color: AppColors.textPrimary,
                                  fontWeight: FontWeight.w700,
                                  fontSize: 11,
                                ),
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                            if (sub.displayRole != null) ...[
                              const SizedBox(width: 6),
                              Container(
                                padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                                decoration: BoxDecoration(
                                  color: AppColors.surface,
                                  borderRadius: BorderRadius.circular(4),
                                  border: Border.all(color: AppColors.infoCyan.withOpacity(0.4), width: 0.5),
                                ),
                                child: Text(
                                  sub.displayRole!,
                                  style: AppTypography.codeXs.copyWith(
                                    color: AppColors.infoCyan,
                                    fontSize: 9.5,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ),
                            ],
                            if (prompt.isNotEmpty) ...[
                              const SizedBox(width: 6),
                              InkWell(
                                onTap: () {
                                  Clipboard.setData(ClipboardData(text: prompt));
                                  HapticFeedback.lightImpact();
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text('Subagent prompt copied'),
                                      duration: Duration(seconds: 1),
                                      backgroundColor: AppColors.surfaceHighlight,
                                    ),
                                  );
                                },
                                child: const Padding(
                                  padding: EdgeInsets.all(2),
                                  child: Icon(Icons.copy_rounded, size: 13, color: AppColors.textMuted),
                                ),
                              ),
                            ],
                          ],
                        ),
                        if (prompt.isNotEmpty) ...[
                          const SizedBox(height: 5),
                          Container(
                            width: double.infinity,
                            padding: const EdgeInsets.all(6),
                            decoration: BoxDecoration(
                              color: AppColors.terminalBlack,
                              borderRadius: BorderRadius.circular(4),
                              border: Border.all(color: AppColors.outlineSubtle.withOpacity(0.4), width: 0.5),
                            ),
                            child: Text(
                              prompt,
                              style: AppTypography.codeXs.copyWith(
                                color: AppColors.textSecondary,
                                fontSize: 10,
                                height: 1.3,
                              ),
                              maxLines: 4,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        ],
                      ],
                    ),
                  );
                },
              ),
            ),
        ],
      ),
    );
  }

  // --- 4. Collapsible Thought Process ---

  Widget _buildThinkingAccordion(String thinking) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.surfaceHighlight.withOpacity(0.4),
        borderRadius: AppSpacing.roundedSm,
        border: Border.all(color: AppColors.outlineSubtle.withOpacity(0.6), width: 0.6),
      ),
      child: Theme(
        data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          tilePadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
          childrenPadding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
          dense: true,
          leading: const Icon(Icons.psychology_outlined, size: 16, color: AppColors.textMuted),
          title: Text(
            '💭 Thought Process',
            style: AppTypography.codeXs.copyWith(
              color: AppColors.textSecondary,
              fontWeight: FontWeight.w600,
              fontSize: 11,
            ),
          ),
          children: [
            SelectableText(
              thinking.trim(),
              style: AppTypography.codeXs.copyWith(
                color: AppColors.textMuted,
                fontSize: 11,
                height: 1.35,
              ),
            ),
          ],
        ),
      ),
    );
  }

  // --- 5. Center-Aligned System Checkpoint ---

  Widget _buildSystemEventBubble(TranscriptMessage message) {
    return Center(
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 12),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        decoration: BoxDecoration(
          color: AppColors.surfaceHighlight.withOpacity(0.5),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.info_outline_rounded, size: 12, color: AppColors.textMuted),
            const SizedBox(width: 6),
            Flexible(
              child: Text(
                message.content.length > 80 ? '${message.content.substring(0, 80)}...' : message.content,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.textMuted,
                  fontSize: 10.5,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
    );
  }

  String _formatTimestamp(DateTime dt) {
    final hour = dt.hour.toString().padLeft(2, '0');
    final min = dt.minute.toString().padLeft(2, '0');
    final sec = dt.second.toString().padLeft(2, '0');
    return '$hour:$min:$sec';
  }
}

class _ParsedToolCall {
  final String name;
  final String detail;

  const _ParsedToolCall({required this.name, required this.detail});

  factory _ParsedToolCall.parse(String raw) {
    final trimmed = raw.trim();
    if (trimmed.isEmpty) return const _ParsedToolCall(name: 'tool', detail: '');

    // Pattern 1: "tool_name: detail/command"
    final colonIdx = trimmed.indexOf(':');
    if (colonIdx > 0 && colonIdx < 30) {
      final n = trimmed.substring(0, colonIdx).trim();
      final d = trimmed.substring(colonIdx + 1).trim();
      if (!n.contains(' ') || n.length < 20) {
        return _ParsedToolCall(name: n, detail: d);
      }
    }

    // Pattern 2: "tool_name (description)"
    final match = RegExp(r'^([a-zA-Z0-9_\-]+)\s*\((.+)\)$').firstMatch(trimmed);
    if (match != null) {
      return _ParsedToolCall(name: match.group(1)!.trim(), detail: match.group(2)!.trim());
    }

    return _ParsedToolCall(name: trimmed, detail: '');
  }
}
