import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/terminal/presentation/terminal_screen.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_screen.dart';

HostRecord dynamicHostRecord() => HostRecord(
      name: 'Legion',
      url: 'http://100.64.0.1:7777',
      authToken: 'secret-host-token',
      online: false,
      createdAt: DateTime.utc(2026, 10, 31),
    );

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

class MockSSEClient extends SSEClient {
  @override
  Stream<Session> subscribeHost(
    HostRecord host, {
    void Function(HostRecord host)? onHostReconnected,
  }) =>
      const Stream.empty();

  @override
  Stream<Session> subscribeMultipleHosts(
    List<HostRecord> hosts, {
    bool includeOffline = false,
    void Function(HostRecord host)? onHostReconnected,
  }) =>
      const Stream.empty();
}

Widget _wrapChatScreen(
  Session session, {
  required ApiClient api,
  required SSEClient sse,
}) {
  return ProviderScope(
    overrides: [
      apiClientProvider.overrideWithValue(api),
      sseClientProvider.overrideWithValue(sse),
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
      home: ChatScreen(session: session),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('ChatScreen Interactive Headless Tests', () {
    testWidgets(
        'keeps Working indicator and Stop button visible after prompt returns 200 OK and handles cancellation',
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
      final sse = MockSSEClient();
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(_wrapChatScreen(session, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Initial reply'), findsOneWidget);
      expect(find.text('Working...'), findsNothing);
      expect(find.byIcon(Icons.arrow_upward_rounded), findsOneWidget);

      await tester.enterText(find.byType(TextField), 'Analyze the codebase');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(promptSentCount, equals(1));
      expect(find.text('Analyze the codebase'), findsOneWidget);
      expect(find.text('Working...'), findsOneWidget);
      expect(find.byIcon(Icons.stop_circle_rounded), findsOneWidget);

      await tester.tap(find.byIcon(Icons.stop_circle_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(cancelCalled, isTrue);
      expect(find.text('Working...'), findsNothing);
    });

    testWidgets(
        'polling silently refreshes transcript while working and clears working state on reply',
        (tester) async {
      var includeSecondReply = false;

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
                  'content': 'First question',
                  'timestamp': '2026-03-28T12:00:00Z',
                },
                if (includeSecondReply)
                  {
                    'role': 'assistant',
                    'content': 'Streamed answer',
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
      final sse = MockSSEClient();
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(_wrapChatScreen(session, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      await tester.enterText(find.byType(TextField), 'Second question');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Working...'), findsOneWidget);

      includeSecondReply = true;
      await tester.pump(const Duration(seconds: 3));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Streamed answer'), findsOneWidget);
      expect(find.text('Working...'), findsNothing);
    });

    testWidgets(
        'take the wheel button calls takeWheel and replaces with TerminalScreen',
        (tester) async {
      var takeWheelCalled = false;

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'claude-code:local:sess-headless-1',
              'native_id': 'sess-headless-1',
              'agent': 'claude-code',
              'title': 'Headless Test Session',
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
        if (request.url.path == '/v1/sessions/take-wheel') {
          takeWheelCalled = true;
          return http.Response(
            jsonEncode({'status': 'ok', 'tmux_name': 'ackbar-sess-headless-1'}),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = MockSSEClient();
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(_wrapChatScreen(session, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      final takeWheelBtn = find.byKey(const Key('chat_take_wheel_button'));
      expect(takeWheelBtn, findsOneWidget);

      await tester.tap(takeWheelBtn);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(takeWheelCalled, isTrue);
      expect(find.byType(TerminalScreen), findsOneWidget);
    });

    testWidgets(
        'view transcript button opens read-only TranscriptScreen without open session button',
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
                  'content': 'Inspect transcript',
                  'timestamp': '2026-03-28T12:00:00Z',
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
      final sse = MockSSEClient();
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(_wrapChatScreen(session, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      final viewTranscriptBtn =
          find.byKey(const Key('chat_open_transcript_button'));
      expect(viewTranscriptBtn, findsOneWidget);

      await tester.tap(viewTranscriptBtn);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.byType(TranscriptScreen), findsOneWidget);
      expect(find.byKey(const Key('transcript_open_session_button')),
          findsNothing);
    });
  });
}
