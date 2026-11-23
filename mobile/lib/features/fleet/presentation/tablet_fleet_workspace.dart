import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/responsive_breakpoints.dart';
import '../../../core/models/session.dart';
import '../../../core/navigation/session_navigator.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/providers/tablet_workspace_provider.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/utils/session_host_resolver.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import '../../chat/presentation/chat_screen.dart';
import '../../terminal/presentation/terminal_screen.dart';
import 'fleet_empty_workspace.dart';
import 'fleet_tree_pane.dart';
import 'new_session_sheet.dart';

class TabletFleetWorkspace extends ConsumerStatefulWidget {
  const TabletFleetWorkspace({super.key});

  @override
  ConsumerState<TabletFleetWorkspace> createState() =>
      _TabletFleetWorkspaceState();
}

class _TabletFleetWorkspaceState extends ConsumerState<TabletFleetWorkspace> {
  final GlobalKey<ScaffoldState> _scaffoldKey = GlobalKey<ScaffoldState>();
  final Map<String, GlobalObjectKey> _sessionKeys = {};

  // Caching GlobalObjectKey per (session id, engine mode) lets Flutter reparent
  // the live ChatScreen/TerminalScreen State across orientation changes without
  // tearing down streams or WebSockets.
  GlobalObjectKey _keyFor(Session s) => _sessionKeys.putIfAbsent(
        '${s.id}:${s.isHeadless}',
        () => GlobalObjectKey('tablet_active_session_${s.id}_${s.isHeadless}'),
      );

  Future<void> _spawnSupervisor() async {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('Spawning zero-input supervisor...'),
        duration: Duration(seconds: 1),
      ),
    );
    final spawned = await ref
        .read(fleetSessionsProvider.notifier)
        .spawnZeroInputSupervisor();
    if (spawned != null && mounted) {
      await SessionNavigator.openSession(context, ref, spawned);
    }
  }

  void _setCollapsed(bool collapsed) =>
      ref.read(tabletFleetTreeCollapsedProvider.notifier).state = collapsed;

  List<Widget> _fleetActions(bool showDone) => [
        IconButton(
          key: const Key('fleet_toggle_show_done'),
          visualDensity: VisualDensity.compact,
          icon: Icon(
            showDone
                ? Icons.task_alt_rounded
                : Icons.check_circle_outline_rounded,
            size: 19,
            color: showDone ? AppColors.statusEmerald : AppColors.textMuted,
          ),
          tooltip: showDone ? 'Hide Done Sessions' : 'Show Done Sessions',
          onPressed: () => ref.read(fleetShowDoneProvider.notifier).toggle(),
        ),
        IconButton(
          visualDensity: VisualDensity.compact,
          icon: const Text('⚓+',
              style: TextStyle(
                  fontSize: 14,
                  fontWeight: FontWeight.bold,
                  color: AppColors.infoCyan)),
          tooltip: '1-Tap New Supervisor',
          onPressed: _spawnSupervisor,
        ),
        IconButton(
          visualDensity: VisualDensity.compact,
          icon: const Icon(Icons.add_circle_outline_rounded,
              size: 19, color: AppColors.infoCyan),
          tooltip: 'Launch New Session',
          onPressed: () => NewSessionSheet.show(context),
        ),
      ];

  Widget _buildLeftHeader(bool showDone, {required bool showCollapse}) =>
      Container(
        height: 48,
        padding:
            const EdgeInsets.only(left: AppSpacing.lg, right: AppSpacing.xs),
        decoration: const BoxDecoration(
          color: AppColors.surface,
          border: Border(bottom: BorderSide(color: AppColors.outlineSubtle)),
        ),
        child: Row(
          children: [
            Text('FLEET',
                style: AppTypography.codeSm.copyWith(
                    color: AppColors.textPrimary,
                    fontWeight: FontWeight.w800,
                    letterSpacing: 1.0)),
            const Spacer(),
            ..._fleetActions(showDone),
            if (showCollapse)
              IconButton(
                key: const Key('tablet_toggle_fleet_pane'),
                visualDensity: VisualDensity.compact,
                icon: const Icon(Icons.chevron_left_rounded,
                    size: 20, color: AppColors.textSecondary),
                tooltip: 'Collapse Fleet Tree',
                onPressed: () => _setCollapsed(true),
              ),
          ],
        ),
      );

  Widget _buildSessionDetail(
    Session active, {
    IconData? leadingIcon,
    Key? leadingKey,
    VoidCallback? onLeadingAction,
  }) {
    final key = _keyFor(active);
    if (active.isHeadless) {
      return ChatScreen(
        key: key,
        session: active,
        isEmbedded: true,
        leadingIcon: leadingIcon,
        leadingKey: leadingKey,
        onLeadingAction: onLeadingAction,
      );
    }
    return TerminalScreen(
      key: key,
      session: active,
      isEmbedded: true,
      leadingIcon: leadingIcon,
      leadingKey: leadingKey,
      onLeadingAction: onLeadingAction,
    );
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<List<Session>>(fleetSessionsProvider, (prev, next) {
      final sel = ref.read(selectedTabletSessionProvider);
      if (sel == null) return;
      final match = SessionHostResolver.findMatchingSession(next, sel);
      final hadInPrev =
          SessionHostResolver.findMatchingSession(prev, sel) != null;
      if ((match != null && (match.deleted || match.archived)) ||
          (hadInPrev && match == null)) {
        ref.read(selectedTabletSessionProvider.notifier).state = null;
      }
    });

    final selected = ref.watch(selectedTabletSessionProvider);
    final sessions = ref.watch(fleetSessionsProvider);
    final showDone = ref.watch(fleetShowDoneProvider);
    final collapsed = ref.watch(tabletFleetTreeCollapsedProvider);
    final isLandscape = ResponsiveBreakpoints.isLandscapeSplit(context);

    Session? active;
    if (selected != null) {
      final live = SessionHostResolver.resolveLiveSession(sessions, selected);
      if (!live.deleted && !live.archived) {
        active = live;
      }
    }

    if (isLandscape) {
      return Scaffold(
        backgroundColor: AppColors.background,
        resizeToAvoidBottomInset: false,
        body: Row(
          children: [
            if (!collapsed)
              Container(
                key: const Key('tablet_fleet_left_pane'),
                width: 320,
                decoration: const BoxDecoration(
                  color: AppColors.background,
                  border: Border(
                    right: BorderSide(color: AppColors.outlineSubtle),
                  ),
                ),
                child: SafeArea(
                  right: false,
                  bottom: false,
                  child: Column(
                    children: [
                      _buildLeftHeader(showDone, showCollapse: true),
                      Expanded(
                        child: FleetTreePane(selectedSessionId: active?.id),
                      ),
                    ],
                  ),
                ),
              ),
            Expanded(
              child: active == null
                  ? FleetEmptyWorkspace(
                      onExpandTree:
                          collapsed ? () => _setCollapsed(false) : null,
                    )
                  : _buildSessionDetail(
                      active,
                      leadingIcon: collapsed ? Icons.menu_open_rounded : null,
                      leadingKey: collapsed
                          ? const Key('tablet_toggle_fleet_pane')
                          : null,
                      onLeadingAction:
                          collapsed ? () => _setCollapsed(false) : null,
                    ),
            ),
          ],
        ),
      );
    }

    if (active == null) {
      return Scaffold(
        backgroundColor: AppColors.background,
        appBar: AckbarAppBar(
            title: 'FLEET CONTROL', actions: _fleetActions(showDone)),
        body: const FleetTreePane(),
      );
    }

    return Scaffold(
      key: _scaffoldKey,
      backgroundColor: AppColors.background,
      resizeToAvoidBottomInset: false,
      drawer: Drawer(
        key: const Key('tablet_portrait_fleet_drawer'),
        width: 320,
        backgroundColor: AppColors.background,
        child: SafeArea(
          bottom: false,
          child: Column(children: [
            _buildLeftHeader(showDone, showCollapse: false),
            Expanded(
              child: FleetTreePane(
                selectedSessionId: active.id,
                onSessionTap: (s) {
                  _scaffoldKey.currentState?.closeDrawer();
                  SessionNavigator.openSession(context, ref, s);
                },
              ),
            ),
          ]),
        ),
      ),
      body: _buildSessionDetail(
        active,
        leadingIcon: Icons.menu_rounded,
        leadingKey: const Key('tablet_open_fleet_drawer_button'),
        onLeadingAction: () => _scaffoldKey.currentState?.openDrawer(),
      ),
    );
  }
}
