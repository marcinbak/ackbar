import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/host.dart';
import '../models/session.dart';
import '../models/subagent.dart';
import '../models/transcript.dart';
import '../models/tree_node.dart';
import '../models/work_task.dart';

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

  Map<String, String> _headers(
      [String? authToken, Map<String, String>? extra]) {
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

  /// GET /v1/sessions: Retrieve all active and recorded sessions from a host.
  /// Returns null if the HTTP request fails, times out, or returns a non-200 status.
  Future<List<Session>?> getSessionsOrNull(
    String hostUrl, {
    String? authToken,
    String? hostName,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions');
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 10));
      if (response.statusCode == 200) {
        final decoded = jsonDecode(response.body);
        if (decoded is! List) return <Session>[];
        final normalizedHost = (hostName != null && hostName.trim().isNotEmpty)
            ? hostName.trim()
            : null;
        return decoded.map((item) {
          final map = Map<String, dynamic>.from(item as Map<String, dynamic>);
          if (normalizedHost != null) {
            map['host'] = normalizedHost;
          }
          return Session.fromJson(map);
        }).toList();
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// GET /v1/sessions: Retrieve all active and recorded sessions from a host
  Future<List<Session>> getSessions(
    String hostUrl, {
    String? authToken,
    String? hostName,
  }) async {
    return await getSessionsOrNull(
          hostUrl,
          authToken: authToken,
          hostName: hostName,
        ) ??
        [];
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
    final uri = Uri.parse('$clean/v1/sessions/control')
        .replace(queryParameters: queryParams);
    try {
      final response = await _client
          .post(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 5));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// POST /v1/sessions/control?id=...&action=read: Mark session state as read
  Future<bool> markSessionRead(String hostUrl, String sessionID,
      {String? authToken}) async {
    return controlSession(hostUrl, sessionID, 'read', authToken: authToken);
  }

  /// POST /v1/sessions/handover: Trigger automated session handover and context rotation
  Future<bool> handoverSession(
    String hostUrl,
    String sessionID, {
    String strategy = 'in_place',
    String? customInstruction,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/handover');
    try {
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode({
              'id': sessionID,
              'strategy': strategy,
              if (customInstruction != null && customInstruction.isNotEmpty)
                'custom_instruction': customInstruction,
            }),
          )
          .timeout(const Duration(seconds: 5));
      return response.statusCode == 200 || response.statusCode == 202;
    } catch (_) {
      return false;
    }
  }

  /// GET /v1/hosts: Retrieve configured remote hosts from the daemon
  Future<List<HostRecord>> getHosts(String hostUrl, {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/hosts');
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> decoded = jsonDecode(response.body);
        return decoded
            .map((item) => HostRecord.fromJson(item as Map<String, dynamic>))
            .toList();
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  /// GET /v1/sessions/{id}/documents: Retrieve list of markdown/proposal documents
  Future<List<String>> getDocuments(String hostUrl, String sessionID,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    var nativeId = sessionID;
    final parts = sessionID.split(':');
    if (parts.length == 3) {
      nativeId = parts[2];
    }
    final uri = Uri.parse('$clean/v1/sessions/$nativeId/documents');
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 4));
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
  Future<String> getPlanContent(
      String hostUrl, String sessionId, String filename,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/documents/content').replace(
      queryParameters: {
        'session_id': sessionId,
        'filename': filename,
      },
    );
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 5));
      if (response.statusCode == 200) {
        return response.body;
      }
      return '';
    } catch (_) {
      return '';
    }
  }

  /// GET /v1/sessions/transcript: Retrieve live/historic agent conversation transcript (markdown/ansi).
  /// Returns null if the request fails, times out, or returns a non-200 status.
  Future<String?> getTranscriptOrNull(String hostUrl, String sessionId,
      {String format = 'markdown', String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/transcript').replace(
      queryParameters: {
        'id': sessionId,
        'format': format,
      },
    );
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 10));
      if (response.statusCode == 200) {
        return response.body;
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// GET /v1/sessions/transcript: Retrieve live/historic agent conversation transcript (markdown/ansi)
  Future<String> getTranscript(String hostUrl, String sessionId,
      {String format = 'markdown', String? authToken}) async {
    return await getTranscriptOrNull(
          hostUrl,
          sessionId,
          format: format,
          authToken: authToken,
        ) ??
        '';
  }

  /// GET /v1/sessions/transcript: Retrieve structured transcript data for rich chat stream UI
  Future<TranscriptData?> getStructuredTranscript(
      String hostUrl, String sessionId,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/transcript').replace(
      queryParameters: {
        'id': sessionId,
        'format': 'json',
      },
    );
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 10));
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
  Future<Map<String, dynamic>?> checkHostHealth(String hostUrl,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/version');
    final stopwatch = Stopwatch()..start();
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 5));
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
      final response = await _client
          .post(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 6));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// POST /v1/sessions/prompt: Dispatch a user prompt to a headless or tmux session
  Future<bool> sendPrompt(String hostUrl, String sessionId, String prompt,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/prompt');
    try {
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode({'session_id': sessionId, 'prompt': prompt}),
          )
          .timeout(const Duration(seconds: 10));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// POST /v1/sessions/cancel: Cancel active turn for a session
  Future<bool> cancelTurn(String hostUrl, String sessionId,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/cancel');
    try {
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode({'session_id': sessionId}),
          )
          .timeout(const Duration(seconds: 5));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// POST /v1/sessions/take-wheel: Seamless handoff from headless session into live tmux session
  Future<Map<String, dynamic>?> takeWheel(String hostUrl, String sessionId,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/take-wheel');
    try {
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode({'session_id': sessionId}),
          )
          .timeout(const Duration(seconds: 8));
      if (response.statusCode == 200) {
        return jsonDecode(response.body) as Map<String, dynamic>;
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// GET /v1/sessions/subagents: Retrieve active/completed subagents spawned by a session
  Future<List<SubagentInfo>> getSubagents(String hostUrl, String sessionId,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/subagents')
        .replace(queryParameters: {'id': sessionId});
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 5));
      if (response.statusCode == 200) {
        final Map<String, dynamic> decoded = jsonDecode(response.body);
        if (decoded['subagents'] is List) {
          final list = decoded['subagents'] as List;
          return list
              .map(
                  (item) => SubagentInfo.fromJson(item as Map<String, dynamic>))
              .toList();
        }
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  /// GET /v1/nodes: Retrieve registered project tree nodes
  Future<List<TreeNode>?> getNodes(
    String hostUrl, {
    bool federated = true,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/nodes')
        .replace(queryParameters: {'federated': federated ? 'true' : 'false'});
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 8));
      if (response.statusCode == 200) {
        final decoded = jsonDecode(response.body);
        if (decoded is! List) return <TreeNode>[];
        return decoded
            .map((item) => TreeNode.fromJson(item as Map<String, dynamic>))
            .toList();
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// GET /v1/tasks: Retrieve tasks from work board
  Future<List<WorkTask>?> getTasks(
    String hostUrl, {
    bool federated = true,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/tasks')
        .replace(queryParameters: {'federated': federated ? 'true' : 'false'});
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 8));
      if (response.statusCode == 200) {
        final decoded = jsonDecode(response.body);
        if (decoded is! List) return <WorkTask>[];
        return decoded
            .map((item) => WorkTask.fromJson(item as Map<String, dynamic>))
            .toList();
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// POST /v1/tasks/merge-pr: Squash and merge a task PR
  Future<bool> mergeTaskPR(
    String hostUrl, {
    required String taskId,
    String method = 'squash',
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/tasks/merge-pr');
    try {
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode({
              'task_id': taskId,
              'method': method,
            }),
          )
          .timeout(const Duration(seconds: 15));
      return response.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// POST /v1/sessions/summarize-speech: Produce structural/LLM spoken briefing
  Future<Map<String, dynamic>?> summarizeSpeech(
    String hostUrl, {
    required String sessionId,
    int? messageIndex,
    required String content,
    bool forceRefresh = false,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/summarize-speech');
    try {
      final payload = <String, dynamic>{
        'session_id': sessionId,
        if (messageIndex != null) 'message_index': messageIndex,
        'content': content,
        if (forceRefresh) 'force_refresh': true,
      };
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode(payload),
          )
          .timeout(const Duration(seconds: 10));
      if (response.statusCode == 200) {
        return jsonDecode(response.body) as Map<String, dynamic>;
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// POST /v1/sessions/spawn: Spawn a new agent session (tmux or headless) on a host
  Future<Map<String, dynamic>?> spawnSession(
    String hostUrl, {
    String agent = 'claude-code',
    String cwd = '',
    String? host,
    String? nodePath,
    String? name,
    String? accountId,
    String engineType = 'tmux',
    String? prompt,
    bool isSupervisor = false,
    String? authToken,
  }) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/sessions/spawn');
    try {
      final payload = <String, dynamic>{
        'agent': agent,
        if (cwd.isNotEmpty || !isSupervisor) 'cwd': cwd,
        if (host != null && host.isNotEmpty) 'host': host,
        if (nodePath != null && nodePath.isNotEmpty) 'node_path': nodePath,
        if (name != null && name.isNotEmpty) 'name': name,
        if (accountId != null && accountId.isNotEmpty) 'account_id': accountId,
        'engine_type': engineType,
        if (prompt != null && prompt.isNotEmpty) 'prompt': prompt,
        if (isSupervisor) 'is_supervisor': true,
      };
      final response = await _client
          .post(
            uri,
            headers: _headers(authToken, {'Content-Type': 'application/json'}),
            body: jsonEncode(payload),
          )
          .timeout(const Duration(seconds: 12));
      if (response.statusCode == 200) {
        return jsonDecode(response.body) as Map<String, dynamic>;
      }
      return null;
    } catch (_) {
      return null;
    }
  }

  /// GET /v1/agents/discovery: Retrieve installed agent CLI discovery results
  Future<List<Map<String, dynamic>>> getAgentsDiscovery(String hostUrl,
      {String? authToken}) async {
    final clean = _cleanUrl(hostUrl);
    final uri = Uri.parse('$clean/v1/agents/discovery');
    try {
      final response = await _client
          .get(uri, headers: _headers(authToken))
          .timeout(const Duration(seconds: 4));
      if (response.statusCode == 200) {
        final List<dynamic> decoded = jsonDecode(response.body);
        return decoded.cast<Map<String, dynamic>>();
      }
      return [];
    } catch (_) {
      return [];
    }
  }

  void dispose() {
    _client.close();
  }
}
