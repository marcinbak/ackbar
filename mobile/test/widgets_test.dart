import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:ackbar_mobile/core/theme/app_colors.dart';
import 'package:ackbar_mobile/core/theme/app_theme.dart';
import 'package:ackbar_mobile/core/widgets/glass_card.dart';
import 'package:ackbar_mobile/core/widgets/segmented_filter_tabs.dart';
import 'package:ackbar_mobile/core/widgets/status_badge.dart';
import 'package:ackbar_mobile/core/widgets/terminal_accessory_bar.dart';
import 'package:ackbar_mobile/core/widgets/token_gauge_bar.dart';

Widget _wrapWithTheme(Widget child) {
  return MaterialApp(
    theme: AppTheme.darkTheme,
    home: Scaffold(body: Center(child: child)),
  );
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  GoogleFonts.config.allowRuntimeFetching = false;

  group('TokenGaugeBar Widget Tests', () {
    testWidgets('renders correctly with direct percentage value', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            percentage: 0.45,
            showLabels: true,
          ),
        ),
      );

      expect(find.text('CONTEXT USAGE'), findsOneWidget);
      expect(find.text('45%'), findsOneWidget);
    });

    testWidgets('renders calculated percentage from usedTokens and maxTokens', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            usedTokens: 142000,
            maxTokens: 200000,
            showLabels: true,
          ),
        ),
      );

      // 142000 / 200000 = 71%
      expect(find.text('142k / 200k (71%)'), findsOneWidget);
    });

    testWidgets('formats millions of tokens with M suffix', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            usedTokens: 1500000,
            maxTokens: 2000000,
            showLabels: true,
          ),
        ),
      );

      expect(find.text('1.5M / 2.0M (75%)'), findsOneWidget);
    });

    testWidgets('clamps percentage between 0.0 and 1.0', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            percentage: 1.5, // above 100%
            showLabels: true,
          ),
        ),
      );

      expect(find.text('100%'), findsOneWidget);
    });

    testWidgets('hides labels when showLabels is false', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            percentage: 0.88,
            showLabels: false,
          ),
        ),
      );

      expect(find.text('CONTEXT USAGE'), findsNothing);
      expect(find.text('88%'), findsNothing);
    });

    testWidgets('renders custom label when provided', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            percentage: 0.30,
            customLabel: 'MEMORY LIMIT',
          ),
        ),
      );

      expect(find.text('MEMORY LIMIT'), findsOneWidget);
      expect(find.text('30%'), findsOneWidget);
    });

    testWidgets('renders compact bar height when isCompact is true', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const TokenGaugeBar(
            percentage: 0.50,
            isCompact: true,
          ),
        ),
      );

      expect(find.byType(TokenGaugeBar), findsOneWidget);
    });
  });

  group('StatusBadge Widget Tests', () {
    testWidgets('renders working status with default symbol and label', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(const StatusBadge.working()),
      );

      expect(find.text('⚙️'), findsOneWidget);
      expect(find.text('WORKING'), findsOneWidget);
    });

    testWidgets('renders blocked status with default symbol and label', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(const StatusBadge.blocked()),
      );

      expect(find.text('❓'), findsOneWidget);
      expect(find.text('BLOCKED'), findsOneWidget);
    });

    testWidgets('renders idle status with default symbol and label', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(const StatusBadge.idle()),
      );

      expect(find.text('✅'), findsOneWidget);
      expect(find.text('IDLE'), findsOneWidget);
    });

    testWidgets('renders offline status with default symbol and label', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(const StatusBadge.offline()),
      );

      expect(find.text('⚪'), findsOneWidget);
      expect(find.text('OFFLINE'), findsOneWidget);
    });

    testWidgets('renders active status with default symbol and label', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(const StatusBadge.active()),
      );

      expect(find.text('🟢'), findsOneWidget);
      expect(find.text('ACTIVE'), findsOneWidget);
    });

    testWidgets('renders custom label and custom symbol when supplied', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const StatusBadge(
            status: AckbarSessionStatus.blocked,
            customLabel: 'WAITING FOR CONFIRMATION',
            customSymbol: '⚠️',
          ),
        ),
      );

      expect(find.text('⚠️'), findsOneWidget);
      expect(find.text('WAITING FOR CONFIRMATION'), findsOneWidget);
      expect(find.text('BLOCKED'), findsNothing);
    });

    testWidgets('renders showDotOnly circular indicator without text', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const StatusBadge(
            status: AckbarSessionStatus.active,
            showDotOnly: true,
          ),
        ),
      );

      expect(find.text('ACTIVE'), findsNothing);
      expect(find.text('🟢'), findsNothing);
      expect(find.byType(Container), findsWidgets);
    });

    testWidgets('renders compact version', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const StatusBadge.working(isCompact: true),
        ),
      );

      expect(find.text('⚙️'), findsOneWidget);
      expect(find.text('WORKING'), findsOneWidget);
    });

    testWidgets('applies overrideColor when specified', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const StatusBadge(
            status: AckbarSessionStatus.working,
            overrideColor: Colors.purple,
          ),
        ),
      );

      expect(find.text('WORKING'), findsOneWidget);
    });
  });

  group('GlassCard Widget Tests', () {
    testWidgets('renders child content inside styled container', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const GlassCard(
            child: Text('Card Content Inside'),
          ),
        ),
      );

      expect(find.text('Card Content Inside'), findsOneWidget);
    });

    testWidgets('renders left accent color indicator bar when accentColor is provided', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          const GlassCard(
            accentColor: AppColors.statusCoral,
            accentWidth: 4.0,
            child: Text('Card with Accent Bar'),
          ),
        ),
      );

      expect(find.text('Card with Accent Bar'), findsOneWidget);
      expect(find.descendant(of: find.byType(GlassCard), matching: find.byType(Stack)), findsOneWidget);
    });

    testWidgets('invokes onTap callback when tapped', (tester) async {
      bool wasTapped = false;

      await tester.pumpWidget(
        _wrapWithTheme(
          GlassCard(
            onTap: () {
              wasTapped = true;
            },
            child: const Text('Tap Me Card'),
          ),
        ),
      );

      expect(find.text('Tap Me Card'), findsOneWidget);
      await tester.tap(find.text('Tap Me Card'));
      await tester.pump();

      expect(wasTapped, isTrue);
    });

    testWidgets('does not wrap in InkWell when isInteractive is false', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          GlassCard(
            isInteractive: false,
            onTap: () {},
            child: const Text('Non-interactive Card'),
          ),
        ),
      );

      expect(find.byType(InkWell), findsNothing);
      expect(find.text('Non-interactive Card'), findsOneWidget);
    });

    testWidgets('applies custom padding, margin, and border radius', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          GlassCard(
            padding: const EdgeInsets.all(24.0),
            margin: const EdgeInsets.symmetric(horizontal: 10.0),
            borderRadius: BorderRadius.circular(20.0),
            backgroundColor: const Color(0xFF202020),
            borderColor: Colors.teal,
            child: const Text('Custom Styling Card'),
          ),
        ),
      );

      expect(find.text('Custom Styling Card'), findsOneWidget);
    });
  });

  group('SegmentedFilterTabs Widget Tests', () {
    testWidgets('renders all tab items with labels and optional badge counts', (tester) async {
      final tabs = [
        const FilterTabItem(label: 'All', count: 5),
        const FilterTabItem(label: 'Working', count: 2),
        const FilterTabItem(label: 'Blocked', count: 1),
        const FilterTabItem(label: 'Idle'),
      ];

      await tester.pumpWidget(
        _wrapWithTheme(
          SegmentedFilterTabs(
            tabs: tabs,
            selectedIndex: 0,
            onTabSelected: (_) {},
          ),
        ),
      );

      expect(find.text('All'), findsOneWidget);
      expect(find.text('5'), findsOneWidget);
      expect(find.text('Working'), findsOneWidget);
      expect(find.text('2'), findsOneWidget);
      expect(find.text('Blocked'), findsOneWidget);
      expect(find.text('1'), findsOneWidget);
      expect(find.text('Idle'), findsOneWidget);
    });

    testWidgets('triggers onTabSelected with correct index on tap', (tester) async {
      int? selectedIndex;
      final tabs = [
        const FilterTabItem(label: 'All'),
        const FilterTabItem(label: 'Attention', count: 3),
        const FilterTabItem(label: 'History'),
      ];

      await tester.pumpWidget(
        _wrapWithTheme(
          SegmentedFilterTabs(
            tabs: tabs,
            selectedIndex: 0,
            onTabSelected: (index) {
              selectedIndex = index;
            },
          ),
        ),
      );

      await tester.tap(find.text('Attention'));
      await tester.pump();

      expect(selectedIndex, equals(1));
    });
  });

  group('TerminalAccessoryBar Widget Tests', () {
    testWidgets('renders default touch keys', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          TerminalAccessoryBar(
            onKeyPressed: (_) {},
          ),
        ),
      );

      expect(find.text('⌨️'), findsOneWidget);
      expect(find.text('Esc'), findsOneWidget);
      expect(find.text('Tab'), findsOneWidget);
      expect(find.text('⌫'), findsOneWidget);
      expect(find.text('Ctrl+C'), findsOneWidget);
      expect(find.text('Enter'), findsOneWidget);
      expect(find.text('↑'), findsOneWidget);
    });

    testWidgets('renders custom keys when supplied', (tester) async {
      await tester.pumpWidget(
        _wrapWithTheme(
          TerminalAccessoryBar(
            customKeys: const ['clear', 'git status', 'ls -la'],
            onKeyPressed: (_) {},
          ),
        ),
      );

      expect(find.text('clear'), findsOneWidget);
      expect(find.text('git status'), findsOneWidget);
      expect(find.text('ls -la'), findsOneWidget);
      expect(find.text('Esc'), findsNothing);
    });

    testWidgets('tapping touch key triggers onKeyPressed callback', (tester) async {
      String? pressedKey;

      await tester.pumpWidget(
        _wrapWithTheme(
          TerminalAccessoryBar(
            enableHaptics: false,
            onKeyPressed: (key) {
              pressedKey = key;
            },
          ),
        ),
      );

      await tester.tap(find.text('Ctrl+C'));
      await tester.pump();

      expect(pressedKey, equals('Ctrl+C'));

      await tester.tap(find.text('⌫'));
      await tester.pump();

      expect(pressedKey, equals('⌫'));
    });
  });
}
