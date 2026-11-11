import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/features/headless/presentation/headless_config_sheet.dart';

class FakeConfigHttpClient extends http.BaseClient {
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

void main() {
  Widget buildApp(Widget child, {ApiClient? api}) {
    return ProviderScope(
      overrides: [
        if (api != null) apiClientProvider.overrideWithValue(api),
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

      expect(find.text('gemini-4-argon'), findsOneWidget);
      expect(find.text('xhigh'), findsNothing);
      expect(find.text('PLAN'), findsOneWidget);
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
  });
}
