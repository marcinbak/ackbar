import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/models/headless_models.dart';
import '../../../../core/models/host.dart';
import '../../../../core/models/session.dart';
import '../../../../core/network/api_client.dart';
import '../../../../core/network/headless_api_client.dart';
import '../../../../core/providers/fleet_providers.dart';
import '../../../../core/theme/app_colors.dart';
import '../../../../core/theme/app_typography.dart';
import 'headless_mcp_section.dart';

class HeadlessConfigPillBar extends StatelessWidget {
  final Session session;
  final VoidCallback? onTap;

  const HeadlessConfigPillBar({super.key, required this.session, this.onTap});

  @override
  Widget build(BuildContext context) {
    final isAgy = session.agent.toLowerCase() == 'antigravity';
    final isPlan = session.permissionMode.toLowerCase() == 'plan';
    final model = session.model.isNotEmpty ? session.model : 'model';

    return InkWell(
      key: const ValueKey('headless_config_pill_bar'),
      onTap: onTap ?? () => HeadlessConfigSheet.show(context, session),
      child: Container(
        height: 36,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        decoration: const BoxDecoration(
          color: AppColors.surface,
          border: Border(bottom: BorderSide(color: AppColors.outlineSubtle)),
        ),
        child: SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: Row(
            children: [
              _pill(Icons.tune_rounded, model, AppColors.textSecondary),
              if (!isAgy && session.effort.isNotEmpty) ...[
                const SizedBox(width: 6),
                _pill(
                    Icons.bolt_rounded, session.effort, AppColors.statusAmber),
              ],
              const SizedBox(width: 6),
              _pill(
                isPlan ? Icons.edit_note_rounded : Icons.auto_mode_rounded,
                isPlan ? 'PLAN' : 'AUTO',
                isPlan ? AppColors.statusAmber : AppColors.statusEmerald,
              ),
              if (session.extraDirs.isNotEmpty) ...[
                const SizedBox(width: 6),
                _pill(Icons.folder_open_rounded,
                    '+${session.extraDirs.length} dirs', AppColors.infoCyan),
              ],
              const SizedBox(width: 6),
              _pill(
                Icons.memory_rounded,
                '${session.contextPct}% ctx',
                session.contextPct > 80
                    ? AppColors.statusCoral
                    : AppColors.textMuted,
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _pill(IconData icon, String label, Color color) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: AppColors.surfaceHighlight,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: AppColors.outlineSubtle),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: color),
          const SizedBox(width: 4),
          Text(label, style: AppTypography.codeXs.copyWith(color: color)),
        ],
      ),
    );
  }
}

class HeadlessConfigSheet extends ConsumerStatefulWidget {
  final Session session;
  const HeadlessConfigSheet({super.key, required this.session});

  static Future<void> show(BuildContext context, Session session) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppColors.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => HeadlessConfigSheet(session: session),
    );
  }

  @override
  ConsumerState<HeadlessConfigSheet> createState() =>
      _HeadlessConfigSheetState();
}

class _HeadlessConfigSheetState extends ConsumerState<HeadlessConfigSheet> {
  late Session _session;
  List<ModelSuggestion> _models = const [];
  bool _isBusy = false;
  String? _errorMessage;
  final _dirController = TextEditingController();

  static const _effortTiers = [
    'default',
    'low',
    'medium',
    'high',
    'xhigh',
    'max'
  ];

  @override
  void initState() {
    super.initState();
    _session = widget.session;
    _loadModels();
  }

  @override
  void dispose() {
    _dirController.dispose();
    super.dispose();
  }

  HostRecord _resolveHost() {
    final hosts = ref.read(hostsListProvider);
    return hosts.firstWhere(
      (h) =>
          h.name.toLowerCase() == _session.host.toLowerCase() ||
          h.url.toLowerCase() == _session.host.toLowerCase(),
      orElse: () => HostRecord(
        name: _session.host.isNotEmpty ? _session.host : 'local',
        url: 'http://127.0.0.1:7777',
        createdAt: DateTime.now(),
      ),
    );
  }

  Future<void> _loadModels() async {
    try {
      final host = _resolveHost();
      final list = await ref.read(apiClientProvider).getModelSuggestions(
            host.url,
            _session.id,
            authToken: host.authToken,
          );
      if (mounted) setState(() => _models = list);
    } catch (_) {}
  }

  Future<void> _updateConfig({
    String? model,
    String? effort,
    String? permissionMode,
    List<String>? addDirs,
    List<String>? removeDirs,
  }) async {
    setState(() {
      _isBusy = true;
      _errorMessage = null;
    });

    try {
      final host = _resolveHost();
      final updated = await ref.read(apiClientProvider).updateSessionConfig(
            host.url,
            _session.id,
            model: model,
            effort: effort,
            permissionMode: permissionMode,
            addDirs: addDirs,
            removeDirs: removeDirs,
            authToken: host.authToken,
          );
      if (!mounted) return;
      setState(() {
        _session = updated;
        _isBusy = false;
      });
      ref.read(fleetSessionsProvider.notifier).upsertSession(updated);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        _errorMessage = e is ApiHttpException ? e.message : e.toString();
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isAgy = _session.agent.toLowerCase() == 'antigravity';
    final isPlan = _session.permissionMode.toLowerCase() == 'plan';

    return SafeArea(
      child: Container(
        key: const ValueKey('headless_config_sheet'),
        padding: EdgeInsets.only(
          left: 16,
          right: 16,
          top: 16,
          bottom: MediaQuery.of(context).viewInsets.bottom + 16,
        ),
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text('SESSION CONFIGURATION',
                      style: AppTypography.codeXs.copyWith(
                          color: AppColors.textMuted,
                          fontWeight: FontWeight.w600)),
                  IconButton(
                    icon: const Icon(Icons.close_rounded, size: 18),
                    color: AppColors.textSecondary,
                    visualDensity: VisualDensity.compact,
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
              if (_errorMessage != null)
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Text(_errorMessage!,
                      style: AppTypography.codeXs
                          .copyWith(color: AppColors.statusCoral)),
                ),
              Text('Model',
                  style: AppTypography.codeXs
                      .copyWith(color: AppColors.textSecondary)),
              const SizedBox(height: 4),
              DropdownButtonFormField<String>(
                key: const ValueKey('config_model_selector'),
                value: _session.model.isNotEmpty ? _session.model : null,
                hint: Text('Select model',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textMuted)),
                dropdownColor: AppColors.surfaceHighlight,
                style:
                    AppTypography.codeXs.copyWith(color: AppColors.textPrimary),
                isDense: true,
                decoration: InputDecoration(
                  filled: true,
                  fillColor: AppColors.background,
                  contentPadding:
                      const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                  border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(6),
                      borderSide:
                          const BorderSide(color: AppColors.outlineSubtle)),
                ),
                items: [
                  if (_session.model.isNotEmpty &&
                      !_models.any((m) => m.id == _session.model))
                    DropdownMenuItem(
                        value: _session.model,
                        child: Text('${_session.model} (current)')),
                  ..._models.map((m) =>
                      DropdownMenuItem(value: m.id, child: Text(m.label))),
                ],
                onChanged: _isBusy
                    ? null
                    : (v) => v != null && v != _session.model
                        ? _updateConfig(model: v)
                        : null,
              ),
              if (!isAgy) ...[
                const SizedBox(height: 12),
                Text('Effort',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textSecondary)),
                const SizedBox(height: 4),
                Wrap(
                  key: const ValueKey('config_effort_section'),
                  spacing: 6,
                  runSpacing: 6,
                  children: _effortTiers.map((tier) {
                    final isSel =
                        _session.effort.toLowerCase() == tier.toLowerCase();
                    return ChoiceChip(
                      label: Text(tier),
                      selected: isSel,
                      labelStyle: AppTypography.codeXs.copyWith(
                          color: isSel
                              ? AppColors.background
                              : AppColors.textSecondary),
                      selectedColor: AppColors.infoCyan,
                      backgroundColor: AppColors.background,
                      visualDensity: VisualDensity.compact,
                      onSelected: _isBusy
                          ? null
                          : (s) => s ? _updateConfig(effort: tier) : null,
                    );
                  }).toList(),
                ),
              ],
              const SizedBox(height: 12),
              Text('Execution Mode',
                  style: AppTypography.codeXs
                      .copyWith(color: AppColors.textSecondary)),
              const SizedBox(height: 4),
              Row(
                children: [
                  ChoiceChip(
                    key: const ValueKey('config_mode_auto'),
                    label: const Text('AUTO (Bypass Permissions)'),
                    selected: !isPlan,
                    labelStyle: AppTypography.codeXs.copyWith(
                        color: !isPlan
                            ? AppColors.background
                            : AppColors.textSecondary),
                    selectedColor: AppColors.statusEmerald,
                    backgroundColor: AppColors.background,
                    visualDensity: VisualDensity.compact,
                    onSelected: _isBusy
                        ? null
                        : (s) => s
                            ? _updateConfig(permissionMode: 'bypassPermissions')
                            : null,
                  ),
                  const SizedBox(width: 8),
                  ChoiceChip(
                    key: const ValueKey('config_mode_plan'),
                    label: const Text('PLAN (Read Only)'),
                    selected: isPlan,
                    labelStyle: AppTypography.codeXs.copyWith(
                        color: isPlan
                            ? AppColors.background
                            : AppColors.textSecondary),
                    selectedColor: AppColors.statusAmber,
                    backgroundColor: AppColors.background,
                    visualDensity: VisualDensity.compact,
                    onSelected: _isBusy
                        ? null
                        : (s) =>
                            s ? _updateConfig(permissionMode: 'plan') : null,
                  ),
                ],
              ),
              const SizedBox(height: 12),
              Text('Extra Directories',
                  style: AppTypography.codeXs
                      .copyWith(color: AppColors.textSecondary)),
              const SizedBox(height: 4),
              if (_session.extraDirs.isNotEmpty)
                Wrap(
                  spacing: 6,
                  runSpacing: 6,
                  children: _session.extraDirs.map((dir) {
                    return Chip(
                      label: Text(dir, style: AppTypography.codeXs),
                      deleteIcon: const Icon(Icons.close_rounded, size: 12),
                      visualDensity: VisualDensity.compact,
                      backgroundColor: AppColors.background,
                      onDeleted: _isBusy
                          ? null
                          : () => _updateConfig(removeDirs: [dir]),
                    );
                  }).toList(),
                ),
              const SizedBox(height: 6),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      key: const ValueKey('config_extra_dir_input'),
                      controller: _dirController,
                      style: AppTypography.codeXs
                          .copyWith(color: AppColors.textPrimary),
                      decoration: InputDecoration(
                        hintText: 'Add path (e.g. /tmp/shared)',
                        hintStyle: AppTypography.codeXs
                            .copyWith(color: AppColors.textMuted),
                        isDense: true,
                        contentPadding: const EdgeInsets.all(8),
                        filled: true,
                        fillColor: AppColors.background,
                        border: OutlineInputBorder(
                            borderRadius: BorderRadius.circular(6),
                            borderSide: const BorderSide(
                                color: AppColors.outlineSubtle)),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    key: const ValueKey('config_extra_dir_add_btn'),
                    onPressed: _isBusy
                        ? null
                        : () {
                            final text = _dirController.text.trim();
                            if (text.isNotEmpty) {
                              _dirController.clear();
                              _updateConfig(addDirs: [text]);
                            }
                          },
                    style: FilledButton.styleFrom(
                        backgroundColor: AppColors.infoCyan,
                        foregroundColor: AppColors.background,
                        visualDensity: VisualDensity.compact),
                    child: const Text('Add'),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              _buildContextBar(),
              const SizedBox(height: 12),
              HeadlessMcpSection(
                  key: const ValueKey('config_mcp_section'), session: _session),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildContextBar() {
    final pct = _session.contextPct.clamp(0, 100);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text('Context Usage',
                style: AppTypography.codeXs
                    .copyWith(color: AppColors.textSecondary)),
            Text('$pct%',
                style: AppTypography.codeXs
                    .copyWith(color: AppColors.textPrimary)),
          ],
        ),
        const SizedBox(height: 4),
        ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: LinearProgressIndicator(
            value: pct / 100.0,
            minHeight: 6,
            backgroundColor: AppColors.background,
            valueColor: AlwaysStoppedAnimation<Color>(
                pct > 80 ? AppColors.statusCoral : AppColors.infoCyan),
          ),
        ),
      ],
    );
  }
}
