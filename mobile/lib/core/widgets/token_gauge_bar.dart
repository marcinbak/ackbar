import 'package:flutter/material.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';

/// Reusable gradient token gauge and context window usage bar.
class TokenGaugeBar extends StatelessWidget {
  final int? usedTokens;
  final int? maxTokens;
  final double? percentage; // 0.0 to 1.0
  final bool showLabels;
  final double height;
  final bool isCompact;
  final String? customLabel;

  const TokenGaugeBar({
    super.key,
    this.usedTokens,
    this.maxTokens,
    this.percentage,
    this.showLabels = true,
    this.height = 6.0,
    this.isCompact = false,
    this.customLabel,
  });

  double get calculatedPercentage {
    if (percentage != null) {
      return percentage!.clamp(0.0, 1.0);
    }
    if (usedTokens != null && maxTokens != null && maxTokens! > 0) {
      return (usedTokens! / maxTokens!).clamp(0.0, 1.0);
    }
    return 0.0;
  }

  String _formatTokenCount(int count) {
    if (count >= 1000000) {
      return '${(count / 1000000).toStringAsFixed(1)}M';
    } else if (count >= 1000) {
      return '${(count / 1000).toStringAsFixed(0)}k';
    }
    return count.toString();
  }

  Color _getThresholdColor(double pct) {
    if (pct < 0.60) {
      return AppColors.infoCyan;
    } else if (pct < 0.85) {
      return AppColors.statusAmber;
    } else {
      return AppColors.statusCoral;
    }
  }

  Gradient _getGradient(double pct) {
    if (pct < 0.60) {
      return AppColors.cyanEmeraldGradient;
    } else if (pct < 0.85) {
      return const LinearGradient(
        colors: [AppColors.infoCyan, AppColors.statusAmber],
        begin: Alignment.centerLeft,
        end: Alignment.centerRight,
      );
    } else {
      return AppColors.amberCoralGradient;
    }
  }

  @override
  Widget build(BuildContext context) {
    final pct = calculatedPercentage;
    final pctInt = (pct * 100).toInt();
    final barHeight = isCompact ? 4.0 : height;

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (showLabels) ...[
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Text(
                customLabel ?? 'CONTEXT USAGE',
                style: AppTypography.codeXs.copyWith(
                  letterSpacing: 0.5,
                  fontWeight: FontWeight.w600,
                ),
              ),
              Text(
                usedTokens != null && maxTokens != null
                    ? '${_formatTokenCount(usedTokens!)} / ${_formatTokenCount(maxTokens!)} ($pctInt%)'
                    : '$pctInt%',
                style: AppTypography.codeXs.copyWith(
                  color: _getThresholdColor(pct),
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          SizedBox(height: isCompact ? 3.0 : 5.0),
        ],
        Container(
          height: barHeight,
          decoration: BoxDecoration(
            color: AppColors.surfaceHighlight,
            borderRadius: BorderRadius.circular(barHeight / 2),
            border: Border.all(
              color: AppColors.outlineSubtle,
              width: 0.5,
            ),
          ),
          clipBehavior: Clip.antiAlias,
          child: LayoutBuilder(
            builder: (context, constraints) {
              final fillWidth = constraints.maxWidth * pct;
              return Align(
                alignment: Alignment.centerLeft,
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 300),
                  curve: Curves.easeOutCubic,
                  width: fillWidth,
                  height: barHeight,
                  decoration: BoxDecoration(
                    gradient: _getGradient(pct),
                    borderRadius: BorderRadius.circular(barHeight / 2),
                  ),
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}
