import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/plan.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';

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
    test('Initial state contains seed decisions', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final audits = container.read(decisionAuditProvider);
      expect(audits, isNotEmpty);
      expect(audits.length, greaterThanOrEqualTo(4));
    });

    test('recordDecision prepends new entry to list', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final notifier = container.read(decisionAuditProvider.notifier);
      final initialCount = container.read(decisionAuditProvider).length;

      notifier.recordDecision(
        sessionId: 'claude-code:local:9999',
        sessionTitle: 'Test Feature Unit Tests',
        agent: 'Claude Code',
        host: 'local',
        actionType: 'allowed',
        summary: 'Allowed execution: flutter test',
      );

      final updated = container.read(decisionAuditProvider);
      expect(updated.length, equals(initialCount + 1));
      expect(updated.first.sessionId, equals('claude-code:local:9999'));
      expect(updated.first.actionType, equals('allowed'));
      expect(updated.first.summary, equals('Allowed execution: flutter test'));
    });
  });

  group('HostsNotifier & hostsListProvider Tests', () {
    late ApiClient mockApiClient;

    setUp(() {
      final mockClient = MockClient((request) async {
        if (request.url.path == '/v1/version') {
          return http.Response(jsonEncode({'version': 'v0.2.2'}), 200);
        }
        return http.Response('', 200);
      });
      mockApiClient = ApiClient(client: mockClient);
    });

    tearDown(() {
      mockApiClient.dispose();
    });

    test('Initial state contains default configured hosts', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
        ],
      );
      addTearDown(container.dispose);

      final hosts = container.read(hostsListProvider);
      expect(hosts.length, equals(3));
      expect(hosts.any((h) => h.name == 'local'), isTrue);
      expect(hosts.any((h) => h.name == 'devbox'), isTrue);
      expect(hosts.any((h) => h.name == 'cloud-gpu'), isTrue);
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
      expect(hosts.length, equals(4));
      expect(hosts.any((h) => h.name == 'staging-node'), isTrue);
    });

    test('removeHost removes host by name', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(hostsListProvider.notifier);
      notifier.removeHost('cloud-gpu');

      final hosts = container.read(hostsListProvider);
      expect(hosts.length, equals(2));
      expect(hosts.any((h) => h.name == 'cloud-gpu'), isFalse);
    });

    test('refreshHosts queries health check and updates host status', () async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(hostsListProvider.notifier);
      await notifier.refreshHosts();

      final hosts = container.read(hostsListProvider);
      expect(hosts.first.online, isTrue);
      expect(hosts.first.version, equals('v0.2.2'));
    });
  });

  group('PlansNotifier & plansListProvider Tests', () {
    test('Initial state contains default architectural plans', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final plans = container.read(plansListProvider);
      expect(plans.length, equals(3));
      expect(plans[0].id, equals('PLAN-2026-08-A'));
      expect(plans[0].isPendingReview, isTrue);
      expect(plans[1].id, equals('PLAN-2026-08-B'));
      expect(plans[1].isInProgress, isTrue);
      expect(plans[2].id, equals('PLAN-2026-08-C'));
      expect(plans[2].isCompleted, isTrue);
    });

    test('quickApprovePlan updates plan status to approved and clears callout', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final notifier = container.read(plansListProvider.notifier);
      notifier.quickApprovePlan('PLAN-2026-08-A');

      final plans = container.read(plansListProvider);
      final approvedPlan = plans.firstWhere((p) => p.id == 'PLAN-2026-08-A');

      expect(approvedPlan.status, equals(PlanStatus.approved));
      expect(approvedPlan.isApproved, isTrue);
      expect(approvedPlan.userReviewCallout, isEmpty);
      expect(approvedPlan.progressPct, equals(0.50));
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

    test('Initial state contains seed sessions across multiple hosts and states', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final sessions = container.read(fleetSessionsProvider);
      expect(sessions.length, equals(5));
      expect(sessions.any((s) => s.state == SessionState.working), isTrue);
      expect(sessions.any((s) => s.state == SessionState.blocked), isTrue);
      expect(sessions.any((s) => s.state == SessionState.idle), isTrue);
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

    test('restartSession updates state and triggers control restart', () async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(fleetSessionsProvider.notifier);
      await notifier.restartSession('codex:local:8488');

      final sessions = container.read(fleetSessionsProvider);
      final session = sessions.firstWhere((s) => s.id == 'codex:local:8488');
      expect(session.state, equals(SessionState.working));
      expect(session.activity, contains('Restarting'));
    });

    test('terminateSession sets state to ended and triggers control kill', () async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(fleetSessionsProvider.notifier);
      await notifier.terminateSession('claude-code:local:8492');

      final sessions = container.read(fleetSessionsProvider);
      final session = sessions.firstWhere((s) => s.id == 'claude-code:local:8492');
      expect(session.state, equals(SessionState.ended));
    });

    test('deleteSession removes session from local state', () async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final notifier = container.read(fleetSessionsProvider.notifier);
      await notifier.deleteSession('claude-code:devbox:8472');

      final sessions = container.read(fleetSessionsProvider);
      expect(sessions.any((s) => s.id == 'claude-code:devbox:8472'), isFalse);
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

    test('Tab 0 (All) returns all sessions', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetFilterIndexProvider.notifier).state = 0;
      final filtered = container.read(filteredSessionsProvider);
      expect(filtered.length, equals(5));
    });

    test('Tab 1 (Working) filters to working sessions only', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetFilterIndexProvider.notifier).state = 1;
      final filtered = container.read(filteredSessionsProvider);
      expect(filtered.every((s) => s.state == SessionState.working), isTrue);
      expect(filtered.length, equals(2));
    });

    test('Tab 2 (Blocked/Attention) filters to blocked sessions only', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetFilterIndexProvider.notifier).state = 2;
      final filtered = container.read(filteredSessionsProvider);
      expect(filtered.every((s) => s.isBlocked), isTrue);
      expect(filtered.length, equals(1));
    });

    test('Tab 3 (Idle) filters to idle sessions only', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(fleetFilterIndexProvider.notifier).state = 3;
      final filtered = container.read(filteredSessionsProvider);
      expect(filtered.every((s) => s.state == SessionState.idle), isTrue);
      expect(filtered.length, equals(2));
    });

    test('Search query matches title, agent, and branch', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      // Search for 'Stripe'
      container.read(fleetSearchQueryProvider.notifier).state = 'Stripe';
      final matchStripe = container.read(filteredSessionsProvider);
      expect(matchStripe.length, equals(1));
      expect(matchStripe.first.id, equals('claude-code:devbox:8472'));

      // Search for agent 'antigravity'
      container.read(fleetSearchQueryProvider.notifier).state = 'antigravity';
      final matchAgy = container.read(filteredSessionsProvider);
      expect(matchAgy.length, equals(2));

      // Search for git branch
      container.read(fleetSearchQueryProvider.notifier).state = 'tmux-pty';
      final matchBranch = container.read(filteredSessionsProvider);
      expect(matchBranch.length, equals(1));
      expect(matchBranch.first.id, equals('codex:local:8488'));
    });

    test('groupedSessionsProvider groups by projectDisplayName', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final grouped = container.read(groupedSessionsProvider);
      expect(grouped.containsKey('Mobile Engineering'), isTrue);
      expect(grouped.containsKey('ackbard'), isTrue);
      expect(grouped.containsKey('Payment & Billing'), isTrue);
      expect(grouped.containsKey('Acme Web Platform'), isTrue);
    });
  });

  group('Attention & Plans Derived Badges Providers Tests', () {
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

    test('attentionBadgeCountProvider counts pending blocked sessions', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final count = container.read(attentionBadgeCountProvider);
      expect(count, equals(1)); // 1 blocked session in seed data
    });

    test('pendingPlansCountProvider counts plans pending review', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final count = container.read(pendingPlansCountProvider);
      expect(count, equals(1)); // PLAN-2026-08-A is pending review in seed data
    });

    test('inProgressSessionsProvider lists working sessions', () {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      final inProgress = container.read(inProgressSessionsProvider);
      expect(inProgress.length, equals(2));
      expect(inProgress.every((s) => s.state == SessionState.working), isTrue);
    });
  });
}
