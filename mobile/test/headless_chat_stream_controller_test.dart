import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/chat_stream_event.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/subagent.dart';
import 'package:ackbar_mobile/core/models/transcript.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/chat_stream_client.dart';
import 'package:ackbar_mobile/features/transcript/application/headless_chat_stream_controller.dart';

class _FakeApiClient extends ApiClient {
  TranscriptData? structuredResponse;
  PromptDispatchResult promptResult =
      PromptDispatchResult.success(status: 'running');
  int promptCalls = 0;
  String? lastClientTurnId;

  _FakeApiClient();

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

  @override
  Future<PromptDispatchResult> sendPromptDetailed(
    String hostUrl,
    String sessionID,
    String prompt, {
    int delaySeconds = 0,
    String? clientTurnId,
    String? authToken,
  }) async {
    promptCalls++;
    lastClientTurnId = clientTurnId;
    return promptResult;
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

HostRecord _testHost() {
  return HostRecord(
    name: 'local',
    url: 'http://127.0.0.1:7777',
    createdAt: DateTime.utc(2026, 11, 1),
  );
}

Session _testSession({String engine = 'headless'}) {
  return Session(
    id: 'claude-code:local:sess-1',
    agent: 'claude-code',
    host: 'local',
    nativeId: 'sess-1',
    cwd: '/workspace',
    state: SessionState.working,
    startedAt: DateTime.utc(2026, 11, 1),
    lastEventAt: DateTime.utc(2026, 11, 1),
    managed: true,
    engineType: engine,
  );
}

void main() {
  test('HeadlessChatStreamController accumulates deltas into streaming message',
      () async {
    final fakeApi = _FakeApiClient();
    final fakeStream = _FakeChatStreamClient();

    final controller = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: fakeApi,
      chatStreamClient: fakeStream,
    );
    addTearDown(() => controller.dispose());

    controller.init();
    await Future<void>.delayed(const Duration(milliseconds: 20));

    expect(controller.transcriptData?.messages ?? [], isEmpty);

    // Emit turn_start
    fakeStream.streamCtrl.add(ChatStreamEvent.fromJson({
      'session_id': 'claude-code:local:sess-1',
      'seq': 1,
      'turn_id': 'turn-1',
      'type': 'turn_start',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(controller.awaitingTurnCompletion, isTrue);

    // Emit text_delta
    fakeStream.streamCtrl.add(ChatStreamEvent.fromJson({
      'session_id': 'claude-code:local:sess-1',
      'seq': 2,
      'turn_id': 'turn-1',
      'type': 'text_delta',
      'text': 'Streaming text...',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final msgs = controller.transcriptData?.messages ?? [];
    expect(msgs, hasLength(1));
    expect(msgs.first.content, 'Streaming text...');
    expect(msgs.first.inFlight, isTrue);

    // Emit tool_start
    fakeStream.streamCtrl.add(ChatStreamEvent.fromJson({
      'session_id': 'claude-code:local:sess-1',
      'seq': 3,
      'turn_id': 'turn-1',
      'type': 'tool_start',
      'tool_name': 'grep',
      'tool_input': {'pattern': 'test'},
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(controller.transcriptData?.messages.first.toolCalls,
        contains('grep(test)'));

    // Emit done
    fakeStream.streamCtrl.add(ChatStreamEvent.fromJson({
      'session_id': 'claude-code:local:sess-1',
      'seq': 4,
      'turn_id': 'turn-1',
      'type': 'done',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(controller.awaitingTurnCompletion, isFalse);
    expect(controller.transcriptData?.messages.first.inFlight, isFalse);
  });

  test(
      'HeadlessChatStreamController submits prompt with generated client_turn_id',
      () async {
    final fakeApi = _FakeApiClient();
    final fakeStream = _FakeChatStreamClient();

    final controller = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: fakeApi,
      chatStreamClient: fakeStream,
    );
    addTearDown(() => controller.dispose());

    controller.init();
    await Future<void>.delayed(const Duration(milliseconds: 20));

    final res = await controller.submitPrompt('Fix the parser bug');
    expect(res.ok, isTrue);
    expect(fakeApi.promptCalls, 1);
    expect(fakeApi.lastClientTurnId, startsWith('ct_'));

    expect(controller.pendingUserMessage?.content, 'Fix the parser bug');
  });

  test('HeadlessChatStreamController retries once on transport failure',
      () async {
    final fakeApi = _FakeApiClient();
    fakeApi.promptResult = PromptDispatchResult.failure('Connection reset');
    final fakeStream = _FakeChatStreamClient();

    final controller = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: fakeApi,
      chatStreamClient: fakeStream,
    );
    addTearDown(() => controller.dispose());

    controller.init();
    await Future<void>.delayed(const Duration(milliseconds: 20));

    final res = await controller.submitPrompt('Hello');
    expect(res.ok, isFalse);
    expect(fakeApi.promptCalls, 2); // 1 initial + 1 retry
  });

  test(
      'HeadlessChatStreamController preserves earlier historical message turnId when submitting repeated prompt',
      () async {
    final fakeApi = _FakeApiClient();
    fakeApi.structuredResponse = TranscriptData(
      sessionId: 'claude-code:local:sess-1',
      nativeId: 'sess-1',
      agent: 'claude-code',
      title: 'Session 1',
      cwd: '/workspace',
      messages: [
        TranscriptMessage(
          role: 'user',
          content: 'continue',
          turnId: 'turn-historical-1',
          timestamp: DateTime.utc(2026, 11, 1),
        ),
        TranscriptMessage(
          role: 'assistant',
          content: 'Working on it',
          turnId: 'turn-historical-1',
          timestamp: DateTime.utc(2026, 11, 1),
        ),
      ],
      rawMarkdown: '',
    );
    final fakeStream = _FakeChatStreamClient();

    final controller = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: fakeApi,
      chatStreamClient: fakeStream,
    );
    addTearDown(() => controller.dispose());

    controller.init();
    await Future<void>.delayed(const Duration(milliseconds: 20));

    await controller.submitPrompt('continue');

    fakeStream.streamCtrl.add(ChatStreamEvent.fromJson({
      'session_id': 'claude-code:local:sess-1',
      'seq': 1,
      'turn_id': 'turn-new-2',
      'type': 'turn_start',
    }));
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final msgs = controller.transcriptData!.messages;
    expect(msgs.first.turnId, 'turn-historical-1');
    expect(msgs.first.content, 'continue');
    final userMsgs = msgs.where((m) => m.isUser).toList();
    expect(userMsgs, hasLength(2));
    expect(userMsgs[0].turnId, 'turn-historical-1');
    expect(userMsgs[1].turnId, 'turn-new-2');
  });

  test('HeadlessChatStreamController isolates state across session rotations',
      () async {
    final fakeApi = _FakeApiClient();
    final fakeStream = _FakeChatStreamClient();

    final controller = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: fakeApi,
      chatStreamClient: fakeStream,
    );
    addTearDown(() => controller.dispose());

    controller.init();
    await Future<void>.delayed(const Duration(milliseconds: 20));

    final sess2 = Session(
      id: 'claude-code:local:sess-2',
      agent: 'claude-code',
      host: 'local',
      nativeId: 'sess-2',
      cwd: '/workspace',
      state: SessionState.idle,
      startedAt: DateTime.utc(2026, 11, 1),
      lastEventAt: DateTime.utc(2026, 11, 1),
      managed: true,
      engineType: 'headless',
    );

    controller.rotateToSession(sess2, _testHost());
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(controller.session.id, 'claude-code:local:sess-2');
    expect(controller.transcriptData?.messages ?? [], isEmpty);
    expect(controller.awaitingTurnCompletion, isFalse);
  });

  test(
      'disposing controller does not close shared ApiClient client and default ApiClient creates owned ChatStreamClient',
      () async {
    final mockHttpClient =
        MockClient((req) async => http.Response('{"ok":true}', 200));
    final sharedApiClient = ApiClient(client: mockHttpClient);
    expect(sharedApiClient.hasInjectedClient, isTrue);

    final controller = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: sharedApiClient,
    );

    controller.init();
    controller.dispose();

    final res = await sharedApiClient.client
        .get(Uri.parse('http://127.0.0.1:7777/health'));
    expect(res.statusCode, 200);

    final defaultApiClient = ApiClient();
    expect(defaultApiClient.hasInjectedClient, isFalse);
    final defaultController = HeadlessChatStreamController(
      session: _testSession(),
      host: _testHost(),
      apiClient: defaultApiClient,
    );
    defaultController.dispose();
  });
}
