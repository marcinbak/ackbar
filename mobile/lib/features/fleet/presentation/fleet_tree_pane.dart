import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/navigation/session_navigator.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import 'fleet_tree_view.dart';
import 'new_session_sheet.dart';
import 'session_detail_sheet.dart';

class FleetTreePane extends ConsumerStatefulWidget {
  final String? selectedSessionId;
  final ValueChanged<Session>? onSessionTap;

  const FleetTreePane({super.key, this.selectedSessionId, this.onSessionTap});

  @override
  ConsumerState<FleetTreePane> createState() => _FleetTreePaneState();
}

class _FleetTreePaneState extends ConsumerState<FleetTreePane> {
  late final TextEditingController _search =
      TextEditingController(text: ref.read(fleetSearchQueryProvider));

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  BoxDecoration _box(double r) => BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(r),
        border: Border.all(color: AppColors.outlineSubtle),
      );

  @override
  Widget build(BuildContext context) {
    final all = ref.watch(fleetSessionsProvider);
    final tree = ref.watch(fleetTreeProvider);
    final mode = ref.watch(fleetViewModeProvider);
    final filter = ref.watch(fleetFilterIndexProvider);
    final query = ref.watch(fleetSearchQueryProvider);
    final hosts = ref.watch(hostsListProvider);
    final act = all.where((s) => !s.archived && !s.deleted && !s.isDone);
    final att = act.where((s) => s.isBlocked).length;
    int count(SessionState st) => act.where((s) => s.state == st).length;

    return RefreshIndicator(
      color: AppColors.infoCyan,
      backgroundColor: AppColors.surfaceHighlight,
      onRefresh: () => Future.wait([
        ref.read(hostsListProvider.notifier).refreshHosts(),
        ref.read(fleetSessionsProvider.notifier).refreshSessions(),
        ref.read(fleetNodesProvider.notifier).refreshNodes(),
      ]),
      child: CustomScrollView(
        physics: const BouncingScrollPhysics(
            parent: AlwaysScrollableScrollPhysics()),
        slivers: [
          SliverToBoxAdapter(
            child: Column(children: [
              _viewModeRow(mode),
              _searchField(),
              Padding(
                padding: const EdgeInsets.symmetric(vertical: AppSpacing.sm),
                child: SegmentedFilterTabs(
                  selectedIndex: filter,
                  onTabSelected: (i) =>
                      ref.read(fleetFilterIndexProvider.notifier).state = i,
                  tabs: [
                    FilterTabItem(label: 'All Sessions', count: act.length),
                    FilterTabItem(
                        label: 'Working', count: count(SessionState.working)),
                    FilterTabItem(
                      label: 'Attention',
                      count: att,
                      countBadgeColor: att > 0 ? AppColors.statusCoral : null,
                      countTextColor: att > 0 ? Colors.white : null,
                    ),
                    FilterTabItem(
                        label: 'Idle', count: count(SessionState.idle)),
                  ],
                ),
              ),
            ]),
          ),
          if (tree.isEmpty)
            SliverFillRemaining(
              hasScrollBody: false,
              child: FleetTreeEmptyState(
                hasHosts: hosts.isNotEmpty,
                hasSessions: all.isNotEmpty,
                onStartSession: () => NewSessionSheet.show(context),
              ),
            )
          else
            SliverPadding(
              padding: const EdgeInsets.symmetric(
                  horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
              sliver: SliverToBoxAdapter(
                child: FleetTreeView(
                  rootFolders: tree,
                  hosts: hosts,
                  isSearching: query.trim().isNotEmpty,
                  selectedSessionId: widget.selectedSessionId,
                  onSessionTap: widget.onSessionTap ??
                      (s) => SessionNavigator.openSession(context, ref, s),
                  onSessionActions: (s) => SessionDetailSheet.show(context, s),
                  onAddSession: (dir) {
                    final m = all
                        .where((s) => s.nodePath.startsWith(dir))
                        .firstOrNull;
                    NewSessionSheet.show(context,
                        prefillFolder: dir,
                        prefillCwd: m?.cwd,
                        prefillHost: m?.host);
                  },
                ),
              ),
            ),
          const SliverToBoxAdapter(child: SizedBox(height: AppSpacing.xxl)),
        ],
      ),
    );
  }

  Widget _viewModeRow(FleetViewMode mode) {
    Widget tab(FleetViewMode t, String label, String keyName) {
      final sel = mode == t, isSup = t == FleetViewMode.supervisors;
      return Expanded(
        child: InkWell(
          key: Key(keyName),
          onTap: () => ref.read(fleetViewModeProvider.notifier).state = t,
          borderRadius: BorderRadius.circular(7),
          child: Container(
            padding: const EdgeInsets.symmetric(vertical: 6),
            decoration: BoxDecoration(
              color: !sel
                  ? Colors.transparent
                  : (isSup
                      ? AppColors.infoCyan.withOpacity(0.18)
                      : AppColors.surfaceHighlight),
              borderRadius: BorderRadius.circular(7),
              border: sel && isSup
                  ? Border.all(
                      color: AppColors.infoCyan.withOpacity(0.4), width: 0.8)
                  : null,
            ),
            alignment: Alignment.center,
            child: Text(label,
                style: AppTypography.codeXs.copyWith(
                  fontWeight: sel ? FontWeight.w700 : FontWeight.w500,
                  color: !sel
                      ? AppColors.textMuted
                      : (isSup ? AppColors.infoCyan : AppColors.textPrimary),
                )),
          ),
        ),
      );
    }

    return Padding(
      padding: const EdgeInsets.fromLTRB(
          AppSpacing.lg, AppSpacing.sm, AppSpacing.lg, AppSpacing.xs),
      child: Container(
        padding: const EdgeInsets.all(3),
        decoration: _box(10),
        child: Row(children: [
          tab(FleetViewMode.all, '🌐 All Tree', 'fleet_view_mode_all'),
          tab(FleetViewMode.supervisors, '⚓ Supervisors',
              'fleet_view_mode_supervisors'),
        ]),
      ),
    );
  }

  Widget _searchField() => Padding(
        padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
        child: Container(
          height: 40,
          decoration: _box(AppSpacing.radiusMd),
          child: TextField(
            controller: _search,
            onChanged: (v) =>
                ref.read(fleetSearchQueryProvider.notifier).state = v,
            style:
                AppTypography.bodyMedium.copyWith(color: AppColors.textPrimary),
            decoration: InputDecoration(
              hintText: 'Filter sessions, groups, branches...',
              hintStyle:
                  AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
              prefixIcon: const Icon(Icons.search_rounded,
                  size: 18, color: AppColors.textMuted),
              suffixIcon: _search.text.isEmpty
                  ? null
                  : IconButton(
                      icon: const Icon(Icons.clear_rounded,
                          size: 16, color: AppColors.textMuted),
                      onPressed: () {
                        _search.clear();
                        ref.read(fleetSearchQueryProvider.notifier).state = '';
                      },
                    ),
              border: InputBorder.none,
              enabledBorder: InputBorder.none,
              focusedBorder: InputBorder.none,
              filled: false,
              contentPadding: const EdgeInsets.symmetric(vertical: 8),
            ),
          ),
        ),
      );
}
