import 'package:flutter/material.dart';
import '../theme/app_colors.dart';
import '../widgets/status_badge.dart';

/// Plan review and execution lifecycle status
enum PlanStatus {
  pendingReview,
  inProgress,
  approved,
  completed,
  rejected;

  String get label {
    switch (this) {
      case PlanStatus.pendingReview:
        return 'PENDING REVIEW';
      case PlanStatus.inProgress:
        return 'IN PROGRESS';
      case PlanStatus.approved:
        return 'APPROVED';
      case PlanStatus.completed:
        return 'COMPLETED';
      case PlanStatus.rejected:
        return 'REJECTED';
    }
  }

  Color get color {
    switch (this) {
      case PlanStatus.pendingReview:
        return AppColors.statusAmber;
      case PlanStatus.inProgress:
        return AppColors.infoCyan;
      case PlanStatus.approved:
      case PlanStatus.completed:
        return AppColors.statusEmerald;
      case PlanStatus.rejected:
        return AppColors.statusCoral;
    }
  }

  AckbarSessionStatus toBadgeStatus() {
    switch (this) {
      case PlanStatus.pendingReview:
        return AckbarSessionStatus.blocked;
      case PlanStatus.inProgress:
        return AckbarSessionStatus.working;
      case PlanStatus.approved:
      case PlanStatus.completed:
        return AckbarSessionStatus.idle;
      case PlanStatus.rejected:
        return AckbarSessionStatus.offline;
    }
  }
}

/// Architectural plan and implementation proposal document
class PlanDocument {
  final String id;
  final String title;
  final String agent;
  final String host;
  final String project;
  final String goal;
  final String userReviewCallout;
  final int filesChanged;
  final int addedLines;
  final int deletedLines;
  final List<String> verificationSteps;
  final PlanStatus status;
  final double progressPct; // 0.0 to 1.0
  final String currentStep;
  final String diffContent;
  final String testSummary;
  final DateTime createdAt;

  const PlanDocument({
    required this.id,
    required this.title,
    required this.agent,
    required this.host,
    required this.project,
    required this.goal,
    this.userReviewCallout = '',
    this.filesChanged = 0,
    this.addedLines = 0,
    this.deletedLines = 0,
    this.verificationSteps = const [],
    this.status = PlanStatus.pendingReview,
    this.progressPct = 0.0,
    this.currentStep = '',
    this.diffContent = '',
    this.testSummary = '',
    required this.createdAt,
  });

  bool get isPendingReview => status == PlanStatus.pendingReview;
  bool get isInProgress => status == PlanStatus.inProgress;
  bool get isApproved => status == PlanStatus.approved;
  bool get isCompleted => status == PlanStatus.completed;

  String get linesDeltaFormatted => '+$addedLines / -$deletedLines';

  /// Agent display name
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

  factory PlanDocument.fromJson(Map<String, dynamic> json) {
    PlanStatus parseStatus(dynamic val) {
      if (val == null) return PlanStatus.pendingReview;
      final s = val.toString().toLowerCase();
      if (s.contains('pending') || s.contains('review')) return PlanStatus.pendingReview;
      if (s.contains('progress') || s.contains('working')) return PlanStatus.inProgress;
      if (s.contains('approved')) return PlanStatus.approved;
      if (s.contains('completed') || s.contains('done')) return PlanStatus.completed;
      if (s.contains('rejected')) return PlanStatus.rejected;
      return PlanStatus.pendingReview;
    }

    DateTime parseDate(dynamic val) {
      if (val == null) return DateTime.now();
      return DateTime.tryParse(val.toString()) ?? DateTime.now();
    }

    List<String> parseSteps(dynamic val) {
      if (val is List) {
        return val.map((e) => e.toString()).toList();
      }
      return const [];
    }

    return PlanDocument(
      id: json['id']?.toString() ?? 'PLAN-001',
      title: json['title']?.toString() ?? 'Untitled Plan',
      agent: json['agent']?.toString() ?? 'Claude Code',
      host: json['host']?.toString() ?? 'local',
      project: json['project']?.toString() ?? 'Default Project',
      goal: json['goal']?.toString() ?? '',
      userReviewCallout: json['user_review_callout']?.toString() ?? '',
      filesChanged: json['files_changed'] is int
          ? json['files_changed'] as int
          : int.tryParse(json['files_changed']?.toString() ?? '') ?? 0,
      addedLines: json['added_lines'] is int
          ? json['added_lines'] as int
          : int.tryParse(json['added_lines']?.toString() ?? '') ?? 0,
      deletedLines: json['deleted_lines'] is int
          ? json['deleted_lines'] as int
          : int.tryParse(json['deleted_lines']?.toString() ?? '') ?? 0,
      verificationSteps: parseSteps(json['verification_steps']),
      status: parseStatus(json['status']),
      progressPct: (json['progress_pct'] as num?)?.toDouble() ?? 0.0,
      currentStep: json['current_step']?.toString() ?? '',
      diffContent: json['diff_content']?.toString() ?? '',
      testSummary: json['test_summary']?.toString() ?? '',
      createdAt: parseDate(json['created_at']),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'title': title,
      'agent': agent,
      'host': host,
      'project': project,
      'goal': goal,
      'user_review_callout': userReviewCallout,
      'files_changed': filesChanged,
      'added_lines': addedLines,
      'deleted_lines': deletedLines,
      'verification_steps': verificationSteps,
      'status': status.name,
      'progress_pct': progressPct,
      'current_step': currentStep,
      'diff_content': diffContent,
      'test_summary': testSummary,
      'created_at': createdAt.toIso8601String(),
    };
  }

  PlanDocument copyWith({
    String? id,
    String? title,
    String? agent,
    String? host,
    String? project,
    String? goal,
    String? userReviewCallout,
    int? filesChanged,
    int? addedLines,
    int? deletedLines,
    List<String>? verificationSteps,
    PlanStatus? status,
    double? progressPct,
    String? currentStep,
    String? diffContent,
    String? testSummary,
    DateTime? createdAt,
  }) {
    return PlanDocument(
      id: id ?? this.id,
      title: title ?? this.title,
      agent: agent ?? this.agent,
      host: host ?? this.host,
      project: project ?? this.project,
      goal: goal ?? this.goal,
      userReviewCallout: userReviewCallout ?? this.userReviewCallout,
      filesChanged: filesChanged ?? this.filesChanged,
      addedLines: addedLines ?? this.addedLines,
      deletedLines: deletedLines ?? this.deletedLines,
      verificationSteps: verificationSteps ?? this.verificationSteps,
      status: status ?? this.status,
      progressPct: progressPct ?? this.progressPct,
      currentStep: currentStep ?? this.currentStep,
      diffContent: diffContent ?? this.diffContent,
      testSummary: testSummary ?? this.testSummary,
      createdAt: createdAt ?? this.createdAt,
    );
  }
}
