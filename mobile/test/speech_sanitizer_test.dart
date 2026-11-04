import 'package:flutter_test/flutter_test.dart';
import 'package:ackbar_mobile/core/utils/speech_sanitizer.dart';

void main() {
  group('isSimpleSpeechCandidate Tests', () {
    test(
        'returns true for empty or whitespace string (trivially simple candidate)',
        () {
      expect(isSimpleSpeechCandidate(''), isTrue);
      expect(isSimpleSpeechCandidate('   \n  '), isTrue);
    });

    test('returns true for simple concise prose under 120 words', () {
      const text =
          'The build completed successfully in 42 seconds. All unit tests passed.';
      expect(isSimpleSpeechCandidate(text), isTrue);
    });

    test('returns false when word count exceeds 120 words', () {
      final longText = List.filled(130, 'word').join(' ');
      expect(isSimpleSpeechCandidate(longText), isFalse);
    });

    test('returns false when text contains 3 or more ## or ### headings', () {
      const markdown = '''
## Heading 1
Intro text.
## Heading 2
Middle text.
### Heading 3
Conclusion text.
''';
      expect(isSimpleSpeechCandidate(markdown), isFalse);
    });

    test('returns false when text contains more than 3 table rows', () {
      const table = '''
| ID | Status | Message |
| --- | --- | --- |
| 1 | OK | Done |
| 2 | OK | Done |
| 3 | FAIL | Error |
| 4 | PENDING | Waiting |
''';
      expect(isSimpleSpeechCandidate(table), isFalse);
    });
  });

  group('sanitizeMarkdownForSpeech Tests', () {
    test('strips code blocks', () {
      const input = '''
Here is the code:
```dart
void main() {
  print("hello world");
}
```
Done with code.
''';
      final sanitized = sanitizeMarkdownForSpeech(input);
      expect(sanitized.contains('print("hello world")'), isFalse);
      expect(sanitized.contains('Done with code'), isTrue);
    });

    test('strips box drawing characters and lines', () {
      const input = '''
System Status:
┌─────────────────┐
│ All OK          │
└─────────────────┘
All services online.
''';
      final sanitized = sanitizeMarkdownForSpeech(input);
      expect(sanitized.contains('┌'), isFalse);
      expect(sanitized.contains('─'), isFalse);
      expect(sanitized.contains('│'), isFalse);
      expect(sanitized.contains('System Status:'), isTrue);
      expect(sanitized.contains('All services online.'), isTrue);
    });

    test('converts small tables (<= 3 rows) to readable text', () {
      const table = '''
| Test | Result |
| --- | --- |
| Unit | Pass |
''';
      final sanitized = sanitizeMarkdownForSpeech(table);
      expect(sanitized.contains('|'), isFalse);
      expect(sanitized.contains('Unit'), isTrue);
      expect(sanitized.contains('Pass'), isTrue);
    });

    test('shortens file paths and 40-character git commit hashes', () {
      const input =
          'Committed at /home/dev4u/Personal/ackbar/mobile/lib/main.dart with hash 0123456789abcdef0123456789abcdef01234567.';
      final sanitized = sanitizeMarkdownForSpeech(input);
      expect(sanitized.contains('/home/dev4u/Personal/ackbar/mobile/lib/'),
          isFalse);
      expect(sanitized.contains('main.dart'), isTrue);
      expect(sanitized.contains('0123456789abcdef0123456789abcdef01234567'),
          isFalse);
      expect(sanitized.contains('commit'), isTrue);
    });

    test('strips bold, italic, strikethrough, and simplifies links', () {
      const input =
          'This is **bold**, *italic*, and a [my document](file:///docs/readme.txt).';
      final sanitized = sanitizeMarkdownForSpeech(input);
      expect(sanitized.contains('**'), isFalse);
      expect(sanitized.contains('*'), isFalse);
      expect(sanitized.contains('file:///docs/readme.txt'), isFalse);
      expect(sanitized.contains('readme.txt'), isTrue);
      expect(sanitized.contains('bold'), isTrue);
      expect(sanitized.contains('italic'), isTrue);
    });
  });
}
