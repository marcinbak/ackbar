import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/services/audio_companion_service.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/terminal/presentation/terminal_screen.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_screen.dart';

Session _makeSession({
  String id = 'claude-code:local:sess-ro-1',
  String engineType = 'headless',
  SessionState state = SessionState.idle,
  String tmuxName = '',
}) {
  final now = DateTime.utc(2026, 3, 28, 12, 0, 0);
  return Session(
    id: id,
    agent: 'claude-code',
    host: 'local',
    nativeId: 'sess-ro-1',
    cwd: '/workspace/ackbar',
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    engineType: engineType,
    tmuxName: tmuxName,
    name: 'Read-Only Test Session',
  );
}

class FakeHostsNotifier extends StateNotifier<List<HostRecord>>
    implements HostsNotifier {
  FakeHostsNotifier([List<HostRecord> initial = const []]) : super(initial);
  @override
  void setHosts(List<HostRecord> hosts) => state = hosts;
  @override
  void addHost(HostRecord host) => state = [...state, host];
  @override
  void updateHost(HostRecord updatedHost) => state =
      state.map((h) => h.name == updatedHost.name ? updatedHost : h).toList();
  @override
  void removeHost(String name) =>
      state = state.where((h) => h.name != name).toList();
  @override
  Future<void> refreshHosts() async {}
}

Widget _wrapTranscriptScreen(
  Session session, {
  required ApiClient api,
  bool showOpenSessionAction = true,
}) {
  return ProviderScope(
    overrides: [
      apiClientProvider.overrideWithValue(api),
      hostsListProvider.overrideWith((_) => FakeHostsNotifier([
            HostRecord(
              name: 'local',
              url: 'http://127.0.0.1:7777',
              online: true,
              createdAt: DateTime.utc(2026, 1, 1),
            ),
          ])),
    ],
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: TranscriptScreen(
        session: session,
        showOpenSessionAction: showOpenSessionAction,
      ),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('TranscriptScreen Read-Only Tests', () {
    testWidgets(
        'renders turns, tool calls, and thinking accordions in read-only mode',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-ro-1',
              'agent': 'claude-code',
              'messages': [
                {
                  'role': 'user',
                  'content': 'Check the logs',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                {
                  'role': 'assistant',
                  'content': 'Here are the findings from the log file.',
                  'thinking': 'Analyzing error trace in stdout...',
                  'tool_calls': [
                    {
                      'name': 'run_command',
                      'input': {'command': 'cat /tmp/server.log'},
                      'output': '2026-03-28 ERROR: port 7777 busy',
                    }
                  ],
                  'timestamp': '2026-03-28T12:00:05Z',
                },
              ],
              'markdown':
                  '### USER\nCheck the logs\n\n### ASSISTANT\nHere are the findings',
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final session = _makeSession();

      await tester.pumpWidget(_wrapTranscriptScreen(session, api: api));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Check the logs'), findsOneWidget);
      expect(find.text('Here are the findings from the log file.'),
          findsOneWidget);
      expect(find.text('💭 Thought Process'), findsOneWidget);
      expect(find.textContaining('run_command'), findsWidgets);
      expect(find.byType(TextField), findsNothing);
    });

    testWidgets(
        'search toggle filters turns by content, thinking, or tool calls and counts matches',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-ro-1',
              'agent': 'claude-code',
              'messages': [
                {
                  'role': 'user',
                  'content': 'Deploy to staging',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                {
                  'role': 'assistant',
                  'content': 'Deployment completed successfully.',
                  'thinking': 'Verifying docker swarm status...',
                  'timestamp': '2026-03-28T12:00:05Z',
                },
                {
                  'role': 'user',
                  'content': 'Run database migrations',
                  'timestamp': '2026-03-28T12:01:00Z',
                },
              ],
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final session = _makeSession();

      await tester.pumpWidget(_wrapTranscriptScreen(session, api: api));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Deploy to staging'), findsOneWidget);
      expect(find.text('Run database migrations'), findsOneWidget);

      final searchToggle = find.byKey(const Key('transcript_search_toggle'));
      expect(searchToggle, findsOneWidget);
      await tester.tap(searchToggle);
      await tester.pumpAndSettle();

      final searchInput = find.byKey(const Key('transcript_search_input'));
      expect(searchInput, findsOneWidget);

      await tester.enterText(searchInput, 'migrations');
      await tester.pump();

      expect(find.text('Run database migrations'), findsOneWidget);
      expect(find.text('Deploy to staging'), findsNothing);
      expect(find.byKey(const Key('transcript_search_count')), findsOneWidget);
      expect(find.text('1 match'), findsOneWidget);

      await tester.enterText(searchInput, 'swarm');
      await tester.pump();
      expect(find.text('Deployment completed successfully.'), findsOneWidget);
      expect(find.text('Run database migrations'), findsNothing);

      await tester.tap(find.byKey(const Key('transcript_search_clear')));
      await tester.pump();

      expect(find.text('Deploy to staging'), findsOneWidget);
      expect(find.text('Run database migrations'), findsOneWidget);
    });

    testWidgets('copy transcript button copies full markdown to clipboard',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-ro-1',
              'agent': 'claude-code',
              'messages': [
                {
                  'role': 'user',
                  'content': 'Hello',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
              ],
              'markdown': '### USER\nHello',
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final session = _makeSession();

      await tester.pumpWidget(_wrapTranscriptScreen(session, api: api));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(
        SystemChannels.platform,
        (message) async => null,
      );

      final copyBtn = find.byKey(const Key('transcript_copy_button'));
      expect(copyBtn, findsOneWidget);
      await tester.tap(copyBtn);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(find.text('Transcript copied to clipboard'), findsOneWidget);
    });

    testWidgets('open session action routes to ChatScreen for headless session',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-ro-1',
              'agent': 'claude-code',
              'messages': [],
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final headless = _makeSession(engineType: 'headless');

      await tester.pumpWidget(_wrapTranscriptScreen(headless, api: api));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      final openBtn = find.byKey(const Key('transcript_open_session_button'));
      expect(openBtn, findsOneWidget);
      await tester.tap(openBtn);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.byType(ChatScreen), findsOneWidget);
    });

    testWidgets('open session action routes to TerminalScreen for tmux session',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-ro-2',
              'agent': 'claude-code',
              'messages': [],
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final tmux = _makeSession(
          id: 'claude-code:local:sess-ro-2',
          engineType: 'tmux',
          tmuxName: 'ackbar-tmux-1',
          state: SessionState.working);

      await tester.pumpWidget(_wrapTranscriptScreen(tmux, api: api));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      final openTmuxBtn =
          find.byKey(const Key('transcript_open_session_button'));
      expect(openTmuxBtn, findsOneWidget);
      await tester.tap(openTmuxBtn);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.byType(TerminalScreen), findsOneWidget);
    });

    testWidgets(
        'open session button is hidden when showOpenSessionAction is false',
        (tester) async {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-ro-1',
              'agent': 'claude-code',
              'messages': [],
            }),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final session = _makeSession();

      await tester.pumpWidget(_wrapTranscriptScreen(session,
          api: api, showOpenSessionAction: false));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.byKey(const Key('transcript_open_session_button')),
          findsNothing);
    });

    testWidgets('shows retry on load error and auto-read toggle updates state',
        (tester) async {
      var shouldFail = true;

      final mockClient = MockClient((request) async {
        if (shouldFail) {
          return http.Response('Internal Error', 500);
        }
        return http.Response(
          jsonEncode({
            'session_id': 'claude-code:local:sess-ro-1',
            'agent': 'claude-code',
            'messages': [
              {
                'role': 'user',
                'content': 'Recovered',
                'timestamp': '2026-03-28T12:00:00Z',
              }
            ],
          }),
          200,
        );
      });

      final api = ApiClient(client: mockClient);
      final session = _makeSession();

      await tester.pumpWidget(_wrapTranscriptScreen(session, api: api));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Retry'), findsOneWidget);

      shouldFail = false;
      await tester.tap(find.text('Retry'));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Recovered'), findsOneWidget);

      final ttsToggle = find.byKey(const Key('transcript_tts_toggle'));
      expect(ttsToggle, findsOneWidget);
      await tester.tap(ttsToggle);
      await tester.pump();
    });
  });
}
