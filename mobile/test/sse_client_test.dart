import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';

class _ClosingClient extends http.BaseClient {
  final Future<http.StreamedResponse> Function(http.BaseRequest) _handler;
  bool wasClosed = false;

  _ClosingClient(this._handler);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) =>
      _handler(request);

  @override
  void close() {
    wasClosed = true;
    super.close();
  }
}

HostRecord _createHost(String name, {bool online = true}) {
  return HostRecord(
    name: name,
    url: 'http://127.0.0.1:7777',
    online: online,
    createdAt: DateTime.utc(2026, 11, 1),
  );
}

void main() {
  test('SSEClient parses single and multi-line data frames and comments',
      () async {
    final streamController = StreamController<List<int>>();
    final client = MockClient.streaming((req, _) async {
      return http.StreamedResponse(streamController.stream, 200);
    });

    final sse = SSEClient(
      client: client,
      inactivityTimeout: const Duration(seconds: 10),
    );
    addTearDown(() => sse.dispose());

    final host = _createHost('local');
    final received = <Session>[];
    final sub = sse.subscribeHost(host).listen(received.add);
    addTearDown(() => sub.cancel());

    final payload1 = {
      'id': 'sess-1',
      'agent': 'claude-code',
      'state': 'working',
    };
    final payload2 = {
      'id': 'sess-2',
      'agent': 'antigravity',
      'state': 'idle',
    };

    streamController.add(utf8.encode(': keepalive\n\n'));
    streamController.add(utf8.encode('data: ${jsonEncode(payload1)}\n\n'));
    streamController.add(utf8.encode('data: ${jsonEncode(payload2)}\n\n'));

    await Future<void>.delayed(const Duration(milliseconds: 50));

    expect(received, hasLength(2));
    expect(received[0].id, 'sess-1');
    expect(received[0].host, 'local');
    expect(received[1].id, 'sess-2');
    expect(received[1].host, 'local');
  });

  test('SSEClient does not close legacy shared client on cancel', () async {
    var clientClosed = false;
    final streamController = StreamController<List<int>>.broadcast();
    final client = MockClient.streaming((req, _) async {
      return http.StreamedResponse(streamController.stream, 200);
    });

    final sse = SSEClient(client: client);
    final sub = sse.subscribeHost(_createHost('local')).listen((_) {});

    await sub.cancel();
    expect(clientClosed, isFalse);
    sse.dispose();
  });

  test('SSEClient closes owned per-attempt client on cancel', () async {
    var attempts = 0;
    _ClosingClient? lastClient;

    http.Client factory() {
      attempts++;
      final c = _ClosingClient((req) async {
        return http.StreamedResponse(const Stream.empty(), 200);
      });
      lastClient = c;
      return c;
    }

    final sse = SSEClient(clientFactory: factory);
    final sub = sse.subscribeHost(_createHost('local')).listen((_) {});
    await Future<void>.delayed(const Duration(milliseconds: 10));

    await sub.cancel();
    expect(attempts, 1);
    expect(lastClient?.wasClosed, isTrue);
    sse.dispose();
  });

  test('SSEClient resets backoff attempt only after receiving valid SSE frame',
      () async {
    var attemptCount = 0;
    final sse = SSEClient(
      clientFactory: () {
        attemptCount++;
        return MockClient.streaming((req, _) async {
          if (attemptCount == 1) {
            // Immediate EOF without any frame
            return http.StreamedResponse(const Stream.empty(), 200);
          }
          final ctrl = StreamController<List<int>>();
          ctrl.add(utf8.encode(': keepalive\n\n'));
          return http.StreamedResponse(ctrl.stream, 200);
        });
      },
      backoffSchedule: const [Duration(milliseconds: 10)],
      random: Random(42),
    );
    addTearDown(() => sse.dispose());

    final sub = sse.subscribeHost(_createHost('local')).listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(attemptCount, greaterThanOrEqualTo(2));
  });

  test('SSEClient triggers onHostReconnected after reconnecting from failure',
      () async {
    var connectAttempts = 0;
    final reconnectedHosts = <String>[];

    final sse = SSEClient(
      clientFactory: () {
        connectAttempts++;
        if (connectAttempts == 1) {
          // Failure on first attempt
          return MockClient((_) async => http.Response('Error', 500));
        }
        final ctrl = StreamController<List<int>>();
        ctrl.add(utf8.encode(': keepalive\n\n'));
        return MockClient.streaming(
            (req, _) async => http.StreamedResponse(ctrl.stream, 200));
      },
      backoffSchedule: const [Duration(milliseconds: 10)],
      random: Random(1),
    );
    addTearDown(() => sse.dispose());

    final host = _createHost('remote-box');
    final sub = sse.subscribeMultipleHosts(
      [host],
      onHostReconnected: (h) => reconnectedHosts.add(h.name),
    ).listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 100));
    expect(reconnectedHosts, contains('remote-box'));
  });

  test('SSEClient subscribeMultipleHosts filters offline hosts when requested',
      () async {
    final sse = SSEClient(
      clientFactory: () =>
          MockClient((_) async => http.Response('unexpected', 500)),
    );
    addTearDown(() => sse.dispose());

    final offlineHost = _createHost('offline-box', online: false);
    final sub = sse.subscribeMultipleHosts([offlineHost],
        includeOffline: false).listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 20));
  });

  test('SSEClient dispose cancels all active host streams', () async {
    final sse = SSEClient(
      clientFactory: () => MockClient.streaming((req, _) async {
        return http.StreamedResponse(const Stream.empty(), 200);
      }),
      backoffSchedule: const [Duration(milliseconds: 50)],
    );

    sse.subscribeHost(_createHost('box-1')).listen((_) {});
    sse.subscribeHost(_createHost('box-2')).listen((_) {});

    await Future<void>.delayed(const Duration(milliseconds: 10));
    sse.dispose();
  });

  test('SSEClient inactivity watchdog aborts hung 200 stream and reconnects',
      () async {
    int attempts = 0;
    final unclosedStreamCtrl1 = StreamController<List<int>>();
    final streamCtrl2 = StreamController<List<int>>();

    final sse = SSEClient(
      clientFactory: () {
        attempts++;
        if (attempts == 1) {
          return MockClient.streaming((req, _) async {
            return http.StreamedResponse(unclosedStreamCtrl1.stream, 200);
          });
        }
        return MockClient.streaming((req, _) async {
          streamCtrl2.add(utf8.encode(': keepalive\n\n'));
          return http.StreamedResponse(streamCtrl2.stream, 200);
        });
      },
      inactivityTimeout: const Duration(milliseconds: 50),
      backoffSchedule: const [Duration(milliseconds: 10)],
      random: Random(1),
    );
    addTearDown(() {
      unclosedStreamCtrl1.close();
      streamCtrl2.close();
      sse.dispose();
    });

    final sub = sse.subscribeHost(_createHost('local')).listen((_) {});
    addTearDown(() => sub.cancel());

    await Future<void>.delayed(const Duration(milliseconds: 130));
    expect(attempts, greaterThanOrEqualTo(2));
  });
}
