import 'package:flutter/material.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';

/// Screen managing supervised daemon hosts and SSH tunnels.
class HostsScreen extends StatelessWidget {
  const HostsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: const AckbarAppBar(title: 'DAEMON HOSTS'),
      body: ListView(
        padding: AppSpacing.paddingScreen,
        children: [
          // Section header
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'SUPERVISED NODES (2 ONLINE)',
                style: AppTypography.codeXs.copyWith(
                  letterSpacing: 0.8,
                  fontWeight: FontWeight.w700,
                ),
              ),
              Text(
                '+ ADD HOST',
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.infoCyan,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
          AppSpacing.gapH12,

          // Host 1: Local
          GlassCard(
            accentColor: AppColors.statusEmerald,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    const Icon(Icons.laptop_mac_rounded, size: 18, color: AppColors.infoCyan),
                    const SizedBox(width: 8),
                    Text(
                      'local',
                      style: AppTypography.titleMedium.copyWith(
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                      decoration: BoxDecoration(
                        color: AppColors.surfaceHighlight,
                        borderRadius: AppSpacing.roundedSm,
                      ),
                      child: Text(
                        '127.0.0.1:7777',
                        style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                      ),
                    ),
                    const Spacer(),
                    const StatusBadge(
                      status: AckbarSessionStatus.active,
                      customLabel: 'ONLINE',
                    ),
                  ],
                ),
                AppSpacing.gapH12,
                Row(
                  children: [
                    _buildMetric('Active Sessions', '2'),
                    _buildMetric('Latency', '1ms'),
                    _buildMetric('ackbard Version', 'v0.2.1'),
                  ],
                ),
              ],
            ),
          ),
          AppSpacing.gapH16,

          // Host 2: gpu-box
          GlassCard(
            accentColor: AppColors.statusEmerald,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    const Icon(Icons.dns_rounded, size: 18, color: AppColors.infoCyan),
                    const SizedBox(width: 8),
                    Text(
                      'gpu-box',
                      style: AppTypography.titleMedium.copyWith(
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                      decoration: BoxDecoration(
                        color: AppColors.surfaceHighlight,
                        borderRadius: AppSpacing.roundedSm,
                      ),
                      child: Text(
                        'ssh://gpu-box:7777',
                        style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                      ),
                    ),
                    const Spacer(),
                    const StatusBadge(
                      status: AckbarSessionStatus.active,
                      customLabel: 'ONLINE',
                    ),
                  ],
                ),
                AppSpacing.gapH12,
                Row(
                  children: [
                    _buildMetric('Active Sessions', '2'),
                    _buildMetric('Latency', '24ms'),
                    _buildMetric('ackbard Version', 'v0.2.1'),
                  ],
                ),
              ],
            ),
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
          Text(label, style: AppTypography.codeXs.copyWith(color: AppColors.textMuted)),
          const SizedBox(height: 2),
          Text(
            value,
            style: AppTypography.codeSm.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}
