import 'dart:async';
import 'dart:convert';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/host.dart';
import '../models/plan.dart';
import '../models/session.dart';
import '../models/tree_node.dart';
import '../models/work_task.dart';
import '../network/api_client.dart';
import '../network/sse_client.dart';
import '../services/audio_companion_service.dart';
import '../utils/node_path_resolver.dart';

// --- View Modes ---

enum FleetViewMode { all, supervisors }

final fleetViewModeProvider =
    StateProvider<FleetViewMode>((ref) => FleetViewMode.all);

/// Checks if a session host matches a configured host that is currently offline
bool isSessionHostOffline(Session session, List<HostRecord> hosts) {
  if (hosts.isEmpty) return false;
  final sHost = session.host.toLowerCase();
  for (final h in hosts) {
    if (h.name.toLowerCase() == sHost || h.url.toLowerCase().contains(sHost)) {
      return !h.online;
    }
  }
  return false;
}

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
  final String
      actionType; // 'allowed' | 'denied' | 'answered' | 'approved_plan'
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
    StateNotifierProvider<DecisionAuditNotifier, List<DecisionAuditEntry>>(
        (ref) {
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
        final loaded = list
            .map((item) => HostRecord.fromJson(item as Map<String, dynamic>))
            .toList();
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
      final health =
          await _apiClient.checkHostHealth(h.url, authToken: h.authToken);
      if (!mounted) return;
      if (health != null) {
        final sessions = await _apiClient.getSessionsOrNull(
          h.url,
          authToken: h.authToken,
          hostName: h.name,
        );
        if (!mounted) return;
        updated.add(h.copyWith(
          online: true,
          latencyMs: health['latency_ms'] as int? ?? 1,
          version: health['version'] as String? ?? '',
          uptime: health['uptime'] as String? ?? 'Active',
          sessionsCount: sessions != null ? sessions.length : h.sessionsCount,
        ));
      } else {
        updated.add(h.copyWith(online: false));
      }
    }
    if (mounted) {
      state = updated;
    }
  }

  void setHosts(List<HostRecord> hosts) {
    state = hosts;
  }

  void addHost(HostRecord host) {
    state = [...state, host];
    _savePersistedHosts();
    refreshHosts();
  }

  void updateHost(HostRecord updatedHost) {
    state = state
        .map((h) => h.name == updatedHost.name || h.url == updatedHost.url
            ? updatedHost
            : h)
        .toList();
    _savePersistedHosts();
    refreshHosts();
  }

  void removeHost(String name) {
    state = state.where((h) => h.name != name && h.url != name).toList();
    _savePersistedHosts();
  }
}

final hostsListProvider =
    StateNotifierProvider<HostsNotifier, List<HostRecord>>((ref) {
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

final plansListProvider =
    StateNotifierProvider<PlansNotifier, List<PlanDocument>>((ref) {
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
    _hostsSub =
        _ref.listen<List<HostRecord>>(hostsListProvider, (previous, next) {
      if (_didHostsConnectionChange(previous, next)) {
        _syncWithHosts(next);
      }
    });
  }

  static bool _didHostsConnectionChange(
    List<HostRecord>? previous,
    List<HostRecord> next,
  ) {
    if (previous == null) return true;
    if (previous.length != next.length) return true;
    for (var i = 0; i < previous.length; i++) {
      final a = previous[i];
      final b = next[i];
      if (a.name != b.name ||
          a.url != b.url ||
          a.authToken != b.authToken ||
          a.online != b.online) {
        return true;
      }
    }
    return false;
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

  bool _sessionBelongsToHost(Session s, HostRecord host, int totalHosts) {
    if (totalHosts <= 1) return true;
    final sh = s.host.toLowerCase();
    final hn = host.name.toLowerCase();
    final hu = host.url.toLowerCase();
    return sh == hn || hu.contains(sh) || (sh == 'local' && host.isLocal);
  }

  Future<void> refreshSessions() async {
    final hosts = _ref.read(hostsListProvider);
    if (hosts.isEmpty) {
      if (mounted) state = const [];
      return;
    }

    final allFetched = <Session>[];
    final seenIds = <String>{};
    for (final host in hosts) {
      final remoteSessions = await _apiClient.getSessionsOrNull(
        host.url,
        authToken: host.authToken,
        hostName: host.name,
      );
      if (!mounted) return;
      final hostSessions = remoteSessions ??
          state.where((s) => _sessionBelongsToHost(s, host, hosts.length));
      for (final s in hostSessions) {
        if (seenIds.add(s.id)) {
          allFetched.add(s);
        }
      }
    }

    if (mounted) {
      state = allFetched;
    }
  }

  void _upsertSession(Session s) {
    final index = state.indexWhere((item) =>
        item.id == s.id ||
        (s.nativeId.isNotEmpty && item.nativeId == s.nativeId) ||
        (s.oldId.isNotEmpty && item.id == s.oldId));
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
          : (action == 'deny'
              ? 'Permission denied'
              : 'Answer submitted: $value'),
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
    final index =
        state.indexWhere((s) => s.id == sessionId || s.nativeId == sessionId);
    if (index != -1 && state[index].isUnread) {
      final updated = List<Session>.from(state);
      updated[index] = updated[index].copyWith(isUnread: false);
      state = updated;
    }

    final host = _getHostRecord(hostName);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';
    await _apiClient.markSessionRead(hostUrl, sessionId,
        authToken: host?.authToken);
  }

  HostRecord? _getHostRecord(String hostName) {
    final hosts = _ref.read(hostsListProvider);
    final target = hostName.toLowerCase();
    final match = hosts.where((h) =>
        h.name.toLowerCase() == target || h.url.toLowerCase().contains(target));
    if (match.isNotEmpty) return match.first;
    final online = hosts.where((h) => h.online);
    if (online.isNotEmpty) return online.first;
    if (hosts.isNotEmpty) return hosts.first;
    return null;
  }

  String _getHostUrl(String hostName) {
    final h = _getHostRecord(hostName);
    return h?.url ?? 'http://127.0.0.1:7777';
  }

  Future<void> resumeSession(String sessionId) async {
    final session =
        state.firstWhere((s) => s.id == sessionId || s.nativeId == sessionId);
    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    _upsertSession(session.copyWith(
      state: SessionState.working,
      activity: 'Resuming session in tmux...',
      lastEventAt: DateTime.now(),
    ));

    await _apiClient.controlSession(hostUrl, session.id, 'resume',
        authToken: host?.authToken);
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

    await _apiClient.controlSession(hostUrl, session.id, 'restart',
        authToken: host?.authToken);
  }

  Future<bool> handoverSession(
    String sessionId, {
    String strategy = 'in_place',
    String? customInstruction,
  }) async {
    final session = state.firstWhere(
      (s) => s.id == sessionId || s.nativeId == sessionId,
      orElse: () => state.first,
    );
    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    _upsertSession(session.copyWith(
      state: SessionState.working,
      activity: 'Handover: Generating briefing...',
      lastEventAt: DateTime.now(),
    ));

    return await _apiClient.handoverSession(
      hostUrl,
      session.id,
      strategy: strategy,
      customInstruction: customInstruction,
      authToken: host?.authToken,
    );
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

    await _apiClient.controlSession(hostUrl, session.id, 'kill',
        authToken: host?.authToken);
  }

  Future<void> deleteSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final host = _getHostRecord(session.host);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';

    state = state.where((s) => s.id != sessionId).toList();
    await _apiClient.controlSession(hostUrl, session.id, 'delete',
        authToken: host?.authToken);
  }

  /// Spawn a new agent session on a specified host
  Future<Map<String, dynamic>?> spawnSession({
    required String hostName,
    required String agent,
    required String cwd,
    String? nodePath,
    String? name,
    String? accountId,
    String engineType = 'tmux',
    String? prompt,
  }) async {
    final host = _getHostRecord(hostName);
    final hostUrl = host?.url ?? 'http://127.0.0.1:7777';
    final resolvedHostName =
        host?.name ?? (hostName.isNotEmpty ? hostName : 'local');

    final result = await _apiClient.spawnSession(
      hostUrl,
      agent: agent,
      cwd: cwd,
      host: 'local',
      nodePath: nodePath,
      name: name,
      accountId: accountId,
      engineType: engineType,
      prompt: prompt,
      authToken: host?.authToken,
    );

    if (result != null) {
      Session spawnedSession;
      if (result['session'] is Map<String, dynamic>) {
        final parsed =
            Session.fromJson(result['session'] as Map<String, dynamic>);
        spawnedSession = (parsed.host.isEmpty || parsed.host == 'local') &&
                resolvedHostName != 'local'
            ? parsed.copyWith(host: resolvedHostName)
            : parsed;
      } else {
        final nativeId = result['session_id']?.toString() ?? '';
        final sessionId =
            result['id']?.toString() ?? '$agent:$resolvedHostName:$nativeId';
        final now = DateTime.now();
        final fallbackTitle = (name != null && name.trim().isNotEmpty)
            ? name.trim()
            : cwd.split('/').where((p) => p.isNotEmpty).lastOrNull ?? agent;
        spawnedSession = Session(
          id: sessionId,
          agent: agent,
          host: resolvedHostName,
          nativeId: nativeId,
          cwd: cwd,
          nodePath: nodePath ?? '',
          name: fallbackTitle,
          customTitle: name ?? '',
          firstPrompt: prompt ?? '',
          lastPrompt: prompt ?? '',
          state: engineType == 'headless'
              ? SessionState.idle
              : SessionState.working,
          activity: engineType == 'headless'
              ? 'Headless chat session ready'
              : 'Spawning session...',
          startedAt: now,
          lastEventAt: now,
          managed: true,
          engineType: engineType,
        );
      }
      if (mounted) {
        _upsertSession(spawnedSession);
      }
      await refreshSessions();
      if (mounted &&
          !state.any((s) =>
              s.id == spawnedSession.id ||
              (spawnedSession.nativeId.isNotEmpty &&
                  s.nativeId == spawnedSession.nativeId))) {
        _upsertSession(spawnedSession);
      }
    }
    return result;
  }

  /// 1-tap Zero-Input Supervisor Creation on connected host
  Future<Session?> spawnZeroInputSupervisor({String? hostName}) async {
    final hosts = _ref.read(hostsListProvider);
    final targetHost = hostName != null && hostName.isNotEmpty
        ? _getHostRecord(hostName)
        : hosts.firstWhere(
            (h) => h.online,
            orElse: () => hosts.isNotEmpty
                ? hosts.first
                : HostRecord(
                    name: 'local',
                    url: 'http://127.0.0.1:7777',
                    createdAt: DateTime.fromMillisecondsSinceEpoch(0),
                  ),
          );
    final hostUrl = targetHost?.url ?? 'http://127.0.0.1:7777';
    final resolvedHostName = targetHost?.name ?? 'local';

    final result = await _apiClient.spawnSession(
      hostUrl,
      agent: 'claude-code',
      cwd: '',
      host: 'local',
      name: '⚓ New Supervisor',
      engineType: 'tmux',
      isSupervisor: true,
      authToken: targetHost?.authToken,
    );

    if (result != null) {
      Session spawned;
      if (result['session'] is Map<String, dynamic>) {
        final parsed =
            Session.fromJson(result['session'] as Map<String, dynamic>);
        spawned = (parsed.host.isEmpty || parsed.host == 'local') &&
                resolvedHostName != 'local'
            ? parsed.copyWith(host: resolvedHostName)
            : parsed;
      } else {
        final nativeId = result['session_id']?.toString() ?? '';
        final sessionId = result['id']?.toString() ??
            'claude-code:$resolvedHostName:$nativeId';
        final now = DateTime.now();
        spawned = Session(
          id: sessionId,
          agent: 'claude-code',
          host: resolvedHostName,
          nativeId: nativeId,
          cwd: '~',
          name: '⚓ New Supervisor',
          customTitle: '⚓ New Supervisor',
          state: SessionState.working,
          activity: 'Spawning supervisor session...',
          startedAt: now,
          lastEventAt: now,
          managed: true,
          isSupervisor: true,
        );
      }
      if (mounted) {
        _upsertSession(spawned);
      }
      await refreshSessions();
      return spawned;
    }
    return null;
  }
}

final fleetSessionsProvider =
    StateNotifierProvider<FleetSessionsNotifier, List<Session>>((ref) {
  final api = ref.watch(apiClientProvider);
  return FleetSessionsNotifier(api, ref);
});

/// Unique list of working directory paths known from existing sessions to suggest in New Session sheet
final knownProjectPathsProvider = Provider<List<String>>((ref) {
  final sessions = ref.watch(fleetSessionsProvider);
  final paths = <String>{};
  for (final s in sessions) {
    if (s.cwd.isNotEmpty) {
      paths.add(s.cwd);
    }
  }
  return paths.toList();
});

/// Fetches and caches supported agent discovery for a given host
final hostAgentDiscoveryProvider = FutureProvider.autoDispose
    .family<List<Map<String, dynamic>>, String>((ref, hostName) async {
  final hostConfig = ref.watch(hostsListProvider.select((hosts) {
    HostRecord? host;
    final target = hostName.toLowerCase();
    final exactName = hosts.where((h) => h.name.toLowerCase() == target);
    if (exactName.isNotEmpty) {
      host = exactName.first;
    } else {
      final urlMatch = hosts.where((h) =>
          h.url.toLowerCase() == target ||
          h.url.toLowerCase().contains(target));
      if (urlMatch.isNotEmpty) {
        host = urlMatch.first;
      }
    }
    return (host?.url ?? 'http://127.0.0.1:7777', host?.authToken);
  }));

  final api = ref.watch(apiClientProvider);
  return api.getAgentsDiscovery(hostConfig.$1, authToken: hostConfig.$2);
});

// --- Fleet Nodes Provider ---

class FleetNodesNotifier extends StateNotifier<List<TreeNode>> {
  final ApiClient _apiClient;
  final Ref _ref;
  ProviderSubscription<List<HostRecord>>? _hostsSub;

  FleetNodesNotifier(this._apiClient, this._ref) : super(const []) {
    _initNodes();
    _hostsSub = _ref.listen<List<HostRecord>>(hostsListProvider, (prev, next) {
      if (prev == null ||
          prev.length != next.length ||
          prev.any((h) => !next.contains(h))) {
        refreshNodes();
      }
    });
  }

  @override
  void dispose() {
    _hostsSub?.close();
    super.dispose();
  }

  void setNodes(List<TreeNode> nodes) {
    state = nodes;
  }

  void _initNodes() {
    refreshNodes();
  }

  Future<void> refreshNodes() async {
    final hosts = _ref.read(hostsListProvider);
    if (hosts.isEmpty) {
      if (mounted) state = const [];
      return;
    }

    final merged = <String, TreeNode>{};
    for (final host in hosts) {
      if (!host.online) continue;
      final hostNodes = await _apiClient.getNodes(
        host.url,
        federated: true,
        authToken: host.authToken,
      );
      if (!mounted) return;
      if (hostNodes != null) {
        for (final node in hostNodes) {
          if (!merged.containsKey(node.path)) {
            merged[node.path] = node;
          } else {
            final existing = merged[node.path]!;
            final combinedHostPaths =
                List<NodeHostPath>.from(existing.hostPaths);
            for (final hp in node.hostPaths) {
              if (!combinedHostPaths.any(
                  (p) => p.host == hp.host && p.projectDir == hp.projectDir)) {
                combinedHostPaths.add(hp);
              }
            }
            merged[node.path] = TreeNode(
              path: existing.path,
              projectDir: existing.projectDir.isNotEmpty
                  ? existing.projectDir
                  : node.projectDir,
              gitUrl:
                  existing.gitUrl.isNotEmpty ? existing.gitUrl : node.gitUrl,
              createdAt: existing.createdAt,
              updatedAt: node.updatedAt.isAfter(existing.updatedAt)
                  ? node.updatedAt
                  : existing.updatedAt,
              hostPaths: combinedHostPaths,
            );
          }
        }
      }
    }

    if (mounted) {
      state = merged.values.toList();
    }
  }
}

final fleetNodesProvider =
    StateNotifierProvider<FleetNodesNotifier, List<TreeNode>>((ref) {
  final api = ref.watch(apiClientProvider);
  return FleetNodesNotifier(api, ref);
});

// --- Filtered Sessions Provider ---

final filteredSessionsProvider = Provider<List<Session>>((ref) {
  final allSessions = ref.watch(fleetSessionsProvider);
  final filterIndex = ref.watch(fleetFilterIndexProvider);
  final viewMode = ref.watch(fleetViewModeProvider);
  final query = ref.watch(fleetSearchQueryProvider).toLowerCase().trim();

  final list = allSessions.where((s) {
    // Hide archived/deleted sessions from active fleet views
    if (s.archived) return false;

    // ViewMode filter (All vs Supervisors)
    if (viewMode == FleetViewMode.supervisors && !s.isSupervisor) return false;

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
      if (!matchesTitle &&
          !matchesAgent &&
          !matchesHost &&
          !matchesProject &&
          !matchesActivity &&
          !matchesBranch) {
        return false;
      }
    }
    return true;
  }).toList();

  // Sort sessions descending by latest interaction (newest / active first)
  list.sort((a, b) {
    final timeA =
        a.lastEventAt.millisecondsSinceEpoch > 0 ? a.lastEventAt : a.startedAt;
    final timeB =
        b.lastEventAt.millisecondsSinceEpoch > 0 ? b.lastEventAt : b.startedAt;
    final cmp = timeB.compareTo(timeA);
    if (cmp != 0) return cmp;
    return a.id.compareTo(b.id);
  });

  return list;
});

// --- Hierarchical Tree Model & Providers ---

class FleetTreeFolderNode {
  final String path;
  final String name;
  final int depth;
  final List<Session> supervisors;
  final List<Session> tasks;
  final List<Session> manualSessions;
  final List<Session> laterSessions;
  final List<Session> doneSessions;
  final List<FleetTreeFolderNode> children;

  const FleetTreeFolderNode({
    required this.path,
    required this.name,
    this.depth = 0,
    this.supervisors = const [],
    this.tasks = const [],
    this.manualSessions = const [],
    this.laterSessions = const [],
    this.doneSessions = const [],
    this.children = const [],
  });

  int get directCount =>
      supervisors.length +
      tasks.length +
      manualSessions.length +
      laterSessions.length +
      doneSessions.length;

  int get totalCount =>
      directCount + children.fold(0, (sum, child) => sum + child.totalCount);

  bool get isEmpty => totalCount == 0;
}

final fleetTreeProvider = Provider<List<FleetTreeFolderNode>>((ref) {
  final filteredSessions = ref.watch(filteredSessionsProvider);
  final nodes = ref.watch(fleetNodesProvider);
  final viewMode = ref.watch(fleetViewModeProvider);

  // 1. Group sessions by resolved node path
  final sessionsByPath = <String, List<Session>>{};
  final unassigned = <Session>[];

  for (final s in filteredSessions) {
    final resolved = resolveSessionNodePath(s, nodes, s.host);
    if (resolved.isNotEmpty) {
      sessionsByPath.putIfAbsent(resolved, () => []).add(s);
    } else {
      unassigned.add(s);
    }
  }

  // 2. Discover all known group paths and their ancestor prefixes
  final allGroupPaths = <String>{};
  if (viewMode != FleetViewMode.supervisors) {
    for (final node in nodes) {
      if (node.path.isNotEmpty) {
        final parts = node.path.split('/');
        for (int i = 1; i <= parts.length; i++) {
          allGroupPaths.add(parts.sublist(0, i).join('/'));
        }
      }
    }
  }

  for (final path in sessionsByPath.keys) {
    final parts = path.split('/');
    for (int i = 1; i <= parts.length; i++) {
      allGroupPaths.add(parts.sublist(0, i).join('/'));
    }
  }

  // Helper to partition direct sessions into the 5 subsections
  (List<Session>, List<Session>, List<Session>, List<Session>, List<Session>)
      partitionSessions(List<Session> direct) {
    final sups = <Session>[];
    final tsks = <Session>[];
    final manuals = <Session>[];
    final laters = <Session>[];
    final dones = <Session>[];

    for (final s in direct) {
      if (s.isLater) {
        laters.add(s);
      } else if (s.isDone || s.state == SessionState.ended) {
        dones.add(s);
      } else if (s.isSupervisor) {
        sups.add(s);
      } else if (s.currentTaskId.isNotEmpty) {
        tsks.add(s);
      } else {
        manuals.add(s);
      }
    }
    return (sups, tsks, manuals, laters, dones);
  }

  // Recursive tree builder
  FleetTreeFolderNode buildNode(String path, int depth) {
    final direct = sessionsByPath[path] ?? const [];
    final (sups, tsks, manuals, laters, dones) = partitionSessions(direct);

    // Find direct child paths
    final directChildren = <FleetTreeFolderNode>[];
    final prefix = '$path/';
    for (final candidate in allGroupPaths) {
      if (candidate.startsWith(prefix)) {
        final sub = candidate.substring(prefix.length);
        if (!sub.contains('/')) {
          directChildren.add(buildNode(candidate, depth + 1));
        }
      }
    }
    directChildren
        .sort((a, b) => a.name.toLowerCase().compareTo(b.name.toLowerCase()));

    final leafName = path.split('/').last;
    return FleetTreeFolderNode(
      path: path,
      name: leafName,
      depth: depth,
      supervisors: sups,
      tasks: tsks,
      manualSessions: manuals,
      laterSessions: laters,
      doneSessions: dones,
      children: directChildren,
    );
  }

  // Build root nodes (paths without '/')
  final sortedRootPaths = allGroupPaths.where((p) => !p.contains('/')).toList()
    ..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase()));

  final rootFolders = <FleetTreeFolderNode>[];
  for (final rootPath in sortedRootPaths) {
    final node = buildNode(rootPath, 0);
    if (viewMode == FleetViewMode.supervisors) {
      bool hasSupervisor(FleetTreeFolderNode n) {
        if (n.supervisors.isNotEmpty) return true;
        return n.children.any(hasSupervisor);
      }

      if (hasSupervisor(node)) {
        rootFolders.add(node);
      }
    } else {
      rootFolders.add(node);
    }
  }

  // Unassigned folder if any unassigned sessions
  if (unassigned.isNotEmpty) {
    final (sups, tsks, manuals, laters, dones) = partitionSessions(unassigned);
    rootFolders.add(FleetTreeFolderNode(
      path: 'Unassigned',
      name: 'Unassigned',
      depth: 0,
      supervisors: sups,
      tasks: tsks,
      manualSessions: manuals,
      laterSessions: laters,
      doneSessions: dones,
      children: const [],
    ));
  }

  return rootFolders;
});

/// Grouped sessions by project / category folder (upgraded with fallback to projectDisplayName)
final groupedSessionsProvider = Provider<Map<String, List<Session>>>((ref) {
  final filtered = ref.watch(filteredSessionsProvider);
  final nodes = ref.watch(fleetNodesProvider);
  final groups = <String, List<Session>>{};

  for (final s in filtered) {
    String groupKey = '';
    if (nodes.isNotEmpty) {
      groupKey = resolveSessionNodePath(s, nodes, s.host);
    }
    if (groupKey.isEmpty) {
      groupKey = s.projectDisplayName.isNotEmpty
          ? s.projectDisplayName
          : (s.nodePath.isNotEmpty ? s.nodePath : 'Unassigned');
    }
    groups.putIfAbsent(groupKey, () => []).add(s);
  }
  return groups;
});

// --- Work Board Tasks Provider ---

class TasksNotifier extends StateNotifier<List<WorkTask>> {
  final ApiClient _apiClient;
  final Ref _ref;
  ProviderSubscription<List<HostRecord>>? _hostsSub;

  TasksNotifier(this._apiClient, this._ref) : super(const []) {
    _initTasks();
    _hostsSub = _ref.listen<List<HostRecord>>(hostsListProvider, (prev, next) {
      if (prev == null ||
          prev.length != next.length ||
          prev.any((h) => !next.contains(h))) {
        refreshTasks();
      }
    });
  }

  @override
  void dispose() {
    _hostsSub?.close();
    super.dispose();
  }

  void setTasks(List<WorkTask> tasks) {
    state = tasks;
  }

  void _initTasks() {
    refreshTasks();
  }

  Future<void> refreshTasks() async {
    final hosts = _ref.read(hostsListProvider);
    if (hosts.isEmpty) {
      if (mounted) state = const [];
      return;
    }

    final merged = <String, WorkTask>{};
    for (final host in hosts) {
      if (!host.online) continue;
      final hostTasks = await _apiClient.getTasks(
        host.url,
        federated: true,
        authToken: host.authToken,
      );
      if (!mounted) return;
      if (hostTasks != null) {
        for (final task in hostTasks) {
          if (!merged.containsKey(task.id) ||
              task.updatedAt.isAfter(merged[task.id]!.updatedAt)) {
            merged[task.id] = task;
          }
        }
      }
    }

    if (mounted) {
      final list = merged.values.toList();
      list.sort((a, b) => b.updatedAt.compareTo(a.updatedAt));
      state = list;
    }
  }

  Future<bool> mergePR(String taskId, {String method = 'squash'}) async {
    final hosts = _ref.read(hostsListProvider);
    final targetHost = hosts.firstWhere(
      (h) => h.online,
      orElse: () => hosts.isNotEmpty
          ? hosts.first
          : HostRecord(
              name: 'local',
              url: 'http://127.0.0.1:7777',
              createdAt: DateTime.fromMillisecondsSinceEpoch(0),
            ),
    );
    final ok = await _apiClient.mergeTaskPR(
      targetHost.url,
      taskId: taskId,
      method: method,
      authToken: targetHost.authToken,
    );
    if (ok) {
      await refreshTasks();
    }
    return ok;
  }
}

final tasksProvider =
    StateNotifierProvider<TasksNotifier, List<WorkTask>>((ref) {
  final api = ref.watch(apiClientProvider);
  return TasksNotifier(api, ref);
});

final taskGroupFilterProvider = StateProvider<String>((ref) => 'all');

/// Discovered top-level group names from nodes and tasks (e.g. Modemobile, Personal)
final discoveredTaskGroupsProvider = Provider<List<String>>((ref) {
  final nodes = ref.watch(fleetNodesProvider);
  final tasks = ref.watch(tasksProvider);
  final groups = <String>{};

  for (final n in nodes) {
    if (n.path.isNotEmpty) {
      final top = n.path.split('/').first;
      if (top.isNotEmpty) groups.add(top);
    }
  }

  for (final t in tasks) {
    if (t.groupName.isNotEmpty) {
      groups.add(t.groupName);
    }
  }

  final sorted = groups.toList()
    ..sort((a, b) => a.toLowerCase().compareTo(b.toLowerCase()));
  return ['all', ...sorted];
});

/// Pillar-grouped tasks: 'NEW', 'IN_PROGRESS', 'REVIEW', 'DONE'
final pillarTasksProvider = Provider<Map<String, List<WorkTask>>>((ref) {
  final allTasks = ref.watch(tasksProvider);
  final groupFilter = ref.watch(taskGroupFilterProvider);

  final filtered = allTasks.where((t) {
    if (groupFilter != 'all') {
      if (t.groupName.toLowerCase() != groupFilter.toLowerCase()) {
        return false;
      }
    }
    return true;
  }).toList();

  final pillars = <String, List<WorkTask>>{
    'NEW': [],
    'IN_PROGRESS': [],
    'REVIEW': [],
    'DONE': [],
  };

  for (final t in filtered) {
    final status = t.status.toUpperCase();
    if (pillars.containsKey(status)) {
      pillars[status]!.add(t);
    } else {
      pillars['NEW']!.add(t);
    }
  }

  return pillars;
});

final audioCompanionServiceProvider =
    StateNotifierProvider<AudioCompanionService, AudioCompanionState>((ref) {
  final api = ref.watch(apiClientProvider);
  return AudioCompanionService(api);
});

// --- Attention & Plans Badges Counts ---

final attentionPendingSessionsProvider = Provider<List<Session>>((ref) {
  final sessions = ref.watch(fleetSessionsProvider);
  return sessions.where((s) => !s.archived && s.isBlocked).toList();
});

final inProgressSessionsProvider = Provider<List<Session>>((ref) {
  final sessions = ref.watch(fleetSessionsProvider);
  return sessions
      .where((s) => !s.archived && s.state == SessionState.working)
      .toList();
});

final attentionBadgeCountProvider = Provider<int>((ref) {
  final pending = ref.watch(attentionPendingSessionsProvider);
  return pending.length;
});

final pendingPlansCountProvider = Provider<int>((ref) {
  final plans = ref.watch(plansListProvider);
  return plans.where((p) => p.isPendingReview).length;
});
