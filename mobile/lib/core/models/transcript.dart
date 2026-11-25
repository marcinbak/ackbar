import 'dart:convert';
import 'chat_stream_event.dart';
import 'checklist_item.dart';

/// A single conversational step / turn within an agent session transcript.
class TranscriptMessage {
  final String role; // "user", "assistant", "system"
  final String content;
  final String? thinking;
  final List<String> toolCalls;
  final List<ChecklistItem> checklist;
  final bool checklistCleared;
  final DateTime timestamp;
  final String? turnId;
  final bool inFlight;

  const TranscriptMessage({
    required this.role,
    required this.content,
    this.thinking,
    this.toolCalls = const [],
    this.checklist = const [],
    this.checklistCleared = false,
    required this.timestamp,
    this.turnId,
    this.inFlight = false,
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
      ts = tsStr != null && tsStr.isNotEmpty
          ? DateTime.parse(tsStr)
          : DateTime.now();
    } catch (_) {
      ts = DateTime.now();
    }

    final role = (json['role']?.toString() ?? 'assistant').toLowerCase();
    final content = json['content']?.toString() ?? '';
    final thinking = json['thinking']?.toString();
    final turnId = json['turn_id']?.toString() ?? json['turnId']?.toString();
    final inFlight =
        json['in_flight'] as bool? ?? json['inFlight'] as bool? ?? false;

    final checklist = ChecklistItem.listFromJson(json['checklist']);
    final checklistCleared = json['checklist_cleared'] == true;

    return TranscriptMessage(
      role: role,
      content: content,
      thinking:
          (thinking != null && thinking.trim().isNotEmpty) ? thinking : null,
      toolCalls: tools,
      checklist: checklist,
      checklistCleared: checklistCleared,
      timestamp: ts,
      turnId: turnId,
      inFlight: inFlight,
    );
  }

  TranscriptMessage copyWith({
    String? role,
    String? content,
    String? thinking,
    List<String>? toolCalls,
    List<ChecklistItem>? checklist,
    bool? checklistCleared,
    DateTime? timestamp,
    String? turnId,
    bool? inFlight,
  }) {
    return TranscriptMessage(
      role: role ?? this.role,
      content: content ?? this.content,
      thinking: thinking ?? this.thinking,
      toolCalls: toolCalls ?? this.toolCalls,
      checklist: checklist ?? this.checklist,
      checklistCleared: checklistCleared ?? this.checklistCleared,
      timestamp: timestamp ?? this.timestamp,
      turnId: turnId ?? this.turnId,
      inFlight: inFlight ?? this.inFlight,
    );
  }

  Map<String, dynamic> toJson() => {
        'role': role,
        'content': content,
        if (thinking != null) 'thinking': thinking,
        if (toolCalls.isNotEmpty) 'tool_calls': toolCalls,
        if (checklist.isNotEmpty)
          'checklist': checklist.map((e) => e.toJson()).toList(),
        if (checklistCleared) 'checklist_cleared': true,
        'timestamp': timestamp.toIso8601String(),
        if (turnId != null) 'turn_id': turnId,
        if (inFlight) 'in_flight': true,
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
  final InFlightTurnMeta? inFlightTurn;
  final List<Map<String, dynamic>> runningSubagents;

  const TranscriptData({
    required this.sessionId,
    required this.nativeId,
    required this.agent,
    required this.title,
    required this.cwd,
    required this.messages,
    required this.rawMarkdown,
    this.inFlightTurn,
    this.runningSubagents = const [],
  });

  factory TranscriptData.fromJson(Map<String, dynamic> json) {
    final msgs = <TranscriptMessage>[];
    if (json['messages'] is List) {
      for (final m in (json['messages'] as List)) {
        if (m is Map) {
          final msg = TranscriptMessage.fromJson(Map<String, dynamic>.from(m));
          // Keep turns that have content, thinking, tool calls, or checklist
          if (msg.content.trim().isNotEmpty ||
              (msg.thinking != null && msg.thinking!.trim().isNotEmpty) ||
              msg.toolCalls.isNotEmpty ||
              msg.checklist.isNotEmpty ||
              msg.checklistCleared) {
            if (msgs.isNotEmpty &&
                msgs.last.isUser &&
                msg.isUser &&
                msgs.last.content.trim() == msg.content.trim() &&
                msg.timestamp.difference(msgs.last.timestamp).inSeconds.abs() <=
                    5) {
              continue;
            }
            msgs.add(msg);
          }
        }
      }
    }

    InFlightTurnMeta? inFlight;
    if (json['in_flight_turn'] is Map) {
      inFlight = InFlightTurnMeta.fromJson(
        Map<String, dynamic>.from(json['in_flight_turn']),
      );
    } else if (json['inFlightTurn'] is Map) {
      inFlight = InFlightTurnMeta.fromJson(
        Map<String, dynamic>.from(json['inFlightTurn']),
      );
    }

    final subagents = <Map<String, dynamic>>[];
    final rawSubs = json['running_subagents'] ?? json['subagents'];
    if (rawSubs is List) {
      for (final s in rawSubs) {
        if (s is Map) {
          subagents.add(Map<String, dynamic>.from(s));
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
      inFlightTurn: inFlight,
      runningSubagents: subagents,
    );
  }

  factory TranscriptData.fromRawMarkdown({
    required String sessionId,
    required String agent,
    required String title,
    required String markdown,
  }) {
    final messages = <TranscriptMessage>[];
    final pattern = RegExp(
      r'(### 👤 User [^\n]*|### 🤖 Assistant [^\n]*|> ℹ️ System [^\n]*)',
    );
    final matches = pattern.allMatches(markdown).toList();

    for (var i = 0; i < matches.length; i++) {
      final header = matches[i].group(0) ?? '';
      final start = matches[i].end;
      final end =
          (i + 1 < matches.length) ? matches[i + 1].start : markdown.length;
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
        if (messages.isNotEmpty &&
            messages.last.isUser &&
            role == 'user' &&
            messages.last.content.trim() == body) {
          continue;
        }
        messages.add(
          TranscriptMessage(
            role: role,
            content: body,
            timestamp: DateTime.now(),
          ),
        );
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

  TranscriptData copyWith({
    String? sessionId,
    String? nativeId,
    String? agent,
    String? title,
    String? cwd,
    List<TranscriptMessage>? messages,
    String? rawMarkdown,
    InFlightTurnMeta? inFlightTurn,
    List<Map<String, dynamic>>? runningSubagents,
  }) {
    return TranscriptData(
      sessionId: sessionId ?? this.sessionId,
      nativeId: nativeId ?? this.nativeId,
      agent: agent ?? this.agent,
      title: title ?? this.title,
      cwd: cwd ?? this.cwd,
      messages: messages ?? this.messages,
      rawMarkdown: rawMarkdown ?? this.rawMarkdown,
      inFlightTurn: inFlightTurn ?? this.inFlightTurn,
      runningSubagents: runningSubagents ?? this.runningSubagents,
    );
  }
}
