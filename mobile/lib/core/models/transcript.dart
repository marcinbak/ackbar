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
      for (final t in (json['tool_calls'] as List)) {
        if (t != null && t.toString().trim().isNotEmpty) {
          tools.add(t.toString().trim());
        }
      }
    }

    DateTime ts;
    try {
      final tsStr = json['timestamp']?.toString();
      ts = tsStr != null && tsStr.isNotEmpty ? DateTime.parse(tsStr) : DateTime.now();
    } catch (_) {
      ts = DateTime.now();
    }

    final role = (json['role']?.toString() ?? 'assistant').toLowerCase();
    final content = json['content']?.toString() ?? '';
    final thinking = json['thinking']?.toString();

    return TranscriptMessage(
      role: role,
      content: content,
      thinking: (thinking != null && thinking.trim().isNotEmpty) ? thinking : null,
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
      for (final m in (json['messages'] as List)) {
        if (m is Map) {
          final msg = TranscriptMessage.fromJson(Map<String, dynamic>.from(m));
          // Keep turns that have content, thinking, or tool calls
          if (msg.content.trim().isNotEmpty ||
              (msg.thinking != null && msg.thinking!.trim().isNotEmpty) ||
              msg.toolCalls.isNotEmpty) {
            msgs.add(msg);
          }
        }
      }
    }

    return TranscriptData(
      sessionId: json['session_id']?.toString() ?? '',
      nativeId: json['native_id']?.toString() ?? '',
      agent: json['agent']?.toString() ?? '',
      title: json['title']?.toString() ?? '',
      cwd: json['cwd']?.toString() ?? '',
      messages: msgs,
      rawMarkdown: json['markdown']?.toString() ?? '',
    );
  }

  factory TranscriptData.fromRawMarkdown({
    required String sessionId,
    required String agent,
    required String title,
    required String markdown,
  }) {
    final messages = <TranscriptMessage>[];
    final pattern = RegExp(r'(### 👤 User [^\n]*|### 🤖 Assistant [^\n]*|> ℹ️ System [^\n]*)');
    final matches = pattern.allMatches(markdown).toList();

    for (var i = 0; i < matches.length; i++) {
      final header = matches[i].group(0) ?? '';
      final start = matches[i].end;
      final end = (i + 1 < matches.length) ? matches[i + 1].start : markdown.length;
      var body = markdown.substring(start, end).trim();
      body = body.replaceAll(RegExp(r'\n*---\n*$'), '').trim();

      String role = 'assistant';
      if (header.contains('👤 User')) {
        role = 'user';
      } else if (header.contains('🤖 Assistant')) {
        role = 'assistant';
      } else if (header.contains('ℹ️ System')) {
        role = 'system';
      }

      if (body.isNotEmpty) {
        messages.add(TranscriptMessage(
          role: role,
          content: body,
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
