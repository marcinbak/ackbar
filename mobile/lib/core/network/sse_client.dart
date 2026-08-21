import 'dart:async';
import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/host.dart';
import '../models/session.dart';

/// Server-Sent Events client for real-time streaming updates from ackbard /v1/events
class SSEClient {
  final http.Client _client;
  final Map<String, StreamSubscription<String>> _activeSubscriptions = {};

  SSEClient({http.Client? client}) : _client = client ?? http.Client();

  /// Connects to /v1/events for a single host and yields Session updates
  Stream<Session> subscribeHost(HostRecord host) async* {
    final streamController = StreamController<Session>.broadcast();
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

    void listenToSSE() async {
      while (!streamController.isClosed) {
        try {
          final request = http.Request('GET', uri)
            ..headers['Accept'] = 'text/event-stream'
            ..headers['Cache-Control'] = 'no-cache';
          if (host.authToken.isNotEmpty) {
            request.headers['Authorization'] = 'Bearer ${host.authToken}';
            request.headers['X-Ackbar-Token'] = host.authToken;
          }

          final streamedResponse = await _client.send(request);
          if (streamedResponse.statusCode == 200) {
            String buffer = '';
            await for (final chunk in streamedResponse.stream.transform(utf8.decoder)) {
              buffer += chunk;
              while (buffer.contains('\n')) {
                final lineIndex = buffer.indexOf('\n');
                final line = buffer.substring(0, lineIndex).trim();
                buffer = buffer.substring(lineIndex + 1);

                if (line.startsWith('data: ')) {
                  final dataStr = line.substring(6).trim();
                  try {
                    final Map<String, dynamic> json = jsonDecode(dataStr);
                    json['host'] = host.name;
                    final session = Session.fromJson(json);
                    if (!streamController.isClosed) {
                      streamController.add(session);
                    }
                  } catch (_) {
                    // Ignore malformed json chunks
                  }
                }
              }
            }
          }
        } catch (_) {
          // Connection error, wait before retry
        }
        await Future.delayed(const Duration(seconds: 3));
      }
    }

    listenToSSE();
    yield* streamController.stream;
  }

  /// Multiplexes SSE event streams from multiple hosts into a single combined stream
  Stream<Session> subscribeMultipleHosts(List<HostRecord> hosts) {
    final controller = StreamController<Session>.broadcast();
    final List<StreamSubscription> subs = [];

    for (final host in hosts) {
      if (!host.online) continue;
      final sub = subscribeHost(host).listen(
        (session) {
          if (!controller.isClosed) {
            controller.add(session);
          }
        },
        onError: (err) {
          // Continue with other hosts
        },
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
    for (final sub in _activeSubscriptions.values) {
      sub.cancel();
    }
    _activeSubscriptions.clear();
    _client.close();
  }
}
