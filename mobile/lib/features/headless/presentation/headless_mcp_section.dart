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

class HeadlessMcpSection extends ConsumerStatefulWidget {
  final Session session;
  final Future<({String url, String? authToken})> Function()? resolveHost;

  const HeadlessMcpSection({
    super.key,
    required this.session,
    this.resolveHost,
  });

  @override
  ConsumerState<HeadlessMcpSection> createState() => _HeadlessMcpSectionState();
}

class _HeadlessMcpSectionState extends ConsumerState<HeadlessMcpSection>
    with WidgetsBindingObserver {
  SessionMcpStatus? _status;
  bool _isLoading = false;
  String? _errorMessage;
  String? _activeTarget;
  String? _activeFlowId;
  String? _activeAuthUrl;
  final _callbackController = TextEditingController();
  bool _isSubmittingCallback = false;
  bool _isRestarting = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _loadStatus();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && _activeFlowId != null) {
      _loadStatus(refresh: true);
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _callbackController.dispose();
    super.dispose();
  }

  Future<({String url, String? authToken})> _getHost() async {
    if (widget.resolveHost != null) {
      return await widget.resolveHost!();
    }
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

  Future<void> _loadStatus({bool refresh = false}) async {
    setState(() {
      _isLoading = true;
      _errorMessage = null;
    });

    try {
      final host = await _getHost();
      final api = ref.read(apiClientProvider);
      final res = await api.getMcpStatus(
        host.url,
        widget.session.id,
        refresh: refresh,
        authToken: host.authToken,
      );
      if (!mounted) return;
      setState(() {
        _status = res;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _errorMessage = e is ApiHttpException ? e.message : e.toString();
      });
    }
  }

  Future<void> _handleRestartSession() async {
    if (_isRestarting) return;
    setState(() {
      _isRestarting = true;
      _errorMessage = null;
    });
    final ok = await ref
        .read(fleetSessionsProvider.notifier)
        .restartSession(widget.session.id);
    if (!mounted) return;
    setState(() => _isRestarting = false);
    if (ok) {
      await _loadStatus(refresh: true);
    } else {
      setState(() => _errorMessage = 'Failed to restart session');
    }
  }

  Future<void> _handleStartAuth(String target) async {
    setState(() {
      _activeTarget = target;
      _errorMessage = null;
    });

    try {
      final host = await _getHost();
      final api = ref.read(apiClientProvider);
      final res = await api.startAuthTool(
        host.url,
        sessionId: widget.session.id,
        target: target,
        authToken: host.authToken,
      );
      if (!mounted) return;
      _activeFlowId = res.flowId;
      _activeAuthUrl = res.authUrl;
      final launcher = ref.read(externalUrlLauncherProvider);
      await launcher.launchHttpsUrl(res.authUrl);
      if (!mounted) return;
      setState(() {});
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _errorMessage = e is ApiHttpException ? e.message : e.toString();
      });
    }
  }

  Future<void> _handleSubmitCallback() async {
    final input = _callbackController.text.trim();
    if (input.isEmpty || _activeFlowId == null) return;
    setState(() => _isSubmittingCallback = true);

    try {
      final host = await _getHost();
      final api = ref.read(apiClientProvider);
      final res = await api.submitAuthCallback(
        host.url,
        sessionId: widget.session.id,
        flowId: _activeFlowId!,
        callbackInput: input,
        authToken: host.authToken,
      );
      if (!mounted) return;
      setState(() => _isSubmittingCallback = false);
      if (res.completed) {
        _callbackController.clear();
        _activeFlowId = null;
        _activeAuthUrl = null;
        _activeTarget = null;
        await _loadStatus(refresh: true);
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isSubmittingCallback = false;
        _errorMessage = e is ApiHttpException ? e.message : e.toString();
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_isLoading && _status == null) {
      return const Padding(
        padding: EdgeInsets.all(12),
        child: Center(
          child: SizedBox(
            width: 20,
            height: 20,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
        ),
      );
    }

    final servers = _status?.servers ?? const [];
    final plugins = _status?.plugins ?? const [];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text(
              'MCP & WORKSPACE TOOLS',
              style: AppTypography.codeXs.copyWith(
                color: AppColors.textMuted,
                fontWeight: FontWeight.w600,
              ),
            ),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextButton.icon(
                  key: const Key('mcp_restart_session_button'),
                  onPressed: _isRestarting ? null : _handleRestartSession,
                  icon: const Icon(Icons.restart_alt_rounded, size: 13),
                  label: Text(_isRestarting ? 'Restarting...' : 'Restart CLI'),
                  style: TextButton.styleFrom(
                    foregroundColor: AppColors.infoCyan,
                    visualDensity: VisualDensity.compact,
                    padding: const EdgeInsets.symmetric(horizontal: 6),
                    textStyle: AppTypography.codeXs,
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.refresh_rounded, size: 14),
                  color: AppColors.textSecondary,
                  visualDensity: VisualDensity.compact,
                  onPressed: () => _loadStatus(refresh: true),
                ),
              ],
            ),
          ],
        ),
        if (_errorMessage != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: Text(
              _errorMessage!,
              style: AppTypography.codeXs.copyWith(
                color: AppColors.statusCoral,
              ),
            ),
          ),
        if (servers.isEmpty && plugins.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Text(
              'No configured MCP servers or workspace plugins',
              style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
            ),
          ),
        if (servers.isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            'Servers',
            style: AppTypography.codeXs.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: 4),
          ...servers.map((s) => _buildServerRow(s)),
        ],
        if (plugins.isNotEmpty) ...[
          const SizedBox(height: 8),
          Text(
            'Workspace Plugins',
            style: AppTypography.codeXs.copyWith(
              color: AppColors.textSecondary,
            ),
          ),
          const SizedBox(height: 4),
          ...plugins.map((p) => _buildPluginRow(p)),
        ],
        if (_activeTarget != null && _activeFlowId != null) ...[
          const SizedBox(height: 8),
          _buildCallbackSection(),
        ],
      ],
    );
  }

  Widget _buildServerRow(McpServerHealth s) {
    final isConfigured = s.isConfigured;
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 2),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.background,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: AppColors.outlineSubtle),
      ),
      child: Row(
        children: [
          Icon(
            isConfigured
                ? Icons.check_circle_rounded
                : Icons.lock_clock_rounded,
            size: 14,
            color:
                isConfigured ? AppColors.statusEmerald : AppColors.statusAmber,
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              s.name,
              style: AppTypography.codeXs.copyWith(
                color: AppColors.textPrimary,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (s.transport.isNotEmpty)
            Text(
              s.transport,
              style: AppTypography.codeXs.copyWith(color: AppColors.textMuted),
            ),
          if (s.authSupported && s.target.isNotEmpty && !isConfigured) ...[
            const SizedBox(width: 8),
            _buildAuthButton(s.target),
          ],
        ],
      ),
    );
  }

  Widget _buildPluginRow(WorkspacePluginHealth p) {
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 2),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: AppColors.background,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: AppColors.outlineSubtle),
      ),
      child: Row(
        children: [
          Icon(
            p.authenticated ? Icons.check_circle_rounded : Icons.lock_rounded,
            size: 14,
            color: p.authenticated
                ? AppColors.statusEmerald
                : AppColors.statusAmber,
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              p.name,
              style: AppTypography.codeXs.copyWith(
                color: AppColors.textPrimary,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (p.authSupported && p.target.isNotEmpty && !p.authenticated) ...[
            const SizedBox(width: 8),
            _buildAuthButton(p.target),
          ],
        ],
      ),
    );
  }

  Widget _buildAuthButton(String target) {
    return SizedBox(
      height: 24,
      child: OutlinedButton(
        key: Key('mcp_sign_in_$target'),
        onPressed: () => _handleStartAuth(target),
        style: OutlinedButton.styleFrom(
          side: const BorderSide(color: AppColors.statusAmber),
          foregroundColor: AppColors.statusAmber,
          padding: const EdgeInsets.symmetric(horizontal: 8),
          visualDensity: VisualDensity.compact,
        ),
        child: const Text('Sign in', style: TextStyle(fontSize: 10)),
      ),
    );
  }

  Widget _buildCallbackSection() {
    return Container(
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: AppColors.surfaceHighlight,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: AppColors.outlineSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'Paste Callback for $_activeTarget',
                style: AppTypography.codeXs.copyWith(
                  color: AppColors.textSecondary,
                ),
              ),
              if (_activeAuthUrl != null && isSafeHttpsUrl(_activeAuthUrl))
                InkWell(
                  onTap: () => ref
                      .read(externalUrlLauncherProvider)
                      .copyToClipboard(_activeAuthUrl!),
                  child: Text(
                    'Copy Link',
                    style: AppTypography.codeXs.copyWith(
                      color: AppColors.infoCyan,
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 6),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _callbackController,
                  style: AppTypography.codeXs.copyWith(
                    color: AppColors.textPrimary,
                  ),
                  decoration: InputDecoration(
                    hintText: 'Paste callback URL or code',
                    hintStyle: AppTypography.codeXs.copyWith(
                      color: AppColors.textMuted,
                    ),
                    isDense: true,
                    contentPadding: const EdgeInsets.all(6),
                    filled: true,
                    fillColor: AppColors.background,
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(4),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 6),
              FilledButton(
                onPressed: _isSubmittingCallback ? null : _handleSubmitCallback,
                style: FilledButton.styleFrom(
                  backgroundColor: AppColors.statusEmerald,
                  padding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 6,
                  ),
                  visualDensity: VisualDensity.compact,
                ),
                child: const Text('Submit', style: TextStyle(fontSize: 11)),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
