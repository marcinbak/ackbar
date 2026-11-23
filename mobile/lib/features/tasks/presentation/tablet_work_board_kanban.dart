import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/responsive_breakpoints.dart';
import '../../../core/models/work_task.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import 'task_card.dart';

class TabletWorkBoardKanban extends ConsumerWidget {
  const TabletWorkBoardKanban({super.key});

  static const _pillars = [
    ('NEW', 'New', AppColors.textSecondary),
    ('IN_PROGRESS', 'In Progress', AppColors.infoCyan),
    ('REVIEW', 'Review', AppColors.statusAmber),
    ('DONE', 'Done', AppColors.statusEmerald),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pillars = ref.watch(pillarTasksProvider);
    final groups = ref.watch(discoveredTaskGroupsProvider);
    final selectedGroup = ref.watch(taskGroupFilterProvider);
    final isFourCol = ResponsiveBreakpoints.isFourColumnKanban(context);

    Widget buildColumn(String key, String label, Color accent) => Padding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.xs),
          child: _KanbanColumn(
            label: label,
            accent: accent,
            tasks: pillars[key] ?? const [],
            onRefresh: () => ref.read(tasksProvider.notifier).refreshTasks(),
          ),
        );

    return Column(
      children: [
        if (groups.length > 1)
          SizedBox(
            height: 44,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(
                  horizontal: AppSpacing.lg, vertical: 4),
              itemCount: groups.length,
              separatorBuilder: (_, __) => const SizedBox(width: 8),
              itemBuilder: (context, index) {
                final group = groups[index];
                final sel = selectedGroup.toLowerCase() == group.toLowerCase();
                return ChoiceChip(
                  label: Text(group == 'all' ? 'All Groups' : group),
                  selected: sel,
                  onSelected: (_) =>
                      ref.read(taskGroupFilterProvider.notifier).state = group,
                  labelStyle: AppTypography.codeXs.copyWith(
                    color:
                        sel ? AppColors.terminalBlack : AppColors.textSecondary,
                    fontWeight: sel ? FontWeight.w700 : FontWeight.w500,
                  ),
                  selectedColor: AppColors.infoCyan,
                  backgroundColor: AppColors.surface,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(16),
                    side: BorderSide(
                      color: sel ? AppColors.infoCyan : AppColors.outlineSubtle,
                      width: 0.8,
                    ),
                  ),
                );
              },
            ),
          ),
        Expanded(
          child: isFourCol
              ? Padding(
                  padding: const EdgeInsets.fromLTRB(AppSpacing.md,
                      AppSpacing.sm, AppSpacing.md, AppSpacing.md),
                  child: Row(
                    key: const Key('tablet_kanban_four_col'),
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      for (final (k, l, c) in _pillars)
                        Expanded(child: buildColumn(k, l, c)),
                    ],
                  ),
                )
              : LayoutBuilder(
                  builder: (context, constraints) {
                    final colW =
                        math.max(260.0, (constraints.maxWidth - 48) / 2);
                    return SingleChildScrollView(
                      key: const Key('tablet_kanban_two_col_scroll'),
                      scrollDirection: Axis.horizontal,
                      padding: const EdgeInsets.fromLTRB(AppSpacing.md,
                          AppSpacing.sm, AppSpacing.md, AppSpacing.md),
                      child: Row(
                        children: [
                          for (final (k, l, c) in _pillars)
                            SizedBox(
                              width: colW,
                              height: constraints.maxHeight - AppSpacing.lg,
                              child: buildColumn(k, l, c),
                            ),
                        ],
                      ),
                    );
                  },
                ),
        ),
      ],
    );
  }
}

class _KanbanColumn extends StatelessWidget {
  final String label;
  final Color accent;
  final List<WorkTask> tasks;
  final Future<void> Function() onRefresh;

  const _KanbanColumn({
    required this.label,
    required this.accent,
    required this.tasks,
    required this.onRefresh,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.surface.withOpacity(0.55),
        borderRadius: AppSpacing.roundedLg,
        border: Border.all(color: AppColors.outlineSubtle),
      ),
      child: Column(
        children: [
          Container(
            padding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.md, vertical: 10),
            decoration: const BoxDecoration(
              color: AppColors.surface,
              borderRadius: BorderRadius.vertical(
                  top: Radius.circular(AppSpacing.radiusLg)),
              border:
                  Border(bottom: BorderSide(color: AppColors.outlineSubtle)),
            ),
            child: Row(
              children: [
                Container(
                    width: 8,
                    height: 8,
                    decoration:
                        BoxDecoration(color: accent, shape: BoxShape.circle)),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(label,
                      style: AppTypography.titleSmall.copyWith(
                          color: AppColors.textPrimary,
                          fontWeight: FontWeight.w700),
                      overflow: TextOverflow.ellipsis),
                ),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
                  decoration: BoxDecoration(
                    color: accent.withOpacity(0.16),
                    borderRadius: BorderRadius.circular(10),
                  ),
                  child: Text('${tasks.length}',
                      style: AppTypography.codeXs.copyWith(
                          color: accent,
                          fontWeight: FontWeight.w700,
                          fontSize: 10)),
                ),
              ],
            ),
          ),
          Expanded(
            child: RefreshIndicator(
              color: AppColors.infoCyan,
              backgroundColor: AppColors.surfaceHighlight,
              onRefresh: onRefresh,
              child: tasks.isEmpty
                  ? ListView(
                      physics: const AlwaysScrollableScrollPhysics(),
                      children: [
                        Padding(
                          padding: const EdgeInsets.all(AppSpacing.xl),
                          child: Center(
                            child: Text('No tasks in $label',
                                style: AppTypography.bodySmall
                                    .copyWith(color: AppColors.textMuted),
                                textAlign: TextAlign.center),
                          ),
                        ),
                      ],
                    )
                  : ListView.builder(
                      padding: const EdgeInsets.all(AppSpacing.sm),
                      physics: const AlwaysScrollableScrollPhysics(),
                      itemCount: tasks.length,
                      itemBuilder: (_, i) => TaskCard(task: tasks[i]),
                    ),
            ),
          ),
        ],
      ),
    );
  }
}
