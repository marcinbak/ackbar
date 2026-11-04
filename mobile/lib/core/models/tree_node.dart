/// Logical project tree node and host-path mappings
class NodeHostPath {
  final String nodePath;
  final String host;
  final String projectDir;
  final String gitRemote;
  final DateTime updatedAt;

  const NodeHostPath({
    required this.nodePath,
    required this.host,
    required this.projectDir,
    this.gitRemote = '',
    required this.updatedAt,
  });

  factory NodeHostPath.fromJson(Map<String, dynamic> json) {
    return NodeHostPath(
      nodePath: json['node_path']?.toString() ?? '',
      host: json['host']?.toString() ?? '',
      projectDir: json['project_dir']?.toString() ?? '',
      gitRemote: json['git_remote']?.toString() ?? '',
      updatedAt: json['updated_at'] != null
          ? DateTime.tryParse(json['updated_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'node_path': nodePath,
      'host': host,
      'project_dir': projectDir,
      if (gitRemote.isNotEmpty) 'git_remote': gitRemote,
      'updated_at': updatedAt.toIso8601String(),
    };
  }
}

class TreeNode {
  final String path;
  final String projectDir;
  final String gitUrl;
  final DateTime createdAt;
  final DateTime updatedAt;
  final List<NodeHostPath> hostPaths;

  const TreeNode({
    required this.path,
    this.projectDir = '',
    this.gitUrl = '',
    required this.createdAt,
    required this.updatedAt,
    this.hostPaths = const [],
  });

  factory TreeNode.fromJson(Map<String, dynamic> json) {
    List<NodeHostPath> paths = [];
    if (json['host_paths'] is List) {
      paths = (json['host_paths'] as List)
          .map((e) => NodeHostPath.fromJson(e as Map<String, dynamic>))
          .toList();
    }

    return TreeNode(
      path: json['path']?.toString() ?? '',
      projectDir: json['project_dir']?.toString() ?? '',
      gitUrl: json['git_url']?.toString() ?? '',
      createdAt: json['created_at'] != null
          ? DateTime.tryParse(json['created_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
      updatedAt: json['updated_at'] != null
          ? DateTime.tryParse(json['updated_at'].toString()) ?? DateTime.now()
          : DateTime.now(),
      hostPaths: paths,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'path': path,
      if (projectDir.isNotEmpty) 'project_dir': projectDir,
      if (gitUrl.isNotEmpty) 'git_url': gitUrl,
      'created_at': createdAt.toIso8601String(),
      'updated_at': updatedAt.toIso8601String(),
      if (hostPaths.isNotEmpty)
        'host_paths': hostPaths.map((hp) => hp.toJson()).toList(),
    };
  }
}
