import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';

/// Reusable horizontal touch key accessory bar for terminal sessions and quick inputs.
class TerminalAccessoryBar extends StatelessWidget {
  final ValueChanged<String> onKeyPressed;
  final List<String>? customKeys;
  final EdgeInsetsGeometry padding;
  final bool enableHaptics;

  const TerminalAccessoryBar({
    super.key,
    required this.onKeyPressed,
    this.customKeys,
    this.padding = const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 6),
    this.enableHaptics = true,
  });

  static const List<String> defaultKeys = [
    'Esc',
    'Tab',
    '⌫',
    'Ctrl+C',
    'Enter',
    '↑',
    '↓',
    '←',
    '→',
    'PgUp',
    'PgDn',
    'Home',
    'End',
    'Shift+Tab',
    'Ctrl+D',
    'Ctrl+B',
    '[',
    '/',
    '-',
    ':',
    '|',
    '~',
  ];

  void _handleKeyTap(String key) {
    if (enableHaptics) {
      HapticFeedback.lightImpact();
    }
    onKeyPressed(key);
  }

  @override
  Widget build(BuildContext context) {
    final keys = customKeys ?? defaultKeys;

    return Container(
      decoration: const BoxDecoration(
        color: AppColors.terminalBlack,
        border: Border(
          top: BorderSide(color: AppColors.outlineSubtle, width: 1),
          bottom: BorderSide(color: AppColors.outlineSubtle, width: 1),
        ),
      ),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        padding: padding,
        physics: const BouncingScrollPhysics(),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: keys.map((key) {
            final isHighlight = key == 'Ctrl+C' || key == 'Esc' || key == 'Enter' || key == '⌫';

            return Padding(
              padding: const EdgeInsets.symmetric(horizontal: 3.0),
              child: Material(
                color: isHighlight
                    ? (key == 'Ctrl+C'
                        ? AppColors.statusCoral.withOpacity(0.15)
                        : (key == 'Enter'
                            ? AppColors.infoCyan.withOpacity(0.15)
                            : (key == '⌫'
                                ? AppColors.statusAmber.withOpacity(0.15)
                                : AppColors.surfaceHighlight)))
                    : AppColors.surface,
                shape: RoundedRectangleBorder(
                  borderRadius: AppSpacing.roundedSm,
                  side: BorderSide(
                    color: isHighlight
                        ? (key == 'Ctrl+C'
                            ? AppColors.statusCoral.withOpacity(0.4)
                            : (key == 'Enter'
                                ? AppColors.infoCyan.withOpacity(0.4)
                                : (key == '⌫'
                                    ? AppColors.statusAmber.withOpacity(0.4)
                                    : AppColors.outline)))
                        : AppColors.outlineSubtle,
                    width: 1,
                  ),
                ),
                clipBehavior: Clip.antiAlias,
                child: InkWell(
                  onTap: () => _handleKeyTap(key),
                  splashColor: AppColors.infoCyan.withOpacity(0.2),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
                    child: Text(
                      key,
                      style: AppTypography.touchKeyText.copyWith(
                        color: key == 'Ctrl+C'
                            ? AppColors.statusCoral
                            : (key == 'Enter'
                                ? AppColors.infoCyan
                                : (key == '⌫'
                                    ? AppColors.statusAmber
                                    : AppColors.textPrimary)),
                        fontSize: 12,
                        fontWeight: isHighlight ? FontWeight.w600 : FontWeight.normal,
                      ),
                    ),
                  ),
                ),
              ),
            );
          }).toList(),
        ),
      ),
    );
  }
}
