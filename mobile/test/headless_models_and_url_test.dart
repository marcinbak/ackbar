import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/headless_models.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/services/external_url_launcher.dart';

void main() {
  group('isSafeHttpsUrl Tests', () {
    test('accepts valid https URLs', () {
      expect(isSafeHttpsUrl('https://auth.example.com/oauth/authorize?flow=1'),
          isTrue);
      expect(isSafeHttpsUrl('https://accounts.google.com/o/oauth2/v2/auth'),
          isTrue);
      expect(isSafeHttpsUrl('https://127.0.0.1:8080/callback'), isTrue);
    });

    test('rejects non-https schemes', () {
      expect(isSafeHttpsUrl('http://example.com'), isFalse);
      expect(isSafeHttpsUrl('javascript:alert(1)'), isFalse);
      expect(isSafeHttpsUrl('file:///etc/passwd'), isFalse);
      expect(isSafeHttpsUrl('data:text/html,test'), isFalse);
    });

    test('rejects empty, null, and untrimmed URLs', () {
      expect(isSafeHttpsUrl(null), isFalse);
      expect(isSafeHttpsUrl(''), isFalse);
      expect(isSafeHttpsUrl('   '), isFalse);
      expect(isSafeHttpsUrl(' https://example.com'), isFalse);
      expect(isSafeHttpsUrl('https://example.com '), isFalse);
    });

    test('rejects URLs with userInfo or without host', () {
      expect(isSafeHttpsUrl('https://user:pass@example.com'), isFalse);
      expect(isSafeHttpsUrl('https://'), isFalse);
    });

    test('rejects control characters and whitespace', () {
      expect(isSafeHttpsUrl('https://example.com/\x00test'), isFalse);
      expect(isSafeHttpsUrl('https://example.com/foo bar'), isFalse);
      expect(isSafeHttpsUrl('https://example.com/\ntest'), isFalse);
    });

    test('rejects URLs exceeding 4096 characters', () {
      final longUrl = 'https://example.com/?q=${"a" * 4080}';
      expect(isSafeHttpsUrl(longUrl), isFalse);
    });
  });

  group('BlockKind and Blocked Model Tests', () {
    test('parses all BlockKind variants including auth and rateLimit', () {
      expect(BlockKind.fromString('auth'), BlockKind.auth);
      expect(BlockKind.fromString('approval'), BlockKind.approval);
      expect(BlockKind.fromString('rate_limit'), BlockKind.rateLimit);
      expect(BlockKind.fromString('ratelimit'), BlockKind.rateLimit);
      expect(BlockKind.fromString('permission'), BlockKind.permission);
      expect(BlockKind.fromString('question'), BlockKind.question);
      expect(BlockKind.fromString('unknown_val'), BlockKind.unknown);
      expect(BlockKind.fromString(null), BlockKind.unknown);
    });

    test('serializes rateLimit as rate_limit', () {
      final blocked = Blocked(
        kind: BlockKind.rateLimit,
        reason: 'Too many requests',
        since: DateTime.parse('2026-11-11T10:00:00Z'),
        resetAt: DateTime.parse('2026-11-11T10:05:00Z'),
      );
      final json = blocked.toJson();
      expect(json['kind'], 'rate_limit');
      expect(json['reset_at'], '2026-11-11T10:05:00.000Z');
    });

    test('Blocked parses and serializes OAuth broker fields', () {
      final now = DateTime.now();
      final blocked = Blocked(
        kind: BlockKind.auth,
        reason: 'Sign-in required',
        since: now,
        authTarget: 'mcp:notion',
        authKind: 'oauth2',
        authServer: 'Notion',
        authUrl: 'https://auth.notion.com/login',
        authHint: 'Use company email',
        flowId: 'flow_123',
      );

      expect(blocked.isAuth, isTrue);
      final json = blocked.toJson();
      expect(json['kind'], 'auth');
      expect(json['auth_target'], 'mcp:notion');
      expect(json['auth_url'], 'https://auth.notion.com/login');
      expect(json['flow_id'], 'flow_123');

      final roundTrip = Blocked.fromJson(json);
      expect(roundTrip.kind, BlockKind.auth);
      expect(roundTrip.authTarget, 'mcp:notion');
      expect(roundTrip.authServer, 'Notion');
      expect(roundTrip.flowId, 'flow_123');
    });
  });

  group('Session Headless Config Fields Tests', () {
    test(
        'Session parses and serializes model, effort, permissionMode, extraDirs',
        () {
      final json = {
        'id': 'sess-1',
        'agent': 'claude-code',
        'host': 'local',
        'native_id': 'nat-1',
        'cwd': '/work',
        'state': 1,
        'started_at': '2026-11-11T10:00:00Z',
        'last_event_at': '2026-11-11T10:00:00Z',
        'model': 'claude-3-7-sonnet',
        'effort': 'xhigh',
        'permission_mode': 'bypassPermissions',
        'extra_dirs': ['/tmp/extra1', '/tmp/extra2'],
      };

      final session = Session.fromJson(json);
      expect(session.model, 'claude-3-7-sonnet');
      expect(session.effort, 'xhigh');
      expect(session.permissionMode, 'bypassPermissions');
      expect(session.extraDirs, ['/tmp/extra1', '/tmp/extra2']);

      final serialized = session.toJson();
      expect(serialized['model'], 'claude-3-7-sonnet');
      expect(serialized['effort'], 'xhigh');
      expect(serialized['permission_mode'], 'bypassPermissions');
      expect(serialized['extra_dirs'], ['/tmp/extra1', '/tmp/extra2']);

      final copied = session.copyWith(
        model: 'claude-3-5-haiku',
        effort: 'low',
        permissionMode: 'plan',
        extraDirs: ['/var/log'],
      );
      expect(copied.model, 'claude-3-5-haiku');
      expect(copied.effort, 'low');
      expect(copied.permissionMode, 'plan');
      expect(copied.extraDirs, ['/var/log']);
    });
  });

  group('Headless DTO Tests', () {
    test('AuthToolStartResult parses from JSON', () {
      final res = AuthToolStartResult.fromJson({
        'status': 'started',
        'flow_id': 'flow_456',
        'auth_url': 'https://auth.example.com',
        'recorded_port': 8080,
        'accepts_stdin': true,
      });
      expect(res.status, 'started');
      expect(res.flowId, 'flow_456');
      expect(res.authUrl, 'https://auth.example.com');
      expect(res.recordedPort, 8080);
      expect(res.acceptsStdin, isTrue);
    });

    test('AuthCallbackResult parses from JSON', () {
      final res = AuthCallbackResult.fromJson(
          {'status': 'authenticated', 'completed': true});
      expect(res.status, 'authenticated');
      expect(res.completed, isTrue);
    });

    test('SessionMcpStatus parses servers and plugins', () {
      final res = SessionMcpStatus.fromJson({
        'servers': [
          {
            'name': 'github',
            'transport': 'stdio',
            'scope': 'user',
            'status': 'configured',
            'auth_supported': false,
            'target': ''
          },
          {
            'name': 'slack',
            'transport': 'sse',
            'scope': 'workspace',
            'status': 'needs_auth',
            'auth_supported': true,
            'target': 'mcp:slack'
          },
        ],
        'plugins': [
          {
            'name': 'gdocs',
            'installed': true,
            'authenticated': true,
            'auth_supported': true,
            'target': 'plugin:gdocs'
          },
        ],
      });
      expect(res.servers.length, 2);
      expect(res.servers[0].isConfigured, isTrue);
      expect(res.servers[1].needsAuth, isTrue);
      expect(res.plugins.length, 1);
      expect(res.plugins[0].authenticated, isTrue);
    });

    test('ShellExecResult, DiffInspectResult, and SessionRotationResult parse',
        () {
      final shell = ShellExecResult.fromJson({
        'command': 'ls -la',
        'cwd': '/work',
        'stdout': 'file.txt',
        'stderr': '',
        'exit_code': 0,
        'duration_ms': 15,
        'timed_out': false,
        'truncated': false,
      });
      expect(shell.command, 'ls -la');
      expect(shell.exitCode, 0);

      final diff = DiffInspectResult.fromJson({
        'is_git_repo': true,
        'cwd': '/work',
        'branch': 'main',
        'status_lines': [' M lib/main.dart'],
        'stat': ' 1 file changed',
        'diff': '+ new line',
        'truncated': false,
        'clean': false,
      });
      expect(diff.branch, 'main');
      expect(diff.clean, isFalse);

      final rot = SessionRotationResult.fromJson({
        'status': 'ok',
        'mode': 'empty',
        'strategy': 'in_place',
        'old_session_id': 'old-1',
        'new_session_id': 'new-1',
        'briefing': 'Summary text',
      });
      expect(rot.oldSessionId, 'old-1');
      expect(rot.newSessionId, 'new-1');
    });
  });

  group('ExternalUrlLauncher Tests', () {
    test('returns rejectedUnsafe on non-https url', () async {
      final launcher = ExternalUrlLauncher(
        nativeOpener: (u) async => true,
        clipboardWriter: (t) async {},
      );
      final outcome = await launcher.launchHttpsUrl('http://unsafe.com');
      expect(outcome, UrlLaunchOutcome.rejectedUnsafe);
    });

    test('returns openedBrowser when native opener succeeds', () async {
      final launcher = ExternalUrlLauncher(
        nativeOpener: (u) async => true,
        clipboardWriter: (t) async {},
      );
      final outcome = await launcher.launchHttpsUrl('https://safe.com');
      expect(outcome, UrlLaunchOutcome.openedBrowser);
    });

    test('returns copiedFallback when native opener fails', () async {
      String? copied;
      final launcher = ExternalUrlLauncher(
        nativeOpener: (u) async => false,
        clipboardWriter: (t) async => copied = t,
      );
      final outcome = await launcher.launchHttpsUrl('https://safe.com/login');
      expect(outcome, UrlLaunchOutcome.copiedFallback);
      expect(copied, 'https://safe.com/login');
    });
  });
}
