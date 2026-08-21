import 'dart:math' as math;
import 'package:flutter/material.dart';
import '../theme/app_colors.dart';

/// Renders resolution-independent vector icons for Claude Code, Google Antigravity, and OpenAI Codex.
class AgentLogo extends StatelessWidget {
  final String agent;
  final double size;
  final Color? color;
  final bool useBrandGradient;

  const AgentLogo({
    super.key,
    required this.agent,
    this.size = 14,
    this.color,
    this.useBrandGradient = true,
  });

  @override
  Widget build(BuildContext context) {
    final a = agent.toLowerCase();
    final effectiveColor = color ?? _defaultColor(a);

    return CustomPaint(
      size: Size(size, size),
      painter: _AgentLogoPainter(
        agentType: _resolveType(a),
        color: effectiveColor,
        useBrandGradient: useBrandGradient,
      ),
    );
  }

  static Color _defaultColor(String agent) {
    if (agent.contains('claude')) {
      return const Color(0xFFD97757); // Claude Code Terracotta / Orange
    } else if (agent.contains('antigravity') || agent.contains('gemini') || agent.contains('agy')) {
      return const Color(0xFF4285F4); // Google Blue / Multicolored
    } else if (agent.contains('codex') || agent.contains('openai')) {
      return const Color(0xFF10A37F); // OpenAI Emerald Green
    }
    return AppColors.textSecondary;
  }

  static _AgentType _resolveType(String agent) {
    if (agent.contains('claude')) {
      return _AgentType.claude;
    } else if (agent.contains('antigravity') || agent.contains('gemini') || agent.contains('agy')) {
      return _AgentType.antigravity;
    } else if (agent.contains('codex') || agent.contains('openai')) {
      return _AgentType.codex;
    }
    return _AgentType.generic;
  }
}

enum _AgentType { claude, antigravity, codex, generic }

class _AgentLogoPainter extends CustomPainter {
  final _AgentType agentType;
  final Color color;
  final bool useBrandGradient;

  _AgentLogoPainter({
    required this.agentType,
    required this.color,
    required this.useBrandGradient,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final w = size.width;
    final h = size.height;
    final cx = w / 2;
    final cy = h / 2;

    switch (agentType) {
      case _AgentType.claude:
        // Claude Code official pixelated CLI terminal creature
        final sx = w / 24.0;
        final sy = h / 24.0;

        final path = Path();
        path.moveTo(20.998 * sx, 10.949 * sy);
        path.lineTo(24 * sx, 10.949 * sy);
        path.lineTo(24 * sx, 14.051 * sy);
        path.lineTo(21 * sx, 14.051 * sy);
        path.lineTo(21 * sx, 17.079 * sy);
        path.lineTo(19.513 * sx, 17.079 * sy);
        path.lineTo(19.513 * sx, 20 * sy);
        path.lineTo(18 * sx, 20 * sy);
        path.lineTo(18 * sx, 17.079 * sy);
        path.lineTo(16.513 * sx, 17.079 * sy);
        path.lineTo(16.513 * sx, 20 * sy);
        path.lineTo(15 * sx, 20 * sy);
        path.lineTo(15 * sx, 17.079 * sy);
        path.lineTo(9 * sx, 17.079 * sy);
        path.lineTo(9 * sx, 20 * sy);
        path.lineTo(7.488 * sx, 20 * sy);
        path.lineTo(7.488 * sx, 17.079 * sy);
        path.lineTo(6 * sx, 17.079 * sy);
        path.lineTo(6 * sx, 20 * sy);
        path.lineTo(4.487 * sx, 20 * sy);
        path.lineTo(4.487 * sx, 17.079 * sy);
        path.lineTo(3 * sx, 17.079 * sy);
        path.lineTo(3 * sx, 14.05 * sy);
        path.lineTo(0 * sx, 14.05 * sy);
        path.lineTo(0 * sx, 10.95 * sy);
        path.lineTo(3 * sx, 10.95 * sy);
        path.lineTo(3 * sx, 5 * sy);
        path.lineTo(20.998 * sx, 5 * sy);
        path.close();

        // Left eye cutout
        final leftEye = Path()
          ..addRect(Rect.fromLTWH(6 * sx, 8.102 * sy, 1.488 * sx, 2.847 * sy));
        // Right eye cutout
        final rightEye = Path()
          ..addRect(Rect.fromLTWH(16.51 * sx, 8.102 * sy, 1.49 * sx, 2.847 * sy));

        final bodyWithoutLeftEye = Path.combine(PathOperation.difference, path, leftEye);
        final finalCharPath = Path.combine(PathOperation.difference, bodyWithoutLeftEye, rightEye);

        final fillPaint = Paint()
          ..color = color
          ..style = PaintingStyle.fill
          ..isAntiAlias = true;

        canvas.drawPath(finalCharPath, fillPaint);
        break;

      case _AgentType.antigravity:
        // Google Antigravity official curved arch wave
        final sx = w / 24.0;
        final sy = h / 24.0;

        final path = Path();
        path.moveTo(12 * sx, 0.8 * sy);
        path.cubicTo(8.2 * sx, 0.8 * sy, 5.6 * sx, 3.2 * sy, 4.1 * sx, 7.8 * sy);
        path.cubicTo(2.6 * sx, 12.5 * sy, 1.2 * sx, 18.2 * sy, 0.3 * sx, 22.4 * sy);
        path.cubicTo(-0.1 * sx, 23.6 * sy, 0.8 * sx, 24.2 * sy, 1.6 * sx, 23.6 * sy);
        path.cubicTo(4.8 * sx, 21.2 * sy, 7.8 * sx, 14.2 * sy, 12 * sx, 12.4 * sy);
        path.cubicTo(16.2 * sx, 14.2 * sy, 19.2 * sx, 21.2 * sy, 22.4 * sx, 23.6 * sy);
        path.cubicTo(23.2 * sx, 24.2 * sy, 24.1 * sx, 23.6 * sy, 23.7 * sx, 22.4 * sy);
        path.cubicTo(22.8 * sx, 18.2 * sy, 21.4 * sx, 12.5 * sy, 19.9 * sx, 7.8 * sy);
        path.cubicTo(18.4 * sx, 3.2 * sy, 15.8 * sx, 0.8 * sy, 12 * sx, 0.8 * sy);
        path.close();

        final paint = Paint()..isAntiAlias = true;
        if (useBrandGradient) {
          paint.shader = const LinearGradient(
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
            colors: [
              Color(0xFFEA4335), // Red
              Color(0xFFFBBC04), // Yellow
              Color(0xFF34A853), // Green
              Color(0xFF4285F4), // Blue
            ],
          ).createShader(Rect.fromLTWH(0, 0, w, h));
        } else {
          paint.color = color;
        }

        canvas.drawPath(path, paint);
        break;

      case _AgentType.codex:
        // OpenAI Codex Blossom 6-lobe interlocking vortex ribbon
        final r = w * 0.46;
        final strokeP = Paint()
          ..color = color
          ..style = PaintingStyle.stroke
          ..strokeWidth = size.width * 0.10
          ..strokeCap = StrokeCap.round
          ..strokeJoin = StrokeJoin.round
          ..isAntiAlias = true;

        for (int i = 0; i < 6; i++) {
          canvas.save();
          canvas.translate(cx, cy);
          canvas.rotate(i * (math.pi / 3));

          final lobe = Path();
          lobe.moveTo(r * 0.28, -r * 0.16);
          lobe.lineTo(r * 0.70, -r * 0.40);
          lobe.arcToPoint(
            Offset(r * 0.88, 0),
            radius: Radius.circular(r * 0.30),
            clockwise: true,
          );
          lobe.arcToPoint(
            Offset(r * 0.50, r * 0.36),
            radius: Radius.circular(r * 0.30),
            clockwise: true,
          );
          lobe.lineTo(0, 0);

          canvas.drawPath(lobe, strokeP);
          canvas.restore();
        }

        // Inner central hexagon core
        final hex = Path();
        final hexR = r * 0.28;
        for (int i = 0; i < 6; i++) {
          final angle = i * (math.pi / 3) - (math.pi / 6);
          final px = cx + hexR * math.cos(angle);
          final py = cy + hexR * math.sin(angle);
          if (i == 0) {
            hex.moveTo(px, py);
          } else {
            hex.lineTo(px, py);
          }
        }
        hex.close();
        canvas.drawPath(hex, strokeP);
        break;

      case _AgentType.generic:
        final strokeP = Paint()
          ..color = color
          ..style = PaintingStyle.stroke
          ..strokeWidth = size.width * 0.12
          ..strokeCap = StrokeCap.round
          ..strokeJoin = StrokeJoin.round
          ..isAntiAlias = true;

        final fillP = Paint()
          ..color = color
          ..style = PaintingStyle.fill
          ..isAntiAlias = true;

        final rect = RRect.fromRectAndRadius(
          Rect.fromCenter(center: Offset(cx, cy), width: w * 0.8, height: h * 0.8),
          Radius.circular(w * 0.2),
        );
        canvas.drawRRect(rect, strokeP);
        canvas.drawCircle(Offset(cx, cy), w * 0.15, fillP);
        break;
    }
  }

  @override
  bool shouldRepaint(covariant _AgentLogoPainter oldDelegate) {
    return oldDelegate.agentType != agentType ||
        oldDelegate.color != color ||
        oldDelegate.useBrandGradient != useBrandGradient;
  }
}
