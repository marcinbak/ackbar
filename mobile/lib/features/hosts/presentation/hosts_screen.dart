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
                        'Encrypted WireGuard mesh active • Direct P2P tunnel to ackbard nodes',
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

          // Empty State or Host Cards List
          if (hosts.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: AppSpacing.xxl),
              child: GlassCard(
                child: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(AppSpacing.lg),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(Icons.dns_rounded, size: 44, color: AppColors.infoCyan),
                        AppSpacing.gapH12,
                        Text(
                          'NO SUPERVISED HOSTS',
                          style: AppTypography.titleMedium.copyWith(
                            fontWeight: FontWeight.w700,
                            color: AppColors.textPrimary,
                          ),
                        ),
                        AppSpacing.gapH8,
                        Text(
                          'Connect to an ackbard daemon running on your Mac or Tailscale node to supervise sessions in real-time.',
                          textAlign: TextAlign.center,
                          style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
                        ),
                        AppSpacing.gapH16,
                        ElevatedButton.icon(
                          onPressed: () => AddHostDialog.show(context),
                          icon: const Icon(Icons.add_rounded, size: 16),
                          label: const Text('Add Supervised Host'),
                          style: ElevatedButton.styleFrom(
                            backgroundColor: AppColors.infoCyan,
                            foregroundColor: AppColors.terminalBlack,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            )
          else
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
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      host.name,
                      style: AppTypography.titleMedium.copyWith(
                        fontWeight: FontWeight.w700,
                        color: AppColors.textPrimary,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                    Text(
                      host.endpointDisplay,
                      style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary, fontSize: 11),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              StatusBadge(
                status: host.badgeStatus,
                customLabel: host.online ? 'ONLINE' : 'UNREACHABLE',
                isCompact: true,
              ),
              const SizedBox(width: 4),
              IconButton(
                icon: const Icon(Icons.delete_outline_rounded, size: 18, color: AppColors.textMuted),
                tooltip: 'Delete host',
                visualDensity: VisualDensity.compact,
                onPressed: () => _confirmDeleteHost(context, ref, host),
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
                _buildMetric('Daemon Ver', host.version.isNotEmpty ? host.version : (host.online ? 'Active' : '—')),
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
                  ref.read(hostsListProvider.notifier).refreshHosts();
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
                icon: Icons.settings_outlined,
                label: 'Settings',
                onTap: () => _showHostSettingsModal(context, ref, host),
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
              fontSize: 11,
            ),
            overflow: TextOverflow.ellipsis,
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

  void _confirmDeleteHost(BuildContext context, WidgetRef ref, HostRecord host) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.surface,
        shape: RoundedRectangleBorder(
          borderRadius: AppSpacing.roundedLg,
          side: const BorderSide(color: AppColors.outline, width: 1),
        ),
        title: Row(
          children: [
            const Icon(Icons.delete_outline_rounded, size: 20, color: AppColors.statusCoral),
            const SizedBox(width: 8),
            Text(
              'DELETE HOST',
              style: AppTypography.codeSm.copyWith(
                color: AppColors.statusCoral,
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
        content: Text(
          'Are you sure you want to disconnect and delete host "${host.name}" (${host.endpointDisplay})?',
          style: AppTypography.bodyMedium.copyWith(color: AppColors.textPrimary),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text('Cancel', style: AppTypography.titleSmall.copyWith(color: AppColors.textMuted)),
          ),
          ElevatedButton(
            onPressed: () {
              ref.read(hostsListProvider.notifier).removeHost(host.name);
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                  content: Text('Host "${host.name}" removed'),
                  backgroundColor: AppColors.surfaceHighlight,
                ),
              );
            },
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.statusCoral,
              foregroundColor: Colors.white,
            ),
            child: const Text('Delete Host'),
          ),
        ],
      ),
    );
  }

  void _showHostSettingsModal(BuildContext context, WidgetRef ref, HostRecord host) {
    final nameCtrl = TextEditingController(text: host.name);
    final urlCtrl = TextEditingController(text: host.url);

    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppColors.surface,
        shape: RoundedRectangleBorder(
          borderRadius: AppSpacing.roundedLg,
          side: const BorderSide(color: AppColors.outline, width: 1),
        ),
        title: Row(
          children: [
            const Icon(Icons.settings_outlined, size: 20, color: AppColors.infoCyan),
            const SizedBox(width: 8),
            Text(
              'HOST SETTINGS',
              style: AppTypography.codeSm.copyWith(
                color: AppColors.textPrimary,
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Host Name / Alias:', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextField(
                controller: nameCtrl,
                style: AppTypography.bodySmall,
                decoration: const InputDecoration(isDense: true),
              ),
              AppSpacing.gapH12,
              Text('Daemon Endpoint URL:', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextField(
                controller: urlCtrl,
                style: AppTypography.bodySmall,
                decoration: const InputDecoration(isDense: true),
              ),
              AppSpacing.gapH16,
              Container(
                padding: AppSpacing.paddingCardDense,
                decoration: BoxDecoration(
                  color: AppColors.terminalBlack,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Status: ${host.online ? "Online 🟢" : "Unreachable 🔴"}', style: AppTypography.codeXs),
                    const SizedBox(height: 4),
                    Text('Latency: ${host.online ? host.latencyDisplay : "—"}', style: AppTypography.codeXs),
                    const SizedBox(height: 4),
                    Text('Daemon Version: ${host.version.isNotEmpty ? host.version : (host.online ? "Active" : "—")}', style: AppTypography.codeXs),
                  ],
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () {
              Navigator.of(ctx).pop();
              _confirmDeleteHost(context, ref, host);
            },
            child: Text('Delete Host', style: AppTypography.titleSmall.copyWith(color: AppColors.statusCoral)),
          ),
          ElevatedButton(
            onPressed: () {
              final updated = host.copyWith(
                name: nameCtrl.text.trim().isNotEmpty ? nameCtrl.text.trim() : host.name,
                url: urlCtrl.text.trim().isNotEmpty ? urlCtrl.text.trim() : host.url,
              );
              ref.read(hostsListProvider.notifier).updateHost(updated);
              Navigator.of(ctx).pop();
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                  content: Text('Host "${updated.name}" updated'),
                  backgroundColor: AppColors.statusEmerald,
                ),
              );
            },
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.infoCyan,
              foregroundColor: AppColors.terminalBlack,
            ),
            child: const Text('Save Changes'),
          ),
        ],
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
              '''[ackbard] 2026/08/20 21:14:02 Starting ackbard on 127.0.0.1:7777 (${host.version.isNotEmpty ? host.version : "active"})
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
