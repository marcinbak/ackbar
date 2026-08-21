import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/host.dart';
import '../models/session.dart';
import '../models/transcript.dart';

/// HTTP Client communicating with ackbard daemon control plane endpoints.
class ApiClient {
  final http.Client _client;

  ApiClient({http.Client? client}) : _client = client ?? http.Client();

  String _cleanUrl(String base) {
    var u = base.trim();
    if (!u.startsWith('http://') && !u.startsWith('https://')) {
      u = 'http://$u';
    }
    return u.endsWith('/') ? u.substring(0, u.length - 1) : u;
  }

  Map<String, String> _headers([String? authToken, Map<String, String>? extra]) {
    final h = <String, String>{};
    if (authToken != null && authToken.isNotEmpty) {
      h['Authorization'] = 'Bearer $authToken';
      h['X-Ackbar-Token'] = authToken;
    }
    if (extra != null) {
      h.addAll(extra);
    }
    return h;
  }

  /// GET /v1/sessions: Retrieve all active and recorded sessions from a host
  Future<List<Session>> getSessions(String hostUrl, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions');
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> decoded = jsonDecode(response.body);
        return decoded.map((item) => Session.fromJson(item as Map<String, dynamic>)).toList();
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  /// POST /v1/sessions/respond: Answer prompts, allow/deny permissions for a session
  Future<bool> respondToSession(
    String hostUrl, {
    required String id,
    required String action,
    required String value,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/respond');
    try {
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode({
              'id': id,
              'action': action,
              'value': value,
            }),
          )
          .timeout(const Duration(seconds: 5));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// POST /v1/sessions/control: Execute control lifecycle actions (restart, kill, delete, archive, rename, move)
  Future<bool> controlSession(
    String hostUrl,
    String sessionID,
    String action, {
    Map<String, String>? params,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final queryParams = {
      'id': sessionID,
      'action': action,
      if (params != null) ...params,
    };
    final uri = Uri.parse('$clean/v1/sessions/control').replace(queryParameters: queryParams);
    try {
      final response = await _client.post(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 5));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// GET /v1/hosts: Retrieve configured remote hosts from the daemon
  Future<List<HostRecord>> getHosts(String hostUrl, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/hosts');
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> decoded = jsonDecode(response.body);
        return decoded.map((item) => HostRecord.fromJson(item as Map<String, dynamic>)).toList();
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  /// GET /v1/nodes: Retrieve tree project nodes
  Future<List<Map<String, dynamic>>> getNodes(String hostUrl, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/nodes');
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> decoded = jsonDecode(response.body);
        return decoded.cast<Map<String, dynamic>>();
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  /// GET /v1/sessions/{id}/documents: Retrieve list of markdown/proposal documents
  Future<List<String>> getDocuments(String hostUrl, String sessionID, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    var nativeId = sessionID;
    final parts = sessionID.split(':');
    if (parts.length == 3) {
      nativeId = parts[2];
    }
    final uri = Uri.parse('$clean/v1/sessions/$nativeId/documents');
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> decoded = jsonDecode(response.body);
        return decoded.map((e) => e.toString()).toList();
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  /// GET /v1/documents/content: Fetch specific plan or document file content
  Future<String> getPlanContent(String hostUrl, String sessionId, String filename, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/documents/content').replace(
      queryParameters: {
        'session_id': sessionId,
        'filename': filename,
      },
    );
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 5));
      if (response.statusCode == 200) {
        return response.body;
      }
      return '';
    } catch (_) {
      return '';
    }
  }

  /// GET /v1/sessions/transcript: Retrieve live/historic agent conversation transcript (markdown/ansi)
  Future<String> getTranscript(String hostUrl, String sessionId, {String format = 'markdown', String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/transcript').replace(
      queryParameters: {
        'id': sessionId,
        'format': format,
      },
    );
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 6));
      if (response.statusCode == 200) {
        return response.body;
      }
      return '';
    } catch (_) {
      return '';
    }
  }

  /// GET /v1/sessions/transcript: Retrieve structured transcript data for rich chat stream UI
  Future<TranscriptData?> getStructuredTranscript(String hostUrl, String sessionId, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/transcript').replace(
      queryParameters: {
        'id': sessionId,
        'format': 'json',
      },
    );
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 6));
      if (response.statusCode == 200) {
        final Map<String, dynamic> decoded = jsonDecode(response.body);
        return TranscriptData.fromJson(decoded);
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// GET /v1/version: Healthcheck and version check for a host
  Future<Map<String, dynamic>?> checkHostHealth(String hostUrl, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/version');
    final stopwatch = Stopwatch()..start();
    try {
      final response = await _client.get(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 5));
      stopwatch.stop();
      if (response.statusCode == 200) {
        final Map<String, dynamic> decoded = jsonDecode(response.body);
        decoded['latency_ms'] = stopwatch.elapsedMilliseconds;
        return decoded;
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// POST /v1/maintenance/purge: Purge and re-sync dead sessions
  Future<bool> purgeSessions(String hostUrl, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/maintenance/purge');
    try {
      final response = await _client.post(uri, headers: _headers(authToken)).timeout(const Duration(seconds: 6));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  void dispose() {
    _client.close();
  }
}
