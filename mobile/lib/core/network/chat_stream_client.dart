import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:http/http.dart' as http;
import '../models/chat_stream_event.dart';
import '../models/host.dart';

class ChatStreamClient {
  final http.Client? _sharedClient;
  final http.Client Function()? _clientFactory;
  final bool _ownsPerAttemptClient;
  final Duration readTimeout;
  final List<Duration> backoffSchedule;
  final Random _random;
  final Set<void Function()> _activeCancels = {};

  ChatStreamClient({
    http.Client? client,
    http.Client Function()? clientFactory,
    this.readTimeout = const Duration(seconds: 40),
    this.backoffSchedule = const [
      Duration(seconds: 1),
      Duration(seconds: 2),
      Duration(seconds: 5),
      Duration(seconds: 10),
    ],
    Random? random,
  })  : _sharedClient = client,
        _clientFactory = clientFactory,
        _ownsPerAttemptClient = clientFactory != null || client == null,
        _random = random ?? Random();

  http.Client Function() get _effectiveClientFactory =>
      _clientFactory ?? http.Client.new;

  Stream<ChatStreamEvent> connect(
    HostRecord host,
    String sessionId, {
    String initialTurnId = '',
    int initialAfterSeq = 0,
  }) {
    late final StreamController<ChatStreamEvent> controller;
    bool cancelled = false;
    int attempt = 0;

    String currentTurnId = initialTurnId;
    int lastSeq = initialAfterSeq;

    http.Client? currentClient;
    StreamSubscription<String>? currentStreamSub;
    Timer? watchdogTimer;
    Timer? backoffTimer;
    Completer<void>? sleepCompleter;

    void cancelStream() {
      if (cancelled) return;
      cancelled = true;
      _activeCancels.remove(cancelStream);
      watchdogTimer?.cancel();
      watchdogTimer = null;
      backoffTimer?.cancel();
      backoffTimer = null;
      if (sleepCompleter != null && !sleepCompleter!.isCompleted) {
        sleepCompleter!.complete();
        sleepCompleter = null;
      }
      currentStreamSub?.cancel();
      currentStreamSub = null;
      if (_ownsPerAttemptClient) {
        currentClient?.close();
        currentClient = null;
      }
    }

    controller = StreamController<ChatStreamEvent>.broadcast(
      onListen: () {
        _activeCancels.add(cancelStream);
        () async {
          var cleanUrl = host.url.trim();
          if (!cleanUrl.startsWith('http://') &&
              !cleanUrl.startsWith('https://')) {
            cleanUrl = 'http://$cleanUrl';
          }
          if (cleanUrl.endsWith('/')) {
            cleanUrl = cleanUrl.substring(0, cleanUrl.length - 1);
          }

          while (!cancelled && !controller.isClosed) {
            http.Client? attemptClient;
            try {
              if (_ownsPerAttemptClient) {
                attemptClient = _effectiveClientFactory();
                currentClient = attemptClient;
              } else {
                attemptClient = _sharedClient!;
                currentClient = attemptClient;
              }

              final queryParams = <String, String>{
                'session_id': sessionId,
                'id': sessionId,
              };
              if (currentTurnId.isNotEmpty) {
                queryParams['turn_id'] = currentTurnId;
              }
              if (lastSeq > 0 || currentTurnId.isNotEmpty) {
                queryParams['after_seq'] = lastSeq.toString();
              }
              if (host.authToken.isNotEmpty) {
                queryParams['token'] = host.authToken;
              }

              final uri = Uri.parse('$cleanUrl/v1/sessions/chat/stream')
                  .replace(queryParameters: queryParams);

              final request = http.Request('GET', uri)
                ..headers['Accept'] = 'text/event-stream'
                ..headers['Cache-Control'] = 'no-cache';
              if (host.authToken.isNotEmpty) {
                request.headers['Authorization'] = 'Bearer ${host.authToken}';
                request.headers['X-Ackbar-Token'] = host.authToken;
              }
              if (lastSeq > 0 || currentTurnId.isNotEmpty) {
                request.headers['Last-Event-ID'] = currentTurnId.isNotEmpty
                    ? '$currentTurnId:$lastSeq'
                    : '$lastSeq';
              }

              final streamedResponse = await attemptClient.send(request);
              if (cancelled || controller.isClosed) break;

              if (streamedResponse.statusCode != 200) {
                throw http.ClientException(
                  'HTTP ${streamedResponse.statusCode}',
                );
              }

              bool receivedValidFrame = false;

              void resetWatchdog() {
                watchdogTimer?.cancel();
                if (cancelled || controller.isClosed) return;
                watchdogTimer = Timer(readTimeout, () {
                  if (_ownsPerAttemptClient) {
                    attemptClient?.close();
                  }
                  currentStreamSub?.cancel();
                });
              }

              resetWatchdog();

              String pendingEventId = '';
              final List<String> dataBuffer = [];

              void flushData() {
                if (dataBuffer.isEmpty) return;
                final raw = dataBuffer.join('\n').trim();
                dataBuffer.clear();
                final eventId = pendingEventId;
                pendingEventId = '';
                if (raw.isEmpty) return;

                try {
                  final Map<String, dynamic> json = jsonDecode(raw);
                  final evt = ChatStreamEvent.fromJson(
                    json,
                    eventId: eventId.isNotEmpty ? eventId : null,
                  );

                  if (evt.turnId.isNotEmpty) {
                    if (evt.turnId != currentTurnId) {
                      currentTurnId = evt.turnId;
                      lastSeq = 0;
                    }
                  }

                  if (evt.seq > 0) {
                    if (evt.seq <= lastSeq) {
                      return;
                    }
                    lastSeq = evt.seq;
                  }

                  if (!cancelled && !controller.isClosed) {
                    controller.add(evt);
                  }
                } catch (_) {}
              }

              final streamCompleter = Completer<void>();
              currentStreamSub = streamedResponse.stream
                  .transform(utf8.decoder)
                  .transform(const LineSplitter())
                  .listen(
                (line) {
                  resetWatchdog();
                  final trimmed = line.trimRight();
                  if (trimmed.startsWith(':')) {
                    if (!receivedValidFrame) {
                      receivedValidFrame = true;
                      attempt = 0;
                    }
                  } else if (trimmed.startsWith('id:')) {
                    if (!receivedValidFrame) {
                      receivedValidFrame = true;
                      attempt = 0;
                    }
                    pendingEventId = trimmed.substring(3).trim();
                  } else if (trimmed.startsWith('data:')) {
                    if (!receivedValidFrame) {
                      receivedValidFrame = true;
                      attempt = 0;
                    }
                    dataBuffer.add(trimmed.substring(5).trimLeft());
                  } else if (trimmed.isEmpty) {
                    flushData();
                  }
                },
                onError: (err) {
                  if (!streamCompleter.isCompleted) {
                    streamCompleter.completeError(err);
                  }
                },
                onDone: () {
                  flushData();
                  if (!streamCompleter.isCompleted) {
                    streamCompleter.complete();
                  }
                },
                cancelOnError: true,
              );

              await streamCompleter.future;
            } catch (_) {
            } finally {
              watchdogTimer?.cancel();
              watchdogTimer = null;
              currentStreamSub?.cancel();
              currentStreamSub = null;
              if (_ownsPerAttemptClient) {
                attemptClient?.close();
                if (identical(currentClient, attemptClient)) {
                  currentClient = null;
                }
              }
            }

            if (cancelled || controller.isClosed) break;

            final baseDelay =
                backoffSchedule[min(attempt, backoffSchedule.length - 1)];
            final jitter = _random.nextDouble() * 0.25;
            final delayMs = (baseDelay.inMilliseconds * (1.0 + jitter)).round();
            attempt++;

            final sleepComp = Completer<void>();
            sleepCompleter = sleepComp;
            final timer = Timer(Duration(milliseconds: delayMs), () {
              if (!sleepComp.isCompleted) sleepComp.complete();
            });
            backoffTimer = timer;

            await sleepComp.future;
            sleepCompleter = null;
            backoffTimer = null;
          }
        }();
      },
      onCancel: cancelStream,
    );

    return controller.stream;
  }

  void dispose() {
    final toCancel = List<void Function()>.from(_activeCancels);
    for (final cancel in toCancel) {
      cancel();
    }
    _activeCancels.clear();
    _sharedClient?.close();
  }
}
