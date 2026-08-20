import 'package:flutter/material.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';

/// Data class representing an item in the segmented filter tab row.
class FilterTabItem {
  final String label;
  final int? count;
  final Color? countBadgeColor;
  final Color? countTextColor;

  const FilterTabItem({
    required this.label,
    this.count,
    this.countBadgeColor,
    this.countTextColor,
  });
}

/// Horizontal segmented pill switcher with badge count indicators.
class SegmentedFilterTabs extends StatelessWidget {
  final List<FilterTabItem> tabs;
  final int selectedIndex;
  final ValueChanged<int> onTabSelected;
  final EdgeInsetsGeometry padding;

  const SegmentedFilterTabs({
    super.key,
    required this.tabs,
    required this.selectedIndex,
    required this.onTabSelected,
    this.padding = const EdgeInsets.symmetric(horizontal: AppSpacing.lg),
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: padding,
      physics: const BouncingScrollPhysics(),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: List.generate(tabs.length, (index) {
          final tab = tabs[index];
          final isSelected = index == selectedIndex;

          return Padding(
            padding: EdgeInsets.only(right: index == tabs.length - 1 ? 0 : AppSpacing.sm),
            child: _FilterPill(
              tab: tab,
              isSelected: isSelected,
              onTap: () => onTabSelected(index),
            ),
          );
        }),
      ),
    );
  }
}

class _FilterPill extends StatelessWidget {
  final FilterTabItem tab;
  final bool isSelected;
  final VoidCallback onTap;

  const _FilterPill({
    required this.tab,
    required this.isSelected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final bgColor = isSelected ? AppColors.surfaceHighlight : AppColors.surface;
    final borderColor = isSelected ? AppColors.infoCyan.withOpacity(0.6) : AppColors.outlineSubtle;
    final textColor = isSelected ? AppColors.textPrimary : AppColors.textSecondary;

    return Material(
      color: bgColor,
      shape: RoundedRectangleBorder(
        borderRadius: AppSpacing.roundedMd,
        side: BorderSide(color: borderColor, width: 1.0),
      ),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        splashColor: AppColors.infoCyan.withOpacity(0.1),
        highlightColor: AppColors.surfaceHighlight,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                tab.label,
                style: AppTypography.titleSmall.copyWith(
                  color: textColor,
                  fontWeight: isSelected ? FontWeight.w600 : FontWeight.w500,
                  fontSize: 12.5,
                ),
              ),
              if (tab.count != null) ...[
                const SizedBox(width: 6),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1.5),
                  decoration: BoxDecoration(
                    color: tab.countBadgeColor ??
                        (isSelected
                            ? AppColors.infoCyan.withOpacity(0.2)
                            : AppColors.surfaceContainerHigh),
                    borderRadius: BorderRadius.circular(10),
                    border: isSelected
                        ? Border.all(color: AppColors.infoCyan.withOpacity(0.4), width: 0.5)
                        : null,
                  ),
                  child: Text(
                    '${tab.count}',
                    style: AppTypography.codeXs.copyWith(
                      color: tab.countTextColor ??
                          (isSelected ? AppColors.infoCyan : AppColors.textMuted),
                      fontWeight: FontWeight.w700,
                      fontSize: 10,
                    ),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
