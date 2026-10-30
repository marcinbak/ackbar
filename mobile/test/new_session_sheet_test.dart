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

    testWidgets(
        'filters agent choices based on host discovery (omits Codex when not installed)',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/agents/discovery') {
          return http.Response(
            jsonEncode([
              {
                'agent': 'claude-code',
                'display_name': 'Claude Code',
                'installed': true
              },
              {
                'agent': 'antigravity',
                'display_name': 'Antigravity',
                'installed': true
              },
              {'agent': 'codex', 'display_name': 'Codex', 'installed': false},
            ]),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final testApi = ApiClient(client: mockClient);
      final testSse = SSEClient(client: mockClient);

      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(prefillHost: 'Legion'),
          overrides: [
            apiClientProvider.overrideWithValue(testApi),
            sseClientProvider.overrideWithValue(testSse),
          ],
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('Claude Code'), findsOneWidget);
      expect(find.text('Antigravity'), findsOneWidget);
      expect(find.text('Codex'), findsNothing);
    });

    testWidgets(
        'auto-adjusts selected agent if default selection is not installed',
        (tester) async {
      String? spawnedAgent;
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/agents/discovery') {
          return http.Response(
            jsonEncode([
              {
                'agent': 'claude-code',
                'display_name': 'Claude Code',
                'installed': false
              },
              {
                'agent': 'antigravity',
                'display_name': 'Antigravity',
                'installed': true
              },
              {'agent': 'codex', 'display_name': 'Codex', 'installed': false},
            ]),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/spawn') {
          final body = jsonDecode(request.body) as Map<String, dynamic>;
          spawnedAgent = body['agent'] as String?;
          return http.Response(
            jsonEncode({'status': 'spawned', 'session_id': 'sess-101'}),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final testApi = ApiClient(client: mockClient);
      final testSse = SSEClient(client: mockClient);

      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(
            prefillCwd: '/my/test/project',
            prefillHost: 'devbox',
          ),
          overrides: [
            apiClientProvider.overrideWithValue(testApi),
            sseClientProvider.overrideWithValue(testSse),
          ],
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('Antigravity'), findsOneWidget);
      expect(find.text('Claude Code'), findsNothing);
      expect(find.text('Codex'), findsNothing);

      // Launch session and verify it spawned antigravity
      await tester.tap(find.text('LAUNCH SESSION'));
      await tester.pumpAndSettle();

      expect(spawnedAgent, equals('antigravity'));
    });

    testWidgets(
        'shows warning banner and disables launch when no agents installed',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/agents/discovery') {
          return http.Response(
            jsonEncode([
              {
                'agent': 'claude-code',
                'display_name': 'Claude Code',
                'installed': false
              },
              {
                'agent': 'antigravity',
                'display_name': 'Antigravity',
                'installed': false
              },
              {'agent': 'codex', 'display_name': 'Codex', 'installed': false},
            ]),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final testApi = ApiClient(client: mockClient);
      final testSse = SSEClient(client: mockClient);

      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(
            prefillCwd: '/my/test/project',
            prefillHost: 'remote-vm',
          ),
          overrides: [
            apiClientProvider.overrideWithValue(testApi),
            sseClientProvider.overrideWithValue(testSse),
          ],
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('No supported agents detected on remote-vm.'),
          findsOneWidget);
      final launchBtn = tester.widget<ElevatedButton>(
          find.widgetWithText(ElevatedButton, 'LAUNCH SESSION'));
      expect(launchBtn.onPressed, isNull);
    });

    testWidgets(
        'matches prefillHost case-insensitively against hostsListProvider and spawns Antigravity headless session into TranscriptScreen',
        (tester) async {
      Map<String, dynamic>? spawnBody;
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/agents/discovery') {
          return http.Response(
            jsonEncode([
              {
                'agent': 'antigravity',
                'display_name': 'Antigravity',
                'installed': true,
              },
            ]),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/spawn') {
          spawnBody = jsonDecode(request.body) as Map<String, dynamic>;
          return http.Response(
            jsonEncode({
              'status': 'spawned',
              'session_id': 'agy-uuid-77',
              'id': 'antigravity:legion:agy-uuid-77',
              'host': 'legion',
              'engine_type': 'headless',
              'session': {
                'id': 'antigravity:legion:agy-uuid-77',
                'agent': 'antigravity',
                'host': 'legion',
                'native_id': 'agy-uuid-77',
                'cwd': '/home/dev/ackbar',
                'state': 3,
                'managed': true,
                'engine_type': 'headless',
                'name': 'Antigravity Chat',
              },
            }),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'antigravity:legion:agy-uuid-77',
              'native_id': 'agy-uuid-77',
              'agent': 'antigravity',
              'title': 'Antigravity Chat',
              'cwd': '/home/dev/ackbar',
              'messages': [],
              'markdown': '',
            }),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/subagents') {
          return http.Response(jsonEncode({'subagents': []}), 200);
        }
        return http.Response('[]', 200);
      });

      final testApi = ApiClient(client: mockClient);
      final testSse = SSEClient(client: mockClient);

      final hostsNotifier = HostsNotifier(testApi);
      hostsNotifier.addHost(
        HostRecord(
          name: 'Legion',
          url: 'http://100.125.180.4:7777',
          online: true,
          latencyMs: 5,
          version: 'v0.3.0',
          uptime: 'Active',
          sessionsCount: 1,
          createdAt: DateTime.now(),
        ),
      );
      hostsNotifier.addHost(
        HostRecord(
          name: 'MacBook',
          url: 'http://100.100.10.2:7777',
          online: true,
          latencyMs: 8,
          version: 'v0.3.0',
          uptime: 'Active',
          sessionsCount: 0,
          createdAt: DateTime.now(),
        ),
      );

      await tester.pumpWidget(
        _wrapWithScope(
          Builder(
            builder: (context) => ElevatedButton(
              onPressed: () => NewSessionSheet.show(
                context,
                prefillCwd: '/home/dev/ackbar',
                prefillHost: 'legion', // lowercase vs 'Legion' in hosts list
              ),
              child: const Text('OPEN SHEET'),
            ),
          ),
          overrides: [
            apiClientProvider.overrideWithValue(testApi),
            sseClientProvider.overrideWithValue(testSse),
            hostsListProvider.overrideWith((_) => hostsNotifier),
          ],
        ),
      );
      await tester.pumpAndSettle();

      await tester.tap(find.text('OPEN SHEET'));
      await tester.pumpAndSettle();

      // Host dropdown should normalize 'legion' to 'Legion'
      expect(find.text('Legion'), findsWidgets);

      // Select Headless engine
      await tester.tap(find.text('HEADLESS (Chat)'));
      await tester.pumpAndSettle();

      // Tap Launch Session
      await tester.tap(find.text('LAUNCH SESSION'));
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump(const Duration(milliseconds: 600));

      expect(spawnBody, isNotNull);
      expect(spawnBody!['agent'], equals('antigravity'));
      expect(spawnBody!['engine_type'], equals('headless'));
      expect(spawnBody!['host'], equals('local'));

      // Should have navigated to TranscriptScreen showing session title
      expect(find.text('Antigravity Chat'), findsOneWidget);
    });

    testWidgets('displays error banner inside NewSessionSheet when spawn fails',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/spawn') {
          return http.Response('Remote host not found', 404);
        }
        return http.Response('[]', 200);
      });

      final testApi = ApiClient(client: mockClient);
      final testSse = SSEClient(client: mockClient);

      await tester.pumpWidget(
        _wrapWithScope(
          const NewSessionSheet(
            prefillCwd: '/home/dev/ackbar',
            prefillHost: 'legion',
          ),
          overrides: [
            apiClientProvider.overrideWithValue(testApi),
            sseClientProvider.overrideWithValue(testSse),
          ],
        ),
      );
      await tester.pumpAndSettle();

      await tester.tap(find.text('LAUNCH SESSION'));
      await tester.pumpAndSettle();

      expect(
        find.text(
          'Failed to launch session on legion. Check daemon connection and directory path.',
        ),
        findsOneWidget,
      );
      // Sheet remains open so user can see error and retry
      expect(find.text('LAUNCH AGENT SESSION'), findsOneWidget);
    });
  });
}
