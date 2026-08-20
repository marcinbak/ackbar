import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/token_gauge_bar.dart';

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
                    Text(session.agentSymbol, style: const TextStyle(fontSize: 13)),
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
                    _showPtyModal(context, session);
                  },
                  icon: const Icon(Icons.terminal_rounded, size: 16),
                  label: const Text('Inspect PTY'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.surfaceHighlight,
                    foregroundColor: AppColors.infoCyan,
                    side: const BorderSide(color: AppColors.infoCyan, width: 1),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: ElevatedButton.icon(
                  onPressed: () async {
                    Navigator.of(context).pop();
                    await ref.read(fleetSessionsProvider.notifier).restartSession(session.id);
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text('Restarting ${session.displayTitle}...'),
                        backgroundColor: AppColors.surfaceHighlight,
                      ),
                    );
                  },
                  icon: const Icon(Icons.refresh_rounded, size: 16),
                  label: const Text('Restart'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.surfaceHighlight,
                    foregroundColor: AppColors.textPrimary,
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

  void _showPtyModal(BuildContext context, Session session) {
    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: AppColors.terminalBlack,
        shape: RoundedRectangleBorder(
          borderRadius: AppSpacing.roundedLg,
          side: const BorderSide(color: AppColors.outline, width: 1),
        ),
        title: Row(
          children: [
            const Icon(Icons.terminal_rounded, size: 18, color: AppColors.infoCyan),
            const SizedBox(width: 8),
            Text(
              'PTY: ${session.tmuxName.isNotEmpty ? session.tmuxName : session.id}',
              style: AppTypography.codeSm.copyWith(
                color: AppColors.textPrimary,
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
        content: Container(
          width: double.maxFinite,
          height: 240,
          padding: AppSpacing.paddingCardDense,
          decoration: BoxDecoration(
            color: const Color(0xFF030406),
            borderRadius: AppSpacing.roundedSm,
            border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
          ),
          child: SingleChildScrollView(
            child: Text(
              '''[ackbard] Supervised tmux pane attached
host: ${session.host} (${session.hostTag})
cwd: ${session.cwd}
agent: ${session.agentDisplayName}
---------------------------------------------
> ${session.activity}
> context_pct: ${session.contextPct}%
> last_event: ${session.lastEventAt.toIso8601String()}
> state: ${session.state.label}
---------------------------------------------
[agent output streaming live...]''',
              style: AppTypography.codeSm.copyWith(
                color: AppColors.statusEmerald,
                fontSize: 11,
              ),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: Text('Close', style: AppTypography.titleSmall.copyWith(color: AppColors.infoCyan)),
          ),
        ],
      ),
    );
  }
}
