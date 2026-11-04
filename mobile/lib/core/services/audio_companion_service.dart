import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/session.dart';
import '../network/api_client.dart';
import '../utils/speech_sanitizer.dart';

/// Pluggable speech synthesis interface
abstract class SpeechEngine {
  Future<void> speak(String text, {VoidCallback? onComplete});
  Future<void> stop();
  bool get isSpeaking;
}

/// In-memory safe speech engine fallback for headless and test environments
class InMemorySpeechEngine implements SpeechEngine {
  bool _speaking = false;
  VoidCallback? _currentComplete;
  Timer? _completeTimer;

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
  final bool autoReadEnabled;
  final bool micEnabled;
  final Map<String, SpeechSummaryData> cachedSummaries;
  final List<String> activeSuggestedChips;

  const AudioCompanionState({
    this.isSpeaking = false,
    this.activeMessageIndex,
    this.lastSpokenText = '',
    this.autoReadEnabled = false,
    this.micEnabled = false,
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
    bool? autoReadEnabled,
    bool? micEnabled,
    Map<String, SpeechSummaryData>? cachedSummaries,
    List<String>? activeSuggestedChips,
  }) {
    return AudioCompanionState(
      isSpeaking: isSpeaking ?? this.isSpeaking,
      activeMessageIndex: clearActiveMessageIndex
          ? null
          : (activeMessageIndex ?? this.activeMessageIndex),
      lastSpokenText: lastSpokenText ?? this.lastSpokenText,
      autoReadEnabled: autoReadEnabled ?? this.autoReadEnabled,
      micEnabled: micEnabled ?? this.micEnabled,
      cachedSummaries: cachedSummaries ?? this.cachedSummaries,
      activeSuggestedChips: activeSuggestedChips ?? this.activeSuggestedChips,
    );
  }
}

/// Service governing voice feedback, 2-tier lazy speech pipeline, and quick-reply chips
class AudioCompanionService extends StateNotifier<AudioCompanionState> {
  final ApiClient _apiClient;
  final SpeechEngine _engine;

  AudioCompanionService(this._apiClient, {SpeechEngine? engine})
      : _engine = engine ?? InMemorySpeechEngine(),
        super(const AudioCompanionState());

  void toggleAutoRead() {
    state = state.copyWith(autoReadEnabled: !state.autoReadEnabled);
  }

  void setAutoReadEnabled(bool enabled) {
    state = state.copyWith(autoReadEnabled: enabled);
  }

  void toggleMic() {
    state = state.copyWith(micEnabled: !state.micEnabled);
  }

  void setMicEnabled(bool enabled) {
    state = state.copyWith(micEnabled: enabled);
  }

  void clearChips() {
    state = state.copyWith(activeSuggestedChips: const []);
  }

  Future<void> stopSpeech() async {
    await _engine.stop();
    if (mounted) {
      state = state.copyWith(
        isSpeaking: false,
        clearActiveMessageIndex: true,
      );
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
      final updatedCache =
          Map<String, SpeechSummaryData>.from(state.cachedSummaries);
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
        final updatedCache =
            Map<String, SpeechSummaryData>.from(state.cachedSummaries);
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
    await stopSpeech();
    if (!mounted) return;

    state = state.copyWith(
      isSpeaking: true,
      activeMessageIndex: messageIndex,
      lastSpokenText: text,
    );

    await _engine.speak(text, onComplete: () {
      if (mounted) {
        state = state.copyWith(
          isSpeaking: false,
          clearActiveMessageIndex: true,
        );
      }
    });
  }
}
