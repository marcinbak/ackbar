import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
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
  DecisionAuditNotifier()
      : super([
          DecisionAuditEntry(
            id: 'dec-001',
            sessionId: 'claude-code:local:8492',
            sessionTitle: 'Scaffold mobile Flutter UI shell',
            agent: 'Claude Code',
            host: 'local',
            actionType: 'allowed',
            summary: 'Allowed execution: flutter pub add flutter_riverpod',
            timestamp: DateTime.now().subtract(const Duration(minutes: 18)),
          ),
          DecisionAuditEntry(
            id: 'dec-002',
            sessionId: 'antigravity:gpu-box:8491',
            sessionTitle: 'SQLite schema migrations',
            agent: 'Antigravity',
            host: 'gpu-box',
            actionType: 'answered',
            summary: 'Selected: Option 2 (Keep SQLite with modernc.org/sqlite)',
            timestamp: DateTime.now().subtract(const Duration(minutes: 42)),
          ),
          DecisionAuditEntry(
            id: 'dec-003',
            sessionId: 'codex:local:8488',
            sessionTitle: 'Tmux process supervisor pty wrapper',
            agent: 'OpenAI Codex',
            host: 'local',
            actionType: 'approved_plan',
            summary: 'Approved Architectural Plan: PLAN-2026-08-B',
            timestamp: DateTime.now().subtract(const Duration(hours: 2, minutes: 15)),
          ),
          DecisionAuditEntry(
            id: 'dec-004',
            sessionId: 'claude-code:devbox:8455',
            sessionTitle: 'Payment & Billing stripe webhook retry logic',
            agent: 'Claude Code',
            host: 'devbox',
            actionType: 'denied',
            summary: 'Denied permission: rm -rf /var/log/stripe-events',
            timestamp: DateTime.now().subtract(const Duration(hours: 5)),
          ),
        ]);

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

  HostsNotifier(this._apiClient)
      : super([
          HostRecord(
            name: 'local',
            url: 'http://127.0.0.1:7777',
            sshTarget: 'localhost',
            remoteCwd: '~/Work/Ackbar',
            online: true,
            latencyMs: 1,
            version: 'v0.2.1',
            uptime: '5d 14h',
            sessionsCount: 3,
            tailscaleIp: '100.82.14.101',
            createdAt: DateTime.now().subtract(const Duration(days: 30)),
          ),
          HostRecord(
            name: 'devbox',
            url: 'http://127.0.0.1:7778',
            sshTarget: 'dev@devbox.internal',
            remoteCwd: '~/Development/ackbard',
            online: true,
            latencyMs: 24,
            version: 'v0.2.1',
            uptime: '18d 02h',
            sessionsCount: 2,
            tailscaleIp: '100.95.42.204',
            createdAt: DateTime.now().subtract(const Duration(days: 20)),
          ),
          HostRecord(
            name: 'cloud-gpu',
            url: 'http://127.0.0.1:7779',
            sshTarget: 'root@cloud-gpu.node',
            remoteCwd: '/opt/work',
            online: false,
            latencyMs: 180,
            version: 'v0.2.0',
            uptime: 'Offline (2h ago)',
            sessionsCount: 0,
            tailscaleIp: '100.104.99.12',
            createdAt: DateTime.now().subtract(const Duration(days: 10)),
          ),
        ]);

  Future<void> refreshHosts() async {
    final updated = <HostRecord>[];
    for (final h in state) {
      final health = await _apiClient.checkHostHealth(h.url);
      if (health != null) {
        updated.add(h.copyWith(
          online: true,
          latencyMs: health['latency_ms'] as int? ?? h.latencyMs,
          version: health['version'] as String? ?? h.version,
        ));
      } else {
        updated.add(h);
      }
    }
    state = updated;
  }

  void addHost(HostRecord host) {
    state = [...state, host];
  }

  void removeHost(String name) {
    state = state.where((h) => h.name != name).toList();
  }
}

final hostsListProvider = StateNotifierProvider<HostsNotifier, List<HostRecord>>((ref) {
  final api = ref.watch(apiClientProvider);
  return HostsNotifier(api);
});

// --- Plans Provider ---

class PlansNotifier extends StateNotifier<List<PlanDocument>> {
  PlansNotifier()
      : super([
          PlanDocument(
            id: 'PLAN-2026-08-A',
            title: 'Mobile Client Flutter High-Fidelity Views & Theme Integration',
            agent: 'Claude Code',
            host: 'local',
            project: 'Mobile Engineering',
            goal: 'Implement all 4 mobile tabs (Fleet, Attention, Plans, Hosts) matching Stitch designs, decoupled theme extensions, and Riverpod control plane.',
            userReviewCallout: '⚠️ HUMAN REVIEW REQUIRED: Adding interactive response actions directly invokes POST /v1/sessions/respond to unblock live sessions.',
            filesChanged: 14,
            addedLines: 842,
            deletedLines: 68,
            verificationSteps: [
              '1. Verify state models and API client contract with daemon endpoints.',
              '2. Verify Fleet folder collapsibility and session action bottom sheets.',
              '3. Verify Attention subviews (Pending PageView, In-Progress, History audit log).',
              '4. Verify Plans diff viewer syntax highlighting and approve button.',
              '5. Verify Hosts tailnet banner and Add Host dialog.',
            ],
            status: PlanStatus.pendingReview,
            progressPct: 0.25,
            currentStep: 'Step 2: Scaffolding high-fidelity screen implementations',
            testSummary: '',
            diffContent: '''diff --git a/mobile/lib/features/fleet/presentation/fleet_screen.dart b/mobile/lib/features/fleet/presentation/fleet_screen.dart
--- a/mobile/lib/features/fleet/presentation/fleet_screen.dart
+++ b/mobile/lib/features/fleet/presentation/fleet_screen.dart
@@ -40,12 +40,28 @@ class FleetScreen extends ConsumerStatefulWidget {
+  // Collapsible project folders & rich session cards
+  final Map<String, List<Session>> groupedProjects;
+  final ValueNotifier<Set<String>> collapsedFolders;
+
+  void toggleFolder(String folderName) {
+    if (collapsedFolders.value.contains(folderName)) {
+      collapsedFolders.value.remove(folderName);
+    } else {
+      collapsedFolders.value.add(folderName);
+    }
+  }
-  final List<MockAgentSession> _sessions = const [];
+  final List<Session> liveSessions = ref.watch(filteredSessionsProvider);
''',
            createdAt: DateTime.now().subtract(const Duration(minutes: 35)),
          ),
          PlanDocument(
            id: 'PLAN-2026-08-B',
            title: 'Daemon SSE Event Hub & SQLite Multi-Agent Persistence',
            agent: 'Antigravity',
            host: 'gpu-box',
            project: 'Infrastructure / ackbard',
            goal: 'Persistent state storage for Claude Code, Codex, and Antigravity hooks with automated SSE broadcast to mobile and TUI frontends.',
            userReviewCallout: '',
            filesChanged: 8,
            addedLines: 520,
            deletedLines: 44,
            verificationSteps: [
              '1. Test SQLite pure Go CGO-free migration on Linux and macOS.',
              '2. Verify SSE /v1/events subscription latency under 5ms.',
              '3. Ensure process supervision monitors active tmux sessions reliably.',
            ],
            status: PlanStatus.inProgress,
            progressPct: 0.66,
            currentStep: 'Step 4/5: Running daemon concurrency test suite & verifying API contracts',
            testSummary: '',
            diffContent: '''diff --git a/internal/daemon/server.go b/internal/daemon/server.go
--- a/internal/daemon/server.go
+++ b/internal/daemon/server.go
@@ -340,6 +340,18 @@ func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
+	w.Header().Set("Content-Type", "text/event-stream")
+	w.Header().Set("Cache-Control", "no-cache")
+	w.Header().Set("Connection", "keep-alive")
+	flusher.Flush()
''',
            createdAt: DateTime.now().subtract(const Duration(hours: 2, minutes: 10)),
          ),
          PlanDocument(
            id: 'PLAN-2026-08-C',
            title: 'Voice Companion Conversational Audio Briefing & Piper TTS Engine',
            agent: 'Claude Code',
            host: 'local',
            project: 'Acme Web Platform',
            goal: 'Add conversational hands-free audio briefings for pending attention queue items with voice approval speech synthesis.',
            userReviewCallout: '',
            filesChanged: 6,
            addedLines: 310,
            deletedLines: 15,
            verificationSteps: [
              '1. Verify Piper neural TTS model loading on macOS ARM64.',
              '2. Test voice briefing trigger on Attention screen pull-to-refresh.',
            ],
            status: PlanStatus.completed,
            progressPct: 1.0,
            currentStep: 'Completed: All tests passing',
            testSummary: '✅ 12/12 Speech tests passed • Audio latency < 120ms',
            diffContent: '',
            createdAt: DateTime.now().subtract(const Duration(days: 1)),
          ),
        ]);

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

// --- Fleet Sessions Provider with Live + Mock Hybrid Persistence ---

class FleetSessionsNotifier extends StateNotifier<List<Session>> {
  final ApiClient _apiClient;
  final Ref _ref;
  StreamSubscription<Session>? _sseSub;

  FleetSessionsNotifier(this._apiClient, this._ref) : super(_initialMockSessions) {
    _initLiveSync();
  }

  static List<Session> get _initialMockSessions => [
        Session(
          id: 'claude-code:local:8492',
          agent: 'claude-code',
          host: 'local',
          nativeId: '8492',
          cwd: '~/Work/Ackbar/mobile',
          projectKey: 'Mobile Engineering',
          nodePath: 'Mobile Engineering/Flutter Client',
          gitBranch: 'feat/mobile-tabs',
          state: SessionState.working,
          activity: 'Compiling high-fidelity Flutter screens & theme widgets...',
          contextPct: 71,
          startedAt: DateTime.now().subtract(const Duration(minutes: 14)),
          lastEventAt: DateTime.now().subtract(const Duration(seconds: 40)),
          managed: true,
          tmuxName: 'ackbar-claude-8492',
          pid: 48210,
          customTitle: 'Scaffold mobile Flutter UI shell & Stitch theme tokens',
          firstPrompt: 'Implement the complete, high-fidelity screens for all 4 mobile tabs (Fleet, Attention, Plans, Hosts)',
        ),
        Session(
          id: 'antigravity:devbox:8491',
          agent: 'antigravity',
          host: 'devbox',
          nativeId: '8491',
          cwd: '~/Development/ackbard/internal/daemon',
          projectKey: 'Infrastructure / ackbard',
          nodePath: 'Infrastructure / ackbard/Backend Server',
          gitBranch: 'feat/sqlite-sse',
          state: SessionState.blocked,
          blocked: Blocked(
            kind: BlockKind.question,
            reason: 'Database schema migration requires database driver confirmation',
            since: DateTime.now().subtract(const Duration(minutes: 8)),
            question: 'Which SQLite driver implementation should be used for pure Go cross-compilation without CGO dependencies?',
            options: [
              'modernc.org/sqlite (Pure Go, no CGO, Recommended)',
              'mattn/go-sqlite3 (Requires CGO, faster on Linux)',
              'glebarez/go-sqlite (Pure Go alternative)',
            ],
          ),
          activity: 'Waiting for user choice on SQLite driver...',
          contextPct: 88,
          startedAt: DateTime.now().subtract(const Duration(minutes: 48)),
          lastEventAt: DateTime.now().subtract(const Duration(minutes: 8)),
          managed: true,
          tmuxName: 'ackbar-agy-8491',
          pid: 59302,
          customTitle: 'SQLite schema migrations & multi-agent persistence',
          firstPrompt: 'Design pure Go SQLite database tables for multi-host agent supervision',
        ),
        Session(
          id: 'codex:local:8488',
          agent: 'codex',
          host: 'local',
          nativeId: '8488',
          cwd: '~/Work/Ackbar/internal/tmux',
          projectKey: 'Infrastructure / ackbard',
          nodePath: 'Infrastructure / ackbard/Tmux Supervisor',
          gitBranch: 'refactor/tmux-pty',
          state: SessionState.idle,
          activity: 'Completed tmux pty session spawn wrapper tests',
          contextPct: 35,
          startedAt: DateTime.now().subtract(const Duration(hours: 1, minutes: 15)),
          lastEventAt: DateTime.now().subtract(const Duration(minutes: 22)),
          managed: true,
          tmuxName: 'ackbar-codex-8488',
          pid: 31044,
          customTitle: 'Refactor tmux process supervisor pty wrapper',
        ),
        Session(
          id: 'claude-code:devbox:8472',
          agent: 'claude-code',
          host: 'devbox',
          nativeId: '8472',
          cwd: '~/Work/AcmePlatform/billing',
          projectKey: 'Payment & Billing',
          nodePath: 'Payment & Billing/Stripe Webhooks',
          gitBranch: 'fix/webhook-retry',
          state: SessionState.working,
          activity: 'Running test: go test -v ./internal/billing/... (Step 3/5)',
          contextPct: 54,
          startedAt: DateTime.now().subtract(const Duration(minutes: 6)),
          lastEventAt: DateTime.now().subtract(const Duration(seconds: 15)),
          managed: true,
          tmuxName: 'ackbar-claude-8472',
          pid: 12049,
          customTitle: 'Stripe webhook idempotency and retry queue',
        ),
        Session(
          id: 'antigravity:local:8460',
          agent: 'antigravity',
          host: 'local',
          nativeId: '8460',
          cwd: '~/Work/AcmePlatform/web',
          projectKey: 'Acme Web Platform',
          nodePath: 'Acme Web Platform/Frontend React',
          gitBranch: 'main',
          state: SessionState.idle,
          activity: 'Waiting for new user prompt',
          contextPct: 22,
          startedAt: DateTime.now().subtract(const Duration(hours: 3)),
          lastEventAt: DateTime.now().subtract(const Duration(minutes: 45)),
          managed: false,
          pid: 29012,
          customTitle: 'React 19 Server Components Migration',
        ),
      ];

  void _initLiveSync() async {
    final hosts = _ref.read(hostsListProvider);
    final sse = _ref.read(sseClientProvider);

    _sseSub = sse.subscribeMultipleHosts(hosts).listen((liveSess) {
      _upsertSession(liveSess);
    });

    // Initial fetch from online hosts
    for (final host in hosts) {
      if (!host.online) continue;
      final remoteSessions = await _apiClient.getSessions(host.url);
      if (remoteSessions.isNotEmpty) {
        for (final s in remoteSessions) {
          _upsertSession(s);
        }
      }
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

    // Find target host URL
    final hosts = _ref.read(hostsListProvider);
    final targetHost = hosts.firstWhere(
      (h) => h.name == session.host,
      orElse: () => hosts.first,
    );

    // Dispatch HTTP POST /v1/sessions/respond
    final ok = await _apiClient.respondToSession(
      targetHost.url,
      id: session.id,
      action: action,
      value: value,
    );
    return ok;
  }

  Future<void> restartSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final hosts = _ref.read(hostsListProvider);
    final targetHost = hosts.firstWhere((h) => h.name == session.host, orElse: () => hosts.first);

    _upsertSession(session.copyWith(
      state: SessionState.working,
      activity: 'Restarting session in tmux...',
      lastEventAt: DateTime.now(),
    ));

    await _apiClient.controlSession(targetHost.url, session.id, 'restart');
  }

  Future<void> terminateSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final hosts = _ref.read(hostsListProvider);
    final targetHost = hosts.firstWhere((h) => h.name == session.host, orElse: () => hosts.first);

    _upsertSession(session.copyWith(
      state: SessionState.ended,
      activity: 'Terminated by user',
      lastEventAt: DateTime.now(),
    ));

    await _apiClient.controlSession(targetHost.url, session.id, 'kill');
  }

  Future<void> deleteSession(String sessionId) async {
    final session = state.firstWhere((s) => s.id == sessionId);
    final hosts = _ref.read(hostsListProvider);
    final targetHost = hosts.firstWhere((h) => h.name == session.host, orElse: () => hosts.first);

    state = state.where((s) => s.id != sessionId).toList();
    await _apiClient.controlSession(targetHost.url, session.id, 'delete');
  }

  @override
  void dispose() {
    _sseSub?.cancel();
    super.dispose();
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

  return allSessions.where((s) {
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
  return sessions.where((s) => s.isBlocked).toList();
});

final inProgressSessionsProvider = Provider<List<Session>>((ref) {
  final sessions = ref.watch(fleetSessionsProvider);
  return sessions.where((s) => s.state == SessionState.working).toList();
});

final attentionBadgeCountProvider = Provider<int>((ref) {
  final pending = ref.watch(attentionPendingSessionsProvider);
  return pending.length;
});

final pendingPlansCountProvider = Provider<int>((ref) {
  final plans = ref.watch(plansListProvider);
  return plans.where((p) => p.isPendingReview).length;
});
