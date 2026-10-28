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
  });
}
