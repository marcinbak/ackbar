import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';

final selectedTabletSessionProvider = StateProvider<Session?>((ref) => null);

final tabletFleetTreeCollapsedProvider = StateProvider<bool>((ref) => false);

final tabletSessionKeyAliasesProvider =
    StateProvider<Map<String, String>>((ref) => const {});

class TabletDraftPromptsNotifier extends StateNotifier<Map<String, String>> {
  TabletDraftPromptsNotifier() : super(const {});

  void setDraft(String sessionId, String text) {
    if (text.isEmpty) {
      clearDraft(sessionId);
      return;
    }
    if (state[sessionId] == text) return;
    state = {...state, sessionId: text};
  }

  void clearDraft(String sessionId) {
    if (!state.containsKey(sessionId)) return;
    final next = Map<String, String>.from(state)..remove(sessionId);
    state = next;
  }

  String draftFor(String sessionId) => state[sessionId] ?? '';
}

final tabletDraftPromptsProvider =
    StateNotifierProvider<TabletDraftPromptsNotifier, Map<String, String>>(
  (ref) => TabletDraftPromptsNotifier(),
);
