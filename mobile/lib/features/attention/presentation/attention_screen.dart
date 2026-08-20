import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/terminal_accessory_bar.dart';
import '../../../core/widgets/token_gauge_bar.dart';
import '../../terminal/presentation/terminal_screen.dart';

/// Screen managing developer attention queue, active working sessions, and decision audit history.
/// Matches Stitch design ecf558261ecc479c8ca30ec6d251af98.
class AttentionScreen extends ConsumerStatefulWidget {
  const AttentionScreen({super.key});

  @override
  ConsumerState<AttentionScreen> createState() => _AttentionScreenState();
}

class _AttentionScreenState extends ConsumerState<AttentionScreen> {
  final PageController _pageController = PageController();
  int _currentPageIndex = 0;
  final Map<String, int> _selectedOptionIndices = {};
  final TextEditingController _customInputController = TextEditingController();

  @override
  void dispose() {
    _pageController.dispose();
    _customInputController.dispose();
    super.dispose();
  }

  void _onTerminalKeyPress(String key) {
    if (key == 'Enter') {
      _customInputController.clear();
    } else if (key == 'Esc') {
      FocusScope.of(context).unfocus();
    } else {
      _customInputController.text += key;
    }
  }

  @override
  Widget build(BuildContext context) {
    final attentionFilter = ref.watch(attentionFilterIndexProvider);
    final pendingSessions = ref.watch(attentionPendingSessionsProvider);
    final inProgressSessions = ref.watch(inProgressSessionsProvider);
    final auditHistory = ref.watch(decisionAuditProvider);
    final hosts = ref.watch(hostsListProvider);

    final tabs = [
      FilterTabItem(
        label: '🔴 Pending',
        count: pendingSessions.length,
        countBadgeColor: pendingSessions.isNotEmpty ? AppColors.statusCoral : null,
        countTextColor: pendingSessions.isNotEmpty ? Colors.white : null,
      ),
      FilterTabItem(
        label: '⚡ In-Progress',
        count: inProgressSessions.length,
      ),
      FilterTabItem(
        label: 'History',
        count: auditHistory.length,
      ),
    ];

    final hostIndicators = hosts.map((h) => h.toIndicator()).toList();

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'ATTENTION CONTROL',
        hosts: hostIndicators,
      ),
      body: Column(
        children: [
          // Top Segmented Tab Filter
          Padding(
            padding: const EdgeInsets.symmetric(vertical: AppSpacing.sm),
            child: SegmentedFilterTabs(
              tabs: tabs,
              selectedIndex: attentionFilter,
              onTabSelected: (index) {
                ref.read(attentionFilterIndexProvider.notifier).state = index;
              },
            ),
          ),

          // Main Sub-Views Content
          Expanded(
            child: IndexedStack(
              index: attentionFilter,
              children: [
                _buildPendingSubView(pendingSessions),
                _buildInProgressSubView(inProgressSessions),
                _buildHistorySubView(auditHistory),
              ],
            ),
          ),

          // Bottom Terminal Accessory Bar
          TerminalAccessoryBar(
            onKeyPressed: _onTerminalKeyPress,
          ),
        ],
      ),
    );
  }

  // --- Sub-View 1: Pending Blocked Sessions ViewPager ---

  Widget _buildPendingSubView(List<Session> pendingSessions) {
    if (pendingSessions.isEmpty) {
      return Center(
        child: Padding(
          padding: AppSpacing.paddingScreen,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                padding: const EdgeInsets.all(20),
                decoration: BoxDecoration(
                  color: AppColors.statusEmerald.withOpacity(0.1),
                  shape: BoxShape.circle,
                  border: Border.all(color: AppColors.statusEmerald.withOpacity(0.3), width: 1.5),
                ),
                child: const Text('🎉', style: TextStyle(fontSize: 36)),
              ),
              AppSpacing.gapH16,
              Text(
                'All Caught Up!',
                style: AppTypography.headlineLarge.copyWith(
                  fontWeight: FontWeight.w700,
                  color: AppColors.textPrimary,
                ),
              ),
              AppSpacing.gapH8,
              Text(
                'No agent sessions currently blocked or requiring manual approval.',
                textAlign: TextAlign.center,
                style: AppTypography.bodyMedium.copyWith(color: AppColors.textSecondary),
              ),
            ],
          ),
        ),
      );
    }

    final safePageIndex = _currentPageIndex.clamp(0, pendingSessions.length - 1);

    return Column(
      children: [
        // Page Navigation Indicator Bar
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: 4.0),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              IconButton(
                icon: const Icon(Icons.arrow_back_ios_rounded, size: 16),
                color: safePageIndex > 0 ? AppColors.infoCyan : AppColors.textMuted,
                onPressed: safePageIndex > 0
                    ? () {
                        _pageController.previousPage(
                          duration: const Duration(milliseconds: 250),
                          curve: Curves.easeInOut,
                        );
                      }
                    : null,
              ),
              Row(
                children: [
                  Text(
                    'Item ${safePageIndex + 1} of ${pendingSessions.length}',
                    style: AppTypography.codeXs.copyWith(
                      fontWeight: FontWeight.w700,
                      color: AppColors.textPrimary,
                    ),
                  ),
                  const SizedBox(width: 8),
                  Row(
                    children: List.generate(pendingSessions.length, (idx) {
                      final isCurrent = idx == safePageIndex;
                      return Container(
                        margin: const EdgeInsets.symmetric(horizontal: 2.5),
                        width: isCurrent ? 14 : 6,
                        height: 6,
                        decoration: BoxDecoration(
                          color: isCurrent ? AppColors.infoCyan : AppColors.surfaceHighlight,
                          borderRadius: BorderRadius.circular(3),
                        ),
                      );
                    }),
                  ),
                ],
              ),
              IconButton(
                icon: const Icon(Icons.arrow_forward_ios_rounded, size: 16),
                color: safePageIndex < pendingSessions.length - 1
                    ? AppColors.infoCyan
                    : AppColors.textMuted,
                onPressed: safePageIndex < pendingSessions.length - 1
                    ? () {
                        _pageController.nextPage(
                          duration: const Duration(milliseconds: 250),
                          curve: Curves.easeInOut,
                        );
                      }
                    : null,
              ),
            ],
          ),
        ),

        // PageView of Blocked Session Items
        Expanded(
          child: PageView.builder(
            controller: _pageController,
            physics: const BouncingScrollPhysics(),
            itemCount: pendingSessions.length,
            onPageChanged: (idx) {
              setState(() => _currentPageIndex = idx);
            },
            itemBuilder: (context, index) {
              final session = pendingSessions[index];
              return SingleChildScrollView(
                padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
                child: _buildPendingCard(session),
              );
            },
          ),
        ),
      ],
    );
  }

  Widget _buildPendingCard(Session session) {
    final blocked = session.blocked;
    final options = blocked?.options ?? [];
    final selectedOptionIndex = _selectedOptionIndices[session.id] ?? 0;

    return GlassCard(
      accentColor: AppColors.statusCoral,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header Row
          Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: session.agentColor.withOpacity(0.15),
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: session.agentColor.withOpacity(0.4), width: 0.8),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(session.agentSymbol, style: const TextStyle(fontSize: 11)),
                    const SizedBox(width: 4),
                    Text(
                      session.agentDisplayName,
                      style: AppTypography.codeXs.copyWith(
                        color: session.agentColor,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                ),
                child: Text(
                  session.hostTag,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const Spacer(),
              StatusBadge(
                status: AckbarSessionStatus.blocked,
                customLabel: blocked?.kind.label ?? 'BLOCKED',
                isCompact: true,
              ),
            ],
          ),
          AppSpacing.gapH12,

          // Session display title
          Text(
            session.displayTitle,
            style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w700),
          ),
          AppSpacing.gapH8,

          // Question / Prompt in JetBrains Mono
          Container(
            width: double.infinity,
            padding: AppSpacing.paddingCardDense,
            decoration: BoxDecoration(
              color: AppColors.terminalBlack,
              borderRadius: AppSpacing.roundedMd,
              border: Border.all(color: AppColors.outlineSubtle, width: 1),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'QUESTION & PROMPT:',
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.statusCoral,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  blocked?.question ?? blocked?.reason ?? session.activity,
                  style: AppTypography.codeMd.copyWith(
                    color: AppColors.textPrimary,
                    fontSize: 13,
                    height: 1.4,
                  ),
                ),
              ],
            ),
          ),
          AppSpacing.gapH12,

          // Selectable Options (if provided)
          if (options.isNotEmpty) ...[
            Text(
              'SELECT AN ACTION OR OPTION:',
              style: AppTypography.codeXs.copyWith(
                letterSpacing: 0.5,
                fontWeight: FontWeight.w700,
                color: AppColors.textSecondary,
              ),
            ),
            AppSpacing.gapH8,
            ...List.generate(options.length, (optIdx) {
              final isSelected = selectedOptionIndex == optIdx;
              return Padding(
                padding: const EdgeInsets.only(bottom: 6.0),
                child: Material(
                  color: isSelected
                      ? AppColors.statusEmerald.withOpacity(0.12)
                      : AppColors.surfaceHighlight,
                  shape: RoundedRectangleBorder(
                    borderRadius: AppSpacing.roundedMd,
                    side: BorderSide(
                      color: isSelected
                          ? AppColors.statusEmerald
                          : AppColors.outlineSubtle,
                      width: isSelected ? 1.5 : 1.0,
                    ),
                  ),
                  clipBehavior: Clip.antiAlias,
                  child: InkWell(
                    onTap: () {
                      HapticFeedback.selectionClick();
                      setState(() {
                        _selectedOptionIndices[session.id] = optIdx;
                      });
                    },
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                      child: Row(
                        children: [
                          Icon(
                            isSelected
                                ? Icons.check_circle_rounded
                                : Icons.radio_button_unchecked_rounded,
                            size: 18,
                            color: isSelected ? AppColors.statusEmerald : AppColors.textMuted,
                          ),
                          const SizedBox(width: 10),
                          Expanded(
                            child: Text(
                              options[optIdx],
                              style: AppTypography.bodySmall.copyWith(
                                color: isSelected
                                    ? AppColors.textPrimary
                                    : AppColors.textSecondary,
                                fontWeight: isSelected ? FontWeight.w600 : FontWeight.w400,
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              );
            }),
            AppSpacing.gapH12,
          ],

          // Context Rationale Box
          if (blocked?.reason.isNotEmpty == true) ...[
            Container(
              padding: AppSpacing.paddingCardDense,
              decoration: BoxDecoration(
                color: AppColors.surfaceHighlight.withOpacity(0.5),
                borderRadius: AppSpacing.roundedMd,
                border: Border.all(color: AppColors.outlineSubtle, width: 0.8),
              ),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('📝', style: TextStyle(fontSize: 13)),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'Context: ${blocked!.reason}',
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.statusAmberLight,
                        fontSize: 11,
                      ),
                    ),
                  ),
                ],
              ),
            ),
            AppSpacing.gapH12,
          ],

          // Custom Reply Input with Mic Icon for Voice Companion
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _customInputController,
                  style: AppTypography.bodySmall.copyWith(color: AppColors.textPrimary),
                  decoration: InputDecoration(
                    hintText: 'Custom reply or voice prompt...',
                    hintStyle: AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
                    contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                    prefixIcon: const Icon(Icons.keyboard_outlined, size: 16, color: AppColors.textMuted),
                    suffixIcon: IconButton(
                      icon: const Icon(Icons.mic_none_rounded, size: 18, color: AppColors.infoCyan),
                      tooltip: 'Voice Companion Input',
                      onPressed: () {
                        HapticFeedback.lightImpact();
                        _customInputController.text = 'Proceed with pure Go modernc.org/sqlite';
                      },
                    ),
                  ),
                ),
              ),
            ],
          ),
          AppSpacing.gapH16,

          // Action Buttons
          Row(
            children: [
              IconButton(
                icon: const Icon(Icons.terminal_rounded, size: 20, color: AppColors.statusEmerald),
                tooltip: 'Attach Terminal',
                style: IconButton.styleFrom(
                  backgroundColor: AppColors.statusEmerald.withOpacity(0.12),
                  side: const BorderSide(color: AppColors.statusEmerald, width: 1),
                  shape: RoundedRectangleBorder(borderRadius: AppSpacing.roundedSm),
                ),
                onPressed: () => TerminalScreen.open(context, session),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: () => _showTranscriptModal(context, session),
                  icon: const Icon(Icons.article_outlined, size: 15),
                  label: const Text('Transcript'),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppColors.infoCyan,
                    side: const BorderSide(color: AppColors.infoCyan),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                flex: 2,
                child: ElevatedButton.icon(
                  onPressed: () async {
                    String answerValue;
                    if (_customInputController.text.trim().isNotEmpty) {
                      answerValue = _customInputController.text.trim();
                      _customInputController.clear();
                    } else if (options.isNotEmpty) {
                      answerValue = options[selectedOptionIndex];
                    } else {
                      answerValue = 'y';
                    }

                    await ref.read(fleetSessionsProvider.notifier).respondToSession(
                          sessionId: session.id,
                          action: 'answer',
                          value: answerValue,
                          answerSummary: 'Answered: $answerValue',
                        );

                    if (context.mounted) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(
                          content: Text('Unblocked session: $answerValue'),
                          backgroundColor: AppColors.statusEmerald,
                        ),
                      );
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
            ],
          ),
        ],
      ),
    );
  }

  // --- Sub-View 2: In-Progress Working Sessions ---

  Widget _buildInProgressSubView(List<Session> inProgress) {
    if (inProgress.isEmpty) {
      return Center(
        child: Text(
          'No active tasks currently executing.',
          style: AppTypography.bodyMedium.copyWith(color: AppColors.textMuted),
        ),
      );
    }

    return ListView.builder(
      padding: AppSpacing.paddingScreen,
      itemCount: inProgress.length,
      itemBuilder: (context, index) {
        final session = inProgress[index];
        return Padding(
          padding: const EdgeInsets.only(bottom: AppSpacing.md),
          child: GlassCard(
            accentColor: AppColors.statusAmber,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Container(
                      width: 8,
                      height: 8,
                      decoration: const BoxDecoration(
                        color: AppColors.statusAmber,
                        shape: BoxShape.circle,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      session.hostTag,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    const SizedBox(width: 6),
                    Text('• ${session.agentDisplayName}', style: AppTypography.codeXs),
                    const Spacer(),
                    StatusBadge.working(isCompact: true),
                  ],
                ),
                AppSpacing.gapH8,
                Text(
                  session.displayTitle,
                  style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w600),
                ),
                AppSpacing.gapH6,
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                  decoration: BoxDecoration(
                    color: AppColors.terminalBlack,
                    borderRadius: AppSpacing.roundedSm,
                    border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                  ),
                  child: Row(
                    children: [
                      const Icon(Icons.terminal_rounded, size: 12, color: AppColors.statusAmber),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Text(
                          session.activity,
                          style: AppTypography.codeXs.copyWith(
                            color: AppColors.statusAmberLight,
                            fontSize: 11,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ],
                  ),
                ),
                AppSpacing.gapH8,
                Row(
                  children: [
                    Text(
                      '⏱ Active: ${session.timeElapsedFormatted}',
                      style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                    ),
                    const Spacer(),
                    if (session.gitBranch.isNotEmpty)
                      Text(
                        '🌿 ${session.gitBranch}',
                        style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
                      ),
                  ],
                ),
                AppSpacing.gapH12,
                TokenGaugeBar(
                  percentage: session.contextPct / 100.0,
                  isCompact: true,
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  // --- Sub-View 3: Decision Audit Log History ---

  Widget _buildHistorySubView(List<DecisionAuditEntry> auditList) {
    if (auditList.isEmpty) {
      return Center(
        child: Text(
          'No decision records in history.',
          style: AppTypography.bodyMedium.copyWith(color: AppColors.textMuted),
        ),
      );
    }

    return ListView.builder(
      padding: AppSpacing.paddingScreen,
      itemCount: auditList.length,
      itemBuilder: (context, index) {
        final entry = auditList[index];
        final isApproved = entry.actionType == 'allowed' || entry.actionType == 'approved_plan';
        final isDenied = entry.actionType == 'denied';

        Color badgeColor = AppColors.infoCyan;
        if (isApproved) badgeColor = AppColors.statusEmerald;
        if (isDenied) badgeColor = AppColors.statusCoral;

        return Padding(
          padding: const EdgeInsets.only(bottom: AppSpacing.md),
          child: GlassCard(
            accentColor: badgeColor,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                      decoration: BoxDecoration(
                        color: badgeColor.withOpacity(0.15),
                        borderRadius: AppSpacing.roundedSm,
                        border: Border.all(color: badgeColor.withOpacity(0.4), width: 0.5),
                      ),
                      child: Text(
                        entry.actionType.toUpperCase(),
                        style: AppTypography.codeXs.copyWith(
                          color: badgeColor,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      '${entry.agent} @${entry.host}',
                      style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                    ),
                    const Spacer(),
                    Text(
                      _formatTimestamp(entry.timestamp),
                      style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
                    ),
                  ],
                ),
                AppSpacing.gapH8,
                Text(
                  entry.sessionTitle,
                  style: AppTypography.titleMedium.copyWith(fontWeight: FontWeight.w600),
                ),
                AppSpacing.gapH4,
                Text(
                  entry.summary,
                  style: AppTypography.bodySmall.copyWith(color: AppColors.textSecondary),
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  String _formatTimestamp(DateTime time) {
    final diff = DateTime.now().difference(time);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inHours < 1) return '${diff.inMinutes}m ago';
    if (diff.inDays < 1) return '${diff.inHours}h ago';
    return '${diff.inDays}d ago';
  }

  void _showTranscriptModal(BuildContext context, Session session) {
    final hosts = ref.read(hostsListProvider);
    final match = hosts.where((h) => h.name == session.host || h.url.contains(session.host));
    final hostUrl = match.isNotEmpty ? match.first.url : (hosts.isNotEmpty ? hosts.first.url : 'http://127.0.0.1:7777');

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => DraggableScrollableSheet(
        initialChildSize: 0.75,
        minChildSize: 0.4,
        maxChildSize: 0.95,
        expand: false,
        builder: (context, scrollController) {
          return FutureBuilder<String>(
            future: ref.read(apiClientProvider).getTranscript(hostUrl, session.id),
            builder: (context, snapshot) {
              return Padding(
                padding: AppSpacing.paddingScreen,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Handle & Header
                    Center(
                      child: Container(
                        width: 36,
                        height: 4,
                        decoration: BoxDecoration(
                          color: AppColors.outlineSubtle,
                          borderRadius: BorderRadius.circular(2),
                        ),
                      ),
                    ),
                    AppSpacing.gapH12,
                    Row(
                      children: [
                        const Icon(Icons.article_outlined, size: 20, color: AppColors.infoCyan),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                'SESSION TRANSCRIPT',
                                style: AppTypography.codeSm.copyWith(
                                  color: AppColors.textPrimary,
                                  fontWeight: FontWeight.w700,
                                ),
                              ),
                              Text(
                                '${session.agentDisplayName} • ${session.displayTitle}',
                                style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
                                overflow: TextOverflow.ellipsis,
                              ),
                            ],
                          ),
                        ),
                        if (snapshot.hasData && snapshot.data!.isNotEmpty)
                          IconButton(
                            icon: const Icon(Icons.copy_rounded, size: 18, color: AppColors.textMuted),
                            tooltip: 'Copy transcript',
                            onPressed: () {
                              Clipboard.setData(ClipboardData(text: snapshot.data!));
                              ScaffoldMessenger.of(context).showSnackBar(
                                const SnackBar(
                                  content: Text('Transcript copied to clipboard'),
                                  backgroundColor: AppColors.surfaceHighlight,
                                ),
                              );
                            },
                          ),
                        IconButton(
                          icon: const Icon(Icons.close_rounded, size: 20, color: AppColors.textMuted),
                          onPressed: () => Navigator.of(ctx).pop(),
                        ),
                      ],
                    ),
                    const Divider(color: AppColors.outlineSubtle, height: 20),
                    Expanded(
                      child: snapshot.connectionState == ConnectionState.waiting
                          ? const Center(child: CircularProgressIndicator(color: AppColors.infoCyan))
                          : (snapshot.data == null || snapshot.data!.trim().isEmpty
                              ? Center(
                                  child: Column(
                                    mainAxisSize: MainAxisSize.min,
                                    children: [
                                      const Icon(Icons.receipt_long_outlined, size: 36, color: AppColors.textMuted),
                                      AppSpacing.gapH8,
                                      Text(
                                        'No transcript available yet',
                                        style: AppTypography.bodySmall.copyWith(color: AppColors.textMuted),
                                      ),
                                    ],
                                  ),
                                )
                              : Container(
                                  padding: AppSpacing.paddingCardDense,
                                  decoration: BoxDecoration(
                                    color: AppColors.terminalBlack,
                                    borderRadius: AppSpacing.roundedSm,
                                    border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                                  ),
                                  child: SingleChildScrollView(
                                    controller: scrollController,
                                    child: MarkdownBody(
                                      data: snapshot.data!,
                                      styleSheet: MarkdownStyleSheet(
                                        p: AppTypography.codeXs.copyWith(color: AppColors.textSecondary, fontSize: 11),
                                        code: AppTypography.codeXs.copyWith(color: AppColors.infoCyan, backgroundColor: Colors.transparent),
                                        h1: AppTypography.titleMedium.copyWith(color: AppColors.textPrimary),
                                        h2: AppTypography.titleSmall.copyWith(color: AppColors.textPrimary),
                                        h3: AppTypography.bodyMedium.copyWith(color: AppColors.textPrimary, fontWeight: FontWeight.bold),
                                      ),
                                    ),
                                  ),
                                )),
                    ),
                  ],
                ),
              );
            },
          );
        },
      ),
    );
  }
}
