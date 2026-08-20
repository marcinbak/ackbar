import 'package:flutter/material.dart';
import '../theme/app_colors.dart';
import '../widgets/status_badge.dart';

/// Enum representing session lifecycle states
enum SessionState {
  unknown(0),
  working(1),
  blocked(2),
  idle(3),
  ended(4),
  failed(5);

  final int value;
  const SessionState(this.value);

  static SessionState fromInt(int val) {
    return SessionState.values.firstWhere(
      (s) => s.value == val,
      orElse: () => SessionState.unknown,
    );
  }

  static SessionState fromString(String str) {
    switch (str.toLowerCase()) {
      case 'working':
        return SessionState.working;
      case 'blocked':
        return SessionState.blocked;
      case 'idle':
        return SessionState.idle;
      case 'ended':
        return SessionState.ended;
      case 'failed':
        return SessionState.failed;
      default:
        return SessionState.unknown;
    }
  }

  String get label {
    switch (this) {
      case SessionState.working:
        return 'WORKING';
      case SessionState.blocked:
        return 'BLOCKED';
      case SessionState.idle:
        return 'IDLE';
      case SessionState.ended:
        return 'ENDED';
      case SessionState.failed:
        return 'FAILED';
      case SessionState.unknown:
        return 'UNKNOWN';
    }
  }

  AckbarSessionStatus toBadgeStatus() {
    switch (this) {
      case SessionState.working:
        return AckbarSessionStatus.working;
      case SessionState.blocked:
        return AckbarSessionStatus.blocked;
      case SessionState.idle:
        return AckbarSessionStatus.idle;
      case SessionState.ended:
      case SessionState.failed:
      case SessionState.unknown:
        return AckbarSessionStatus.offline;
    }
  }
}

/// Block reason kind
enum BlockKind {
  permission,
  question,
  unknown;

  static BlockKind fromString(String? val) {
    if (val == null) return BlockKind.unknown;
    switch (val.toLowerCase()) {
      case 'permission':
        return BlockKind.permission;
      case 'question':
        return BlockKind.question;
      default:
        return BlockKind.unknown;
    }
  }

  String get label {
    switch (this) {
      case BlockKind.permission:
        return 'PERMISSION';
      case BlockKind.question:
        return 'QUESTION';
      case BlockKind.unknown:
        return 'BLOCKED';
    }
  }
}

/// Information on why a session is blocked
class Blocked {
  final BlockKind kind;
  final String reason;
  final DateTime since;
  final String? question;
  final List<String> options;
  final String? command;

  const Blocked({
    required this.kind,
    required this.reason,
    required this.since,
    this.question,
    this.options = const [],
    this.command,
  });

  bool get isQuestion => kind == BlockKind.question || (question != null && question!.isNotEmpty);
  bool get isPermission => kind == BlockKind.permission || (command != null && command!.isNotEmpty);

  factory Blocked.fromJson(Map<String, dynamic> json) {
    DateTime sinceDate;
    if (json['since'] != null) {
      sinceDate = DateTime.tryParse(json['since'].toString()) ?? DateTime.now();
    } else {
      sinceDate = DateTime.now();
    }

    List<String> parsedOptions = [];
    if (json['options'] != null && json['options'] is List) {
      parsedOptions = (json['options'] as List).map((e) => e.toString()).toList();
    }

    return Blocked(
      kind: BlockKind.fromString(json['kind']?.toString()),
      reason: json['reason']?.toString() ?? '',
      since: sinceDate,
      question: json['question']?.toString(),
      options: parsedOptions,
      command: json['command']?.toString(),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'kind': kind.name,
      'reason': reason,
      'since': since.toIso8601String(),
      if (question != null) 'question': question,
      if (options.isNotEmpty) 'options': options,
      if (command != null) 'command': command,
    };
  }

  Blocked copyWith({
    BlockKind? kind,
    String? reason,
    DateTime? since,
    String? question,
    List<String>? options,
    String? command,
  }) {
    return Blocked(
      kind: kind ?? this.kind,
      reason: reason ?? this.reason,
      since: since ?? this.since,
      question: question ?? this.question,
      options: options ?? this.options,
      command: command ?? this.command,
    );
  }
}

/// Agent session model representing an active or historical agentic session
class Session {
  final String id;
  final String agent;
  final String host;
  final String nativeId;
  final String cwd;
  final List<String> roots;
  final String projectKey;
  final SessionState state;
  final Blocked? blocked;
  final String activity;
  final DateTime startedAt;
  final DateTime lastEventAt;
  final bool managed;
  final String tmuxName;
  final int pid;
  final bool archived;
  final String nodePath;
  final String name;
  final String entrypoint;
  final String kind;
  final String version;
  final int contextPct;
  final String gitBranch;
  final bool deleted;
  final String customTitle;
  final String aiTitle;
  final String aiDescription;
  final String firstPrompt;
  final String lastPrompt;

  const Session({
    required this.id,
    required this.agent,
    required this.host,
    required this.nativeId,
    required this.cwd,
    this.roots = const [],
    this.projectKey = '',
    required this.state,
    this.blocked,
    this.activity = '',
    required this.startedAt,
    required this.lastEventAt,
    this.managed = false,
    this.tmuxName = '',
    this.pid = 0,
    this.archived = false,
    this.nodePath = '',
    this.name = '',
    this.entrypoint = '',
    this.kind = '',
    this.version = '',
    this.contextPct = 0,
    this.gitBranch = '',
    this.deleted = false,
    this.customTitle = '',
    this.aiTitle = '',
    this.aiDescription = '',
    this.firstPrompt = '',
    this.lastPrompt = '',
  });

  bool get isBlocked => state == SessionState.blocked || blocked != null;
  bool get isWorking => state == SessionState.working;
  bool get isIdle => state == SessionState.idle;
  bool get isEnded => state == SessionState.ended;

  /// High-priority resolved human-friendly session title
  String get displayTitle {
    if (customTitle.isNotEmpty) return customTitle;
    if (aiTitle.isNotEmpty) return aiTitle;
    if (name.isNotEmpty && name != agent && !name.startsWith('<')) return name;
    if (firstPrompt.isNotEmpty) {
      final firstLine = firstPrompt.split('\n').first.trim();
      if (firstLine.isNotEmpty) {
        return firstLine.length > 60 ? '${firstLine.substring(0, 57)}...' : firstLine;
      }
    }
    if (nativeId.isNotEmpty) return '$agentDisplayName ($nativeId)';
    return '$agentDisplayName session';
  }

  /// Formatted agent provider name
  String get agentDisplayName {
    switch (agent.toLowerCase()) {
      case 'claude-code':
      case 'claude':
        return 'Claude Code';
      case 'antigravity':
      case 'gemini':
        return 'Antigravity';
      case 'codex':
      case 'openai-codex':
        return 'OpenAI Codex';
      default:
        return agent.isNotEmpty ? agent : 'Agent';
    }
  }

  /// Agent icon/symbol
  String get agentSymbol {
    switch (agent.toLowerCase()) {
      case 'claude-code':
      case 'claude':
        return '🤖';
      case 'antigravity':
      case 'gemini':
        return '✨';
      case 'codex':
      case 'openai-codex':
        return '⚡';
      default:
        return '💻';
    }
  }

  /// Agent theme color
  Color get agentColor {
    switch (agent.toLowerCase()) {
      case 'claude-code':
      case 'claude':
        return const Color(0xFFD97706); // Warm Amber/Orange
      case 'antigravity':
      case 'gemini':
        return AppColors.infoCyan; // Cyan
      case 'codex':
      case 'openai-codex':
        return AppColors.statusEmerald; // Emerald
      default:
        return AppColors.textSecondary;
    }
  }

  /// Short host display tag (e.g. "@local", "@devbox")
  String get hostTag => host.startsWith('@') ? host : '@$host';

  /// Human-readable duration since last event or start
  String get timeElapsedFormatted {
    final now = DateTime.now();
    final duration = now.difference(startedAt.isBefore(lastEventAt) ? lastEventAt : startedAt);
    if (duration.inMinutes < 1) {
      return '${duration.inSeconds}s ago';
    } else if (duration.inHours < 1) {
      return '${duration.inMinutes}m ago';
    } else if (duration.inDays < 1) {
      return '${duration.inHours}h ${duration.inMinutes % 60}m';
    } else {
      return '${duration.inDays}d ago';
    }
  }

  /// Project display name derived from projectKey or nodePath or directory
  String get projectDisplayName {
    if (projectKey.isNotEmpty) {
      final parts = projectKey.split('/');
      if (parts.isNotEmpty) return parts.last.replaceAll('.git', '');
    }
    if (nodePath.isNotEmpty) {
      final parts = nodePath.split('/');
      if (parts.isNotEmpty) return parts.first;
    }
    if (cwd.isNotEmpty) {
      final parts = cwd.split('/');
      if (parts.isNotEmpty && parts.last.isNotEmpty) return parts.last;
    }
    return 'Default Project';
  }

  factory Session.fromJson(Map<String, dynamic> json) {
    SessionState parseState(dynamic val) {
      if (val is int) return SessionState.fromInt(val);
      if (val is String) return SessionState.fromString(val);
      return SessionState.unknown;
    }

    DateTime parseDate(dynamic val) {
      if (val == null) return DateTime.now();
      return DateTime.tryParse(val.toString()) ?? DateTime.now();
    }

    List<String> parseStringList(dynamic val) {
      if (val is List) {
        return val.map((e) => e.toString()).toList();
      }
      return const [];
    }

    return Session(
      id: json['id']?.toString() ?? '',
      agent: json['agent']?.toString() ?? '',
      host: json['host']?.toString() ?? 'local',
      nativeId: json['native_id']?.toString() ?? '',
      cwd: json['cwd']?.toString() ?? '',
      roots: parseStringList(json['roots']),
      projectKey: json['project_key']?.toString() ?? '',
      state: parseState(json['state']),
      blocked: json['blocked'] != null && json['blocked'] is Map<String, dynamic>
          ? Blocked.fromJson(json['blocked'] as Map<String, dynamic>)
          : null,
      activity: json['activity']?.toString() ?? '',
      startedAt: parseDate(json['started_at']),
      lastEventAt: parseDate(json['last_event_at']),
      managed: json['managed'] == true,
      tmuxName: json['tmux_name']?.toString() ?? '',
      pid: json['pid'] is int ? json['pid'] as int : int.tryParse(json['pid']?.toString() ?? '') ?? 0,
      archived: json['archived'] == true,
      nodePath: json['node_path']?.toString() ?? '',
      name: json['name']?.toString() ?? '',
      entrypoint: json['entrypoint']?.toString() ?? '',
      kind: json['kind']?.toString() ?? '',
      version: json['version']?.toString() ?? '',
      contextPct: json['context_pct'] is int
          ? json['context_pct'] as int
          : int.tryParse(json['context_pct']?.toString() ?? '') ?? 0,
      gitBranch: json['git_branch']?.toString() ?? '',
      deleted: json['deleted'] == true,
      customTitle: json['custom_title']?.toString() ?? '',
      aiTitle: json['ai_title']?.toString() ?? '',
      aiDescription: json['ai_description']?.toString() ?? '',
      firstPrompt: json['first_prompt']?.toString() ?? '',
      lastPrompt: json['last_prompt']?.toString() ?? '',
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'agent': agent,
      'host': host,
      'native_id': nativeId,
      'cwd': cwd,
      'roots': roots,
      'project_key': projectKey,
      'state': state.value,
      if (blocked != null) 'blocked': blocked!.toJson(),
      'activity': activity,
      'started_at': startedAt.toIso8601String(),
      'last_event_at': lastEventAt.toIso8601String(),
      'managed': managed,
      'tmux_name': tmuxName,
      'pid': pid,
      'archived': archived,
      'node_path': nodePath,
      'name': name,
      'entrypoint': entrypoint,
      'kind': kind,
      'version': version,
      'context_pct': contextPct,
      'git_branch': gitBranch,
      'deleted': deleted,
      'custom_title': customTitle,
      'ai_title': aiTitle,
      'ai_description': aiDescription,
      'first_prompt': firstPrompt,
      'last_prompt': lastPrompt,
    };
  }

  Session copyWith({
    String? id,
    String? agent,
    String? host,
    String? nativeId,
    String? cwd,
    List<String>? roots,
    String? projectKey,
    SessionState? state,
    Blocked? blocked,
    String? activity,
    DateTime? startedAt,
    DateTime? lastEventAt,
    bool? managed,
    String? tmuxName,
    int? pid,
    bool? archived,
    String? nodePath,
    String? name,
    String? entrypoint,
    String? kind,
    String? version,
    int? contextPct,
    String? gitBranch,
    bool? deleted,
    String? customTitle,
    String? aiTitle,
    String? aiDescription,
    String? firstPrompt,
    String? lastPrompt,
  }) {
    return Session(
      id: id ?? this.id,
      agent: agent ?? this.agent,
      host: host ?? this.host,
      nativeId: nativeId ?? this.nativeId,
      cwd: cwd ?? this.cwd,
      roots: roots ?? this.roots,
      projectKey: projectKey ?? this.projectKey,
      state: state ?? this.state,
      blocked: blocked ?? this.blocked,
      activity: activity ?? this.activity,
      startedAt: startedAt ?? this.startedAt,
      lastEventAt: lastEventAt ?? this.lastEventAt,
      managed: managed ?? this.managed,
      tmuxName: tmuxName ?? this.tmuxName,
      pid: pid ?? this.pid,
      archived: archived ?? this.archived,
      nodePath: nodePath ?? this.nodePath,
      name: name ?? this.name,
      entrypoint: entrypoint ?? this.entrypoint,
      kind: kind ?? this.kind,
      version: version ?? this.version,
      contextPct: contextPct ?? this.contextPct,
      gitBranch: gitBranch ?? this.gitBranch,
      deleted: deleted ?? this.deleted,
      customTitle: customTitle ?? this.customTitle,
      aiTitle: aiTitle ?? this.aiTitle,
      aiDescription: aiDescription ?? this.aiDescription,
      firstPrompt: firstPrompt ?? this.firstPrompt,
      lastPrompt: lastPrompt ?? this.lastPrompt,
    );
  }
}
