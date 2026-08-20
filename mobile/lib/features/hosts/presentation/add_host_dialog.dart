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
  final _hostController = TextEditingController();
  final _aliasController = TextEditingController();
  bool _isTesting = false;

  @override
  void dispose() {
    _hostController.dispose();
    _aliasController.dispose();
    super.dispose();
  }

  String _normalizeUrl(String input) {
    var raw = input.trim();
    if (raw.startsWith('http://')) {
      raw = raw.substring(7);
    } else if (raw.startsWith('https://')) {
      raw = raw.substring(8);
    }
    raw = raw.replaceAll('/', '');
    if (!raw.contains(':')) {
      raw = '$raw:7777';
    }
    return 'http://$raw';
  }

  void _onSave() async {
    if (!_formKey.currentState!.validate()) return;

    final rawHost = _hostController.text.trim();
    final normalizedUrl = _normalizeUrl(rawHost);
    final aliasInput = _aliasController.text.trim();
    final displayName = aliasInput.isNotEmpty ? aliasInput : rawHost;

    setState(() => _isTesting = true);

    final api = ref.read(apiClientProvider);
    final health = await api.checkHostHealth(normalizedUrl);

    final isTailscale = rawHost.startsWith('100.') || rawHost.contains('.ts.net');
    final tailscaleIp = isTailscale ? rawHost.split(':').first : '';

    final newHost = HostRecord(
      name: displayName,
      url: normalizedUrl,
      tailscaleIp: tailscaleIp,
      online: health != null,
      latencyMs: health != null ? (health['latency_ms'] as int? ?? 1) : 999,
      version: health != null ? (health['version'] as String? ?? '') : '',
      uptime: 'Active',
      sessionsCount: 0,
      createdAt: DateTime.now(),
    );

    ref.read(hostsListProvider.notifier).addHost(newHost);

    if (mounted) {
      setState(() => _isTesting = false);
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('Host "$displayName" connected (${newHost.online ? "Online 🟢" : "Unreachable 🔴"})'),
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
            'ADD SUPERVISED HOST',
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
              Text('Host or IP Address (Required):', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _hostController,
                style: AppTypography.bodySmall,
                autofocus: true,
                validator: (val) {
                  if (val == null || val.trim().isEmpty) return 'Host or IP is required';
                  return null;
                },
                decoration: const InputDecoration(
                  hintText: '100.117.71.84 or mac-m3.local',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                'Default port :7777 and http:// protocol are added automatically.',
                style: AppTypography.codeXs.copyWith(color: AppColors.textMuted, fontSize: 10),
              ),
              AppSpacing.gapH16,

              Text('Alias / Name (Optional):', style: AppTypography.codeXs),
              const SizedBox(height: 4),
              TextFormField(
                controller: _aliasController,
                style: AppTypography.bodySmall,
                decoration: const InputDecoration(
                  hintText: 'e.g. Work Mac, Devbox (Optional)',
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
