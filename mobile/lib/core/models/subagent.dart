/// Model representing an active or completed subagent spawned by an AI agent.
class SubagentInfo {
  final String id;
  final String name;
  final String? role;
  final String prompt;
  final String state; // "running", "completed"
  final DateTime startedAt;

  const SubagentInfo({
    required this.id,
    required this.name,
    this.role,
    this.prompt = '',
    this.state = 'running',
    required this.startedAt,
  });

  bool get isRunning => state.toLowerCase() == 'running';

  String get displayName {
    if (name.isNotEmpty) return name;
    if (role != null && role!.isNotEmpty) return role!;
    return 'Subagent';
  }

  String? get displayRole {
    if (role != null && role!.isNotEmpty && role != displayName) {
      return role;
    }
    return null;
  }

  String get timeElapsedFormatted {
    final now = DateTime.now();
    final duration = now.difference(startedAt);
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

  factory SubagentInfo.fromJson(Map<String, dynamic> json) {
    DateTime started;
    try {
      final sStr = json['started_at']?.toString();
      started = sStr != null && sStr.isNotEmpty ? DateTime.parse(sStr) : DateTime.now();
    } catch (_) {
      started = DateTime.now();
    }

    return SubagentInfo(
      id: json['id']?.toString() ?? '',
      name: json['name']?.toString() ?? '',
      role: json['role']?.toString(),
      prompt: json['prompt']?.toString() ?? '',
      state: json['state']?.toString() ?? 'running',
      startedAt: started,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        if (role != null) 'role': role,
        'prompt': prompt,
        'state': state,
        'started_at': startedAt.toIso8601String(),
      };

  SubagentInfo copyWith({
    String? id,
    String? name,
    String? role,
    String? prompt,
    String? state,
    DateTime? startedAt,
  }) {
    return SubagentInfo(
      id: id ?? this.id,
      name: name ?? this.name,
      role: role ?? this.role,
      prompt: prompt ?? this.prompt,
      state: state ?? this.state,
      startedAt: startedAt ?? this.startedAt,
    );
  }
}
