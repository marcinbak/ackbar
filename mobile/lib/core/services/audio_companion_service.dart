import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../models/session.dart';
import '../network/api_client.dart';
import '../utils/speech_sanitizer.dart';

/// Pluggable speech synthesis, speech recognition, and raw PCM audio bridge interface
abstract class SpeechEngine {
  Future<void> speak(String text, {VoidCallback? onComplete});
  Future<void> stop();
  bool get isSpeaking;

  Future<bool> startListening({
    void Function(String text, bool isFinal)? onResult,
    VoidCallback? onError,
  }) async =>
      true;

  Future<void> stopListening() async {}

  Future<bool> startLivePcm({void Function(Uint8List pcm)? onMicPcm}) async =>
      true;

  Future<void> stopLivePcm() async {}

  Future<void> playPcm(Uint8List pcm, {int sampleRate = 24000}) async {}

  Future<void> clearPcm() async {}
}

/// In-memory safe speech engine fallback for headless and test environments
class InMemorySpeechEngine implements SpeechEngine {
  bool _speaking = false;
  VoidCallback? _currentComplete;
  Timer? _completeTimer;
  void Function(String text, bool isFinal)? _onSpeechResult;
  void Function(Uint8List pcm)? _onMicPcm;
  final List<Uint8List> playedPcmChunks = [];
  int clearPcmCount = 0;

  @override
  bool get isSpeaking => _speaking;

  @override
  Future<void> speak(String text, {VoidCallback? onComplete}) async {
    _completeTimer?.cancel();
    _speaking = true;
    _currentComplete = onComplete;
    // Complete automatically after a microtask/brief delay in non-test or when triggered
    _completeTimer = Timer(const Duration(milliseconds: 100), () {
      if (_speaking) {
        _speaking = false;
        _currentComplete?.call();
        _currentComplete = null;
      }
    });
  }

  @override
  Future<void> stop() async {
    _completeTimer?.cancel();
    _speaking = false;
    _currentComplete = null;
  }

  @override
  Future<bool> startListening({
    void Function(String text, bool isFinal)? onResult,
    VoidCallback? onError,
  }) async {
    _onSpeechResult = onResult;
    return true;
  }

  @override
  Future<void> stopListening() async {
    _onSpeechResult = null;
  }

  /// Helper for widget/unit tests to simulate STT dictation results
  void simulateSpeechResult(String text, {bool isFinal = true}) {
    _onSpeechResult?.call(text, isFinal);
  }

  @override
  Future<bool> startLivePcm({void Function(Uint8List pcm)? onMicPcm}) async {
    _onMicPcm = onMicPcm;
    return true;
  }

  @override
  Future<void> stopLivePcm() async {
    _onMicPcm = null;
  }

  /// Helper for widget/unit tests to simulate outgoing 16kHz S16_LE mic PCM
  void simulateMicPcm(Uint8List pcm) {
    _onMicPcm?.call(pcm);
  }

  @override
  Future<void> playPcm(Uint8List pcm, {int sampleRate = 24000}) async {
    playedPcmChunks.add(pcm);
  }

  @override
  Future<void> clearPcm() async {
    clearPcmCount++;
    playedPcmChunks.clear();
  }
}

/// Native OS MethodChannel speech & PCM engine (iOS AVFoundation/Speech & Android TTS/SpeechRecognizer/AudioTrack)
/// with automatic fallback to [InMemorySpeechEngine] in headless/test environments.
class PlatformSpeechEngine implements SpeechEngine {
  static const MethodChannel _channel = MethodChannel('dev.ackbar.app/speech');
  final InMemorySpeechEngine _fallback = InMemorySpeechEngine();

  bool _speaking = false;
  VoidCallback? _onSpeakComplete;
  void Function(String text, bool isFinal)? _onSpeechResult;
  VoidCallback? _onSpeechError;
  void Function(Uint8List pcm)? _onMicPcm;
  bool _handlerRegistered = false;

  PlatformSpeechEngine() {
    _ensureHandler();
  }

  void _ensureHandler() {
    if (_handlerRegistered) return;
    try {
      _channel.setMethodCallHandler(_handlePlatformCallback);
      _handlerRegistered = true;
    } catch (_) {
      // Binding not initialized or unavailable; fallback will handle calls
    }
  }

  Future<dynamic> _handlePlatformCallback(MethodCall call) async {
    switch (call.method) {
      case 'onSpeakComplete':
        _speaking = false;
        final cb = _onSpeakComplete;
        _onSpeakComplete = null;
        cb?.call();
        break;
      case 'onSpeechResult':
        final args = call.arguments;
        if (args is Map) {
          final text = args['text']?.toString() ?? '';
          final isFinal = args['isFinal'] == true;
          _onSpeechResult?.call(text, isFinal);
        }
        break;
      case 'onSpeechError':
        _onSpeechError?.call();
        break;
      case 'onMicPcm':
        final args = call.arguments;
        if (args is Uint8List) {
          _onMicPcm?.call(args);
        } else if (args is List<int>) {
          _onMicPcm?.call(Uint8List.fromList(args));
        }
        break;
    }
  }

  @override
  bool get isSpeaking => _speaking || _fallback.isSpeaking;

  @override
  Future<void> speak(String text, {VoidCallback? onComplete}) async {
    _ensureHandler();
    try {
      _speaking = true;
      _onSpeakComplete = onComplete;
      await _channel.invokeMethod('speak', {'text': text});
    } on MissingPluginException {
      _speaking = false;
      _onSpeakComplete = null;
      await _fallback.speak(text, onComplete: onComplete);
    } catch (_) {
      _speaking = false;
      _onSpeakComplete = null;
      await _fallback.speak(text, onComplete: onComplete);
    }
  }

  @override
  Future<void> stop() async {
    final wasSpeaking = _speaking;
    _speaking = false;
    _onSpeakComplete = null;
    await _fallback.stop();
    if (!wasSpeaking) return;
    try {
      await _channel.invokeMethod('stop');
    } on MissingPluginException {
      // Handled by fallback
    } catch (_) {}
  }

  @override
  Future<bool> startListening({
    void Function(String text, bool isFinal)? onResult,
    VoidCallback? onError,
  }) async {
    _ensureHandler();
    _onSpeechResult = onResult;
    _onSpeechError = onError;
    try {
      final ok = await _channel.invokeMethod<bool>('startListening');
      return ok ?? true;
    } on MissingPluginException {
      return _fallback.startListening(onResult: onResult, onError: onError);
    } catch (_) {
      return _fallback.startListening(onResult: onResult, onError: onError);
    }
  }

  @override
  Future<void> stopListening() async {
    _onSpeechResult = null;
    _onSpeechError = null;
    try {
      await _channel.invokeMethod('stopListening');
    } on MissingPluginException {
      await _fallback.stopListening();
    } catch (_) {
      await _fallback.stopListening();
    }
  }

  @override
  Future<bool> startLivePcm({void Function(Uint8List pcm)? onMicPcm}) async {
    _ensureHandler();
    _onMicPcm = onMicPcm;
    try {
      final ok = await _channel.invokeMethod<bool>('startLivePcm');
      return ok ?? true;
    } on MissingPluginException {
      return _fallback.startLivePcm(onMicPcm: onMicPcm);
    } catch (_) {
      return _fallback.startLivePcm(onMicPcm: onMicPcm);
    }
  }

  @override
  Future<void> stopLivePcm() async {
    _onMicPcm = null;
    try {
      await _channel.invokeMethod('stopLivePcm');
    } on MissingPluginException {
      await _fallback.stopLivePcm();
    } catch (_) {
      await _fallback.stopLivePcm();
    }
  }

  @override
  Future<void> playPcm(Uint8List pcm, {int sampleRate = 24000}) async {
    try {
      await _channel.invokeMethod('playPcm', {
        'pcm': pcm,
        'sampleRate': sampleRate,
      });
    } on MissingPluginException {
      await _fallback.playPcm(pcm, sampleRate: sampleRate);
    } catch (_) {
      await _fallback.playPcm(pcm, sampleRate: sampleRate);
    }
  }

  @override
  Future<void> clearPcm() async {
    try {
      await _channel.invokeMethod('clearPcm');
    } on MissingPluginException {
      await _fallback.clearPcm();
    } catch (_) {
      await _fallback.clearPcm();
    }
  }
}

/// Summarized speech response payload from daemon or client cache
class SpeechSummaryData {
  final String spokenText;
  final List<String> suggestedChips;
  final String tier;
  final bool cached;

  const SpeechSummaryData({
    required this.spokenText,
    this.suggestedChips = const [],
    this.tier = 'simple',
    this.cached = false,
  });

  factory SpeechSummaryData.fromJson(Map<String, dynamic> json) {
    List<String> chips = [];
    if (json['suggested_chips'] is List) {
      chips =
          (json['suggested_chips'] as List).map((e) => e.toString()).toList();
    }
    return SpeechSummaryData(
      spokenText: json['spoken_text']?.toString() ?? '',
      suggestedChips: chips,
      tier: json['tier']?.toString() ?? 'simple',
      cached: json['cached'] == true || json['cached'] == 1,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'spoken_text': spokenText,
      'suggested_chips': suggestedChips,
      'tier': tier,
      'cached': cached,
    };
  }
}

/// State for the voice and audio companion
class AudioCompanionState {
  final bool isSpeaking;
  final int? activeMessageIndex;
  final String lastSpokenText;
  final String lastRecognizedText;
  final bool autoReadEnabled;
  final bool micEnabled;
  final bool liveVoiceConnected;
  final bool liveVoiceRecording;
  final bool liveVoicePlaying;
  final Map<String, SpeechSummaryData> cachedSummaries;
  final List<String> activeSuggestedChips;

  const AudioCompanionState({
    this.isSpeaking = false,
    this.activeMessageIndex,
    this.lastSpokenText = '',
    this.lastRecognizedText = '',
    this.autoReadEnabled = false,
    this.micEnabled = false,
    this.liveVoiceConnected = false,
    this.liveVoiceRecording = false,
    this.liveVoicePlaying = false,
    this.cachedSummaries = const {},
    this.activeSuggestedChips = const [],
  });

  bool isCached(String sessionId, int index) =>
      cachedSummaries.containsKey('$sessionId:$index');

  AudioCompanionState copyWith({
    bool? isSpeaking,
    int? activeMessageIndex,
    bool clearActiveMessageIndex = false,
    String? lastSpokenText,
    String? lastRecognizedText,
    bool? autoReadEnabled,
    bool? micEnabled,
    bool? liveVoiceConnected,
    bool? liveVoiceRecording,
    bool? liveVoicePlaying,
    Map<String, SpeechSummaryData>? cachedSummaries,
    List<String>? activeSuggestedChips,
  }) {
    return AudioCompanionState(
      isSpeaking: isSpeaking ?? this.isSpeaking,
      activeMessageIndex: clearActiveMessageIndex
          ? null
          : (activeMessageIndex ?? this.activeMessageIndex),
      lastSpokenText: lastSpokenText ?? this.lastSpokenText,
      lastRecognizedText: lastRecognizedText ?? this.lastRecognizedText,
      autoReadEnabled: autoReadEnabled ?? this.autoReadEnabled,
      micEnabled: micEnabled ?? this.micEnabled,
      liveVoiceConnected: liveVoiceConnected ?? this.liveVoiceConnected,
      liveVoiceRecording: liveVoiceRecording ?? this.liveVoiceRecording,
      liveVoicePlaying: liveVoicePlaying ?? this.liveVoicePlaying,
      cachedSummaries: cachedSummaries ?? this.cachedSummaries,
      activeSuggestedChips: activeSuggestedChips ?? this.activeSuggestedChips,
    );
  }
}

/// Service governing voice feedback, 2-tier lazy speech pipeline, native STT dictation,
/// and Antigravity JETSKI-AUDIO/1 Live Voice WebSocket streaming.
class AudioCompanionService extends StateNotifier<AudioCompanionState> {
  final ApiClient _apiClient;
  final SpeechEngine _engine;

  WebSocketChannel? _liveChannel;
  StreamSubscription? _liveSub;
  String? _liveSessionId;
  int _liveSampleRate = 24000;

  AudioCompanionService(this._apiClient, {SpeechEngine? engine})
      : _engine = engine ?? PlatformSpeechEngine(),
        super(const AudioCompanionState());

  SpeechEngine get engine => _engine;

  void toggleAutoRead() {
    final next = !state.autoReadEnabled;
    state = state.copyWith(autoReadEnabled: next);
    if (!next) {
      stopSpeech();
    }
  }

  void setAutoReadEnabled(bool enabled) {
    state = state.copyWith(autoReadEnabled: enabled);
  }

  Future<void> toggleMic({
    void Function(String text, bool isFinal)? onResult,
  }) async {
    final next = !state.micEnabled;
    state = state.copyWith(micEnabled: next);
    if (next) {
      if (state.isSpeaking) {
        await stopSpeech();
      }
      await _engine.startListening(
        onResult: (text, isFinal) {
          if (!mounted) return;
          state = state.copyWith(
            lastRecognizedText: text,
            micEnabled: !isFinal,
          );
          onResult?.call(text, isFinal);
        },
        onError: () {
          if (!mounted) return;
          state = state.copyWith(micEnabled: false);
        },
      );
    } else {
      await _engine.stopListening();
    }
  }

  void setMicEnabled(bool enabled) {
    state = state.copyWith(micEnabled: enabled);
    if (!enabled) {
      _engine.stopListening();
    }
  }

  void clearChips() {
    state = state.copyWith(activeSuggestedChips: const []);
  }

  /// Toggles Antigravity CLI's native two-way Gemini Live voice mode (`/voice live`)
  /// by streaming 16kHz S16_LE mic PCM and 24kHz S16_LE speaker PCM over `/v1/sessions/voice-bridge`.
  Future<void> toggleAntigravityLiveVoice(
    Session session, {
    String hostUrl = 'http://127.0.0.1:7777',
    String? authToken,
    String action = 'start_live',
    WebSocketChannel Function(Uri uri)? channelBuilder,
  }) async {
    if (state.liveVoiceConnected) {
      await stopAntigravityLiveVoice();
      return;
    }

    await stopSpeech();
    if (state.micEnabled) {
      await _engine.stopListening();
      state = state.copyWith(micEnabled: false);
    }

    final parsed = Uri.parse(hostUrl);
    final wsScheme = parsed.scheme == 'https' ? 'wss' : 'ws';
    final authority =
        parsed.authority.isNotEmpty ? parsed.authority : '127.0.0.1:7777';
    final queryParams = <String, String>{
      'session_id': session.id,
      if (authToken != null && authToken.isNotEmpty) 'token': authToken,
    };
    final wsUri = Uri.parse('$wsScheme://$authority/v1/sessions/voice-bridge')
        .replace(queryParameters: queryParams);

    try {
      final channel = channelBuilder != null
          ? channelBuilder(wsUri)
          : WebSocketChannel.connect(wsUri);
      _liveChannel = channel;
      _liveSessionId = session.id;
      _liveSampleRate = 24000;

      state = state.copyWith(
        liveVoiceConnected: true,
        liveVoiceRecording: false,
        liveVoicePlaying: false,
      );

      await _engine.startLivePcm(
        onMicPcm: (pcm) {
          if (state.liveVoiceConnected && _liveChannel != null) {
            try {
              _liveChannel!.sink.add(pcm);
            } catch (_) {}
          }
        },
      );

      _liveSub = channel.stream.listen(
        (message) {
          if (!mounted) return;
          if (message is Uint8List) {
            _engine.playPcm(message, sampleRate: _liveSampleRate);
          } else if (message is List<int>) {
            _engine.playPcm(
              Uint8List.fromList(message),
              sampleRate: _liveSampleRate,
            );
          } else if (message is String) {
            try {
              final decoded = jsonDecode(message);
              if (decoded is Map<String, dynamic>) {
                final type = decoded['type']?.toString() ?? '';
                if (type == 'clear') {
                  _engine.clearPcm();
                } else if (type == 'status') {
                  final rate = decoded['sample_rate'];
                  if (rate is int && rate > 0) {
                    _liveSampleRate = rate;
                  }
                  state = state.copyWith(
                    liveVoiceRecording: decoded['recording'] == true,
                    liveVoicePlaying: decoded['playing'] == true,
                  );
                }
              }
            } catch (_) {}
          }
        },
        onError: (_) {
          stopAntigravityLiveVoice(sendStopCmd: false);
        },
        onDone: () {
          stopAntigravityLiveVoice(sendStopCmd: false);
        },
      );

      if (action.isNotEmpty) {
        channel.sink.add(
          jsonEncode({'type': action, 'session_id': session.id}),
        );
      }
    } catch (_) {
      await stopAntigravityLiveVoice(sendStopCmd: false);
    }
  }

  Future<void> stopAntigravityLiveVoice({bool sendStopCmd = true}) async {
    final channel = _liveChannel;
    final sessionId = _liveSessionId;
    _liveChannel = null;
    _liveSessionId = null;

    await _liveSub?.cancel();
    _liveSub = null;

    if (sendStopCmd && channel != null && sessionId != null) {
      try {
        channel.sink.add(
          jsonEncode({'type': 'stop_voice', 'session_id': sessionId}),
        );
      } catch (_) {}
    }
    try {
      await channel?.sink.close();
    } catch (_) {}

    await _engine.stopLivePcm();

    if (mounted) {
      state = state.copyWith(
        liveVoiceConnected: false,
        liveVoiceRecording: false,
        liveVoicePlaying: false,
      );
    }
  }

  Future<void> stopSpeech() async {
    await _engine.stop();
    if (mounted) {
      state = state.copyWith(isSpeaking: false, clearActiveMessageIndex: true);
    }
  }

  Future<void> speakMessage(
    Session session,
    int messageIndex,
    String rawContent, {
    bool forceRefresh = false,
    bool forceFull = false,
    String? hostUrl,
    String? authToken,
  }) async {
    if (rawContent.trim().isEmpty) return;

    final cacheKey = '${session.id}:$messageIndex';

    // Tier 1 direct full force read
    if (forceFull) {
      final sanitized = sanitizeMarkdownForSpeech(rawContent);
      await _playSpokenText(sanitized, messageIndex);
      return;
    }

    // 1. Check client memory cache
    if (!forceRefresh && state.cachedSummaries.containsKey(cacheKey)) {
      final cached = state.cachedSummaries[cacheKey]!;
      state = state.copyWith(activeSuggestedChips: cached.suggestedChips);
      await _playSpokenText(cached.spokenText, messageIndex);
      return;
    }

    // 2. Tier 1: Check if simple prose (< 120 words, <= 3 table rows, < 3 headings)
    if (isSimpleSpeechCandidate(rawContent)) {
      final sanitized = sanitizeMarkdownForSpeech(rawContent);
      final summary = SpeechSummaryData(
        spokenText: sanitized,
        suggestedChips: const [],
        tier: 'simple',
      );
      final updatedCache = Map<String, SpeechSummaryData>.from(
        state.cachedSummaries,
      );
      updatedCache[cacheKey] = summary;
      state = state.copyWith(
        cachedSummaries: updatedCache,
        activeSuggestedChips: const [],
      );
      await _playSpokenText(sanitized, messageIndex);
      return;
    }

    // 3. Tier 2: Call daemon POST /v1/sessions/summarize-speech
    final effectiveHostUrl = hostUrl ?? 'http://127.0.0.1:7777';
    try {
      final res = await _apiClient.summarizeSpeech(
        effectiveHostUrl,
        sessionId: session.id,
        messageIndex: messageIndex,
        content: rawContent,
        forceRefresh: forceRefresh,
        authToken: authToken,
      );

      if (res != null && res['spoken_text'] != null) {
        final summary = SpeechSummaryData.fromJson(res);
        final updatedCache = Map<String, SpeechSummaryData>.from(
          state.cachedSummaries,
        );
        updatedCache[cacheKey] = summary;
        state = state.copyWith(
          cachedSummaries: updatedCache,
          activeSuggestedChips: summary.suggestedChips,
        );
        await _playSpokenText(summary.spokenText, messageIndex);
        return;
      }
    } catch (_) {}

    // Fallback to Tier 1 local sanitize if API unavailable
    final fallbackSanitized = sanitizeMarkdownForSpeech(rawContent);
    await _playSpokenText(fallbackSanitized, messageIndex);
  }

  Future<void> _playSpokenText(String text, int messageIndex) async {
    if (!mounted) return;

    state = state.copyWith(
      isSpeaking: true,
      activeMessageIndex: messageIndex,
      lastSpokenText: text,
    );

    await _engine.stop();
    if (!mounted) return;

    await _engine.speak(
      text,
      onComplete: () {
        if (mounted) {
          state = state.copyWith(
            isSpeaking: false,
            clearActiveMessageIndex: true,
          );
        }
      },
    );
  }

  @override
  void dispose() {
    _liveSub?.cancel();
    _liveChannel?.sink.close();
    _engine.stopLivePcm();
    _engine.stopListening();
    _engine.stop();
    super.dispose();
  }
}
