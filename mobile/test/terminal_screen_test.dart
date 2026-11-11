import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:xterm/xterm.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/terminal/application/terminal_reconnect_controller.dart';
import 'package:ackbar_mobile/features/terminal/presentation/terminal_screen.dart';

class _FakeWebSocketSink implements WebSocketSink {
  final List<dynamic> sentMessages;
  bool closed = false;

  _FakeWebSocketSink(this.sentMessages);

  @override
  void add(dynamic data) {
    sentMessages.add(data);
  }

  @override
  void addError(Object error, [StackTrace? stackTrace]) {}

  @override
  Future addStream(Stream stream) async {
    await for (final item in stream) {
      sentMessages.add(item);
    }
  }

  @override
  Future close([int? closeCode, String? closeReason]) async {
    closed = true;
  }

  @override
  Future get done => Future.value();
}

class _FakeWebSocketChannel implements WebSocketChannel {
  final StreamController<dynamic> serverToClient =
      StreamController<dynamic>.broadcast();
  final List<dynamic> sentMessages = [];
  late final _FakeWebSocketSink _sink = _FakeWebSocketSink(sentMessages);

  @override
  Stream get stream => serverToClient.stream;

  @override
  WebSocketSink get sink => _sink;

  @override
  int? get closeCode => null;

  @override
  String? get closeReason => null;

  @override
  String? get protocol => null;

  @override
  Future<void> get ready => Future.value();

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

Session _buildSession({SessionState state = SessionState.working}) {
  final now = DateTime.utc(2026, 10, 31, 12, 0, 0);
  return Session(
    id: 'claude-code:local:sess-term-1',
    agent: 'claude-code',
    host: 'local',
    nativeId: 'sess-term-1',
    cwd: '/workspace/ackbar',
    state: state,
    startedAt: now,
    lastEventAt: now,
    managed: true,
    tmuxName: 'ackbar-claude-sess-term-1',
    name: 'Terminal Scroll Test',
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  testWidgets(
      'TerminalScreen vertical touch drag and PgUp/PgDn/Bottom controls dispatch tmux scroll JSON and accessory keys do not double-send',
      (tester) async {
    final fakeChannel = _FakeWebSocketChannel();
    addTearDown(() => fakeChannel.serverToClient.close());

    final session = _buildSession();

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          theme: AppTheme.darkTheme,
          home: TerminalScreen(
            session: session,
            channelBuilder: (_) => fakeChannel,
          ),
        ),
      ),
    );
    await tester.pump();

    // Clear initial resize message
    fakeChannel.sentMessages.clear();

    // 1. Vertical drag DOWN on TerminalView -> scrolls UP in tmux history and shows 'Bottom' pill
    await tester.drag(find.byType(TerminalView), const Offset(0, 80));
    await tester.pump();

    final scrollUpMsgs = fakeChannel.sentMessages.where((m) {
      if (m is! String || !m.startsWith('{')) return false;
      final decoded = jsonDecode(m) as Map<String, dynamic>;
      return decoded['type'] == 'scroll' && decoded['direction'] == 'up';
    }).toList();
    expect(scrollUpMsgs, isNotEmpty);
    expect(find.text('Bottom'), findsOneWidget);

    // 2. Tap 'Bottom' pill -> sends scroll bottom and hides 'Bottom' pill
    fakeChannel.sentMessages.clear();
    await tester.tap(find.text('Bottom'));
    await tester.pump();

    expect(
      fakeChannel.sentMessages,
      contains(
          jsonEncode({'type': 'scroll', 'direction': 'bottom', 'lines': 1})),
    );
    expect(find.text('Bottom'), findsNothing);

    // 3. Scroll accessory bar to reveal PgUp / PgDn and verify they send scroll control messages
    fakeChannel.sentMessages.clear();
    await tester.drag(
        find.byType(SingleChildScrollView).last, const Offset(-300, 0));
    await tester.pump();

    await tester.tap(find.text('PgUp'));
    await tester.pump();
    expect(
      fakeChannel.sentMessages,
      contains(
          jsonEncode({'type': 'scroll', 'direction': 'page_up', 'lines': 1})),
    );
    expect(find.text('Bottom'), findsOneWidget);

    fakeChannel.sentMessages.clear();
    await tester.tap(find.text('PgDn'));
    await tester.pump();
    expect(
      fakeChannel.sentMessages,
      contains(
          jsonEncode({'type': 'scroll', 'direction': 'page_down', 'lines': 1})),
    );

    // 4. Verify accessory keys (e.g. ↑) send escape sequence once, not twice
    await tester.drag(
        find.byType(SingleChildScrollView).last, const Offset(300, 0));
    await tester.pump();
    fakeChannel.sentMessages.clear();

    await tester.tap(find.text('↑'));
    await tester.pump();
    expect(fakeChannel.sentMessages, equals(['\x1b[A']));
  });

  testWidgets(
      'TerminalScreen displays reconnecting banner on websocket error and reconnects automatically',
      (tester) async {
    var channelCount = 0;
    _FakeWebSocketChannel? lastChannel;

    WebSocketChannel channelBuilder(Uri uri) {
      channelCount++;
      final ch = _FakeWebSocketChannel();
      lastChannel = ch;
      return ch;
    }

    final session = _buildSession();

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          theme: AppTheme.darkTheme,
          home: TerminalScreen(
            session: session,
            channelBuilder: channelBuilder,
          ),
        ),
      ),
    );
    await tester.pump();
    expect(channelCount, 1);

    // Simulate websocket disconnect/error
    lastChannel!.serverToClient.addError('Connection dropped');
    await tester.pump();

    expect(
        find.textContaining('Reconnecting (attempt 1/5)...'), findsOneWidget);

    // Advance 1s to allow backoff timer to fire
    await tester.pump(const Duration(seconds: 2));
    await tester.pump();

    expect(channelCount, 2);

    // First frame arrives on reconnected channel: verifies force resize sent and banner cleared
    lastChannel!.serverToClient.add('welcome back\r\n');
    await tester.pump();
    expect(
      lastChannel!.sentMessages
          .any((m) => m is String && m.contains('"type":"resize"')),
      isTrue,
    );
    expect(find.textContaining('Reconnecting'), findsNothing);
  });

  testWidgets(
      'TerminalScreen displays [Tap to Reconnect] when max attempts reached, and tapping it reconnects',
      (tester) async {
    var channelCount = 0;
    _FakeWebSocketChannel? currentChannel;
    WebSocketChannel channelBuilder(Uri uri) {
      channelCount++;
      final ch = _FakeWebSocketChannel();
      currentChannel = ch;
      return ch;
    }

    final session = _buildSession();

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          theme: AppTheme.darkTheme,
          home: TerminalScreen(
            session: session,
            channelBuilder: channelBuilder,
          ),
        ),
      ),
    );
    await tester.pump();

    // Trigger disconnects until max attempts exhausted
    for (var i = 1; i <= 6; i++) {
      currentChannel?.serverToClient.addError('Drop $i');
      await tester.pump();
      await tester.pump(const Duration(seconds: 6));
    }

    expect(find.text('[Tap to Reconnect]'), findsOneWidget);

    // Tap to reconnect
    await tester.tap(find.text('[Tap to Reconnect]'));
    await tester.pump();

    expect(channelCount, greaterThan(5));
  });

  testWidgets('TerminalScreen suppresses auto-reconnect when session is ended',
      (tester) async {
    var channelCount = 0;
    _FakeWebSocketChannel? currentChannel;
    WebSocketChannel channelBuilder(Uri uri) {
      channelCount++;
      final ch = _FakeWebSocketChannel();
      currentChannel = ch;
      return ch;
    }

    final endedSession = _buildSession(state: SessionState.ended);

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          theme: AppTheme.darkTheme,
          home: TerminalScreen(
            session: endedSession,
            channelBuilder: channelBuilder,
          ),
        ),
      ),
    );
    await tester.pump();

    currentChannel!.serverToClient.addError('Ended socket drop');
    await tester.pump();
    await tester.pump(const Duration(seconds: 3));

    expect(channelCount, 1);
    expect(find.textContaining('Reconnecting'), findsNothing);
  });
}
