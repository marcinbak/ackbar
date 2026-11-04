import '../models/session.dart';
import '../models/tree_node.dart';

/// Normalizes a Git remote URL so SSH, HTTPS, git://, and trailing .git can be compared cleanly.
String normalizeGitUrl(String url) {
  if (url.trim().isEmpty) return '';
  var cleaned = url.trim().toLowerCase();
  // Strip protocol prefix
  cleaned = cleaned.replaceFirst(RegExp(r'^(?:https?|ssh|git)://'), '');
  // Strip user@ prefix (e.g. git@github.com: -> github.com:)
  cleaned = cleaned.replaceFirst(RegExp(r'^[^@]+@'), '');
  // Replace colon after domain with slash (e.g. github.com:org/repo -> github.com/org/repo)
  cleaned = cleaned.replaceFirst(':', '/');
  // Strip trailing .git or .git/
  cleaned = cleaned.replaceFirst(RegExp(r'\.git/?$'), '');
  // Strip trailing slashes
  while (cleaned.endsWith('/')) {
    cleaned = cleaned.substring(0, cleaned.length - 1);
  }
  return cleaned;
}

bool _sameOrSubDir(String cwd, String projDir) {
  if (cwd.isEmpty || projDir.isEmpty) return false;
  final c = cwd.replaceAll(r'\', '/');
  final p = projDir.replaceAll(r'\', '/');
  if (c == p) return true;
  if (c.startsWith('$p/')) return true;
  return false;
}

/// Resolves the logical tree node path for a session matching daemon & web behavior.
String resolveSessionNodePath(
  Session session,
  List<TreeNode> nodes, [
  String hostName = '',
]) {
  // 1. Explicit node_path assigned to session
  if (session.nodePath.isNotEmpty) {
    return session.nodePath;
  }

  // 2. Unassigned supervisors remain top-level until explicitly bound
  if (session.isSupervisor) {
    return '';
  }

  // 3. Match by normalized Git remote URL across any registered node or host path
  if (session.gitRemote.isNotEmpty) {
    final normSessGit = normalizeGitUrl(session.gitRemote);
    if (normSessGit.isNotEmpty) {
      for (final n in nodes) {
        if (n.gitUrl.isNotEmpty && normalizeGitUrl(n.gitUrl) == normSessGit) {
          return n.path;
        }
        for (final nhp in n.hostPaths) {
          if (nhp.gitRemote.isNotEmpty &&
              normalizeGitUrl(nhp.gitRemote) == normSessGit) {
            return n.path;
          }
        }
      }
    }
  }

  // 4. Longest exact or sub-directory prefix match against projectDir
  if (session.cwd.isNotEmpty) {
    String bestMatch = '';
    int bestLen = 0;

    for (final n in nodes) {
      if (n.projectDir.isNotEmpty && _sameOrSubDir(session.cwd, n.projectDir)) {
        if (n.projectDir.length > bestLen) {
          bestMatch = n.path;
          bestLen = n.projectDir.length;
        }
      }
      for (final nhp in n.hostPaths) {
        final hostLower = hostName.toLowerCase();
        final nhpHost = nhp.host.toLowerCase();
        final matchesHost = hostLower.isEmpty ||
            hostLower == 'local' ||
            nhpHost == 'local' ||
            nhpHost.isEmpty ||
            nhpHost == hostLower;
        if (matchesHost &&
            nhp.projectDir.isNotEmpty &&
            _sameOrSubDir(session.cwd, nhp.projectDir)) {
          if (nhp.projectDir.length > bestLen) {
            bestMatch = n.path;
            bestLen = nhp.projectDir.length;
          }
        }
      }
    }
    if (bestMatch.isNotEmpty) {
      return bestMatch;
    }

    // 5. Leaf directory name match (>3 chars, case-insensitive)
    final cwdParts = session.cwd
        .toLowerCase()
        .split(RegExp(r'[/\\]'))
        .where((s) => s.isNotEmpty)
        .toList();

    for (final n in nodes) {
      final leaf = n.path
          .split('/')
          .where((s) => s.isNotEmpty)
          .lastOrNull
          ?.toLowerCase();
      if (leaf != null && leaf.length > 3 && cwdParts.contains(leaf)) {
        return n.path;
      }
    }

    // 6. Ancestor group match (>3 chars, case-insensitive)
    for (final n in nodes) {
      final parts = n.path.split('/').where((s) => s.isNotEmpty).toList();
      final topGroup = parts.firstOrNull;
      if (topGroup != null &&
          topGroup.length > 3 &&
          cwdParts.contains(topGroup.toLowerCase())) {
        return topGroup;
      }
    }
  }

  // 7. Fallback to projectKey or empty string
  if (session.projectKey.isNotEmpty) {
    return session.projectKey;
  }

  return '';
}
