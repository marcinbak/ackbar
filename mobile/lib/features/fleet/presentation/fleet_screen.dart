import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../../core/navigation/session_navigator.dart';
import 'fleet_tree_view.dart';
import 'new_session_sheet.dart';
import 'session_detail_sheet.dart';

/// Screen displaying the active fleet of agent sessions across all supervised machines,
/// organized into collapsible project folders matching Stitch design 08dc7df6aacf41529b1914ff96ce7c83.
class FleetScreen extends ConsumerStatefulWidget {
  const FleetScreen({super.key});

  @override
  ConsumerState<FleetScreen> createState() => _FleetScreenState();
}

class _FleetScreenState extends ConsumerState<FleetScreen> {
  final TextEditingController _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    Future.microtask(() {
      ref.read(hostsListProvider.notifier).refreshHosts();
      ref.read(fleetSessionsProvider.notifier).refreshSessions();
      ref.read(fleetNodesProvider.notifier).refreshNodes();
    });
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _spawnSupervisor() async {
    final scaffoldMessenger = ScaffoldMessenger.of(context);
    scaffoldMessenger.showSnackBar(
      const SnackBar(
        content: Text('Spawning zero-input supervisor...'),
        duration: Duration(seconds: 1),
      ),
    );
    final spawned = await ref
        .read(fleetSessionsProvider.notifier)
        .spawnZeroInputSupervisor();
    if (spawned != null && mounted) {
      await SessionNavigator.openSession(context, ref, spawned);
    }
  }

  @override
  Widget build(BuildContext context) {
    final allSessions = ref.watch(fleetSessionsProvider);
    final activeSessions = allSessions.where((s) => !s.archived).toList();
    final fleetTree = ref.watch(fleetTreeProvider);
    final viewMode = ref.watch(fleetViewModeProvider);
    final filterIndex = ref.watch(fleetFilterIndexProvider);
    final hosts = ref.watch(hostsListProvider);

    final workingCount =
        activeSessions.where((s) => s.state == SessionState.working).length;
    final attentionCount = activeSessions.where((s) => s.isBlocked).length;
    final idleCount =
        activeSessions.where((s) => s.state == SessionState.idle).length;

    final filterTabs = [
      FilterTabItem(label: 'All Sessions', count: activeSessions.length),
      FilterTabItem(label: 'Working', count: workingCount),
      FilterTabItem(
        label: 'Attention',
        count: attentionCount,
        countBadgeColor: attentionCount > 0 ? AppColors.statusCoral : null,
        countTextColor: attentionCount > 0 ? Colors.white : null,
      ),
      FilterTabItem(label: 'Idle', count: idleCount),
    ];

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'FLEET CONTROL',
        actions: [
          IconButton(
            icon: const Text(
              '⚓+',
              style: TextStyle(
                fontSize: 15,
                fontWeight: FontWeight.bold,
                color: AppColors.infoCyan,
              ),
            ),
            tooltip: '1-Tap New Supervisor',
            onPressed: _spawnSupervisor,
          ),
          IconButton(
            icon: const Icon(Icons.add_circle_outline_rounded,
                size: 20, color: AppColors.infoCyan),
            tooltip: 'Launch New Session',
            onPressed: () => NewSessionSheet.show(context),
          ),
        ],
      ),
      body: RefreshIndicator(
        color: AppColors.infoCyan,
        backgroundColor: AppColors.surfaceHighlight,
        onRefresh: () async {
          await ref.read(hostsListProvider.notifier).refreshHosts();
          await ref.read(fleetSessionsProvider.notifier).refreshSessions();
          await ref.read(fleetNodesProvider.notifier).refreshNodes();
        },
        child: CustomScrollView(
          physics: const BouncingScrollPhysics(
              parent: AlwaysScrollableScrollPhysics()),
          slivers: [
            // View Mode Switcher: All Tree vs Supervisors
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.only(
                  left: AppSpacing.lg,
                  right: AppSpacing.lg,
                  top: AppSpacing.sm,
                  bottom: AppSpacing.xs,
                ),
                child: Container(
                  padding: const EdgeInsets.all(3),
                  decoration: BoxDecoration(
                    color: AppColors.surface,
                    borderRadius: BorderRadius.circular(10),
                    border:
                        Border.all(color: AppColors.outlineSubtle, width: 1),
                  ),
                  child: Row(
                    children: [
                      Expanded(
                        child: InkWell(
                          key: const Key('fleet_view_mode_all'),
                          onTap: () {
                            ref.read(fleetViewModeProvider.notifier).state =
                                FleetViewMode.all;
                          },
                          borderRadius: BorderRadius.circular(7),
                          child: Container(
                            padding: const EdgeInsets.symmetric(vertical: 6),
                            decoration: BoxDecoration(
                              color: viewMode == FleetViewMode.all
                                  ? AppColors.surfaceHighlight
                                  : Colors.transparent,
                              borderRadius: BorderRadius.circular(7),
                            ),
                            alignment: Alignment.center,
                            child: Text(
                              '🌐 All Tree',
                              style: AppTypography.codeXs.copyWith(
                                fontWeight: viewMode == FleetViewMode.all
                                    ? FontWeight.w700
                                    : FontWeight.w500,
                                color: viewMode == FleetViewMode.all
                                    ? AppColors.textPrimary
                                    : AppColors.textMuted,
                              ),
                            ),
                          ),
                        ),
                      ),
                      Expanded(
                        child: InkWell(
                          key: const Key('fleet_view_mode_supervisors'),
                          onTap: () {
                            ref.read(fleetViewModeProvider.notifier).state =
                                FleetViewMode.supervisors;
                          },
                          borderRadius: BorderRadius.circular(7),
                          child: Container(
                            padding: const EdgeInsets.symmetric(vertical: 6),
                            decoration: BoxDecoration(
                              color: viewMode == FleetViewMode.supervisors
                                  ? AppColors.infoCyan.withOpacity(0.18)
                                  : Colors.transparent,
                              borderRadius: BorderRadius.circular(7),
                              border: viewMode == FleetViewMode.supervisors
                                  ? Border.all(
                                      color:
                                          AppColors.infoCyan.withOpacity(0.4),
                                      width: 0.8)
                                  : null,
                            ),
                            alignment: Alignment.center,
                            child: Text(
                              '⚓ Supervisors',
                              style: AppTypography.codeXs.copyWith(
                                fontWeight:
                                    viewMode == FleetViewMode.supervisors
                                        ? FontWeight.w700
                                        : FontWeight.w500,
                                color: viewMode == FleetViewMode.supervisors
                                    ? AppColors.infoCyan
                                    : AppColors.textMuted,
                              ),
                            ),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),

            // Search Filter Bar
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.only(
                  left: AppSpacing.lg,
                  right: AppSpacing.lg,
                  top: AppSpacing.xs,
                  bottom: AppSpacing.xs,
                ),
                child: Container(
                  height: 40,
                  decoration: BoxDecoration(
                    color: AppColors.surface,
                    borderRadius: AppSpacing.roundedMd,
                    border:
                        Border.all(color: AppColors.outlineSubtle, width: 1),
                  ),
                  child: TextField(
                    controller: _searchController,
                    onChanged: (val) {
                      ref.read(fleetSearchQueryProvider.notifier).state = val;
                    },
                    style: AppTypography.bodyMedium
                        .copyWith(color: AppColors.textPrimary),
                    decoration: InputDecoration(
                      hintText: 'Filter sessions, groups, branches...',
                      hintStyle: AppTypography.bodySmall
                          .copyWith(color: AppColors.textMuted),
                      prefixIcon: const Icon(Icons.search_rounded,
                          size: 18, color: AppColors.textMuted),
                      suffixIcon: _searchController.text.isNotEmpty
                          ? IconButton(
                              icon: const Icon(Icons.clear_rounded,
                                  size: 16, color: AppColors.textMuted),
                              onPressed: () {
                                _searchController.clear();
                                ref
                                    .read(fleetSearchQueryProvider.notifier)
                                    .state = '';
                              },
                            )
                          : null,
                      border: InputBorder.none,
                      enabledBorder: InputBorder.none,
                      focusedBorder: InputBorder.none,
                      filled: false,
                      contentPadding: const EdgeInsets.symmetric(vertical: 8),
                    ),
                  ),
                ),
              ),
            ),

            // Horizontal Segmented Filter Tabs
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: AppSpacing.sm),
                child: SegmentedFilterTabs(
                  tabs: filterTabs,
                  selectedIndex: filterIndex,
                  onTabSelected: (index) {
                    ref.read(fleetFilterIndexProvider.notifier).state = index;
                  },
                ),
              ),
            ),

            // Grouped Project Folders & Sessions List
            if (fleetTree.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.xl),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          hosts.isEmpty
                              ? Icons.dns_rounded
                              : (allSessions.isEmpty
                                  ? Icons.smart_toy_outlined
                                  : Icons.search_off_rounded),
                          size: 36,
                          color: AppColors.textMuted,
                        ),
                        AppSpacing.gapH12,
                        Text(
                          hosts.isEmpty
                              ? 'No Hosts Connected'
                              : (allSessions.isEmpty
                                  ? 'No Sessions on Connected Hosts'
                                  : 'No matching sessions found'),
                          style: AppTypography.titleMedium
                              .copyWith(color: AppColors.textSecondary),
                        ),
                        AppSpacing.gapH4,
                        Text(
                          hosts.isEmpty
                              ? 'Add a host in the Hosts tab to see active agent sessions.'
                              : (allSessions.isEmpty
                                  ? 'Pull down to refresh or launch a new agent session.'
                                  : 'Try adjusting your search or tab filter.'),
                          textAlign: TextAlign.center,
                          style: AppTypography.bodySmall
                              .copyWith(color: AppColors.textMuted),
                        ),
                        if (hosts.isNotEmpty && allSessions.isEmpty) ...[
                          AppSpacing.gapH16,
                          ElevatedButton.icon(
                            onPressed: () => NewSessionSheet.show(context),
                            icon: const Icon(Icons.rocket_launch_rounded,
                                size: 16),
                            label: Text(
                              'START FIRST SESSION',
                              style: AppTypography.codeXs.copyWith(
                                fontWeight: FontWeight.w700,
                                color: AppColors.terminalBlack,
                              ),
                            ),
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.infoCyan,
                              foregroundColor: AppColors.terminalBlack,
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 16, vertical: 10),
                              shape: RoundedRectangleBorder(
                                  borderRadius: AppSpacing.roundedMd),
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.symmetric(
                    horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
                sliver: SliverToBoxAdapter(
                  child: FleetTreeView(
                    rootFolders: fleetTree,
                    hosts: hosts,
                    onSessionTap: (session) =>
                        SessionNavigator.openSession(context, ref, session),
                    onSessionActions: (session) =>
                        SessionDetailSheet.show(context, session),
                    onAddSession: (folderPath) {
                      final matchingSession = allSessions
                          .where((s) => s.nodePath.startsWith(folderPath))
                          .firstOrNull;
                      NewSessionSheet.show(
                        context,
                        prefillFolder: folderPath,
                        prefillCwd: matchingSession?.cwd,
                        prefillHost: matchingSession?.host,
                      );
                    },
                  ),
                ),
              ),

            const SliverToBoxAdapter(
              child: SizedBox(height: AppSpacing.xxl),
            ),
          ],
        ),
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => NewSessionSheet.show(context),
        backgroundColor: AppColors.infoCyan,
        foregroundColor: AppColors.terminalBlack,
        icon: const Icon(Icons.add_rounded, size: 20),
        label: Text(
          'NEW SESSION',
          style: AppTypography.codeXs.copyWith(
            color: AppColors.terminalBlack,
            fontWeight: FontWeight.w800,
            letterSpacing: 0.6,
          ),
        ),
      ),
    );
  }
}
