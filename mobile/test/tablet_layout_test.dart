import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/tree_node.dart';
import 'package:ackbar_mobile/core/models/work_task.dart';
import 'package:ackbar_mobile/core/navigation/session_navigator.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/providers/tablet_workspace_provider.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/core/widgets/ackbar_bottom_nav.dart';
import 'package:ackbar_mobile/core/widgets/ackbar_nav_rail.dart';
import 'package:ackbar_mobile/core/widgets/segmented_filter_tabs.dart';
import 'package:ackbar_mobile/features/attention/presentation/attention_screen.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/fleet/presentation/tablet_fleet_workspace.dart';
import 'package:ackbar_mobile/features/hosts/presentation/hosts_screen.dart';
import 'package:ackbar_mobile/features/main_navigation_shell.dart';
import 'package:ackbar_mobile/features/tasks/presentation/deliverable_markdown_modal.dart';
import 'package:ackbar_mobile/features/tasks/presentation/work_board_screen.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_screen.dart';

class _MockSSEClient extends SSEClient {
  @override
  Stream<Session> subscribeMultipleHosts(List<HostRecord> hosts,
          {bool includeOffline = false,
          void Function(HostRecord host)? onHostReconnected,
          bool? includeArchived,
          bool? includeDone}) =>
      const Stream.empty();
}

class _FakeHostsNotifier extends StateNotifier<List<HostRecord>>
    implements HostsNotifier {
  _FakeHostsNotifier(super.initial);
  @override
  Future<void> refreshHosts() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => null;
}

final _t0 = DateTime.utc(2026, 11, 20, 12, 0);

Session _sess(String id, String name,
        {SessionState state = SessionState.idle, Blocked? blocked}) =>
    Session(
      id: id,
      agent: 'claude-code',
      host: 'mac.local',
      nativeId: id,
      cwd: '/workspace/ackbar',
      nodePath: 'Personal/ackbar',
      name: name,
      state: state,
      startedAt: _t0,
      lastEventAt: _t0,
      managed: true,
      engineType: 'headless',
      blocked: blocked,
    );

void _setViewport(WidgetTester tester, Size size, {double bottomInset = 0}) {
  tester.view.devicePixelRatio = 1.0;
  tester.view.physicalSize = size;
  tester.view.viewInsets = FakeViewPadding(bottom: bottomInset);
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
    tester.view.resetViewInsets();
  });
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  final hosts = [
    HostRecord(
        name: 'mac.local',
        url: 'http://127.0.0.1:7777',
        online: true,
        createdAt: _t0),
    HostRecord(
        name: 'linux.local',
        url: 'http://100.64.0.2:7777',
        online: true,
        createdAt: _t0),
  ];
  final nodes = [
    TreeNode(
        path: 'Personal/ackbar',
        projectDir: '/workspace/ackbar',
        createdAt: _t0,
        updatedAt: _t0),
  ];
  final sessA = _sess('sess-a', 'Session Alpha');
  final sessB = _sess('sess-b', 'Session Beta');
  final blockedSess = _sess(
    'sess-blk',
    'Blocked Session',
    state: SessionState.blocked,
    blocked: Blocked(
      kind: BlockKind.question,
      reason: 'Choose deploy mode',
      since: _t0,
      question: 'Deploy to staging?',
      options: ['Yes', 'No'],
    ),
  );
  final tasks = [
    WorkTask(
        id: 'T-1',
        title: 'Review Task',
        groupName: 'Personal',
        projectName: 'ackbar',
        status: 'REVIEW',
        createdAt: _t0,
        updatedAt: _t0),
    WorkTask(
        id: 'T-2',
        title: 'Active Task',
        groupName: 'Personal',
        projectName: 'ackbar',
        status: 'IN_PROGRESS',
        createdAt: _t0,
        updatedAt: _t0),
  ];

  late ApiClient api;
  late _MockSSEClient sse;
  List<Session> mockSessions = [sessA, sessB];

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    mockSessions = [sessA, sessB];
    api = ApiClient(
      client: MockClient((req) async {
        final body = switch (req.url.path) {
          '/v1/sessions' =>
            jsonEncode(mockSessions.map((s) => s.toJson()).toList()),
          '/v1/nodes' => jsonEncode(nodes.map((n) => n.toJson()).toList()),
          '/v1/tasks' => jsonEncode(tasks.map((t) => t.toJson()).toList()),
          '/v1/sessions/transcript' => jsonEncode({
              'session_id': 'sess-a',
              'native_id': 'sess-a',
              'agent': 'claude-code',
              'title': 'Session',
              'cwd': '/workspace/ackbar',
              'messages': [],
              'markdown': '',
            }),
          '/v1/sessions/subagents' => jsonEncode({'subagents': []}),
          '/v1/sessions/commands' => jsonEncode({'commands': []}),
          _ => '[]',
        };
        return http.Response(body, 200);
      }),
    );
    sse = _MockSSEClient();
  });

  tearDown(() {
    api.dispose();
    sse.dispose();
  });

  ProviderContainer makeContainer({List<Session>? sessions}) {
    if (sessions != null) mockSessions = sessions;
    final c = ProviderContainer(overrides: [
      apiClientProvider.overrideWithValue(api),
      sseClientProvider.overrideWithValue(sse),
      hostsListProvider.overrideWith((_) => _FakeHostsNotifier(hosts)),
    ]);
    addTearDown(c.dispose);
    c.read(fleetNodesProvider.notifier).setNodes(nodes);
    c.read(fleetSessionsProvider.notifier).setSessions(mockSessions);
    c.read(tasksProvider.notifier).setTasks(tasks);
    return c;
  }

  Widget wrap(Widget child, ProviderContainer container) =>
      UncontrolledProviderScope(
        container: container,
        child: MaterialApp(theme: AppTheme.darkTheme, home: child),
      );

  Future<void> p(WidgetTester t, [int ms = 300]) =>
      t.pump(Duration(milliseconds: ms));
  Finder k(String s) => find.byKey(Key(s));
  Finder composer() => find.widgetWithText(TextField, 'Ask Claude Code...');

  group('Adaptive Tablet UI Tests', () {
    testWidgets(
        'phone viewports (390x844 and 800x600) preserve phone nav, tabs, and push route',
        (tester) async {
      for (final size in const [Size(390, 844), Size(800, 600)]) {
        _setViewport(tester, size);
        final c = makeContainer();
        await tester.pumpWidget(wrap(const MainNavigationShell(), c));
        await p(tester);
        expect(find.byType(AckbarBottomNav), findsOneWidget);
        expect(find.byType(AckbarNavRail), findsNothing);
        await tester.tap(find.text('Plans'));
        await p(tester, 200);
        expect(find.byType(SegmentedFilterTabs), findsWidgets);
        expect(k('tablet_kanban_four_col'), findsNothing);
      }
      _setViewport(tester, const Size(390, 844));
      final c = makeContainer();
      await tester.pumpWidget(wrap(
        Scaffold(
          body: Consumer(
            builder: (ctx, ref, _) => ElevatedButton(
              onPressed: () => SessionNavigator.openSession(ctx, ref, sessA),
              child: const Text('Go'),
            ),
          ),
        ),
        c,
      ));
      await tester.tap(find.text('Go'));
      await tester.pump();
      await p(tester);
      expect(find.byType(ChatScreen), findsOneWidget);
      expect(c.read(selectedTabletSessionProvider), isNull);
    });

    testWidgets(
        'landscape 1200x800 shows NavRail, 320dp tree + empty state, mounts ChatScreen, toggles collapse',
        (tester) async {
      _setViewport(tester, const Size(1200, 800));
      final c = makeContainer();
      await tester.pumpWidget(wrap(const MainNavigationShell(), c));
      await p(tester);
      expect(k('nav_rail_item_fleet'), findsOneWidget);
      expect(find.byType(AckbarBottomNav), findsNothing);
      expect(tester.getSize(k('tablet_fleet_left_pane')).width, equals(320.0));
      expect(k('tablet_fleet_empty_workspace'), findsOneWidget);
      expect(c.read(selectedTabletSessionProvider), isNull);

      await tester.tap(find.text('Session Alpha'));
      await p(tester, 400);
      expect(c.read(selectedTabletSessionProvider)?.id, equals('sess-a'));
      expect(find.byType(ChatScreen), findsOneWidget);
      expect(find.byIcon(Icons.arrow_back_rounded), findsNothing);

      await tester.tap(k('tablet_toggle_fleet_pane'));
      await p(tester, 200);
      expect(k('tablet_fleet_left_pane'), findsNothing);
      await tester.tap(k('tablet_toggle_fleet_pane'));
      await p(tester, 200);
      expect(k('tablet_fleet_left_pane'), findsOneWidget);
    });

    testWidgets(
        'draft persistence restores unsent composer prompt when switching sessions',
        (tester) async {
      _setViewport(tester, const Size(1200, 800));
      final c = makeContainer();
      await tester.pumpWidget(wrap(const TabletFleetWorkspace(), c));
      await p(tester);

      await tester.tap(find.text('Session Alpha'));
      await p(tester, 400);
      await tester.enterText(composer(), 'Unsent draft A');
      await tester.pump();

      await tester.tap(find.text('Session Beta'));
      await p(tester, 400);
      expect(tester.widget<TextField>(composer()).controller?.text, isEmpty);

      await tester.tap(find.text('Session Alpha'));
      await p(tester, 400);
      expect(tester.widget<TextField>(composer()).controller?.text,
          equals('Unsent draft A'));
    });

    testWidgets(
        'docked keyboard compaction keeps 320dp tree, hides pill bar & caption, compacts AppBar to 40dp',
        (tester) async {
      for (final cfg in [
        (const Size(1200, 800), 360.0),
        (const Size(1200, 400), 0.0),
      ]) {
        _setViewport(tester, cfg.$1, bottomInset: cfg.$2);
        final c = makeContainer();
        c.read(selectedTabletSessionProvider.notifier).state = sessA;
        await tester.pumpWidget(wrap(const MainNavigationShell(), c));
        await p(tester, 400);
        expect(find.byType(AckbarNavRail), findsOneWidget);
        expect(
            tester.getSize(k('tablet_fleet_left_pane')).width, equals(320.0));
        expect(find.byKey(const ValueKey('headless_config_pill_bar')),
            findsNothing);
        expect(find.textContaining('Headless turn-by-turn'), findsNothing);
        expect(k('chat_compact_config_button'), findsOneWidget);
        expect(tester.getSize(find.byType(AppBar)).height, equals(40.0));
      }
    });

    testWidgets(
        'cross-tab Open Chat jump from Attention switches to Fleet split workspace',
        (tester) async {
      _setViewport(tester, const Size(1200, 800));
      final c = makeContainer(sessions: [sessA, blockedSess]);
      await tester.pumpWidget(wrap(const MainNavigationShell(), c));
      await p(tester);

      await tester.tap(k('nav_rail_item_attention'));
      await p(tester);
      expect(k('tablet_attention_split_view'), findsOneWidget);

      await tester.tap(k('attention_open_session_sess-blk'));
      await p(tester, 400);
      expect(c.read(mainTabNavIndexProvider), equals(0));
      expect(c.read(selectedTabletSessionProvider)?.id, equals('sess-blk'));
      expect(find.byType(ChatScreen), findsOneWidget);
    });

    testWidgets(
        'WorkBoard kanban columns, Attention split, 2-col Hosts grid, and side sheets',
        (tester) async {
      _setViewport(tester, const Size(1200, 800));
      final c = makeContainer(sessions: [sessA, blockedSess]);
      await tester.pumpWidget(wrap(const WorkBoardScreen(), c));
      await p(tester, 200);
      expect(k('tablet_kanban_four_col'), findsOneWidget);

      _setViewport(tester, const Size(800, 1200));
      await p(tester, 200);
      expect(k('tablet_kanban_two_col_scroll'), findsOneWidget);

      _setViewport(tester, const Size(1200, 800));
      await tester.pumpWidget(wrap(const AttentionScreen(), c));
      await p(tester, 200);
      expect(k('tablet_attention_split_view'), findsOneWidget);

      await tester.pumpWidget(wrap(const HostsScreen(), c));
      await p(tester, 200);
      expect(tester.getTopLeft(find.text('mac.local')).dy,
          equals(tester.getTopLeft(find.text('linux.local')).dy));
      expect(tester.getTopLeft(find.text('linux.local')).dx,
          greaterThan(tester.getTopLeft(find.text('mac.local')).dx));

      late BuildContext sheetCtx;
      await tester.pumpWidget(wrap(
        Scaffold(body: Builder(builder: (ctx) {
          sheetCtx = ctx;
          return const SizedBox.shrink();
        })),
        c,
      ));
      DeliverableMarkdownModal.show(
        sheetCtx,
        TaskDeliverable(
            id: 'd1',
            taskId: 'T-1',
            kind: 'markdown',
            title: 'Plan',
            contentMd: '# Hi',
            createdAt: _t0),
      );
      await p(tester);
      expect(k('adaptive_side_sheet_container'), findsOneWidget);
      Navigator.of(sheetCtx).pop();
      await p(tester);

      TranscriptScreen.open(sheetCtx, sessA);
      await p(tester);
      expect(k('adaptive_side_sheet_container'), findsOneWidget);
      expect(find.byType(TranscriptScreen), findsOneWidget);
    });

    testWidgets(
        'rotation continuity preserves ChatScreen State instance across landscape <-> portrait',
        (tester) async {
      _setViewport(tester, const Size(1200, 800));
      final c = makeContainer();
      c.read(selectedTabletSessionProvider.notifier).state = sessA;
      await tester.pumpWidget(wrap(const TabletFleetWorkspace(), c));
      await p(tester);
      final stateBefore = tester.state(find.byType(ChatScreen));

      _setViewport(tester, const Size(800, 1200));
      await p(tester);
      expect(identical(stateBefore, tester.state(find.byType(ChatScreen))),
          isTrue);

      await tester.tap(k('tablet_open_fleet_drawer_button'));
      await p(tester);
      expect(k('tablet_portrait_fleet_drawer'), findsOneWidget);
    });
  });
}
