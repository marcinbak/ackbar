import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../layout/responsive_breakpoints.dart';
import '../theme/app_colors.dart';

abstract final class AdaptiveSideSheet {
  static Future<T?> show<T>(
    BuildContext context, {
    required WidgetBuilder builder,
    double width = 580,
    bool isScrollControlled = true,
    Color? phoneBackgroundColor = Colors.transparent,
    ShapeBorder? phoneShape,
  }) {
    if (!ResponsiveBreakpoints.isTablet(context)) {
      return showModalBottomSheet<T>(
        context: context,
        isScrollControlled: isScrollControlled,
        backgroundColor: phoneBackgroundColor,
        shape: phoneShape,
        builder: builder,
      );
    }

    return showGeneralDialog<T>(
      context: context,
      barrierDismissible: true,
      barrierLabel: MaterialLocalizations.of(context).modalBarrierDismissLabel,
      barrierColor: Colors.black54,
      transitionDuration: const Duration(milliseconds: 220),
      pageBuilder: (ctx, _, __) => Align(
        alignment: Alignment.centerRight,
        child: Material(
          color: AppColors.surface,
          child: SizedBox(
            key: const Key('adaptive_side_sheet_container'),
            width: math.min(width, MediaQuery.sizeOf(ctx).width * 0.88),
            height: double.infinity,
            child: SafeArea(
              left: false,
              child: builder(ctx),
            ),
          ),
        ),
      ),
      transitionBuilder: (_, animation, __, child) {
        final offset = Tween<Offset>(
          begin: const Offset(1, 0),
          end: Offset.zero,
        ).animate(
          CurvedAnimation(parent: animation, curve: Curves.easeOutCubic),
        );
        return SlideTransition(position: offset, child: child);
      },
    );
  }
}
