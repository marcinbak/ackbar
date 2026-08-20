import 'package:flutter/material.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../../core/widgets/glass_card.dart';
import '../../../core/widgets/segmented_filter_tabs.dart';
import '../../../core/widgets/status_badge.dart';
import '../../../core/widgets/token_gauge_bar.dart';

/// Mock session data for fleet preview
class MockAgentSession {
  final String id;
  final String title;
  final String agent;
  final String host;
  final String directory;
  final AckbarSessionStatus status;
  final int usedTokens;
  final int maxTokens;
  final String timeElapsed;

  const MockAgentSession({
    required this.id,
    required this.title,
    required this.agent,
    required this.host,
    required this.directory,
    required this.status,
    required this.usedTokens,
    required this.maxTokens,
    required this.timeElapsed,
  });
}

/// Screen displaying the active fleet of agent sessions across all supervised machines.
class FleetScreen extends StatefulWidget {
  const FleetScreen({super.key});

  @override
  State<FleetScreen> createState() => _FleetScreenState();
}

class _FleetScreenState extends State<FleetScreen> {
  int _selectedFilterIndex = 0;

  final List<FilterTabItem> _filterTabs = const [
    FilterTabItem(label: 'All Sessions', count: 4),
    FilterTabItem(label: 'Working', count: 2),
    FilterTabItem(
      label: 'Attention',
      count: 1,
      countBadgeColor: Color(0xFFEF4444),
      countTextColor: Colors.white,
    ),
    FilterTabItem(label: 'Idle', count: 1),
  ];

  final List<MockAgentSession> _sessions = const [
    MockAgentSession(
      id: 'sess-8492',
      title: 'Scaffold mobile Flutter UI shell & theme tokens',
      agent: 'Claude Code',
      host: 'local',
      directory: '~/Work/Ackbar',
      status: AckbarSessionStatus.working,
      usedTokens: 142500,
      maxTokens: 200000,
      timeElapsed: '12m 45s',
    ),
    MockAgentSession(
      id: 'sess-8491',
      title: 'Implement daemon SSE event hub & SQLite migrations',
      agent: 'Antigravity',
      host: 'gpu-box',
      directory: '~/Work/Ackbar/cmd/ackbard',
      status: AckbarSessionStatus.blocked,
      usedTokens: 188000,
      maxTokens: 200000,
      timeElapsed: '44m 10s',
    ),
    MockAgentSession(
      id: 'sess-8488',
      title: 'Refactor tmux process supervisor pty wrapper',
      agent: 'OpenAI Codex',
      host: 'local',
      directory: '~/Work/Ackbar/internal/tmux',
      status: AckbarSessionStatus.idle,
      usedTokens: 45000,
      maxTokens: 128000,
      timeElapsed: '1h 05m',
    ),
    MockAgentSession(
      id: 'sess-8472',
      title: 'Voice companion audio briefing & Piper TTS engine',
      agent: 'Claude Code',
      host: 'gpu-box',
      directory: '~/Work/Ackbar/docs',
      status: AckbarSessionStatus.working,
      usedTokens: 92000,
      maxTokens: 200000,
      timeElapsed: '6m 12s',
    ),
  ];

  List<MockAgentSession> get _filteredSessions {
    switch (_selectedFilterIndex) {
      case 1:
        return _sessions.where((s) => s.status == AckbarSessionStatus.working).toList();
      case 2:
        return _sessions.where((s) => s.status == AckbarSessionStatus.blocked).toList();
      case 3:
        return _sessions.where((s) => s.status == AckbarSessionStatus.idle).toList();
      default:
        return _sessions;
    }
  }

  @override
  Widget build(BuildContext context) {
    final filtered = _filteredSessions;

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: const AckbarAppBar(),
      body: CustomScrollView(
        slivers: [
          // Filter Tabs Header
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.only(top: AppSpacing.md, bottom: AppSpacing.sm),
              child: SegmentedFilterTabs(
                tabs: _filterTabs,
                selectedIndex: _selectedFilterIndex,
                onTabSelected: (index) {
                  setState(() => _selectedFilterIndex = index);
                },
              ),
            ),
          ),

          // Session Counter & Overview Row
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.lg,
                vertical: AppSpacing.sm,
              ),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text(
                    'ACTIVE SESSIONS (${filtered.length})',
                    style: AppTypography.codeXs.copyWith(
                      letterSpacing: 0.8,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  Row(
                    children: [
                      Icon(Icons.tune_rounded, size: 14, color: AppColors.textMuted),
                      const SizedBox(width: 4),
                      Text('SORT: RECENT', style: AppTypography.codeXs),
                    ],
                  ),
                ],
              ),
            ),
          ),

          // Sessions List
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg, vertical: AppSpacing.xs),
            sliver: SliverList(
              delegate: SliverChildBuilderDelegate(
                (context, index) {
                  final session = filtered[index];
                  return Padding(
                    padding: const EdgeInsets.only(bottom: AppSpacing.md),
                    child: _buildSessionCard(session),
                  );
                },
                childCount: filtered.length,
              ),
            ),
          ),

          const SliverToBoxAdapter(
            child: SizedBox(height: AppSpacing.xxl),
          ),
        ],
      ),
    );
  }

  Widget _buildSessionCard(MockAgentSession session) {
    Color accent;
    switch (session.status) {
      case AckbarSessionStatus.working:
        accent = AppColors.statusAmber;
        break;
      case AckbarSessionStatus.blocked:
        accent = AppColors.statusCoral;
        break;
      case AckbarSessionStatus.idle:
      case AckbarSessionStatus.active:
        accent = AppColors.statusEmerald;
        break;
      case AckbarSessionStatus.offline:
        accent = AppColors.statusOffline;
        break;
    }

    return GlassCard(
      accentColor: accent,
      onTap: () {
        // Open session detail
      },
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Top metadata row
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              // Host badge
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: AppSpacing.roundedSm,
                  border: Border.all(color: AppColors.outlineSubtle, width: 0.5),
                ),
                child: Text(
                  session.host,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.infoCyan,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const SizedBox(width: 6),
              Text(
                '•  ${session.agent}',
                style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
              ),
              const Spacer(),
              StatusBadge(status: session.status, isCompact: true),
            ],
          ),
          AppSpacing.gapH8,

          // Session title
          Text(
            session.title,
            style: AppTypography.titleMedium.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w600,
            ),
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
          ),
          AppSpacing.gapH4,

          // Directory path & runtime
          Row(
            children: [
              Icon(Icons.folder_outlined, size: 12, color: AppColors.textMuted),
              const SizedBox(width: 4),
              Expanded(
                child: Text(
                  session.directory,
                  style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                '⏱ ${session.timeElapsed}',
                style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
              ),
            ],
          ),
          AppSpacing.gapH12,

          // Token gauge bar
          TokenGaugeBar(
            usedTokens: session.usedTokens,
            maxTokens: session.maxTokens,
            isCompact: true,
          ),
        ],
      ),
    );
  }
}
