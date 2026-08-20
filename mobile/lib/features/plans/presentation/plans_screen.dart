import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/plan.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../../core/widgets/status_badge.dart';
import 'plan_diff_viewer_modal.dart';

/// Screen listing all architectural plans and implementation proposals matching Stitch design fb386a3002f649cbad9fac40663c8ac9.
class PlansScreen extends ConsumerWidget {
  const PlansScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final plans = ref.watch(plansListProvider);
    final filterIndex = ref.watch(plansFilterIndexProvider);
    final hosts = ref.watch(hostsListProvider);

    final pendingPlans = plans.where((p) => p.isPendingReview).toList();
    final inProgressPlans = plans.where((p) => p.isInProgress).toList();
    final approvedPlans = plans.where((p) => p.isApproved).toList();
    final completedPlans = plans.where((p) => p.isCompleted).toList();

    final activeCount = pendingPlans.length + inProgressPlans.length;

    final filterTabs = [
      FilterTabItem(
        label: 'Active',
        count: activeCount,
        countBadgeColor: pendingPlans.isNotEmpty ? AppColors.infoCyan : null,
      ),
      FilterTabItem(
        label: 'Approved',
        count: approvedPlans.length,
      ),
      FilterTabItem(
        label: 'History',
        count: completedPlans.length,
      ),
    ];

    final hostIndicators = hosts.map((h) => h.toIndicator()).toList();

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'PLANS & PROPOSALS',
        hosts: hostIndicators,
      ),
      body: CustomScrollView(
        physics: const BouncingScrollPhysics(parent: AlwaysScrollableScrollPhysics()),
        slivers: [
          // Filter Tabs Header
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: AppSpacing.sm),
              child: SegmentedFilterTabs(
                tabs: filterTabs,
                selectedIndex: filterIndex,
                onTabSelected: (idx) {
                  ref.read(plansFilterIndexProvider.notifier).state = idx;
                },
              ),
            ),
          ),

          // Tab 0: Active (Pending Review + In-Progress + Recently Completed)
          if (filterIndex == 0) ...[
            // PENDING REVIEW SECTION
            if (pendingPlans.isNotEmpty) ...[
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
                  child: Row(
                    children: [
                      const Icon(Icons.rate_review_outlined, size: 14, color: AppColors.statusAmber),
                      const SizedBox(width: 6),
                      Text(
                        'PENDING REVIEW (${pendingPlans.length})',
                        style: AppTypography.codeXs.copyWith(
                          letterSpacing: 0.8,
                          fontWeight: FontWeight.w700,
                          color: AppColors.statusAmber,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final plan = pendingPlans[index];
                      return Padding(
                        padding: const EdgeInsets.only(bottom: AppSpacing.md),
                        child: _buildPendingReviewCard(context, ref, plan),
                      );
                    },
                    childCount: pendingPlans.length,
                  ),
                ),
              ),
            ],

            // IN PROGRESS SECTION
            if (inProgressPlans.isNotEmpty) ...[
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.only(
                    left: AppSpacing.lg,
                    right: AppSpacing.lg,
                    top: AppSpacing.md,
                    bottom: AppSpacing.xs,
                  ),
                  child: Row(
                    children: [
                      const Icon(Icons.bolt_rounded, size: 14, color: AppColors.infoCyan),
                      const SizedBox(width: 6),
                      Text(
                        'IN PROGRESS (${inProgressPlans.length})',
                        style: AppTypography.codeXs.copyWith(
                          letterSpacing: 0.8,
                          fontWeight: FontWeight.w700,
                          color: AppColors.infoCyan,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final plan = inProgressPlans[index];
                      return Padding(
                        padding: const EdgeInsets.only(bottom: AppSpacing.md),
                        child: _buildInProgressPlanCard(context, ref, plan),
                      );
                    },
                    childCount: inProgressPlans.length,
                  ),
                ),
              ),
            ],

            // RECENTLY COMPLETED SECTION
            if (completedPlans.isNotEmpty) ...[
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.only(
                    left: AppSpacing.lg,
                    right: AppSpacing.lg,
                    top: AppSpacing.md,
                    bottom: AppSpacing.xs,
                  ),
                  child: Row(
                    children: [
                      const Icon(Icons.check_circle_outline_rounded, size: 14, color: AppColors.statusEmerald),
                      const SizedBox(width: 6),
                      Text(
                        'RECENTLY COMPLETED (${completedPlans.length})',
                        style: AppTypography.codeXs.copyWith(
                          letterSpacing: 0.8,
                          fontWeight: FontWeight.w700,
                          color: AppColors.statusEmerald,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final plan = completedPlans[index];
                      return Padding(
                        padding: const EdgeInsets.only(bottom: AppSpacing.md),
                        child: _buildCompletedPlanCard(context, plan),
                      );
                    },
                    childCount: completedPlans.length,
                  ),
                ),
              ),
            ],
          ]

          // Tab 1: Approved Plans
          else if (filterIndex == 1) ...[
            if (approvedPlans.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Text(
                    'No currently approved plans waiting execution.',
                    style: AppTypography.bodyMedium.copyWith(color: AppColors.textMuted),
                  ),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.sm),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final plan = approvedPlans[index];
                      return Padding(
                        padding: const EdgeInsets.only(bottom: AppSpacing.md),
                        child: _buildApprovedPlanCard(context, plan),
                      );
                    },
                    childCount: approvedPlans.length,
                  ),
                ),
              ),
          ]

          // Tab 2: History (Completed)
          else ...[
            if (completedPlans.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Text(
                    'No completed plan proposals in history.',
                    style: AppTypography.bodyMedium.copyWith(color: AppColors.textMuted),
                  ),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.sm),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) {
                      final plan = completedPlans[index];
                      return Padding(
                        padding: const EdgeInsets.only(bottom: AppSpacing.md),
                        child: _buildCompletedPlanCard(context, plan),
                      );
                    },
                    childCount: completedPlans.length,
                  ),
                ),
              ),
          ],

          const SliverToBoxAdapter(
            child: SizedBox(height: AppSpacing.xxl),
          ),
        ],
      ),
    );
  }

  Widget _buildPendingReviewCard(BuildContext context, WidgetRef ref, PlanDocument plan) {
    return GlassCard(
      accentColor: AppColors.statusAmber,
      onTap: () => PlanDiffViewerModal.show(context, plan),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.infoCyan.withOpacity(0.4), width: 0.5),
                ),
                child: Text(
                  plan.id,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                '${plan.agentDisplayName} @${plan.host}',
                style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
              ),
              const Spacer(),
              const StatusBadge(
                status: AckbarSessionStatus.blocked,
                customLabel: 'PENDING REVIEW',
                isCompact: true,
              ),
            ],
          ),
          AppSpacing.gapH8,

          Text(
            plan.title,
            style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w700),
          ),
          AppSpacing.gapH6,

          Text(
            plan.goal,
            style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
          AppSpacing.gapH8,

          if (plan.userReviewCallout.isNotEmpty) ...[
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
              decoration: BoxDecoration(
                color: AppColors.statusAmber.withOpacity(0.12),
                borderRadius: AppSpacing.roundedSm,
                border: Border.all(color: AppColors.statusAmber.withOpacity(0.3), width: 0.5),
              ),
              child: Text(
                plan.userReviewCallout,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.statusAmberLight,
                  fontSize: 10.5,
                ),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ),
            AppSpacing.gapH8,
          ],

          Row(
            children: [
              const Icon(Icons.edit_document, size: 12, color: AppColors.textMuted),
              const SizedBox(width: 4),
              Text('${plan.filesChanged} files', style: AppTypography.codeXs),
              const SizedBox(width: 8),
              Text(
                plan.linesDeltaFormatted,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.statusEmerald,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          AppSpacing.gapH12,

          Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: () => PlanDiffViewerModal.show(context, plan),
                  icon: const Icon(Icons.difference_rounded, size: 15),
                  label: const Text('View Diff'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.infoCyan,
                    side: const BorderSide(color: AppColors.outlineSubtle),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: ElevatedButton.icon(
                  onPressed: () {
                    ref.read(plansListProvider.notifier).quickApprovePlan(plan.id);
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                        content: Text('Approved ${plan.id}'),
                        backgroundColor: AppColors.statusEmerald,
                      ),
                    );
                  },
                  icon: const Icon(Icons.flash_on_rounded, size: 15),
                  label: const Text('Quick Approve'),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppColors.statusEmerald,
                    foregroundColor: AppColors.terminalBlack,
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildInProgressPlanCard(BuildContext context, WidgetRef ref, PlanDocument plan) {
    final pctInt = (plan.progressPct * 100).toInt();

    return GlassCard(
      accentColor: AppColors.infoCyan,
      onTap: () => PlanDiffViewerModal.show(context, plan),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                plan.id,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.infoCyan,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(width: 8),
              Text('${plan.agentDisplayName} @${plan.host}', style: AppTypography.codeXs),
              const Spacer(),
              const StatusBadge(
                status: AckbarSessionStatus.working,
                customLabel: 'EXECUTING',
                isCompact: true,
              ),
            ],
          ),
          AppSpacing.gapH8,

          Text(
            plan.title,
            style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w700),
          ),
          AppSpacing.gapH6,

          if (plan.currentStep.isNotEmpty) ...[
            Text(
              plan.currentStep,
              style: AppTypography.codeXs.copyWith(color: AppColors.statusAmberLight),
            ),
            AppSpacing.gapH8,
          ],

          // Progress Bar
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'PROGRESS',
                style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
              ),
              Text(
                '$pctInt% Completed',
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.infoCyan,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          ClipRRect(
            borderRadius: BorderRadius.circular(3),
            child: LinearProgressIndicator(
              value: plan.progressPct,
              backgroundColor: AppColors.surfaceHighlight,
              valueColor: const AlwaysStoppedAnimation<Color>(AppColors.infoCyan),
              minHeight: 6,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildApprovedPlanCard(BuildContext context, PlanDocument plan) {
    return GlassCard(
      accentColor: AppColors.statusEmerald,
      onTap: () => PlanDiffViewerModal.show(context, plan),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                plan.id,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.statusEmerald,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(width: 8),
              Text('${plan.agentDisplayName} @${plan.host}', style: AppTypography.codeXs),
              const Spacer(),
              const StatusBadge(
                status: AckbarSessionStatus.active,
                customLabel: 'APPROVED',
                isCompact: true,
              ),
            ],
          ),
          AppSpacing.gapH8,
          Text(
            plan.title,
            style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w700),
          ),
          AppSpacing.gapH4,
          Text(
            plan.goal,
            style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
    );
  }

  Widget _buildCompletedPlanCard(BuildContext context, PlanDocument plan) {
    return GlassCard(
      accentColor: AppColors.statusEmerald,
      onTap: () => PlanDiffViewerModal.show(context, plan),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                plan.id,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.statusEmerald,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(width: 8),
              Text('${plan.agentDisplayName} @${plan.host}', style: AppTypography.codeXs),
              const Spacer(),
              const StatusBadge(
                status: AckbarSessionStatus.idle,
                customLabel: 'COMPLETED',
                isCompact: true,
              ),
            ],
          ),
          AppSpacing.gapH8,
          Text(
            plan.title,
            style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w700),
          ),
          AppSpacing.gapH4,
          if (plan.testSummary.isNotEmpty) ...[
            Text(
              plan.testSummary,
              style: AppTypography.codeXs.copyWith(color: AppColors.statusEmerald),
            ),
            AppSpacing.gapH4,
          ],
          Text(
            plan.goal,
            style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
    );
  }
}
