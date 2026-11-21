import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

class FleetShowDoneNotifier extends StateNotifier<bool> {
  static const String _kShowDoneKey = 'ackbar_fleet_show_done';
  bool _userUpdated = false;

  FleetShowDoneNotifier() : super(false) {
    _load();
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
    if (state != value) {
      state = value;
    }
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setBool(_kShowDoneKey, value);
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

  bool isFolderCollapsed(String path, {bool isSearching = false}) {
    if (isSearching) return false;
    return collapsedFolders.contains(path);
  }

  bool isSubsectionCollapsed(String key, {bool defaultCollapsed = false}) {
    if (defaultCollapsed) {
      return !expandedSubsections.contains(key);
    }
    return collapsedSubsections.contains(key);
  }

  FleetTreeCollapseState copyWith({
    Set<String>? collapsedFolders,
    Set<String>? collapsedSubsections,
    Set<String>? expandedSubsections,
  }) {
    return FleetTreeCollapseState(
      collapsedFolders: collapsedFolders ?? this.collapsedFolders,
      collapsedSubsections: collapsedSubsections ?? this.collapsedSubsections,
      expandedSubsections: expandedSubsections ?? this.expandedSubsections,
    );
  }
}

class FleetTreeCollapseNotifier extends StateNotifier<FleetTreeCollapseState> {
  static const String _kCollapsedFoldersKey = 'ackbar_collapsed_groups';
  static const String _kCollapsedSubsectionsKey =
      'ackbar_collapsed_subsections';
  static const String _kExpandedSubsectionsKey = 'ackbar_expanded_subsections';
  final Set<String> _dirtyFolders = {};
  final Set<String> _dirtySubsections = {};

  FleetTreeCollapseNotifier() : super(const FleetTreeCollapseState()) {
    _load();
  }

  Set<String> _decodeSet(String? raw) {
    if (raw == null || raw.isEmpty) return const {};
    final decoded = jsonDecode(raw);
    if (decoded is! List) return const {};
    return decoded.map((e) => e.toString()).toSet();
  }

  Set<String> _mergeLoaded(
    Set<String> current,
    Set<String> loaded,
    Set<String> dirty,
  ) {
    final merged = Set<String>.from(current);
    for (final item in loaded) {
      if (!dirty.contains(item)) {
        merged.add(item);
      }
    }
    return merged;
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (!mounted) return;
      state = FleetTreeCollapseState(
        collapsedFolders: _mergeLoaded(
          state.collapsedFolders,
          _decodeSet(prefs.getString(_kCollapsedFoldersKey)),
          _dirtyFolders,
        ),
        collapsedSubsections: _mergeLoaded(
          state.collapsedSubsections,
          _decodeSet(prefs.getString(_kCollapsedSubsectionsKey)),
          _dirtySubsections,
        ),
        expandedSubsections: _mergeLoaded(
          state.expandedSubsections,
          _decodeSet(prefs.getString(_kExpandedSubsectionsKey)),
          _dirtySubsections,
        ),
      );
    } catch (_) {}
  }

  bool isFolderCollapsed(String path, {bool isSearching = false}) =>
      state.isFolderCollapsed(path, isSearching: isSearching);

  bool isSubsectionCollapsed(String key, {bool defaultCollapsed = false}) =>
      state.isSubsectionCollapsed(key, defaultCollapsed: defaultCollapsed);

  Future<void> toggleFolder(String path) async {
    _dirtyFolders.add(path);
    final next = Set<String>.from(state.collapsedFolders);
    if (!next.add(path)) {
      next.remove(path);
    }
    state = state.copyWith(collapsedFolders: next);
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kCollapsedFoldersKey, jsonEncode(next.toList()));
    } catch (_) {}
  }

  Future<void> toggleSubsection(
    String key, {
    bool defaultCollapsed = false,
  }) async {
    _dirtySubsections.add(key);
    final wasCollapsed = state.isSubsectionCollapsed(
      key,
      defaultCollapsed: defaultCollapsed,
    );
    final nextCollapsed = Set<String>.from(state.collapsedSubsections);
    final nextExpanded = Set<String>.from(state.expandedSubsections);
    if (wasCollapsed) {
      nextCollapsed.remove(key);
      nextExpanded.add(key);
    } else {
      nextCollapsed.add(key);
      nextExpanded.remove(key);
    }
    state = state.copyWith(
      collapsedSubsections: nextCollapsed,
      expandedSubsections: nextExpanded,
    );
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(
        _kCollapsedSubsectionsKey,
        jsonEncode(nextCollapsed.toList()),
      );
      await prefs.setString(
        _kExpandedSubsectionsKey,
        jsonEncode(nextExpanded.toList()),
      );
    } catch (_) {}
  }
}

final fleetTreeCollapseProvider =
    StateNotifierProvider<FleetTreeCollapseNotifier, FleetTreeCollapseState>(
  (ref) => FleetTreeCollapseNotifier(),
);
