import 'package:flutter/material.dart';
import 'core/theme/app_theme.dart';
import 'features/main_navigation_shell.dart';

/// Root application widget configuring Ackbar dark theme and shell routing.
class AckbarApp extends StatelessWidget {
  const AckbarApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Ackbar',
      debugShowCheckedModeBanner: false,
      theme: AppTheme.darkTheme,
      home: const MainNavigationShell(),
    );
  }
}
