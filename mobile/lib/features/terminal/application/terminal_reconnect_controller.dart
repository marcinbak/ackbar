import 'dart:async';
import 'dart:math';
import 'package:flutter/foundation.dart';

enum TerminalReconnectPhase {
  connecting,
  connected,
  reconnecting,
  exhausted,
  ended,
  disposed,
}

class TerminalReconnectController extends ChangeNotifier {
  final VoidCallback onReconnect;
  final List<Duration> backoffSchedule;
  final int maxAutoAttempts;

  TerminalReconnectPhase _phase = TerminalReconnectPhase.connecting;
  int _attempt = 0;
  Timer? _reconnectTimer;

  TerminalReconnectController({
    required this.onReconnect,
    this.backoffSchedule = const [
      Duration(seconds: 1),
      Duration(seconds: 2),
      Duration(seconds: 5),
      Duration(seconds: 5),
      Duration(seconds: 5),
    ],
    this.maxAutoAttempts = 5,
  });

  TerminalReconnectPhase get phase => _phase;
  int get attempt => _attempt;
  bool get isConnected => _phase == TerminalReconnectPhase.connected;
  bool get isReconnecting => _phase == TerminalReconnectPhase.reconnecting;
  bool get isExhausted => _phase == TerminalReconnectPhase.exhausted;
  bool get isEnded => _phase == TerminalReconnectPhase.ended;

  void onConnecting() {
    if (_phase == TerminalReconnectPhase.disposed ||
        _phase == TerminalReconnectPhase.ended) {
      return;
    }
    _phase = TerminalReconnectPhase.connecting;
    notifyListeners();
  }

  void onFrameReceived() {
    if (_phase == TerminalReconnectPhase.disposed ||
        _phase == TerminalReconnectPhase.ended) {
      return;
    }
    _attempt = 0;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    if (_phase != TerminalReconnectPhase.connected) {
      _phase = TerminalReconnectPhase.connected;
      notifyListeners();
    }
  }

  void onDisconnected({String? reason, bool isEnded = false}) {
    if (_phase == TerminalReconnectPhase.disposed ||
        _phase == TerminalReconnectPhase.ended) {
      return;
    }

    if (isEnded ||
        (reason != null && reason.contains('[Session was deleted]'))) {
      _phase = TerminalReconnectPhase.ended;
      _reconnectTimer?.cancel();
      _reconnectTimer = null;
      notifyListeners();
      return;
    }

    if (_attempt >= maxAutoAttempts) {
      _phase = TerminalReconnectPhase.exhausted;
      _reconnectTimer?.cancel();
      _reconnectTimer = null;
      notifyListeners();
      return;
    }

    _phase = TerminalReconnectPhase.reconnecting;
    final delay = backoffSchedule[min(_attempt, backoffSchedule.length - 1)];
    _attempt++;
    notifyListeners();

    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(delay, () {
      if (_phase == TerminalReconnectPhase.reconnecting) {
        _phase = TerminalReconnectPhase.connecting;
        notifyListeners();
        onReconnect();
      }
    });
  }

  void retryNow() {
    if (_phase == TerminalReconnectPhase.disposed ||
        _phase == TerminalReconnectPhase.ended) {
      return;
    }
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _attempt = 0;
    _phase = TerminalReconnectPhase.connecting;
    notifyListeners();
    onReconnect();
  }

  void onAppResumed() {
    if (_phase == TerminalReconnectPhase.disposed ||
        _phase == TerminalReconnectPhase.ended) {
      return;
    }
    if (_phase != TerminalReconnectPhase.connected) {
      retryNow();
    }
  }

  void markEnded() {
    if (_phase == TerminalReconnectPhase.ended ||
        _phase == TerminalReconnectPhase.disposed) {
      return;
    }
    _phase = TerminalReconnectPhase.ended;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    notifyListeners();
  }

  @override
  void dispose() {
    _phase = TerminalReconnectPhase.disposed;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    super.dispose();
  }
}
