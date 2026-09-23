import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/host.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/agent_logo.dart';

/// Modal bottom sheet for configuring and spawning a new agent session on a supervised host.
class NewSessionSheet extends ConsumerStatefulWidget {
  final String? prefillCwd;
  final String? prefillFolder;
  final String? prefillHost;

  const NewSessionSheet({
    super.key,
    this.prefillCwd,
    this.prefillFolder,
    this.prefillHost,
  });

  static Future<void> show(
    BuildContext context, {
    String? prefillCwd,
    String? prefillFolder,
    String? prefillHost,
  }) {
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (context) => NewSessionSheet(
        prefillCwd: prefillCwd,
        prefillFolder: prefillFolder,
        prefillHost: prefillHost,
      ),
    );
  }

  @override
  ConsumerState<NewSessionSheet> createState() => _NewSessionSheetState();
}

class _NewSessionSheetState extends ConsumerState<NewSessionSheet> {
  final _formKey = GlobalKey<FormState>();
  late final TextEditingController _cwdController;
  late final TextEditingController _titleController;
  late final TextEditingController _promptController;
  late final TextEditingController _groupController;

  String _selectedAgent = 'claude-code';
  String _selectedHost = 'local';
  String _engineType = 'tmux';
  bool _showAdvanced = false;
  bool _isLaunching = false;

  final List<Map<String, String>> _agents = const [
    {
      'id': 'claude-code',
      'name': 'Claude Code',
      'desc': 'Anthropic Opus / Sonnet'
    },
    {
      'id': 'antigravity',
      'name': 'Antigravity',
      'desc': 'Google Gemini Coding Agent'
    },
    {'id': 'codex', 'name': 'Codex', 'desc': 'OpenAI Codex Engine'},
  ];

  @override
  void initState() {
    super.initState();
    _cwdController = TextEditingController(text: widget.prefillCwd ?? '');
    _titleController = TextEditingController();
    _promptController = TextEditingController();
    _groupController = TextEditingController(text: widget.prefillFolder ?? '');

    if (widget.prefillHost != null && widget.prefillHost!.isNotEmpty) {
      _selectedHost = widget.prefillHost!;
    }
  }

  @override
  void dispose() {
    _cwdController.dispose();
    _titleController.dispose();
    _promptController.dispose();
    _groupController.dispose();
    super.dispose();
  }

  void _onLaunch() async {
    if (!_formKey.currentState!.validate()) return;

    final cwd = _cwdController.text.trim();
    final title = _titleController.text.trim();
    final prompt = _promptController.text.trim();
    final group = _groupController.text.trim();

    final messenger = ScaffoldMessenger.of(context);
    setState(() => _isLaunching = true);

    final notifier = ref.read(fleetSessionsProvider.notifier);
    final result = await notifier.spawnSession(
      hostName: _selectedHost,
      agent: _selectedAgent,
      cwd: cwd,
      name: title.isNotEmpty ? title : null,
      prompt: prompt.isNotEmpty ? prompt : null,
      nodePath: group.isNotEmpty ? group : null,
      engineType: _engineType,
    );

    if (mounted) {
      setState(() => _isLaunching = false);
      if (result != null) {
        Navigator.of(context).pop();
      }
    }

    if (result != null) {
      messenger.showSnackBar(
        SnackBar(
          content: Text('🚀 Session spawned on $_selectedHost'),
          backgroundColor: AppColors.statusEmerald,
          behavior: SnackBarBehavior.floating,
        ),
      );
    } else {
      messenger.showSnackBar(
        SnackBar(
          content: Text(
              'Failed to spawn session on $_selectedHost. Check connection.'),
          backgroundColor: AppColors.statusCoral,
          behavior: SnackBarBehavior.floating,
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final hosts = ref.watch(hostsListProvider);
    final knownPaths = ref.watch(knownProjectPathsProvider);

    // Ensure selected host is valid
    if (hosts.isNotEmpty && !hosts.any((h) => h.name == _selectedHost)) {
      _selectedHost = hosts.first.name;
    }

    return Container(
      decoration: const BoxDecoration(
        color: AppColors.background,
        borderRadius:
            BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusXl)),
        border: Border(
          top: BorderSide(color: AppColors.outline, width: 1.5),
        ),
      ),
      padding: EdgeInsets.only(
        left: AppSpacing.lg,
        right: AppSpacing.lg,
        top: AppSpacing.md,
        bottom: MediaQuery.of(context).viewInsets.bottom + AppSpacing.xl,
      ),
      child: SingleChildScrollView(
        child: Form(
          key: _formKey,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Drag Handle
              Center(
                child: Container(
                  width: 36,
                  height: 4,
                  decoration: BoxDecoration(
                    color: AppColors.outline,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              AppSpacing.gapH12,

              // Header Row
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.all(6),
                        decoration: BoxDecoration(
                          color: AppColors.infoCyan.withOpacity(0.12),
                          borderRadius: AppSpacing.roundedSm,
                          border: Border.all(
                              color: AppColors.infoCyan.withOpacity(0.3),
                              width: 1),
                        ),
                        child: const Icon(Icons.rocket_launch_rounded,
                            size: 16, color: AppColors.infoCyan),
                      ),
                      const SizedBox(width: 8),
                      Text(
                        'LAUNCH AGENT SESSION',
                        style: AppTypography.codeSm.copyWith(
                          color: AppColors.textPrimary,
                          fontWeight: FontWeight.w700,
                          letterSpacing: 0.8,
                        ),
                      ),
                    ],
                  ),
                  IconButton(
                    icon: const Icon(Icons.close_rounded,
                        size: 20, color: AppColors.textMuted),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ],
              ),
              AppSpacing.gapH16,

              // Target Host Row (if multiple hosts or show host badge)
              if (hosts.length > 1) ...[
                Text('TARGET HOST',
                    style: AppTypography.codeXs.copyWith(
                        color: AppColors.textSecondary,
                        fontWeight: FontWeight.w700)),
                const SizedBox(height: 6),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  decoration: BoxDecoration(
                    color: AppColors.surface,
                    borderRadius: AppSpacing.roundedMd,
                    border:
                        Border.all(color: AppColors.outlineSubtle, width: 1),
                  ),
                  child: DropdownButtonHideUnderline(
                    child: DropdownButton<String>(
                      value: _selectedHost,
                      isExpanded: true,
                      dropdownColor: AppColors.surface,
                      items: hosts.map((h) {
                        return DropdownMenuItem<String>(
                          value: h.name,
                          child: Row(
                            children: [
                              Container(
                                width: 8,
                                height: 8,
                                decoration: BoxDecoration(
                                  color: h.online
                                      ? AppColors.statusEmerald
                                      : AppColors.statusCoral,
                                  shape: BoxShape.circle,
                                ),
                              ),
                              const SizedBox(width: 8),
                              Text(h.name,
                                  style: AppTypography.bodySmall
                                      .copyWith(color: AppColors.textPrimary)),
                              if (h.tailscaleIp.isNotEmpty) ...[
                                const SizedBox(width: 6),
                                Text('(${h.tailscaleIp})',
                                    style: AppTypography.codeXs.copyWith(
                                        color: AppColors.textMuted,
                                        fontSize: 10)),
                              ],
                            ],
                          ),
                        );
                      }).toList(),
                      onChanged: (val) {
                        if (val != null) setState(() => _selectedHost = val);
                      },
                    ),
                  ),
                ),
                AppSpacing.gapH16,
              ] else if (hosts.length == 1) ...[
                Row(
                  children: [
                    Text('TARGET HOST:',
                        style: AppTypography.codeXs.copyWith(
                            color: AppColors.textSecondary,
                            fontWeight: FontWeight.w700)),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 2),
                      decoration: BoxDecoration(
                        color: AppColors.surface,
                        borderRadius: AppSpacing.roundedSm,
                        border: Border.all(
                            color: AppColors.outlineSubtle, width: 0.8),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Container(
                            width: 6,
                            height: 6,
                            decoration: BoxDecoration(
                              color: hosts.first.online
                                  ? AppColors.statusEmerald
                                  : AppColors.statusCoral,
                              shape: BoxShape.circle,
                            ),
                          ),
                          const SizedBox(width: 5),
                          Text(
                            hosts.first.name,
                            style: AppTypography.codeXs.copyWith(
                              color: AppColors.textPrimary,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
                AppSpacing.gapH16,
              ],

              // Agent Selection
              Text('SELECT AGENT',
                  style: AppTypography.codeXs.copyWith(
                      color: AppColors.textSecondary,
                      fontWeight: FontWeight.w700)),
              const SizedBox(height: 6),
              Row(
                children: _agents.map((ag) {
                  final isSelected = _selectedAgent == ag['id'];
                  return Expanded(
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 3.0),
                      child: InkWell(
                        onTap: () => setState(() => _selectedAgent = ag['id']!),
                        borderRadius: AppSpacing.roundedMd,
                        child: AnimatedContainer(
                          duration: const Duration(milliseconds: 150),
                          padding: const EdgeInsets.symmetric(
                              vertical: 10, horizontal: 8),
                          decoration: BoxDecoration(
                            color: isSelected
                                ? AppColors.infoCyan.withOpacity(0.12)
                                : AppColors.surface,
                            borderRadius: AppSpacing.roundedMd,
                            border: Border.all(
                              color: isSelected
                                  ? AppColors.infoCyan
                                  : AppColors.outlineSubtle,
                              width: isSelected ? 1.5 : 1,
                            ),
                          ),
                          child: Column(
                            children: [
                              AgentLogo(agent: ag['id']!, size: 20),
                              const SizedBox(height: 6),
                              Text(
                                ag['name']!,
                                style: AppTypography.codeXs.copyWith(
                                  fontSize: 10.5,
                                  color: isSelected
                                      ? AppColors.infoCyan
                                      : AppColors.textPrimary,
                                  fontWeight: isSelected
                                      ? FontWeight.w700
                                      : FontWeight.w500,
                                ),
                                textAlign: TextAlign.center,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  );
                }).toList(),
              ),
              AppSpacing.gapH16,

              // Working Directory Input
              Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text('WORKING DIRECTORY (REQUIRED)',
                      style: AppTypography.codeXs.copyWith(
                          color: AppColors.textSecondary,
                          fontWeight: FontWeight.w700)),
                  if (_cwdController.text.isNotEmpty)
                    InkWell(
                      onTap: () => setState(() => _cwdController.clear()),
                      child: Text('CLEAR',
                          style: AppTypography.codeXs.copyWith(
                              color: AppColors.infoCyan, fontSize: 10)),
                    ),
                ],
              ),
              const SizedBox(height: 6),
              TextFormField(
                controller: _cwdController,
                style: AppTypography.bodySmall.copyWith(
                    color: AppColors.textPrimary, fontFamily: 'monospace'),
                validator: (val) {
                  if (val == null || val.trim().isEmpty) {
                    return 'Working directory is required';
                  }
                  return null;
                },
                decoration: InputDecoration(
                  hintText: 'e.g. ~/Work/Ackbar or /Users/dev/project',
                  hintStyle: AppTypography.bodySmall
                      .copyWith(color: AppColors.textMuted),
                  isDense: true,
                  prefixIcon: const Icon(Icons.folder_open_rounded,
                      size: 18, color: AppColors.infoCyan),
                  suffixIcon: _cwdController.text.isNotEmpty
                      ? IconButton(
                          icon: const Icon(Icons.clear_rounded,
                              size: 16, color: AppColors.textMuted),
                          onPressed: () =>
                              setState(() => _cwdController.clear()),
                        )
                      : null,
                ),
              ),

              // Recent project path suggestions
              if (knownPaths.isNotEmpty) ...[
                const SizedBox(height: 8),
                SizedBox(
                  height: 28,
                  child: ListView.separated(
                    scrollDirection: Axis.horizontal,
                    itemCount: knownPaths.length,
                    separatorBuilder: (_, __) => const SizedBox(width: 6),
                    itemBuilder: (context, idx) {
                      final path = knownPaths[idx];
                      final basename = path
                              .split('/')
                              .where((p) => p.isNotEmpty)
                              .lastOrNull ??
                          path;
                      return ActionChip(
                        label: Text(basename,
                            style: AppTypography.codeXs.copyWith(
                                fontSize: 10, color: AppColors.textSecondary)),
                        backgroundColor: AppColors.surfaceHighlight,
                        side: const BorderSide(
                            color: AppColors.outlineSubtle, width: 0.8),
                        padding: const EdgeInsets.symmetric(horizontal: 4),
                        onPressed: () {
                          setState(() {
                            _cwdController.text = path;
                          });
                        },
                      );
                    },
                  ),
                ),
              ],
              AppSpacing.gapH16,

              // Engine Type Toggle (Tmux vs Headless)
              Text('EXECUTION ENGINE',
                  style: AppTypography.codeXs.copyWith(
                      color: AppColors.textSecondary,
                      fontWeight: FontWeight.w700)),
              const SizedBox(height: 6),
              Row(
                children: [
                  Expanded(
                    child: InkWell(
                      onTap: () => setState(() => _engineType = 'tmux'),
                      borderRadius: AppSpacing.roundedMd,
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                            vertical: 8, horizontal: 10),
                        decoration: BoxDecoration(
                          color: _engineType == 'tmux'
                              ? AppColors.infoCyan.withOpacity(0.12)
                              : AppColors.surface,
                          borderRadius: AppSpacing.roundedMd,
                          border: Border.all(
                            color: _engineType == 'tmux'
                                ? AppColors.infoCyan
                                : AppColors.outlineSubtle,
                            width: _engineType == 'tmux' ? 1.5 : 1,
                          ),
                        ),
                        child: Row(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Icon(Icons.terminal_rounded,
                                size: 16,
                                color: _engineType == 'tmux'
                                    ? AppColors.infoCyan
                                    : AppColors.textMuted),
                            const SizedBox(width: 6),
                            Text(
                              'TMUX (Live PTY)',
                              style: AppTypography.codeXs.copyWith(
                                color: _engineType == 'tmux'
                                    ? AppColors.infoCyan
                                    : AppColors.textPrimary,
                                fontWeight: _engineType == 'tmux'
                                    ? FontWeight.w700
                                    : FontWeight.w500,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: InkWell(
                      onTap: () => setState(() => _engineType = 'headless'),
                      borderRadius: AppSpacing.roundedMd,
                      child: Container(
                        padding: const EdgeInsets.symmetric(
                            vertical: 8, horizontal: 10),
                        decoration: BoxDecoration(
                          color: _engineType == 'headless'
                              ? AppColors.infoCyan.withOpacity(0.12)
                              : AppColors.surface,
                          borderRadius: AppSpacing.roundedMd,
                          border: Border.all(
                            color: _engineType == 'headless'
                                ? AppColors.infoCyan
                                : AppColors.outlineSubtle,
                            width: _engineType == 'headless' ? 1.5 : 1,
                          ),
                        ),
                        child: Row(
                          mainAxisAlignment: MainAxisAlignment.center,
                          children: [
                            Icon(Icons.chat_bubble_outline_rounded,
                                size: 16,
                                color: _engineType == 'headless'
                                    ? AppColors.infoCyan
                                    : AppColors.textMuted),
                            const SizedBox(width: 6),
                            Text(
                              'HEADLESS (Chat)',
                              style: AppTypography.codeXs.copyWith(
                                color: _engineType == 'headless'
                                    ? AppColors.infoCyan
                                    : AppColors.textPrimary,
                                fontWeight: _engineType == 'headless'
                                    ? FontWeight.w700
                                    : FontWeight.w500,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ],
              ),
              AppSpacing.gapH12,

              // Advanced / Optional toggle
              InkWell(
                onTap: () => setState(() => _showAdvanced = !_showAdvanced),
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 4.0),
                  child: Row(
                    children: [
                      Icon(
                        _showAdvanced
                            ? Icons.expand_less_rounded
                            : Icons.expand_more_rounded,
                        size: 18,
                        color: AppColors.textMuted,
                      ),
                      const SizedBox(width: 4),
                      Text(
                        _showAdvanced
                            ? 'HIDE OPTIONS'
                            : 'OPTIONAL CONFIGURATION (Title, Prompt, Group)',
                        style: AppTypography.codeXs
                            .copyWith(color: AppColors.textMuted, fontSize: 10),
                      ),
                    ],
                  ),
                ),
              ),

              if (_showAdvanced) ...[
                AppSpacing.gapH12,
                // Session Custom Title
                Text('SESSION TITLE (OPTIONAL)',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textSecondary)),
                const SizedBox(height: 4),
                TextFormField(
                  controller: _titleController,
                  style: AppTypography.bodySmall,
                  decoration: const InputDecoration(
                    hintText: 'e.g. Refactor auth module',
                    isDense: true,
                  ),
                ),
                AppSpacing.gapH12,

                // Initial Prompt
                Text('INITIAL PROMPT (OPTIONAL)',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textSecondary)),
                const SizedBox(height: 4),
                TextFormField(
                  controller: _promptController,
                  maxLines: 2,
                  style: AppTypography.bodySmall,
                  decoration: const InputDecoration(
                    hintText: 'e.g. Inspect test failures and fix imports',
                    isDense: true,
                  ),
                ),
                AppSpacing.gapH12,

                // Group / Category
                Text('PROJECT GROUP (OPTIONAL)',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textSecondary)),
                const SizedBox(height: 4),
                TextFormField(
                  controller: _groupController,
                  style: AppTypography.bodySmall,
                  decoration: const InputDecoration(
                    hintText: 'e.g. Ackbar / Mobile',
                    isDense: true,
                  ),
                ),
              ],
              AppSpacing.gapH20,

              // Launch Submit Button
              ElevatedButton(
                onPressed: _isLaunching ? null : _onLaunch,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppColors.infoCyan,
                  foregroundColor: AppColors.terminalBlack,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                  shape: RoundedRectangleBorder(
                      borderRadius: AppSpacing.roundedMd),
                ),
                child: _isLaunching
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: AppColors.terminalBlack),
                      )
                    : Row(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          const Icon(Icons.bolt_rounded, size: 18),
                          const SizedBox(width: 6),
                          Text(
                            'LAUNCH SESSION',
                            style: AppTypography.codeSm.copyWith(
                              fontWeight: FontWeight.w800,
                              color: AppColors.terminalBlack,
                              letterSpacing: 0.8,
                            ),
                          ),
                        ],
                      ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
