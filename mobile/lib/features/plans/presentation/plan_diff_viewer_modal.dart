import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/plan.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';

/// Modal dialog/sheet displaying detailed architectural proposals, verification checklists,
/// and syntax-highlighted code diffs matching Stitch design e8fd4bece32f4e348f33e6640aef68b8.
class PlanDiffViewerModal extends ConsumerWidget {
  final PlanDocument plan;

  const PlanDiffViewerModal({super.key, required this.plan});

  static Future<void> show(BuildContext context, PlanDocument plan) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => PlanDiffViewerModal(plan: plan),
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      height: MediaQuery.of(context).size.height * 0.90,
      decoration: const BoxDecoration(
        color: AppColors.background,
        borderRadius: BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXl)),
        border: Border(
          top: BorderSide(color: AppColors.outline, width: 1.5),
        ),
      ),
      child: Column(
        children: [
          // Header Bar with Drag Handle
          Padding(
            padding: const EdgeInsets.only(top: 10, left: AppSpacing.lg, right: AppSpacing.lg, bottom: 8),
            child: Column(
              children: [
                Center(
                  child: Container(
                    width: 40,
                    height: 4,
                    decoration: BoxDecoration(
                      color: AppColors.outline,
                      borderRadius: BorderRadius.circular(2),
                    ),
                  ),
                ),
                AppSpacing.gapH8,
                Row(
                  children: [
                    Text(
                      plan.id,
                      style: AppTypography.codeSm.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                      decoration: BoxDecoration(
                        color: AppColors.surfaceHighlight,
                        borderRadius: AppSpacing.roundedSm,
                      ),
                      child: Text(
                        '${plan.agentDisplayName} @${plan.host}',
                        style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                      ),
                    ),
                    const Spacer(),
                    StatusBadge(
                      status: plan.status.toBadgeStatus(),
                      customLabel: plan.status.label,
                      isCompact: true,
                    ),
                    IconButton(
                      icon: const Icon(Icons.close_rounded, size: 20),
                      color: AppColors.textMuted,
                      onPressed: () => Navigator.of(context).pop(),
                    ),
                  ],
                ),
              ],
            ),
          ),
          const Divider(height: 1, color: AppColors.outlineSubtle),

          // Scrollable Body
          Expanded(
            child: ListView(
              padding: AppSpacing.paddingScreen,
              physics: const BouncingScrollPhysics(),
              children: [
                // Plan Title
                Text(
                  plan.title,
                  style: AppTypography.headlineMedium.copyWith(
                    fontWeight: FontWeight.w700,
                    color: AppColors.textPrimary,
                  ),
                ),
                AppSpacing.gapH8,

                // Goal Summary Box
                GlassCard(
                  padding: AppSpacing.paddingCardDense,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'ARCHITECTURAL GOAL',
                        style: AppTypography.codeXs.copyWith(
                          color: AppColors.infoCyan,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      const SizedBox(height: 4),
                      Text(
                        plan.goal,
                        style: AppTypography.bodySmall.copyWith(
                          color: AppColors.textPrimary,
                          height: 1.45,
                        ),
                      ),
                    ],
                  ),
                ),
                AppSpacing.gapH12,

                // Human Review Callout (if active)
                if (plan.userReviewCallout.isNotEmpty) ...[
                  Container(
                    padding: AppSpacing.paddingCardDense,
                    decoration: BoxDecoration(
                      color: AppColors.statusAmber.withOpacity(0.12),
                      borderRadius: AppSpacing.roundedMd,
                      border: Border.all(color: AppColors.statusAmber.withOpacity(0.4), width: 1),
                    ),
                    child: Text(
                      plan.userReviewCallout,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.statusAmberLight,
                        fontWeight: FontWeight.w600,
                        fontSize: 11,
                      ),
                    ),
                  ),
                  AppSpacing.gapH12,
                ],

                // Files Changed & Delta stats
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                      decoration: BoxDecoration(
                        color: AppColors.surface,
                        borderRadius: AppSpacing.roundedSm,
                        border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.edit_document, size: 13, color: AppColors.textMuted),
                          const SizedBox(width: 5),
                          Text(
                            '${plan.filesChanged} Files Changed',
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.textPrimary,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                      decoration: BoxDecoration(
                        color: AppColors.statusEmerald.withOpacity(0.1),
                        borderRadius: AppSpacing.roundedSm,
                      ),
                      child: Text(
                        '+${plan.addedLines}',
                        style: AppTypography.codeXs.copyWith(
                          color: AppColors.statusEmerald,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                    const SizedBox(width: 6),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                      decoration: BoxDecoration(
                        color: AppColors.statusCoral.withOpacity(0.1),
                        borderRadius: AppSpacing.roundedSm,
                      ),
                      child: Text(
                        '-${plan.deletedLines}',
                        style: AppTypography.codeXs.copyWith(
                          color: AppColors.statusCoral,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                  ],
                ),
                AppSpacing.gapH16,

                // Verification Steps Checklist
                if (plan.verificationSteps.isNotEmpty) ...[
                  Text(
                    'VERIFICATION STEPS',
                    style: AppTypography.codeXs.copyWith(
                      letterSpacing: 0.8,
                      fontWeight: FontWeight.w700,
                      color: AppColors.textSecondary,
                    ),
                  ),
                  AppSpacing.gapH8,
                  GlassCard(
                    padding: AppSpacing.paddingCardDense,
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: plan.verificationSteps.map((step) {
                        return Padding(
                          padding: const EdgeInsets.symmetric(vertical: 4.0),
                          child: Row(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              const Icon(
                                Icons.check_circle_outline_rounded,
                                size: 15,
                                color: AppColors.statusEmerald,
                              ),
                              const SizedBox(width: 8),
                              Expanded(
                                child: Text(
                                  step,
                                  style: AppTypography.codeSm.copyWith(
                                    fontSize: 11.5,
                                    color: AppColors.textPrimary,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        );
                      }).toList(),
                    ),
                  ),
                  AppSpacing.gapH16,
                ],

                // Syntax Highlighted Diff Viewer
                Text(
                  'PROPOSED CODE DIFF',
                  style: AppTypography.codeXs.copyWith(
                    letterSpacing: 0.8,
                    fontWeight: FontWeight.w700,
                    color: AppColors.textSecondary,
                  ),
                ),
                AppSpacing.gapH8,
                _buildDiffViewer(plan.diffContent),
                AppSpacing.gapH24,
              ],
            ),
          ),

          // Floating Bottom Approval Bar
          Container(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.md),
            decoration: const BoxDecoration(
              color: AppColors.surface,
              border: Border(top: BorderSide(color: AppColors.outlineSubtle, width: 1)),
            ),
            child: SafeArea(
              top: false,
              child: Row(
                children: [
                  Expanded(
                    child: OutlinedButton(
                      onPressed: () => Navigator.of(context).pop(),
                      style: OutlinedButton.styleFrom(
                        foregroundColor: AppColors.textSecondary,
                        side: const BorderSide(color: AppColors.outlineSubtle),
                      ),
                      child: const Text('Close'),
                    ),
                  ),
                  const SizedBox(width: AppSpacing.md),
                  Expanded(
                    flex: 2,
                    child: ElevatedButton.icon(
                      onPressed: () {
                        ref.read(plansListProvider.notifier).quickApprovePlan(plan.id);
                        Navigator.of(context).pop();
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text('Approved ${plan.id}: ${plan.title}'),
                            backgroundColor: AppColors.statusEmerald,
                          ),
                        );
                      },
                      icon: const Icon(Icons.check_circle_rounded, size: 16),
                      label: const Text('Approve Plan'),
                      style: ElevatedButton.styleFrom(
                        backgroundColor: AppColors.statusEmerald,
                        foregroundColor: AppColors.terminalBlack,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildDiffViewer(String diffText) {
    if (diffText.isEmpty) {
      return Container(
        padding: AppSpacing.paddingCardDense,
        decoration: BoxDecoration(
          color: AppColors.terminalBlack,
          borderRadius: AppSpacing.roundedMd,
        ),
        child: Text(
          'No code changes recorded for this proposal.',
          style: AppTypography.codeSm.copyWith(color: AppColors.textMuted),
        ),
      );
    }

    final lines = diffText.split('\n');

    return Container(
      decoration: BoxDecoration(
        color: AppColors.terminalBlack,
        borderRadius: AppSpacing.roundedMd,
        border: Border.all(color: AppColors.outlineSubtle, width: 1),
      ),
      clipBehavior: Clip.antiAlias,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: lines.map((line) {
          Color bgColor = Colors.transparent;
          Color textColor = AppColors.textSecondary;
          FontWeight fontWeight = FontWeight.w400;

          if (line.startsWith('+') && !line.startsWith('+++')) {
            bgColor = AppColors.statusEmerald.withOpacity(0.14);
            textColor = const Color(0xFF34D399); // Light Emerald
            fontWeight = FontWeight.w500;
          } else if (line.startsWith('-') && !line.startsWith('---')) {
            bgColor = AppColors.statusCoral.withOpacity(0.14);
            textColor = const Color(0xFFF87171); // Light Coral
            fontWeight = FontWeight.w500;
          } else if (line.startsWith('@@')) {
            bgColor = AppColors.infoCyan.withOpacity(0.08);
            textColor = AppColors.infoCyan;
            fontWeight = FontWeight.w600;
          } else if (line.startsWith('diff --git') || line.startsWith('index ')) {
            textColor = AppColors.textMuted;
            fontWeight = FontWeight.w700;
          }

          return Container(
            color: bgColor,
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 2.5),
            child: Text(
              line.isEmpty ? ' ' : line,
              style: AppTypography.codeSm.copyWith(
                color: textColor,
                fontSize: 11.5,
                fontWeight: fontWeight,
                height: 1.35,
              ),
            ),
          );
        }).toList(),
      ),
    );
  }
}
