import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';
import 'ackbar_bottom_nav.dart';

class AckbarNavRail extends StatelessWidget {
  final int currentIndex;
  final ValueChanged<int> onTap;
  final int attentionCount;
  final int plansCount;
  final int? hostsCount;
  final int? hostsOnlineCount;

  const AckbarNavRail({
    super.key,
    required this.currentIndex,
    required this.onTap,
    this.attentionCount = 0,
    this.plansCount = 0,
    this.hostsCount,
    this.hostsOnlineCount,
  });

  @override
  Widget build(BuildContext context) {
    final items = [
      (
        'Fleet',
        Icons.grid_view_rounded,
        Icons.grid_view_rounded,
        0,
        false,
        null as Color?
      ),
      (
        'Attention',
        Icons.notifications_none_rounded,
        Icons.notifications_active_rounded,
        attentionCount,
        false,
        AppColors.statusCoral
      ),
      (
        'Plans',
        Icons.assignment_outlined,
        Icons.assignment_rounded,
        plansCount,
        false,
        AppColors.infoCyan
      ),
      (
        'Hosts',
        Icons.dns_outlined,
        Icons.dns_rounded,
        hostsCount ?? 0,
        hostsCount != null,
        hostsCount != null
            ? AckbarBottomNav.hostsBadgeColor(
                total: hostsCount!, online: hostsOnlineCount ?? 0)
            : null
      ),
    ];

    return Container(
      width: 72,
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(right: BorderSide(color: AppColors.outlineSubtle)),
      ),
      child: SafeArea(
        right: false,
        child: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(vertical: AppSpacing.md),
          child: Column(
            children: List.generate(items.length, (i) {
              final (label, icon, selIcon, count, showZero, badgeColor) =
                  items[i];
              return Padding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                child: _tile(
                  index: i,
                  label: label,
                  icon: icon,
                  selectedIcon: selIcon,
                  badgeCount: count,
                  showBadgeWhenZero: showZero,
                  badgeColor: badgeColor,
                ),
              );
            }),
          ),
        ),
      ),
    );
  }

  Widget _tile({
    required int index,
    required String label,
    required IconData icon,
    required IconData selectedIcon,
    required int badgeCount,
    required bool showBadgeWhenZero,
    required Color? badgeColor,
  }) {
    final selected = index == currentIndex;
    final keySlug = label.toLowerCase();
    final color = selected ? AppColors.infoCyan : AppColors.textMuted;

    return InkWell(
      key: Key('nav_rail_item_$keySlug'),
      onTap: () {
        HapticFeedback.selectionClick();
        onTap(index);
      },
      borderRadius: AppSpacing.roundedMd,
      child: Container(
        width: 60,
        padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
        decoration: BoxDecoration(
          color: selected
              ? AppColors.infoCyan.withOpacity(0.12)
              : Colors.transparent,
          borderRadius: AppSpacing.roundedMd,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Stack(
              clipBehavior: Clip.none,
              children: [
                Icon(selected ? selectedIcon : icon, color: color, size: 22),
                if (badgeCount > 0 || showBadgeWhenZero)
                  Positioned(
                    top: -4,
                    right: -10,
                    child: Container(
                      key: Key('nav_rail_badge_$keySlug'),
                      padding: const EdgeInsets.symmetric(
                          horizontal: 4.5, vertical: 1.5),
                      decoration: BoxDecoration(
                        color: badgeColor ?? AppColors.statusCoral,
                        borderRadius: BorderRadius.circular(10),
                      ),
                      constraints:
                          const BoxConstraints(minWidth: 15, minHeight: 15),
                      child: Text(
                        '$badgeCount',
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
            const SizedBox(height: 4),
            Text(
              label,
              style: AppTypography.bodySmall.copyWith(
                color: color,
                fontSize: 10.5,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }
}
