import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/headless_models.dart';

enum UrlLaunchOutcome {
  openedBrowser,
  copiedFallback,
  rejectedUnsafe,
}

class ExternalUrlLauncher {
  final Future<bool> Function(String url)? _nativeOpener;
  final Future<void> Function(String text)? _clipboardWriter;

  static const MethodChannel _channel =
      MethodChannel('dev.ackbar.app/launcher');

  ExternalUrlLauncher({
    Future<bool> Function(String url)? nativeOpener,
    Future<void> Function(String text)? clipboardWriter,
  })  : _nativeOpener = nativeOpener,
        _clipboardWriter = clipboardWriter;

  Future<UrlLaunchOutcome> launchHttpsUrl(String url) async {
    final trimmed = url.trim();
    if (!isSafeHttpsUrl(trimmed)) {
      return UrlLaunchOutcome.rejectedUnsafe;
    }

    try {
      final opened = _nativeOpener != null
          ? await _nativeOpener!(trimmed)
          : await _channel
              .invokeMethod<bool>('openExternalUrl', {'url': trimmed});

      if (opened == true) {
        return UrlLaunchOutcome.openedBrowser;
      }
    } catch (_) {}

    await copyToClipboard(trimmed);
    return UrlLaunchOutcome.copiedFallback;
  }

  Future<bool> copyToClipboard(String text) async {
    final trimmed = text.trim();
    if (_clipboardWriter != null) {
      await _clipboardWriter!(trimmed);
      return true;
    }
    await Clipboard.setData(ClipboardData(text: trimmed));
    return true;
  }
}

final externalUrlLauncherProvider = Provider<ExternalUrlLauncher>((ref) {
  return ExternalUrlLauncher();
});
