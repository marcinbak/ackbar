import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/work_task.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../plans/presentation/plans_screen.dart';
import 'task_card.dart';

enum WorkBoardViewMode { board, plans }

/// Main Work Board screen displaying task pillars (NEW, IN_PROGRESS, REVIEW, DONE),
/// top-level group filter chips, hosted deliverables, and PR merge actions.
class WorkBoardScreen extends ConsumerStatefulWidget {
  const WorkBoardScreen({super.key});

  @override
  ConsumerState<WorkBoardScreen> createState() => _WorkBoardScreenState();
}

class _WorkBoardScreenState extends ConsumerState<WorkBoardScreen> {
  WorkBoardViewMode _viewMode = WorkBoardViewMode.board;
  int _pillarIndex = 1; // Default to IN_PROGRESS (index 1)

  static const List<String> _pillarKeys = [
    'NEW',
    'IN_PROGRESS',
    'REVIEW',
    'DONE',
  ];

  static const List<String> _pillarLabels = [
    'New',
    'In Progress',
    'Review',
    'Done',
  ];

  @override
  void initState() {
    super.initState();
    Future.microtask(() {
      ref.read(tasksProvider.notifier).refreshTasks();
    });
  }

  @override
  Widget build(BuildContext context) {
    if (_viewMode == WorkBoardViewMode.plans) {
      return Scaffold(
        backgroundColor: AppColors.background,
        appBar: AckbarAppBar(
          title: 'PLANS & WORK',
          actions: [
            TextButton.icon(
              onPressed: () {
                setState(() => _viewMode = WorkBoardViewMode.board);
              },
              icon: const Icon(Icons.dashboard_outlined,
                  size: 16, color: AppColors.infoCyan),
              label: Text(
                'WORK BOARD',
                style: AppTypography.codeXs.copyWith(
                  fontWeight: FontWeight.w700,
                  color: AppColors.infoCyan,
                ),
              ),
            ),
          ],
        ),
        body: const PlansScreen(),
      );
    }

    final pillars = ref.watch(pillarTasksProvider);
    final groups = ref.watch(discoveredTaskGroupsProvider);
    final selectedGroup = ref.watch(taskGroupFilterProvider);
    final allTasks = ref.watch(tasksProvider);

    final newCount = pillars['NEW']?.length ?? 0;
    final inProgressCount = pillars['IN_PROGRESS']?.length ?? 0;
    final reviewCount = pillars['REVIEW']?.length ?? 0;
    final doneCount = pillars['DONE']?.length ?? 0;

    final filterTabs = [
      FilterTabItem(label: _pillarLabels[0], count: newCount),
      FilterTabItem(label: _pillarLabels[1], count: inProgressCount),
      FilterTabItem(
        label: _pillarLabels[2],
        count: reviewCount,
        countBadgeColor: reviewCount > 0 ? AppColors.statusAmber : null,
        countTextColor: reviewCount > 0 ? Colors.black : null,
      ),
      FilterTabItem(label: _pillarLabels[3], count: doneCount),
    ];

    final currentPillarKey = _pillarKeys[_pillarIndex];
    final currentPillarTasks = pillars[currentPillarKey] ?? <WorkTask>[];

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'WORK BOARD',
        actions: [
          TextButton.icon(
            onPressed: () {
              setState(() => _viewMode = WorkBoardViewMode.plans);
            },
            icon: const Icon(Icons.assignment_outlined,
                size: 16, color: AppColors.infoCyan),
            label: Text(
              'PLANS',
              style: AppTypography.codeXs.copyWith(
                fontWeight: FontWeight.w700,
                color: AppColors.infoCyan,
              ),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.refresh_rounded,
                size: 18, color: AppColors.infoCyan),
            tooltip: 'Refresh Tasks',
            onPressed: () => ref.read(tasksProvider.notifier).refreshTasks(),
          ),
        ],
      ),
      body: RefreshIndicator(
        color: AppColors.infoCyan,
        backgroundColor: AppColors.surfaceHighlight,
        onRefresh: () async {
          await ref.read(tasksProvider.notifier).refreshTasks();
        },
        child: CustomScrollView(
          physics: const BouncingScrollPhysics(
              parent: AlwaysScrollableScrollPhysics()),
          slivers: [
            // Group Filter Chips Row
            if (groups.length > 1)
              SliverToBoxAdapter(
                child: Container(
                  height: 44,
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  child: ListView.separated(
                    scrollDirection: Axis.horizontal,
                    padding:
                        const EdgeInsets.symmetric(horizontal: AppSpacing.lg),
                    itemCount: groups.length,
                    separatorBuilder: (_, __) => const SizedBox(width: 8),
                    itemBuilder: (context, index) {
                      final group = groups[index];
                      final isSelected =
                          selectedGroup.toLowerCase() == group.toLowerCase();
                      final label = group == 'all' ? 'All Groups' : group;

                      return ChoiceChip(
                        label: Text(label),
                        selected: isSelected,
                        onSelected: (_) {
                          ref.read(taskGroupFilterProvider.notifier).state =
                              group;
                        },
                        labelStyle: AppTypography.codeXs.copyWith(
                          color: isSelected
                              ? AppColors.terminalBlack
                              : AppColors.textSecondary,
                          fontWeight:
                              isSelected ? FontWeight.w700 : FontWeight.w500,
                        ),
                        selectedColor: AppColors.infoCyan,
                        backgroundColor: AppColors.surface,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(16),
                          side: BorderSide(
                            color: isSelected
                                ? AppColors.infoCyan
                                : AppColors.outlineSubtle,
                            width: 0.8,
                          ),
                        ),
                      );
                    },
                  ),
                ),
              ),

            // Horizontal Segmented Pillar Tabs
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: AppSpacing.sm),
                child: SegmentedFilterTabs(
                  tabs: filterTabs,
                  selectedIndex: _pillarIndex,
                  onTabSelected: (index) {
                    setState(() => _pillarIndex = index);
                  },
                ),
              ),
            ),

            // Task Cards List
            if (currentPillarTasks.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xl),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          allTasks.isEmpty
                              ? Icons.task_alt_rounded
                              : Icons.inbox_rounded,
                          size: 38,
                          color: AppColors.textMuted,
                        ),
                        AppSpacing.gapH12,
                        Text(
                          allTasks.isEmpty
                              ? 'No Tasks on Connected Hosts'
                              : 'No tasks in $_pillarLabels[$_pillarIndex]',
                          style: AppTypography.titleMedium
                              .copyWith(color: AppColors.textSecondary),
                        ),
                        AppSpacing.gapH4,
                        Text(
                          allTasks.isEmpty
                              ? 'Connect an ackbard daemon with tasks or run ackbar-tasks CLI to populate work items.'
                              : 'Tasks assigned to this pillar will appear here.',
                          textAlign: TextAlign.center,
                          style: AppTypography.bodySmall
                              .copyWith(color: AppColors.textMuted),
                        ),
                      ],
                    ),
                  ),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.symmetric(
                  horizontal: AppSpacing.lg,
                  vertical: AppSpacing.xs,
                ),
                sliver: SliverList(
                  delegate: SliverChildBuilderDelegate(
                    (context, index) =>
                        TaskCard(task: currentPillarTasks[index]),
                    childCount: currentPillarTasks.length,
                  ),
                ),
              ),

            const SliverToBoxAdapter(
              child: SizedBox(height: AppSpacing.xxl),
            ),
          ],
        ),
      ),
    );
  }
}
