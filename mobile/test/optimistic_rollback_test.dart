import 'dart:async';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';

Session _createSession({
  String id = 'claude-code:local:sess-1',
  SessionState state = SessionState.idle,
  bool blocked = false,
  bool archived = false,
  DateTime? lastEventAt,
}) {
  final now = lastEventAt ?? DateTime.utc(2026, 11, 1, 10, 0, 0);
  return Session(
    id: id,
    agent: 'claude-code',
    host: 'local',
    nativeId: 'sess-1',
    cwd: '/workspace',
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    blocked: blocked
        ? Blocked(
            kind: BlockKind.permission,
            reason: 'Permission needed',
            options: const ['allow', 'deny'],
            since: DateTime.utc(2026, 11, 1),
          )
        : null,
    archived: archived,
  );
}

HostRecord _createLocalHost() {
  return HostRecord(
    name: 'local',
    url: 'http://127.0.0.1:7777',
    createdAt: DateTime.utc(2026, 11, 1),
  );
}

void main() {
  group('Optimistic Mutation Rollback Tests', () {
    test('respondToSession succeeds and records decision audit entry',
        () async {
      final mockClient = MockClient((req) async {
        if (req.url.path == '/v1/sessions/respond') {
          return http.Response('{"ok":true}', 200);
        }
        return http.Response('Not found', 404);
      });

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initialSession =
          _createSession(state: SessionState.idle, blocked: true);
      container
          .read(fleetSessionsProvider.notifier)
          .setSessions([initialSession]);

      final ok = await container
          .read(fleetSessionsProvider.notifier)
          .respondToSession(sessionId: initialSession.id, action: 'allow');

      expect(ok, isTrue);

      final current = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == initialSession.id);
      expect(current.isBlocked, isFalse);
      expect(current.state, SessionState.working);

      final audit = container.read(decisionAuditProvider);
      expect(audit, hasLength(1));
      expect(audit.first.sessionId, initialSession.id);
      expect(audit.first.actionType, 'allow');
    });

    test('respondToSession rolls back on network failure without audit entry',
        () async {
      final mockClient = MockClient((req) async {
        return http.Response('Internal Error', 500);
      });

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initialSession =
          _createSession(state: SessionState.idle, blocked: true);
      container
          .read(fleetSessionsProvider.notifier)
          .setSessions([initialSession]);

      final ok = await container
          .read(fleetSessionsProvider.notifier)
          .respondToSession(sessionId: initialSession.id, action: 'allow');

      expect(ok, isFalse);

      final rolledBack = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == initialSession.id);
      expect(rolledBack.isBlocked, isTrue);
      expect(rolledBack.state, SessionState.idle);

      final audit = container.read(decisionAuditProvider);
      expect(audit, isEmpty);
    });

    test('respondToSession failure preserves newer authoritative SSE instance',
        () async {
      final completer = Completer<http.Response>();
      final mockClient = MockClient((req) async => completer.future);

      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initial = _createSession(state: SessionState.idle, blocked: true);
      container.read(fleetSessionsProvider.notifier).setSessions([initial]);

      // Start mutation in-flight
      final future = container
          .read(fleetSessionsProvider.notifier)
          .respondToSession(sessionId: initial.id, action: 'allow');

      // Newer authoritative SSE arrives mid-request
      final newerSseSession = initial.copyWith(
        state: SessionState.working,
        clearBlocked: true,
        activity: 'Updated via SSE event',
        lastEventAt: DateTime.utc(2026, 11, 1, 10, 5, 0),
      );
      container
          .read(fleetSessionsProvider.notifier)
          .upsertSession(newerSseSession);

      // Now network request fails
      completer.complete(http.Response('500 Error', 500));
      final ok = await future;
      expect(ok, isFalse);

      // Verify newer SSE state was NOT overwritten by rollback
      final sessionAfter = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == initial.id);
      expect(sessionAfter.activity, 'Updated via SSE event');
      expect(sessionAfter.lastEventAt, newerSseSession.lastEventAt);
    });

    test('terminateSession rolls back state on network failure', () async {
      final mockClient = MockClient((req) async => http.Response('Error', 500));
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initial = _createSession(state: SessionState.working);
      container.read(fleetSessionsProvider.notifier).setSessions([initial]);

      final ok = await container
          .read(fleetSessionsProvider.notifier)
          .terminateSession(initial.id);
      expect(ok, isFalse);

      final current = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == initial.id);
      expect(current.state, SessionState.working);
    });

    test('deleteSession restores session on network failure', () async {
      final mockClient = MockClient((req) async => http.Response('Error', 500));
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initial = _createSession(id: 'sess-delete-1');
      container.read(fleetSessionsProvider.notifier).setSessions([initial]);

      final ok = await container
          .read(fleetSessionsProvider.notifier)
          .deleteSession(initial.id);
      expect(ok, isFalse);

      final sessions = container.read(fleetSessionsProvider);
      expect(sessions.any((s) => s.id == initial.id), isTrue);
    });

    test('archiveSession rolls back archived status on network failure',
        () async {
      final mockClient = MockClient((req) async => http.Response('Error', 500));
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initial = _createSession(archived: false);
      container.read(fleetSessionsProvider.notifier).setSessions([initial]);

      final ok = await container
          .read(fleetSessionsProvider.notifier)
          .archiveSession(initial.id);
      expect(ok, isFalse);

      final current = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == initial.id);
      expect(current.archived, isFalse);
    });

    test('resumeSession rolls back to ended state on network failure',
        () async {
      final mockClient = MockClient((req) async => http.Response('Error', 500));
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(ApiClient(client: mockClient)),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts([_createLocalHost()]);
      final initial = _createSession(state: SessionState.ended);
      container.read(fleetSessionsProvider.notifier).setSessions([initial]);

      final ok = await container
          .read(fleetSessionsProvider.notifier)
          .resumeSession(initial.id);
      expect(ok, isFalse);

      final current = container
          .read(fleetSessionsProvider)
          .firstWhere((s) => s.id == initial.id);
      expect(current.state, SessionState.ended);
    });
  });
}
