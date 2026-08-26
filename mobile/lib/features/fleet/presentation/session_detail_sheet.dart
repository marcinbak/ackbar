import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/token_gauge_bar.dart';
import '../../terminal/presentation/terminal_screen.dart';
import '../../transcript/presentation/transcript_screen.dart';

/// Modal bottom sheet providing deep inspection and lifecycle control for an agent session.
class SessionDetailSheet extends ConsumerWidget {
  final Session session;

  const SessionDetailSheet({super.key, required this.session});

  static Future<void> show(BuildContext context, Session session) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => SessionDetailSheet(session: session),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final statusColor = session.state.toBadgeStatus().color;

    if (session.isUnread) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        ref.read(fleetSessionsProvider.notifier).markSessionRead(session.id, session.host);
      });
    }

    return Container(
      decoration: const BoxDecoration(
        color: AppColors.background,
        borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXl)),
        border: Border(
          top: BorderSide(color: AppColors.outline, width: 1.5),
        ),
      ),
      padding: EdgeInsets.only(
        left: AppSpacing.lg,
        right: AppSpacing.lg,
        top: AppSpacing.md,
        bottom: MediaQuery.of(context).viewInsets.bottom + AppSpacing.xl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Drag handle
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.outline,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          AppSpacing.gapH12,

          // Header Row
          Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                decoration: BoxDecoration(
                  color: session.agentColor.withOpacity(0.15),
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: session.agentColor.withOpacity(0.4), width: 1),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    AgentLogo(agent: session.agent, size: 12, color: session.agentColor),
                    const SizedBox(width: 5),
                    Text(
                      session.agentDisplayName,
                      style: AppTypography.codeXs.copyWith(
                        color: session.agentColor,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                ),
                child: Text(
                  session.hostTag,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (session.gitBranch.isNotEmpty) ...[
                const SizedBox(width: 8),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
                  decoration: BoxDecoration(
                    color: AppColors.surfaceHighlight,
                    borderRadius: AppSpacing.roundedSm,
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.fork_right_rounded, size: 11, color: AppColors.textMuted),
                      const SizedBox(width: 3),
                      Text(
                        session.gitBranch,
                        style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                      ),
                    ],
                  ),
                ),
              ],
              const Spacer(),
              StatusBadge(status: session.state.toBadgeStatus(), isCompact: true),
            ],
          ),
          AppSpacing.gapH16,

          // Session title
          Text(
            session.displayTitle,
            style: AppTypography.headlineMedium.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w700,
            ),
          ),
          AppSpacing.gapH8,

          // Activity line
          if (session.activity.isNotEmpty)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
              decoration: BoxDecoration(
                color: AppColors.surface,
                borderRadius: AppSpacing.roundedSm,
                border: Border.all(color: AppColors.outlineSubtle, width: 0.8),
              ),
              child: Row(
                children: [
                  Icon(Icons.bolt_rounded, size: 14, color: statusColor),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      session.activity,
                      style: AppTypography.codeSm.copyWith(
                        color: AppColors.textPrimary,
                        fontSize: 11.5,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
            ),
          AppSpacing.gapH16,

          // Metadata Grid
          GlassCard(
            padding: AppSpacing.paddingCardDense,
            child: Column(
              children: [
                _buildMetaRow('SESSION ID', session.id),
                const Divider(height: 12, color: AppColors.outlineSubtle),
                _buildMetaRow('WORKING DIRECTORY', session.cwd),
                if (session.tmuxName.isNotEmpty) ...[
                  const Divider(height: 12, color: AppColors.outlineSubtle),
                  _buildMetaRow('TMUX SESSION', session.tmuxName),
                ],
                if (session.pid > 0) ...[
                  const Divider(height: 12, color: AppColors.outlineSubtle),
                  _buildMetaRow('PROCESS PID', '${session.pid}'),
                ],
                const Divider(height: 12, color: AppColors.outlineSubtle),
                _buildMetaRow('RUNTIME', session.timeElapsedFormatted),
              ],
            ),
          ),
          AppSpacing.gapH16,

          // Context Usage
          TokenGaugeBar(
            percentage: session.contextPct / 100.0,
            customLabel: 'CONTEXT WINDOW USAGE',
          ),
          AppSpacing.gapH20,

          // Action Buttons
          Row(
            children: [
              Expanded(
                child: ElevatedButton.icon(
                  onPressed: () {
                    Navigator.of(context).pop();
                    TerminalScreen.open(context, session);
                  },
                  icon: const Icon(Icons.terminal_rounded, size: 16),
                  label: const Text('Terminal'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.statusEmerald.withOpacity(0.15),
                    foregroundColor: AppColors.statusEmerald,
                    side: const BorderSide(color: AppColors.statusEmerald, width: 1),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: ElevatedButton.icon(
                  onPressed: () {
                    Navigator.of(context).pop();
                    TranscriptScreen.open(context, session);
                  },
                  icon: const Icon(Icons.forum_outlined, size: 16),
                  label: const Text('Transcript'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.surfaceHighlight,
                    foregroundColor: AppColors.infoCyan,
                    side: const BorderSide(color: AppColors.infoCyan, width: 1),
                  ),
                ),
              ),
            ],
          ),
          AppSpacing.gapH8,
          Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: () async {
                    Navigator.of(context).pop();
                    await ref.read(fleetSessionsProvider.notifier).terminateSession(session.id);
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text('Terminated ${session.displayTitle}'),
                        backgroundColor: AppColors.statusCoral.withOpacity(0.8),
                      ),
                    );
                  },
                  icon: const Icon(Icons.stop_circle_outlined, size: 16),
                  label: const Text('Terminate'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.statusCoral,
                    side: BorderSide(color: AppColors.statusCoral.withOpacity(0.4)),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: () async {
                    Navigator.of(context).pop();
                    await ref.read(fleetSessionsProvider.notifier).deleteSession(session.id);
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text('Deleted ${session.displayTitle}'),
                        backgroundColor: AppColors.surfaceHighlight,
                      ),
                    );
                  },
                  icon: const Icon(Icons.delete_outline_rounded, size: 16),
                  label: const Text('Delete'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.textMuted,
                    side: const BorderSide(color: AppColors.outlineSubtle),
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildMetaRow(String label, String value) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(
          label,
          style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Text(
            value,
            textAlign: TextAlign.right,
            style: AppTypography.codeXs.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w600,
            ),
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ],
    );
  }

  void _showTranscriptModal(BuildContext context, WidgetRef ref, Session session) {
    final hosts = ref.read(hostsListProvider);
    final match = hosts.where((h) => h.name == session.host || h.url.contains(session.host));
    final hostUrl = match.isNotEmpty ? match.first.url : (hosts.isNotEmpty ? hosts.first.url : 'http://127.0.0.1:7777');

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => DraggableScrollableSheet(
        initialChildSize: 0.75,
        minChildSize: 0.4,
        maxChildSize: 0.95,
        expand: false,
        builder: (context, scrollController) {
          return FutureBuilder<String>(
            future: ref.read(apiClientProvider).getTranscript(hostUrl, session.id),
            builder: (context, snapshot) {
              return Padding(
                padding: AppSpacing.paddingScreen,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Handle & Header
                    Center(
                      child: Container(
                        width: 36,
                        height: 4,
                        decoration: BoxDecoration(
                          color: AppColors.outlineSubtle,
                          borderRadius: BorderRadius.circular(2),
                        ),
                      ),
                    ),
                    AppSpacing.gapH12,
                    Row(
                      children: [
                        const Icon(Icons.article_outlined, size: 20, color: AppColors.infoCyan),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                'SESSION TRANSCRIPT',
                                style: AppTypography.codeSm.copyWith(
                                  color: AppColors.textPrimary,
                                  fontWeight: FontWeight.w700,
                                ),
                              ),
                              Text(
                                '${session.agentDisplayName} • ${session.displayTitle}',
                                style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                                overflow: TextOverflow.ellipsis,
                              ),
                            ],
                          ),
                        ),
                        if (snapshot.hasData && snapshot.data!.isNotEmpty)
                          IconButton(
                            icon: const Icon(Icons.copy_rounded, size: 18, color: AppColors.textMuted),
                            tooltip: 'Copy transcript',
                            onPressed: () {
                              Clipboard.setData(ClipboardData(text: snapshot.data!));
                              ScaffoldMessenger.of(context).showSnackBar(
                                const SnackBar(
                                  content: Text('Transcript copied to clipboard'),
                                  backgroundColor: AppColors.surfaceHighlight,
                                ),
                              );
                            },
                          ),
                        IconButton(
                          icon: const Icon(Icons.close_rounded, size: 20, color: AppColors.textMuted),
                          onPressed: () => Navigator.of(ctx).pop(),
                        ),
                      ],
                    ),
                    const Divider(color: AppColors.outlineSubtle, height: 20),
                    Expanded(
                      child: snapshot.connectionState == ConnectionState.waiting
                          ? const Center(child: CircularProgressIndicator(color: AppColors.infoCyan))
                          : (snapshot.data == null || snapshot.data!.trim().isEmpty
                              ? Center(
                                  child: Column(
                                    mainAxisSize: MainAxisSize.min,
                                    children: [
                                      const Icon(Icons.receipt_long_outlined, size: 36, color: AppColors.textMuted),
                                      AppSpacing.gapH8,
                                      Text(
                                        'No transcript available yet',
                                        style: AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
                                      ),
                                    ],
                                  ),
                                )
                              : Container(
                                  padding: AppSpacing.paddingCardDense,
                                  decoration: BoxDecoration(
                                    color: AppColors.terminalBlack,
                                    borderRadius: AppSpacing.roundedSm,
                                    border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                                  ),
                                  child: Builder(
                                    builder: (context) {
                                      WidgetsBinding.instance.addPostFrameCallback((_) {
                                        if (scrollController.hasClients) {
                                          scrollController.jumpTo(scrollController.position.maxScrollExtent);
                                        }
                                      });
                                      return SingleChildScrollView(
                                        controller: scrollController,
                                        child: MarkdownBody(
                                          data: snapshot.data!,
                                          styleSheet: MarkdownStyleSheet(
                                            p: AppTypography.codeXs.copyWith(color: AppColors.textSecondary, fontSize: 11),
                                            code: AppTypography.codeXs.copyWith(color: AppColors.infoCyan, backgroundColor: Colors.transparent),
                                            h1: AppTypography.titleMedium.copyWith(color: AppColors.textPrimary),
                                            h2: AppTypography.titleSmall.copyWith(color: AppColors.textPrimary),
                                            h3: AppTypography.bodyMedium.copyWith(color: AppColors.textPrimary, fontWeight: FontWeight.bold),
                                          ),
                                        ),
                                      );
                                    },
                                  ),
                                )),
                    ),
                  ],
                ),
              );
            },
          );
        },
      ),
    );
  }
}
