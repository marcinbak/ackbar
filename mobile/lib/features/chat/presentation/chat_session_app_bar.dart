import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/session.dart';
import '../../../core/navigation/session_navigator.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_typography.dart';

class ChatSessionAppBar extends ConsumerWidget implements PreferredSizeWidget {
  final Session session;
  final int messageCount;
  final VoidCallback onToggleLiveVoice;
  final VoidCallback onToggleMic;
  final VoidCallback onTakeWheel;
  final VoidCallback onRefresh;

  const ChatSessionAppBar({
    super.key,
    required this.session,
    required this.messageCount,
    required this.onToggleLiveVoice,
    required this.onToggleMic,
    required this.onTakeWheel,
    required this.onRefresh,
  });

  @override
  Size get preferredSize => const Size.fromHeight(kToolbarHeight);

  IconButton _action(
    IconData icon,
    String tip,
    VoidCallback press, [
    Color? color,
    Key? key,
  ]) =>
      IconButton(
        key: key,
        icon: Icon(icon, size: 20, color: color),
        tooltip: tip,
        onPressed: press,
      );

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final audio = ref.watch(audioCompanionServiceProvider);
    final auto = audio.autoReadEnabled;
    final live = audio.liveVoiceConnected;

    return AppBar(
      backgroundColor: AppColors.surface,
      elevation: 0,
      leading: IconButton(
        icon:
            const Icon(Icons.arrow_back_rounded, color: AppColors.textPrimary),
        onPressed: () => Navigator.of(context).pop(),
      ),
      title: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            const Icon(Icons.chat_bubble_outline_rounded,
                size: 16, color: AppColors.infoCyan),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                session.displayTitle,
                style: AppTypography.titleMedium.copyWith(
                  color: AppColors.textPrimary,
                  fontWeight: FontWeight.w700,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ]),
          Text(
            '${session.agentDisplayName} @ ${session.hostTag} • $messageCount messages',
            style: AppTypography.codeXs
                .copyWith(color: AppColors.textSecondary, fontSize: 10),
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
      actions: [
        if (session.agent == 'antigravity')
          _action(
            Icons.record_voice_over_rounded,
            live ? 'Live Voice: Connected' : 'Live Voice: Connect',
            onToggleLiveVoice,
            live ? AppColors.statusCoral : AppColors.textMuted,
          ),
        _action(
          auto ? Icons.volume_up_rounded : Icons.volume_off_rounded,
          auto ? 'Auto-read enabled' : 'Auto-read disabled',
          ref.read(audioCompanionServiceProvider.notifier).toggleAutoRead,
          auto ? AppColors.infoCyan : AppColors.textMuted,
        ),
        _action(
          audio.micEnabled ? Icons.mic_rounded : Icons.mic_off_rounded,
          audio.micEnabled ? 'Microphone active' : 'Microphone off',
          onToggleMic,
          audio.micEnabled ? AppColors.statusCoral : AppColors.textMuted,
        ),
        _action(
          Icons.sports_motorsports_rounded,
          'Take the Wheel',
          onTakeWheel,
          AppColors.statusEmerald,
          const Key('chat_take_wheel_button'),
        ),
        _action(
          Icons.article_outlined,
          'View Transcript',
          () => SessionNavigator.openTranscript(context, session,
              showOpenSessionAction: false),
          AppColors.infoCyan,
          const Key('chat_open_transcript_button'),
        ),
        _action(Icons.refresh_rounded, 'Refresh', onRefresh),
      ],
    );
  }
}
