import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/token_gauge_bar.dart';
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
  final Set<String> _collapsedFolders = {};

  @override
  void initState() {
    super.initState();
    Future.microtask(() {
      ref.read(fleetSessionsProvider.notifier).refreshSessions();
    });
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  void _toggleFolder(String folderName) {
    setState(() {
      if (_collapsedFolders.contains(folderName)) {
        _collapsedFolders.remove(folderName);
      } else {
        _collapsedFolders.add(folderName);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final allSessions = ref.watch(fleetSessionsProvider);
    final groupedSessions = ref.watch(groupedSessionsProvider);
    final filterIndex = ref.watch(fleetFilterIndexProvider);
    final hosts = ref.watch(hostsListProvider);

    final workingCount = allSessions.where((s) => s.state == SessionState.working).length;
    final attentionCount = allSessions.where((s) => s.isBlocked).length;
    final idleCount = allSessions.where((s) => s.state == SessionState.idle).length;

    final filterTabs = [
      FilterTabItem(label: 'All Sessions', count: allSessions.length),
      FilterTabItem(label: 'Working', count: workingCount),
      FilterTabItem(
        label: 'Attention',
        count: attentionCount,
        countBadgeColor: attentionCount > 0 ? AppColors.statusCoral : null,
        countTextColor: attentionCount > 0 ? Colors.white : null,
      ),
      FilterTabItem(label: 'Idle', count: idleCount),
    ];

    final hostIndicators = hosts.map((h) => h.toIndicator()).toList();

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'FLEET CONTROL',
        hosts: hostIndicators,
      ),
      body: RefreshIndicator(
        color: AppColors.infoCyan,
        backgroundColor: AppColors.surfaceHighlight,
        onRefresh: () async {
          await ref.read(hostsListProvider.notifier).refreshHosts();
          await ref.read(fleetSessionsProvider.notifier).refreshSessions();
        },
        child: CustomScrollView(
          physics: const BouncingScrollPhysics(parent: AlwaysScrollableScrollPhysics()),
          slivers: [
            // Search Filter Bar
            SliverToBoxAdapter(
              child: Padding(
                padding: const EdgeInsets.only(
                  left: AppSpacing.lg,
                  right: AppSpacing.lg,
                  top: AppSpacing.md,
                  bottom: AppSpacing.xs,
                ),
                child: Container(
                  height: 40,
                  decoration: BoxDecoration(
                    color: AppColors.surface,
                    borderRadius: AppSpacing.roundedMd,
                    border: Border.all(color: AppColors.outlineSubtle, width: 1),
                  ),
                  child: TextField(
                    controller: _searchController,
                    onChanged: (val) {
                      ref.read(fleetSearchQueryProvider.notifier).state = val;
                    },
                    style: AppTypography.bodyMedium.copyWith(color: AppColors.textPrimary),
                    decoration: InputDecoration(
                      hintText: 'Filter sessions, groups, branches...',
                      hintStyle: AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
                      prefixIcon: const Icon(Icons.search_rounded, size: 18, color: AppColors.textMuted),
                      suffixIcon: _searchController.text.isNotEmpty
                          ? IconButton(
                              icon: const Icon(Icons.clear_rounded, size: 16, color: AppColors.textMuted),
                              onPressed: () {
                                _searchController.clear();
                                ref.read(fleetSearchQueryProvider.notifier).state = '';
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
            if (groupedSessions.isEmpty)
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
                              : (allSessions.isEmpty ? Icons.smart_toy_outlined : Icons.search_off_rounded),
                          size: 36,
                          color: AppColors.textMuted,
                        ),
                        AppSpacing.gapH12,
                        Text(
                          hosts.isEmpty
                              ? 'No Hosts Connected'
                              : (allSessions.isEmpty ? 'No Sessions on Connected Hosts' : 'No matching sessions found'),
                          style: AppTypography.titleMedium.copyWith(color: AppColors.textSecondary),
                        ),
                        AppSpacing.gapH4,
                        Text(
                          hosts.isEmpty
                              ? 'Add a host in the Hosts tab to see active agent sessions.'
                              : (allSessions.isEmpty
                                  ? 'Pull down to refresh or start a session on your Mac.'
                                  : 'Try adjusting your search or tab filter.'),
                          textAlign: TextAlign.center,
                          style: AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
                        ),
                      ],
                    ),
                  ),
                ),
              )
            else
            SliverPadding(
              padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
              sliver: SliverList(
                delegate: SliverChildBuilderDelegate(
                  (context, index) {
                    final folderName = groupedSessions.keys.elementAt(index);
                    final sessionsInFolder = groupedSessions[folderName]!;
                    final isCollapsed = _collapsedFolders.contains(folderName);

                    return Padding(
                      padding: const EdgeInsets.only(bottom: AppSpacing.lg),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          // Collapsible Folder Header
                          InkWell(
                            onTap: () => _toggleFolder(folderName),
                            borderRadius: AppSpacing.roundedSm,
                            child: Padding(
                              padding: const EdgeInsets.symmetric(vertical: 6.0, horizontal: 2.0),
                              child: Row(
                                children: [
                                  Icon(
                                    isCollapsed ? Icons.folder_rounded : Icons.folder_open_rounded,
                                    size: 16,
                                    color: AppColors.infoCyan,
                                  ),
                                  const SizedBox(width: 8),
                                  Text(
                                    folderName.toUpperCase(),
                                    style: AppTypography.codeXs.copyWith(
                                      letterSpacing: 0.8,
                                      fontWeight: FontWeight.w700,
                                      color: AppColors.textPrimary,
                                    ),
                                  ),
                                  const SizedBox(width: 8),
                                  Container(
                                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                                    decoration: BoxDecoration(
                                      color: AppColors.surfaceHighlight,
                                      borderRadius: BorderRadius.circular(10),
                                      border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                                    ),
                                    child: Text(
                                      '${sessionsInFolder.length}',
                                      style: AppTypography.codeXs.copyWith(
                                        fontSize: 9.5,
                                        color: AppColors.infoCyan,
                                        fontWeight: FontWeight.w700,
                                      ),
                                    ),
                                  ),
                                  const Spacer(),
                                  Icon(
                                    isCollapsed ? Icons.expand_more_rounded : Icons.expand_less_rounded,
                                    size: 18,
                                    color: AppColors.textMuted,
                                  ),
                                ],
                              ),
                            ),
                          ),
                          AppSpacing.gapH6,

                          // Session cards inside folder
                          if (!isCollapsed)
                            ...sessionsInFolder.map((session) {
                              return Padding(
                                padding: const EdgeInsets.only(bottom: AppSpacing.md),
                                child: _buildSessionCard(session),
                              );
                            }),
                        ],
                      ),
                    );
                  },
                  childCount: groupedSessions.length,
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

  Widget _buildSessionCard(Session session) {
    final statusColor = session.state.toBadgeStatus().color;

    return GlassCard(
      accentColor: statusColor,
      onTap: () => SessionDetailSheet.show(context, session),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Top Tags & Status Row
          Row(
            children: [
              // Host Tag
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                ),
                child: Text(
                  session.hostTag,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 6),

              // Agent Badge
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: session.agentColor.withOpacity(0.12),
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: session.agentColor.withOpacity(0.3), width: 0.8),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(session.agentSymbol, style: const TextStyle(fontSize: 10)),
                    const SizedBox(width: 4),
                    Text(
                      session.agentDisplayName,
                      style: AppTypography.codeXs.copyWith(
                        color: session.agentColor,
                        fontWeight: FontWeight.w600,
                        fontSize: 10,
                      ),
                    ),
                  ],
                ),
              ),

              if (session.gitBranch.isNotEmpty) ...[
                const SizedBox(width: 6),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                  decoration: BoxDecoration(
                    color: AppColors.surfaceHighlight,
                    borderRadius: AppSpacing.roundedSm,
                  ),
                  child: Text(
                    session.gitBranch,
                    style: AppTypography.codeXs.copyWith(
                      color: AppColors.textSecondary,
                      fontSize: 10,
                    ),
                  ),
                ),
              ],

              const Spacer(),
              StatusBadge(status: session.state.toBadgeStatus(), isCompact: true),
            ],
          ),
          AppSpacing.gapH8,

          // Session title
          Text(
            session.displayTitle,
            style: AppTypography.titleMedium.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w600,
            ),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
          AppSpacing.gapH6,

          // Activity Line
          if (session.activity.isNotEmpty)
            Row(
              children: [
                Icon(
                  session.isBlocked ? Icons.help_outline_rounded : Icons.bolt_rounded,
                  size: 13,
                  color: statusColor,
                ),
                const SizedBox(width: 4),
                Expanded(
                  child: Text(
                    session.activity,
                    style: AppTypography.codeXs.copyWith(
                      color: session.isBlocked ? AppColors.statusCoral : AppColors.textSecondary,
                      fontSize: 11,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
          AppSpacing.gapH6,

          // Directory path & runtime clock
          Row(
            children: [
              const Icon(Icons.folder_outlined, size: 12, color: AppColors.textMuted),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  session.cwd,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textMuted,
                    fontSize: 10.5,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                '⏱ ${session.timeElapsedFormatted}',
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.textSecondary,
                  fontSize: 10.5,
                ),
              ),
            ],
          ),
          AppSpacing.gapH12,

          // Gradient Token / Context Usage Bar
          TokenGaugeBar(
            percentage: session.contextPct / 100.0,
            isCompact: true,
          ),
        ],
      ),
    );
  }
}
