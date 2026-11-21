import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

class FleetShowDoneNotifier extends StateNotifier<bool> {
  static const String _kShowDoneKey = 'ackbar_fleet_show_done';
  bool _userUpdated = false;
  late final Future<void> _loadFuture = _load();

  FleetShowDoneNotifier() : super(false) {
    _loadFuture;
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final saved = prefs.getBool(_kShowDoneKey);
      if (mounted && !_userUpdated && saved != null && saved != state) {
        state = saved;
      }
    } catch (_) {}
  }

  Future<void> toggle() => setShowDone(!state);

  Future<void> setShowDone(bool value) async {
    _userUpdated = true;
    if (state != value) state = value;
    await _loadFuture;
    if (!mounted) return;
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setBool(_kShowDoneKey, state);
    } catch (_) {}
  }
}

final fleetShowDoneProvider =
    StateNotifierProvider<FleetShowDoneNotifier, bool>(
  (ref) => FleetShowDoneNotifier(),
);

class FleetTreeCollapseState {
  final Set<String> collapsedFolders;
  final Set<String> collapsedSubsections;
  final Set<String> expandedSubsections;

  const FleetTreeCollapseState({
    this.collapsedFolders = const {},
    this.collapsedSubsections = const {},
    this.expandedSubsections = const {},
  });

  bool isFolderCollapsed(String path, {bool isSearching = false}) =>
      !isSearching && collapsedFolders.contains(path);

  bool isSubsectionCollapsed(
    String key, {
    bool defaultCollapsed = false,
    bool isSearching = false,
  }) {
    if (isSearching) return false;
    return defaultCollapsed
        ? !expandedSubsections.contains(key)
        : collapsedSubsections.contains(key);
  }
}

class FleetTreeCollapseNotifier extends StateNotifier<FleetTreeCollapseState> {
  static const String _kFoldersKey = 'ackbar_collapsed_groups';
  static const String _kCollapsedSubsKey = 'ackbar_collapsed_subsections';
  static const String _kExpandedSubsKey = 'ackbar_expanded_subsections';
  final Set<String> _dirtyFolders = {};
  final Set<String> _dirtySubsections = {};
  late final Future<void> _loadFuture = _load();

  FleetTreeCollapseNotifier() : super(const FleetTreeCollapseState()) {
    _loadFuture;
  }

  Set<String> _decodeSet(String? raw) {
    if (raw == null || raw.isEmpty) return const {};
    final decoded = jsonDecode(raw);
    return decoded is List
        ? decoded.map((e) => e.toString()).toSet()
        : const {};
  }

  Set<String> _merge(Set<String> cur, Set<String> loaded, Set<String> dirty) =>
      {...cur, ...loaded.where((item) => !dirty.contains(item))};

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (!mounted) return;
      state = FleetTreeCollapseState(
        collapsedFolders: _merge(
          state.collapsedFolders,
          _decodeSet(prefs.getString(_kFoldersKey)),
          _dirtyFolders,
        ),
        collapsedSubsections: _merge(
          state.collapsedSubsections,
          _decodeSet(prefs.getString(_kCollapsedSubsKey)),
          _dirtySubsections,
        ),
        expandedSubsections: _merge(
          state.expandedSubsections,
          _decodeSet(prefs.getString(_kExpandedSubsKey)),
          _dirtySubsections,
        ),
      );
    } catch (_) {}
  }

  Future<void> _persist(Map<String, Set<String>> Function() entries) async {
    await _loadFuture;
    if (!mounted) return;
    try {
      final prefs = await SharedPreferences.getInstance();
      for (final entry in entries().entries) {
        await prefs.setString(entry.key, jsonEncode(entry.value.toList()));
      }
    } catch (_) {}
  }

  bool isFolderCollapsed(String path, {bool isSearching = false}) =>
      state.isFolderCollapsed(path, isSearching: isSearching);

  bool isSubsectionCollapsed(
    String key, {
    bool defaultCollapsed = false,
    bool isSearching = false,
  }) =>
      state.isSubsectionCollapsed(
        key,
        defaultCollapsed: defaultCollapsed,
        isSearching: isSearching,
      );

  Future<void> toggleFolder(String path) async {
    _dirtyFolders.add(path);
    final next = Set<String>.from(state.collapsedFolders);
    if (!next.add(path)) next.remove(path);
    state = FleetTreeCollapseState(
      collapsedFolders: next,
      collapsedSubsections: state.collapsedSubsections,
      expandedSubsections: state.expandedSubsections,
    );
    await _persist(() => {_kFoldersKey: state.collapsedFolders});
  }

  Future<void> toggleSubsection(
    String key, {
    bool defaultCollapsed = false,
  }) async {
    _dirtySubsections.add(key);
    final wasCollapsed =
        state.isSubsectionCollapsed(key, defaultCollapsed: defaultCollapsed);
    final nextCollapsed = Set<String>.from(state.collapsedSubsections);
    final nextExpanded = Set<String>.from(state.expandedSubsections);
    if (wasCollapsed) {
      nextCollapsed.remove(key);
      nextExpanded.add(key);
    } else {
      nextCollapsed.add(key);
      nextExpanded.remove(key);
    }
    state = FleetTreeCollapseState(
      collapsedFolders: state.collapsedFolders,
      collapsedSubsections: nextCollapsed,
      expandedSubsections: nextExpanded,
    );
    await _persist(
      () => {
        _kCollapsedSubsKey: state.collapsedSubsections,
        _kExpandedSubsKey: state.expandedSubsections,
      },
    );
  }
}

final fleetTreeCollapseProvider =
    StateNotifierProvider<FleetTreeCollapseNotifier, FleetTreeCollapseState>(
  (ref) => FleetTreeCollapseNotifier(),
);
