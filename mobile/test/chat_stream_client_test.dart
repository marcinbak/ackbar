import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/chat_stream_event.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/network/chat_stream_client.dart';

HostRecord _createHost({String token = ''}) {
  return HostRecord(
    name: 'local',
    url: 'http://127.0.0.1:7777',
    authToken: token,
    createdAt: DateTime.utc(2026, 11, 1),
  );
}

void main() {
  test('ChatStreamClient sends query parameters and auth headers', () async {
    Uri? capturedUri;
    Map<String, String>? capturedHeaders;

    final streamCtrl = StreamController<List<int>>();
    final client = MockClient.streaming((req, _) async {
      capturedUri = req.url;
      capturedHeaders = req.headers;
      return http.StreamedResponse(streamCtrl.stream, 200);
    });

    final streamClient = ChatStreamClient(
      client: client,
      readTimeout: const Duration(seconds: 10),
    );
    addTearDown(() => streamClient.dispose());

    final sub = streamClient
        .connect(
          _createHost(token: 'secret-token'),
          'claude-code:local:sess-1',
          initialTurnId: 'turn-99',
          initialAfterSeq: 5,
        )
        .listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 30));

    expect(capturedUri?.path, '/v1/sessions/chat/stream');
    expect(
        capturedUri?.queryParameters['session_id'], 'claude-code:local:sess-1');
    expect(capturedUri?.queryParameters['id'], 'claude-code:local:sess-1');
    expect(capturedUri?.queryParameters['turn_id'], 'turn-99');
    expect(capturedUri?.queryParameters['after_seq'], '5');
    expect(capturedHeaders?['authorization'], 'Bearer secret-token');
    expect(capturedHeaders?['last-event-id'], 'turn-99:5');
    expect(capturedUri?.queryParameters.containsKey('token'), isFalse);
  });

  test('ChatStreamClient parses event frames and handles keepalives', () async {
    final streamCtrl = StreamController<List<int>>();
    final client = MockClient.streaming((req, _) async {
      return http.StreamedResponse(streamCtrl.stream, 200);
    });

    final streamClient = ChatStreamClient(
      client: client,
      readTimeout: const Duration(seconds: 5),
    );
    addTearDown(() => streamClient.dispose());

    final events = <ChatStreamEvent>[];
    final sub =
        streamClient.connect(_createHost(), 'sess-1').listen(events.add);
    addTearDown(() => sub.cancel());

    streamCtrl.add(utf8.encode(': keepalive\n\n'));
    streamCtrl.add(utf8.encode('id: turn-1:1\n'
        'event: turn_start\n'
        'data: {"turn_id":"turn-1","seq":1,"type":"turn_start"}\n\n'));
    streamCtrl.add(utf8.encode('id: turn-1:2\n'
        'event: text_delta\n'
        'data: {"turn_id":"turn-1","seq":2,"type":"text_delta","text":"Hello"}\n\n'));

    await Future<void>.delayed(const Duration(milliseconds: 40));

    expect(events, hasLength(2));
    expect(events[0].type, ChatStreamEventType.turnStart);
    expect(events[0].turnId, 'turn-1');
    expect(events[0].seq, 1);
    expect(events[1].type, ChatStreamEventType.textDelta);
    expect(events[1].text, 'Hello');
    expect(events[1].seq, 2);
  });

  test('ChatStreamClient deduplicates seq <= lastSeq within same turnId',
      () async {
    final streamCtrl = StreamController<List<int>>();
    final client = MockClient.streaming((req, _) async {
      return http.StreamedResponse(streamCtrl.stream, 200);
    });

    final streamClient = ChatStreamClient(
      client: client,
      readTimeout: const Duration(seconds: 5),
    );
    addTearDown(() => streamClient.dispose());

    final events = <ChatStreamEvent>[];
    final sub = streamClient
        .connect(
          _createHost(),
          'sess-1',
          initialTurnId: 'turn-1',
          initialAfterSeq: 2,
        )
        .listen(events.add);
    addTearDown(() => sub.cancel());

    // Duplicate seq 2 (should be skipped)
    streamCtrl.add(utf8.encode('id: turn-1:2\n'
        'event: text_delta\n'
        'data: {"turn_id":"turn-1","seq":2,"type":"text_delta","text":"Duplicate"}\n\n'));

    // New seq 3 (should be emitted)
    streamCtrl.add(utf8.encode('id: turn-1:3\n'
        'event: text_delta\n'
        'data: {"turn_id":"turn-1","seq":3,"type":"text_delta","text":"Fresh"}\n\n'));

    await Future<void>.delayed(const Duration(milliseconds: 40));

    expect(events, hasLength(1));
    expect(events[0].seq, 3);
    expect(events[0].text, 'Fresh');
  });

  test('ChatStreamClient resets lastSeq when turnId transitions', () async {
    final streamCtrl = StreamController<List<int>>();
    final client = MockClient.streaming((req, _) async {
      return http.StreamedResponse(streamCtrl.stream, 200);
    });

    final streamClient = ChatStreamClient(
      client: client,
      readTimeout: const Duration(seconds: 5),
    );
    addTearDown(() => streamClient.dispose());

    final events = <ChatStreamEvent>[];
    final sub =
        streamClient.connect(_createHost(), 'sess-1').listen(events.add);
    addTearDown(() => sub.cancel());

    streamCtrl.add(utf8.encode('id: turn-1:5\n'
        'event: done\n'
        'data: {"turn_id":"turn-1","seq":5,"type":"done"}\n\n'));

    streamCtrl.add(utf8.encode('id: turn-2:1\n'
        'event: turn_start\n'
        'data: {"turn_id":"turn-2","seq":1,"type":"turn_start"}\n\n'));

    await Future<void>.delayed(const Duration(milliseconds: 40));

    expect(events, hasLength(2));
    expect(events[0].turnId, 'turn-1');
    expect(events[0].seq, 5);
    expect(events[1].turnId, 'turn-2');
    expect(events[1].seq, 1);
  });

  test('ChatStreamClient reconnects with backoff and resets on valid frame',
      () async {
    var attempts = 0;
    final streamClient = ChatStreamClient(
      clientFactory: () {
        attempts++;
        if (attempts == 1) {
          return MockClient((_) async => http.Response('Error', 500));
        }
        final ctrl = StreamController<List<int>>();
        ctrl.add(utf8.encode(': keepalive\n\n'));
        return MockClient.streaming(
            (req, _) async => http.StreamedResponse(ctrl.stream, 200));
      },
      backoffSchedule: const [Duration(milliseconds: 10)],
      random: Random(7),
    );
    addTearDown(() => streamClient.dispose());

    final sub = streamClient.connect(_createHost(), 'sess-1').listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(attempts, greaterThanOrEqualTo(2));
  });

  test('ChatStreamClient cleanly cancels active attempt and timers', () async {
    final streamCtrl = StreamController<List<int>>.broadcast();
    final client = MockClient.streaming((req, _) async {
      return http.StreamedResponse(streamCtrl.stream, 200);
    });

    final streamClient = ChatStreamClient(client: client);
    final sub = streamClient.connect(_createHost(), 'sess-1').listen((_) {});

    await Future<void>.delayed(const Duration(milliseconds: 10));
    await sub.cancel();
    streamClient.dispose();
  });

  test(
      'ChatStreamClient watchdog aborts hung 200 stream and reconnects with updated turn_id and after_seq',
      () async {
    int attempts = 0;
    final capturedUrls = <Uri>[];
    final unclosedStreamCtrl1 = StreamController<List<int>>();
    final streamCtrl2 = StreamController<List<int>>();

    final streamClient = ChatStreamClient(
      clientFactory: () {
        attempts++;
        if (attempts == 1) {
          return MockClient.streaming((req, _) async {
            capturedUrls.add(req.url);
            unclosedStreamCtrl1.add(utf8.encode('id: turn-watchdog:10\n'
                'event: text_delta\n'
                'data: {"turn_id":"turn-watchdog","seq":10,"type":"text_delta","text":"hello"}\n\n'));
            return http.StreamedResponse(unclosedStreamCtrl1.stream, 200);
          });
        }
        return MockClient.streaming((req, _) async {
          capturedUrls.add(req.url);
          streamCtrl2.add(utf8.encode(': keepalive\n\n'));
          return http.StreamedResponse(streamCtrl2.stream, 200);
        });
      },
      readTimeout: const Duration(milliseconds: 50),
      backoffSchedule: const [Duration(milliseconds: 10)],
      random: Random(1),
    );
    addTearDown(() {
      unclosedStreamCtrl1.close();
      streamCtrl2.close();
      streamClient.dispose();
    });

    final sub =
        streamClient.connect(_createHost(), 'sess-watchdog').listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 130));

    expect(attempts, greaterThanOrEqualTo(2));
    expect(capturedUrls.length, greaterThanOrEqualTo(2));
    expect(capturedUrls[1].queryParameters['turn_id'], 'turn-watchdog');
    expect(capturedUrls[1].queryParameters['after_seq'], '10');
  });
}
