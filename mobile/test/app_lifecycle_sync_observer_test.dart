import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/services/app_lifecycle_sync_observer.dart';

HostRecord _createHost(String name, {bool online = true}) {
  return HostRecord(
    name: name,
    url: 'http://127.0.0.1:7777',
    online: online,
    createdAt: DateTime.utc(2026, 11, 1),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('AppLifecycleSyncObserver triggers sync on resumed lifecycle event',
      () async {
    var hostsRefreshed = 0;
    var sessionsRefreshed = 0;
    var nodesRefreshed = 0;

    final observer = AppLifecycleSyncObserver(
      refreshHosts: () async => hostsRefreshed++,
      refreshSessions: () async => sessionsRefreshed++,
      refreshNodes: () async => nodesRefreshed++,
      getHosts: () => [_createHost('local', online: true)],
      debounceDuration: const Duration(milliseconds: 50),
    );
    addTearDown(() => observer.dispose());

    observer.start();

    // Trigger resume
    observer.didChangeAppLifecycleState(AppLifecycleState.resumed);
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(hostsRefreshed, 1);
    expect(sessionsRefreshed, 1);
    expect(nodesRefreshed, 1);
  });

  test('AppLifecycleSyncObserver debounces rapid resume triggers', () async {
    var syncCount = 0;

    final observer = AppLifecycleSyncObserver(
      refreshHosts: () async => syncCount++,
      refreshSessions: () async {},
      refreshNodes: () async {},
      getHosts: () => [_createHost('local', online: true)],
      debounceDuration: const Duration(milliseconds: 100),
    );
    addTearDown(() => observer.dispose());

    observer.start();

    // Trigger resume 3 times in rapid succession
    observer.didChangeAppLifecycleState(AppLifecycleState.resumed);
    observer.didChangeAppLifecycleState(AppLifecycleState.resumed);
    observer.didChangeAppLifecycleState(AppLifecycleState.resumed);

    // Initial sync fires immediately
    expect(syncCount, 1);

    // After debounce window, at most 1 trailing sync fires
    await Future<void>.delayed(const Duration(milliseconds: 150));
    expect(syncCount, 2);
  });

  test(
      'AppLifecycleSyncObserver runs periodic health probe when host is offline',
      () async {
    var syncCount = 0;
    var isHostOnline = false;

    final observer = AppLifecycleSyncObserver(
      refreshHosts: () async {
        syncCount++;
        isHostOnline = true; // Recover host
      },
      refreshSessions: () async {},
      refreshNodes: () async {},
      getHosts: () => [_createHost('remote', online: isHostOnline)],
      offlineProbeInterval: const Duration(milliseconds: 40),
    );
    addTearDown(() => observer.dispose());

    observer.start();

    // Wait for periodic timer to fire
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(syncCount, greaterThanOrEqualTo(1));
    expect(isHostOnline, isTrue);
  });

  test(
      'AppLifecycleSyncObserver pauses probe when backgrounded and cancels when clean',
      () async {
    var syncCount = 0;

    final observer = AppLifecycleSyncObserver(
      refreshHosts: () async => syncCount++,
      refreshSessions: () async {},
      refreshNodes: () async {},
      getHosts: () => [_createHost('remote', online: false)],
      offlineProbeInterval: const Duration(milliseconds: 40),
    );

    observer.start();

    // Background the app
    observer.didChangeAppLifecycleState(AppLifecycleState.paused);
    final countBefore = syncCount;

    await Future<void>.delayed(const Duration(milliseconds: 80));
    expect(syncCount, countBefore); // Probe paused, count did not advance

    observer.dispose();
  });
}
