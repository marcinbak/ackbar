import 'dart:async';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/host.dart';
import '../providers/fleet_providers.dart';

/// Monitors global app lifecycle to trigger foreground resume sync and periodic health recovery.
class AppLifecycleSyncObserver with WidgetsBindingObserver {
  final Future<void> Function() refreshHosts;
  final Future<void> Function() refreshSessions;
  final Future<void> Function() refreshNodes;
  final List<HostRecord> Function() getHosts;
  final Duration debounceDuration;
  final Duration offlineProbeInterval;
  final WidgetsBinding? binding;

  Timer? _debounceTimer;
  Timer? _probeTimer;
  DateTime? _lastSyncTime;
  bool _isForeground = true;
  bool _disposed = false;

  AppLifecycleSyncObserver({
    required this.refreshHosts,
    required this.refreshSessions,
    required this.refreshNodes,
    required this.getHosts,
    this.debounceDuration = const Duration(seconds: 2),
    this.offlineProbeInterval = const Duration(seconds: 30),
    this.binding,
  });

  void start() {
    final b = binding ?? WidgetsBinding.instance;
    b.addObserver(this);
    checkProbeTimer();
  }

  void dispose() {
    _disposed = true;
    _debounceTimer?.cancel();
    _probeTimer?.cancel();
    final b = binding ?? WidgetsBinding.instance;
    b.removeObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _isForeground = true;
      triggerResumeSync();
      checkProbeTimer();
    } else {
      _isForeground = false;
      _probeTimer?.cancel();
      _probeTimer = null;
    }
  }

  /// Triggers a foreground resume sync, debounced by [debounceDuration].
  void triggerResumeSync() {
    if (_disposed) return;
    final now = DateTime.now();
    if (_lastSyncTime == null ||
        now.difference(_lastSyncTime!) >= debounceDuration) {
      _debounceTimer?.cancel();
      _debounceTimer = null;
      syncAll();
    } else if (_debounceTimer == null || !_debounceTimer!.isActive) {
      final remaining = debounceDuration - now.difference(_lastSyncTime!);
      _debounceTimer =
          Timer(remaining > Duration.zero ? remaining : Duration.zero, () {
        if (!_disposed && _isForeground) {
          syncAll();
        }
      });
    }
  }

  /// Refreshes hosts, sessions, and nodes in sequence.
  Future<void> syncAll() async {
    if (_disposed) return;
    _lastSyncTime = DateTime.now();
    try {
      await refreshHosts();
      if (_disposed) return;
      await Future.wait([
        refreshSessions(),
        refreshNodes(),
      ]);
    } catch (_) {}
    if (!_disposed) {
      checkProbeTimer();
    }
  }

  /// Starts or cancels the 30s offline health probe timer depending on host states.
  void checkProbeTimer() {
    if (_disposed || !_isForeground) {
      _probeTimer?.cancel();
      _probeTimer = null;
      return;
    }

    final hosts = getHosts();
    final hasOffline = hosts.any((h) => !h.online);

    if (hasOffline && (_probeTimer == null || !_probeTimer!.isActive)) {
      _probeTimer = Timer.periodic(offlineProbeInterval, (_) async {
        if (_disposed || !_isForeground) {
          _probeTimer?.cancel();
          _probeTimer = null;
          return;
        }
        await syncAll();
      });
    } else if (!hasOffline) {
      _probeTimer?.cancel();
      _probeTimer = null;
    }
  }
}

final appLifecycleSyncObserverProvider =
    Provider.autoDispose<AppLifecycleSyncObserver>((ref) {
  final observer = AppLifecycleSyncObserver(
    refreshHosts: () => ref.read(hostsListProvider.notifier).refreshHosts(),
    refreshSessions: () =>
        ref.read(fleetSessionsProvider.notifier).refreshSessions(),
    refreshNodes: () => ref.read(fleetNodesProvider.notifier).refreshNodes(),
    getHosts: () => ref.read(hostsListProvider),
  );
  observer.start();
  ref.listen<List<HostRecord>>(hostsListProvider, (_, __) {
    observer.checkProbeTimer();
  });
  ref.onDispose(() => observer.dispose());
  return observer;
});
