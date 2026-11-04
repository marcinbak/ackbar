import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../core/models/plan.dart';
import '../core/models/session.dart';
import '../core/providers/fleet_providers.dart';
import '../core/theme/app_colors.dart';
import '../core/widgets/ackbar_bottom_nav.dart';
import 'attention/presentation/attention_screen.dart';
import 'fleet/presentation/fleet_screen.dart';
import 'fleet/presentation/session_detail_sheet.dart';
import 'hosts/presentation/hosts_screen.dart';
import 'plans/presentation/plan_diff_viewer_modal.dart';
import 'plans/presentation/plans_screen.dart';
import 'tasks/presentation/work_board_screen.dart';

/// Root navigation shell managing bottom navigation state and preserved screens via IndexedStack.
class MainNavigationShell extends ConsumerStatefulWidget {
  final String? initialRoute;

  const MainNavigationShell({super.key, this.initialRoute});

  @override
  ConsumerState<MainNavigationShell> createState() =>
      _MainNavigationShellState();
}

class _MainNavigationShellState extends ConsumerState<MainNavigationShell>
    with WidgetsBindingObserver {
  static const List<Widget> _screens = [
    FleetScreen(),
    AttentionScreen(),
    WorkBoardScreen(),
    HostsScreen(),
  ];

  String? _pendingSessionId;
  String? _pendingTaskId;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    if (widget.initialRoute != null &&
        widget.initialRoute!.isNotEmpty &&
        widget.initialRoute != '/') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        _handleRouteString(widget.initialRoute!);
      });
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Future<bool> didPushRouteInformation(RouteInformation routeInformation) {
    final location = routeInformation.uri.toString();
    if (location.isNotEmpty && location != '/') {
      _handleRouteString(location);
      return Future.value(true);
    }
    return super.didPushRouteInformation(routeInformation);
  }

  @override
  Future<bool> didPushRoute(String route) {
    if (route.isNotEmpty && route != '/') {
      _handleRouteString(route);
      return Future.value(true);
    }
    return super.didPushRoute(route);
  }

  void _handleRouteString(String routeStr) {
    final uri = Uri.tryParse(routeStr);
    if (uri != null) {
      _handleDeepLinkUri(uri);
    }
  }

  void _handleDeepLinkUri(Uri uri) {
    String target = uri.host.toLowerCase();
    if (target.isEmpty || target == 'localhost') {
      if (uri.pathSegments.isNotEmpty) {
        target = uri.pathSegments.first.toLowerCase();
      } else {
        target = uri.path.replaceAll('/', '').toLowerCase();
      }
    }

    if (target == 'session') {
      final id = uri.queryParameters['id'] ?? uri.queryParameters['session_id'];
      ref.read(mainTabNavIndexProvider.notifier).state = 0;
      if (id != null && id.isNotEmpty) {
        _openSessionOrQueue(id);
      }
    } else if (target == 'attention') {
      final sessionId =
          uri.queryParameters['session_id'] ?? uri.queryParameters['id'];
      ref.read(mainTabNavIndexProvider.notifier).state = 1;
      if (sessionId != null && sessionId.isNotEmpty) {
        _openSessionOrQueue(sessionId);
      }
    } else if (target == 'task' || target == 'plan') {
      final id = uri.queryParameters['id'] ?? uri.queryParameters['task_id'];
      ref.read(mainTabNavIndexProvider.notifier).state = 2;
      if (id != null && id.isNotEmpty) {
        _openTaskOrQueue(id);
      }
    }
  }

  void _openSessionOrQueue(String sessionId) {
    final sessions = ref.read(fleetSessionsProvider);
    Session? matching;
    for (final s in sessions) {
      if (s.id == sessionId) {
        matching = s;
        break;
      }
    }
    if (matching != null) {
      _pendingSessionId = null;
      SessionDetailSheet.show(context, matching);
    } else {
      _pendingSessionId = sessionId;
    }
  }

  void _openTaskOrQueue(String taskId) {
    final plans = ref.read(plansListProvider);
    PlanDocument? matching;
    for (final p in plans) {
      if (p.id == taskId) {
        matching = p;
        break;
      }
    }
    if (matching != null) {
      _pendingTaskId = null;
      PlanDiffViewerModal.show(context, matching);
    } else {
      _pendingTaskId = taskId;
    }
  }

  @override
  Widget build(BuildContext context) {
    final sessions = ref.watch(fleetSessionsProvider);
    final plans = ref.watch(plansListProvider);

    // Fulfill pending deep links if sessions/plans arrive or update
    if (_pendingSessionId != null) {
      for (final s in sessions) {
        if (s.id == _pendingSessionId) {
          final target = s;
          _pendingSessionId = null;
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (mounted) {
              SessionDetailSheet.show(context, target);
            }
          });
          break;
        }
      }
    }

    if (_pendingTaskId != null) {
      for (final p in plans) {
        if (p.id == _pendingTaskId) {
          final target = p;
          _pendingTaskId = null;
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (mounted) {
              PlanDiffViewerModal.show(context, target);
            }
          });
          break;
        }
      }
    }

    final currentIndex = ref.watch(mainTabNavIndexProvider);
    final attentionCount = ref.watch(attentionBadgeCountProvider);
    final plansCount = ref.watch(pendingPlansCountProvider);
    final hosts = ref.watch(hostsListProvider);
    final hostsCount = hosts.length;
    final hostsOnlineCount = hosts.where((h) => h.online).length;

    return Scaffold(
      backgroundColor: AppColors.background,
      body: IndexedStack(
        index: currentIndex,
        children: _screens,
      ),
      bottomNavigationBar: AckbarBottomNav(
        currentIndex: currentIndex,
        onTap: (index) {
          ref.read(mainTabNavIndexProvider.notifier).state = index;
        },
        attentionCount: attentionCount,
        plansCount: plansCount,
        hostsCount: hostsCount,
        hostsOnlineCount: hostsOnlineCount,
      ),
    );
  }
}
