import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/host.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';
import 'add_host_dialog.dart';

/// Screen managing supervised daemon hosts, Tailscale mesh connectivity, and remote updates.
/// Matches Stitch design 62dd13edfa6146e188cf245314f06d59.
class HostsScreen extends ConsumerWidget {
  const HostsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hosts = ref.watch(hostsListProvider);
    final sessions = ref.watch(fleetSessionsProvider);

    final onlineCount = hosts.where((h) => h.online).length;
    final hostIndicators = hosts.map((h) => h.toIndicator()).toList();

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'DAEMON HOSTS',
        hosts: hostIndicators,
      ),
      body: ListView(
        padding: AppSpacing.paddingScreen,
        physics: const BouncingScrollPhysics(),
        children: [
          // Section Header Row with Add Host Button
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'SUPERVISED NODES ($onlineCount ONLINE)',
                style: AppTypography.codeXs.copyWith(
                  letterSpacing: 0.8,
                  fontWeight: FontWeight.w700,
                  color: AppColors.textSecondary,
                ),
              ),
              InkWell(
                onTap: () => AddHostDialog.show(context),
                borderRadius: AppSpacing.roundedSm,
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
                  child: Row(
                    children: [
                      const Icon(Icons.add_rounded, size: 14, color: AppColors.infoCyan),
                      const SizedBox(width: 4),
                      Text(
                        'ADD HOST',
                        style: AppTypography.codeXs.copyWith(
                          color: AppColors.infoCyan,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
          AppSpacing.gapH12,

          // Tailnet Mesh Status Banner
          Container(
            padding: AppSpacing.paddingCardDense,
            decoration: BoxDecoration(
              color: AppColors.infoCyan.withOpacity(0.08),
              borderRadius: AppSpacing.roundedLg,
              border: Border.all(color: AppColors.infoCyan.withOpacity(0.3), width: 1),
            ),
            child: Row(
              children: [
                Container(
                  padding: const EdgeInsets.all(8),
                  decoration: BoxDecoration(
                    color: AppColors.infoCyan.withOpacity(0.15),
                    borderRadius: AppSpacing.roundedSm,
                  ),
                  child: const Text('🌐', style: TextStyle(fontSize: 16)),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Text(
                            'Tailnet: my-dev-mesh.ts.net',
                            style: AppTypography.titleSmall.copyWith(
                              color: AppColors.textPrimary,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                          const SizedBox(width: 6),
                          Container(
                            width: 6,
                            height: 6,
                            decoration: const BoxDecoration(
                              color: AppColors.statusEmerald,
                              shape: BoxShape.circle,
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 2),
                      Text(
                        'Encrypted WireGuard mesh active across ${hosts.length} nodes • Direct P2P tunnel',
                        style: AppTypography.bodySmall.copyWith(
                          color: AppColors.textSecondary,
                          fontSize: 11,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
          AppSpacing.gapH16,

          // Host Cards List
          ...hosts.map((host) {
            final activeSessionsCount = sessions.where((s) => s.host == host.name).length;
            return Padding(
              padding: const EdgeInsets.only(bottom: AppSpacing.md),
              child: _buildHostCard(context, ref, host, activeSessionsCount),
            );
          }),
        ],
      ),
    );
  }

  Widget _buildHostCard(BuildContext context, WidgetRef ref, HostRecord host, int activeSessionsCount) {
    return GlassCard(
      accentColor: host.statusColor,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Top Row
          Row(
            children: [
              Icon(
                host.isLocal ? Icons.laptop_mac_rounded : Icons.dns_rounded,
                size: 18,
                color: host.online ? AppColors.infoCyan : AppColors.textMuted,
              ),
              const SizedBox(width: 8),
              Text(
                host.name,
                style: AppTypography.titleMedium.copyWith(
                  fontWeight: FontWeight.w700,
                  color: AppColors.textPrimary,
                ),
              ),
              const SizedBox(width: 8),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                ),
                child: Text(
                  host.endpointDisplay,
                  style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                ),
              ),
              const Spacer(),
              StatusBadge(
                status: host.badgeStatus,
                customLabel: host.online ? 'ONLINE' : 'UNREACHABLE',
                isCompact: true,
              ),
            ],
          ),
          AppSpacing.gapH8,

          // Tailscale Mesh Tag
          if (host.tailscaleIp.isNotEmpty) ...[
            Row(
              children: [
                const Icon(Icons.shield_outlined, size: 12, color: AppColors.textMuted),
                const SizedBox(width: 4),
                Text(
                  'Tailscale IP: ${host.tailscaleIp}',
                  style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
                ),
              ],
            ),
            AppSpacing.gapH8,
          ],

          // Metrics Grid
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
            decoration: BoxDecoration(
              color: AppColors.surfaceHighlight.withOpacity(0.4),
              borderRadius: AppSpacing.roundedSm,
              border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
            ),
            child: Row(
              children: [
                _buildMetric('Active Sessions', '$activeSessionsCount'),
                _buildMetric('Latency', host.online ? host.latencyDisplay : '—'),
                _buildMetric('Daemon Ver', host.version),
                _buildMetric('Uptime', host.uptime),
              ],
            ),
          ),
          AppSpacing.gapH12,

          // Action Buttons Row
          Row(
            children: [
              _buildHostActionButton(
                context: context,
                icon: Icons.refresh_rounded,
                label: 'Rescan',
                onTap: () async {
                  await ref.read(apiClientProvider).purgeSessions(host.url);
                  ScaffoldMessenger.of(context).showSnackBar(
                    SnackBar(
                      content: Text('Rescanning ${host.name}...'),
                      backgroundColor: AppColors.surfaceHighlight,
                    ),
                  );
                },
              ),
              const SizedBox(width: 6),
              _buildHostActionButton(
                context: context,
                icon: Icons.article_outlined,
                label: 'Logs',
                onTap: () => _showLogsModal(context, host),
              ),
              const SizedBox(width: 6),
              _buildHostActionButton(
                context: context,
                icon: Icons.system_update_alt_rounded,
                label: 'Update Daemon',
                onTap: () {
                  ScaffoldMessenger.of(context).showSnackBar(
                    SnackBar(
                      content: Text('Daemon on ${host.name} is up to date (v0.2.1)'),
                      backgroundColor: AppColors.statusEmerald,
                    ),
                  );
                },
              ),
              const SizedBox(width: 6),
              _buildHostActionButton(
                context: context,
                icon: Icons.settings_outlined,
                label: 'Settings',
                onTap: () {
                  ScaffoldMessenger.of(context).showSnackBar(
                    SnackBar(
                      content: Text('Host ${host.name}: ${host.remoteCwd}'),
                      backgroundColor: AppColors.surfaceHighlight,
                    ),
                  );
                },
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildMetric(String label, String value) {
    return Expanded(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: AppTypography.codeXs.copyWith(color: AppColors.textMuted, fontSize: 9.5),
          ),
          const SizedBox(height: 2),
          Text(
            value,
            style: AppTypography.codeSm.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w700,
              fontSize: 12,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildHostActionButton({
    required BuildContext context,
    required IconData icon,
    required String label,
    required VoidCallback onTap,
  }) {
    return Expanded(
      child: Material(
        color: AppColors.surface,
        shape: RoundedRectangleBorder(
          borderRadius: AppSpacing.roundedSm,
          side: const BorderSide(color: AppColors.outlineSubtle, width: 0.8),
        ),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          splashColor: AppColors.infoCyan.withOpacity(0.15),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 6.0),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Icon(icon, size: 12, color: AppColors.textSecondary),
                const SizedBox(width: 4),
                Text(
                  label,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textSecondary,
                    fontSize: 9.5,
                    fontWeight: FontWeight.w600,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _showLogsModal(BuildContext context, HostRecord host) {
    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: AppColors.terminalBlack,
        shape: RoundedRectangleBorder(
          borderRadius: AppSpacing.roundedLg,
          side: const BorderSide(color: AppColors.outline, width: 1),
        ),
        title: Row(
          children: [
            const Icon(Icons.article_outlined, size: 18, color: AppColors.infoCyan),
            const SizedBox(width: 8),
            Text(
              'DAEMON LOGS: ${host.name}',
              style: AppTypography.codeSm.copyWith(
                color: AppColors.textPrimary,
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
        content: Container(
          width: double.maxFinite,
          height: 260,
          padding: AppSpacing.paddingCardDense,
          decoration: BoxDecoration(
            color: const Color(0xFF030406),
            borderRadius: AppSpacing.roundedSm,
          ),
          child: SingleChildScrollView(
            child: Text(
              '''[ackbard] 2026/08/20 21:14:02 Starting ackbard on 127.0.0.1:7777 (v0.2.1)
[ackbard] 2026/08/20 21:14:02 SQLite pure Go database initialized (~/.ackbard.db)
[ackbard] 2026/08/20 21:14:02 SSE Event hub listening on /v1/events
[ackbard] 2026/08/20 21:14:02 Hook ingest endpoints active: claude-code, codex, antigravity
[ackbard] 2026/08/20 21:15:10 Ingested event from claude-code: SessionStart
[ackbard] 2026/08/20 21:16:30 Ingested event from antigravity: Blocked (question)
[ackbard] 2026/08/20 21:18:22 SSE client connected from mobile client (127.0.0.1:58421)''',
              style: AppTypography.codeSm.copyWith(
                color: AppColors.statusEmerald,
                fontSize: 11,
              ),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: Text('Close', style: AppTypography.titleSmall.copyWith(color: AppColors.infoCyan)),
          ),
        ],
      ),
    );
  }
}
