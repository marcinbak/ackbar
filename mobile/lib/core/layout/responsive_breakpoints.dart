import 'dart:math' as math;

import 'package:flutter/widgets.dart';

abstract final class ResponsiveBreakpoints {
  // Adding viewInsets.bottom keeps Android ARC++ devices in tablet mode when
  // a docked keyboard shrinks window height mid-typing.
  static bool isTablet(BuildContext context) {
    final mq = MediaQuery.of(context);
    final fullH = mq.size.height + mq.viewInsets.bottom;
    return math.min(mq.size.width, fullH) >= 650 || mq.size.width >= 1000;
  }

  static bool isLandscapeSplit(BuildContext context) =>
      isTablet(context) && MediaQuery.sizeOf(context).width >= 900;

  static bool isTabletPortrait(BuildContext context) =>
      isTablet(context) && MediaQuery.sizeOf(context).width < 900;

  static bool isTwoColumnGrid(BuildContext context) =>
      isTablet(context) && MediaQuery.sizeOf(context).width >= 840;

  static bool isFourColumnKanban(BuildContext context) =>
      isTablet(context) && MediaQuery.sizeOf(context).width >= 960;

  static bool isAttentionSplit(BuildContext context) =>
      isTablet(context) && MediaQuery.sizeOf(context).width >= 720;

  static bool isKeyboardCompact(BuildContext context) {
    final mq = MediaQuery.of(context);
    return isTablet(context) && (mq.size.height - mq.viewInsets.bottom) < 480;
  }
}
