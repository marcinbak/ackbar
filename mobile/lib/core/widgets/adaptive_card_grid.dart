import 'package:flutter/material.dart';

import '../layout/responsive_breakpoints.dart';
import '../theme/app_spacing.dart';

class AdaptiveCardListOrGrid extends StatelessWidget {
  final List<Widget> children;
  final double spacing;
  final double runSpacing;
  final double maxWidth;

  const AdaptiveCardListOrGrid({
    super.key,
    required this.children,
    this.spacing = AppSpacing.md,
    this.runSpacing = AppSpacing.md,
    this.maxWidth = 1120,
  });

  @override
  Widget build(BuildContext context) {
    if (!ResponsiveBreakpoints.isTwoColumnGrid(context)) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (var i = 0; i < children.length; i++)
            Padding(
              padding: EdgeInsets.only(
                  bottom: i == children.length - 1 ? 0 : runSpacing),
              child: children[i],
            ),
        ],
      );
    }

    return Center(
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxWidth),
        child: LayoutBuilder(
          builder: (context, constraints) {
            final colW = ((constraints.maxWidth - spacing) / 2).floorToDouble();
            return Wrap(
              spacing: spacing,
              runSpacing: runSpacing,
              children: [
                for (final child in children)
                  SizedBox(width: colW, child: child),
              ],
            );
          },
        ),
      ),
    );
  }
}

class AdaptiveSliverCardGrid extends StatelessWidget {
  final int itemCount;
  final IndexedWidgetBuilder itemBuilder;
  final double spacing;
  final double runSpacing;
  final double maxWidth;

  const AdaptiveSliverCardGrid({
    super.key,
    required this.itemCount,
    required this.itemBuilder,
    this.spacing = AppSpacing.md,
    this.runSpacing = AppSpacing.md,
    this.maxWidth = 1120,
  });

  @override
  Widget build(BuildContext context) {
    if (!ResponsiveBreakpoints.isTwoColumnGrid(context)) {
      return SliverList(
        delegate: SliverChildBuilderDelegate(
          (ctx, idx) => Padding(
            padding: EdgeInsets.only(bottom: runSpacing),
            child: itemBuilder(ctx, idx),
          ),
          childCount: itemCount,
        ),
      );
    }

    return SliverToBoxAdapter(
      child: Center(
        child: ConstrainedBox(
          constraints: BoxConstraints(maxWidth: maxWidth),
          child: LayoutBuilder(
            builder: (ctx, constraints) {
              final colW =
                  ((constraints.maxWidth - spacing) / 2).floorToDouble();
              return Wrap(
                spacing: spacing,
                runSpacing: runSpacing,
                children: [
                  for (var i = 0; i < itemCount; i++)
                    SizedBox(width: colW, child: itemBuilder(ctx, i)),
                ],
              );
            },
          ),
        ),
      ),
    );
  }
}
