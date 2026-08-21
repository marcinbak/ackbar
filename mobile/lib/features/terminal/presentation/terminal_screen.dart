import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:xterm/xterm.dart';
import '../../../core/models/session.dart';
import '../../../core/providers/fleet_providers.dart';
import '../../../core/theme/app_colors.dart';
import '../../../core/theme/app_spacing.dart';
import '../../../core/theme/app_typography.dart';
import '../../../core/widgets/terminal_accessory_bar.dart';

/// Fullscreen interactive PTY Terminal screen streaming live tmux session I/O
/// with horizontal scrolling, dynamic auto-fit, pinch-to-zoom, and font controls.
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

  // Terminal Display & Sizing State
  double _fontSize = 11.0;
  bool _autoFit = true; // Auto-fit columns to mobile width vs fixed 80/120 cols
  int _currentCols = 80;
  int _currentRows = 28;
  final ScrollController _horizontalScrollController = ScrollController();

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
    final parsed = Uri.parse(hostUrl);
    final wsScheme = parsed.scheme == 'https' ? 'wss' : 'ws';
    final hostAddress = parsed.authority.isNotEmpty ? parsed.authority : '127.0.0.1:7777';

    final host = match.isNotEmpty ? match.first : (hosts.isNotEmpty ? hosts.first : null);

    final queryParams = {
      'id': widget.session.id,
      'host': widget.session.host.isNotEmpty ? widget.session.host : 'local',
      'cols': _currentCols.toString(),
      'rows': _currentRows.toString(),
      if (host != null && host.authToken.isNotEmpty) 'token': host.authToken,
    };

    final uri = Uri.parse('$wsScheme://$hostAddress/v1/sessions/pty').replace(
      queryParameters: queryParams,
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

  void _sendResize(int cols, int rows) {
    if (cols == _currentCols && rows == _currentRows) return;
    _currentCols = cols;
    _currentRows = rows;
    _terminal.resize(cols, rows);

    try {
      _channel?.sink.add(jsonEncode({
        'type': 'resize',
        'cols': cols,
        'rows': rows,
      }));
    } catch (_) {}
  }

  void _updateDimensions(BoxConstraints constraints) {
    final charWidth = _fontSize * 0.60;
    final charHeight = _fontSize * 1.30;

    if (_autoFit) {
      final fitCols = max(20, (constraints.maxWidth / charWidth).floor());
      final fitRows = max(10, (constraints.maxHeight / charHeight).floor());
      _sendResize(fitCols, fitRows);
    } else {
      final fixedCols = 80;
      final fitRows = max(10, (constraints.maxHeight / charHeight).floor());
      _sendResize(fixedCols, fitRows);
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

  void _zoomIn() {
    HapticFeedback.selectionClick();
    setState(() {
      _fontSize = min(18.0, _fontSize + 1.0);
    });
  }

  void _zoomOut() {
    HapticFeedback.selectionClick();
    setState(() {
      _fontSize = max(8.0, _fontSize - 1.0);
    });
  }

  void _toggleAutoFit() {
    HapticFeedback.selectionClick();
    setState(() {
      _autoFit = !_autoFit;
    });
  }

  @override
  void dispose() {
    _horizontalScrollController.dispose();
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
                Expanded(
                  child: Text(
                    widget.session.displayTitle,
                    style: AppTypography.codeSm.copyWith(
                      color: AppColors.textPrimary,
                      fontWeight: FontWeight.w700,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            Text(
              '${widget.session.agentDisplayName} @ ${widget.session.hostTag} • ${_currentCols}x$_currentRows',
              style: AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: [
          // Fit mode toggle
          TextButton(
            onPressed: _toggleAutoFit,
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              minimumSize: const Size(40, 32),
            ),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: _autoFit ? AppColors.infoCyan.withValues(alpha: 0.15) : AppColors.surfaceHighlight,
                borderRadius: BorderRadius.circular(4),
                border: Border.all(
                  color: _autoFit ? AppColors.infoCyan : AppColors.outlineSubtle,
                  width: 0.8,
                ),
              ),
              child: Text(
                _autoFit ? 'FIT' : '80 COL',
                style: AppTypography.codeXs.copyWith(
                  color: _autoFit ? AppColors.infoCyan : AppColors.textSecondary,
                  fontWeight: FontWeight.w700,
                  fontSize: 10,
                ),
              ),
            ),
          ),

          // Zoom Out
          IconButton(
            icon: const Icon(Icons.remove_rounded, size: 18, color: AppColors.textSecondary),
            tooltip: 'Decrease font size',
            onPressed: _zoomOut,
          ),

          // Zoom In
          IconButton(
            icon: const Icon(Icons.add_rounded, size: 18, color: AppColors.textSecondary),
            tooltip: 'Increase font size',
            onPressed: _zoomIn,
          ),

          // Reconnect
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
                color: AppColors.statusCoral.withValues(alpha: 0.15),
                child: Text(
                  _errorMessage!,
                  style: AppTypography.codeXs.copyWith(color: AppColors.statusCoral),
                ),
              ),
            Expanded(
              child: Container(
                color: AppColors.terminalBlack,
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
                child: LayoutBuilder(
                  builder: (context, constraints) {
                    WidgetsBinding.instance.addPostFrameCallback((_) {
                      if (mounted) {
                        _updateDimensions(constraints);
                      }
                    });

                    final charWidth = _fontSize * 0.60;
                    final terminalRenderWidth = _autoFit
                        ? constraints.maxWidth
                        : max(constraints.maxWidth, _currentCols * charWidth + 16.0);

                    return Scrollbar(
                      controller: _horizontalScrollController,
                      thumbVisibility: !_autoFit,
                      trackVisibility: !_autoFit,
                      child: SingleChildScrollView(
                        controller: _horizontalScrollController,
                        scrollDirection: Axis.horizontal,
                        physics: const BouncingScrollPhysics(),
                        child: SizedBox(
                          width: terminalRenderWidth,
                          height: constraints.maxHeight,
                          child: TerminalView(
                            _terminal,
                            autofocus: true,
                            backgroundOpacity: 0.0,
                            textStyle: TerminalStyle(
                              fontSize: _fontSize,
                              fontFamily: 'JetBrains Mono',
                            ),
                          ),
                        ),
                      ),
                    );
                  },
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
