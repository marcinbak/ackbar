import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../core/widgets/ackbar_bottom_nav.dart';
import 'attention/presentation/attention_screen.dart';
import 'fleet/presentation/fleet_screen.dart';
import 'hosts/presentation/hosts_screen.dart';
import 'plans/presentation/plans_screen.dart';

/// Root navigation shell managing bottom navigation state and preserved screens via IndexedStack.
class MainNavigationShell extends ConsumerStatefulWidget {
  const MainNavigationShell({super.key});

  @override
  ConsumerState<MainNavigationShell> createState() => _MainNavigationShellState();
}

class _MainNavigationShellState extends ConsumerState<MainNavigationShell> {
  int _currentIndex = 0;

  final List<Widget> _screens = const [
    FleetScreen(),
    AttentionScreen(),
    PlansScreen(),
    HostsScreen(),
  ];

  void _onTabSelected(int index) {
    setState(() {
      _currentIndex = index;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: IndexedStack(
        index: _currentIndex,
        children: _screens,
      ),
      bottomNavigationBar: AckbarBottomNav(
        currentIndex: _currentIndex,
        onTap: _onTabSelected,
        attentionCount: 1,
        plansCount: 1,
      ),
    );
  }
}
