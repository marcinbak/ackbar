import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/plan.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/work_task.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/fleet/presentation/session_detail_sheet.dart';
import 'package:ackbar_mobile/features/main_navigation_shell.dart';
import 'package:ackbar_mobile/features/plans/presentation/plan_diff_viewer_modal.dart';
import 'package:ackbar_mobile/features/tasks/presentation/deliverable_markdown_modal.dart';

class TestFleetSessionsNotifier extends StateNotifier<List<Session>>
    implements FleetSessionsNotifier {
  TestFleetSessionsNotifier(super.initial);

  @override
  void setSessions(List<Session> sessions) {
    state = sessions;
  }

  @override
  Future<void> refreshSessions() async {}

  @override
  dynamic noSuchMethod(Invocation invocation) => null;
}

final _testSession = Session(
  id: 'claude-code:local:8492',
  agent: 'claude-code',
  host: 'local',
  nativeId: '8492',
  cwd: '/home/user/project',
  projectKey: 'Personal / ackbar',
  state: SessionState.working,
  activity: 'Building features...',
  contextPct: 50,
  startedAt: DateTime.now().subtract(const Duration(minutes: 10)),
  lastEventAt: DateTime.now().subtract(const Duration(seconds: 30)),
  managed: true,
  name: 'Feature Implementation',
);

final _testBlockedSession = Session(
  id: 'codex:local:9999',
  agent: 'codex',
  host: 'local',
  nativeId: '9999',
  cwd: '/home/user/backend',
  projectKey: 'Backend',
  state: SessionState.blocked,
  blocked: Blocked(
    kind: BlockKind.question,
    reason: 'Database port selection',
    since: DateTime.now().subtract(const Duration(minutes: 2)),
    question: 'Select database port to bind:',
    options: ['5432', '5433'],
  ),
  activity: 'Waiting for port decision',
  contextPct: 65,
  startedAt: DateTime.now().subtract(const Duration(minutes: 20)),
  lastEventAt: DateTime.now().subtract(const Duration(minutes: 2)),
  managed: true,
  name: 'Backend Microservice',
);

final _testPlan = PlanDocument(
  id: 'plan_123',
  title: 'Add ntfy push notifications',
  agent: 'claude-code',
  host: 'local',
  project: 'Ackbar',
  goal: 'Implement ntfy.sh and mobile deep-linking',
  userReviewCallout: 'Please review proposed diff',
  filesChanged: 5,
  addedLines: 200,
  deletedLines: 15,
  status: PlanStatus.pendingReview,
  createdAt: DateTime.now(),
  diffContent: 'diff --git a/main.go b/main.go\n+ // new line',
);

Widget _buildApp({
  String? initialRoute,
  List<Session>? sessions,
  List<PlanDocument>? plans,
}) {
  return ProviderScope(
    overrides: [
      fleetSessionsProvider.overrideWith(
        (ref) => TestFleetSessionsNotifier(
            sessions ?? [_testSession, _testBlockedSession]),
      ),
      plansListProvider.overrideWith((ref) {
        final notifier = PlansNotifier();
        notifier.setPlans(plans ?? [_testPlan]);
        return notifier;
      }),
    ],
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: MainNavigationShell(initialRoute: initialRoute),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('Mobile Deep-Linking Tests', () {
    testWidgets(
        'Cold start with ackbar://session?id=... switches to Fleet tab and opens SessionDetailSheet',
        (tester) async {
      await tester.pumpWidget(
        _buildApp(initialRoute: 'ackbar://session?id=claude-code:local:8492'),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      // Check Fleet tab is selected (index 0)
      final element = tester.element(find.byType(MainNavigationShell));
      final container = ProviderScope.containerOf(element);
      expect(container.read(mainTabNavIndexProvider), equals(0));

      // Session detail sheet opened
      expect(find.byType(SessionDetailSheet), findsOneWidget);
      expect(find.text('Feature Implementation'), findsWidgets);
    });

    testWidgets(
        'Warm start with ackbar://attention?session_id=... switches to Attention tab and opens SessionDetailSheet',
        (tester) async {
      await tester.pumpWidget(
        _buildApp(),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      final state = tester.state(find.byType(MainNavigationShell)) as dynamic;
      await state.didPushRouteInformation(
        RouteInformation(
            uri: Uri.parse('ackbar://attention?session_id=codex:local:9999')),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      final element = tester.element(find.byType(MainNavigationShell));
      final container = ProviderScope.containerOf(element);
      expect(container.read(mainTabNavIndexProvider), equals(1));

      expect(find.byType(SessionDetailSheet), findsOneWidget);
      expect(find.text('Backend Microservice'), findsWidgets);
    });

    testWidgets(
        'Warm start with ackbar://task?id=... switches to Plans tab and opens PlanDiffViewerModal',
        (tester) async {
      await tester.pumpWidget(
        _buildApp(),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      final state = tester.state(find.byType(MainNavigationShell)) as dynamic;
      await state.didPushRouteInformation(
        RouteInformation(uri: Uri.parse('ackbar://task?id=plan_123')),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      final element = tester.element(find.byType(MainNavigationShell));
      final container = ProviderScope.containerOf(element);
      expect(container.read(mainTabNavIndexProvider), equals(2));

      expect(find.byType(PlanDiffViewerModal), findsOneWidget);
      expect(find.text('Add ntfy push notifications'), findsWidgets);
    });

    testWidgets(
        'Warm start with ackbar://task?id=... resolves WorkTask and opens DeliverableMarkdownModal',
        (tester) async {
      final taskWithDeliverable = WorkTask(
        id: 'TASK-555',
        title: 'Deep Linked WorkTask',
        status: 'REVIEW',
        deliverables: [
          TaskDeliverable(
            id: 'del-555',
            taskId: 'TASK-555',
            kind: 'markdown',
            title: 'Task Deliverable',
            contentMd: '# Markdown Content for Deep Link',
            createdAt: DateTime.now(),
          ),
        ],
        createdAt: DateTime.now(),
        updatedAt: DateTime.now(),
      );

      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/tasks') {
          return http.Response(
            jsonEncode([taskWithDeliverable.toJson()]),
            200,
          );
        }
        return http.Response('[]', 200);
      });
      final mockApi = ApiClient(client: mockClient);

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApi),
          fleetSessionsProvider.overrideWith(
            (ref) => TestFleetSessionsNotifier([_testSession]),
          ),
          plansListProvider.overrideWith((ref) => PlansNotifier()),
        ],
      );
      addTearDown(container.dispose);
      container.read(hostsListProvider.notifier).setHosts([
        HostRecord(
          name: 'local',
          url: 'http://127.0.0.1:7777',
          online: true,
          createdAt: DateTime.now(),
        ),
      ]);
      container.read(tasksProvider.notifier).setTasks([taskWithDeliverable]);

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            theme: AppTheme.darkTheme,
            home: const MainNavigationShell(),
          ),
        ),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      final state = tester.state(find.byType(MainNavigationShell)) as dynamic;
      await state.didPushRouteInformation(
        RouteInformation(uri: Uri.parse('ackbar://task?id=TASK-555')),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      expect(container.read(mainTabNavIndexProvider), equals(2));
      expect(find.byType(DeliverableMarkdownModal), findsOneWidget);
      expect(find.text('Markdown Content for Deep Link'), findsWidgets);
    });

    testWidgets('Pending session is resolved once fleet sessions are populated',
        (tester) async {
      final sessionNotifier = TestFleetSessionsNotifier([]);

      await tester.pumpWidget(
        ProviderScope(
          overrides: [
            fleetSessionsProvider.overrideWith((ref) => sessionNotifier),
            plansListProvider.overrideWith((ref) => PlansNotifier()),
          ],
          child: const MaterialApp(
            home: MainNavigationShell(
                initialRoute: 'ackbar://session?id=delayed_sess'),
          ),
        ),
      );
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      // At start, sessions list is empty, so sheet is not opened yet
      expect(find.byType(SessionDetailSheet), findsNothing);

      // Now sessions arrive
      final delayedSession =
          _testSession.copyWith(id: 'delayed_sess', name: 'Delayed Session');
      sessionNotifier.setSessions([delayedSession]);
      await tester.pump();
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 500));

      // Now sheet opens!
      expect(find.byType(SessionDetailSheet), findsOneWidget);
      expect(find.text('Delayed Session'), findsWidgets);
    });
  });
}
