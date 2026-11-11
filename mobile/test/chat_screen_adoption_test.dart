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

Session _makeHeadlessSession({
  String id = 'claude-code:local:sess-headless-1',
  String nativeId = 'sess-headless-1',
  SessionState state = SessionState.idle,
  String agent = 'claude-code',
  String host = 'local',
  String oldId = '',
  String name = 'Headless Test Session',
}) {
  final now = DateTime.utc(2026, 3, 28, 12, 0, 0);
  return Session(
    id: id,
    agent: agent,
    host: host,
    nativeId: nativeId,
    oldId: oldId,
    cwd: '/workspace/ackbar',
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    engineType: 'headless',
    name: name,
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
            HostRecord(
              name: 'legion',
              url: 'http://100.64.0.1:7777',
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

  group('ChatScreen Lifecycle & Adoption Tests', () {
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
      final sse = MockSSEClient();
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
                return ChatScreen(session: initialSession);
              },
            ),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Working...'), findsOneWidget);
      expect(find.byIcon(Icons.stop_circle_rounded), findsOneWidget);

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
      final sse = MockSSEClient();
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(_wrapChatScreen(session, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('Before lock screen'), findsOneWidget);
      expect(
        find.text('Reply generated while phone was locked'),
        findsNothing,
      );

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
      final sse = MockSSEClient();
      final session = _makeHeadlessSession(state: SessionState.idle);

      await tester.pumpWidget(_wrapChatScreen(session, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('continue'), findsOneWidget);
      expect(find.text('Step 1 finished.'), findsOneWidget);

      await tester.enterText(find.byType(TextField), 'continue');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.text('continue'), findsNWidgets(2));

      await tester.pump(const Duration(seconds: 3));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

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
        if (request.url.path == '/v1/sessions') {
          return http.Response(
            jsonEncode([
              {
                'id': 'antigravity:legion:real-conv-999',
                'native_id': 'real-conv-999',
                'old_id': 'antigravity:legion:temp-uuid-111',
                'agent': 'antigravity',
                'host': 'legion',
                'cwd': '/workspace/ackbar',
                'state': 'idle',
                'custom_title': 'Adopted Antigravity Session',
                'engine_type': 'headless',
                'managed': true,
              }
            ]),
            200,
          );
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = MockSSEClient();
      final initialSession = _makeHeadlessSession(
        id: 'antigravity:legion:temp-uuid-111',
        nativeId: 'temp-uuid-111',
        agent: 'antigravity',
        host: 'legion',
        name: 'Spawning Antigravity',
      );

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(api),
          sseClientProvider.overrideWithValue(sse),
          hostsListProvider.overrideWith((_) => FakeHostsNotifier([
                HostRecord(
                  name: 'legion',
                  url: 'http://100.64.0.1:7777',
                  online: true,
                  createdAt: DateTime.utc(2026, 1, 1),
                ),
              ])),
        ],
      );
      addTearDown(container.dispose);

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
            home: ChatScreen(session: initialSession),
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

    testWidgets(
        'mounting already-adopted Antigravity session with oldId sends zero prompt requests until user submits',
        (tester) async {
      var promptRequestsCount = 0;
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions/transcript') {
          return http.Response(
            jsonEncode({
              'session_id': 'antigravity:legion:real-conv-999',
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
          promptRequestsCount++;
          return http.Response(jsonEncode({'status': 'running'}), 200);
        }
        return http.Response('[]', 200);
      });

      final api = ApiClient(client: mockClient);
      final sse = MockSSEClient();
      final adoptedSession = _makeHeadlessSession(
        id: 'antigravity:legion:real-conv-999',
        agent: 'antigravity',
        host: 'legion',
        nativeId: 'real-conv-999',
        oldId: 'antigravity:legion:temp-uuid-111',
        name: 'Adopted Antigravity',
      );

      await tester
          .pumpWidget(_wrapChatScreen(adoptedSession, api: api, sse: sse));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(promptRequestsCount, equals(0));

      await tester.enterText(find.byType(TextField), 'User prompt after mount');
      await tester.pump();
      await tester.tap(find.byIcon(Icons.arrow_upward_rounded));
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(promptRequestsCount, equals(1));
    });
  });
}
