import 'dart:async';
import 'dart:convert';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/host.dart';
import '../models/plan.dart';
import '../models/session.dart';
import '../network/api_client.dart';
import '../network/sse_client.dart';

// --- Client Providers ---

final apiClientProvider = Provider<ApiClient>((ref) {
  final client = ApiClient();
  ref.onDispose(() => client.dispose());
  return client;
});

final sseClientProvider = Provider<SSEClient>((ref) {
  final sse = SSEClient();
  ref.onDispose(() => sse.dispose());
  return sse;
});

// --- Navigation & Filter State Providers ---

final mainTabNavIndexProvider = StateProvider<int>((ref) => 0);
final fleetSearchQueryProvider = StateProvider<String>((ref) => '');
final fleetFilterIndexProvider = StateProvider<int>((ref) => 0);
final attentionFilterIndexProvider = StateProvider<int>((ref) => 0);
final plansFilterIndexProvider = StateProvider<int>((ref) => 0);

// --- Decision Audit Entry for History Sub-View ---

class DecisionAuditEntry {
  final String id;
  final String sessionId;
  final String sessionTitle;
  final String agent;
  final String host;
  final String actionType; // 'allowed' | 'denied' | 'answered' | 'approved_plan'
  final String summary;
  final DateTime timestamp;

  const DecisionAuditEntry({
    required this.id,
    required this.sessionId,
    required this.sessionTitle,
    required this.agent,
    required this.host,
    required this.actionType,
    required this.summary,
    required this.timestamp,
  });
}

class DecisionAuditNotifier extends StateNotifier<List<DecisionAuditEntry>> {
  DecisionAuditNotifier() : super(const []);

  void recordDecision({
    required String sessionId,
    required String sessionTitle,
    required String agent,
    required String host,
    required String actionType,
    required String summary,
  }) {
    final entry = DecisionAuditEntry(
      id: 'dec-${DateTime.now().millisecondsSinceEpoch}',
      sessionId: sessionId,
      sessionTitle: sessionTitle,
      agent: agent,
      host: host,
      actionType: actionType,
      summary: summary,
      timestamp: DateTime.now(),
    );
    state = [entry, ...state];
  }
}

final decisionAuditProvider =
    StateNotifierProvider<DecisionAuditNotifier, List<DecisionAuditEntry>>((ref) {
  return DecisionAuditNotifier();
});

// --- Hosts Provider ---

class HostsNotifier extends StateNotifier<List<HostRecord>> {
  final ApiClient _apiClient;
  static const String _kHostsKey = 'ackbar_persisted_hosts';

  HostsNotifier(this._apiClient) : super(const []) {
    _loadPersistedHosts();
  }

  Future<void> _loadPersistedHosts() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final jsonStr = prefs.getString(_kHostsKey);
      if (jsonStr != null && jsonStr.isNotEmpty) {
        final List<dynamic> list = jsonDecode(jsonStr);
        final loaded = list.map((item) => HostRecord.fromJson(item as Map<String, dynamic>)).toList();
        if (mounted && loaded.isNotEmpty) {
          state = loaded;
          refreshHosts();
        }
      }
    } catch (_) {}
  }

  Future<void> _savePersistedHosts() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final list = state.map((h) => h.toJson()).toList();
      await prefs.setString(_kHostsKey, jsonEncode(list));
    } catch (_) {}
  }

  Future<void> refreshHosts() async {
    final updated = <HostRecord>[];
    for (final h in state) {
      final health = await _apiClient.checkHostHealth(h.url, authToken: h.authToken);
      if (!mounted) return;
      if (health != null) {
        final sessions = await _apiClient.getSessions(h.url, authToken: h.authToken);
        if (!mounted) return;
        updated.add(h.copyWith(
          online: true,
          latencyMs: health['latency_ms'] as int? ?? 1,
          version: health['version'] as String? ?? '',
          uptime: health['uptime'] as String? ?? 'Active',
          sessionsCount: sessions.length,
        ));
      } else {
        updated.add(h.copyWith(online: false));
      }
    }
    if (mounted) {
      state = updated;
    }
  }

  void addHost(HostRecord host) {
    state = [...state, host];
    _savePersistedHosts();
    refreshHosts();
  }

  void updateHost(HostRecord updatedHost) {
    state = state.map((h) => h.name == updatedHost.name || h.url == updatedHost.url ? updatedHost : h).toList();
    _savePersistedHosts();
    refreshHosts();
  }

  void removeHost(String name) {
    state = state.where((h) => h.name != name && h.url != name).toList();
    _savePersistedHosts();
  }
}

final hostsListProvider = StateNotifierProvider<HostsNotifier, List<HostRecord>>((ref) {
  final api = ref.watch(apiClientProvider);
  return HostsNotifier(api);
});

// --- Plans Provider ---

class PlansNotifier extends StateNotifier<List<PlanDocument>> {
  PlansNotifier() : super(const []);

  void setPlans(List<PlanDocument> plans) {
    state = plans;
  }

  void quickApprovePlan(String planId) {
    state = state.map((plan) {
      if (plan.id == planId) {
        return plan.copyWith(
          status: PlanStatus.approved,
          userReviewCallout: '',
          progressPct: 0.50,
          currentStep: 'Plan approved by user. Beginning execution...',
        );
      }
      return plan;
    }).toList();
  }
}

final plansListProvider = StateNotifierProvider<PlansNotifier, List<PlanDocument>>((ref) {
  return PlansNotifier();
});

// --- Fleet Sessions Provider with Pure Live State ---

class FleetSessionsNotifier extends StateNotifier<List<Session>> {
  final ApiClient _apiClient;
  final Ref _ref;
  StreamSubscription<Session>? _sseSub;
  ProviderSubscription<List<HostRecord>>? _hostsSub;

  FleetSessionsNotifier(this._apiClient, this._ref) : super(const []) {
    _initLiveSync();
    _hostsSub = _ref.listen<List<HostRecord>>(hostsListProvider, (previous, next) {
      _syncWithHosts(next);
    });
  }

  @override
  void dispose() {
    _sseSub?.cancel();
    _hostsSub?.close();
    super.dispose();
  }

  void setSessions(List<Session> sessions) {
    state = sessions;
  }

  void _initLiveSync() {
    final hosts = _ref.read(hostsListProvider);
    _syncWithHosts(hosts);
  }

  void _syncWithHosts(List<HostRecord> hosts) async {
    _sseSub?.cancel();
    if (hosts.isNotEmpty) {
      final sse = _ref.read(sseClientProvider);
      _sseSub = sse.subscribeMultipleHosts(hosts).listen((liveSess) {
        if (mounted) {
          _upsertSession(liveSess);
        }
      });
    }

    await refreshSessions();
  }

  Future<void> refreshSessions() async {
    final hosts = _ref.read(hostsListProvider);
    if (hosts.isEmpty) {
      if (mounted) state = const [];
      return;
    }

    final allFetched = <Session>[];
    for (final host in hosts) {
      final remoteSessions = await _apiClient.getSessions(host.url, authToken: host.authToken);
      if (!mounted) return;
      allFetched.addAll(remoteSessions);
    }

    if (mounted) {
      state = allFetched;
    }
  }

  void _upsertSession(Session s) {
    final index = state.indexWhere((item) => item.id == s.id || item.nativeId == s.nativeId);
    if (index != -1) {
      final updated = List<Session>.from(state);
      updated[index] = s;
      state = updated;
    } else {
      state = [s, ...state];
    }
  }

  /// Real-time response action to unblock a session
  Future<bool> respondToSession({
    required String sessionId,
    required String action,
    required String value,
    String? answerSummary,
  }) async {
    final session = state.firstWhere(
      (s) => s.id == sessionId || s.nativeId == sessionId,
      orElse: () => state.first,
    );

    // Optimistically update local session state to Working
    final updatedSession = session.copyWith(
      state: SessionState.working,
      clearBlocked: true,
      activity: action == 'allow'
          ? 'Permission allowed: $value'
          : (action == 'deny' ? 'Permission denied' : 'Answer submitted: $value'),
      lastEventAt: DateTime.now(),
    );
    _upsertSession(updatedSession);

    // Record in decision audit log
    _ref.read(decisionAuditProvider.notifier).recordDecision(
          sessionId: session.id,
          sessionTitle: session.displayTitle,
          agent: session.agentDisplayName,
          host: session.host,
          actionType: action,
          summary: answerSummary ?? '$action: $value',
        );

    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    // Dispatch HTTP POST /v1/sessions/respond
    final ok = await _apiClient.respondToSession(
      hostUrl,
      id: session.id,
      action: action,
      value: value,
      authToken: host?.authToken,
    );
    return ok;
  }

  /// Mark unread session state as read
  Future<void> markSessionRead(String sessionId, String hostName) async {
    final index = state.indexWhere((s) => s.id == sessionId || s.nativeId == sessionId);
    if (index != -1 && state[index].isUnread) {
      final updated = List<Session>.from(state);
      updated[index] = updated[index].copyWith(isUnread: false);
      state = updated;
    }

    final host = _getHostRecord(hostName);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';
    await _apiClient.markSessionRead(hostUrl, sessionId, authToken: host?.authToken);
  }

  HostRecord? _getHostRecord(String hostName) {
    final hosts = _ref.read(hostsListProvider);
    final match = hosts.where((h) => h.name == hostName || h.url.contains(hostName));
    if (match.isNotEmpty) return match.first;
    if (hosts.isNotEmpty) return hosts.first;
    return null;
  }

  String _getHostUrl(String hostName) {
    final h = _getHostRecord(hostName);
    return h?.url ?? 'http://127.0.0.1:7777';
  }

  Future<void> restartSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    _upsertSession(session.copyWith(
      state: SessionState.working,
      activity: 'Restarting session in tmux...',
      lastEventAt: DateTime.now(),
    ));

    await _apiClient.controlSession(hostUrl, session.id, 'restart', authToken: host?.authToken);
  }

  Future<void> terminateSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    _upsertSession(session.copyWith(
      state: SessionState.ended,
      activity: 'Terminated by user',
      lastEventAt: DateTime.now(),
    ));

    await _apiClient.controlSession(hostUrl, session.id, 'kill', authToken: host?.authToken);
  }

  Future<void> deleteSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    state = state.where((s) => s.id != sessionId).toList();
    await _apiClient.controlSession(hostUrl, session.id, 'delete', authToken: host?.authToken);
  }
}

final fleetSessionsProvider =
    StateNotifierProvider<FleetSessionsNotifier, List<Session>>((ref) {
  final api = ref.watch(apiClientProvider);
  return FleetSessionsNotifier(api, ref);
});

// --- Filtered Sessions Provider ---

final filteredSessionsProvider = Provider<List<Session>>((ref) {
  final allSessions = ref.watch(fleetSessionsProvider);
  final filterIndex = ref.watch(fleetFilterIndexProvider);
  final query = ref.watch(fleetSearchQueryProvider).toLowerCase().trim();

  final list = allSessions.where((s) {
    // Hide archived/deleted sessions from active fleet views
    if (s.archived) return false;

    // Tab filter: 0: All, 1: Working, 2: Attention/Blocked, 3: Idle
    if (filterIndex == 1 && s.state != SessionState.working) return false;
    if (filterIndex == 2 && !s.isBlocked) return false;
    if (filterIndex == 3 && s.state != SessionState.idle) return false;

    // Search query filter
    if (query.isNotEmpty) {
      final matchesTitle = s.displayTitle.toLowerCase().contains(query);
      final matchesAgent = s.agent.toLowerCase().contains(query);
      final matchesHost = s.host.toLowerCase().contains(query);
      final matchesProject = s.projectDisplayName.toLowerCase().contains(query);
      final matchesActivity = s.activity.toLowerCase().contains(query);
      final matchesBranch = s.gitBranch.toLowerCase().contains(query);
      if (!matchesTitle && !matchesAgent && !matchesHost && !matchesProject && !matchesActivity && !matchesBranch) {
        return false;
      }
    }
    return true;
  }).toList();

  // Sort sessions descending by latest interaction (newest / active first)
  list.sort((a, b) {
    final timeA = a.lastEventAt.isAfter(a.startedAt) ? a.lastEventAt : a.startedAt;
    final timeB = b.lastEventAt.isAfter(b.startedAt) ? b.lastEventAt : b.startedAt;
    final cmp = timeB.compareTo(timeA);
    if (cmp != 0) return cmp;
    return a.id.compareTo(b.id);
  });

  return list;
});

/// Grouped sessions by project / category folder
final groupedSessionsProvider = Provider<Map<String, List<Session>>>((ref) {
  final filtered = ref.watch(filteredSessionsProvider);
  final groups = <String, List<Session>>{};

  for (final s in filtered) {
    final groupKey = s.projectDisplayName;
    groups.putIfAbsent(groupKey, () => []).add(s);
  }
  return groups;
});

// --- Attention & Plans Badges Counts ---

final attentionPendingSessionsProvider = Provider<List<Session>>((ref) {
  final sessions = ref.watch(fleetSessionsProvider);
  return sessions.where((s) => !s.archived && s.isBlocked).toList();
});

final inProgressSessionsProvider = Provider<List<Session>>((ref) {
  final sessions = ref.watch(fleetSessionsProvider);
  return sessions.where((s) => !s.archived && s.state == SessionState.working).toList();
});

final attentionBadgeCountProvider = Provider<int>((ref) {
  final pending = ref.watch(attentionPendingSessionsProvider);
  return pending.length;
});

final pendingPlansCountProvider = Provider<int>((ref) {
  final plans = ref.watch(plansListProvider);
  return plans.where((p) => p.isPendingReview).length;
});
