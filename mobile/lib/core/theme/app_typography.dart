import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';
import 'app_colors.dart';

/// Typography configuration for Ackbar Mobile.
/// - Primary UI Font: Inter (Geist fallback / clean geometric sans-serif)
/// - Monospace Font: JetBrains Mono (Terminal, Session IDs, Badges, Tokens, Timestamps)
abstract final class AppTypography {
  // Base font family getters
  static String get uiFontFamily => GoogleFonts.inter().fontFamily ?? 'Inter';
  static String get monoFontFamily => GoogleFonts.jetBrainsMono().fontFamily ?? 'JetBrains Mono';

  // Headings & Display (UI Font)
  static TextStyle get displayLarge => GoogleFonts.inter(
        fontSize: 32,
        fontWeight: FontWeight.w700,
        letterSpacing: -0.8,
        color: AppColors.textPrimary,
        height: 1.2,
      );

  static TextStyle get displayMedium => GoogleFonts.inter(
        fontSize: 26,
        fontWeight: FontWeight.w700,
        letterSpacing: -0.6,
        color: AppColors.textPrimary,
        height: 1.25,
      );

  static TextStyle get headlineLarge => GoogleFonts.inter(
        fontSize: 22,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.4,
        color: AppColors.textPrimary,
        height: 1.3,
      );

  static TextStyle get headlineMedium => GoogleFonts.inter(
        fontSize: 18,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.2,
        color: AppColors.textPrimary,
        height: 1.35,
      );

  static TextStyle get titleLarge => GoogleFonts.inter(
        fontSize: 16,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.1,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get titleMedium => GoogleFonts.inter(
        fontSize: 14,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.0,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get titleSmall => GoogleFonts.inter(
        fontSize: 13,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.1,
        color: AppColors.textSecondary,
        height: 1.4,
      );

  // Body text (UI Font)
  static TextStyle get bodyLarge => GoogleFonts.inter(
        fontSize: 15,
        fontWeight: FontWeight.w400,
        letterSpacing: 0.0,
        color: AppColors.textPrimary,
        height: 1.5,
      );

  static TextStyle get bodyMedium => GoogleFonts.inter(
        fontSize: 13,
        fontWeight: FontWeight.w400,
        letterSpacing: 0.0,
        color: AppColors.textSecondary,
        height: 1.45,
      );

  static TextStyle get bodySmall => GoogleFonts.inter(
        fontSize: 12,
        fontWeight: FontWeight.w400,
        letterSpacing: 0.0,
        color: AppColors.textMuted,
        height: 1.4,
      );

  // Monospace / Code / Telemetry / Badges (JetBrains Mono)
  static TextStyle get codeLg => GoogleFonts.jetBrainsMono(
        fontSize: 15,
        fontWeight: FontWeight.w500,
        letterSpacing: -0.2,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get codeMd => GoogleFonts.jetBrainsMono(
        fontSize: 13,
        fontWeight: FontWeight.w500,
        letterSpacing: -0.1,
        color: AppColors.textPrimary,
        height: 1.4,
      );

  static TextStyle get codeSm => GoogleFonts.jetBrainsMono(
        fontSize: 11.5,
        fontWeight: FontWeight.w500,
        letterSpacing: 0.0,
        color: AppColors.textSecondary,
        height: 1.35,
      );

  static TextStyle get codeXs => GoogleFonts.jetBrainsMono(
        fontSize: 10,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.2,
        color: AppColors.textMuted,
        height: 1.3,
      );

  // Status Badge Text
  static TextStyle get badgeText => GoogleFonts.jetBrainsMono(
        fontSize: 11,
        fontWeight: FontWeight.w600,
        letterSpacing: 0.3,
        height: 1.2,
      );

  // Touch Key Accessory Text
  static TextStyle get touchKeyText => GoogleFonts.jetBrainsMono(
        fontSize: 12.5,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.2,
        color: AppColors.textPrimary,
      );

  // App Bar Title
  static TextStyle get appBarTitle => GoogleFonts.jetBrainsMono(
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
      labelLarge: GoogleFonts.inter(
        fontSize: 13,
        fontWeight: FontWeight.w600,
        color: AppColors.textPrimary,
      ),
      labelMedium: GoogleFonts.inter(
        fontSize: 12,
        fontWeight: FontWeight.w500,
        color: AppColors.textSecondary,
      ),
      labelSmall: GoogleFonts.inter(
        fontSize: 10,
        fontWeight: FontWeight.w500,
        color: AppColors.textMuted,
      ),
    );
  }
}
