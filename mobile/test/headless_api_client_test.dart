import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/headless_api_client.dart';

class FakeHttpClient extends http.BaseClient {
  http.Response Function(http.BaseRequest request)? handler;

  FakeHttpClient([this.handler]);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final response = handler != null
        ? handler!(request)
        : http.Response('{"status": "ok"}', 200,
            headers: {'content-type': 'application/json'});
    return http.StreamedResponse(
      Stream.value(response.bodyBytes),
      response.statusCode,
      headers: response.headers,
    );
  }
}

void main() {
  group('HeadlessApiClient Extension Tests', () {
    test('startAuthTool sends correct POST payload and parses response',
        () async {
      http.BaseRequest? capturedReq;
      String? capturedBody;

      final client = FakeHttpClient((req) {
        capturedReq = req;
        if (req is http.Request) capturedBody = req.body;
        return http.Response(
          jsonEncode({
            'status': 'started',
            'flow_id': 'flow_1',
            'auth_url': 'https://auth.notion.com',
            'recorded_port': 8080,
            'accepts_stdin': true,
          }),
          200,
        );
      });

      final api = ApiClient(client: client);
      final res = await api.startAuthTool('http://127.0.0.1:7777',
          sessionId: 'sess-1', target: 'mcp:notion');

      expect(capturedReq?.method, 'POST');
      expect(capturedReq?.url.path, '/v1/sessions/auth-tool');
      expect(
          capturedReq?.headers['content-type'], contains('application/json'));
      final decoded = jsonDecode(capturedBody!) as Map<String, dynamic>;
      expect(decoded['session_id'], 'sess-1');
      expect(decoded['target'], 'mcp:notion');
      expect(res.flowId, 'flow_1');
      expect(res.authUrl, 'https://auth.notion.com');
    });

    test('submitAuthCallback sends callback_url parameter', () async {
      String? capturedBody;
      final client = FakeHttpClient((req) {
        if (req is http.Request) capturedBody = req.body;
        return http.Response(
            jsonEncode({'status': 'authenticated', 'completed': true}), 200);
      });

      final api = ApiClient(client: client);
      final res = await api.submitAuthCallback(
        'http://127.0.0.1:7777',
        sessionId: 'sess-1',
        flowId: 'flow_1',
        callbackInput: 'http://localhost:8080/?code=xyz',
      );

      final decoded = jsonDecode(capturedBody!) as Map<String, dynamic>;
      expect(decoded['callback_url'], 'http://localhost:8080/?code=xyz');
      expect(decoded['flow_id'], 'flow_1');
      expect(res.completed, isTrue);
    });

    test('getMcpStatus encodes session ID and query parameters', () async {
      http.BaseRequest? capturedReq;
      final client = FakeHttpClient((req) {
        capturedReq = req;
        return http.Response(jsonEncode({'servers': [], 'plugins': []}), 200);
      });

      final api = ApiClient(client: client);
      await api.getMcpStatus('http://127.0.0.1:7777', 'sess:with:colon',
          refresh: true);

      expect(capturedReq?.method, 'GET');
      expect(
          capturedReq?.url.path, '/v1/sessions/sess%3Awith%3Acolon/mcp-status');
      expect(capturedReq?.url.queryParameters['refresh'], '1');
    });

    test('updateSessionConfig sends PATCH with JSON body', () async {
      http.BaseRequest? capturedReq;
      String? capturedBody;
      final client = FakeHttpClient((req) {
        capturedReq = req;
        if (req is http.Request) capturedBody = req.body;
        return http.Response(
          jsonEncode({
            'id': 's1',
            'agent': 'claude-code',
            'host': 'local',
            'native_id': 'n1',
            'cwd': '/w',
            'state': 1,
            'started_at': '2026-11-11T10:00:00Z',
            'last_event_at': '2026-11-11T10:00:00Z',
            'model': 'claude-3-7-sonnet',
          }),
          200,
        );
      });

      final api = ApiClient(client: client);
      final updated = await api.updateSessionConfig(
        'http://127.0.0.1:7777',
        's1',
        model: 'claude-3-7-sonnet',
        addDirs: ['/tmp/test'],
      );

      expect(capturedReq?.method, 'PATCH');
      expect(
          capturedReq?.headers['content-type'], contains('application/json'));
      final decoded = jsonDecode(capturedBody!) as Map<String, dynamic>;
      expect(decoded['model'], 'claude-3-7-sonnet');
      expect(decoded['add_dirs'], ['/tmp/test']);
      expect(updated.model, 'claude-3-7-sonnet');
    });

    test(
        'runSessionShell, clearSession, and compactSession send application/json',
        () async {
      final paths = <String>[];
      final contentTypes = <String>[];
      final client = FakeHttpClient((req) {
        paths.add(req.url.path);
        contentTypes.add(req.headers['content-type'] ?? '');
        return http.Response(jsonEncode({'status': 'ok'}), 200);
      });

      final api = ApiClient(client: client);
      await api.runSessionShell('http://127.0.0.1:7777', 's1', 'echo hi');
      await api.clearSession('http://127.0.0.1:7777', 's1');
      await api.compactSession('http://127.0.0.1:7777', 's1',
          instructions: 'summarize');

      expect(paths, [
        '/v1/sessions/s1/shell',
        '/v1/sessions/s1/clear',
        '/v1/sessions/s1/compact',
      ]);
      for (final ct in contentTypes) {
        expect(ct, contains('application/json'));
      }
    });

    test('throws ApiHttpException on non-2xx with error message', () async {
      final client = FakeHttpClient((req) {
        return http.Response(jsonEncode({'error': 'Target not found'}), 404);
      });

      final api = ApiClient(client: client);
      expect(
        () => api.getMcpStatus('http://127.0.0.1:7777', 's1'),
        throwsA(isA<ApiHttpException>()
            .having((e) => e.statusCode, 'statusCode', 404)
            .having((e) => e.message, 'message', 'Target not found')),
      );
    });
  });
}
