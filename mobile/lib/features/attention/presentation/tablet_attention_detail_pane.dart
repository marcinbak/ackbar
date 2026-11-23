import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/models/session.dart';
import '../../../core/navigation/session_navigator.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/terminal_accessory_bar.dart';
import '../../../core/widgets/token_gauge_bar.dart';
import '../../headless/presentation/headless_auth_card.dart';

class TabletAttentionDetailPane extends ConsumerStatefulWidget {
  final int filterIndex;
  final Session? selectedPending;
  final Session? selectedInProgress;
  final DecisionAuditEntry? selectedHistory;

  const TabletAttentionDetailPane({
    super.key,
    required this.filterIndex,
    this.selectedPending,
    this.selectedInProgress,
    this.selectedHistory,
  });

  @override
  ConsumerState<TabletAttentionDetailPane> createState() =>
      _TabletAttentionDetailPaneState();
}

class _TabletAttentionDetailPaneState
    extends ConsumerState<TabletAttentionDetailPane> {
  final Map<String, int> _selectedOptions = {};
  final Map<String, String> _replyDrafts = {};
  final TextEditingController _replyCtrl = TextEditingController();

  void _setReplyText(String text) => _replyCtrl.value = TextEditingValue(
        text: text,
        selection: TextSelection.collapsed(offset: text.length),
      );

  @override
  void didUpdateWidget(covariant TabletAttentionDetailPane oldWidget) {
    super.didUpdateWidget(oldWidget);
    final oldId = oldWidget.selectedPending?.id;
    final newId = widget.selectedPending?.id;
    if (oldId != newId) {
      if (oldId != null) {
        _replyCtrl.text.isEmpty
            ? _replyDrafts.remove(oldId)
            : _replyDrafts[oldId] = _replyCtrl.text;
      }
      _setReplyText(newId != null ? (_replyDrafts[newId] ?? '') : '');
    }
  }

  @override
  void dispose() {
    _replyCtrl.dispose();
    super.dispose();
  }

  void _onAccessoryKey(String key) {
    if (key == 'Enter') {
      _replyCtrl.clear();
    } else if (key == 'Esc') {
      FocusScope.of(context).unfocus();
    } else {
      _replyCtrl.text += key;
    }
  }

  Widget _centered(Widget child) => SingleChildScrollView(
        padding: AppSpacing.paddingScreen,
        child: Center(
          child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 640), child: child),
        ),
      );

  @override
  Widget build(BuildContext context) {
    if (widget.filterIndex == 0) {
      final s = widget.selectedPending;
      if (s == null) {
        return Center(
          child: Text('All Caught Up! No blocked sessions.',
              style: AppTypography.titleMedium
                  .copyWith(color: AppColors.textSecondary)),
        );
      }
      return Column(children: [
        Expanded(
          child: _centered(
            s.blocked?.kind == BlockKind.auth
                ? HeadlessAuthCard(session: s, surfacePrefix: 'attention')
                : _pendingDetailCard(s),
          ),
        ),
        TerminalAccessoryBar(onKeyPressed: _onAccessoryKey),
      ]);
    }
    if (widget.filterIndex == 1) {
      final s = widget.selectedInProgress;
      if (s == null) return const Center(child: Text('No active tasks.'));
      return _centered(
        GlassCard(
          accentColor: AppColors.statusAmber,
          child:
              Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              Expanded(
                child: Text('${s.hostTag} • ${s.agentDisplayName}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.infoCyan)),
              ),
              const SizedBox(width: 8),
              StatusBadge.working(isCompact: true),
            ]),
            AppSpacing.gapH8,
            Text(s.displayTitle, style: AppTypography.titleMedium),
            AppSpacing.gapH8,
            Text(s.activity,
                style: AppTypography.codeSm
                    .copyWith(color: AppColors.statusAmberLight)),
            AppSpacing.gapH12,
            TokenGaugeBar(percentage: s.contextPct / 100.0),
            AppSpacing.gapH16,
            _sessionActionButtons(s),
          ]),
        ),
      );
    }
    final e = widget.selectedHistory;
    if (e == null) {
      return const Center(child: Text('No decision records in history.'));
    }
    return _centered(
      GlassCard(
        accentColor: AppColors.infoCyan,
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text('${e.actionType.toUpperCase()} • ${e.agent} @${e.host}',
              style: AppTypography.codeXs.copyWith(color: AppColors.infoCyan)),
          AppSpacing.gapH8,
          Text(e.sessionTitle, style: AppTypography.titleMedium),
          AppSpacing.gapH8,
          Text(e.summary, style: AppTypography.bodyMedium),
        ]),
      ),
    );
  }

  Widget _pendingDetailCard(Session session) {
    final blocked = session.blocked;
    final options = blocked?.options ?? const <String>[];
    final selectedOpt = _selectedOptions[session.id] ?? 0;

    return GlassCard(
      accentColor: AppColors.statusCoral,
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [
          AgentLogo(agent: session.agent, size: 12, color: session.agentColor),
          const SizedBox(width: 6),
          Expanded(
            child: Text('${session.agentDisplayName} @ ${session.hostTag}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style:
                    AppTypography.codeXs.copyWith(color: AppColors.infoCyan)),
          ),
          const SizedBox(width: 8),
          StatusBadge(
              status: AckbarSessionStatus.blocked,
              customLabel: blocked?.kind.label ?? 'BLOCKED',
              isCompact: true),
        ]),
        AppSpacing.gapH8,
        Text(session.displayTitle,
            style: AppTypography.titleMedium
                .copyWith(fontWeight: FontWeight.w700)),
        AppSpacing.gapH12,
        Container(
          width: double.infinity,
          padding: AppSpacing.paddingCardDense,
          decoration: BoxDecoration(
            color: AppColors.terminalBlack,
            borderRadius: AppSpacing.roundedMd,
            border: Border.all(color: AppColors.outlineSubtle),
          ),
          child: Text(
            blocked?.question ?? blocked?.reason ?? session.activity,
            style: AppTypography.codeMd
                .copyWith(color: AppColors.textPrimary, fontSize: 13),
          ),
        ),
        if (options.isNotEmpty) ...[
          AppSpacing.gapH12,
          for (var i = 0; i < options.length; i++)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: InkWell(
                onTap: () {
                  HapticFeedback.selectionClick();
                  setState(() => _selectedOptions[session.id] = i);
                },
                child: Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                  decoration: BoxDecoration(
                    color: selectedOpt == i
                        ? AppColors.statusEmerald.withOpacity(0.12)
                        : AppColors.surfaceHighlight,
                    borderRadius: AppSpacing.roundedMd,
                    border: Border.all(
                      color: selectedOpt == i
                          ? AppColors.statusEmerald
                          : AppColors.outlineSubtle,
                    ),
                  ),
                  child: Row(children: [
                    Icon(
                      selectedOpt == i
                          ? Icons.check_circle_rounded
                          : Icons.radio_button_unchecked_rounded,
                      size: 18,
                      color: selectedOpt == i
                          ? AppColors.statusEmerald
                          : AppColors.textMuted,
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                        child:
                            Text(options[i], style: AppTypography.bodySmall)),
                  ]),
                ),
              ),
            ),
        ],
        AppSpacing.gapH12,
        TextField(
          controller: _replyCtrl,
          style: AppTypography.bodySmall,
          decoration: const InputDecoration(
            hintText: 'Custom reply or voice prompt...',
            contentPadding: EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          ),
        ),
        AppSpacing.gapH12,
        SizedBox(
          width: double.infinity,
          height: 42,
          child: ElevatedButton.icon(
            onPressed: () async {
              final val = _replyCtrl.text.trim().isNotEmpty
                  ? _replyCtrl.text.trim()
                  : (options.isNotEmpty ? options[selectedOpt] : 'y');
              _replyDrafts.remove(session.id);
              _replyCtrl.clear();
              final ok = await ref
                  .read(fleetSessionsProvider.notifier)
                  .respondToSession(
                    sessionId: session.id,
                    action: 'answer',
                    value: val,
                    answerSummary: 'Answered: $val',
                  );
              if (!ok && mounted) {
                _replyDrafts[session.id] = val;
                if (widget.selectedPending?.id == session.id) {
                  _setReplyText(val);
                }
              }
            },
            icon: const Icon(Icons.check_circle_rounded, size: 16),
            label: const Text('Submit Choice'),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppColors.statusEmerald,
              foregroundColor: AppColors.terminalBlack,
            ),
          ),
        ),
        AppSpacing.gapH8,
        _sessionActionButtons(session),
      ]),
    );
  }

  Widget _sessionActionButtons(Session session) => Row(children: [
        Expanded(
          child: OutlinedButton.icon(
            key: Key('attention_open_session_${session.id}'),
            onPressed: () =>
                SessionNavigator.openSession(context, ref, session),
            icon: Icon(
                session.isHeadless
                    ? Icons.chat_bubble_outline_rounded
                    : Icons.terminal_rounded,
                size: 15),
            label: Text(session.isHeadless ? 'Open Chat' : 'Live Terminal'),
            style: OutlinedButton.styleFrom(
              foregroundColor: AppColors.statusEmerald,
              side: const BorderSide(color: AppColors.statusEmerald),
            ),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: OutlinedButton.icon(
            key: Key('attention_open_transcript_${session.id}'),
            onPressed: () =>
                SessionNavigator.openTranscript(context, session, ref: ref),
            icon: const Icon(Icons.article_outlined, size: 15),
            label: const Text('Transcript'),
          ),
        ),
      ]);
}
