import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/tree_node.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/fleet/presentation/fleet_screen.dart';
import 'package:ackbar_mobile/features/fleet/presentation/fleet_tree_view.dart';
import 'package:ackbar_mobile/features/fleet/presentation/session_detail_sheet.dart';
import 'package:ackbar_mobile/features/terminal/presentation/terminal_screen.dart';

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

Widget _wrapScreen(Widget child, ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: child,
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  final testNodes = [
    TreeNode(
      path: 'Modemobile/NGL/ngl-ios',
      projectDir: '/Users/dev4u/Work/ngl-ios',
      gitUrl: 'https://github.com/modemobile/ngl-ios.git',
      createdAt: DateTime.now(),
      updatedAt: DateTime.now(),
    ),
  ];

  final testSessions = [
    Session(
      id: 'sess-task',
      agent: 'claude-code',
      host: 'mac.local',
      nativeId: 'nat-1',
      cwd: '/Users/dev4u/Work/ngl-ios',
      nodePath: 'Modemobile/NGL/ngl-ios',
      name: 'Task Session',
      customTitle: '',
      firstPrompt: '',
      lastPrompt: '',
      state: SessionState.working,
      activity: 'Running tests',
      startedAt: DateTime.now(),
      lastEventAt: DateTime.now(),
      managed: true,
      engineType: 'headless',
      currentTaskId: 'TASK-101',
    ),
    Session(
      id: 'sess-manual',
      agent: 'claude-code',
      host: 'mac.local',
      nativeId: 'nat-2',
      cwd: '/Users/dev4u/Work/ngl-ios',
      nodePath: 'Modemobile/NGL/ngl-ios',
      name: 'Manual Session',
      customTitle: '',
      firstPrompt: '',
      lastPrompt: '',
      state: SessionState.idle,
      activity: 'Waiting for prompt',
      startedAt: DateTime.now(),
      lastEventAt: DateTime.now(),
      managed: true,
      engineType: 'tmux',
    ),
    Session(
      id: 'sess-sup',
      agent: 'claude-code',
      host: 'mac.local',
      nativeId: 'nat-3',
      cwd: '/Users/dev4u/Work/ngl-ios',
      nodePath: 'Modemobile/NGL/ngl-ios',
      name: 'Supervisor Alpha',
      customTitle: '',
      firstPrompt: '',
      lastPrompt: '',
      state: SessionState.working,
      activity: 'Supervising agents',
      startedAt: DateTime.now(),
      lastEventAt: DateTime.now(),
      managed: true,
      engineType: 'headless',
      isSupervisor: true,
    ),
  ];

  final List<HostRecord> testHosts = [
    HostRecord(
      name: 'mac.local',
      url: 'http://127.0.0.1:7777',
      online: true,
      createdAt: DateTime.now(),
    ),
  ];

  late ApiClient mockApiClient;
  late SSEClient mockSseClient;

  setUp(() {
    final mockClient = MockClient((request) async {
      if (request.url.path == '/v1/sessions') {
        return http.Response(
          jsonEncode(testSessions.map((s) => s.toJson()).toList()),
          200,
        );
      }
      if (request.url.path == '/v1/nodes') {
        return http.Response(
          jsonEncode(testNodes.map((n) => n.toJson()).toList()),
          200,
        );
      }
      return http.Response('{}', 200);
    });
    mockApiClient = ApiClient(client: mockClient);
    mockSseClient = MockSSEClient();
  });

  tearDown(() {
    mockApiClient.dispose();
    mockSseClient.dispose();
  });

  group('FleetScreen & FleetTreeView Widget Tests', () {
    testWidgets('renders segmented view mode switcher [All Tree | Supervisors]',
        (tester) async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts(testHosts);
      container.read(fleetNodesProvider.notifier).setNodes(testNodes);
      container.read(fleetSessionsProvider.notifier).setSessions(testSessions);

      await tester.pumpWidget(
        _wrapScreen(
          const FleetScreen(),
          container,
        ),
      );
      await tester.pump(const Duration(seconds: 5));

      expect(find.text('🌐 All Tree'), findsOneWidget);
      expect(
          find.byKey(const Key('fleet_view_mode_supervisors')), findsOneWidget);
      expect(find.text('⚓+'), findsOneWidget);
    });

    testWidgets('tapping Supervisors segmented mode switches fleetViewMode',
        (tester) async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts(testHosts);
      container.read(fleetNodesProvider.notifier).setNodes(testNodes);
      container.read(fleetSessionsProvider.notifier).setSessions(testSessions);

      await tester.pumpWidget(
        _wrapScreen(
          const FleetScreen(),
          container,
        ),
      );
      await tester.pump(const Duration(seconds: 5));

      // Tap Supervisors tab in segmented switcher
      await tester.tap(find.byKey(const Key('fleet_view_mode_supervisors')));
      await tester.pump(const Duration(seconds: 5));

      expect(container.read(fleetViewModeProvider),
          equals(FleetViewMode.supervisors));
      expect(find.text('Supervisor Alpha'), findsOneWidget);
    });

    testWidgets(
        'FleetTreeView displays subsections (Supervisors, Tasks, Manual Sessions)',
        (tester) async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts(testHosts);
      container.read(fleetNodesProvider.notifier).setNodes(testNodes);
      container.read(fleetSessionsProvider.notifier).setSessions(testSessions);

      final treeRoots = container.read(fleetTreeProvider);

      await tester.pumpWidget(
        _wrapScreen(
          Scaffold(
            body: FleetTreeView(
              rootFolders: treeRoots,
              hosts: testHosts,
              onSessionTap: (_) {},
              onAddSession: (_) {},
            ),
          ),
          container,
        ),
      );
      await tester.pump(const Duration(seconds: 5));

      // Folder header
      expect(find.text('MODEMOBILE'), findsOneWidget);

      // Section headers
      expect(find.text('⚓ Supervisors'), findsOneWidget);
      expect(find.text('📋 Tasks'), findsOneWidget);
      expect(find.text('💻 Manual Sessions'), findsOneWidget);

      // Session titles
      expect(find.text('Supervisor Alpha'), findsOneWidget);
      expect(find.text('Task Session'), findsOneWidget);
      expect(find.text('Manual Session'), findsOneWidget);
    });

    testWidgets(
        '_upsertSession scopes nativeId matching to same host, preventing cross-host collision',
        (tester) async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(fleetSessionsProvider.notifier);
      final sessionMac = Session(
        id: 'sess-mac-1',
        agent: 'claude-code',
        host: 'mac.local',
        nativeId: 'native-dup',
        cwd: '/Users/dev/proj',
        state: SessionState.working,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        managed: true,
      );
      final sessionLinux = Session(
        id: 'sess-linux-1',
        agent: 'claude-code',
        host: 'linux.local',
        nativeId: 'native-dup', // Duplicate nativeId on different host!
        cwd: '/home/dev/proj',
        state: SessionState.idle,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        managed: true,
      );

      notifier.upsertSessionForTesting(sessionMac);
      notifier.upsertSessionForTesting(sessionLinux);

      final sessions = container.read(fleetSessionsProvider);
      expect(sessions.length, equals(2));
      expect(sessions.any((s) => s.id == 'sess-mac-1' && s.host == 'mac.local'),
          isTrue);
      expect(
          sessions
              .any((s) => s.id == 'sess-linux-1' && s.host == 'linux.local'),
          isTrue);
    });

    testWidgets(
        'renders CHAT and PTY badges and tapping session actions opens SessionDetailSheet',
        (tester) async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts(testHosts);
      container.read(fleetNodesProvider.notifier).setNodes(testNodes);
      container.read(fleetSessionsProvider.notifier).setSessions(testSessions);

      await tester.pumpWidget(
        _wrapScreen(
          const FleetScreen(),
          container,
        ),
      );
      await tester.pump(const Duration(seconds: 1));

      expect(find.byKey(const Key('session_mode_badge_sess-task')),
          findsOneWidget);
      expect(find.text('CHAT'), findsWidgets);
      expect(find.byKey(const Key('session_mode_badge_sess-manual')),
          findsOneWidget);
      expect(find.text('PTY'), findsWidgets);

      final moreBtn = find.byKey(const Key('session_more_sess-task'));
      expect(moreBtn, findsOneWidget);
      await tester.drag(find.byType(FleetTreeView), const Offset(0, -300));
      await tester.pump(const Duration(milliseconds: 300));
      await tester.tap(moreBtn);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      expect(find.byType(SessionDetailSheet), findsOneWidget);
    });
  });
}
