import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:http/http.dart' as http;
import '../models/host.dart';
import '../models/session.dart';

class SSEClient {
  final http.Client? _sharedClient;
  final http.Client Function()? _clientFactory;
  final bool _ownsPerAttemptClient;
  final Duration inactivityTimeout;
  final List<Duration> backoffSchedule;
  final Random _random;
  final Set<void Function()> _activeCancels = {};

  SSEClient({
    http.Client? client,
    http.Client Function()? clientFactory,
    this.inactivityTimeout = const Duration(seconds: 45),
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

  Stream<Session> subscribeHost(
    HostRecord host, {
    void Function(HostRecord host)? onHostReconnected,
  }) {
    late final StreamController<Session> controller;
    bool cancelled = false;
    int attempt = 0;
    bool hasExperiencedFailure = false;

    http.Client? currentClient;
    StreamSubscription<String>? currentStreamSub;
    Timer? watchdogTimer;
    Timer? backoffTimer;
    Completer<void>? sleepCompleter;
    Completer<void>? activeStreamCompleter;

    void cancelSubscription() {
      if (cancelled) return;
      cancelled = true;
      _activeCancels.remove(cancelSubscription);
      watchdogTimer?.cancel();
      watchdogTimer = null;
      backoffTimer?.cancel();
      backoffTimer = null;
      if (sleepCompleter != null && !sleepCompleter!.isCompleted) {
        sleepCompleter!.complete();
        sleepCompleter = null;
      }
      if (activeStreamCompleter != null &&
          !activeStreamCompleter!.isCompleted) {
        activeStreamCompleter!.complete();
        activeStreamCompleter = null;
      }
      currentStreamSub?.cancel();
      currentStreamSub = null;
      if (_ownsPerAttemptClient) {
        currentClient?.close();
        currentClient = null;
      }
    }

    controller = StreamController<Session>.broadcast(
      onListen: () {
        _activeCancels.add(cancelSubscription);
        _runLoop(
          host: host,
          isCancelled: () => cancelled || controller.isClosed,
          emitSession: (s) {
            if (!cancelled && !controller.isClosed) {
              controller.add(s);
            }
          },
          onHostReconnected: onHostReconnected,
          setClient: (c) => currentClient = c,
          setStreamSub: (s) => currentStreamSub = s,
          setWatchdog: (t) => watchdogTimer = t,
          getWatchdog: () => watchdogTimer,
          setBackoffTimer: (t) => backoffTimer = t,
          setSleepCompleter: (c) => sleepCompleter = c,
          setActiveStreamCompleter: (c) => activeStreamCompleter = c,
          getAttempt: () => attempt,
          setAttempt: (a) => attempt = a,
          getHasExperiencedFailure: () => hasExperiencedFailure,
          setHasExperiencedFailure: (f) => hasExperiencedFailure = f,
        );
      },
      onCancel: cancelSubscription,
    );

    return controller.stream;
  }

  Future<void> _runLoop({
    required HostRecord host,
    required bool Function() isCancelled,
    required void Function(Session) emitSession,
    required void Function(HostRecord)? onHostReconnected,
    required void Function(http.Client?) setClient,
    required void Function(StreamSubscription<String>?) setStreamSub,
    required void Function(Timer?) setWatchdog,
    required Timer? Function() getWatchdog,
    required void Function(Timer?) setBackoffTimer,
    required void Function(Completer<void>?) setSleepCompleter,
    required void Function(Completer<void>?) setActiveStreamCompleter,
    required int Function() getAttempt,
    required void Function(int) setAttempt,
    required bool Function() getHasExperiencedFailure,
    required void Function(bool) setHasExperiencedFailure,
  }) async {
    var cleanUrl = host.url.trim();
    if (!cleanUrl.startsWith('http://') && !cleanUrl.startsWith('https://')) {
      cleanUrl = 'http://$cleanUrl';
    }
    if (cleanUrl.endsWith('/')) {
      cleanUrl = cleanUrl.substring(0, cleanUrl.length - 1);
    }
    var uri = Uri.parse('$cleanUrl/v1/events');
    if (host.authToken.isNotEmpty) {
      uri = uri.replace(queryParameters: {'token': host.authToken});
    }

    while (!isCancelled()) {
      http.Client? attemptClient;
      StreamSubscription<String>? sub;
      try {
        if (_ownsPerAttemptClient) {
          attemptClient = _effectiveClientFactory();
          setClient(attemptClient);
        } else {
          attemptClient = _sharedClient!;
          setClient(attemptClient);
        }

        final request = http.Request('GET', uri)
          ..headers['Accept'] = 'text/event-stream'
          ..headers['Cache-Control'] = 'no-cache';
        if (host.authToken.isNotEmpty) {
          request.headers['Authorization'] = 'Bearer ${host.authToken}';
          request.headers['X-Ackbar-Token'] = host.authToken;
        }

        final streamedResponse = await attemptClient.send(request);
        if (isCancelled()) break;

        if (streamedResponse.statusCode != 200) {
          throw http.ClientException('HTTP ${streamedResponse.statusCode}');
        }

        bool receivedValidFrame = false;
        final streamCompleter = Completer<void>();
        setActiveStreamCompleter(streamCompleter);

        void resetWatchdog() {
          getWatchdog()?.cancel();
          if (isCancelled()) return;
          setWatchdog(
            Timer(inactivityTimeout, () {
              if (!streamCompleter.isCompleted) {
                streamCompleter.completeError(
                  TimeoutException('SSE inactivity timeout'),
                );
              }
              if (_ownsPerAttemptClient) {
                attemptClient?.close();
              }
              sub?.cancel();
              setStreamSub(null);
            }),
          );
        }

        resetWatchdog();

        final List<String> dataBuffer = [];
        void flushData() {
          if (dataBuffer.isEmpty) return;
          final raw = dataBuffer.join('\n').trim();
          dataBuffer.clear();
          if (raw.isEmpty) return;
          try {
            final Map<String, dynamic> json = jsonDecode(raw);
            json['host'] = host.name;
            emitSession(Session.fromJson(json));
          } catch (_) {}
        }

        sub = streamedResponse.stream
            .transform(utf8.decoder)
            .transform(const LineSplitter())
            .listen(
          (line) {
            resetWatchdog();
            final trimmed = line.trimRight();
            if (trimmed.startsWith(':')) {
              if (!receivedValidFrame) {
                receivedValidFrame = true;
                setAttempt(0);
                if (getHasExperiencedFailure()) {
                  setHasExperiencedFailure(false);
                  onHostReconnected?.call(host);
                }
              }
            } else if (trimmed.startsWith('data:')) {
              if (!receivedValidFrame) {
                receivedValidFrame = true;
                setAttempt(0);
                if (getHasExperiencedFailure()) {
                  setHasExperiencedFailure(false);
                  onHostReconnected?.call(host);
                }
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

        setStreamSub(sub);
        await streamCompleter.future;
      } catch (_) {
      } finally {
        setActiveStreamCompleter(null);
        getWatchdog()?.cancel();
        setWatchdog(null);
        sub?.cancel();
        setStreamSub(null);
        if (_ownsPerAttemptClient) {
          attemptClient?.close();
          setClient(null);
        }
      }

      if (isCancelled()) break;

      setHasExperiencedFailure(true);
      final currentAttempt = getAttempt();
      final baseDelay =
          backoffSchedule[min(currentAttempt, backoffSchedule.length - 1)];
      final jitter = _random.nextDouble() * 0.25;
      final delayMs = (baseDelay.inMilliseconds * (1.0 + jitter)).round();
      setAttempt(currentAttempt + 1);

      final sleepComp = Completer<void>();
      setSleepCompleter(sleepComp);
      final timer = Timer(Duration(milliseconds: delayMs), () {
        if (!sleepComp.isCompleted) sleepComp.complete();
      });
      setBackoffTimer(timer);

      await sleepComp.future;
      setSleepCompleter(null);
      setBackoffTimer(null);
    }
  }

  Stream<Session> subscribeMultipleHosts(
    List<HostRecord> hosts, {
    bool includeOffline = false,
    void Function(HostRecord host)? onHostReconnected,
  }) {
    final controller = StreamController<Session>.broadcast();
    final List<StreamSubscription> subs = [];

    for (final host in hosts) {
      if (!includeOffline && !host.online) continue;
      final sub = subscribeHost(
        host,
        onHostReconnected: onHostReconnected,
      ).listen(
        (session) {
          if (!controller.isClosed) {
            controller.add(session);
          }
        },
        onError: (_) {},
      );
      subs.add(sub);
    }

    controller.onCancel = () {
      for (final s in subs) {
        s.cancel();
      }
    };

    return controller.stream;
  }

  void dispose() {
    final toCancel = List<void Function()>.from(_activeCancels);
    for (final cancel in toCancel) {
      cancel();
    }
    _activeCancels.clear();
  }
}
