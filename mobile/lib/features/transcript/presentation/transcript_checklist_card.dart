import 'package:flutter/material.dart';

import '../../../core/models/checklist_item.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';

class TranscriptChecklistCard extends StatelessWidget {
  final List<ChecklistItem> items;

  const TranscriptChecklistCard({
    super.key,
    required this.items,
  });

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return const SizedBox.shrink();
    }

    final doneCount =
        items.where((item) => item.status == ChecklistStatus.done).length;
    final progress = doneCount / items.length;
    final pct = (progress * 100).round();
    final isComplete = doneCount == items.length;

    return Container(
      key: const ValueKey('transcript_checklist_card'),
      width: double.infinity,
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppColors.terminalBlack.withOpacity(0.65),
        borderRadius: AppSpacing.roundedMd,
        border: Border.all(
          color: AppColors.infoCyan.withOpacity(0.32),
          width: 0.8,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              const Icon(
                Icons.checklist_rounded,
                size: 15,
                color: AppColors.infoCyan,
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  'Task Progress ($doneCount/${items.length})',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w700,
                    fontSize: 11.5,
                  ),
                ),
              ),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 6, vertical: 1.5),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(
                    color: AppColors.outlineSubtle,
                    width: 0.6,
                  ),
                ),
                child: Text(
                  '$pct%',
                  style: AppTypography.codeXs.copyWith(
                    color: isComplete
                        ? AppColors.statusEmerald
                        : AppColors.textSecondary,
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          ClipRRect(
            borderRadius: AppSpacing.roundedFull,
            child: LinearProgressIndicator(
              key: const ValueKey('checklist_progress_bar'),
              value: progress,
              minHeight: 3,
              backgroundColor: AppColors.surfaceHighlight,
              valueColor: AlwaysStoppedAnimation<Color>(
                isComplete ? AppColors.statusEmerald : AppColors.infoCyan,
              ),
            ),
          ),
          const SizedBox(height: 8),
          for (var i = 0; i < items.length; i++) ...[
            if (i > 0) const SizedBox(height: 5),
            _ChecklistItemRow(
              key: ValueKey('checklist_item_$i'),
              item: items[i],
            ),
          ],
        ],
      ),
    );
  }
}

class _ChecklistItemRow extends StatelessWidget {
  final ChecklistItem item;

  const _ChecklistItemRow({
    super.key,
    required this.item,
  });

  @override
  Widget build(BuildContext context) {
    final (glyph, glyphColor, textColor, decoration, weight) =
        switch (item.status) {
      ChecklistStatus.done => (
          '✓',
          AppColors.statusEmerald,
          AppColors.textMuted,
          TextDecoration.lineThrough,
          FontWeight.w400,
        ),
      ChecklistStatus.inProgress => (
          '▶',
          AppColors.statusAmberLight,
          AppColors.textPrimary,
          TextDecoration.none,
          FontWeight.w600,
        ),
      ChecklistStatus.skipped => (
          '–',
          AppColors.textMuted,
          AppColors.textMuted,
          TextDecoration.lineThrough,
          FontWeight.w400,
        ),
      ChecklistStatus.failed => (
          '✕',
          AppColors.statusCoral,
          AppColors.statusCoral,
          TextDecoration.none,
          FontWeight.w500,
        ),
      ChecklistStatus.pending => (
          '○',
          AppColors.textMuted,
          AppColors.textSecondary,
          TextDecoration.none,
          FontWeight.w400,
        ),
    };

    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 16,
          child: Text(
            glyph,
            style: AppTypography.codeXs.copyWith(
              color: glyphColor,
              fontSize: 12,
              fontWeight: FontWeight.w700,
              height: 1.35,
            ),
          ),
        ),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            item.displayLabel,
            style: AppTypography.bodySmall.copyWith(
              color: textColor,
              decoration: decoration,
              decorationColor: AppColors.textMuted,
              fontWeight: weight,
              fontSize: 12,
              height: 1.35,
            ),
          ),
        ),
      ],
    );
  }
}
