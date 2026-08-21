import 'dart:convert';

/// A single conversational step / turn within an agent session transcript.
class TranscriptMessage {
  final String role; // "user", "assistant", "system"
  final String content;
  final String? thinking;
  final List<String> toolCalls;
  final DateTime timestamp;

  const TranscriptMessage({
    required this.role,
    required this.content,
    this.thinking,
    this.toolCalls = const [],
    required this.timestamp,
  });

  bool get isUser => role == 'user';
  bool get isAssistant => role == 'assistant';
  bool get isSystem => role == 'system';

  factory TranscriptMessage.fromJson(Map<String, dynamic> json) {
    final tools = <String>[];
    if (json['tool_calls'] is List) {
      for (final t in json['tool_calls']) {
        if (t != null) tools.add(t.toString());
      }
    }

    DateTime ts;
    try {
      final tsStr = json['timestamp'] as String?;
      ts = tsStr != null && tsStr.isNotEmpty ? DateTime.parse(tsStr) : DateTime.now();
    } catch (_) {
      ts = DateTime.now();
    }

    return TranscriptMessage(
      role: (json['role'] as String? ?? 'assistant').toLowerCase(),
      content: json['content'] as String? ?? '',
      thinking: json['thinking'] as String?,
      toolCalls: tools,
      timestamp: ts,
    );
  }

  Map<String, dynamic> toJson() => {
        'role': role,
        'content': content,
        if (thinking != null) 'thinking': thinking,
        if (toolCalls.isNotEmpty) 'tool_calls': toolCalls,
        'timestamp': timestamp.toIso8601String(),
      };
}

/// Parsed transcript payload returned by ackbard GET /v1/sessions/transcript.
class TranscriptData {
  final String sessionId;
  final String nativeId;
  final String agent;
  final String title;
  final String cwd;
  final List<TranscriptMessage> messages;
  final String rawMarkdown;

  const TranscriptData({
    required this.sessionId,
    required this.nativeId,
    required this.agent,
    required this.title,
    required this.cwd,
    required this.messages,
    required this.rawMarkdown,
  });

  factory TranscriptData.fromJson(Map<String, dynamic> json) {
    final msgs = <TranscriptMessage>[];
    if (json['messages'] is List) {
      for (final m in json['messages']) {
        if (m is Map<String, dynamic>) {
          msgs.add(TranscriptMessage.fromJson(m));
        }
      }
    }

    return TranscriptData(
      sessionId: json['session_id'] as String? ?? '',
      nativeId: json['native_id'] as String? ?? '',
      agent: json['agent'] as String? ?? '',
      title: json['title'] as String? ?? '',
      cwd: json['cwd'] as String? ?? '',
      messages: msgs,
      rawMarkdown: json['markdown'] as String? ?? '',
    );
  }

  factory TranscriptData.fromRawMarkdown({
    required String sessionId,
    required String agent,
    required String title,
    required String markdown,
  }) {
    // Fallback parser if raw markdown was returned
    final messages = <TranscriptMessage>[];
    final sections = markdown.split(RegExp(r'\n---\n+'));
    for (final sec in sections) {
      final trimmed = sec.trim();
      if (trimmed.isEmpty) continue;

      if (trimmed.contains('### 👤 User')) {
        final content = trimmed.replaceFirst(RegExp(r'### 👤 User \([^\)]+\)\n*'), '').trim();
        messages.add(TranscriptMessage(
          role: 'user',
          content: content,
          timestamp: DateTime.now(),
        ));
      } else if (trimmed.contains('### 🤖 Assistant')) {
        final content = trimmed.replaceFirst(RegExp(r'### 🤖 Assistant \([^\)]+\)\n*'), '').trim();
        messages.add(TranscriptMessage(
          role: 'assistant',
          content: content,
          timestamp: DateTime.now(),
        ));
      } else if (trimmed.startsWith('> ℹ️ System')) {
        messages.add(TranscriptMessage(
          role: 'system',
          content: trimmed,
          timestamp: DateTime.now(),
        ));
      }
    }

    return TranscriptData(
      sessionId: sessionId,
      nativeId: sessionId,
      agent: agent,
      title: title,
      cwd: '',
      messages: messages,
      rawMarkdown: markdown,
    );
  }
}
