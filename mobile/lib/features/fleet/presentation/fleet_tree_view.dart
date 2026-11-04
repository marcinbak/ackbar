import 'package:flutter/material.dart';
import '../../../core/models/host.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';

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
class FleetTreeView extends StatefulWidget {
  final List<FleetTreeFolderNode> rootFolders;
  final List<HostRecord> hosts;
  final SessionTapCallback onSessionTap;
  final FolderAddCallback onAddSession;

  const FleetTreeView({
    super.key,
    required this.rootFolders,
    required this.hosts,
    required this.onSessionTap,
    required this.onAddSession,
  });

  @override
  State<FleetTreeView> createState() => _FleetTreeViewState();
}

class _FleetTreeViewState extends State<FleetTreeView> {
  final Set<String> _collapsedFolders = {};
  final Set<String> _collapsedSubsections = {};

  void _toggleFolder(String path) {
    setState(() {
      if (_collapsedFolders.contains(path)) {
        _collapsedFolders.remove(path);
      } else {
        _collapsedFolders.add(path);
      }
    });
  }

  void _toggleSubsection(String key) {
    setState(() {
      if (_collapsedSubsections.contains(key)) {
        _collapsedSubsections.remove(key);
      } else {
        _collapsedSubsections.add(key);
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    if (widget.rootFolders.isEmpty) {
      return const SizedBox.shrink();
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children:
          widget.rootFolders.map((root) => _buildFolderBranch(root)).toList(),
    );
  }

  Widget _buildFolderBranch(FleetTreeFolderNode node) {
    final isCollapsed = _collapsedFolders.contains(node.path);
    final indent = node.depth * 14.0;

    return Padding(
      padding: EdgeInsets.only(left: indent, bottom: AppSpacing.sm),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Folder Header
          InkWell(
            onTap: () => _toggleFolder(node.path),
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
                    onTap: () => widget.onAddSession(node.path),
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

          // Folder Contents (if expanded)
          if (!isCollapsed) ...[
            // 1. ⚓ Supervisors
            if (node.supervisors.isNotEmpty)
              _buildSubsection(
                key: '${node.path}:supervisors',
                label: '⚓ Supervisors',
                color: AppColors.infoCyan,
                sessions: node.supervisors,
              ),

            // 2. 📋 Tasks
            if (node.tasks.isNotEmpty)
              _buildSubsection(
                key: '${node.path}:tasks',
                label: '📋 Tasks',
                color: AppColors.statusEmerald,
                sessions: node.tasks,
              ),

            // 3. 💻 Manual Sessions
            if (node.manualSessions.isNotEmpty)
              _buildSubsection(
                key: '${node.path}:manual',
                label: '💻 Manual Sessions',
                color: AppColors.textSecondary,
                sessions: node.manualSessions,
              ),

            // 4. ⏳ Later
            if (node.laterSessions.isNotEmpty)
              _buildSubsection(
                key: '${node.path}:later',
                label: '⏳ Later',
                color: AppColors.statusAmber,
                sessions: node.laterSessions,
              ),

            // 5. ✅ Done
            if (node.doneSessions.isNotEmpty)
              _buildSubsection(
                key: '${node.path}:done',
                label: '✅ Done',
                color: AppColors.textMuted,
                sessions: node.doneSessions,
              ),

            // Child Folders (recursive)
            if (node.children.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 4.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: node.children
                      .map((child) => _buildFolderBranch(child))
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
    required String key,
    required String label,
    required Color color,
    required List<Session> sessions,
  }) {
    final isCollapsed = _collapsedSubsections.contains(key);

    return Padding(
      padding: const EdgeInsets.only(left: 8.0, top: 4.0, bottom: 6.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Subsection Toggle Header
          InkWell(
            onTap: () => _toggleSubsection(key),
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
    final statusColor = session.state.toBadgeStatus().color;
    final isOffline = isSessionHostOffline(session, widget.hosts);

    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.sm),
      child: GlassCard(
        accentColor: statusColor,
        onTap: () => widget.onSessionTap(session),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Top Tags Row
            Row(
              children: [
                // Host Tag
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(
                    color: AppColors.surfaceHighlight,
                    borderRadius: AppSpacing.roundedSm,
                    border:
                        Border.all(color: AppColors.outlineSubtle, width: 0.5),
                  ),
                  child: Text(
                    session.hostTag,
                    style: AppTypography.codeXs.copyWith(
                      color: AppColors.infoCyan,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),

                // Offline badge if host is offline
                if (isOffline) ...[
                  const SizedBox(width: 6),
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                    decoration: BoxDecoration(
                      color: AppColors.statusCoral.withOpacity(0.15),
                      borderRadius: AppSpacing.roundedSm,
                      border: Border.all(
                        color: AppColors.statusCoral.withOpacity(0.4),
                        width: 0.8,
                      ),
                    ),
                    child: Text(
                      'offline',
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.statusCoral,
                        fontWeight: FontWeight.w700,
                        fontSize: 9.5,
                      ),
                    ),
                  ),
                ],

                // Supervisor Tag if session is supervisor
                if (session.isSupervisor) ...[
                  const SizedBox(width: 6),
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                    decoration: BoxDecoration(
                      color: AppColors.infoCyan.withOpacity(0.15),
                      borderRadius: AppSpacing.roundedSm,
                      border: Border.all(
                        color: AppColors.infoCyan.withOpacity(0.4),
                        width: 0.8,
                      ),
                    ),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Text('⚓', style: TextStyle(fontSize: 10)),
                        const SizedBox(width: 3),
                        Text(
                          'SUPERVISOR',
                          style: AppTypography.codeXs.copyWith(
                            color: AppColors.infoCyan,
                            fontWeight: FontWeight.w700,
                            fontSize: 9.5,
                          ),
                        ),
                      ],
                    ),
                  ),
                ],

                const SizedBox(width: 6),

                // Agent Badge
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                  decoration: BoxDecoration(
                    color: session.agentColor.withOpacity(0.12),
                    borderRadius: AppSpacing.roundedSm,
                    border: Border.all(
                      color: session.agentColor.withOpacity(0.3),
                      width: 0.8,
                    ),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      AgentLogo(
                        agent: session.agent,
                        size: 10,
                        color: session.agentColor,
                      ),
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
                    padding:
                        const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
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
                if (session.isUnread) ...[
                  Container(
                    width: 8,
                    height: 8,
                    margin: const EdgeInsets.only(right: 6),
                    decoration: const BoxDecoration(
                      color: AppColors.infoCyan,
                      shape: BoxShape.circle,
                      boxShadow: [
                        BoxShadow(
                          color: Color(0x9906B6D4),
                          blurRadius: 6,
                          spreadRadius: 1,
                        ),
                      ],
                    ),
                  ),
                ],
                StatusBadge(
                    status: session.state.toBadgeStatus(), isCompact: true),
              ],
            ),
            AppSpacing.gapH8,

            // Session Title
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
                    session.isBlocked
                        ? Icons.help_outline_rounded
                        : Icons.bolt_rounded,
                    size: 13,
                    color: statusColor,
                  ),
                  const SizedBox(width: 4),
                  Expanded(
                    child: Text(
                      session.activity,
                      style: AppTypography.codeXs.copyWith(
                        color: session.isBlocked
                            ? AppColors.statusCoral
                            : AppColors.textSecondary,
                        fontSize: 11,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
          ],
        ),
      ),
    );
  }
}
