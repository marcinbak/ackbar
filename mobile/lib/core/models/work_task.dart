/// Models representing work board tasks, deliverables, workers, and workstreams.
library;

class TaskDeliverable {
  final String id;
  final String taskId;
  final String kind;
  final String title;
  final String host;
  final String filePath;
  final String url;
  final String provider;
  final String contentMd;
  final DateTime createdAt;
  final DateTime? updatedAt;

  const TaskDeliverable({
    required this.id,
    required this.taskId,
    required this.kind,
    required this.title,
    this.host = '',
    this.filePath = '',
    this.url = '',
    this.provider = '',
    this.contentMd = '',
    required this.createdAt,
    this.updatedAt,
  });

  bool get isUrl => url.isNotEmpty;
  bool get isMarkdown =>
      contentMd.isNotEmpty ||
      kind == 'markdown' ||
      kind == 'retro' ||
      kind == 'plan';

  String get providerDisplayName {
    switch (provider.toLowerCase()) {
      case 'claude':
        return 'Claude Artifact';
      case 'gdocs':
        return 'Google Docs';
      case 'gslides':
        return 'Google Slides';
      case 'gdrive':
        return 'Google Drive';
      case 'gsheets':
        return 'Google Sheets';
      default:
        return provider.isNotEmpty
            ? provider
            : (isUrl ? 'Web Link' : 'Markdown');
    }
  }

  String get providerIcon {
    switch (provider.toLowerCase()) {
      case 'claude':
        return '🤖';
      case 'gdocs':
        return '📄';
      case 'gslides':
        return '📊';
      case 'gdrive':
        return '📁';
      case 'gsheets':
        return '📈';
      default:
        return isUrl ? '🔗' : '📝';
    }
  }

  factory TaskDeliverable.fromJson(Map<String, dynamic> json) {
    return TaskDeliverable(
      id: json['id']?.toString() ?? '',
      taskId: json['task_id']?.toString() ?? '',
      kind: json['kind']?.toString() ?? '',
      title: json['title']?.toString() ?? '',
      host: json['host']?.toString() ?? '',
      filePath: json['file_path']?.toString() ?? '',
      url: json['url']?.toString() ?? '',
      provider: json['provider']?.toString() ?? '',
      contentMd: json['content_md']?.toString() ?? '',
      createdAt: json['created_at'] != null
          ? DateTime.tryParse(json['created_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
      updatedAt: json['updated_at'] != null
          ? DateTime.tryParse(json['updated_at'].toString())
          : null,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'task_id': taskId,
      'kind': kind,
      'title': title,
      if (host.isNotEmpty) 'host': host,
      if (filePath.isNotEmpty) 'file_path': filePath,
      if (url.isNotEmpty) 'url': url,
      if (provider.isNotEmpty) 'provider': provider,
      if (contentMd.isNotEmpty) 'content_md': contentMd,
      'created_at': createdAt.toIso8601String(),
      if (updatedAt != null) 'updated_at': updatedAt!.toIso8601String(),
    };
  }
}

class TaskExternalRef {
  final String taskId;
  final String tracker;
  final String refKey;
  final String url;

  const TaskExternalRef({
    required this.taskId,
    required this.tracker,
    required this.refKey,
    this.url = '',
  });

  factory TaskExternalRef.fromJson(Map<String, dynamic> json) {
    return TaskExternalRef(
      taskId: json['task_id']?.toString() ?? '',
      tracker: json['tracker']?.toString() ?? '',
      refKey: json['ref_key']?.toString() ?? '',
      url: json['url']?.toString() ?? '',
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'task_id': taskId,
      'tracker': tracker,
      'ref_key': refKey,
      if (url.isNotEmpty) 'url': url,
    };
  }
}

class TaskWorker {
  final String taskId;
  final String sessionId;
  final String agent;
  final String host;
  final bool isActive;
  final DateTime assignedAt;
  final String repoNodePath;
  final String workflowNode;

  const TaskWorker({
    required this.taskId,
    required this.sessionId,
    this.agent = '',
    this.host = '',
    this.isActive = false,
    required this.assignedAt,
    this.repoNodePath = '',
    this.workflowNode = '',
  });

  factory TaskWorker.fromJson(Map<String, dynamic> json) {
    return TaskWorker(
      taskId: json['task_id']?.toString() ?? '',
      sessionId: json['session_id']?.toString() ?? '',
      agent: json['agent']?.toString() ?? '',
      host: json['host']?.toString() ?? '',
      isActive: json['is_active'] == true || json['is_active'] == 1,
      assignedAt: json['assigned_at'] != null
          ? DateTime.tryParse(json['assigned_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
      repoNodePath: json['repo_node_path']?.toString() ?? '',
      workflowNode: json['workflow_node']?.toString() ?? '',
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'task_id': taskId,
      'session_id': sessionId,
      if (agent.isNotEmpty) 'agent': agent,
      if (host.isNotEmpty) 'host': host,
      'is_active': isActive,
      'assigned_at': assignedAt.toIso8601String(),
      if (repoNodePath.isNotEmpty) 'repo_node_path': repoNodePath,
      if (workflowNode.isNotEmpty) 'workflow_node': workflowNode,
    };
  }
}

class TaskWorkstream {
  final String taskId;
  final String repoNodePath;
  final String targetHost;
  final String dependsOn;
  final String workflowNode;
  final String status;
  final String branch;
  final String worktreePath;
  final String prUrl;
  final DateTime updatedAt;

  const TaskWorkstream({
    required this.taskId,
    required this.repoNodePath,
    this.targetHost = '',
    this.dependsOn = '',
    this.workflowNode = '',
    this.status = 'NEW',
    this.branch = '',
    this.worktreePath = '',
    this.prUrl = '',
    required this.updatedAt,
  });

  factory TaskWorkstream.fromJson(Map<String, dynamic> json) {
    return TaskWorkstream(
      taskId: json['task_id']?.toString() ?? '',
      repoNodePath: json['repo_node_path']?.toString() ?? '',
      targetHost: json['target_host']?.toString() ?? '',
      dependsOn: json['depends_on']?.toString() ?? '',
      workflowNode: json['workflow_node']?.toString() ?? '',
      status: json['status']?.toString() ?? 'NEW',
      branch: json['branch']?.toString() ?? '',
      worktreePath: json['worktree_path']?.toString() ?? '',
      prUrl: json['pr_url']?.toString() ?? '',
      updatedAt: json['updated_at'] != null
          ? DateTime.tryParse(json['updated_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'task_id': taskId,
      'repo_node_path': repoNodePath,
      if (targetHost.isNotEmpty) 'target_host': targetHost,
      if (dependsOn.isNotEmpty) 'depends_on': dependsOn,
      if (workflowNode.isNotEmpty) 'workflow_node': workflowNode,
      'status': status,
      if (branch.isNotEmpty) 'branch': branch,
      if (worktreePath.isNotEmpty) 'worktree_path': worktreePath,
      if (prUrl.isNotEmpty) 'pr_url': prUrl,
      'updated_at': updatedAt.toIso8601String(),
    };
  }
}

class WorkTask {
  final String id;
  final String title;
  final String groupName;
  final String projectName;
  final String subprojectName;
  final String status;
  final String substatus;
  final String notes;
  final String blockerQuestion;
  final String branch;
  final String worktreePath;
  final String prUrl;
  final int prNumber;
  final String prState;
  final String ciStatus;
  final DateTime createdAt;
  final DateTime updatedAt;
  final DateTime? completedAt;
  final DateTime? deletedAt;
  final String taskKind;
  final List<TaskExternalRef> externalRefs;
  final List<TaskWorker> workers;
  final List<TaskWorkstream> workstreams;
  final List<TaskDeliverable> deliverables;

  const WorkTask({
    required this.id,
    required this.title,
    this.groupName = '',
    this.projectName = '',
    this.subprojectName = '',
    this.status = 'NEW',
    this.substatus = '',
    this.notes = '',
    this.blockerQuestion = '',
    this.branch = '',
    this.worktreePath = '',
    this.prUrl = '',
    this.prNumber = 0,
    this.prState = '',
    this.ciStatus = '',
    required this.createdAt,
    required this.updatedAt,
    this.completedAt,
    this.deletedAt,
    this.taskKind = 'code',
    this.externalRefs = const [],
    this.workers = const [],
    this.workstreams = const [],
    this.deliverables = const [],
  });

  bool get isReview => status.toUpperCase() == 'REVIEW';
  bool get isApprovedReview =>
      isReview && substatus.toLowerCase() == 'approved';
  bool get hasDeliverables => deliverables.isNotEmpty;

  /// Primary tracking key if present (e.g. NGL-409)
  String get primaryTrackerRef =>
      externalRefs.isNotEmpty ? externalRefs.first.refKey : '';

  factory WorkTask.fromJson(Map<String, dynamic> json) {
    List<TaskExternalRef> refs = [];
    if (json['external_refs'] is List) {
      refs = (json['external_refs'] as List)
          .map((e) => TaskExternalRef.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    List<TaskWorker> workers = [];
    if (json['workers'] is List) {
      workers = (json['workers'] as List)
          .map((e) => TaskWorker.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    List<TaskWorkstream> streams = [];
    if (json['workstreams'] is List) {
      streams = (json['workstreams'] as List)
          .map((e) => TaskWorkstream.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    List<TaskDeliverable> deliverables = [];
    if (json['deliverables'] is List) {
      deliverables = (json['deliverables'] as List)
          .map((e) => TaskDeliverable.fromJson(e as Map<String, dynamic>))
          .toList();
    }

    return WorkTask(
      id: json['id']?.toString() ?? '',
      title: json['title']?.toString() ?? '',
      groupName: json['group_name']?.toString() ?? '',
      projectName: json['project_name']?.toString() ?? '',
      subprojectName: json['subproject_name']?.toString() ?? '',
      status: json['status']?.toString() ?? 'NEW',
      substatus: json['substatus']?.toString() ?? '',
      notes: json['notes']?.toString() ?? '',
      blockerQuestion: json['blocker_question']?.toString() ?? '',
      branch: json['branch']?.toString() ?? '',
      worktreePath: json['worktree_path']?.toString() ?? '',
      prUrl: json['pr_url']?.toString() ?? '',
      prNumber: json['pr_number'] is int
          ? json['pr_number'] as int
          : int.tryParse(json['pr_number']?.toString() ?? '') ?? 0,
      prState: json['pr_state']?.toString() ?? '',
      ciStatus: json['ci_status']?.toString() ?? '',
      createdAt: json['created_at'] != null
          ? DateTime.tryParse(json['created_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
      updatedAt: json['updated_at'] != null
          ? DateTime.tryParse(json['updated_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
      completedAt: json['completed_at'] != null
          ? DateTime.tryParse(json['completed_at'].toString())
          : null,
      deletedAt: json['deleted_at'] != null
          ? DateTime.tryParse(json['deleted_at'].toString())
          : null,
      taskKind: json['task_kind']?.toString() ?? 'code',
      externalRefs: refs,
      workers: workers,
      workstreams: streams,
      deliverables: deliverables,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'title': title,
      'group_name': groupName,
      'project_name': projectName,
      if (subprojectName.isNotEmpty) 'subproject_name': subprojectName,
      'status': status,
      'substatus': substatus,
      if (notes.isNotEmpty) 'notes': notes,
      if (blockerQuestion.isNotEmpty) 'blocker_question': blockerQuestion,
      if (branch.isNotEmpty) 'branch': branch,
      if (worktreePath.isNotEmpty) 'worktree_path': worktreePath,
      if (prUrl.isNotEmpty) 'pr_url': prUrl,
      if (prNumber > 0) 'pr_number': prNumber,
      if (prState.isNotEmpty) 'pr_state': prState,
      if (ciStatus.isNotEmpty) 'ci_status': ciStatus,
      'created_at': createdAt.toIso8601String(),
      'updated_at': updatedAt.toIso8601String(),
      if (completedAt != null) 'completed_at': completedAt!.toIso8601String(),
      if (deletedAt != null) 'deleted_at': deletedAt!.toIso8601String(),
      'task_kind': taskKind,
      'external_refs': externalRefs.map((r) => r.toJson()).toList(),
      'workers': workers.map((w) => w.toJson()).toList(),
      'workstreams': workstreams.map((ws) => ws.toJson()).toList(),
      'deliverables': deliverables.map((d) => d.toJson()).toList(),
    };
  }
}
