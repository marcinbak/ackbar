import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/chat_stream_event.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
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
    expect(controller.session.id, 'claude-code:local:sess-2');
    expect(controller.transcriptData?.messages ?? [], isEmpty);
    expect(controller.awaitingTurnCompletion, isFalse);
  });
}
