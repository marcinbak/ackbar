import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/transcript.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_turn_bubble.dart';

Widget _wrapBubble(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: Scaffold(
        body: SingleChildScrollView(child: child),
      ),
    ),
  );
}

bool _hasBoldSpan(InlineSpan span, String text) {
  if (span is TextSpan) {
    if (span.text == text && span.style?.fontWeight == FontWeight.w700) {
      return true;
    }
    if (span.children != null) {
      for (final child in span.children!) {
        if (_hasBoldSpan(child, text)) return true;
      }
    }
  }
  return false;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  final testSession = Session(
    id: 'antigravity:local:test-sess-1',
    agent: 'antigravity',
    host: 'local',
    nativeId: 'test-sess-1',
    cwd: '/tmp',
    state: SessionState.working,
    startedAt: DateTime.utc(2026, 11, 1),
    lastEventAt: DateTime.utc(2026, 11, 1),
  );

  testWidgets(
      'Assistant TranscriptTurnBubble renders markdown bold without literal asterisks',
      (tester) async {
    const rawContent =
        'Point 3 controls **what shopping, pricing, and technical build data** in the app.';
    final msg = TranscriptMessage(
      role: 'assistant',
      content: rawContent,
      timestamp: DateTime.utc(2026, 11, 1, 12, 0, 0),
    );

    await tester.pumpWidget(
      _wrapBubble(
        TranscriptTurnBubble(
          message: msg,
          index: 0,
          session: testSession,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('**'), findsNothing);
    expect(
      find.textContaining('what shopping, pricing, and technical build data',
          findRichText: true),
      findsOneWidget,
    );

    final selectableList = tester.widgetList<SelectableText>(
      find.byType(SelectableText),
    );
    final hasBold = selectableList.any((st) {
      final span = st.textSpan;
      return span != null &&
          _hasBoldSpan(
            span,
            'what shopping, pricing, and technical build data',
          );
    });
    expect(hasBold, isTrue);
  });

  testWidgets(
      'User TranscriptTurnBubble renders markdown bold without literal asterisks',
      (tester) async {
    const rawContent =
        'Point 3 controls **what shopping, pricing, and technical build data** in the app.';
    final msg = TranscriptMessage(
      role: 'user',
      content: rawContent,
      timestamp: DateTime.utc(2026, 11, 1, 12, 0, 0),
    );

    await tester.pumpWidget(
      _wrapBubble(
        TranscriptTurnBubble(
          message: msg,
          index: 0,
          session: testSession,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('**'), findsNothing);
    expect(
      find.textContaining('what shopping, pricing, and technical build data',
          findRichText: true),
      findsOneWidget,
    );

    final selectableList = tester.widgetList<SelectableText>(
      find.byType(SelectableText),
    );
    final hasBold = selectableList.any((st) {
      final span = st.textSpan;
      return span != null &&
          _hasBoldSpan(
            span,
            'what shopping, pricing, and technical build data',
          );
    });
    expect(hasBold, isTrue);
  });
}
