import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/providers/fleet_providers.dart';
import '../../../core/providers/tablet_workspace_provider.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import 'new_session_sheet.dart';

class FleetEmptyWorkspace extends ConsumerWidget {
  final VoidCallback? onExpandTree;

  const FleetEmptyWorkspace({super.key, this.onExpandTree});

  Future<void> _spawnSupervisor(BuildContext context, WidgetRef ref) async {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('Spawning zero-input supervisor...'),
        duration: Duration(seconds: 1),
      ),
    );
    final spawned = await ref
        .read(fleetSessionsProvider.notifier)
        .spawnZeroInputSupervisor();
    if (spawned != null) {
      ref.read(selectedTabletSessionProvider.notifier).state = spawned;
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Container(
      key: const Key('tablet_fleet_empty_workspace'),
      color: AppColors.background,
      child: SafeArea(
        left: false,
        child: Stack(
          children: [
            if (onExpandTree != null)
              Positioned(
                top: AppSpacing.sm,
                left: AppSpacing.sm,
                child: IconButton(
                  key: const Key('tablet_toggle_fleet_pane'),
                  icon: const Icon(Icons.menu_open_rounded,
                      color: AppColors.infoCyan),
                  tooltip: 'Show Fleet Tree',
                  onPressed: onExpandTree,
                ),
              ),
            Center(
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 440),
                child: Padding(
                  padding: const EdgeInsets.all(AppSpacing.xxl),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Container(
                        padding: const EdgeInsets.all(18),
                        decoration: BoxDecoration(
                          color: AppColors.infoCyan.withOpacity(0.1),
                          shape: BoxShape.circle,
                          border: Border.all(
                            color: AppColors.infoCyan.withOpacity(0.35),
                            width: 1.2,
                          ),
                        ),
                        child: const Icon(
                          Icons.anchor_rounded,
                          size: 40,
                          color: AppColors.infoCyan,
                        ),
                      ),
                      AppSpacing.gapH16,
                      Text(
                        'Select a Session',
                        style: AppTypography.titleLarge.copyWith(
                          color: AppColors.textPrimary,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      AppSpacing.gapH8,
                      Text(
                        'Pick an active agent session from the Fleet tree, or launch a new session.',
                        textAlign: TextAlign.center,
                        style: AppTypography.bodySmall
                            .copyWith(color: AppColors.textSecondary),
                      ),
                      AppSpacing.gapH24,
                      Wrap(
                        spacing: AppSpacing.md,
                        runSpacing: AppSpacing.md,
                        alignment: WrapAlignment.center,
                        children: [
                          ElevatedButton.icon(
                            onPressed: () => NewSessionSheet.show(context),
                            icon: const Icon(Icons.add_rounded, size: 18),
                            label: const Text('Launch Session'),
                            style: ElevatedButton.styleFrom(
                              backgroundColor: AppColors.infoCyan,
                              foregroundColor: AppColors.terminalBlack,
                            ),
                          ),
                          OutlinedButton.icon(
                            onPressed: () => _spawnSupervisor(context, ref),
                            icon:
                                const Text('⚓', style: TextStyle(fontSize: 14)),
                            label: const Text('New Supervisor'),
                            style: OutlinedButton.styleFrom(
                              foregroundColor: AppColors.infoCyan,
                              side: const BorderSide(color: AppColors.infoCyan),
                            ),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
