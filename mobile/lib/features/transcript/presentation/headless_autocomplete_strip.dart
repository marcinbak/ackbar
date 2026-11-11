import 'dart:async';
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

enum ComposerTriggerKind { shell, command, file }

class ComposerTrigger {
  final ComposerTriggerKind kind;
  final String query;
  final int replaceStart;
  final int replaceEnd;

  const ComposerTrigger._({
    required this.kind,
    required this.query,
    required this.replaceStart,
    required this.replaceEnd,
  });

  factory ComposerTrigger.shell(String command) {
    return ComposerTrigger._(
      kind: ComposerTriggerKind.shell,
      query: command,
      replaceStart: 0,
      replaceEnd: command.length,
    );
  }

  factory ComposerTrigger.command(String query, int end) {
    return ComposerTrigger._(
      kind: ComposerTriggerKind.command,
      query: query,
      replaceStart: 0,
      replaceEnd: end,
    );
  }

  factory ComposerTrigger.file(String query, int start, int end) {
    return ComposerTrigger._(
      kind: ComposerTriggerKind.file,
      query: query,
      replaceStart: start,
      replaceEnd: end,
    );
  }
}

ComposerTrigger? detectComposerTrigger(String text, int cursorOffset) {
  if (text.isEmpty) return null;
  final safeOffset = cursorOffset.clamp(0, text.length);

  if (text.trimLeft().startsWith('!')) {
    final trimmed = text.trimLeft();
    final cmd = trimmed.length > 1 ? trimmed.substring(1).trimLeft() : '';
    return ComposerTrigger.shell(cmd);
  }

  if (text.startsWith('/')) {
    final firstSpace = text.indexOf(' ');
    if (firstSpace == -1 || safeOffset <= firstSpace) {
      final end = firstSpace == -1 ? text.length : firstSpace;
      final q = text.substring(1, end);
      return ComposerTrigger.command(q, end);
    }
  }

  if (safeOffset > 0) {
    final atIdx = text.lastIndexOf('@', safeOffset - 1);
    if (atIdx != -1 &&
        (atIdx == 0 || text[atIdx - 1] == ' ' || text[atIdx - 1] == '\n')) {
      final segment = text.substring(atIdx + 1, safeOffset);
      if (!segment.contains(' ') && !segment.contains('\n')) {
        final sp = text.indexOf(' ', atIdx);
        final nl = text.indexOf('\n', atIdx);
        var end = (sp != -1 && (nl == -1 || sp < nl)) ? sp : nl;
        if (end == -1) end = text.length;
        final query = text.substring(atIdx + 1, end);
        return ComposerTrigger.file(query, atIdx, end);
      }
    }
  }

  return null;
}

class HeadlessAutocompleteStrip extends ConsumerStatefulWidget {
  final TextEditingController controller;
  final Session session;
  final Future<({String url, String? authToken})> Function()? resolveHost;

  const HeadlessAutocompleteStrip({
    super.key,
    required this.controller,
    required this.session,
    this.resolveHost,
  });

  @override
  ConsumerState<HeadlessAutocompleteStrip> createState() =>
      _HeadlessAutocompleteStripState();
}

class _HeadlessAutocompleteStripState
    extends ConsumerState<HeadlessAutocompleteStrip> {
  Timer? _debounceTimer;
  ComposerTrigger? _currentTrigger;
  List<CommandSuggestion> _commandSuggestions = const [];
  List<FileSuggestion> _fileSuggestions = const [];

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onTextChanged);
    _checkTrigger();
  }

  @override
  void didUpdateWidget(covariant HeadlessAutocompleteStrip oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_onTextChanged);
      widget.controller.addListener(_onTextChanged);
      _checkTrigger();
    }
  }

  @override
  void dispose() {
    _debounceTimer?.cancel();
    widget.controller.removeListener(_onTextChanged);
    super.dispose();
  }

  void _onTextChanged() {
    _checkTrigger();
  }

  void _checkTrigger() {
    final text = widget.controller.text;
    final sel = widget.controller.selection.baseOffset;
    final trig = detectComposerTrigger(text, sel >= 0 ? sel : text.length);

    if (trig?.kind != _currentTrigger?.kind ||
        trig?.query != _currentTrigger?.query) {
      _currentTrigger = trig;
      _debounceTimer?.cancel();
      if (trig == null || trig.kind == ComposerTriggerKind.shell) {
        setState(() {
          _commandSuggestions = const [];
          _fileSuggestions = const [];
        });
        return;
      }

      _debounceTimer = Timer(const Duration(milliseconds: 120), () {
        _fetchSuggestions(trig);
      });
    }
  }

  Future<({String url, String? authToken})> _getHost() async {
    if (widget.resolveHost != null) return await widget.resolveHost!();
    final hosts = ref.read(hostsListProvider);
    final h = hosts.firstWhere(
      (item) =>
          item.name.toLowerCase() == widget.session.host.toLowerCase() ||
          item.url.toLowerCase() == widget.session.host.toLowerCase(),
      orElse: () => hosts.isNotEmpty
          ? hosts.first
          : HostRecord(
              name: 'local',
              url: 'http://127.0.0.1:7777',
              createdAt: DateTime.now(),
            ),
    );
    return (url: h.url, authToken: h.authToken.isNotEmpty ? h.authToken : null);
  }

  Future<void> _fetchSuggestions(ComposerTrigger trigger) async {
    try {
      final host = await _getHost();
      final api = ref.read(apiClientProvider);

      if (trigger.kind == ComposerTriggerKind.command) {
        final res = await api.getCommandSuggestions(
          host.url,
          widget.session.id,
          query: trigger.query,
          authToken: host.authToken,
        );
        if (!mounted) return;
        setState(() => _commandSuggestions = res);
      } else if (trigger.kind == ComposerTriggerKind.file) {
        final res = await api.getFileSuggestions(
          host.url,
          widget.session.id,
          query: trigger.query,
          authToken: host.authToken,
        );
        if (!mounted) return;
        setState(() => _fileSuggestions = res);
      }
    } catch (_) {}
  }

  void _applySuggestion(String replacement) {
    final trig = _currentTrigger;
    if (trig == null) return;

    final text = widget.controller.text;
    final prefix = text.substring(0, trig.replaceStart);
    final suffix =
        trig.replaceEnd <= text.length ? text.substring(trig.replaceEnd) : '';
    final trailing = suffix.startsWith(' ') ? '' : ' ';
    final newText = '$prefix$replacement$trailing$suffix';

    widget.controller.value = TextEditingValue(
      text: newText,
      selection: TextSelection.collapsed(
        offset: prefix.length + replacement.length + 1,
      ),
    );
    setState(() {
      _currentTrigger = null;
      _commandSuggestions = const [];
      _fileSuggestions = const [];
    });
  }

  @override
  Widget build(BuildContext context) {
    final trig = _currentTrigger;
    if (trig == null) return const SizedBox.shrink();

    if (trig.kind == ComposerTriggerKind.shell) {
      return Container(
        key: const ValueKey('headless_shell_mode_pill'),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        color: AppColors.surface,
        child: Row(
          children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: AppColors.statusAmber.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(
                    color: AppColors.statusAmber.withValues(alpha: 0.4)),
              ),
              child: Text(
                'SHELL · 0 tokens',
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.statusAmber,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ],
        ),
      );
    }

    if (trig.kind == ComposerTriggerKind.command &&
        _commandSuggestions.isNotEmpty) {
      return Container(
        key: const ValueKey('headless_autocomplete_strip'),
        height: 38,
        color: AppColors.surface,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          itemCount: _commandSuggestions.length,
          separatorBuilder: (_, __) => const SizedBox(width: 8),
          itemBuilder: (ctx, idx) {
            final cmd = _commandSuggestions[idx];
            return InkWell(
              onTap: () => _applySuggestion('/${cmd.name}'),
              borderRadius: BorderRadius.circular(4),
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(color: AppColors.outlineSubtle),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      '/${cmd.name}',
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.infoCyan,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (cmd.description.isNotEmpty) ...[
                      const SizedBox(width: 4),
                      Text(
                        cmd.description,
                        style: AppTypography.codeXs.copyWith(
                          color: AppColors.textMuted,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            );
          },
        ),
      );
    }

    if (trig.kind == ComposerTriggerKind.file && _fileSuggestions.isNotEmpty) {
      return Container(
        key: const ValueKey('headless_autocomplete_strip'),
        height: 38,
        color: AppColors.surface,
        child: ListView.separated(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
          itemCount: _fileSuggestions.length,
          separatorBuilder: (_, __) => const SizedBox(width: 8),
          itemBuilder: (ctx, idx) {
            final f = _fileSuggestions[idx];
            return InkWell(
              onTap: () => _applySuggestion('@${f.path}'),
              borderRadius: BorderRadius.circular(4),
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                decoration: BoxDecoration(
                  color: AppColors.surfaceHighlight,
                  borderRadius: BorderRadius.circular(4),
                  border: Border.all(color: AppColors.outlineSubtle),
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      f.isDir
                          ? Icons.folder_rounded
                          : Icons.description_rounded,
                      size: 12,
                      color: AppColors.textSecondary,
                    ),
                    const SizedBox(width: 4),
                    Text(
                      f.path,
                      style: AppTypography.codeXs.copyWith(
                        color: AppColors.textPrimary,
                      ),
                    ),
                  ],
                ),
              ),
            );
          },
        ),
      );
    }

    return const SizedBox.shrink();
  }
}
