import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import '../../../core/models/work_task.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';

/// Modal dialog displaying hosted Markdown deliverables (plans, retrospectives, review findings)
class DeliverableMarkdownModal extends StatelessWidget {
  final TaskDeliverable deliverable;

  const DeliverableMarkdownModal({super.key, required this.deliverable});

  static Future<void> show(BuildContext context, TaskDeliverable deliverable) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => SizedBox(
        height: MediaQuery.of(context).size.height * 0.85,
        child: ClipRRect(
          borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
          child: DeliverableMarkdownModal(deliverable: deliverable),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final title = deliverable.title.isNotEmpty
        ? deliverable.title
        : '${deliverable.providerDisplayName} Deliverable';
    final content = deliverable.contentMd.isNotEmpty
        ? deliverable.contentMd
        : (deliverable.url.isNotEmpty
            ? 'Deliverable link: [${deliverable.url}](${deliverable.url})'
            : 'No markdown content available for this deliverable.');

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AppBar(
        backgroundColor: AppColors.surface,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.close_rounded, color: AppColors.textPrimary),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Row(
          children: [
            Text(deliverable.providerIcon,
                style: const TextStyle(fontSize: 16)),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                title,
                style: AppTypography.titleMedium
                    .copyWith(color: AppColors.textPrimary),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
        actions: [
          Container(
            margin: const EdgeInsets.only(right: 16),
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            decoration: BoxDecoration(
              color: AppColors.surfaceHighlight,
              borderRadius: AppSpacing.roundedSm,
              border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
            ),
            child: Text(
              deliverable.providerDisplayName,
              style: AppTypography.codeXs.copyWith(
                color: AppColors.infoCyan,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
        ],
      ),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.md),
          child: Markdown(
            data: content,
            selectable: true,
            styleSheet:
                MarkdownStyleSheet.fromTheme(Theme.of(context)).copyWith(
              p: AppTypography.bodyMedium
                  .copyWith(color: AppColors.textPrimary),
              h1: AppTypography.titleLarge.copyWith(color: AppColors.infoCyan),
              h2: AppTypography.titleMedium
                  .copyWith(color: AppColors.textPrimary),
              h3: AppTypography.titleSmall
                  .copyWith(color: AppColors.textSecondary),
              code: AppTypography.codeXs.copyWith(
                backgroundColor: AppColors.surfaceHighlight,
                color: AppColors.infoCyan,
              ),
              codeblockDecoration: BoxDecoration(
                color: AppColors.surface,
                borderRadius: AppSpacing.roundedMd,
                border: Border.all(color: AppColors.outlineSubtle, width: 0.8),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
