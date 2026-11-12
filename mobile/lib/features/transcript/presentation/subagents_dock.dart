import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../core/models/subagent.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';

class SubagentsDock extends StatefulWidget {
  final List<SubagentInfo> runningSubagents;

  const SubagentsDock({
    super.key,
    required this.runningSubagents,
  });

  @override
  State<SubagentsDock> createState() => _SubagentsDockState();
}

class _SubagentsDockState extends State<SubagentsDock> {
  bool _isExpanded = false;

  @override
  Widget build(BuildContext context) {
    if (widget.runningSubagents.isEmpty) {
      return const SizedBox.shrink();
    }

    final count = widget.runningSubagents.length;
    final countText =
        count == 1 ? '1 subagent running...' : '$count subagents running...';

    return Container(
      decoration: const BoxDecoration(
        color: Color(0xFF141720),
        border: Border(
          top: BorderSide(color: Color(0xFF2C3245), width: 1),
          bottom: BorderSide(color: AppColors.outlineSubtle, width: 0.5),
        ),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          InkWell(
            onTap: () {
              HapticFeedback.selectionClick();
              setState(() {
                _isExpanded = !_isExpanded;
              });
            },
            child: Padding(
              padding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.md,
                vertical: 7,
              ),
              child: Row(
                children: [
                  Container(
                    width: 7,
                    height: 7,
                    decoration: const BoxDecoration(
                      color: AppColors.statusAmber,
                      shape: BoxShape.circle,
                      boxShadow: [
                        BoxShadow(
                          color: AppColors.statusAmber,
                          blurRadius: 4,
                          spreadRadius: 1,
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 8),
                  const Text('⚡', style: TextStyle(fontSize: 11)),
                  const SizedBox(width: 4),
                  Expanded(
                    child: Text(
                      countText,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.statusAmberLight,
                        fontWeight: FontWeight.w700,
                        fontSize: 11,
                      ),
                    ),
                  ),
                  Icon(
                    _isExpanded
                        ? Icons.keyboard_arrow_up_rounded
                        : Icons.keyboard_arrow_down_rounded,
                    size: 18,
                    color: AppColors.textMuted,
                  ),
                ],
              ),
            ),
          ),
          if (_isExpanded)
            Container(
              constraints: const BoxConstraints(maxHeight: 200),
              padding: const EdgeInsets.fromLTRB(
                AppSpacing.md,
                0,
                AppSpacing.md,
                8,
              ),
              child: ListView.separated(
                shrinkWrap: true,
                itemCount: widget.runningSubagents.length,
                separatorBuilder: (_, __) => const SizedBox(height: 6),
                itemBuilder: (context, idx) {
                  final sub = widget.runningSubagents[idx];
                  final prompt = sub.prompt.trim();

                  return Container(
                    padding: const EdgeInsets.all(8),
                    decoration: BoxDecoration(
                      color: AppColors.surfaceHighlight.withOpacity(0.6),
                      borderRadius: BorderRadius.circular(6),
                      border: Border.all(
                        color: AppColors.outlineSubtle.withOpacity(0.6),
                        width: 0.6,
                      ),
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Container(
                              width: 6,
                              height: 6,
                              decoration: const BoxDecoration(
                                color: AppColors.statusAmber,
                                shape: BoxShape.circle,
                              ),
                            ),
                            const SizedBox(width: 6),
                            Expanded(
                              child: Text(
                                sub.displayName,
                                style: AppTypography.codeXs.copyWith(
                                  color: AppColors.textPrimary,
                                  fontWeight: FontWeight.w700,
                                  fontSize: 11,
                                ),
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                            if (sub.displayRole != null) ...[
                              const SizedBox(width: 6),
                              Container(
                                padding: const EdgeInsets.symmetric(
                                  horizontal: 5,
                                  vertical: 1,
                                ),
                                decoration: BoxDecoration(
                                  color: AppColors.surface,
                                  borderRadius: BorderRadius.circular(4),
                                  border: Border.all(
                                    color: AppColors.infoCyan.withOpacity(0.4),
                                    width: 0.5,
                                  ),
                                ),
                                child: Text(
                                  sub.displayRole!,
                                  style: AppTypography.codeXs.copyWith(
                                    color: AppColors.infoCyan,
                                    fontSize: 9.5,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ),
                            ],
                            if (prompt.isNotEmpty) ...[
                              const SizedBox(width: 6),
                              InkWell(
                                onTap: () {
                                  Clipboard.setData(
                                    ClipboardData(text: prompt),
                                  );
                                  HapticFeedback.lightImpact();
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    const SnackBar(
                                      content: Text('Subagent prompt copied'),
                                      duration: Duration(seconds: 1),
                                      backgroundColor:
                                          AppColors.surfaceHighlight,
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
                          ],
                        ),
                        if (prompt.isNotEmpty) ...[
                          const SizedBox(height: 5),
                          Container(
                            width: double.infinity,
                            padding: const EdgeInsets.all(6),
                            decoration: BoxDecoration(
                              color: AppColors.terminalBlack,
                              borderRadius: BorderRadius.circular(4),
                              border: Border.all(
                                color: AppColors.outlineSubtle.withOpacity(0.4),
                                width: 0.5,
                              ),
                            ),
                            child: Text(
                              prompt,
                              style: AppTypography.codeXs.copyWith(
                                color: AppColors.textSecondary,
                                fontSize: 10,
                                height: 1.3,
                              ),
                              maxLines: 4,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        ],
                      ],
                    ),
                  );
                },
              ),
            ),
        ],
      ),
    );
  }
}
