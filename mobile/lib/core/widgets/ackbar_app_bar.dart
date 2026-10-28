import 'package:flutter/material.dart';
import '../constants/app_version.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_typography.dart';

/// Data class representing host connectivity for the status indicator model.
class HostStatusIndicator {
  final String name;
  final bool isOnline;
  final String? latency;

  const HostStatusIndicator({
    required this.name,
    required this.isOnline,
    this.latency,
  });
}

/// Standard top AppBar for Ackbar Mobile with branding and optional version badge.
class AckbarAppBar extends StatelessWidget implements PreferredSizeWidget {
  final String title;
  final String version;
  final bool showVersion;
  final List<HostStatusIndicator>? hosts;
  final List<Widget>? actions;
  final bool showBackButton;
  final VoidCallback? onBackPressed;
  final ValueChanged<HostStatusIndicator>? onHostTapped;

  const AckbarAppBar({
    super.key,
    this.title = 'ACKBAR',
    this.version = AppVersion.displayVersion,
    this.showVersion = false,
    this.hosts,
    this.actions,
    this.showBackButton = false,
    this.onBackPressed,
    this.onHostTapped,
  });

  static const List<HostStatusIndicator> defaultHosts = [
    HostStatusIndicator(name: 'local', isOnline: true),
    HostStatusIndicator(name: 'gpu-box', isOnline: true),
  ];

  @override
  Size get preferredSize => const Size.fromHeight(56.0);

  @override
  Widget build(BuildContext context) {
    return AppBar(
      backgroundColor: AppColors.background,
      elevation: 0,
      scrolledUnderElevation: 0,
      automaticallyImplyLeading: false,
      titleSpacing: AppSpacing.lg,
      leading: showBackButton
          ? IconButton(
              icon: const Icon(Icons.arrow_back_ios_new_rounded, size: 18),
              color: AppColors.textPrimary,
              onPressed:
                  onBackPressed ?? () => Navigator.of(context).maybePop(),
            )
          : null,
      title: Row(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          // Anchor Logo & Title
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text('⚓', style: TextStyle(fontSize: 16)),
              const SizedBox(width: 6),
              Text(
                title,
                style: AppTypography.appBarTitle.copyWith(
                  color: AppColors.textPrimary,
                  letterSpacing: 1.2,
                ),
              ),
            ],
          ),
          if (showVersion) ...[
            const SizedBox(width: 8),

            // Version badge pill
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
              decoration: BoxDecoration(
                color: AppColors.surfaceHighlight,
                borderRadius: AppSpacing.roundedSm,
                border: Border.all(color: AppColors.outlineSubtle, width: 0.8),
              ),
              child: Text(
                version,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.infoCyan,
                  fontSize: 9.5,
                ),
              ),
            ),
          ],
        ],
      ),
      actions: [
        if (actions != null) ...actions!,
        const SizedBox(width: AppSpacing.md),
      ],
      bottom: const PreferredSize(
        preferredSize: Size.fromHeight(1.0),
        child: Divider(
          height: 1,
          thickness: 1,
          color: AppColors.outlineSubtle,
        ),
      ),
    );
  }
}
