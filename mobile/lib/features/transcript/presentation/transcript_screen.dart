import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/models/transcript.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/glass_card.dart';
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
  bool _showJumpToBottom = false;
  bool _isLoading = true;
  String? _errorMessage;
  TranscriptData? _transcriptData;

  @override
  void initState() {
    super.initState();
    _loadTranscript();
    _scrollController.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scrollController.removeListener(_onScroll);
    _scrollController.dispose();
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

  Future<void> _loadTranscript() async {
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    final hosts = ref.read(hostsListProvider);
    final match = hosts.where((h) => h.name == widget.session.host || h.url.contains(widget.session.host));
    final hostUrl = match.isNotEmpty ? match.first.url : (hosts.isNotEmpty ? hosts.first.url : 'http://127.0.0.1:7777');

    try {
      final structured = await ref.read(apiClientProvider).getStructuredTranscript(hostUrl, widget.session.id);
      if (structured != null && structured.messages.isNotEmpty) {
        setState(() {
          _transcriptData = structured;
          _isLoading = false;
        });
      } else {
        // Fallback to markdown parser
        final rawMd = await ref.read(apiClientProvider).getTranscript(hostUrl, widget.session.id);
        final parsed = TranscriptData.fromRawMarkdown(
          sessionId: widget.session.id,
          agent: widget.session.agent,
          title: widget.session.displayTitle,
          markdown: rawMd,
        );
        setState(() {
          _transcriptData = parsed;
          _isLoading = false;
        });
      }

      WidgetsBinding.instance.addPostFrameCallback((_) {
        _scrollToBottom(animate: false);
      });
    } catch (e) {
      setState(() {
        _errorMessage = 'Failed to load transcript: $e';
        _isLoading = false;
      });
    }
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
            onPressed: _loadTranscript,
          ),
        ],
      ),
      body: Stack(
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
                      onPressed: _loadTranscript,
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
              padding: const EdgeInsets.fromLTRB(AppSpacing.md, AppSpacing.md, AppSpacing.md, 80),
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
              bottom: 20,
              right: 20,
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
                      Text(widget.session.agentSymbol, style: const TextStyle(fontSize: 11)),
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

            // Tool Invocations Accordion
            if (message.toolCalls.isNotEmpty) ...[
              const SizedBox(height: 8),
              _buildToolCallsPills(message.toolCalls),
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

  // --- 3. Collapsible Tool Invocations Pills ---

  Widget _buildToolCallsPills(List<String> tools) {
    return Theme(
      data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
      child: ExpansionTile(
        tilePadding: EdgeInsets.zero,
        childrenPadding: const EdgeInsets.only(top: 4),
        dense: true,
        leading: const Icon(Icons.bolt_rounded, size: 16, color: AppColors.statusAmberLight),
        title: Text(
          '⚡ ${tools.length} ${tools.length == 1 ? 'Tool' : 'Tools'} Executed',
          style: AppTypography.codeXs.copyWith(
            color: AppColors.statusAmberLight,
            fontWeight: FontWeight.w700,
            fontSize: 11,
          ),
        ),
        children: [
          Wrap(
            spacing: 6,
            runSpacing: 4,
            children: tools.map((t) {
              return Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                decoration: BoxDecoration(
                  color: AppColors.terminalBlack,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.6),
                ),
                child: Text(
                  '🛠️ $t',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textSecondary,
                    fontSize: 10.5,
                  ),
                ),
              );
            }).toList(),
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
