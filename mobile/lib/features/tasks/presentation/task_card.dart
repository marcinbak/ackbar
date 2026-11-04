import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/models/work_task.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/glass_card.dart';
import '../../transcript/presentation/transcript_screen.dart';
import 'deliverable_markdown_modal.dart';

/// Card rendering an individual work task with status, workers, deliverables, and PR actions
class TaskCard extends ConsumerStatefulWidget {
  final WorkTask task;

  const TaskCard({super.key, required this.task});

  @override
  ConsumerState<TaskCard> createState() => _TaskCardState();
}

class _TaskCardState extends ConsumerState<TaskCard> {
  bool _isMerging = false;

  Color _getSubstatusColor(String substatus) {
    switch (substatus.toLowerCase()) {
      case 'approved':
      case 'done':
        return AppColors.statusEmerald;
      case 'working':
      case 'in_progress':
        return AppColors.infoCyan;
      case 'blocked':
      case 'failing':
        return AppColors.statusCoral;
      case 'review':
      case 'changes_requested':
        return AppColors.statusAmber;
      default:
        return AppColors.textMuted;
    }
  }

  void _onWorkerTap(TaskWorker worker) {
    final allSessions = ref.read(fleetSessionsProvider);
    final match = allSessions
        .where((s) =>
            s.id == worker.sessionId ||
            (worker.sessionId.isNotEmpty && s.nativeId == worker.sessionId))
        .firstOrNull;

    final targetSession = match ??
        Session(
          id: worker.sessionId.isNotEmpty ? worker.sessionId : 'worker-session',
          agent: worker.agent.isNotEmpty ? worker.agent : 'claude-code',
          host: worker.host.isNotEmpty ? worker.host : 'local',
          nativeId: worker.sessionId,
          cwd: worker.repoNodePath,
          state: SessionState.working,
          startedAt: worker.assignedAt,
          lastEventAt: DateTime.now(),
          managed: true,
          name: '${worker.agent} @ ${worker.host}',
        );

    TranscriptScreen.open(context, targetSession);
  }

  void _onDeliverableTap(TaskDeliverable deliv) {
    if (deliv.isMarkdown || deliv.contentMd.isNotEmpty) {
      DeliverableMarkdownModal.show(context, deliv);
    } else if (deliv.isUrl) {
      showDialog(
        context: context,
        builder: (ctx) => AlertDialog(
          backgroundColor: AppColors.surface,
          title: Row(
            children: [
              Text(deliv.providerIcon),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  deliv.title.isNotEmpty
                      ? deliv.title
                      : deliv.providerDisplayName,
                  style: AppTypography.titleMedium
                      .copyWith(color: AppColors.textPrimary),
                ),
              ),
            ],
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Hosted link (${deliv.providerDisplayName}):',
                style: AppTypography.bodySmall
                    .copyWith(color: AppColors.textMuted),
              ),
              const SizedBox(height: 8),
              SelectableText(
                deliv.url,
                style: AppTypography.codeXs.copyWith(color: AppColors.infoCyan),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: const Text('Close'),
            ),
          ],
        ),
      );
    }
  }

  Future<void> _mergePR() async {
    setState(() => _isMerging = true);
    final scaffoldMessenger = ScaffoldMessenger.of(context);
    try {
      final ok = await ref
          .read(tasksProvider.notifier)
          .mergePR(widget.task.id, method: 'squash');
      if (mounted) {
        scaffoldMessenger.showSnackBar(
          SnackBar(
            content: Text(ok
                ? 'PR squashed & merged successfully for ${widget.task.title}'
                : 'Failed to merge PR. Check host logs.'),
            backgroundColor:
                ok ? AppColors.statusEmerald : AppColors.statusCoral,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _isMerging = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final task = widget.task;
    final substatusColor = _getSubstatusColor(task.substatus);

    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.md),
      child: GlassCard(
        accentColor: substatusColor,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Top Row: Tracker Ref, Project Tag, Substatus
            Row(
              children: [
                if (task.primaryTrackerRef.isNotEmpty) ...[
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                    decoration: BoxDecoration(
                      color: AppColors.infoCyan.withOpacity(0.15),
                      borderRadius: AppSpacing.roundedSm,
                      border: Border.all(
                        color: AppColors.infoCyan.withOpacity(0.4),
                        width: 0.8,
                      ),
                    ),
                    child: Text(
                      task.primaryTrackerRef,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w700,
                        fontSize: 10,
                      ),
                    ),
                  ),
                  const SizedBox(width: 6),
                ],
                if (task.projectName.isNotEmpty) ...[
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                    decoration: BoxDecoration(
                      color: AppColors.surfaceHighlight,
                      borderRadius: AppSpacing.roundedSm,
                      border: Border.all(
                          color: AppColors.outlineSubtle, width: 0.5),
                    ),
                    child: Text(
                      task.projectName,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.textSecondary,
                        fontSize: 10,
                      ),
                    ),
                  ),
                ],
                const Spacer(),
                if (task.substatus.isNotEmpty) ...[
                  Container(
                    width: 7,
                    height: 7,
                    decoration: BoxDecoration(
                      color: substatusColor,
                      shape: BoxShape.circle,
                    ),
                  ),
                  const SizedBox(width: 4),
                  Text(
                    task.substatus.toUpperCase(),
                    style: AppTypography.codeXs.copyWith(
                      color: substatusColor,
                      fontWeight: FontWeight.w700,
                      fontSize: 10,
                    ),
                  ),
                ],
              ],
            ),
            AppSpacing.gapH8,

            // Title
            Text(
              task.title,
              style: AppTypography.titleMedium.copyWith(
                color: AppColors.textPrimary,
                fontWeight: FontWeight.w600,
              ),
            ),

            // Notes / Blocker Question
            if (task.blockerQuestion.isNotEmpty) ...[
              AppSpacing.gapH6,
              Container(
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: AppColors.statusCoral.withOpacity(0.12),
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(
                    color: AppColors.statusCoral.withOpacity(0.3),
                    width: 0.8,
                  ),
                ),
                child: Row(
                  children: [
                    const Icon(Icons.help_outline_rounded,
                        size: 14, color: AppColors.statusCoral),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        task.blockerQuestion,
                        style: AppTypography.bodySmall.copyWith(
                          color: AppColors.statusCoral,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ] else if (task.notes.isNotEmpty) ...[
              AppSpacing.gapH4,
              Text(
                task.notes,
                style: AppTypography.bodySmall.copyWith(
                  color: AppColors.textMuted,
                ),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ],

            // Workers Row
            if (task.workers.isNotEmpty) ...[
              AppSpacing.gapH8,
              Wrap(
                spacing: 6,
                runSpacing: 4,
                children: task.workers.map((w) {
                  return InkWell(
                    onTap: () => _onWorkerTap(w),
                    borderRadius: AppSpacing.roundedSm,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 7, vertical: 3),
                      decoration: BoxDecoration(
                        color: AppColors.surfaceHighlight,
                        borderRadius: AppSpacing.roundedSm,
                        border: Border.all(
                            color: AppColors.outlineSubtle, width: 0.5),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.person_outline_rounded,
                              size: 11, color: AppColors.infoCyan),
                          const SizedBox(width: 4),
                          Text(
                            '@${w.host.isNotEmpty ? w.host : 'local'}',
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.textPrimary,
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                          if (w.agent.isNotEmpty) ...[
                            const SizedBox(width: 3),
                            Text(
                              '(${w.agent})',
                              style: AppTypography.codeXs.copyWith(
                                color: AppColors.textMuted,
                                fontSize: 9.5,
                              ),
                            ),
                          ],
                        ],
                      ),
                    ),
                  );
                }).toList(),
              ),
            ],

            // Deliverables Row
            if (task.deliverables.isNotEmpty) ...[
              AppSpacing.gapH8,
              Wrap(
                spacing: 6,
                runSpacing: 4,
                children: task.deliverables.map((d) {
                  return InkWell(
                    onTap: () => _onDeliverableTap(d),
                    borderRadius: AppSpacing.roundedSm,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 7, vertical: 3),
                      decoration: BoxDecoration(
                        color: AppColors.surfaceHighlight,
                        borderRadius: AppSpacing.roundedSm,
                        border: Border.all(
                          color: AppColors.infoCyan.withOpacity(0.35),
                          width: 0.8,
                        ),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(d.providerIcon,
                              style: const TextStyle(fontSize: 10)),
                          const SizedBox(width: 4),
                          Text(
                            d.title.isNotEmpty
                                ? d.title
                                : d.providerDisplayName,
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.infoCyan,
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ],
                      ),
                    ),
                  );
                }).toList(),
              ),
            ],

            // Squash & Merge PR Action Button
            if (task.isApprovedReview) ...[
              AppSpacing.gapH12,
              SizedBox(
                width: double.infinity,
                child: ElevatedButton.icon(
                  onPressed: _isMerging ? null : _mergePR,
                  icon: _isMerging
                      ? const SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: AppColors.terminalBlack,
                          ),
                        )
                      : const Icon(Icons.merge_type_rounded, size: 16),
                  label: Text(
                    _isMerging ? 'MERGING...' : '⚡ SQUASH & MERGE PR',
                    style: AppTypography.codeXs.copyWith(
                      fontWeight: FontWeight.w800,
                      color: AppColors.terminalBlack,
                      letterSpacing: 0.5,
                    ),
                  ),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.statusEmerald,
                    foregroundColor: AppColors.terminalBlack,
                    padding: const EdgeInsets.symmetric(vertical: 10),
                    shape: RoundedRectangleBorder(
                      borderRadius: AppSpacing.roundedMd,
                    ),
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
