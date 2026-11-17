import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/plan.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/widgets/agent_logo.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('Grok Agent Parity Tests', () {
    test('Session model displays xAI Grok branding, symbol, and colors', () {
      final sess = Session(
        id: 'grok:local:sess-1',
        nativeId: 'sess-1',
        agent: 'grok',
        cwd: '/path/to/project',
        name: 'Grok Task',
        host: 'local',
        engineType: 'headless',
        state: SessionState.idle,
        managed: true,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
      );

      expect(sess.agentDisplayName, equals('xAI Grok'));
      expect(sess.agentSymbol, equals('𝕏'));
      expect(sess.agentColor, equals(const Color(0xFFE5E7EB)));

      final sessXai = sess.copyWith(agent: 'xai');
      expect(sessXai.agentDisplayName, equals('xAI Grok'));
      expect(sessXai.agentSymbol, equals('𝕏'));
      expect(sessXai.agentColor, equals(const Color(0xFFE5E7EB)));
    });

    test('PlanDocument displays xAI Grok display name', () {
      final plan = PlanDocument(
        id: 'PLAN-001',
        title: 'Refactor Task',
        agent: 'grok',
        host: 'local',
        project: 'ackbar',
        goal: 'Complete Phase 2',
        createdAt: DateTime.now(),
      );

      expect(plan.agentDisplayName, equals('xAI Grok'));

      final planXai = plan.copyWith(agent: 'xai');
      expect(planXai.agentDisplayName, equals('xAI Grok'));
    });

    test('Session.formatModelLabel resolves Grok models correctly', () {
      expect(Session.formatModelLabel('grok-4.7', 'grok'), equals('Grok 4.7'));
      expect(Session.formatModelLabel('grok-4.6', 'grok'), equals('Grok 4.6'));
      expect(
          Session.formatModelLabel('grok-build', 'grok'), equals('Grok Build'));
    });

    testWidgets('AgentLogo renders for Grok agent without crashing',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Center(
              child: AgentLogo(agent: 'grok', size: 24),
            ),
          ),
        ),
      );

      expect(find.byType(AgentLogo), findsOneWidget);
      expect(find.byType(CustomPaint), findsWidgets);
    });
  });
}
