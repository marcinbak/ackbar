import 'package:flutter/material.dart';
import '../../../../core/models/headless_models.dart';
import '../../../../core/models/session.dart';
import '../../../../core/theme/app_colors.dart';
import '../../../../core/theme/app_typography.dart';
import '../../headless/presentation/headless_mcp_section.dart';

class HeadlessCommandCardView extends StatelessWidget {
  final HeadlessEphemeralCardEntry entry;
  final Session? session;
  final Future<({String url, String? authToken})> Function()? resolveHost;
  final VoidCallback onDismiss;
  final void Function(String text)? onAttachToPrompt;
  final void Function(int turn)? onRewindToTurn;

  const HeadlessCommandCardView({
    super.key,
    required this.entry,
    this.session,
    this.resolveHost,
    required this.onDismiss,
    this.onAttachToPrompt,
    this.onRewindToTurn,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      key: ValueKey('headless_cmd_card_${entry.kind}_${entry.id}'),
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color:
              entry.isError ? AppColors.statusCoral : AppColors.outlineSubtle,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                _titleFor(entry),
                style: AppTypography.codeXs.copyWith(
                  color: entry.isError
                      ? AppColors.statusCoral
                      : AppColors.infoCyan,
                  fontWeight: FontWeight.w600,
                ),
              ),
              InkWell(
                onTap: onDismiss,
                borderRadius: BorderRadius.circular(12),
                child: const Padding(
                  padding: EdgeInsets.all(2),
                  child: Icon(Icons.close_rounded,
                      size: 16, color: AppColors.textMuted),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          _buildContent(context),
        ],
      ),
    );
  }

  String _titleFor(HeadlessEphemeralCardEntry e) {
    switch (e.kind) {
      case 'shell':
        return 'SHELL EXECUTION';
      case 'diff':
        return 'GIT DIFF & STATUS';
      case 'rewind':
        return 'REWIND TURNS';
      case 'inspect':
        return 'INSPECT (${e.inspect?.kind.toUpperCase() ?? ""})';
      case 'mcp':
        return 'MCP STATUS';
      case 'status':
      default:
        return e.statusTitle ?? 'STATUS';
    }
  }

  Widget _buildContent(BuildContext context) {
    if (entry.shell != null) return _buildShell(context, entry.shell!);
    if (entry.diff != null) return _buildDiff(context, entry.diff!);
    if (entry.rewind != null) return _buildRewind(context, entry.rewind!);
    if (entry.inspect != null) return _buildInspect(context, entry.inspect!);
    if (entry.mcp != null) return _buildMcp(context, entry.mcp!);
    return Text(
      entry.statusBody ?? '',
      style: AppTypography.codeXs.copyWith(
        color: entry.isError ? AppColors.statusCoral : AppColors.textPrimary,
      ),
    );
  }

  Widget _buildShell(BuildContext context, ShellExecResult s) {
    final ok = s.exitCode == 0;
    final hasStdout = s.stdout.trim().isNotEmpty;
    final hasStderr = s.stderr.trim().isNotEmpty;
    final out = (hasStdout && hasStderr)
        ? '${s.stdout.trimRight()}\n[stderr]\n${s.stderr.trimRight()}'
        : (hasStdout ? s.stdout : s.stderr);

    final tags = <String>[
      'exit ${s.exitCode}',
      '${s.durationMs}ms',
      if (s.timedOut) 'TIMED OUT',
      if (s.truncated) 'TRUNCATED',
    ];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: Text(
                '\$ ${s.command}',
                style: AppTypography.codeSm.copyWith(
                  color: AppColors.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            const SizedBox(width: 8),
            Wrap(
              spacing: 4,
              children: tags
                  .map(
                    (t) => Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 2,
                      ),
                      decoration: BoxDecoration(
                        color: ok
                            ? AppColors.statusEmerald.withValues(alpha: 0.15)
                            : AppColors.statusCoral.withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        t,
                        style: AppTypography.codeXs.copyWith(
                          color: ok
                              ? AppColors.statusEmerald
                              : AppColors.statusCoral,
                        ),
                      ),
                    ),
                  )
                  .toList(),
            ),
          ],
        ),
        if (out.isNotEmpty) ...[
          const SizedBox(height: 6),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: AppColors.background,
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: AppColors.outlineSubtle),
            ),
            constraints: const BoxConstraints(maxHeight: 180),
            child: SingleChildScrollView(
              child: Text(
                out,
                style: AppTypography.codeXs
                    .copyWith(color: AppColors.textSecondary),
              ),
            ),
          ),
        ],
        const SizedBox(height: 6),
        Align(
          alignment: Alignment.centerRight,
          child: TextButton.icon(
            key: ValueKey('headless_attach_to_prompt_${entry.id}'),
            onPressed: () {
              final fenced = '```shell\n\$ ${s.command}\n$out\n```\n';
              onAttachToPrompt?.call(fenced);
            },
            icon: const Icon(Icons.add_comment_rounded, size: 14),
            label: const Text('+ Attach to Prompt',
                style: TextStyle(fontSize: 11)),
            style: TextButton.styleFrom(
              foregroundColor: AppColors.infoCyan,
              visualDensity: VisualDensity.compact,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildDiff(BuildContext context, DiffInspectResult d) {
    final stat = d.stat.trim();
    final diff = d.diff.trim();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (d.branch.isNotEmpty)
          Text('Branch: ${d.branch}',
              style: AppTypography.codeXs.copyWith(
                  color: AppColors.infoCyan, fontWeight: FontWeight.w600)),
        if (d.statusLines.isNotEmpty) ...[
          const SizedBox(height: 4),
          ...d.statusLines.take(6).map((l) => Text(l,
              style:
                  AppTypography.codeXs.copyWith(color: AppColors.statusAmber))),
          if (d.statusLines.length > 6)
            Text('...and ${d.statusLines.length - 6} more files',
                style:
                    AppTypography.codeXs.copyWith(color: AppColors.textMuted)),
        ],
        if (stat.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(stat,
              style: AppTypography.codeXs
                  .copyWith(color: AppColors.textSecondary)),
        ],
        if (diff.isNotEmpty) ...[
          const SizedBox(height: 6),
          Container(
            width: double.infinity,
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: AppColors.background,
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: AppColors.outlineSubtle),
            ),
            constraints: const BoxConstraints(maxHeight: 180),
            child: SingleChildScrollView(
              child: Text(
                diff,
                style: AppTypography.codeXs
                    .copyWith(color: AppColors.textSecondary),
              ),
            ),
          ),
        ],
        const SizedBox(height: 6),
        Align(
          alignment: Alignment.centerRight,
          child: TextButton.icon(
            key: ValueKey('headless_attach_to_prompt_${entry.id}'),
            onPressed: () {
              final content = diff.isNotEmpty ? diff : stat;
              final fenced = '```diff\n$content\n```\n';
              onAttachToPrompt?.call(fenced);
            },
            icon: const Icon(Icons.add_comment_rounded, size: 14),
            label: const Text('+ Attach to Prompt',
                style: TextStyle(fontSize: 11)),
            style: TextButton.styleFrom(
              foregroundColor: AppColors.infoCyan,
              visualDensity: VisualDensity.compact,
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildRewind(BuildContext context, RewindPreviewResult r) {
    if (r.turns.isEmpty) {
      return Text('No turns available for rewind',
          style: AppTypography.codeXs.copyWith(color: AppColors.textMuted));
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('Select turn to rewind before:',
            style:
                AppTypography.codeXs.copyWith(color: AppColors.textSecondary)),
        const SizedBox(height: 6),
        ...r.turns.map((t) {
          return Container(
            margin: const EdgeInsets.symmetric(vertical: 2),
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            decoration: BoxDecoration(
              color: AppColors.background,
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: AppColors.outlineSubtle),
            ),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    'Turn ${t.turn}: ${t.preview}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textPrimary),
                  ),
                ),
                const SizedBox(width: 8),
                OutlinedButton(
                  key: ValueKey('headless_rewind_turn_${t.turn}'),
                  onPressed: () => onRewindToTurn?.call(t.turn),
                  style: OutlinedButton.styleFrom(
                    side: const BorderSide(color: AppColors.statusAmber),
                    foregroundColor: AppColors.statusAmber,
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    visualDensity: VisualDensity.compact,
                  ),
                  child: Text('Turn ${t.turn}',
                      style: const TextStyle(fontSize: 10)),
                ),
              ],
            ),
          );
        }),
      ],
    );
  }

  Widget _buildInspect(BuildContext context, HeadlessInspectData d) {
    final rep = _formatInspect(d);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: AppColors.background,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: AppColors.outlineSubtle),
      ),
      constraints: const BoxConstraints(maxHeight: 220),
      child: SingleChildScrollView(
        child: Text(
          rep,
          style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
        ),
      ),
    );
  }

  String _formatInspect(HeadlessInspectData d) {
    if (d.report.isNotEmpty) return d.report;
    final k = d.kind.toLowerCase();
    if (k == 'context' || k == 'cost') {
      final b = StringBuffer();
      final model = d.raw['model']?.toString() ?? '';
      if (model.isNotEmpty) b.writeln('Model: $model');
      b.writeln('Turns: ${d.raw['turn_count'] ?? 0}');
      final used = d.raw['context_used_tokens'] ?? 0;
      final limit = d.raw['context_limit_tokens'] ?? 0;
      final pct = d.raw['context_pct'] ?? 0;
      b.writeln('Context: $used / $limit ($pct%)');
      final inT = d.raw['input_tokens'] ?? 0;
      final outT = d.raw['output_tokens'] ?? 0;
      final crT = d.raw['cache_read_tokens'] ?? 0;
      final thT = d.raw['thinking_tokens'] ?? 0;
      b.writeln('Tokens: in: $inT · out: $outT · cache_r: $crT · think: $thT');
      final est = d.raw['estimated_usd'];
      if (est != null && est is num) {
        final note = d.raw['billing_note']?.toString() ?? '';
        final noteStr = note.isNotEmpty ? ' ($note)' : '';
        b.writeln('Est. API: \$${est.toStringAsFixed(4)}$noteStr');
      }
      return b.toString().trimRight();
    }
    if (k == 'agents') {
      final b = StringBuffer();
      final builtins = d.raw['builtins'];
      if (builtins is List && builtins.isNotEmpty) {
        b.writeln('Builtin Agents:');
        for (final item in builtins) {
          final n = item is Map ? item['name'] ?? '' : item.toString();
          b.writeln('  • $n');
        }
      }
      final locals = d.raw['local_roles'];
      if (locals is List && locals.isNotEmpty) {
        if (b.isNotEmpty) b.writeln();
        b.writeln('Local Roles:');
        for (final item in locals) {
          if (item is Map) {
            b.writeln('  • ${item['name'] ?? ''} (${item['source'] ?? ''})');
          } else {
            b.writeln('  • $item');
          }
        }
      }
      if (b.isNotEmpty) return b.toString().trimRight();
    }
    if (k == 'plugins') {
      final b = StringBuffer();
      void addList(String title, dynamic list) {
        if (list is List && list.isNotEmpty) {
          if (b.isNotEmpty) b.writeln();
          b.writeln('$title:');
          for (final item in list) {
            if (item is Map) {
              final n = item['name'] ?? '';
              final st = item['installed'] == true
                  ? 'installed'
                  : (item['status'] ?? 'available');
              b.writeln('  • $n ($st)');
            } else {
              b.writeln('  • $item');
            }
          }
        }
      }

      addList('Workspace Plugins', d.raw['workspace_plugins']);
      addList('Installed Plugins', d.raw['installed_plugins']);
      if (b.isNotEmpty) return b.toString().trimRight();
    }
    return d.raw.toString();
  }

  Widget _buildMcp(BuildContext context, SessionMcpStatus m) {
    if (session != null) {
      return HeadlessMcpSection(
        session: session!,
        resolveHost: resolveHost,
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (m.servers.isNotEmpty)
          ...m.servers.map((s) => Text('• ${s.name} (${s.status})',
              style: AppTypography.codeXs.copyWith(
                color: s.isConfigured
                    ? AppColors.statusEmerald
                    : AppColors.statusAmber,
              ))),
        if (m.plugins.isNotEmpty)
          ...m.plugins.map((p) => Text(
              '• ${p.name} (${p.authenticated ? "authenticated" : "needs auth"})',
              style: AppTypography.codeXs.copyWith(
                color: p.authenticated
                    ? AppColors.statusEmerald
                    : AppColors.statusAmber,
              ))),
      ],
    );
  }
}
