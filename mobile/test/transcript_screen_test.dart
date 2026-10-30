import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_screen.dart';

Session _makeHeadlessSession({
  String id = 'claude-code:local:sess-headless-1',
  String nativeId = 'sess-headless-1',
  SessionState state = SessionState.idle,
}) {
  final now = DateTime.utc(2026, 3, 28, 12, 0, 0);
  return Session(
    id: id,
    agent: 'claude-code',
    host: 'local',
    nativeId: nativeId,
    cwd: '/workspace/ackbar',
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    engineType: 'headless',
    name: 'Headless Test Session',
  );
}

Widget _wrapTranscriptScreen(
  Session session, {
  required ApiClient api,
  required SSEClient sse,
}) {
  return ProviderScope(
    overrides: [
      apiClientProvider.overrideWithValue(api),
      sseClientProvider.overrideWithValue(sse),
    ],
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: TranscriptScreen(session: session),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('TranscriptScreen Headless Turn & Auto-Refresh Tests', () {
    testWidgets(
        'keeps Working indicator and Stop button visible after POST /v1/sessions/prompt returns 200 OK, and allows queuing follow-up prompts',
        (tester) async {
      var promptSentCount = 0;
      var cancelCalled = false;

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-headless-1',
              'native_id': 'sess-headless-1',
              'agent': 'claude-code',
              'title': 'Headless Test Session',
              'cwd': '/workspace/ackbar',
              'messages': [
                {
                  'role': 'user',
                  'content': 'Initial prompt',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                {
                  'role': 'assistant',
                  'content': 'Initial reply',
                  'timestamp': '2026-03-28T12:00:02Z',
                },
              ],
              'markdown': '',
            }),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/subagents') {
          return http.Response(jsonEncode({'subagents': []}), 200);
        }
        if (request.url.path == '/v1/sessions/prompt') {
          promptSentCount++;
          return http.Response(jsonEncode({'status': 'running'}), 200);
        }
        if (request.url.path == '/v1/sessions/cancel') {
          cancelCalled = true;
          return http.Response(jsonEncode({'status': 'cancelled'}), 200);
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = SSEClient(client: mockClient);
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(
        _wrapTranscriptScreen(session, api: api, sse: sse),
      );
      // Wait for _resolveHostUrl retries + initial transcript load
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Initial reply'), findsOneWidget);
      expect(find.text('Working...'), findsNothing);
      expect(find.byIcon(Icons.arrow_upward_rounded), findsOneWidget);
      expect(find.byIcon(Icons.stop_circle_rounded), findsNothing);

      // Type and send a prompt
      await tester.enterText(find.byType(TextField), 'Analyze the codebase');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      // Even after sendPrompt HTTP POST has completed, Working... and Stop button stay visible!
      expect(promptSentCount, equals(1));
      expect(find.text('Analyze the codebase'), findsOneWidget);
      expect(find.text('Working...'), findsOneWidget);
      expect(find.byIcon(Icons.stop_circle_rounded), findsOneWidget);
      expect(find.byIcon(Icons.arrow_upward_rounded), findsNothing);

      // Typing a follow-up while working shows BOTH Stop and Send buttons
      await tester.enterText(find.byType(TextField), 'Also check tests');
      await tester.pump();
      expect(find.byIcon(Icons.stop_circle_rounded), findsOneWidget);
      expect(find.byIcon(Icons.arrow_upward_rounded), findsOneWidget);

      // Clear text and tap Stop to cancel the turn
      await tester.enterText(find.byType(TextField), '');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.stop_circle_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(cancelCalled, isTrue);
      expect(find.text('Working...'), findsNothing);
      expect(find.byIcon(Icons.arrow_upward_rounded), findsOneWidget);
    });

    testWidgets(
        'periodic polling silently refreshes transcript while working and clears working state when reply arrives',
        (tester) async {
      var includeSecondReply = false;

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          final messages = <Map<String, dynamic>>[
            {
              'role': 'user',
              'content': 'First question',
              'timestamp': '2026-03-28T12:00:00Z',
            },
            if (includeSecondReply)
              {
                'role': 'assistant',
                'content': 'Automated streamed answer from headless turn',
                'timestamp': '2026-03-28T12:00:05Z',
              },
          ];
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-headless-1',
              'native_id': 'sess-headless-1',
              'agent': 'claude-code',
              'title': 'Headless Test Session',
              'cwd': '/workspace/ackbar',
              'messages': messages,
              'markdown': '',
            }),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/subagents') {
          return http.Response(jsonEncode({'subagents': []}), 200);
        }
        if (request.url.path == '/v1/sessions/prompt') {
          return http.Response(jsonEncode({'status': 'running'}), 200);
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = SSEClient(client: mockClient);
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(
        _wrapTranscriptScreen(session, api: api, sse: sse),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      // Send a prompt to enter awaitingTurnCompletion state
      await tester.enterText(find.byType(TextField), 'Second question');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Working...'), findsOneWidget);
      expect(
        find.text('Automated streamed answer from headless turn'),
        findsNothing,
      );

      // Now backend finishes writing the reply; advance 3s periodic timer
      includeSecondReply = true;
      await tester.pump(const Duration(seconds: 3));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(
        find.text('Automated streamed answer from headless turn'),
        findsOneWidget,
      );
      expect(find.text('Working...'), findsNothing);
    });

    testWidgets(
        'reacts to live fleetSessionsProvider working -> idle transition and refreshes transcript immediately',
        (tester) async {
      var sessionFinished = false;

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-headless-1',
              'native_id': 'sess-headless-1',
              'agent': 'claude-code',
              'title': 'Headless Test Session',
              'cwd': '/workspace/ackbar',
              'messages': [
                {
                  'role': 'user',
                  'content': 'Run task',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                if (sessionFinished)
                  {
                    'role': 'assistant',
                    'content': 'Task completed on transition to idle',
                    'timestamp': '2026-03-28T12:00:10Z',
                  },
              ],
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

      final api = ApiClient(client: mockClient);
      final sse = SSEClient(client: mockClient);
      final initialSession = _makeHeadlessSession(state: SessionState.working);

      late WidgetRef capturedRef;
      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            apiClientProvider.overrideWithValue(api),
            sseClientProvider.overrideWithValue(sse),
          ],
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: Consumer(
              builder: (context, ref, _) {
                capturedRef = ref;
                return TranscriptScreen(session: initialSession);
              },
            ),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      // Initially working
      expect(find.text('Working...'), findsOneWidget);
      expect(find.byIcon(Icons.stop_circle_rounded), findsOneWidget);

      // Simulate SSE updating fleetSessionsProvider from working -> idle
      sessionFinished = true;
      capturedRef.read(fleetSessionsProvider.notifier).setSessions([
        initialSession.copyWith(state: SessionState.idle),
      ]);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Working...'), findsNothing);
      expect(
        find.text('Task completed on transition to idle'),
        findsOneWidget,
      );
    });

    testWidgets(
        'refreshes transcript silently when app resumes from background (AppLifecycleState.resumed)',
        (tester) async {
      var backgroundReplyReady = false;

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-headless-1',
              'native_id': 'sess-headless-1',
              'agent': 'claude-code',
              'title': 'Headless Test Session',
              'cwd': '/workspace/ackbar',
              'messages': [
                {
                  'role': 'user',
                  'content': 'Before lock screen',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                if (backgroundReplyReady)
                  {
                    'role': 'assistant',
                    'content': 'Reply generated while phone was locked',
                    'timestamp': '2026-03-28T12:01:00Z',
                  },
              ],
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

      final api = ApiClient(client: mockClient);
      final sse = SSEClient(client: mockClient);
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(
        _wrapTranscriptScreen(session, api: api, sse: sse),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Before lock screen'), findsOneWidget);
      expect(
        find.text('Reply generated while phone was locked'),
        findsNothing,
      );

      // Simulate reply arriving while app is inactive/backgrounded, then resuming app
      backgroundReplyReady = true;
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      await tester.pump();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(
        find.text('Reply generated while phone was locked'),
        findsOneWidget,
      );
    });

    testWidgets(
        'preserves optimistic user bubble on silent poll when repeated prompt matches earlier historical message',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-headless-1',
              'native_id': 'sess-headless-1',
              'agent': 'claude-code',
              'title': 'Headless Test Session',
              'cwd': '/workspace/ackbar',
              'messages': [
                {
                  'role': 'user',
                  'content': 'continue',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                {
                  'role': 'assistant',
                  'content': 'Step 1 finished.',
                  'timestamp': '2026-03-28T12:00:05Z',
                },
              ],
              'markdown': '',
            }),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/subagents') {
          return http.Response(jsonEncode({'subagents': []}), 200);
        }
        if (request.url.path == '/v1/sessions/prompt') {
          return http.Response(jsonEncode({'status': 'running'}), 200);
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = SSEClient(client: mockClient);
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(
        _wrapTranscriptScreen(session, api: api, sse: sse),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('continue'), findsOneWidget);
      expect(find.text('Step 1 finished.'), findsOneWidget);

      // Send "continue" again (matching the earlier user message)
      await tester.enterText(find.byType(TextField), 'continue');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      // Both the historical and optimistic "continue" bubbles should be visible
      expect(find.text('continue'), findsNWidgets(2));

      // Advance 3s periodic poll before backend writes the new turn to disk
      await tester.pump(const Duration(seconds: 3));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      // Optimistic second "continue" bubble must still be preserved
      expect(find.text('continue'), findsNWidgets(2));
      expect(find.text('Working...'), findsOneWidget);
    });

    testWidgets(
        'stays bound to adopted Antigravity session via oldId and sends subsequent prompts with adopted session ID',
        (tester) async {
      String? lastPromptSessionId;
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': request.url.queryParameters['id'] ?? '',
              'native_id': 'real-conv-999',
              'agent': 'antigravity',
              'title': 'Adopted Antigravity Session',
              'cwd': '/workspace/ackbar',
              'messages': [],
              'markdown': '',
            }),
            200,
          );
        }
        if (request.url.path == '/v1/sessions/subagents') {
          return http.Response(jsonEncode({'subagents': []}), 200);
        }
        if (request.url.path == '/v1/sessions/prompt') {
          final body = jsonDecode(request.body) as Map<String, dynamic>;
          lastPromptSessionId = body['session_id'] as String?;
          return http.Response(jsonEncode({'status': 'running'}), 200);
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = SSEClient(client: mockClient);
      final initialSession = Session(
        id: 'antigravity:legion:temp-uuid-111',
        agent: 'antigravity',
        host: 'legion',
        nativeId: 'temp-uuid-111',
        cwd: '/workspace/ackbar',
        state: SessionState.idle,
        startedAt: DateTime.utc(2026, 3, 28, 12, 0, 0),
        lastEventAt: DateTime.utc(2026, 3, 28, 12, 0, 0),
        managed: true,
        engineType: 'headless',
        name: 'Spawning Antigravity',
      );

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(api),
          sseClientProvider.overrideWithValue(sse),
        ],
      );
      addTearDown(container.dispose);

      // Simulate adoption in fleetSessionsProvider where id and nativeId changed, and oldId matches initialSession.id
      final adoptedSession = initialSession.copyWith(
        id: 'antigravity:legion:real-conv-999',
        nativeId: 'real-conv-999',
        oldId: 'antigravity:legion:temp-uuid-111',
        customTitle: 'Adopted Antigravity Session',
      );
      container
          .read(fleetSessionsProvider.notifier)
          .setSessions([adoptedSession]);

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: TranscriptScreen(session: initialSession),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Adopted Antigravity Session'), findsOneWidget);

      await tester.enterText(
          find.byType(TextField), 'Follow up after adoption');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(
        lastPromptSessionId,
        equals('antigravity:legion:real-conv-999'),
      );
    });
  });
}
