import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/plan.dart';
import 'package:ackbar_mobile/core/theme/app_colors.dart';
import 'package:ackbar_mobile/core/widgets/status_badge.dart';

void main() {
  group('SessionState Enum Tests', () {
    test('SessionState.fromInt parses all integer values correctly', () {
      expect(SessionState.fromInt(0), equals(SessionState.unknown));
      expect(SessionState.fromInt(1), equals(SessionState.working));
      expect(SessionState.fromInt(2), equals(SessionState.blocked));
      expect(SessionState.fromInt(3), equals(SessionState.idle));
      expect(SessionState.fromInt(4), equals(SessionState.ended));
      expect(SessionState.fromInt(5), equals(SessionState.failed));
      expect(SessionState.fromInt(99), equals(SessionState.unknown));
      expect(SessionState.fromInt(-1), equals(SessionState.unknown));
    });

    test('SessionState.fromString parses case-insensitively', () {
      expect(SessionState.fromString('working'), equals(SessionState.working));
      expect(SessionState.fromString('WORKING'), equals(SessionState.working));
      expect(SessionState.fromString('blocked'), equals(SessionState.blocked));
      expect(SessionState.fromString('BLOCKED'), equals(SessionState.blocked));
      expect(SessionState.fromString('idle'), equals(SessionState.idle));
      expect(SessionState.fromString('ended'), equals(SessionState.ended));
      expect(SessionState.fromString('failed'), equals(SessionState.failed));
      expect(SessionState.fromString('invalid_state'), equals(SessionState.unknown));
      expect(SessionState.fromString(''), equals(SessionState.unknown));
    });

    test('SessionState label returns expected strings', () {
      expect(SessionState.working.label, equals('WORKING'));
      expect(SessionState.blocked.label, equals('BLOCKED'));
      expect(SessionState.idle.label, equals('IDLE'));
      expect(SessionState.ended.label, equals('ENDED'));
      expect(SessionState.failed.label, equals('FAILED'));
      expect(SessionState.unknown.label, equals('UNKNOWN'));
    });

    test('SessionState toBadgeStatus maps correctly to AckbarSessionStatus', () {
      expect(SessionState.working.toBadgeStatus(), equals(AckbarSessionStatus.working));
      expect(SessionState.blocked.toBadgeStatus(), equals(AckbarSessionStatus.blocked));
      expect(SessionState.idle.toBadgeStatus(), equals(AckbarSessionStatus.idle));
      expect(SessionState.ended.toBadgeStatus(), equals(AckbarSessionStatus.offline));
      expect(SessionState.failed.toBadgeStatus(), equals(AckbarSessionStatus.offline));
      expect(SessionState.unknown.toBadgeStatus(), equals(AckbarSessionStatus.offline));
    });
  });

  group('BlockKind Enum Tests', () {
    test('BlockKind.fromString handles permission, question, unknown, and null', () {
      expect(BlockKind.fromString('permission'), equals(BlockKind.permission));
      expect(BlockKind.fromString('PERMISSION'), equals(BlockKind.permission));
      expect(BlockKind.fromString('question'), equals(BlockKind.question));
      expect(BlockKind.fromString('QUESTION'), equals(BlockKind.question));
      expect(BlockKind.fromString('other'), equals(BlockKind.unknown));
      expect(BlockKind.fromString(null), equals(BlockKind.unknown));
    });

    test('BlockKind label returns expected values', () {
      expect(BlockKind.permission.label, equals('PERMISSION'));
      expect(BlockKind.question.label, equals('QUESTION'));
      expect(BlockKind.unknown.label, equals('BLOCKED'));
    });
  });

  group('Blocked Model Tests', () {
    test('Blocked.fromJson parses valid JSON with all fields', () {
      final json = {
        'kind': 'question',
        'reason': 'Choice required',
        'since': '2026-08-20T12:00:00.000Z',
        'question': 'Which driver?',
        'options': ['Option A', 'Option B'],
        'command': 'go test',
      };

      final blocked = Blocked.fromJson(json);
      expect(blocked.kind, equals(BlockKind.question));
      expect(blocked.reason, equals('Choice required'));
      expect(blocked.since, equals(DateTime.parse('2026-08-20T12:00:00.000Z')));
      expect(blocked.question, equals('Which driver?'));
      expect(blocked.options, equals(['Option A', 'Option B']));
      expect(blocked.command, equals('go test'));
      expect(blocked.isQuestion, isTrue);
      expect(blocked.isPermission, isTrue);
    });

    test('Blocked.fromJson handles missing optional fields gracefully', () {
      final json = {
        'kind': 'permission',
        'reason': 'Confirm rm',
      };

      final blocked = Blocked.fromJson(json);
      expect(blocked.kind, equals(BlockKind.permission));
      expect(blocked.reason, equals('Confirm rm'));
      expect(blocked.question, isNull);
      expect(blocked.options, isEmpty);
      expect(blocked.command, isNull);
      expect(blocked.isPermission, isTrue);
      expect(blocked.isQuestion, isFalse);
    });

    test('Blocked.toJson serializes all fields properly', () {
      final since = DateTime.parse('2026-08-20T15:30:00.000Z');
      final blocked = Blocked(
        kind: BlockKind.question,
        reason: 'Select database',
        since: since,
        question: 'Choose an option',
        options: const ['Opt1', 'Opt2'],
        command: 'run db:migrate',
      );

      final json = blocked.toJson();
      expect(json['kind'], equals('question'));
      expect(json['reason'], equals('Select database'));
      expect(json['since'], equals(since.toIso8601String()));
      expect(json['question'], equals('Choose an option'));
      expect(json['options'], equals(['Opt1', 'Opt2']));
      expect(json['command'], equals('run db:migrate'));
    });

    test('Blocked.copyWith modifies only specified fields', () {
      final original = Blocked(
        kind: BlockKind.permission,
        reason: 'Old Reason',
        since: DateTime.parse('2026-08-20T10:00:00.000Z'),
        command: 'rm -rf',
      );

      final modified = original.copyWith(
        reason: 'New Reason',
        kind: BlockKind.question,
        question: 'Allow delete?',
      );

      expect(modified.reason, equals('New Reason'));
      expect(modified.kind, equals(BlockKind.question));
      expect(modified.question, equals('Allow delete?'));
      expect(modified.command, equals('rm -rf'));
      expect(modified.since, equals(original.since));
    });
  });

  group('Session Model - JSON Serialization & Parsing', () {
    test('Session.fromJson parses complete JSON with string and int states', () {
      final json = {
        'id': 'claude-code:local:1001',
        'agent': 'claude-code',
        'host': 'local',
        'native_id': '1001',
        'cwd': '/Users/dev4u/Work/Ackbar',
        'roots': ['/Users/dev4u/Work/Ackbar'],
        'project_key': 'github.com/marcinbak/ackbar',
        'state': 1, // integer working
        'activity': 'Writing tests',
        'started_at': '2026-08-20T10:00:00.000Z',
        'last_event_at': '2026-08-20T10:30:00.000Z',
        'managed': true,
        'tmux_name': 'ackbar-claude-1001',
        'pid': 12345,
        'archived': false,
        'node_path': 'Ackbar/Backend',
        'name': 'Test Session',
        'entrypoint': 'main.go',
        'kind': 'cli',
        'version': '1.0.0',
        'context_pct': 42,
        'git_branch': 'feat/tests',
        'deleted': false,
        'custom_title': 'Custom Test Session',
        'ai_title': 'AI Generated Title',
        'ai_description': 'Session description',
        'first_prompt': 'First prompt instruction',
        'last_prompt': 'Last prompt response',
        'blocked': {
          'kind': 'question',
          'reason': 'Confirm choice',
          'since': '2026-08-20T10:25:00.000Z',
          'question': 'Proceed?',
          'options': ['Yes', 'No'],
        },
      };

      final session = Session.fromJson(json);
      expect(session.id, equals('claude-code:local:1001'));
      expect(session.agent, equals('claude-code'));
      expect(session.host, equals('local'));
      expect(session.nativeId, equals('1001'));
      expect(session.cwd, equals('/Users/dev4u/Work/Ackbar'));
      expect(session.roots, equals(['/Users/dev4u/Work/Ackbar']));
      expect(session.projectKey, equals('github.com/marcinbak/ackbar'));
      expect(session.state, equals(SessionState.working));
      expect(session.activity, equals('Writing tests'));
      expect(session.managed, isTrue);
      expect(session.tmuxName, equals('ackbar-claude-1001'));
      expect(session.pid, equals(12345));
      expect(session.archived, isFalse);
      expect(session.nodePath, equals('Ackbar/Backend'));
      expect(session.name, equals('Test Session'));
      expect(session.contextPct, equals(42));
      expect(session.gitBranch, equals('feat/tests'));
      expect(session.deleted, isFalse);
      expect(session.customTitle, equals('Custom Test Session'));
      expect(session.aiTitle, equals('AI Generated Title'));
      expect(session.firstPrompt, equals('First prompt instruction'));
      expect(session.lastPrompt, equals('Last prompt response'));
      expect(session.blocked, isNotNull);
      expect(session.blocked!.question, equals('Proceed?'));
      expect(session.isBlocked, isTrue);
      expect(session.isWorking, isTrue);
    });

    test('Session.fromJson handles string states, string pid/contextPct, and missing defaults', () {
      final json = {
        'id': 'antigravity:remote:2002',
        'agent': 'antigravity',
        'state': 'blocked',
        'pid': '54321',
        'context_pct': '85',
      };

      final session = Session.fromJson(json);
      expect(session.id, equals('antigravity:remote:2002'));
      expect(session.agent, equals('antigravity'));
      expect(session.host, equals('local'));
      expect(session.state, equals(SessionState.blocked));
      expect(session.pid, equals(54321));
      expect(session.contextPct, equals(85));
      expect(session.managed, isFalse);
      expect(session.blocked, isNull);
      expect(session.isBlocked, isTrue);
    });

    test('Session.toJson serializes all fields correctly', () {
      final start = DateTime.parse('2026-08-20T08:00:00.000Z');
      final last = DateTime.parse('2026-08-20T09:00:00.000Z');
      final session = Session(
        id: 'codex:devbox:3003',
        agent: 'codex',
        host: 'devbox',
        nativeId: '3003',
        cwd: '/opt/work',
        projectKey: 'infrastructure',
        state: SessionState.idle,
        startedAt: start,
        lastEventAt: last,
        managed: true,
        tmuxName: 'ackbar-codex-3003',
        pid: 9999,
        contextPct: 60,
        gitBranch: 'main',
        customTitle: 'Supervisor PTY',
      );

      final json = session.toJson();
      expect(json['id'], equals('codex:devbox:3003'));
      expect(json['agent'], equals('codex'));
      expect(json['host'], equals('devbox'));
      expect(json['state'], equals(SessionState.idle.value));
      expect(json['started_at'], equals(start.toIso8601String()));
      expect(json['last_event_at'], equals(last.toIso8601String()));
      expect(json['managed'], isTrue);
      expect(json['pid'], equals(9999));
      expect(json['context_pct'], equals(60));
      expect(json['git_branch'], equals('main'));
      expect(json['custom_title'], equals('Supervisor PTY'));
    });
  });

  group('Session Model - Title Resolution Hierarchy', () {
    final baseTime = DateTime.parse('2026-08-20T10:00:00.000Z');

    test('Priority 1: customTitle takes top precedence', () {
      final session = Session(
        id: '1',
        agent: 'claude-code',
        host: 'local',
        nativeId: '100',
        cwd: '/work',
        state: SessionState.working,
        startedAt: baseTime,
        lastEventAt: baseTime,
        customTitle: 'My Handcrafted Title',
        aiTitle: 'AI Suggested Title',
        name: 'Session Name',
        firstPrompt: 'First prompt line',
      );
      expect(session.displayTitle, equals('My Handcrafted Title'));
    });

    test('Priority 2: aiTitle takes precedence if customTitle is empty', () {
      final session = Session(
        id: '2',
        agent: 'claude-code',
        host: 'local',
        nativeId: '100',
        cwd: '/work',
        state: SessionState.working,
        startedAt: baseTime,
        lastEventAt: baseTime,
        customTitle: '',
        aiTitle: 'AI Suggested Title',
        name: 'Session Name',
        firstPrompt: 'First prompt line',
      );
      expect(session.displayTitle, equals('AI Suggested Title'));
    });

    test('Priority 3: name is used if not equal to agent and not starting with <', () {
      final session = Session(
        id: '3',
        agent: 'claude-code',
        host: 'local',
        nativeId: '100',
        cwd: '/work',
        state: SessionState.working,
        startedAt: baseTime,
        lastEventAt: baseTime,
        name: 'Feature Auth Flow',
        firstPrompt: 'First prompt line',
      );
      expect(session.displayTitle, equals('Feature Auth Flow'));
    });

    test('Priority 4: firstPrompt first line truncated to 60 chars', () {
      final longPrompt = 'This is a very long first prompt instruction that exceeds sixty characters easily and will be truncated\nSecond line';
      final session = Session(
        id: '4',
        agent: 'claude-code',
        host: 'local',
        nativeId: '100',
        cwd: '/work',
        state: SessionState.working,
        startedAt: baseTime,
        lastEventAt: baseTime,
        name: 'claude-code',
        firstPrompt: longPrompt,
      );
      expect(session.displayTitle, equals('This is a very long first prompt instruction that exceeds...'));
      expect(session.displayTitle.length, equals(60));
    });

    test('Priority 5: nativeId with agent display name', () {
      final session = Session(
        id: '5',
        agent: 'antigravity',
        host: 'local',
        nativeId: '8491',
        cwd: '/work',
        state: SessionState.working,
        startedAt: baseTime,
        lastEventAt: baseTime,
        name: '<unnamed>',
      );
      expect(session.displayTitle, equals('Antigravity (8491)'));
    });

    test('Priority 6: fallback to default agent session', () {
      final session = Session(
        id: '6',
        agent: 'codex',
        host: 'local',
        nativeId: '',
        cwd: '/work',
        state: SessionState.working,
        startedAt: baseTime,
        lastEventAt: baseTime,
      );
      expect(session.displayTitle, equals('OpenAI Codex session'));
    });
  });

  group('Session Model - Agent Metadata & Formatting', () {
    final now = DateTime.now();

    test('agentDisplayName formats standard agent names', () {
      expect(Session(id: '1', agent: 'claude-code', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('Claude Code'));
      expect(Session(id: '2', agent: 'claude', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('Claude Code'));
      expect(Session(id: '3', agent: 'antigravity', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('Antigravity'));
      expect(Session(id: '4', agent: 'gemini', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('Antigravity'));
      expect(Session(id: '5', agent: 'codex', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('OpenAI Codex'));
      expect(Session(id: '6', agent: 'openai-codex', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('OpenAI Codex'));
      expect(Session(id: '7', agent: 'custom-agent', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('custom-agent'));
      expect(Session(id: '8', agent: '', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentDisplayName, equals('Agent'));
    });

    test('agentSymbol returns corresponding emojis', () {
      expect(Session(id: '1', agent: 'claude-code', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentSymbol, equals('🤖'));
      expect(Session(id: '2', agent: 'antigravity', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentSymbol, equals('✨'));
      expect(Session(id: '3', agent: 'codex', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentSymbol, equals('⚡'));
      expect(Session(id: '4', agent: 'other', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentSymbol, equals('💻'));
    });

    test('agentColor returns semantic accent colors', () {
      expect(Session(id: '1', agent: 'claude-code', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentColor, equals(const Color(0xFFD97706)));
      expect(Session(id: '2', agent: 'antigravity', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentColor, equals(AppColors.infoCyan));
      expect(Session(id: '3', agent: 'codex', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentColor, equals(AppColors.statusEmerald));
      expect(Session(id: '4', agent: 'unknown', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).agentColor, equals(AppColors.textSecondary));
    });

    test('hostTag prepends @ if missing', () {
      expect(Session(id: '1', agent: 'a', host: 'local', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).hostTag, equals('@local'));
      expect(Session(id: '2', agent: 'a', host: '@devbox', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now).hostTag, equals('@devbox'));
    });

    test('projectDisplayName resolution hierarchy', () {
      final sessWithKey = Session(id: '1', agent: 'a', host: 'l', nativeId: '1', cwd: '/work/other', projectKey: 'github.com/org/my-project.git', state: SessionState.idle, startedAt: now, lastEventAt: now);
      expect(sessWithKey.projectDisplayName, equals('my-project'));

      final sessWithNode = Session(id: '2', agent: 'a', host: 'l', nativeId: '1', cwd: '/work/dir', nodePath: 'Mobile Client/UI', state: SessionState.idle, startedAt: now, lastEventAt: now);
      expect(sessWithNode.projectDisplayName, equals('Mobile Client'));

      final sessWithCwd = Session(id: '3', agent: 'a', host: 'l', nativeId: '1', cwd: '/home/dev/AckbarCore', state: SessionState.idle, startedAt: now, lastEventAt: now);
      expect(sessWithCwd.projectDisplayName, equals('AckbarCore'));

      final sessFallback = Session(id: '4', agent: 'a', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now, lastEventAt: now);
      expect(sessFallback.projectDisplayName, equals('Default Project'));
    });

    test('timeElapsedFormatted formats seconds, minutes, hours, and days', () {
      final sAgo = Session(id: '1', agent: 'a', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now.subtract(const Duration(seconds: 25)), lastEventAt: now.subtract(const Duration(seconds: 25)));
      expect(sAgo.timeElapsedFormatted, contains('s ago'));

      final mAgo = Session(id: '2', agent: 'a', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now.subtract(const Duration(minutes: 15)), lastEventAt: now.subtract(const Duration(minutes: 15)));
      expect(mAgo.timeElapsedFormatted, contains('15m ago'));

      final hAgo = Session(id: '3', agent: 'a', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now.subtract(const Duration(hours: 3, minutes: 12)), lastEventAt: now.subtract(const Duration(hours: 3, minutes: 12)));
      expect(hAgo.timeElapsedFormatted, contains('3h 12m'));

      final dAgo = Session(id: '4', agent: 'a', host: 'l', nativeId: '1', cwd: '', state: SessionState.idle, startedAt: now.subtract(const Duration(days: 4)), lastEventAt: now.subtract(const Duration(days: 4)));
      expect(dAgo.timeElapsedFormatted, contains('4d ago'));
    });

    test('Session.copyWith updates specified fields correctly', () {
      final original = Session(
        id: '1',
        agent: 'claude-code',
        host: 'local',
        nativeId: '100',
        cwd: '/work',
        state: SessionState.idle,
        startedAt: now,
        lastEventAt: now,
        contextPct: 50,
      );

      final updated = original.copyWith(
        state: SessionState.working,
        activity: 'Running tests',
        contextPct: 75,
      );

      expect(updated.id, equals(original.id));
      expect(updated.state, equals(SessionState.working));
      expect(updated.activity, equals('Running tests'));
      expect(updated.contextPct, equals(75));
      expect(updated.agent, equals('claude-code'));
    });
  });

  group('HostRecord Model Tests', () {
    final now = DateTime.parse('2026-08-20T10:00:00.000Z');

    test('HostRecord.fromJson parses full json', () {
      final json = {
        'name': 'devbox',
        'url': 'http://100.95.42.204:7777',
        'ssh_target': 'dev@devbox.internal',
        'remote_cwd': '~/Development/ackbard',
        'online': true,
        'latency_ms': 24,
        'version': 'v0.2.1',
        'uptime': '18d 02h',
        'sessions_count': 5,
        'tailscale_ip': '100.95.42.204',
        'created_at': '2026-08-20T10:00:00.000Z',
      };

      final host = HostRecord.fromJson(json);
      expect(host.name, equals('devbox'));
      expect(host.url, equals('http://100.95.42.204:7777'));
      expect(host.sshTarget, equals('dev@devbox.internal'));
      expect(host.remoteCwd, equals('~/Development/ackbard'));
      expect(host.online, isTrue);
      expect(host.latencyMs, equals(24));
      expect(host.version, equals('v0.2.1'));
      expect(host.uptime, equals('18d 02h'));
      expect(host.sessionsCount, equals(5));
      expect(host.tailscaleIp, equals('100.95.42.204'));
      expect(host.createdAt, equals(now));
    });

    test('HostRecord.fromJson handles projects_dir fallback and string numbers', () {
      final json = {
        'name': 'cloud',
        'url': 'http://127.0.0.1:7777',
        'projects_dir': '/custom/projects',
        'latency_ms': '45',
        'sessions_count': '3',
      };

      final host = HostRecord.fromJson(json);
      expect(host.name, equals('cloud'));
      expect(host.remoteCwd, equals('/custom/projects'));
      expect(host.latencyMs, equals(45));
      expect(host.sessionsCount, equals(3));
      expect(host.isLocal, isTrue);
    });

    test('HostRecord.isLocal detects local name and localhost url', () {
      final localByName = HostRecord(name: 'Local', url: 'http://custom:7777', createdAt: now);
      expect(localByName.isLocal, isTrue);

      final localByUrl = HostRecord(name: 'my-mac', url: 'http://127.0.0.1:7777', createdAt: now);
      expect(localByUrl.isLocal, isTrue);

      final remoteHost = HostRecord(name: 'remote-gpu', url: 'http://10.0.0.5:7777', createdAt: now);
      expect(remoteHost.isLocal, isFalse);
    });

    test('HostRecord.endpointDisplay resolves Tailscale, SSH, and URL', () {
      final withTailscale = HostRecord(name: 'h1', url: 'http://10.0.0.1:7777', tailscaleIp: '100.64.1.2', sshTarget: 'user@box', createdAt: now);
      expect(withTailscale.endpointDisplay, equals('100.64.1.2'));

      final withSsh = HostRecord(name: 'h2', url: 'http://10.0.0.1:7777', sshTarget: 'user@box', createdAt: now);
      expect(withSsh.endpointDisplay, equals('ssh://user@box'));

      final withUrlOnly = HostRecord(name: 'h3', url: 'http://10.0.0.1:7777', createdAt: now);
      expect(withUrlOnly.endpointDisplay, equals('10.0.0.1:7777'));
    });

    test('HostRecord status helpers and indicator conversion', () {
      final onlineHost = HostRecord(name: 'devbox', url: 'http://127.0.0.1:7777', online: true, latencyMs: 12, createdAt: now);
      expect(onlineHost.latencyDisplay, equals('12ms'));
      expect(onlineHost.statusColor, equals(AppColors.statusEmerald));
      expect(onlineHost.badgeStatus, equals(AckbarSessionStatus.active));

      final indicator = onlineHost.toIndicator();
      expect(indicator.name, equals('devbox'));
      expect(indicator.isOnline, isTrue);
      expect(indicator.latency, equals('12ms'));

      final offlineHost = HostRecord(name: 'cloud-gpu', url: 'http://10.0.0.5:7777', online: false, createdAt: now);
      expect(offlineHost.statusColor, equals(AppColors.statusCoral));
      expect(offlineHost.badgeStatus, equals(AckbarSessionStatus.offline));
    });

    test('HostRecord.toJson and copyWith', () {
      final host = HostRecord(
        name: 'test-node',
        url: 'http://localhost:7777',
        sshTarget: 'test@host',
        remoteCwd: '/work',
        online: true,
        latencyMs: 5,
        version: 'v0.2.1',
        uptime: '1d',
        sessionsCount: 2,
        tailscaleIp: '100.1.2.3',
        createdAt: now,
      );

      final json = host.toJson();
      expect(json['name'], equals('test-node'));
      expect(json['online'], isTrue);
      expect(json['tailscale_ip'], equals('100.1.2.3'));

      final updated = host.copyWith(latencyMs: 15, online: false);
      expect(updated.name, equals('test-node'));
      expect(updated.latencyMs, equals(15));
      expect(updated.online, isFalse);
    });
  });

  group('PlanDocument Model Tests', () {
    final now = DateTime.parse('2026-08-20T10:00:00.000Z');

    test('PlanStatus enum labels, colors, and badge status mapping', () {
      expect(PlanStatus.pendingReview.label, equals('PENDING REVIEW'));
      expect(PlanStatus.pendingReview.color, equals(AppColors.statusAmber));
      expect(PlanStatus.pendingReview.toBadgeStatus(), equals(AckbarSessionStatus.blocked));

      expect(PlanStatus.inProgress.label, equals('IN PROGRESS'));
      expect(PlanStatus.inProgress.color, equals(AppColors.infoCyan));
      expect(PlanStatus.inProgress.toBadgeStatus(), equals(AckbarSessionStatus.working));

      expect(PlanStatus.approved.label, equals('APPROVED'));
      expect(PlanStatus.approved.color, equals(AppColors.statusEmerald));
      expect(PlanStatus.approved.toBadgeStatus(), equals(AckbarSessionStatus.idle));

      expect(PlanStatus.completed.label, equals('COMPLETED'));
      expect(PlanStatus.completed.color, equals(AppColors.statusEmerald));
      expect(PlanStatus.completed.toBadgeStatus(), equals(AckbarSessionStatus.idle));

      expect(PlanStatus.rejected.label, equals('REJECTED'));
      expect(PlanStatus.rejected.color, equals(AppColors.statusCoral));
      expect(PlanStatus.rejected.toBadgeStatus(), equals(AckbarSessionStatus.offline));
    });

    test('PlanDocument.fromJson parses all fields and status keywords', () {
      final json = {
        'id': 'PLAN-2026-08-A',
        'title': 'Flutter High-Fidelity Views',
        'agent': 'claude-code',
        'host': 'local',
        'project': 'Mobile Engineering',
        'goal': 'Implement mobile screens',
        'user_review_callout': '⚠️ Review needed',
        'files_changed': 14,
        'added_lines': 842,
        'deleted_lines': 68,
        'verification_steps': ['Step 1', 'Step 2'],
        'status': 'pending_review',
        'progress_pct': 0.25,
        'current_step': 'Scaffolding',
        'diff_content': 'diff --git a/test.dart',
        'test_summary': 'All passing',
        'created_at': '2026-08-20T10:00:00.000Z',
      };

      final plan = PlanDocument.fromJson(json);
      expect(plan.id, equals('PLAN-2026-08-A'));
      expect(plan.title, equals('Flutter High-Fidelity Views'));
      expect(plan.agent, equals('claude-code'));
      expect(plan.agentDisplayName, equals('Claude Code'));
      expect(plan.host, equals('local'));
      expect(plan.project, equals('Mobile Engineering'));
      expect(plan.goal, equals('Implement mobile screens'));
      expect(plan.userReviewCallout, equals('⚠️ Review needed'));
      expect(plan.filesChanged, equals(14));
      expect(plan.addedLines, equals(842));
      expect(plan.deletedLines, equals(68));
      expect(plan.linesDeltaFormatted, equals('+842 / -68'));
      expect(plan.verificationSteps, equals(['Step 1', 'Step 2']));
      expect(plan.status, equals(PlanStatus.pendingReview));
      expect(plan.isPendingReview, isTrue);
      expect(plan.progressPct, equals(0.25));
      expect(plan.currentStep, equals('Scaffolding'));
      expect(plan.diffContent, equals('diff --git a/test.dart'));
      expect(plan.testSummary, equals('All passing'));
      expect(plan.createdAt, equals(now));
    });

    test('PlanDocument.fromJson maps various status strings', () {
      expect(PlanDocument.fromJson({'status': 'working', 'created_at': now.toIso8601String()}).status, equals(PlanStatus.inProgress));
      expect(PlanDocument.fromJson({'status': 'approved', 'created_at': now.toIso8601String()}).status, equals(PlanStatus.approved));
      expect(PlanDocument.fromJson({'status': 'completed', 'created_at': now.toIso8601String()}).status, equals(PlanStatus.completed));
      expect(PlanDocument.fromJson({'status': 'done', 'created_at': now.toIso8601String()}).status, equals(PlanStatus.completed));
      expect(PlanDocument.fromJson({'status': 'rejected', 'created_at': now.toIso8601String()}).status, equals(PlanStatus.rejected));
      expect(PlanDocument.fromJson({'status': 'unknown_str', 'created_at': now.toIso8601String()}).status, equals(PlanStatus.pendingReview));
    });

    test('PlanDocument agentDisplayName formatting for different agents', () {
      expect(PlanDocument(id: '1', title: 'T', agent: 'antigravity', host: 'l', project: 'P', goal: 'G', createdAt: now).agentDisplayName, equals('Antigravity'));
      expect(PlanDocument(id: '2', title: 'T', agent: 'codex', host: 'l', project: 'P', goal: 'G', createdAt: now).agentDisplayName, equals('OpenAI Codex'));
      expect(PlanDocument(id: '3', title: 'T', agent: 'custom', host: 'l', project: 'P', goal: 'G', createdAt: now).agentDisplayName, equals('custom'));
      expect(PlanDocument(id: '4', title: 'T', agent: '', host: 'l', project: 'P', goal: 'G', createdAt: now).agentDisplayName, equals('Agent'));
    });

    test('PlanDocument.toJson and copyWith', () {
      final plan = PlanDocument(
        id: 'PLAN-001',
        title: 'Initial Plan',
        agent: 'claude',
        host: 'local',
        project: 'Ackbar',
        goal: 'Testing',
        filesChanged: 2,
        addedLines: 50,
        deletedLines: 10,
        status: PlanStatus.pendingReview,
        progressPct: 0.1,
        createdAt: now,
      );

      final json = plan.toJson();
      expect(json['id'], equals('PLAN-001'));
      expect(json['status'], equals('pendingReview'));
      expect(json['files_changed'], equals(2));

      final updated = plan.copyWith(
        status: PlanStatus.approved,
        progressPct: 0.5,
        currentStep: 'Step 1 complete',
      );

      expect(updated.status, equals(PlanStatus.approved));
      expect(updated.isApproved, isTrue);
      expect(updated.progressPct, equals(0.5));
      expect(updated.currentStep, equals('Step 1 complete'));
      expect(updated.id, equals('PLAN-001'));
    });
  });
}
