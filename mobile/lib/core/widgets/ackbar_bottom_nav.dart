import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';

/// Reusable 4-tab bottom navigation bar for Ackbar Mobile.
class AckbarBottomNav extends StatelessWidget {
  final int currentIndex;
  final ValueChanged<int> onTap;
  final int attentionCount;
  final int plansCount;

  const AckbarBottomNav({
    super.key,
    required this.currentIndex,
    required this.onTap,
    this.attentionCount = 0,
    this.plansCount = 0,
  });

  void _handleTap(int index) {
    HapticFeedback.selectionClick();
    onTap(index);
  }

  @override
  Widget build(BuildContext context) {
    final navItems = [
      _NavItemData(
        label: 'Fleet',
        icon: Icons.grid_view_rounded,
        selectedIcon: Icons.grid_view_rounded,
      ),
      _NavItemData(
        label: 'Attention',
        icon: Icons.notifications_none_rounded,
        selectedIcon: Icons.notifications_active_rounded,
        badgeCount: attentionCount,
        badgeColor: AppColors.statusCoral,
      ),
      _NavItemData(
        label: 'Plans',
        icon: Icons.assignment_outlined,
        selectedIcon: Icons.assignment_rounded,
        badgeCount: plansCount,
        badgeColor: AppColors.infoCyan,
      ),
      _NavItemData(
        label: 'Hosts',
        icon: Icons.dns_outlined,
        selectedIcon: Icons.dns_rounded,
      ),
    ];

    return Container(
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(
          top: BorderSide(color: AppColors.outlineSubtle, width: 1.0),
        ),
      ),
      child: SafeArea(
        top: false,
        child: Container(
          height: 60,
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.sm),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceAround,
            children: List.generate(navItems.length, (index) {
              final item = navItems[index];
              final isSelected = index == currentIndex;

              return Expanded(
                child: _NavButton(
                  item: item,
                  isSelected: isSelected,
                  onTap: () => _handleTap(index),
                ),
              );
            }),
          ),
        ),
      ),
    );
  }
}

class _NavItemData {
  final String label;
  final IconData icon;
  final IconData selectedIcon;
  final int badgeCount;
  final Color? badgeColor;

  const _NavItemData({
    required this.label,
    required this.icon,
    required this.selectedIcon,
    this.badgeCount = 0,
    this.badgeColor,
  });
}

class _NavButton extends StatelessWidget {
  final _NavItemData item;
  final bool isSelected;
  final VoidCallback onTap;

  const _NavButton({
    required this.item,
    required this.isSelected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final activeColor = AppColors.infoCyan;
    final inactiveColor = AppColors.textMuted;
    final color = isSelected ? activeColor : inactiveColor;

    return InkWell(
      onTap: onTap,
      splashColor: AppColors.infoCyan.withOpacity(0.1),
      highlightColor: Colors.transparent,
      borderRadius: AppSpacing.roundedMd,
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Stack(
            clipBehavior: Clip.none,
            children: [
              Icon(
                isSelected ? item.selectedIcon : item.icon,
                color: color,
                size: 22,
              ),
              if (item.badgeCount > 0)
                Positioned(
                  top: -4,
                  right: -8,
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 4.5, vertical: 1.5),
                    decoration: BoxDecoration(
                      color: item.badgeColor ?? AppColors.statusCoral,
                      borderRadius: BorderRadius.circular(10),
                      boxShadow: [
                        BoxShadow(
                          color: (item.badgeColor ?? AppColors.statusCoral).withOpacity(0.4),
                          blurRadius: 4,
                          spreadRadius: 1,
                        ),
                      ],
                    ),
                    constraints: const BoxConstraints(
                      minWidth: 15,
                      minHeight: 15,
                    ),
                    child: Text(
                      '${item.badgeCount}',
                      textAlign: TextAlign.center,
                      style: AppTypography.codeXs.copyWith(
                        color: Colors.white,
                        fontSize: 9,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 3),
          Text(
            item.label,
            style: AppTypography.bodySmall.copyWith(
              color: color,
              fontSize: 11,
              fontWeight: isSelected ? FontWeight.w600 : FontWeight.w400,
            ),
          ),
        ],
      ),
    );
  }
}
