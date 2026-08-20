import 'package:flutter/material.dart';
import 'app_colors.dart';

/// Typography configuration for Ackbar Mobile.
/// - Primary UI Font: Inter (Geist fallback / clean geometric sans-serif)
/// - Monospace Font: JetBrains Mono (Terminal, Session IDs, Badges, Tokens, Timestamps)
abstract final class AppTypography {
  // Base font family getters
  static const String uiFontFamily = 'Inter';
  static const String monoFontFamily = 'JetBrains Mono';

  static TextStyle _sans({
    required double fontSize,
    FontWeight fontWeight = FontWeight.w400,
    double? letterSpacing,
    Color color = AppColors.textPrimary,
    double? height,
  }) {
    return TextStyle(
      fontFamily: uiFontFamily,
      fontSize: fontSize,
      fontWeight: fontWeight,
      letterSpacing: letterSpacing,
      color: color,
      height: height,
    );
  }

  static TextStyle _mono({
    required double fontSize,
    FontWeight fontWeight = FontWeight.w500,
    double? letterSpacing,
    Color color = AppColors.textPrimary,
    double? height,
  }) {
    return TextStyle(
      fontFamily: monoFontFamily,
      fontSize: fontSize,
      fontWeight: fontWeight,
      letterSpacing: letterSpacing,
      color: color,
      height: height,
    );
  }

  // Headings & Display (UI Font)
  static TextStyle get displayLarge => _sans(
        fontSize: 32,
        fontWeight: FontWeight.w700,
        letterSpacing: -0.8,
        color: AppColors.textPrimary,
        height: 1.2,
      );

  static TextStyle get displayMedium => _sans(
        fontSize: 26,
        fontWeight: FontWeight.w700,
        letterSpacing: -0.6,
        color: AppColors.textPrimary,
        height: 1.25,
      );

  static TextStyle get headlineLarge => _sans(
        fontSize: 22,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.4,
        color: AppColors.textPrimary,
        height: 1.3,
      );

  static TextStyle get headlineMedium => _sans(
        fontSize: 18,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.2,
        color: AppColors.textPrimary,
        height: 1.35,
      );

  static TextStyle get titleLarge => _sans(
        fontSize: 16,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.1,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get titleMedium => _sans(
        fontSize: 14,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.0,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get titleSmall => _sans(
        fontSize: 13,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.1,
        color: AppColors.textSecondary,
        height: 1.4,
      );

  // Body text (UI Font)
  static TextStyle get bodyLarge => _sans(
        fontSize: 15,
        fontWeight: FontWeight.w400,
        letterSpacing: 0.0,
        color: AppColors.textPrimary,
        height: 1.5,
      );

  static TextStyle get bodyMedium => _sans(
        fontSize: 13,
        fontWeight: FontWeight.w400,
        letterSpacing: 0.0,
        color: AppColors.textSecondary,
        height: 1.45,
      );

  static TextStyle get bodySmall => _sans(
        fontSize: 12,
        fontWeight: FontWeight.w400,
        letterSpacing: 0.0,
        color: AppColors.textMuted,
        height: 1.4,
      );

  // Monospace / Code / Telemetry / Badges (JetBrains Mono)
  static TextStyle get codeLg => _mono(
        fontSize: 15,
        fontWeight: FontWeight.w500,
        letterSpacing: -0.2,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get codeMd => _mono(
        fontSize: 13,
        fontWeight: FontWeight.w500,
        letterSpacing: -0.1,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get codeSm => _mono(
        fontSize: 11.5,
        fontWeight: FontWeight.w500,
        letterSpacing: 0.0,
        color: AppColors.textSecondary,
        height: 1.35,
      );

  static TextStyle get codeXs => _mono(
        fontSize: 10,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.2,
        color: AppColors.textMuted,
        height: 1.3,
      );

  // Status Badge Text
  static TextStyle get badgeText => _mono(
        fontSize: 11,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.3,
        height: 1.2,
      );

  // Touch Key Accessory Text
  static TextStyle get touchKeyText => _mono(
        fontSize: 12.5,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.2,
        color: AppColors.textPrimary,
      );

  // App Bar Title
  static TextStyle get appBarTitle => _mono(
        fontSize: 15,
        fontWeight: FontWeight.w700,
        letterSpacing: 0.8,
        color: AppColors.textPrimary,
      );

  /// Builds the ThemeData.textTheme
  static TextTheme buildTextTheme() {
    return TextTheme(
      displayLarge: displayLarge,
      displayMedium: displayMedium,
      headlineLarge: headlineLarge,
      headlineMedium: headlineMedium,
      titleLarge: titleLarge,
      titleMedium: titleMedium,
      titleSmall: titleSmall,
      bodyLarge: bodyLarge,
      bodyMedium: bodyMedium,
      bodySmall: bodySmall,
      labelLarge: _sans(
        fontSize: 13,
        fontWeight: FontWeight.w600,
        color: AppColors.textPrimary,
      ),
      labelMedium: _sans(
        fontSize: 12,
        fontWeight: FontWeight.w500,
        color: AppColors.textSecondary,
      ),
      labelSmall: _sans(
        fontSize: 10,
        fontWeight: FontWeight.w500,
        color: AppColors.textMuted,
      ),
    );
  }
}
