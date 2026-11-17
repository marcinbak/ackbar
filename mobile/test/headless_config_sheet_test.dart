import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/features/headless/presentation/headless_config_sheet.dart';

class FakeConfigHttpClient extends http.BaseClient {
  String? lastPatchBody;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final path = request.url.path;
    dynamic body;
    if (path.endsWith('/suggestions')) {
      body = [
        {'id': 'claude-3-7-sonnet', 'label': 'Claude 3.7 Sonnet'},
        {'id': 'claude-3-5-haiku', 'label': 'Claude 3.5 Haiku'},
      ];
    } else if (path.endsWith('/mcp-status')) {
      body = {
        'servers': [
          {
            'name': 'fetch',
            'transport': 'stdio',
            'scope': 'user',
            'status': 'configured',
            'auth_supported': false,
            'target': ''
          },
          {
            'name': 'jira',
            'transport': 'sse',
            'scope': 'workspace',
            'status': 'needs_auth',
            'auth_supported': true,
            'target': 'mcp:jira'
          },
        ],
        'plugins': [
          {
            'name': 'gsheets',
            'installed': true,
            'authenticated': false,
            'auth_supported': true,
            'target': 'plugin:gsheets'
          },
        ],
      };
    } else if (path.endsWith('/config')) {
      if (request is http.Request) {
        lastPatchBody = request.body;
      }
      body = {
        'id': 's-cfg-1',
        'agent': 'claude-code',
        'host': 'local',
        'native_id': 'n1',
        'cwd': '/work',
        'state': 1,
        'started_at': '2026-11-11T10:00:00Z',
        'last_event_at': '2026-11-11T10:00:00Z',
        'model': 'claude-3-7-sonnet',
        'resolved_model': 'claude-fable-5-1',
        'effort': 'high',
        'permission_mode': 'plan',
        'extra_dirs': ['/tmp/shared'],
      };
    } else {
      body = {'status': 'ok'};
    }
    final resp = http.Response(jsonEncode(body), 200,
        headers: {'content-type': 'application/json'});
    return http.StreamedResponse(Stream.value(resp.bodyBytes), resp.statusCode,
        headers: resp.headers);
  }
}

class _FakeHostsNotifier extends StateNotifier<List<HostRecord>>
    implements HostsNotifier {
  _FakeHostsNotifier(super.state);
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

class _GatewayFailingSuggestionsClient extends http.BaseClient {
  final List<Uri> requestedUris = [];
  String? lastPatchBody;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    requestedUris.add(request.url);
    final path = request.url.path;
    if (path.endsWith('/suggestions')) {
      final resp = http.Response('{"error":"suggestions timeout"}', 500);
      return http.StreamedResponse(Stream.value(resp.bodyBytes), 500);
    }
    if (path.endsWith('/config')) {
      if (request is http.Request) {
        lastPatchBody = request.body;
      }
      final resp = http.Response(
        jsonEncode({
          'id': 's-gw-1',
          'agent': 'antigravity',
          'host': 'federated-node',
          'native_id': 'n-gw',
          'cwd': '/work',
          'state': 3,
          'started_at': '2026-11-11T10:00:00Z',
          'last_event_at': '2026-11-11T10:00:00Z',
          'model': 'gemini-4-carbon-medium',
        }),
        200,
        headers: {'content-type': 'application/json'},
      );
      return http.StreamedResponse(Stream.value(resp.bodyBytes), 200,
          headers: resp.headers);
    }
    final resp = http.Response(
        jsonEncode({'servers': const [], 'plugins': const []}), 200,
        headers: {'content-type': 'application/json'});
    return http.StreamedResponse(Stream.value(resp.bodyBytes), 200,
        headers: resp.headers);
  }
}

void main() {
  Widget buildApp(Widget child, {ApiClient? api, List<HostRecord>? hosts}) {
    return ProviderScope(
      overrides: [
        if (api != null) apiClientProvider.overrideWithValue(api),
        if (hosts != null)
          hostsListProvider.overrideWith((_) => _FakeHostsNotifier(hosts)),
      ],
      child: MaterialApp(home: Scaffold(body: child)),
    );
  }

  group('HeadlessConfigPillBar Tests', () {
    testWidgets('renders effort pill for claude-code but hides for antigravity',
        (tester) async {
      final claude = Session(
        id: 's-cl',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n1',
        cwd: '/work',
        state: SessionState.working,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        model: 'sonnet-3-7',
        effort: 'xhigh',
        permissionMode: 'bypassPermissions',
        extraDirs: const ['/tmp'],
        contextPct: 42,
      );

      await tester.pumpWidget(buildApp(HeadlessConfigPillBar(session: claude)));
      await tester.pump();

      expect(find.byKey(const ValueKey('headless_config_pill_bar')),
          findsOneWidget);
      expect(find.text('sonnet-3-7'), findsOneWidget);
      expect(find.text('xhigh'), findsOneWidget);
      expect(find.text('AUTO'), findsOneWidget);
      expect(find.text('+1 dirs'), findsOneWidget);
      expect(find.text('42% ctx'), findsOneWidget);

      final agy = claude.copyWith(
        agent: 'antigravity',
        model: 'gemini-4-argon',
        effort: 'xhigh',
        permissionMode: 'plan',
      );

      await tester.pumpWidget(buildApp(HeadlessConfigPillBar(session: agy)));
      await tester.pump();

      expect(find.text('Gemini 4 Argon'), findsOneWidget);
      expect(find.text('xhigh'), findsNothing);
      expect(find.text('PLAN'), findsOneWidget);
    });

    testWidgets('renders formatted resolvedModel when model is empty',
        (tester) async {
      final sess = Session(
        id: 's-res-pill',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n1',
        cwd: '/work',
        state: SessionState.working,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        model: '',
        resolvedModel: 'claude-fable-5-1',
      );

      await tester.pumpWidget(buildApp(HeadlessConfigPillBar(session: sess)));
      await tester.pump();

      expect(find.byKey(const ValueKey('headless_pill_model')), findsOneWidget);
      expect(find.text('Claude Fable 5.1'), findsOneWidget);
    });
  });

  group('HeadlessConfigSheet Modal Tests', () {
    testWidgets(
        'renders model selector, effort section on claude, and mcp section',
        (tester) async {
      final api = ApiClient(client: FakeConfigHttpClient());
      final session = Session(
        id: 's-cfg-1',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n1',
        cwd: '/work',
        state: SessionState.working,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        model: 'claude-3-7-sonnet',
        effort: 'medium',
        permissionMode: 'bypassPermissions',
        contextPct: 65,
      );

      await tester.pumpWidget(
          buildApp(HeadlessConfigSheet(session: session), api: api));
      await tester.pumpAndSettle();

      expect(
          find.byKey(const ValueKey('headless_config_sheet')), findsOneWidget);
      expect(
          find.byKey(const ValueKey('config_model_selector')), findsOneWidget);
      expect(
          find.byKey(const ValueKey('config_effort_section')), findsOneWidget);
      expect(find.byKey(const ValueKey('config_mode_auto')), findsOneWidget);
      expect(find.byKey(const ValueKey('config_mode_plan')), findsOneWidget);
      expect(
          find.byKey(const ValueKey('config_extra_dir_input')), findsOneWidget);
      expect(find.byKey(const ValueKey('config_extra_dir_add_btn')),
          findsOneWidget);
      expect(find.byKey(const ValueKey('config_mcp_section')), findsOneWidget);

      // Verify MCP & Plugin rows
      expect(find.text('fetch'), findsOneWidget);
      expect(find.text('jira'), findsOneWidget);
      expect(find.byKey(const Key('mcp_sign_in_mcp:jira')), findsOneWidget);
      expect(
          find.byKey(const Key('mcp_sign_in_plugin:gsheets')), findsOneWidget);
    });

    testWidgets('strictly hides effort section on antigravity session',
        (tester) async {
      final api = ApiClient(client: FakeConfigHttpClient());
      final agySession = Session(
        id: 's-cfg-agy',
        agent: 'antigravity',
        host: 'local',
        nativeId: 'n2',
        cwd: '/work',
        state: SessionState.working,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        model: 'gemini-3.8-flash-high',
      );

      await tester.pumpWidget(
          buildApp(HeadlessConfigSheet(session: agySession), api: api));
      await tester.pumpAndSettle();

      expect(
          find.byKey(const ValueKey('headless_config_sheet')), findsOneWidget);
      expect(find.byKey(const ValueKey('config_effort_section')), findsNothing);
    });

    testWidgets(
        'shows Default (Claude Fable 5.1) and resetting override sends model: ""',
        (tester) async {
      final fakeClient = FakeConfigHttpClient();
      final api = ApiClient(client: fakeClient);
      final session = Session(
        id: 's-cfg-default',
        agent: 'claude-code',
        host: 'local',
        nativeId: 'n1',
        cwd: '/work',
        state: SessionState.working,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
        model: '',
        resolvedModel: 'claude-fable-5-1',
      );

      await tester.pumpWidget(
          buildApp(HeadlessConfigSheet(session: session), api: api));
      await tester.pumpAndSettle();

      expect(find.text('Default (Claude Fable 5.1)'), findsOneWidget);

      await tester.tap(find.byKey(const ValueKey('config_model_selector')));
      await tester.pumpAndSettle();

      final option = find.text('Claude 3.7 Sonnet').last;
      await tester.tap(option);
      await tester.pumpAndSettle();

      expect(fakeClient.lastPatchBody, isNotNull);
      var decoded =
          jsonDecode(fakeClient.lastPatchBody!) as Map<String, dynamic>;
      expect(decoded['model'], equals('claude-3-7-sonnet'));

      // Now reset back to default
      await tester.tap(find.byKey(const ValueKey('config_model_selector')));
      await tester.pumpAndSettle();

      final defaultOption = find.text('Default (Claude Fable 5.1)').last;
      expect(defaultOption, findsOneWidget);
      await tester.tap(defaultOption);
      await tester.pumpAndSettle();

      decoded = jsonDecode(fakeClient.lastPatchBody!) as Map<String, dynamic>;
      expect(decoded['model'], equals(''));
    });

    testWidgets(
        'uses SessionHostResolver gateway host instead of 127.0.0.1 and renders default models when suggestions fail',
        (tester) async {
      final gwClient = _GatewayFailingSuggestionsClient();
      final api = ApiClient(client: gwClient);
      final gatewayHost = HostRecord(
        name: 'gateway',
        url: 'https://relay.example.com',
        authToken: 'gw-token',
        online: true,
        createdAt: DateTime.utc(2026, 11, 1),
      );
      final agySession = Session(
        id: 's-gw-1',
        agent: 'antigravity',
        host: 'federated-node',
        nativeId: 'n-gw',
        cwd: '/work',
        state: SessionState.idle,
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        engineType: 'headless',
      );

      await tester.pumpWidget(buildApp(
        HeadlessConfigSheet(session: agySession),
        api: api,
        hosts: [gatewayHost],
      ));
      await tester.pumpAndSettle();

      expect(gwClient.requestedUris, isNotEmpty);
      expect(
        gwClient.requestedUris.every((u) => u.host == 'relay.example.com'),
        isTrue,
      );
      expect(
        gwClient.requestedUris.any((u) => u.host == '127.0.0.1'),
        isFalse,
      );

      // Even though /suggestions returned 500, built-in Antigravity defaults are selectable
      await tester.tap(find.byKey(const ValueKey('config_model_selector')));
      await tester.pumpAndSettle();

      final carbonOption = find.text('Gemini 4 Carbon (Medium)').last;
      expect(carbonOption, findsOneWidget);
      await tester.tap(carbonOption);
      await tester.pumpAndSettle();

      expect(gwClient.lastPatchBody, isNotNull);
      final decoded =
          jsonDecode(gwClient.lastPatchBody!) as Map<String, dynamic>;
      expect(decoded['model'], equals('gemini-4-carbon-medium'));
    });
  });
}
