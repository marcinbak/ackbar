#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

BASE_REF="${ACKBAR_BASE_REF:-origin/main}"
SKIP_VERSION_CHECK=false

while [ $# -gt 0 ]; do
    case "$1" in
        --base)
            if [ -z "${2:-}" ]; then
                echo "ERROR: --base requires a git ref argument" >&2
                exit 1
            fi
            BASE_REF="$2"
            shift 2
            ;;
        --skip-version-check)
            SKIP_VERSION_CHECK=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [--base <ref>] [--skip-version-check]"
            exit 0
            ;;
        *)
            echo "ERROR: Unknown option: $1" >&2
            echo "Usage: $0 [--base <ref>] [--skip-version-check]" >&2
            exit 1
            ;;
    esac
done

echo "=== T0: Verification Suite (base: $BASE_REF) ==="

# a. Version bump check
if [ "$SKIP_VERSION_CHECK" = false ]; then
    echo "--> Checking version bump..."
    ./scripts/bump-version.sh --check --base "$BASE_REF"
else
    echo "--> Version check skipped via --skip-version-check."
fi

# b. Code formatting, vet, and AGENTS.md budget
echo "--> Checking Go formatting..."
UNFORMATTED=$(find . -type f -name '*.go' -not -path './.worktrees/*' -not -path './vendor/*' -exec gofmt -l {} +)
if [ -n "$UNFORMATTED" ]; then
    echo "ERROR: Unformatted Go files detected:" >&2
    echo "$UNFORMATTED" >&2
    echo "Run 'gofmt -w <file>' to format." >&2
    exit 1
fi

echo "--> Running go vet..."
go vet ./...

echo "--> Checking AGENTS.md line budget (<= 160)..."
if [ ! -f "AGENTS.md" ]; then
    echo "ERROR: AGENTS.md not found" >&2
    exit 1
fi
AGENTS_LINES=$(wc -l < AGENTS.md | tr -d '[:space:]')
if [ "$AGENTS_LINES" -gt 160 ]; then
    echo "ERROR: AGENTS.md line count ($AGENTS_LINES) exceeds 160 lines budget" >&2
    exit 1
fi

# c. Isolated Go race tests
echo "--> Running Go race test suite..."
TMP_HOME="$(mktemp -d)"
cleanup_tmp() {
    chmod -R u+w "$TMP_HOME" 2>/dev/null || true
    rm -rf "$TMP_HOME" 2>/dev/null || true
}
trap cleanup_tmp EXIT
REAL_GOPATH="${GOPATH:-$(go env GOPATH)}"
HOME="$TMP_HOME" GOPATH="$REAL_GOPATH" env -u ACKBAR_HOST go test -race ./...

# d. Compile gates
echo "--> Verifying native binary compilation..."
CGO_ENABLED=0 go build -o /dev/null ./cmd/ackbard ./cmd/ackbar ./cmd/ackbar-hook ./cmd/ackbar-relay

echo "--> Verifying darwin/arm64 cross-compilation..."
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /dev/null ./...

# e. Web JS unit tests
echo "--> Running Web JS unit tests..."
node --test web/js/*.test.mjs

# f. Conditional mobile gates
echo "--> Checking conditional mobile/ verification..."
MERGE_BASE="$(git merge-base "$BASE_REF" HEAD 2>/dev/null || git rev-parse "$BASE_REF" 2>/dev/null || git rev-parse HEAD)"

MOBILE_DIFF="$(git diff -w "$MERGE_BASE" -- mobile/ 2>/dev/null || true)"
MOBILE_UNTRACKED="$(git ls-files --others --exclude-standard -- mobile/ 2>/dev/null || true)"

if [ -n "$MOBILE_DIFF" ] || [ -n "$MOBILE_UNTRACKED" ]; then
    echo "Mobile changes detected vs $MERGE_BASE; running mobile gates..."
    if [ ! -f "mobile/.dart_tool/package_config.json" ]; then
        (cd mobile && flutter pub get)
    fi

    CHANGED_DARTS=()
    while IFS= read -r f; do
        [ -n "$f" ] && [ -f "$f" ] && CHANGED_DARTS+=("$f")
    done < <(git diff -w --name-only --diff-filter=ACMR "$MERGE_BASE" -- 'mobile/*.dart' 'mobile/**/*.dart' 2>/dev/null || true)

    while IFS= read -r f; do
        [ -n "$f" ] && [ -f "$f" ] && [[ "$f" == *.dart ]] && CHANGED_DARTS+=("$f")
    done < <(git ls-files --others --exclude-standard -- mobile/ 2>/dev/null || true)

    if [ ${#CHANGED_DARTS[@]} -gt 0 ]; then
        dart format --output=none --set-exit-if-changed "${CHANGED_DARTS[@]}"
    fi
    (cd mobile && flutter analyze --no-pub --no-fatal-infos)
else
    echo "Skipped mobile/ verification (no non-whitespace mobile/ changes vs $BASE_REF)."
fi

echo "=== All T0 verification gates passed successfully ==="
