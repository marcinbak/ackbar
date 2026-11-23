import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:ackbar_mobile/core/layout/responsive_breakpoints.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/navigation/session_navigator.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/providers/tablet_workspace_provider.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/attention/presentation/attention_screen.dart';
import 'package:ackbar_mobile/features/attention/presentation/tablet_attention_detail_pane.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/fleet/presentation/fleet_empty_workspace.dart';
import 'package:ackbar_mobile/features/fleet/presentation/tablet_fleet_workspace.dart';
import 'package:ackbar_mobile/features/main_navigation_shell.dart';
import 'package:ackbar_mobile/features/terminal/presentation/terminal_screen.dart';

class _SSE extends SSEClient {
  @override
  Stream<Session> subscribeMultipleHosts(List<HostRecord> h,
          {bool includeOffline = false,
          void Function(HostRecord)? onHostReconnected,
          bool? includeArchived,
          bool? includeDone}) =>
      const Stream.empty();
}

class _Hosts extends StateNotifier<List<HostRecord>> implements HostsNotifier {
  _Hosts(super.i);
  @override
  Future<void> refreshHosts() async {}
  @override
  dynamic noSuchMethod(Invocation i) => null;
}

final _t0 = DateTime.utc(2026, 11, 20, 12);
Session _s(String id, String name,
        {String h = 'mac.local',
        SessionState st = SessionState.idle,
        BlockKind? b,
        bool u = false,
        String old = ''}) =>
    Session(
      id: id,
      agent: 'claude-code',
      host: h,
      nativeId: id,
      cwd: '/work',
      name: name,
      state: st,
      startedAt: _t0,
      lastEventAt: _t0,
      managed: true,
      engineType: 'headless',
      blocked: b != null ? Blocked(kind: b, reason: 'R', since: _t0) : null,
      isUnread: u,
      oldId: old,
    );

void _vp(WidgetTester t, Size sz, {double inset = 0}) {
  t.view
    ..devicePixelRatio = 1.0
    ..physicalSize = sz
    ..viewInsets = FakeViewPadding(bottom: inset);
  addTearDown(() => t.view
    ..resetPhysicalSize()
    ..resetDevicePixelRatio()
    ..resetViewInsets());
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;
  Completer<bool>? respondGate, spawnGate;
  var sessions = <Session>[];
  late ApiClient api;
  final sse = _SSE();
  final rotB = _s('sess-rot-b', 'Rotated B');

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    respondGate = spawnGate = null;
    sessions = [_s('sess-a', 'Alpha'), _s('sess-b', 'Beta')];
    api = ApiClient(client: MockClient((req) async {
      final p = req.url.path;
      if (p == '/v1/sessions/respond') {
        final ok = await (respondGate?.future ?? Future.value(true));
        return http.Response('{"ok":$ok}', ok ? 200 : 500);
      }
      if (p == '/v1/sessions/spawn') {
        await (spawnGate?.future ?? Future.value(true));
        return http.Response('{"ok":true,"session_id":"sup-1"}', 200);
      }
      return http.Response(
          p == '/v1/sessions'
              ? jsonEncode(sessions)
              : p == '/v1/sessions/take-wheel'
                  ? '{"ok":true,"tmux_name":"ackbar-auth"}'
                  : p.endsWith('/clear')
                      ? jsonEncode({'ok': true, 'session': rotB.toJson()})
                      : p == '/v1/sessions/transcript'
                          ? '{"session_id":"sess-a","native_id":"sess-a","agent":"claude-code","title":"S","cwd":"/work","messages":[],"markdown":""}'
                          : '{}',
          200);
    }));
  });
  tearDown(() => api.dispose());

  ProviderContainer mk(List<Session> seed, {String host = 'mac.local'}) {
    sessions = seed;
    final h = HostRecord(
        name: host, url: 'http://127.0.0.1:7777', online: true, createdAt: _t0);
    final c = ProviderContainer(overrides: [
      apiClientProvider.overrideWithValue(api),
      sseClientProvider.overrideWithValue(sse),
      hostsListProvider.overrideWith((_) => _Hosts([h]))
    ]);
    addTearDown(c.dispose);
    return c..read(fleetSessionsProvider.notifier).setSessions(seed);
  }

  Future<void> show(WidgetTester t, Widget w, ProviderContainer c,
      [int ms = 200]) async {
    await t.pumpWidget(UncontrolledProviderScope(
        container: c, child: MaterialApp(theme: AppTheme.darkTheme, home: w)));
    await t.pump(Duration(milliseconds: ms));
  }

  Finder k(String id) => find.byKey(Key(id));
  Future<void> tap(WidgetTester t, Finder f, [int ms = 0]) async {
    await t.tap(f);
    await (ms < 0 ? t.pumpAndSettle() : t.pump(Duration(milliseconds: ms)));
  }

  testWidgets('HeadlessAuthCard terminal + SessionNavigator null ref',
      (t) async {
    _vp(t, const Size(1200, 800));
    final c = mk(
        [_s('sess-auth', 'Auth', st: SessionState.blocked, b: BlockKind.auth)])
      ..read(mainTabNavIndexProvider.notifier).state = 1;
    await show(t, const MainNavigationShell(), c, 300);
    await tap(
        t, find.byKey(const ValueKey('attention_auth_terminal_btn')), 300);
    expect(find.byType(MainNavigationShell), findsOneWidget);
    expect(c.read(mainTabNavIndexProvider), 0);
    expect(c.read(selectedTabletSessionProvider)?.isHeadless, isFalse);
    expect(find.byType(TerminalScreen), findsOneWidget);

    final unread = _s('sess-u', 'Unread', u: true);
    final c2 = mk([unread])..read(mainTabNavIndexProvider.notifier).state = 2;
    late BuildContext ctx;
    await show(t, Scaffold(body: Builder(builder: (x) {
      ctx = x;
      return const SizedBox();
    })), c2, 0);
    Navigator.of(ctx).push(MaterialPageRoute(builder: (_) => const SizedBox()));
    await t.pumpAndSettle();
    await SessionNavigator.openSession(ctx, null, unread);
    await t.pumpAndSettle();
    expect(c2.read(mainTabNavIndexProvider), 0);
    expect(c2.read(selectedTabletSessionProvider)?.id, 'sess-u');
    expect(c2.read(fleetSessionsProvider).first.isUnread, isFalse);
    expect(find.byType(ChatScreen), findsNothing);
  });

  testWidgets('Attention split threshold, row ellipsis, and draft isolation',
      (t) async {
    const longH = 'macbook-pro-extremely-long-hostname.tailnet.ts';
    final b1 = _s('blk-1', 'Blocked One',
        st: SessionState.blocked, b: BlockKind.question);
    final b2 = _s('blk-2', 'Blocked Two',
        st: SessionState.blocked, b: BlockKind.question);
    final c = mk([b1, b2]);
    _vp(t, const Size(660, 1024));
    await show(t, const AttentionScreen(), c);
    expect(k('tablet_attention_split_view'), findsNothing);
    _vp(t, const Size(720, 1024));
    await show(t, const AttentionScreen(), c);
    expect(k('tablet_attention_split_view'), findsOneWidget);

    String txt() =>
        t.widget<TextField>(find.byType(TextField)).controller!.text;
    await t.enterText(find.byType(TextField), 'reply A');
    await tap(t, find.text('Blocked Two').first);
    expect(txt(), isEmpty);
    await tap(t, find.text('Blocked One').first);
    expect(txt(), 'reply A');

    respondGate = Completer<bool>();
    await tap(t, find.text('Submit Choice'));
    await tap(t, find.text('Blocked Two').first);
    await t.enterText(find.byType(TextField), 'draft B');
    respondGate!.complete(false);
    await t.pump();
    expect(txt(), 'draft B');
    await tap(t, find.text('Blocked One').first);
    expect(txt(), 'reply A');

    final longB = _s('blk-l', 'Long Host',
        h: longH, st: SessionState.blocked, b: BlockKind.question);
    for (final idx in [0, 1]) {
      await show(
          t,
          Scaffold(
              body: SizedBox(
                  width: 320,
                  child: TabletAttentionDetailPane(
                      filterIndex: idx,
                      selectedPending: longB,
                      selectedInProgress: longB))),
          mk([longB], host: longH),
          0);
    }
  });

  testWidgets('portrait keyboard, drawer closes, mounted guard, /clear',
      (t) async {
    _vp(t, const Size(800, 1200), inset: 360);
    await show(t, Builder(builder: (ctx) {
      expect(ResponsiveBreakpoints.isKeyboardCompact(ctx), isFalse);
      return const SizedBox();
    }), mk([]), 0);

    _vp(t, const Size(800, 1200));
    final c = mk([_s('sess-a', 'Alpha'), _s('sess-b', 'Beta')]);
    c.read(selectedTabletSessionProvider.notifier).state = sessions.first;
    await show(t, const TabletFleetWorkspace(), c, 300);
    await tap(t, k('tablet_open_fleet_drawer_button'), -1);
    expect(k('tablet_portrait_fleet_drawer'), findsOneWidget);
    c.read(selectedTabletSessionProvider.notifier).state = sessions.last;
    await t.pumpAndSettle();
    expect(k('tablet_portrait_fleet_drawer'), findsNothing);
    await tap(t, k('tablet_open_fleet_drawer_button'), -1);
    await tap(t, k('tablet_fleet_new_session_button'), -1);
    expect(k('tablet_portrait_fleet_drawer'), findsNothing);
    expect(k('adaptive_side_sheet_container'), findsOneWidget);
    Navigator.of(t.element(k('adaptive_side_sheet_container'))).pop();
    await t.pumpAndSettle();

    _vp(t, const Size(1200, 800));
    c.read(selectedTabletSessionProvider.notifier).state = sessions.first;
    await show(t, const TabletFleetWorkspace(), c, 300);
    final before = t.state(find.byType(ChatScreen));
    await t.enterText(
        find.widgetWithText(TextField, 'Ask Claude Code...'), '/clear');
    await t.pump();
    await tap(t, find.byTooltip('Send prompt'), 300);
    expect(c.read(selectedTabletSessionProvider)?.id, 'sess-rot-b');
    expect(identical(before, t.state(find.byType(ChatScreen))), isTrue);
    expect(find.text('Conversation Rotated'), findsOneWidget);
    c.read(selectedTabletSessionProvider.notifier).state =
        _s('sess-rot-c', 'Adopted C', old: 'sess-rot-b');
    await t.pump(const Duration(milliseconds: 300));
    expect(identical(before, t.state(find.byType(ChatScreen))), isTrue);

    spawnGate = Completer<bool>();
    await show(t, const Scaffold(body: FleetEmptyWorkspace()), c, 0);
    await tap(t, find.text('New Supervisor'));
    await show(t, const SizedBox(), c, 0);
    spawnGate!.complete(true);
    await t.pump(const Duration(milliseconds: 50));
  });

  testWidgets(
      'non-embedded ChatScreen preserves rotated session across parent rebuilds',
      (t) async {
    _vp(t, const Size(390, 844));
    final c = mk([_s('sess-a', 'Alpha')]);
    late StateSetter rebuildParent;
    await show(t, StatefulBuilder(builder: (_, setState) {
      rebuildParent = setState;
      return ChatScreen(session: sessions.first);
    }), c, 300);
    await t.enterText(
        find.widgetWithText(TextField, 'Ask Claude Code...'), '/clear');
    await t.pump();
    await tap(t, find.byTooltip('Send prompt'), 300);
    expect(find.text('Conversation Rotated'), findsOneWidget);
    expect(find.text('Rotated B'), findsOneWidget);
    rebuildParent(() {});
    await t.pump(const Duration(milliseconds: 200));
    expect(find.text('Conversation Rotated'), findsOneWidget);
    expect(find.text('Rotated B'), findsOneWidget);
    expect(find.text('Alpha'), findsNothing);
  });
}
