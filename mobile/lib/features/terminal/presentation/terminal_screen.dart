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
/// with horizontal scrolling, vertical swipe-to-scroll, dynamic auto-fit, zoom, and touch keyboard controls.
class TerminalScreen extends ConsumerStatefulWidget {
  final Session session;
  final WebSocketChannel Function(Uri uri)? channelBuilder;

  const TerminalScreen({
    super.key,
    required this.session,
    this.channelBuilder,
  });

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

class _TerminalScreenState extends ConsumerState<TerminalScreen>
    with WidgetsBindingObserver {
  late final Terminal _terminal;
  late final TerminalController _terminalController;
  final FocusNode _terminalFocusNode = FocusNode();
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
  final ScrollController _verticalScrollController = ScrollController();
  bool _showScrollToBottom = false;

  // Touch drag gesture scroll accumulator
  double _dragScrollAccumulator = 0.0;
  double _lastBottomInset = 0.0;
  int? _activePointerId;
  Offset? _pointerDownPos;
  Duration? _lastPointerTimeStamp;
  double _lastPointerVelocityY = 0.0;
  bool _isPointerVerticalScroll = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _terminal = Terminal(maxLines: 10000);
    _terminalController = TerminalController();
    _verticalScrollController.addListener(_onVerticalScroll);
    _connectWebSocket();
  }

  @override
  void didChangeMetrics() {
    super.didChangeMetrics();
    if (!mounted) return;
    final bottomInset = View.of(context).viewInsets.bottom;
    if (bottomInset != _lastBottomInset) {
      _lastBottomInset = bottomInset;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        if (_verticalScrollController.hasClients) {
          _verticalScrollController.jumpTo(0.0);
        }
      });
    }
  }

  void _onVerticalScroll() {
    if (!_verticalScrollController.hasClients) return;
    final maxScroll = _verticalScrollController.position.maxScrollExtent;
    final currentScroll = _verticalScrollController.offset;
    final show = (maxScroll - currentScroll) > 40.0;
    if (show && !_showScrollToBottom) {
      setState(() {
        _showScrollToBottom = true;
      });
    }
  }

  void _sendScroll(String direction, {int lines = 1}) {
    try {
      _channel?.sink.add(jsonEncode({
        'type': 'scroll',
        'direction': direction,
        'lines': lines,
      }));
    } catch (_) {}
  }

  void _scrollToBottom() {
    if (_verticalScrollController.hasClients) {
      _verticalScrollController.animateTo(
        0.0,
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOut,
      );
    }
    _sendScroll('bottom');
    if (_showScrollToBottom) {
      setState(() {
        _showScrollToBottom = false;
      });
    }
  }

  Session _resolveLiveSession() {
    final sessions = ref.read(fleetSessionsProvider);
    for (final s in sessions) {
      if (s.id == widget.session.id ||
          (s.oldId.isNotEmpty && s.oldId == widget.session.id) ||
          (widget.session.nativeId.isNotEmpty &&
              s.nativeId == widget.session.nativeId)) {
        return s;
      }
    }
    return widget.session;
  }

  void _connectWebSocket() {
    final liveSession = _resolveLiveSession();
    final hosts = ref.read(hostsListProvider);
    final sessionHost = liveSession.host.toLowerCase();
    final match = hosts.where((h) =>
        h.name.toLowerCase() == sessionHost ||
        h.url.toLowerCase().contains(sessionHost));
    final hostUrl = match.isNotEmpty
        ? match.first.url
        : (hosts.isNotEmpty ? hosts.first.url : 'http://127.0.0.1:7777');
    final parsed = Uri.parse(hostUrl);
    final wsScheme = parsed.scheme == 'https' ? 'wss' : 'ws';
    final hostAddress =
        parsed.authority.isNotEmpty ? parsed.authority : '127.0.0.1:7777';

    final host = match.isNotEmpty
        ? match.first
        : (hosts.isNotEmpty ? hosts.first : null);

    final queryParams = {
      'id': liveSession.id,
      'host': liveSession.host.isNotEmpty ? liveSession.host : 'local',
      'cols': _currentCols.toString(),
      'rows': _currentRows.toString(),
      if (host != null && host.authToken.isNotEmpty) 'token': host.authToken,
    };

    final uri = Uri.parse('$wsScheme://$hostAddress/v1/sessions/pty').replace(
      queryParameters: queryParams,
    );

    try {
      final channel = widget.channelBuilder != null
          ? widget.channelBuilder!(uri)
          : WebSocketChannel.connect(uri);
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
              _terminal
                  .write('\r\n\x1b[90m[Disconnected from session]\x1b[0m\r\n');
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

    if (_verticalScrollController.hasClients) {
      _verticalScrollController.jumpTo(0.0);
    }

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

    if (_verticalScrollController.hasClients &&
        _verticalScrollController.offset != 0.0) {
      _verticalScrollController.jumpTo(0.0);
    }
  }

  void _handleAccessoryKey(String key) {
    if (key == '⌨️' || key == 'Hide ⌨️' || key == 'Dismiss') {
      if (_terminalFocusNode.hasFocus ||
          MediaQuery.of(context).viewInsets.bottom > 0) {
        _terminalFocusNode.unfocus();
        FocusScope.of(context).unfocus();
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (_verticalScrollController.hasClients) {
            _verticalScrollController.jumpTo(0.0);
          }
        });
      } else {
        _terminalFocusNode.requestFocus();
      }
      return;
    }

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
      case '⌫':
      case 'Del':
      case 'Backspace':
        _channel!.sink.add('\x7f');
        break;
      case 'Ctrl+C':
        _channel!.sink.add('\x03');
        break;
      case 'Ctrl+D':
        _channel!.sink.add('\x04');
        break;
      case 'Ctrl+B':
        _channel!.sink.add('\x02');
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
      case 'PgUp':
        _sendScroll('page_up');
        if (!_showScrollToBottom) {
          setState(() {
            _showScrollToBottom = true;
          });
        }
        break;
      case 'PgDn':
        _sendScroll('page_down');
        break;
      case 'Home':
        _channel!.sink.add('\x1b[H');
        break;
      case 'End':
        _channel!.sink.add('\x1b[F');
        break;
      case 'Enter':
        _channel!.sink.add('\r');
        break;
      default:
        _channel!.sink.add(key);
    }
  }

  void _handlePointerDown(PointerDownEvent event) {
    if (_activePointerId != null) return;
    _activePointerId = event.pointer;
    _pointerDownPos = event.position;
    _lastPointerTimeStamp = event.timeStamp;
    _lastPointerVelocityY = 0.0;
    _isPointerVerticalScroll = false;
    _dragScrollAccumulator = 0.0;
  }

  void _handlePointerMove(PointerMoveEvent event) {
    if (event.pointer != _activePointerId || _pointerDownPos == null) return;

    if (!_isPointerVerticalScroll) {
      final totalDx = (event.position.dx - _pointerDownPos!.dx).abs();
      final totalDy = (event.position.dy - _pointerDownPos!.dy).abs();
      if (totalDy >= 6.0 && totalDy > totalDx) {
        _isPointerVerticalScroll = true;
      } else if (totalDx > 10.0 && totalDx > totalDy) {
        _activePointerId = null;
        return;
      } else {
        return;
      }
    }

    if (_lastPointerTimeStamp != null) {
      final dtMicros =
          (event.timeStamp - _lastPointerTimeStamp!).inMicroseconds;
      if (dtMicros > 0) {
        _lastPointerVelocityY = (event.delta.dy * 1000000.0) / dtMicros;
      }
    }
    _lastPointerTimeStamp = event.timeStamp;

    _applyVerticalScrollDelta(event.delta.dy);
  }

  void _handlePointerUpOrCancel(PointerEvent event) {
    if (event.pointer != _activePointerId) return;
    if (_isPointerVerticalScroll && _lastPointerVelocityY.abs() > 800.0) {
      if (_lastPointerVelocityY > 0) {
        _sendScroll('page_up');
        if (!_showScrollToBottom) {
          setState(() {
            _showScrollToBottom = true;
          });
        }
      } else {
        _sendScroll('page_down');
      }
    }
    _activePointerId = null;
    _pointerDownPos = null;
    _lastPointerTimeStamp = null;
    _lastPointerVelocityY = 0.0;
    _isPointerVerticalScroll = false;
    _dragScrollAccumulator = 0.0;
  }

  void _applyVerticalScrollDelta(double dy) {
    _dragScrollAccumulator += dy;

    // Approximately ~14px per terminal row scroll
    const lineDelta = 14.0;
    if (_dragScrollAccumulator.abs() >= lineDelta) {
      final rows = (_dragScrollAccumulator / lineDelta).truncate();
      _dragScrollAccumulator -= rows * lineDelta;

      if (rows > 0) {
        // Swiping finger DOWN -> Scroll UP in tmux history
        _sendScroll('up', lines: rows);
        if (!_showScrollToBottom) {
          setState(() {
            _showScrollToBottom = true;
          });
        }
      } else if (rows < 0) {
        // Swiping finger UP -> Scroll DOWN towards live prompt
        _sendScroll('down', lines: -rows);
      }
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
    WidgetsBinding.instance.removeObserver(this);
    _verticalScrollController.removeListener(_onVerticalScroll);
    _verticalScrollController.dispose();
    _horizontalScrollController.dispose();
    _terminalController.dispose();
    _terminalFocusNode.dispose();
    _pingTimer?.cancel();
    _sub?.cancel();
    _channel?.sink.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    ref.watch(fleetSessionsProvider);
    final liveSession = _resolveLiveSession();
    final keyboardOpen = MediaQuery.of(context).viewInsets.bottom > 0 ||
        _terminalFocusNode.hasFocus;

    return Scaffold(
      backgroundColor: AppColors.terminalBlack,
      appBar: AppBar(
        backgroundColor: AppColors.surface,
        elevation: 0,
        leading: IconButton(
          icon: const Icon(Icons.arrow_back_ios_new_rounded,
              size: 18, color: AppColors.textPrimary),
          onPressed: () => Navigator.of(context).pop(),
        ),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.terminal_rounded,
                    size: 16, color: AppColors.infoCyan),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    liveSession.displayTitle,
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
              '${liveSession.agentDisplayName} @ ${liveSession.hostTag} • ${_currentCols}x$_currentRows',
              style:
                  AppTypography.codeXs.copyWith(color: AppColors.textSecondary),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: [
          // Keyboard toggle button (shows/hides on-screen keyboard)
          IconButton(
            icon: Icon(
              keyboardOpen
                  ? Icons.keyboard_hide_rounded
                  : Icons.keyboard_rounded,
              size: 20,
              color:
                  keyboardOpen ? AppColors.infoCyan : AppColors.textSecondary,
            ),
            tooltip: keyboardOpen ? 'Hide Keyboard' : 'Show Keyboard',
            onPressed: () {
              if (keyboardOpen) {
                _terminalFocusNode.unfocus();
                FocusScope.of(context).unfocus();
                WidgetsBinding.instance.addPostFrameCallback((_) {
                  if (_verticalScrollController.hasClients) {
                    _verticalScrollController.jumpTo(0.0);
                  }
                });
              } else {
                _terminalFocusNode.requestFocus();
              }
            },
          ),

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
                color: _autoFit
                    ? AppColors.infoCyan.withOpacity(0.15)
                    : AppColors.surfaceHighlight,
                borderRadius: BorderRadius.circular(4),
                border: Border.all(
                  color:
                      _autoFit ? AppColors.infoCyan : AppColors.outlineSubtle,
                  width: 0.8,
                ),
              ),
              child: Text(
                _autoFit ? 'FIT' : '80 COL',
                style: AppTypography.codeXs.copyWith(
                  color:
                      _autoFit ? AppColors.infoCyan : AppColors.textSecondary,
                  fontWeight: FontWeight.w700,
                  fontSize: 10,
                ),
              ),
            ),
          ),

          // Tmux scroll mode helper
          IconButton(
            icon: const Icon(Icons.history_rounded,
                size: 18, color: AppColors.textSecondary),
            tooltip: 'Tmux Scrollback Mode (Ctrl+B [)',
            onPressed: () {
              _channel?.sink.add('\x02[');
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(
                  content: Text(
                      'Tmux Scroll Mode active. Swipe or use ↑/↓/PgUp to scroll, Esc to exit.'),
                  duration: Duration(seconds: 2),
                ),
              );
            },
          ),

          // Zoom Out
          IconButton(
            icon: const Icon(Icons.remove_rounded,
                size: 18, color: AppColors.textSecondary),
            tooltip: 'Decrease font size',
            onPressed: _zoomOut,
          ),

          // Zoom In
          IconButton(
            icon: const Icon(Icons.add_rounded,
                size: 18, color: AppColors.textSecondary),
            tooltip: 'Increase font size',
            onPressed: _zoomIn,
          ),

          // Reconnect
          IconButton(
            icon: const Icon(Icons.refresh_rounded,
                size: 20, color: AppColors.textMuted),
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
                    const Icon(
                      Icons.sync_rounded,
                      size: 14,
                      color: AppColors.infoCyan,
                    ),
                    const SizedBox(width: 8),
                    Text(
                      'Connecting to tmux session...',
                      style: AppTypography.codeXs
                          .copyWith(color: AppColors.infoCyan),
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
                  style: AppTypography.codeXs
                      .copyWith(color: AppColors.statusCoral),
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
                        : max(constraints.maxWidth,
                            _currentCols * charWidth + 16.0);

                    return Listener(
                      onPointerDown: _handlePointerDown,
                      onPointerMove: _handlePointerMove,
                      onPointerUp: _handlePointerUpOrCancel,
                      onPointerCancel: _handlePointerUpOrCancel,
                      behavior: HitTestBehavior.translucent,
                      child: GestureDetector(
                        onTap: () {
                          if (!_terminalFocusNode.hasFocus) {
                            _terminalFocusNode.requestFocus();
                          }
                        },
                        behavior: HitTestBehavior.translucent,
                        child: Stack(
                          children: [
                            Scrollbar(
                              controller: _verticalScrollController,
                              thumbVisibility: true,
                              trackVisibility: false,
                              child: Scrollbar(
                                controller: _horizontalScrollController,
                                thumbVisibility: !_autoFit,
                                trackVisibility: !_autoFit,
                                notificationPredicate: (notif) =>
                                    notif.metrics.axis == Axis.horizontal,
                                child: SingleChildScrollView(
                                  controller: _horizontalScrollController,
                                  scrollDirection: Axis.horizontal,
                                  physics: const BouncingScrollPhysics(),
                                  child: SizedBox(
                                    width: terminalRenderWidth,
                                    height: constraints.maxHeight,
                                    child: TerminalView(
                                      _terminal,
                                      controller: _terminalController,
                                      scrollController:
                                          _verticalScrollController,
                                      focusNode: _terminalFocusNode,
                                      autofocus:
                                          false, // Clean startup: keyboard hidden by default
                                      autoResize: false,
                                      deleteDetection:
                                          true, // Fix for iOS backspace/delete
                                      keyboardType: TextInputType.text,
                                      keyboardAppearance: Brightness.dark,
                                      backgroundOpacity: 0.0,
                                      textStyle: TerminalStyle(
                                        fontSize: _fontSize,
                                        fontFamily: 'JetBrains Mono',
                                      ),
                                    ),
                                  ),
                                ),
                              ),
                            ),
                            if (_showScrollToBottom)
                              Positioned(
                                bottom: 8,
                                right: 16,
                                child: Material(
                                  color: AppColors.surfaceHighlight
                                      .withOpacity(0.9),
                                  borderRadius: BorderRadius.circular(20),
                                  elevation: 4,
                                  child: InkWell(
                                    borderRadius: BorderRadius.circular(20),
                                    onTap: _scrollToBottom,
                                    child: Padding(
                                      padding: const EdgeInsets.symmetric(
                                          horizontal: 10, vertical: 6),
                                      child: Row(
                                        mainAxisSize: MainAxisSize.min,
                                        children: [
                                          const Icon(
                                              Icons.arrow_downward_rounded,
                                              size: 14,
                                              color: AppColors.infoCyan),
                                          const SizedBox(width: 4),
                                          Text(
                                            'Bottom',
                                            style:
                                                AppTypography.codeXs.copyWith(
                                              color: AppColors.infoCyan,
                                              fontWeight: FontWeight.w600,
                                              fontSize: 11,
                                            ),
                                          ),
                                        ],
                                      ),
                                    ),
                                  ),
                                ),
                              ),
                          ],
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
