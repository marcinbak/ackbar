import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:ackbar_mobile/core/models/host.dart';
import 'package:ackbar_mobile/core/models/session.dart';
import 'package:ackbar_mobile/core/network/api_client.dart';
import 'package:ackbar_mobile/core/network/sse_client.dart';
import 'package:ackbar_mobile/core/providers/fleet_providers.dart';
import 'package:ackbar_mobile/core/services/audio_companion_service.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/features/chat/presentation/chat_screen.dart';
import 'package:ackbar_mobile/features/transcript/presentation/transcript_screen.dart';

class MockSSEClient extends SSEClient {
  @override
  Stream<Session> subscribeHost(
    HostRecord host, {
    void Function(HostRecord host)? onHostReconnected,
  }) =>
      const Stream.empty();

  @override
  Stream<Session> subscribeMultipleHosts(
    List<HostRecord> hosts, {
    bool includeOffline = false,
    void Function(HostRecord host)? onHostReconnected,
  }) =>
      const Stream.empty();
}

Widget _wrapScreen(Widget child, ProviderContainer container) {
  return UncontrolledProviderScope(
    container: container,
    child: MaterialApp(theme: AppTheme.darkTheme, home: child),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  final testSession = Session(
    id: 'claude-code:mac.local:sess-speech-1',
    agent: 'claude-code',
    host: 'mac.local',
    nativeId: 'sess-speech-1',
    cwd: '/workspace/ackbar',
    name: 'Voice Companion Session',
    customTitle: 'Voice Companion Session',
    firstPrompt: 'Test Voice',
    lastPrompt: 'Test Voice',
    state: SessionState.idle,
    activity: 'Awaiting user input',
    startedAt: DateTime.now(),
    lastEventAt: DateTime.now(),
    managed: true,
    engineType: 'headless',
  );

  final List<HostRecord> testHosts = [
    HostRecord(
      name: 'mac.local',
      url: 'http://127.0.0.1:7777',
      online: true,
      createdAt: DateTime.now(),
    ),
  ];

  late ApiClient mockApiClient;
  late SSEClient mockSseClient;

  setUp(() {
    final mockClient = MockClient((request) async {
      if (request.url.path == '/v1/sessions/transcript') {
        return http.Response(
          jsonEncode({
            'session_id': testSession.id,
            'native_id': testSession.nativeId,
            'agent': testSession.agent,
            'title': testSession.displayTitle,
            'cwd': testSession.cwd,
            'messages': [
              {
                'role': 'user',
                'content': 'Run test suite',
                'timestamp': '2026-11-04T07:00:00Z',
              },
              {
                'role': 'assistant',
                'content':
                    'All test suites completed successfully with zero defects.',
                'timestamp': '2026-11-04T07:00:10Z',
              },
            ],
            'markdown': '',
          }),
          200,
        );
      }
      if (request.url.path == '/v1/sessions/subagents') {
        return http.Response(jsonEncode({'subagents': []}), 200);
      }
      if (request.url.path == '/v1/sessions/prompt') {
        return http.Response(jsonEncode({'status': 'sent'}), 200);
      }
      return http.Response('{}', 200);
    });
    mockApiClient = ApiClient(client: mockClient);
    mockSseClient = MockSSEClient();
  });

  tearDown(() {
    mockApiClient.dispose();
    mockSseClient.dispose();
  });

  group('Voice & Audio Companion Tests', () {
    testWidgets('toggles Auto-Read and Mic buttons in ChatScreen AppBar',
        (tester) async {
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts(testHosts);
      container.read(fleetSessionsProvider.notifier).setSessions([testSession]);

      await tester.pumpWidget(
        _wrapScreen(ChatScreen(session: testSession), container),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      final autoReadBtn = find.byTooltip('Auto-read disabled');
      expect(autoReadBtn, findsOneWidget);

      await tester.tap(autoReadBtn);
      await tester.pump();

      expect(
        container.read(audioCompanionServiceProvider).autoReadEnabled,
        isTrue,
      );
      expect(find.byTooltip('Auto-read enabled'), findsOneWidget);

      final micBtn = find.byTooltip('Microphone off');
      expect(micBtn, findsOneWidget);

      await tester.tap(micBtn);
      await tester.pump();

      expect(container.read(audioCompanionServiceProvider).micEnabled, isTrue);
      expect(find.byTooltip('Microphone active'), findsOneWidget);
    });

    testWidgets(
      'tapping Read aloud button on assistant message starts speech playback',
      (tester) async {
        final container = ProviderContainer(
          overrides: [
            apiClientProvider.overrideWithValue(mockApiClient),
            sseClientProvider.overrideWithValue(mockSseClient),
          ],
        );
        addTearDown(container.dispose);

        container.read(hostsListProvider.notifier).setHosts(testHosts);
        container.read(fleetSessionsProvider.notifier).setSessions([
          testSession,
        ]);

        await tester.pumpWidget(
          _wrapScreen(TranscriptScreen(session: testSession), container),
        );
        await tester.pump(const Duration(milliseconds: 600));
        await tester.pump();

        final readAloudBtn = find.byTooltip('Read aloud');
        expect(readAloudBtn, findsOneWidget);

        await tester.tap(readAloudBtn);
        await tester.pump();

        expect(
          container.read(audioCompanionServiceProvider).isSpeaking,
          isTrue,
        );
        expect(
          container.read(audioCompanionServiceProvider).lastSpokenText,
          contains('All test suites completed successfully'),
        );

        await tester.pump(const Duration(milliseconds: 150));
      },
    );

    testWidgets(
      'renders suggested commuter chips and tapping chip sends prompt in ChatScreen',
      (tester) async {
        final container = ProviderContainer(
          overrides: [
            apiClientProvider.overrideWithValue(mockApiClient),
            sseClientProvider.overrideWithValue(mockSseClient),
          ],
        );
        addTearDown(container.dispose);

        container.read(hostsListProvider.notifier).setHosts(testHosts);
        container.read(fleetSessionsProvider.notifier).setSessions([
          testSession,
        ]);

        container.read(audioCompanionServiceProvider.notifier).state =
            container.read(audioCompanionServiceProvider).copyWith(
          activeSuggestedChips: [
            'Proceed with deployment',
            'Review diffs',
            'Cancel',
          ],
        );

        await tester.pumpWidget(
          _wrapScreen(ChatScreen(session: testSession), container),
        );
        await tester.pump(const Duration(milliseconds: 600));
        await tester.pump();

        expect(find.text('Proceed with deployment'), findsOneWidget);
        expect(find.text('Review diffs'), findsOneWidget);
        expect(find.text('Cancel'), findsOneWidget);

        await tester.tap(find.text('Proceed with deployment'));
        await tester.pump();

        expect(find.text('Proceed with deployment'), findsWidgets);
      },
    );

    testWidgets(
      'auto-read waits for silent transcript refresh to complete on working -> idle transition before speaking',
      (tester) async {
        final workingSession = testSession.copyWith(
          state: SessionState.working,
        );
        final idleSession = testSession.copyWith(state: SessionState.idle);

        final container = ProviderContainer(
          overrides: [
            apiClientProvider.overrideWithValue(mockApiClient),
            sseClientProvider.overrideWithValue(mockSseClient),
          ],
        );
        addTearDown(container.dispose);

        container.read(hostsListProvider.notifier).setHosts(testHosts);
        container.read(fleetSessionsProvider.notifier).setSessions([
          workingSession,
        ]);

        container.read(audioCompanionServiceProvider.notifier).state = container
            .read(audioCompanionServiceProvider)
            .copyWith(autoReadEnabled: true);

        await tester.pumpWidget(
          _wrapScreen(TranscriptScreen(session: workingSession), container),
        );
        await tester.pump(const Duration(milliseconds: 600));
        await tester.pump();

        expect(
          container.read(audioCompanionServiceProvider).isSpeaking,
          isFalse,
        );

        container.read(fleetSessionsProvider.notifier).setSessions([
          idleSession,
        ]);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 50));

        expect(
          container.read(audioCompanionServiceProvider).lastSpokenText,
          contains('All test suites completed successfully'),
        );

        await tester.pump(const Duration(milliseconds: 150));
      },
    );

    testWidgets(
      'STT mic dictation dispatches recognized speech prompt to agent in ChatScreen',
      (tester) async {
        final testEngine = InMemorySpeechEngine();
        final container = ProviderContainer(
          overrides: [
            apiClientProvider.overrideWithValue(mockApiClient),
            sseClientProvider.overrideWithValue(mockSseClient),
            audioCompanionServiceProvider.overrideWith(
              (ref) => AudioCompanionService(
                ref.watch(apiClientProvider),
                engine: testEngine,
              ),
            ),
          ],
        );
        addTearDown(container.dispose);

        container.read(hostsListProvider.notifier).setHosts(testHosts);
        container.read(fleetSessionsProvider.notifier).setSessions([
          testSession,
        ]);

        await tester.pumpWidget(
          _wrapScreen(ChatScreen(session: testSession), container),
        );
        await tester.pump(const Duration(milliseconds: 600));
        await tester.pump();

        await tester.tap(find.byTooltip('Microphone off'));
        await tester.pump();
        expect(
          container.read(audioCompanionServiceProvider).micEnabled,
          isTrue,
        );

        testEngine.simulateSpeechResult(
          'Check Jira sprint backlog',
          isFinal: true,
        );
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 50));

        expect(
          container.read(audioCompanionServiceProvider).lastRecognizedText,
          equals('Check Jira sprint backlog'),
        );
        expect(find.text('Check Jira sprint backlog'), findsWidgets);
      },
    );

    testWidgets(
        'renders Live Voice button for Antigravity sessions in ChatScreen', (
      tester,
    ) async {
      final agySession = testSession.copyWith(
        id: 'antigravity:mac.local:sess-agy-1',
        agent: 'antigravity',
        engineType: 'headless',
      );
      final container = ProviderContainer(
        overrides: [
          apiClientProvider.overrideWithValue(mockApiClient),
          sseClientProvider.overrideWithValue(mockSseClient),
        ],
      );
      addTearDown(container.dispose);

      container.read(hostsListProvider.notifier).setHosts(testHosts);
      container.read(fleetSessionsProvider.notifier).setSessions([agySession]);

      await tester.pumpWidget(
        _wrapScreen(ChatScreen(session: agySession), container),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pump();

      expect(find.byTooltip('Live Voice: Connect'), findsOneWidget);
    });
  });
}
