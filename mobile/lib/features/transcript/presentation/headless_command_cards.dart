import 'package:flutter/material.dart';
import '../../../../core/models/headless_models.dart';
import '../../../../core/theme/app_colors.dart';
import '../../../../core/theme/app_typography.dart';

class HeadlessCommandCardView extends StatelessWidget {
  final HeadlessEphemeralCardEntry entry;
  final VoidCallback onDismiss;
  final void Function(String text)? onAttachToPrompt;
  final void Function(int turn)? onRewindToTurn;

  const HeadlessCommandCardView({
    super.key,
    required this.entry,
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
    final out = s.stdout.trim().isNotEmpty ? s.stdout : s.stderr;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text('\$ ${s.command}',
                  style: AppTypography.codeSm.copyWith(
                      color: AppColors.textPrimary,
                      fontWeight: FontWeight.w600)),
            ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: ok
                    ? AppColors.statusEmerald.withValues(alpha: 0.15)
                    : AppColors.statusCoral.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                'exit ${s.exitCode}',
                style: AppTypography.codeXs.copyWith(
                    color:
                        ok ? AppColors.statusEmerald : AppColors.statusCoral),
              ),
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
    final rep = d.report.isNotEmpty ? d.report : d.raw.toString();
    return Container(
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
          rep,
          style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
        ),
      ),
    );
  }

  Widget _buildMcp(BuildContext context, SessionMcpStatus m) {
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
