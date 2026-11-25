import 'package:flutter/foundation.dart';

enum ChecklistStatus {
  pending,
  inProgress,
  done,
  skipped,
  failed,
}

ChecklistStatus parseChecklistStatus(String? raw) {
  switch (raw?.toLowerCase().trim()) {
    case 'done':
    case 'completed':
      return ChecklistStatus.done;
    case 'in_progress':
    case 'inprogress':
    case 'running':
      return ChecklistStatus.inProgress;
    case 'skipped':
    case 'cancelled':
    case 'canceled':
      return ChecklistStatus.skipped;
    case 'failed':
    case 'error':
      return ChecklistStatus.failed;
    default:
      return ChecklistStatus.pending;
  }
}

extension ChecklistStatusJson on ChecklistStatus {
  String toJsonString() {
    switch (this) {
      case ChecklistStatus.pending:
        return 'pending';
      case ChecklistStatus.inProgress:
        return 'in_progress';
      case ChecklistStatus.done:
        return 'done';
      case ChecklistStatus.skipped:
        return 'skipped';
      case ChecklistStatus.failed:
        return 'failed';
    }
  }
}

@immutable
class ChecklistItem {
  final String id;
  final String title;
  final String? activeForm;
  final ChecklistStatus status;

  const ChecklistItem({
    this.id = '',
    required this.title,
    this.activeForm,
    this.status = ChecklistStatus.pending,
  });

  String get displayLabel => (status == ChecklistStatus.inProgress &&
          activeForm != null &&
          activeForm!.trim().isNotEmpty)
      ? activeForm!.trim()
      : title;

  factory ChecklistItem.fromJson(Map<String, dynamic> json) {
    final rawTitle = (json['title'] ?? json['content'] ?? json['text'] ?? '')
        .toString()
        .trim();
    final rawActiveForm =
        (json['active_form'] ?? json['activeForm'])?.toString().trim();
    return ChecklistItem(
      id: json['id']?.toString().trim() ?? '',
      title: rawTitle,
      activeForm: (rawActiveForm != null && rawActiveForm.isNotEmpty)
          ? rawActiveForm
          : null,
      status: parseChecklistStatus(json['status']?.toString()),
    );
  }

  static List<ChecklistItem> listFromJson(dynamic raw) {
    if (raw is! List) return const [];
    final items = <ChecklistItem>[];
    for (final entry in raw) {
      if (entry is Map) {
        final item = ChecklistItem.fromJson(Map<String, dynamic>.from(entry));
        if (item.title.isNotEmpty) {
          items.add(item);
        }
      }
    }
    return List<ChecklistItem>.unmodifiable(items);
  }

  Map<String, dynamic> toJson() => {
        if (id.isNotEmpty) 'id': id,
        'title': title,
        if (activeForm != null && activeForm!.isNotEmpty)
          'active_form': activeForm,
        'status': status.toJsonString(),
      };

  ChecklistItem copyWith({
    String? id,
    String? title,
    String? activeForm,
    ChecklistStatus? status,
  }) {
    return ChecklistItem(
      id: id ?? this.id,
      title: title ?? this.title,
      activeForm: activeForm ?? this.activeForm,
      status: status ?? this.status,
    );
  }

  @override
  bool operator ==(Object other) {
    if (identical(this, other)) return true;
    return other is ChecklistItem &&
        other.id == id &&
        other.title == title &&
        other.activeForm == activeForm &&
        other.status == status;
  }

  @override
  int get hashCode => Object.hash(id, title, activeForm, status);
}
