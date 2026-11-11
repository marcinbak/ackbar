import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/services/external_url_launcher.dart';
import 'package:ackbar_mobile/features/headless/presentation/headless_auth_card.dart';

void main() {
  Widget buildTestWidget({
    required Session session,
    required String surfacePrefix,
    ExternalUrlLauncher? launcher,
    VoidCallback? onResolved,
  }) {
    return ProviderScope(
      overrides: [
        if (launcher != null)
          externalUrlLauncherProvider.overrideWithValue(launcher),
      ],
      child: MaterialApp(
        home: Scaffold(
          body: HeadlessAuthCard(
            session: session,
            surfacePrefix: surfacePrefix,
            onResolved: onResolved,
          ),
        ),
      ),
    );
  }

  group('HeadlessAuthCard Widget Tests', () {
    testWidgets('renders all core buttons and keys', (tester) async {
      final session = Session(
        id: 's-auth-1',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n1',
        cwd: '/work',
        state: SessionState.blocked,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        blocked: Blocked(
          kind: BlockKind.auth,
          reason: 'Sign-in required for MCP GitHub',
          since: DateTime.now(),
          authServer: 'GitHub',
          authTarget: 'mcp:github',
          authUrl: 'https://github.com/login/oauth/authorize',
          flowId: 'flow-gh-1',
        ),
      );

      await tester.pumpWidget(
          buildTestWidget(session: session, surfacePrefix: 'attention'));
      await tester.pump();

      expect(find.byKey(const ValueKey('attention_headless_auth_card')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('attention_auth_sign_in_btn')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('attention_auth_copy_link_btn')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('attention_auth_retry_btn')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('attention_auth_terminal_btn')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('attention_auth_dismiss_btn')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('attention_auth_remote_toggle')),
          findsOneWidget);
      expect(find.textContaining('Authentication Required: GitHub'),
          findsOneWidget);
    });

    testWidgets('toggling remote paste reveals input field and submit button',
        (tester) async {
      final session = Session(
        id: 's-auth-2',
        agent: 'antigravity',
        host: 'local',
        nativeId: 'n2',
        cwd: '/work',
        state: SessionState.blocked,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        blocked: Blocked(
          kind: BlockKind.auth,
          reason: 'Sign-in required',
          since: DateTime.now(),
          authTarget: 'agent:agy',
        ),
      );

      await tester.pumpWidget(
          buildTestWidget(session: session, surfacePrefix: 'transcript'));
      await tester.pump();

      expect(find.byKey(const ValueKey('transcript_auth_callback_input')),
          findsNothing);

      await tester
          .tap(find.byKey(const ValueKey('transcript_auth_remote_toggle')));
      await tester.pumpAndSettle();

      expect(find.byKey(const ValueKey('transcript_auth_callback_input')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('transcript_auth_callback_submit')),
          findsOneWidget);
    });

    testWidgets('tapping copy link copies safe url to clipboard',
        (tester) async {
      String? copiedText;
      final launcher = ExternalUrlLauncher(
        nativeOpener: (u) async => true,
        clipboardWriter: (t) async => copiedText = t,
      );

      final session = Session(
        id: 's-auth-3',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n3',
        cwd: '/work',
        state: SessionState.blocked,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        blocked: Blocked(
          kind: BlockKind.auth,
          reason: 'Sign-in',
          since: DateTime.now(),
          authUrl: 'https://auth.example.com/oauth',
          flowId: 'f1',
        ),
      );

      await tester.pumpWidget(buildTestWidget(
        session: session,
        surfacePrefix: 'attention',
        launcher: launcher,
      ));
      await tester.pump();

      await tester
          .tap(find.byKey(const ValueKey('attention_auth_copy_link_btn')));
      await tester.pumpAndSettle();

      expect(copiedText, 'https://auth.example.com/oauth');
      expect(find.text('Sign-in link copied to clipboard'), findsOneWidget);
    });

    testWidgets('tapping dismiss sets optimistic state to idle',
        (tester) async {
      final container = ProviderContainer();
      final session = Session(
        id: 's-auth-4',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n4',
        cwd: '/work',
        state: SessionState.blocked,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        blocked: Blocked(
          kind: BlockKind.auth,
          reason: 'Sign-in',
          since: DateTime.now(),
        ),
      );
      container.read(fleetSessionsProvider.notifier).upsertSession(session);

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            home: Scaffold(
              body: HeadlessAuthCard(
                  session: session, surfacePrefix: 'attention'),
            ),
          ),
        ),
      );
      await tester.pump();

      await tester
          .tap(find.byKey(const ValueKey('attention_auth_dismiss_btn')));
      await tester.pump();

      final updated = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == session.id);
      expect(updated.state, SessionState.idle);
      expect(updated.blocked, isNull);
    });
  });
}
