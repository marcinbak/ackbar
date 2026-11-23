import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/navigation/session_navigator.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/ackbar_app_bar.dart';
import 'fleet_tree_pane.dart';
import 'new_session_sheet.dart';

/// Screen displaying the active fleet of agent sessions across all supervised machines.
class FleetScreen extends ConsumerStatefulWidget {
  const FleetScreen({super.key});

  @override
  ConsumerState<FleetScreen> createState() => _FleetScreenState();
}

class _FleetScreenState extends ConsumerState<FleetScreen> {
  @override
  void initState() {
    super.initState();
    Future.microtask(() {
      ref.read(hostsListProvider.notifier).refreshHosts();
      ref.read(fleetSessionsProvider.notifier).refreshSessions();
      ref.read(fleetNodesProvider.notifier).refreshNodes();
    });
  }

  Future<void> _spawnSupervisor() async {
    final scaffoldMessenger = ScaffoldMessenger.of(context);
    scaffoldMessenger.showSnackBar(
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

  @override
  Widget build(BuildContext context) {
    final showDone = ref.watch(fleetShowDoneProvider);

    return Scaffold(
      backgroundColor: AppColors.background,
      appBar: AckbarAppBar(
        title: 'FLEET CONTROL',
        actions: [
          IconButton(
            key: const Key('fleet_toggle_show_done'),
            visualDensity: VisualDensity.compact,
            icon: Icon(
              showDone
                  ? Icons.task_alt_rounded
                  : Icons.check_circle_outline_rounded,
              size: 20,
              color: showDone ? AppColors.statusEmerald : AppColors.textMuted,
            ),
            tooltip: showDone ? 'Hide Done Sessions' : 'Show Done Sessions',
            onPressed: () => ref.read(fleetShowDoneProvider.notifier).toggle(),
          ),
          IconButton(
            icon: const Text(
              '⚓+',
              style: TextStyle(
                fontSize: 15,
                fontWeight: FontWeight.bold,
                color: AppColors.infoCyan,
              ),
            ),
            tooltip: '1-Tap New Supervisor',
            onPressed: _spawnSupervisor,
          ),
          IconButton(
            icon: const Icon(Icons.add_circle_outline_rounded,
                size: 20, color: AppColors.infoCyan),
            tooltip: 'Launch New Session',
            onPressed: () => NewSessionSheet.show(context),
          ),
        ],
      ),
      body: const FleetTreePane(),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => NewSessionSheet.show(context),
        backgroundColor: AppColors.infoCyan,
        foregroundColor: AppColors.terminalBlack,
        icon: const Icon(Icons.add_rounded, size: 20),
        label: Text(
          'NEW SESSION',
          style: AppTypography.codeXs.copyWith(
            color: AppColors.terminalBlack,
            fontWeight: FontWeight.w800,
            letterSpacing: 0.6,
          ),
        ),
      ),
    );
  }
}
