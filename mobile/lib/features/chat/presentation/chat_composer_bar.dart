import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/services/audio_companion_service.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../transcript/presentation/speech_companion_bar.dart';

class ChatComposerBar extends ConsumerWidget {
  final Session session;
  final bool isWorking;
  final TextEditingController promptController;
  final FocusNode promptFocusNode;
  final VoidCallback onSend;
  final VoidCallback onCancel;

  const ChatComposerBar({
    super.key,
    required this.session,
    required this.isWorking,
    required this.promptController,
    required this.promptFocusNode,
    required this.onSend,
    required this.onCancel,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final audioCompanion = ref.watch(audioCompanionServiceProvider);

    return Container(
      decoration: const BoxDecoration(
        color: AppColors.surface,
        border: Border(
          top: BorderSide(color: AppColors.outlineSubtle, width: 1),
        ),
      ),
      padding: const EdgeInsets.symmetric(
        horizontal: AppSpacing.md,
        vertical: 8,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (audioCompanion.activeSuggestedChips.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: SpeechCompanionBar(
                chips: audioCompanion.activeSuggestedChips,
                onChipSelected: (chip) {
                  promptController.text = chip;
                  onSend();
                },
                onClear: () {
                  ref.read(audioCompanionServiceProvider.notifier).clearChips();
                },
              ),
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: Container(
                  decoration: BoxDecoration(
                    color: AppColors.terminalBlack,
                    borderRadius: BorderRadius.circular(10),
                    border: Border.all(
                      color: isWorking
                          ? AppColors.infoCyan
                          : AppColors.outlineSubtle,
                      width: 1,
                    ),
                  ),
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 6,
                  ),
                  child: TextField(
                    controller: promptController,
                    focusNode: promptFocusNode,
                    minLines: 1,
                    maxLines: 5,
                    style: AppTypography.bodySmall.copyWith(
                      color: AppColors.textPrimary,
                    ),
                    textInputAction: TextInputAction.send,
                    onSubmitted: (_) => onSend(),
                    decoration: InputDecoration(
                      isDense: true,
                      contentPadding: EdgeInsets.zero,
                      border: InputBorder.none,
                      hintText: 'Ask ${session.agentDisplayName}...',
                      hintStyle: AppTypography.bodySmall.copyWith(
                        color: AppColors.textMuted,
                      ),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              ValueListenableBuilder<TextEditingValue>(
                valueListenable: promptController,
                builder: (context, value, _) {
                  final hasPromptText = value.text.trim().isNotEmpty;
                  return Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (isWorking)
                        IconButton(
                          icon: const Icon(
                            Icons.stop_circle_rounded,
                            size: 28,
                            color: AppColors.statusCoral,
                          ),
                          tooltip: 'Cancel turn',
                          onPressed: onCancel,
                        ),
                      if (!isWorking || hasPromptText)
                        IconButton(
                          icon: const Icon(
                            Icons.arrow_upward_rounded,
                            size: 22,
                            color: AppColors.infoCyan,
                          ),
                          style: IconButton.styleFrom(
                            backgroundColor: AppColors.surfaceHighlight,
                            shape: const CircleBorder(),
                          ),
                          tooltip: 'Send prompt',
                          onPressed: onSend,
                        ),
                    ],
                  );
                },
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.only(top: 4, left: 2, right: 2),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  session.isHeadless
                      ? '💬 Headless turn-by-turn • OAuth Flat-rate'
                      : '🖥️ Tmux process • Interactive shell',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textMuted,
                    fontSize: 10,
                  ),
                ),
                if (isWorking)
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const SizedBox(
                        width: 10,
                        height: 10,
                        child: CircularProgressIndicator(
                          strokeWidth: 1.5,
                          color: AppColors.infoCyan,
                        ),
                      ),
                      const SizedBox(width: 4),
                      Text(
                        'Working...',
                        style: AppTypography.codeXs.copyWith(
                          color: AppColors.infoCyan,
                          fontSize: 10,
                        ),
                      ),
                    ],
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
