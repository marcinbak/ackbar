import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/models/headless_models.dart';
import '../../../../core/models/host.dart';
import '../../../../core/models/session.dart';
import '../../../../core/network/api_client.dart';
import '../../../../core/network/headless_api_client.dart';
import '../../../../core/providers/fleet_providers.dart';
import '../../../../core/services/external_url_launcher.dart';
import '../../../../core/theme/app_colors.dart';
import '../../../../core/theme/app_typography.dart';
import '../../terminal/presentation/terminal_screen.dart';

class HeadlessAuthCard extends ConsumerStatefulWidget {
  final Session session;
  final String surfacePrefix;
  final VoidCallback? onResolved;

  const HeadlessAuthCard({
    super.key,
    required this.session,
    required this.surfacePrefix,
    this.onResolved,
  });

  @override
  ConsumerState<HeadlessAuthCard> createState() => _HeadlessAuthCardState();
}

class _HeadlessAuthCardState extends ConsumerState<HeadlessAuthCard>
    with WidgetsBindingObserver {
  static final _secondaryBtnStyle = OutlinedButton.styleFrom(
    foregroundColor: AppColors.textSecondary,
    side: const BorderSide(color: AppColors.outlineSubtle),
    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
    visualDensity: VisualDensity.compact,
  );

  String? _localFlowId;
  String? _localAuthUrl;
  String? _statusMessage;
  String? _errorMessage;
  bool _isBusy = false;
  bool _showRemotePaste = false;
  final _callbackController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _localFlowId = widget.session.blocked?.flowId;
    _localAuthUrl = widget.session.blocked?.authUrl;
  }

  @override
  void didUpdateWidget(covariant HeadlessAuthCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    final b = widget.session.blocked;
    if (b?.flowId != null && b?.flowId != _localFlowId) {
      _localFlowId = b?.flowId;
    }
    if (b?.authUrl != null && b?.authUrl != _localAuthUrl) {
      _localAuthUrl = b?.authUrl;
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      ref.read(fleetSessionsProvider.notifier).refreshSessions();
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _callbackController.dispose();
    super.dispose();
  }

  HostRecord _resolveHost() {
    final hosts = ref.read(hostsListProvider);
    return hosts.firstWhere(
      (h) =>
          h.name.toLowerCase() == widget.session.host.toLowerCase() ||
          h.url.toLowerCase() == widget.session.host.toLowerCase(),
      orElse: () => HostRecord(
        name: widget.session.host.isNotEmpty ? widget.session.host : 'local',
        url: 'http://127.0.0.1:7777',
        createdAt: DateTime.now(),
      ),
    );
  }

  String _resolveTarget() {
    final explicit = widget.session.blocked?.authTarget;
    if (explicit != null && explicit.trim().isNotEmpty) return explicit.trim();
    if (widget.session.agent.toLowerCase() == 'antigravity') return 'agent:agy';
    return '';
  }

  Future<void> _handleSignIn() async {
    final launcher = ref.read(externalUrlLauncherProvider);
    final api = ref.read(apiClientProvider);
    final host = _resolveHost();

    setState(() {
      _isBusy = true;
      _errorMessage = null;
      _statusMessage = null;
    });

    try {
      var url = _localAuthUrl;
      if (url == null || !isSafeHttpsUrl(url)) {
        final target = _resolveTarget();
        if (target.isEmpty) {
          setState(() {
            _isBusy = false;
            _errorMessage =
                'No auth target specified. Use Terminal to sign in manually.';
          });
          return;
        }
        final res = await api.startAuthTool(
          host.url,
          sessionId: widget.session.id,
          target: target,
          authToken: host.authToken,
        );
        _localFlowId = res.flowId;
        _localAuthUrl = res.authUrl;
        url = res.authUrl;
      }

      final outcome = await launcher.launchHttpsUrl(url);
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        switch (outcome) {
          case UrlLaunchOutcome.openedBrowser:
            _statusMessage =
                'Browser opened — complete sign-in then return to Ackbar';
          case UrlLaunchOutcome.copiedFallback:
            _statusMessage = 'Sign-in link copied to clipboard';
          case UrlLaunchOutcome.rejectedUnsafe:
            _errorMessage = 'Blocked unsafe non-HTTPS sign-in URL';
        }
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        _errorMessage = e is ApiHttpException ? e.message : e.toString();
      });
    }
  }

  Future<void> _handleCopyLink() async {
    final url = _localAuthUrl;
    if (url == null || !isSafeHttpsUrl(url)) return;
    await ref.read(externalUrlLauncherProvider).copyToClipboard(url);
    if (!mounted) return;
    setState(() => _statusMessage = 'Sign-in link copied to clipboard');
  }

  Future<void> _handleSubmitCallback() async {
    final input = _callbackController.text.trim();
    final flowId = _localFlowId ?? widget.session.blocked?.flowId;
    if (input.isEmpty || flowId == null || flowId.isEmpty) {
      setState(() =>
          _errorMessage = 'Enter a valid redirect URL or authorization code');
      return;
    }

    final api = ref.read(apiClientProvider);
    final host = _resolveHost();
    setState(() {
      _isBusy = true;
      _errorMessage = null;
      _statusMessage = null;
    });

    try {
      final res = await api.submitAuthCallback(
        host.url,
        sessionId: widget.session.id,
        flowId: flowId,
        callbackInput: input,
        authToken: host.authToken,
      );
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        if (res.completed) {
          _statusMessage = 'Authentication completed successfully';
          _callbackController.clear();
          _showRemotePaste = false;
          ref.read(fleetSessionsProvider.notifier).refreshSessions();
          widget.onResolved?.call();
        } else {
          _statusMessage = 'Callback delivered — waiting for token exchange...';
        }
      });
    } on ApiHttpException catch (e) {
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        if (e.statusCode == 404) {
          _localFlowId = null;
          _localAuthUrl = null;
          _errorMessage =
              'Sign-in flow expired — tap Sign In to start a new flow';
        } else {
          _errorMessage = e.message;
        }
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        _errorMessage = e.toString();
      });
    }
  }

  Future<void> _handleRetry() async {
    setState(() => _isBusy = true);
    await ref.read(fleetSessionsProvider.notifier).respondToSession(
          sessionId: widget.session.id,
          action: 'retry',
        );
    if (mounted) setState(() => _isBusy = false);
  }

  Future<void> _handleTerminal() async {
    final api = ref.read(apiClientProvider);
    final host = _resolveHost();
    setState(() => _isBusy = true);

    try {
      final res = await api.takeWheel(
        host.url,
        widget.session.id,
        authToken: host.authToken,
      );
      if (!mounted) return;
      setState(() => _isBusy = false);
      if (res != null) {
        final updated = widget.session.copyWith(
          engineType: 'tmux',
          managed: true,
          state: SessionState.working,
          clearBlocked: true,
        );
        ref.read(fleetSessionsProvider.notifier).upsertSession(updated);
        TerminalScreen.open(context, updated);
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isBusy = false;
        _errorMessage = e is ApiHttpException ? e.message : e.toString();
      });
    }
  }

  Future<void> _handleDismiss() async {
    setState(() => _isBusy = true);
    await ref.read(fleetSessionsProvider.notifier).respondToSession(
          sessionId: widget.session.id,
          action: 'dismiss',
        );
    if (mounted) setState(() => _isBusy = false);
  }

  @override
  Widget build(BuildContext context) {
    final prefix = widget.surfacePrefix;
    final b = widget.session.blocked;
    final name =
        b?.authServer ?? b?.authTarget ?? widget.session.agentDisplayName;
    final hint = b?.authHint ?? b?.reason ?? '';
    final hasSafeUrl = _localAuthUrl != null && isSafeHttpsUrl(_localAuthUrl);

    return Container(
      key: ValueKey('${prefix}_headless_auth_card'),
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppColors.statusAmber.withValues(alpha: 0.6)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              const Icon(Icons.vpn_key_rounded,
                  size: 18, color: AppColors.statusAmber),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  'Authentication Required: $name',
                  style: AppTypography.titleSmall.copyWith(
                    color: AppColors.textPrimary,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          if (hint.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(hint,
                style: AppTypography.bodySmall
                    .copyWith(color: AppColors.textSecondary)),
          ],
          if (_statusMessage != null) ...[
            const SizedBox(height: 6),
            Text(_statusMessage!,
                style: AppTypography.codeXs
                    .copyWith(color: AppColors.statusEmerald)),
          ],
          if (_errorMessage != null) ...[
            const SizedBox(height: 6),
            Text(_errorMessage!,
                style: AppTypography.codeXs
                    .copyWith(color: AppColors.statusCoral)),
          ],
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              FilledButton.icon(
                key: ValueKey('${prefix}_auth_sign_in_btn'),
                onPressed: _isBusy ? null : _handleSignIn,
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.infoCyan,
                  foregroundColor: AppColors.background,
                  padding:
                      const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                  visualDensity: VisualDensity.compact,
                ),
                icon: const Icon(Icons.login_rounded, size: 16),
                label: const Text('Sign In'),
              ),
              if (hasSafeUrl)
                OutlinedButton.icon(
                  key: ValueKey('${prefix}_auth_copy_link_btn'),
                  onPressed: _isBusy ? null : _handleCopyLink,
                  style: _secondaryBtnStyle,
                  icon: const Icon(Icons.copy_rounded, size: 14),
                  label: const Text('Copy Link'),
                ),
              OutlinedButton.icon(
                key: ValueKey('${prefix}_auth_retry_btn'),
                onPressed: _isBusy ? null : _handleRetry,
                style: _secondaryBtnStyle,
                icon: const Icon(Icons.refresh_rounded, size: 14),
                label: const Text('Retry'),
              ),
              OutlinedButton.icon(
                key: ValueKey('${prefix}_auth_terminal_btn'),
                onPressed: _isBusy ? null : _handleTerminal,
                style: _secondaryBtnStyle,
                icon: const Icon(Icons.terminal_rounded, size: 14),
                label: const Text('Terminal'),
              ),
              TextButton(
                key: ValueKey('${prefix}_auth_dismiss_btn'),
                onPressed: _isBusy ? null : _handleDismiss,
                style: TextButton.styleFrom(
                  foregroundColor: AppColors.textMuted,
                  visualDensity: VisualDensity.compact,
                ),
                child: const Text('Dismiss'),
              ),
            ],
          ),
          const SizedBox(height: 6),
          InkWell(
            key: ValueKey('${prefix}_auth_remote_toggle'),
            onTap: () => setState(() => _showRemotePaste = !_showRemotePaste),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Row(
                children: [
                  Icon(
                    _showRemotePaste
                        ? Icons.keyboard_arrow_down_rounded
                        : Icons.keyboard_arrow_right_rounded,
                    size: 16,
                    color: AppColors.textMuted,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    'Paste redirect URL / code (remote host)',
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textMuted),
                  ),
                ],
              ),
            ),
          ),
          if (_showRemotePaste) ...[
            const SizedBox(height: 6),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    key: ValueKey('${prefix}_auth_callback_input'),
                    controller: _callbackController,
                    style: AppTypography.codeXs
                        .copyWith(color: AppColors.textPrimary),
                    decoration: InputDecoration(
                      hintText: 'Paste callback URL or code',
                      hintStyle: AppTypography.codeXs
                          .copyWith(color: AppColors.textMuted),
                      isDense: true,
                      contentPadding: const EdgeInsets.all(8),
                      filled: true,
                      fillColor: AppColors.background,
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(6),
                        borderSide:
                            const BorderSide(color: AppColors.outlineSubtle),
                      ),
                      enabledBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(6),
                        borderSide:
                            const BorderSide(color: AppColors.outlineSubtle),
                      ),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  key: ValueKey('${prefix}_auth_callback_submit'),
                  onPressed: _isBusy ? null : _handleSubmitCallback,
                  style: FilledButton.styleFrom(
                    backgroundColor: AppColors.statusEmerald,
                    foregroundColor: AppColors.background,
                    padding:
                        const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                    visualDensity: VisualDensity.compact,
                  ),
                  child: const Text('Submit'),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}
