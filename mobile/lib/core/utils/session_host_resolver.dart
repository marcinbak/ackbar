import '../models/host.dart';
import '../models/session.dart';

class ResolvedSessionHost {
  final String baseUrl;
  final String? authToken;

  const ResolvedSessionHost({
    required this.baseUrl,
    this.authToken,
  });

  String get url => baseUrl;
}

class SessionHostResolver {
  static HostRecord resolveHostRecord(List<HostRecord> hosts, String hostName) {
    for (final h in hosts) {
      if (h.name.toLowerCase() == hostName.toLowerCase() ||
          h.url.toLowerCase() == hostName.toLowerCase()) {
        return h;
      }
    }
    return hosts.isNotEmpty
        ? hosts.first
        : HostRecord(
            name: hostName.isNotEmpty ? hostName : 'local',
            url: 'http://127.0.0.1:7777',
            createdAt: DateTime.now(),
          );
  }

  static Session? findMatchingSession(List<Session>? sessions, Session target) {
    if (sessions == null) return null;
    for (final s in sessions) {
      if (s.id == target.id ||
          (s.oldId.isNotEmpty && s.oldId == target.id) ||
          (target.nativeId.isNotEmpty && s.nativeId == target.nativeId)) {
        return s;
      }
    }
    return null;
  }

  static Session resolveLiveSession(List<Session>? sessions, Session fallback) {
    return findMatchingSession(sessions, fallback) ?? fallback;
  }

  static Future<ResolvedSessionHost> resolveSessionEndpoint({
    required List<HostRecord> Function() getHosts,
    required Session session,
    bool Function()? isMounted,
  }) async {
    var hosts = getHosts();
    if (hosts.isEmpty) {
      for (var i = 0; i < 6 && hosts.isEmpty; i++) {
        await Future.delayed(const Duration(milliseconds: 80));
        if (isMounted != null && !isMounted()) {
          return const ResolvedSessionHost(baseUrl: 'http://127.0.0.1:7777');
        }
        hosts = getHosts();
      }
    }

    final sessionHost = session.host.toLowerCase();
    final match = hosts.where(
      (h) =>
          h.name.toLowerCase() == sessionHost ||
          h.url.toLowerCase().contains(sessionHost),
    );
    if (match.isNotEmpty) {
      final h = match.first;
      return ResolvedSessionHost(baseUrl: h.url, authToken: h.authToken);
    }

    final online = hosts.where((h) => h.online);
    if (online.isNotEmpty) {
      final h = online.first;
      return ResolvedSessionHost(baseUrl: h.url, authToken: h.authToken);
    }

    if (hosts.isNotEmpty) {
      final h = hosts.first;
      return ResolvedSessionHost(baseUrl: h.url, authToken: h.authToken);
    }

    return const ResolvedSessionHost(baseUrl: 'http://127.0.0.1:7777');
  }
}
