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
import 'package:ackbar_mobile/core/models/work_task.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/tasks/presentation/work_board_screen.dart';

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
    TreeNode(
      path: 'Personal/Ackbar',
      projectDir: '/home/dev4u/Personal/ackbar',
      gitUrl: 'git@github.com:marcinbak/ackbar.git',
      createdAt: DateTime.now(),
      updatedAt: DateTime.now(),
    ),
  ];

  final testTasks = [
    WorkTask(
      id: 'TASK-101',
      title: 'Align Mobile Client with Phase 5 Specifications',
      groupName: 'Modemobile',
      projectName: 'ngl-ios',
      status: 'REVIEW',
      substatus: 'approved',
      notes: 'Ready for merge',
      createdAt: DateTime.now(),
      updatedAt: DateTime.now(),
      externalRefs: [
        const TaskExternalRef(
          taskId: 'TASK-101',
          tracker: 'linear',
          refKey: 'NGL-409',
          url: 'https://linear.app/issue/NGL-409',
        ),
      ],
      deliverables: [
        TaskDeliverable(
          id: 'del-1',
          taskId: 'TASK-101',
          kind: 'markdown',
          title: 'Implementation Notes',
          contentMd: '# Phase 5 Notes\n- Hierarchical tree implemented.',
          createdAt: DateTime.now(),
        ),
      ],
      workers: [
        TaskWorker(
          taskId: 'TASK-101',
          host: 'mac.local',
          sessionId: 'sess-worker-1',
          agent: 'claude-code',
          assignedAt: DateTime.now(),
        ),
      ],
    ),
    WorkTask(
      id: 'TASK-102',
      title: 'Fix SQLite Connection Pool Exhaustion',
      groupName: 'Personal',
      projectName: 'ackbar',
      status: 'IN_PROGRESS',
      notes: 'Under active development',
      createdAt: DateTime.now(),
      updatedAt: DateTime.now(),
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
  var mergePrCalled = false;

  setUp(() {
    mergePrCalled = false;
    final mockClient = MockClient((request) async {
      if (request.url.path == '/v1/tasks') {
        return http.Response(
          jsonEncode(testTasks.map((t) => t.toJson()).toList()),
          200,
        );
      }
      if (request.url.path == '/v1/nodes') {
        return http.Response(
          jsonEncode(testNodes.map((n) => n.toJson()).toList()),
          200,
        );
      }
      if (request.url.path == '/v1/tasks/merge-pr') {
        mergePrCalled = true;
        return http.Response(jsonEncode({'status': 'merged'}), 200);
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

  group('WorkBoardScreen Widget Tests', () {
    testWidgets('renders group filter chips, pillar tabs, and task cards',
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
      container.read(tasksProvider.notifier).setTasks(testTasks);

      await tester.pumpWidget(
        _wrapScreen(
          const WorkBoardScreen(),
          container,
        ),
      );
      await tester.pump();

      // Top title and controls
      expect(find.text('WORK BOARD'), findsOneWidget);
      expect(find.text('PLANS'), findsOneWidget);

      // Group filter chips
      expect(find.text('All Groups'), findsOneWidget);
      expect(find.text('Modemobile'), findsOneWidget);
      expect(find.text('Personal'), findsOneWidget);

      // Pillar tabs
      expect(find.text('New'), findsOneWidget);
      expect(find.text('In Progress'), findsOneWidget);
      expect(find.text('Review'), findsOneWidget);
      expect(find.text('Done'), findsOneWidget);
    });

    testWidgets(
        'switching to Review pillar displays TaskCard with squash merge button',
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
      container.read(tasksProvider.notifier).setTasks(testTasks);

      await tester.pumpWidget(
        _wrapScreen(
          const WorkBoardScreen(),
          container,
        ),
      );
      await tester.pump();

      // Tap Review pillar tab
      await tester.tap(find.text('Review'));
      await tester.pump();

      // Task card should be visible
      expect(find.text('Align Mobile Client with Phase 5 Specifications'),
          findsOneWidget);
      expect(find.text('NGL-409'), findsOneWidget);
      expect(find.text('@mac.local'), findsOneWidget);
      expect(find.text('⚡ SQUASH & MERGE PR'), findsOneWidget);

      // Tap Squash & Merge PR button
      await tester.tap(find.text('⚡ SQUASH & MERGE PR'));
      await tester.pump();

      expect(mergePrCalled, isTrue);
    });

    testWidgets(
        'tapping deliverable chip opens deliverable markdown modal sheet',
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
      container.read(tasksProvider.notifier).setTasks(testTasks);

      await tester.pumpWidget(
        _wrapScreen(
          const WorkBoardScreen(),
          container,
        ),
      );
      await tester.pump();

      // Tap Review pillar tab to see task with deliverable
      await tester.tap(find.text('Review'));
      await tester.pump();

      expect(find.text('Implementation Notes'), findsOneWidget);

      // Tap deliverable chip
      await tester.tap(find.text('Implementation Notes'));
      await tester.pump();

      // Modal bottom sheet should display deliverable markdown
      expect(find.text('Phase 5 Notes'), findsOneWidget);
      expect(find.text('Hierarchical tree implemented.'), findsOneWidget);
    });

    testWidgets(
        'empty pillar displays correctly interpolated label without list syntax',
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
      container.read(tasksProvider.notifier).setTasks(testTasks);

      await tester.pumpWidget(
        _wrapScreen(
          const WorkBoardScreen(),
          container,
        ),
      );
      await tester.pump(const Duration(milliseconds: 100));

      // Tap 'New' pillar tab (testTasks has 0 NEW tasks)
      await tester.tap(find.text('New'));
      await tester.pump(const Duration(milliseconds: 100));

      // Should display 'No tasks in New' without list brackets or interpolation bugs
      expect(find.text('No tasks in New'), findsOneWidget);
      expect(find.textContaining('No tasks in ['), findsNothing);
    });

    testWidgets(
        'TasksNotifier.mergePR dispatches to targetHost specified by task worker/deliverable',
        (tester) async {
      String? mergedUrl;
      final customClient = MockClient((request) async {
        if (request.url.path == '/v1/tasks/merge-pr') {
          mergedUrl = request.url.toString();
          return http.Response(jsonEncode({'status': 'merged'}), 200);
        }
        if (request.url.path == '/v1/tasks') {
          return http.Response('[]', 200);
        }
        return http.Response('{}', 200);
      });
      final customApi = ApiClient(client: customClient);

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(customApi),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final host1 = HostRecord(
        name: 'mac-host',
        url: 'http://127.0.0.1:7771',
        online: true,
        createdAt: DateTime.now(),
      );
      final host2 = HostRecord(
        name: 'remote-host',
        url: 'http://127.0.0.1:7772',
        online: true,
        createdAt: DateTime.now(),
      );
      container.read(hostsListProvider.notifier).setHosts([host1, host2]);

      final remoteTask = WorkTask(
        id: 'TASK-REMOTE',
        title: 'Remote PR Task',
        status: 'REVIEW',
        substatus: 'approved',
        createdAt: DateTime.now(),
        updatedAt: DateTime.now(),
        workers: [
          TaskWorker(
            taskId: 'TASK-REMOTE',
            host: 'remote-host',
            sessionId: 'sess-remote',
            agent: 'claude-code',
            assignedAt: DateTime.now(),
          ),
        ],
      );
      container.read(tasksProvider.notifier).setTasks([remoteTask]);

      final ok =
          await container.read(tasksProvider.notifier).mergePR('TASK-REMOTE');
      expect(ok, isTrue);
      expect(mergedUrl, startsWith('http://127.0.0.1:7772'));
    });
  });
}
