import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../../core/widgets/status_badge.dart';
import 'tablet_attention_detail_pane.dart';

class TabletAttentionView extends ConsumerStatefulWidget {
  const TabletAttentionView({super.key});

  @override
  ConsumerState<TabletAttentionView> createState() =>
      _TabletAttentionViewState();
}

class _TabletAttentionViewState extends ConsumerState<TabletAttentionView> {
  String? _selectedId;

  @override
  Widget build(BuildContext context) {
    final pending = ref.watch(attentionPendingSessionsProvider);
    final inProgress = ref.watch(inProgressSessionsProvider);
    final history = ref.watch(decisionAuditProvider);
    final filter = ref.watch(attentionFilterIndexProvider);

    final tabs = [
      FilterTabItem(
        label: '🔴 Pending',
        count: pending.length,
        countBadgeColor: pending.isNotEmpty ? AppColors.statusCoral : null,
        countTextColor: pending.isNotEmpty ? Colors.white : null,
      ),
      FilterTabItem(label: '⚡ In-Progress', count: inProgress.length),
      FilterTabItem(label: 'History', count: history.length),
    ];

    final selPending = pending.isEmpty
        ? null
        : pending.firstWhere((x) => x.id == _selectedId,
            orElse: () => pending.first);
    final selInProg = inProgress.isEmpty
        ? null
        : inProgress.firstWhere((x) => x.id == _selectedId,
            orElse: () => inProgress.first);
    final selHist = history.isEmpty
        ? null
        : history.firstWhere((x) => x.id == _selectedId,
            orElse: () => history.first);

    return Row(
      key: const Key('tablet_attention_split_view'),
      children: [
        Container(
          width: 320,
          decoration: const BoxDecoration(
            color: AppColors.background,
            border: Border(right: BorderSide(color: AppColors.outlineSubtle)),
          ),
          child: Column(
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(vertical: AppSpacing.sm),
                child: SegmentedFilterTabs(
                  tabs: tabs,
                  selectedIndex: filter,
                  padding:
                      const EdgeInsets.symmetric(horizontal: AppSpacing.md),
                  onTabSelected: (i) =>
                      ref.read(attentionFilterIndexProvider.notifier).state = i,
                ),
              ),
              Expanded(
                child: _leftList(
                  filter,
                  pending,
                  inProgress,
                  history,
                  selPending?.id,
                  selInProg?.id,
                  selHist?.id,
                ),
              ),
            ],
          ),
        ),
        Expanded(
          child: TabletAttentionDetailPane(
            filterIndex: filter,
            selectedPending: selPending,
            selectedInProgress: selInProg,
            selectedHistory: selHist,
          ),
        ),
      ],
    );
  }

  Widget _leftList(
    int filter,
    List<Session> pending,
    List<Session> inProgress,
    List<DecisionAuditEntry> history,
    String? activePendingId,
    String? activeInProgId,
    String? activeHistId,
  ) {
    if (filter == 0) {
      if (pending.isEmpty) return _emptyList('All caught up!');
      return ListView.builder(
        padding: const EdgeInsets.all(AppSpacing.md),
        itemCount: pending.length,
        itemBuilder: (_, i) => _sessionTile(pending[i],
            pending[i].id == activePendingId, AppColors.statusCoral),
      );
    }
    if (filter == 1) {
      if (inProgress.isEmpty) return _emptyList('No active tasks.');
      return ListView.builder(
        padding: const EdgeInsets.all(AppSpacing.md),
        itemCount: inProgress.length,
        itemBuilder: (_, i) => _sessionTile(inProgress[i],
            inProgress[i].id == activeInProgId, AppColors.statusAmber),
      );
    }
    if (history.isEmpty) return _emptyList('No decision records.');
    return ListView.builder(
      padding: const EdgeInsets.all(AppSpacing.md),
      itemCount: history.length,
      itemBuilder: (_, i) {
        final e = history[i];
        final sel = e.id == activeHistId;
        return Padding(
          padding: const EdgeInsets.only(bottom: AppSpacing.sm),
          child: GlassCard(
            accentColor: AppColors.infoCyan,
            borderColor: sel ? AppColors.infoCyan : null,
            backgroundColor: sel ? AppColors.infoCyan.withOpacity(0.1) : null,
            onTap: () => setState(() => _selectedId = e.id),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(e.actionType.toUpperCase(),
                    style: AppTypography.codeXs.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w700)),
                AppSpacing.gapH4,
                Text(e.sessionTitle,
                    style: AppTypography.titleSmall,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis),
                Text(e.summary,
                    style: AppTypography.bodySmall
                        .copyWith(color: AppColors.textMuted),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis),
              ],
            ),
          ),
        );
      },
    );
  }

  Widget _sessionTile(Session s, bool selected, Color accent) {
    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.sm),
      child: GlassCard(
        accentColor: accent,
        borderColor: selected ? AppColors.infoCyan : null,
        backgroundColor: selected ? AppColors.infoCyan.withOpacity(0.10) : null,
        onTap: () => setState(() => _selectedId = s.id),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(children: [
              AgentLogo(agent: s.agent, size: 11, color: s.agentColor),
              const SizedBox(width: 5),
              Expanded(
                child: Text('${s.agentDisplayName} • ${s.hostTag}',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textSecondary),
                    overflow: TextOverflow.ellipsis),
              ),
              if (s.blocked != null)
                StatusBadge(
                    status: AckbarSessionStatus.blocked,
                    customLabel: s.blocked!.kind.label,
                    isCompact: true),
            ]),
            AppSpacing.gapH6,
            Text(s.displayTitle,
                style: AppTypography.titleSmall
                    .copyWith(fontWeight: FontWeight.w700),
                maxLines: 1,
                overflow: TextOverflow.ellipsis),
            if (s.blocked?.reason.isNotEmpty == true || s.activity.isNotEmpty)
              Text(s.blocked?.reason ?? s.activity,
                  style: AppTypography.bodySmall
                      .copyWith(color: AppColors.textMuted, fontSize: 11),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis),
          ],
        ),
      ),
    );
  }

  Widget _emptyList(String msg) => Center(
        child: Padding(
          padding: const EdgeInsets.all(AppSpacing.xl),
          child: Text(msg,
              style:
                  AppTypography.bodySmall.copyWith(color: AppColors.textMuted)),
        ),
      );
}
