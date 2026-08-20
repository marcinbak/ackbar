import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../core/providers/fleet_providers.dart';
import '../core/widgets/ackbar_bottom_nav.dart';
import 'attention/presentation/attention_screen.dart';
import 'fleet/presentation/fleet_screen.dart';
import 'hosts/presentation/hosts_screen.dart';
import 'plans/presentation/plans_screen.dart';

/// Root navigation shell managing bottom navigation state and preserved screens via IndexedStack.
class MainNavigationShell extends ConsumerWidget {
  const MainNavigationShell({super.key});

  static const List<Widget> _screens = [
    FleetScreen(),
    AttentionScreen(),
    PlansScreen(),
    HostsScreen(),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final currentIndex = ref.watch(mainTabNavIndexProvider);
    final attentionCount = ref.watch(attentionBadgeCountProvider);
    final plansCount = ref.watch(pendingPlansCountProvider);

    return Scaffold(
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
      ),
    );
  }
}
