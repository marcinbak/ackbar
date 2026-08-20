import 'package:flutter/material.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';

/// Screen listing all architectural plans and implementation proposals.
class PlansScreen extends StatelessWidget {
  const PlansScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: const AckbarAppBar(title: 'PLANS & PROPOSALS'),
      body: ListView(
        padding: AppSpacing.paddingScreen,
        children: [
          // Section title
          Text(
            'ACTIVE ARCHITECTURAL PLANS',
            style: AppTypography.codeXs.copyWith(
              letterSpacing: 0.8,
              fontWeight: FontWeight.w700,
            ),
          ),
          AppSpacing.gapH12,

          // Plan card 1
          GlassCard(
            accentColor: AppColors.infoCyan,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(
                      'PLAN-2026-08-A',
                      style: AppTypography.codeSm.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const Spacer(),
                    const StatusBadge(
                      status: AckbarSessionStatus.active,
                      customLabel: 'IN REVIEW',
                    ),
                  ],
                ),
                AppSpacing.gapH8,
                Text(
                  'Mobile Client Flutter Scaffolding & Stitch Theme Integration',
                  style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w600),
                ),
                AppSpacing.gapH8,
                Text(
                  'Scaffolding the mobile control plane with decoupled ThemeExtension, GlassCard containers, StatusBadge, TokenGaugeBar, and 4-tab bottom navigation shell.',
                  style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
                ),
                AppSpacing.gapH12,
                Row(
                  children: [
                    Icon(Icons.edit_document, size: 14, color: AppColors.textMuted),
                    const SizedBox(width: 4),
                    Text('14 Files Changed', style: AppTypography.codeXs),
                    const SizedBox(width: 12),
                    Icon(Icons.check_circle_outline, size: 14, color: AppColors.statusEmerald),
                    const SizedBox(width: 4),
                    Text('3 Approved', style: AppTypography.codeXs),
                  ],
                ),
              ],
            ),
          ),
          AppSpacing.gapH16,

          // Plan card 2
          GlassCard(
            accentColor: AppColors.statusEmerald,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(
                      'PLAN-2026-08-B',
                      style: AppTypography.codeSm.copyWith(
                        color: AppColors.statusEmerald,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const Spacer(),
                    const StatusBadge(
                      status: AckbarSessionStatus.idle,
                      customLabel: 'COMPLETED',
                    ),
                  ],
                ),
                AppSpacing.gapH8,
                Text(
                  'SSE Streaming Endpoint & SQLite Multi-Agent Persistence',
                  style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w600),
                ),
                AppSpacing.gapH8,
                Text(
                  'Persistent state storage for Claude Code, Codex, and Antigravity hooks with automated broadcast to client TUI and web frontends.',
                  style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
