import 'package:flutter/material.dart';

/// 4px grid spacing and layout tokens for Ackbar Mobile.
abstract final class AppSpacing {
  // Base unit
  static const double unit = 4.0;

  // Discrete Scale
  static const double xxs = 2.0;
  static const double xs = 4.0;
  static const double sm = 8.0;          // stackGap
  static const double md = 12.0;         // gutter
  static const double lg = 16.0;         // standard padding
  static const double xl = 20.0;
  static const double xxl = 24.0;        // sectionGap
  static const double xxxl = 32.0;
  static const double huge = 48.0;

  // Corner Radii
  static const double radiusXs = 2.0;
  static const double radiusSm = 4.0;    // Tag / Badge / Button small
  static const double radiusMd = 8.0;    // Filter pill / Input / Key
  static const double radiusLg = 12.0;   // Card / Modal / Container
  static const double radiusXl = 16.0;   // Sheet / Large modal
  static const double radiusFull = 999.0;// Circle / Full pill

  // BorderRadius instances
  static const BorderRadius roundedSm = BorderRadius.all(Radius.circular(radiusSm));
  static const BorderRadius roundedMd = BorderRadius.all(Radius.circular(radiusMd));
  static const BorderRadius roundedLg = BorderRadius.all(Radius.circular(radiusLg));
  static const BorderRadius roundedXl = BorderRadius.all(Radius.circular(radiusXl));
  static const BorderRadius roundedFull = BorderRadius.all(Radius.circular(radiusFull));

  // Common Edge Insets
  static const EdgeInsets paddingScreen = EdgeInsets.symmetric(horizontal: lg, vertical: lg);
  static const EdgeInsets paddingScreenHorizontal = EdgeInsets.symmetric(horizontal: lg);
  static const EdgeInsets paddingCard = EdgeInsets.all(lg);
  static const EdgeInsets paddingCardDense = EdgeInsets.all(md);
  static const EdgeInsets paddingPill = EdgeInsets.symmetric(horizontal: 10, vertical: 4);
  static const EdgeInsets paddingBadge = EdgeInsets.symmetric(horizontal: 8, vertical: 3);
  static const EdgeInsets paddingInput = EdgeInsets.symmetric(horizontal: md, vertical: sm);

  // Horizontal Sized Box Spacers
  static const SizedBox gapW2 = SizedBox(width: xxs);
  static const SizedBox gapW4 = SizedBox(width: xs);
  static const SizedBox gapW6 = SizedBox(width: 6.0);
  static const SizedBox gapW8 = SizedBox(width: sm);
  static const SizedBox gapW12 = SizedBox(width: md);
  static const SizedBox gapW16 = SizedBox(width: lg);
  static const SizedBox gapW20 = SizedBox(width: xl);
  static const SizedBox gapW24 = SizedBox(width: xxl);

  // Vertical Sized Box Spacers
  static const SizedBox gapH2 = SizedBox(height: xxs);
  static const SizedBox gapH4 = SizedBox(height: xs);
  static const SizedBox gapH6 = SizedBox(height: 6.0);
  static const SizedBox gapH8 = SizedBox(height: sm);
  static const SizedBox gapH12 = SizedBox(height: md);
  static const SizedBox gapH16 = SizedBox(height: lg);
  static const SizedBox gapH20 = SizedBox(height: xl);
  static const SizedBox gapH24 = SizedBox(height: xxl);
  static const SizedBox gapH32 = SizedBox(height: xxxl);
  static const SizedBox gapH48 = SizedBox(height: huge);
}
