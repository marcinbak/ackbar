import 'package:flutter/material.dart';

import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';

/// Reusable dark surface card container with 12px rounded corners,
/// subtle 1px border, and optional left accent indicator bar for session state.
class GlassCard extends StatelessWidget {
  final Widget child;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;
  final EdgeInsetsGeometry padding;
  final EdgeInsetsGeometry? margin;
  final Color? accentColor;
  final double accentWidth;
  final Color? backgroundColor;
  final Color? borderColor;
  final BorderRadius? borderRadius;
  final bool isInteractive;

  const GlassCard({
    super.key,
    required this.child,
    this.onTap,
    this.onLongPress,
    this.padding = AppSpacing.paddingCard,
    this.margin,
    this.accentColor,
    this.accentWidth = 3.5,
    this.backgroundColor,
    this.borderColor,
    this.borderRadius,
    this.isInteractive = true,
  });

  Widget _wrapAccent(Widget inner) {
    if (accentColor == null) return inner;
    return Stack(
      children: [
        Positioned(
          left: 0,
          top: 0,
          bottom: 0,
          width: accentWidth,
          child: Container(color: accentColor),
        ),
        Padding(
          padding: EdgeInsets.only(left: accentWidth),
          child: inner,
        ),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final radius = borderRadius ?? AppSpacing.roundedLg;
    final bg = backgroundColor ?? AppColors.surface;
    final border = borderColor ?? AppColors.outlineSubtle;

    if ((onTap != null || onLongPress != null) && isInteractive) {
      return Container(
        margin: margin,
        child: Material(
          color: bg,
          shape: RoundedRectangleBorder(
            borderRadius: radius,
            side: BorderSide(color: border, width: 1.0),
          ),
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            onTap: onTap,
            onLongPress: onLongPress,
            splashColor: AppColors.infoCyan.withOpacity(0.08),
            highlightColor: AppColors.surfaceHighlight.withOpacity(0.5),
            child: _wrapAccent(Container(padding: padding, child: child)),
          ),
        ),
      );
    }

    return Container(
      margin: margin,
      decoration: BoxDecoration(
        color: bg,
        borderRadius: radius,
        border: Border.all(color: border, width: 1.0),
      ),
      clipBehavior: Clip.antiAlias,
      child: _wrapAccent(Container(padding: padding, child: child)),
    );
  }
}
