import 'package:flutter/material.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/terminal_accessory_bar.dart';

/// Screen listing all sessions requiring developer intervention or approval.
class AttentionScreen extends StatefulWidget {
  const AttentionScreen({super.key});

  @override
  State<AttentionScreen> createState() => _AttentionScreenState();
}

class _AttentionScreenState extends State<AttentionScreen> {
  final TextEditingController _inputController = TextEditingController();

  @override
  void dispose() {
    _inputController.dispose();
    super.dispose();
  }

  void _onTerminalKeyPress(String key) {
    if (key == 'Enter') {
      _inputController.clear();
    } else {
      _inputController.text += key;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: const AckbarAppBar(title: 'ATTENTION QUEUE'),
      body: Column(
        children: [
          Expanded(
            child: ListView(
              padding: AppSpacing.paddingScreen,
              children: [
                // Attention Alert Banner
                Container(
                  padding: AppSpacing.paddingCardDense,
                  decoration: BoxDecoration(
                    color: AppColors.statusCoral.withOpacity(0.1),
                    borderRadius: AppSpacing.roundedLg,
                    border: Border.all(
                      color: AppColors.statusCoral.withOpacity(0.35),
                      width: 1.0,
                    ),
                  ),
                  child: Row(
                    children: [
                      const Icon(
                        Icons.warning_amber_rounded,
                        color: AppColors.statusCoral,
                        size: 22,
                      ),
                      const SizedBox(width: AppSpacing.md),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              '1 SESSION WAITING FOR INPUT',
                              style: AppTypography.titleSmall.copyWith(
                                color: AppColors.statusCoral,
                                fontWeight: FontWeight.w700,
                              ),
                            ),
                            const SizedBox(height: 2),
                            Text(
                              'Antigravity session blocked on SQLite migration approval.',
                              style: AppTypography.bodySmall.copyWith(
                                color: AppColors.textSecondary,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
                ),
                AppSpacing.gapH16,

                // Urgent Session Card
                GlassCard(
                  accentColor: AppColors.statusCoral,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Container(
                            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                            decoration: BoxDecoration(
                              color: AppColors.surfaceHighlight,
                              borderRadius: AppSpacing.roundedSm,
                            ),
                            child: Text(
                              'gpu-box',
                              style: AppTypography.codeXs.copyWith(
                                color: AppColors.infoCyan,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ),
                          const SizedBox(width: 6),
                          Text(
                            '•  Antigravity',
                            style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                          ),
                          const Spacer(),
                          const StatusBadge.blocked(customLabel: 'PLAN REVIEW'),
                        ],
                      ),
                      AppSpacing.gapH12,
                      Text(
                        'Migration Plan: Add column is_daemon to schema_migrations',
                        style: AppTypography.titleMedium.copyWith(
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      AppSpacing.gapH8,
                      Container(
                        padding: AppSpacing.paddingCardDense,
                        decoration: BoxDecoration(
                          color: AppColors.terminalBlack,
                          borderRadius: AppSpacing.roundedMd,
                          border: Border.all(color: AppColors.outlineSubtle, width: 1),
                        ),
                        child: Text(
                          '> Proposed: ALTER TABLE projects ADD COLUMN is_pinned INTEGER DEFAULT 0;\n> Confirm execution? [y/N]',
                          style: AppTypography.codeSm.copyWith(
                            color: AppColors.statusAmberLight,
                          ),
                        ),
                      ),
                      AppSpacing.gapH16,
                      Row(
                        children: [
                          Expanded(
                            child: ElevatedButton.icon(
                              onPressed: () {},
                              icon: const Icon(Icons.check_circle_outline, size: 16),
                              label: const Text('Approve (y)'),
                              style: ElevatedButton.styleFrom(
                                backgroundColor: AppColors.statusEmerald.withOpacity(0.2),
                                foregroundColor: AppColors.statusEmerald,
                                side: BorderSide(
                                  color: AppColors.statusEmerald.withOpacity(0.5),
                                  width: 1,
                                ),
                              ),
                            ),
                          ),
                          const SizedBox(width: AppSpacing.md),
                          Expanded(
                            child: OutlinedButton.icon(
                              onPressed: () {},
                              icon: const Icon(Icons.cancel_outlined, size: 16),
                              label: const Text('Deny (n)'),
                              style: OutlinedButton.styleFrom(
                                foregroundColor: AppColors.statusCoral,
                                side: BorderSide(
                                  color: AppColors.statusCoral.withOpacity(0.5),
                                  width: 1,
                                ),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),

          // Terminal Accessory Key Row at bottom
          TerminalAccessoryBar(
            onKeyPressed: _onTerminalKeyPress,
          ),
        ],
      ),
    );
  }
}
