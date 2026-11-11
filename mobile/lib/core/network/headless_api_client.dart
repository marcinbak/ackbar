import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/headless_models.dart';
import '../models/session.dart';
import 'api_client.dart';

extension HeadlessApiClient on ApiClient {
  Never _throwError(http.Response res) {
    var msg = res.body.trim();
    try {
      final decoded = jsonDecode(res.body);
      if (decoded is Map && decoded['error'] != null) {
        msg = decoded['error'].toString();
      }
    } catch (_) {}
    if (msg.isEmpty) {
      msg = 'HTTP ${res.statusCode}';
    }
    throw ApiHttpException(res.statusCode, msg);
  }

  Future<AuthToolStartResult> startAuthTool(
    String baseUrl, {
    required String sessionId,
    required String target,
    String? authToken,
  }) async {
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/auth-tool');
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode({'session_id': sessionId, 'target': target}),
        )
        .timeout(const Duration(seconds: 12));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return AuthToolStartResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<AuthCallbackResult> submitAuthCallback(
    String baseUrl, {
    required String sessionId,
    required String flowId,
    required String callbackInput,
    String? authToken,
  }) async {
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/auth-callback');
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode({
            'session_id': sessionId,
            'flow_id': flowId,
            'callback_url': callbackInput.trim(),
          }),
        )
        .timeout(const Duration(seconds: 15));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return AuthCallbackResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<SessionMcpStatus> getMcpStatus(
    String baseUrl,
    String sessionId, {
    bool refresh = false,
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final query = refresh ? {'refresh': '1'} : null;
    final uri = buildEndpointUri(
      baseUrl,
      '/v1/sessions/$encId/mcp-status',
      query,
    );
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 8));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return SessionMcpStatus.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<Session> updateSessionConfig(
    String baseUrl,
    String sessionId, {
    String? model,
    String? effort,
    String? permissionMode,
    List<String>? extraDirs,
    List<String>? addDirs,
    List<String>? removeDirs,
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/config');
    final body = <String, dynamic>{
      if (model != null) 'model': model,
      if (effort != null) 'effort': effort,
      if (permissionMode != null) 'permission_mode': permissionMode,
      if (extraDirs != null) 'extra_dirs': extraDirs,
      if (addDirs != null) 'add_dirs': addDirs,
      if (removeDirs != null) 'remove_dirs': removeDirs,
    };
    final res = await rawClient
        .patch(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode(body),
        )
        .timeout(const Duration(seconds: 8));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return Session.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
    }
    _throwError(res);
  }

  Future<ShellExecResult> runSessionShell(
    String baseUrl,
    String sessionId,
    String command, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/shell');
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode({'command': command}),
        )
        .timeout(const Duration(seconds: 35));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return ShellExecResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<SessionRotationResult> clearSession(
    String baseUrl,
    String sessionId, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/clear');
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode({}),
        )
        .timeout(const Duration(seconds: 10));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return SessionRotationResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<SessionRotationResult> compactSession(
    String baseUrl,
    String sessionId, {
    String? instructions,
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/compact');
    final body = <String, dynamic>{
      if (instructions != null && instructions.trim().isNotEmpty)
        'instructions': instructions.trim(),
    };
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode(body),
        )
        .timeout(const Duration(seconds: 15));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return SessionRotationResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<SessionRotationResult> handoverSessionDetailed(
    String baseUrl,
    String sessionId, {
    String strategy = 'in_place',
    String? customInstruction,
    String? authToken,
  }) async {
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/handover');
    final body = <String, dynamic>{
      'id': sessionId,
      'strategy': strategy,
      if (customInstruction != null && customInstruction.trim().isNotEmpty)
        'custom_instruction': customInstruction.trim(),
    };
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode(body),
        )
        .timeout(const Duration(seconds: 15));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return SessionRotationResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<HeadlessInspectData> inspectSession(
    String baseUrl,
    String sessionId,
    String kind, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(
      baseUrl,
      '/v1/sessions/$encId/inspect',
      {'kind': kind},
    );
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 12));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return HeadlessInspectData.fromJson(
        kind,
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<DiffInspectResult> getSessionDiff(
    String baseUrl,
    String sessionId, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/diff');
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 10));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return DiffInspectResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<RewindPreviewResult> getRewindTurns(
    String baseUrl,
    String sessionId, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/rewind');
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 8));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return RewindPreviewResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<SessionRotationResult> rewindSession(
    String baseUrl,
    String sessionId,
    int beforeTurn, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(baseUrl, '/v1/sessions/$encId/rewind');
    final res = await rawClient
        .post(
          uri,
          headers: buildAuthHeaders(authToken, jsonContent: true),
          body: jsonEncode({'before_turn': beforeTurn}),
        )
        .timeout(const Duration(seconds: 15));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      return SessionRotationResult.fromJson(
        jsonDecode(res.body) as Map<String, dynamic>,
      );
    }
    _throwError(res);
  }

  Future<List<CommandSuggestion>> getCommandSuggestions(
    String baseUrl,
    String sessionId, {
    String query = '',
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(
      baseUrl,
      '/v1/sessions/$encId/suggestions',
      {'kind': 'commands', 'q': query},
    );
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 4));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      final list = jsonDecode(res.body) as List<dynamic>? ?? const [];
      return list
          .whereType<Map<String, dynamic>>()
          .map(CommandSuggestion.fromJson)
          .toList();
    }
    _throwError(res);
  }

  Future<List<FileSuggestion>> getFileSuggestions(
    String baseUrl,
    String sessionId, {
    String query = '',
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(
      baseUrl,
      '/v1/sessions/$encId/suggestions',
      {'kind': 'files', 'q': query},
    );
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 4));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      final list = jsonDecode(res.body) as List<dynamic>? ?? const [];
      return list
          .whereType<Map<String, dynamic>>()
          .map(FileSuggestion.fromJson)
          .toList();
    }
    _throwError(res);
  }

  Future<List<ModelSuggestion>> getModelSuggestions(
    String baseUrl,
    String sessionId, {
    String? authToken,
  }) async {
    final encId = Uri.encodeComponent(sessionId);
    final uri = buildEndpointUri(
      baseUrl,
      '/v1/sessions/$encId/suggestions',
      {'kind': 'models'},
    );
    final res = await rawClient
        .get(uri, headers: buildAuthHeaders(authToken))
        .timeout(const Duration(seconds: 4));
    if (res.statusCode >= 200 && res.statusCode < 300) {
      final list = jsonDecode(res.body) as List<dynamic>? ?? const [];
      return list
          .whereType<Map<String, dynamic>>()
          .map(ModelSuggestion.fromJson)
          .toList();
    }
    _throwError(res);
  }
}
