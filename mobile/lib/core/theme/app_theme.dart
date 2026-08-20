import 'package:flutter/material.dart';
import 'app_colors.dart';
import 'app_spacing.dart';
import 'app_typography.dart';

/// Complete dark theme configuration for Ackbar Mobile.
abstract final class AppTheme {
  static ThemeData get darkTheme {
    final textTheme = AppTypography.buildTextTheme();

    final colorScheme = const ColorScheme.dark(
      primary: AppColors.infoCyan,
      onPrimary: AppColors.terminalBlack,
      primaryContainer: AppColors.surfaceHighlight,
      onPrimaryContainer: AppColors.infoCyan,
      secondary: AppColors.statusEmerald,
      onSecondary: AppColors.terminalBlack,
      secondaryContainer: Color(0xFF064E3B),
      onSecondaryContainer: AppColors.statusEmerald,
      tertiary: AppColors.statusAmber,
      onTertiary: AppColors.terminalBlack,
      error: AppColors.statusCoral,
      onError: AppColors.textPrimary,
      surface: AppColors.surface,
      onSurface: AppColors.textPrimary,
      outline: AppColors.outline,
      outlineVariant: AppColors.outlineSubtle,
      shadow: Colors.black,
      scrim: Colors.black54,
    );

    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.dark,
      fontFamily: AppTypography.uiFontFamily,
      colorScheme: colorScheme,
      scaffoldBackgroundColor: AppColors.background,
      canvasColor: AppColors.background,
      dividerColor: AppColors.outlineSubtle,
      textTheme: textTheme,
      extensions: const <ThemeExtension<dynamic>>[
        AckbarColorsExtension.dark,
      ],
      appBarTheme: AppBarTheme(
        backgroundColor: AppColors.background,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        titleTextStyle: AppTypography.appBarTitle,
        iconTheme: const IconThemeData(color: AppColors.textPrimary, size: 20),
      ),
      cardTheme: CardTheme(
        color: AppColors.surface,
        elevation: 0,
        margin: EdgeInsets.zero,
        clipBehavior: Clip.antiAlias,
        shape: RoundedRectangleBorder(
          borderRadius: AppSpacing.roundedLg,
          side: const BorderSide(color: AppColors.outlineSubtle, width: 1),
        ),
      ),
      bottomNavigationBarTheme: const BottomNavigationBarThemeData(
        backgroundColor: AppColors.surface,
        selectedItemColor: AppColors.infoCyan,
        unselectedItemColor: AppColors.textMuted,
        type: BottomNavigationBarType.fixed,
        elevation: 0,
        showSelectedLabels: true,
        showUnselectedLabels: true,
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: AppColors.surface,
        indicatorColor: AppColors.surfaceHighlight,
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return AppTypography.bodySmall.copyWith(
              color: AppColors.infoCyan,
              fontWeight: FontWeight.w600,
            );
          }
          return AppTypography.bodySmall.copyWith(color: AppColors.textMuted);
        }),
      ),
      dividerTheme: const DividerThemeData(
        color: AppColors.outlineSubtle,
        thickness: 1,
        space: 1,
      ),
      elevatedButtonTheme: ElevatedButtonThemeData(
        style: ElevatedButton.styleFrom(
          backgroundColor: AppColors.surfaceHighlight,
          foregroundColor: AppColors.textPrimary,
          elevation: 0,
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.md),
          shape: RoundedRectangleBorder(
            borderRadius: AppSpacing.roundedMd,
            side: const BorderSide(color: AppColors.outline, width: 1),
          ),
          textStyle: AppTypography.titleMedium,
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          foregroundColor: AppColors.infoCyan,
          side: const BorderSide(color: AppColors.outline, width: 1),
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.md),
          shape: RoundedRectangleBorder(
            borderRadius: AppSpacing.roundedMd,
          ),
          textStyle: AppTypography.titleMedium,
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: AppColors.surfaceHighlight,
        contentPadding: AppSpacing.paddingInput,
        border: OutlineInputBorder(
          borderRadius: AppSpacing.roundedMd,
          borderSide: const BorderSide(color: AppColors.outlineSubtle, width: 1),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: AppSpacing.roundedMd,
          borderSide: const BorderSide(color: AppColors.outlineSubtle, width: 1),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: AppSpacing.roundedMd,
          borderSide: const BorderSide(color: AppColors.infoCyan, width: 1.5),
        ),
        hintStyle: AppTypography.bodyMedium.copyWith(color: AppColors.textMuted),
      ),
    );
  }
}

/// Convenient context extensions for theme access across all widgets.
extension AckbarThemeContextX on BuildContext {
  /// Theme data shortcut
  ThemeData get theme => Theme.of(this);

  /// ColorScheme shortcut
  ColorScheme get colors => Theme.of(this).colorScheme;

  /// TextTheme shortcut
  TextTheme get textTheme => Theme.of(this).textTheme;

  /// Decoupled semantic Ackbar color tokens
  AckbarColorsExtension get ackbarColors =>
      Theme.of(this).extension<AckbarColorsExtension>() ?? AckbarColorsExtension.dark;
}
