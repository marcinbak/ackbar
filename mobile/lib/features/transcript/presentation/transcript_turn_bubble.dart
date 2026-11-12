import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/session.dart';
import '../../../core/models/transcript.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/services/audio_companion_service.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';
import 'transcript_tool_widgets.dart';

class TranscriptTurnBubble extends ConsumerWidget {
  final TranscriptMessage message;
  final int index;
  final Session session;
  final void Function(
    int index,
    String content, {
    bool forceRefresh,
    bool forceFull,
  })? onSpeak;

  const TranscriptTurnBubble({
    super.key,
    required this.message,
    required this.index,
    required this.session,
    this.onSpeak,
  });

  String _formatTimestamp(DateTime dt) {
    final hour = dt.hour.toString().padLeft(2, '0');
    final min = dt.minute.toString().padLeft(2, '0');
    final sec = dt.second.toString().padLeft(2, '0');
    return '$hour:$min:$sec';
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (message.isSystem) {
      return TranscriptSystemBubble(content: message.content);
    } else if (message.isUser) {
      return _buildUserBubble(context);
    } else {
      return _buildAssistantBubble(context, ref);
    }
  }

  Widget _buildUserBubble(BuildContext context) {
    final timeStr = _formatTimestamp(message.timestamp);

    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        margin: const EdgeInsets.only(bottom: 14, left: 40),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: const Color(0xFF1E2638),
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

  Widget _buildAssistantBubble(BuildContext context, WidgetRef ref) {
    final timeStr = _formatTimestamp(message.timestamp);
    final audioCompanion = ref.watch(audioCompanionServiceProvider);
    final isSpeakingThis =
        audioCompanion.isSpeaking && audioCompanion.activeMessageIndex == index;
    final isCached = audioCompanion.isCached(session.id, index);

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.only(bottom: 16, right: 16),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: AppColors.surface,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(3),
            bottomRight: Radius.circular(16),
          ),
          border: Border.all(
            color:
                isSpeakingThis ? AppColors.infoCyan : AppColors.outlineSubtle,
            width: isSpeakingThis ? 1.5 : 0.8,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 6,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: session.agentColor.withOpacity(0.15),
                    borderRadius: AppSpacing.roundedSm,
                    border: Border.all(
                      color: session.agentColor.withOpacity(0.4),
                      width: 0.8,
                    ),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      AgentLogo(
                        agent: session.agent,
                        size: 11,
                        color: session.agentColor,
                      ),
                      const SizedBox(width: 4),
                      Text(
                        session.agentDisplayName,
                        style: AppTypography.codeXs.copyWith(
                          color: session.agentColor,
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
                if (isSpeakingThis)
                  IconButton(
                    icon: const Icon(
                      Icons.stop_circle_rounded,
                      size: 16,
                      color: AppColors.statusCoral,
                    ),
                    padding: EdgeInsets.zero,
                    constraints: const BoxConstraints(
                      minWidth: 24,
                      minHeight: 24,
                    ),
                    tooltip: 'Stop speaking',
                    onPressed: () {
                      ref
                          .read(audioCompanionServiceProvider.notifier)
                          .stopSpeech();
                    },
                  )
                else ...[
                  IconButton(
                    icon: const Icon(
                      Icons.volume_up_rounded,
                      size: 15,
                      color: AppColors.textMuted,
                    ),
                    padding: EdgeInsets.zero,
                    constraints: const BoxConstraints(
                      minWidth: 24,
                      minHeight: 24,
                    ),
                    tooltip: 'Read aloud',
                    onPressed: () => onSpeak?.call(index, message.content),
                  ),
                  if (isCached) ...[
                    IconButton(
                      icon: const Icon(
                        Icons.replay_rounded,
                        size: 14,
                        color: AppColors.infoCyan,
                      ),
                      padding: EdgeInsets.zero,
                      constraints: const BoxConstraints(
                        minWidth: 24,
                        minHeight: 24,
                      ),
                      tooltip: 'Read again',
                      onPressed: () => onSpeak?.call(
                        index,
                        message.content,
                        forceRefresh: true,
                      ),
                    ),
                    IconButton(
                      icon: const Icon(
                        Icons.article_outlined,
                        size: 14,
                        color: AppColors.infoCyan,
                      ),
                      padding: EdgeInsets.zero,
                      constraints: const BoxConstraints(
                        minWidth: 24,
                        minHeight: 24,
                      ),
                      tooltip: 'Read unabridged full text',
                      onPressed: () => onSpeak?.call(
                        index,
                        message.content,
                        forceFull: true,
                      ),
                    ),
                  ],
                ],
                const SizedBox(width: 2),
                IconButton(
                  icon: const Icon(
                    Icons.copy_rounded,
                    size: 14,
                    color: AppColors.textMuted,
                  ),
                  padding: EdgeInsets.zero,
                  constraints: const BoxConstraints(
                    minWidth: 24,
                    minHeight: 24,
                  ),
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
            if (message.toolCalls.isNotEmpty) ...[
              const SizedBox(height: 8),
              TranscriptToolCallsGroup(toolCalls: message.toolCalls),
            ],
            if (message.thinking != null &&
                message.thinking!.trim().isNotEmpty) ...[
              const SizedBox(height: 8),
              TranscriptThinkingAccordion(thinking: message.thinking!),
            ],
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
                    border: Border.all(
                      color: AppColors.outlineSubtle,
                      width: 0.8,
                    ),
                  ),
                  h1: AppTypography.titleLarge.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  h2: AppTypography.titleMedium.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  h3: AppTypography.titleSmall.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  blockquote: AppTypography.bodySmall.copyWith(
                    color: AppColors.textSecondary,
                  ),
                  blockquoteDecoration: BoxDecoration(
                    border: const Border(
                      left: BorderSide(color: AppColors.infoCyan, width: 3),
                    ),
                    color: AppColors.surfaceHighlight.withOpacity(0.3),
                  ),
                  listBullet: AppTypography.bodyMedium.copyWith(
                    color: AppColors.infoCyan,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
