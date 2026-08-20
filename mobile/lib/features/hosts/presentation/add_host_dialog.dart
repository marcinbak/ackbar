import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/host.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';

/// Modal dialog for registering a new remote host or Tailscale node
class AddHostDialog extends ConsumerStatefulWidget {
  const AddHostDialog({super.key});

  static Future<void> show(BuildContext context) {
    return showDialog(
      context: context,
      builder: (context) => const AddHostDialog(),
    );
  }

  @override
  ConsumerState<AddHostDialog> createState() => _AddHostDialogState();
}

class _AddHostDialogState extends ConsumerState<AddHostDialog> {
  final _formKey = GlobalKey<FormState>();
  final _nameController = TextEditingController();
  final _urlController = TextEditingController(text: 'http://127.0.0.1:7778');
  final _sshTargetController = TextEditingController();
  final _remoteCwdController = TextEditingController(text: '~/Work');
  final _tailscaleIpController = TextEditingController();
  bool _isTesting = false;

  @override
  void dispose() {
    _nameController.dispose();
    _urlController.dispose();
    _sshTargetController.dispose();
    _remoteCwdController.dispose();
    _tailscaleIpController.dispose();
    super.dispose();
  }

  void _onSave() async {
    if (!_formKey.currentState!.validate()) return;

    setState(() => _isTesting = true);

    final api = ref.read(apiClientProvider);
    final health = await api.checkHostHealth(_urlController.text.trim());

    final newHost = HostRecord(
      name: _nameController.text.trim(),
      url: _urlController.text.trim(),
      sshTarget: _sshTargetController.text.trim(),
      remoteCwd: _remoteCwdController.text.trim(),
      tailscaleIp: _tailscaleIpController.text.trim(),
      online: health != null,
      latencyMs: health != null ? (health['latency_ms'] as int? ?? 12) : 999,
      version: health != null ? (health['version'] as String? ?? 'v0.2.1') : 'unknown',
      uptime: '1m',
      sessionsCount: 0,
      createdAt: DateTime.now(),
    );

    ref.read(hostsListProvider.notifier).addHost(newHost);

    if (mounted) {
      setState(() => _isTesting = false);
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('Host ${newHost.name} added (${newHost.online ? "Online 🟢" : "Unreachable 🔴"})'),
          backgroundColor: newHost.online ? AppColors.statusEmerald : AppColors.statusCoral,
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      backgroundColor: AppColors.surface,
      shape: RoundedRectangleBorder(
        borderRadius: AppSpacing.roundedLg,
        side: const BorderSide(color: AppColors.outline, width: 1),
      ),
      title: Row(
        children: [
          const Icon(Icons.dns_rounded, size: 20, color: AppColors.infoCyan),
          const SizedBox(width: 8),
          Text(
            'REGISTER NEW HOST',
            style: AppTypography.codeSm.copyWith(
              color: AppColors.textPrimary,
              fontWeight: FontWeight.w700,
            ),
          ),
        ],
      ),
      content: SingleChildScrollView(
        child: Form(
          key: _formKey,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Host Name / Alias (e.g. devbox-2):', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _nameController,
                style: AppTypography.bodySmall,
                validator: (val) => val == null || val.trim().isEmpty ? 'Name required' : null,
                decoration: const InputDecoration(
                  hintText: 'devbox-2',
                  isDense: true,
                ),
              ),
              AppSpacing.gapH12,

              Text('Daemon Endpoint URL:', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _urlController,
                style: AppTypography.bodySmall,
                validator: (val) => val == null || val.trim().isEmpty ? 'URL required' : null,
                decoration: const InputDecoration(
                  hintText: 'http://127.0.0.1:7778',
                  isDense: true,
                ),
              ),
              AppSpacing.gapH12,

              Text('Tailscale Mesh IP (Optional):', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _tailscaleIpController,
                style: AppTypography.bodySmall,
                decoration: const InputDecoration(
                  hintText: '100.95.42.205',
                  isDense: true,
                ),
              ),
              AppSpacing.gapH12,

              Text('SSH Target (Optional):', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _sshTargetController,
                style: AppTypography.bodySmall,
                decoration: const InputDecoration(
                  hintText: 'dev@devbox.internal',
                  isDense: true,
                ),
              ),
              AppSpacing.gapH12,

              Text('Remote Working Directory Root:', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _remoteCwdController,
                style: AppTypography.bodySmall,
                decoration: const InputDecoration(
                  hintText: '~/Work',
                  isDense: true,
                ),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text('Cancel', style: AppTypography.titleSmall.copyWith(color: AppColors.textMuted)),
        ),
        ElevatedButton(
          onPressed: _isTesting ? null : _onSave,
          style: ElevatedButton.styleFrom(
            backgroundColor: AppColors.infoCyan,
            foregroundColor: AppColors.terminalBlack,
          ),
          child: _isTesting
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                )
              : const Text('Connect & Add'),
        ),
      ],
    );
  }
}
