/// Speech text sanitizer and candidate classifier ported from Ackbar Web (chat.js).
library;

/// Determines if a markdown response is short and simple enough for direct Tier 1 spoken playback
/// (< 120 words, <= 3 table rows, < 3 headings, and no large code blocks).
bool isSimpleSpeechCandidate(String? md) {
  if (md == null || md.trim().isEmpty) return true;

  // 1. Strip fenced code blocks
  final withoutCode = md.replaceAll(RegExp(r'```[\s\S]*?```'), ' ').trim();
  if (withoutCode.isEmpty) return true;

  // 2. Count words
  final words =
      withoutCode.split(RegExp(r'\s+')).where((s) => s.isNotEmpty).toList();
  if (words.length >= 120) {
    return false;
  }

  // 3. Count table rows (lines containing | that are not separator lines)
  final lines = withoutCode.split('\n');
  int tableRows = 0;
  final separatorRegex = RegExp(r'^\|[-:\s|]+\|$');
  for (final line in lines) {
    final trimmed = line.trim();
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      if (separatorRegex.hasMatch(trimmed)) {
        continue;
      }
      tableRows++;
    }
  }
  if (tableRows > 3) {
    return false;
  }

  // 4. Count markdown ## / ### section headings
  int headings = 0;
  final headingRegex = RegExp(r'^#{2,3}\s+');
  for (final line in lines) {
    if (headingRegex.hasMatch(line.trim())) {
      headings++;
    }
  }
  if (headings >= 3) {
    return false;
  }

  return true;
}

/// Cleans markdown text to produce natural, fluid text suitable for TTS speech engines.
String sanitizeMarkdownForSpeech(String? md) {
  if (md == null || md.isEmpty) return '';
  var text = md;

  // 1. Strip fenced code blocks & diffs
  text = text.replaceAll(RegExp(r'```[\s\S]*?```'), ' ');

  // 2. Strip Mermaid blocks and ASCII box-drawing diagrams, handle small tables
  final lines = text.split('\n');
  final filteredLines = <String>[];
  final boxCharsRegex = RegExp(r'[┌┐└┘─│├┤┬┴┼═║╔╗╚╝╠╣╦╩╬]');
  final separatorRegex = RegExp(r'^\|[-:\s|]+\|$');

  bool inTable = false;
  List<String> tableHeaderCols = [];

  for (int i = 0; i < lines.length; i++) {
    final line = lines[i];
    final trimmed = line.trim();

    // Skip ASCII box drawing diagrams
    if (boxCharsRegex.hasMatch(trimmed)) {
      continue;
    }

    // Small table conversion (<= 3 data rows)
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      if (separatorRegex.hasMatch(trimmed)) {
        continue;
      }
      final cells = trimmed
          .split('|')
          .sublist(1, trimmed.split('|').length - 1)
          .map((c) => c.trim())
          .toList();
      if (!inTable) {
        inTable = true;
        tableHeaderCols = cells;
      } else {
        final rowSentences = <String>[];
        for (int c = 0; c < cells.length; c++) {
          final colName = c < tableHeaderCols.length
              ? tableHeaderCols[c]
              : 'Column ${c + 1}';
          final val = cells[c];
          if (val.isNotEmpty) {
            rowSentences.add('$colName: $val');
          }
        }
        if (rowSentences.isNotEmpty) {
          filteredLines.add('${rowSentences.join(', ')}.');
        }
      }
      continue;
    } else {
      inTable = false;
    }

    filteredLines.add(line);
  }
  text = filteredLines.join('\n');

  // 3. Shorten Markdown links with file:// or file paths to their basename
  text = text.replaceAllMapped(
    RegExp(r'\[([^\]]+)\]\((?:file:\/\/)?([^)]+)\)'),
    (match) {
      final label = match.group(1) ?? '';
      final urlPath = match.group(2) ?? '';
      if (urlPath.contains('/') || urlPath.startsWith('file:')) {
        final cleanPath = urlPath
            .replaceFirst(RegExp(r'^file:\/\/'), '')
            .split('#')
            .first
            .split('?')
            .first;
        final base = cleanPath.split('/').where((s) => s.isNotEmpty).lastOrNull;
        if (base != null && base.isNotEmpty) {
          return base;
        }
      }
      return label;
    },
  );

  // Shorten raw absolute file paths
  text = text.replaceAllMapped(
    RegExp(
        r'(?:^|\s)(?:\/[a-zA-Z0-9_\.\-]+)+\/([a-zA-Z0-9_\.\-]+(?:\.[a-zA-Z0-9]+)?)'),
    (match) => ' ${match.group(1)}',
  );

  // 4. Strip or shorten UUIDs and 40-char Git SHA hashes
  text = text.replaceAll(
      RegExp(r'\b[0-9a-f]{40}\b', caseSensitive: false), 'commit');
  text = text.replaceAll(
    RegExp(r'\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b',
        caseSensitive: false),
    '',
  );

  // 5. Strip residual Markdown formatting
  text = text.replaceAll(
      RegExp(r'>\s*\[!(?:NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]',
          caseSensitive: false),
      '');
  text = text.replaceAll(RegExp(r'^\s*>\s?', multiLine: true), '');
  text = text.replaceAll(RegExp(r'^\s*#{1,6}\s+', multiLine: true), '');
  text = text.replaceAllMapped(
    RegExp(r'[*_~]{1,3}([^*_~]+)[*_~]{1,3}'),
    (match) => match.group(1) ?? '',
  );
  text = text.replaceAllMapped(
    RegExp(r'`([^`]+)`'),
    (match) => match.group(1) ?? '',
  );
  text = text.replaceAll(RegExp(r'^\s*[-*+]\s+', multiLine: true), '');
  text = text.replaceAll(RegExp(r'^\s*\d+[\.\)]\s+', multiLine: true), '');
  text = text.replaceAll(RegExp(r'<[^>]+>'), ' ');

  // 6. Normalize whitespace
  text = text.replaceAll('\r\n', '\n');
  text = text.replaceAll(RegExp(r'\n+'), ' ');
  text = text.replaceAll(RegExp(r'\s+'), ' ').trim();

  return text;
}
