import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';

void main() {
  group('ApiClient Unit Tests', () {
    test('getSessions returns parsed sessions on 200 OK', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/sessions'));
        expect(request.method, equals('GET'));

        final body = jsonEncode([
          {
            'id': 'claude-code:local:1234',
            'agent': 'claude-code',
            'host': 'local',
            'native_id': '1234',
            'state': 1,
            'activity': 'Working on feature',
            'context_pct': 45,
          },
          {
            'id': 'antigravity:devbox:5678',
            'agent': 'antigravity',
            'host': 'devbox',
            'native_id': '5678',
            'state': 2,
            'blocked': {
              'kind': 'question',
              'reason': 'Confirm action',
            },
          },
        ]);

        return http.Response(body, 200, headers: {'content-type': 'application/json'});
      });

      final api = ApiClient(client: mockClient);
      final sessions = await api.getSessions('127.0.0.1:7777');

      expect(sessions.length, equals(2));
      expect(sessions[0].id, equals('claude-code:local:1234'));
      expect(sessions[0].agentDisplayName, equals('Claude Code'));
      expect(sessions[0].state, equals(SessionState.working));
      expect(sessions[1].id, equals('antigravity:devbox:5678'));
      expect(sessions[1].isBlocked, isTrue);
    });

    test('getSessions returns empty list on HTTP error or exception', () async {
      final mockClient = MockClient((request) async {
        return http.Response('Internal Server Error', 500);
      });

      final api = ApiClient(client: mockClient);
      final sessions = await api.getSessions('http://localhost:7777');

      expect(sessions, isEmpty);
    });

    test('respondToSession sends POST request with correct payload', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/sessions/respond'));
        expect(request.method, equals('POST'));
        expect(request.headers['Content-Type'], contains('application/json'));

        final body = jsonDecode(request.body) as Map<String, dynamic>;
        expect(body['id'], equals('claude-code:local:1234'));
        expect(body['action'], equals('allow'));
        expect(body['value'], equals('flutter pub add'));

        return http.Response(jsonEncode({'status': 'ok'}), 200);
      });

      final api = ApiClient(client: mockClient);
      final success = await api.respondToSession(
        'http://127.0.0.1:7777',
        id: 'claude-code:local:1234',
        action: 'allow',
        value: 'flutter pub add',
      );

      expect(success, isTrue);
    });

    test('respondToSession returns false on non-200 response', () async {
      final mockClient = MockClient((request) async {
        return http.Response('Bad Request', 400);
      });

      final api = ApiClient(client: mockClient);
      final success = await api.respondToSession(
        'http://127.0.0.1:7777',
        id: 'session-1',
        action: 'deny',
        value: 'blocked',
      );

      expect(success, isFalse);
    });

    test('controlSession dispatches POST with query parameters', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/sessions/control'));
        expect(request.method, equals('POST'));
        expect(request.url.queryParameters['id'], equals('sess-42'));
        expect(request.url.queryParameters['action'], equals('rename'));
        expect(request.url.queryParameters['new_name'], equals('New Title'));

        return http.Response(jsonEncode({'success': true}), 200);
      });

      final api = ApiClient(client: mockClient);
      final ok = await api.controlSession(
        'http://127.0.0.1:7777/',
        'sess-42',
        'rename',
        params: {'new_name': 'New Title'},
      );

      expect(ok, isTrue);
    });

    test('controlSession handles lifecycle actions like restart, kill, and delete', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.queryParameters['action'], equals('restart'));
        return http.Response('', 200);
      });

      final api = ApiClient(client: mockClient);
      final ok = await api.controlSession('http://127.0.0.1:7777', 'sess-1', 'restart');
      expect(ok, isTrue);
    });

    test('getHosts returns list of HostRecord objects', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/hosts'));
        final body = jsonEncode([
          {
            'name': 'local',
            'url': 'http://127.0.0.1:7777',
            'online': true,
            'latency_ms': 1,
            'tailscale_ip': '100.82.14.101',
          },
          {
            'name': 'devbox',
            'url': 'http://127.0.0.1:7778',
            'online': true,
            'latency_ms': 25,
            'tailscale_ip': '100.95.42.204',
          },
        ]);
        return http.Response(body, 200);
      });

      final api = ApiClient(client: mockClient);
      final hosts = await api.getHosts('http://127.0.0.1:7777');

      expect(hosts.length, equals(2));
      expect(hosts[0].name, equals('local'));
      expect(hosts[0].isLocal, isTrue);
      expect(hosts[1].name, equals('devbox'));
      expect(hosts[1].tailscaleIp, equals('100.95.42.204'));
    });

    test('getNodes returns decoded project tree nodes', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/nodes'));
        final body = jsonEncode([
          {'name': 'Mobile Client', 'type': 'project'},
          {'name': 'Backend Server', 'type': 'subproject'},
        ]);
        return http.Response(body, 200);
      });

      final api = ApiClient(client: mockClient);
      final nodes = await api.getNodes('http://127.0.0.1:7777');

      expect(nodes.length, equals(2));
      expect(nodes[0]['name'], equals('Mobile Client'));
    });

    test('getDocuments extracts nativeId from compound ID and returns document paths', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/sessions/8492/documents'));
        final body = jsonEncode(['PLAN.md', 'WALKTHROUGH.md']);
        return http.Response(body, 200);
      });

      final api = ApiClient(client: mockClient);
      final docs = await api.getDocuments('http://127.0.0.1:7777', 'claude-code:local:8492');

      expect(docs.length, equals(2));
      expect(docs, contains('PLAN.md'));
      expect(docs, contains('WALKTHROUGH.md'));
    });

    test('getPlanContent queries /v1/documents/content with query params and returns string body', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/documents/content'));
        expect(request.url.queryParameters['session_id'], equals('8492'));
        expect(request.url.queryParameters['filename'], equals('PLAN.md'));

        return http.Response('# Implementation Plan\n\n- Step 1\n- Step 2', 200);
      });

      final api = ApiClient(client: mockClient);
      final content = await api.getPlanContent('http://127.0.0.1:7777', '8492', 'PLAN.md');

      expect(content, contains('# Implementation Plan'));
    });

    test('getTranscript queries /v1/sessions/transcript with id and format', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/sessions/transcript'));
        expect(request.url.queryParameters['id'], equals('claude-code:local:8492'));
        expect(request.url.queryParameters['format'], equals('markdown'));
        return http.Response('# Transcript\nUser: Hello\nAgent: Hi', 200);
      });

      final api = ApiClient(client: mockClient);
      final transcript = await api.getTranscript('http://127.0.0.1:7777', 'claude-code:local:8492');

      expect(transcript, contains('# Transcript'));
      expect(transcript, contains('Agent: Hi'));
    });

    test('checkHostHealth measures latency and returns decoded version map', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/version'));
        return http.Response(jsonEncode({'version': 'v0.2.1', 'uptime': '4d 12h'}), 200);
      });

      final api = ApiClient(client: mockClient);
      final health = await api.checkHostHealth('http://127.0.0.1:7777');

      expect(health, isNotNull);
      expect(health!['version'], equals('v0.2.1'));
      expect(health.containsKey('latency_ms'), isTrue);
    });

    test('purgeSessions dispatches POST to /v1/maintenance/purge', () async {
      final mockClient = MockClient((request) async {
        expect(request.url.path, equals('/v1/maintenance/purge'));
        expect(request.method, equals('POST'));
        return http.Response(jsonEncode({'purged': 3}), 200);
      });

      final api = ApiClient(client: mockClient);
      final ok = await api.purgeSessions('http://127.0.0.1:7777');

      expect(ok, isTrue);
    });

    test('dispose cleans up resources safely', () {
      final mockClient = MockClient((request) async => http.Response('', 200));
      final api = ApiClient(client: mockClient);
      expect(() => api.dispose(), returnsNormally);
    });
  });
}
