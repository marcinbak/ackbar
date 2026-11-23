import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../layout/responsive_breakpoints.dart';
import '../models/session.dart';
import '../providers/fleet_providers.dart';
import '../providers/tablet_workspace_provider.dart';
import '../../features/chat/presentation/chat_screen.dart';
import '../../features/terminal/presentation/terminal_screen.dart';
import '../../features/transcript/presentation/transcript_screen.dart';

class SessionNavigator {
  static Future<void> openSession(
    BuildContext context,
    WidgetRef? ref,
    Session session,
  ) {
    if (ref != null && session.isUnread) {
      ref.read(fleetSessionsProvider.notifier).markSessionRead(
            session.id,
            session.host,
          );
    }
    if (ref != null && ResponsiveBreakpoints.isTablet(context)) {
      Navigator.of(context).popUntil((route) => route.isFirst);
      ref.read(selectedTabletSessionProvider.notifier).state = session;
      ref.read(mainTabNavIndexProvider.notifier).state = 0;
      return Future.value();
    }
    if (session.isHeadless) {
      return ChatScreen.open(context, session);
    } else {
      return TerminalScreen.open(context, session);
    }
  }

  static Future<void> openTranscript(
    BuildContext context,
    Session session, {
    WidgetRef? ref,
    bool showOpenSessionAction = true,
  }) {
    if (ref != null && session.isUnread) {
      ref.read(fleetSessionsProvider.notifier).markSessionRead(
            session.id,
            session.host,
          );
    }
    return TranscriptScreen.open(
      context,
      session,
      showOpenSessionAction: showOpenSessionAction,
    );
  }
}
