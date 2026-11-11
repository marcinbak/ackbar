import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';

class ParsedToolCall {
  final String name;
  final String detail;

  const ParsedToolCall({required this.name, required this.detail});

  factory ParsedToolCall.parse(String raw) {
    final trimmed = raw.trim();
    if (trimmed.isEmpty) return const ParsedToolCall(name: 'tool', detail: '');

    final colonIdx = trimmed.indexOf(':');
    if (colonIdx > 0 && colonIdx < 30) {
      final n = trimmed.substring(0, colonIdx).trim();
      final d = trimmed.substring(colonIdx + 1).trim();
      if (!n.contains(' ') || n.length < 20) {
        return ParsedToolCall(name: n, detail: d);
      }
    }

    final match = RegExp(r'^([a-zA-Z0-9_\-]+)\s*\((.+)\)$').firstMatch(trimmed);
    if (match != null) {
      return ParsedToolCall(
        name: match.group(1)!.trim(),
        detail: match.group(2)!.trim(),
      );
    }

    return ParsedToolCall(name: trimmed, detail: '');
  }
}

class TranscriptToolCallsGroup extends StatelessWidget {
  final List<String> toolCalls;

  const TranscriptToolCallsGroup({
    super.key,
    required this.toolCalls,
  });

  String _getToolGroupSummaryLabel(List<String> calls) {
    if (calls.isEmpty) return '⚡ Tools';
    final parsed = calls.map(ParsedToolCall.parse).toList();
    final counts = <String, int>{};
    for (final p in parsed) {
      counts[p.name] = (counts[p.name] ?? 0) + 1;
    }
    final uniqueTools = counts.keys.toList();
    final total = calls.length;

    if (uniqueTools.length == 1) {
      final name = uniqueTools[0];
      if (total > 1) {
        return '⚡ $name x$total';
      }
      if (parsed[0].detail.isNotEmpty) {
        final shortDetail = parsed[0].detail.length > 35
            ? '${parsed[0].detail.substring(0, 32)}...'
            : parsed[0].detail;
        return '⚡ $name: $shortDetail';
      }
      return '⚡ $name';
    }

    final breakdown =
        uniqueTools.map((name) => '$name x${counts[name]}').join(', ');
    return '⚡ $total tools ($breakdown)';
  }

  @override
  Widget build(BuildContext context) {
    final summaryLabel = _getToolGroupSummaryLabel(toolCalls);
    final parsed = toolCalls.map(ParsedToolCall.parse).toList();
    final total = toolCalls.length;

    return Container(
      decoration: BoxDecoration(
        color: AppColors.terminalBlack.withOpacity(0.6),
        borderRadius: AppSpacing.roundedSm,
        border: Border.all(
          color: AppColors.outlineSubtle.withOpacity(0.7),
          width: 0.6,
        ),
      ),
      child: Theme(
        data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          tilePadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
          childrenPadding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
          dense: true,
          leading: const Icon(
            Icons.bolt_rounded,
            size: 16,
            color: AppColors.statusAmberLight,
          ),
          title: Row(
            children: [
              Expanded(
                child: Text(
                  summaryLabel,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.statusAmberLight,
                    fontWeight: FontWeight.w700,
                    fontSize: 11,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 6,
                  vertical: 1.5,
                ),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(
                    color: AppColors.outlineSubtle,
                    width: 0.5,
                  ),
                ),
                child: Text(
                  '$total ${total == 1 ? 'action' : 'actions'}',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textMuted,
                    fontSize: 9.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          children: [
            ListView.separated(
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              itemCount: parsed.length,
              separatorBuilder: (_, __) => const SizedBox(height: 6),
              itemBuilder: (context, idx) {
                final item = parsed[idx];
                return Container(
                  padding: const EdgeInsets.all(8),
                  decoration: BoxDecoration(
                    color: AppColors.surfaceHighlight.withOpacity(0.5),
                    borderRadius: BorderRadius.circular(6),
                    border: Border.all(
                      color: AppColors.outlineSubtle.withOpacity(0.5),
                      width: 0.5,
                    ),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Text(
                            '#${idx + 1}',
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.textMuted,
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                          const SizedBox(width: 6),
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 6,
                              vertical: 1,
                            ),
                            decoration: BoxDecoration(
                              color: AppColors.surface,
                              borderRadius: BorderRadius.circular(4),
                              border: Border.all(
                                color: AppColors.statusAmberLight.withOpacity(
                                  0.3,
                                ),
                                width: 0.5,
                              ),
                            ),
                            child: Text(
                              item.name,
                              style: AppTypography.codeXs.copyWith(
                                color: AppColors.statusAmberLight,
                                fontSize: 10.5,
                                fontWeight: FontWeight.w700,
                              ),
                            ),
                          ),
                          const Spacer(),
                          if (item.detail.isNotEmpty)
                            InkWell(
                              onTap: () {
                                Clipboard.setData(
                                  ClipboardData(text: item.detail),
                                );
                                HapticFeedback.lightImpact();
                                ScaffoldMessenger.of(context).showSnackBar(
                                  const SnackBar(
                                    content: Text(
                                      'Command copied to clipboard',
                                    ),
                                    duration: Duration(seconds: 1),
                                    backgroundColor: AppColors.surfaceHighlight,
                                  ),
                                );
                              },
                              child: const Padding(
                                padding: EdgeInsets.all(2),
                                child: Icon(
                                  Icons.copy_rounded,
                                  size: 13,
                                  color: AppColors.textMuted,
                                ),
                              ),
                            ),
                        ],
                      ),
                      if (item.detail.isNotEmpty) ...[
                        const SizedBox(height: 4),
                        Container(
                          width: double.infinity,
                          padding: const EdgeInsets.symmetric(
                            horizontal: 6,
                            vertical: 4,
                          ),
                          decoration: BoxDecoration(
                            color: AppColors.terminalBlack,
                            borderRadius: BorderRadius.circular(4),
                            border: Border.all(
                              color: AppColors.outlineSubtle.withOpacity(0.4),
                              width: 0.5,
                            ),
                          ),
                          child: SelectableText(
                            item.detail,
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.infoCyan,
                              fontSize: 10.5,
                              height: 1.3,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                );
              },
            ),
          ],
        ),
      ),
    );
  }
}

class TranscriptThinkingAccordion extends StatelessWidget {
  final String thinking;

  const TranscriptThinkingAccordion({
    super.key,
    required this.thinking,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppColors.surfaceHighlight.withOpacity(0.4),
        borderRadius: AppSpacing.roundedSm,
        border: Border.all(
          color: AppColors.outlineSubtle.withOpacity(0.6),
          width: 0.6,
        ),
      ),
      child: Theme(
        data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          tilePadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
          childrenPadding: const EdgeInsets.fromLTRB(10, 0, 10, 8),
          dense: true,
          leading: const Icon(
            Icons.psychology_outlined,
            size: 16,
            color: AppColors.textMuted,
          ),
          title: Text(
            '💭 Thought Process',
            style: AppTypography.codeXs.copyWith(
              color: AppColors.textSecondary,
              fontWeight: FontWeight.w600,
              fontSize: 11,
            ),
          ),
          children: [
            SelectableText(
              thinking.trim(),
              style: AppTypography.codeXs.copyWith(
                color: AppColors.textMuted,
                fontSize: 11,
                height: 1.35,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class TranscriptSystemBubble extends StatelessWidget {
  final String content;

  const TranscriptSystemBubble({
    super.key,
    required this.content,
  });

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 12),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        decoration: BoxDecoration(
          color: AppColors.surfaceHighlight.withOpacity(0.5),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(
              Icons.info_outline_rounded,
              size: 12,
              color: AppColors.textMuted,
            ),
            const SizedBox(width: 6),
            Flexible(
              child: Text(
                content.length > 80
                    ? '${content.substring(0, 80)}...'
                    : content,
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.textMuted,
                  fontSize: 10.5,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
