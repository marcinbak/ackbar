import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/plan.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';

final _testSessions = [
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

void main() {
  group('Navigation & Filter State Providers Tests', () {
    test('Initial filter and navigation index states', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      expect(container.read(mainTabNavIndexProvider), equals(0));
      expect(container.read(fleetSearchQueryProvider), equals(''));
      expect(container.read(fleetFilterIndexProvider), equals(0));
      expect(container.read(attentionFilterIndexProvider), equals(0));
      expect(container.read(plansFilterIndexProvider), equals(0));

      // Test state mutation
      container.read(mainTabNavIndexProvider.notifier).state = 2;
      container.read(fleetSearchQueryProvider.notifier).state = 'Stripe';
      container.read(fleetFilterIndexProvider.notifier).state = 1;

      expect(container.read(mainTabNavIndexProvider), equals(2));
      expect(container.read(fleetSearchQueryProvider), equals('Stripe'));
      expect(container.read(fleetFilterIndexProvider), equals(1));
    });
  });

  group('DecisionAuditNotifier & decisionAuditProvider Tests', () {
    test('Initial state starts empty (pure live state)', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final audits = container.read(decisionAuditProvider);
      expect(audits, isEmpty);
    });

    test('recordDecision prepends new entry to list', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final notifier = container.read(decisionAuditProvider.notifier);

      notifier.recordDecision(
        sessionId: 'claude-code:local:9999',
        sessionTitle: 'Test Feature Unit Tests',
        agent: 'Claude Code',
        host: 'local',
        actionType: 'allowed',
        summary: 'Allowed execution: flutter test',
      );

      final updated = container.read(decisionAuditProvider);
      expect(updated.length, equals(1));
      expect(updated.first.sessionId, equals('claude-code:local:9999'));
      expect(updated.first.actionType, equals('allowed'));
      expect(updated.first.summary, equals('Allowed execution: flutter test'));
    });
  });

  group('HostsNotifier & hostsListProvider Tests', () {
    late ApiClient mockApiClient;

    setUp(() {
      SharedPreferences.setMockInitialValues({});
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/version') {
          return http.Response(jsonEncode({'version': 'v0.2.2'}), 200);
        }
        return http.Response('[]', 200);
      });
      mockApiClient = ApiClient(client: mockClient);
    });

    tearDown(() {
      mockApiClient.dispose();
    });

    test('Initial state starts empty (pure live state)', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
        ],
      );
      addTearDown(container.dispose);

      final hosts = container.read(hostsListProvider);
      expect(hosts, isEmpty);
    });

    test('addHost adds host to state', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(hostsListProvider.notifier);
      final newHost = HostRecord(
        name: 'staging-node',
        url: 'http://10.0.0.99:7777',
        tailscaleIp: '100.99.1.1',
        createdAt: DateTime.now(),
      );

      notifier.addHost(newHost);
      final hosts = container.read(hostsListProvider);
      expect(hosts.length, equals(1));
      expect(hosts.first.name, equals('staging-node'));
    });

    test('removeHost removes host by name or url', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(hostsListProvider.notifier);
      notifier.addHost(HostRecord(
        name: 'test-host',
        url: 'http://100.117.71.84:7777',
        createdAt: DateTime.now(),
      ));
      expect(container.read(hostsListProvider).length, equals(1));

      notifier.removeHost('test-host');
      expect(container.read(hostsListProvider), isEmpty);
    });
  });

  group('PlansNotifier & plansListProvider Tests', () {
    test('Initial state starts empty and quickApprovePlan updates plan', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final notifier = container.read(plansListProvider.notifier);
      notifier.setPlans([
        PlanDocument(
          id: 'PLAN-TEST-1',
          title: 'Test Plan',
          agent: 'Claude Code',
          host: 'local',
          project: 'Ackbar',
          goal: 'Test goal',
          userReviewCallout: 'Warning',
          filesChanged: 2,
          addedLines: 50,
          deletedLines: 10,
          status: PlanStatus.pendingReview,
          createdAt: DateTime.now(),
        ),
      ]);

      expect(container.read(plansListProvider).length, equals(1));
      notifier.quickApprovePlan('PLAN-TEST-1');

      final approvedPlan = container.read(plansListProvider).first;
      expect(approvedPlan.status, equals(PlanStatus.approved));
      expect(approvedPlan.isApproved, isTrue);
    });
  });

  group('FleetSessionsNotifier & fleetSessionsProvider Tests', () {
    late ApiClient mockApiClient;
    late SSEClient mockSseClient;

    setUp(() {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/sessions') {
          return http.Response(jsonEncode([]), 200);
        }
        if (request.url.path == '/v1/sessions/respond') {
          return http.Response(jsonEncode({'status': 'ok'}), 200);
        }
        if (request.url.path == '/v1/sessions/control') {
          return http.Response(jsonEncode({'status': 'ok'}), 200);
        }
        return http.Response('', 200);
      });
      mockApiClient = ApiClient(client: mockClient);
      mockSseClient = SSEClient(client: mockClient);
    });

    tearDown(() {
      mockApiClient.dispose();
      mockSseClient.dispose();
    });

    test('respondToSession unblocks session optimistically and adds audit entry', () async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(fleetSessionsProvider.notifier);
      notifier.setSessions(_testSessions);

      final ok = await notifier.respondToSession(
        sessionId: 'antigravity:devbox:8491',
        action: 'allow',
        value: 'modernc.org/sqlite',
        answerSummary: 'Selected modernc.org/sqlite driver',
      );

      expect(ok, isTrue);

      final sessions = container.read(fleetSessionsProvider);
      final session = sessions.firstWhere((s) => s.id == 'antigravity:devbox:8491');
      expect(session.state, equals(SessionState.working));
      expect(session.blocked, isNull);

      final audits = container.read(decisionAuditProvider);
      expect(audits.first.sessionId, equals('antigravity:devbox:8491'));
      expect(audits.first.summary, equals('Selected modernc.org/sqlite driver'));
    });
  });

  group('filteredSessionsProvider & groupedSessionsProvider Tests', () {
    late ApiClient mockApiClient;
    late SSEClient mockSseClient;

    setUp(() {
      final mockClient = MockClient((request) async => http.Response('[]', 200));
      mockApiClient = ApiClient(client: mockClient);
      mockSseClient = SSEClient(client: mockClient);
    });

    tearDown(() {
      mockApiClient.dispose();
      mockSseClient.dispose();
    });

    test('Filter tabs work correctly with seeded sessions', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetSessionsProvider.notifier).setSessions(_testSessions);

      // Tab 0: All
      container.read(fleetFilterIndexProvider.notifier).state = 0;
      expect(container.read(filteredSessionsProvider).length, equals(5));

      // Tab 1: Working
      container.read(fleetFilterIndexProvider.notifier).state = 1;
      final working = container.read(filteredSessionsProvider);
      expect(working.length, equals(2));
      expect(working.every((s) => s.state == SessionState.working), isTrue);

      // Tab 2: Blocked / Attention
      container.read(fleetFilterIndexProvider.notifier).state = 2;
      final blocked = container.read(filteredSessionsProvider);
      expect(blocked.length, equals(1));
      expect(blocked.first.isBlocked, isTrue);

      // Tab 3: Idle
      container.read(fleetFilterIndexProvider.notifier).state = 3;
      final idle = container.read(filteredSessionsProvider);
      expect(idle.length, equals(2));
      expect(idle.every((s) => s.state == SessionState.idle), isTrue);
    });

    test('Search query matches title, agent, and branch', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetSessionsProvider.notifier).setSessions(_testSessions);

      // Search for 'Stripe'
      container.read(fleetSearchQueryProvider.notifier).state = 'Stripe';
      final matchStripe = container.read(filteredSessionsProvider);
      expect(matchStripe.length, equals(1));
      expect(matchStripe.first.id, equals('claude-code:devbox:8472'));
    });

    test('groupedSessionsProvider groups by projectDisplayName', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetSessionsProvider.notifier).setSessions(_testSessions);

      final grouped = container.read(groupedSessionsProvider);
      expect(grouped.containsKey('Mobile Engineering'), isTrue);
      expect(grouped.containsKey('ackbard'), isTrue);
      expect(grouped.containsKey('Payment & Billing'), isTrue);
      expect(grouped.containsKey('Acme Web Platform'), isTrue);
    });
  });
}
