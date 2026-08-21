import 'dart:math' as math;
import 'package:flutter/material.dart';
import 'package:mobile/core/theme/app_colors.dart';

/// Renders a crisp vector icon for Claude Code, OpenAI Codex, or Google Antigravity.
class AgentLogo extends StatelessWidget {
  final String agent;
  final double size;
  final Color? color;

  const AgentLogo({
    super.key,
    required this.agent,
    this.size = 14,
    this.color,
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
      ),
    );
  }

  static Color _defaultColor(String agent) {
    if (agent.contains('claude')) {
      return const Color(0xFFD97706); // Warm Amber / Terracotta
    } else if (agent.contains('antigravity') || agent.contains('gemini') || agent.contains('agy')) {
      return const Color(0xFF60A5FA); // Sky Blue / Gemini Spark
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

  _AgentLogoPainter({
    required this.agentType,
    required this.color,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.fill
      ..isAntiAlias = true;

    final strokePaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = size.width * 0.12
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round
      ..isAntiAlias = true;

    final w = size.width;
    final h = size.height;
    final cx = w / 2;
    final cy = h / 2;

    switch (agentType) {
      case _AgentType.antigravity:
        // Google Gemini / Antigravity 4-point curved astroid sparkle
        final path = Path();
        path.moveTo(cx, 0);
        path.quadraticBezierTo(cx, cy, 0, cy);
        path.quadraticBezierTo(cx, cy, cx, h);
        path.quadraticBezierTo(cx, cy, w, cy);
        path.quadraticBezierTo(cx, cy, cx, 0);
        path.close();
        canvas.drawPath(path, paint);
        break;

      case _AgentType.claude:
        // Anthropic Claude Asterism / Starburst icon
        final numArms = 8;
        final outerR = w * 0.48;
        final innerR = w * 0.18;
        final armThickness = w * 0.11;

        for (int i = 0; i < numArms; i++) {
          final angle = (i * math.pi / 4);
          final armPaint = Paint()
            ..color = color
            ..style = PaintingStyle.stroke
            ..strokeWidth = armThickness
            ..strokeCap = StrokeCap.round
            ..isAntiAlias = true;

          final x1 = cx + math.cos(angle) * innerR;
          final y1 = cy + math.sin(angle) * innerR;
          final x2 = cx + math.cos(angle) * outerR;
          final y2 = cy + math.sin(angle) * outerR;
          canvas.drawLine(Offset(x1, y1), Offset(x2, y2), armPaint);
        }

        // Center hub
        canvas.drawCircle(Offset(cx, cy), innerR * 1.1, paint);
        break;

      case _AgentType.codex:
        // OpenAI / Codex looped hexagon vortex geometry
        final r = w * 0.42;
        final path = Path();
        for (int i = 0; i < 6; i++) {
          final a1 = i * (math.pi / 3) - (math.pi / 6);
          final x1 = cx + r * math.cos(a1);
          final y1 = cy + r * math.sin(a1);

          final a2 = (i + 1) * (math.pi / 3) - (math.pi / 6);
          final x2 = cx + r * math.cos(a2);
          final y2 = cy + r * math.sin(a2);

          canvas.drawLine(
            Offset(x1, y1),
            Offset(x2, y2),
            strokePaint..strokeWidth = w * 0.14,
          );

          // Inner vortex ray
          final innerA = a1 + (math.pi / 3);
          final ix = cx + (r * 0.45) * math.cos(innerA);
          final iy = cy + (r * 0.45) * math.sin(innerA);
          canvas.drawLine(
            Offset(x1, y1),
            Offset(ix, iy),
            strokePaint..strokeWidth = w * 0.11,
          );
        }
        break;

      case _AgentType.generic:
        // Clean terminal / microchip fallback
        final rect = RRect.fromRectAndRadius(
          Rect.fromCenter(center: Offset(cx, cy), width: w * 0.8, height: h * 0.8),
          Radius.circular(w * 0.2),
        );
        canvas.drawRRect(rect, strokePaint..strokeWidth = w * 0.12);
        canvas.drawCircle(Offset(cx, cy), w * 0.15, paint);
        break;
    }
  }

  @override
  bool shouldRepaint(covariant _AgentLogoPainter oldDelegate) {
    return oldDelegate.agentType != agentType || oldDelegate.color != color;
  }
}
