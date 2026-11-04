import 'package:flutter/material.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';

typedef ChipSelectCallback = void Function(String chipText);

/// Commuter quick-reply chips bar displayed above the prompt input field
class SpeechCompanionBar extends StatelessWidget {
  final List<String> chips;
  final ChipSelectCallback onChipSelected;
  final VoidCallback onClear;

  const SpeechCompanionBar({
    super.key,
    required this.chips,
    required this.onChipSelected,
    required this.onClear,
  });

  @override
  Widget build(BuildContext context) {
    if (chips.isEmpty) {
      return const SizedBox.shrink();
    }

    return Container(
      padding:
          const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 6),
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(
          top: BorderSide(color: AppColors.outlineSubtle, width: 0.5),
        ),
      ),
      child: Row(
        children: [
          const Icon(Icons.record_voice_over_rounded,
              size: 14, color: AppColors.infoCyan),
          const SizedBox(width: 8),
          Expanded(
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: Row(
                children: chips.map((chip) {
                  return Padding(
                    padding: const EdgeInsets.only(right: 6.0),
                    child: ActionChip(
                      label: Text(chip),
                      onPressed: () => onChipSelected(chip),
                      backgroundColor: AppColors.surfaceHighlight,
                      labelStyle: AppTypography.codeXs.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w600,
                        fontSize: 11,
                      ),
                      padding: const EdgeInsets.symmetric(
                          horizontal: 6, vertical: 2),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(12),
                        side: BorderSide(
                          color: AppColors.infoCyan.withOpacity(0.4),
                          width: 0.8,
                        ),
                      ),
                    ),
                  );
                }).toList(),
              ),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.close_rounded,
                size: 14, color: AppColors.textMuted),
            tooltip: 'Dismiss suggestions',
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
            onPressed: onClear,
          ),
        ],
      ),
    );
  }
}
