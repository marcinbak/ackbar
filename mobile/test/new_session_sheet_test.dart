import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/fleet/presentation/new_session_sheet.dart';

Widget _wrapWithScope(Widget child, {List<Override> overrides = const []}) {
  return ProviderScope(
    overrides: overrides,
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: Scaffold(
        body: child,
      ),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('NewSessionSheet Widget Tests', () {
    testWidgets('renders all core fields, agent choices, and launch button',
        (tester) async {
      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(),
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('LAUNCH AGENT SESSION'), findsOneWidget);
      expect(find.text('Claude Code'), findsOneWidget);
      expect(find.text('Antigravity'), findsOneWidget);
      expect(find.text('Codex'), findsOneWidget);
      expect(find.text('WORKING DIRECTORY (REQUIRED)'), findsOneWidget);
      expect(find.text('TMUX (Live PTY)'), findsOneWidget);
      expect(find.text('HEADLESS (Chat)'), findsOneWidget);
      expect(find.text('LAUNCH SESSION'), findsOneWidget);
    });

    testWidgets('validates empty working directory on submission',
        (tester) async {
      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(),
        ),
      );
      await tester.pumpAndSettle();

      // Tap Launch without entering cwd
      final launchBtn = find.text('LAUNCH SESSION');
      expect(launchBtn, findsOneWidget);
      await tester.tap(launchBtn);
      await tester.pumpAndSettle();

      expect(find.text('Working directory is required'), findsOneWidget);
    });

    testWidgets('honors prefilled cwd and folder parameters', (tester) async {
      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(
            prefillCwd: '~/Work/Ackbar/mobile',
            prefillFolder: 'Ackbar Mobile',
            prefillHost: 'gpu-box',
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('~/Work/Ackbar/mobile'), findsOneWidget);

      // Open optional configuration accordion
      await tester
          .tap(find.text('OPTIONAL CONFIGURATION (Title, Prompt, Group)'));
      await tester.pumpAndSettle();

      expect(find.text('Ackbar Mobile'), findsOneWidget);
    });

    testWidgets('allows toggling execution engine between tmux and headless',
        (tester) async {
      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(),
        ),
      );
      await tester.pumpAndSettle();

      final headlessToggle = find.text('HEADLESS (Chat)');
      expect(headlessToggle, findsOneWidget);
      await tester.tap(headlessToggle);
      await tester.pumpAndSettle();

      // Tapping tmux toggle switches back
      final tmuxToggle = find.text('TMUX (Live PTY)');
      await tester.tap(tmuxToggle);
      await tester.pumpAndSettle();
    });

    testWidgets('successful launch calls spawnSession and dismisses',
        (tester) async {
      bool spawnCalled = false;

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/spawn') {
          spawnCalled = true;
          return http.Response(
            jsonEncode({
              'status': 'spawned',
              'session_id': 'sess-100',
              'id': 'claude-code:local:sess-100',
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final testApi = ApiClient(client: mockClient);
      final testSse = SSEClient(client: mockClient);

      await tester.pumpWidget(
        _wrapWithScope(
          Builder(
            builder: (context) => ElevatedButton(
              onPressed: () => NewSessionSheet.show(
                context,
                prefillCwd: '/my/test/project',
              ),
              child: const Text('OPEN'),
            ),
          ),
          overrides: [
            apiClientProvider.overrideWithValue(testApi),
            sseClientProvider.overrideWithValue(testSse),
          ],
        ),
      );
      await tester.pumpAndSettle();

      // Open sheet
      await tester.tap(find.text('OPEN'));
      await tester.pumpAndSettle();

      expect(find.text('LAUNCH AGENT SESSION'), findsOneWidget);
      expect(find.text('/my/test/project'), findsOneWidget);

      // Tap Launch Session
      await tester.tap(find.text('LAUNCH SESSION'));
      await tester.pumpAndSettle();

      expect(spawnCalled, isTrue);
      // Sheet should now be popped
      expect(find.text('LAUNCH AGENT SESSION'), findsNothing);
    });
  });
}
