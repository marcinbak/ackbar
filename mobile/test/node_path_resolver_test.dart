import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/tree_node.dart';
import 'package:ackbar_mobile/core/utils/node_path_resolver.dart';

void main() {
  group('normalizeGitUrl Tests', () {
    test('normalizes https, ssh, and trailing .git', () {
      expect(
        normalizeGitUrl('https://github.com/modemobile/ngl-ios.git'),
        equals('github.com/modemobile/ngl-ios'),
      );
      expect(
        normalizeGitUrl('git@github.com:modemobile/ngl-ios.git'),
        equals('github.com/modemobile/ngl-ios'),
      );
      expect(
        normalizeGitUrl('ssh://git@github.com/modemobile/ngl-ios'),
        equals('github.com/modemobile/ngl-ios'),
      );
      expect(
        normalizeGitUrl('https://github.com/org/repo/'),
        equals('github.com/org/repo'),
      );
      expect(normalizeGitUrl(''), equals(''));
    });
  });

  group('resolveSessionNodePath Tests', () {
    final nodes = [
      TreeNode(
        path: 'Modemobile/NGL/ngl-ios',
        projectDir: '/Users/dev4u/Work/ngl-ios',
        gitUrl: 'https://github.com/modemobile/ngl-ios.git',
        createdAt: DateTime.now(),
        updatedAt: DateTime.now(),
        hostPaths: [
          NodeHostPath(
            nodePath: 'Modemobile/NGL/ngl-ios',
            host: 'mac.local',
            projectDir: '/Users/dev4u/Work/ngl-ios',
            gitRemote: 'git@github.com:modemobile/ngl-ios.git',
            updatedAt: DateTime.now(),
          ),
          NodeHostPath(
            nodePath: 'Modemobile/NGL/ngl-ios',
            host: 'linux.local',
            projectDir: '/home/dev4u/workspace/ngl-ios',
            gitRemote: 'https://github.com/modemobile/ngl-ios.git',
            updatedAt: DateTime.now(),
          ),
        ],
      ),
      TreeNode(
        path: 'Personal/Ackbar',
        projectDir: '/home/dev4u/Personal/ackbar',
        gitUrl: 'git@github.com:marcinbak/ackbar.git',
        createdAt: DateTime.now(),
        updatedAt: DateTime.now(),
      ),
    ];

    Session makeSession({
      String id = 'sess-1',
      String nodePath = '',
      bool isSupervisor = false,
      String gitRemote = '',
      String cwd = '',
      String projectKey = '',
      String host = 'mac.local',
    }) {
      return Session(
        id: id,
        agent: 'claude-code',
        host: host,
        nativeId: 'nat-1',
        cwd: cwd,
        nodePath: nodePath,
        name: 'Test Session',
        customTitle: '',
        firstPrompt: '',
        lastPrompt: '',
        state: SessionState.working,
        activity: '',
        startedAt: DateTime.now(),
        lastEventAt: DateTime.now(),
        managed: true,
        engineType: 'headless',
        isSupervisor: isSupervisor,
        gitRemote: gitRemote,
        projectKey: projectKey,
      );
    }

    test('Rule 1: Direct session.nodePath takes immediate precedence', () {
      final s = makeSession(nodePath: 'Custom/Path/Direct');
      expect(resolveSessionNodePath(s, nodes, 'mac.local'),
          equals('Custom/Path/Direct'));
    });

    test('Rule 2: Unbound supervisor returns empty string', () {
      final s = makeSession(
        isSupervisor: true,
        cwd: '/Users/dev4u/Work/ngl-ios',
      );
      expect(resolveSessionNodePath(s, nodes, 'mac.local'), equals(''));
    });

    test('Rule 3: Normalized Git remote URL match', () {
      final s = makeSession(
        gitRemote: 'git@github.com:modemobile/ngl-ios.git',
        cwd: '/tmp/somewhere/unknown',
      );
      expect(resolveSessionNodePath(s, nodes, 'mac.local'),
          equals('Modemobile/NGL/ngl-ios'));
    });

    test('Rule 4: Exact and subdirectory CWD prefix match', () {
      final sExact = makeSession(
        cwd: '/home/dev4u/Personal/ackbar',
      );
      expect(resolveSessionNodePath(sExact, nodes, 'linux.local'),
          equals('Personal/Ackbar'));

      final sSubdir = makeSession(
        cwd: '/home/dev4u/Personal/ackbar/cmd/ackbar',
      );
      expect(resolveSessionNodePath(sSubdir, nodes, 'linux.local'),
          equals('Personal/Ackbar'));
    });

    test('Rule 5: Leaf directory segment match (>3 chars)', () {
      final sLeaf = makeSession(
        cwd: '/var/tmp/builds/ngl-ios/sub/dir',
      );
      expect(resolveSessionNodePath(sLeaf, nodes, 'other.host'),
          equals('Modemobile/NGL/ngl-ios'));
    });

    test('Rule 6: Ancestor group segment match', () {
      final sAncestor = makeSession(
        cwd: '/data/Modemobile/scratch',
      );
      expect(resolveSessionNodePath(sAncestor, nodes, 'other.host'),
          equals('Modemobile'));
    });

    test('Rule 7: Fallback to projectKey if non-empty, else empty string', () {
      final sKey = makeSession(
        projectKey: 'ProjectAlpha',
        cwd: '/unknown/random/path',
      );
      expect(resolveSessionNodePath(sKey, nodes, 'other.host'),
          equals('ProjectAlpha'));

      final sEmpty = makeSession(
        cwd: '/unknown/random/path',
      );
      expect(resolveSessionNodePath(sEmpty, nodes, 'other.host'), equals(''));
    });
  });
}
