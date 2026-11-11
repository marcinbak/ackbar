import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:ackbar_mobile/core/models/headless_models.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/features/transcript/presentation/headless_autocomplete_strip.dart';
import 'package:ackbar_mobile/features/transcript/presentation/headless_command_cards.dart';
import 'package:ackbar_mobile/features/transcript/presentation/headless_command_dispatcher.dart';

class FakeCommandHttpClient extends http.BaseClient {
  http.Response Function(http.BaseRequest request)? handler;
  FakeCommandHttpClient([this.handler]);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final response = handler != null
        ? handler!(request)
        : http.Response(jsonEncode({'status': 'ok'}), 200,
            headers: {'content-type': 'application/json'});
    return http.StreamedResponse(
        Stream.value(response.bodyBytes), response.statusCode,
        headers: response.headers);
  }
}

void main() {
  group('detectComposerTrigger Tests', () {
    test('detects shell mode with leading exclamation mark', () {
      final t1 = detectComposerTrigger('! git status', 12);
      expect(t1?.kind, ComposerTriggerKind.shell);
      expect(t1?.query, 'git status');

      final t2 = detectComposerTrigger('!', 1);
      expect(t2?.kind, ComposerTriggerKind.shell);
      expect(t2?.query, '');
    });

    test('detects slash command at start of text', () {
      final t1 = detectComposerTrigger('/con', 4);
      expect(t1?.kind, ComposerTriggerKind.command);
      expect(t1?.query, 'con');

      final t2 = detectComposerTrigger('/config please', 4);
      expect(t2?.kind, ComposerTriggerKind.command);
      expect(t2?.query, 'config');

      final t3 = detectComposerTrigger('/config please', 10);
      expect(t3, isNull);
    });

    test('detects @ file mention token', () {
      final t1 = detectComposerTrigger('@main.dart', 10);
      expect(t1?.kind, ComposerTriggerKind.file);
      expect(t1?.query, 'main.dart');

      final t2 = detectComposerTrigger('Check @lib/api', 14);
      expect(t2?.kind, ComposerTriggerKind.file);
      expect(t2?.query, 'lib/api');

      final t3 = detectComposerTrigger('email@example.com', 10);
      expect(t3, isNull);
    });

    test('returns null for standard messages', () {
      expect(detectComposerTrigger('Hello world', 5), isNull);
      expect(detectComposerTrigger('', 0), isNull);
    });
  });

  group('HeadlessCommandDispatcher Tests', () {
    final host = HostRecord(
        name: 'local', url: 'http://127.0.0.1:7777', createdAt: DateTime.now());
    final session = Session(
      id: 's-cmd-1',
      agent: 'claude-code',
      host: 'local',
      nativeId: 'n1',
      cwd: '/work',
      state: SessionState.working,
      startedAt: DateTime.now(),
      lastEventAt: DateTime.now(),
      engineType: 'headless',
    );

    test('intercepts ! and executes shell', () async {
      final client = FakeCommandHttpClient((req) {
        return http.Response(
          jsonEncode({
            'command': 'git status',
            'cwd': '/work',
            'stdout': 'On branch main',
            'stderr': '',
            'exit_code': 0,
            'duration_ms': 10,
            'timed_out': false,
            'truncated': false,
          }),
          200,
        );
      });

      final dispatcher = HeadlessCommandDispatcher(
        apiClient: ApiClient(client: client),
        host: host,
      );

      final res = await dispatcher.dispatch(session, '! git status');
      expect(res.intercepted, isTrue);
      expect(res.card?.kind, 'shell');
      expect(res.card?.shell?.stdout, 'On branch main');
    });

    test('/effort on antigravity returns info card without network call',
        () async {
      var callMade = false;
      final client = FakeCommandHttpClient((req) {
        callMade = true;
        return http.Response(jsonEncode({}), 200);
      });

      final agySession = session.copyWith(agent: 'antigravity');
      final dispatcher = HeadlessCommandDispatcher(
        apiClient: ApiClient(client: client),
        host: host,
      );

      final res = await dispatcher.dispatch(agySession, '/effort high');
      expect(res.intercepted, isTrue);
      expect(callMade, isFalse);
      expect(res.card?.statusBody, contains('Antigravity model IDs'));
    });

    test('/commit passes through to normal prompt dispatch', () async {
      final dispatcher = HeadlessCommandDispatcher(
        apiClient: ApiClient(client: FakeCommandHttpClient()),
        host: host,
      );

      final res =
          await dispatcher.dispatch(session, '/commit feat: new feature');
      expect(res.intercepted, isFalse);
    });
  });

  group('HeadlessCommandCardView Tests', () {
    testWidgets('Shell card renders stdout and triggers attach to prompt',
        (tester) async {
      String? attachedText;
      final card = HeadlessEphemeralCardEntry(
        id: 'shell-1',
        kind: 'shell',
        createdAt: DateTime.now(),
        shell: const ShellExecResult(
          command: 'ls',
          cwd: '/work',
          stdout: 'file1.dart\nfile2.dart',
          stderr: '',
          exitCode: 0,
          durationMs: 8,
          timedOut: false,
          truncated: false,
        ),
      );

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: HeadlessCommandCardView(
              entry: card,
              onDismiss: () {},
              onAttachToPrompt: (t) => attachedText = t,
            ),
          ),
        ),
      );
      await tester.pump();

      expect(find.text('\$ ls'), findsOneWidget);
      expect(find.text('exit 0'), findsOneWidget);
      expect(find.text('file1.dart\nfile2.dart'), findsOneWidget);

      await tester
          .tap(find.byKey(const ValueKey('headless_attach_to_prompt_shell-1')));
      await tester.pump();

      expect(attachedText,
          contains('```shell\n\$ ls\nfile1.dart\nfile2.dart\n```'));
    });

    testWidgets('Rewind card renders interactive turn buttons', (tester) async {
      int? selectedTurn;
      final card = HeadlessEphemeralCardEntry(
        id: 'rewind-1',
        kind: 'rewind',
        createdAt: DateTime.now(),
        rewind: RewindPreviewResult(
          sessionId: 's-1',
          agent: 'claude-code',
          totalTurns: 2,
          turns: [
            RewindTurnItem(
                turn: 1, timestamp: DateTime.now(), preview: 'Initial setup'),
            RewindTurnItem(
                turn: 2, timestamp: DateTime.now(), preview: 'Add models'),
          ],
        ),
      );

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: HeadlessCommandCardView(
              entry: card,
              onDismiss: () {},
              onRewindToTurn: (t) => selectedTurn = t,
            ),
          ),
        ),
      );
      await tester.pump();

      expect(
          find.byKey(const ValueKey('headless_rewind_turn_1')), findsOneWidget);
      expect(
          find.byKey(const ValueKey('headless_rewind_turn_2')), findsOneWidget);

      await tester.tap(find.byKey(const ValueKey('headless_rewind_turn_2')));
      await tester.pump();

      expect(selectedTurn, 2);
    });
  });
}
