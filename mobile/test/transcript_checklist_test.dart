import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:ackbar_mobile/core/models/chat_stream_event.dart';
import 'package:ackbar_mobile/core/models/checklist_item.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/subagent.dart';
import 'package:ackbar_mobile/core/models/transcript.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/chat_stream_client.dart';
import 'package:ackbar_mobile/core/theme/app_colors.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/transcript/application/headless_chat_stream_controller.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_checklist_card.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_turn_bubble.dart';

class _FakeApiClient extends ApiClient {
  TranscriptData? structuredResponse;

  @override
  Future<TranscriptData?> getStructuredTranscript(
    String hostUrl,
    String sessionID, {
    String? authToken,
  }) async {
    return structuredResponse;
  }

  @override
  Future<String?> getTranscriptOrNull(
    String hostUrl,
    String sessionID, {
    String format = 'markdown',
    String? authToken,
  }) async {
    return null;
  }

  @override
  Future<List<SubagentInfo>> getSubagents(
    String hostUrl,
    String sessionID, {
    String? authToken,
  }) async {
    return const [];
  }
}

class _FakeChatStreamClient extends ChatStreamClient {
  final StreamController<ChatStreamEvent> streamCtrl =
      StreamController<ChatStreamEvent>.broadcast();

  @override
  Stream<ChatStreamEvent> connect(
    HostRecord host,
    String sessionId, {
    String initialTurnId = '',
    int initialAfterSeq = 0,
  }) {
    return streamCtrl.stream;
  }
}

Widget _wrapWidget(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      theme: AppTheme.darkTheme,
      home: Scaffold(
        body: SingleChildScrollView(child: child),
      ),
    ),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  final testSession = Session(
    id: 'antigravity:local:sess-cl-1',
    agent: 'antigravity',
    host: 'local',
    nativeId: 'sess-cl-1',
    cwd: '/workspace',
    state: SessionState.working,
    startedAt: DateTime.utc(2026, 11, 25),
    lastEventAt: DateTime.utc(2026, 11, 25),
    managed: true,
    engineType: 'headless',
  );

  final testHost = HostRecord(
    name: 'local',
    url: 'http://127.0.0.1:7777',
    createdAt: DateTime.utc(2026, 11, 25),
  );

  test(
      'ChecklistItem and TranscriptData parse checklist-only turns and in-flight meta',
      () {
    final data = TranscriptData.fromJson({
      'session_id': 'antigravity:local:sess-cl-1',
      'native_id': 'sess-cl-1',
      'agent': 'antigravity',
      'messages': [
        {
          'role': 'user',
          'content': 'Track progress',
          'timestamp': '2026-11-25T08:00:00Z',
        },
        {
          'role': 'assistant',
          'content': '',
          'checklist': [
            {'id': '1', 'title': 'Schema', 'status': 'done'},
            {
              'id': '2',
              'title': 'UI',
              'active_form': 'Building UI',
              'status': 'in_progress',
            },
          ],
          'timestamp': '2026-11-25T08:00:01Z',
        },
      ],
      'in_flight_turn': {
        'turn_id': 'turn_1',
        'last_seq': 4,
        'active': true,
        'checklist': [
          {'id': '1', 'title': 'Schema', 'status': 'done'},
        ],
      },
    });

    expect(data.messages.length, 2);
    final cl = data.messages[1].checklist;
    expect(cl.length, 2);
    expect(cl[0].status, ChecklistStatus.done);
    expect(cl[0].displayLabel, 'Schema');
    expect(cl[1].status, ChecklistStatus.inProgress);
    expect(cl[1].displayLabel, 'Building UI');
    expect(data.inFlightTurn?.checklist.length, 1);
  });

  test(
      'HeadlessChatStreamController updates assistant turn checklist in-place on sequential tool_start events',
      () async {
    final api = _FakeApiClient()
      ..structuredResponse = TranscriptData(
        sessionId: testSession.id,
        nativeId: testSession.nativeId,
        agent: testSession.agent,
        title: 'Checklist test',
        cwd: testSession.cwd,
        messages: const [],
        rawMarkdown: '',
      );
    final stream = _FakeChatStreamClient();
    final ctrl = HeadlessChatStreamController(
      session: testSession,
      host: testHost,
      apiClient: api,
      chatStreamClient: stream,
    );
    ctrl.init();
    await Future<void>.delayed(const Duration(milliseconds: 20));

    stream.streamCtrl.add(ChatStreamEvent.fromJson({
      'type': 'turn_start',
      'session_id': testSession.id,
      'turn_id': 'turn-1',
      'seq': 1,
      'text': 'Build it',
      'timestamp': '2026-11-25T08:00:00Z',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    stream.streamCtrl.add(ChatStreamEvent.fromJson({
      'type': 'tool_start',
      'session_id': testSession.id,
      'turn_id': 'turn-1',
      'seq': 2,
      'tool_name': 'progress',
      'checklist': [
        {
          'id': '1',
          'title': 'Step 1',
          'active_form': 'Running Step 1',
          'status': 'in_progress',
        },
        {'id': '2', 'title': 'Step 2', 'status': 'pending'},
      ],
      'timestamp': '2026-11-25T08:00:01Z',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    stream.streamCtrl.add(ChatStreamEvent.fromJson({
      'type': 'tool_start',
      'session_id': testSession.id,
      'turn_id': 'turn-1',
      'seq': 3,
      'tool_name': 'progress',
      'checklist': [
        {'id': '1', 'title': 'Step 1', 'status': 'done'},
        {'id': '2', 'title': 'Step 2', 'status': 'done'},
      ],
      'timestamp': '2026-11-25T08:00:02Z',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final assistants =
        ctrl.transcriptData!.messages.where((m) => m.isAssistant).toList();
    expect(assistants.length, 1);
    expect(assistants.first.checklist.length, 2);
    expect(assistants.first.checklist[0].status, ChecklistStatus.done);
    expect(assistants.first.checklist[1].status, ChecklistStatus.done);

    ctrl.dispose();
    await stream.streamCtrl.close();
  });

  testWidgets(
      'TranscriptTurnBubble and TranscriptChecklistCard render all 5 statuses, active_form, and stable keys',
      (tester) async {
    final msg = TranscriptMessage(
      role: 'assistant',
      content: 'Checklist-backed reply',
      checklist: const [
        ChecklistItem(
            id: '1', title: 'Read files', status: ChecklistStatus.done),
        ChecklistItem(
          id: '2',
          title: 'Edit code',
          activeForm: 'Editing code now',
          status: ChecklistStatus.inProgress,
        ),
        ChecklistItem(
            id: '3', title: 'Run tests', status: ChecklistStatus.pending),
        ChecklistItem(
            id: '4', title: 'Optional check', status: ChecklistStatus.skipped),
        ChecklistItem(
            id: '5', title: 'Lint failed', status: ChecklistStatus.failed),
      ],
      timestamp: DateTime.utc(2026, 11, 25, 8, 0, 0),
    );

    await tester.pumpWidget(
      _wrapWidget(
        TranscriptTurnBubble(
          message: msg,
          index: 0,
          session: testSession,
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.byKey(const ValueKey('transcript_checklist_card')),
        findsOneWidget);
    expect(
        find.byKey(const ValueKey('checklist_progress_bar')), findsOneWidget);
    for (var i = 0; i < 5; i++) {
      expect(find.byKey(ValueKey('checklist_item_$i')), findsOneWidget);
    }

    expect(find.text('Task Progress (1/5)'), findsOneWidget);
    expect(find.text('20%'), findsOneWidget);
    expect(find.text('✓'), findsOneWidget);
    expect(find.text('▶'), findsOneWidget);
    expect(find.text('○'), findsOneWidget);
    expect(find.text('–'), findsOneWidget);
    expect(find.text('✕'), findsOneWidget);

    expect(find.text('Read files'), findsOneWidget);
    expect(find.text('Editing code now'), findsOneWidget);
    expect(find.text('Edit code'), findsNothing);
    expect(find.text('Run tests'), findsOneWidget);
    expect(find.text('Optional check'), findsOneWidget);
    expect(find.text('Lint failed'), findsOneWidget);

    final progressBar = tester.widget<LinearProgressIndicator>(
      find.byKey(const ValueKey('checklist_progress_bar')),
    );
    expect(progressBar.value, closeTo(0.2, 0.001));

    final doneText = tester.widget<Text>(find.text('Read files'));
    expect(doneText.style?.decoration, TextDecoration.lineThrough);

    final failedText = tester.widget<Text>(find.text('Lint failed'));
    expect(failedText.style?.color, AppColors.statusCoral);

    await tester
        .pumpWidget(_wrapWidget(const TranscriptChecklistCard(items: [])));
    expect(
        find.byKey(const ValueKey('transcript_checklist_card')), findsNothing);
  });
}
