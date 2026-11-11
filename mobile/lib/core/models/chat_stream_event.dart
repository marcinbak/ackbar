import 'dart:convert';
import 'session.dart';

enum ChatStreamEventType {
  status,
  sessionInit,
  turnStart,
  textDelta,
  thoughtDelta,
  toolStart,
  toolResult,
  turnComplete,
  done,
  turnCancelled,
  error,
  queueUpdate,
  subagentsUpdate,
  interactiveBlock,
  authComplete,
  unknown,
}

ChatStreamEventType parseChatStreamEventType(String? type) {
  switch (type?.toLowerCase().trim()) {
    case 'status':
      return ChatStreamEventType.status;
    case 'session_init':
      return ChatStreamEventType.sessionInit;
    case 'turn_start':
      return ChatStreamEventType.turnStart;
    case 'text_delta':
      return ChatStreamEventType.textDelta;
    case 'thought_delta':
      return ChatStreamEventType.thoughtDelta;
    case 'tool_start':
      return ChatStreamEventType.toolStart;
    case 'tool_result':
      return ChatStreamEventType.toolResult;
    case 'turn_complete':
      return ChatStreamEventType.turnComplete;
    case 'done':
      return ChatStreamEventType.done;
    case 'turn_cancelled':
      return ChatStreamEventType.turnCancelled;
    case 'error':
      return ChatStreamEventType.error;
    case 'queue_update':
      return ChatStreamEventType.queueUpdate;
    case 'subagents_update':
      return ChatStreamEventType.subagentsUpdate;
    case 'interactive_block':
      return ChatStreamEventType.interactiveBlock;
    case 'auth_complete':
      return ChatStreamEventType.authComplete;
    default:
      return ChatStreamEventType.unknown;
  }
}

class ChatStreamEvent {
  final int seq;
  final String sessionId;
  final String turnId;
  final String nativeId;
  final ChatStreamEventType type;
  final String rawType;
  final String text;
  final String thinking;
  final String toolName;
  final dynamic toolInput;
  final String toolOutput;
  final Blocked? blocked;
  final bool isError;
  final List<PromptQueueItemModel> queueItems;
  final bool queuePaused;
  final List<Map<String, dynamic>> subagents;
  final DateTime timestamp;

  const ChatStreamEvent({
    this.seq = 0,
    required this.sessionId,
    this.turnId = '',
    this.nativeId = '',
    required this.type,
    required this.rawType,
    this.text = '',
    this.thinking = '',
    this.toolName = '',
    this.toolInput,
    this.toolOutput = '',
    this.blocked,
    this.isError = false,
    this.queueItems = const [],
    this.queuePaused = false,
    this.subagents = const [],
    required this.timestamp,
  });

  static (String, int) parseEventId(String rawId) {
    final trimmed = rawId.trim();
    if (trimmed.isEmpty) return ('', 0);
    final idx = trimmed.lastIndexOf(':');
    if (idx >= 0) {
      final turn = trimmed.substring(0, idx).trim();
      final seq = int.tryParse(trimmed.substring(idx + 1).trim()) ?? 0;
      return (turn, seq);
    }
    final seq = int.tryParse(trimmed) ?? 0;
    return ('', seq);
  }

  factory ChatStreamEvent.fromJson(
    Map<String, dynamic> json, {
    String? eventId,
  }) {
    String turnId = json['turn_id']?.toString() ?? '';
    int seq = (json['seq'] as num?)?.toInt() ?? 0;

    if (eventId != null && eventId.trim().isNotEmpty) {
      final (parsedTurn, parsedSeq) = parseEventId(eventId);
      if (turnId.isEmpty && parsedTurn.isNotEmpty) {
        turnId = parsedTurn;
      }
      if (seq == 0 && parsedSeq > 0) {
        seq = parsedSeq;
      }
    }

    final rawType = json['type']?.toString() ?? 'status';
    final parsedType = parseChatStreamEventType(rawType);

    Blocked? blocked;
    if (json['blocked'] is Map) {
      blocked = Blocked.fromJson(Map<String, dynamic>.from(json['blocked']));
    }

    final qItems = <PromptQueueItemModel>[];
    if (json['queue_items'] is List) {
      for (final item in (json['queue_items'] as List)) {
        if (item is Map) {
          qItems.add(
            PromptQueueItemModel.fromJson(Map<String, dynamic>.from(item)),
          );
        }
      }
    }

    final subs = <Map<String, dynamic>>[];
    if (json['subagents'] is List) {
      for (final s in (json['subagents'] as List)) {
        if (s is Map) {
          subs.add(Map<String, dynamic>.from(s));
        }
      }
    }

    DateTime ts = DateTime.now();
    if (json['timestamp'] != null) {
      ts = DateTime.tryParse(json['timestamp'].toString()) ?? DateTime.now();
    }

    return ChatStreamEvent(
      seq: seq,
      sessionId: json['session_id']?.toString() ?? '',
      turnId: turnId,
      nativeId: json['native_id']?.toString() ?? '',
      type: parsedType,
      rawType: rawType,
      text: json['text']?.toString() ?? '',
      thinking: json['thinking']?.toString() ?? '',
      toolName: json['tool_name']?.toString() ?? '',
      toolInput: json['tool_input'],
      toolOutput: json['tool_output']?.toString() ?? '',
      blocked: blocked,
      isError: json['is_error'] as bool? ?? false,
      queueItems: qItems,
      queuePaused: json['queue_paused'] as bool? ?? false,
      subagents: subs,
      timestamp: ts,
    );
  }

  String formatToolCallSummary() {
    if (toolName.isEmpty) return 'Tool call';
    if (toolInput == null) return toolName;
    if (toolInput is Map) {
      final map = toolInput as Map;
      if (map.containsKey('path')) return '$toolName(${map['path']})';
      if (map.containsKey('file_path')) return '$toolName(${map['file_path']})';
      if (map.containsKey('command')) return '$toolName(${map['command']})';
      if (map.containsKey('pattern')) return '$toolName(${map['pattern']})';
      if (map.containsKey('description')) {
        return '$toolName: ${map['description']}';
      }
    }
    if (toolInput is String && (toolInput as String).trim().isNotEmpty) {
      final s = (toolInput as String).trim();
      return '$toolName($s)';
    }
    return toolName;
  }
}

class InFlightTurnMeta {
  final String turnId;
  final int lastSeq;
  final bool active;
  final String? prompt;
  final DateTime? startedAt;

  const InFlightTurnMeta({
    required this.turnId,
    required this.lastSeq,
    this.active = true,
    this.prompt,
    this.startedAt,
  });

  factory InFlightTurnMeta.fromJson(Map<String, dynamic> json) {
    final turnId =
        json['turn_id']?.toString() ?? json['turnId']?.toString() ?? '';
    final lastSeq = (json['last_seq'] as num?)?.toInt() ??
        (json['lastSeq'] as num?)?.toInt() ??
        (json['seq'] as num?)?.toInt() ??
        0;
    final active = json['active'] as bool? ?? true;
    final prompt = json['prompt']?.toString();
    DateTime? started;
    if (json['timestamp'] != null) {
      started = DateTime.tryParse(json['timestamp'].toString());
    } else if (json['started_at'] != null) {
      started = DateTime.tryParse(json['started_at'].toString());
    }
    return InFlightTurnMeta(
      turnId: turnId,
      lastSeq: lastSeq,
      active: active,
      prompt: prompt,
      startedAt: started,
    );
  }

  Map<String, dynamic> toJson() => {
        'turn_id': turnId,
        'last_seq': lastSeq,
        'active': active,
        if (prompt != null) 'prompt': prompt,
        if (startedAt != null) 'started_at': startedAt!.toIso8601String(),
      };
}

class PromptQueueItemModel {
  final String id;
  final String text;
  final DateTime? deliverAt;
  final DateTime createdAt;

  const PromptQueueItemModel({
    required this.id,
    required this.text,
    this.deliverAt,
    required this.createdAt,
  });

  bool get isDelayed => deliverAt != null && deliverAt!.isAfter(DateTime.now());

  factory PromptQueueItemModel.fromJson(Map<String, dynamic> json) {
    final id = json['id']?.toString() ?? '';
    final text = json['text']?.toString() ?? '';
    DateTime? deliverAt;
    if (json['deliver_at'] != null) {
      deliverAt = DateTime.tryParse(json['deliver_at'].toString());
    }
    DateTime createdAt = DateTime.now();
    if (json['created_at'] != null) {
      createdAt =
          DateTime.tryParse(json['created_at'].toString()) ?? DateTime.now();
    }
    return PromptQueueItemModel(
      id: id,
      text: text,
      deliverAt: deliverAt,
      createdAt: createdAt,
    );
  }
}

class PromptDispatchResult {
  final bool ok;
  final String status;
  final String? id;
  final bool deduplicated;
  final int? queueDepth;
  final String? error;

  const PromptDispatchResult({
    required this.ok,
    required this.status,
    this.id,
    this.deduplicated = false,
    this.queueDepth,
    this.error,
  });

  factory PromptDispatchResult.success({
    required String status,
    String? id,
    bool deduplicated = false,
    int? queueDepth,
  }) {
    return PromptDispatchResult(
      ok: true,
      status: status,
      id: id,
      deduplicated: deduplicated,
      queueDepth: queueDepth,
    );
  }

  factory PromptDispatchResult.failure(String error) {
    return PromptDispatchResult(
      ok: false,
      status: 'error',
      error: error,
    );
  }
}
