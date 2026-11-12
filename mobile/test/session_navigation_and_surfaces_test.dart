import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/navigation/session_navigator.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/attention/presentation/attention_screen.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/fleet/presentation/session_detail_sheet.dart';
import 'package:ackbar_mobile/features/terminal/presentation/terminal_screen.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_screen.dart';

Session _makeSession({
  required String id,
  required String engineType,
  SessionState state = SessionState.idle,
  bool isUnread = false,
  String tmuxName = '',
  String? blockerKind,
}) {
  final now = DateTime.utc(2026, 3, 28, 12, 0, 0);
  return Session(
    id: id,
    agent: 'claude-code',
    host: 'local',
    nativeId: id,
    cwd: '/workspace/ackbar',
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    engineType: engineType,
    tmuxName: tmuxName,
    isUnread: isUnread,
    name: 'Session $id',
    blocked: blockerKind != null
        ? Blocked(
            kind: BlockKind.permission,
            reason: 'Run rm -rf',
            since: now,
          )
        : null,
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

final _testOverrides = [
  sseClientProvider.overrideWithValue(MockSSEClient()),
  hostsListProvider.overrideWith((_) => FakeHostsNotifier([
        HostRecord(
          name: 'local',
          url: 'http://127.0.0.1:7777',
          online: true,
          createdAt: DateTime.utc(2026, 1, 1),
        ),
      ])),
];

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('SessionNavigator Routing Tests', () {
    testWidgets('routes headless session to ChatScreen and marks read',
        (tester) async {
      final session = _makeSession(
        id: 'sess-headless',
        engineType: 'headless',
        isUnread: true,
      );

      late BuildContext capturedContext;
      late WidgetRef capturedRef;

      await tester.pumpWidget(
        ProviderScope(
          overrides: _testOverrides,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: Scaffold(
              body: Consumer(
                builder: (context, ref, _) {
                  capturedContext = context;
                  capturedRef = ref;
                  return ElevatedButton(
                    onPressed: () => SessionNavigator.openSession(
                        capturedContext, capturedRef, session),
                    child: const Text('Open'),
                  );
                },
              ),
            ),
          ),
        ),
      );

      // Seed session in fleet
      capturedRef.read(fleetSessionsProvider.notifier).setSessions([session]);

      await tester.tap(find.text('Open'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(find.byType(ChatScreen), findsOneWidget);
      final live = capturedRef
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == session.id);
      expect(live.isUnread, isFalse);
    });

    testWidgets('routes tmux session to TerminalScreen and marks read',
        (tester) async {
      final session = _makeSession(
        id: 'sess-tmux',
        engineType: 'tmux',
        tmuxName: 'ackbar-sess-tmux',
        isUnread: true,
      );

      late BuildContext capturedContext;
      late WidgetRef capturedRef;

      await tester.pumpWidget(
        ProviderScope(
          overrides: _testOverrides,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: Scaffold(
              body: Consumer(
                builder: (context, ref, _) {
                  capturedContext = context;
                  capturedRef = ref;
                  return ElevatedButton(
                    onPressed: () => SessionNavigator.openSession(
                        capturedContext, capturedRef, session),
                    child: const Text('Open'),
                  );
                },
              ),
            ),
          ),
        ),
      );

      capturedRef.read(fleetSessionsProvider.notifier).setSessions([session]);

      await tester.tap(find.text('Open'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(find.byType(TerminalScreen), findsOneWidget);
      final live = capturedRef
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == session.id);
      expect(live.isUnread, isFalse);
    });

    testWidgets('openTranscript opens TranscriptScreen', (tester) async {
      final session = _makeSession(id: 'sess-t1', engineType: 'headless');

      late BuildContext capturedContext;

      await tester.pumpWidget(
        ProviderScope(
          overrides: _testOverrides,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: Scaffold(
              body: Builder(
                builder: (context) {
                  capturedContext = context;
                  return ElevatedButton(
                    onPressed: () => SessionNavigator.openTranscript(
                        capturedContext, session),
                    child: const Text('Open Transcript'),
                  );
                },
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.text('Open Transcript'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(find.byType(TranscriptScreen), findsOneWidget);
    });
  });

  group('SessionDetailSheet & AttentionScreen Surface Button Tests', () {
    testWidgets(
        'SessionDetailSheet displays Open Chat for headless and Open Terminal for tmux',
        (tester) async {
      final headlessSession =
          _makeSession(id: 'sess-sheet-1', engineType: 'headless');

      await tester.pumpWidget(
        ProviderScope(
          overrides: _testOverrides,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: Scaffold(
              body: SessionDetailSheet(session: headlessSession),
            ),
          ),
        ),
      );
      await tester.pump();

      final openBtn = find.byKey(const Key('sheet_open_session_button'));
      expect(openBtn, findsOneWidget);
      expect(find.text('Open Chat'), findsOneWidget);

      final transcriptBtn =
          find.byKey(const Key('sheet_open_transcript_button'));
      expect(transcriptBtn, findsOneWidget);

      await tester.tap(transcriptBtn);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(find.byType(TranscriptScreen), findsOneWidget);
    });

    testWidgets(
        'SessionDetailSheet disables Open Terminal when session is ended and tmux is deleted',
        (tester) async {
      final endedTmux = _makeSession(
        id: 'sess-sheet-2',
        engineType: 'tmux',
        state: SessionState.ended,
        tmuxName: '(deleted)',
      );

      await tester.pumpWidget(
        ProviderScope(
          overrides: _testOverrides,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: Scaffold(
              body: SessionDetailSheet(session: endedTmux),
            ),
          ),
        ),
      );
      await tester.pump();

      final openBtn = find.byKey(const Key('sheet_open_session_button'));
      expect(openBtn, findsOneWidget);
      expect(find.text('Open Terminal'), findsOneWidget);

      // Button is disabled
      final elevated = tester.widget<ElevatedButton>(openBtn);
      expect(elevated.onPressed, isNull);
    });

    testWidgets(
        'AttentionScreen secondary tool buttons route to session and transcript',
        (tester) async {
      final blockedSession = _makeSession(
        id: 'sess-att-1',
        engineType: 'headless',
        state: SessionState.working,
        blockerKind: 'permission',
      );

      final container = ProviderContainer(overrides: _testOverrides);
      addTearDown(container.dispose);

      container
          .read(fleetSessionsProvider.notifier)
          .setSessions([blockedSession]);

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: const AttentionScreen(),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      final openSessionBtn =
          find.byKey(Key('attention_open_session_${blockedSession.id}'));
      expect(openSessionBtn, findsOneWidget);
      expect(find.text('Open Chat'), findsOneWidget);

      final openTranscriptBtn =
          find.byKey(Key('attention_open_transcript_${blockedSession.id}'));
      expect(openTranscriptBtn, findsOneWidget);

      await tester.tap(openTranscriptBtn);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));

      expect(find.byType(TranscriptScreen), findsOneWidget);
    });
  });
}
