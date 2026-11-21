import 'dart:async';
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
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/fleet/presentation/fleet_screen.dart';
import 'package:ackbar_mobile/features/fleet/presentation/fleet_tree_view.dart';

class _RecordingSSEClient extends SSEClient {
  final StreamController<Session> controller =
      StreamController<Session>.broadcast();
  final List<({bool? includeArchived, bool? includeDone})> calls = [];

  @override
  Stream<Session> subscribeHost(
    HostRecord host, {
    void Function(HostRecord host)? onHostReconnected,
    bool? includeArchived,
    bool? includeDone,
  }) {
    calls.add((includeArchived: includeArchived, includeDone: includeDone));
    return controller.stream;
  }

  @override
  Stream<Session> subscribeMultipleHosts(
    List<HostRecord> hosts, {
    bool includeOffline = false,
    void Function(HostRecord host)? onHostReconnected,
    bool? includeArchived,
    bool? includeDone,
  }) {
    calls.add((includeArchived: includeArchived, includeDone: includeDone));
    return controller.stream;
  }

  @override
  void dispose() {
    controller.close();
    super.dispose();
  }
}

Session _makeSession({
  required String id,
  String nativeId = '',
  String title = 'Test Session',
  String nodePath = 'Personal/Ackbar',
  SessionState state = SessionState.idle,
  bool isDone = false,
  bool archived = false,
  bool deleted = false,
}) {
  final now = DateTime.utc(2026, 11, 21, 12, 0, 0);
  return Session(
    id: id,
    agent: 'claude-code',
    host: 'local',
    nativeId: nativeId.isEmpty ? id : nativeId,
    cwd: '/workspace/ackbar',
    nodePath: nodePath,
    name: title,
    customTitle: title,
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    isDone: isDone,
    archived: archived,
    deleted: deleted,
  );
}

Widget _wrap(Widget child, ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: Scaffold(body: child),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  test('ApiClient.getSessions and SSEClient.subscribeHost send filter params',
      () async {
    final requestedUris = <Uri>[];
    final mockHttp = MockClient((req) async {
      requestedUris.add(req.url);
      return http.Response('[]', 200);
    });
    final api = ApiClient(client: mockHttp);
    addTearDown(api.dispose);

    await api.getSessions('http://127.0.0.1:7777');
    await api.getSessions(
      'http://127.0.0.1:7777',
      includeArchived: false,
      includeDone: false,
    );

    expect(requestedUris[0].toString(), 'http://127.0.0.1:7777/v1/sessions');
    expect(requestedUris[1].queryParameters, {
      'include_archived': 'false',
      'include_done': 'false',
    });

    final sseUris = <Uri>[];
    final streamCtrl = StreamController<List<int>>();
    final mockSseHttp = MockClient.streaming((req, _) async {
      sseUris.add(req.url);
      return http.StreamedResponse(streamCtrl.stream, 200);
    });
    final sse = SSEClient(client: mockSseHttp);
    addTearDown(() async {
      await streamCtrl.close();
      sse.dispose();
    });

    final sub = sse
        .subscribeHost(
          HostRecord(
            name: 'local',
            url: 'http://127.0.0.1:7777',
            authToken: 'tok-123',
            online: true,
            createdAt: DateTime.utc(2026, 11, 21),
          ),
          includeArchived: false,
          includeDone: false,
        )
        .listen((_) {});
    await Future<void>.delayed(const Duration(milliseconds: 20));
    await sub.cancel();

    expect(sseUris, hasLength(1));
    expect(sseUris.first.path, '/v1/events');
    expect(sseUris.first.queryParameters, {
      'token': 'tok-123',
      'include_archived': 'false',
      'include_done': 'false',
    });

    final parsedFromInts = Session.fromJson({
      'id': 's-int',
      'agent': 'claude-code',
      'state': 1,
      'archived': 1,
      'deleted': 1,
    });
    expect(parsedFromInts.archived, isTrue);
    expect(parsedFromInts.deleted, isTrue);
  });

  testWidgets(
      'FleetSessionsNotifier filters fetch, evicts SSE transitions, and toggles showDone',
      (tester) async {
    SharedPreferences.setMockInitialValues({});
    final sessionQueries = <Map<String, String>>[];

    final activeSess = _makeSession(
      id: 'sess-active',
      title: 'Active Work',
      nodePath: 'Modemobile/NGL',
    );
    final endedNotDoneSess = _makeSession(
      id: 'sess-ended-active',
      title: 'Recent Ended',
      nodePath: 'Modemobile/NGL',
      state: SessionState.ended,
      isDone: false,
    );
    final doneSess = _makeSession(
      id: 'sess-done',
      title: 'Finished Task',
      nodePath: 'Modemobile/NGL',
      state: SessionState.ended,
      isDone: true,
    );
    final archivedSess = _makeSession(
      id: 'sess-archived',
      title: 'Archived Task',
      nodePath: 'Modemobile/NGL',
      state: SessionState.ended,
      archived: true,
    );
    final deletedSess = _makeSession(
      id: 'sess-deleted',
      title: 'Deleted Task',
      nodePath: 'Modemobile/NGL',
      deleted: true,
    );

    final mockHttp = MockClient((req) async {
      if (req.url.path == '/v1/version') {
        return http.Response(jsonEncode({'version': 'v0.3.0'}), 200);
      }
      if (req.url.path == '/v1/sessions') {
        sessionQueries.add(req.url.queryParameters);
        return http.Response(
          jsonEncode([
            activeSess.toJson(),
            endedNotDoneSess.toJson(),
            doneSess.toJson(),
            archivedSess.toJson(),
            deletedSess.toJson(),
          ]),
          200,
        );
      }
      if (req.url.path == '/v1/nodes') {
        return http.Response(
          jsonEncode([
            TreeNode(
              path: 'Modemobile/NGL',
              projectDir: '/workspace/ngl',
              gitUrl: '',
              createdAt: DateTime.utc(2026, 11, 21),
              updatedAt: DateTime.utc(2026, 11, 21),
            ).toJson(),
          ]),
          200,
        );
      }
      return http.Response('[]', 200);
    });

    final api = ApiClient(client: mockHttp);
    final sse = _RecordingSSEClient();
    addTearDown(() {
      api.dispose();
      sse.dispose();
    });

    final container = ProviderContainer(
      overrides: [
        apiClientProvider.overrideWithValue(api),
        sseClientProvider.overrideWithValue(sse),
      ],
    );
    addTearDown(container.dispose);

    container.read(hostsListProvider.notifier).setHosts([
      HostRecord(
        name: 'local',
        url: 'http://127.0.0.1:7777',
        online: true,
        createdAt: DateTime.utc(2026, 11, 21),
      ),
    ]);

    final notifier = container.read(fleetSessionsProvider.notifier);
    await notifier.refreshSessions();

    expect(
      sse.calls.first,
      equals((includeArchived: false, includeDone: false)),
    );
    expect(sessionQueries.last, {
      'include_archived': 'false',
      'include_done': 'false',
    });

    var currentIds =
        container.read(fleetSessionsProvider).map((s) => s.id).toSet();
    expect(currentIds, {'sess-active', 'sess-ended-active'});

    final tree = container.read(fleetTreeProvider);
    expect(
      tree.first.children.first.manualSessions.map((s) => s.id),
      containsAll(['sess-active', 'sess-ended-active']),
    );

    sse.controller.add(activeSess.copyWith(isDone: true));
    await tester.pump();
    expect(
      container.read(fleetSessionsProvider).any((s) => s.id == 'sess-active'),
      isFalse,
    );

    final sseTemp = _makeSession(id: 'sess-temp', title: 'Temp');
    sse.controller.add(sseTemp);
    await tester.pump();
    expect(
      container.read(fleetSessionsProvider).any((s) => s.id == 'sess-temp'),
      isTrue,
    );
    sse.controller.add(sseTemp.copyWith(archived: true));
    await tester.pump();
    expect(
      container.read(fleetSessionsProvider).any((s) => s.id == 'sess-temp'),
      isFalse,
    );

    final sseTempDel = _makeSession(id: 'sess-temp-del', title: 'Temp Del');
    sse.controller.add(sseTempDel);
    await tester.pump();
    sse.controller.add(sseTempDel.copyWith(deleted: true));
    await tester.pump();
    expect(
      container.read(fleetSessionsProvider).any((s) => s.id == 'sess-temp-del'),
      isFalse,
    );

    await tester.pumpWidget(_wrap(const FleetScreen(), container));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('fleet_toggle_show_done')));
    await tester.pumpAndSettle();

    expect(container.read(fleetShowDoneProvider), isTrue);
    expect(sse.calls.last, equals((includeArchived: false, includeDone: true)));
    expect(sessionQueries.last, {
      'include_archived': 'false',
      'include_done': 'true',
    });

    currentIds = container.read(fleetSessionsProvider).map((s) => s.id).toSet();
    expect(currentIds, contains('sess-done'));

    expect(find.text('✅ Done'), findsOneWidget);
    expect(find.text('Finished Task'), findsNothing);

    await tester.tap(
        find.byKey(const Key('fleet_subsection_header_Modemobile/NGL:done')));
    await tester.pumpAndSettle();
    expect(find.text('Finished Task'), findsOneWidget);

    await tester.tap(find.byKey(const Key('fleet_toggle_show_done')));
    await tester.pumpAndSettle();
    expect(container.read(fleetShowDoneProvider), isFalse);
    expect(find.text('Finished Task'), findsNothing);

    final prefs = await tester.runAsync(() => SharedPreferences.getInstance());
    expect(prefs?.getBool('ackbar_fleet_show_done'), isFalse);
  });

  testWidgets(
      'FleetTreeView persists folder and subsection collapse state and expands temporarily on search',
      (tester) async {
    SharedPreferences.setMockInitialValues({});

    final folderA = FleetTreeFolderNode(
      path: 'Personal/Ackbar',
      name: 'Ackbar',
      manualSessions: [
        _makeSession(
          id: 'a-manual',
          title: 'Ackbar Manual Session',
          nodePath: 'Personal/Ackbar',
        ),
      ],
    );
    final folderB = FleetTreeFolderNode(
      path: 'Work/App',
      name: 'App',
      manualSessions: [
        _makeSession(
          id: 'b-manual',
          title: 'Work Manual Session',
          nodePath: 'Work/App',
        ),
      ],
      doneSessions: [
        _makeSession(
          id: 'b-done',
          title: 'Work Done Session',
          nodePath: 'Work/App',
          state: SessionState.ended,
          isDone: true,
        ),
      ],
    );

    final container1 = ProviderContainer();
    addTearDown(container1.dispose);

    await tester.pumpWidget(
      _wrap(
        FleetTreeView(
          rootFolders: [folderA, folderB],
          hosts: const [],
          onSessionTap: (_) {},
          onAddSession: (_) {},
        ),
        container1,
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Ackbar Manual Session'), findsOneWidget);
    expect(find.text('Work Manual Session'), findsOneWidget);
    expect(find.text('Work Done Session'), findsNothing);

    await tester
        .tap(find.byKey(const Key('fleet_folder_header_Personal/Ackbar')));
    await tester.pumpAndSettle();
    expect(find.text('Ackbar Manual Session'), findsNothing);

    await tester
        .tap(find.byKey(const Key('fleet_subsection_header_Work/App:manual')));
    await tester.pumpAndSettle();
    expect(find.text('Work Manual Session'), findsNothing);

    await tester
        .tap(find.byKey(const Key('fleet_subsection_header_Work/App:done')));
    await tester.pumpAndSettle();
    expect(find.text('Work Done Session'), findsOneWidget);

    final prefs = await tester.runAsync(() => SharedPreferences.getInstance());
    expect(
      jsonDecode(prefs!.getString('ackbar_collapsed_groups')!),
      contains('Personal/Ackbar'),
    );
    expect(
      jsonDecode(prefs.getString('ackbar_collapsed_subsections')!),
      contains('Work/App:manual'),
    );
    expect(
      jsonDecode(prefs.getString('ackbar_expanded_subsections')!),
      contains('Work/App:done'),
    );

    await tester.pumpWidget(const SizedBox.shrink());
    container1.dispose();

    final container2 = ProviderContainer();
    addTearDown(container2.dispose);
    container2.read(fleetTreeCollapseProvider);
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));

    await tester.pumpWidget(
      _wrap(
        FleetTreeView(
          rootFolders: [folderA, folderB],
          hosts: const [],
          onSessionTap: (_) {},
          onAddSession: (_) {},
        ),
        container2,
      ),
    );
    await tester.pump();

    expect(find.text('Ackbar Manual Session'), findsNothing);
    expect(find.text('Work Manual Session'), findsNothing);
    expect(find.text('Work Done Session'), findsOneWidget);

    await tester.pumpWidget(
      _wrap(
        FleetTreeView(
          rootFolders: [folderA, folderB],
          hosts: const [],
          isSearching: true,
          onSessionTap: (_) {},
          onAddSession: (_) {},
        ),
        container2,
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Ackbar Manual Session'), findsOneWidget);

    await tester.pumpWidget(
      _wrap(
        FleetTreeView(
          rootFolders: [folderA, folderB],
          hosts: const [],
          isSearching: false,
          onSessionTap: (_) {},
          onAddSession: (_) {},
        ),
        container2,
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Ackbar Manual Session'), findsNothing);
  });
}
