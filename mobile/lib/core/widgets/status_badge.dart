import 'package:flutter/material.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';

/// Semantic session status enum for Ackbar agents.
enum AckbarSessionStatus {
  working,
  blocked,
  idle,
  offline,
  active;

  String get label {
    switch (this) {
      case AckbarSessionStatus.working:
        return 'WORKING';
      case AckbarSessionStatus.blocked:
        return 'BLOCKED';
      case AckbarSessionStatus.idle:
        return 'IDLE';
      case AckbarSessionStatus.offline:
        return 'OFFLINE';
      case AckbarSessionStatus.active:
        return 'ACTIVE';
    }
  }

  String get symbol {
    switch (this) {
      case AckbarSessionStatus.working:
        return '⚡';
      case AckbarSessionStatus.blocked:
        return '❓';
      case AckbarSessionStatus.idle:
        return '✅';
      case AckbarSessionStatus.offline:
        return '⚪';
      case AckbarSessionStatus.active:
        return '🟢';
    }
  }

  Color get color {
    switch (this) {
      case AckbarSessionStatus.working:
        return AppColors.statusAmber;
      case AckbarSessionStatus.blocked:
        return AppColors.statusCoral;
      case AckbarSessionStatus.idle:
      case AckbarSessionStatus.active:
        return AppColors.statusEmerald;
      case AckbarSessionStatus.offline:
        return AppColors.statusOffline;
    }
  }
}

/// Reusable status badge pill component with monospace typography.
class StatusBadge extends StatelessWidget {
  final AckbarSessionStatus status;
  final String? customLabel;
  final String? customSymbol;
  final bool isCompact;
  final bool showDotOnly;
  final Color? overrideColor;

  const StatusBadge({
    super.key,
    required this.status,
    this.customLabel,
    this.customSymbol,
    this.isCompact = false,
    this.showDotOnly = false,
    this.overrideColor,
  });

  // Named convenience constructors
  const StatusBadge.working({super.key, this.customLabel, this.isCompact = false})
      : status = AckbarSessionStatus.working,
        customSymbol = null,
        showDotOnly = false,
        overrideColor = null;

  const StatusBadge.blocked({super.key, this.customLabel, this.isCompact = false})
      : status = AckbarSessionStatus.blocked,
        customSymbol = null,
        showDotOnly = false,
        overrideColor = null;

  const StatusBadge.idle({super.key, this.customLabel, this.isCompact = false})
      : status = AckbarSessionStatus.idle,
        customSymbol = null,
        showDotOnly = false,
        overrideColor = null;

  const StatusBadge.offline({super.key, this.customLabel, this.isCompact = false})
      : status = AckbarSessionStatus.offline,
        customSymbol = null,
        showDotOnly = false,
        overrideColor = null;

  const StatusBadge.active({super.key, this.customLabel, this.isCompact = false})
      : status = AckbarSessionStatus.active,
        customSymbol = null,
        showDotOnly = false,
        overrideColor = null;

  @override
  Widget build(BuildContext context) {
    final statusColor = overrideColor ?? status.color;
    final symbol = customSymbol ?? status.symbol;
    final text = customLabel ?? status.label;

    if (showDotOnly) {
      return Container(
        width: 8,
        height: 8,
        decoration: BoxDecoration(
          color: statusColor,
          shape: BoxShape.circle,
          boxShadow: [
            BoxShadow(
              color: statusColor.withOpacity(0.4),
              blurRadius: 4,
              spreadRadius: 1,
            ),
          ],
        ),
      );
    }

    return Container(
      padding: isCompact
          ? const EdgeInsets.symmetric(horizontal: 6, vertical: 2)
          : const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: statusColor.withOpacity(0.12),
        borderRadius: AppSpacing.roundedSm,
        border: Border.all(
          color: statusColor.withOpacity(0.35),
          width: 1.0,
        ),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Text(
            symbol,
            style: TextStyle(
              fontSize: isCompact ? 10 : 11,
              height: 1.1,
            ),
          ),
          const SizedBox(width: 4),
          Text(
            text,
            style: AppTypography.badgeText.copyWith(
              color: statusColor,
              fontSize: isCompact ? 9.5 : 11,
            ),
          ),
        ],
      ),
    );
  }
}
