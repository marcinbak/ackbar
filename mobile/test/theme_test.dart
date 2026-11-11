import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:ackbar_mobile/core/theme/app_colors.dart';
import 'package:ackbar_mobile/core/theme/app_spacing.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/core/theme/app_typography.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('AppColors Stitch "Terminal Velocity" Design Tokens', () {
    test('Base Surface & Canvas Color tokens match Stitch design specification', () {
      expect(AppColors.background, equals(const Color(0xFF0E1015)));
      expect(AppColors.surface, equals(const Color(0xFF161922)));
      expect(AppColors.surfaceHighlight, equals(const Color(0xFF1E293B)));
      expect(AppColors.surfaceContainerHigh, equals(const Color(0xFF282A2F)));
      expect(AppColors.terminalBlack, equals(const Color(0xFF050608)));
    });

    test('Border & Outline Color tokens match specification', () {
      expect(AppColors.outline, equals(const Color(0xFF3C4A42)));
      expect(AppColors.outlineSubtle, equals(const Color(0xFF232834)));
      expect(AppColors.outlineFocused, equals(const Color(0xFF06B6D4)));
    });

    test('Status & Semantic Accent Color tokens match specification', () {
      expect(AppColors.statusEmerald, equals(const Color(0xFF10B981)));
      expect(AppColors.statusAmber, equals(const Color(0xFFF59E0B)));
      expect(AppColors.statusAmberLight, equals(const Color(0xFFFFB95F)));
      expect(AppColors.statusCoral, equals(const Color(0xFFEF4444)));
      expect(AppColors.infoCyan, equals(const Color(0xFF06B6D4)));
      expect(AppColors.statusOffline, equals(const Color(0xFF64748B)));
    });

    test('Text & Content Color tokens match specification', () {
      expect(AppColors.textPrimary, equals(const Color(0xFFF8FAFC)));
      expect(AppColors.textSecondary, equals(const Color(0xFF94A3B8)));
      expect(AppColors.textMuted, equals(const Color(0xFF64748B)));
      expect(AppColors.textDisabled, equals(const Color(0xFF475569)));
    });

    test('Linear gradients have correct colors and alignments', () {
      expect(AppColors.cyanEmeraldGradient.colors, equals([AppColors.infoCyan, AppColors.statusEmerald]));
      expect(AppColors.cyanEmeraldGradient.begin, equals(Alignment.centerLeft));
      expect(AppColors.cyanEmeraldGradient.end, equals(Alignment.centerRight));

      expect(AppColors.amberCoralGradient.colors, equals([AppColors.statusAmber, AppColors.statusCoral]));
      expect(AppColors.terminalHeaderGradient.colors, equals([const Color(0xFF161922), const Color(0xFF0E1015)]));
    });
  });

  group('AckbarColorsExtension Theme Extension Tests', () {
    test('AckbarColorsExtension.dark initializes with all design tokens', () {
      const ext = AckbarColorsExtension.dark;
      expect(ext.background, equals(AppColors.background));
      expect(ext.surface, equals(AppColors.surface));
      expect(ext.surfaceHighlight, equals(AppColors.surfaceHighlight));
      expect(ext.surfaceContainerHigh, equals(AppColors.surfaceContainerHigh));
      expect(ext.terminalBlack, equals(AppColors.terminalBlack));
      expect(ext.outline, equals(AppColors.outline));
      expect(ext.outlineSubtle, equals(AppColors.outlineSubtle));
      expect(ext.statusEmerald, equals(AppColors.statusEmerald));
      expect(ext.statusAmber, equals(AppColors.statusAmber));
      expect(ext.statusAmberLight, equals(AppColors.statusAmberLight));
      expect(ext.statusCoral, equals(AppColors.statusCoral));
      expect(ext.infoCyan, equals(AppColors.infoCyan));
      expect(ext.statusOffline, equals(AppColors.statusOffline));
      expect(ext.textPrimary, equals(AppColors.textPrimary));
      expect(ext.textSecondary, equals(AppColors.textSecondary));
      expect(ext.textMuted, equals(AppColors.textMuted));
    });

    test('AckbarColorsExtension copyWith modifies individual colors', () {
      const ext = AckbarColorsExtension.dark;
      final custom = ext.copyWith(
        background: Colors.black,
        statusEmerald: Colors.green,
      );

      expect(custom.background, equals(Colors.black));
      expect(custom.statusEmerald, equals(Colors.green));
      expect(custom.surface, equals(AppColors.surface));
      expect(custom.infoCyan, equals(AppColors.infoCyan));
    });

    test('AckbarColorsExtension lerp smoothly interpolates colors', () {
      const extA = AckbarColorsExtension.dark;
      final extB = extA.copyWith(background: const Color(0xFF000000));

      final lerpedHalf = extA.lerp(extB, 0.5);
      expect(lerpedHalf.background, equals(Color.lerp(extA.background, const Color(0xFF000000), 0.5)));

      final lerpedNone = extA.lerp(null, 0.5);
      expect(lerpedNone, equals(extA));
    });
  });

  group('AppSpacing Tokens Tests', () {
    test('Base unit and discrete scale tokens', () {
      expect(AppSpacing.unit, equals(4.0));
      expect(AppSpacing.xxs, equals(2.0));
      expect(AppSpacing.xs, equals(4.0));
      expect(AppSpacing.sm, equals(8.0));
      expect(AppSpacing.md, equals(12.0));
      expect(AppSpacing.lg, equals(16.0));
      expect(AppSpacing.xl, equals(20.0));
      expect(AppSpacing.xxl, equals(24.0));
      expect(AppSpacing.xxxl, equals(32.0));
      expect(AppSpacing.huge, equals(48.0));
    });

    test('Corner Radii and BorderRadius instances', () {
      expect(AppSpacing.radiusXs, equals(2.0));
      expect(AppSpacing.radiusSm, equals(4.0));
      expect(AppSpacing.radiusMd, equals(8.0));
      expect(AppSpacing.radiusLg, equals(12.0));
      expect(AppSpacing.radiusXl, equals(16.0));
      expect(AppSpacing.radiusFull, equals(999.0));

      expect(AppSpacing.roundedSm, equals(BorderRadius.circular(4.0)));
      expect(AppSpacing.roundedMd, equals(BorderRadius.circular(8.0)));
      expect(AppSpacing.roundedLg, equals(BorderRadius.circular(12.0)));
      expect(AppSpacing.roundedXl, equals(BorderRadius.circular(16.0)));
      expect(AppSpacing.roundedFull, equals(BorderRadius.circular(999.0)));
    });

    test('Common EdgeInsets instances', () {
      expect(AppSpacing.paddingScreen, equals(const EdgeInsets.symmetric(horizontal: 16.0, vertical: 16.0)));
      expect(AppSpacing.paddingScreenHorizontal, equals(const EdgeInsets.symmetric(horizontal: 16.0)));
      expect(AppSpacing.paddingCard, equals(const EdgeInsets.all(16.0)));
      expect(AppSpacing.paddingCardDense, equals(const EdgeInsets.all(12.0)));
      expect(AppSpacing.paddingPill, equals(const EdgeInsets.symmetric(horizontal: 10, vertical: 4)));
      expect(AppSpacing.paddingBadge, equals(const EdgeInsets.symmetric(horizontal: 8, vertical: 3)));
      expect(AppSpacing.paddingInput, equals(const EdgeInsets.symmetric(horizontal: 12.0, vertical: 8.0)));
    });

    test('Gap sized box instances have exact dimensions', () {
      expect(AppSpacing.gapW2.width, equals(2.0));
      expect(AppSpacing.gapW4.width, equals(4.0));
      expect(AppSpacing.gapW8.width, equals(8.0));
      expect(AppSpacing.gapW12.width, equals(12.0));
      expect(AppSpacing.gapW16.width, equals(16.0));
      expect(AppSpacing.gapW24.width, equals(24.0));

      expect(AppSpacing.gapH2.height, equals(2.0));
      expect(AppSpacing.gapH4.height, equals(4.0));
      expect(AppSpacing.gapH8.height, equals(8.0));
      expect(AppSpacing.gapH12.height, equals(12.0));
      expect(AppSpacing.gapH16.height, equals(16.0));
      expect(AppSpacing.gapH24.height, equals(24.0));
      expect(AppSpacing.gapH32.height, equals(32.0));
      expect(AppSpacing.gapH48.height, equals(48.0));
    });
  });

  group('AppTypography Tokens & TextTheme Tests', () {
    test('Font family getters return valid fonts', () {
      expect(AppTypography.uiFontFamily, isNotEmpty);
      expect(AppTypography.monoFontFamily, isNotEmpty);
    });

    test('Headings and Body text styles configure expected font sizes and weights', () {
      expect(AppTypography.displayLarge.fontSize, equals(32));
      expect(AppTypography.displayLarge.fontWeight, equals(FontWeight.w700));
      expect(AppTypography.displayMedium.fontSize, equals(26));
      expect(AppTypography.headlineLarge.fontSize, equals(22));
      expect(AppTypography.headlineMedium.fontSize, equals(18));
      expect(AppTypography.titleLarge.fontSize, equals(16));
      expect(AppTypography.titleMedium.fontSize, equals(14));
      expect(AppTypography.titleSmall.fontSize, equals(13));

      expect(AppTypography.bodyLarge.fontSize, equals(15));
      expect(AppTypography.bodyLarge.fontWeight, equals(FontWeight.w400));
      expect(AppTypography.bodyMedium.fontSize, equals(13));
      expect(AppTypography.bodySmall.fontSize, equals(12));
    });

    test('Monospace styles configure JetBrains Mono and correct metrics', () {
      expect(AppTypography.codeLg.fontSize, equals(15));
      expect(AppTypography.codeMd.fontSize, equals(13));
      expect(AppTypography.codeSm.fontSize, equals(11.5));
      expect(AppTypography.codeXs.fontSize, equals(10));
      expect(AppTypography.badgeText.fontSize, equals(11));
      expect(AppTypography.touchKeyText.fontSize, equals(12.5));
      expect(AppTypography.appBarTitle.fontSize, equals(15));
    });

    test('buildTextTheme creates complete TextTheme', () {
      final textTheme = AppTypography.buildTextTheme();
      expect(textTheme.displayLarge, isNotNull);
      expect(textTheme.headlineLarge, isNotNull);
      expect(textTheme.titleMedium, isNotNull);
      expect(textTheme.bodyLarge, isNotNull);
      expect(textTheme.labelLarge, isNotNull);
      expect(textTheme.labelMedium, isNotNull);
      expect(textTheme.labelSmall, isNotNull);
    });
  });

  group('AppTheme.darkTheme Configuration Tests', () {
    late ThemeData theme;

    setUp(() {
      theme = AppTheme.darkTheme;
    });

    test('ThemeData is configured with Material 3 dark brightness', () {
      expect(theme.useMaterial3, isTrue);
      expect(theme.brightness, equals(Brightness.dark));
      expect(theme.scaffoldBackgroundColor, equals(AppColors.background));
      expect(theme.canvasColor, equals(AppColors.background));
      expect(theme.dividerColor, equals(AppColors.outlineSubtle));
    });

    test('ColorScheme matches Ackbar design system specification', () {
      final scheme = theme.colorScheme;
      expect(scheme.brightness, equals(Brightness.dark));
      expect(scheme.primary, equals(AppColors.infoCyan));
      expect(scheme.onPrimary, equals(AppColors.terminalBlack));
      expect(scheme.primaryContainer, equals(AppColors.surfaceHighlight));
      expect(scheme.onPrimaryContainer, equals(AppColors.infoCyan));
      expect(scheme.secondary, equals(AppColors.statusEmerald));
      expect(scheme.onSecondary, equals(AppColors.terminalBlack));
      expect(scheme.secondaryContainer, equals(const Color(0xFF064E3B)));
      expect(scheme.onSecondaryContainer, equals(AppColors.statusEmerald));
      expect(scheme.tertiary, equals(AppColors.statusAmber));
      expect(scheme.onTertiary, equals(AppColors.terminalBlack));
      expect(scheme.error, equals(AppColors.statusCoral));
      expect(scheme.onError, equals(AppColors.textPrimary));
      expect(scheme.surface, equals(AppColors.surface));
      expect(scheme.onSurface, equals(AppColors.textPrimary));
      expect(scheme.outline, equals(AppColors.outline));
      expect(scheme.outlineVariant, equals(AppColors.outlineSubtle));
    });

    test('AppBarTheme has transparent background and 0 elevation', () {
      final appBarTheme = theme.appBarTheme;
      expect(appBarTheme.backgroundColor, equals(AppColors.background));
      expect(appBarTheme.elevation, equals(0));
      expect(appBarTheme.scrolledUnderElevation, equals(0));
      expect(appBarTheme.centerTitle, isFalse);
    });

    test('CardTheme is configured with surface background and 12px rounded borders', () {
      final cardTheme = theme.cardTheme;
      expect(cardTheme.color, equals(AppColors.surface));
      expect(cardTheme.elevation, equals(0));
      expect(cardTheme.margin, equals(EdgeInsets.zero));
      expect(cardTheme.shape, isA<RoundedRectangleBorder>());
      final shape = cardTheme.shape as RoundedRectangleBorder;
      expect(shape.borderRadius, equals(AppSpacing.roundedLg));
      expect(shape.side.color, equals(AppColors.outlineSubtle));
    });

    test('NavigationBarTheme is configured with surface background and pill indicator', () {
      final navBarTheme = theme.navigationBarTheme;
      expect(navBarTheme.backgroundColor, equals(AppColors.surface));
      expect(navBarTheme.indicatorColor, equals(AppColors.surfaceHighlight));
      expect(navBarTheme.labelTextStyle, isNotNull);

      // Verify selected vs unselected text style resolution
      final selectedStyle = navBarTheme.labelTextStyle!.resolve({WidgetState.selected});
      expect(selectedStyle?.color, equals(AppColors.infoCyan));
      expect(selectedStyle?.fontWeight, equals(FontWeight.w600));

      final unselectedStyle = navBarTheme.labelTextStyle!.resolve({});
      expect(unselectedStyle?.color, equals(AppColors.textMuted));
    });

    test('BottomNavigationBarTheme is configured with fixed type and surface background', () {
      final btmTheme = theme.bottomNavigationBarTheme;
      expect(btmTheme.backgroundColor, equals(AppColors.surface));
      expect(btmTheme.selectedItemColor, equals(AppColors.infoCyan));
      expect(btmTheme.unselectedItemColor, equals(AppColors.textMuted));
      expect(btmTheme.type, equals(BottomNavigationBarType.fixed));
      expect(btmTheme.elevation, equals(0));
    });

    test('InputDecorationTheme is configured with filled highlight surface and rounded border', () {
      final inputTheme = theme.inputDecorationTheme;
      expect(inputTheme.filled, isTrue);
      expect(inputTheme.fillColor, equals(AppColors.surfaceHighlight));
      expect(inputTheme.border, isA<OutlineInputBorder>());
      expect(inputTheme.focusedBorder, isA<OutlineInputBorder>());
      final focusedBorder = inputTheme.focusedBorder as OutlineInputBorder;
      expect(focusedBorder.borderSide.color, equals(AppColors.infoCyan));
      expect(focusedBorder.borderSide.width, equals(1.5));
    });

    test('Theme extensions contain AckbarColorsExtension.dark', () {
      final ext = theme.extension<AckbarColorsExtension>();
      expect(ext, isNotNull);
      expect(ext?.background, equals(AppColors.background));
      expect(ext?.statusEmerald, equals(AppColors.statusEmerald));
      expect(ext?.infoCyan, equals(AppColors.infoCyan));
    });

    testWidgets('AckbarThemeContextX extension provides convenient access in BuildContext', (tester) async {
      late BuildContext testContext;

      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.darkTheme,
          home: Builder(
            builder: (context) {
              testContext = context;
              return const Scaffold(body: Text('Theme Context Test'));
            },
          ),
        ),
      );

      expect(testContext.theme.brightness, equals(Brightness.dark));
      expect(testContext.theme.scaffoldBackgroundColor, equals(AppColors.background));
      expect(testContext.colors.primary, equals(AppColors.infoCyan));
      expect(testContext.textTheme.displayLarge, isNotNull);
      expect(testContext.ackbarColors.statusAmber, equals(AppColors.statusAmber));
    });
  });
}
