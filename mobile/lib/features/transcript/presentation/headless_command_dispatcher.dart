import 'package:flutter/foundation.dart';
import '../../../../core/models/headless_models.dart';
import '../../../../core/models/host.dart';
import '../../../../core/models/session.dart';
import '../../../../core/network/api_client.dart';
import '../../../../core/network/headless_api_client.dart';

class CommandDispatchResult {
  final bool intercepted;
  final HeadlessEphemeralCardEntry? card;
  final Session? rotatedSession;
  final String? notice;

  const CommandDispatchResult({
    required this.intercepted,
    this.card,
    this.rotatedSession,
    this.notice,
  });

  factory CommandDispatchResult.notIntercepted() =>
      const CommandDispatchResult(intercepted: false);

  factory CommandDispatchResult.handled({HeadlessEphemeralCardEntry? card}) =>
      CommandDispatchResult(intercepted: true, card: card);

  factory CommandDispatchResult.rotated(Session session, {String? notice}) =>
      CommandDispatchResult(
        intercepted: true,
        rotatedSession: session,
        notice: notice,
      );
}

class HeadlessCommandDispatcher {
  final ApiClient apiClient;
  final HostRecord host;
  final VoidCallback? onOpenConfig;
  final VoidCallback? onTakeWheel;
  final void Function(Session newSession, {String? notice})? onRotateSession;
  final void Function(Session updated)? onSessionUpdated;

  const HeadlessCommandDispatcher({
    required this.apiClient,
    required this.host,
    this.onOpenConfig,
    this.onTakeWheel,
    this.onRotateSession,
    this.onSessionUpdated,
  });

  String _cardId() =>
      'card_${DateTime.now().millisecondsSinceEpoch}_${(DateTime.now().microsecond % 1000)}';

  HeadlessEphemeralCardEntry _statusCard(
    String title,
    String body, {
    bool isError = false,
  }) {
    return HeadlessEphemeralCardEntry(
      id: _cardId(),
      kind: 'status',
      createdAt: DateTime.now(),
      statusTitle: title,
      statusBody: body,
      isError: isError,
    );
  }

  Future<CommandDispatchResult> dispatch(Session session, String raw) async {
    final trimmed = raw.trim();
    if (!session.isHeadless || trimmed.isEmpty) {
      return CommandDispatchResult.notIntercepted();
    }

    if (trimmed.startsWith('!')) {
      final cmd = trimmed.substring(1).trim();
      if (cmd.isEmpty) {
        return CommandDispatchResult.handled(
          card:
              _statusCard('Shell Command', 'Usage: ! <command>', isError: true),
        );
      }
      try {
        final res = await apiClient.runSessionShell(
          host.url,
          session.id,
          cmd,
          authToken: host.authToken,
        );
        return CommandDispatchResult.handled(
          card: HeadlessEphemeralCardEntry(
            id: _cardId(),
            kind: 'shell',
            createdAt: DateTime.now(),
            shell: res,
          ),
        );
      } catch (e) {
        final msg = e is ApiHttpException ? e.message : e.toString();
        return CommandDispatchResult.handled(
          card: _statusCard('Shell Error', msg, isError: true),
        );
      }
    }

    if (!trimmed.startsWith('/')) {
      return CommandDispatchResult.notIntercepted();
    }

    final spaceIdx = trimmed.indexOf(' ');
    final cmdName =
        (spaceIdx == -1 ? trimmed.substring(1) : trimmed.substring(1, spaceIdx))
            .toLowerCase();
    final argText =
        spaceIdx == -1 ? '' : trimmed.substring(spaceIdx + 1).trim();

    try {
      switch (cmdName) {
        case 'config':
          onOpenConfig?.call();
          return CommandDispatchResult.handled();

        case 'terminal':
          onTakeWheel?.call();
          return CommandDispatchResult.handled();

        case 'plan':
          final updated = await apiClient.updateSessionConfig(
            host.url,
            session.id,
            permissionMode: 'plan',
            authToken: host.authToken,
          );
          onSessionUpdated?.call(updated);
          return CommandDispatchResult.handled(
            card: _statusCard(
                'Configuration', 'Execution mode set to PLAN (Read Only)'),
          );

        case 'auto':
          final updated = await apiClient.updateSessionConfig(
            host.url,
            session.id,
            permissionMode: 'bypassPermissions',
            authToken: host.authToken,
          );
          onSessionUpdated?.call(updated);
          return CommandDispatchResult.handled(
            card: _statusCard('Configuration',
                'Execution mode set to AUTO (Bypass Permissions)'),
          );

        case 'permissions':
          if (argText.isEmpty) {
            final cur = session.permissionMode.isNotEmpty
                ? session.permissionMode
                : 'default';
            return CommandDispatchResult.handled(
              card: _statusCard('Permissions', 'Current permission mode: $cur'),
            );
          }
          final updated = await apiClient.updateSessionConfig(
            host.url,
            session.id,
            permissionMode: argText,
            authToken: host.authToken,
          );
          onSessionUpdated?.call(updated);
          return CommandDispatchResult.handled(
            card: _statusCard('Permissions', 'Permission mode set to $argText'),
          );

        case 'model':
          if (argText.isEmpty) {
            onOpenConfig?.call();
            return CommandDispatchResult.handled();
          }
          final updated = await apiClient.updateSessionConfig(
            host.url,
            session.id,
            model: argText,
            authToken: host.authToken,
          );
          onSessionUpdated?.call(updated);
          return CommandDispatchResult.handled(
            card: _statusCard('Configuration', 'Model set to $argText'),
          );

        case 'effort':
          if (session.agent.toLowerCase() == 'antigravity') {
            return CommandDispatchResult.handled(
              card: _statusCard(
                'Effort Configuration',
                'Effort tiers are encoded in Antigravity model IDs — use /model or /config.',
              ),
            );
          }
          if (argText.isEmpty) {
            final cur = session.effort.isNotEmpty ? session.effort : 'default';
            return CommandDispatchResult.handled(
              card: _statusCard('Configuration', 'Current effort tier: $cur'),
            );
          }
          final updated = await apiClient.updateSessionConfig(
            host.url,
            session.id,
            effort: argText,
            authToken: host.authToken,
          );
          onSessionUpdated?.call(updated);
          return CommandDispatchResult.handled(
            card: _statusCard('Configuration', 'Effort tier set to $argText'),
          );

        case 'add-dir':
          if (argText.isEmpty) {
            final cur = session.extraDirs.isEmpty
                ? 'none'
                : session.extraDirs.join(', ');
            return CommandDispatchResult.handled(
              card: _statusCard('Directories', 'Extra directories: $cur'),
            );
          }
          Session updated;
          if (argText == '--clear') {
            updated = await apiClient.updateSessionConfig(
              host.url,
              session.id,
              extraDirs: const [],
              authToken: host.authToken,
            );
          } else if (argText.startsWith('--remove ')) {
            final removeTarget = argText.substring(9).trim();
            updated = await apiClient.updateSessionConfig(
              host.url,
              session.id,
              removeDirs: [removeTarget],
              authToken: host.authToken,
            );
          } else {
            updated = await apiClient.updateSessionConfig(
              host.url,
              session.id,
              addDirs: [argText],
              authToken: host.authToken,
            );
          }
          onSessionUpdated?.call(updated);
          return CommandDispatchResult.handled(
            card: _statusCard('Directories',
                'Directories updated: ${updated.extraDirs.join(", ")}'),
          );

        case 'mcp':
          final res = await apiClient.getMcpStatus(
            host.url,
            session.id,
            refresh: true,
            authToken: host.authToken,
          );
          return CommandDispatchResult.handled(
            card: HeadlessEphemeralCardEntry(
              id: _cardId(),
              kind: 'mcp',
              createdAt: DateTime.now(),
              mcp: res,
            ),
          );

        case 'plugins':
        case 'agents':
        case 'doctor':
        case 'context':
        case 'cost':
          final res = await apiClient.inspectSession(
            host.url,
            session.id,
            cmdName,
            authToken: host.authToken,
          );
          return CommandDispatchResult.handled(
            card: HeadlessEphemeralCardEntry(
              id: _cardId(),
              kind: 'inspect',
              createdAt: DateTime.now(),
              inspect: res,
            ),
          );

        case 'diff':
          final res = await apiClient.getSessionDiff(
            host.url,
            session.id,
            authToken: host.authToken,
          );
          return CommandDispatchResult.handled(
            card: HeadlessEphemeralCardEntry(
              id: _cardId(),
              kind: 'diff',
              createdAt: DateTime.now(),
              diff: res,
            ),
          );

        case 'rewind':
          if (argText.isEmpty) {
            final res = await apiClient.getRewindTurns(
              host.url,
              session.id,
              authToken: host.authToken,
            );
            return CommandDispatchResult.handled(
              card: HeadlessEphemeralCardEntry(
                id: _cardId(),
                kind: 'rewind',
                createdAt: DateTime.now(),
                rewind: res,
              ),
            );
          }
          final turn = int.tryParse(argText);
          if (turn == null) {
            return CommandDispatchResult.handled(
              card: _statusCard('Rewind Error', 'Usage: /rewind [turn_number]',
                  isError: true),
            );
          }
          final res = await apiClient.rewindSession(
            host.url,
            session.id,
            turn,
            authToken: host.authToken,
          );
          if (res.session != null) {
            onRotateSession?.call(res.session!,
                notice: 'Rewound to Turn $turn');
            return CommandDispatchResult.rotated(res.session!,
                notice: 'Rewound to Turn $turn');
          }
          return CommandDispatchResult.handled(
            card: _statusCard('Rewind', 'Rewind completed'),
          );

        case 'clear':
        case 'reset':
          final res = await apiClient.clearSession(
            host.url,
            session.id,
            authToken: host.authToken,
          );
          if (res.session != null) {
            onRotateSession?.call(res.session!,
                notice: 'Context cleared (reset to Turn 1)');
            return CommandDispatchResult.rotated(res.session!,
                notice: 'Context cleared (reset to Turn 1)');
          }
          return CommandDispatchResult.handled(
            card: _statusCard('Session Reset', 'Session context cleared'),
          );

        case 'compact':
          final res = await apiClient.compactSession(
            host.url,
            session.id,
            instructions: argText,
            authToken: host.authToken,
          );
          if (res.mode == 'rotated_session' && res.session != null) {
            onRotateSession?.call(res.session!,
                notice: 'Compacted conversation into new session');
            return CommandDispatchResult.rotated(res.session!,
                notice: 'Compacted conversation into new session');
          }
          return CommandDispatchResult.handled(
            card: _statusCard(
                'Compaction', 'Compaction turn dispatched via native stream'),
          );

        case 'handover':
          final res = await apiClient.handoverSessionDetailed(
            host.url,
            session.id,
            strategy: 'in_place',
            customInstruction: argText,
            authToken: host.authToken,
          );
          if (res.session != null) {
            onRotateSession?.call(res.session!,
                notice: 'Handover complete: context rotated in-place');
            return CommandDispatchResult.rotated(res.session!,
                notice: 'Handover complete: context rotated in-place');
          }
          return CommandDispatchResult.handled(
            card: _statusCard('Handover', 'Handover turn dispatched'),
          );

        default:
          return CommandDispatchResult.notIntercepted();
      }
    } catch (e) {
      final msg = e is ApiHttpException ? e.message : e.toString();
      return CommandDispatchResult.handled(
        card: _statusCard('Command Error', msg, isError: true),
      );
    }
  }
}
