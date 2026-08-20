import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:xterm/xterm.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/terminal_accessory_bar.dart';

/// Fullscreen interactive PTY Terminal screen streaming live tmux session I/O.
class TerminalScreen extends ConsumerStatefulWidget {
  final Session session;

  const TerminalScreen({super.key, required this.session});

  static Future<void> open(BuildContext context, Session session) {
    return Navigator.of(context).push(
      MaterialPageRoute(
        builder: (context) => TerminalScreen(session: session),
      ),
    );
  }

  @override
  ConsumerState<TerminalScreen> createState() => _TerminalScreenState();
}

class _TerminalScreenState extends ConsumerState<TerminalScreen> {
  late final Terminal _terminal;
  WebSocketChannel? _channel;
  StreamSubscription? _sub;
  bool _connecting = true;
  String? _errorMessage;

  Timer? _pingTimer;

  @override
  void initState() {
    super.initState();
    _terminal = Terminal(maxLines: 5000);
    _connectWebSocket();
  }

  void _connectWebSocket() {
    final hosts = ref.read(hostsListProvider);
    final match = hosts.where((h) => h.name == widget.session.host || h.url.contains(widget.session.host));
    final hostUrl = match.isNotEmpty ? match.first.url : (hosts.isNotEmpty ? hosts.first.url : 'http://127.0.0.1:7777');

    final cleanUrl = hostUrl.replaceAll(RegExp(r'/+$'), '');
    final wsScheme = cleanUrl.startsWith('https') ? 'wss' : 'ws';
    final hostAddress = cleanUrl.replaceFirst(RegExp(r'^https?://'), '');

    final uri = Uri.parse('$wsScheme://$hostAddress/v1/sessions/pty').replace(
      queryParameters: {
        'id': widget.session.id,
        'host': widget.session.host.isNotEmpty ? widget.session.host : 'local',
        'cols': '80',
        'rows': '28',
      },
    );

    try {
      final channel = WebSocketChannel.connect(uri);
      _channel = channel;

      _terminal.onOutput = (data) {
        _channel?.sink.add(data);
      };

      _pingTimer?.cancel();
      _pingTimer = Timer.periodic(const Duration(seconds: 15), (_) {
        try {
          _channel?.sink.add(jsonEncode({'type': 'ping'}));
        } catch (_) {}
      });

      _sub = channel.stream.listen(
        (message) {
          if (_connecting) {
            setState(() {
              _connecting = false;
            });
          }
          if (message is String) {
            _terminal.write(message);
          } else if (message is List<int>) {
            _terminal.write(utf8.decode(message, allowMalformed: true));
          }
        },
        onError: (err) {
          setState(() {
            _connecting = false;
            _errorMessage = 'WebSocket Error: $err';
          });
        },
        onDone: () {
          setState(() {
            _connecting = false;
            if (_errorMessage == null) {
              _terminal.write('\r\n\x1b[90m[Disconnected from session]\x1b[0m\r\n');
            }
          });
        },
      );
    } catch (e) {
      setState(() {
        _connecting = false;
        _errorMessage = 'Connection failed: $e';
      });
    }
  }

  void _handleAccessoryKey(String key) {
    if (_channel == null) return;

    switch (key) {
      case 'Esc':
        _channel!.sink.add('\x1b');
        break;
      case 'Tab':
        _channel!.sink.add('\t');
        break;
      case 'Shift+Tab':
        _channel!.sink.add('\x1b[Z');
        break;
      case 'Ctrl+C':
        _channel!.sink.add('\x03');
        break;
      case 'Ctrl+D':
        _channel!.sink.add('\x04');
        break;
      case '↑':
        _channel!.sink.add('\x1b[A');
        break;
      case '↓':
        _channel!.sink.add('\x1b[B');
        break;
      case '←':
        _channel!.sink.add('\x1b[D');
        break;
      case '→':
        _channel!.sink.add('\x1b[C');
        break;
      case 'Enter':
        _channel!.sink.add('\r');
        break;
      default:
        _channel!.sink.add(key);
    }
  }

  @override
  void dispose() {
    _pingTimer?.cancel();
    _sub?.cancel();
    _channel?.sink.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppColors.terminalBlack,
      appBar: AppBar(
        backgroundColor: AppColors.surface,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded, size: 18, color: AppColors.textPrimary),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.terminal_rounded, size: 16, color: AppColors.infoCyan),
                const SizedBox(width: 6),
                Text(
                  widget.session.tmuxName.isNotEmpty ? widget.session.tmuxName : widget.session.id,
                  style: AppTypography.codeSm.copyWith(
                    color: AppColors.textPrimary,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ],
            ),
            Text(
              '${widget.session.agentDisplayName} @ ${widget.session.hostTag}',
              style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
            ),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh_rounded, size: 20, color: AppColors.textMuted),
            tooltip: 'Reconnect PTY',
            onPressed: () {
              _sub?.cancel();
              _channel?.sink.close();
              _terminal.buffer.clear();
              setState(() {
                _connecting = true;
                _errorMessage = null;
              });
              _connectWebSocket();
            },
          ),
        ],
      ),
      body: SafeArea(
        child: Column(
          children: [
            if (_connecting)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.symmetric(vertical: 4),
                color: AppColors.surfaceHighlight,
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    const SizedBox(
                      width: 12,
                      height: 12,
                      child: CircularProgressIndicator(strokeWidth: 2, color: AppColors.infoCyan),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      'Connecting to tmux session...',
                      style: AppTypography.codeXs.copyWith(color: AppColors.infoCyan),
                    ),
                  ],
                ),
              ),
            if (_errorMessage != null)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(AppSpacing.md),
                color: AppColors.statusCoral.withOpacity(0.15),
                child: Text(
                  _errorMessage!,
                  style: AppTypography.codeXs.copyWith(color: AppColors.statusCoral),
                ),
              ),
            Expanded(
              child: Container(
                color: AppColors.terminalBlack,
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                child: TerminalView(
                  _terminal,
                  autofocus: true,
                  backgroundOpacity: 0.0,
                  textStyle: const TerminalStyle(
                    fontSize: 12,
                    fontFamily: 'JetBrains Mono',
                  ),
                ),
              ),
            ),
            TerminalAccessoryBar(
              onKeyPressed: _handleAccessoryKey,
            ),
          ],
        ),
      ),
    );
  }
}
