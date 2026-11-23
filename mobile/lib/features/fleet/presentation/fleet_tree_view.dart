import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/host.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import 'fleet_session_card.dart';

/// Callback when a session card is tapped
typedef SessionTapCallback = void Function(Session session);

/// Callback when the '+' button on a folder is tapped
typedef FolderAddCallback = void Function(String folderPath);

/// Collapsible hierarchical tree view rendering multi-level project folders
/// and the 5 standard subsections per folder:
/// 1. ⚓ Supervisors
/// 2. 📋 Tasks
/// 3. 💻 Manual Sessions
/// 4. ⏳ Later
/// 5. ✅ Done
class FleetTreeView extends ConsumerWidget {
  final List<FleetTreeFolderNode> rootFolders;
  final List<HostRecord> hosts;
  final SessionTapCallback onSessionTap;
  final SessionTapCallback? onSessionActions;
  final FolderAddCallback onAddSession;
  final bool isSearching;
  final String? selectedSessionId;

  const FleetTreeView({
    super.key,
    required this.rootFolders,
    required this.hosts,
    required this.onSessionTap,
    this.onSessionActions,
    required this.onAddSession,
    this.isSearching = false,
    this.selectedSessionId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (rootFolders.isEmpty) {
      return const SizedBox.shrink();
    }

    final collapseState = ref.watch(fleetTreeCollapseProvider);
    final collapseNotifier = ref.read(fleetTreeCollapseProvider.notifier);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: rootFolders
          .map(
            (root) => _buildFolderBranch(
              root,
              collapseState,
              collapseNotifier,
            ),
          )
          .toList(),
    );
  }

  Widget _buildFolderBranch(
    FleetTreeFolderNode node,
    FleetTreeCollapseState collapseState,
    FleetTreeCollapseNotifier collapseNotifier,
  ) {
    final isCollapsed = collapseState.isFolderCollapsed(
      node.path,
      isSearching: isSearching,
    );
    final indent = node.depth * 14.0;

    return Padding(
      padding: EdgeInsets.only(left: indent, bottom: AppSpacing.sm),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            key: Key('fleet_folder_header_${node.path}'),
            onTap: () => collapseNotifier.toggleFolder(node.path),
            borderRadius: AppSpacing.roundedSm,
            child: Padding(
              padding:
                  const EdgeInsets.symmetric(vertical: 6.0, horizontal: 2.0),
              child: Row(
                children: [
                  Icon(
                    isCollapsed
                        ? Icons.folder_rounded
                        : Icons.folder_open_rounded,
                    size: 16,
                    color: AppColors.infoCyan,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      node.name.toUpperCase(),
                      style: AppTypography.codeXs.copyWith(
                        letterSpacing: 0.8,
                        fontWeight: FontWeight.w700,
                        color: AppColors.textPrimary,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  const SizedBox(width: 8),
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                    decoration: BoxDecoration(
                      color: AppColors.surfaceHighlight,
                      borderRadius: BorderRadius.circular(10),
                      border: Border.all(
                          color: AppColors.outlineSubtle, width: 0.5),
                    ),
                    child: Text(
                      '${node.totalCount}',
                      style: AppTypography.codeXs.copyWith(
                        fontSize: 9.5,
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  const SizedBox(width: 6),
                  InkWell(
                    onTap: () => onAddSession(node.path),
                    borderRadius: BorderRadius.circular(12),
                    child: const Padding(
                      padding: EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                      child: Icon(
                        Icons.add_circle_outline_rounded,
                        size: 16,
                        color: AppColors.infoCyan,
                      ),
                    ),
                  ),
                  const SizedBox(width: 4),
                  Icon(
                    isCollapsed
                        ? Icons.expand_more_rounded
                        : Icons.expand_less_rounded,
                    size: 18,
                    color: AppColors.textMuted,
                  ),
                ],
              ),
            ),
          ),
          if (!isCollapsed) ...[
            if (node.supervisors.isNotEmpty)
              _buildSubsection(
                subsectionKey: '${node.path}:supervisors',
                label: '⚓ Supervisors',
                color: AppColors.infoCyan,
                sessions: node.supervisors,
                collapseState: collapseState,
                collapseNotifier: collapseNotifier,
              ),

            if (node.tasks.isNotEmpty)
              _buildSubsection(
                subsectionKey: '${node.path}:tasks',
                label: '📋 Tasks',
                color: AppColors.statusEmerald,
                sessions: node.tasks,
                collapseState: collapseState,
                collapseNotifier: collapseNotifier,
              ),

            if (node.manualSessions.isNotEmpty)
              _buildSubsection(
                subsectionKey: '${node.path}:manual',
                label: '💻 Manual Sessions',
                color: AppColors.textSecondary,
                sessions: node.manualSessions,
                collapseState: collapseState,
                collapseNotifier: collapseNotifier,
              ),

            if (node.laterSessions.isNotEmpty)
              _buildSubsection(
                subsectionKey: '${node.path}:later',
                label: '⏳ Later',
                color: AppColors.statusAmber,
                sessions: node.laterSessions,
                collapseState: collapseState,
                collapseNotifier: collapseNotifier,
              ),

            if (node.doneSessions.isNotEmpty)
              _buildSubsection(
                subsectionKey: '${node.path}:done',
                label: '✅ Done',
                color: AppColors.textMuted,
                sessions: node.doneSessions,
                collapseState: collapseState,
                collapseNotifier: collapseNotifier,
              ),

            if (node.children.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 4.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: node.children
                      .map(
                        (child) => _buildFolderBranch(
                          child,
                          collapseState,
                          collapseNotifier,
                        ),
                      )
                      .toList(),
                ),
              ),

            // Empty Folder Hint
            if (node.isEmpty)
              Padding(
                padding:
                    const EdgeInsets.only(left: 12.0, top: 4.0, bottom: 8.0),
                child: Text(
                  'Empty group (tap + to launch a session)',
                  style: AppTypography.bodySmall.copyWith(
                    color: AppColors.textMuted,
                    fontStyle: FontStyle.italic,
                  ),
                ),
              ),
          ],
        ],
      ),
    );
  }

  Widget _buildSubsection({
    required String subsectionKey,
    required String label,
    required Color color,
    required List<Session> sessions,
    required FleetTreeCollapseState collapseState,
    required FleetTreeCollapseNotifier collapseNotifier,
  }) {
    final defaultCollapsed = subsectionKey.endsWith(':done');
    final isCollapsed = collapseState.isSubsectionCollapsed(
      subsectionKey,
      defaultCollapsed: defaultCollapsed,
      isSearching: isSearching,
    );

    return Padding(
      padding: const EdgeInsets.only(left: 8.0, top: 4.0, bottom: 6.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            key: Key('fleet_subsection_header_$subsectionKey'),
            onTap: () => collapseNotifier.toggleSubsection(
              subsectionKey,
              defaultCollapsed: defaultCollapsed,
            ),
            borderRadius: AppSpacing.roundedSm,
            child: Padding(
              padding:
                  const EdgeInsets.symmetric(vertical: 4.0, horizontal: 4.0),
              child: Row(
                children: [
                  Icon(
                    isCollapsed
                        ? Icons.chevron_right_rounded
                        : Icons.arrow_drop_down_rounded,
                    size: 16,
                    color: color,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    label,
                    style: AppTypography.codeXs.copyWith(
                      fontWeight: FontWeight.w600,
                      color: color,
                    ),
                  ),
                  const SizedBox(width: 6),
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                    decoration: BoxDecoration(
                      color: color.withOpacity(0.12),
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: Text(
                      '${sessions.length}',
                      style: AppTypography.codeXs.copyWith(
                        fontSize: 9.0,
                        color: color,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),

          // Session Cards
          if (!isCollapsed)
            Padding(
              padding: const EdgeInsets.only(top: 4.0),
              child: Column(
                children: sessions.map((s) => _buildSessionCard(s)).toList(),
              ),
            ),
        ],
      ),
    );
  }

  Widget _buildSessionCard(Session session) {
    return FleetSessionCard(
      session: session,
      hosts: hosts,
      onTap: onSessionTap,
      onActions: onSessionActions,
      selectedSessionId: selectedSessionId,
    );
  }
}

class FleetTreeEmptyState extends StatelessWidget {
  final bool hasHosts;
  final bool hasSessions;
  final VoidCallback onStartSession;

  const FleetTreeEmptyState({
    super.key,
    required this.hasHosts,
    required this.hasSessions,
    required this.onStartSession,
  });

  @override
  Widget build(BuildContext context) {
    final noH = !hasHosts, noS = !hasSessions;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(AppSpacing.xl),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              noH
                  ? Icons.dns_rounded
                  : (noS ? Icons.smart_toy_outlined : Icons.search_off_rounded),
              size: 36,
              color: AppColors.textMuted,
            ),
            AppSpacing.gapH12,
            Text(
              noH
                  ? 'No Hosts Connected'
                  : (noS
                      ? 'No Sessions on Connected Hosts'
                      : 'No matching sessions found'),
              style: AppTypography.titleMedium
                  .copyWith(color: AppColors.textSecondary),
            ),
            AppSpacing.gapH4,
            Text(
              noH
                  ? 'Add a host in the Hosts tab to see active agent sessions.'
                  : (noS
                      ? 'Pull down to refresh or launch a new agent session.'
                      : 'Try adjusting your search or tab filter.'),
              textAlign: TextAlign.center,
              style:
                  AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
            ),
            if (hasHosts && !hasSessions)
              Padding(
                padding: const EdgeInsets.only(top: AppSpacing.lg),
                child: ElevatedButton.icon(
                  onPressed: onStartSession,
                  icon: const Icon(Icons.rocket_launch_rounded, size: 16),
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
                    shape: RoundedRectangleBorder(
                        borderRadius: AppSpacing.roundedMd),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
