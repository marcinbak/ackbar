import 'package:flutter/material.dart';

/// Design tokens for the Ackbar Mobile UI derived from the
/// "Terminal Velocity / Ackbar Command" Stitch design system.
abstract final class AppColors {
  // Base Surface & Canvas Colors
  static const Color background = Color(0xFF0E1015);
  static const Color surface = Color(0xFF161922);
  static const Color surfaceHighlight = Color(0xFF1E293B);
  static const Color surfaceContainerHigh = Color(0xFF282A2F);
  static const Color terminalBlack = Color(0xFF050608);

  // Border & Outline Colors
  static const Color outline = Color(0xFF3C4A42);
  static const Color outlineSubtle = Color(0xFF232834);
  static const Color outlineFocused = Color(0xFF06B6D4);

  // Status & Semantic Accent Colors
  static const Color statusEmerald = Color(0xFF10B981); // Active / Idle / Success
  static const Color statusAmber = Color(0xFFF59E0B);   // Working / In-Progress
  static const Color statusAmberLight = Color(0xFFFFB95F);
  static const Color statusCoral = Color(0xFFEF4444);   // Blocked / Error / Attention
  static const Color infoCyan = Color(0xFF06B6D4);      // Telemetry / Primary Accent
  static const Color statusOffline = Color(0xFF64748B); // Disconnected / Inactive

  // Text & Content Colors
  static const Color textPrimary = Color(0xFFF8FAFC);
  static const Color textSecondary = Color(0xFF94A3B8);
  static const Color textMuted = Color(0xFF64748B);
  static const Color textDisabled = Color(0xFF475569);

  // Gradients
  static const LinearGradient cyanEmeraldGradient = LinearGradient(
    colors: [infoCyan, statusEmerald],
    begin: Alignment.centerLeft,
    end: Alignment.centerRight,
  );

  static const LinearGradient amberCoralGradient = LinearGradient(
    colors: [statusAmber, statusCoral],
    begin: Alignment.centerLeft,
    end: Alignment.centerRight,
  );

  static const LinearGradient terminalHeaderGradient = LinearGradient(
    colors: [Color(0xFF161922), Color(0xFF0E1015)],
    begin: Alignment.topCenter,
    end: Alignment.bottomCenter,
  );
}

/// ThemeExtension allowing access to semantic Ackbar tokens via Theme.of(context).
@immutable
class AckbarColorsExtension extends ThemeExtension<AckbarColorsExtension> {
  final Color background;
  final Color surface;
  final Color surfaceHighlight;
  final Color surfaceContainerHigh;
  final Color terminalBlack;
  final Color outline;
  final Color outlineSubtle;
  final Color statusEmerald;
  final Color statusAmber;
  final Color statusAmberLight;
  final Color statusCoral;
  final Color infoCyan;
  final Color statusOffline;
  final Color textPrimary;
  final Color textSecondary;
  final Color textMuted;

  const AckbarColorsExtension({
    required this.background,
    required this.surface,
    required this.surfaceHighlight,
    required this.surfaceContainerHigh,
    required this.terminalBlack,
    required this.outline,
    required this.outlineSubtle,
    required this.statusEmerald,
    required this.statusAmber,
    required this.statusAmberLight,
    required this.statusCoral,
    required this.infoCyan,
    required this.statusOffline,
    required this.textPrimary,
    required this.textSecondary,
    required this.textMuted,
  });

  static const AckbarColorsExtension dark = AckbarColorsExtension(
    background: AppColors.background,
    surface: AppColors.surface,
    surfaceHighlight: AppColors.surfaceHighlight,
    surfaceContainerHigh: AppColors.surfaceContainerHigh,
    terminalBlack: AppColors.terminalBlack,
    outline: AppColors.outline,
    outlineSubtle: AppColors.outlineSubtle,
    statusEmerald: AppColors.statusEmerald,
    statusAmber: AppColors.statusAmber,
    statusAmberLight: AppColors.statusAmberLight,
    statusCoral: AppColors.statusCoral,
    infoCyan: AppColors.infoCyan,
    statusOffline: AppColors.statusOffline,
    textPrimary: AppColors.textPrimary,
    textSecondary: AppColors.textSecondary,
    textMuted: AppColors.textMuted,
  );

  @override
  AckbarColorsExtension copyWith({
    Color? background,
    Color? surface,
    Color? surfaceHighlight,
    Color? surfaceContainerHigh,
    Color? terminalBlack,
    Color? outline,
    Color? outlineSubtle,
    Color? statusEmerald,
    Color? statusAmber,
    Color? statusAmberLight,
    Color? statusCoral,
    Color? infoCyan,
    Color? statusOffline,
    Color? textPrimary,
    Color? textSecondary,
    Color? textMuted,
  }) {
    return AckbarColorsExtension(
      background: background ?? this.background,
      surface: surface ?? this.surface,
      surfaceHighlight: surfaceHighlight ?? this.surfaceHighlight,
      surfaceContainerHigh: surfaceContainerHigh ?? this.surfaceContainerHigh,
      terminalBlack: terminalBlack ?? this.terminalBlack,
      outline: outline ?? this.outline,
      outlineSubtle: outlineSubtle ?? this.outlineSubtle,
      statusEmerald: statusEmerald ?? this.statusEmerald,
      statusAmber: statusAmber ?? this.statusAmber,
      statusAmberLight: statusAmberLight ?? this.statusAmberLight,
      statusCoral: statusCoral ?? this.statusCoral,
      infoCyan: infoCyan ?? this.infoCyan,
      statusOffline: statusOffline ?? this.statusOffline,
      textPrimary: textPrimary ?? this.textPrimary,
      textSecondary: textSecondary ?? this.textSecondary,
      textMuted: textMuted ?? this.textMuted,
    );
  }

  @override
  AckbarColorsExtension lerp(ThemeExtension<AckbarColorsExtension>? other, double t) {
    if (other is! AckbarColorsExtension) return this;
    return AckbarColorsExtension(
      background: Color.lerp(background, other.background, t)!,
      surface: Color.lerp(surface, other.surface, t)!,
      surfaceHighlight: Color.lerp(surfaceHighlight, other.surfaceHighlight, t)!,
      surfaceContainerHigh: Color.lerp(surfaceContainerHigh, other.surfaceContainerHigh, t)!,
      terminalBlack: Color.lerp(terminalBlack, other.terminalBlack, t)!,
      outline: Color.lerp(outline, other.outline, t)!,
      outlineSubtle: Color.lerp(outlineSubtle, other.outlineSubtle, t)!,
      statusEmerald: Color.lerp(statusEmerald, other.statusEmerald, t)!,
      statusAmber: Color.lerp(statusAmber, other.statusAmber, t)!,
      statusAmberLight: Color.lerp(statusAmberLight, other.statusAmberLight, t)!,
      statusCoral: Color.lerp(statusCoral, other.statusCoral, t)!,
      infoCyan: Color.lerp(infoCyan, other.infoCyan, t)!,
      statusOffline: Color.lerp(statusOffline, other.statusOffline, t)!,
      textPrimary: Color.lerp(textPrimary, other.textPrimary, t)!,
      textSecondary: Color.lerp(textSecondary, other.textSecondary, t)!,
      textMuted: Color.lerp(textMuted, other.textMuted, t)!,
    );
  }
}
