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

class FleetSessionCard extends StatelessWidget {
  final Session session;
  final List<HostRecord> hosts;
  final void Function(Session session) onTap;
  final void Function(Session session)? onActions;

  const FleetSessionCard({
    super.key,
    required this.session,
    required this.hosts,
    required this.onTap,
    this.onActions,
  });

  @override
  Widget build(BuildContext context) {
    final statusColor = session.state.toBadgeStatus().color;
    final isOffline = isSessionHostOffline(session, hosts);

    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.sm),
      child: GlassCard(
        accentColor: statusColor,
        onTap: () => onTap(session),
        onLongPress: onActions != null ? () => onActions!(session) : null,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
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
                  status: session.state.toBadgeStatus(),
                  isCompact: true,
                ),
              ],
            ),
            AppSpacing.gapH8,
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
