import 'session.dart';

bool isSafeHttpsUrl(String? raw) {
  if (raw == null || raw.isEmpty) return false;
  if (raw != raw.trim()) return false;
  if (raw.length > 4096) return false;
  for (var i = 0; i < raw.length; i++) {
    final code = raw.codeUnitAt(i);
    if (code <= 0x20 || code == 0x7F) return false;
  }
  if (RegExp(r'\s').hasMatch(raw)) return false;
  final uri = Uri.tryParse(raw);
  if (uri == null) return false;
  if (uri.scheme.toLowerCase() != 'https') return false;
  if (uri.host.isEmpty) return false;
  if (uri.userInfo.isNotEmpty) return false;
  return true;
}

class AuthToolStartResult {
  final String status;
  final String flowId;
  final String authUrl;
  final int recordedPort;
  final bool acceptsStdin;

  const AuthToolStartResult({
    required this.status,
    required this.flowId,
    required this.authUrl,
    required this.recordedPort,
    required this.acceptsStdin,
  });

  factory AuthToolStartResult.fromJson(Map<String, dynamic> json) {
    return AuthToolStartResult(
      status: json['status']?.toString() ?? '',
      flowId: json['flow_id']?.toString() ?? '',
      authUrl: json['auth_url']?.toString() ?? '',
      recordedPort: (json['recorded_port'] as num?)?.toInt() ?? 0,
      acceptsStdin: json['accepts_stdin'] == true,
    );
  }
}

class AuthCallbackResult {
  final String status;
  final bool completed;

  const AuthCallbackResult({required this.status, required this.completed});

  factory AuthCallbackResult.fromJson(Map<String, dynamic> json) {
    return AuthCallbackResult(
      status: json['status']?.toString() ?? '',
      completed: json['completed'] == true,
    );
  }
}

class McpServerHealth {
  final String name;
  final String transport;
  final String scope;
  final String status;
  final bool authSupported;
  final String target;

  const McpServerHealth({
    required this.name,
    required this.transport,
    required this.scope,
    required this.status,
    required this.authSupported,
    required this.target,
  });

  bool get isConfigured => status.toLowerCase() == 'configured';
  bool get needsAuth => status.toLowerCase() == 'needs_auth';

  factory McpServerHealth.fromJson(Map<String, dynamic> json) {
    return McpServerHealth(
      name: json['name']?.toString() ?? '',
      transport: json['transport']?.toString() ?? '',
      scope: json['scope']?.toString() ?? '',
      status: json['status']?.toString() ?? '',
      authSupported: json['auth_supported'] == true,
      target: json['target']?.toString() ?? '',
    );
  }
}

class WorkspacePluginHealth {
  final String name;
  final bool installed;
  final bool authenticated;
  final bool authSupported;
  final String target;

  const WorkspacePluginHealth({
    required this.name,
    required this.installed,
    required this.authenticated,
    required this.authSupported,
    required this.target,
  });

  factory WorkspacePluginHealth.fromJson(Map<String, dynamic> json) {
    return WorkspacePluginHealth(
      name: json['name']?.toString() ?? '',
      installed: json['installed'] == true,
      authenticated: json['authenticated'] == true,
      authSupported: json['auth_supported'] == true,
      target: json['target']?.toString() ?? '',
    );
  }
}

class SessionMcpStatus {
  final List<McpServerHealth> servers;
  final List<WorkspacePluginHealth> plugins;

  const SessionMcpStatus({
    this.servers = const [],
    this.plugins = const [],
  });

  factory SessionMcpStatus.fromJson(Map<String, dynamic> json) {
    final sList = json['servers'] as List<dynamic>? ?? const [];
    final pList = json['plugins'] as List<dynamic>? ?? const [];
    return SessionMcpStatus(
      servers: sList
          .whereType<Map<String, dynamic>>()
          .map(McpServerHealth.fromJson)
          .toList(),
      plugins: pList
          .whereType<Map<String, dynamic>>()
          .map(WorkspacePluginHealth.fromJson)
          .toList(),
    );
  }
}

class CommandSuggestion {
  final String name;
  final String trigger;
  final String description;
  final String category;
  final String executionMode;

  const CommandSuggestion({
    required this.name,
    required this.trigger,
    required this.description,
    required this.category,
    required this.executionMode,
  });

  bool get isClientAction => executionMode == 'client_action';
  bool get isPrompt => executionMode == 'prompt';

  factory CommandSuggestion.fromJson(Map<String, dynamic> json) {
    return CommandSuggestion(
      name: json['name']?.toString() ?? '',
      trigger: json['trigger']?.toString() ?? '',
      description: json['description']?.toString() ?? '',
      category: json['category']?.toString() ?? '',
      executionMode: json['execution_mode']?.toString() ?? 'client_action',
    );
  }
}

class FileSuggestion {
  final String path;
  final String name;
  final bool isDir;

  const FileSuggestion({
    required this.path,
    required this.name,
    required this.isDir,
  });

  factory FileSuggestion.fromJson(Map<String, dynamic> json) {
    return FileSuggestion(
      path: json['path']?.toString() ?? '',
      name: json['name']?.toString() ?? '',
      isDir: json['is_dir'] == true,
    );
  }
}

class ModelSuggestion {
  final String id;
  final String label;

  const ModelSuggestion({required this.id, required this.label});

  factory ModelSuggestion.fromJson(Map<String, dynamic> json) {
    return ModelSuggestion(
      id: json['id']?.toString() ?? '',
      label: json['label']?.toString() ?? '',
    );
  }
}

class ShellExecResult {
  final String command;
  final String cwd;
  final String stdout;
  final String stderr;
  final int exitCode;
  final int durationMs;
  final bool timedOut;
  final bool truncated;

  const ShellExecResult({
    required this.command,
    required this.cwd,
    required this.stdout,
    required this.stderr,
    required this.exitCode,
    required this.durationMs,
    required this.timedOut,
    required this.truncated,
  });

  factory ShellExecResult.fromJson(Map<String, dynamic> json) {
    return ShellExecResult(
      command: json['command']?.toString() ?? '',
      cwd: json['cwd']?.toString() ?? '',
      stdout: json['stdout']?.toString() ?? '',
      stderr: json['stderr']?.toString() ?? '',
      exitCode: (json['exit_code'] as num?)?.toInt() ?? 0,
      durationMs: (json['duration_ms'] as num?)?.toInt() ?? 0,
      timedOut: json['timed_out'] == true,
      truncated: json['truncated'] == true,
    );
  }
}

class DiffInspectResult {
  final bool isGitRepo;
  final String cwd;
  final String branch;
  final List<String> statusLines;
  final String stat;
  final String diff;
  final bool truncated;
  final bool clean;

  const DiffInspectResult({
    required this.isGitRepo,
    required this.cwd,
    required this.branch,
    required this.statusLines,
    required this.stat,
    required this.diff,
    required this.truncated,
    required this.clean,
  });

  factory DiffInspectResult.fromJson(Map<String, dynamic> json) {
    final sl = json['status_lines'] as List<dynamic>? ?? const [];
    return DiffInspectResult(
      isGitRepo: json['is_git_repo'] == true,
      cwd: json['cwd']?.toString() ?? '',
      branch: json['branch']?.toString() ?? '',
      statusLines: sl.map((e) => e.toString()).toList(),
      stat: json['stat']?.toString() ?? '',
      diff: json['diff']?.toString() ?? '',
      truncated: json['truncated'] == true,
      clean: json['clean'] == true,
    );
  }
}

class RewindTurnItem {
  final int turn;
  final DateTime timestamp;
  final String preview;

  const RewindTurnItem({
    required this.turn,
    required this.timestamp,
    required this.preview,
  });

  factory RewindTurnItem.fromJson(Map<String, dynamic> json) {
    final tsRaw = json['timestamp']?.toString();
    return RewindTurnItem(
      turn: (json['turn'] as num?)?.toInt() ?? 0,
      timestamp: tsRaw != null
          ? DateTime.tryParse(tsRaw) ?? DateTime.now()
          : DateTime.now(),
      preview: json['preview']?.toString() ?? '',
    );
  }
}

class RewindPreviewResult {
  final String sessionId;
  final String agent;
  final int totalTurns;
  final List<RewindTurnItem> turns;

  const RewindPreviewResult({
    required this.sessionId,
    required this.agent,
    required this.totalTurns,
    required this.turns,
  });

  factory RewindPreviewResult.fromJson(Map<String, dynamic> json) {
    final tList = json['turns'] as List<dynamic>? ?? const [];
    return RewindPreviewResult(
      sessionId: json['session_id']?.toString() ?? '',
      agent: json['agent']?.toString() ?? '',
      totalTurns: (json['total_turns'] as num?)?.toInt() ?? 0,
      turns: tList
          .whereType<Map<String, dynamic>>()
          .map(RewindTurnItem.fromJson)
          .toList(),
    );
  }
}

class SessionRotationResult {
  final String status;
  final String mode;
  final String strategy;
  final String oldSessionId;
  final String newSessionId;
  final int? beforeTurn;
  final String briefing;
  final Session? session;

  const SessionRotationResult({
    required this.status,
    required this.mode,
    required this.strategy,
    required this.oldSessionId,
    required this.newSessionId,
    this.beforeTurn,
    required this.briefing,
    this.session,
  });

  factory SessionRotationResult.fromJson(Map<String, dynamic> json) {
    return SessionRotationResult(
      status: json['status']?.toString() ?? '',
      mode: json['mode']?.toString() ?? '',
      strategy: json['strategy']?.toString() ?? '',
      oldSessionId: json['old_session_id']?.toString() ?? '',
      newSessionId: json['new_session_id']?.toString() ?? '',
      beforeTurn: (json['before_turn'] as num?)?.toInt(),
      briefing: json['briefing']?.toString() ?? '',
      session: json['session'] is Map<String, dynamic>
          ? Session.fromJson(json['session'] as Map<String, dynamic>)
          : null,
    );
  }
}

class HeadlessInspectData {
  final String kind;
  final Map<String, dynamic> raw;

  const HeadlessInspectData({required this.kind, required this.raw});

  String get report => raw['report']?.toString() ?? '';
  String get version => raw['version']?.toString() ?? '';
  String get agent => raw['agent']?.toString() ?? '';
  String get cwd => raw['cwd']?.toString() ?? '';

  factory HeadlessInspectData.fromJson(String kind, Map<String, dynamic> json) {
    return HeadlessInspectData(kind: kind, raw: json);
  }
}

class HeadlessEphemeralCardEntry {
  final String id;
  final String kind;
  final DateTime createdAt;
  final ShellExecResult? shell;
  final DiffInspectResult? diff;
  final RewindPreviewResult? rewind;
  final HeadlessInspectData? inspect;
  final SessionMcpStatus? mcp;
  final String? statusTitle;
  final String? statusBody;
  final bool isError;

  const HeadlessEphemeralCardEntry({
    required this.id,
    required this.kind,
    required this.createdAt,
    this.shell,
    this.diff,
    this.rewind,
    this.inspect,
    this.mcp,
    this.statusTitle,
    this.statusBody,
    this.isError = false,
  });
}
